package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// sftp drives the whole session through stdin: password first, then
// commands, then quit — the same stream a live session types.
func sftpExec(t *testing.T, w *core.World, dev *core.Device, user, target string, lines ...string) string {
	t.Helper()
	return runWithStdin(t, w, dev, user, target, lines...)
}

func TestSFTPTransferOverNetwork(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	// a local file to upload, then a full session: auth, list, get, put, quit
	pc.FS.Write("/home/alex/tosend.txt", "local cargo\n", 0644, "alex", "alex")
	out := sftpExec(t, w, pc, "alex", "sftp alex@nas",
		"alex123",
		"ls /srv/data",
		"get /srv/data/photos/vacation.txt",
		"put /home/alex/tosend.txt",
		"quit",
	)
	if !strings.Contains(out, "vacation.txt") || !strings.Contains(out, "backups.tar.gz") {
		t.Fatalf("remote listing does not show the real NAS files:\n%s", out)
	}

	// the fetched file is really on the PC, byte for byte
	data, ok := pc.FS.Read("/home/alex/vacation.txt")
	if !ok || string(data) != "42 photos (placeholder)\n" {
		t.Fatalf("the fetched file is not on the PC:\n%q", data)
	}
	// the uploaded file is really on the NAS, owned by the remote account
	up, ok := nas.FS.Read("/home/alex/tosend.txt")
	if !ok || string(up) != "local cargo\n" {
		t.Fatalf("the uploaded file is not on the NAS:\n%q", up)
	}
	node, _ := nas.FS.Get("/home/alex/tosend.txt")
	if node.Owner != "alex" {
		t.Fatalf("the upload is owned by %q, not the remote account", node.Owner)
	}

	// the session and its transfers are evidence on the server
	syslog, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "sftp session opened for alex") ||
		!strings.Contains(string(syslog), "fetched /srv/data/photos/vacation.txt") ||
		!strings.Contains(string(syslog), "stored /home/alex/tosend.txt") {
		t.Fatalf("the NAS did not log the sftp session:\n%s", syslog)
	}
}

func TestSFTPAuthIsReal(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := pc.Fail2Ban["10.77.1.30"]

	out := sftpExec(t, w, pc, "alex", "sftp alex@nas", "wrongpass", "quit")
	if !strings.Contains(out, "Permission denied, please try again.") {
		t.Fatalf("a wrong password must be refused:\n%s", out)
	}
	if pc.Fail2Ban["10.77.1.30"] != before+1 {
		t.Fatal("the failed sftp login did not feed fail2ban")
	}
	// an account that does not exist on the NAS is refused identically
	out = sftpExec(t, w, pc, "alex", "sftp ghost@nas", "whatever", "quit")
	if !strings.Contains(out, "Permission denied, please try again.") {
		t.Fatalf("an unknown account must be refused identically:\n%s", out)
	}

	// four bad attempts trip the ban: the next connection times out
	for i := 0; i < 3; i++ {
		sftpExec(t, w, pc, "alex", "sftp alex@nas", "wrongpass", "quit")
	}
	out = sftpExec(t, w, pc, "alex", "sftp alex@nas", "alex123", "quit")
	if !strings.Contains(out, "Connection timed out") {
		t.Fatalf("fail2ban did not close the door:\n%s", out)
	}
}

func TestSFTPPermissionsAreHonest(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// the credential store is not fetchable by an ordinary account
	out := sftpExec(t, w, pc, "alex", "sftp alex@nas",
		"alex123",
		"get /etc/shadow",
		"quit",
	)
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("fetching /etc/shadow must be refused:\n%s", out)
	}
	if _, exists := pc.FS.Read("/home/alex/shadow"); exists {
		t.Fatal("the denied fetch left the file behind anyway")
	}

	// a file the remote account cannot write refuses the upload
	out = sftpExec(t, w, pc, "alex", "sftp alex@nas",
		"alex123",
		"put /etc/hostname /root/stolen",
		"quit",
	)
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("an upload into /root must be refused:\n%s", out)
	}

	// a missing remote file is a missing file, not an empty copy
	out = sftpExec(t, w, pc, "alex", "sftp alex@nas",
		"alex123",
		"get /srv/data/no-such-file",
		"quit",
	)
	if !strings.Contains(out, "no such file or directory") {
		t.Fatalf("a missing file must be reported:\n%s", out)
	}
}

func TestSCPDoesRealRemoteCopy(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	pc.FS.Write("/home/alex/send.txt", "scp cargo\n", 0644, "alex", "alex")

	g := newGSession(t, w, pc, "alex")
	g.must("-> /home/alex/scp-vacation.txt",
		g.exec("scp alex@nas:/srv/data/photos/vacation.txt /home/alex/scp-vacation.txt", "alex123"),
		"scp fetch failed")
	data, ok := pc.FS.Read("/home/alex/scp-vacation.txt")
	if !ok || string(data) != "42 photos (placeholder)\n" {
		t.Fatalf("scp did not really fetch the file:\n%q", data)
	}

	g.must("/home/alex/send.txt -> /srv/data/send.txt",
		g.exec("scp /home/alex/send.txt alex@nas:/srv/data/", "alex123"),
		"scp upload failed")
	up, ok := nas.FS.Read("/srv/data/send.txt")
	if !ok || string(up) != "scp cargo\n" {
		t.Fatalf("scp did not really upload the file:\n%q", up)
	}

	// local-to-local stays plain cp; two remotes are refused
	g.must("scp cargo", g.exec("cp /home/alex/send.txt /home/alex/copy.txt && cat /home/alex/copy.txt"), "local cp broke")
	out := g.exec("scp alex@nas:/a/x alex@nas:/b/y", "alex123")
	if !strings.Contains(out, "not supported") {
		t.Fatalf("remote-to-remote must be refused:\n%s", out)
	}
}

func TestSFTPGatedByServiceState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	if _, err := nas.StopService("sshd"); err != nil {
		t.Fatalf("stopping sshd failed: %v", err)
	}
	out := sftpExec(t, w, pc, "alex", "sftp alex@nas", "alex123", "quit")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped sshd must refuse the sftp session:\n%s", out)
	}
	if _, err := nas.StartService("sshd"); err != nil {
		t.Fatalf("starting sshd failed: %v", err)
	}
	out = sftpExec(t, w, pc, "alex", "sftp alex@nas", "alex123", "pwd", "quit")
	if !strings.Contains(out, "Remote working directory: /home/alex") {
		t.Fatalf("sftp did not come back with sshd:\n%s", out)
	}
}
