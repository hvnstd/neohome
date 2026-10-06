package core

import (
	"sort"
	"strings"
)

// UCI: the configuration language OpenWrt-family devices actually use.
//
// §14 and §28 both rest on the same rule — exposure must come from
// configuration a player made, not from an event the world scripted. That
// makes the configuration file the load-bearing object: `/etc/config/firewall`
// is read by the packet path, written by `uci`, and readable with `cat`. If
// this file were only decoration, `uci set` would be a pretend command and the
// router's behaviour would be a hidden variable.
//
// The model is deliberately the real one: anonymous or named `config`
// sections, `option` keys, `list` values, and the `@type[index]` references
// players type at the shell.

// UCISection is one `config` block.
type UCISection struct {
	Type string
	Name string // anonymous sections have no name
	// Options keeps insertion order so a file round-trips byte-for-byte
	// (rendering a player's edit must not shuffle their file).
	Options []UCIOption
}

// UCIOption is one `option` or `list` line inside a section.
type UCIOption struct {
	Key  string
	Val  string
	List bool
}

// Get returns the value of a key, or "".
func (s *UCISection) Get(key string) string {
	for _, o := range s.Options {
		if o.Key == key {
			return o.Val
		}
	}
	return ""
}

// Set writes a key, replacing the first occurrence and preserving the order of
// everything else.
func (s *UCISection) Set(key, val string) {
	for i := range s.Options {
		if s.Options[i].Key == key && !s.Options[i].List {
			s.Options[i].Val = val
			return
		}
	}
	s.Options = append(s.Options, UCIOption{Key: key, Val: val})
}

// AddList appends to a list key.
func (s *UCISection) AddList(key, val string) {
	s.Options = append(s.Options, UCIOption{Key: key, Val: val, List: true})
}

// Del removes every option with this key; it reports whether anything went.
func (s *UCISection) Del(key string) bool {
	kept := s.Options[:0]
	removed := false
	for _, o := range s.Options {
		if o.Key == key {
			removed = true
			continue
		}
		kept = append(kept, o)
	}
	s.Options = kept
	return removed
}

// UCIFile is a whole `/etc/config/<name>` document.
type UCIFile struct {
	// Name is the config name (firewall, network, upnpd…), used for paths.
	Name     string
	Sections []*UCISection
}

// UCIPath is where a config lives on every device that speaks UCI.
func UCIPath(name string) string { return "/etc/config/" + name }

// ParseUCI reads the real syntax: `config TYPE ['NAME']`, `option KEY 'VAL'`,
// `list KEY 'VAL'`. Quotes are optional, comments start with `#`, and a
// malformed line is an error the caller can report instead of silently
// dropping — a firewall that ignores a bad line is worse than one that says so.
func ParseUCI(name, body string) (*UCIFile, []string) {
	f := &UCIFile{Name: name}
	var errs []string
	var cur *UCISection
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitUCIFields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "config":
			if len(fields) < 2 {
				errs = append(errs, uciLineErr(i+1, "config without a type"))
				continue
			}
			cur = &UCISection{Type: fields[1]}
			if len(fields) > 2 {
				cur.Name = fields[2]
			}
			f.Sections = append(f.Sections, cur)
		case "option", "list":
			if cur == nil {
				errs = append(errs, uciLineErr(i+1, fields[0]+" outside a config block"))
				continue
			}
			if len(fields) < 3 {
				errs = append(errs, uciLineErr(i+1, fields[0]+" without a value"))
				continue
			}
			cur.Options = append(cur.Options, UCIOption{
				Key: fields[1], Val: fields[2], List: fields[0] == "list",
			})
		default:
			errs = append(errs, uciLineErr(i+1, "unrecognised keyword "+fields[0]))
		}
	}
	return f, errs
}

func uciLineErr(n int, msg string) string {
	return "line " + itoa(n) + ": " + msg
}

// splitUCIFields splits on whitespace, honouring single and double quotes.
func splitUCIFields(line string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				flush()
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case ' ', '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// Render writes the file back out in uci's own style.
func (f *UCIFile) Render() string {
	var b strings.Builder
	for i, s := range f.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("config " + s.Type)
		if s.Name != "" {
			b.WriteString(" '" + s.Name + "'")
		}
		b.WriteString("\n")
		for _, o := range s.Options {
			kind := "option"
			if o.List {
				kind = "list"
			}
			b.WriteString("\t" + kind + " " + o.Key + " '" + o.Val + "'\n")
		}
	}
	return b.String()
}

// Find resolves what a player types: a section name (`nas-ftp`), a type when it
// is unique (`defaults`), or `@type[index]`.
func (f *UCIFile) Find(ref string) *UCISection {
	if ref == "" {
		return nil
	}
	if strings.HasPrefix(ref, "@") {
		body := strings.TrimPrefix(ref, "@")
		idx := 0
		if i := strings.Index(body, "["); i >= 0 {
			idx = atoi(strings.TrimSuffix(body[i+1:], "]"))
			body = body[:i]
		}
		n := 0
		for _, s := range f.Sections {
			if s.Type != body {
				continue
			}
			if n == idx {
				return s
			}
			n++
		}
		return nil
	}
	for _, s := range f.Sections {
		if s.Name != "" && s.Name == ref {
			return s
		}
	}
	var only *UCISection
	for _, s := range f.Sections {
		if s.Type == ref {
			if only != nil {
				return nil // ambiguous: two sections of that type
			}
			only = s
		}
	}
	return only
}

