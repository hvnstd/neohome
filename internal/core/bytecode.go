package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Game Bytecode (§8) — programs the world runs without running the host
//
// The third execution class: player and community programs compile to a tiny
// instruction set and run against a capability API (this device's files as
// this user, Dial-coupled sockets, spawned processes), never host syscalls,
// host files, host network or host machine code. Malicious code at most eats
// game CPU: every invocation is fuel-metered, background slices are bounded
// per tick, and infinite loops die on fuel instead of hanging the world.
//
// Model limits, stated (like maxFileMB and resticBlobLimit): integer regs
// R0-R7, string regs S0-S3, 256 int memory cells, 4 socket handles, 1 MiB
// file reads, no floats, no GRANT-like privilege changes from inside.
// ---------------------------------------------------------------------------

// BC fuel: how many instructions one go may execute. Inline runs get a big
// slice, background states a small one per tick; exceeding it kills the
// program with exit 125 instead of hanging the tick loop.
const (
	bcFuelInline = 20000
	bcFuelTick   = 200
	bcFuelMax    = 50000
	bcCallDepth  = 64
	bcMemCells   = 256
	bcReadLimit  = 1048576
	bcSockCap    = 4096
)

// BCInstr is one assembled instruction: positional int args, one string
// immediate, and a resolved jump target (-1 when unused). Plain data, so
// saved background states round-trip through gob.
type BCInstr struct {
	Op string
	A  int
	B  int
	C  int
	S  string
	J  int
}

// BCState is one background program: registers, memory, sockets and a
// program counter, resumed a slice at a time by BCTick.
type BCState struct {
	PID    int
	DevID  string
	User   string
	Path   string
	Code   []BCInstr
	PC     int
	Regs   [8]int64
	Sreg   [4]string
	Mem    [bcMemCells]int64
	Flag   int
	Stack  []int
	Socks  map[int]*BCSock
	Fuel   int
	Output []string
}

// BCSock is an open socket handle: the Dial that opened it (flows and logs
// already exist from the connect) plus a bounded write buffer.
type BCSock struct {
	Host  string
	Port  int
	Wrote int
	// Banner is the service banner, readable once, then EOF.
	Banner string
}

// bcAssemble compiles assembly text to instructions, with line numbers on
// every error. Labels end with a colon, either alone or prefixing an op;
// everything after # is a comment.
func BCAssemble(src string) ([]BCInstr, error) {
	type raw struct {
		op   string
		args []string
		line int
	}
	var raws []raw
	labels := map[string]int{}
	for i, ln := range strings.Split(src, "\n") {
		if h := strings.Index(ln, "#"); h >= 0 {
			ln = ln[:h]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		for {
			colon := strings.Index(ln, ":")
			if colon < 0 {
				break
			}
			name := strings.TrimSpace(ln[:colon])
			if name == "" || strings.ContainsAny(name, " \t\"") {
				return nil, fmt.Errorf("asm:%d: bad label", i+1)
			}
			if _, dup := labels[name]; dup {
				return nil, fmt.Errorf("asm:%d: duplicate label %q", i+1, name)
			}
			labels[name] = len(raws)
			ln = strings.TrimSpace(ln[colon+1:])
			if ln == "" {
				break
			}
			// a label alone on flow ops still needs the op parsed below
			if !strings.Contains(ln, " ") && isFlowOp(strings.ToUpper(ln)) {
				raws = append(raws, raw{op: strings.ToUpper(ln), line: i + 1})
				ln = ""
				break
			}
			break
		}
		if ln == "" {
			continue
		}
		fields := bcSplitArgs(ln)
		if len(fields) == 0 {
			continue
		}
		raws = append(raws, raw{op: strings.ToUpper(fields[0]), args: fields[1:], line: i + 1})
	}
	var out []BCInstr
	for _, r := range raws {
		ins, err := bcEncode(r, labels)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, nil
}

func isFlowOp(op string) bool {
	switch op {
	case "JMP", "JZ", "JNZ", "JGT", "JLT", "JGE", "JLE", "CALL":
		return true
	}
	return false
}

// bcSplitArgs tokenizes one line: commas separate, quotes hold.
func bcSplitArgs(ln string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(ln); i++ {
		c := ln[i]
		if c == '"' {
			inStr = !inStr
			cur.WriteByte(c)
			continue
		}
		if (c == ',' || c == ' ' || c == '\t') && !inStr {
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

func bcReg(s string, line int) (int, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) == 2 && s[0] == 'R' && s[1] >= '0' && s[1] <= '7' {
		return int(s[1] - '0'), nil
	}
	return 0, fmt.Errorf("asm:%d: bad register %q (R0-R7)", line, s)
}

func bcSreg(s string, line int) (int, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) == 2 && s[0] == 'S' && s[1] >= '0' && s[1] <= '3' {
		return int(s[1] - '0'), nil
	}
	return 0, fmt.Errorf("asm:%d: bad string register %q (S0-S3)", line, s)
}

func bcInt(s string, line int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("asm:%d: bad integer %q", line, s)
	}
	return n, nil
}

func bcStr(s string, line int) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1], nil
	}
	return "", fmt.Errorf("asm:%d: bad string %q (double-quoted)", line, s)
}

