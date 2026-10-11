// Package webtty is the world's browser front end: a real HTTP + WebSocket
// entry that binds a browser tab to a real shell session on a machine in the
// world. It is the same door as the ssh and telnet entries — the player
// authenticates as a player record, lands as an account on their own device,
// and every line they type runs through the same builtins and writes the same
// evidence (Active logins, syslog, auth failures, history).
//
// The WebSocket is implemented here rather than pulled in as a dependency:
// RFC 6455 is a handshake, a framer and a small state machine, and this world
// ships with exactly one module dependency on purpose.
package webtty

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// opcodes, RFC 6455 §5.2
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

const (
	// Subprotocol is the wire protocol the terminal front speaks. The client
	// asks for it and the server echoes it, so a future version can coexist.
	Subprotocol = "neohome.term.v1"
	// maxFramePayload bounds one frame. Terminal input and output are small;
	// this is a limit on a hostile peer, not on the game.
	maxFramePayload = 1 << 20
	acceptGUID      = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// Conn is one upgraded WebSocket connection.
type Conn struct {
	rw  net.Conn
	br  *bufio.Reader
	wmu sync.Mutex

	// closed guards against writing after a close frame was sent.
	closed bool

	// idle bounds the gap between frames from the peer. A browser tab that has
	// been closed (or a laptop that went to sleep) stops answering pings, and
	// the session it held must end rather than leak.
	idle time.Duration
}

// ErrIdle is returned when the peer stopped answering for longer than the
// connection's idle timeout.
var ErrIdle = errors.New("websocket: peer idle")

// SetIdleTimeout makes every read wait at most d for the next frame; a peer
// that answers pings keeps the connection open, one that does not is gone.
func (c *Conn) SetIdleTimeout(d time.Duration) { c.idle = d }

// Upgrade completes the WebSocket handshake and returns the connection. It
// writes its own 101 response through the hijacked socket, because a normal
// http.ResponseWriter cannot honour the upgrade.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if err := CheckUpgrade(w, r); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, httpErrorf(http.StatusInternalServerError, "this server cannot upgrade")
	}
	rw, brw, err := hj.Hijack()
	if err != nil {
		return nil, httpErrorf(http.StatusInternalServerError, "hijack: %v", err)
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n")
	if headerHasToken(r.Header, "Sec-WebSocket-Protocol", Subprotocol) {
		b.WriteString("Sec-WebSocket-Protocol: " + Subprotocol + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := rw.Write([]byte(b.String())); err != nil {
		rw.Close()
		return nil, err
	}
	// the client may have pipelined frames behind the handshake: keep the
	// hijacked reader, not just the socket
	return &Conn{rw: rw, br: brw.Reader}, nil
}

// CheckUpgrade validates the handshake without performing it. A door with
// checks of its own to make first — an origin, a ticket — asks this before it
// spends them, so that the protocol's answer never depends on authorisation:
// a client speaking the wrong version is told so whether or not it holds a
// ticket, and no ticket is consumed by a request that was never going to work.
func CheckUpgrade(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return httpErrorf(http.StatusMethodNotAllowed, "websocket upgrade requires GET")
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") {
		return httpErrorf(http.StatusBadRequest, "not a websocket upgrade: Connection header")
	}
	if !headerHasToken(r.Header, "Upgrade", "websocket") {
		return httpErrorf(http.StatusBadRequest, "not a websocket upgrade: Upgrade header")
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		return httpErrorf(http.StatusUpgradeRequired, "unsupported websocket version")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key")) == "" {
		return httpErrorf(http.StatusBadRequest, "missing Sec-WebSocket-Key")
	}
	return nil
}

func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func httpErrorf(code int, format string, a ...any) error {
	return &httpError{code: code, msg: fmt.Sprintf(format, a...)}
}

// HTTPStatus reports the status an Upgrade failure should be answered with.
func HTTPStatus(err error) int {
	var he *httpError
	if errors.As(err, &he) {
		return he.code
	}
	return http.StatusBadRequest
}

// ErrClosed is returned by reads after the peer sent a close frame.
var ErrClosed = errors.New("websocket: closed by peer")

// SetReadDeadline bounds the next read. The session uses it for liveness: a
// browser tab that stops answering pings is gone, whatever it claims.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.rw.SetReadDeadline(t) }

// RemoteAddr is the peer address, for the login record.
func (c *Conn) RemoteAddr() net.Addr { return c.rw.RemoteAddr() }

// ReadMessage returns the next complete data message. Control frames are
// handled inside: a ping is answered with a pong, a pong is swallowed, and a
// close is answered and reported as ErrClosed. Fragmented messages are
// reassembled, so callers only ever see whole messages.
func (c *Conn) ReadMessage() (opcode byte, payload []byte, err error) {
	var acc []byte
	fragmented := false
	for {
		if c.idle > 0 {
			if err := c.rw.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
				return 0, nil, err
			}
		}
		h := make([]byte, 2)
		if _, err := io.ReadFull(c.br, h); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return 0, nil, ErrIdle
			}
			return 0, nil, err
		}
		fin := h[0]&0x80 != 0
		if h[0]&0x70 != 0 {
			c.fail(1002, "reserved bits set")
			return 0, nil, errors.New("websocket: reserved bits set")
		}
		op := h[0] & 0x0f
		masked := h[1]&0x80 != 0
		length := int64(h[1] & 0x7f)
		switch length {
		case 126:
			ext := make([]byte, 2)
			if _, err := io.ReadFull(c.br, ext); err != nil {
				return 0, nil, err
			}
			length = int64(ext[0])<<8 | int64(ext[1])
		case 127:
			ext := make([]byte, 8)
			if _, err := io.ReadFull(c.br, ext); err != nil {
				return 0, nil, err
			}
			length = 0
			for _, b := range ext {
				length = length<<8 | int64(b)
			}
			if length < 0 {
				c.fail(1002, "bad length")
				return 0, nil, errors.New("websocket: bad length")
			}
		}
		if length > maxFramePayload {
			c.fail(1009, "message too big")
			return 0, nil, errors.New("websocket: message too big")
		}
		if op >= opClose {
			// control frame: never fragmented, never longer than 125 bytes
			if !fin || length > 125 {
				c.fail(1002, "bad control frame")
				return 0, nil, errors.New("websocket: bad control frame")
			}
			frame, err := c.readPayload(length, masked)
			if err != nil {
				return 0, nil, err
			}
			switch op {
			case opPing:
				if err := c.writeFrame(opPong, frame); err != nil {
					return 0, nil, err
				}
				continue
			case opPong:
				continue
			case opClose:
				code := uint16(1000)
				if len(frame) >= 2 {
					code = uint16(frame[0])<<8 | uint16(frame[1])
				}
				c.writeClose(code, "")
				return opClose, nil, ErrClosed
			default:
				c.fail(1002, "unknown control opcode")
				return 0, nil, errors.New("websocket: unknown control opcode")
			}
		}
		// the client MUST mask (RFC 6455 §5.1); an unmasked frame is a broken
		// or hostile peer, and saying so is cheaper than guessing
		if !masked {
			c.fail(1002, "client frames must be masked")
			return 0, nil, errors.New("websocket: unmasked client frame")
		}
		frame, err := c.readPayload(length, true)
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opContinuation:
			if !fragmented {
				c.fail(1002, "continuation without a start")
				return 0, nil, errors.New("websocket: unexpected continuation")
			}
			acc = append(acc, frame...)
			if fin {
				return opText, acc, nil
			}
		case opText, opBinary:
			if fragmented {
				c.fail(1002, "new data frame inside a fragmented message")
				return 0, nil, errors.New("websocket: interleaved data frame")
			}
			if fin {
				return op, frame, nil
			}
			fragmented = true
			acc = append(acc[:0], frame...)
		default:
			c.fail(1002, "unknown opcode")
			return 0, nil, errors.New("websocket: unknown opcode")
		}
	}
}

