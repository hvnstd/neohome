package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// A shell's history belongs in a file on the device, like a real shell. If it
// only lives in the session, then logging out erases the trail — and the whole
// forensic half of the game has nothing to read.
func TestShellHistoryIsAFileOnTheDevice(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	if u == nil {
		t.Fatal("no alex on the pc")
	}
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	// a real interactive loop, so history recording is exercised
	sh.SetInput(strings.NewReader("echo first\r\necho second\r\nexit\r\n"))
	sh.RunLoop(nil)

	data, ok := pc.FS.Read("/home/alex/.bash_history")
	if !ok {
		t.Fatalf("no history file was written; alex's home holds:\n%s", listRoot(pc, "/home/alex"))
	}
	body := string(data)
	if !strings.Contains(body, "echo first") || !strings.Contains(body, "echo second") {
		t.Fatalf("the history file is missing the typed commands:\n%s", body)
	}
	// `exit` is a real command and belongs in history like any other
	if !strings.Contains(body, "exit") {
		t.Fatalf("exit should be recorded too:\n%s", body)
	}
}

// The history file must survive the session: reopening a shell and reading the
// history shows what was done before, which is what makes it evidence.
func TestShellHistorySurvivesTheSession(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")

	run1 := &bufOut{}
	sh1 := shell.NewShell(w, pc, u, run1, "10.77.1.11", "xterm")
	sh1.SetInput(strings.NewReader("touch /tmp/marker-from-session-one\r\nexit\r\n"))
	sh1.RunLoop(nil)

	// a brand new shell = a new login, and the history is still there
	run2 := &bufOut{}
	sh2 := shell.NewShell(w, pc, u, run2, "10.77.1.11", "xterm")
	sh2.SetInput(strings.NewReader("history\r\nexit\r\n"))
	sh2.RunLoop(nil)

	if !strings.Contains(run2.String(), "touch /tmp/marker-from-session-one") {
		t.Fatalf("`history` did not show the previous session's command:\n%s", run2.String())
	}
}

// ash is a different shell with a different history file. Storing everything in
// bash's file would put a file on the router that a BusyBox box would never have.
func TestHistoryFileMatchesTheShell(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	u := router.FindUser("root")
	if u == nil {
		t.Fatal("no root on the router")
	}
	out := &bufOut{}
	sh := shell.NewShell(w, router, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("uname -a\r\nexit\r\n"))
	sh.RunLoop(nil)

	if _, ok := router.FS.Read("/root/.ash_history"); !ok {
		t.Fatal("a BusyBox router should keep .ash_history, not .bash_history")
	}
	if _, ok := router.FS.Read("/root/.bash_history"); ok {
		t.Fatal("the router should not have a bash history file")
	}
}

// Clearing the history is a deliberate act with a consequence: it removes the
// trail AND is itself recorded. Otherwise an attacker could erase the evidence
// for free, and the defensive side of the game would have nothing to act on.
func TestClearingHistoryIsRecorded(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")

	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("echo important\r\nhistory -c\r\nexit\r\n"))
	sh.RunLoop(nil)

	if len(w.Case.Events) == 0 {
		t.Fatal("clearing shell history left no trace in the evidence graph")
	}
	found := false
	for _, e := range w.Case.Events {
		if strings.Contains(e.Detail, "history") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the history-clearing act was not recorded: %+v", w.Case.Events)
	}
}

func listRoot(d *core.Device, dir string) string {
	var b strings.Builder
	names := d.FS.List(dir)
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	return b.String()
}