func bcAddr(s string, line int) (int, error) {
	n, err := bcInt(s, line)
	if err != nil {
		return 0, err
	}
	if n < 0 || n >= bcMemCells {
		return 0, fmt.Errorf("asm:%d: address %d outside 0..%d", line, n, bcMemCells-1)
	}
	return n, nil
}

// bcPathRef reads a file/socket host reference: either a "literal" or an
// S-register holding the path. Literals come back as text, registers as
// their index rendered back for the encoder to store.
func bcPathRef(s string, line int) (string, bool, error) {
	s = strings.TrimSpace(s)
	if lit, err := bcStr(s, line); err == nil {
		return lit, false, nil
	}
	if idx, err := bcSreg(s, line); err == nil {
		return fmt.Sprintf("S%d", idx), true, nil
	}
	return "", false, fmt.Errorf("asm:%d: bad path %q (quoted literal or S-register)", line, s)
}

func pathReg(s string) int { return int(s[1] - '0') }

func bcLabel(args []string, labels map[string]int, line int) (int, error) {
	if len(args) < 1 {
		return 0, fmt.Errorf("asm:%d: missing label", line)
	}
	j, ok := labels[strings.TrimSpace(args[0])]
	if !ok {
		return 0, fmt.Errorf("asm:%d: unknown label %q", line, args[0])
	}
	return j, nil
}

