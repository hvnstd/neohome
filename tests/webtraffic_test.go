package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// §52 运营网站: a website is traffic plus the will to read it. These tests
// prove the traffic is real (access-log lines appear on the serving machine
// only while the site is genuinely reachable) and the analytics are derived
// (webstats parses the log; nothing is counted twice).

// siteByHandler derives a site from the world instead of pinning an address:
// the device running a service with the given handler.
func siteByHandler(t *testing.T, w *core.World, handler string) *core.Device {
	t.Helper()
	for _, id := range w.Order {
		d := w.Devices[id]
		for _, svc := range d.Services {
			if svc.Handler == handler {
				return d
			}
		}
	}
	t.Fatalf("no device with a %s service in this world", handler)
	return nil
}

func accessLogLines(d *core.Device, path string) []string {
	data, ok := d.FS.Read(path)
	if !ok {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// tickUntilLogExceeds advances the world clock until the site's access log
// holds more than n lines. Sites are visited on a fixed cadence from a
// rotating visitor pool, so "traffic arrives" is a matter of ticks, never of
// wall-clock sleeps.
func tickUntilLogExceeds(t *testing.T, w *core.World, d *core.Device, path string, n int) int {
	t.Helper()
	for i := 0; i < 24; i++ {
		w.Tick()
		if got := len(accessLogLines(d, path)); got > n {
			return got
		}
	}
	t.Fatalf("the access log on %s never grew past %d lines", d.Hostname, n)
	return 0
}

func webstatNumber(t *testing.T, out, label string) int {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, label) {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(l[len(label):]), "%d", &n); err == nil {
				return n
			}
		}
	}
	t.Fatalf("no %q line in:\n%s", label, out)
	return 0
}

func TestWebsiteTickAccumulatesRealVisits(t *testing.T) {
	w := core.NewWorld()
	site := siteByHandler(t, w, "http-mirror")
	logPath, ok := core.AccessLogPath(site)
	if !ok {
		t.Fatal("the mirror runs a web service, it must have an access-log path")
	}
	// an unread site has no log: the file is created by the first visit
	if site.FS.Exists(logPath) {
		t.Fatal("the access log must not exist before the first visit")
	}
	tickUntilLogExceeds(t, w, site, logPath, 0)
	lines := accessLogLines(site, logPath)
	if len(lines) == 0 {
		t.Fatal("a public, reachable site must accumulate access-log lines")
	}
	// every line is a real combined-format entry: source, parseable
	// timestamp, quoted GET request, status
	uniq := map[string]bool{}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 7 || !strings.Contains(l, "\"GET /") {
			t.Fatalf("line is not an access-log line: %q", l)
		}
		uniq[f[0]] = true
		ob, cb := strings.Index(l, "["), strings.Index(l, "]")
		if ob < 0 || cb <= ob {
			t.Fatalf("no timestamp bracket in %q", l)
		}
		if _, err := time.Parse(core.AccessLogTimeLayout, l[ob+1:cb]); err != nil {
			t.Fatalf("timestamp in %q does not parse: %v", l, err)
		}
	}
	// webstats derives exactly what the log holds — nothing stored twice
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "webstats "+site.Hostname)
	if got := webstatNumber(t, out, "total hits:"); got != len(lines) {
		t.Fatalf("webstats total %d != log lines %d\n%s", got, len(lines), out)
	}
	if got := webstatNumber(t, out, "unique visitors:"); got != len(uniq) {
		t.Fatalf("webstats uniques %d != distinct log sources %d\n%s", got, len(uniq), out)
	}
	// six ticks are three sim-minutes: everything is inside the last hour
	if got := webstatNumber(t, out, "last hour:"); got != len(lines) {
		t.Fatalf("last-hour %d != total %d for a three-minute-old log\n%s", got, len(lines), out)
	}
	if !strings.Contains(out, "top paths:") || !strings.Contains(out, "/") {
		t.Fatalf("webstats must rank the paths it parsed:\n%s", out)
	}
}

