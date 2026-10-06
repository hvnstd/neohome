package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Fetching, verification and mirroring.
//
// Everything here goes through the same network stack the rest of the world
// uses: name resolution, Dial, power, firewall, service state. A package
// manager is therefore not a special case — it is a client (spec §39).
//
// The mirror is a real node with a real sync process, so sync status is a
// fact about the world: SYNCED means the bytes on the mirror match the signed
// metadata, BEHIND means the tree is stale, PARTIAL means a component the
// upstream no longer carries, CORRUPTED means the mirror's files no longer
// match the release file that is supposed to describe them, and OFFLINE means
// the last sync could not reach either end.

// BehindThreshold is how stale a repository may get before the world calls it
// BEHIND. It is sim time, and the seeded mirror starts three days behind, so
// nothing about this is a wait.
const BehindThreshold = 6 * time.Hour

// ---- serving files over HTTP ----

// WebRoot is the document root a device's web service publishes.
func WebRoot(d *Device) string {
	for _, name := range sortedServiceNames(d) {
		svc := d.Services[name]
		switch svc.Handler {
		case "http-mirror":
			return "/srv/www/mirror"
		case "http-archive":
			return "/srv/www/archive"
		case "http-repo":
			return "/srv/www/repo"
		case "http-user":
			return "/srv/www/html"
		}
	}
	return ""
}

func sortedServiceNames(d *Device) []string {
	out := make([]string, 0, len(d.Services))
	for n := range d.Services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ServeFile maps a URL path to a real file on the serving device. A directory
// produces a listing, like any web server; a missing path produces nothing,
// and the caller reports 404 honestly.
func ServeFile(d *Device, urlPath string) ([]byte, bool) {
	root := WebRoot(d)
	if root == "" {
		return nil, false
	}
	p := root + "/" + strings.TrimPrefix(urlPath, "/")
	p = strings.TrimSuffix(p, "/")
	if p == root && isMirrorHost(d) {
		// A mirror's front page is an index of what it mirrors — a real one
		// lists the trees a client can point at, with their state.
		return []byte(RenderMirrorIndex(d)), true
	}
	if data, ok := d.FS.Read(p); ok {
		return data, true
	}
	if d.FS.IsDir(p) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s — directory listing\n", urlPath)
		for _, child := range d.FS.List(p) {
			if d.FS.IsDir(child) {
				b.WriteString(strings.TrimPrefix(child, p+"/") + "/\n")
				continue
			}
			size := 0
			if n, ok := d.FS.Get(child); ok {
				size = len(n.Data)
			}
			fmt.Fprintf(&b, "%-40s %8d\n", strings.TrimPrefix(child, p+"/"), size)
		}
		return []byte(b.String()), true
	}
	return nil, false
}

// isMirrorHost reports whether this device's web service publishes mirrored
// package trees (as opposed to a user's own site).
func isMirrorHost(d *Device) bool {
	for _, name := range sortedServiceNames(d) {
		if d.Services[name].Handler == "http-mirror" {
			return true
		}
	}
	return false
}

// RenderMirrorIndex is the front page of a mirror: which trees it serves, in
// what state, and where a client should point at. It is generated from the
// same repository facts the machine-readable metadata uses.
func RenderMirrorIndex(d *Device) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s index\n", d.Hostname)
	fmt.Fprintf(&b, "# trees served from %s\n", WebRoot(d))
	if d.W == nil {
		return b.String()
	}
	names := make([]string, 0, len(d.W.Repos))
	for n, r := range d.W.Repos {
		if r.DeviceID == d.ID {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		r := d.W.Repos[n]
		fmt.Fprintf(&b, "%-10s %-8s %-10s last sync %s\n", n, r.Suite, r.Status, HumanAge(d.W.Sim.Sub(r.LastSync)))
	}
	return b.String()
}

