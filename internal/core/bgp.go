package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// BGP (缺口 5b) — the control plane behind the map
//
// wan.go already names every AS, its prefixes and its peers; what was missing
// was the protocol state that makes those facts breakable: sessions that can
// drop, routes that can be withdrawn, and a data plane that consults them.
// This file owns that layer:
//
//   - sessions are derived, never stored: bird running on core-gw, the
//     neighbor configured in /etc/bird.conf, and the peer AS SYNCED (a
//     BEHIND peer is stale but up; anything else is down),
//   - the RIB is longest-match over originated prefixes: a public address is
//     reachable exactly while its origin AS holds an established session,
//   - dial() and Reach() consult the RIB for public destinations, so a
//     stopped bird or an OFFLINE peer darkens those prefixes with the cause
//     naming the session — a blackhole with a paper trail, never a dice roll.
//
// No stored state means no tick and no save format: the world cannot disagree
// with itself about whether a session is up.
// ---------------------------------------------------------------------------

// BGPSession is one adjacency as the looking glass sees it.
type BGPSession struct {
	PeerASN  int
	State    string // Established | Idle
	Why      string // one line: since when, or why not
	Prefixes int    // prefixes this session currently feeds the RIB
}

// bgpNeighbors reads the configured peers from bird's own configuration:
// a neighbor that is not configured never establishes, however healthy.
func bgpNeighbors(gw *Device) []int {
	var out []int
	if gw == nil || gw.FS == nil {
		return out
	}
	data, ok := gw.FS.Read("/etc/bird.conf")
	if !ok {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "neighbor ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(strings.Trim(fields[1], ";"))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// birdUp reports whether the daemon that speaks BGP is running.
func birdUp(w *World) bool {
	if w == nil {
		return false
	}
	gw := w.Devices["core-gw"]
	if gw == nil || !gw.Powered() {
		return false
	}
	svc := gw.Svc("bird")
	return svc != nil && svc.State == "running"
}

// BGPSessions derives every adjacency of the transit router: configured
// neighbors, each Established or Idle with its reason.
func BGPSessions(w *World) []BGPSession {
	var out []BGPSession
	if w == nil || w.WAN == nil {
		return out
	}
	gw := w.Devices["core-gw"]
	for _, asn := range bgpNeighbors(gw) {
		as := w.WAN.ASes[asn]
		if as == nil {
			continue
		}
		s := BGPSession{PeerASN: asn}
		switch {
		case !birdUp(w):
			s.State, s.Why = "Idle", "bird is down on core-gw"
		case as.Status != "SYNCED" && as.Status != "BEHIND":
			s.State, s.Why = "Idle", fmt.Sprintf("AS%d is %s", asn, as.Status)
		default:
			s.State = "Established"
			s.Why = fmt.Sprintf("since boot (holding %d prefixes)", len(as.Prefixes))
			s.Prefixes = len(as.Prefixes)
		}
		out = append(out, s)
	}
	return out
}

// bgpSessionUp is the one question the data plane asks the control plane.
func BGPSessionUp(w *World, asn int) bool {
	if w == nil || w.WAN == nil || asn == asCore {
		return true // the transit fabric itself is always here
	}
	for _, s := range BGPSessions(w) {
		if s.PeerASN == asn {
			return s.State == "Established"
		}
	}
	return false // no session at all: unannounced space
}

// bgpOrigin finds the AS whose announced prefix covers ip, longest match.
func bgpOrigin(w *World, ip string) *AS {
	if w == nil || w.WAN == nil {
		return nil
	}
	var best *AS
	bestLen := -1
	consider := func(as *AS, lists ...[]string) {
		for _, ls := range lists {
			for _, p := range ls {
				if inNet(ip, p) && len(p) > bestLen {
					best, bestLen = as, len(p)
				}
			}
		}
	}
	for _, as := range w.WAN.ASes {
		consider(as, as.Prefixes, as.Prefixes6)
	}
	return best
}

// bgpReachable answers whether the public internet still carries ip, and if
// not, which session took it away. Private, CGNAT, ULA, link-local and
// loopback space never consults the RIB — it was never announced.
func bgpReachable(w *World, ip string) (bool, string) {
	if w == nil || w.WAN == nil {
		return true, ""
	}
	if !ClassifyAddr(ip).Public {
		return true, ""
	}
	origin := bgpOrigin(w, ip)
	if origin == nil {
		return false, fmt.Sprintf("No route to host (no AS announces %s)", ip)
	}
	if BGPSessionUp(w, origin.ASN) {
		return true, ""
	}
	return false, fmt.Sprintf("No route to host (prefix withdrawn: AS%d session down)", origin.ASN)
}
