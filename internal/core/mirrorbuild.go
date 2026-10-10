package core

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// Mirror builder (Phase 3, §38) — adopt a tree, reuse the real sync
//
// The serving model in this world is "one tree served by one host at its
// document root" (Repo.DeviceID), so a second copy of a served tree is a
// different repo, not another mount of the same one. What a household
// operator therefore builds — and what the spec's builder asks for — is
// reach: giving this machine more trees to serve. Adopting retargets an
// existing repository's DeviceID to this machine, marks the archive as its
// upstream, and starts a real sync through the machinery that already
// exists (StartMirrorSync / mirror-sync). Nothing new is invented: no second
// copy of the tree, no second trust domain, no fake catalogue entries.
//
// What is deliberately NOT here: turning the PC into an extra origin (see
// workstream notes) — that needs a per-device catalogue, which is a model
// change, not a verb.
// ---------------------------------------------------------------------------

// AdoptRepo moves tree r to be served by host, with the archive as its
// upstream. The tree must not already belong to another machine, and the
// host must run a web server able to serve it.
func (w *World) AdoptRepo(host *Device, name string) (*Repo, error) {
	r := w.Repos[name]
	if r == nil {
		return nil, fmt.Errorf("no repository called %s", name)
	}
	if r.DeviceID == host.ID {
		return nil, fmt.Errorf("%s is already served by %s", name, host.Hostname)
	}
	if r.DeviceID != "" && r.DeviceID != r.UpstreamID {
		return nil, fmt.Errorf("%s is served by another machine", name)
	}
	serves := false
	for _, s := range host.Services {
		if s.Port == 80 || s.Port == 443 {
			serves = true
		}
	}
	if !serves {
		return nil, fmt.Errorf("%s runs no web server", host.Hostname)
	}
	// the archive is where this tree comes from: the upstream the sync will
	// actually pull from
	archive := w.Devices["archive"]
	if archive == nil {
		return nil, fmt.Errorf("this world has no archive host")
	}
	r.DeviceID = host.ID
	r.UpstreamID = archive.ID
	if r.ReleaseHash == nil {
		r.ReleaseHash = map[string]string{}
	}
	host.Logf("info", "mirror", "adopted repository %s from %s (upstream %s)", name, r.Path, archive.Hostname)
	w.AddEvent(host.ID, "info", "mirror", "%s now serves %s", host.Hostname, name)
	return r, nil
}
