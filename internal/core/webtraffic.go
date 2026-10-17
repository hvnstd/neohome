package core

import (
	"fmt"
	"sort"
	"strings"
)

// §52 运营网站: a public website is not a running service plus a counter — it
// is traffic. This file is the traffic. Every tick, the world's other
// machines browse whatever the DNS namespace actually publishes: each visit
// resolves the name through the visitor's own resolver chain, dials through
// the same packet path everything else uses (power, links, NAT, forwards,
// firewalls, bans, scope), and only a connection that lands on a real web
// service with real content to serve leaves a line in that site's access
// log. A dark, unresolvable, firewalled or empty site gets no lines — that
// absence is the story, and it is why the analytics (webstats) are derived
// from the log and never stored beside it.

// AccessLogTimeLayout is the timestamp format every web access log in the
// world uses (the nginx/Apache default), so the writer here and the reader
// in the shell's webstats can never disagree about it.
const AccessLogTimeLayout = "02/Jan/2006:15:04:05 -0700"

// webPorts are the ports a browser tries, in the order it tries them.
var webPorts = []int{80, 443, 8080}

// WebServiceOf returns the device's primary web service: the first (service
// names sorted, so the answer is stable) service speaking an http-* handler
// on a web port. State is deliberately not consulted — webstats reads a
// stopped site's log too; "running" is the packet path's verdict, not a
// lookup's.
func WebServiceOf(d *Device) *Service {
	for _, name := range sortedServiceNames(d) {
		svc := d.Services[name]
		if !strings.HasPrefix(svc.Handler, "http-") {
			continue
		}
		switch svc.Port {
		case 80, 443, 8080:
			return svc
		}
	}
	return nil
}

// accessLogForSvc names the file a web service writes its access log to,
// following the daemon's own convention (nginx, Apache, webd). The file does
// not exist until the first visit lands — an unread site has no log.
func accessLogForSvc(svc *Service) string {
	switch {
	case svc.Name == "httpd" || svc.Banner == "Apache":
		return "/var/log/apache2/access.log"
	case svc.Handler == "http-terminal":
		return "/var/log/webd/access.log"
	default:
		return "/var/log/nginx/access.log"
	}
}

// AccessLogPath is the access-log file of the device's web service, if it
// runs one. Writer (WebsiteTick) and reader (webstats) both go through here,
// so the two ends can never point at different files.
func AccessLogPath(d *Device) (string, bool) {
	svc := WebServiceOf(d)
	if svc == nil {
		return "", false
	}
	return accessLogForSvc(svc), true
}

// WebsiteTick is wired from World.Tick. The access log on the site device is
// the only state: there is no counter here to drift away from it.
func (w *World) WebsiteTick() {
	pool := w.webVisitors()
	if len(pool) == 0 {
		return
	}
	// The candidate sites are the names the world's DNS publishes. A name is
	// visited once per pass even when several records (A and AAAA) carry it.
	seen := map[string]bool{}
	var names []string
	for _, r := range w.Records {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		names = append(names, r.Name)
	}
	for i, name := range names {
		// Bounded and deterministic: each site every fourth tick, one
		// visitor drawn from the pool by tick count — no rand, no surge.
		// ~30 visits per sim-hour on a site is plenty of traffic for a
		// log to be worth reading, and the tick stays cheap.
		if (w.TickCount+i)%4 != 0 {
			continue
		}
		spin := w.TickCount + i
		w.webVisit(pool[spin%len(pool)], name, spin)
	}
}

// webVisitors is the pool the drop-in traffic comes from: the world's
// person-machines and WAN hosts, in world order. Whether any of them really
// reaches a site is the visit's own question — a machine in the dark browses
// nothing.
func (w *World) webVisitors() []*Device {
	var pool []*Device
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !d.Powered() {
			continue
		}
		switch d.Profile {
		case "pc", "laptop", "phone", "infra", "vps", "peer":
			pool = append(pool, d)
		}
	}
	return pool
}

