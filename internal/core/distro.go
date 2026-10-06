package core

import (
	"sort"
	"strings"
)

// Distributions (§9 软件包系统, §10 APT/Repository/Mirror).
//
// A distribution is not a label on a device: it decides which package
// manager exists, where its sources are configured, where indexes are
// cached, which keyring is trusted and what the index files are called. A
// NeoOS PC has apt and /etc/apt/sources.list; an OpenWrt router has opkg and
// /etc/opkg/distfeeds.conf, and asking either one for the other manager is
// `command not found` — the binary is not on that box.
//
// This file owns the mapping and the sources parsers. The package managers
// themselves live in internal/shell/pkg_cmds.go; the catalogue, the index
// format and the verification live in pkgs.go / pkgfiles.go.

// Family is the packaging family: it fixes the index layout, the payload
// suffix and the manager semantics. Distros share a family.
type Family string

const (
	FamilyDeb    Family = "deb"    // Debian, Ubuntu, NeoOS (apt)
	FamilyAPK    Family = "apk"    // Alpine (apk)
	FamilyPacman Family = "pacman" // Arch (pacman)
	FamilyRPM    Family = "rpm"    // Fedora / RHEL (dnf)
	FamilyOpkg   Family = "opkg"   // OpenWrt / BusyBox routers (opkg)
)

// DistroSpec is what a distribution means in this world.
type DistroSpec struct {
	ID      string // debian|ubuntu|neoos|alpine|arch|fedora|openwrt|…
	Family  Family
	Manager string // apt|apk|pacman|dnf|opkg — the command a player types
	// Sources are the real configuration files that name repositories, in
	// the order the manager reads them.
	Sources []string
	// Keyring holds the trusted signing keys.
	Keyring string
	// Lists is where downloaded indexes are cached. Installing reads these,
	// exactly like a real box: no update means nothing to install from.
	Lists string
	// Arch is the build architecture this distro's boxes run.
	Arch string
	// ReleaseName is the signed metadata file for one suite, per family.
	ReleaseName string
}

// distroSpecs is the world's distribution table. NeoOS is a Debian
// derivative and NeoWRT/StockOS are OpenWrt builds: they share the family,
// the sources format and the manager with their upstream.
var distroSpecs = map[string]*DistroSpec{
	"debian": {ID: "debian", Family: FamilyDeb, Manager: "apt",
		Sources: []string{"/etc/apt/sources.list", "/etc/apt/sources.list.d"},
		Keyring: "/etc/apt/trusted.gpg.d", Lists: "/var/lib/apt/lists",
		Arch: "amd64", ReleaseName: "InRelease"},
	"neoos": {ID: "debian", Family: FamilyDeb, Manager: "apt",
		Sources: []string{"/etc/apt/sources.list", "/etc/apt/sources.list.d"},
		Keyring: "/etc/apt/trusted.gpg.d", Lists: "/var/lib/apt/lists",
		Arch: "amd64", ReleaseName: "InRelease"},
	"ubuntu": {ID: "ubuntu", Family: FamilyDeb, Manager: "apt",
		Sources: []string{"/etc/apt/sources.list", "/etc/apt/sources.list.d"},
		Keyring: "/etc/apt/trusted.gpg.d", Lists: "/var/lib/apt/lists",
		Arch: "amd64", ReleaseName: "InRelease"},
	"alpine": {ID: "alpine", Family: FamilyAPK, Manager: "apk",
		Sources: []string{"/etc/apk/repositories"},
		Keyring: "/etc/apk/keys", Lists: "/var/cache/apk",
		Arch: "x86_64", ReleaseName: "APKINDEX"},
	"arch": {ID: "arch", Family: FamilyPacman, Manager: "pacman",
		Sources: []string{"/etc/pacman.conf", "/etc/pacman.d/mirrorlist"},
		Keyring: "/etc/pacman.d/gnupg", Lists: "/var/lib/pacman/sync",
		Arch: "x86_64", ReleaseName: "core.db"},
	"fedora": {ID: "fedora", Family: FamilyRPM, Manager: "dnf",
		Sources: []string{"/etc/yum.repos.d"},
		Keyring: "/etc/pki/rpm-gpg", Lists: "/var/cache/dnf",
		Arch: "x86_64", ReleaseName: "primary"},
	"rhel": {ID: "fedora", Family: FamilyRPM, Manager: "dnf",
		Sources: []string{"/etc/yum.repos.d"},
		Keyring: "/etc/pki/rpm-gpg", Lists: "/var/cache/dnf",
		Arch: "x86_64", ReleaseName: "primary"},
	"openwrt": {ID: "openwrt", Family: FamilyOpkg, Manager: "opkg",
		Sources: []string{"/etc/opkg/distfeeds.conf", "/etc/opkg/customfeeds.conf"},
		Keyring: "/etc/opkg/keys", Lists: "/var/opkg-lists",
		Arch: "mips_24kc", ReleaseName: "Packages"},
	"neowrt": {ID: "openwrt", Family: FamilyOpkg, Manager: "opkg",
		Sources: []string{"/etc/opkg/distfeeds.conf", "/etc/opkg/customfeeds.conf"},
		Keyring: "/etc/opkg/keys", Lists: "/var/opkg-lists",
		Arch: "mips_24kc", ReleaseName: "Packages"},
	"stockos": {ID: "openwrt", Family: FamilyOpkg, Manager: "opkg",
		Sources: []string{"/etc/opkg/distfeeds.conf", "/etc/opkg/customfeeds.conf"},
		Keyring: "/etc/opkg/keys", Lists: "/var/opkg-lists",
		Arch: "arm_cortex-a7", ReleaseName: "Packages"},
	"openbmc": {ID: "openwrt", Family: FamilyOpkg, Manager: "opkg",
		Sources: []string{"/etc/opkg/distfeeds.conf", "/etc/opkg/customfeeds.conf"},
		Keyring: "/etc/opkg/keys", Lists: "/var/opkg-lists",
		Arch: "arm_cortex-a7", ReleaseName: "Packages"},
	"neocore": {ID: "openwrt", Family: FamilyOpkg, Manager: "opkg",
		Sources: []string{"/etc/opkg/distfeeds.conf", "/etc/opkg/customfeeds.conf"},
		Keyring: "/etc/opkg/keys", Lists: "/var/opkg-lists",
		Arch: "x86_64", ReleaseName: "Packages"},
}

