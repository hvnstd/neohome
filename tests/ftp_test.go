package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// ftpExec drives a full client session through stdin, the way a live session
// types it: optional password first, then commands, then bye.
func ftpExec(t *testing.T, w *core.World, dev *core.Device, user, target string, lines ...string) string {
	t.Helper()
	return runWithStdin(t, w, dev, user, target, lines...)
}

// npcPublicIP is the neighbour's address as the internet sees it: the only way
// in is the port-forward on her router.
func npcPublicIP(w *core.World) string {
	return w.Devices["npc-router"].FirstWANIP()
}

// ---- the daemon and its configuration are real state ----

func TestFTPServerSeedsAreReal(t *testing.T) {
	w := core.NewWorld()
	npc := w.Devices["npc-pc"]

	svc, port := core.FTPDaemon(npc)
	if svc == nil || port != 21 || svc.State != "running" {
		t.Fatalf("the neighbour should run a real ftpd on 21, got %+v port %d", svc, port)
	}
	if svc.Handler != "npc-ftp" {
		t.Fatalf("unexpected handler %q", svc.Handler)
	}
	c := core.FTPConfOf(npc)
	if !c.Anonymous || !c.AnonUpload || c.AnonRoot != "/" {
		t.Fatalf("the seeded configuration is the attack surface: %+v", c)
	}
	// the anonymous account is a real account with no password and no shell
	acc := npc.FindUser("ftp")
	if acc == nil || acc.UID != 21 {
		t.Fatalf("an anonymous account must exist for the session to run as: %+v", acc)
	}
	if acc.CheckPassword("") || acc.CheckPassword("anything") {
		t.Fatal("the ftp service account must never authenticate")
	}
	// and the filesystem state the config points at
	if !npc.FS.Exists("/srv/ftp/pub") || !npc.FS.Exists("/home/devops/backup/accounts-2024.csv") {
		t.Fatal("the seeded drop directory and export must exist")
	}
}

func TestFTPConfigurationIsParsedNotShadowed(t *testing.T) {
	w := core.NewWorld()
	npc := w.Devices["npc-pc"]

	// editing the daemon's configuration changes what it permits, with no
	// restart and no cached copy anywhere: the file is the policy
	npc.FS.Write("/etc/vsftpd.conf", "anonymous_enable=NO\nlocal_enable=YES\nwrite_enable=NO\n", 0644, "root", "root")
	c := core.FTPConfOf(npc)
	if c.Anonymous || c.WriteEnable || c.AnonRoot != "" {
		t.Fatalf("the edited configuration was not honoured: %+v", c)
	}
	out := ftpExec(t, w, npc, "root", "ftp -A 127.0.0.1", "bye")
	if !strings.Contains(out, "530 Permission denied.") {
		t.Fatalf("anonymous must be refused once anonymous_enable=NO:\n%s", out)
	}
}

// ---- a session over the network, with real files on both ends ----

func TestFTPAnonymousSessionOverPortForward(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)

	out := ftpExec(t, w, pc, "alex", "ftp -A "+pub,
		"ls /srv/ftp/pub",
		"pwd",
		"get /home/devops/deploy/notes.md",
		"bye",
	)
	if !strings.Contains(out, "230 Login successful.") {
		t.Fatalf("an anonymous login must succeed while the config allows it:\n%s", out)
	}
	if !strings.Contains(out, "welcome.txt") {
		t.Fatalf("the listing must show the real drop directory:\n%s", out)
	}
	if !strings.Contains(out, "257 \"/\" is the current directory") {
		t.Fatalf("anon_root=/ is the seeded misconfiguration and must be visible:\n%s", out)
	}
	// the file really arrived on the PC, byte for byte
	data, ok := pc.FS.Read("/home/alex/notes.md")
	if !ok || !strings.Contains(string(data), "mara still uses her old uni password") {
		t.Fatalf("the fetched file is not on the PC:\n%q", data)
	}
	// and the server logged the session it served
	syslog, _ := npc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "anonymous login ok") ||
		!strings.Contains(string(syslog), "downloaded /home/devops/deploy/notes.md") {
		t.Fatalf("the daemon did not log the session:\n%s", syslog)
	}
}

