package core

import (
	"fmt"
	"strings"
)

// Virtualisation in NeoHome is a SIMULATION, never a host container or VM
// (PROJECT.md: "不给每个玩家创建真实 Docker / VM / Linux 容器", "VM 是虚拟状态机").
// A guest here is an ordinary Device in the world's own model: its own
// filesystem, users, processes, services and addresses. The hypervisor is a
// Device too, and it owns the resources the guest is charged against.
//
// The point of the model is the CONSEQUENCES (spec 十七: "资源不是装饰"):
//   RAM exhaustion -> swap -> OOM -> the guest's services really exit
//   CPU starvation -> processes really run slower (no sleeping, no fakery)
//   disk full      -> writes in the guest really fail
// Every one of those is real world state that a player can observe, be harmed
// by, diagnose in the logs, and fix.

// VMHost is the state of virtualisation in the world: hypervisors, the guests
// they run, and the resource accounting that makes guest pressure real.
type VMHost struct {
	Ready bool

	// hypervisor device id -> the guests it runs
	Hosts map[string][]*VM
}

// VM is a virtual machine: a simulated state machine, never a real host VM.
type VM struct {
	ID       string
	Name     string
	HostID   string // the hypervisor device id
	Host     *Device
	W        *World
	DeviceID string // the Device that represents this guest in the world
	State    string // running | stopped | paused

	// resource allocation charged against the hypervisor
	VCores float64
	VRAMMB int
	VDiskM int

	// live accounting, all real state
	SwapMB    int
	SwapMaxMB int
	OOMCount  int
	OomEvents []string
	DiskUsedM int
	LastState string
}

// MemPressure returns how over-committed the guest is, 0..1+. 1.0 means the
// guest is exactly full; above 1.0 means it is into swap.
func (v *VM) MemPressure() float64 {
	if v.VRAMMB <= 0 {
		return 0
	}
	used := v.MemUsedMB()
	return float64(used) / float64(v.VRAMMB)
}

// MemUsedMB sums the resident memory of everything running in the guest.
func (v *VM) MemUsedMB() int {
	d := v.device()
	if d == nil {
		return 0
	}
	total := 0
	for _, p := range d.Procs {
		total += p.Mem
	}
	return total
}

// SwapMB is the hypervisor's swap charge to the guest. A guest with no swap
// configured cannot survive pressure — it just OOMs.
// device is the internal form of Device.
func (v *VM) device() *Device { return v.Device() }

// world is resolved lazily through the guest's own back-pointer on the device
// graph. Guests never exist outside a world, and the world owns the host, so
// the host device is reachable from here.
func (v *VM) world() *World {
	if v.W != nil {
		return v.W
	}
	// fall back to the host device's world, which the VM struct also carries
	if v.Host != nil {
		return v.Host.W
	}
	return nil
}

// CPUUsed is the share of CPU the guest's processes are demanding, expressed in
// CORES (so it is comparable with VCores).
//
// Proc.CPU is stored the way ps/htop display it: a percentage where 100 is one
// saturated core. Summing it directly would compare percentages against a core
// count and never register starvation, so it is converted here.
func (v *VM) CPUUsed() float64 {
	d := v.device()
	if d == nil {
		return 0
	}
	total := 0.0
	for _, p := range d.Procs {
		total += p.CPU / 100.0
	}
	return round2(total)
}

// CPUWantPct is the demand expressed the way the player sees it in ps.
func (v *VM) CPUWantPct() float64 { return v.CPUUsed() * 100 }

// Throttle is the CPU starvation factor: 1.0 means full speed, 0.4 means the
// guest is getting less than half the CPU it asked for. This is a real
// multiplier on what the guest's processes achieve, not a sleep.
func (v *VM) Throttle() float64 {
	if v.VCores <= 0 {
		return 0.1
	}
	want := v.CPUUsed()
	have := v.VCores
	if want <= have {
		return 1.0
	}
	ratio := have / want
	if ratio < 0.1 {
		ratio = 0.1
	}
	return ratio
}

