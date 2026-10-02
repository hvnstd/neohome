package main

// sshdrive is a real SSH client used to verify the SSH entry point end-to-end.
// It authenticates with a password against the world's own player records and
// runs a scripted list of commands, printing exactly what came back.
//
// usage: go run ./tools/sshdrive <user> <pass> <cmd> [cmd...]

import (
	"bytes"
	"fmt"
	"os"
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

	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &out
	sess.Stdin = bytes.NewReader([]byte(script(cmds)))

	// request a pty like a real client, then a shell
	_ = sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{})
	if err := sess.Shell(); err != nil {
		fmt.Println("shell:", err)
		os.Exit(1)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		fmt.Println("(timeout)")
	}
	fmt.Print(out.String())
}

func script(cmds []string) string {
	var b bytes.Buffer
	for _, c := range cmds {
		b.WriteString(c)
		b.WriteString("\r\n")
	}
	b.WriteString("exit\r\n")
	return b.String()
}