// bcEncode turns one parsed line into an instruction, resolving labels.
func bcEncode(r struct {
	op   string
	args []string
	line int
}, labels map[string]int) (BCInstr, error) {
	req := func(n int) error {
		if len(r.args) != n {
			return fmt.Errorf("asm:%d: %s wants %d args", r.line, r.op, n)
		}
		return nil
	}
	switch r.op {
	case "NOP":
		return BCInstr{Op: "NOP", J: -1}, nil
	case "HALT", "EXIT":
		code := 0
		if len(r.args) > 0 {
			var err error
			if code, err = bcInt(r.args[0], r.line); err != nil {
				return BCInstr{}, err
			}
		}
		return BCInstr{Op: "HALT", A: code, J: -1}, nil
	case "LOAD":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		a, err := bcReg(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		b, err := bcInt(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: "LOAD", A: a, B: b, J: -1}, nil
	case "STORE":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		a, err := bcAddr(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		if len(r.args[1]) > 0 && (r.args[1][0] == 'R' || r.args[1][0] == 'r') {
			b, err := bcReg(r.args[1], r.line)
			if err != nil {
				return BCInstr{}, err
			}
			return BCInstr{Op: "STORER", A: a, B: b, J: -1}, nil
		}
		b, err := bcInt(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: "STORE", A: a, B: b, J: -1}, nil
	case "ADD", "SUB", "MUL", "DIV", "MOD":
		if err := req(3); err != nil {
			return BCInstr{}, err
		}
		a, err := bcReg(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		b, err := bcReg(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		c, err := bcReg(r.args[2], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: r.op, A: a, B: b, C: c, J: -1}, nil
	case "CMP":
		if err := req(3); err != nil {
			return BCInstr{}, err
		}
		a, err := bcReg(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		b, err := bcReg(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		c, err := bcReg(r.args[2], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: "CMP", A: a, B: b, C: c, J: -1}, nil
	case "JMP", "JZ", "JNZ", "JGT", "JLT", "JGE", "JLE", "CALL":
		j, err := bcLabel(r.args, labels, r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: r.op, J: j}, nil
	case "RET":
		return BCInstr{Op: "RET", J: -1}, nil
	case "PRINT":
		if err := req(1); err != nil {
			return BCInstr{}, err
		}
		if s, err := bcStr(r.args[0], r.line); err == nil {
			return BCInstr{Op: "PRINTS", S: s, J: -1}, nil
		}
		if a, err := bcReg(r.args[0], r.line); err == nil {
			return BCInstr{Op: "PRINTR", A: a, J: -1}, nil
		}
		// a string register prints what the program read or built: without
		// this, data read from files and sockets could never be shown
		if a, err := bcSreg(r.args[0], r.line); err == nil {
			return BCInstr{Op: "PRINTS", A: a, C: 1, J: -1}, nil
		}
		return BCInstr{}, fmt.Errorf("asm:%d: bad print arg %q (quoted, R-register or S-register)", r.line, r.args[0])
	case "SLOAD":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		a, err := bcSreg(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		if s, err := bcStr(r.args[1], r.line); err == nil {
			return BCInstr{Op: "SLOADS", A: a, S: s, J: -1}, nil
		}
		b, err := bcReg(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: "SLOADR", A: a, B: b, J: -1}, nil
	case "READ_FILE", "WRITE_FILE":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		sreg, err := bcSreg(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		path, isReg, err := bcPathRef(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		b := -1
		s := path
		if isReg {
			b = pathReg(path)
			s = ""
		}
		return BCInstr{Op: r.op, A: sreg, B: b, S: s, J: -1}, nil
	case "OPEN_SOCKET":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		host, isReg, err := bcPathRef(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		port, err := bcInt(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		b := -1
		s := host
		if isReg {
			b = pathReg(host)
			s = ""
		}
		return BCInstr{Op: "OPEN_SOCKET", A: b, B: port, S: s, J: -1}, nil
	case "WRITE_SOCKET", "READ_SOCKET":
		if err := req(2); err != nil {
			return BCInstr{}, err
		}
		a, err := bcInt(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		if a < 0 || a > 3 {
			return BCInstr{}, fmt.Errorf("asm:%d: socket handle 0..3", r.line)
		}
		if r.op == "READ_SOCKET" {
			b, err := bcSreg(r.args[1], r.line)
			if err != nil {
				return BCInstr{}, err
			}
			return BCInstr{Op: r.op, A: a, B: b, J: -1}, nil
		}
		if s, err := bcStr(r.args[1], r.line); err == nil {
			return BCInstr{Op: r.op, A: a, S: s, J: -1}, nil
		}
		b, err := bcSreg(r.args[1], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		return BCInstr{Op: r.op, A: a, B: b, C: 1, J: -1}, nil
	case "CLOSE_SOCKET":
		if err := req(1); err != nil {
			return BCInstr{}, err
		}
		a, err := bcInt(r.args[0], r.line)
		if err != nil {
			return BCInstr{}, err
		}
		if a < 0 || a > 3 {
			return BCInstr{}, fmt.Errorf("asm:%d: socket handle 0..3", r.line)
		}
		return BCInstr{Op: r.op, A: a, J: -1}, nil
	case "SPAWN":
		if len(r.args) < 1 {
			return BCInstr{}, fmt.Errorf("asm:%d: SPAWN wants a program path", r.line)
		}
		p := strings.TrimSpace(r.args[0])
		if lit, err := bcStr(p, r.line); err == nil {
			p = lit
		}
		return BCInstr{Op: "SPAWN", S: p, J: -1}, nil
	}
	return BCInstr{}, fmt.Errorf("asm:%d: unknown op %q", r.line, r.op)
}

// ---- execution ----

// bcResolvePath renders an encoded path reference: a literal, or an
// S-register holding one.
func bcResolvePath(st *BCState, isReg bool, idx int, lit string) string {
	if isReg {
		return st.Sreg[idx]
	}
	return lit
}

// bcStep executes one instruction. (halted, exit, err): halted ends the
// program with exit; err is a runtime fault (also ending, exit 1).
func bcStep(w *World, d *Device, u *User, st *BCState) (bool, int, error) {
	if st.PC < 0 || st.PC >= len(st.Code) {
		return true, 0, nil // fell off the end: clean exit like falling off main
	}
	ins := st.Code[st.PC]
	st.PC++
	jump := func(cond bool, j int) {
		if cond {
			st.PC = j
		}
	}
	switch ins.Op {
	case "NOP":
	case "HALT":
		return true, ins.A, nil
	case "LOAD":
		st.Regs[ins.A] = int64(ins.B)
	case "STORE":
		st.Mem[ins.A] = int64(ins.B)
	case "STORER":
		st.Mem[ins.A] = st.Regs[ins.B]
	case "ADD":
		st.Regs[ins.A] = st.Regs[ins.B] + st.Regs[ins.C]
	case "SUB":
		st.Regs[ins.A] = st.Regs[ins.B] - st.Regs[ins.C]
	case "MUL":
		st.Regs[ins.A] = st.Regs[ins.B] * st.Regs[ins.C]
	case "DIV":
		if st.Regs[ins.C] == 0 {
			return true, 1, fmt.Errorf("runtime: division by zero")
		}
		st.Regs[ins.A] = st.Regs[ins.B] / st.Regs[ins.C]
	case "MOD":
		if st.Regs[ins.C] == 0 {
			return true, 1, fmt.Errorf("runtime: division by zero")
		}
		st.Regs[ins.A] = st.Regs[ins.B] % st.Regs[ins.C]
	case "CMP":
		a, b := st.Regs[ins.B], st.Regs[ins.C]
		st.Regs[ins.A] = 0
		st.Flag = 0
		if a < b {
			st.Flag = -1
		} else if a > b {
			st.Flag = 1
		}
	case "JMP":
		jump(true, ins.J)
	case "JZ":
		jump(st.Flag == 0, ins.J)
	case "JNZ":
		jump(st.Flag != 0, ins.J)
	case "JGT":
		jump(st.Flag > 0, ins.J)
	case "JLT":
		jump(st.Flag < 0, ins.J)
	case "JGE":
		jump(st.Flag >= 0, ins.J)
	case "JLE":
		jump(st.Flag <= 0, ins.J)
	case "CALL":
		if len(st.Stack) >= bcCallDepth {
			return true, 1, fmt.Errorf("runtime: call stack overflow")
		}
		st.Stack = append(st.Stack, st.PC)
		st.PC = ins.J
	case "RET":
		if len(st.Stack) == 0 {
			return true, 1, fmt.Errorf("runtime: ret with empty stack")
		}
		st.PC = st.Stack[len(st.Stack)-1]
		st.Stack = st.Stack[:len(st.Stack)-1]
	case "PRINTS":
		if ins.C == 1 {
			st.Output = append(st.Output, st.Sreg[ins.A])
		} else {
			st.Output = append(st.Output, ins.S)
		}
	case "PRINTR":
		st.Output = append(st.Output, fmt.Sprintf("%d", st.Regs[ins.A]))
	case "SLOADS":
		st.Sreg[ins.A] = ins.S
	case "SLOADR":
		st.Sreg[ins.A] = fmt.Sprintf("%d", st.Regs[ins.B])
	case "READ_FILE", "WRITE_FILE":
		if ins.Op == "READ_FILE" {
			p := bcResolvePath(st, ins.B >= 0, ins.B, ins.S)
			data, exists, allowed := d.FS.ReadPathAs(p, u)
			if !exists {
				return true, 1, fmt.Errorf("runtime: %s: no such file", p)
			}
			if !allowed {
				d.Logf("notice", "audit", "%s: bytecode read denied on %s", u.Name, p)
				return true, 1, fmt.Errorf("runtime: %s: permission denied", p)
			}
			if len(data) > bcReadLimit {
				return true, 1, fmt.Errorf("runtime: %s too large (%d byte cap)", p, bcReadLimit)
			}
			st.Sreg[ins.A] = string(data)
			break
		}
		p := bcResolvePath(st, ins.B >= 0, ins.B, ins.S)
		if err := d.WriteGuest(p, []byte(st.Sreg[ins.A]), u); err != nil {
			return true, 1, fmt.Errorf("runtime: %s: %v", p, err)
		}
	case "OPEN_SOCKET", "WRITE_SOCKET", "READ_SOCKET", "CLOSE_SOCKET":
		if done, code, err := bcSocket(w, d, u, st, ins); done || err != nil {
			return done, code, err
		}
	case "SPAWN":
		pid, err := bcSpawn(w, d, u, ins.S)
		if err != nil {
			return true, 1, err
		}
		st.Regs[0] = int64(pid)
	default:
		return true, 1, fmt.Errorf("runtime: unknown op %q", ins.Op)
	}
	if len(st.Output) > 200 {
		st.Output = st.Output[len(st.Output)-200:]
	}
	return false, 0, nil
}

// bcSocket implements the socket instructions. Sockets are Dial-coupled
// handles: OPEN passes the same DNS/route/power/firewall/ban gates as every
// other connection (and leaves the same flows for the IDS), READ returns the
// service banner once and then EOF, WRITE buffers bounded bytes the target
// logs at receipt. Talking protocols is future work; reaching banners with
// an audit trail is today.
func bcSocket(w *World, d *Device, u *User, st *BCState, ins BCInstr) (bool, int, error) {
	if st.Socks == nil {
		st.Socks = map[int]*BCSock{}
	}
	switch ins.Op {
	case "OPEN_SOCKET":
		host := bcResolvePath(st, ins.A >= 0, ins.A, ins.S)
		if host == "" {
			return true, 1, fmt.Errorf("runtime: empty socket host")
		}
		ip, ok, how := DNSAnswer(d, host)
		if !ok {
			return true, 1, fmt.Errorf("runtime: resolve %s: %s", host, how)
		}
		svc, _, msg := Dial(d, ip, ins.B)
		if svc == nil {
			return true, 1, fmt.Errorf("runtime: connect %s:%d: %s", host, ins.B, msg)
		}
		h := -1
		for i := 0; i < 4; i++ {
			if _, taken := st.Socks[i]; !taken {
				h = i
				break
			}
		}
		if h < 0 {
			return true, 1, fmt.Errorf("runtime: too many open sockets")
		}
		st.Socks[h] = &BCSock{Host: host, Port: ins.B, Banner: svc.Banner}
		st.Regs[0] = int64(h)
		return false, 0, nil
	case "WRITE_SOCKET":
		s, ok := st.Socks[ins.A]
		if !ok {
			return true, 1, fmt.Errorf("runtime: socket %d not open", ins.A)
		}
		var data string
		if ins.C == 1 {
			data = st.Sreg[ins.B]
		} else {
			data = ins.S
		}
		if len(data)+s.Wrote > bcSockCap {
			return true, 1, fmt.Errorf("runtime: socket %d buffer full (%d byte cap)", ins.A, bcSockCap)
		}
		s.Wrote += len(data)
		d.Logf("debug", "net", "bytecode sent %d bytes to %s:%d", len(data), s.Host, s.Port)
		return false, 0, nil
	case "READ_SOCKET":
		s, ok := st.Socks[ins.A]
		if !ok {
			return true, 1, fmt.Errorf("runtime: socket %d not open", ins.A)
		}
		if s.Banner == "" {
			st.Sreg[ins.B] = ""
			return false, 0, nil
		}
		st.Sreg[ins.B] = s.Banner
		s.Banner = ""
		return false, 0, nil
	case "CLOSE_SOCKET":
		if _, ok := st.Socks[ins.A]; !ok {
			return true, 1, fmt.Errorf("runtime: socket %d not open", ins.A)
		}
		delete(st.Socks, ins.A)
		return false, 0, nil
	}
	return true, 1, fmt.Errorf("runtime: unknown op %q", ins.Op)
}

// bcSpawn assembles another program file and backgrounds it: a real Proc in
// the device table (visible in ps, killable, memory-charged) plus a resumable
// state the tick loop slices. The child starts with a fresh machine and a
// bounded lifetime fuel supply.
func bcSpawn(w *World, d *Device, u *User, path string) (int, error) {
	data, exists, allowed := d.FS.ReadPathAs(path, u)
	if !exists {
		return 0, fmt.Errorf("runtime: spawn %s: no such file", path)
	}
	if !allowed {
		return 0, fmt.Errorf("runtime: spawn %s: permission denied", path)
	}
	code, err := BCAssemble(string(data))
	if err != nil {
		return 0, fmt.Errorf("runtime: spawn %s: %v", path, err)
	}
	pid := w.NewPID()
	d.Procs = append(d.Procs, &Proc{Name: "brun", Args: path, User: u.Name, CPU: 5, WantCPU: 10,
		Mem: 8, TTY: "?", State: "S", Start: w.Sim, StartTick: w.TickCount, Kind: "vm"})
	d.Procs[len(d.Procs)-1].PID = pid
	if w.BCStates == nil {
		w.BCStates = map[int]*BCState{}
	}
	w.BCStates[pid] = &BCState{PID: pid, DevID: d.ID, User: u.Name, Path: path,
		Code: code, Fuel: bcFuelMax}
	d.Logf("info", "brun", "spawned %s as pid %d", path, pid)
	return pid, nil
}

// BCRun executes assembled code inline to completion: HALT, a runtime fault,
// or fuel exhaustion (exit 125). Output lines and the exit code come back;
// the world gains nothing but what the instructions wrote.
func BCRun(w *World, d *Device, u *User, code []BCInstr, fuel int) ([]string, int) {
	if fuel <= 0 || fuel > bcFuelMax {
		fuel = bcFuelInline
	}
	st := &BCState{DevID: d.ID, User: u.Name, Code: code, Fuel: fuel}
	for st.Fuel > 0 {
		done, code, err := bcStep(w, d, u, st)
		st.Fuel--
		if err != nil {
			st.Output = append(st.Output, err.Error())
			return st.Output, 1
		}
		if done {
			return st.Output, code
		}
	}
	st.Output = append(st.Output, fmt.Sprintf("out of fuel after %d instructions (exit 125)", fuel))
	return st.Output, 125
}

// BCTick resumes every background program for one bounded slice. Dead
// devices freeze their programs (power returns them); killed PIDs reap
// theirs; finished or exhausted ones are collected with a log line.
func (w *World) BCTick() {
	if len(w.BCStates) == 0 {
		return
	}
	for pid, st := range w.BCStates {
		d := w.Devices[st.DevID]
		if d == nil || !d.Powered() {
			continue
		}
		alive := false
		for _, p := range d.Procs {
			if p.PID == pid {
				alive = true
				break
			}
		}
		if !alive {
			delete(w.BCStates, pid)
			continue
		}
		u := d.FindUser(st.User)
		if u == nil {
			delete(w.BCStates, pid)
			continue
		}
		finished := false
		exit := 0
		for i := 0; i < bcFuelTick && st.Fuel > 0; i++ {
			done, code, err := bcStep(w, d, u, st)
			st.Fuel--
			if err != nil {
				st.Output = append(st.Output, err.Error())
				exit = 1
				finished = true
				break
			}
			if done {
				exit = code
				finished = true
				break
			}
		}
		if st.Fuel <= 0 {
			st.Output = append(st.Output, "out of fuel (background limit)")
			exit = 125
			finished = true
		}
		if finished {
			for i, p := range d.Procs {
				if p.PID == pid {
					d.Procs = append(d.Procs[:i], d.Procs[i+1:]...)
					break
				}
			}
			// background output lands in nohup.out, like a real detached
			// job: without it a finished program's prints would vanish
			if len(st.Output) > 0 {
				if u2 := d.FindUser(st.User); u2 != nil && u2.Home != "" {
					out := strings.Join(st.Output, "\n") + "\n"
					if data, ok := d.FS.Read(u2.Home + "/nohup.out"); ok {
						out = string(data) + out
					}
					_ = d.WriteGuest(u2.Home+"/nohup.out", []byte(out), u2)
				}
			}
			d.Logf("info", "brun", "%s (pid %d) exited %d", st.Path, pid, exit)
			delete(w.BCStates, pid)
		}
	}
}