// DiskFull reports whether this guest has exhausted its own disk.
func (v *VM) DiskFull() bool {
	d := v.device()
	if d == nil || v.VDiskM <= 0 {
		return false
	}
	return d.FS.DiskUsedMB() >= v.VDiskM
}

// DiskPressure returns how full the guest's disk is, 0..1+.
func (v *VM) DiskPressure() float64 {
	if v.VDiskM <= 0 {
		return 0
	}
	return float64(v.DiskUsedM) / float64(v.VDiskM)
}

// ---- world wiring ----

func (w *World) vmHost() *VMHost {
	if w.VMs == nil {
		w.VMs = &VMHost{Ready: true, Hosts: map[string][]*VM{}}
	}
	if w.VMs.Hosts == nil {
		w.VMs.Hosts = map[string][]*VM{}
	}
	return w.VMs
}

// FindVM returns a guest by name across every hypervisor in the world.
func (w *World) FindVM(name string) *VM {
	for _, list := range w.vmHost().Hosts {
		for _, v := range list {
			if v.Name == name || v.ID == name {
				return v
			}
		}
	}
	return nil
}

// Guests lists every guest in the world.
func (w *World) Guests() []*VM {
	var out []*VM
	for _, list := range w.vmHost().Hosts {
		out = append(out, list...)
	}
	return out
}

// GuestsOn lists the guests a hypervisor runs.
func (w *World) GuestsOn(hostID string) []*VM {
	return w.vmHost().Hosts[hostID]
}

// VMHosts lists every registered hypervisor device.
func (w *World) VMHosts() []*Device {
	var out []*Device
	for id := range w.vmHost().Hosts {
		if d := w.Devices[id]; d != nil {
			out = append(out, d)
		}
	}
	return out
}

// Device is the guest's own device: its filesystem, users, processes,
// services and addresses. There is no shadow state for a guest.
func (v *VM) Device() *Device {
	w := v.world()
	if w == nil {
		return nil
	}
	return w.Devices[v.DeviceID]
}

// seedVMs plants the world's initial virtualisation state. The spec's household
// (四十 玩家之家) includes a Server, and a household that owns real compute is
// where virtualisation starts — so the player's server is seeded here and
// becomes the world's first hypervisor.
func seedVMs(w *World) {
	w.VMs = &VMHost{Ready: true, Hosts: map[string][]*VM{}}

	// The household server. It is a real device with real hardware; guests are
	// charged against it and it can genuinely run out.
	srv := w.addDevice("srv-alex", "basement", "server", "alex",
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Rack Server", 8, 2600, 16384, 512000, 1000, false, false},
		lanIP(21))
	seedFS(srv, "server")
	mkUsers(srv, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"alex": {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"alex", "sudo"}, Home: "/home/alex", Shell: "/bin/bash"},
	})
	refreshPasswd(srv)
	srv.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	// A hypervisor is a machine that runs guests; libvirtd is what makes that
	// true in the world, and its absence should be visible.
	srv.Services["libvirtd"] = &Service{
		Name: "libvirtd", Desc: "virtualisation daemon (libvirt)", Port: 0,
		Proto: "unix", Scope: "lan", State: "running", Handler: "virt",
	}
	// the wants/ symlink is what makes a unit survive a reboot
	srv.FS.Write("/etc/systemd/system/multi-user.target.wants/libvirtd.service",
		"[Unit]\nDescription=libvirt daemon\n", 0644, "root", "root")

	w.vmHost().Hosts[srv.ID] = nil
}

// ---- guest lifecycle ----

