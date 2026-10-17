package core

// Vulnerabilities are WORLD STATE, not payloads. A vuln exists when real
// conditions hold (version + config + exposure). Exploiting = the engine
// applying the declared effect to state. No code execution ever crosses the
// host boundary.

type Vuln struct {
	ID        string
	Name      string
	Desc      string
	Port      int
	Detect    func(d *Device, s *Service) bool // honest precondition check
	Effect    string                           // key: what the engine does on success
	File      string                           // the path an effect reads over the wire
	Detection int                              // 0..5 how noisy (IDS/log likelihood)
	Help      string                           // hint text
}

func Vulns() []Vuln {
	return []Vuln{
		{
			ID: "vsftpd-anon-upload", Name: "vsftpd anonymous upload + weak local auth",
			Desc: "vsftpd allows anonymous write and local users reuse leaked passwords (file /etc/vsftpd.conf says anon_upload_enable=YES)",
			Port: 21,
			Detect: func(d *Device, s *Service) bool {
				if s == nil || s.Name != "vsftpd" || s.State != "running" {
					return false
				}
				c := FTPConfOf(d)
				if !c.Anonymous || !c.AnonUpload {
					return false
				}
				// the anonymous account and its root must really exist: the
				// upload this vuln claims is one the protocol can perform
				_, _, err := ftpAnonAccount(d, c)
				return err == nil
			},
			Effect: "ftp-access:/srv/ftp/pub/.probe", Detection: 2,
			Help: "ftp -A host: upload a probe file, then LIST to see if writes stick.",
		},
		{
			ID: "ftp-cred-file", Name: "account export readable through anonymous FTP",
			Desc: "with anonymous access, the migration export under /home/devops is world-readable and holds real passwords",
			Port: 21,
			Detect: func(d *Device, s *Service) bool {
				if s == nil || s.Name != "vsftpd" || s.State != "running" {
					return false
				}
				c := FTPConfOf(d)
				if !c.Anonymous {
					return false
				}
				acc, root, err := ftpAnonAccount(d, c)
				if err != nil {
					return false
				}
				// the file must be inside the anonymous root AND readable as the
				// anonymous account: anon_root=/srv/ftp hides it, anon_root=/
				// exposes it, and the mode bits decide the rest
				const f = "/home/devops/backup/accounts-2024.csv"
				if root != "/" && !hasPathPrefix(f, root) {
					return false
				}
				data, exists, allowed := d.FS.ReadPathAs(f, acc)
				return exists && allowed && len(data) > 0
			},
			Effect: "cred-leak:mara", File: "/home/devops/backup/accounts-2024.csv", Detection: 1,
			Help: "after anonymous access, `get /home/devops/backup/accounts-2024.csv` — it predates the rotation.",
		},
		{
			ID: "dropbear-default-pass", Name: "router ssh with vendor default password",
			Desc: "dropbear accepts root/admin unless the owner changed it",
			Port: 22,
			Detect: func(d *Device, s *Service) bool {
				u := d.FindUser("root")
				return s != nil && s.Name == "dropbear" && s.State == "running" && u != nil && u.Pass == "admin"
			},
			Effect: "root-shell", Detection: 3,
			Help: "ssh root@host — password admin. This is how you got in if the LAN is yours.",
		},

		// ---- household exposure beyond the FTP drop --------------------------
		//
		// Each entry names the box whose seed really holds the condition (with
		// the file it was read from) and checks the condition against that
		// box's live state, so `recon` never reports a vulnerability the world
		// does not have and repairing the condition removes it.

		{
			ID: "router-telnet-default", Name: "router telnetd enabled with vendor default login",
			Desc: "the legacy BusyBox telnetd on 23 is listening and root still uses the vendor default password the stock firmware ships",
			Port: 23,
			Detect: func(d *Device, s *Service) bool {
				// router-alex runs telnetd on 23 (world_init.go:412) and its
				// root account keeps the stock password admin (world_init.go:173)
				u := d.FindUser("root")
				return s != nil && s.Name == "telnetd" && s.State == "running" && u != nil && u.Pass == "admin"
			},
			Effect: "root-shell", Detection: 3,
			Help: "telnet gateway — root/admin over the cleartext legacy daemon.",
		},
		{
			ID: "nas-smb-guest-share", Name: "SMB guest share exposes household files",
			Desc: "smbd serves the data share with `guest ok = yes`, so any LAN host reads the household backup without credentials",
			Port: 445,
			Detect: func(d *Device, s *Service) bool {
				// nas-alex runs smbd (world_init.go:418) and its [data] share is
				// guest-readable per the smb.conf seed (devseed.go:162-166)
				if d == nil || d.W == nil || s == nil || s.Name != "smbd" || s.State != "running" {
					return false
				}
				sh, err := d.W.SMBShare(d, "data")
				if err != nil || !sh.GuestOK || sh.Path == "" {
					return false
				}
				const f = "/srv/data/backups.tar.gz"
				if !hasPathPrefix(f, sh.Path) {
					return false
				}
				// a guest session runs as nobody; the file must really be
				// readable as that account for the share to expose it
				acc := d.FindUser("nobody")
				if acc == nil {
					return false
				}
				_, exists, allowed := d.FS.ReadPathAs(f, acc)
				return exists && allowed
			},
			Effect: "smb-access:data", File: "/srv/data/backups.tar.gz", Detection: 1,
			Help: "smbclient -L nas, then read //nas/data — the data share takes guests.",
		},
		{
			ID: "guest-weak-account", Name: "ssh account whose password is its own name",
			Desc: "a non-root shell account (guest/guest) is reachable over ssh, so the login is guessable without even a wordlist",
			Port: 22,
			Detect: func(d *Device, s *Service) bool {
				// laptop-alex seeds guest/guest (world_init.go:245-247) with
				// dropbear running (world_init.go:251); the PC carries the same
				// account. Any box with such an account and a live ssh daemon
				// really exposes it.
				if s == nil || s.State != "running" || s.Handler != "ssh" {
					return false
				}
				for name, u := range d.Users {
					if u.UID == 0 || u.Pass == "" {
						continue
					}
					if u.Pass == name {
						return true
					}
				}
				return false
			},
			Effect: "user-shell:guest", Detection: 3,
			Help: "ssh guest@host — the password is the account name.",
		},
		{
			ID: "printer-cups-noauth", Name: "CUPS accepts jobs from the whole LAN",
			Desc: "cupsd's configuration allows every host on the LAN to submit print jobs with no authentication",
			Port: 631,
			Detect: func(d *Device, s *Service) bool {
				// prn-alex runs cupsd (world_init.go:264) with an
				// unauthenticated LAN Allow stanza (devseed.go:199-201)
				if s == nil || s.Name != "cupsd" || s.State != "running" || d.Printer == nil {
					return false
				}
				data, ok := d.FS.Read("/etc/cups/cupsd.conf")
				if !ok {
					return false
				}
				conf := string(data)
				// the file is the policy: it must allow the household subnet
				// and it must not require an authenticated user
				return contains(conf, LANSubnet) && !contains(conf, "Require user")
			},
			Effect: "cups-print:unauthorized page", Detection: 2,
			Help: "any LAN host can lp to the printer — CUPS allows the whole subnet with no login.",
		},
		{
			ID: "bazaar-anon-drop", Name: "public dead-drop FTP accepts anonymous writes",
			Desc: "the market host's vsftpd serves an anonymous, world-writable drop box reachable from the whole internet",
			Port: 21,
			Detect: func(d *Device, s *Service) bool {
				// bazaar is seeded with a public vsftpd on 21 whose anon_root
				// is a real drop directory (market.go:396-407). The NPC box's
				// anon_root=/ case is the separate vsftpd-anon-upload entry, so
				// these two never describe the same daemon.
				if d == nil || d.W == nil || s == nil || s.Name != "vsftpd" || s.State != "running" {
					return false
				}
				c := FTPConfOf(d)
				if !c.Anonymous || !c.AnonUpload || c.AnonRoot == "" || c.AnonRoot == "/" {
					return false
				}
				if d.WANIP() == "" {
					return false
				}
				_, _, err := ftpAnonAccount(d, c)
				return err == nil
			},
			Effect: "ftp-access:/srv/bazaar/drops/.probe", Detection: 2,
			Help: "ftp -A bazaar.neohome.example — anonymous and writable; anything dropped is public.",
		},
	}
}

// hasPathPrefix reports whether p is inside dir (or is dir itself).
func hasPathPrefix(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || len(p) > len(dir) && p[:len(dir)+1] == dir+"/"
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
