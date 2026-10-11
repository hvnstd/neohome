package tests

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
	"neohome/internal/shell"
	"neohome/internal/webtty"
)

// ---------------------------------------------------------------------------
// §45's browser front: a real WebSocket terminal.
//
// These tests drive the same wire a browser drives, over a real TCP socket:
// the handshake, masked frames, keystrokes forwarded one at a time, the
// machine's own echo coming back, a password prompt that hides what is typed,
// the login record `who` reads, and the session ending when the tab does. The
// front end's renderer is checked separately, under node, from the exact
// JavaScript the page ships.
// ---------------------------------------------------------------------------

// ---- a small, deliberate WebSocket client: the peer the server must satisfy

type wsClient struct {
	conn net.Conn
	br   *bufio.Reader
	t    *testing.T
}

// wsDial performs the handshake by hand, so the test proves the handshake
// rather than trusting a library to do it.
func wsDial(t *testing.T, base, path string, header map[string]string) (*wsClient, *http.Response) {
	return wsDialFrom(t, base, path, header, "")
}

// wsDialFrom dials from a chosen local address, which is how a test can be a
// different client than the one that logged in.
func wsDialFrom(t *testing.T, base, path string, header map[string]string, local string) (*wsClient, *http.Response) {
	t.Helper()
	host := strings.TrimPrefix(base, "http://")
	d := net.Dialer{}
	if local != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(local)}
	}
	conn, err := d.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial %s from %s: %v", host, local, err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Protocol: " + webtty.Subprotocol + "\r\n"
	for k, v := range header {
		req += k + ": " + v + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, resp
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(h[:])
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != want {
		t.Fatalf("Sec-WebSocket-Accept = %q, want %q", got, want)
	}
	return &wsClient{conn: conn, br: br, t: t}, resp
}

// wsOpen is the whole browser dance: log in over HTTP, then upgrade with the
// ticket, exactly as the page does.
func wsOpen(t *testing.T, srv *httptest.Server, user, pass string) *wsClient {
	t.Helper()
	status, tk := webLogin(t, srv, user, pass)
	if status != http.StatusOK {
		t.Fatalf("login failed with status %d", status)
	}
	c, resp := wsDial(t, srv.URL, "/ws?t="+tk, nil)
	if c == nil {
		t.Fatalf("handshake with a ticket failed: %d", resp.StatusCode)
	}
	return c
}

func (c *wsClient) close() { c.conn.Close() }

// sendMasked writes one masked client frame — what every browser sends.
func (c *wsClient) sendMasked(opcode byte, payload []byte) {
	c.t.Helper()
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	hdr := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xffff:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	body := make([]byte, n)
	for i := range payload {
		body[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(append(hdr, mask[:]...), body...)); err != nil {
		c.t.Fatalf("write frame: %v", err)
	}
}

func (c *wsClient) sendJSON(v any) {
	c.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	c.sendMasked(1, data)
}

// typeText sends a line the way a person types it: one keystroke per frame.
func (c *wsClient) typeText(s string) {
	for _, ch := range s {
		c.sendJSON(map[string]any{"t": "in", "d": string(ch)})
	}
}

// sendRaw writes an unmasked frame: the protocol violation a real browser
// cannot produce, and the one the server must refuse.
func (c *wsClient) sendRaw(opcode byte, payload []byte) {
	c.t.Helper()
	hdr := append([]byte{0x80 | opcode}, byte(len(payload)))
	if _, err := c.conn.Write(append(hdr, payload...)); err != nil {
		c.t.Fatalf("write raw frame: %v", err)
	}
}

type wsFrame struct {
	opcode  byte
	payload []byte
	closed  bool
	code    int
}

func (c *wsClient) readFrame(timeout time.Duration) (wsFrame, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return wsFrame{}, err
	}
	op := head[0] & 0x0f
	if head[1]&0x80 != 0 {
		return wsFrame{}, fmt.Errorf("server frame is masked")
	}
	n := int64(head[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return wsFrame{}, err
		}
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return wsFrame{}, err
		}
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return wsFrame{}, err
	}
	f := wsFrame{opcode: op, payload: buf}
	if op == 0x8 {
		f.closed = true
		if len(buf) >= 2 {
			f.code = int(binary.BigEndian.Uint16(buf[:2]))
		}
	}
	return f, nil
}

