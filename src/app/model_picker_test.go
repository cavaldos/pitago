package app

import (
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Two-pane model picker: left = providers, right = scoped models.
func TestModelPickerProvFilter(t *testing.T) {
	provs := buildProvs([]string{"ollama", "opencode-zen", "ollama", ""})
	if len(provs) == 0 || provs[0] != "All" {
		t.Fatalf("buildProvs must start with All: %v", provs)
	}
	for _, want := range []string{"ollama", "opencode-zen", "other"} {
		found := false
		for _, p := range provs[1:] {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("buildProvs missing %q: %v", want, provs)
		}
	}
	if !sort.StringsAreSorted(provs[1:]) {
		t.Fatalf("providers not sorted: %v", provs)
	}

	d := &Dialog{
		Kind:      "model",
		Options:   []string{"m1", "m2", "m3"},
		Descs:     []string{"a · ollama", "b · opencode-zen", "c · ollama"},
		Providers: []string{"ollama", "opencode-zen", "ollama"},
		Provs:     provs,
	}
	// All: everything visible
	d.ProvCursor = 0
	d.Reindex()
	if len(d.FIdx) != 3 {
		t.Fatalf("All FIdx = %v", d.FIdx)
	}
	// ollama scope (find its index: catalog shares the list now)
	ollama := 0
	for i, p := range provs {
		if p == "ollama" {
			ollama = i
			break
		}
	}
	d.ProvCursor = ollama
	d.Reindex()
	if len(d.FIdx) != 2 || d.FIdx[0] != 0 || d.FIdx[1] != 2 {
		t.Fatalf("ollama FIdx = %v", d.FIdx)
	}
	// text filter on the providers pane searches globally (ignores scope)
	d.ProvFocus = true
	d.Filter = "m2"
	d.Reindex()
	if len(d.FIdx) != 1 || d.FIdx[0] != 1 {
		t.Fatalf("global filtered FIdx = %v", d.FIdx)
	}
	// provider id itself is matchable globally
	d.Filter = "opencode-zen"
	d.Reindex()
	if len(d.FIdx) != 1 || d.FIdx[0] != 1 {
		t.Fatalf("provider filtered FIdx = %v", d.FIdx)
	}
	// same filter on the models pane stays scoped to the provider
	d.ProvFocus = false
	d.Filter = "m2"
	d.Reindex()
	if len(d.FIdx) != 0 {
		t.Fatalf("scoped filtered FIdx = %v, want []", d.FIdx)
	}
	d.Filter = "m3"
	d.Reindex()
	if len(d.FIdx) != 1 || d.FIdx[0] != 2 {
		t.Fatalf("filtered FIdx = %v", d.FIdx)
	}
	if got := d.selProv(); got != "ollama" {
		t.Fatalf("selProv = %q", got)
	}
	d.ProvCursor = 0
	if got := d.selProv(); got != "" {
		t.Fatalf("All selProv = %q", got)
	}
}

// Connected providers float above the rest (after All), alpha in-group.
func TestSortProvsConn(t *testing.T) {
	provs := []string{"All", "b", "a", "c"}
	sortProvsConn(provs, map[string]bool{"a": true, "c": true})
	want := []string{"All", "a", "c", "b"}
	for i := range want {
		if provs[i] != want[i] {
			t.Fatalf("sorted = %v, want %v", provs, want)
		}
	}
	sortProvsConn([]string{"All"}, nil) // must not panic
}

// Green dot: models listed, saved key, or preset env all count as connected.
func TestProvConnSources(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir()) // isolate from ~/.pi/agent/auth.json
	t.Setenv("GROQ_API_KEY", "gsk-test")
	conn := provConn(t.TempDir(), []string{"opencode"})
	if !conn["opencode"] {
		t.Error("provider with models must be connected")
	}
	if !conn["groq"] {
		t.Error("provider with env key must be connected")
	}
	if conn["anthropic"] {
		t.Error("provider with nothing must not be connected")
	}
	if conn["opencode-zen"] {
		t.Error("unknown provider without models must not be connected")
	}
	conn = provConn(t.TempDir(), []string{"opencode-zen"})
	if !conn["opencode-zen"] {
		t.Error("unknown provider with models must be connected")
	}
}

