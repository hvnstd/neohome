package core

// Virtual package ecosystem. `apt install nginx` creates files, users,
// configs, a registered service and a running process — real consequences,
// no real binaries. Binaries are "virtual": they exist as inodes flagged
// Binary so ls/file/hash behave, but running them dispatches to builtins.

func BuildMainRepo() *Repo {
	r := &Repo{Name: "main", URL: "http://mirror.neohome.example/debian", DeviceID: "mirror",
		Distro: "debian", Comps: []string{"main"}, Signed: true, Status: "SYNCED", Pkgs: map[string]*VPkg{}}
	add := func(p *VPkg) { r.Pkgs[p.Name] = p }

	add(&VPkg{Name: "curl", Version: "8.9.1-2", Arch: "amd64", Desc: "command line URL transfer tool", Size: 412,
		Files: map[string]*PkgFile{"/usr/bin/curl": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	add(&VPkg{Name: "htop", Version: "3.3.0-4", Arch: "amd64", Desc: "interactive process viewer", Size: 220,
		Files: map[string]*PkgFile{"/usr/bin/htop": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	add(&VPkg{Name: "nginx", Version: "1.26.2-1", Arch: "amd64", Desc: "high performance web server", Size: 1240,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/nginx":                   {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"},
			"/etc/nginx/nginx.conf":             {Content: "user www-data;\nworker_processes auto;\nhttp {\n  include /etc/nginx/sites-enabled/*;\n}\n", Mode: 0644, Owner: "root", Group: "root"},
			"/etc/nginx/sites-enabled/default":  {Content: "server {\n  listen 80 default_server;\n  root /var/www/html;\n}\n", Mode: 0644, Owner: "root", Group: "root"},
			"/etc/systemd/system/nginx.service": {Content: "[Unit]\nDescription=A high performance web server\nAfter=network.target\n[Service]\nExecStart=/usr/sbin/nginx\nExecReload=/usr/sbin/nginx -s reload\n[Install]\nWantedBy=multi-user.target\n", Mode: 0644, Owner: "root", Group: "root"},
			"/var/www/html/index.html":          {Content: "<html><body><h1>Welcome to nginx!</h1></body></html>\n", Mode: 0644, Owner: "root", Group: "www-data"},
			"/etc/passwd.d/nginx-wwwdata":       {Content: "www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\n", Mode: 0644, Owner: "root", Group: "root"},
		},
		Service: &SvcSpec{Name: "nginx", Desc: "A high performance web server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/sites-enabled/default", Autostart: true}})
	add(&VPkg{Name: "openssh-server", Version: "1:9.7p1-3", Arch: "amd64", Desc: "secure shell daemon", Size: 890,
		Files: map[string]*PkgFile{
			"/usr/sbin/sshd":                   {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"},
			"/etc/ssh/sshd_config":             {Content: "Port 22\nPermitRootLogin prohibit-password\nPasswordAuthentication yes\n", Mode: 0644, Owner: "root", Group: "root"},
			"/etc/systemd/system/sshd.service": {Content: "[Unit]\nDescription=OpenBSD Secure Shell server\n[Service]\nExecStart=/usr/sbin/sshd\n[Install]\nWantedBy=multi-user.target\n", Mode: 0644, Owner: "root", Group: "root"},
		},
		Service: &SvcSpec{Name: "sshd", Desc: "OpenBSD Secure Shell server", Port: 22, Proto: "tcp", Scope: "lan",
			Handler: "ssh", Autostart: true}})
	add(&VPkg{Name: "vsftpd", Version: "3.0.5-1", Arch: "amd64", Desc: "the very secure FTP daemon", Size: 190,
		Files: map[string]*PkgFile{"/usr/sbin/vsftpd": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"},
			"/etc/vsftpd.conf": {Content: "listen=YES\nanonymous_enable=NO\nlocal_enable=YES\nwrite_enable=YES\n", Mode: 0644, Owner: "root", Group: "root"}},
		Service: &SvcSpec{Name: "vsftpd", Desc: "FTP daemon", Port: 21, Proto: "tcp", Scope: "lan", Handler: "ftp-user", Autostart: false}})
	add(&VPkg{Name: "ircd", Version: "1.2.3-1", Arch: "amd64", Desc: "small IRC daemon", Size: 160,
		Files:   map[string]*PkgFile{"/usr/sbin/ircd": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}},
		Service: &SvcSpec{Name: "ircd", Desc: "IRC daemon", Port: 6667, Proto: "tcp", Scope: "any", Handler: "irc-self", Autostart: false}})
	add(&VPkg{Name: "bindutils", Version: "9.18-28", Arch: "amd64", Desc: "DNS utilities (dig, nslookup)", Size: 60,
		Files: map[string]*PkgFile{"/usr/bin/dig": {Content: "", Mode: 0755, Binary: true}, "/usr/bin/nslookup": {Content: "", Mode: 0755, Binary: true}}})
	add(&VPkg{Name: "libc", Version: "2.39-7", Arch: "amd64", Desc: "GNU C library (meta)", Size: 5000,
		Files: map[string]*PkgFile{"/usr/lib/x86_64-linux-gnu/libc.so.6": {Content: "", Mode: 0644, Binary: true}}})
	add(&VPkg{Name: "busybox", Version: "1.36.1-3", Arch: "amd64", Desc: "Tiny tools in one binary (virtual)", Size: 96,
		Files: map[string]*PkgFile{"/bin/busybox": {Content: "", Mode: 0755, Binary: true}}})
	add(&VPkg{Name: "dnsutils", Version: "11.7-2", Arch: "amd64", Desc: "network DNS client tools", Size: 80,
		Files: map[string]*PkgFile{"/usr/bin/dig": {Content: "", Mode: 0755, Binary: true}}})
	add(&VPkg{Name: "traceroute", Version: "2.1.6-1", Arch: "amd64", Desc: "path MTU discovery and traceroute", Size: 44,
		Files: map[string]*PkgFile{"/usr/bin/traceroute": {Content: "", Mode: 0755, Binary: true}}})
	add(&VPkg{Name: "python3", Version: "3.12.5-3", Arch: "amd64", Desc: "interpreted high-level language (game-VM)", Size: 21000,
		Files:    map[string]*PkgFile{"/usr/bin/python3": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}},
		PostInst: "registers the game bytecode runner for .py files (sandboxed, capability API)"})
	add(&VPkg{Name: "opensmtpd", Version: "6.8.2p1-1", Arch: "amd64", Desc: "small SMTP daemon (mail transport agent)", Size: 340,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/smtpd":                       {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"},
			"/etc/mail/smtpd.conf":                  {Content: "listen on lo port 25\nlisten on eth0 port 25\n\naction \"local\" mbox\naction \"relay\" relay\n\nmatch from any for local\n", Mode: 0644, Owner: "root", Group: "root"},
			"/etc/mail/aliases":                     {Content: "root: alex\npostmaster: alex\nabuse: alex\n", Mode: 0644, Owner: "root", Group: "root"},
			"/etc/systemd/system/opensmtpd.service": {Content: "[Unit]\nDescription=OpenSMTPD mail transfer agent\nAfter=network.target\n[Service]\nExecStart=/usr/sbin/smtpd\n[Install]\nWantedBy=multi-user.target\n", Mode: 0644, Owner: "root", Group: "root"},
		},
		Service: &SvcSpec{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp", Scope: "lan",
			Handler: "smtpd", Conf: "/etc/mail/smtpd.conf", Autostart: false}})
	return r
}

func BuildContribRepo() *Repo {
	r := &Repo{Name: "contrib", URL: "http://mirror.neohome.example/debian", DeviceID: "mirror",
		Distro: "debian", Comps: []string{"contrib"}, Signed: true, Status: "SYNCED", Pkgs: map[string]*VPkg{}}
	r.Pkgs["nmap"] = &VPkg{Name: "nmap", Version: "7.94+dfsg-2", Arch: "amd64", Desc: "network exploration / port scanner (world-aware)", Size: 3800,
		Files: map[string]*PkgFile{"/usr/bin/nmap": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}}}
	r.Pkgs["hydra"] = &VPkg{Name: "hydra", Version: "9.5-1", Arch: "amd64", Desc: "login breaker (slow, noisy, world-aware)", Size: 420,
		Files: map[string]*PkgFile{"/usr/bin/hydra": {Content: "", Mode: 0755, Binary: true, Owner: "root", Group: "root"}}}
	return r
}