// session is what a test accumulates while watching the wire.
type session struct {
	out     strings.Builder
	secrets []bool
	exited  bool
	closed  bool
	closeNo int
}

// drain reads until want is satisfied, the session ends, or time runs out.
// Pings are answered the way a browser answers them.
func (c *wsClient) drain(s *session, deadline time.Duration, want func(*session) bool) {
	c.t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		f, err := c.readFrame(time.Until(stop) + time.Second)
		if err != nil {
			return
		}
		switch f.opcode {
		case 0x9:
			c.sendMasked(0xA, f.payload)
		case 0x8:
			s.closed, s.closeNo = true, f.code
			return
		case 0x1:
			var m struct {
				T  string `json:"t"`
				D  string `json:"d"`
				On bool   `json:"on"`
			}
			if err := json.Unmarshal(f.payload, &m); err != nil {
				continue
			}
			switch m.T {
			case "out":
				s.out.WriteString(m.D)
			case "secret":
				s.secrets = append(s.secrets, m.On)
			case "exit":
				s.exited = true
			}
		}
		if want != nil && want(s) {
			return
		}
	}
}

func hasText(s *session, want string) func(*session) bool {
	return func(x *session) bool { return strings.Contains(x.out.String(), want) }
}

// ---- the fixture -----------------------------------------------------------

// webConsole serves the front on a loopback port for a test world.
func webConsole(t *testing.T, w *core.World) *httptest.Server {
	srv, _ := webConsoleClock(t, w)
	return srv
}

// webConsoleClock also hands back the console, whose clock a test can move:
// ticket expiry is a sixty-second rule that should not cost sixty seconds.
func webConsoleClock(t *testing.T, w *core.World) (*httptest.Server, *webtty.Console) {
	t.Helper()
	c := webtty.NewConsole(w)
	c.Idle = 20 * time.Second
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return srv, c
}

// webLogin posts the login form's payload and returns the status and the
// ticket (or the error text).
func webLogin(t *testing.T, srv *httptest.Server, user, pass string) (int, string) {
	t.Helper()
	body := fmt.Sprintf(`{"user":%q,"pass":%q}`, user, pass)
	resp, err := http.Post(srv.URL+"/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Ticket string `json:"ticket"`
		Error  string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, out.Ticket
	}
	return resp.StatusCode, out.Error
}

func syslogOf(d *core.Device) string {
	data, _ := d.FS.Read("/var/log/syslog")
	return string(data)
}

// ---- the tests -------------------------------------------------------------

func TestFrontServesItsOwnPage(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	page := string(raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("content type: %q", resp.Header.Get("Content-Type"))
	}
	// self-contained: a terminal that needs the internet to render is a
	// terminal that stops working in exactly the world this game is about
	for _, bad := range []string{"cdn.", "unpkg", "cdnjs", "googleapis", "jsdelivr", "src=\"//", "href=\"//"} {
		if strings.Contains(page, bad) {
			t.Fatalf("the page must not load external assets, found %q", bad)
		}
	}
	for _, want := range []string{"<form id=\"loginform\"", "new WebSocket(", "/login", "/ws?t=", "NeohomeTerm"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page should contain %q", want)
		}
	}
	// a framing block would break every embed, and the live preview is one
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Fatal("the front must not send X-Frame-Options")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors") {
		t.Fatalf("the front must not block framing: %q", csp)
	}
}

func TestFrontLoginIsARealLogin(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)
	pc := w.Devices[w.Players["alex"].PC]

	// a wrong password is refused, and it lands in the world's own record: the
	// same counter an ssh attempt feeds, so fail2ban and the IDS see it
	if status, _ := webLogin(t, srv, "alex", "not-the-password"); status != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", status)
	}
	if n := pc.FailsFrom("127.0.0.1", time.Hour); n == 0 {
		t.Fatal("a wrong web password must be recorded on the account's machine")
	}
	if !strings.Contains(syslogOf(pc), "web password for alex") {
		t.Fatalf("the machine's own log should carry the failure:\n%s", syslogOf(pc))
	}
	// an account that does not exist is refused the same way
	if status, _ := webLogin(t, srv, "nobody", "x"); status != http.StatusUnauthorized {
		t.Fatalf("unknown account: status %d, want 401", status)
	}
	// and the right one mints a ticket bound to this account
	status, tk := webLogin(t, srv, "alex", "alex123")
	if status != http.StatusOK || tk == "" {
		t.Fatalf("login: status %d ticket %q", status, tk)
	}
}