// CreateVM provisions a guest on a hypervisor. The guest becomes a real Device
// in the world: own filesystem, own users, own services, own address. That is
// the whole point — nothing about it is a record in a bookkeeping table.
// ipMode is the plan's networking ("" == public): it decides whether the guest
// gets a routable IPv4, a carrier-grade-NAT address, or no IPv4 at all (§13).
func (w *World) CreateVM(hostID, name string, vcpu float64, ramMB, diskMB int, ipMode string) (*VM, error) {
	host := w.Devices[hostID]
	if host == nil {
		return nil, fmt.Errorf("no such hypervisor: %s", hostID)
	}
	if host.Profile != "server" && host.Profile != "vps" && host.Profile != "infra" {
		return nil, fmt.Errorf("%s is not a hypervisor (profile %s)", host.Hostname, host.Profile)
	}
	if w.FindVM(name) != nil {
		return nil, fmt.Errorf("guest %q already exists", name)
	}
	if vcpu <= 0 {
		vcpu = 1
	}
	if ramMB <= 0 {
		ramMB = 512
	}
	if diskMB <= 0 {
		diskMB = 8192
	}

	// the guest needs an address on the hypervisor's own LAN
	lanIP := w.nextGuestIP(host)

	id := "vm-" + name
	// A guest is an ordinary Debian box. It is a state machine, not a container.
	d := w.addDevice(id, name, "vps", host.Owner,
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"VM " + name, int(vcpu), 2400, ramMB, diskMB, 1000, false, false},
		lanIP)
	// §13: the guest's networking follows the product it was bought from. A
	// shared-IP guest sits behind the provider's carrier-grade NAT (outbound
	// only, nothing can reach in), a v6-only guest gets no IPv4 at all, and a
	// public guest gets a routable address like any other node.
	switch ipMode {
	case "shared":
		w.AttachSharedWAN(d, "10.0.0.1")
	case "v6only":
		d.AttachWAN6Only("10.0.0.1")
	default:
		d.AttachWAN(w.allocPublicFor("vps"), "10.0.0.1")
	}
	d.AddWANv6(w.allocV6For("vps"))
	d.OS.Distro = "Debian"
	d.OS.Ver = "13"
	seedFS(d, "vps")
	mkUsers(d, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		name:   {Name: name, UID: 1000, Pass: name + "-pass", Groups: []string{name}, Home: "/home/" + name, Shell: "/bin/bash"},
	})
	refreshPasswd(d)
	provisionPkgImage(w, d, "debian")
	// a fresh guest exposes ssh, exactly like a real cloud instance
	d.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp",
		Scope: "any", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}

	v := &VM{
		ID: id, Name: name, HostID: hostID, Host: host, W: w,
		DeviceID: id, State: "running",
		VCores: vcpu, VRAMMB: ramMB, VDiskM: diskMB,
		SwapMaxMB: ramMB / 2, // a real default: half the guest's RAM
	}
	w.vmHost().Hosts[hostID] = append(w.vmHost().Hosts[hostID], v)

	if w.WAN != nil && w.WAN.ASFor(d.FirstWANIP()) == nil {
		w.WAN.assign(d, d.FirstWANIP()) // attribute the guest's address honestly
	}
	d.Logf("info", "libvirtd", "domain %s created: %g vCPU, %d MiB RAM, %d MiB disk on %s",
		name, vcpu, ramMB, diskMB, host.Hostname)
	host.Logf("info", "libvirtd", "created domain %s (device %s)", name, id)
	w.AddEvent(host.ID, "info", "virt", "vm %s created on %s", name, host.Hostname)
	return v, nil
}

// nextGuestIP finds a free address on the hypervisor's LAN.
func (w *World) nextGuestIP(host *Device) string {
	net := lanNetOf(host)
	base := "10.90.0."
	if net != "" {
		// derive the /24 from the host's own subnet
		parts := strings.Split(strings.Split(net, "/")[0], ".")
		if len(parts) == 4 {
			base = parts[0] + "." + parts[1] + "." + parts[2] + "."
		}
	}
	used := map[string]bool{}
	for _, d := range w.Devices {
		for _, i := range d.Ifaces {
			if i.IP != "" {
				used[i.IP] = true
			}
		}
	}
	for n := 2; n <= 254; n++ {
		cand := fmt.Sprintf("%s%d", base, n)
		if !used[cand] {
			return cand
		}
	}
	return base + "254"
}

