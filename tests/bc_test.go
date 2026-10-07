package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Game bytecode (§8) — programs the world runs without running the host:
// integer/string regs, memory, calls, files as the invoking user, sockets
// through Dial, fuel instead of hanging. Each test walks a happy path, a
// boundary and (where it applies) a recovery.

// bcProg writes assembly with real newlines (printf interprets the format's
// escapes, %s would not) and returns nothing: read the world after.
func bcProg(t *testing.T, w *core.World, dev *core.Device, user, path, body string) {
	t.Helper()
	flat := strings.ReplaceAll(body, "\n", `\n`)
	out := run(t, w, dev, user, "printf '"+flat+"' > "+path)
	if strings.Contains(out, "error") {
		t.Fatalf("setup write failed: %s", out)
	}
}

func TestBytecodeArithmeticAndFlow(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// sum 1..100 with a loop, a call and a comparison
	bcProg(t, w, pc, "alex", "/tmp/sum.basm",
		"LOAD R0, 0\nLOAD R1, 1\nLOAD R2, 100\nLOAD R3, 1\nLOOP:\nADD R0, R0, R1\nADD R1, R1, R3\nCMP R7, R1, R2\nJLE LOOP\nCALL SHOW\nHALT\nSHOW:\nPRINT R0\nRET\n")
	if out := run(t, w, pc, "alex", "brun /tmp/sum.basm"); strings.TrimSpace(out) != "5050" {
		t.Fatalf("sum 1..100 should print 5050, got:\n%s", out)
	}
	// assembly errors carry line numbers, not silence
	bcProg(t, w, pc, "alex", "/tmp/bad.basm", "LOAD R0\nBOGUS R1, 2\n")
	if out := run(t, w, pc, "alex", "brun /tmp/bad.basm"); !strings.Contains(out, "asm:") {
		t.Fatalf("bad ops must fail with a line number, got:\n%s", out)
	}
	// division by zero is a runtime fault with an exit code, not a hang
	bcProg(t, w, pc, "alex", "/tmp/div0.basm", "LOAD R0, 1\nLOAD R1, 0\nDIV R2, R0, R1\n")
	if out := run(t, w, pc, "alex", "brun /tmp/div0.basm"); !strings.Contains(out, "division by zero") {
		t.Fatalf("div0 must fault honestly, got:\n%s", out)
	}
	// and an infinite loop dies on fuel, fast
	bcProg(t, w, pc, "alex", "/tmp/loop.basm", "AGAIN: JMP AGAIN\n")
	if out := run(t, w, pc, "alex", "brun /tmp/loop.basm --fuel 100"); !strings.Contains(out, "out of fuel") {
		t.Fatalf("an infinite loop must die on fuel, got:\n%s", out)
	}
}

func TestBytecodeFilesRunAsTheUser(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	bcProg(t, w, pc, "alex", "/tmp/io.basm",
		"SLOAD S0, \"written by bytecode\"\nWRITE_FILE S0, \"/tmp/bc-out.txt\"\nREAD_FILE S1, \"/tmp/bc-out.txt\"\nPRINT S1\n")
	if out := run(t, w, pc, "alex", "brun /tmp/io.basm"); !strings.Contains(out, "written by bytecode") {
		t.Fatalf("file roundtrip failed:\n%s", out)
	}
	if data, ok := pc.FS.Read("/tmp/bc-out.txt"); !ok || string(data) != "written by bytecode" {
		t.Fatalf("the write must land for real: %q", string(data))
	}
	// ...through the same permission checks as the shell
	bcProg(t, w, pc, "guest", "/tmp/steal.basm", "READ_FILE S0, \"/etc/shadow\"\n")
	if out := run(t, w, pc, "guest", "brun /tmp/steal.basm"); !strings.Contains(strings.ToLower(out), "permission denied") {
		t.Fatalf("shadow reads must be refused, got:\n%s", out)
	}
}

