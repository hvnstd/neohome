package tests

import (
	"os"
	"path/filepath"
	"testing"

	"neohome/internal/core"
)

// WS-0.1 — mail is real state with a real spool, not an unstructured text file.
//
// A mailbox a player can read but the world cannot persist, or vice versa, is
// not a mail system. These tests pin the two halves: the on-disk shape a real
// box has (/var/spool/mail owned by the daemon, /var/mail readable by the
// user), and that a message survives a gob round trip with its device log and
// world event intact.
func TestMailSpoolIsRealWorldState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if pc == nil {
		t.Fatal("pc-alex missing")
	}

	// The daemon-owned spool and the user-readable inbox both exist as real
	// directories on an initialised world.
	if !pc.FS.IsDir(core.MailSpoolDir) {
		t.Fatalf("daemon spool %s does not exist on a fresh world", core.MailSpoolDir)
	}
	if !pc.FS.IsDir(core.MailInboxDir) {
		t.Fatalf("inbox dir %s does not exist on a fresh world", core.MailInboxDir)
	}

	// Deliver a message. Delivery must leave three traceable consequences: the
	// mbox file itself, a syslog line on the receiving device, and a world
	// event. Any one of them missing means the delivery is not observable.
	from := core.MailAddr("world", "core-gw")
	if err := w.DeliverLocal(pc, from, "alex", "DHCP lease renewed", "your router handed out a new lease"); err != nil {
		t.Fatalf("DeliverLocal refused a valid recipient: %v", err)
	}

	mbox := core.MailboxPath("alex")
	data, ok := pc.FS.Read(mbox)
	if !ok || len(data) == 0 {
		t.Fatalf("no mbox written at %s", mbox)
	}
	if subs := core.InboxSubjects(pc, "alex"); len(subs) != 1 || subs[0] != "DHCP lease renewed" {
		t.Fatalf("inbox subjects = %v, want the delivered subject", subs)
	}

	syslog, _ := pc.FS.Read("/var/log/syslog")
	if !containsSub(string(syslog), "accepted") {
		t.Fatalf("smtpd left no syslog trace:\n%s", string(syslog))
	}
	found := false
	for _, e := range w.Events {
		if e.Source == "mail" && e.Dev == pc.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("delivery left no world event")
	}

	// An unknown local user must be refused, not silently accepted: a real MTA
	// bounces. This is the failure boundary the subsystem has to keep.
	if err := w.DeliverLocal(pc, from, "nobody-here", "x", "y"); err == nil {
		t.Fatal("DeliverLocal accepted mail for an unknown local user")
	}

	// Only the recipient's inbox exists — a bounced recipient must not have
	// created a mailbox as a side effect.
	if pc.FS.Exists(core.MailboxPath("nobody-here")) {
		t.Fatal("a refused delivery created a mailbox")
	}
}

// The mailbox is the recipient's; another ordinary account cannot read it.
// This is the permission boundary the existing VFS already enforces, and mail
// must not bypass it. Existence and permission are asserted separately — a
// mailbox that exists but is unreadable is a different finding from "absent".
func TestMailboxIsNotWorldReadable(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if err := w.DeliverLocal(pc, core.MailAddr("world", "core-gw"), "alex", "private", "bank token"); err != nil {
		t.Fatal(err)
	}
	alex := pc.FindUser("alex")
	if alex == nil {
		t.Fatal("alex missing")
	}
	if _, exists, permitted := core.ReadInbox(pc, "alex", alex); !exists || !permitted {
		t.Fatalf("the owner cannot read their own inbox (exists=%v permitted=%v)", exists, permitted)
	}

	// An unrelated ordinary account: the file exists, and it is refused.
	// Copying alex and renaming keeps every other field identical so the only
	// thing that can grant access is identity.
	other := *alex
	other.Name = "guest"
	other.UID = alex.UID + 1
	if _, exists, permitted := core.ReadInbox(pc, "alex", &other); !exists {
		t.Fatal("the mailbox should still exist for an unrelated account — that is what is refused")
	} else if permitted {
		t.Fatal("an unrelated ordinary account read someone else's mailbox")
	}

	// root is allowed: administration is a real capability, not decoration.
	if root := pc.FindUser("root"); root != nil {
		if _, _, permitted := core.ReadInbox(pc, "alex", root); !permitted {
			t.Fatal("root must be able to read a mailbox")
		}
	}

	// And a mailbox that was never delivered to is absent, not "denied" —
	// the two must never be conflated in output either.
	if _, exists, _ := core.ReadInbox(pc, "nobody-here", &other); exists {
		t.Fatal("a mailbox appeared for a user that never received mail")
	}
}