func TestFTPAnonymousUploadIsRealAndLeavesEvidence(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)
	pc.FS.Write("/home/alex/cargo.txt", "delivered by ftp\n", 0644, "alex", "alex")

	events := len(w.Events)
	out := ftpExec(t, w, pc, "alex", "ftp -A "+pub,
		"put /home/alex/cargo.txt /srv/ftp/pub/cargo.txt",
		"bye",
	)
	if !strings.Contains(out, "226 Transfer complete.") {
		t.Fatalf("the upload should succeed into the world-writable drop:\n%s", out)
	}
	up, ok := npc.FS.Read("/srv/ftp/pub/cargo.txt")
	if !ok || string(up) != "delivered by ftp\n" {
		t.Fatalf("the uploaded file is not on the target:\n%q", up)
	}
	// the new file belongs to the anonymous account, as a real vsftpd writes it
	if n, _ := npc.FS.Get("/srv/ftp/pub/cargo.txt"); n.Owner != "ftp" {
		t.Fatalf("the anonymous upload should be owned by ftp, got %q", n.Owner)
	}
	// an anonymous write from the outside is alert-level world state: the
	// camera records it, the log names it, and it raises heat
	alerted := false
	for _, e := range w.Events[events:] {
		if strings.Contains(e.Message, "anonymous upload") {
			alerted = true
		}
	}
	if !alerted {
		t.Fatalf("the anonymous upload must reach the world event stream: %+v", w.Events)
	}
	syslog, _ := npc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "anonymous/ftp uploaded /srv/ftp/pub/cargo.txt") {
		t.Fatalf("the upload must be logged on the target:\n%s", syslog)
	}
	if w.Case == nil || w.Case.Heat < 3 {
		t.Fatalf("an anonymous upload should be evidence, heat = %v", w.Case)
	}
}

func TestFTPWritePolicyComesFromTheFile(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)
	pc.FS.Write("/home/alex/cargo.txt", "nope\n", 0644, "alex", "alex")

	// anon_upload_enable=NO: the login still works, the write does not
	npc.FS.Write("/etc/vsftpd.conf", "anonymous_enable=YES\nanon_root=/\nanon_upload_enable=NO\nlocal_enable=YES\nwrite_enable=YES\n", 0644, "root", "root")
	out := ftpExec(t, w, pc, "alex", "ftp -A "+pub, "put /home/alex/cargo.txt /srv/ftp/pub/cargo.txt", "bye")
	if !strings.Contains(out, "550 Permission denied.") {
		t.Fatalf("anon_upload_enable=NO must refuse the write:\n%s", out)
	}
	if npc.FS.Exists("/srv/ftp/pub/cargo.txt") {
		t.Fatal("a refused upload must not create the file")
	}
	syslog, _ := npc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "anon_upload_enable=NO") {
		t.Fatalf("the refusal must name its reason in the target's log:\n%s", syslog)
	}

	// and the daemon's OTHER gate is the server's filesystem: with writes
	// enabled the drop directory itself still has to allow it
	npc.FS.Write("/etc/vsftpd.conf", "anonymous_enable=YES\nanon_root=/\nanon_upload_enable=YES\nanon_mkdir_write_enable=YES\nlocal_enable=YES\nwrite_enable=YES\n", 0644, "root", "root")
	npc.FS.Write("/srv/ftp/pub/locked.txt", "mine\n", 0600, "root", "root")
	out = ftpExec(t, w, pc, "alex", "ftp -A "+pub, "put /home/alex/cargo.txt /srv/ftp/pub/locked.txt", "bye")
	if !strings.Contains(out, "553 Could not create file.") {
		t.Fatalf("a 0600 root-owned file must not be overwritten anonymously:\n%s", out)
	}
	if d, _ := npc.FS.Read("/srv/ftp/pub/locked.txt"); string(d) != "mine\n" {
		t.Fatal("the protected file was modified")
	}

	// a directory the anonymous account cannot write into refuses too
	npc.FS.MkdirAll("/srv/ftp/private", 0700, "root", "root")
	out = ftpExec(t, w, pc, "alex", "ftp -A "+pub, "put /home/alex/cargo.txt /srv/ftp/private/x.txt", "bye")
	if !strings.Contains(out, "553 Could not create file.") {
		t.Fatalf("a 0700 root directory must refuse an anonymous write:\n%s", out)
	}
	if npc.FS.Exists("/srv/ftp/private/x.txt") {
		t.Fatal("nothing may land in a directory the anonymous account cannot write")
	}
}