// FetchHTTP performs a real http GET from a device: resolve the name through
// the device's own resolver chain, dial the host's port through the real
// firewall/NAT rules, then read the served file. Every failure is reported in
// the vocabulary the caller needs to explain it.
func (w *World) FetchHTTP(from *Device, rawURL string) ([]byte, *PackageError) {
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host, path := rest, "/"
	if i := strings.Index(rest, "/"); i >= 0 {
		host, path = rest[:i], rest[i:]
	}
	if host == "" {
		return nil, pkgErr("BAD_URL", "malformed URL %q", rawURL)
	}
	ip, ok, how := DNSAnswer(from, host)
	if !ok {
		return nil, pkgErr("NOT_FOUND", "could not resolve '%s': %s", host, how)
	}
	svc, dst, msg := Dial(from, ip, 80)
	if svc == nil {
		return nil, pkgErr("UNREACHABLE", "could not connect to %s (%s): %s", host, ip, msg)
	}
	if dst == nil {
		return nil, pkgErr("UNREACHABLE", "could not connect to %s (%s): no such host", host, ip)
	}
	body, served := ServeFile(dst, path)
	if !served {
		return nil, pkgErr("404", "%s: %s not found on %s", rawURL, path, host)
	}
	return body, nil
}

// IsMirrored reports whether this repository is a copy that syncs from an
// upstream archive (as opposed to a tree the host publishes itself).
func (r *Repo) IsMirrored() bool {
	return r.UpstreamID != "" && r.DeviceID != "" && r.UpstreamID != r.DeviceID
}

// ---- repository access from a device ----

// RepoView is a repository as one device sees it: which components were
// actually read, the entries they carry, and the warnings the world's facts
// justify (stale tree, missing component).
type RepoView struct {
	Repo     *Repo
	Source   Source
	Comps    []string
	Entries  []IndexEntry
	Warnings []string
	// Cached marks a view read from the device's own lists directory rather
	// than freshly downloaded: installing uses cached lists, exactly like a
	// real box.
	Cached bool
	// Signed means the release file was verified against the device keyring.
	Signed bool
}

// EntriesByName indexes the view.
func (v *RepoView) EntriesByName() map[string]IndexEntry {
	m := map[string]IndexEntry{}
	for _, e := range v.Entries {
		m[e.Name] = e
	}
	return m
}

// cachedIndexPath is where one component's index is cached on the device.
func cachedIndexPath(d *Device, src Source, comp string) string {
	spec := DistroFor(d)
	base := baseName(IndexRel(src.Repo, comp))
	slug := strings.NewReplacer("/", "_", ":", "_").Replace(src.URL)
	return fmt.Sprintf("%s/%s_%s_%s", spec.Lists, slug, comp, base)
}