// A message must survive save/load with every consequence still attached —
// otherwise restarts silently lose mail while the log claims it was delivered.
func TestMailSurvivesSaveLoad(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if err := w.DeliverLocal(pc, core.MailAddr("world", "core-gw"), "alex", "persist me", "body"); err != nil {
		t.Fatal(err)
	}
	before := core.InboxSubjects(pc, "alex")

	path := filepath.Join(t.TempDir(), "w.gob")
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("save wrote no file: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc2 := w2.Devices["pc-alex"]
	after := core.InboxSubjects(pc2, "alex")
	if len(after) != len(before) {
		t.Fatalf("inbox lost messages across save/load: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("subject %d changed across save/load: %q -> %q", i, before[i], after[i])
		}
	}
	// The subsystem handle survives with its counter, so status text stays true.
	if w2.MailDomain() == "" {
		t.Fatal("mail domain lost across save/load")
	}
	// And delivery still works after a load — the subsystem is live, not a
	// read-only snapshot.
	if err := w2.DeliverLocal(pc2, core.MailAddr("world", "core-gw"), "alex", "after reload", "b"); err != nil {
		t.Fatalf("delivery broken after load: %v", err)
	}
	if subs := core.InboxSubjects(pc2, "alex"); len(subs) != len(before)+1 {
		t.Fatalf("post-load delivery did not append: %v", subs)
	}
}

// WS-0.2 — the MTA is a real daemon on a real port, not a world-level function.
//
// If :25 answers regardless of service state, then `systemctl stop smtpd` is
// decoration and the player has no lever. The port must be gated exactly like
// nfsd/sshd are.
func TestSMTPDPortIsGatedByServiceState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]
	nas := w.Devices["nas-alex"]

	if pc.Svc("smtpd") == nil {
		t.Fatal("the player's own machine has no mail transfer agent")
	}
	if pc.Svc("smtpd").Port != 25 {
		t.Fatalf("smtpd listens on %d, want 25", pc.Svc("smtpd").Port)
	}
	// A storage box is not a mail host; installing the daemon everywhere would
	// make the service meaningless.
	if nas.Svc("smtpd") != nil {
		t.Fatal("the NAS should not ship a mail transfer agent")
	}
	// The assistant node really does run one — it has its own account.
	if asst.Svc("smtpd") == nil {
		t.Fatal("the assistant node should run a mail transfer agent")
	}

	// Running: the LAN port answers. Dial(src, dst.IP, port) connects *to dst*,
	// so the service under test is the one on the target, not on src.
	_, _, msg := core.Dial(pc, asst.FirstLANIP(), 25)
	if msg != "connected" {
		t.Fatalf("smtpd should accept LAN connections, got: %s", msg)
	}

	// Stopped on the *target*: the port must genuinely close.
	asst.StopService("smtpd")
	_, _, msg = core.Dial(pc, asst.FirstLANIP(), 25)
	if msg == "connected" {
		t.Fatal("a stopped smtpd must not accept connections")
	}

	// Started again: it must come back, and re-arming must not need a restart.
	asst.StartService("smtpd")
	_, _, msg = core.Dial(pc, asst.FirstLANIP(), 25)
	if msg != "connected" {
		t.Fatalf("restarted smtpd should accept connections again, got: %s", msg)
	}

	// Scope is real too: a LAN-only MTA must not answer from the internet.
	// 198.51.100.1 is the household router's public address in this world.
	if svc, _, msg := core.Dial(w.Devices["isp-dns"], "198.51.100.1", 25); svc != nil {
		t.Fatalf("a LAN-scoped smtpd answered a WAN client: %s", msg)
	}

	// The config it claims to honour is a real file on the device.
	if data, ok := pc.FS.Read("/etc/mail/smtpd.conf"); !ok || len(data) == 0 {
		t.Fatal("smtpd has no readable configuration file")
	}
}

// Local-vs-remote resolution: an address for another machine's account must not
// be quietly delivered into a local mailbox. Conflating the two is how a player
// ends up with mail that "arrived" that could not possibly have.
func TestLocalUserResolutionRejectsForeignHosts(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	for _, addr := range []string{"alex", "alex@" + pc.Hostname, "alex@localhost", "alex@" + pc.ID} {
		if user, local := core.LocalUserFor(pc, addr); !local || user != "alex" {
			t.Errorf("LocalUserFor(%q) = (%q, %v), want (alex, true)", addr, user, local)
		}
	}
	for _, addr := range []string{
		"alex@nas-alex",          // a real device, but not this one
		"alex@somewhere.example", // a remote domain
		"nobody-here",            // no such local account
		"",                       // no address at all
	} {
		if _, local := core.LocalUserFor(pc, addr); local {
			t.Errorf("LocalUserFor(%q) claimed local delivery", addr)
		}
	}
}

