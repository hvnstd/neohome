package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// §附: a player's first connection should show a short welcome naming the
// device, account, world time and the help/guide entries — like a real login
// showing /etc/motd. It must not block, accept work or move money.
func TestWelcomeBannerOnLogin(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("exit\r\n"))
	sh.RunLoop(nil)
	got := out.String()
	for _, want := range []string{"NeoHome — you are alex@home-pc", "World time:", "assist guide", "help <cmd>", "motd"} {
		if !strings.Contains(got, want) {
			t.Fatalf("welcome banner missing %q:\n%s", want, got)
		}
	}
	// the banner is observation only — it must never act for the player
	if w.Job("J-101").Accepted != "" || w.Job("J-101").Done {
		t.Fatal("the welcome banner must not accept or complete work")
	}
}

// The welcome fires only on the player's own PC. A nested login on the router
// (root, not a registered player) gets its prompt, not the NeoHome banner —
// the way a real box shows its own motd, not the originating shell's.
func TestWelcomeBannerSkipsNonPlayerDevice(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	u := router.FindUser("root")
	out := &bufOut{}
	sh := shell.NewShell(w, router, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("exit\r\n"))
	sh.RunLoop(nil)
	if strings.Contains(out.String(), "NeoHome — you are") {
		t.Fatalf("the welcome banner must not fire on a non-player device:\n%s", out.String())
	}
}

// `motd` replays the welcome on demand (§附: re-viewable any time).
func TestMotdReplaysWelcome(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	got := run(t, w, pc, "alex", "motd")
	for _, want := range []string{"NeoHome — you are alex@", "World time:", "assist guide"} {
		if !strings.Contains(got, want) {
			t.Fatalf("motd should replay the welcome, missing %q:\n%s", want, got)
		}
	}
}

// §附: `help <cmd>` must show purpose, usage and permissions drawn from the
// actually registered command; an unknown command and an uninstalled §33 tool
// must each get a clear, diagnosable error.
func TestHelpPerCommand(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	got := run(t, w, pc, "alex", "help ls")
	for _, want := range []string{"ls —", "usage:", "permissions:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help ls should show purpose/usage/permissions, missing %q:\n%s", want, got)
		}
	}

	// an unknown command is a clear error, not silence or a ghost entry
	got = run(t, w, pc, "alex", "help definitelynotacommand")
	if !strings.Contains(got, "command not found") {
		t.Fatalf("help on an unknown command must say command not found:\n%s", got)
	}

	// a §33 tool whose package is not installed is reported as a gap, so the
	// player knows installing is the next step (not that the command is fake)
	got = run(t, w, pc, "alex", "help fail2ban-client")
	if !strings.Contains(got, "uninstalled") {
		t.Fatalf("help on an uninstalled §33 tool should name the gap:\n%s", got)
	}
}

// §附: after the opening DNS chain resolves, `assist guide` should offer a
// concrete next step from real state — not just "use job list". With the
// assistant untrained, that next step is to train it. The DNS chain itself
// (completed-job + job-list message) must still be present.
func TestAssistGuideSuggestsNextSteps(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	// repair DNS the way the existing guide test does, then pay the job
	data, ok := router.FS.Read("/etc/dnsmasq.conf")
	if !ok {
		t.Fatal("router dnsmasq config is missing")
	}
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	fixed := strings.Replace(string(data), "resolv-file=/var/run/dnsmasq/resolv.conf", "resolv-file=/etc/dnsmasq.upstream", 1)
	router.FS.Write("/etc/dnsmasq.conf", fixed, 0644, "root", "root")
	if _, err := router.RestartService("dnsmasq"); err != nil {
		t.Fatalf("restart dnsmasq: %v", err)
	}
	if out := run(t, w, pc, "alex", "job accept J-101"); !strings.Contains(out, "accepted") {
		t.Fatalf("accept J-101: %s", out)
	}
	if _, _, err := w.PayJob("alex", "J-101"); err != nil {
		t.Fatalf("pay J-101: %v", err)
	}

	got := run(t, w, pc, "alex", "assist guide")
	// the completed job is still recognised
	if !strings.Contains(got, "already completed") || !strings.Contains(got, "job list") {
		t.Fatalf("guide should still recognise the completed DNS job:\n%s", got)
	}
	// and a real next step is offered: the assistant has no trained skill yet
	if !strings.Contains(got, "assist train") {
		t.Fatalf("guide should suggest training the assistant next:\n%s", got)
	}
}