// cachedReleasePath is where the verified release file is kept.
func cachedReleasePath(d *Device, src Source) string {
	spec := DistroFor(d)
	slug := strings.NewReplacer("/", "_", ":", "_").Replace(src.URL)
	return fmt.Sprintf("%s/%s_%s", spec.Lists, slug, baseName(ReleaseRel(src.Repo)))
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// UpdateRepo is `apt update` for one source line: it really reads the served
// metadata, verifies the signing key and the index hashes, and only then
// caches the lists the installer will later use. A source that cannot be
// reached caches nothing — that is why a stale box cannot install.
func (w *World) UpdateRepo(d *Device, src Source) (*RepoView, *PackageError) {
	r := src.Repo
	if r == nil {
		return nil, pkgErr("UNKNOWN_SOURCE", "%s %s %s — no mirror in this world serves that URL",
			"Source", src.Line, "")
	}
	view := &RepoView{Repo: r, Source: src}

	// The released metadata first: it names the components and their hashes.
	// Paths are relative to the tree root, because a source line may name a
	// directory inside the tree (OpenWrt feeds, Arch sections) or a different
	// host entirely (a third-party repository) — the files are always found
	// where the tree's own URL says they are.
	base := "http://" + r.URL
	releaseBody, err := w.FetchHTTP(d, base+"/"+ReleaseRel(r))
	if err != nil {
		// explain the repository's stored state when it is the real cause
		switch r.Status {
		case "OFFLINE":
			return nil, pkgErr("OFFLINE", "%s is offline: %s", r.URL, r.StatusWhy)
		case "CORRUPTED":
			return nil, pkgErr("CORRUPTED", "%s is corrupt: %s", r.URL, r.StatusWhy)
		}
		err.Hint = "check the mirror host and the network path to it"
		return nil, err
	}
	if perr := VerifySigner(d, r, string(releaseBody), w.Sim); perr != nil {
		perr.Hint = "add the repository's signing key to " + DistroFor(d).Keyring
		return nil, perr
	}
	view.Signed = r.Signed

	// components: what the source asked for, minus what upstream dropped
	wantComps := r.CompsFor(src)
	missing := map[string]bool{}
	for _, m := range r.Missing {
		missing[m] = true
	}
	for _, comp := range wantComps {
		if missing[comp] {
			view.Warnings = append(view.Warnings,
				fmt.Sprintf("component '%s' is not available on this mirror (%s)", comp, r.Status))
			continue
		}
		body, err := w.FetchHTTP(d, base+"/"+IndexRel(r, comp))
		if err != nil {
			if err.Code == "404" {
				view.Warnings = append(view.Warnings,
					fmt.Sprintf("component '%s' is listed in the release file but missing from this mirror", comp))
				continue
			}
			return nil, err
		}
		// hash check: the release file says what this index must be
		want := releaseHashFor(string(releaseBody), comp)
		if want == "" {
			return nil, pkgErr("BAD_METADATA", "%s: the release file does not cover component '%s'", r.URL, comp)
		}
		if got := sha256hex(body); got != want {
			return nil, pkgErr("CORRUPTED",
				"hash sum mismatch for '%s': the release file claims %s but the mirror serves %s",
				comp, shortHash(want), shortHash(got))
		}
		d.FS.MkdirAll(DistroFor(d).Lists, 0755, "root", "root")
		d.FS.WriteBytes(cachedIndexPath(d, src, comp), body, 0644, "root", "root")
		view.Comps = append(view.Comps, comp)
		view.Entries = append(view.Entries, ParseIndex(string(body))...)
	}
	d.FS.WriteBytes(cachedReleasePath(d, src), releaseBody, 0644, "root", "root")

	// freshness: a tree that is behind still installs, and says so
	if age := w.Sim.Sub(r.LastSync); r.LastSync.IsZero() || age > BehindThreshold {
		view.Warnings = append(view.Warnings, fmt.Sprintf(
			"repository '%s' is stale (BEHIND): last successful sync %s ago",
			r.Suite, HumanAge(age)))
	}
	return view, nil
}

// LoadCachedRepo reads the lists a previous `update` verified. No network is
// touched: this is the state the box is currently able to install from.
func (w *World) LoadCachedRepo(d *Device, src Source) (*RepoView, *PackageError) {
	r := src.Repo
	if r == nil {
		return nil, pkgErr("UNKNOWN_SOURCE", "no mirror serves %s", src.Line)
	}
	spec := DistroFor(d)
	view := &RepoView{Repo: r, Source: src, Cached: true}
	missing := map[string]bool{}
	for _, m := range r.Missing {
		missing[m] = true
	}
	found := false
	for _, comp := range r.CompsFor(src) {
		if missing[comp] {
			view.Warnings = append(view.Warnings, fmt.Sprintf("component '%s' is not available on this mirror (%s)", comp, r.Status))
			continue
		}
		data, ok := d.FS.Read(cachedIndexPath(d, src, comp))
		if !ok {
			continue
		}
		found = true
		view.Comps = append(view.Comps, comp)
		view.Entries = append(view.Entries, ParseIndex(string(data))...)
	}
	if !found {
		return nil, pkgErr("NOLISTS", "no package lists cached for %s", src.URL)
	}
	if _, ok := d.FS.Read(cachedReleasePath(d, src)); ok {
		view.Signed = r.Signed
	}
	_ = spec
	return view, nil
}

func releaseHashFor(releaseBody, comp string) string {
	want := ""
	for _, line := range strings.Split(releaseBody, "\n") {
		key, val, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "SHA256-"+comp) {
			f := strings.Fields(val)
			if len(f) > 0 {
				want = strings.ToLower(f[0])
			}
		}
	}
	return want
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// humanAge renders a sim duration the way a log line would.
func HumanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// ---- the mirror's sync ----

// StartMirrorSync starts a real sync: a process on the mirror host that the
// engine advances. It can be started by the mirror's cron line or by a player
// with root on the box, and it can be killed like any other process — which
// is precisely how a mirror ends up CORRUPTED.
func (w *World) StartMirrorSync(r *Repo) *PackageError {
	if r == nil {
		return pkgErr("NO_SUCH_REPO", "no such repository")
	}
	if r.SyncPhase != 0 {
		return pkgErr("SYNC_RUNNING", "%s is already syncing", r.Name)
	}
	d := w.Devices[r.DeviceID]
	if d == nil {
		return pkgErr("NO_MIRROR", "%s has no mirror host", r.Name)
	}
	if !d.Powered() {
		r.Status = "OFFLINE"
		r.StatusWhy = d.Hostname + " is not powered"
		w.AddEvent(d.ID, "warn", "mirror", "%s sync failed: host is down", r.Name)
		return pkgErr("OFFLINE", "%s: %s", r.Name, r.StatusWhy)
	}
	d.AddProc(&Proc{Name: "mirror-sync", Args: r.Distro + "/" + r.Suite, User: "root",
		CPU: 12, Mem: 64, TTY: "?", State: "R", Kind: "task", Start: w.Sim})
	r.SyncPhase = 1
	r.SyncDirty = false
	d.Logf("info", "mirror", "sync started for %s (%s)", r.Name, r.Suite)
	return nil
}

// MirrorTick advances running syncs and refreshes every tree's state. Called
// from World.Tick(); never sleeps, never guesses.
//
// State is derived from what the mirror actually serves, not from what a sync
// intended: CORRUPTED means the release file does not describe the indexes on
// that host, BEHIND means the only complete copy is older than the threshold.
// A sync that dies having written bytes that still match its release file has
// not corrupted anything, and the world does not pretend otherwise.
func (w *World) MirrorTick() {
	names := make([]string, 0, len(w.Repos))
	for n := range w.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := w.Repos[n]
		if r.SyncPhase > 0 {
			w.mirrorSyncStep(r)
			continue
		}
		if r.Status == "OFFLINE" {
			continue // a failed sync owns this state until a new sync runs
		}
		if perr := w.RepoIntegrity(r); perr != nil {
			r.Status = "CORRUPTED"
			r.StatusWhy = perr.Msg
			continue
		}
		if len(r.Missing) > 0 {
			r.Status = "PARTIAL"
			r.StatusWhy = "upstream no longer carries: " + strings.Join(r.Missing, ", ")
			continue
		}
		if age := w.Sim.Sub(r.LastSync); r.LastSync.IsZero() || age > BehindThreshold {
			r.Status = "BEHIND"
			r.StatusWhy = "last successful sync was " + HumanAge(age) + " ago" + w.disabledSyncHint(r)
			continue
		}
		r.Status = "SYNCED"
		r.StatusWhy = ""
	}
}

