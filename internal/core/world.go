// Package core holds the whole simulated world: devices, virtual filesystem,
// processes, services, networking, economy and agents. Nothing here executes
// real binaries — everything is state.
package core

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sync"
	"time"
)

type World struct {
	mu      sync.Mutex // unexported: gob skips it, and the world stays pure data
	Started time.Time
	Sim     time.Time

	Devices map[string]*Device
	Order   []string
	IPMap   map[string]string // ip -> device id

	Players map[string]*Player

	PublicCounter int // allocates 203.0.113.x / 198.51.100.x pool
	Records       []DNSRecord
	Repos         map[string]*Repo
	Chat          *Chat
	Bank          *Bank
	Jobs          *Jobs
	Prov          *Provider
	CauseFault    Fault
	Vulns         []Vuln

	// The SSH host key is part of the world's identity: a server whose key
	// changes every restart is not the same server, and every client would
	// rightly complain. Stored as PKCS8 bytes so gob can carry it.
	SSHHostKey []byte

	Events   []Event
	News     []string
	MailLog  []string
	NPCNames []string
	Case     *Case
	// §34: the organisations that investigate, and the cases they hold. Both
	// are world state, so a case survives a save exactly as a ban does.
	Desks []*Desk
	Abuse *AbuseLog

	BusyDay   []string // trace of what the NPC world did today, for the news feed
	nextPID   int
	TickCount int
	Tasks     []*Task

	assistSkills    int
	assistTracks    []string
	assistantCanFix bool

	// uci's staging area: `uci set` records pending edits here and `uci
	// commit` applies them to the config file. Unexported, so a save does not
	// carry them — uncommitted changes are exactly the kind of thing a reboot
	// loses, which is the behaviour the tool is famous for.
	uciStage map[string]*uciPending

	// ---- workstream slots (WS-0). Shape owned by mail.go.
	mail *MailBox

	Power              float64 // household power budget 0..1
	Heating            bool
	UtilitiesSuspended bool
	PowerCut           bool // main breaker thrown
	PowerOutTicks      int  // game-ticks the house has been dark

	// ---- workstream slots. Each pointer's SHAPE is owned by one file:
	//   WAN  -> wan.go   (public internet / transit / ASN)
	//   Cron -> cron.go  (world scheduler)
	//   VMs  -> vm.go    (virtualisation)
	//   TLS  -> tls.go   (certificate authorities / issued certs)
	//   BBS  -> bbs.go   (bulletin board: boards, posts, NPC replies)
	//   IoT  -> iot.go   (front-door camera recordings + smart lock)
	//   SMS  -> sms.go   (phones: registry, spools, batteries)
	//   USB  -> usb.go   (sticks: attachment, device nodes)
	// Touch only the pointer here; add fields inside the owning file.
	WAN  *WAN
	Cron *CronState
	VMs  *VMHost
	TLS  *TLSState
	BBS  *BBS
	IoT  *IoT
	SMS  *SMS
	USB  *USB
}

type Event struct {
	At      time.Time
	Dev     string
	Level   string
	Source  string
	Message string
}

type DNSRecord struct {
	Name string
	IP   string
}

type Player struct {
	Name      string
	Pass      string
	MCPOnly   bool
	PC        string
	Router    string
	NAS       string
	Assistant string
	HouseKey  string
	Created   time.Time
}

