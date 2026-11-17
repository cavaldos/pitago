package app

import (
	"os"
	"path/filepath"
	"testing"

	"pitago/src/components/textarea"

	"pitago/src/components/mention"
)

func TestAtToken(t *testing.T) {
	cases := []struct {
		in   string
		col  int
		want string
		ok   bool
	}{
		{"@foo", 4, "@foo", true},
		{"@", 1, "@", true},
		{"hello @src/app", 14, "@src/app", true},
		{"a@b", 3, "", false}, // email-like, no trigger
		{"x = @y", 5, "@", true},
		{`@"my dir/fi`, 11, `@"my dir/fi`, true},
		{"no at here", 10, "", false},
		{"@foo bar", 4, "@foo", true}, // cursor mid-line keeps token
		{`"quoted" @f`, 11, "@f", true},
		{`"unclosed @x`, 12, "", false}, // " wins: non-@ path context
	}
	for _, c := range cases {
		prefix, _, ok := mention.Token([]rune(c.in), c.col)
		if ok != c.ok || prefix != c.want {
			t.Errorf("mention.Token(%q,%d) = (%q,%v), want (%q,%v)",
				c.in, c.col, prefix, ok, c.want, c.ok)
		}
	}
}

func mkAtTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"main.go", "a b.txt", "sub/inner.go", "sub/deep/x.go"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAtListDir(t *testing.T) {
	dir := mkAtTree(t)
	items := mention.ListDir(dir, "", "ma", false)
	if len(items) != 1 || items[0].Value != "@main.go" {
		t.Fatalf("prefix filter: got %+v", items)
	}
	items = mention.ListDir(dir, "", "", false)
	if len(items) != 3 { // main.go, a b.txt, sub/
		t.Fatalf("want 3 items, got %+v", items)
	}
	if !items[0].Dir || items[0].Label != "sub/" { // dirs first
		t.Fatalf("dirs first: got %+v", items)
	}
	var spaced *mention.Item
	for i := range items {
		if items[i].Label == "a b.txt" {
			spaced = &items[i]
		}
	}
	if spaced == nil || spaced.Value != `@"a b.txt"` {
		t.Fatalf("spaced path must be quoted: %+v", items)
	}
	if got := mention.ListDir(filepath.Join(dir, "sub"), "sub/", "", false); len(got) != 2 || got[0].Value != "@sub/deep/" {
		t.Fatalf("scoped listing: got %+v", got)
	}
}

func TestAtFuzzy(t *testing.T) {
	var m Model
	m.cwd = mkAtTree(t)
	items := m.atCandidates("inner", false)
	found := false
	for _, it := range items {
		if it.Value == "@sub/inner.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fuzzy must find nested file: %+v", items)
	}
	if items := m.atCandidates("no-such-file-xyz", false); len(items) != 0 {
		t.Fatalf("no match must close popup: %+v", items)
	}
}

func TestRefreshAtComplete(t *testing.T) {
	var m Model
	m.cwd = mkAtTree(t)
	m.ta = textarea.New()
	m.ta.SetWidth(80)
	m.ta.SetValue("@mai")
	m.refreshAt()
	if !m.atOpen || len(m.atItems) == 0 {
		t.Fatalf("popup must open on @mai: open=%v items=%+v", m.atOpen, m.atItems)
	}
	m.completeAt()
	if got := m.ta.Value(); got != "@main.go " {
		t.Fatalf("complete must replace token: %q", got)
	}
	if m.atOpen {
		t.Fatal("popup must close after file completion (trailing space)")
	}
	// dir completion keeps the popup open for continued completion
	m.ta.SetValue("@su")
	m.refreshAt()
	if !m.atOpen {
		t.Fatal("popup must open on @su")
	}
	m.completeAt()
	if got := m.ta.Value(); got != "@sub/" {
		t.Fatalf("dir completion: %q", got)
	}
	if !m.atOpen {
		t.Fatal("popup must stay open inside a directory")
	}
}
