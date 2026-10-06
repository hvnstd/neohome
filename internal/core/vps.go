package core

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// §12: renting a machine
//
// 十二、VPS / 计算机 / 组网 lists what a player does with a rented node: pick a
// region, a spec, IPv4/IPv6 and an OS, then start, stop, reboot, reinstall,
// snapshot, restore, add disk, resize and enter through the console. Every one
// of those is an operation on the very same Device the rest of the world talks
// to, so a stopped node really is absent from the network, a reinstalled one
// really has a different filesystem, and a restored one really gets its files
// back. A panel that only printed "stopped" would be the kind of lie this
// project exists to avoid.
//
// NovaPanel's control plane lives here — the provider's own record of the
// machine — and the `vps` builtin in the shell layer is only its keyboard.
// ---------------------------------------------------------------------------

// Region is a datacenter NovaPanel sells into. A region is not a label on a
// list: it is an autonomous system with its own public block and its own
// latency, so choosing one changes the node's address, its whois attribution
// and how far away it really is.
type Region struct {
	Name       string // "eu-central"
	Datacenter string // "nl-ams-1"
	Country    string
	ASN        int
	Block      string // the /24 the region numbers its nodes from
}

// Snapshot is a point-in-time copy of a node's disk. Restoring it is a real
// rollback: files deleted afterwards come back, and accounts created afterwards
// are gone again — which is exactly what a player pays for.
type Snapshot struct {
	Name          string
	At            time.Time
	FS            *VFS
	Users         map[string]*User
	Installed     map[string]*VPkg
	InstalledFrom map[string]string
}

// NodeRecord is the provider's record of a customer's node: what was bought,
// where it runs, how reverse DNS names it, and what the customer has kept.
type NodeRecord struct {
	DeviceID   string
	Plan       string
	Image      string
	Region     string
	Datacenter string
	RDNS       string
	Monthly    int64
	CreatedAt  time.Time
	Rebuilds   int
	Snapshots  []Snapshot
}

// DefaultRegions is NovaPanel's footprint: three datacenters, each announced by
// its own AS, which is why an address tells you where the machine is.
func DefaultRegions() []Region {
	return []Region{
		{Name: "eu-central", Datacenter: "nl-ams-1", Country: "NL", ASN: asNova, Block: blockBase(asNova)},
		{Name: "us-east", Datacenter: "us-ash-1", Country: "US", ASN: asNovaUS, Block: blockBase(asNovaUS)},
		{Name: "ap-northeast", Datacenter: "jp-tyo-1", Country: "JP", ASN: asNovaAP, Block: blockBase(asNovaAP)},
	}
}

// NodeRegions is the region list the panel publishes.
func (w *World) NodeRegions() []Region {
	if w.Prov == nil {
		return DefaultRegions()
	}
	if len(w.Prov.Regions) == 0 {
		w.Prov.Regions = DefaultRegions()
	}
	return w.Prov.Regions
}

// RegionByName resolves a region the player asked for. An unknown region is a
// refusal, not a silent fallback to the default: a node in the wrong country is
// not a rounding error.
func (w *World) RegionByName(name string) (*Region, error) {
	regs := w.NodeRegions()
	for i := range regs {
		if strings.EqualFold(regs[i].Name, name) || strings.EqualFold(regs[i].Datacenter, name) {
			return &regs[i], nil
		}
	}
	var names []string
	for _, r := range regs {
		names = append(names, r.Name)
	}
	return nil, fmt.Errorf("no such region: %s (available: %s)", name, strings.Join(names, ", "))
}

// NodeOf returns the provider's record for a device, or nil if the device is
// not a rented node (the household's own machines are not).
func (w *World) NodeOf(d *Device) *NodeRecord {
	if w.Prov == nil || d == nil || w.Prov.Nodes == nil {
		return nil
	}
	return w.Prov.Nodes[d.ID]
}

// NodeByHostname finds a node by the name the customer uses, which is how the
// panel addresses machines.
func (w *World) NodeByHostname(hostname string) (*Device, *NodeRecord, error) {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || d.Hostname != hostname || d.Profile != "vps" {
			continue
		}
		if rec := w.NodeOf(d); rec != nil {
			return d, rec, nil
		}
		// a node provisioned before the panel kept records: adopt it, so an
		// old world can still be managed instead of refusing to see its nodes
		rec := &NodeRecord{DeviceID: d.ID, Plan: "nano-1", Image: "debian", Region: "eu-central",
			Datacenter: "nl-ams-1", CreatedAt: d.Boot}
		w.recordNode(d, rec)
		return d, rec, nil
	}
	return nil, nil, fmt.Errorf("no node named %s on this account", hostname)
}