type Device struct {
	W            *World
	ID           string
	Hostname     string
	Profile      string // pc|router|nas|vps|infra|core
	Owner        string
	OS           OSInfo
	HW           Hardware
	FS           *VFS
	Users        map[string]*User
	Procs        []*Proc
	Services     map[string]*Service
	Ifaces       []*Iface
	Boot         time.Time
	PowerOK      bool
	NetUp        bool     // false once the machine is dark
	MainsDropped bool     // this device's plug is pulled
	UPS          *UPSInfo // battery, if the machine has one
	BootSet      []string // services that were running before the lights went out
	MeterKWh     float64
	BillDue      int64
	Dmesg        []string
	DHCPL        map[string]Lease
	Notes        string
	Purposes     string

	Installed     map[string]*VPkg
	InstalledFrom map[string]string // package name -> repository that served it

	// NATed marks a node with no public IPv4 of its own: everything it sends
	// leaves through its provider's carrier-grade NAT, so the address a remote
	// host logs is the provider's, not the customer's. §13's 「公开 IP 不等于
	// 公开玩家真实身份」 is literally this field.
	NATed    bool
	Mounts   []Mount
	Sessions map[string]*TermSession
	Active   []Login
	Fail2Ban map[string]int // source ip -> failed count

	// ---- §15 家庭设备: the wire, the battery and the spooler ----
	//
	// Switch is the port configuration of a managed switch; Uplink and
	// UplinkPort say which port a device is plugged into. PoEPowered marks a
	// device that takes its power over the ethernet cable, so its power state
	// is its switch's business rather than the wall socket's.
	Switch      *SwitchState
	Uplink      string
	UplinkPort  int
	PoEPowered  bool
	PoeUp       bool // whether the PoE port is delivering power right now
	Battery     *LaptopBattery
	Printer     *PrinterState
	BackupIndex int // the last backup run, for the NAS's own records

	// Rsrc is §17's kernel-side accounting: load average, swap, OOM count,
	// fork refusals, dropped log lines and the last measured CPU/work rates.
	// Everything else about resources (memory used, disk used, process count)
	// is derived from the real state instead of stored here.
	Rsrc *Rsrc

	// Security is §33's defensive state: the connection attempts this machine
	// really saw, the authentication failures it recorded, the bans it has
	// issued (which Dial/Reach enforce), the alerts its detectors raised and
	// the lines its neighbours forwarded to it as their log server. Sec()
	// creates it lazily, so a save written before §33 stays loadable.
	Security *SecState
}

type Mount struct {
	Src  string // nas id + path "nas-alex:/srv/data"
	Dst  string
	FSTy string // nfs|smb|vfat
}

type Login struct {
	User string
	TTY  string
	From string
	At   time.Time
}

type TermSession struct {
	Name  string
	Lines []string
	CWD   string
	User  string
	Proc  *Proc
	Alive bool
}

type Lease struct {
	MAC string
	IP  string
	GW  string
	DNS string
}

type OSInfo struct {
	Distro string
	Ver    string
	Kernel string
	Arch   string
	Shell  string
}

type Hardware struct {
	Model   string
	Cores   int
	CPUMHz  int
	RAMMB   int
	DiskMB  int
	NetMbps int
	HasWifi bool
	Battery bool
}

type Iface struct {
	Name string
	IP   string
	CIDR string // network like 10.77.1.0/24
	// IP6 are this interface's IPv6 addresses as CIDRs, in the order the
	// kernel would list them: the global address first, then the link-local
	// one. §13's v6 kinds (public IPv6, ULA, link-local) are all spelled here.
	IP6 []string
	// SharedIP is the carrier-grade-NAT address a provider numbers a customer
	// with when the plan has no public IPv4. It is NOT this interface's
	// address: it is what the provider translated to on the way out, and it is
	// deliberately absent from the world's address map.
	SharedIP string
	// Extra are secondary IPv4 addresses on this interface: §13's "virtual
	// IP", a reserved address the provider lends to one node and can move.
	// Real kernels print these with the `secondary` flag, which is why the
	// command does too.
	Extra []string
	MAC   string
	Zone  string // lo|lan|wan
	GW    string
	GW6   string // the v6 default gateway, when the ISP provides one
	Up    bool
	Link  string // device id directly connected
	Mode  string // static|dhcp
}

type User struct {
	Name   string
	UID    int
	Pass   string
	Groups []string
	Home   string
	Shell  string
	IsBot  bool
	// HistFile is where this account's shell history is kept, as HISTFILE
	// actually works. The file is world state: it survives the session, the
	// account's own user can rewrite it, and it is evidence on disk.
	HistFile string
}

