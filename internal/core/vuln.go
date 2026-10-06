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
