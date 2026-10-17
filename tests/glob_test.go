package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Pathname expansion (通配符): a word holding an unquoted * ? [ is matched
// against the VFS, quoted/escaped stars stay literal, an unmatched pattern is
// left alone the way bash leaves it, and a bare star skips dotfiles.
func TestGlobExpansion(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	pc.FS.MkdirAll("/home/alex/ft", 0755, "alex", "alex")
	pc.FS.Write("/home/alex/ft/a.txt", "x", 0644, "alex", "alex")
	pc.FS.Write("/home/alex/ft/b.txt", "x", 0644, "alex", "alex")
	pc.FS.Write("/home/alex/ft/c.log", "x", 0644, "alex", "alex")
	pc.FS.Write("/home/alex/ft/.hidden", "x", 0644, "alex", "alex")

	cases := []struct{ line, want string }{
		{"echo ft/*.txt", "ft/a.txt ft/b.txt"},              // relative match, sorted
		{"echo ft/*", "ft/a.txt ft/b.txt ft/c.log"},         // bare star skips .hidden
		{"echo ft/.*", "ft/.hidden"},                        // explicit dot glob matches
		{"echo /home/alex/ft/*.log", "/home/alex/ft/c.log"}, // absolute stays absolute
	}
	for _, c := range cases {
		if out := strings.TrimSpace(run(t, w, pc, "alex", c.line)); out != c.want {
			t.Fatalf("%q should expand to %q, got %q", c.line, c.want, out)
		}
	}

	// a quoted or unmatched glob is literal text
	for _, line := range []string{"echo 'ft/*'", "echo ft/*.xyz"} {
		want := strings.TrimPrefix(line, "echo ")
		want = strings.Trim(want, "'")
		if out := strings.TrimSpace(run(t, w, pc, "alex", line)); out != want {
			t.Fatalf("%q must stay literal as %q, got %q", line, want, out)
		}
	}

	// a glob feeds a real command: cat reads every matched file's bytes
	out := run(t, w, pc, "alex", "cat ft/*.txt")
	if strings.Count(out, "x") != 2 {
		t.Fatalf("cat ft/*.txt should read both matched files, got:\n%s", out)
	}
}

// Globbing happens per command word, so it composes with pipes and redirects
// instead of swallowing them.
func TestGlobComposesWithPipeAndRedirect(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	pc.FS.MkdirAll("/home/alex/logs", 0755, "alex", "alex")
	pc.FS.Write("/home/alex/logs/a.log", "alpha\n", 0644, "alex", "alex")
	pc.FS.Write("/home/alex/logs/b.log", "beta\n", 0644, "alex", "alex")
	pc.FS.Write("/home/alex/logs/c.txt", "gamma\n", 0644, "alex", "alex")

	out := run(t, w, pc, "alex", "cat logs/*.log | grep beta")
	if strings.TrimSpace(out) != "beta" {
		t.Fatalf("a glob should feed a pipeline, got:\n%s", out)
	}
	run(t, w, pc, "alex", "cat logs/*.log > /tmp/combined")
	data, ok := pc.FS.Read("/tmp/combined")
	if !ok || !strings.Contains(string(data), "alpha") || !strings.Contains(string(data), "beta") {
		t.Fatalf("a glob should feed a redirect, got %q", data)
	}
}
