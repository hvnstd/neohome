package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Web terminal (Phase 3) — a browser is one-shot HTTP, so the world serves
// webd on 8080 with the same accounts ssh uses: POST /session mints a token,
// GET /query runs one line, and every session leaves the same evidence.

func TestBrowseOpensSessionAndRuns(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]

	if svc := asst.Svc("webd"); svc == nil || svc.Port != 8080 {
		t.Fatalf("webd must be a real service: %+v", svc)
	}
	// bad password is refused by the target, like ssh
	out := runWithStdin(t, w, pc, "alex", "browse assistant@assistant -c whoami", "wrong")
	if !strings.Contains(out, "invalid credentials") {
		t.Fatalf("bad credentials must be refused, got:\n%s", out)
	}
	// good password: a session opens, the command runs on the far machine
	out = runWithStdin(t, w, pc, "alex", "browse assistant@assistant -c whoami -c hostname", "assist-pass")
	if !strings.Contains(out, "session wt") || !strings.Contains(out, "assistant\n") {
		t.Fatalf("the session must run commands, got:\n%s", out)
	}
	if !strings.Contains(out, "session closed") {
		t.Fatalf("the session must close:\n%s", out)
	}
	// and the login is evidence: syslog line, active-login row removed
	syslog, _ := asst.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "web session opened") {
		t.Fatalf("the session must be logged:\n%s", string(syslog))
	}
	web := 0
	for _, l := range asst.Active {
		if l.TTY == "web" {
			web++
		}
	}
	if web != 0 {
		t.Fatal("a closed session must not leave an active-login row")
	}
}

func TestBrowseGatesLikeEveryOtherPort(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// stopping the daemon closes the port
	asst := w.Devices["asst-alex"]
	asst.StopService("webd")
	out := runWithStdin(t, w, pc, "alex", "browse assistant -c whoami", "assist-pass")
	if !strings.Contains(out, "refused") && !strings.Contains(out, "Connection") {
		t.Fatalf("a stopped webd must refuse, got:\n%s", out)
	}
	// a machine with no webd is refused with the world's own words: the
	// NAS's 8080 belongs to nginx, and a webd-less box refuses the session
	// (its landing page says what it really is) rather than pretending
	if out := runWithStdin(t, w, pc, "alex", "browse nas -c whoami", "alex123"); !strings.Contains(out, "session") && !strings.Contains(out, "credential") {
		t.Fatalf("no webd on the NAS must be honestly refused, got:\n%s", out)
	}
}

func TestWebSessionsExpire(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]
	runWithStdin(t, w, pc, "alex", "browse assistant -c whoami", "assist-pass")
	if len(w.WebSessions) != 0 {
		t.Fatal("a closed session must be reaped immediately")
	}
	// sessions are created and destroyed through the same API; a raw one
	// expires on world time, never on wall clock
	s, err := w.WebOpen(asst, "assistant", "assist-pass")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, ok := w.WebSessionFor(s.Token); !ok {
		t.Fatal("a fresh session must resolve")
	}
	w.Sim = w.Sim.Add(20 * 60 * 1000000000)
	if _, ok := w.WebSessionFor(s.Token); ok {
		t.Fatal("an expired token must be gone")
	}
}

func TestWebSessionSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	asst := w.Devices["asst-alex"]
	if _, err := w.WebOpen(asst, "assistant", "assist-pass"); err != nil {
		t.Fatalf("open: %v", err)
	}
	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back.WebSessions) != 1 {
		t.Fatal("live sessions must survive the save")
	}
}