// NodeFor is the permission boundary the panel works through: a node is only
// controllable by the account that rents it.
func (w *World) NodeFor(owner, hostname string) (*Device, *NodeRecord, error) {
	d, rec, err := w.NodeByHostname(hostname)
	if err != nil {
		return nil, nil, err
	}
	if d.Owner != owner {
		return nil, nil, fmt.Errorf("%s belongs to another account — you cannot manage it", d.Hostname)
	}
	return d, rec, nil
}

func (w *World) recordNode(d *Device, rec *NodeRecord) {
	if w.Prov == nil {
		return
	}
	if w.Prov.Nodes == nil {
		w.Prov.Nodes = map[string]*NodeRecord{}
	}
	w.Prov.Nodes[d.ID] = rec
}

// ---- power: the panel's switch is the machine's switch ----

// VPSState is what the panel shows: the node either has a link or it does not.
func VPSState(d *Device) string {
	if d.NetUp {
		return "running"
	}
	return "stopped"
}

// VPSStart powers a node back on. Services the machine was running before it
// went down come back with it, exactly as a power restore in the house does.
func (w *World) VPSStart(owner, hostname string) (*Device, error) {
	d, _, err := w.NodeFor(owner, hostname)
	if err != nil {
		return nil, err
	}
	if d.NetUp {
		return nil, fmt.Errorf("%s is already running", d.Hostname)
	}
	d.setPowered(true, "")
	w.AddEvent(d.ID, "info", "provider", "%s powered on from the panel", d.Hostname)
	return d, nil
}

// VPSStop powers a node off. Its services stop, its processes disappear, and
// the network stops answering for it: a stopped node is not reachable because
// there is nothing there to answer.
func (w *World) VPSStop(owner, hostname string) (*Device, error) {
	d, _, err := w.NodeFor(owner, hostname)
	if err != nil {
		return nil, err
	}
	if !d.NetUp {
		return nil, fmt.Errorf("%s is already stopped", d.Hostname)
	}
	d.setPowered(false, "poweroff requested from the panel")
	w.AddEvent(d.ID, "warn", "provider", "%s powered off from the panel", d.Hostname)
	return d, nil
}

// VPSReboot restarts a running node: the machine comes back with the same disk,
// the same services and a fresh uptime.
func (w *World) VPSReboot(owner, hostname string) (*Device, error) {
	d, _, err := w.NodeFor(owner, hostname)
	if err != nil {
		return nil, err
	}
	if !d.NetUp {
		return nil, fmt.Errorf("%s is not running — start it instead of rebooting it", d.Hostname)
	}
	d.setPowered(false, "reboot requested from the panel")
	d.setPowered(true, "")
	w.AddEvent(d.ID, "info", "provider", "%s rebooted from the panel", d.Hostname)
	return d, nil
}

// ---- reinstall: a different machine on the same address ----

// ReinstallVPS wipes the node's disk and lays down another distribution. The
// address, the region and the reserved addresses stay with the account — that
// is the whole point of a reinstall — while the filesystem, the accounts, the
// package manager and the installed packages are replaced by the new image's.
func (w *World) ReinstallVPS(owner, hostname, distro string) (*Device, string, error) {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return nil, "", err
	}
	if d.NetUp {
		return nil, "", fmt.Errorf("refusing to reinstall %s while it is running — power it off first", d.Hostname)
	}
	os := vpsImage(distro)
	if os.Distro == "" {
		return nil, "", fmt.Errorf("no such image: %s", distro)
	}
	rec.Rebuilds++
	d.OS = os
	d.FS = NewVFS()
	d.Services = map[string]*Service{}
	d.Procs = nil
	d.Installed = map[string]*VPkg{}
	d.InstalledFrom = map[string]string{}
	d.BootSet = nil
	seedFS(d, "vps")
	provisionPkgImage(w, d, distro)
	// a fresh image has fresh credentials: reusing the old password would make
	// "reinstall" a security theatre
	pass := randPass(fmt.Sprintf("%s#%d", hostname, rec.Rebuilds))
	d.Users = map[string]*User{}
	d.Users["root"] = &User{Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: os.Shell}
	d.Users["deploy"] = &User{Name: "deploy", UID: 1000, Pass: pass, Groups: []string{"deploy", "sudo"},
		Home: "/home/deploy", Shell: os.Shell}
	refreshPasswd(d)
	d.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "any",
		State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	d.Logf("info", "kernel", "reinstall: %s %s on a fresh disk", os.Distro, os.Ver)
	w.AddEvent(d.ID, "warn", "provider", "%s reinstalled as %s (rebuild %d): the old filesystem is gone",
		d.Hostname, os.Distro, rec.Rebuilds)
	return d, fmt.Sprintf("deploy@%s password: %s", d.Hostname, pass), nil
}