// DistroFor returns the distribution profile a device runs, or nil for an
// image with no package management at all (phones, IoT firmware: they update
// through their vendor, not through a shell).
func DistroFor(d *Device) *DistroSpec {
	if d == nil {
		return nil
	}
	key := lower(d.OS.Distro)
	if s := distroSpecs[key]; s != nil {
		return s
	}
	return nil
}

// ManagerFor is the command name a device's distribution provides.
func ManagerFor(d *Device) string {
	if s := DistroFor(d); s != nil {
		return s.Manager
	}
	return ""
}

// HasManager reports whether this device really has that package manager.
// A distribution ships exactly one, so `apt` on an Alpine box is not a
// missing feature of the game — it is a command that is not installed.
func HasManager(d *Device, manager string) bool {
	return ManagerFor(d) == manager
}

// Source is one configured repository line, as the device's configuration
// file expresses it. Repo is filled in when the line matches a repository
// the world actually knows about; a line that matches nothing is a
// misconfiguration (or a third-party source that has not been added yet).
type Source struct {
	URL   string // normalized base URL, no trailing slash
	Suite string // stable | v3.20 | core | 40 | 23.05 …
	Comps []string
	Repo  *Repo
	File  string // which configuration file it came from
	Line  string // the raw line, for error messages
}

// SourcesForDevice reads the device's real configuration files and returns
// the repositories it is configured to use, in file order. Nothing is
// cached: editing sources.list and re-running the manager is the whole point.
func (w *World) SourcesForDevice(d *Device) []Source {
	spec := DistroFor(d)
	if spec == nil {
		return nil
	}
	var out []Source
	for _, f := range spec.Sources {
		// a directory of drop-ins (sources.list.d, yum.repos.d) comes first:
		// Read on a directory returns an empty body successfully, so testing
		// Read before IsDir silently skipped every drop-in file — which is
		// where a player adds a repository without touching the main list.
		if d.FS.IsDir(f) {
			for _, full := range d.FS.List(f) {
				if d.FS.IsDir(full) {
					continue
				}
				if data, ok := d.FS.Read(full); ok {
					out = append(out, ParseSources(spec, full, string(data))...)
				}
			}
			continue
		}
		if data, ok := d.FS.Read(f); ok {
			out = append(out, ParseSources(spec, f, string(data))...)
		}
	}
	for i := range out {
		out[i].Repo = w.matchRepo(out[i])
	}
	return out
}