func TestBytecodeSocketsDialAndReadBanners(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]

	// banner grab against the real board: connect, read, print
	bcProg(t, w, pc, "alex", "/tmp/banner.basm",
		"OPEN_SOCKET \"bbs.neohome.example\", 2323\nREAD_SOCKET 0, S0\nPRINT S0\nCLOSE_SOCKET 0\nHALT\n")
	if out := run(t, w, pc, "alex", "brun /tmp/banner.basm"); !strings.Contains(out, "NeoBBS") {
		t.Fatalf("banner grab failed:\n%s", out)
	}
	// a closed port fails with the network's own reason
	bcProg(t, w, pc, "alex", "/tmp/closed.basm", "OPEN_SOCKET \"bbs.neohome.example\", 9999\n")
	if out := run(t, w, pc, "alex", "brun /tmp/closed.basm"); !strings.Contains(out, "refused") {
		t.Fatalf("closed ports must fail honestly, got:\n%s", out)
	}
	// ...and the attempt is evidence where it landed (flows, like any dial)
	found := false
	for _, f := range w.Devices["bbs"].Sec().Flows {
		if f.Port == 2323 {
			found = true
		}
	}
	if !found {
		t.Fatal("the socket must leave a flow on the target")
	}
}

func TestBytecodeSpawnRunsInBackground(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	bcProg(t, w, pc, "alex", "/tmp/bg.basm", "SLOAD S0, \"bg hello\"\nPRINT S0\nHALT\n")
	bcProg(t, w, pc, "alex", "/tmp/sp.basm", "SPAWN \"/tmp/bg.basm\"\nPRINT R0\nHALT\n")
	out := run(t, w, pc, "alex", "brun /tmp/sp.basm")
	pid := 0
	for _, p := range pc.Procs {
		if p.Name == "brun" {
			pid = p.PID
		}
	}
	if pid == 0 {
		t.Fatalf("the spawned program must be in the process table:\n%s", out)
	}
	for i := 0; i < 3; i++ {
		w.Tick()
	}
	stillThere := false
	for _, p := range pc.Procs {
		if p.PID == pid {
			stillThere = true
		}
	}
	if stillThere {
		t.Fatal("a halted background program must be reaped")
	}
	if len(w.BCStates) != 0 {
		t.Fatal("finished states must be collected")
	}
	// background output lands in nohup.out, like a real detached job
	data, ok := pc.FS.Read("/home/alex/nohup.out")
	if !ok || !strings.Contains(string(data), "bg hello") {
		t.Fatalf("nohup.out must carry the background output:\n%s", string(data))
	}
	// killing works: a looping child never finishes on its own
	bcProg(t, w, pc, "alex", "/tmp/forever.basm", "AGAIN: JMP AGAIN\n")
	bcProg(t, w, pc, "alex", "/tmp/sp2.basm", "SPAWN \"/tmp/forever.basm\"\nHALT\n")
	run(t, w, pc, "alex", "brun /tmp/sp2.basm")
	killed := 0
	for _, p := range pc.Procs {
		if p.Name == "brun" {
			killed = p.PID
		}
	}
	if killed == 0 {
		t.Fatal("setup: looping child missing")
	}
	run(t, w, pc, "alex", "kill "+itoaOf(killed))
	w.Tick()
	for _, p := range pc.Procs {
		if p.PID == killed {
			t.Fatal("kill must end the background program")
		}
	}
	if len(w.BCStates) != 0 {
		t.Fatal("killed states must be reaped")
	}
}

func itoaOf(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestBytecodeSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	bcProg(t, w, pc, "alex", "/tmp/bg.basm", "SLOAD S0, \"saved hello\"\nPRINT S0\nHALT\n")
	bcProg(t, w, pc, "alex", "/tmp/sp.basm", "SPAWN \"/tmp/bg.basm\"\nHALT\n")
	run(t, w, pc, "alex", "brun /tmp/sp.basm")
	if len(w.BCStates) != 1 {
		t.Fatal("setup: one background state expected")
	}
	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back.BCStates) != 1 {
		t.Fatal("background states must survive the save")
	}
	back.Tick()
	back.Tick()
	if len(back.BCStates) != 0 {
		t.Fatal("the restored program must run to completion")
	}
	if data, ok := back.Devices["pc-alex"].FS.Read("/home/alex/nohup.out"); !ok || !strings.Contains(string(data), "saved hello") {
		t.Fatalf("the restored program must deliver its output:\n%s", string(data))
	}
}
