package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"neohome/internal/core"
)

// webstats — §52 运营网站's reading half. Nothing here is a counter: the
// command parses the site's real access log (the file WebsiteTick and every
// other request path append to) and derives the numbers at the moment it
// runs. A site nobody visited has no log, and the command says so, because
// "no visits recorded" is the honest answer, not zeroes from a table.

func init() {
	builtinTable["webstats"] = cmdWebstats
}

func cmdWebstats(s *Shell, args []string) int {
	if len(args) > 1 {
		s.errf("usage: webstats [HOST]")
		return 1
	}
	if len(args) == 1 {
		d := s.W.Devices[args[0]]
		if d == nil {
			for _, id := range s.W.Order {
				if dd := s.W.Devices[id]; dd.Hostname == args[0] {
					d = dd
				}
			}
		}
		if d == nil {
			s.errf("webstats: no device called %s", args[0])
			return 1
		}
		return webstatsOne(s, d)
	}
	// No HOST: the caller's own sites — the machines they own that run a web
	// service, whether or not the internet has found them yet.
	shown := false
	for _, id := range s.W.Order {
		d := s.W.Devices[id]
		if d == nil || d.Owner != s.User.Name {
			continue
		}
		if core.WebServiceOf(d) == nil {
			continue
		}
		webstatsOne(s, d)
		shown = true
	}
	if !shown {
		fmt.Fprintln(s.Out, "no sites: none of your machines runs a web service (install nginx, give it content, publish it)")
	}
	return 0
}

func webstatsOne(s *Shell, d *core.Device) int {
	svc := core.WebServiceOf(d)
	if svc == nil {
		fmt.Fprintf(s.Out, "%s: no web service (nothing serving http on 80/443/8080)\n", d.Hostname)
		return 0
	}
	logPath, _ := core.AccessLogPath(d)
	data, ok := d.FS.Read(logPath)
	if !ok {
		fmt.Fprintf(s.Out, "%s (%s :%d): no visits recorded (%s does not exist yet)\n",
			d.Hostname, svc.Name, svc.Port, logPath)
		return 0
	}
	st := parseAccessLog(string(data), s.W.Sim)
	fmt.Fprintf(s.Out, "%s — %s :%d (%s), log %s\n", d.Hostname, svc.Name, svc.Port, svc.State, logPath)
	fmt.Fprintf(s.Out, "  total hits:      %d\n", st.total)
	fmt.Fprintf(s.Out, "  unique visitors: %d\n", len(st.visitors))
	fmt.Fprintf(s.Out, "  last hour:       %d\n", st.lastHour)
	fmt.Fprintf(s.Out, "  last day:        %d\n", st.lastDay)
	if len(st.statuses) > 0 {
		codes := make([]string, 0, len(st.statuses))
		for c := range st.statuses {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		var parts []string
		for _, c := range codes {
			parts = append(parts, fmt.Sprintf("%s×%d", c, st.statuses[c]))
		}
		fmt.Fprintf(s.Out, "  statuses:        %s\n", strings.Join(parts, " "))
	}
	if len(st.paths) > 0 {
		fmt.Fprintln(s.Out, "  top paths:")
		type pv struct {
			p string
			n int
		}
		ranked := make([]pv, 0, len(st.paths))
		for p, n := range st.paths {
			ranked = append(ranked, pv{p, n})
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].n != ranked[j].n {
				return ranked[i].n > ranked[j].n
			}
			return ranked[i].p < ranked[j].p
		})
		if len(ranked) > 5 {
			ranked = ranked[:5]
		}
		for _, kv := range ranked {
			fmt.Fprintf(s.Out, "    %-36s %d\n", kv.p, kv.n)
		}
	}
	if st.unparsed > 0 {
		fmt.Fprintf(s.Out, "  (%d line(s) not in access-log format — the log was hand-edited?)\n", st.unparsed)
	}
	if st.total == 0 {
		fmt.Fprintln(s.Out, "  no visits recorded (the log is empty)")
	}
	return 0
}

// accessStats is what one pass over the log yields. It lives for the
// duration of the command and is then gone — the log remains the state.
type accessStats struct {
	total    int
	visitors map[string]int
	paths    map[string]int
	statuses map[string]int
	lastHour int
	lastDay  int
	unparsed int
}

// parseAccessLog reads combined-format lines:
//
//	10.0.0.5 - - [17/Oct/2026:09:12:00 +0000] "GET / HTTP/1.1" 200 612 "-" "UA"
//
// Lines that do not parse are counted, not silently dropped: a log the
// operator edited still says how much of it is unreadable.
func parseAccessLog(data string, now time.Time) *accessStats {
	st := &accessStats{visitors: map[string]int{}, paths: map[string]int{}, statuses: map[string]int{}}
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\"")
		if len(parts) < 3 {
			st.unparsed++
			continue
		}
		head := strings.Fields(parts[0])
		if len(head) == 0 {
			st.unparsed++
			continue
		}
		req := strings.Fields(parts[1])
		tail := strings.Fields(parts[2])
		if len(req) < 2 || len(tail) < 1 {
			st.unparsed++
			continue
		}
		st.total++
		st.visitors[head[0]]++
		st.paths[req[1]]++
		st.statuses[tail[0]]++
		ob, cb := strings.Index(parts[0], "["), strings.Index(parts[0], "]")
		if ob < 0 || cb <= ob {
			st.unparsed++
			continue
		}
		t, err := time.Parse(core.AccessLogTimeLayout, parts[0][ob+1:cb])
		if err != nil {
			st.unparsed++
			continue
		}
		if age := now.Sub(t); age <= time.Hour {
			st.lastHour++
		}
		if age := now.Sub(t); age <= 24*time.Hour {
			st.lastDay++
		}
	}
	return st
}
