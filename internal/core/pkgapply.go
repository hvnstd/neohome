package core

import (
	"fmt"
	"sort"
	"strings"
)

// Applying packages and taking them away again (§9, §30 permissions).
//
// Installation is a plan, not a loop: the dependency graph is resolved
// against what the box's *cached metadata* says, ordered so dependencies are
// configured first, and every package is fetched as the payload file the
// repository serves — parsed, checksum-verified and then applied. Removal is
// the same graph read backwards: a package that something installed depends
// on cannot silently disappear, and what a package added (files, unit,
// system user, background processes) goes away with it.

// InstallStep is one package in an install plan.
type InstallStep struct {
	Entry   IndexEntry
	View    *RepoView
	Already bool
}

// PlanInstall resolves the dependency closure for the requested package
// names across the views the device can currently install from. It returns
// the steps in configuration order (dependencies first) or a PackageError
// naming what is missing.
func PlanInstall(d *Device, views []*RepoView, want []string) ([]InstallStep, *PackageError) {
	byName := map[string]InstallStep{}
	for _, v := range views {
		for _, e := range v.Entries {
			if _, ok := byName[e.Name]; !ok {
				byName[e.Name] = InstallStep{Entry: e, View: v}
			}
		}
	}
	var order []InstallStep
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(name, needer string) *PackageError
	visit = func(name, needer string) *PackageError {
		if d.Installed[name] != nil {
			if state[name] == 0 {
				state[name] = 2
			}
			return nil
		}
		step, ok := byName[name]
		if !ok {
			if needer == "" {
				return pkgErr("NOT_FOUND", "unable to locate package %s", name)
			}
			return pkgErr("MISSING_DEP", "%s depends on %s, which no configured repository provides", needer, name)
		}
		switch state[name] {
		case 1:
			return pkgErr("DEP_LOOP", "dependency loop involving %s", name)
		case 2:
			return nil
		}
		state[name] = 1
		deps := step.Entry.Depends
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep, name); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, step)
		return nil
	}
	for _, name := range want {
		if err := visit(name, ""); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// FetchPayload downloads one package's artefact from its repository and
// verifies it against the index entry: same name, same version, same bytes.
// Both mismatches are real §11 conditions (包版本异常, 签名/校验不匹配).
func (w *World) FetchPayload(d *Device, step InstallStep) (*VPkg, *PackageError) {
	// the artefact is named relative to the tree root, so it is fetched from
	// the tree's URL — the source line only says which tree this is
	url := "http://" + step.View.Repo.URL + "/" + step.Entry.Filename
	body, err := w.FetchHTTP(d, url)
	if err != nil {
		return nil, err
	}
	if step.Entry.SHA256 != "" {
		if got := sha256hex(body); got != step.Entry.SHA256 {
			return nil, pkgErr("SUM_MISMATCH",
				"%s: the downloaded file does not match the index (expected %s, got %s)",
				step.Entry.Name, shortHash(step.Entry.SHA256), shortHash(got))
		}
	}
	p, perr := ParsePayload(string(body))
	if perr != nil {
		return nil, pkgErr("BAD_PAYLOAD", "%s: %v", step.Entry.Name, perr)
	}
	if p.Name != step.Entry.Name || p.Version != step.Entry.Version {
		return nil, pkgErr("VERSION_MISMATCH",
			"the index offers %s %s but the payload on the mirror is %s %s",
			step.Entry.Name, step.Entry.Version, p.Name, p.Version)
	}
	return p, nil
}

// InstalledPkg is one package that really landed, with what it changed.
type InstalledPkg struct {
	Name    string
	Version string
	Actions []string
}

// InstallFromView installs every step of a plan, dependency first, stopping at
// the first failure and reporting what was already applied — the honest state
// of a half-finished install.
func (w *World) InstallFromView(d *Device, steps []InstallStep) ([]InstalledPkg, *PackageError) {
	var done []InstalledPkg
	for _, step := range steps {
		if d.Installed[step.Entry.Name] != nil {
			continue
		}
		// §36: a FAILED disk fails the unpack before room is even asked about.
		// The error is the disk's own EIO; the shell renders it in each
		// manager's voice (mgrErr), the way the real tools report the same
		// errno. Removing files does not help here — fsck buys time, a backup
		// saves the data.
		if d.DiskFailed() {
			_, why := d.DiskHealth()
			return done, pkgErr("IOERROR",
				"cannot unpack %s: input/output error (disk FAILED: %s)",
				step.Entry.Name, why)
		}
		// §17: the unpack needs room before it needs anything else. The error
		// is the filesystem's own; the shell renders it in each manager's
		// voice (mgrErr), the way the real tools report the same errno.
		if need := (step.Entry.Size + 1023) / 1024; need > 0 && d.DiskFreeMB() < need {
			return done, pkgErr("NOSPACE",
				"cannot unpack %s: no space left on device (%d MiB needed, %d MiB free)",
				step.Entry.Name, need, d.DiskFreeMB())
		}
		p, err := w.FetchPayload(d, step)
		if err != nil {
			if len(done) == 0 {
				return nil, err
			}
			var names []string
			for _, d := range done {
				names = append(names, d.Name)
			}
			return done, pkgErr("PARTIAL_INSTALL", "%v (installed so far: %s)", err, strings.Join(names, ", "))
		}
		// provenance: which repository this really came from. Two repositories
		// can publish a package with the same name, so it is recorded at the
		// moment of install rather than guessed from the catalogue later.
		if d.InstalledFrom == nil {
			d.InstalledFrom = map[string]string{}
		}
		d.InstalledFrom[p.Name] = step.View.Repo.Name
		done = append(done, InstalledPkg{Name: p.Name, Version: p.Version, Actions: w.ApplyPackage(d, p)})
	}
	return done, nil
}

// ApplyPackage applies an already-parsed payload to a device, recording it as
// installed. This is the single path by which software enters a device in
// this world: players, the assistant and seeds all end up here.
func (w *World) ApplyPackage(d *Device, p *VPkg) []string {
	n := len(d.Procs)
	actions := d.InstallPkg(p)
	if len(d.Procs) > n {
		// the package declared background processes: name them in the action
		// list so the player sees them without running ps
		for _, pr := range d.Procs[n:] {
			actions = append(actions, "started "+pr.Name+" ("+pr.User+")")
		}
	}
	return actions
}

// InstallRendered installs a catalogue definition by rendering it first and
// applying the bytes — the path the world's own seed and the assistant use, so
// even they cannot bypass the payload format.
func (w *World) InstallRendered(d *Device, r *Repo, p *VPkg) ([]string, error) {
	parsed, err := ParsePayload(RenderPayload(r, p))
	if err != nil {
		return nil, err
	}
	actions := w.ApplyPackage(d, parsed)
	if d.InstalledFrom == nil {
		d.InstalledFrom = map[string]string{}
	}
	d.InstalledFrom[parsed.Name] = r.Name
	return actions, nil
}

// InstallCommunity installs an unsigned payload from a file: the community
// path. Dependencies must already be present (like dpkg -i: no fetching),
// provenance records the file it came from, and the world logs the unsigned
// install the way it logs every other third-party risk (§11). Same apply
// path as everything else, so removal and services behave identically.
func (w *World) InstallCommunity(d *Device, u *User, p *VPkg, file string) ([]string, error) {
	if p == nil || p.Name == "" {
		return nil, fmt.Errorf("empty package")
	}
	if d.Installed[p.Name] != nil {
		return nil, fmt.Errorf("%s is already installed", p.Name)
	}
	for _, dep := range p.Depends {
		name := dep
		if i := strings.IndexAny(name, " ("); i >= 0 {
			name = strings.TrimSpace(name[:i])
		}
		if d.Installed[name] == nil {
			return nil, pkgErr("MISSING_DEP", "%s depends on %s, which is not installed", p.Name, name)
		}
	}
	if d.InstalledFrom == nil {
		d.InstalledFrom = map[string]string{}
	}
	d.InstalledFrom[p.Name] = "community:" + file
	actions := w.ApplyPackage(d, p)
	d.Logf("warn", "pkg", "installed unsigned community package %s %s from %s", p.Name, p.Version, file)
	w.AddEvent(d.ID, "warn", "pkg", "%s installed unsigned %s from %s", d.Hostname, p.Name, file)
	out := []string{"WARNING: " + p.Name + " is unsigned (community package, no signature check possible)"}
	return append(out, actions...), nil
}

// ---- removal ----

// RemoveCheck answers whether a package can be removed: something else
// installed that declares a dependency on it is a real refusal.
func RemoveCheck(d *Device, name string) *PackageError {
	if d.Installed[name] == nil {
		return pkgErr("NOT_INSTALLED", "package %s is not installed", name)
	}
	var breakers []string
	names := make([]string, 0, len(d.Installed))
	for n := range d.Installed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == name {
			continue
		}
		for _, dep := range d.Installed[n].Depends {
			if dep == name {
				breakers = append(breakers, n)
			}
		}
	}
	if len(breakers) > 0 {
		return pkgErr("BREAKS", "removing %s would break %s", name, strings.Join(breakers, ", "))
	}
	return nil
}