// disabledSyncHint distinguishes "nobody has synced this tree" from "somebody
// commented the sync line out": the difference between forgetting and
// switching something off, and the whole reason the seeded debian tree is
// stale. It reads the mirror host's real crontab.
func (w *World) disabledSyncHint(r *Repo) string {
	d := w.Devices[r.DeviceID]
	if d == nil {
		return ""
	}
	data, ok := d.FS.Read(CronSpoolDir + "/root")
	if !ok {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") && strings.Contains(t, "mirror-sync "+r.Name) {
			return "; the sync line for this tree is disabled on " + d.Hostname
		}
	}
	return ""
}

// RepoIntegrity checks the served copy of a tree against its own signed
// release file: the same verification a client performs, run on the host that
// serves it. A nil error means the bytes on this mirror are self-consistent.
func (w *World) RepoIntegrity(r *Repo) *PackageError {
	d := w.Devices[r.DeviceID]
	if d == nil {
		return pkgErr("NO_MIRROR", "%s has no mirror host", r.Name)
	}
	if !d.Powered() {
		return pkgErr("OFFLINE", "%s is not powered", d.Hostname)
	}
	body, ok := d.FS.Read(servedPath(d, ReleasePath(r)))
	if !ok {
		return pkgErr("CORRUPTED", "%s: no release file at %s", r.Name, ReleasePath(r))
	}
	return verifyReleaseAgainstTree(d, r, body)
}