func (c *Conn) readPayload(length int64, masked bool) ([]byte, error) {
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return nil, err
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	if masked {
		for i := range buf {
			buf[i] ^= mask[i%4]
		}
	}
	return buf, nil
}

// WriteMessage sends one unfragmented data message. Server frames are never
// masked (RFC 6455 §5.1).
func (c *Conn) WriteMessage(opcode byte, payload []byte) error {
	return c.writeFrame(opcode, payload)
}

// WriteJSON marshals v into a text message.
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(opText, data)
}

// Ping sends a ping the peer must answer with a pong.
func (c *Conn) Ping(payload string) error { return c.writeFrame(opPing, []byte(payload)) }

// Close sends a close frame. It is safe to call more than once: the first
// frame wins, later calls are no-ops, so a session ending for two reasons
// (the shell exited, the socket dropped) cannot write garbage.
func (c *Conn) Close(code uint16, reason string) error {
	return c.writeClose(code, reason)
}

// CloseSocket closes the underlying connection.
func (c *Conn) CloseSocket() error { return c.rw.Close() }

func (c *Conn) writeClose(code uint16, reason string) error {
	buf := []byte{byte(code >> 8), byte(code)}
	buf = append(buf, reason...)
	if len(buf) > 125 {
		buf = buf[:125]
	}
	return c.writeFrame(opClose, buf)
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if opcode == opClose {
		c.closed = true
	}
	hdr := make([]byte, 0, 10)
	hdr = append(hdr, 0x80|opcode)
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xffff:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	// one write per frame: a header and its payload split across writes is a
	// frame a peer can misread, and the mutex keeps two writers from
	// interleaving inside one message
	if _, err := c.rw.Write(append(hdr, payload...)); err != nil {
		return err
	}
	return nil
}

// fail tells the peer why the connection is being torn down, best effort.
func (c *Conn) fail(code uint16, reason string) {
	if err := c.writeClose(code, reason); err != nil {
		log.Printf("webtty: close frame: %v", err)
	}
	_ = c.rw.Close()
}

// IsUpgradeRequest reports whether r looks like a WebSocket handshake, so a
// handler can answer a plain HTTP visitor with the landing page instead.
func IsUpgradeRequest(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade") &&
		headerHasToken(r.Header, "Upgrade", "websocket")
}