// ---- resize and disk ----

// VPSResize changes the machine's size. The panel bills the difference in
// monthly price: an upgrade costs the difference now, a downgrade refunds
// nothing, which is what every real provider does.
func (w *World) VPSResize(owner, hostname string, cores float64, memMB, diskMB int) (int64, int64, error) {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return 0, 0, err
	}
	if d.NetUp {
		return 0, 0, fmt.Errorf("refusing to resize %s while it is running — power it off first", d.Hostname)
	}
	if diskMB < d.HW.DiskMB {
		return 0, 0, fmt.Errorf("disk can only grow: %s has %d MiB, you asked for %d", d.Hostname, d.HW.DiskMB, diskMB)
	}
	if memMB < d.MemUsed() {
		return 0, 0, fmt.Errorf("cannot shrink below what the machine is using (%d MiB in use)", d.MemUsed())
	}
	price := NodePriceCents(cores, memMB, diskMB, planIPMode(w, rec))
	delta := price - rec.Monthly
	if delta > 0 {
		acc := w.Bank.Accts[owner]
		if acc == nil {
			return 0, 0, fmt.Errorf("no account")
		}
		if acc.Balance < delta {
			return 0, 0, fmt.Errorf("insufficient funds: the upgrade costs %d cents, you have %d", delta, acc.Balance)
		}
		acc.Balance -= delta
		acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -delta,
			Memo: "VPS " + d.Hostname + " upgrade", Balance: acc.Balance})
	}
	d.HW.Cores = int(cores)
	d.HW.RAMMB = memMB
	d.HW.DiskMB = diskMB
	rec.Monthly = price
	d.Logf("info", "kernel", "hardware changed: %g vCPU, %d MiB RAM, %d MiB disk", cores, memMB, diskMB)
	w.AddEvent(d.ID, "info", "provider", "%s resized to %g vCPU / %d MiB / %d MiB (now %s/mo)",
		d.Hostname, cores, memMB, diskMB, money(price))
	return price, delta, nil
}

// VPSAddDisk attaches another block of storage to a stopped node.
func (w *World) VPSAddDisk(owner, hostname string, gib int) (int64, error) {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return 0, err
	}
	if gib <= 0 {
		return 0, fmt.Errorf("disk size must be positive")
	}
	if d.NetUp {
		return 0, fmt.Errorf("refusing to add a disk to %s while it is running — power it off first", d.Hostname)
	}
	cost := int64(gib) * diskCentPerGiB
	acc := w.Bank.Accts[owner]
	if acc == nil {
		return 0, fmt.Errorf("no account")
	}
	if acc.Balance < cost {
		return 0, fmt.Errorf("insufficient funds: %d GiB of block storage costs %d cents, you have %d", gib, cost, acc.Balance)
	}
	acc.Balance -= cost
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -cost,
		Memo: "VPS " + d.Hostname + " +" + strconv.Itoa(gib) + " GiB", Balance: acc.Balance})
	d.HW.DiskMB += gib * 1024
	rec.Monthly += cost
	d.Logf("info", "kernel", "block storage attached: +%d GiB (%d MiB total)", gib, d.HW.DiskMB)
	w.AddEvent(d.ID, "info", "provider", "%s attached %d GiB of block storage", d.Hostname, gib)
	return rec.Monthly, nil
}

// ---- snapshots ----

// VPSnapshot copies the node's disk. A running machine can be snapshotted, as
// on a real hypervisor; the copy is the state at this moment, not a pointer to
// the live filesystem.
func (w *World) VPSnapshot(owner, hostname, name string) (*Snapshot, error) {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = "snap-" + strconv.Itoa(len(rec.Snapshots)+1)
	}
	for _, s := range rec.Snapshots {
		if s.Name == name {
			return nil, fmt.Errorf("a snapshot called %s already exists", name)
		}
	}
	s := Snapshot{Name: name, At: w.Sim, FS: cloneVFS(d.FS), Users: cloneUsers(d.Users),
		Installed: clonePkgs(d.Installed), InstalledFrom: cloneStrMap(d.InstalledFrom)}
	rec.Snapshots = append(rec.Snapshots, s)
	if len(rec.Snapshots) > 8 {
		rec.Snapshots = rec.Snapshots[len(rec.Snapshots)-8:]
	}
	d.Logf("info", "provider", "snapshot %s taken", name)
	w.AddEvent(d.ID, "info", "provider", "%s: snapshot %s taken", d.Hostname, name)
	return &s, nil
}

