package core

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ---- resolver plumbing shared by net.go / engine.go ----

func firstSvc(d *Device, names ...string) string {
	for _, n := range names {
		if d.Svc(n) != nil {
			return n
		}
	}
	return "resolver"
}

// hasNoUpstream: true when this forwarder genuinely cannot reach a nameserver.
// nsd is authoritative and never needs one. A device configured by dnsmasq is
// judged by its resolv-file; an unconfigured infra forwarder is judged by its
// own /etc/resolv.conf — which is exactly how the ISP resolver differs from the
// home router.
func (d *Device) hasNoUpstream() bool {
	if d.Svc("nsd") != nil {
		return false // authoritative
	}
	if _, ok := d.FS.Read("/etc/dnsmasq.conf"); ok {
		return !d.dnsmasqUpstreamOK()
	}
	data, ok := d.FS.Read("/etc/resolv.conf")
	return !ok || len(nameservers(string(data))) == 0
}

// upstreamServers: what this forwarder queries when it receives a request.
func (d *Device) upstreamServers() []string {
	// dnsmasq resolv-file first
	if data, ok := d.FS.Read("/etc/dnsmasq.conf"); ok {
		resolv := ""
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "resolv-file=") {
				resolv = strings.TrimPrefix(line, "resolv-file=")
			}
		}
		if resolv != "" {
			if x, ok2 := d.FS.Read(resolv); ok2 {
				return nameservers(string(x))
			}
		}
	}
	if data, ok := d.FS.Read("/etc/resolv.conf"); ok {
		return nameservers(string(data))
	}
	return nil
}

func nameservers(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}

// ResolverIPFor is exported for the shell's dig @server paths.
func (d *Device) ResolverIPFor(serverIP string) (*Device, bool) {
	id, ok := d.W.IPMap[serverIP]
	if !ok {
		return nil, false
	}
	return d.W.Devices[id], true
}

// HasNoUpstream exported wrapper used by shell diagnostics.
func (d *Device) HasNoUpstream() bool { return d.hasNoUpstream() }

// DNSAnswer method form for shells.
func (d *Device) DNSAnswer(name string) (string, bool, string) { return DNSAnswer(d, name) }

// Router returns the LAN router serving this device.
func (d *Device) Router() *Device { return d.W.routerFor(d) }

// AssistantSkill exposes the growth counter.
func (w *World) AssistantSkill() int { return w.assistSkills }

// SetAssistantSkill raises skill (learning by shadowing).
func (w *World) SetAssistantSkill(n int) { w.assistSkills = n }

// DnsmasqHealthy tells whether this router's forwarder would answer.
func (d *Device) DnsmasqHealthy() bool { return d.dnsmasqUpstreamOK() }

// FirstWANIP / FirstLANIP for display.
func (d *Device) FirstWANIP() string { return wanIP(d) }
func (d *Device) FirstLANIP() string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.Up && i.IP != "" {
			return i.IP
		}
	}
	return "127.0.0.1"
}

// LanNet exported.
func (d *Device) LanNet() string { return lanNetOf(d) }

// AllocPublic exported for VPS creation.
func (w *World) AllocPublic() string { return w.allocPublic() }

// AddIP exported.
func (w *World) AddIP(ip, id string) { w.IPMap[ip] = id }

// NewDeviceLite exported for VPS provisioning from the shell layer.
func (w *World) NewDeviceLite(id, hostname string, os OSInfo, hw Hardware) *Device {
	return w.addDevice(id, hostname, "vps", "", os, hw, "")
}

// AttachWAN registers a public ip on the device.
func (d *Device) AttachWAN(ip, gw string) {
	d.Ifaces = append(d.Ifaces, &Iface{Name: "eth0", IP: ip, MAC: macFor(d.ID + "-wan"), Zone: "wan", Up: true, GW: gw})
	d.W.AddIP(ip, d.ID)
}

// DeliverMail writes a message into a player's mailbox (world → player loop).
func (w *World) DeliverMail(owner, subject, body string) {
	p := w.Players[owner]
	if p == nil {
		return
	}
	pc := w.Devices[p.PC]
	if pc == nil {
		return
	}
	mbox := "/var/mail/" + owner
	old, _ := pc.FS.Read(mbox)
	msg := fmt.Sprintf("From: world@neohome\nSubject: %s\nDate: %s\n\n%s\n\n", subject, w.Sim.Format("2006-01-02 15:04"), body)
	pc.FS.Write(mbox, string(old)+msg, 0600, owner, owner)
	w.AddEvent(pc.ID, "info", "mail", "delivered mail to %s: %s", owner, subject)
}

// ChatBus lives outside World so the whole world stays gob-persistable.
var chatBus = struct {
	mu   sync.Mutex
	subs map[int]chan ChatMsg
	seq  int
}{}

func ChatSubscribe() (int, chan ChatMsg) {
	chatBus.mu.Lock()
	defer chatBus.mu.Unlock()
	if chatBus.subs == nil {
		chatBus.subs = map[int]chan ChatMsg{}
	}
	chatBus.seq++
	c := make(chan ChatMsg, 64)
	chatBus.subs[chatBus.seq] = c
	return chatBus.seq, c
}

func ChatUnsubscribe(id int) {
	chatBus.mu.Lock()
	delete(chatBus.subs, id)
	chatBus.mu.Unlock()
}

func chatBroadcast(m ChatMsg) {
	chatBus.mu.Lock()
	for _, c := range chatBus.subs {
		select {
		case c <- m:
		default:
		}
	}
	chatBus.mu.Unlock()
}

// ---- persistence (gob snapshots of the entire world) ----

func (w *World) Save(path string) error {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(w); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func LoadWorld(path string) (*World, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	w := &World{}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(w); err != nil {
		return nil, err
	}
	// re-link back pointers gob lost (device.W set via reflection-free walk)
	for _, id := range w.Order {
		w.Devices[id].W = w
	}
	if w.Chat == nil {
		w.Chat = &Chat{Channels: map[string]bool{}}
	}
	if w.IPMap == nil {
		w.IPMap = map[string]string{}
	}
	if w.TLS == nil {
		// saves from before the TLS subsystem existed: an empty state is
		// honest (no CAs known yet); seedTLS only runs for fresh worlds
		w.TLS = &TLSState{CAs: map[string]*TLSCert{}}
	}
	return w, nil
}

// busyNyquist: devices with no dnsmasq fall through to resolv.conf, which the
// ISP resolver does have. This keeps the chain honest: an infra resolver works
// unless its own resolv.conf loses its nameserver line.
var _ = strings.Contains

// MemUsed sums process memory (real state, shown by fastfetch/free).
func (d *Device) MemUsed() int {
	total := 0
	for _, p := range d.Procs {
		total += p.Mem
	}
	if total == 0 {
		total = d.HW.RAMMB / 6
	}
	return total
}

// sourceIPFor exported for shell.
func (d *Device) SourceIPFor(dst *Device) string { return d.sourceIPFor(dst) }
