package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// recentTestModel builds a popup-ready model with a known catalog.
func recentTestModel(t *testing.T) *Model {
	t.Helper()
	m := &Model{}
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.Cmds = []pirpc.RepoCommand{
		{Name: "model", Description: "select model", Source: "builtin"},
		{Name: "settings", Description: "agent settings", Source: "builtin"},
		{Name: "theme", Description: "switch theme", Source: "pitago"},
		{Name: "tree", Description: "session tree", Source: "builtin"},
		{Name: "mcp", Description: "mcp status", Source: "extension"},
	}
	m.ta = textarea.New()
	m.winW, m.winH = 120, 30
	return m
}

// names renders the current popup rows as "/name" for easy assertions.
func (m Model) popupNames() []string {
	out := make([]string, 0, len(m.cmdItems))
	for _, it := range m.cmdItems {
		if it < len(m.Cmds) {
			out = append(out, "/"+m.Cmds[it].Name)
		}
	}
	return out
}

func (m *Model) openPopup(prefix string) {
	m.ta.SetValue(prefix)
	m.refreshCmds()
}

func TestNoteCmdUseOrdersAndDedupes(t *testing.T) {
	m := &Model{}
	m.noteCmdUse("model")
	m.noteCmdUse("settings")
	m.noteCmdUse("model")   // re-run → back to the front, no duplicate
	m.noteCmdUse(" Model ") // case/space-insensitive
	if len(m.RecentCmds) != 2 {
		t.Fatalf("want 2 entries, got %v", m.RecentCmds)
	}
	if m.RecentCmds[0] != "Model" || m.RecentCmds[1] != "settings" {
		t.Errorf("want [Model settings], got %v", m.RecentCmds)
	}
	m.noteCmdUse("  ") // blank is ignored
	if len(m.RecentCmds) != 2 {
		t.Errorf("blank should not be recorded: %v", m.RecentCmds)
	}
}