// CheckPassword is the one rule for password checks in this world: an account
// with no stored password never authenticates by password. Service accounts
// (www-data, ftp, nobody) exist to own files and run daemons, not to log in; a
// direct `pass != u.Pass` comparison would hand anyone a session by pressing
// enter at the prompt, so every credential check goes through here.
func (u *User) CheckPassword(pw string) bool {
	if u == nil || u.Pass == "" {
		return false
	}
	return pw == u.Pass
}

type Proc struct {
	PID  int
	Name string
	Args string
	User string
	// CPU is what the process actually got last tick; WantCPU is what it asked
	// for. Under contention the two differ, and that difference is what makes
	// "CPU 不足 → 进程变慢" measurable instead of a claim.
	CPU     float64
	WantCPU float64
	Mem     int
	TTY     string
	State   string
	Start   time.Time
	// StartTick/EndTick bound a background load (`stress --timeout`); EndTick 0
	// means it runs until something kills it.
	StartTick int
	EndTick   int
	Nice      int
	Svc       string
	Kind      string // builtin|task|shell|load
}

type Service struct {
	Name    string
	Desc    string
	Port    int
	Proto   string
	Scope   string // lan|wan|any
	State   string // running|stopped|failed
	Handler string
	PID     int
	Conf    string
	Banner  string
	// TLSCert names the certificate (a leaf in World.TLS) this service
	// presents on a TLS handshake; empty means the service speaks plaintext.
	TLSCert string
	// TelnetUser optionally pre-fills the account a telnetd presents first.
	TelnetUser string
	// MonitDown counts consecutive failed checks a §33 watchdog has seen for
	// this service; it is reset when the service runs again.
	MonitDown int
}

type Task struct {
	ID        int
	Who       string
	Kind      string
	JobID     string
	DeviceID  string
	Done      bool
	DoneAt    time.Time
	StartTick int
	// Progress is how much of the work is done: a tick-sized task advances by
	// the CPU share the machine actually had (§17), so a busy node finishes it
	// late instead of on schedule.
	Progress  float64
	Narrative string
}

type Account struct {
	Owner   string
	Name    string
	Balance int64
	Tx      []Tx
}

type Tx struct {
	At      time.Time
	Amount  int64
	Memo    string
	Balance int64
}

type Bank struct {
	DeviceID string
	Accts    map[string]*Account
}

type Job struct {
	ID       string
	Title    string
	Client   string
	Pay      int64
	Tier     int
	Help     string
	Verify   string
	Target   string
	Accepted string
	Done     bool
}

type Jobs struct {
	DeviceID string
	List     []*Job
}

type ChatMsg struct {
	At   time.Time
	Chan string
	Nick string
	Text string
}

type Chat struct {
	DeviceID string
	Channels map[string]bool
	History  []ChatMsg
}

type Mail struct {
	DeviceID string
}

// Case is the evidence graph: the honest substrate behind Heat/Trace.
type Case struct {
	Events   []Evidence
	Heat     int
	Notified bool
}

type Evidence struct {
	At     time.Time
	Kind   string // net|auth|proc|file|ids
	Actor  string // who did it (player name, npc, unknown)
	Origin string // source ip
	Target string // device id
	Detail string
	Weight int // how much this raises heat
}

type Provider struct {
	DeviceID    string
	APIKey      string
	Plans       []Plan
	Issued      int
	FirstMonths int
	// Regions is the datacenter list the panel sells into (§12 lets the player
	// choose one, and a region is an AS with its own block, not a label).
	Regions []Region
	// Nodes is the provider's record of every node it has rented out.
	Nodes map[string]*NodeRecord
}

