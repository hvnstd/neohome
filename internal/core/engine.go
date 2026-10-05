package core

import (
	"fmt"
	"strings"
	"time"
)

// ---- service lifecycle ----

func (d *Device) StartService(name string) (string, error) {
	svc := d.Svc(name)
	if svc == nil {
		return "", fmt.Errorf("Unit %s.service not found.", name)
	}
	if svc.State == "running" {
		return "already active", nil
	}
	svc.State = "running"
	p := &Proc{Name: svc.Name, User: "root", CPU: 0.3, Mem: 24, TTY: "?", State: "S", Start: d.W.Sim, Svc: svc.Name, Kind: "builtin"}
	d.AddProc(p)
	svc.PID = p.PID
	d.Logf("info", svc.Name, "service %s started (pid %d)", svc.Name, p.PID)
	d.W.AddEvent(d.ID, "info", svc.Name, "service %s started on %s", svc.Name, d.Hostname)
	return "started", nil
}

func (d *Device) StopService(name string) (string, error) {
	svc := d.Svc(name)
	if svc == nil {
		return "", fmt.Errorf("Unit %s.service not found.", name)
	}
	if svc.State != "running" {
		return "already inactive", nil
	}
	svc.State = "stopped"
	var keep []*Proc
	for _, p := range d.Procs {
		if p.Svc != name {
			keep = append(keep, p)
		}
	}
	d.Procs = keep
	d.Logf("warn", svc.Name, "service %s stopped", svc.Name)
	return "stopped", nil
}

func (d *Device) RestartService(name string) (string, error) {
	svc := d.Svc(name)
	if svc == nil {
		return "", fmt.Errorf("Unit %s.service not found.", name)
	}
	if svc.State == "running" {
		if _, err := d.StopService(name); err != nil {
			return "", err
		}
	}
	return d.StartService(name)
}

func (d *Device) ServiceHealth(svc *Service) string {
	if svc.State != "running" {
		return svc.State
	}
	if svc.Name == "dnsmasq" && d.Profile == "router" && !d.dnsmasqUpstreamOK() {
		return "active (degraded: SERVFAIL on all queries)"
	}
	return "active"
}

// dnsmasqUpstreamOK is the exact causal mechanism of the planted fault.
func (d *Device) dnsmasqUpstreamOK() bool {
	data, ok := d.FS.Read("/etc/dnsmasq.conf")
	if !ok {
		return false
	}
	resolv := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "resolv-file=") {
			resolv = strings.TrimPrefix(line, "resolv-file=")
		}
	}
	if resolv != "" {
		x, ok2 := d.FS.Read(resolv)
		if !ok2 {
			return false
		}
		return len(nameservers(string(x))) > 0
	}
	// no resolv-file directive → use /etc/resolv.conf
	x, ok2 := d.FS.Read("/etc/resolv.conf")
	return ok2 && len(nameservers(string(x))) > 0
}

// FaultDNSActive is the verifier the world engine and jobs consult.
func (w *World) FaultDNSActive() bool {
	if !w.CauseFault.Active {
		return false
	}
	d := w.Devices[w.CauseFault.DeviceID]
	if d == nil {
		return false
	}
	svc := d.Svc("dnsmasq")
	broken := svc != nil && svc.State == "running" && !d.dnsmasqUpstreamOK()
	if !broken {
		// fault resolved: mark inactive so jobs can pay out
		w.CauseFault.Active = false
	}
	return broken
}

// ---- installs ----

func (d *Device) InstallPkg(p *VPkg) []string {
	var actions []string
	for pth, f := range p.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 0644
		}
		owner := f.Owner
		if owner == "" {
			owner = "root"
		}
		group := f.Group
		if group == "" {
			group = "root"
		}
		d.FS.Write(pth, f.Content, mode, owner, group)
		if f.Binary {
			actions = append(actions, "installed virtual binary "+pth)
		}
		if strings.HasPrefix(pth, "/etc/passwd.d/") {
			for _, line := range strings.Split(f.Content, "\n") {
				fs := strings.Split(line, ":")
				if len(fs) >= 7 && d.Users[fs[0]] == nil {
					d.Users[fs[0]] = &User{Name: fs[0], UID: atoi(fs[2]), Groups: []string{fs[3]}, Home: fs[5], Shell: fs[6]}
					actions = append(actions, "created system user "+fs[0])
				}
			}
		}
	}
	if p.Service != nil && d.Svc(p.Service.Name) == nil {
		d.Services[p.Service.Name] = &Service{Name: p.Service.Name, Desc: p.Service.Desc, Port: p.Service.Port,
			Proto: p.Service.Proto, Scope: p.Service.Scope, State: "stopped", Handler: p.Service.Handler,
			Conf: p.Service.Conf, Banner: bannerFor(p.Service.Handler)}
		actions = append(actions, "registered unit "+p.Service.Name+".service")
		if p.Service.Autostart {
			if msg, err := d.StartService(p.Service.Name); err == nil {
				actions = append(actions, "autostart: "+msg)
			}
		}
	}
	if p.Malicious {
		d.AddProc(&Proc{Name: "updater", Args: "--daemon", User: "www-data", CPU: 40, Mem: 60, TTY: "?", State: "R", Start: d.W.Sim, Kind: "builtin"})
		actions = append(actions, "postinst registered background updater (suspicious? check 'ps')")
	}
	if p.PostInst != "" {
		actions = append(actions, "postinst: "+p.PostInst)
	}
	d.W.AddEvent(d.ID, "info", "pkg", "installed %s %s on %s", p.Name, p.Version, d.Hostname)
	return actions
}

func bannerFor(handler string) string {
	switch handler {
	case "http-user":
		return "nginx/1.26.2"
	case "ftp-user":
		return "220 (vsFTPd 3.0.5)"
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ---- engine tick ----

func (w *World) Tick() {
	w.TickCount++
	// Power is resolved before anything else: if the lights went out, the rest
	// of the tick must see a world where those machines are genuinely gone.
	w.PowerTick()
	w.Sim = w.Sim.Add(30 * time.Second) // 30 game-seconds per tick; no gameplay gates on it
	for _, id := range w.Order {
		d := w.Devices[id]
		kwh := 0.00005
		for _, p := range d.Procs {
			kwh += p.CPU * 0.000001
		}
		d.MeterKWh += kwh
	}
	w.AssistantWork()
	if w.TickCount%120 == 7 {
		w.NPCPatrol()
	}
	if w.TickCount%30 == 11 {
		w.DecayHeat()
	}
	w.NPCSchedule()
	w.MarketTick()
	w.WANTick()
	w.CronTick()
	w.VMTick()
	w.BBSTick()
	w.IoTTick()
}

func (w *World) AssistantSkillCount() int { return w.assistSkills }

func (w *World) SetAssistSkills(n int) { w.assistSkills = n }

var _ = strings.Contains
