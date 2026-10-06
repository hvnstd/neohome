package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The world's package catalogue (§9 软件包系统).
//
// Everything here is a *definition*: name, version, dependencies, files,
// service, scripts. The installable form of a package is the payload file
// rendered from this definition (pkgfiles.go) and served by a repository
// (pkgnet.go); installing reads those files back. That indirection is the
// point — a package manager installs what the mirror serves, not what a Go
// map happens to contain.
//
// The catalogue is per distribution: Alpine's nginx depends on pcre and zlib
// and lives in /etc/nginx/http.d, Arch's depends on pcre2 and lives in
// /etc/nginx, OpenWrt's services are uhttpd with procd init scripts. Same
// world, same network, different distributions (§10).

// ---- file helpers ----

func binFile(path string) *PkgFile {
	return &PkgFile{Mode: 0755, Owner: "root", Group: "root", Binary: true}
}

func textFile(content string) *PkgFile {
	return &PkgFile{Content: content, Mode: 0644, Owner: "root", Group: "root"}
}

func cfgFile(path, content string) *PkgFile {
	return &PkgFile{Content: content, Mode: 0644, Owner: "root", Group: "root"}
}

// unitFile renders an init unit in the style of the distribution's manager.
// Debian and Ubuntu get systemd units, Alpine an OpenRC script, Arch a
// systemd unit, Fedora a systemd unit, OpenWrt a procd init script.
func unitFile(spec *DistroSpec, name, desc, exec string) (path, body string) {
	switch spec.Family {
	case FamilyAPK:
		return "/etc/init.d/" + name, "#!/sbin/openrc-run\n" +
			"name=\"" + name + "\"\ndescription=\"" + desc + "\"\ncommand=\"" + exec + "\"\ncommand_background=true\npidfile=\"/run/" + name + ".pid\"\n"
	case FamilyOpkg:
		return "/etc/init.d/" + name, "#!/bin/sh /etc/rc.common\n" +
			"START=95\nSTOP=10\nUSE_PROCD=1\n\nstart_service() {\n\tprocd_open_instance\n\tprocd_set_param command " + exec + "\n\tprocd_set_param respawn\n\tprocd_close_instance\n}\n"
	case FamilyPacman:
		return "/usr/lib/systemd/system/" + name + ".service", "[Unit]\nDescription=" + desc + "\nAfter=network.target\n[Service]\nExecStart=" + exec + "\n[Install]\nWantedBy=multi-user.target\n"
	default:
		return "/etc/systemd/system/" + name + ".service", "[Unit]\nDescription=" + desc + "\nAfter=network.target\n[Service]\nExecStart=" + exec + "\n[Install]\nWantedBy=multi-user.target\n"
	}
}

// ---- the repositories ----

// ArchiveKey is the world's package signing key. Repositories published by
// the archive are signed with it; every distribution's keyring installs with
// it. A third-party repository uses its own key, which is exactly why adding
// one changes the risk (§11).
const (
	ArchiveKeyFP         = "9F2C1A4B77E3D05C8A1B2F6E4D9C0A3B5E7F1234"
	ArchiveKeyOwner      = "NeoHome Packages Archive Signing Key"
	ArchiveKeyValidUntil = "2028-01-01"

	ThirdPartyKeyFP         = "A11CE0F14B2D88C3E5F70912AB34CD56EF7890AB"
	ThirdPartyKeyOwner      = "Sashimi CDN Package Key"
	ThirdPartyKeyValidUntil = "2027-06-30"
)

// KeyFile renders a keyring entry the way the world stores keys: a real file
// with the signer's identity and its validity, readable and editable.
func KeyFile(fingerprint, owner, validUntil, comment string) string {
	grouped := groupFingerprint(fingerprint)
	return "-----BEGIN PGP PUBLIC KEY BLOCK-----\n" +
		"Fingerprint: " + grouped + "\n" +
		"Owner: " + owner + "\n" +
		"Valid-Until: " + validUntil + "\n" +
		"Comment: " + comment + "\n" +
		"-----END PGP PUBLIC KEY BLOCK-----\n"
}

func groupFingerprint(fp string) string {
	fp = keyFingerprint(fp)
	var groups []string
	for len(fp) > 4 {
		groups = append(groups, fp[:4])
		fp = fp[4:]
	}
	if fp != "" {
		groups = append(groups, fp)
	}
	return strings.Join(groups, " ")
}

// BuildRepos is the world's catalogue: every distribution the spec names, each
// served from the same physical mirror host under its own path.
func BuildRepos() map[string]*Repo {
	repos := map[string]*Repo{}
	add := func(r *Repo) { repos[r.Name] = r }

	add(debianRepo())
	add(ubuntuRepo())
	add(alpineRepo())
	add(archRepo())
	add(fedoraRepo())
	add(openwrtRepo())
	add(sashimiRepo())
	return repos
}

