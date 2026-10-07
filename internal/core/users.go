package core

import (
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// User and group management (§4/§46) — accounts as files, not just rows
//
// The account table (`Device.Users`) was always real state, but the only way
// in was seeding and package payloads: no `useradd`, no `groupadd`, and no
// `/etc/group` at all. This file owns the mutators every account verb ends
// up in — each one updates the table AND re-renders the credential files,
// so the login path, sudo and `cat /etc/group` can never disagree.
//
// GIDs follow one rule (`GroupID`, the single owner of that fact): a group
// named after a user carries that user's UID (Debian user-private groups),
// anything else keeps its allocated GID forever in `Device.GroupIDs`.
// `id` prints through the same rule, which is why it agrees with the file.
// ---------------------------------------------------------------------------

// GroupID returns the GID of a group on this device: the namesake user's UID,
// or a stably allocated id for the rest. Allocation order is sorted for
// determinism, allocations skip every number a UID or another GID already
// holds, and they persist in GroupIDs so GIDs never shift when membership
// changes.
func (d *Device) GroupID(name string) int {
	if u, ok := d.Users[name]; ok {
		return u.UID
	}
	if d.GroupIDs != nil {
		if g, ok := d.GroupIDs[name]; ok {
			return g
		}
	}
	taken := map[int]bool{}
	for _, u := range d.Users {
		taken[u.UID] = true
	}
	for _, g := range d.GroupIDs {
		taken[g] = true
	}
	for gid := 1000; ; gid++ {
		if !taken[gid] {
			return gid
		}
	}
}

// groupNames is every group known on this device: anything on an account
// plus every explicitly created (possibly empty) group.
func (d *Device) groupNames() []string {
	seen := map[string]bool{}
	for _, u := range d.Users {
		for _, g := range u.Groups {
			seen[g] = true
		}
	}
	for g := range d.GroupIDs {
		seen[g] = true
	}
	var out []string
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// renderGroup writes /etc/group from the account table: the file real tools
// read, regenerated on every account change like its passwd/shadow siblings.
func renderGroup(d *Device) {
	var b []string
	for _, g := range d.groupNames() {
		var members []string
		for _, u := range d.Users {
			if hasGroup(u, g) && u.Name != g {
				members = append(members, u.Name)
			}
		}
		sort.Strings(members)
		b = append(b, fmt.Sprintf("%s:x:%d:%s\n", g, d.GroupID(g), joinCSV(members)))
	}
	if d.FS == nil {
		return
	}
	d.FS.Write("/etc/group", joinLines(b), 0644, "root", "root")
}

func joinCSV(xs []string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += ","
		}
		s += x
	}
	return s
}

func joinLines(xs []string) string {
	s := ""
	for _, x := range xs {
		s += x
	}
	return s
}

// nextUID is the lowest free UID at or above 1000: system accounts keep
// their low numbers, people stack above them, the way useradd does it.
func (d *Device) nextUID() int {
	used := map[int]bool{}
	for _, u := range d.Users {
		used[u.UID] = true
	}
	for uid := 1000; ; uid++ {
		if !used[uid] {
			return uid
		}
	}
}

// AddUser creates an account: table row, home directory (this world's
// useradd always behaves like -m), locked password, re-rendered files.
// Groups beyond the private one come from -G; the shell defaults to bash.
func (d *Device) AddUser(name string, groups []string, shell string) (*User, error) {
	if !playerNameRe.MatchString(name) {
		return nil, fmt.Errorf("bad account name %q", name)
	}
	if _, taken := d.Users[name]; taken {
		return nil, fmt.Errorf("user %q already exists", name)
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	seen := map[string]bool{name: true}
	var gs []string
	gs = append(gs, name)
	for _, g := range groups {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		gs = append(gs, g)
	}
	u := &User{Name: name, UID: d.nextUID(), Groups: gs,
		Home: "/home/" + name, Shell: shell}
	if d.Users == nil {
		d.Users = map[string]*User{}
	}
	d.Users[name] = u
	d.FS.MkdirAll(u.Home, 0755, name, name)
	refreshPasswd(d)
	d.Logf("info", "useradd", "new user: %s (UID %d)", name, u.UID)
	return u, nil
}

// DelUser removes an account. UID 0 is refused outright; with removeHome the
// home directory and the mail spool go too, otherwise files stay behind with
// their numeric owner — exactly like the real tool.
func (d *Device) DelUser(name string, removeHome bool) error {
	u := d.Users[name]
	if u == nil {
		return fmt.Errorf("user %q does not exist", name)
	}
	if u.UID == 0 {
		return fmt.Errorf("cannot remove %s: it is UID 0", name)
	}
	delete(d.Users, name)
	// groups are never removed with the user: pin the private group so its
	// GID survives in /etc/group, exactly like the real tool leaves it
	if d.GroupIDs == nil {
		d.GroupIDs = map[string]int{}
	}
	if _, ok := d.GroupIDs[name]; !ok {
		d.GroupIDs[name] = u.UID
	}
	if removeHome && u.Home != "" && u.Home != "/" {
		d.FS.Remove(u.Home)
		d.FS.Remove("/var/mail/" + name)
	}
	refreshPasswd(d)
	d.Logf("info", "userdel", "removed user %s", name)
	return nil
}

// AddGroup registers a group with no members yet: it appears in /etc/group
// with a freshly allocated GID and stays there, so `usermod -aG` has
// something to attach to.
func (d *Device) AddGroup(name string) error {
	if !playerNameRe.MatchString(name) {
		return fmt.Errorf("bad group name %q", name)
	}
	for _, g := range d.groupNames() {
		if g == name {
			return fmt.Errorf("group %q already exists", name)
		}
	}
	if d.GroupIDs == nil {
		d.GroupIDs = map[string]int{}
	}
	d.GroupIDs[name] = d.GroupID(name)
	refreshPasswd(d)
	d.Logf("info", "groupadd", "new group: %s (GID %d)", name, d.GroupIDs[name])
	return nil
}

// UsermodGroups changes an account's secondary groups: append adds, replace
// swaps everything past the primary (Groups[0], which is never removed this
// way). Every named group must exist — attaching to a phantom is refused,
// which is what makes `groupadd` load-bearing instead of decorative.
func (d *Device) UsermodGroups(name string, groups []string, appendMode bool) error {
	u := d.Users[name]
	if u == nil {
		return fmt.Errorf("user %q does not exist", name)
	}
	known := map[string]bool{}
	for _, g := range d.groupNames() {
		known[g] = true
	}
	for _, g := range groups {
		if !known[g] {
			return fmt.Errorf("group %q does not exist", g)
		}
	}
	if len(u.Groups) == 0 {
		u.Groups = []string{u.Name}
	}
	if appendMode {
		for _, g := range groups {
			if !hasGroup(u, g) {
				u.Groups = append(u.Groups, g)
			}
		}
	} else {
		keep := []string{u.Groups[0]}
		seen := map[string]bool{u.Groups[0]: true}
		for _, g := range groups {
			if !seen[g] {
				seen[g] = true
				keep = append(keep, g)
			}
		}
		u.Groups = keep
	}
	refreshPasswd(d)
	d.Logf("info", "usermod", "groups of %s now: %s", name, joinCSV(u.Groups))
	return nil
}
