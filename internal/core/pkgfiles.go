package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Package metadata and payloads are FILES (§9 软件包系统, §10 Repository,
// §11 软件安全).
//
// The catalogue in Go is what the world *can* publish; what a box can install
// is what its distribution's metadata says, read through that distribution's
// own layout and verified the way that distribution verifies it. That is why
// the index/release/payload rendering and parsing live together here:
//
//	catalogue (pkgs.go)  →  files on the archive host (rendered here)
//	                     →  files on the mirror host (copied by a real sync)
//	                     →  verified + cached on the client (pkgnet.go)
//	                     →  applied to the world (pkgapply.go)
//
// Two properties fall out of writing real files: a player can host their own
// repository by producing these files (spec §10 wants exactly that), and an
// interrupted sync really does leave a tree whose release file no longer
// matches its indexes — CORRUPTED is a checksum, not a mood.

// ---- layout ----

// Arch is the published architecture of this repository's packages.
func (r *Repo) Arch() string { return repoArch(r) }

// repoArch is the architecture this repository's packages are built for.
func repoArch(r *Repo) string {
	if s := distroSpecs[r.Distro]; s != nil {
		return s.Arch
	}
	return "amd64"
}

// IndexPath is the served index for one component, per family layout: it is
// where the file lives on the serving host, under the tree's path.
func IndexPath(r *Repo, comp string) string { return r.Path + "/" + IndexRel(r, comp) }

// IndexRel is the same index relative to the repository root, which is the
// form the signed metadata and the URL both use.
func IndexRel(r *Repo, comp string) string {
	arch := repoArch(r)
	switch distroSpecs[r.Distro].Family {
	case FamilyAPK:
		return fmt.Sprintf("%s/%s/%s/APKINDEX", r.Suite, comp, arch)
	case FamilyPacman:
		return fmt.Sprintf("%s/os/%s/%s.db", comp, arch, comp)
	case FamilyRPM:
		return fmt.Sprintf("%s/%s/%s/repodata/primary", r.Suite, comp, arch)
	case FamilyOpkg:
		return fmt.Sprintf("%s/packages/%s/Packages", r.Suite, arch)
	}
	return fmt.Sprintf("dists/%s/%s/binary-%s/Packages", r.Suite, comp, arch)
}

// ReleasePath is the signed metadata file for the repository's suite, on the
// serving host.
func ReleasePath(r *Repo) string { return r.Path + "/" + ReleaseRel(r) }

// ReleaseRel is the release file relative to the repository root.
func ReleaseRel(r *Repo) string {
	switch distroSpecs[r.Distro].Family {
	case FamilyAPK:
		return fmt.Sprintf("%s/%s/%s/APKINDEX.sig", r.Suite, r.Comps[0], repoArch(r))
	case FamilyPacman:
		return fmt.Sprintf("%s/os/%s/%s.db.sig", r.Comps[0], repoArch(r), r.Comps[0])
	case FamilyRPM:
		return fmt.Sprintf("%s/%s/%s/repodata/repomd.xml", r.Suite, r.Comps[0], repoArch(r))
	case FamilyOpkg:
		return fmt.Sprintf("%s/packages/%s/Packages.sig", r.Suite, repoArch(r))
	}
	return fmt.Sprintf("dists/%s/%s", r.Suite, distroSpecs[r.Distro].ReleaseName)
}

// KeyRel is where a tree publishes the public key that signs it, relative to
// the tree root: how a player gets the key material to trust in the first
// place. For the world's own archive key this is a copy of the well-known
// key; for a third-party tree it is the only way to obtain its key.
func KeyRel(r *Repo) string { return "keys/" + keyFingerprint(r.SignKey) + ".asc" }

// TreeIndexPath is the human-readable index page for the whole tree. It is
// what `curl http://mirror…/debian/Release` fetches, and it is generated from
// the same facts the machine-readable metadata uses.
func TreeIndexPath(r *Repo) string { return r.Path + "/Release" }

// PayloadPath is the installable artefact on the serving host.
func PayloadPath(r *Repo, p *VPkg) string { return r.Path + "/" + PayloadRel(r, p) }

// PayloadRel is the artefact relative to the repository root, which is the
// name an index records and a client downloads.
func PayloadRel(r *Repo, p *VPkg) string {
	arch := p.Arch
	if arch == "" {
		arch = repoArch(r)
	}
	if len(p.Name) == 0 {
		return ""
	}
	letter := string(p.Name[0])
	comp := p.Comp
	if comp == "" && len(r.Comps) > 0 {
		comp = r.Comps[0]
	}
	switch distroSpecs[r.Distro].Family {
	case FamilyAPK:
		return fmt.Sprintf("%s/%s/%s/%s-%s.apk", r.Suite, comp, arch, p.Name, p.Version)
	case FamilyPacman:
		return fmt.Sprintf("%s/os/%s/%s-%s-%s.pkg.tar.zst", comp, arch, p.Name, p.Version, arch)
	case FamilyRPM:
		return fmt.Sprintf("%s/%s/%s/Packages/%s/%s-%s.%s.rpm", r.Suite, comp, arch, letter, p.Name, p.Version, arch)
	case FamilyOpkg:
		return fmt.Sprintf("%s/packages/%s/%s_%s_%s.ipk", r.Suite, arch, p.Name, p.Version, arch)
	}
	return fmt.Sprintf("pool/%s/%s/%s/%s_%s_%s.vpkg", comp, letter, p.Name, p.Name, p.Version, arch)
}

// ---- hashing ----

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- rendering ----

// RenderIndex produces one component's package index. Every field a client
// later verifies or trusts comes from here, so the index cannot drift from
// the catalogue without a test noticing.
func RenderIndex(r *Repo, comp string) string {
	var names []string
	for n, p := range r.Pkgs {
		if p.Comp != "" && p.Comp != comp {
			continue
		}
		if p.Comp == "" && len(r.Comps) > 0 && comp != r.Comps[0] {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		p := r.Pkgs[n]
		payload := RenderPayload(r, p)
		fmt.Fprintf(&b, "Package: %s\n", p.Name)
		fmt.Fprintf(&b, "Version: %s\n", p.Version)
		fmt.Fprintf(&b, "Architecture: %s\n", repoArch(r))
		fmt.Fprintf(&b, "Component: %s\n", comp)
		fmt.Fprintf(&b, "Description: %s\n", p.Desc)
		fmt.Fprintf(&b, "Size: %d\n", p.Size)
		if len(p.Depends) > 0 {
			fmt.Fprintf(&b, "Depends: %s\n", strings.Join(p.Depends, ", "))
		}
		fmt.Fprintf(&b, "SHA256: %s\n", sha256hex([]byte(payload)))
		fmt.Fprintf(&b, "Filename: %s\n", PayloadRel(r, p))
		b.WriteString("\n")
	}
	return b.String()
}

// RenderRelease is the signed metadata for the whole suite: which indexes are
// part of it, what they hash to, who signed it and when. A client that cannot
// match a served index against this file has found a corrupt or tampered
// mirror, which is exactly the diagnosis §11 asks for.
func RenderRelease(r *Repo, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Origin: neohome-packages\n")
	fmt.Fprintf(&b, "Label: %s\n", r.Name)
	fmt.Fprintf(&b, "Suite: %s\n", r.Suite)
	fmt.Fprintf(&b, "Date: %s\n", now.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	if r.Signed {
		fmt.Fprintf(&b, "Signer: %s\n", r.SignKey)
	}
	for _, comp := range r.Comps {
		body := indexBytesFor(r, comp)
		fmt.Fprintf(&b, "SHA256-%s: %s %s\n", strings.ToUpper(comp), sha256hex(body), IndexRel(r, comp))
	}
	return b.String()
}

// indexBytesFor is the published bytes of one component's index.
func indexBytesFor(r *Repo, comp string) []byte { return []byte(RenderIndex(r, comp)) }

// RenderTreeIndex is the readable front page of a tree.
func RenderTreeIndex(r *Repo) string {
	var b strings.Builder
	// The first line is the stable thing a client, a browser or a script can
	// look for: "<host>/<path> index". Everything under it is the same facts
	// the machine-readable metadata carries.
	fmt.Fprintf(&b, "%s index\n", r.URL)
	fmt.Fprintf(&b, "# distro:     %s\n", r.Distro)
	fmt.Fprintf(&b, "# suite:      %s\n", r.Suite)
	fmt.Fprintf(&b, "# components: %s\n", strings.Join(r.Comps, " "))
	fmt.Fprintf(&b, "# metadata:   %s\n", ReleasePath(r))
	fmt.Fprintf(&b, "# signed by:  %s\n", r.SignKey)
	fmt.Fprintf(&b, "# signing key: %s\n", KeyRel(r))
	names := make([]string, 0, len(r.Pkgs))
	for n := range r.Pkgs {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "# packages:   %d\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s\n", n, r.Pkgs[n].Version)
	}
	return b.String()
}

// RenderPayload writes one installable artefact: everything the world needs
// to reproduce the package, in a format a player can read, edit and host.
func RenderPayload(r *Repo, p *VPkg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Package: %s\n", p.Name)
	fmt.Fprintf(&b, "Version: %s\n", p.Version)
	arch := p.Arch
	if arch == "" {
		arch = repoArch(r)
	}
	fmt.Fprintf(&b, "Architecture: %s\n", arch)
	if p.Comp != "" {
		fmt.Fprintf(&b, "Component: %s\n", p.Comp)
	}
	fmt.Fprintf(&b, "Description: %s\n", p.Desc)
	fmt.Fprintf(&b, "Size: %d\n", p.Size)
	if len(p.Depends) > 0 {
		fmt.Fprintf(&b, "Depends: %s\n", strings.Join(p.Depends, ", "))
	}
	if p.Service != nil {
		auto := "no"
		if p.Service.Autostart {
			auto = "yes"
		}
		fmt.Fprintf(&b, "Service: %s; %s; %d; %s; %s; %s; %s; %s\n",
			p.Service.Name, p.Service.Desc, p.Service.Port, p.Service.Proto,
			p.Service.Scope, p.Service.Handler, p.Service.Conf, auto)
	}
	for _, pr := range p.Procs {
		kind := pr.Kind
		if kind == "" {
			kind = "builtin"
		}
		fmt.Fprintf(&b, "Proc: %s; %s; %s; %g; %d; %s\n", pr.Name, pr.Args, pr.User, pr.CPU, pr.Mem, kind)
	}
	if p.PostInst != "" {
		fmt.Fprintf(&b, "Postinst: %s\n", p.PostInst)
	}
	if p.PreRemove != "" {
		fmt.Fprintf(&b, "PreRemove: %s\n", p.PreRemove)
	}
	if p.Malicious {
		b.WriteString("X-Neohome-Flag: suspicious\n")
	}
	// deterministic order: sorted paths
	paths := make([]string, 0, len(p.Files))
	for k := range p.Files {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	for _, path := range paths {
		f := p.Files[path]
		kind := "text"
		if f.Binary {
			kind = "binary"
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0644
		}
		owner, group := f.Owner, f.Group
		if owner == "" {
			owner = "root"
		}
		if group == "" {
			group = "root"
		}
		fmt.Fprintf(&b, "File: %s; %#o; %s; %s; %s\n", path, mode, owner, group, kind)
		if !f.Binary && f.Content != "" {
			b.WriteString("Data:\n")
			b.WriteString(f.Content)
			if !strings.HasSuffix(f.Content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("End\n")
		}
	}
	return b.String()
}

// ---- parsing ----

// IndexEntry is one package as the served index describes it.
type IndexEntry struct {
	Name     string
	Version  string
	Arch     string
	Comp     string
	Desc     string
	Size     int
	Depends  []string
	SHA256   string
	Filename string
}

// ParseIndex reads the world's index format. Unknown keys are kept out of
// the way rather than rejected: real indexes grow fields over time.
func ParseIndex(body string) []IndexEntry {
	var out []IndexEntry
	var cur *IndexEntry
	flush := func() {
		if cur != nil && cur.Name != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if cur == nil {
			cur = &IndexEntry{}
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "package":
			cur.Name = val
		case "version":
			cur.Version = val
		case "architecture":
			cur.Arch = val
		case "component":
			cur.Comp = val
		case "description":
			cur.Desc = val
		case "size":
			cur.Size = atoi(val)
		case "depends":
			cur.Depends = splitDeps(val)
		case "sha256":
			cur.SHA256 = strings.ToLower(val)
		case "filename":
			cur.Filename = val
		}
	}
	flush()
	return out
}

func splitDeps(v string) []string {
	v = strings.ReplaceAll(v, ",", " ")
	var out []string
	for _, f := range strings.Fields(v) {
		// version constraints (>=, <<) are recorded but not resolved against:
		// the world's catalogues do not yet publish versioned dependencies.
		if strings.HasPrefix(f, ">") || strings.HasPrefix(f, "<") || strings.HasPrefix(f, "=") {
			continue
		}
		out = append(out, strings.TrimSuffix(f, ":"))
	}
	return out
}

// ParsePayload reads an installable artefact back into the world's package
// record. This is the only way a package enters a device now: the bytes that
// were served are the bytes that are applied.
func ParsePayload(body string) (*VPkg, error) {
	p := &VPkg{Version: "0", Files: map[string]*PkgFile{}}
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "Package":
			p.Name = val
		case "Version":
			p.Version = val
		case "Architecture":
			p.Arch = val
		case "Component":
			p.Comp = val
		case "Description":
			p.Desc = val
		case "Size":
			p.Size = atoi(val)
		case "Depends":
			p.Depends = splitDeps(val)
		case "Service":
			svc, err := parseServiceLine(val)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %v", p.Name, err)
			}
			p.Service = svc
		case "Proc":
			pr, err := parseProcLine(val)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %v", p.Name, err)
			}
			p.Procs = append(p.Procs, pr)
		case "Postinst":
			p.PostInst = val
		case "PreRemove":
			p.PreRemove = val
		case "X-Neohome-Flag":
			if strings.EqualFold(val, "suspicious") {
				p.Malicious = true
			}
		case "File":
			f, err := parseFileLine(val)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %v", p.Name, err)
			}
			if f.Path != "" {
				p.Files[f.Path] = f.File
			}
		case "Data":
			// the content of the file named by the preceding File: header
			path := lastFilePath(p, lines, i)
			body := []string{}
			for i++; i < len(lines); i++ {
				if strings.TrimRight(lines[i], "\r") == "End" {
					break
				}
				body = append(body, strings.TrimRight(lines[i], "\r"))
			}
			if path != "" && p.Files[path] != nil && !p.Files[path].Binary {
				p.Files[path].Content = strings.Join(body, "\n")
				if len(body) > 0 {
					p.Files[path].Content += "\n"
				}
			}
		}
	}
	if p.Name == "" {
		return nil, fmt.Errorf("payload has no Package: header")
	}
	return p, nil
}