func TestTicketIsSingleUse(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)
	_, tk := webLogin(t, srv, "alex", "alex123")

	first, resp := wsDial(t, srv.URL, "/ws?t="+tk, nil)
	if first == nil {
		t.Fatalf("the first use of a ticket should work: %d", resp.StatusCode)
	}
	defer first.close()
	second, resp2 := wsDial(t, srv.URL, "/ws?t="+tk, nil)
	if second != nil {
		second.close()
		t.Fatal("a ticket must not open a second session")
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused ticket = %d, want 401", resp2.StatusCode)
	}
	// an invented ticket is refused too, of course
	if c, _ := wsDial(t, srv.URL, "/ws?t=deadbeef", nil); c != nil {
		c.close()
		t.Fatal("an invented ticket must not open a session")
	}
}

func TestFrontRefusesBadHandshakes(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)

	// a plain GET on /ws is answered as documentation, not as a crash
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatalf("GET /ws: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plain GET /ws = %d", resp.StatusCode)
	}

	// an upgrade with no ticket never becomes a connection
	req, _ := http.NewRequest("GET", srv.URL+"/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAAAA==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upgrade without a ticket: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upgrade without a ticket = %d, want 401", resp2.StatusCode)
	}

	// an old protocol version is answered with the one this server speaks
	req3, _ := http.NewRequest("GET", srv.URL+"/ws", nil)
	req3.Header.Set("Connection", "Upgrade")
	req3.Header.Set("Upgrade", "websocket")
	req3.Header.Set("Sec-WebSocket-Key", "AAAAAAAAAAAAAAAAAAAAAA==")
	req3.Header.Set("Sec-WebSocket-Version", "8")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("old version: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("old version = %d, want 426", resp3.StatusCode)
	}
	if resp3.Header.Get("Sec-WebSocket-Version") != "13" {
		t.Fatal("a 426 must name the version this server speaks")
	}

	// a page from somewhere else may not mint tickets against this console
	req4, _ := http.NewRequest("POST", srv.URL+"/login", strings.NewReader(`{"user":"alex","pass":"alex123"}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("Origin", "https://evil.example")
	resp4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatalf("cross-origin login: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d, want 403", resp4.StatusCode)
	}

	// and the console's own origin is accepted (that is what a proxy sends)
	req5, _ := http.NewRequest("POST", srv.URL+"/login", strings.NewReader(`{"user":"alex","pass":"alex123"}`))
	req5.Header.Set("Content-Type", "application/json")
	req5.Header.Set("Origin", srv.URL)
	resp5, err := http.DefaultClient.Do(req5)
	if err != nil {
		t.Fatalf("same-origin login: %v", err)
	}
	resp5.Body.Close()
	if resp5.StatusCode != http.StatusOK {
		t.Fatalf("same-origin login = %d, want 200", resp5.StatusCode)
	}
}

func TestFrontRunsARealShellSession(t *testing.T) {
	w := secSetup(t)
	srv := webConsole(t, w)
	pc := w.Devices[w.Players["alex"].PC]

	c := wsOpen(t, srv, "alex", "alex123")
	defer c.close()
	s := &session{}
	c.drain(s, 5*time.Second, hasText(s, "NeoHome web terminal"))
	if !strings.Contains(s.out.String(), "NeoHome web terminal") {
		t.Fatalf("no banner:\n%q", s.out.String())
	}

	// one keystroke per frame: the echo is the machine's, not the client's —
	// the front never edits a line locally, so what comes back is the world
	c.typeText("whoami")
	c.drain(s, 5*time.Second, hasText(s, "whoami"))
	if !strings.Contains(s.out.String(), "whoami") {
		t.Fatalf("the shell should have echoed the line as it was typed:\n%q", s.out.String())
	}
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, hasText(s, "alex\n"))
	if !strings.Contains(s.out.String(), "alex") {
		t.Fatalf("whoami should answer alex:\n%q", s.out.String())
	}

	// the login is world state while the session is open: `who` reads it, the
	// machine's log carries it, and the world's evidence records it
	if got := w.SessionsAt(pc); !hasLogin(got, "alex", "webterm") {
		t.Fatalf("the session should be in the machine's login table: %+v", got)
	}
	if !strings.Contains(syslogOf(pc), "session opened for alex on webterm") {
		t.Fatalf("the machine's log should carry the login:\n%s", syslogOf(pc))
	}
	// and the shell's own `who` inside the session shows it
	c.typeText("who")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, hasText(s, "webterm"))
	if !strings.Contains(s.out.String(), "webterm") {
		t.Fatalf("`who` inside the session should list the webterm login:\n%q", s.out.String())
	}

	// the terminal size the browser reports is the session's, and `stty size`
	// reads it back the way it does on any Unix
	c.typeText("stty size")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, hasText(s, "24 80"))
	if !strings.Contains(s.out.String(), "24 80") {
		t.Fatalf("a session with no size reported should be the classic 80x24:\n%q", s.out.String())
	}
	c.sendJSON(map[string]any{"t": "resize", "cols": 100, "rows": 30})
	c.typeText("stty size")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, hasText(s, "30 100"))
	if !strings.Contains(s.out.String(), "30 100") {
		t.Fatalf("the browser's window size should reach the shell:\n%q", s.out.String())
	}

	// closing the tab ends the session and clears its record
	c.close()
	waitFor(t, 5*time.Second, func() bool { return !hasLogin(w.SessionsAt(pc), "alex", "webterm") })
	if hasLogin(w.SessionsAt(pc), "alex", "webterm") {
		t.Fatalf("closing the tab should end the login: %+v", w.SessionsAt(pc))
	}
	if !strings.Contains(syslogOf(pc), "session closed for alex on webterm") {
		t.Fatalf("the machine's log should carry the logout:\n%s", syslogOf(pc))
	}
}

func hasLogin(list []core.Login, user, tty string) bool {
	for _, l := range list {
		if l.User == user && l.TTY == tty {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	stop := time.Now().Add(d)
	for time.Now().Before(stop) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPasswordPromptHidesWhatIsTyped(t *testing.T) {
	w := secSetup(t)
	srv := webConsole(t, w)
	c := wsOpen(t, srv, "alex", "alex123")
	defer c.close()
	s := &session{}
	c.drain(s, 5*time.Second, hasText(s, "web terminal"))

	// sudo asks for the invoking user's password. The prompt is what tells the
	// front to stop showing what is typed — the same signal a real tty gives,
	// taken from the machine's own output rather than from anything the client
	// decided.
	c.typeText("sudo id")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, func(x *session) bool {
		return strings.Contains(x.out.String(), "password for alex")
	})
	if !strings.Contains(s.out.String(), "password for alex") {
		t.Fatalf("no password prompt:\n%q", s.out.String())
	}
	c.drain(s, 3*time.Second, func(x *session) bool { return anySecret(x, true) })
	if !anySecret(s, true) {
		t.Fatalf("the password prompt should switch the front to hidden input:\n%q\nsecrets=%v",
			s.out.String(), s.secrets)
	}

	// the typed password must still reach the shell unhidden — masking that
	// eats the bytes is the classic way this breaks — and must not come back
	c.typeText("alex123")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, hasText(s, "uid=0"))
	if !strings.Contains(s.out.String(), "uid=0(root)") {
		t.Fatalf("the password should have authenticated sudo:\n%q", s.out.String())
	}
	if strings.Contains(s.out.String(), "alex123") {
		t.Fatalf("what is typed at a password prompt must never come back on the wire:\n%q", s.out.String())
	}
	c.drain(s, 3*time.Second, func(x *session) bool { return anySecret(x, false) })
	if !anySecret(s, false) {
		t.Fatalf("the front should resume echoing after the prompt:\n%v", s.secrets)
	}
}

func anySecret(s *session, want bool) bool {
	for _, v := range s.secrets {
		if v == want {
			return true
		}
	}
	return false
}

func TestUnmaskedFramesAreRefused(t *testing.T) {
	w := secSetup(t)
	srv := webConsole(t, w)
	c := wsOpen(t, srv, "alex", "alex123")
	defer c.close()
	s := &session{}
	c.drain(s, 5*time.Second, hasText(s, "web terminal"))

	// a client frame without a mask is a protocol violation (RFC 6455 §5.1)
	c.sendRaw(1, []byte(`{"t":"in","d":"whoami\r"}`))
	c.drain(s, 5*time.Second, func(x *session) bool { return x.closed })
	if !s.closed {
		t.Fatal("an unmasked client frame must close the connection")
	}
	if s.closeNo != 1002 {
		t.Fatalf("the close code should be 1002 (protocol error), got %d", s.closeNo)
	}
}

func TestSessionDiesWithTheMachine(t *testing.T) {
	w := secSetup(t)
	srv := webConsole(t, w)
	pc := w.Devices[w.Players["alex"].PC]

	c := wsOpen(t, srv, "alex", "alex123")
	defer c.close()
	s := &session{}
	c.drain(s, 5*time.Second, hasText(s, "web terminal"))

	// pulling the plug is not a message: the shell's next prompt notices, says
	// why in the machine's own words, and the session ends
	pc.PlugPull()
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 6*time.Second, func(x *session) bool { return x.exited || x.closed })
	if !strings.Contains(s.out.String(), "Connection to host lost") {
		t.Fatalf("a dark machine must say so:\n%q", s.out.String())
	}
	if !s.exited && !s.closed {
		t.Fatal("the session should end when its machine goes dark")
	}
	if hasLogin(w.SessionsAt(pc), "alex", "webterm") {
		t.Fatal("a session that ended must not stay in the login table")
	}
}

func TestConsoleThrottlesAStubbornClient(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)
	status := 0
	for i := 0; i < 5; i++ {
		status, _ = webLogin(t, srv, "alex", "wrong-again")
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("the fifth failure = %d, want 401", status)
	}
	status, msg := webLogin(t, srv, "alex", "alex123")
	if status != http.StatusTooManyRequests {
		t.Fatalf("after five failures the address should wait, got %d (%s)", status, msg)
	}
}

// ---------------------------------------------------------------------------
// the front end's own half, run under node from the JavaScript the page ships
// ---------------------------------------------------------------------------

// runNode runs the page's terminal engine under node and returns its stdout.
// It skips when node is not installed: the world does not depend on it, and a
// missing node is not a failing test.
func runNode(t *testing.T, script string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the renderer test needs it")
	}
	f, err := writeTemp(t, "term.js", webtty.TermJS())
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	f2, err := writeTemp(t, "harness.js", script)
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	out, err := exec.Command(node, f2, f).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return string(out)
}

func writeTemp(t *testing.T, name, content string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func TestTerminalEngineRendersLikeATerminal(t *testing.T) {
	// the harness needs the engine class, not the module: read it and eval it
	js := `
const fs = require('node:fs');
const src = fs.readFileSync(process.argv[2], 'utf8');
const NeohomeTerm = new Function(src + '; return NeohomeTerm;')();
function term() { return new NeohomeTerm(80, 24); }
function eq(got, want, what) {
  if (got !== want) throw new Error(what + ": " + JSON.stringify(got) + " != " + JSON.stringify(want));
}

// a prompt, a typed line the machine echoed, and its answer
const t = term();
t.write("home-pc:~$ ls\r\nfile-a  file-b\r\nhome-pc:~$ ");
eq(t.snapshot()[0], "home-pc:~$ ls", "row0");
eq(t.snapshot()[1], "file-a  file-b", "row1");
eq(t.snapshot()[2], "home-pc:~$ ", "row2");
eq(t.col, 11, "cursor column");

// a bare CR rewrites the line in place: that is how a progress line works
const tp = term();
tp.write("50%");
tp.write("\r100%");
eq(tp.snapshot()[0], "100%", "CR overwrite");
// and a shorter write keeps the tail of the line, as a real terminal does
const tq = term();
tq.write("abcdef\rxy");
eq(tq.snapshot()[0], "xycdef", "CR partial");
// CR after a full line does not eat the finished one
const tr = term();
tr.write("first\r\nsecond");
eq(tr.snapshot().join("|"), "first|second", "CR then newline");

// backspace, as the shell's own line discipline sends it
const tb = term();
tb.write("whoamii");
tb.write("\b");
eq(tb.snapshot()[0], "whoami", "backspace");

// ^L: the shell clears the screen with ESC[H ESC[2J
const tc = term();
tc.write("noise\r\n\x1b[H\x1b[2Jprompt> ");
eq(tc.snapshot().join("|"), "prompt> ", "clear");

// SGR: bold and colour survive as runs, and reset ends them
const ts = term();
ts.write("\x1b[1;31mALERT\x1b[0m ok");
const runs = ts.snapshot(0, true)[0];
const styled = runs.filter(function (r) { return r.t; }).map(function (r) { return r.t + "{" + r.s + "}"; }).join(",");
if (styled.indexOf("ALERT{b c31}") < 0) throw new Error("sgr: " + styled);
if (styled.indexOf("ok{}") < 0) throw new Error("plain run: " + styled);

// a tab advances to the next stop; a bell is silent
const tt = term();
tt.write("a\tb\x07c");
eq(tt.snapshot()[0], "a       bc", "tab");

// hidden input: while secret, the screen shows bullets, never the password
const tk = term();
tk.write("Password: ");
tk.setSecret(true);
"hunter".split("").forEach(function (c) { tk.secretInput(c); });
const masked = tk.snapshot()[0];
if (masked.indexOf("hunter") >= 0) throw new Error("secret leaked: " + masked);
eq(masked, "Password: \u2022\u2022\u2022\u2022\u2022\u2022", "mask");
// a mistake at a prompt takes one bullet away, not the whole prompt
tk.secretInput("ab");
tk.secretInput("\u007f");
eq(tk.snapshot()[0], "Password: \u2022\u2022\u2022\u2022\u2022\u2022\u2022", "mask backspace");
tk.setSecret(false);
// once it is over, typing is echoed by the shell and bullets stop
tk.secretInput("x");
eq(tk.snapshot()[0], "Password: \u2022\u2022\u2022\u2022\u2022\u2022\u2022", "mask off");

// an escape sequence split across two frames must survive the boundary
const te = term();
te.write("\x1b[1");
te.write(";32mGREEN");
const er = te.snapshot(0, true)[0];
const es = er.map(function (r) { return r.t + "{" + r.s + "}"; }).join(",");
if (es.indexOf("GREEN{b c32}") < 0) throw new Error("split escape: " + es);

// the screen is bounded: a long session does not grow without limit
const tl = new NeohomeTerm(80, 24);
for (var i = 0; i < 3000; i++) tl.write("line " + i + "\r\n");
if (tl.done.length > 2001) throw new Error("unbounded screen: " + tl.done.length);
console.log("engine ok");
`
	out := runNode(t, js)
	if !strings.Contains(out, "engine ok") {
		t.Fatalf("engine output: %s", out)
	}
}

func TestPageJavaScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the syntax check needs it")
	}
	path, err := writeTemp(t, "page.js", webtty.PageJS())
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("the page's JavaScript does not parse: %v\n%s", err, out)
	}
}

func TestTicketExpiresAndIsBoundToItsClient(t *testing.T) {
	w := core.NewWorld()
	srv, console := webConsoleClock(t, w)
	now := time.Now()
	console.Now = func() time.Time { return now }

	// a ticket is only good for the address that asked for it, and presenting
	// it is what spends it: a leaked ticket is not a session for whoever finds
	// it, and a thief cannot hand it back and let the owner use it
	_, tk := webLogin(t, srv, "alex", "alex123")
	if c, resp := wsDialFrom(t, srv.URL, "/ws?t="+tk, nil, "127.0.0.2"); c != nil {
		c.close()
		t.Fatal("a ticket from another address must not open a session")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other address: %d, want 401", resp.StatusCode)
	}
	if c, resp := wsDial(t, srv.URL, "/ws?t="+tk, nil); c != nil {
		c.close()
		t.Fatal("a ticket that was presented is spent, whoever presented it")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("spent ticket: %d, want 401", resp.StatusCode)
	}
	// the address that did log in gets its own session, of course
	_, tk0 := webLogin(t, srv, "alex", "alex123")
	if c, resp := wsDial(t, srv.URL, "/ws?t="+tk0, nil); c == nil {
		t.Fatalf("the address that logged in should be able to use its ticket: %d", resp.StatusCode)
	} else {
		c.close()
	}

	// and it is good for a minute, not for a day
	_, tk2 := webLogin(t, srv, "alex", "alex123")
	now = now.Add(59 * time.Second)
	if c, resp := wsDial(t, srv.URL, "/ws?t="+tk2, nil); c == nil {
		t.Fatalf("a fresh ticket should survive a minute: %d", resp.StatusCode)
	} else {
		c.close()
	}
	_, tk3 := webLogin(t, srv, "alex", "alex123")
	now = now.Add(61 * time.Second)
	if c, resp := wsDial(t, srv.URL, "/ws?t="+tk3, nil); c != nil {
		c.close()
		t.Fatal("an expired ticket must not open a session")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired ticket: %d, want 401", resp.StatusCode)
	}
}

func TestBrowserDoorLandsOnTheOutOfBandController(t *testing.T) {
	w := secSetup(t)
	srv := webConsole(t, w)

	// a dead house is not a locked door: the browser front is a transport, and
	// the world's own rule decides where a session lands
	w.CutPower("test")
	c := wsOpen(t, srv, "alex", "alex123")
	defer c.close()
	s := &session{}
	c.drain(s, 5*time.Second, hasText(s, "out-of-band"))
	if !strings.Contains(s.out.String(), "out-of-band") {
		t.Fatalf("with the house dark the front should land on the controller:\n%q", s.out.String())
	}
	if !strings.Contains(s.out.String(), "home-pc is down") {
		t.Fatalf("the session should say why it is not on the player's machine:\n%q", s.out.String())
	}

	// and what it landed on is the controller, so the session is a real one
	c.typeText("hostname")
	c.sendJSON(map[string]any{"t": "in", "d": "\r"})
	c.drain(s, 5*time.Second, func(x *session) bool { return strings.Contains(x.out.String(), "\n") })
	bmc := w.OutOfBand()
	if bmc == nil {
		t.Fatal("this world should have an out-of-band controller")
	}
	if got := w.SessionsAt(bmc); !hasLogin(got, "admin", "webterm") {
		t.Fatalf("the session should be recorded on the controller: %+v", got)
	}
	if got := w.SessionsAt(w.Devices["pc-alex"]); len(got) != 0 {
		t.Fatalf("a dark machine has no sessions: %+v", got)
	}
}

// The terminal size a session is given is world state, so it has a shell
// command that reads it: a program that wants to know how wide the window is
// asks the same way it does anywhere else.
func TestSttyReportsTheSessionsTerminal(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices[w.Players["alex"].PC]

	// a session with no door-reported size is the classic 80x24
	if out := run(t, w, pc, "alex", "stty size"); strings.TrimSpace(out) != "24 80" {
		t.Fatalf("stty size = %q, want \"24 80\"", out)
	}
	if out := run(t, w, pc, "alex", "tty"); strings.TrimSpace(out) != "/dev/pts/0" {
		t.Fatalf("tty = %q", out)
	}
	if out := run(t, w, pc, "alex", "stty -a"); !strings.Contains(out, "rows 24; columns 80;") {
		t.Fatalf("stty -a should report the size it is set to:\n%s", out)
	}
	// a person can set it, and then it stays set for the session that asked
	out := &bufOut{}
	sh := shell.NewShell(w, pc, pc.FindUser("alex"), out, "10.77.1.11", "xterm")
	sh.SetTTYSize(100, 30)
	sh.ExecLine("stty size")
	if got := strings.TrimSpace(out.String()); got != "30 100" {
		t.Fatalf("stty size after a resize = %q, want \"30 100\"", got)
	}
	// nonsense is refused rather than silently accepted
	if status := remoteStatus(t, w, pc, "alex", "stty rows banana"); status == 0 {
		t.Fatal("stty rows banana should fail")
	}
}

// The front is usually reached through something in front of it — a reverse
// proxy, a preview tunnel — and those rewrite Host. The origin check must keep
// working there without opening up to a page on another site.
func TestOriginCheckSurvivesAProxy(t *testing.T) {
	w := core.NewWorld()
	srv := webConsole(t, w)
	post := func(hdr map[string]string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/login", strings.NewReader(`{"user":"alex","pass":"alex123"}`))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// a tunnel that rewrites Host but forwards the original one
	if got := post(map[string]string{
		"Origin":           "https://8080-abc123.e2b.app",
		"X-Forwarded-Host": "8080-abc123.e2b.app",
	}); got != http.StatusOK {
		t.Fatalf("a proxied same-origin login = %d, want 200", got)
	}
	// RFC 7239 spelling
	if got := post(map[string]string{
		"Origin":    "https://term.example.net",
		"Forwarded": "for=10.0.0.5;host=term.example.net;proto=https",
	}); got != http.StatusOK {
		t.Fatalf("an RFC 7239 proxied login = %d, want 200", got)
	}
	// and a page on another site is still refused
	if got := post(map[string]string{
		"Origin":           "https://evil.example",
		"X-Forwarded-Host": "8080-abc123.e2b.app",
	}); got != http.StatusForbidden {
		t.Fatalf("a cross-site login = %d, want 403", got)
	}
}