func TestWebsiteTickStopsWhileDark(t *testing.T) {
	w := core.NewWorld()
	site := siteByHandler(t, w, "http-mirror")
	logPath, _ := core.AccessLogPath(site)
	before := tickUntilLogExceeds(t, w, site, logPath, 0)

	// dark, phase 1: the web service is stopped
	var svc *core.Service
	for _, s := range site.Services {
		if s.Handler == "http-mirror" {
			svc = s
		}
	}
	svc.State = "stopped"
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if got := len(accessLogLines(site, logPath)); got != before {
		t.Fatalf("a stopped site must get no visits: %d -> %d", before, got)
	}

	// recovered: the service runs again, the visitors come back
	svc.State = "running"
	after := tickUntilLogExceeds(t, w, site, logPath, before)

	// dark, phase 2: the DNS records are gone — nobody finds the site. A name
	// in this world carries an A and an AAAA record; both go.
	var kept []core.DNSRecord
	var removed []core.DNSRecord
	for _, r := range w.Records {
		if r.Name == site.Hostname {
			removed = append(removed, r)
			continue
		}
		kept = append(kept, r)
	}
	if len(removed) == 0 {
		t.Fatal("precondition: the site's name must be in the world's DNS")
	}
	w.Records = kept
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if got := len(accessLogLines(site, logPath)); got != after {
		t.Fatalf("an unresolvable site must get no visits: %d -> %d", after, got)
	}
	w.Records = append(w.Records, removed...)
	tickUntilLogExceeds(t, w, site, logPath, after)
}