// Typing on the providers pane searches globally and stays there;
// moving to the models pane scopes the same filter to the provider.
func TestModelPickerTypingSearchesAll(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Dialogs = []*Dialog{{
		Kind: "model", Title: "Select model",
		Options: []string{"m1", "m2", "m3"},
		Descs:   []string{"a · ollama", "b · opencode-zen", "c · ollama"},
		Providers: []string{"ollama", "opencode-zen", "ollama"},
		Provs:     []string{"All", "ollama", "opencode-zen"},
		ProvCursor: 1, ProvFocus: true,
	}}
	d := m.Dialogs[0]
	d.Reindex()
	if len(d.FIdx) != 2 { // empty filter previews the cursor scope
		t.Fatalf("scoped FIdx = %v", d.FIdx)
	}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = um.(Model)
	d = m.Dialogs[0]
	if !d.ProvFocus {
		t.Fatal("typing on the providers pane must stay there")
	}
	if d.Filter != "m" {
		t.Fatalf("Filter = %q", d.Filter)
	}
	if len(d.FIdx) != 3 { // providers pane: global across providers
		t.Fatalf("global FIdx = %v", d.FIdx)
	}
	// → models pane: the same filter scopes to ollama
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRight})
	m = um.(Model)
	d = m.Dialogs[0]
	if d.ProvFocus {
		t.Fatal("→ must move to the models pane")
	}
	if len(d.FIdx) != 2 || d.FIdx[0] != 0 || d.FIdx[1] != 2 {
		t.Fatalf("scoped FIdx = %v", d.FIdx)
	}
	// ← back: global again
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyLeft})
	m = um.(Model)
	d = m.Dialogs[0]
	if !d.ProvFocus {
		t.Fatal("← must move back to the providers pane")
	}
	if len(d.FIdx) != 3 {
		t.Fatalf("global FIdx = %v", d.FIdx)
	}
}

// Starred models sort first (stable) and Ctrl+F toggles the star,
// with the cursor following the toggled model.
func TestModelPickerFavorites(t *testing.T) {
	mk := func() Model {
		m := New(nil, t.TempDir()) // favPath "" → no file writes
		m.Dialogs = []*Dialog{{
			Kind: "model", Title: "Select model",
			Options: []string{"m1", "m2", "m3"},
			Descs:   []string{"a · ollama", "b · opencode-zen", "c · ollama"},
			Providers: []string{"ollama", "opencode-zen", "ollama"},
			Provs:     []string{"All", "ollama", "opencode-zen"},
			ProvFocus: false,
		}}
		return m
	}

	// fav-first: m3 starred lands on top, the rest keep pi's order
	m := mk()
	d := m.Dialogs[0]
	d.FavSet = map[string]bool{"ollama\x00m3": true}
	d.Reindex()
	if len(d.FIdx) != 3 || d.FIdx[0] != 2 || d.FIdx[1] != 0 || d.FIdx[2] != 1 {
		t.Fatalf("fav-first FIdx = %v", d.FIdx)
	}

	// Ctrl+F stars the highlighted model and moves it to the top
	m = mk()
	d = m.Dialogs[0]
	d.Reindex()
	d.Cursor = 1 // m2
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = um.(Model)
	d = m.Dialogs[0]
	if !d.isFavIdx(1) {
		t.Fatal("Ctrl+F must star m2")
	}
	if len(d.FIdx) == 0 || d.FIdx[0] != 1 {
		t.Fatalf("starred FIdx = %v", d.FIdx)
	}
	if d.Cursor != 0 {
		t.Fatalf("cursor must follow the star to 0, got %d", d.Cursor)
	}
	// Ctrl+F again unstars
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = um.(Model)
	d = m.Dialogs[0]
	if d.isFavIdx(1) {
		t.Fatal("second Ctrl+F must unstar m2")
	}
	if len(d.FIdx) != 3 || d.FIdx[0] != 0 {
		t.Fatalf("unstarred FIdx = %v", d.FIdx)
	}
}

// Ctrl+L in the model picker closes it and opens provider login.
func TestModelPickerCtrlLLogin(t *testing.T) {
	m := New(nil, t.TempDir())
	m.UseBuiltins([]Builtin{{Name: "login", Run: func(mm *Model, arg string) tea.Cmd {
		mm.Dialogs = append(mm.Dialogs, &Dialog{Kind: "login", Title: "login"})
		return nil
	}}}, nil)
	m.Dialogs = []*Dialog{{
		Kind: "model", Title: "Select model",
		Options: []string{"m1"}, Providers: []string{"opencode"},
		Provs: []string{"All", "opencode"}, ProvFocus: true,
	}}
	m.Dialogs[0].Reindex()
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "login" {
		t.Fatalf("Ctrl+L must swap to login, got %+v", m.Dialogs)
	}
}

// fixedWin must always fill exactly win rows (data + markers), whatever
// the cursor and list size — the dialog box never resizes while scrolling.
func TestFixedWinConstantRows(t *testing.T) {
	const win = 12
	for _, total := range []int{0, 1, 5, 11, 12, 13, 30, 76} {
		for c := -1; c <= total; c++ {
			cur := c
			if cur < 0 {
				cur = 0
			}
			if total > 0 && cur >= total {
				cur = total - 1
			}
			start, end, above, below := fixedWin(cur, total, win)
			if start < 0 || end > total || start > end {
				t.Fatalf("total=%d cur=%d: bad window [%d,%d)", total, cur, start, end)
			}
			rows := end - start
			if above {
				rows++
			}
			if below {
				rows++
			}
			want := total
			if want > win {
				want = win
			}
			if rows != want {
				t.Fatalf("total=%d cur=%d: rows=%d, want %d", total, cur, rows, want)
			}
			if total > 0 && (cur < start || cur >= end) {
				t.Fatalf("total=%d cur=%d: cursor outside [%d,%d)", total, cur, start, end)
			}
		}
	}
}
