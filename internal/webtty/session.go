package webtty

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// ---------------------------------------------------------------------------
// The session bridge: one browser tab ↔ one shell session on one machine.
//
// The transport is a text protocol — terminal output goes one way as it is
// written, keystrokes come back the other way as they are typed. There is no
// local line editing and no local echo in the client, because the world's own
// shell does both: it echoes every character it receives, handles backspace,
// ^C, ^D and ^L, and only runs a line when one arrives. That makes the browser
// behave like a real terminal on a real tty, rather than a chat box that
// guesses at what a shell would have done.
//
// The envelope is JSON per message, with two fields that matter:
//
//	{"t":"in","d":"ls\r"}            a browser keystroke (bytes, verbatim)
//	{"t":"out","d":"home-pc:~$ …"}   what the shell wrote
//
// plus resize/secret/exit for the things a terminal has besides bytes. Text
// frames only: the world's shell speaks text, and a binary framing would only
// hide that.
// ---------------------------------------------------------------------------

// wireMsg is the JSON envelope both directions use.
type wireMsg struct {
	T string `json:"t"`
	// D carries text: keystrokes in, output out.
	D string `json:"d,omitempty"`
	// On is the payload of a "secret" message: the client should stop showing
	// what it types (a password prompt), or resume when it goes false. It is
	// sent even when false — "no" is an instruction, not an absent field.
	On bool `json:"on"`
	// Cols/Rows are a terminal size, in cells.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
	// Code is a process exit status on "exit".
	Code int `json:"code,omitempty"`
}

// SessionOptions decide how a browser session behaves on the wire.
type SessionOptions struct {
	// From is the peer address, recorded in the login record like any other.
	From string
	// Idle is how long the connection may go without a frame from the browser
	// before the session is declared gone. The browser answers pings on its
	// own, so a tab that is merely being read stays alive.
	Idle time.Duration
	// Ping is how often the server pings. Zero picks a third of Idle.
	Ping time.Duration
	// Cols/Rows are the terminal size the client reported at connect time.
	Cols, Rows int
}

func (o SessionOptions) idle() time.Duration {
	if o.Idle > 0 {
		return o.Idle
	}
	return 90 * time.Second
}

func (o SessionOptions) ping() time.Duration {
	if o.Ping > 0 {
		return o.Ping
	}
	return o.idle() / 3
}

// Session is one live browser session.
type Session struct {
	w    *core.World
	dev  *core.Device
	user *core.User
	conn *Conn
	opts SessionOptions

	mu   sync.Mutex
	sh   *shell.Shell
	cols int
	rows int
}

// RunSession lands the connection on d as u and runs the world's shell over it
// until the browser goes away, the shell exits, or the machine loses power.
// The login record and the machine's own log lines are written by
// core.BeginSession, so a browser session is visible exactly like an ssh one.
func RunSession(w *core.World, d *core.Device, u *core.User, c *Conn, opts SessionOptions) error {
	if w == nil || d == nil || u == nil || c == nil {
		return fmt.Errorf("webtty: incomplete session")
	}
	s := &Session{w: w, dev: d, user: u, conn: c, opts: opts, cols: opts.Cols, rows: opts.Rows}
	end := w.BeginSession(d, u.Name, "webterm", opts.From)
	defer end()

	c.SetIdleTimeout(opts.idle())
	out := &wsOutput{s: s}

	fmt.Fprintf(out, "\r\nWelcome, %s. NeoHome web terminal — %s as %s.\r\n", u.Name, d.Hostname, u.Name)
	if ip, ok, _ := d.DNSAnswer("mirror.neohome.example"); ok {
		fmt.Fprintf(out, "Your DNS is %s — try `dig mirror.neohome.example`.\r\n", ip)
	}
	fmt.Fprintf(out, "Type `help` for the commands. Closing the tab ends the session.\r\n\r\n")

	sh := shell.NewShell(w, d, u, out, opts.From, "xterm-256color")
	s.applySize(sh)
	s.mu.Lock()
	s.sh = sh
	s.mu.Unlock()

	// keepalive: a browser answers pings by itself, so this is what separates
	// "the user is reading" from "the tab is gone"
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(opts.ping())
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := c.Ping("neohome"); err != nil {
					return
				}
			}
		}
	}()
	defer close(stop)

	in := &wsInput{s: s}
	sh.RunLoop(in)

	_ = c.WriteJSON(wireMsg{T: "exit"})
	_ = c.Close(1000, "session ended")
	return nil
}