// syncProcFor finds the sync process on a repository's mirror host.
func syncProcFor(d *Device, r *Repo) *Proc {
	for _, p := range d.Procs {
		if p.Name == "mirror-sync" {
			return p
		}
	}
	return nil
}

func (w *World) mirrorSyncStep(r *Repo) {
	d := w.Devices[r.DeviceID]
	if d == nil || !d.Powered() {
		w.finishSyncInterrupted(r, "the mirror host went down mid-sync")
		return
	}
	if syncProcFor(d, r) == nil {
		w.finishSyncInterrupted(r, "the sync process was killed mid-run")
		return
	}
	r.SyncPhase++
	switch r.SyncPhase {
	case 2:
		// copy the indexes first: this is the window that makes an
		// interrupted sync visible as a hash mismatch
		if perr := w.mirrorWriteIndexes(r); perr != nil {
			w.killSyncProc(d)
			r.SyncPhase = 0
			r.Status = "OFFLINE"
			r.StatusWhy = perr.Msg
			d.Logf("warn", "mirror", "sync failed for %s: %s", r.Name, perr.Msg)
			w.AddEvent(d.ID, "warn", "mirror", "sync failed for %s: %s", r.Name, perr.Msg)
			return
		}
		r.SyncDirty = true
	case 3:
		// and the signed release last, copied verbatim from the archive
		if perr := w.mirrorWriteRelease(r); perr != nil {
			w.killSyncProc(d)
			r.SyncPhase = 0
			r.Status = "OFFLINE"
			r.StatusWhy = perr.Msg
			d.Logf("warn", "mirror", "sync failed for %s: %s", r.Name, perr.Msg)
			return
		}
		w.killSyncProc(d)
		r.SyncPhase = 0
		r.SyncDirty = false
		r.LastSync = w.Sim
		r.Status = "SYNCED"
		r.StatusWhy = ""
		d.Logf("info", "mirror", "%s synced: suite %s, components %s", r.Name, r.Suite, strings.Join(r.Comps, " "))
		w.AddEvent(d.ID, "info", "mirror", "%s is in sync (suite %s)", r.Name, r.Suite)
	}
}

func (w *World) killSyncProc(d *Device) {
	for i := 0; i < len(d.Procs); i++ {
		if d.Procs[i].Name == "mirror-sync" {
			d.Procs = append(d.Procs[:i], d.Procs[i+1:]...)
			return
		}
	}
}

// finishSyncInterrupted records what an interrupted sync really left behind,
// by checking the bytes rather than assuming the worst: an interrupted copy is
// only damage if the release file no longer describes what is on the disk.
func (w *World) finishSyncInterrupted(r *Repo, why string) {
	d := w.Devices[r.DeviceID]
	r.SyncPhase = 0
	dirty := r.SyncDirty
	r.SyncDirty = false
	if perr := w.RepoIntegrity(r); perr != nil {
		r.Status = "CORRUPTED"
		r.StatusWhy = why + "; " + perr.Msg
	} else if dirty {
		// the copy was cut short, but what it left behind still matches its
		// signature: the tree is consistent, just not fresh
		r.Status = "BEHIND"
		r.StatusWhy = why + "; the files written so far still match the release file"
	} else {
		r.Status = "BEHIND"
		r.StatusWhy = why + "; no files were changed"
	}
	if d != nil {
		d.Logf("warn", "mirror", "%s sync interrupted: %s", r.Name, why)
		w.AddEvent(d.ID, "warn", "mirror", "%s sync interrupted: %s", r.Name, why)
	}
}