// RemovePkg takes a package out of the world: its processes stop, its unit is
// deregistered, files it owns are deleted unless another installed package
// uses them, and the system account it created goes away with it.
func (w *World) RemovePkg(d *Device, p *VPkg) []string {
	var actions []string
	// stop the processes it declared, by name — the same names InstallPkg
	// started, so nothing is left running that was not asked for again
	procs := map[string]bool{}
	if p != nil {
		for _, pr := range p.Procs {
			procs[pr.Name] = true
		}
	}
	before := len(d.Procs)
	kept := d.Procs[:0]
	for _, proc := range d.Procs {
		if procs[proc.Name] || (p != nil && p.Malicious && proc.Name == "updater") {
			continue
		}
		kept = append(kept, proc)
	}
	d.Procs = kept
	if stopped := before - len(d.Procs); stopped > 0 {
		actions = append(actions, fmt.Sprintf("stopped %d process(es)", stopped))
	}
	if p != nil && p.Service != nil {
		if svc := d.Svc(p.Service.Name); svc != nil {
			if _, err := d.StopService(p.Service.Name); err == nil {
				delete(d.Services, p.Service.Name)
				actions = append(actions, "deregistered unit "+p.Service.Name+".service")
			}
		}
	}
	if p != nil && p.PreRemove != "" {
		actions = append(actions, "prerm: "+p.PreRemove)
	}
	// files: a path survives if any other installed package declares it
	owned := map[string]bool{}
	for n, other := range d.Installed {
		if n == p.Name || other == nil {
			continue
		}
		for path := range other.Files {
			owned[path] = true
		}
	}
	paths := make([]string, 0, len(p.Files))
	for path := range p.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if owned[path] {
			continue
		}
		if d.FS.Remove(path) {
			actions = append(actions, "removed "+path)
		}
	}
	// system accounts the package created, unless another package claims them
	for path := range p.Files {
		if !strings.HasPrefix(path, "/etc/passwd.d/") {
			continue
		}
		name := strings.TrimPrefix(path, "/etc/passwd.d/")
		claimed := false
		for n, other := range d.Installed {
			if n == p.Name || other == nil {
				continue
			}
			if other.Files[path] != nil {
				claimed = true
			}
		}
		if !claimed {
			if u := d.Users[name]; u != nil && u.UID != 0 {
				delete(d.Users, name)
				actions = append(actions, "removed system user "+name)
			}
		}
	}
	delete(d.Installed, p.Name)
	// provenance goes with the package: a removed package has no origin
	if d.InstalledFrom != nil {
		delete(d.InstalledFrom, p.Name)
	}
	refreshPasswd(d)
	w.AddEvent(d.ID, "info", "pkg", "removed %s %s from %s", p.Name, p.Version, d.Hostname)
	return actions
}

// UpsertProcs adds the background processes a package declares. Kept separate
// so the engine's install path stays readable.
func (d *Device) UpsertProcs(specs []ProcSpec) {
	for _, s := range specs {
		kind := s.Kind
		if kind == "" {
			kind = "builtin"
		}
		d.AddProc(&Proc{Name: s.Name, Args: s.Args, User: s.User, CPU: s.CPU, Mem: s.Mem,
			TTY: "?", State: "R", Kind: kind, Start: d.W.Sim})
	}
}