// lastFilePath finds the file header the current Data: block belongs to: the
// most recent File: line in the payload.
func lastFilePath(p *VPkg, lines []string, upto int) string {
	for i := upto - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "File") {
			continue
		}
		f, err := parseFileLine(strings.TrimSpace(val))
		if err != nil {
			return ""
		}
		return f.Path
	}
	return ""
}

type fileHeader struct {
	Path string
	File *PkgFile
}

func parseFileLine(v string) (fileHeader, error) {
	f := strings.Split(v, ";")
	if len(f) < 4 {
		return fileHeader{}, fmt.Errorf("malformed File: %q", v)
	}
	mode := uint32(0644)
	if m, err := strconv.ParseUint(strings.TrimSpace(f[1]), 8, 32); err == nil {
		mode = uint32(m)
	}
	binary := strings.EqualFold(strings.TrimSpace(f[4]), "binary")
	return fileHeader{
		Path: strings.TrimSpace(f[0]),
		File: &PkgFile{
			Mode: mode, Owner: strings.TrimSpace(f[2]), Group: strings.TrimSpace(f[3]),
			Binary: binary,
		},
	}, nil
}

func parseServiceLine(v string) (*SvcSpec, error) {
	f := strings.Split(v, ";")
	if len(f) < 6 {
		return nil, fmt.Errorf("malformed Service: %q", v)
	}
	port := atoi(strings.TrimSpace(f[2]))
	auto := len(f) > 7 && strings.EqualFold(strings.TrimSpace(f[7]), "yes")
	conf := ""
	if len(f) > 6 {
		conf = strings.TrimSpace(f[6])
	}
	return &SvcSpec{
		Name: strings.TrimSpace(f[0]), Desc: strings.TrimSpace(f[1]), Port: port,
		Proto: strings.TrimSpace(f[3]), Scope: strings.TrimSpace(f[4]),
		Handler: strings.TrimSpace(f[5]), Conf: conf, Autostart: auto,
	}, nil
}

