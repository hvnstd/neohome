package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
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

func newHostKey() ssh.Signer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("ssh host key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(priv)
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
			if p == nil || p.Pass != string(pass) {
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
			if p == nil || p.Pass != answers[0] {
				return nil, fmt.Errorf("permission denied")
			}
			return &ssh.Permissions{Extensions: map[string]string{"player": c.User()}}, nil
		},
	}
	cfg.AddHostKey(newHostKey())

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("neohome ssh entry listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("ssh accept:", err)
			continue
		}
		go serveSSH(conn, w, cfg)
	}
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
// any transport (ssh channel or telnet conn).
func runPlayerSession(w *core.World, who, ip string, out io.Writer, in io.Reader) {
	w.Lock()
	p := w.Players[who]
	var pc *core.Device
	if p != nil {
		pc = w.Devices[p.PC]
	}
	w.Unlock()
	if pc == nil {
		fmt.Fprintf(out, "no device for %s\r\n", who)
		return
	}
	fmt.Fprintf(out, "Welcome, %s. NeoHome over ssh.\r\n", who)
	_ = bufio.NewReader(in)
	u := pc.FindUser("alex")
	if u == nil {
		u = &core.User{Name: "alex", UID: 1000, Home: "/home/alex", Shell: "/bin/bash"}
	}
	s := shell.NewShell(w, pc, u, out, ip, "xterm-256color")
	s.RunLoop(in)
}