type Plan struct {
	Name    string
	Cores   int
	RAM     int
	Disk    int
	Monthly int64
	Region  string
	// IPMode is what the plan's networking really is, and it decides what the
	// node can do: "public" ships one routable IPv4, "shared" puts the node
	// behind the provider's carrier-grade NAT (outbound only — no port
	// forward, no inbound at all), "v6only" ships no IPv4 the internet can
	// reach. §13 lists the kinds; §12 makes the player choose one.
	IPMode string
	// V6 is whether the plan includes a routed IPv6 /64. On the shared and
	// v6only plans this is what makes hosting possible at all — which is how
	// a real budget provider pushes customers onto v6.
	V6 bool
}

// Repo is one repository tree as the world serves it: a real path on a real
// device, with the metadata those files carry. The catalogue in Pkgs is the
// list of packages the repository *can* carry; what a box can actually
// install is decided by the served index files, verified through the
// distribution's own rules (see pkgfiles.go / pkgnet.go).
type Repo struct {
	Name     string
	URL      string
	DeviceID string // the mirror that serves installs
	Distro   string // debian|ubuntu|alpine|arch|fedora|openwrt
	Suite    string
	// SuiteAliases are the other names a mirror publishes the same tree
	// under (stable == bookworm, v3.20 == edge at times).
	SuiteAliases []string
	Comps        []string
	Signed       bool
	Status       string // SYNCED|BEHIND|OFFLINE|PARTIAL|CORRUPTED
	// StatusWhy is always the honest cause of Status, never a mood.
	StatusWhy string
	Pkgs      map[string]*VPkg
	SyncLag   string

	// Path is the URL subpath this tree lives under on the serving host.
	Path string
	// UpstreamID is the archive host this mirror syncs from ("" when the
	// repo IS the archive).
	UpstreamID string
	// SignKey is the fingerprint the served metadata claims. A device can
	// only install from this repo if its keyring holds that key.
	SignKey string
	// LastSync is sim time of the last successful sync; a repo that drifts
	// past the threshold is BEHIND, which is a fact about the world, not a
	// timer the player waits out.
	LastSync time.Time
	// ReleaseHash is what the served InRelease claims, per component. It is
	// computed from the bytes that were written, so a sync interrupted
	// between writing an index and its release file really does mismatch.
	ReleaseHash map[string]string
	// SyncPhase: 0 idle; >0 a sync process is running (see MirrorTick).
	SyncPhase int
	// SyncCredit accumulates the mirror host's work-per-tick: a phase costs one
	// unit, so a slow or busy host finishes the same sync in more ticks (§17).
	SyncCredit float64
	// SyncDirty records that the running sync already rewrote index files:
	// interrupting after that leaves the tree inconsistent (CORRUPTED).
	SyncDirty bool
	// Missing lists components the upstream no longer carries: the real
	// cause of PARTIAL, and the reason those indexes are gone from the tree.
	Missing []string
}

type VPkg struct {
	Name    string
	Version string
	Arch    string
	Desc    string
	Size    int
	// Comp is the repository component the package lives in (main,
	// contrib, community…). Sources decide which components a box sees.
	Comp      string
	Depends   []string
	Signed    bool
	Files     map[string]*PkgFile
	Service   *SvcSpec
	Procs     []ProcSpec
	PostInst  string
	PreRemove string
	Malicious bool
}

// ProcSpec is a background process a package starts. Removing the package
// stops exactly the processes it declared — that symmetry is what makes
// "postinst started something you did not ask for" findable and undoable.
type ProcSpec struct {
	Name string
	Args string
	User string
	CPU  float64
	Mem  int
	// Kind is the process kind recorded on the device (builtin|task|…).
	Kind string
}

type PkgFile struct {
	Content string
	Mode    uint32
	Owner   string
	Group   string
	Binary  bool
}

type SvcSpec struct {
	Name      string
	Desc      string
	Port      int
	Proto     string
	Scope     string
	Handler   string
	Conf      string
	Autostart bool
}

// Lock/Unlock guard the world during a tick. They exist as methods so the mutex
// itself can stay unexported and unencodable.
func (w *World) Lock()   { w.mu.Lock() }
func (w *World) Unlock() { w.mu.Unlock() }