func TestWebsiteTickSkipsLanOnlyAndFirewalled(t *testing.T) {
	w := core.NewWorld()
	// the household's web consoles (NAS on 8080, assistant webd) are LAN-only
	// and nothing publishes them: no DNS record names them, no forward exists
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Owner != "alex" {
			continue
		}
		if p, ok := core.AccessLogPath(d); ok && d.FS.Exists(p) {
			t.Fatalf("a LAN-only site must see no drop-in traffic, %s has %s", d.Hostname, p)
		}
	}

	// a name the edge does not publish: DNS resolves, content exists, but the
	// router's default WAN policy is the firewall — zero visits
	nas := w.Devices["nas-alex"]
	rtr := w.Devices["router-alex"]
	nas.Services["site"] = &core.Service{Name: "site", Desc: "player website", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-user", Banner: "nginx"}
	nas.FS.MkdirAll("/srv/www/html", 0755, "root", "root")
	nas.FS.Write("/srv/www/html/index.html", "<html><body>unpublished</body></html>\n", 0644, "root", "root")
	w.Records = append(w.Records, core.DNSRecord{Name: "www.alex-site.example", IP: rtr.FirstWANIP()})
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if nas.FS.Exists("/var/log/nginx/access.log") {
		t.Fatal("a name without a forward is a filtered port: zero visits is the honest answer")
	}

	// a public node whose own host firewall drops everything: same verdict.
	// (A VPS, not the mirror — the mirror's neighbours are on-link with it,
	// and this world's host firewall judges WAN-arriving packets only.)
	vps, _, err := w.ProvisionVPS("alex", "nano-1", "site-fw")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	vps.Services["nginx"] = &core.Service{Name: "nginx", Desc: "web", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-user", Banner: "nginx"}
	vps.FS.MkdirAll("/srv/www/html", 0755, "root", "root")
	vps.FS.Write("/srv/www/html/index.html", "<html><body>vps site</body></html>\n", 0644, "root", "root")
	logPath := "/var/log/nginx/access.log"
	before := tickUntilLogExceeds(t, w, vps, logPath, 0)
	vps.FS.Write("/etc/iptables/rules.v4", core.RenderIPTables("DROP", nil), 0644, "root", "root")
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if got := len(accessLogLines(vps, logPath)); got != before {
		t.Fatalf("a firewalled site must get zero visits: %d -> %d", before, got)
	}
}

func TestWebsiteTickPlayerPublishedSite(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	nas := w.Devices["nas-alex"]
	rtr := w.Devices["router-alex"]
	// the player installs nginx, writes a real page, publishes the port and
	// registers the name — the whole §52 story end to end
	nas.Services["site"] = &core.Service{Name: "site", Desc: "player website", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-user", Banner: "nginx"}
	nas.FS.MkdirAll("/srv/www/html", 0755, "root", "root")
	nas.FS.Write("/srv/www/html/index.html", "<html><body>alex's site</body></html>\n", 0644, "root", "root")
	shr := func(line string) string { return run(t, w, rtr, "root", line) }
	shr("uci set firewall.site=redirect")
	shr("uci set firewall.site.target=DNAT")
	shr("uci set firewall.site.src=wan")
	shr("uci set firewall.site.src_dport=80")
	shr("uci set firewall.site.dest_ip=" + nas.FirstLANIP())
	shr("uci set firewall.site.dest_port=80")
	shr("uci set firewall.site.enabled=1")
	shr("uci commit firewall")
	w.Records = append(w.Records, core.DNSRecord{Name: "www.alex-site.example", IP: rtr.FirstWANIP()})

	logPath := "/var/log/nginx/access.log"
	if nas.FS.Exists(logPath) {
		t.Fatal("the log must not exist before the first visit")
	}
	tickUntilLogExceeds(t, w, nas, logPath, 0)
	lines := accessLogLines(nas, logPath)
	out := run(t, w, nas, "alex", "webstats")
	if !strings.Contains(out, "total hits:") {
		t.Fatalf("webstats with no argument must report the owner's published site:\n%s", out)
	}
	if got := webstatNumber(t, out, "total hits:"); got != len(lines) {
		t.Fatalf("webstats total %d != log lines %d\n%s", got, len(lines), out)
	}
}

func TestWebsiteTickLogSurvivesSaveLoad(t *testing.T) {
	w := core.NewWorld()
	site := siteByHandler(t, w, "http-mirror")
	logPath, _ := core.AccessLogPath(site)
	tickUntilLogExceeds(t, w, site, logPath, 0)
	before := accessLogLines(site, logPath)
	path := filepath.Join(t.TempDir(), "world.gob")
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	site2 := w2.Devices[site.ID]
	if site2 == nil {
		t.Fatal("the site device must survive the round trip")
	}
	loaded := accessLogLines(site2, logPath)
	if len(loaded) != len(before) {
		t.Fatalf("the log is a file: %d lines before save, %d after load", len(before), len(loaded))
	}
	out := run(t, w2, w2.Devices["pc-alex"], "alex", "webstats "+site2.Hostname)
	if got := webstatNumber(t, out, "total hits:"); got != len(before) {
		t.Fatalf("webstats after load must parse the same log: %d != %d\n%s", got, len(before), out)
	}
	// and the world keeps visiting after the reload
	tickUntilLogExceeds(t, w2, site2, logPath, len(before))
}

func TestWebstatsHonestForNonWebDevices(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	// a device with no web service says so
	out := run(t, w, pc, "alex", "webstats printer")
	if !strings.Contains(out, "no web service") {
		t.Fatalf("a printer is not a website:\n%s", out)
	}
	// an unknown host is an error, not a report
	out = run(t, w, pc, "alex", "webstats no-such-box")
	if !strings.Contains(out, "no device called no-such-box") {
		t.Fatalf("an unknown host is an error, not a report:\n%s", out)
	}
	// the fresh household: consoles exist, nobody visited — honest zeroes
	out = run(t, w, pc, "alex", "webstats")
	if !strings.Contains(out, "nas") || !strings.Contains(out, "assistant") {
		t.Fatalf("the household's own web services must be listed:\n%s", out)
	}
	if !strings.Contains(out, "no visits recorded") {
		t.Fatalf("a site with no log must say so:\n%s", out)
	}
	// an owner with no web services at all gets the honest empty case
	mara := w.Devices["npc-pc"]
	out = run(t, w, mara, "mara", "webstats")
	if !strings.Contains(out, "no sites") {
		t.Fatalf("mara runs no web service:\n%s", out)
	}
}
