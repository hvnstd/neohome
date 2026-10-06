package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §19: IRC is a communication system, not a local chat log. The channel only
// exists while the community server answers, and reaching it is subject to
// the same name resolution and routing as everything else in the world.
func TestIRCNeedsItsServerAndTheName(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if pc == nil {
		t.Fatal("no player PC")
	}
	ircd := w.Devices["irc"]
	if ircd == nil {
		t.Fatal("the community IRC host is not in the world")
	}
	if svc := ircd.Svc("ircd"); svc == nil || svc.Port != 6667 || svc.State != "running" {
		t.Fatalf("ircd is not a real running service: %+v", svc)
	}

	// the scripted DNS fault is still active on a fresh world: by name, the
	// server is unreachable, and the error says so instead of failing silently
	out := run(t, w, pc, "alex", "irc")
	if !strings.Contains(out, "irc.neohome.example") || !strings.Contains(out, "resolve") {
		t.Fatalf("during the DNS fault irc must report the resolution failure:\n%s", out)
	}

	// fix the resolver the way the player does, and the same command works
	repairDNS(t, w)
	out = run(t, w, pc, "alex", "irc")
	if !strings.Contains(out, "#local") || !strings.Contains(out, "online:") {
		t.Fatalf("after the repair irc should reach the real server:\n%s", out)
	}

	// the server process is the gate: stop ircd and the channel is down
	if _, err := ircd.StopService("ircd"); err != nil {
		t.Fatalf("stopping ircd: %v", err)
	}
	out = run(t, w, pc, "alex", "irc read")
	if !strings.Contains(out, "connect") || !strings.Contains(out, "refused") {
		t.Fatalf("with ircd stopped, irc must refuse honestly:\n%s", out)
	}

	// recovery: starting it again restores the channel without touching the client
	if _, err := ircd.StartService("ircd"); err != nil {
		t.Fatalf("starting ircd: %v", err)
	}
	out = run(t, w, pc, "alex", "irc read")
	if strings.Contains(out, "refused") || strings.Contains(out, "resolve") {
		t.Fatalf("restarting ircd should restore the channel:\n%s", out)
	}
}

// Saying something: the message is world state, the residents answer, and the
// answer is grounded in what is actually true right now.
func TestIRCSayIsWorldState(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	before := len(w.Chat.History)

	out := run(t, w, pc, "alex", "irc say my dns is broken, any idea?")
	if !strings.Contains(out, "my dns is broken") {
		t.Fatalf("the player's own line should be echoed from the channel:\n%s", out)
	}
	if len(w.Chat.History) <= before {
		t.Fatal("posting to IRC did not change the world's chat state")
	}
	// an NPC answered, and the answer came from the live fault state
	last := w.Chat.History[len(w.Chat.History)-1]
	if last.Nick == "alex" {
		t.Fatal("no resident answered a direct technical question")
	}
	low := strings.ToLower(last.Text)
	if !strings.Contains(low, "dnsmasq") && !strings.Contains(low, "resolv") {
		t.Fatalf("the answer is not grounded in the real fault: %s", last.Text)
	}
	// the reply is visible to the next reader, because the log is real
	out = run(t, w, pc, "alex", "irc read")
	if !strings.Contains(out, last.Text) {
		t.Fatalf("the channel log does not contain the reply:\n%s", out)
	}
}

// §24: the player can SSH into the assistant's machine and look at its working
// environment — key-only, because that is the trust that was actually seeded.
func TestAssistantNodeIsReachableByKeyOnly(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]
	if asst == nil {
		t.Fatal("no assistant node")
	}
	if data, ok := asst.FS.Read("/etc/ssh/sshd_config"); !ok || !strings.Contains(string(data), "PasswordAuthentication no") {
		t.Fatalf("the assistant node should be key-only:\n%s", data)
	}
	if !w.AssistantKeyTrusted(pc, asst) {
		t.Fatal("the assistant node should trust a session on the owner's PC")
	}

	// the owner's session gets in without a password prompt and sees the
	// assistant's workspace — the non-interactive form runs one command there
	// and returns its status, like real ssh
	out := run(t, w, pc, "alex", "ssh assistant@assistant whoami")
	if strings.Contains(out, "password:") {
		t.Fatalf("key-only access should not ask for a password:\n%s", out)
	}
	if !strings.Contains(out, "Welcome to assistant") || !strings.Contains(out, "assistant\n") {
		t.Fatalf("the owner cannot log into the assistant node:\n%s", out)
	}
	out = run(t, w, pc, "alex", "ssh assistant@assistant ls /home/assistant")
	if !strings.Contains(out, "tasks.md") || !strings.Contains(out, "jobs.log") {
		t.Fatalf("the assistant's working environment is not visible:\n%s", out)
	}
	// an interactive session on the same node works and reads the same stdin
	out = runWithStdin(t, w, pc, "alex", "ssh assistant@assistant", "ls /home/assistant", "exit")
	if !strings.Contains(out, "tasks.md") || !strings.Contains(out, "assistant@assistant") {
		t.Fatalf("interactive ssh into the assistant node is broken:\n%s", out)
	}
	// a command that fails on the target reports its real exit status
	if st := remoteStatus(t, w, pc, "alex", "ssh assistant@assistant true; ssh assistant@assistant false"); st != 1 {
		t.Fatalf("ssh command form must return the remote status, got %d", st)
	}

	// the boundary: a machine whose owner does not own this assistant is not
	// trusted, so the key path refuses it
	mcp := w.Devices["mcp-agent-pc"]
	if mcp == nil {
		if _, _, err := w.EnsureMCPPlayer(); err != nil {
			t.Fatalf("provisioning the second character: %v", err)
		}
		mcp = w.Devices["mcp-agent-pc"]
	}
	if w.AssistantKeyTrusted(mcp, asst) {
		t.Fatal("a stranger's machine must not be trusted by the assistant node")
	}
	out = run(t, w, mcp, "mcp-agent", "ssh assistant@assistant whoami")
	if !strings.Contains(out, "Permission denied (publickey)") {
		t.Fatalf("an untrusted owner must be refused, got:\n%s", out)
	}
}