// ---- chat bus: kept outside the world so a save never has to encode a channel ----

// GobEncode breaks the Device→World→Device reference cycle that would otherwise
// make the encoder recurse until the stack blows.
func (d *Device) GobEncode() ([]byte, error) {
	shadow := struct {
		ID, Hostname, Profile, Owner string
		OS                           OSInfo
		HW                           Hardware
		FS                           *VFS
		Users                        map[string]*User
		Procs                        []*Proc
		Services                     map[string]*Service
		Ifaces                       []*Iface
		Boot                         time.Time
		PowerOK                      bool
		NetUp                        bool
		MainsDropped                 bool
		UPS                          *UPSInfo
		BootSet                      []string
		Switch                       *SwitchState
		Uplink                       string
		UplinkPort                   int
		PoEPowered                   bool
		PoeUp                        bool
		Battery                      *LaptopBattery
		Printer                      *PrinterState
		BackupIndex                  int
		Rsrc                         *Rsrc
		MeterKWh                     float64
		BillDue                      int64
		Dmesg                        []string
		DHCPL                        map[string]Lease
		Notes                        string
		Purposes                     string
		Installed                    map[string]*VPkg
		InstalledFrom                map[string]string
		NATed                        bool
		Mounts                       []Mount
		Sessions                     map[string]*TermSession
		Active                       []Login
		Fail2Ban                     map[string]int
		Security                     *SecState
	}{
		ID: d.ID, Hostname: d.Hostname, Profile: d.Profile, Owner: d.Owner,
		OS: d.OS, HW: d.HW, FS: d.FS, Users: d.Users, Procs: d.Procs, Services: d.Services,
		Ifaces: d.Ifaces, Boot: d.Boot, PowerOK: d.PowerOK, MeterKWh: d.MeterKWh, BillDue: d.BillDue,
		NetUp: d.NetUp, MainsDropped: d.MainsDropped, UPS: d.UPS, BootSet: d.BootSet,
		Switch: d.Switch, Uplink: d.Uplink, UplinkPort: d.UplinkPort,
		PoEPowered: d.PoEPowered, PoeUp: d.PoeUp, Battery: d.Battery, Printer: d.Printer,
		BackupIndex: d.BackupIndex,
		Dmesg:       d.Dmesg, DHCPL: d.DHCPL, Notes: d.Notes,
		Purposes: d.Purposes, Installed: d.Installed, InstalledFrom: d.InstalledFrom,
		Mounts: d.Mounts, Sessions: d.Sessions,
		Active: d.Active, Fail2Ban: d.Fail2Ban, Security: d.Security,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&shadow); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *Device) GobDecode(b []byte) error {
	shadow := struct {
		ID, Hostname, Profile, Owner string
		OS                           OSInfo
		HW                           Hardware
		FS                           *VFS
		Users                        map[string]*User
		Procs                        []*Proc
		Services                     map[string]*Service
		Ifaces                       []*Iface
		Boot                         time.Time
		PowerOK                      bool
		NetUp                        bool
		MainsDropped                 bool
		UPS                          *UPSInfo
		BootSet                      []string
		Switch                       *SwitchState
		Uplink                       string
		UplinkPort                   int
		PoEPowered                   bool
		PoeUp                        bool
		Battery                      *LaptopBattery
		Printer                      *PrinterState
		BackupIndex                  int
		Rsrc                         *Rsrc
		MeterKWh                     float64
		BillDue                      int64
		Dmesg                        []string
		DHCPL                        map[string]Lease
		Notes                        string
		Purposes                     string
		Installed                    map[string]*VPkg
		InstalledFrom                map[string]string
		Mounts                       []Mount
		Sessions                     map[string]*TermSession
		Active                       []Login
		Fail2Ban                     map[string]int
		Security                     *SecState
	}{}
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&shadow); err != nil {
		return err
	}
	d.ID, d.Hostname, d.Profile, d.Owner = shadow.ID, shadow.Hostname, shadow.Profile, shadow.Owner
	d.OS, d.HW, d.FS, d.Users, d.Procs, d.Services = shadow.OS, shadow.HW, shadow.FS, shadow.Users, shadow.Procs, shadow.Services
	d.Ifaces, d.Boot, d.PowerOK, d.MeterKWh, d.BillDue = shadow.Ifaces, shadow.Boot, shadow.PowerOK, shadow.MeterKWh, shadow.BillDue
	d.Dmesg, d.DHCPL, d.Notes = shadow.Dmesg, shadow.DHCPL, shadow.Notes
	d.Purposes, d.Installed, d.Mounts, d.Sessions = shadow.Purposes, shadow.Installed, shadow.Mounts, shadow.Sessions
	d.InstalledFrom = shadow.InstalledFrom
	d.Active, d.Fail2Ban = shadow.Active, shadow.Fail2Ban
	d.Security = shadow.Security
	if d.Security != nil {
		// maps built lazily would otherwise be nil after a round trip
		if d.Security.AlertSeen == nil {
			d.Security.AlertSeen = map[string]time.Time{}
		}
		if d.Security.AideDB == nil {
			d.Security.AideDB = map[string]string{}
		}
		if d.Security.AideReported == nil {
			d.Security.AideReported = map[string]string{}
		}
	}
	d.NetUp, d.MainsDropped, d.UPS, d.BootSet = shadow.NetUp, shadow.MainsDropped, shadow.UPS, shadow.BootSet
	d.Switch, d.Uplink, d.UplinkPort = shadow.Switch, shadow.Uplink, shadow.UplinkPort
	d.PoEPowered, d.PoeUp = shadow.PoEPowered, shadow.PoeUp
	d.Battery, d.Printer, d.BackupIndex = shadow.Battery, shadow.Printer, shadow.BackupIndex
	d.Rsrc = shadow.Rsrc
	return nil
}