// matchRepo finds the repository a source line names. A source points at a
// *location inside* a tree — Debian's sources.list names the tree root,
// OpenWrt's feed names the directory the index sits in, Fedora's baseurl names
// the arch directory — so matching is by prefix against the URLs that tree
// actually serves. A typo, a dead third-party host or a path nobody mirrors is
// simply an unmatched source, which the manager reports as an ignored line.
func (w *World) matchRepo(src Source) *Repo {
	names := make([]string, 0, len(w.Repos))
	for n := range w.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	best := (*Repo)(nil)
	bestLen := -1
	for _, n := range names {
		r := w.Repos[n]
		if r.URL == "" || r.DeviceID == "" {
			continue
		}
		if r.Suite != "" && src.Suite != "" && !r.suiteMatches(src.Suite) {
			continue
		}
		for _, url := range r.servedURLs() {
			if !urlHasPrefix(url, src.URL) {
				continue
			}
			if len(src.Comps) > 0 && len(r.Comps) > 0 && !compsOverlap(src.Comps, r.Comps) {
				continue
			}
			// the most specific tree wins, so a third-party repo mounted
			// deeper in a path is not shadowed by the mirror's own tree
			if len(r.URL) > bestLen {
				best, bestLen = r, len(r.URL)
			}
		}
	}
	return best
}

// servedURLs are the URLs this repository publishes metadata at, normalized.
func (r *Repo) servedURLs() []string {
	out := []string{normalizeURL(r.URL + "/" + ReleaseRel(r))}
	for _, comp := range r.Comps {
		out = append(out, normalizeURL(r.URL+"/"+IndexRel(r, comp)))
	}
	return out
}

// urlHasPrefix compares URLs on segment boundaries: /debian must not match
// /debian-archive.
func urlHasPrefix(url, prefix string) bool {
	if prefix == "" {
		return false
	}
	if url == prefix {
		return true
	}
	return strings.HasPrefix(url, prefix) && strings.HasPrefix(url[len(prefix):], "/")
}

// suiteMatches accepts a repository's own suite plus its aliases, because a
// real mirror carries both `stable` and `bookworm` for the same tree.
func (r *Repo) suiteMatches(suite string) bool {
	if strings.EqualFold(r.Suite, suite) {
		return true
	}
	for _, a := range r.SuiteAliases {
		if strings.EqualFold(a, suite) {
			return true
		}
	}
	return false
}

// CompsFor narrows a repo to the components this device asked for.
func (r *Repo) CompsFor(src Source) []string {
	if len(src.Comps) == 0 {
		return r.Comps
	}
	var out []string
	for _, c := range src.Comps {
		for _, have := range r.Comps {
			if strings.EqualFold(c, have) {
				out = append(out, have)
			}
		}
	}
	if len(out) == 0 {
		return r.Comps
	}
	return out
}

func compsOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(x, y) {
				return true
			}
		}
	}
	return false
}

// ParseSources turns a configuration file into repository lines. Each family
// has its own real syntax; the world speaks all five because it has boxes of
// all five kinds.
func ParseSources(spec *DistroSpec, file, body string) []Source {
	switch spec.Family {
	case FamilyDeb:
		return parseDebSources(file, body)
	case FamilyAPK:
		return parseAPKRepositories(file, body)
	case FamilyPacman:
		return parsePacmanConf(file, body)
	case FamilyRPM:
		return parseYumRepo(file, body)
	case FamilyOpkg:
		return parseOpkgFeeds(file, body)
	}
	return nil
}

// parseDebSources: `deb[arch=amd64] http://host/path suite comp1 comp2`.
func parseDebSources(file, body string) []Source {
	var out []Source
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		// deb822 stanzas ("Types: deb" / "URIs:" / "Suites:" / "Components:")
		if strings.HasPrefix(line, "URIs:") {
			// handled below by the stanza reader
			continue
		}
		if len(f) < 3 || (f[0] != "deb" && f[0] != "deb-src") {
			continue
		}
		if f[0] == "deb-src" {
			continue // source packages are not part of this world yet
		}
		url := f[1]
		if i := strings.Index(url, "["); i == 0 {
			// options attached to the type: deb [arch=amd64] URL suite comps
			url = f[2]
			f = append(f[:1], f[2:]...)
		}
		if len(f) < 3 {
			continue
		}
		out = append(out, Source{
			URL: normalizeURL(url), Suite: f[2], Comps: f[3:],
			File: file, Line: line,
		})
	}
	// deb822 stanza form: Types/URIs/Suites/Components lines
	url, suite, comps := "", "", []string{}
	flush := func() {
		if url != "" && suite != "" {
			out = append(out, Source{URL: normalizeURL(url), Suite: suite, Comps: comps, File: file, Line: "deb822 stanza"})
		}
		url, suite, comps = "", "", nil
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			if line == "" {
				flush()
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "uris":
			url = strings.TrimSpace(val)
		case "suites":
			suite = strings.Fields(val)[0]
		case "components":
			comps = strings.Fields(val)
		}
	}
	flush()
	return out
}