// SectionsOf returns every section of one type, in file order.
func (f *UCIFile) SectionsOf(typ string) []*UCISection {
	var out []*UCISection
	for _, s := range f.Sections {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// Delete removes a section.
func (f *UCIFile) Delete(s *UCISection) bool {
	for i, x := range f.Sections {
		if x == s {
			f.Sections = append(f.Sections[:i], f.Sections[i+1:]...)
			return true
		}
	}
	return false
}

// Add creates a section of a type, with an optional name.
func (f *UCIFile) Add(typ, name string) *UCISection {
	s := &UCISection{Type: typ, Name: name}
	f.Sections = append(f.Sections, s)
	return s
}

// ReadUCIFile loads a config from a device. Nothing is cached: a player who
// edits the file by hand sees the change on the next command, which is the
// whole point of a config-file-backed firewall.
func (d *Device) ReadUCIFile(name string) (*UCIFile, []string, bool) {
	data, ok := d.FS.Read(UCIPath(name))
	if !ok {
		return nil, nil, false
	}
	f, errs := ParseUCI(name, string(data))
	return f, errs, true
}

// WriteUCIFile stores a config back to the device.
func (d *Device) WriteUCIFile(f *UCIFile) {
	d.FS.Write(UCIPath(f.Name), f.Render(), 0644, "root", "root")
}

// uciOptionTrue reads a uci boolean the way the real tools do.
func uciOptionTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes", "enabled":
		return true
	}
	return false
}

// ---- pending changes: what `uci set` does before `uci commit` ----

// uciPending is one device's uncommitted edits: the edited copy of each config
// plus the commands that produced them, which is what `uci changes` prints.
// Both are kept per config, so committing one config clears exactly the lines
// that belong to it.
type uciPending struct {
	Files map[string]*UCIFile
	Log   map[string][]string
}

func (w *World) pendingFor(d *Device, create bool) *uciPending {
	if w.uciStage == nil {
		if !create {
			return nil
		}
		w.uciStage = map[string]*uciPending{}
	}
	p := w.uciStage[d.ID]
	if p == nil && create {
		p = &uciPending{Files: map[string]*UCIFile{}, Log: map[string][]string{}}
		w.uciStage[d.ID] = p
	}
	return p
}

// StageUCIChange records an edit without applying it. The device keeps
// behaving according to its committed configuration until `uci commit`.
func (w *World) StageUCIChange(d *Device, config string, f *UCIFile, what string) {
	p := w.pendingFor(d, true)
	p.Files[config] = f
	if p.Log == nil {
		p.Log = map[string][]string{}
	}
	p.Log[config] = append(p.Log[config], "uci: "+what)
}

// UCIStaged returns the edited copy of a config, if one is pending: a second
// `uci set` must start from the first one's result, not from the file.
func (w *World) UCIStaged(d *Device, config string) (*UCIFile, bool) {
	p := w.pendingFor(d, false)
	if p == nil {
		return nil, false
	}
	f, ok := p.Files[config]
	return f, ok
}

// UCIChanges lists the pending commands, in config order.
func (w *World) UCIChanges(d *Device) []string {
	p := w.pendingFor(d, false)
	if p == nil {
		return nil
	}
	names := make([]string, 0, len(p.Log))
	for n := range p.Log {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		out = append(out, p.Log[n]...)
	}
	return out
}

// CommitUCIChange applies one config's pending edits to its file. This is the
// moment the packet path changes behaviour: nothing before it did.
func (w *World) CommitUCIChange(d *Device, ref string) bool {
	p := w.pendingFor(d, false)
	if p == nil {
		return false
	}
	name := ref
	if i := strings.Index(ref, "."); i > 0 {
		name = ref[:i]
	}
	f, ok := p.Files[name]
	if !ok {
		return false
	}
	d.WriteUCIFile(f)
	delete(p.Files, name)
	delete(p.Log, name)
	d.Logf("info", "firewall", "configuration '%s' applied (%d section(s))", name, len(f.Sections))
	return true
}

// CommitUCIChanges applies everything pending and reports how many configs
// were written.
func (w *World) CommitUCIChanges(d *Device) int {
	p := w.pendingFor(d, false)
	if p == nil {
		return 0
	}
	names := make([]string, 0, len(p.Files))
	for n := range p.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	n := 0
	for _, name := range names {
		if w.CommitUCIChange(d, name) {
			n++
		}
	}
	if len(p.Files) == 0 {
		p.Log = map[string][]string{}
	}
	return n
}

// RevertUCIChanges discards pending edits. With no arguments it discards all
// of them; with a config name, just that one.
func (w *World) RevertUCIChanges(d *Device, args []string) int {
	p := w.pendingFor(d, false)
	if p == nil {
		return 0
	}
	if len(args) == 0 {
		n := len(p.Files)
		p.Files = map[string]*UCIFile{}
		p.Log = map[string][]string{}
		return n
	}
	name := args[0]
	if i := strings.Index(name, "."); i > 0 {
		name = name[:i]
	}
	if _, ok := p.Files[name]; !ok {
		return 0
	}
	delete(p.Files, name)
	delete(p.Log, name)
	return 1
}

// UCIKnownConfigs are the configs this world models, for `uci show`.
var UCIKnownConfigs = []string{"firewall", "upnpd", "network", "dhcp", "system", "wireless"}