// VPSRestore rolls the node back to a snapshot. The disk is replaced wholesale,
// so this only happens on a stopped machine: restoring underneath a running
// system would be a different (and dishonest) operation.
func (w *World) VPSRestore(owner, hostname, name string) error {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return err
	}
	if d.NetUp {
		return fmt.Errorf("refusing to restore %s while it is running — power it off first", d.Hostname)
	}
	var snap *Snapshot
	for i := range rec.Snapshots {
		if rec.Snapshots[i].Name == name {
			snap = &rec.Snapshots[i]
		}
	}
	if snap == nil {
		return fmt.Errorf("no snapshot called %s on %s", name, d.Hostname)
	}
	d.FS = cloneVFS(snap.FS)
	d.Users = cloneUsers(snap.Users)
	d.Installed = clonePkgs(snap.Installed)
	d.InstalledFrom = cloneStrMap(snap.InstalledFrom)
	refreshPasswd(d)
	// the machine is stopped: start it to boot the restored system
	d.Logf("info", "provider", "restored from snapshot %s (%s)", snap.Name, snap.At.Format("2006-01-02 15:04"))
	w.AddEvent(d.ID, "warn", "provider", "%s rolled back to snapshot %s", d.Hostname, snap.Name)
	return nil
}

// VPSSetRDNS publishes what a reverse lookup of the node's address returns. The
// name is the operator's to choose — a PTR that leaked a person's identity
// would break §13's rule that a public IP must not be a public identity.
func (w *World) VPSSetRDNS(owner, hostname, ptr string) error {
	d, rec, err := w.NodeFor(owner, hostname)
	if err != nil {
		return err
	}
	if ptr != "" && (!strings.Contains(ptr, ".") || strings.ContainsAny(ptr, " /")) {
		return fmt.Errorf("a PTR name must be a fully qualified name like %s.neohome.example", d.Hostname)
	}
	if ptr == "" {
		ptr = d.Hostname + ".neohome.example"
	}
	rec.RDNS = ptr
	d.Logf("info", "provider", "reverse DNS set to %s", ptr)
	w.AddEvent(d.ID, "info", "provider", "%s: rDNS is now %s", d.Hostname, ptr)
	return nil
}

// ---- provisioning in a chosen region ----

// ProvisionVPSInRegion buys a node in a specific datacenter. The address comes
// out of that region's own block and the node's IPv6 out of that region's /48,
// so `whois` and a latency measurement both agree with the panel.
func (w *World) ProvisionVPSInRegion(owner, plan, hostname, distro, region string) (*Device, string, error) {
	return w.provisionVPS(owner, plan, hostname, distro, region)
}

func planIPMode(w *World, rec *NodeRecord) string {
	if w.Prov == nil || rec == nil {
		return "public"
	}
	for i := range w.Prov.Plans {
		if w.Prov.Plans[i].Name == rec.Plan {
			if w.Prov.Plans[i].IPMode == "" {
				return "public"
			}
			return w.Prov.Plans[i].IPMode
		}
	}
	return "public"
}

// ---- reverse DNS ----
//
// §12 lists rDNS as a property of a node, and §13's rule is that a lookup must
// never yield a person. These two meet here: the address answers with the name
// its operator published, which is the node's own hostname unless the customer
// chose something else — and never a human.

// ArpaName is the reverse-lookup name of an address ("5.2.0.192.in-addr.arpa").
func ArpaName(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])
	}
	b := a.As16()
	hex := fmt.Sprintf("%x", b[:])
	var parts []string
	for i := len(hex) - 1; i >= 0; i-- {
		parts = append(parts, string(hex[i]))
	}
	return strings.Join(parts, ".") + ".ip6.arpa"
}

// IsArpaName reports whether a query is a reverse lookup.
func IsArpaName(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	return strings.HasSuffix(n, ".in-addr.arpa") || strings.HasSuffix(n, ".ip6.arpa")
}

// arpaToIP decodes the address an arpa name asks about.
func arpaToIP(name string) (netip.Addr, bool) {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	switch {
	case strings.HasSuffix(n, ".in-addr.arpa"):
		labels := strings.Split(strings.TrimSuffix(n, ".in-addr.arpa"), ".")
		if len(labels) != 4 {
			return netip.Addr{}, false
		}
		for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
			labels[i], labels[j] = labels[j], labels[i]
		}
		a, err := netip.ParseAddr(strings.Join(labels, "."))
		if err != nil {
			return netip.Addr{}, false
		}
		return a, true
	case strings.HasSuffix(n, ".ip6.arpa"):
		labels := strings.Split(strings.TrimSuffix(n, ".ip6.arpa"), ".")
		if len(labels) != 32 {
			return netip.Addr{}, false
		}
		for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
			labels[i], labels[j] = labels[j], labels[i]
		}
		hex := strings.Join(labels, "")
		var raw [16]byte
		if _, err := fmt.Sscanf(hex, "%32x", &raw); err != nil {
			return netip.Addr{}, false
		}
		return netip.AddrFrom16(raw), true
	}
	return netip.Addr{}, false
}

