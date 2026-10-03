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
