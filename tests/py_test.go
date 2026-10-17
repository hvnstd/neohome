package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The ".py frontend" (PROGRESS gap #4): a small Python subset that compiles to
// the SAME game bytecode the VM already runs, as installed software. Each test
// walks a happy path, a boundary (a compile error) and a recovery (the
// command does not exist until the package is installed).

// pyWorld installs python3 on the household PC — the package that ships
// /usr/bin/python3, which is what the shell's command gate reads.
func pyWorld(t *testing.T) (*core.World, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("setup: apt update failed:\n%s", out)
	}
	if out := run(t, w, pc, "root", "apt install python3"); !strings.Contains(out, "Setting up python3") && !strings.Contains(out, "Unpacking") {
		t.Fatalf("setup: install python3 failed:\n%s", out)
	}
	return w, pc
}

// A while loop summing 1..100 must print 5050 — the same program as the
// assembly test, now written in the subset and compiled, not assembled.
func TestPyCompilesAndRunsLoop(t *testing.T) {
	w, pc := pyWorld(t)

	bcProg(t, w, pc, "alex", "/tmp/sum.py",
		"x = 0\ni = 1\nwhile i <= 100:\n    x = x + i\n    i = i + 1\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/sum.py"); strings.TrimSpace(out) != "5050" {
		t.Fatalf("sum 1..100 should print 5050, got:\n%s", out)
	}
	// the bare `python` alias runs the same compiler
	if out := run(t, w, pc, "alex", "python /tmp/sum.py"); strings.TrimSpace(out) != "5050" {
		t.Fatalf("`python` must behave like `python3`, got:\n%s", out)
	}
}

// if/elif/else chooses a branch, and * / bind tighter than + -.
func TestPyIfElseAndPrecedence(t *testing.T) {
	w, pc := pyWorld(t)

	bcProg(t, w, pc, "alex", "/tmp/prec.py", "x = 2 + 3 * 4\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/prec.py"); strings.TrimSpace(out) != "14" {
		t.Fatalf("2 + 3 * 4 should be 14, got:\n%s", out)
	}
	bcProg(t, w, pc, "alex", "/tmp/if.py",
		"x = 2\nif x > 3:\n    print(\"big\")\nelif x == 2:\n    print(\"two\")\nelse:\n    print(\"other\")\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/if.py"); strings.TrimSpace(out) != "two" {
		t.Fatalf("elif branch should print two, got:\n%s", out)
	}
	bcProg(t, w, pc, "alex", "/tmp/neg.py", "x = -3 + 1\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/neg.py"); strings.TrimSpace(out) != "-2" {
		t.Fatalf("unary minus must work, got:\n%s", out)
	}
	// %% reaches the .py file as a single % (printf's own escape)
	bcProg(t, w, pc, "alex", "/tmp/mod.py", "x = 17 %% 5\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/mod.py"); strings.TrimSpace(out) != "2" {
		t.Fatalf("17 %% 5 should be 2, got:\n%s", out)
	}
	// != is a real comparison, not the equality branch
	bcProg(t, w, pc, "alex", "/tmp/neq.py", "x = 0\nif 3 != 4:\n    x = 1\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/neq.py"); strings.TrimSpace(out) != "1" {
		t.Fatalf("3 != 4 must be true, got:\n%s", out)
	}
}

// Compile errors carry py:<line>: and a nonzero status, and unsupported
// constructs are refused by name instead of crashing.
func TestPySyntaxErrorsAreHonest(t *testing.T) {
	w, pc := pyWorld(t)

	bcProg(t, w, pc, "alex", "/tmp/bad.py", "x = 1 +\nprint(x)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/bad.py"); !strings.Contains(out, "py:1:") {
		t.Fatalf("a syntax error must read py:<line>:, got:\n%s", out)
	}
	if rc := remoteStatus(t, w, pc, "alex", "python3 /tmp/bad.py"); rc == 0 {
		t.Fatal("a syntax error must exit nonzero")
	}

	bcProg(t, w, pc, "alex", "/tmp/imp.py", "import os\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/imp.py"); !strings.Contains(out, "import is not supported") {
		t.Fatalf("import must be refused by name, got:\n%s", out)
	}

	bcProg(t, w, pc, "alex", "/tmp/undef.py", "print(zzz)\n")
	if out := run(t, w, pc, "alex", "python3 /tmp/undef.py"); !strings.Contains(out, "undefined name") {
		t.Fatalf("an undefined name must be named, got:\n%s", out)
	}
}

// python3 is installed software: a machine without the package has no command.
func TestPyIsInstalledSoftware(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "alex", "python3 /tmp/x.py"); !strings.Contains(out, "command not found") {
		t.Fatalf("python3 must not exist before install, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "python /tmp/x.py"); !strings.Contains(out, "command not found") {
		t.Fatalf("the python alias must be gated too, got:\n%s", out)
	}
}