// parseAPKRepositories: one URL per line, the tree is baked into the path.
// `http://mirror.example/alpine/v3.20/main` is suite v3.20, component main.
func parseAPKRepositories(file, body string) []Source {
	var out []Source
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		url := normalizeURL(line)
		suite, comp := "", ""
		if i := strings.LastIndex(url, "/"); i > 0 {
			comp = url[i+1:]
			url = url[:i]
		}
		if i := strings.LastIndex(url, "/"); i > 0 {
			suite = url[i+1:]
			url = url[:i]
		}
		out = append(out, Source{URL: url, Suite: suite, Comps: []string{comp}, File: file, Line: line})
	}
	return out
}

// parsePacmanConf: `[core]` sections with `Server = URL/$repo/os/$arch`.
func parsePacmanConf(file, body string) []Source {
	var out []Source
	section := ""
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(strings.TrimSpace(line), "[]")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Server") {
			continue
		}
		url := strings.TrimSpace(val)
		if section == "" || section == "options" {
			continue
		}
		url = strings.ReplaceAll(url, "$repo", section)
		url = strings.ReplaceAll(url, "$arch", "x86_64")
		// Arch paths embed the repository twice: <base>/<repo>/os/<arch>.
		// Strip the repository and everything under it, so the source is the
		// tree base and the section stays the repository name.
		base, suite := url, section
		if i := strings.Index(base, "/"+section+"/os/"); i > 0 {
			base = base[:i]
		}
		out = append(out, Source{URL: normalizeURL(base), Suite: suite, Comps: []string{section}, File: file, Line: line})
	}
	return out
}

// parseYumRepo: INI stanzas with `baseurl=` and `enabled=`.
func parseYumRepo(file, body string) []Source {
	var out []Source
	name, baseurl, enabled := "", "", true
	flush := func() {
		if name != "" && baseurl != "" && enabled {
			out = append(out, Source{URL: normalizeURL(baseurl), File: file, Line: "[" + name + "]"})
		}
		name, baseurl, enabled = "", "", true
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			name = strings.Trim(strings.TrimSpace(line), "[]")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "baseurl", "mirrorlist":
			baseurl = strings.TrimSpace(val)
		case "enabled":
			enabled = !strings.EqualFold(strings.TrimSpace(val), "0")
		}
	}
	flush()
	return out
}

// parseOpkgFeeds: `src/gz <name> <url>`.
func parseOpkgFeeds(file, body string) []Source {
	var out []Source
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || (f[0] != "src" && f[0] != "src/gz") {
			continue
		}
		url := normalizeURL(f[2])
		out = append(out, Source{URL: url, File: file, Line: line})
	}
	return out
}

// normalizeURL strips the scheme and any trailing slash so configuration
// files, repository records and error messages all compare the same way.
// https and http to the same host are the same repository here, exactly as
// they would be on a real mirror.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	return strings.TrimSuffix(u, "/")
}

// ---- keyrings (§11: signing) ----

// TrustedKey is one entry in a device's keyring, read from the real file.
type TrustedKey struct {
	Fingerprint string
	Owner       string
	ValidUntil  string // YYYY-MM-DD, empty means no expiry recorded
}

// keyFingerprint formats a fingerprint the way the world's key files store
// it: uppercase hex groups. Callers pass "9F2C1A4B…" or grouped text.
func keyFingerprint(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// KeyringOf reads every key file on the device. A key is a real file with a
// real fingerprint; that is what makes "未知源/签名错误/密钥过期" possible
// instead of a flag.
func KeyringOf(d *Device) []TrustedKey {
	spec := DistroFor(d)
	if spec == nil {
		return nil
	}
	var out []TrustedKey
	p := spec.Keyring
	if d.FS.IsDir(p) {
		for _, full := range d.FS.List(p) {
			if d.FS.IsDir(full) {
				continue
			}
			if data, ok := d.FS.Read(full); ok {
				out = append(out, parseKeyFile(string(data))...)
			}
		}
		return out
	}
	if data, ok := d.FS.Read(p); ok {
		out = append(out, parseKeyFile(string(data))...)
	}
	return out
}

// parseKeyFile reads the fingerprint lines out of an armored key file. The
// world does not do real crypto for packages (no host crypto is trusted to
// be a game mechanic), but the *identity and validity of the signer* is
// world state, and that is what verification checks.
func parseKeyFile(body string) []TrustedKey {
	var out []TrustedKey
	var cur *TrustedKey
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "fingerprint":
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &TrustedKey{Fingerprint: keyFingerprint(val)}
		case "owner":
			if cur != nil {
				cur.Owner = strings.TrimSpace(val)
			}
		case "valid-until":
			if cur != nil {
				cur.ValidUntil = strings.TrimSpace(val)
			}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// KeyIsTrusted answers whether the device trusts that signing fingerprint.
func KeyIsTrusted(d *Device, fingerprint string) bool {
	want := keyFingerprint(fingerprint)
	for _, k := range KeyringOf(d) {
		if k.Fingerprint == want {
			return true
		}
	}
	return false
}
