package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// The board is a real host with a real service and real files — spec §19
// forbids a decorative BBS, so the posts must be files on the box and the
// seeded threads must be true of the world.
func TestBBSHostAndBoardsAreReal(t *testing.T) {
	w := core.NewWorld()
	bbs := w.Devices["bbs"]
	if bbs == nil {
		t.Fatal("no BBS host in the world")
	}
	svc := bbs.Svc("bbsd")
	if svc == nil || svc.Port != 2323 || svc.State != "running" || svc.Scope != "any" {
		t.Fatalf("bbsd is not a real public service: %+v", svc)
	}
	if conf, ok := bbs.FS.Read("/etc/bbsd.conf"); !ok || !strings.Contains(string(conf), "2323") {
		t.Fatal("bbsd has no real configuration file")
	}
	// the seeded threads are files on the box, readable like any file
	data, ok := bbs.FS.Read("/srv/bbs/hacker/001.txt")
	if !ok || !strings.Contains(string(data), "From: mara-bot") ||
		!strings.Contains(string(data), "port 23") {
		t.Fatalf("the hacker board's opening post is not a real file:\n%s", data)
	}
	// and the DNS layer knows the box
	found := false
	for _, r := range w.Records {
		if r.Name == "bbs.neohome.example" {
			found = true
		}
	}
	if !found {
		t.Fatal("bbs.neohome.example is not in the zone")
	}
	for _, b := range core.BBSBoards {
		if n := len(w.BBSList(b)); n < 2 {
			t.Fatalf("board %s shipped with %d posts; a dead board is decoration", b, n)
		}
	}
}

func TestBBSReadOverNetwork(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("bbs boards")
	if !strings.Contains(out, "general") || !strings.Contains(out, "hacker") {
		t.Fatalf("bbs boards does not list the boards:\n%s", out)
	}
	out = sh("bbs read hacker")
	if !strings.Contains(out, "old consumer routers still ship telnet") ||
		!strings.Contains(out, "scans are logged on the target") {
		t.Fatalf("the hacker board does not show its real threads:\n%s", out)
	}
	out = sh("bbs read hacker 1")
	if !strings.Contains(out, "port 23, no banner") || !strings.Contains(out, "mara-bot") {
		t.Fatalf("reading one post does not show its body:\n%s", out)
	}
	if out := sh("bbs read nonsense"); out == "" || !strings.Contains(out, "no such board") {
		t.Fatalf("an unknown board must be refused:\n%s", out)
	}

	// the port only answers because bbsd runs — the honest gate
	bbs := w.Devices["bbs"]
	if _, err := bbs.StopService("bbsd"); err != nil {
		t.Fatalf("stopping bbsd failed: %v", err)
	}
	out = sh("bbs boards")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped bbsd must refuse:\n%s", out)
	}
	if _, err := bbs.StartService("bbsd"); err != nil {
		t.Fatalf("starting bbsd failed: %v", err)
	}
	if out := sh("bbs read market"); !strings.Contains(out, "WTS: mini PC") {
		t.Fatalf("bbsd did not come back:\n%s", out)
	}
}

func TestBBSPlayerPostGetsStateAwareReply(t *testing.T) {
	w := core.NewWorld()

	// Part 1 — the fault-aware reply. The world's DNS fault is active, and a
	// post about DNS must get an answer that knows it. Posting through the
	// core API keeps the resolver broken (the shell path below needs it
	// fixed, since the board's name is a public zone record).
	if _, err := w.BBSPost("intel", "alex", "is anyone else's dns dead?", "every name SERVFAILs from here since the power blip", ""); err != nil {
		t.Fatalf("post failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		w.Tick()
	}
	posts := w.BBSList("intel")
	last := posts[len(posts)-1]
	if last.From != "sysmods" || !strings.HasPrefix(last.Subject, "re:") {
		t.Fatalf("no NPC reply arrived: %+v", last)
	}
	if !strings.Contains(last.Body, "resolv-file") && !strings.Contains(last.Body, "dnsmasq") {
		t.Fatalf("the reply does not reflect the actual fault state:\n%s", last.Body)
	}

	// Part 2 — the shell path: repair the router exactly like a player, then
	// post through the client with its stdin body. The post is a real file,
	// and the board host logged it.
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("healthy again on my end, thanks\n.\n"))
	sh.ExecLine("bbs post intel resolver is fine now")
	if !strings.Contains(out.String(), "posted intel/") {
		t.Fatalf("the post did not go through:\n%s", out.String())
	}
	bbs := w.Devices["bbs"]
	var mine *core.BBSPost
	for _, p := range w.BBSList("intel") {
		if p.From == "alex" && p.Subject == "resolver is fine now" {
			mine = &p
		}
	}
	if mine == nil {
		t.Fatal("the player's post is not listed")
	}
	if data, ok := bbs.FS.Read(fmt.Sprintf("/srv/bbs/intel/%03d.txt", mine.Num)); !ok || !strings.Contains(string(data), "From: alex") {
		t.Fatalf("the player's post is not a real file on the board:\n%s", data)
	}
	syslog, _ := bbs.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "in intel from alex") {
		t.Fatal("bbsd did not log the post")
	}
	// and this second post earns its own reply within a few ticks
	for i := 0; i < 5; i++ {
		w.Tick()
	}
	posts = w.BBSList("intel")
	last = posts[len(posts)-1]
	if !strings.HasPrefix(last.Subject, "re: resolver is fine now") {
		t.Fatalf("the shell post earned no reply: %+v", last)
	}
}

func TestBBSAmbientPostIsStateAware(t *testing.T) {
	w := core.NewWorld()
	// tick past the first ambient window (tick 40): the world's DNS fault is
	// active, so the board's NPC must complain about the resolver, in the
	// same board where the seeded thread pointed at it
	for i := 0; i < 45; i++ {
		w.Tick()
	}
	found := false
	for _, p := range w.BBSList("intel") {
		if strings.Contains(p.Subject, "DNS dead") &&
			strings.Contains(p.Body, "my cron job that curls the mirror agrees") {
			found = true
		}
	}
	if !found {
		t.Fatal("while the DNS fault is active the board stays silent about it")
	}
}

func TestBBSPersistenceRoundTrip(t *testing.T) {
	w := core.NewWorld()
	if _, err := w.BBSPost("market", "alex", "WTB: old GPU", "cheap, for the lab", ""); err != nil {
		t.Fatalf("post failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bbs_world.gob")
	if err := w.Save(path); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if w2.BBS == nil || w2.BBSDevice() == nil {
		t.Fatal("loaded world lost its BBS subsystem")
	}
	found := false
	for _, p := range w2.BBSList("market") {
		if p.Subject == "WTB: old GPU" {
			found = true
		}
	}
	if !found {
		t.Fatal("the post did not survive the round trip")
	}
	// the counter carried over: the next post number must not collide
	if p, err := w2.BBSPost("market", "alex", "second", "still here", ""); err != nil || p.Num == 1 {
		t.Fatalf("post numbering restarted after load: %+v err=%v", p, err)
	}
}
