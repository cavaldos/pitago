// Package integration holds black-box tests: public API only, no access to
// unexported Model fields. They prove the components compose correctly.
package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/components/chat"
	"pitago/src/components/format"
	"pitago/src/components/mention"
	"pitago/src/components/palette"
	"pitago/src/components/pet"
	"pitago/src/components/recent"
	"pitago/src/components/yank"
)

// The components must compose: @mention finds a file, palette matches a
// command, yank extracts it from history, recents persist it.
func TestComponentsCompose(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"main.go", "sub/inner.go"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// mention: tokenize then find the nested file
	prefix, _, ok := mention.Token([]rune("@inn"), 4)
	if !ok || prefix != "@inn" {
		t.Fatalf("token = %q,%v", prefix, ok)
	}
	items := mention.Candidates(dir, "inner", false)
	found := false
	for _, it := range items {
		if it.Value == "@sub/inner.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidates miss nested file: %+v", items)
	}

	// palette: "/" matches everything, query filters
	names := []string{"model", "recent", "yank"}
	if got := palette.Match("", names); len(got) != 3 {
		t.Fatalf("empty query must match all: %v", got)
	}
	if got := palette.Match("yan", names); len(got) != 1 || got[0] != 2 {
		t.Fatalf("query must filter: %v", got)
	}

	// yank: latest assistant answer out of mixed history
	blocks := []chat.Block{
		{Kind: "user", Text: "hi"},
		{Kind: "assistant", Text: "first"},
		{Kind: "assistant", Text: "latest"},
	}
	if got := yank.LastAssistantText(blocks); got != "latest" {
		t.Fatalf("last answer = %q", got)
	}
	opts, _, payload := yank.Entries(blocks)
	if len(opts) != 3 || payload[0] != "latest" {
		t.Fatalf("entries = %v %v", opts, payload)
	}

	// recent: push dedupes + caps, save/load round-trips
	list := recent.Push(nil, "anthropic", "claude-a", "claude-a")
	list = recent.Push(list, "openai", "gpt-b", "gpt-b")
	list = recent.Push(list, "", "claude-a", "claude-a")
	if len(list) != 2 || list[0].ID != "claude-a" {
		t.Fatalf("push = %+v", list)
	}
	path := filepath.Join(dir, "recents.json")
	recent.Save(path, list)
	if got := recent.Load(path); len(got) != 2 || got[0].DispLabel() != "claude-a" {
		t.Fatalf("round-trip = %+v", got)
	}

	// pet: busy states time, idle reads clean
	if !pet.Working.Busy() || pet.Success.Busy() || !pet.Success.Flashing() {
		t.Fatal("pet state predicates wrong")
	}
	if got := pet.Label(pet.Idle, time.Time{}); got != "Ready" {
		t.Fatalf("idle label = %q", got)
	}
	if got := pet.Label(pet.Working, time.Now().Add(-7*time.Second)); got != "Working... 7s" {
		t.Fatalf("timed label = %q", got)
	}
	if p, ok := pet.Get("dragon"); !ok || len(p.Frames) < 2 {
		t.Fatal("sidebar pet art missing or not animated")
	}
	if pet.Resolve("nope").Name != pet.DefaultName {
		t.Fatal("unknown pet name must fall back to the default")
	}

	// format sanity used across sidebar + palette rows
	// (Short reserves one cell for "…", so width 3 fits 2 chars + ellipsis)
	if format.Short("abcdef", 3) != "ab…" || format.FmtNum(1500) != "1.5k" {
		t.Fatal("format helpers wrong")
	}
}

// Smoke: a fresh Model sizes to the terminal and renders a full frame
// through the public API only.
func TestAppSmoke(t *testing.T) {
	m := app.New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(app.Model)
	view := m.View()
	if view == "" {
		t.Fatal("empty view")
	}
	if lines := strings.Split(view, "\n"); len(lines) != 24 {
		t.Fatalf("frame is %d rows, want 24", len(lines))
	}
}