func (w *World) NewPID() int { w.nextPID++; return w.nextPID }

func (w *World) AddEvent(dev, level, src, format string, a ...any) {
	msg := format
	if len(a) > 0 {
		msg = fmt.Sprintf(format, a...)
	}
	w.Events = append(w.Events, Event{At: w.Sim, Dev: dev, Level: level, Source: src, Message: msg})
	if len(w.Events) > 500 {
		w.Events = w.Events[len(w.Events)-500:]
	}
}

func (w *World) Now() time.Time { return w.Sim }

func (d *Device) Logf(level, src string, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	line := fmt.Sprintf("%s %s %s[%s]: %s\n", d.W.Sim.Format("Jan 2 15:04:05"), d.Hostname, src, level, msg)
	// §17: a full filesystem really loses log lines. rsyslog cannot write into
	// /var, drops the message, and says how many it lost once there is room
	// again (see diskTick) — which is exactly what the real daemon does.
	if d.DiskFull() {
		d.Resources().LogDropped++
	} else {
		d.FS.Append("/var/log/syslog", []byte(line))
	}
	if src == "kernel" {
		d.Dmesg = append(d.Dmesg, line)
		if len(d.Dmesg) > 200 {
			d.Dmesg = d.Dmesg[len(d.Dmesg)-200:]
		}
	}
	// §33 central logs: a machine configured to forward sends this line to its
	// collector over the same packet path as everything else, so a collector
	// that is unreachable really loses the line.
	if d.W != nil {
		d.ForwardLog(level, src, msg)
	}
}

func (d *Device) FindUser(name string) *User { return d.Users[name] }

func (d *Device) Svc(name string) *Service {
	if d.Services == nil {
		return nil
	}
	return d.Services[name]
}

func (d *Device) AddProc(p *Proc) {
	p.PID = d.W.NewPID()
	d.Procs = append(d.Procs, p)
}

func (d *Device) Uptime() time.Duration { return d.W.Sim.Sub(d.Boot) }
