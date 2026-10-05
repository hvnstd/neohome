package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The reading half of the mail system: imapd is a real service on the mail
// hosts, and a mailbox on another machine opens over the network with the
// account's real credentials.
func TestIMAPServiceIsSeeded(t *testing.T) {
	w := core.NewWorld()
	for _, id := range []string{"pc-alex", "asst-alex"} {
		d := w.Devices[id]
		svc := d.Svc("imapd")
		if svc == nil {
			t.Fatalf("%s has no imapd service", id)
		}
		if svc.Port != 143 || svc.State != "running" {
			t.Fatalf("imapd on %s is not a live service on 143: %+v", id, svc)
		}
		if conf, ok := d.FS.Read("/etc/mail/imapd.conf"); !ok || len(conf) == 0 {
			t.Fatalf("imapd on %s has no real configuration file", id)
		}
	}
	if w.Devices["nas-alex"].Svc("imapd") != nil {
		t.Fatal("the NAS is a storage box, not a mail host — it must not run imapd")
	}
}

func TestIMAPRemoteReadOverNetwork(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	// a message really delivered into alex's mailbox on the PC, with a body
	// line that must not swallow the next record
	if err := w.DeliverLocal(pc, "world@neohome", "alex", "backup finished", "all good\nFrom the nightly job\nsee you tomorrow"); err != nil {
		t.Fatalf("seed delivery failed: %v", err)
	}
	if err := w.DeliverLocal(pc, "assistant@asst-alex.local", "alex", "second message", "plain body"); err != nil {
		t.Fatalf("seed delivery failed: %v", err)
	}

	sh := func(line string) string { return run(t, w, nas, "alex", line) }

	// from the NAS, over the LAN, with the account's own credentials
	out := sh("mutt -f imap://alex:alex123@home-pc/INBOX")
	if !strings.Contains(out, "2 messages in INBOX") {
		t.Fatalf("remote INBOX did not open with its real contents:\n%s", out)
	}
	if !strings.Contains(out, "backup finished") || !strings.Contains(out, "second message") {
		t.Fatalf("the index does not show the delivered subjects:\n%s", out)
	}

	// opening message 1 shows the body with the mboxo escape undone
	out = sh("mutt -f imap://alex:alex123@home-pc/INBOX 1")
	if !strings.Contains(out, "From the nightly job") {
		t.Fatalf("the body lost its From line to the mboxo escape:\n%s", out)
	}
	if !strings.Contains(out, "see you tomorrow") {
		t.Fatal("the From line split the message: the rest of the body is missing")
	}

	// the read is evidence on the mail host, like any session
	syslog, _ := pc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "alex opened INBOX") {
		t.Fatal("the imapd did not log the successful session")
	}
}

func TestIMAPAuthIsReal(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	if err := w.DeliverLocal(pc, "world@neohome", "alex", "private", "secret body"); err != nil {
		t.Fatalf("seed delivery failed: %v", err)
	}
	sh := func(line string) string { return run(t, w, nas, "alex", line) }

	// a wrong password must not open the mailbox, and must not say whether
	// the account or the password was wrong
	out := sh("mutt -f imap://alex:wrongpass@home-pc/INBOX")
	if !strings.Contains(out, "login failed") {
		t.Fatalf("a wrong password opened the mailbox:\n%s", out)
	}
	if strings.Contains(out, "secret body") || strings.Contains(out, "private") {
		t.Fatal("the failure leaked mailbox contents")
	}
	// an account that does not exist on the mail host fails the same way
	out = sh("mutt -f imap://assistant:whatever@home-pc/INBOX")
	if !strings.Contains(out, "login failed") {
		t.Fatalf("a foreign account opened a mailbox on home-pc:\n%s", out)
	}
	// both failures are evidence on the target
	syslog, _ := pc.FS.Read("/var/log/syslog")
	if got := strings.Count(string(syslog), "login failed for"); got < 2 {
		t.Fatalf("failed logins were not logged on the mail host: %d", got)
	}
}

// The port only answers because the daemon is running — stopping imapd
// really closes remote mailbox access, and starting it reopens it.
func TestIMAPGatedByServiceState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	if err := w.DeliverLocal(pc, "world@neohome", "alex", "hi", "hello body"); err != nil {
		t.Fatalf("seed delivery failed: %v", err)
	}
	sh := func(line string) string { return run(t, w, nas, "alex", line) }

	if _, err := pc.StopService("imapd"); err != nil {
		t.Fatalf("stopping imapd failed: %v", err)
	}
	out := sh("mutt -f imap://alex:alex123@home-pc/INBOX")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped imapd must refuse the connection:\n%s", out)
	}
	if _, err := pc.StartService("imapd"); err != nil {
		t.Fatalf("starting imapd failed: %v", err)
	}
	out = sh("mutt -f imap://alex:alex123@home-pc/INBOX")
	if !strings.Contains(out, "1 messages in INBOX") {
		t.Fatalf("imapd did not come back:\n%s", out)
	}
}

// Locally, mutt is just a file reader: plain file rules decide, and the
// session account's own inbox is the default mailbox.
func TestMuttLocalMbox(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if err := w.DeliverLocal(pc, "world@neohome", "alex", "local read", "body here"); err != nil {
		t.Fatalf("seed delivery failed: %v", err)
	}
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("mutt")
	if !strings.Contains(out, "local read") {
		t.Fatalf("mutt with no argument must open the account's own inbox:\n%s", out)
	}
	out = sh("mutt -f /var/mail/alex 1")
	if !strings.Contains(out, "body here") || !strings.Contains(out, "world@neohome") {
		t.Fatalf("reading one local message failed:\n%s", out)
	}
	// a mailbox the account has no file rights to refuses like any file
	out = sh("mutt -f /var/mail/root")
	if !strings.Contains(out, "No such file or directory") && !strings.Contains(out, "INBOX is empty") {
		t.Fatalf("reading root's nonexistent mailbox must be honest about it:\n%s", out)
	}
}