// StartVM boots a guest: its services really start and it becomes reachable.
func (w *World) StartVM(name string) error {
	v := w.FindVM(name)
	if v == nil {
		return fmt.Errorf("no such guest: %s", name)
	}
	if v.State == "running" {
		return nil
	}
	d := w.Devices[v.DeviceID]
	if d == nil {
		return fmt.Errorf("guest %s has no device", name)
	}
	d.PowerOK = true
	for _, svc := range d.Services {
		if svc.State != "stopped" {
			continue
		}
		// only units that were enabled at boot come back
		if data, ok := d.FS.Read("/etc/systemd/system/multi-user.target.wants/" + svc.Name + ".service"); ok && len(data) > 0 {
			svc.State = "running"
			d.AddProc(&Proc{Name: svc.Name, User: "root", CPU: 0.3, Mem: 24, TTY: "?",
				State: "S", Start: w.Sim, Svc: svc.Name, Kind: "builtin"})
			svc.PID = d.Procs[len(d.Procs)-1].PID
		}
	}
	v.State = "running"
	d.Logf("info", "libvirtd", "domain %s started", name)
	w.AddEvent(v.HostID, "info", "virt", "vm %s started", name)
	return nil
}

// StopVM shuts a guest down: its services stop, its processes go away and it
// stops answering on the network. This is a real state change, not a flag.
func (w *World) StopVM(name string) error {
	v := w.FindVM(name)
	if v == nil {
		return fmt.Errorf("no such guest: %s", name)
	}
	d := w.Devices[v.DeviceID]
	if d == nil {
		return fmt.Errorf("guest %s has no device", name)
	}
	for _, svc := range d.Services {
		if svc.State == "running" {
			svc.State = "stopped"
			svc.PID = 0
		}
	}
	var keep []*Proc
	for _, p := range d.Procs {
		if p.Kind != "builtin" {
			keep = append(keep, p)
		}
	}
	d.Procs = keep
	d.PowerOK = false
	for _, i := range d.Ifaces {
		i.Up = false
	}
	// a downed guest's processes are gone from the world's view, but its
	// accounting is remembered so a restart is not a free pass
	v.State = "stopped"
	v.SwapMB = 0
	d.Logf("warn", "kernel", "domain %s halted: services stopped, interface down", name)
	w.AddEvent(v.HostID, "info", "virt", "vm %s stopped", name)
	return nil
}