// webVisit is one browser dropping in on one name: resolve from the
// visitor's own chain, dial the web ports through the one packet path, and
// if a real site answers with real content, log the request on the machine
// that served it. Every failure — NXDOMAIN, SERVFAIL, no route, filtered,
// refused, a dead box — simply leaves no line, which is exactly what the
// site's operator sees when they read the log.
func (w *World) webVisit(visitor *Device, name string, spin int) {
	if !visitor.Powered() {
		return
	}
	ip, ok, _ := DNSAnswer(visitor, name)
	if !ok {
		return
	}
	addressed := w.Devices[w.IPMap[ip]]
	if addressed == nil || addressed == visitor {
		return // a host does not drop in on itself
	}
	ports := w.sitePorts(ip)
	if len(ports) == 0 {
		return // the name publishes no web port — not a site, nobody knocks
	}
	for _, port := range ports {
		if !w.portForwarded(addressed, port) && !siteHasContent(addressed) {
			// The addressed machine itself would answer, and its document
			// root is empty: a placeholder gets no knocks at all. (A
			// forwarded port is different — the content lives behind the
			// router, and only the dial can find it.)
			continue
		}
		svc, dst, _ := Dial(visitor, ip, port)
		if svc == nil || dst == nil {
			continue // nothing answers on this port; try the next
		}
		if !strings.HasPrefix(svc.Handler, "http-") {
			continue // something else listens there — not a website
		}
		if dst == visitor {
			return
		}
		if !siteHasContent(dst) {
			return // an empty document root is a placeholder, not a site
		}
		path := "/"
		if spin%3 == 2 {
			if paths := sitePaths(dst); len(paths) > 0 {
				path = paths[spin%len(paths)]
			}
		}
		body, served := ServeFile(dst, path)
		status, size := 404, 0
		if served {
			status, size = 200, len(body)
		}
		logAccess(dst, svc, visitor, path, status, size)
		return
	}
}

// sitePorts is which web ports a name is actually published on: the http
// services on the addressed machine, plus the web ports its edge forwards (a
// redirect is a public claim of a service). A name with no published web
// port is not a site, and visitors do not knock on it — background radiation
// is the scanner's job (§33), not the web's. The dial that follows still
// decides everything about the visit itself; this only keeps browsers from
// prodding ports nobody ever claimed.
func (w *World) sitePorts(ip string) []int {
	id, ok := w.IPMap[ip]
	if !ok {
		return nil
	}
	d := w.Devices[id]
	if d == nil {
		return nil
	}
	seen := map[int]bool{}
	var ports []int
	add := func(p int) {
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	for _, name := range sortedServiceNames(d) {
		svc := d.Services[name]
		if !strings.HasPrefix(svc.Handler, "http-") {
			continue
		}
		for _, p := range webPorts {
			if svcListensOn(svc, p) {
				add(p)
			}
		}
	}
	r := w.routerFor(d)
	if r == nil && d.Profile == "router" {
		r = d
	}
	if r != nil {
		for _, p := range webPorts {
			if r.matchFwd(p) != nil {
				add(p)
			}
		}
	}
	sort.Ints(ports)
	return ports
}

// portForwarded reports whether the addressed machine's edge translates this
// port to somewhere inside — in which case the dial, not a lookup on the
// addressed box, is the only way to know what answers.
func (w *World) portForwarded(addressed *Device, port int) bool {
	r := w.routerFor(addressed)
	if r == nil && addressed.Profile == "router" {
		r = addressed
	}
	if r == nil {
		return false
	}
	return r.matchFwd(port) != nil
}

// siteHasContent is the qualification that separates a site from a socket: a
// document root with something in it. A mirror's front page is rendered from
// the trees it carries, so it always qualifies.
func siteHasContent(d *Device) bool {
	root := WebRoot(d)
	if root == "" {
		return false
	}
	if isMirrorHost(d) {
		return true
	}
	return len(d.FS.List(root)) > 0
}

// sitePaths lists real URL paths under the document root, so visitors ask
// for pages that exist. Bounded walk: the log needs variety, not a crawl.
func sitePaths(d *Device) []string {
	root := WebRoot(d)
	if root == "" {
		return nil
	}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 || len(out) >= 24 {
			return
		}
		for _, p := range d.FS.List(dir) {
			if len(out) >= 24 {
				return
			}
			if d.FS.IsDir(p) {
				walk(p, depth+1)
				continue
			}
			out = append(out, strings.TrimPrefix(p, root))
		}
	}
	walk(root, 0)
	sort.Strings(out)
	return out
}

// logAccess appends one combined-format access-log line on the serving
// machine. The log is a file like any other: a full or failed disk loses the
// line, counted the same way syslog losses are counted (§17), and the file
// is created on first use because VFS.Append never creates.
func logAccess(dst *Device, svc *Service, visitor *Device, urlPath string, status, size int) {
	logPath := accessLogForSvc(svc)
	ua := fmt.Sprintf("Mozilla/5.0 (%s; %s)", visitor.OS.Distro, visitor.OS.Arch)
	line := fmt.Sprintf("%s - - [%s] \"GET %s HTTP/1.1\" %d %d \"-\" \"%s\"\n",
		visitor.SourceIPFor(dst), dst.W.Sim.Format(AccessLogTimeLayout), urlPath, status, size, ua)
	if dst.DiskFull() || dst.DiskFailed() {
		dst.Resources().LogDropped++
		return
	}
	if !dst.FS.Exists(logPath) {
		dst.FS.MkdirAll(parentDir(logPath), 0755, "root", "root")
		dst.FS.Write(logPath, "", 0644, "root", "root")
	}
	dst.FS.Append(logPath, []byte(line))
}