// mirrorWriteIndexes copies each component index from the archive host. A
// component the archive no longer carries is removed here and recorded, which
// is what PARTIAL means.
func (w *World) mirrorWriteIndexes(r *Repo) *PackageError {
	d := w.Devices[r.DeviceID]
	up := w.Devices[r.UpstreamID]
	if d == nil || up == nil {
		return pkgErr("NO_UPSTREAM", "%s has no upstream archive", r.Name)
	}
	upURL := "http://" + hostOf(up) + "/" + r.Path
	missing := []string{}
	for _, comp := range r.Comps {
		rel := IndexRel(r, comp)
		body, err := w.FetchHTTP(d, upURL+"/"+rel)
		if err != nil {
			if err.Code == "404" {
				// upstream dropped this component: the mirror must not keep
				// serving a stale copy of it
				d.FS.Remove(servedPath(d, IndexPath(r, comp)))
				missing = append(missing, comp)
				continue
			}
			return err
		}
		dst := servedPath(d, IndexPath(r, comp))
		d.FS.MkdirAll(parentDir(dst), 0755, "root", "root")
		d.FS.WriteBytes(dst, body, 0644, "root", "root")
		// and the artefacts that index points at: a mirror that copies
		// metadata but not the pool serves an index nobody can install from
		for _, e := range ParseIndex(string(body)) {
			if e.Filename == "" {
				continue
			}
			pdst := servedPath(d, r.Path+"/"+e.Filename)
			if _, ok := d.FS.Read(pdst); ok {
				continue
			}
			payload, perr := w.FetchHTTP(d, upURL+"/"+e.Filename)
			if perr != nil {
				return pkgErr("POOL_MISSING",
					"%s: the index lists %s but the archive does not serve %s",
					r.Name, e.Name, e.Filename)
			}
			if want := e.SHA256; want != "" && sha256hex(payload) != want {
				return pkgErr("POOL_MISMATCH",
					"%s: %s on the archive does not match the index (expected %s)",
					r.Name, e.Filename, shortHash(want))
			}
			d.FS.MkdirAll(parentDir(pdst), 0755, "root", "root")
			d.FS.WriteBytes(pdst, payload, 0644, "root", "root")
		}
	}
	r.Missing = missing
	if len(missing) > 0 {
		r.Status = "PARTIAL"
		r.StatusWhy = "upstream no longer carries: " + strings.Join(missing, ", ")
	} else if r.Status == "PARTIAL" {
		r.Status = "SYNCED"
	}
	return nil
}

// mirrorWriteRelease copies the signed release file itself: mirrors do not
// sign, they republish the archive's signature.
func (w *World) mirrorWriteRelease(r *Repo) *PackageError {
	d := w.Devices[r.DeviceID]
	up := w.Devices[r.UpstreamID]
	if d == nil || up == nil {
		return pkgErr("NO_UPSTREAM", "%s has no upstream archive", r.Name)
	}
	upURL := "http://" + hostOf(up) + "/" + r.Path
	body, err := w.FetchHTTP(d, upURL+"/"+ReleaseRel(r))
	if err != nil {
		return err
	}
	// the release must describe the index bytes actually on this mirror
	if perr := verifyReleaseAgainstTree(d, r, body); perr != nil {
		return perr
	}
	d.FS.WriteBytes(servedPath(d, ReleasePath(r)), body, 0644, "root", "root")
	return nil
}

// servedPath is where a repository file lives on the host that serves it: the
// tree's paths are relative to the document root, so the document root is
// always prefixed. Without this the sync wrote into a path that only existed
// relative to nothing at all.
func servedPath(d *Device, rel string) string {
	return WebRoot(d) + "/" + rel
}

// verifyReleaseAgainstTree refuses to republish a release file that does not
// match the indexes on this mirror: that check is what makes a half-finished
// sync fail loudly instead of quietly serving tampered metadata.
func verifyReleaseAgainstTree(d *Device, r *Repo, releaseBody []byte) *PackageError {
	for _, comp := range r.Comps {
		want := releaseHashFor(string(releaseBody), comp)
		if want == "" {
			return pkgErr("BAD_METADATA", "release file for %s does not cover component '%s'", r.Name, comp)
		}
		body, ok := d.FS.Read(servedPath(d, IndexPath(r, comp)))
		if !ok {
			if listContains(r.Missing, comp) {
				continue // upstream dropped it, and the mirror removed it on purpose
			}
			return pkgErr("CORRUPTED", "%s: index for '%s' is missing from the mirror", r.Name, comp)
		}
		if got := sha256hex(body); got != want {
			return pkgErr("CORRUPTED", "%s: index for '%s' does not match the release file (claims %s, has %s)",
				r.Name, comp, shortHash(want), shortHash(got))
		}
	}
	return nil
}

func hostOf(d *Device) string {
	if d.Hostname != "" {
		return d.Hostname
	}
	return d.ID
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}

func listContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