func newRepo(name, distro, path, suite string, aliases, comps []string) *Repo {
	return &Repo{
		Name: name, Distro: distro, Path: path, Suite: suite,
		SuiteAliases: aliases, Comps: comps,
		URL:    "mirror.neohome.example/" + path,
		Signed: true, Status: "SYNCED",
		SignKey: ArchiveKeyFP, Pkgs: map[string]*VPkg{},
		ReleaseHash: map[string]string{},
	}
}

func (r *Repo) put(p *VPkg) { r.Pkgs[p.Name] = p }

func debianRepo() *Repo {
	r := newRepo("debian", "debian", "debian", "stable", []string{"bookworm"}, []string{"main", "contrib"})
	r.put(&VPkg{Name: "libc", Version: "2.39-7", Comp: "main", Desc: "GNU C library (meta)", Size: 5000,
		Files: map[string]*PkgFile{"/usr/lib/x86_64-linux-gnu/libc.so.6": {Mode: 0644, Owner: "root", Group: "root", Binary: true}}})
	r.put(&VPkg{Name: "curl", Version: "8.9.1-2", Comp: "main", Desc: "command line URL transfer tool", Size: 412,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "wget", Version: "1.24.5-1", Comp: "main", Desc: "retrieves files from the web", Size: 980,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/wget": binFile("/usr/bin/wget")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-4", Comp: "main", Desc: "interactive process viewer", Size: 220,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	r.put(&VPkg{Name: "nano", Version: "7.2-1", Comp: "main", Desc: "small, friendly text editor", Size: 280,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{"/usr/bin/nano": binFile("/usr/bin/nano"),
			"/etc/nanorc": cfgFile("/etc/nanorc", "set autoindent\nset linenumbers\n")}})
	r.put(&VPkg{Name: "rsync", Version: "3.2.7-1", Comp: "main", Desc: "fast, versatile file copier", Size: 640,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/rsync": binFile("/usr/bin/rsync")}})
	r.put(&VPkg{Name: "tcpdump", Version: "4.99.4-4", Comp: "main", Desc: "command-line packet analyser", Size: 1200,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/tcpdump": binFile("/usr/bin/tcpdump")}})

	unit, unitBody := unitFile(distroSpecs["debian"], "nginx", "A high performance web server", "/usr/sbin/nginx")
	r.put(&VPkg{Name: "nginx", Version: "1.26.2-1", Comp: "main", Desc: "high performance web server", Size: 1240,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/nginx":                  binFile("/usr/sbin/nginx"),
			"/etc/nginx/nginx.conf":            cfgFile("/etc/nginx/nginx.conf", "user www-data;\nworker_processes auto;\nhttp {\n  include /etc/nginx/sites-enabled/*;\n}\n"),
			"/etc/nginx/sites-enabled/default": cfgFile("/etc/nginx/sites-enabled/default", "server {\n  listen 80 default_server;\n  root /var/www/html;\n}\n"),
			unit:                               cfgFile(unit, unitBody),
			"/var/www/html/index.html":         cfgFile("/var/www/html/index.html", "<html><body><h1>Welcome to nginx!</h1></body></html>\n"),
			"/etc/passwd.d/nginx-wwwdata":      cfgFile("/etc/passwd.d/nginx-wwwdata", "www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\n"),
		},
		Service: &SvcSpec{Name: "nginx", Desc: "A high performance web server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/sites-enabled/default", Autostart: true}})

	unit, unitBody = unitFile(distroSpecs["debian"], "sshd", "OpenBSD Secure Shell server", "/usr/sbin/sshd")
	r.put(&VPkg{Name: "openssh-server", Version: "1:9.7p1-3", Comp: "main", Desc: "secure shell daemon", Size: 890,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/sshd": binFile("/usr/sbin/sshd"),
			"/etc/ssh/sshd_config": cfgFile("/etc/ssh/sshd_config",
				"Port 22\nPermitRootLogin prohibit-password\nPasswordAuthentication yes\n"),
			unit: cfgFile(unit, unitBody),
		},
		Service: &SvcSpec{Name: "sshd", Desc: "OpenBSD Secure Shell server", Port: 22, Proto: "tcp", Scope: "lan",
			Handler: "ssh", Autostart: true}})

	unit, unitBody = unitFile(distroSpecs["debian"], "vsftpd", "FTP daemon", "/usr/sbin/vsftpd")
	r.put(&VPkg{Name: "vsftpd", Version: "3.0.5-1", Comp: "main", Desc: "the very secure FTP daemon", Size: 190,
		Files: map[string]*PkgFile{
			"/usr/sbin/vsftpd": binFile("/usr/sbin/vsftpd"),
			// Debian's default: local logins only, anonymous off, writes off.
			// What a player exposes later is their own configuration, and the
			// FTP server they run is the same one they can attack elsewhere.
			"/etc/vsftpd.conf": cfgFile("/etc/vsftpd.conf", "listen=YES\nanonymous_enable=NO\nlocal_enable=YES\nwrite_enable=YES\n"),
			// the anonymous account and its drop directory: without a writable
			// 0777 directory under the root, an anonymous upload has nowhere
			// legal to land (the server's own permission checks decide)
			"/etc/passwd.d/vsftpd-ftp": cfgFile("/etc/passwd.d/vsftpd-ftp", "ftp:x:21:21:ftp:/srv/ftp:/usr/sbin/nologin\n"),
			"/srv/ftp/pub/.keep":       cfgFile("/srv/ftp/pub/.keep", ""),
			unit:                       cfgFile(unit, unitBody),
		},
		Service: &SvcSpec{Name: "vsftpd", Desc: "FTP daemon", Port: 21, Proto: "tcp", Scope: "lan", Handler: "ftp-user", Autostart: false}})

	r.put(&VPkg{Name: "bindutils", Version: "9.18-28", Comp: "main", Desc: "DNS utilities (dig, nslookup)", Size: 60,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{"/usr/bin/dig": binFile("/usr/bin/dig"),
			"/usr/bin/nslookup": binFile("/usr/bin/nslookup")}})
	r.put(&VPkg{Name: "dnsutils", Version: "11.7-2", Comp: "main", Desc: "network DNS client tools", Size: 80,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/dig": binFile("/usr/bin/dig")}})
	r.put(&VPkg{Name: "traceroute", Version: "2.1.6-1", Comp: "main", Desc: "path MTU discovery and traceroute", Size: 44,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/traceroute": binFile("/usr/bin/traceroute")}})
	r.put(&VPkg{Name: "busybox", Version: "1.36.1-3", Comp: "main", Desc: "Tiny tools in one binary (virtual)", Size: 96,
		Files: map[string]*PkgFile{"/bin/busybox": binFile("/bin/busybox")}})
	r.put(&VPkg{Name: "python3", Version: "3.12.5-3", Comp: "main", Desc: "interpreted high-level language (game-VM)", Size: 21000,
		Depends:  []string{"libc"},
		Files:    map[string]*PkgFile{"/usr/bin/python3": binFile("/usr/bin/python3")},
		PostInst: "registers the game bytecode runner for .py files (sandboxed, capability API)"})
	r.put(&VPkg{Name: "opensmtpd", Version: "6.8.2p1-1", Comp: "main", Desc: "small SMTP daemon (mail transport agent)", Size: 340,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/smtpd":      binFile("/usr/sbin/smtpd"),
			"/etc/mail/smtpd.conf": cfgFile("/etc/mail/smtpd.conf", "listen on lo port 25\nlisten on eth0 port 25\n\naction \"local\" mbox\naction \"relay\" relay\n\nmatch from any for local\n"),
			"/etc/mail/aliases":    cfgFile("/etc/mail/aliases", "root: alex\npostmaster: alex\nabuse: alex\n"),
			"/etc/systemd/system/opensmtpd.service": cfgFile("/etc/systemd/system/opensmtpd.service",
				"[Unit]\nDescription=OpenSMTPD mail transfer agent\nAfter=network.target\n[Service]\nExecStart=/usr/sbin/smtpd\n[Install]\nWantedBy=multi-user.target\n"),
		},
		Service: &SvcSpec{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp", Scope: "lan",
			Handler: "smtpd", Conf: "/etc/mail/smtpd.conf", Autostart: false}})

	// contrib: the tooling a security-minded player installs
	r.put(&VPkg{Name: "nmap", Version: "7.94+dfsg-2", Comp: "contrib", Desc: "network exploration / port scanner (world-aware)", Size: 3800,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/nmap": binFile("/usr/bin/nmap")}})
	r.put(&VPkg{Name: "hydra", Version: "9.5-1", Comp: "contrib", Desc: "login breaker (slow, noisy, world-aware)", Size: 420,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/hydra": binFile("/usr/bin/hydra")}})
	return r
}

func ubuntuRepo() *Repo {
	r := newRepo("ubuntu", "ubuntu", "ubuntu", "jammy", []string{"22.04"}, []string{"main"})
	r.put(&VPkg{Name: "libc6", Version: "2.35-0ubuntu3.8", Comp: "main", Desc: "GNU C Library", Size: 5200,
		Files: map[string]*PkgFile{"/usr/lib/x86_64-linux-gnu/libc.so.6": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "curl", Version: "8.5.0-2ubuntu10.3", Comp: "main", Desc: "command line tool for transferring data with URL syntax", Size: 430,
		Depends: []string{"libc6"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-4build1", Comp: "main", Desc: "interactive processes viewer", Size: 230,
		Depends: []string{"libc6"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	unit, body := unitFile(distroSpecs["ubuntu"], "nginx", "A high performance web server", "/usr/sbin/nginx")
	r.put(&VPkg{Name: "nginx", Version: "1.24.0-2ubuntu7.1", Comp: "main", Desc: "small, powerful, scalable web/proxy server", Size: 1300,
		Depends: []string{"libc6"},
		Files: map[string]*PkgFile{
			"/usr/sbin/nginx":                  binFile("/usr/sbin/nginx"),
			"/etc/nginx/nginx.conf":            cfgFile("/etc/nginx/nginx.conf", "user www-data;\nworker_processes auto;\nhttp {\n  include /etc/nginx/sites-enabled/*;\n}\n"),
			"/etc/nginx/sites-enabled/default": cfgFile("/etc/nginx/sites-enabled/default", "server {\n  listen 80 default_server;\n  root /var/www/html;\n}\n"),
			"/var/www/html/index.html":         cfgFile("/var/www/html/index.html", "<html><body><h1>Welcome to nginx!</h1></body></html>\n"),
			unit:                               cfgFile(unit, body),
		},
		Service: &SvcSpec{Name: "nginx", Desc: "A high performance web server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/sites-enabled/default", Autostart: true}})
	return r
}

func alpineRepo() *Repo {
	r := newRepo("alpine", "alpine", "alpine", "v3.20", nil, []string{"main", "community"})
	r.put(&VPkg{Name: "musl", Version: "1.2.5-r0", Comp: "main", Desc: "the musl c library", Size: 640,
		Files: map[string]*PkgFile{"/lib/ld-musl-x86_64.so.1": {Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "pcre", Version: "8.45-r3", Comp: "main", Desc: "Perl-compatible regular expression library", Size: 260,
		Files: map[string]*PkgFile{"/usr/lib/libpcre.so.1": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "zlib", Version: "1.3.1-r1", Comp: "main", Desc: "compression library", Size: 98,
		Files: map[string]*PkgFile{"/usr/lib/libz.so.1": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "curl", Version: "8.9.1-r2", Comp: "main", Desc: "URL retriever", Size: 380,
		Depends: []string{"musl"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-r0", Comp: "main", Desc: "interactive process viewer", Size: 210,
		Depends: []string{"musl"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	unit, body := unitFile(distroSpecs["alpine"], "nginx", "HTTP and reverse proxy server", "/usr/sbin/nginx")
	r.put(&VPkg{Name: "nginx", Version: "1.26.2-r0", Comp: "main", Desc: "HTTP and reverse proxy server", Size: 520,
		Depends: []string{"musl", "pcre", "zlib"},
		Files: map[string]*PkgFile{
			"/usr/sbin/nginx": binFile("/usr/sbin/nginx"),
			"/etc/nginx/http.d/default.conf": cfgFile("/etc/nginx/http.d/default.conf",
				"server {\n    listen 80 default_server;\n    root /var/www/localhost/htdocs;\n}\n"),
			"/var/www/localhost/htdocs/index.html": cfgFile("/var/www/localhost/htdocs/index.html",
				"<html><body><h1>It works!</h1></body></html>\n"),
			"/etc/nginx/nginx.conf": cfgFile("/etc/nginx/nginx.conf",
				"user nginx;\nworker_processes auto;\nhttp {\n    include /etc/nginx/http.d/*.conf;\n}\n"),
			unit: cfgFile(unit, body),
		},
		Service: &SvcSpec{Name: "nginx", Desc: "HTTP and reverse proxy server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/http.d/default.conf", Autostart: true}})
	unit, body = unitFile(distroSpecs["alpine"], "sshd", "OpenSSH server", "/usr/sbin/sshd")
	r.put(&VPkg{Name: "openssh", Version: "9.7_p1-r4", Comp: "main", Desc: "OpenSSH server", Size: 620,
		Depends: []string{"musl"},
		Files: map[string]*PkgFile{
			"/usr/sbin/sshd":       binFile("/usr/sbin/sshd"),
			"/etc/ssh/sshd_config": cfgFile("/etc/ssh/sshd_config", "Port 22\nPasswordAuthentication yes\n"),
			unit:                   cfgFile(unit, body),
		},
		Service: &SvcSpec{Name: "sshd", Desc: "OpenSSH server", Port: 22, Proto: "tcp", Scope: "lan", Handler: "ssh", Autostart: true}})
	r.put(&VPkg{Name: "tcpdump", Version: "4.99.5-r0", Comp: "main", Desc: "network packet analyser", Size: 740,
		Depends: []string{"musl"},
		Files:   map[string]*PkgFile{"/usr/bin/tcpdump": binFile("/usr/bin/tcpdump")}})
	r.put(&VPkg{Name: "nmap", Version: "7.95-r0", Comp: "community", Desc: "network scanner", Size: 4200,
		Depends: []string{"musl"},
		Files:   map[string]*PkgFile{"/usr/bin/nmap": binFile("/usr/bin/nmap")}})
	return r
}

func archRepo() *Repo {
	r := newRepo("arch", "arch", "archlinux", "core", nil, []string{"core"})
	r.put(&VPkg{Name: "glibc", Version: "2.39+r52+gf8e4623421-1", Comp: "core", Desc: "GNU C Library", Size: 9400,
		Files: map[string]*PkgFile{"/usr/lib/libc.so.6": {Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "pcre2", Version: "10.44-1", Comp: "core", Desc: "Perl-compatible regular expression library", Size: 1200,
		Files: map[string]*PkgFile{"/usr/lib/libpcre2-8.so.0": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "curl", Version: "8.9.1-1", Comp: "core", Desc: "command line tool for transferring data with URLs", Size: 1200,
		Depends: []string{"glibc"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-1", Comp: "core", Desc: "interactive process viewer", Size: 240,
		Depends: []string{"glibc"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	unit, body := unitFile(distroSpecs["arch"], "nginx", "Lightweight HTTP server", "/usr/bin/nginx")
	r.put(&VPkg{Name: "nginx", Version: "1.26.2-1", Comp: "core", Desc: "Lightweight HTTP server and IMAP/POP3 proxy server", Size: 560,
		Depends: []string{"glibc", "pcre2"},
		Files: map[string]*PkgFile{
			"/usr/bin/nginx": binFile("/usr/bin/nginx"),
			"/etc/nginx/nginx.conf": cfgFile("/etc/nginx/nginx.conf",
				"worker_processes auto;\nhttp {\n    include /etc/nginx/http.d/*.conf;\n}\n"),
			"/etc/nginx/http.d/default.conf": cfgFile("/etc/nginx/http.d/default.conf",
				"server {\n    listen 80;\n    root /usr/share/nginx/html;\n}\n"),
			"/usr/share/nginx/html/index.html": cfgFile("/usr/share/nginx/html/index.html",
				"<html><body><h1>Welcome to nginx!</h1></body></html>\n"),
			unit: cfgFile(unit, body),
		},
		Service: &SvcSpec{Name: "nginx", Desc: "Lightweight HTTP server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/http.d/default.conf", Autostart: true}})
	return r
}

func fedoraRepo() *Repo {
	r := newRepo("fedora", "fedora", "fedora", "40", nil, []string{"everything"})
	r.put(&VPkg{Name: "glibc", Version: "2.39-22.fc40", Comp: "everything", Desc: "The GNU libc libraries", Size: 9600,
		Files: map[string]*PkgFile{"/usr/lib64/libc.so.6": {Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "pcre2", Version: "10.42-2.fc40", Comp: "everything", Desc: "Perl-compatible regular expression library", Size: 1250,
		Files: map[string]*PkgFile{"/usr/lib64/libpcre2-8.so.0": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "curl", Version: "8.6.0-10.fc40", Comp: "everything", Desc: "A utility for getting files from remote servers", Size: 1180,
		Depends: []string{"glibc"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-3.fc40", Comp: "everything", Desc: "Interactive process viewer", Size: 250,
		Depends: []string{"glibc"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	unit, body := unitFile(distroSpecs["fedora"], "nginx", "A high performance web server", "/usr/sbin/nginx")
	r.put(&VPkg{Name: "nginx", Version: "1.26.2-1.fc40", Comp: "everything", Desc: "A high performance web server and reverse proxy server", Size: 1320,
		Depends: []string{"glibc", "pcre2"},
		Files: map[string]*PkgFile{
			"/usr/sbin/nginx": binFile("/usr/sbin/nginx"),
			"/etc/nginx/nginx.conf": cfgFile("/etc/nginx/nginx.conf",
				"user nginx;\nworker_processes auto;\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n}\n"),
			"/etc/nginx/conf.d/default.conf": cfgFile("/etc/nginx/conf.d/default.conf",
				"server {\n    listen 80;\n    root /usr/share/nginx/html;\n}\n"),
			"/usr/share/nginx/html/index.html": cfgFile("/usr/share/nginx/html/index.html",
				"<html><body><h1>Welcome to nginx on Fedora!</h1></body></html>\n"),
			unit: cfgFile(unit, body),
		},
		Service: &SvcSpec{Name: "nginx", Desc: "A high performance web server", Port: 80, Proto: "tcp", Scope: "any",
			Handler: "http-user", Conf: "/etc/nginx/conf.d/default.conf", Autostart: true}})
	return r
}

func openwrtRepo() *Repo {
	r := newRepo("openwrt", "openwrt", "openwrt", "23.05", nil, []string{"packages"})
	r.put(&VPkg{Name: "libc", Version: "1.2.4-1", Comp: "packages", Desc: "C library (musl)", Size: 320,
		Files: map[string]*PkgFile{"/lib/libc.so": {Mode: 0755, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "curl", Version: "8.6.0-1", Comp: "packages", Desc: "command line URL transfer tool", Size: 260,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/curl": binFile("/usr/bin/curl")}})
	r.put(&VPkg{Name: "htop", Version: "3.3.0-1", Comp: "packages", Desc: "interactive process viewer", Size: 180,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/htop": binFile("/usr/bin/htop")}})
	r.put(&VPkg{Name: "tcpdump", Version: "4.99.5-1", Comp: "packages", Desc: "network packet analyser", Size: 420,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/tcpdump": binFile("/usr/bin/tcpdump")}})
	r.put(&VPkg{Name: "nano", Version: "7.2-1", Comp: "packages", Desc: "small text editor", Size: 160,
		Depends: []string{"libc"},
		Files:   map[string]*PkgFile{"/usr/bin/nano": binFile("/usr/bin/nano")}})
	// uhttpd is what a router actually serves its LAN pages with, and the
	// package installs the procd init script the box's own init system runs
	r.put(&VPkg{Name: "uhttpd", Version: "2023-06-25-1", Comp: "packages", Desc: "tiny HTTP server for embedded devices", Size: 48,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/sbin/uhttpd":     binFile("/usr/sbin/uhttpd"),
			"/etc/config/uhttpd":   cfgFile("/etc/config/uhttpd", "config uhttpd 'main'\n\tlist listen_http '0.0.0.0:80'\n\toption home '/www'\n"),
			"/etc/init.d/uhttpd":   cfgFile("/etc/init.d/uhttpd", "#!/bin/sh /etc/rc.common\nSTART=50\nUSE_PROCD=1\n\nstart_service() {\n\tprocd_open_instance\n\tprocd_set_param command /usr/sbin/uhttpd -f -h /www\n\tprocd_close_instance\n}\n"),
			"/www/index.html":      cfgFile("/www/index.html", "<html><body><h1>NeoWRT</h1></body></html>\n"),
			"/etc/passwd.d/uhttpd": cfgFile("/etc/passwd.d/uhttpd", "uhttpd:x:81:81:uhttpd:/var/run/uhttpd:/usr/sbin/nologin\n"),
		},
		Service: &SvcSpec{Name: "uhttpd", Desc: "tiny HTTP server", Port: 80, Proto: "tcp", Scope: "lan",
			Handler: "http-user", Conf: "/etc/config/uhttpd", Autostart: false}})
	return r
}

// sashimiRepo is the third-party repository (§11). It is a real tree on a
// real CDN host, signed with a key that is NOT in anyone's keyring: nothing
// about it is special-cased, which is why the risk it carries is the risk a
// real third-party source carries.
func sashimiRepo() *Repo {
	r := newRepo("sashimi", "debian", "debian", "stable", nil, []string{"main"})
	r.Name = "sashimi"
	r.URL = "cdn.sashimi-cdn.example/debian"
	r.DeviceID = "cdn"
	r.UpstreamID = "cdn"
	r.SignKey = ThirdPartyKeyFP
	r.Pkgs = map[string]*VPkg{}
	r.put(&VPkg{Name: "libc", Version: "2.39-7", Comp: "main", Desc: "GNU C library (vendor build)", Size: 5000,
		Files: map[string]*PkgFile{"/usr/lib/x86_64-linux-gnu/libc.so.6": {Mode: 0644, Binary: true, Owner: "root", Group: "root"}}})
	r.put(&VPkg{Name: "nettop", Version: "2.4.1", Comp: "main", Desc: "vendor network monitoring toolkit", Size: 320,
		Depends: []string{"libc"},
		Files: map[string]*PkgFile{
			"/usr/bin/nettop":         binFile("/usr/bin/nettop"),
			"/etc/nettop/nettop.conf": cfgFile("/etc/nettop/nettop.conf", "collect = true\nreport = vendor\ninterval = 60\n"),
			"/var/lib/nettop/README":  cfgFile("/var/lib/nettop/README", "nettop keeps a rolling record of connections and uploads summaries.\ncontact support@sashimi-cdn.example for the reporting endpoint.\n"),
		},
		Procs:     []ProcSpec{{Name: "updater", Args: "--daemon", User: "www-data", CPU: 40, Mem: 60, Kind: "builtin"}},
		PostInst:  "enabled vendor background updater (--daemon)",
		Malicious: true,
	})
	return r
}

// ---- seeding ----

// SeedPackageWorld builds the package world: catalogue, the archive host's
// published trees, the mirror's copies, keyrings, sources files and the
// mirror's sync schedule. Called from NewWorld (and from LoadWorld for saves
// that predate this workstream).
func SeedPackageWorld(w *World) {
	if w.Repos == nil {
		w.Repos = map[string]*Repo{}
	}
	for name, r := range BuildRepos() {
		w.Repos[name] = r
	}
	linkReposToDevices(w)

	// the archive publishes every tree it is the origin of
	for _, r := range w.Repos {
		up := w.Devices[r.UpstreamID]
		if up == nil {
			continue
		}
		if err := WriteTree(up, r, w.Sim); err != nil {
			up.Logf("err", "archive", "could not publish %s: %v", r.Name, err)
			continue
		}
		r.LastSync = w.Sim
		r.Status = "SYNCED"
		r.StatusWhy = ""
	}
	// the mirror carries a copy of everything it mirrors, including its state
	for _, r := range w.Repos {
		if !r.IsMirrored() {
			continue
		}
		m := w.Devices[r.DeviceID]
		up := w.Devices[r.UpstreamID]
		if m == nil || up == nil {
			continue
		}
		for rel, body := range RenderTree(r, w.Sim) {
			m.FS.MkdirAll(parentDir(WebRoot(m)+"/"+rel), 0755, "root", "root")
			m.FS.WriteBytes(WebRoot(m)+"/"+rel, body, 0644, "root", "root")
		}
		r.Missing = nil
		r.ReleaseHash = treeHashes(r, w.Sim)
	}
	seedPackageClients(w)
	seedMirrorSchedule(w)
	markStaleDebianTree(w)
}

// markStaleDebianTree is the seeded incident: the debian sync line on the
// mirror host was commented out after a disk incident, so that one tree has
// not synced for three days while every other tree is current. It is a fact
// with a readable cause, not a random degradation.
func markStaleDebianTree(w *World) {
	r := w.Repos["debian"]
	if r == nil {
		return
	}
	r.LastSync = w.Sim.Add(-76 * time.Hour)
	r.Status = "BEHIND"
	r.StatusWhy = "last successful sync was 3d ago; the sync line for this tree is disabled on " + r.DeviceID
}

// linkReposToDevices points each repository at the hosts that serve it.
func linkReposToDevices(w *World) {
	for _, r := range w.Repos {
		if r.Name == "sashimi" {
			continue
		}
		r.DeviceID = "mirror"
		r.UpstreamID = "archive"
	}
}

// RenderTree renders every file of a repository tree, relative to the
// document root: indexes, payloads, the signed release (hashed from the
// index bytes it will actually describe) and the readable front page.
func RenderTree(r *Repo, now time.Time) map[string][]byte {
	files := map[string][]byte{}
	for _, comp := range r.Comps {
		files[IndexPath(r, comp)] = indexBytesFor(r, comp)
	}
	// payloads are what an index's Filename: points at
	for _, p := range r.Pkgs {
		files[PayloadPath(r, p)] = []byte(RenderPayload(r, p))
	}
	release := RenderRelease(r, now)
	files[ReleasePath(r)] = []byte(release)
	files[TreeIndexPath(r)] = []byte(RenderTreeIndex(r))
	// the signing key is published with the tree, so trusting a repository is
	// a real act with real material behind it
	files[r.Path+"/"+KeyRel(r)] = []byte(KeyFile(r.SignKey, keyOwner(r.SignKey),
		keyValidUntil(r.SignKey), "public key for the "+r.Name+" tree"))
	return files
}

// treeHashes records what the tree's release file claims, so status reporting
// can compare claims against bytes.
func treeHashes(r *Repo, now time.Time) map[string]string {
	body := RenderRelease(r, now)
	out := map[string]string{}
	for _, comp := range r.Comps {
		out[comp] = releaseHashFor(body, comp)
	}
	return out
}

// keyOwner / keyValidUntil describe the world's two signing keys.
func keyOwner(fp string) string {
	if keyFingerprint(fp) == keyFingerprint(ThirdPartyKeyFP) {
		return ThirdPartyKeyOwner
	}
	return ArchiveKeyOwner
}

func keyValidUntil(fp string) string {
	if keyFingerprint(fp) == keyFingerprint(ThirdPartyKeyFP) {
		return ThirdPartyKeyValidUntil
	}
	return ArchiveKeyValidUntil
}

// PublishPackage publishes a package (or a new version of one) on the archive
// and republishes the tree, so its indexes and signed release describe it.
// This is what upstream looks like from a mirror's side: the bytes it syncs
// change, and an interrupted sync can leave a copy that no longer matches.
func (w *World) PublishPackage(r *Repo, p *VPkg) error {
	if r == nil || p == nil {
		return fmt.Errorf("no repository or package to publish")
	}
	up := w.Devices[r.UpstreamID]
	if up == nil {
		return fmt.Errorf("%s has no upstream archive", r.Name)
	}
	old := r.Pkgs[p.Name]
	r.put(p)
	if err := WriteTree(up, r, w.Sim); err != nil {
		r.Pkgs[p.Name] = old
		return err
	}
	up.Logf("info", "archive", "published %s %s to the %s tree", p.Name, p.Version, r.Name)
	w.AddEvent(up.ID, "info", "archive", "%s %s published to the %s tree", p.Name, p.Version, r.Name)
	return nil
}

// WriteTree writes a repository's files onto the host that publishes it.
func WriteTree(dev *Device, r *Repo, now time.Time) error {
	root := WebRoot(dev)
	if root == "" {
		return fmt.Errorf("%s has no web root to publish %s from", dev.Hostname, r.Name)
	}
	for rel, body := range RenderTree(r, now) {
		dev.FS.MkdirAll(parentDir(root+"/"+rel), 0755, "root", "root")
		dev.FS.WriteBytes(root+"/"+rel, body, 0644, "root", "root")
	}
	return nil
}

// seedPackageClients gives every device the pieces its distribution needs: a
// trusted keyring and a sources configuration that names the mirror.
func seedPackageClients(w *World) {
	var ids []string
	for id := range w.Devices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := w.Devices[id]
		spec := DistroFor(d)
		if spec == nil {
			continue
		}
		// the world's archive key is trusted by every distribution that has
		// a keyring; the third-party key is deliberately absent
		d.FS.MkdirAll(spec.Keyring, 0755, "root", "root")
		name := "neohome-archive.asc"
		switch spec.Family {
		case FamilyAPK:
			name = "neohome-archive.rsa.pub"
		case FamilyOpkg:
			name = "neohome-archive.pub"
		case FamilyRPM:
			name = "RPM-GPG-KEY-neohome"
		}
		d.FS.Write(spec.Keyring+"/"+name,
			KeyFile(ArchiveKeyFP, ArchiveKeyOwner, ArchiveKeyValidUntil,
				"signed package metadata for this world"), 0644, "root", "root")
		seedSources(d, spec)
	}
}

// seedSources writes the configuration file each distribution reads. The
// values are the world's mirror; a player who changes them changes what their
// box can install.
func seedSources(d *Device, spec *DistroSpec) {
	mirror := "mirror.neohome.example"
	switch spec.Family {
	case FamilyDeb:
		path := "debian"
		if spec.ID == "ubuntu" {
			path = "ubuntu"
		}
		if d.FS.Exists("/etc/apt/sources.list") {
			return // the image already knows its sources
		}
		suite := "stable"
		if spec.ID == "ubuntu" {
			suite = "jammy"
		}
		d.FS.MkdirAll("/etc/apt", 0755, "root", "root")
		d.FS.Write("/etc/apt/sources.list",
			fmt.Sprintf("deb http://%s/%s %s main\n", mirror, path, suite), 0644, "root", "root")
	case FamilyAPK:
		if d.FS.Exists("/etc/apk/repositories") {
			return
		}
		d.FS.MkdirAll("/etc/apk", 0755, "root", "root")
		d.FS.Write("/etc/apk/repositories",
			fmt.Sprintf("http://%s/alpine/v3.20/main\nhttp://%s/alpine/v3.20/community\n", mirror, mirror), 0644, "root", "root")
	case FamilyPacman:
		if d.FS.Exists("/etc/pacman.conf") {
			return
		}
		d.FS.MkdirAll("/etc/pacman.d", 0755, "root", "root")
		d.FS.Write("/etc/pacman.conf",
			fmt.Sprintf("[options]\nArchitecture = x86_64\n\n[core]\nServer = http://%s/archlinux/$repo/os/$arch\n", mirror), 0644, "root", "root")
	case FamilyRPM:
		if d.FS.IsDir("/etc/yum.repos.d") {
			return
		}
		d.FS.MkdirAll("/etc/yum.repos.d", 0755, "root", "root")
		d.FS.Write("/etc/yum.repos.d/neohome.repo",
			fmt.Sprintf("[neohome]\nname=NeoHome Packages\nbaseurl=http://%s/fedora/40/everything/x86_64\nenabled=1\ngpgcheck=1\n", mirror), 0644, "root", "root")
	case FamilyOpkg:
		if d.FS.Exists("/etc/opkg/distfeeds.conf") {
			return
		}
		d.FS.MkdirAll("/etc/opkg", 0755, "root", "root")
		d.FS.Write("/etc/opkg/distfeeds.conf",
			fmt.Sprintf("src/gz neohome http://%s/openwrt/23.05/packages/%s\n", mirror, spec.Arch), 0644, "root", "root")
	}
}

// seedMirrorSchedule gives the mirror host its real sync schedule: one job per
// tree, run by cron, except the debian one — which was commented out after a
// disk incident and never re-enabled. That single commented line is the whole
// cause of the BEHIND state a player can be hired to fix, and it is readable
// on the host.
func seedMirrorSchedule(w *World) {
	m := w.Devices["mirror"]
	if m == nil {
		return
	}
	m.EnsureCronDaemon()
	if svc := m.CronDaemon(); svc != nil {
		svc.State = "running"
	}
	var lines []string
	lines = append(lines, "# mirror sync jobs — one line per tree, every 15 minutes",
		"# the debian tree was disabled on 2026-10-03 after the pool filled the disk;",
		"# re-enable this line (or run `mirror-sync debian`) once the pool is clean",
		"#*/15 * * * * mirror-sync debian")
	for _, name := range []string{"ubuntu", "alpine", "arch", "fedora", "openwrt"} {
		lines = append(lines, fmt.Sprintf("*/15 * * * * mirror-sync %s", name))
	}
	w.plantCronJob("mirror", "root", strings.Join(lines, "\n")+"\n")
}

// RepoForDevice finds the repository a device is actually configured to use
// that publishes this package. Provenance has to be a source the box itself
// has: the first tree in the catalogue that happens to carry the name may be
// one this machine has never heard of.
func (w *World) RepoForDevice(d *Device, name string) *Repo {
	if d == nil {
		return nil
	}
	for _, src := range w.SourcesForDevice(d) {
		if src.Repo != nil && src.Repo.Pkgs[name] != nil {
			return src.Repo
		}
	}
	return nil
}

// RepoOf finds the repository that publishes a package, for reports that show
// where something came from.
func (w *World) RepoOf(name string) *Repo {
	keys := make([]string, 0, len(w.Repos))
	for k := range w.Repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if w.Repos[k].Pkgs[name] != nil {
			return w.Repos[k]
		}
	}
	return nil
}

// FindPkg looks a package up across the world's catalogue. Used by the
// assistant (which installs what a job asks for) and by anything that needs a
// definition rather than a served file.
func (w *World) FindPkg(name string) *VPkg {
	names := make([]string, 0, len(w.Repos))
	for n := range w.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if p := w.Repos[n].Pkgs[name]; p != nil {
			return p
		}
	}
	return nil
}
