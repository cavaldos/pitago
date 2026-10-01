package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// Sidebar shows the /session detail: file, msgs split, cached split and
// the COST breakdown — and click mapping still matches the rendered rows
// with those extras present.
func TestSidebarSessionDetail(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Side = map[string]bool{SidePlugins: true} // hidden by default
	m.Status = "ready"
	m.sessionFile = "/tmp/sess.json"
	m.Stats = pirpc.Stats{
		TotalMessages: 10, UserMsgs: 4, AsstMsgs: 4,
		In: 8000, Out: 2000, CacheRead: 1500, CacheWrite: 500,
		TokensTotal: 12000, Cost: 0.123,
	}
	m.sessBreak = []pirpc.CostBreak{
		{Key: "anthropic/claude-opus", Cost: 0.1, Tokens: 8500},
		{Key: "openai/gpt", Cost: 0.023, Tokens: 3500},
	}
	m.recentModels = []RecentModel{{ID: "a"}, {ID: "b"}}
	m.ModelLbl = "a"
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	out := stripANSI(m.renderSidebar())
	for _, want := range []string{
		"/tmp/sess.json", "msgs 10 · u 4 a 4", "cached 1,500", "uncached 8,500",
		"COST", "anthropic/claude-opus", "$0.100",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
	lines := strings.Split(out, "\n")
	outLine := func(needle string) int {
		for i, ln := range lines {
			if strings.Contains(ln, needle) {
				return i
			}
		}
		return -1
	}
	x := m.mainW()
	// output line 0 is the box top border, so the row below a header
	// (line i+1) is the clickable row, like TestRecentAtMatchesRender.
	if i := outLine("RECENT MODELS"); i < 0 {
		t.Fatal("no RECENT MODELS header")
	} else if idx, ok := m.recentAt(x, i+1); !ok || idx != 0 {
		t.Fatalf("recentAt y=%d: got %d,%v, want 0", i+1, idx, ok)
	}
	if i := outLine("PLUGINS"); i < 0 {
		t.Fatal("no PLUGINS header")
	} else if !m.pluginToggleAt(x, i) {
		t.Fatalf("pluginToggleAt y=%d missed", i)
	}
}