// WS-0.4 — routing must respect real topology: a message to another machine
// goes through that machine's MTA, and a down MTA produces a queued message
// with a diagnostic rather than a delivery nobody can trace.
func TestRouteMailCrossesRealServiceGates(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]

	// The assistant account exists on a different machine, so this is a
	// cross-host submission: it must succeed while that MTA is running.
	res := w.RouteMail(pc, "alex", "assistant", "status?", "how is the node")
	if !res.Delivered {
		t.Fatalf("cross-host delivery to a running MTA failed: %+v", res)
	}
	if res.Via != "relay:"+asst.ID {
		t.Errorf("delivery took the wrong path: %q", res.Via)
	}
	if subs := core.InboxSubjects(asst, "assistant"); len(subs) != 1 || subs[0] != "status?" {
		t.Fatalf("the recipient's mailbox does not hold the message: %v", subs)
	}
	// The recipient's MTA recorded it, and the sender is not credited with a
	// local delivery that never happened.
	syslog, _ := asst.FS.Read("/var/log/syslog")
	if !containsSub(string(syslog), "relayed message") {
		t.Fatalf("the receiving MTA left no trace:\n%s", string(syslog))
	}
	if subs := core.InboxSubjects(pc, "alex"); len(subs) != 0 {
		t.Fatalf("a cross-host message landed in the sender's own mailbox: %v", subs)
	}

	// Stop the remote MTA: the same submission must now queue, with a real
	// diagnostic, and must NOT appear as delivered anywhere.
	asst.StopService("smtpd")
	queuedBefore := len(w.Mail().Queue)
	res = w.RouteMail(pc, "alex", "assistant", "second", "while you are down")
	if res.Delivered {
		t.Fatalf("a message was delivered while the remote MTA was stopped: %+v", res)
	}
	if !res.Queued {
		t.Fatalf("a down MTA should defer the message, got: %+v", res)
	}
	if res.Diagnostic == "" {
		t.Error("a queued message must carry a diagnostic saying why")
	}
	if len(w.Mail().Queue) != queuedBefore+1 {
		t.Fatalf("the deferred message is not in the queue: %+v", w.Mail().Queue)
	}
	if subs := core.InboxSubjects(asst, "assistant"); len(subs) != 1 {
		t.Fatalf("a stopped MTA still delivered mail: %v", subs)
	}
	// The sender's own log must say it was deferred, not sent.
	psys, _ := pc.FS.Read("/var/log/syslog")
	if !containsSub(string(psys), "queued message") {
		t.Fatalf("the sending MTA did not record the deferral:\n%s", string(psys))
	}

	// An unknown recipient bounces and is not silently swallowed.
	res = w.RouteMail(pc, "alex", "ghost@nowhere.example", "hi", "anyone?")
	if res.Delivered || res.Queued {
		t.Fatalf("an unknown recipient must bounce: %+v", res)
	}
	if res.Via != "bounce" {
		t.Errorf("Via = %q, want bounce", res.Via)
	}
}

// WS-0.5 — job notifications must go through the same real path, so a player
// finds out about work the same way any other mail arrives: in their mailbox.
func TestJobNotificationArrivesAsRealMail(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	before := len(core.InboxSubjects(pc, "alex"))
	// An NPC client posting work is the real origin of these notifications.
	if err := w.SendMail("jobs", "alex", "new job: fix the neighbour's DNS", "50 credits, pay on completion"); err != nil {
		t.Fatalf("job notification was not accepted: %v", err)
	}
	after := core.InboxSubjects(pc, "alex")
	if len(after) != before+1 {
		t.Fatalf("job notification did not arrive as mail: %v", after)
	}
	if after[len(after)-1] != "new job: fix the neighbour's DNS" {
		t.Fatalf("wrong subject landed: %q", after[len(after)-1])
	}
	// And the world-level mail log agrees, so `mail log` is not a fiction.
	found := false
	for _, l := range w.MailLog {
		if containsSub(l, "fix the neighbour's DNS") {
			found = true
		}
	}
	if !found {
		t.Fatalf("mail log has no record of the notification: %v", w.MailLog)
	}
}

func containsSub(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