// applySize gives the shell the terminal size the browser reported, so
// $COLUMNS/$LINES and anything that reads them tell the truth about the client
// rather than a guess. It is the one piece of PTY state this world keeps.
func (s *Session) applySize(sh *shell.Shell) {
	if sh == nil {
		return
	}
	s.mu.Lock()
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	sh.SetTTYSize(cols, rows)
}

// resize records a new terminal size and passes it on to the shell.
func (s *Session) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	sh := s.sh
	s.mu.Unlock()
	s.applySize(sh)
}

// ---------------------------------------------------------------------------
// input
// ---------------------------------------------------------------------------

// wsInput is the shell's input stream: a reader that blocks on the socket and
// hands back whatever the browser typed, byte for byte.
type wsInput struct {
	s   *Session
	buf []byte
}

func (in *wsInput) Read(p []byte) (int, error) {
	for len(in.buf) == 0 {
		op, data, err := in.s.conn.ReadMessage()
		if err != nil {
			if err == ErrIdle {
				// the tab is gone: say so, then end the session like ^D
				_ = in.s.conn.Close(1001, "idle")
				return 0, io.EOF
			}
			if err == ErrClosed {
				return 0, io.EOF
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return 0, io.EOF
			}
			return 0, err
		}
		if op != opText {
			continue
		}
		var m wireMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue // a frame this protocol does not know: ignore, never die
		}
		switch m.T {
		case "in":
			in.buf = append(in.buf, []byte(m.D)...)
		case "resize":
			in.s.resize(m.Cols, m.Rows)
		case "exit":
			return 0, io.EOF
		}
	}
	n := copy(p, in.buf)
	in.buf = in.buf[n:]
	return n, nil
}

// ---------------------------------------------------------------------------
// output
// ---------------------------------------------------------------------------

// wsOutput is what the shell writes to. Every write becomes one frame, and the
// writer watches the text for the one thing a browser cannot work out on its
// own: that the next line it types is a password, and should not be shown.
type wsOutput struct {
	s      *Session
	tail   string // text since the last newline, for prompt detection
	secret bool
}

func (o *wsOutput) Write(p []byte) (int, error) {
	text := string(p)
	if err := o.s.conn.WriteJSON(wireMsg{T: "out", D: text}); err != nil {
		return 0, err
	}
	o.trackSecret(text)
	return len(p), nil
}

// trackSecret turns the prompt itself into the echo setting a real tty would
// have: when the shell writes something ending in "password: " (or "Passcode:",
// which the lock tool uses) the client hides what is typed until the line ends.
// The bytes on the wire are untouched — this only decides what the screen shows.
func (o *wsOutput) trackSecret(text string) {
	if i := strings.LastIndexAny(text, "\r\n"); i >= 0 {
		o.tail = text[i+1:]
	} else {
		o.tail += text
		if len(o.tail) > 200 {
			o.tail = o.tail[len(o.tail)-200:]
		}
	}
	want := promptNeedsSecret(o.tail)
	if want == o.secret {
		return
	}
	o.secret = want
	_ = o.s.conn.WriteJSON(wireMsg{T: "secret", On: want})
}

// promptNeedsSecret reports whether a prompt line ends in a secret-looking
// question. It is deliberately narrow: a shell that says "password" is asking
// for one, and anything else is just text that happens to be on screen.
func promptNeedsSecret(tail string) bool {
	t := strings.ToLower(strings.TrimRight(tail, " "))
	if t == "" {
		return false
	}
	// only the end of the line counts, and only a short question: a log line
	// that happens to mention a password is not a prompt
	if len(t) > 120 {
		t = t[len(t)-120:]
	}
	asks := strings.HasSuffix(t, ":") ||
		strings.HasSuffix(t, "password") || strings.HasSuffix(t, "passphrase") ||
		strings.HasSuffix(t, "passcode")
	if !asks {
		return false
	}
	for _, word := range []string{"password", "passphrase", "passcode", "passwd"} {
		if strings.Contains(t, word) {
			return true
		}
	}
	return false
}
