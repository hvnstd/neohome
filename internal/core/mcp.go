package core

import (
	"fmt"
)

// EnsureMCPPlayer provisions the persistent, unprivileged character used by
// external model sessions. It has its own device and account, but joins the
// household LAN so its actions use the same simulated world as other players.
func (w *World) EnsureMCPPlayer() (*Device, *User, error) {
	const playerName = "mcp-agent"
	const deviceID = "mcp-agent-pc"

	if w == nil {
		return nil, nil, fmt.Errorf("world is nil")
	}
	if w.Devices == nil {
		w.Devices = map[string]*Device{}
	}
	if w.IPMap == nil {
		w.IPMap = map[string]string{}
	}
	if w.Players == nil {
		w.Players = map[string]*Player{}
	}
	if w.Bank == nil {
		return nil, nil, fmt.Errorf("world has no bank state for MCP character")
	}
	if w.Bank.Accts == nil {
		w.Bank.Accts = map[string]*Account{}
	}
	if p := w.Players[playerName]; p != nil {
		d := w.Devices[p.PC]
		if d == nil {
			return nil, nil, fmt.Errorf("MCP player %q refers to missing device %q", playerName, p.PC)
		}
		if d.ID != deviceID || d.Owner != playerName {
			return nil, nil, fmt.Errorf("MCP player %q is not bound to its dedicated device", playerName)
		}
		u := d.FindUser(playerName)
		if u == nil || u.UID == 0 {
			return nil, nil, fmt.Errorf("MCP player %q has no matching device account", playerName)
		}
		p.MCPOnly = true
		if w.Bank.Accts[playerName] == nil {
			w.Bank.Accts[playerName] = &Account{Owner: playerName, Name: playerName}
		}
		return d, u, nil
	}
	if w.Devices[deviceID] != nil {
		return nil, nil, fmt.Errorf("MCP device ID %q is already in use", deviceID)
	}
	// the address is allocated, not carried: the LAN plan lives in addr.go
	// and the allocator cannot hand out an address twice
	address, err := w.AllocLANStatic()
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning MCP device: %v", err)
	}

	d := w.addDevice(deviceID, "mcp-agent", "pc", playerName,
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Virtual Agent Workstation", 2, 2400, 4096, 32768, 1000, false, false}, address)
	d.Ifaces[0].GW = LANGateway
	d.Ifaces[0].Mode = "static"
	d.Users[playerName] = &User{
		Name: playerName, UID: 1000, Groups: []string{playerName},
		Home: "/home/" + playerName, Shell: "/bin/bash",
	}
	seedFS(d, "pc")
	d.FS.MkdirAll("/home/"+playerName, 0755, playerName, playerName)
	d.FS.Write("/etc/resolv.conf", "nameserver "+LANGateway+"\n", 0644, "root", "root")
	refreshPasswd(d)

	w.Players[playerName] = &Player{
		Name: playerName, MCPOnly: true, PC: d.ID, HouseKey: "house:" + playerName, Created: w.Sim,
	}
	w.Bank.Accts[playerName] = &Account{Owner: playerName, Name: playerName}
	w.AddEvent(d.ID, "info", "mcp", "external AI character joined the world")
	return d, d.FindUser(playerName), nil
}