// ptrAnswer answers a reverse lookup from real state: who owns the address, and
// whether that network publishes a reverse zone at all. An address whose block
// is not routed (private, CGNAT, link-local) has no PTR to give, and saying so
// is the honest answer.
func ptrAnswer(w *World, name string) (string, bool, string) {
	a, ok := arpaToIP(name)
	if !ok {
		return "", false, "NXDOMAIN (malformed reverse name)"
	}
	ip := a.String()
	info := ClassifyAddr(ip)
	switch info.Kind {
	case KindPrivate, KindShared, KindLoopback, KindLinkLocal, KindULA, KindUnassigned, KindUnknown:
		return "", false, "NXDOMAIN (no reverse zone covers " + ip + ": " + info.Scope + ")"
	}
	id, ok := w.IPMap[ip]
	if !ok {
		return "", false, "NXDOMAIN (nothing in the world holds " + ip + ")"
	}
	d := w.Devices[id]
	as := w.WAN.ASFor(ip)
	if as == nil || as.RDNS == "" {
		return "", false, "NXDOMAIN (the network announcing " + ip + " publishes no reverse zone)"
	}
	rec := w.NodeOf(d)
	ptr := d.Hostname + ".neohome.example"
	if rec != nil && rec.RDNS != "" {
		ptr = rec.RDNS
	}
	return ptr, true, fmt.Sprintf("authoritative via %s#53 (%s)", as.RDNS, as.Name)
}

// ReverseNameForTest exposes what the world publishes for an address, for tests
// that must see a PTR change.
func ReverseNameForTest(w *World, ip string) (string, bool) {
	name, ok, _ := ptrAnswer(w, ArpaName(ip))
	return name, ok
}

// ---- pricing, in one place ----

const (
	coreCentPerVCPU = 400 // $4.00 per vCPU
	ramCentPerGiB   = 80  // $0.80 per GiB of RAM
	diskCentPerGiB  = 20  // $0.20 per provisioned GiB
)

// NodePriceCents is the monthly price of a machine of this size. The rates are
// fitted to novapanel's published plans so the panel and the products cannot
// drift apart:
//
//	nano-shared 1 core,  512 MiB, 10 GiB, shared  -> $3.00
//	nano-1      1 core,  512 MiB, 10 GiB, public  -> $6.00
//	v6-sandbox  1 core,    1 GiB, 20 GiB, v6 only -> $4.40
//	small-2     2 core,    2 GiB, 40 GiB, public  -> $18.00
//	medium-4    4 core,    4 GiB, 80 GiB, public  -> $35.00
//
// Memory dominates, as it does on a real host; provisioned disk is charged
// lightly because it is thin. A node with no dedicated public IPv4 is billed
// half, because on a small plan most of the price *is* the address.
func NodePriceCents(cores float64, memMB, diskMB int, ipMode string) int64 {
	base := int64(cores)*coreCentPerVCPU +
		int64(memMB/1024)*ramCentPerGiB +
		int64(diskMB/1024)*diskCentPerGiB
	if ipMode == "shared" || ipMode == "v6only" {
		base /= 2
	}
	return base
}

func money(cents int64) string { return fmt.Sprintf("$%.2f", float64(cents)/100) }

// ---- copies ----

func cloneVFS(v *VFS) *VFS {
	if v == nil {
		return NewVFS()
	}
	out := &VFS{Nodes: make(map[string]*INode, len(v.Nodes))}
	for p, n := range v.Nodes {
		cp := *n
		if n.Data != nil {
			cp.Data = append([]byte(nil), n.Data...)
		}
		out.Nodes[p] = &cp
	}
	return out
}

func cloneUsers(u map[string]*User) map[string]*User {
	out := make(map[string]*User, len(u))
	for k, v := range u {
		cp := *v
		out[k] = &cp
	}
	return out
}

func clonePkgs(m map[string]*VPkg) map[string]*VPkg {
	out := make(map[string]*VPkg, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		cp := *v
		out[k] = &cp
	}
	return out
}

func cloneStrMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
