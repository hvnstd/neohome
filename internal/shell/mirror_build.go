package shell

import (
	"fmt"

	"neohome/internal/core"
)

// mirror build (Phase 3) — adopt a tree this machine will serve, then let
// the real sync fill it. Executing means: re-point the repository at this
// host, name the archive as upstream, and start the sync the mirror's own
// cron uses. No fake catalogue, no invented state.

func init() {}

// cmdMirrorBuild handles `mirror build TREE`: a subcommand of the existing
// mirror-sync family rather than a new top-level verb, so a machine that
// has no web server answers with the reason instead of a usage wall.
func cmdMirrorBuild(s *Shell, args []string) (*core.Repo, int) {
	if len(args) != 1 {
		s.errf("usage: mirror-sync build TREE")
		return nil, 1
	}
	d := s.Dev
	serves := false
	for _, svc := range d.Services {
		if svc.Port == 80 || svc.Port == 443 {
			serves = true
		}
	}
	if !serves {
		s.errf("mirror-sync: %s runs no web server — install nginx first", d.Hostname)
		return nil, 1
	}
	r, err := s.W.AdoptRepo(d, args[0])
	if err != nil {
		s.errf("mirror-sync: %v", err)
		return nil, 1
	}
	fmt.Fprintf(s.Out, "adopted %s (upstream %s) — sync it with: mirror-sync %s\n", r.Name, r.UpstreamID, r.Name)
	return r, 0
}
