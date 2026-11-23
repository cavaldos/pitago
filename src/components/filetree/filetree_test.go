package filetree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(rows []Row) string {
	var b []string
	for _, r := range rows {
		s := strings.Repeat("  ", r.Depth) + r.N.Name
		if r.N.Dir {
			s += "/"
		}
		if r.N.Status != "" {
			s += " " + r.N.Status
		}
		b = append(b, s)
	}
	return strings.Join(b, "\n")
}

// Outside git: lazy reads, dirs first, .git/node_modules hidden.
func TestLazyTreeOrderingAndReveal(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b.go", "")
	write(t, dir, "A.md", "")
	write(t, dir, "src/app/x.go", "")
	write(t, dir, "node_modules/m/i.js", "")
	tr := Load(dir)
	if tr.Git {
		t.Skip("temp dir is inside a git work tree")
	}
	if got := names(tr.Rows()); got != "src/\nA.md\nb.go" {
		t.Fatalf("rows:\n%s", got)
	}
	n := tr.Reveal("src/app/x.go")
	if n == nil || n.Path != "src/app/x.go" {
		t.Fatalf("reveal = %+v", n)
	}
	rows := tr.Rows()
	if got := names(rows); got != "src/\n  app/\n    x.go\nA.md\nb.go" {
		t.Fatalf("after reveal:\n%s", got)
	}
	if IndexOf(rows, n) != 2 {
		t.Fatal("IndexOf wrong")
	}
	tr.Toggle(tr.Root.Kids[0])
	if got := names(tr.Rows()); got != "src/\nA.md\nb.go" {
		t.Fatalf("after collapse:\n%s", got)
	}
}

// In git: ignored files are hidden and changed files carry marks that roll
// up to their directories.
func TestGitTreeRespectsIgnoreAndMarksStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	write(t, dir, ".gitignore", "bin/\n")
	write(t, dir, "src/a.go", "a")
	write(t, dir, "src/b.go", "b")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	write(t, dir, "src/a.go", "changed")
	write(t, dir, "src/new.go", "new")
	write(t, dir, "bin/out", "x")

	tr := Load(dir)
	if !tr.Git {
		t.Fatal("git mode not detected")
	}
	tr.Expand(tr.Reveal("src"))
	got := names(tr.Rows())
	want := "src/ M\n  a.go M\n  b.go\n  new.go ?\n.gitignore"
	if got != want {
		t.Fatalf("rows:\n%s\nwant:\n%s", got, want)
	}
	// Reload keeps src/ open.
	if got := names(Reload(tr).Rows()); got != want {
		t.Fatalf("reload:\n%s", got)
	}
}

func TestParseStatus(t *testing.T) {
	out := " M src/a.go\nA  b.go\n?? new dir/c.go\nR  old.go -> renamed.go\n D gone.go\nUU conflict.go\n?? ../outside.go\n"
	got := ParseStatus(out)
	want := map[string]string{"src/a.go": "M", "b.go": "A", "new dir/c.go": "?", "renamed.go": "R", "gone.go": "D", "conflict.go": "U"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