// ---- authentication and the permission boundary ----

func TestFTPAuthIsRealAndLeaksNothing(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)

	// the leaked-from-notes password really opens mara's account, and the
	// session can read her files because it IS her account
	out := ftpExec(t, w, pc, "alex", "ftp mara@"+pub, "hunter2",
		"get /home/mara/Desktop/passwords.kdbx", "bye")
	if !strings.Contains(out, "230 Login successful.") {
		t.Fatalf("mara's real password must work:\n%s", out)
	}
	if d, ok := pc.FS.Read("/home/alex/passwords.kdbx"); !ok || len(d) == 0 {
		t.Fatal("an authenticated session must be able to read the account's own files")
	}

	// a wrong password is refused identically to an unknown account
	before := pc.Fail2Ban[pub]
	out = ftpExec(t, w, pc, "alex", "ftp mara@"+pub, "wrongpass", "bye")
	if !strings.Contains(out, "530 Login incorrect.") {
		t.Fatalf("a wrong password must be refused:\n%s", out)
	}
	if strings.Contains(out, "no such user") || strings.Contains(out, "unknown account") {
		t.Fatalf("the failure must not reveal whether the account exists:\n%s", out)
	}
	syslog, _ := npc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "failed login for mara") {
		t.Fatalf("the failed login must be evidence on the target:\n%s", syslog)
	}
	if w.Case == nil || w.Case.Heat < 3 {
		t.Fatal("a failed login should raise heat")
	}
	_ = before

	// The name "ftp" IS the anonymous login in this protocol (real vsftpd
	// treats it the same way), so the empty-password rule has to be tested
	// against a real local account that has no stored password: root on this
	// image has none, and pressing enter must not produce a session — the same
	// rule ssh and sftp now apply.
	out = ftpExec(t, w, pc, "alex", "ftp root@"+pub, "", "bye")
	if strings.Contains(out, "230 Login successful.") {
		t.Fatalf("an account with no password must not authenticate:\n%s", out)
	}
	if !strings.Contains(out, "530 Login incorrect.") {
		t.Fatalf("the refusal must be the daemon's own:\n%s", out)
	}
}

// ---- the world's gates apply to FTP like to everything else ----

func TestFTPGatesFollowTheWorld(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)

	// (1) the seeded DNS fault must not matter for an IP, but a name must fail
	out := ftpExec(t, w, pc, "alex", "ftp darkden.home.invalid", "bye")
	if !strings.Contains(out, "Name or service not known") {
		t.Fatalf("an unresolvable name must fail at resolution:\n%s", out)
	}

	// (2) stopping the daemon closes the port
	npc.Svc("vsftpd").State = "stopped"
	out = ftpExec(t, w, pc, "alex", "ftp -A "+pub, "bye")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped daemon must refuse the connection:\n%s", out)
	}
	npc.Svc("vsftpd").State = "running"

	// (3) the owner closing the port-forward closes the hole: the same address
	// now filters, exactly as before the forward existed
	w.Devices["npc-router"].PortFwd[0].Enable = false
	out = ftpExec(t, w, pc, "alex", "ftp -A "+pub, "bye")
	if !strings.Contains(out, "Connection timed out (filtered)") {
		t.Fatalf("without the forward the host is unreachable:\n%s", out)
	}
	w.Devices["npc-router"].PortFwd[0].Enable = true

	// (4) a session does not outlive the daemon: the next command answers 421
	sess, err := w.FTPLogin(pc, "alex", npc, "anonymous", "anonymous@")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	npc.Svc("vsftpd").State = "stopped"
	if _, err := sess.List("/srv/ftp/pub"); err == nil || !strings.Contains(err.Error(), "421") {
		t.Fatalf("a dead daemon must close the control connection, got %v", err)
	}
	npc.Svc("vsftpd").State = "running"

	// (5) power is a gate like any other
	npc.MainsDropped = true
	out = ftpExec(t, w, pc, "alex", "ftp -A "+pub, "bye")
	if !strings.Contains(out, "timed out") {
		t.Fatalf("a dark host cannot accept a session:\n%s", out)
	}
	npc.MainsDropped = false
}

