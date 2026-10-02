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
				data, ok := d.FS.Read("/etc/vsftpd.conf")
				return ok && contains(string(data), "anon_upload_enable=YES")
			},
			Effect: "ftp-access", Detection: 2,
			Help: "ftp -A host: upload a probe file, then LIST to see if writes stick.",
		},
		{
			ID: "ftp-cred-file", Name: "credentials readable in FTP-reachable home",
			Desc: "after initial ftp access, world-readable notes leak a real ssh password",
			Port: 21,
			Detect: func(d *Device, s *Service) bool {
				if s == nil || s.Name != "vsftpd" {
					return false
				}
				// precondition: someone already dropped creds marker by exploiting anon upload:
				// we model the chain inside the exploit handler instead; always "present"
				return true
			},
			Effect: "cred-leak:devops", Detection: 1,
			Help: "read /home/devops/deploy/notes.md via ftp.",
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