func parseProcLine(v string) (ProcSpec, error) {
	f := strings.Split(v, ";")
	if len(f) < 3 {
		return ProcSpec{}, fmt.Errorf("malformed Proc: %q", v)
	}
	cpu := 0.0
	if len(f) > 3 {
		cpu, _ = strconv.ParseFloat(strings.TrimSpace(f[3]), 64)
	}
	mem := 0
	if len(f) > 4 {
		mem = atoi(strings.TrimSpace(f[4]))
	}
	kind := ""
	if len(f) > 5 {
		kind = strings.TrimSpace(f[5])
	}
	return ProcSpec{Name: strings.TrimSpace(f[0]), Args: strings.TrimSpace(f[1]),
		User: strings.TrimSpace(f[2]), CPU: cpu, Mem: mem, Kind: kind}, nil
}

// ---- trust (§11) ----

// PackageError is a failure a package manager reports to the player. The code
// is the distribution's own vocabulary (NO_PUBKEY, EXPKEYSIG, …) and the hint
// is the next thing worth checking — never a solution, never a secret.
type PackageError struct {
	Code string
	Msg  string
	Hint string
}

func (e *PackageError) Error() string { return e.Msg }

func pkgErr(code, format string, args ...interface{}) *PackageError {
	return &PackageError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// VerifySigner checks the metadata's claimed signer against the device's real
// keyring, including key validity: an expired key fails verification even
// though the file is still installed.
func VerifySigner(d *Device, r *Repo, releaseBody string, now time.Time) *PackageError {
	if !r.Signed {
		return nil // an unsigned tree installs without a signature check
	}
	signer := ""
	for _, line := range strings.Split(releaseBody, "\n") {
		if key, val, ok := strings.Cut(strings.TrimRight(line, "\r"), ":"); ok && strings.EqualFold(strings.TrimSpace(key), "Signer") {
			signer = strings.TrimSpace(val)
		}
	}
	if signer == "" {
		return pkgErr("NO_SIGNER", "%s is not signed (no Signer in %s)", r.URL, ReleasePath(r))
	}
	if !KeyIsTrusted(d, signer) {
		return pkgErr("NO_PUBKEY", "the public key %s is not available for verification on %s",
			shortKey(signer), d.Hostname)
	}
	for _, k := range KeyringOf(d) {
		if k.Fingerprint != keyFingerprint(signer) {
			continue
		}
		if k.ValidUntil != "" {
			until, err := time.Parse("2006-01-02", k.ValidUntil)
			if err == nil && now.After(until) {
				return pkgErr("EXPKEYSIG", "the key %s expired on %s", shortKey(signer), k.ValidUntil)
			}
		}
	}
	return nil
}

func shortKey(fp string) string {
	fp = keyFingerprint(fp)
	if len(fp) <= 8 {
		return fp
	}
	return fp[len(fp)-8:]
}

var _ = sort.Strings
