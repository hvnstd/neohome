package main

// sshdrive is a real SSH client used to verify the SSH entry point end-to-end.
// It authenticates with a password against the world's own player records and
// runs a scripted list of commands, printing exactly what came back.
//
// Like a real interactive client it keeps stdin OPEN (a pipe that is closed only
// after the commands have been sent and drained) — closing it immediately makes
// the server see EOF before it has read the buffered commands.
//
// usage: go run ./tools/sshdrive <user> <pass> <cmd> [cmd...]

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	args := os.Args[1:]
	if len(args) < 3 {
		fmt.Println("usage: sshdrive <user> <pass> <cmd> [cmd...]")
		os.Exit(2)
	}
	user, pass := args[0], args[1]
	cmds := args[2:]

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", "127.0.0.1:2222", cfg)
	if err != nil {
		fmt.Println("ssh dial/auth FAILED:", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("ssh: authenticated as", user)

	sess, err := client.NewSession()
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	defer sess.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	sess.Stdout = &tee{w: &out, mu: &mu}
	sess.Stderr = &tee{w: &out, mu: &mu}

	// stdin stays open until we are done talking.
	pr, pw := io.Pipe()
	sess.Stdin = pr

	_ = sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{})
	if err := sess.Shell(); err != nil {
		fmt.Println("shell:", err)
		os.Exit(1)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	// one command at a time, so each one's output is unambiguous
	for _, c := range cmds {
		fmt.Fprintf(pw, "%s\r\n", c)
		time.Sleep(350 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	fmt.Fprintln(pw, "exit")
	pw.Close()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		fmt.Println("(timeout waiting for session)")
	}

	mu.Lock()
	defer mu.Unlock()
	fmt.Print(out.String())
}

type tee struct {
	w  io.Writer
	mu *sync.Mutex
}

func (t *tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// strip the telnet/pty CR so transcripts stay readable
	s := strings.ReplaceAll(string(p), "\r\n", "\n")
	_, err := t.w.Write([]byte(s))
	return len(p), err
}