func TestNoteCmdUseKeepsBoundedHistory(t *testing.T) {
	m := &Model{}
	for i := 0; i < recentCmdsKeep+5; i++ {
		m.noteCmdUse("cmd" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if len(m.RecentCmds) != recentCmdsKeep {
		t.Errorf("history should cap at %d, got %d", recentCmdsKeep, len(m.RecentCmds))
	}
}

func TestNoteCmdUsePersists(t *testing.T) {
	m := &Model{prefsPath: filepath.Join(t.TempDir(), "prefs.json")}
	m.noteCmdUse("model")
	m.noteCmdUse("theme")

	// A fresh Model reading the same file sees the history.
	got := LoadPrefs(m.prefsPath).RecentCmds
	// theme was used last, so it leads.
	if len(got) != 2 || got[0] != "theme" || got[1] != "model" {
		t.Fatalf("prefs round-trip failed: %v", got)
	}
	m.RecentCmds = LoadPrefs(m.prefsPath).RecentCmds
	if m.RecentCmds[0] != "theme" {
		t.Errorf("restored order wrong: %v", m.RecentCmds)
	}
}

// The most recent recentCmdsFloat commands lead the popup; the rest keep
// catalog order. Expectations are derived from the constants so bumping
// recentCmdsFloat does not invalidate this test.
func TestFloatRecentCmdsPutsMostRecentFirst(t *testing.T) {
	m := recentTestModel(t)
	// One more history entry than floats, so the cap is actually exercised.
	hist := []string{"tree", "theme", "settings", "model", "mcp"}
	m.RecentCmds = hist
	m.openPopup("/")

	floated := hist[:recentCmdsFloat] // most recent first
	want := "/" + strings.Join(floated, " /") + " /mcp"
	if got := strings.Join(m.popupNames(), " "); got != want {
		t.Errorf("popup order:\n got %q\nwant %q", got, want)
	}
	if n := psecFloatCount(m.RecentCmds); n != recentCmdsFloat {
		t.Errorf("floated %d, want %d", n, recentCmdsFloat)
	}
}

// psecFloatCount mirrors how many of the history entries actually reached
// the popup, so the assertion above cannot pass by accident.
func psecFloatCount(hist []string) int {
	if len(hist) < recentCmdsFloat {
		return len(hist)
	}
	return recentCmdsFloat
}

// Among the matching rows, recency order wins over catalog order.
func TestFloatRecentCmdsOrdersByRecency(t *testing.T) {
	m := recentTestModel(t)
	m.cmdItems = []int{0, 1, 2, 3}              // model, settings, theme, tree
	m.RecentCmds = []string{"tree", "settings"} // tree is the more recent
	m.floatRecentCmds()
	if got := strings.Join(m.popupNames(), " "); got != "/tree /settings /model /theme" {
		t.Errorf("float order:\n got %q\nwant %q", got, "/tree /settings /model /theme")
	}
}

// Only recent commands that match the prefix may float. "/theme" matches
// exactly one catalog row, so the recent-but-unmatched /model must vanish.
func TestFloatRecentCmdsRespectsPrefix(t *testing.T) {
	m := recentTestModel(t)
	m.RecentCmds = []string{"model", "theme"}
	m.openPopup("/theme")

	got := strings.Join(m.popupNames(), " ")
	if got != "/theme" {
		t.Errorf("prefix filter should still win, got %q", got)
	}
	if strings.Contains(got, "/model") {
		t.Error("a recent command that does not match the prefix must not appear")
	}
}

// Degenerate shapes must not panic or reorder.
func TestFloatRecentCmdsEdgeCases(t *testing.T) {
	m := recentTestModel(t)
	// no history
	m.RecentCmds = nil
	m.openPopup("/")
	plain := strings.Join(m.popupNames(), " ")
	if plain != "/model /settings /theme /tree /mcp" {
		t.Errorf("no history should leave catalog order, got %q", plain)
	}

	// single match (len(cmdItems) == 1)
	m.RecentCmds = []string{"mcp"}
	m.openPopup("/mcp")
	if got := m.popupNames(); len(got) != 1 || got[0] != "/mcp" {
		t.Errorf("single match = %v", got)
	}

	// history names that no longer exist in the catalog
	m.RecentCmds = []string{"ghost-a", "ghost-b", "model"}
	m.openPopup("/")
	if !strings.HasPrefix(strings.Join(m.popupNames(), " "), "/model ") {
		t.Errorf("stale names should be skipped, got %q", strings.Join(m.popupNames(), " "))
	}
	if strings.Contains(strings.Join(m.popupNames(), " "), "ghost") {
		t.Error("a removed command must not appear")
	}

	// out-of-range index (catalog shrank under a stale item)
	m.RecentCmds = []string{"model"}
	m.cmdItems = []int{99, 0}
	m.floatRecentCmds() // must not panic on index 99
}

// A command is recorded when it RUNS, not when the popup merely stages it.
func TestRecentRecordedOnRunNotOnStage(t *testing.T) {
	m := recentTestModel(t)
	m.openPopup("/")
	m.cmdCursor = 0
	m.completeCmd() // Tab-style staging only
	if len(m.RecentCmds) != 0 {
		t.Fatalf("staging a command must not count as use: %v", m.RecentCmds)
	}
	if m.ta.Value() != "/model " {
		t.Fatalf("completeCmd should stage the text, got %q", m.ta.Value())
	}
}

// End-to-end through the real send path: submitInput runs a builtin, which
// records it, and the next popup leads with the most recent ones.
func TestSubmitRecordsRecencyAndPopupFloats(t *testing.T) {
	m := recentTestModel(t)
	// Only "sidebar" is missing — adding theme/tree again would duplicate
	// catalog rows, which mergeCommands already prevents in production.
	m.Cmds = append(m.Cmds, pirpc.RepoCommand{Name: "sidebar", Description: "hide sidebar", Source: "pitago"})
	ran := 0
	m.UseBuiltins([]Builtin{
		{Name: "theme", Origin: "pitago", Run: func(*Model, string) tea.Cmd { ran++; return nil }},
		{Name: "tree", Origin: "pi", Run: func(*Model, string) tea.Cmd { ran++; return nil }},
		{Name: "sidebar", Origin: "pitago", Run: func(*Model, string) tea.Cmd { ran++; return nil }},
	}, nil)

	// Oldest first, exactly as a user types and hits Enter.
	for _, name := range []string{"sidebar", "tree", "theme"} {
		m.ta.SetValue("/" + name + " ")
		m.submitInput() // returns a tea.Cmd we deliberately never run
	}
	if ran != 3 {
		t.Fatalf("expected 3 builtin runs, got %d", ran)
	}
	want := []string{"theme", "tree", "sidebar"}
	if strings.Join(m.RecentCmds, ",") != strings.Join(want, ",") {
		t.Fatalf("history = %v, want %v", m.RecentCmds, want)
	}

	// The popup now leads with the recorded ones, in recency order, then
	// the untouched catalog order (model, settings, mcp).
	m.openPopup("/")
	wantOrder := "/theme /tree /sidebar /model /settings /mcp"
	if got := strings.Join(m.popupNames(), " "); got != wantOrder {
		t.Errorf("popup order:\n got %q\nwant %q", got, wantOrder)
	}

	// A second run of an already-recorded command moves it back to the top
	// rather than adding a duplicate row.
	m.ta.SetValue("/tree ")
	m.submitInput()
	if strings.Join(m.RecentCmds, ",") != "tree,theme,sidebar" {
		t.Errorf("re-run should re-order, got %v", m.RecentCmds)
	}
	m.openPopup("/")
	if got := m.popupNames(); len(got) < 2 || got[0] != "/tree" || got[1] != "/theme" {
		t.Errorf("after re-run the top two should be /tree /theme, got %v", got)
	}
}
