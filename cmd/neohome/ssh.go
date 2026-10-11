package main

import (
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// Neohome's SSH entry point. The spec (四十五) calls for SSH as the primary
// door into the world; telnet stays as the legacy/IoT path. Remote SSH *inside*
// the world (ssh root@10.77.1.1) is handled by the virtual `ssh` builtin — this
// listener is only how a real player gets their first shell.
//
// No real host code is executed here: auth is checked against the world's own
// Player records and the session is handed to the same virtual Shell the telnet
// entry uses.

var ssHostOnce sync.Once

// hostSigner builds the server's signing identity from the key kept with the
// world, so the server is the SAME machine after a restart — which is the whole
// point of a host key.
func hostSigner(w *core.World) ssh.Signer {
	der, err := hostKey(w)
	if err != nil {
		log.Fatalf("ssh host key: %v", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		log.Fatalf("ssh host key parse: %v", err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		log.Fatalf("ssh host key signer: %v", err)
	}
	return s
}

// sshEntry listens for real SSH clients. It returns on fatal listen errors
// only; each accepted connection is served on its own goroutine.
func sshEntry(w *core.World, addr string) error {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			w.Lock()
			p := w.Players[c.User()]
			w.Unlock()
			if p == nil || p.MCPOnly || p.Pass == "" || p.Pass != string(pass) {
				return nil, fmt.Errorf("permission denied")
			}
			return &ssh.Permissions{Extensions: map[string]string{"player": c.User()}}, nil
		},
		// keyboard-interactive is what most clients actually use
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := ch("", "Password: ", []string{"password: "}, []bool{false})
			if err != nil || len(answers) == 0 {
				return nil, fmt.Errorf("permission denied")
			}
			w.Lock()
			p := w.Players[c.User()]
			w.Unlock()
			if p == nil || p.MCPOnly || p.Pass == "" || p.Pass != answers[0] {
				return nil, fmt.Errorf("permission denied")
			}
			return &ssh.Permissions{Extensions: map[string]string{"player": c.User()}}, nil
		},
	}
	cfg.AddHostKey(hostSigner(w))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("neohome ssh entry listening on %s", addr)
	// accept in the background: the entries are independent, and a blocking
	// accept here would stop the ones started after this one from ever
	// binding (which is exactly what happened to the browser front)
	go acceptLoop(ln, func(c net.Conn) { serveSSH(c, w, cfg) })
	return nil
}

func serveSSH(nConn net.Conn, w *core.World, cfg *ssh.ServerConfig) {
	defer nConn.Close()
	sconn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	who := sconn.Permissions.Extensions["player"]
	ip := nConn.RemoteAddr().String()

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			// wait for the shell request
			for r := range chReqs {
				if r.Type != "shell" && r.Type != "pty-req" {
					if r.WantReply {
						r.Reply(false, nil)
					}
					continue
				}
				if r.WantReply {
					r.Reply(true, nil)
				}
				if r.Type == "pty-req" {
					continue
				}
				runPlayerSession(w, who, ip, ch, ch)
				ch.Close()
				return
			}
		}()
	}
}

// runPlayerSession starts the virtual shell for an authenticated player, over
// any transport (ssh channel, telnet conn, web terminal). Where the session
// lands — the player's own machine, or the out-of-band controller when the
// house is dark — is decided in one place in the world (core.LandPlayer), so
// the three doors cannot land the same player in different places.
func runPlayerSession(w *core.World, who, ip string, out io.Writer, in io.Reader) {
	w.Lock()
	pc, u, note, err := w.LandPlayer(who)
	w.Unlock()
	if err != nil {
		fmt.Fprintf(out, "%v\r\n", err)
		return
	}
	if note != "" {
		fmt.Fprintf(out, "%s\r\n", note)
	}
	fmt.Fprintf(out, "Welcome, %s. NeoHome over ssh.\r\n", who)
	s := shell.NewShell(w, pc, u, out, ip, "xterm-256color")
	s.RunLoop(in)
}