// ---- the player can run the same daemon ----

func TestPlayerRunsTheSameFTPServer(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	pkg := w.Repos["debian"].Pkgs["vsftpd"]
	if pkg == nil {
		t.Fatal("vsftpd must be installable from the repository")
	}
	nas.InstallPkg(pkg)
	if nas.FindUser("ftp") == nil {
		t.Fatal("the package must create the anonymous account")
	}
	// installing a package registers the unit, it does not start it
	out := ftpExec(t, w, pc, "alex", "ftp -A nas", "bye")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a freshly installed, stopped daemon must refuse:\n%s", out)
	}
	// starting it with the shipped configuration keeps anonymous access off:
	// secure by default, and the player decides what to expose
	if msg, err := nas.StartService("vsftpd"); err != nil {
		t.Fatalf("start vsftpd: %v (msg %q)", err, msg)
	}
	out = ftpExec(t, w, pc, "alex", "ftp -A nas", "bye")
	if !strings.Contains(out, "530 Permission denied.") {
		t.Fatalf("the packaged default must not serve anonymous sessions:\n%s", out)
	}

	// the player exposes a drop the way the real thing requires: turn the
	// feature on in the configuration AND make the directory writable for the
	// anonymous account — two real steps, both through real commands
	nas.FS.Write("/etc/vsftpd.conf", "listen=YES\nanonymous_enable=YES\nanon_upload_enable=YES\nlocal_enable=YES\nwrite_enable=YES\n", 0644, "root", "root")
	run(t, w, nas, "root", "chmod 777 /srv/ftp/pub")
	if n, ok := nas.FS.Get("/srv/ftp/pub"); !ok || n.Mode.Perm() != 0777 {
		t.Fatalf("the drop directory must be world-writable for an anonymous upload, got %v", n)
	}
	pc.FS.Write("/home/alex/local.txt", "to the nas\n", 0644, "alex", "alex")

	out = ftpExec(t, w, pc, "alex", "ftp -A nas",
		"put /home/alex/local.txt /srv/ftp/pub/local.txt",
		"bye")
	if !strings.Contains(out, "226 Transfer complete.") {
		t.Fatalf("the player's own FTP server must accept the upload:\n%s", out)
	}
	if d, ok := nas.FS.Read("/srv/ftp/pub/local.txt"); !ok || string(d) != "to the nas\n" {
		t.Fatalf("the upload did not land on the NAS:\n%q", d)
	}
	// and it is confined to the anonymous root: the package sets no anon_root,
	// so it is the ftp account's home (/srv/ftp) and nothing outside it is
	// reachable — while the account that really owns the file still can
	nas.FS.Write("/home/alex/secret.txt", "not for anonymous\n", 0644, "alex", "alex")
	out = ftpExec(t, w, pc, "alex", "ftp -A nas", "get /home/alex/secret.txt stolen.txt", "bye")
	if !strings.Contains(out, "550") || pc.FS.Exists("/home/alex/stolen.txt") {
		t.Fatalf("an anonymous session must not leave its root:\n%s", out)
	}
	out = ftpExec(t, w, pc, "alex", "ftp alex@nas", "alex123",
		"get /home/alex/secret.txt allowed.txt", "bye")
	if !strings.Contains(out, "226 Transfer complete.") {
		t.Fatalf("an authenticated local session sees the real filesystem:\n%s", out)
	}
	if d, ok := pc.FS.Read("/home/alex/allowed.txt"); !ok || string(d) != "not for anonymous\n" {
		t.Fatalf("the authenticated download did not arrive:\n%q", d)
	}
}