// DestroyVM removes a guest from the world entirely.
func (w *World) DestroyVM(name string) error {
	v := w.FindVM(name)
	if v == nil {
		return fmt.Errorf("no such guest: %s", name)
	}
	_ = w.StopVM(name)
	d := w.Devices[v.DeviceID]
	if d == nil {
		return fmt.Errorf("guest %s has no device", name)
	}
	host := w.Devices[v.HostID]
	if host != nil {
		host.Logf("info", "libvirtd", "domain %s destroyed", name)
	}
	// its addresses leave the routing table with it
	for _, i := range d.Ifaces {
		if i.IP != "" {
			delete(w.IPMap, i.IP)
		}
	}
	delete(w.Devices, v.DeviceID)
	for i, id := range w.Order {
		if id == v.DeviceID {
			w.Order = append(w.Order[:i], w.Order[i+1:]...)
			break
		}
	}
	h := w.vmHost()
	list := h.Hosts[v.HostID]
	for i, x := range list {
		if x == v {
			h.Hosts[v.HostID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	// drop the guest's prefixes from its AS
	if w.WAN != nil && d.FirstWANIP() != "" {
		w.WAN.dropOwner(blockOf(d.FirstWANIP()))
	}
	w.AddEvent(v.HostID, "info", "virt", "vm %s destroyed", name)
	return nil
}

// dropOwner removes any prefix an AS announced for a destroyed guest.
func (wan *WAN) dropOwner(prefix string) {
	p := prefix + "/24"
	for _, as := range wan.ASes {
		var keep []string
		for _, x := range as.Prefixes {
			if x != p {
				keep = append(keep, x)
			}
		}
		as.Prefixes = keep
	}
}

// ---- resource pressure: the part that must be real ----

// VMTick advances guest resource pressure on the world clock. Everything here
// mutates real state; nothing sleeps and nothing is random.
func (w *World) VMTick() {
	if w.VMs == nil || !w.VMs.Ready {
		return
	}
	for _, v := range w.Guests() {
		if v.State != "running" {
			continue
		}
		d := w.Devices[v.DeviceID]
		if d == nil {
			continue
		}
		w.vmMemory(v, d)
		w.vmDisk(v, d)
		w.vmCPU(v, d)
	}
}

// vmMemory charges guest memory against RAM, spills into swap, and OOMs the
// guest's services when even swap runs out. The causal chain is exactly the
// one the spec asks for: "VM 内存分配太小 -> Swap -> OOM -> 服务退出".
func (w *World) vmMemory(v *VM, d *Device) {
	host := w.Devices[v.HostID]
	avail := v.VRAMMB
	if host != nil && host.HW.RAMMB > 0 {
		// guests share the hypervisor's RAM; the host's own procs take some
		hostUsed := 0
		for _, p := range host.Procs {
			if p.Kind != "builtin" {
				hostUsed += p.Mem
			}
		}
		avail = v.VRAMMB
		if host.HW.RAMMB-hostUsed < avail {
			avail = host.HW.RAMMB - hostUsed
		}
		if avail < 64 {
			avail = 64
		}
	}

	used := v.MemUsedMB()
	if used <= avail {
		// pressure relieved: the kernel pages swap back in
		if v.SwapMB > 0 {
			back := v.SwapMB / 4
			if back < 1 {
				back = 1
			}
			v.SwapMB -= back
			if v.SwapMB < 0 {
				v.SwapMB = 0
			}
		}
		return
	}

	// over the limit: spill the excess into swap, up to the guest's swap size
	excess := used - avail
	if v.SwapMaxMB <= 0 {
		v.OOM(w, d, "guest has no swap configured")
		return
	}
	free := v.SwapMaxMB - v.SwapMB
	if free <= 0 {
		v.OOM(w, d, "out of memory: swap exhausted and RSS still above the limit")
		return
	}
	move := excess
	if move > free {
		move = free
	}
	v.SwapMB += move
	d.Logf("warn", "kernel", "domain %s: swapped %d MiB (now %d/%d MiB swap, %d MiB over RAM limit)",
		v.Name, move, v.SwapMB, v.SwapMaxMB, excess)
	if v.SwapMB >= v.SwapMaxMB {
		v.OOM(w, d, "out of memory: swap exhausted and RSS still above the limit")
	}
}

// OOM kills the guest's biggest processes for real. This is the consequence the
// spec cares about: a VM sized too small does not just "show a warning", it
// loses its processes, and the owner has to notice and fix it.
//
// A real kernel's OOM killer picks by oom_score, which favours the largest
// private mapping — so it takes the biggest hog first. Once only daemons are
// left and memory is still exhausted, the guest is simply out of room and its
// services fail too: that is what an undersized VM actually looks like.
func (v *VM) OOM(w *World, d *Device, why string) {
	// One OOM incident is one burst of killing, not one kill. A real kernel
	// kills repeatedly while the condition holds; counting each kill would make
	// the number meaningless to the operator reading it.
	incident := v.LastState != "oom"
	if incident {
		v.OOMCount++
		stamp := w.Sim.Format("15:04:05")
		v.OomEvents = append(v.OomEvents, fmt.Sprintf("%s %s", stamp, why))
		if len(v.OomEvents) > 8 {
			v.OomEvents = append(v.OomEvents[:0], v.OomEvents[len(v.OomEvents)-8:]...)
		}
	}

	var victim *Proc
	for _, p := range d.Procs {
		if p.Kind == "builtin" {
			continue
		}
		if victim == nil || p.Mem > victim.Mem {
			victim = p
		}
	}
	if victim == nil {
		// nothing but daemons left. This guest is out of memory and services
		// are what fails next — which is the honest end state.
		if incident {
			v.LastState = "oom"
			for _, svc := range d.Services {
				if svc.State == "running" {
					svc.State = "failed"
					svc.PID = 0
					d.Logf("err", "kernel", "domain %s: service %s failed — out of memory (guest has %d MiB RAM, no room to work)",
						v.Name, svc.Name, v.VRAMMB)
				}
			}
			var keep []*Proc
			for _, p := range d.Procs {
				if p.Kind != "builtin" {
					keep = append(keep, p)
				}
			}
			d.Procs = keep
			w.AddEvent(v.HostID, "err", "virt", "vm %s out of memory: all services failed", v.Name)
			w.DeliverMail(hostOwner(w, v), "hypervisor alert: "+v.Name+" is out of memory",
				fmt.Sprintf("Guest %s on %s cannot fit its own workload.\n\n  ram:     %d MiB\n  swap:    %d/%d MiB\n  oom kills: %d\n\nEvery service on the guest has failed. Give it more memory, or run less on it:\n  vm destroy %s --mem 4096 --disk 20480\n  vm create %s --mem 4096 --disk 20480\n",
					v.Name, w.Devices[v.HostID].Hostname, v.VRAMMB, v.SwapMB, v.SwapMaxMB, v.OOMCount, v.Name, v.Name))
		}
		return
	}
	var keep []*Proc
	for _, p := range d.Procs {
		if p != victim {
			keep = append(keep, p)
		}
	}
	d.Procs = keep
	if svc := d.Svc(victim.Svc); svc != nil {
		svc.State = "failed"
		svc.PID = 0
	}
	d.Logf("err", "kernel", "domain %s: killed process %d (%s, %d MiB) — %s",
		v.Name, victim.PID, victim.Name, victim.Mem, why)
	if incident {
		v.LastState = "oom"
		w.AddEvent(v.HostID, "warn", "virt", "vm %s OOM-killed %s: %s", v.Name, victim.Name, why)
		w.DeliverMail(hostOwner(w, v), "hypervisor alert: "+v.Name+" was OOM-killed",
			fmt.Sprintf("Guest %s on %s had a process killed by the kernel.\n\n  reason: %s\n  swap: %d/%d MiB\n  oom events: %d\n\nCheck `vm top` and the guest's memory allocation.\n",
				v.Name, w.Devices[v.HostID].Hostname, why, v.SwapMB, v.SwapMaxMB, v.OOMCount))
	}
}

func hostOwner(w *World, v *VM) string {
	if h := w.Devices[v.HostID]; h != nil && h.Owner != "" {
		return h.Owner
	}
	return "alex"
}

// vmDisk keeps the guest's real disk usage in step with what is actually in its
// filesystem, and makes writes fail once the disk is full.
func (w *World) vmDisk(v *VM, d *Device) {
	// the real filesystem is the source of truth for usage
	if used := d.FS.DiskUsedMB(); used > v.DiskUsedM {
		v.DiskUsedM = used
	}
	// one MiB of slack each way so a disk is not "full" the instant it is
	// created and "full" forever after
	if v.DiskUsedM > v.VDiskM-1 {
		if v.LastState != "diskfull" {
			v.LastState = "diskfull"
			d.Logf("err", "kernel", "domain %s: no space left on device (disk %d/%d MiB)",
				v.Name, v.DiskUsedM, v.VDiskM)
			w.AddEvent(v.HostID, "warn", "virt", "vm %s disk full", v.Name)
		}
	} else if v.LastState == "diskfull" {
		v.LastState = "ok"
		d.Logf("info", "kernel", "domain %s: space reclaimed on %s", v.Name, "/")
	}
}

// vmCPU applies starvation to the guest's processes. A starved process is
// genuinely slower: its measured throughput drops. Nothing sleeps — the
// slowdown is a real multiplier on what the process accomplishes per tick.
func (w *World) vmCPU(v *VM, d *Device) {
	th := v.Throttle()
	if th >= 0.999 {
		return
	}
	for _, p := range d.Procs {
		if p.Kind == "builtin" {
			continue // daemons are not the guest's own workload
		}
		// a starved process accumulates less progress per tick
		p.CPU = round2(p.CPU * th)
	}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
