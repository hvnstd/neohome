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

	BusyDay   []string // trace of what the NPC world did today, for the news feed
	nextPID   int
	TickCount int
	Tasks     []*Task

	assistSkills    int
	assistTracks    []string
	assistantCanFix bool

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
	Firewall     []FWRule
	PortFwd      []FwdRule
	DHCPL        map[string]Lease
	Notes        string
	Purposes     string

	Installed map[string]*VPkg
	Mounts    []Mount
	Sessions  map[string]*TermSession
	Active    []Login
	Fail2Ban  map[string]int // source ip -> failed count
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

type FWRule struct {
	Chain  string // INPUT|FORWARD
	Proto  string
	Port   int
	Src    string
	Action string // ACCEPT|DROP
}

type FwdRule struct {
	Proto  string
	WPort  int
	DstIP  string
	DPort  int
	Enable bool
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
	MAC  string
	Zone string // lo|lan|wan
	GW   string
	Up   bool
	Link string // device id directly connected
	Mode string // static|dhcp
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
	PID   int
	Name  string
	Args  string
	User  string
	CPU   float64
	Mem   int
	TTY   string
	State string
	Start time.Time
	Nice  int
	Svc   string
	Kind  string // builtin|task|shell
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
}

type Plan struct {
	Name    string
	Cores   int
	RAM     int
	Disk    int
	Monthly int64
	Region  string
}

type Repo struct {
	Name     string
	URL      string
	DeviceID string
	Distro   string
	Comps    []string
	Signed   bool
	Status   string
	Pkgs     map[string]*VPkg
	SyncLag  string
}

type VPkg struct {
	Name      string
	Version   string
	Arch      string
	Desc      string
	Size      int
	Depends   []string
	Signed    bool
	Files     map[string]*PkgFile
	Service   *SvcSpec
	PostInst  string
	Malicious bool
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
		MeterKWh                     float64
		BillDue                      int64
		Dmesg                        []string
		Firewall                     []FWRule
		PortFwd                      []FwdRule
		DHCPL                        map[string]Lease
		Notes                        string
		Purposes                     string
		Installed                    map[string]*VPkg
		Mounts                       []Mount
		Sessions                     map[string]*TermSession
		Active                       []Login
		Fail2Ban                     map[string]int
	}{
		ID: d.ID, Hostname: d.Hostname, Profile: d.Profile, Owner: d.Owner,
		OS: d.OS, HW: d.HW, FS: d.FS, Users: d.Users, Procs: d.Procs, Services: d.Services,
		Ifaces: d.Ifaces, Boot: d.Boot, PowerOK: d.PowerOK, MeterKWh: d.MeterKWh, BillDue: d.BillDue,
		NetUp: d.NetUp, MainsDropped: d.MainsDropped, UPS: d.UPS, BootSet: d.BootSet,
		Dmesg: d.Dmesg, Firewall: d.Firewall, PortFwd: d.PortFwd, DHCPL: d.DHCPL, Notes: d.Notes,
		Purposes: d.Purposes, Installed: d.Installed, Mounts: d.Mounts, Sessions: d.Sessions,
		Active: d.Active, Fail2Ban: d.Fail2Ban,
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
		MeterKWh                     float64
		BillDue                      int64
		Dmesg                        []string
		Firewall                     []FWRule
		PortFwd                      []FwdRule
		DHCPL                        map[string]Lease
		Notes                        string
		Purposes                     string
		Installed                    map[string]*VPkg
		Mounts                       []Mount
		Sessions                     map[string]*TermSession
		Active                       []Login
		Fail2Ban                     map[string]int
	}{}
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&shadow); err != nil {
		return err
	}
	d.ID, d.Hostname, d.Profile, d.Owner = shadow.ID, shadow.Hostname, shadow.Profile, shadow.Owner
	d.OS, d.HW, d.FS, d.Users, d.Procs, d.Services = shadow.OS, shadow.HW, shadow.FS, shadow.Users, shadow.Procs, shadow.Services
	d.Ifaces, d.Boot, d.PowerOK, d.MeterKWh, d.BillDue = shadow.Ifaces, shadow.Boot, shadow.PowerOK, shadow.MeterKWh, shadow.BillDue
	d.Dmesg, d.Firewall, d.PortFwd, d.DHCPL, d.Notes = shadow.Dmesg, shadow.Firewall, shadow.PortFwd, shadow.DHCPL, shadow.Notes
	d.Purposes, d.Installed, d.Mounts, d.Sessions = shadow.Purposes, shadow.Installed, shadow.Mounts, shadow.Sessions
	d.Active, d.Fail2Ban = shadow.Active, shadow.Fail2Ban
	d.NetUp, d.MainsDropped, d.UPS, d.BootSet = shadow.NetUp, shadow.MainsDropped, shadow.UPS, shadow.BootSet
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
	d.FS.Append("/var/log/syslog", []byte(line))
	if src == "kernel" {
		d.Dmesg = append(d.Dmesg, line)
		if len(d.Dmesg) > 200 {
			d.Dmesg = d.Dmesg[len(d.Dmesg)-200:]
		}
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