// ---- the exploit chain uses the protocol, not a shortcut ----

func TestFTPExploitChainRunsOverTheWire(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	pub := npcPublicIP(w)

	// recon names the port, the host behind it and what is wrong there
	out := run(t, w, pc, "alex", "recon "+pub)
	if !strings.Contains(out, "21/tcp -> forwarded to darkden") ||
		!strings.Contains(out, "vsftpd-anon-upload") ||
		!strings.Contains(out, "ftp-cred-file") {
		t.Fatalf("recon must follow the port-forward and name the real target:\n%s", out)
	}

	// the upload effect really uploads: a file appears on the target, owned by
	// the anonymous account, and the daemon logged it
	out = run(t, w, pc, "alex", "exploit "+pub+" vsftpd-anon-upload")
	if !strings.Contains(out, "anonymous write accepted") {
		t.Fatalf("the upload effect must go through the protocol:\n%s", out)
	}
	probe, ok := npc.FS.Read("/srv/ftp/pub/.probe")
	if !ok || !strings.Contains(string(probe), "from home-pc") {
		t.Fatalf("the probe must be a real file on the target:\n%q", probe)
	}

	// the credential effect reads the file over FTP and verifies the credential
	out = run(t, w, pc, "alex", "exploit "+pub+" ftp-cred-file")
	if !strings.Contains(out, "fetched /home/devops/backup/accounts-2024.csv") ||
		!strings.Contains(out, "credential recovered and verified: mara:hunter2") {
		t.Fatalf("the credential effect must fetch and verify:\n%s", out)
	}
	if d, ok := pc.FS.Read("/tmp/accounts-2024.csv"); !ok || !strings.Contains(string(d), "mara:hunter2") {
		t.Fatal("the retrieved export must be kept on the attacker's machine")
	}

	// and the same chain fails once the owner closes the hole
	w.Devices["npc-router"].PortFwd[0].Enable = false
	out = run(t, w, pc, "alex", "exploit "+pub+" vsftpd-anon-upload")
	if !strings.Contains(out, "precondition failed") && !strings.Contains(out, "failed") {
		t.Fatalf("a closed forward must stop the exploit:\n%s", out)
	}
}

// ---- persistence: nothing about FTP is invented at load time ----

func TestFTPSurvivesSaveLoad(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	pub := npcPublicIP(w)
	if _, err := w.FTPLogin(pc, "alex", w.Devices["npc-pc"], "anonymous", "anonymous@"); err != nil {
		t.Fatalf("login before save: %v", err)
	}

	path := t.TempDir() + "/ftp_world.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc2 := w2.Devices["pc-alex"]
	npc2 := w2.Devices["npc-pc"]
	if npc2.FindUser("ftp") == nil {
		t.Fatal("the anonymous account must survive the round trip")
	}
	if c := core.FTPConfOf(npc2); !c.Anonymous || c.AnonRoot != "/" {
		t.Fatalf("the daemon's configuration must survive: %+v", c)
	}
	if d, ok := npc2.FS.Read("/home/devops/backup/accounts-2024.csv"); !ok || !strings.Contains(string(d), "mara:hunter2") {
		t.Fatal("the leaked export must survive the round trip")
	}
	out := ftpExec(t, w2, pc2, "alex", "ftp -A "+pub, "get /home/devops/deploy/notes.md", "bye")
	if !strings.Contains(out, "226 Transfer complete.") {
		t.Fatalf("a session must still work after a load:\n%s", out)
	}
}
