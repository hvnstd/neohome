package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// telnet used to hand out a root shell with no authentication whatsoever, which
// made every privilege check pointless — an attacker could simply telnet in.
// A telnet endpoint must demand an account and a password.
func TestTelnetRequiresLogin(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	router := w.Devices["router-alex"]
	if router == nil {
		t.Skip("no router in this world")
	}

	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	// wrong password: must be refused, and the nested shell must never open
	sh.SetInput(strings.NewReader("root\r\nwrong-password\r\n"))
	// by address: the world starts with the planted DNS fault, which is correct
	sh.ExecLine("telnet 10.77.1.1")
	if !strings.Contains(out.String(), "Login incorrect") {
		t.Fatalf("a bad telnet password should be refused:\n%s", out.String())
	}
	// it must not have reached a shell prompt as root
	if strings.Contains(out.String(), "root@gateway:~#") {
		t.Fatalf("telnet opened a root shell without authenticating:\n%s", out.String())
	}
}

// With the right credentials it does open a session — the login is real, not a
// blanket refusal.
func TestTelnetAcceptsCorrectCredentials(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	root := w.Devices["router-alex"].FindUser("root")
	if root == nil {
		t.Skip("router has no root account")
	}
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("root\r\n" + root.Pass + "\r\nuname -a\r\nexit\r\n"))
	sh.ExecLine("telnet 10.77.1.1")
	if !strings.Contains(out.String(), "Welcome to gateway") {
		t.Fatalf("correct credentials should open a session:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "root@gateway:~#") {
		t.Fatalf("the session should be a root shell on the router:\n%s", out.String())
	}
}
