package snippet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormClampsAndOrders(t *testing.T) {
	if r := Norm(5, 2, 10); r != (Range{3, 6}) {
		t.Fatalf("Norm(5,2) = %+v", r)
	}
	if r := Norm(-3, 99, 4); r != (Range{1, 4}) {
		t.Fatalf("Norm clamp = %+v", r)
	}
}

func TestRef(t *testing.T) {
	if got := Ref("a.go", Range{7, 7}); got != "a.go:7" {
		t.Fatal(got)
	}
	if got := Ref("a.go", Range{7, 9}); got != "a.go:7-9" {
		t.Fatal(got)
	}
}

func TestLoadAndPromptCarryOnlyTheRange(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "x.go" || f.Lang != "go" || len(f.Lines) != 5 {
		t.Fatalf("loaded %+v", f)
	}
	p := Prompt("rename B", f, Range{4, 4})
	if !strings.HasPrefix(p, "rename B\n\n`x.go:4`\n```go\nfunc B() {}\n```") {
		t.Fatalf("prompt:\n%s", p)
	}
	if strings.Contains(p, "func A") || strings.Contains(p, "func C") {
		t.Fatal("prompt leaked lines outside the range")
	}
}

func TestLoadRefusesBinaryAndDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), []byte{1, 0, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "b.bin"); err == nil {
		t.Fatal("binary accepted")
	}
	if _, err := Load(dir, "."); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestFenceOutgrowsBackticksInCode(t *testing.T) {
	f := &File{Path: "r.md", Lang: "markdown", Lines: []string{"```go", "x", "```"}}
	if b := Block(f, Range{1, 3}); !strings.Contains(b, "````markdown\n") {
		t.Fatalf("fence not lengthened:\n%s", b)
	}
}

func TestResolveOutsideCwdStaysAbsolute(t *testing.T) {
	_, rel := Resolve("/a/b", "/c/d.go")
	if rel != "/c/d.go" {
		t.Fatal(rel)
	}
	_, rel = Resolve("/a/b", "sub/e.go")
	if rel != "sub/e.go" {
		t.Fatal(rel)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		path string
		r    Range
		ok   bool
	}{
		{"a.go", "a.go", Range{}, false},
		{"a.go:12", "a.go", Range{12, 12}, true},
		{"a.go:20-12", "a.go", Range{12, 20}, true},
		{"C:/x.go", "C:/x.go", Range{}, false},
		{"a.go:x", "a.go:x", Range{}, false},
	}
	for _, c := range cases {
		p, r, ok := ParseTarget(c.in)
		if p != c.path || r != c.r || ok != c.ok {
			t.Errorf("ParseTarget(%q) = %q %+v %v", c.in, p, r, ok)
		}
	}
}
