package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// stripSideBox splits a rendered sidebar into plain lines with the box
// borders removed, so tests can compare content instead of frame drawing.
func stripSideBox(s string) []string {
	raw := strings.Split(stripANSI(s), "\n")
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = strings.TrimSpace(strings.ReplaceAll(ln, "│", ""))
	}
	return out
}

// Behaviour: the sidebar model block prints the thinking levels as a
// horizontal strip under the "model · <name> - <level>" row — no ●/○ markers,
// the level in use in bold text colour. Pi precedent: pi's model block shows
// the available level list under the model name.
func TestSidebarRendersThinkingLevels(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "sonnet-4.5"
	m.thinkLvl = "high"
	m.thinkLevels = []string{"off", "low", "high"}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	lines := stripSideBox(m.renderSidebar())
	plain := lines
	modelRow := -1
	for i, ln := range plain {
		if strings.Contains(ln, "model · ") {
			modelRow = i
			break
		}
	}
	if modelRow < 0 {
		t.Fatal("rendered sidebar has no model row")
	}
	// The model row keeps appending the level (see buildSidebarContent).
	if !strings.Contains(plain[modelRow], "sonnet-4.5 - high") {
		t.Fatalf("model row lost the level suffix: %q", plain[modelRow])
	}
	// Short level names share one horizontal row, markers removed.
	if got := strings.TrimSpace(plain[modelRow+1]); got != "off low high" {
		t.Fatalf("level strip: got %q, want %q", got, "off low high")
	}
	if strings.Contains(plain[modelRow+1], "●") || strings.Contains(plain[modelRow+1], "○") {
		t.Fatalf("level strip must not carry ●/○ markers: %q", plain[modelRow+1])
	}
	// The context bar follows the strip directly.
	if !strings.Contains(plain[modelRow+2], "ctx ") {
		t.Fatalf("row after the level strip: got %q, want the ctx bar", plain[modelRow+2])
	}
	if got := m.thinkLevelLines(sideInnerW); len(got) != 1 {
		t.Fatalf("want 1 level row, got %d", len(got))
	}
	// Only the level in use is lit: bold ink, same emphasis toolNameStyle uses.
	if !thinkPickStyle.GetBold() {
		t.Error("the selected thinking level must be rendered bold")
	}
	if thinkPickStyle.GetForeground() != cText {
		t.Error("the selected thinking level must use the full text colour")
	}
}

// Behaviour: a full 7-level model wraps the horizontal strip instead of
// pushing the whole sidebar down — two rows, none wider than the sidebar,
// and no level name silently cut off.
func TestSidebarThinkingLevelsWrapHorizontally(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinkLvl = "high"
	m.thinkLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	rows := m.thinkLevelLines(sideInnerW)
	if len(rows) != 2 {
		t.Fatalf("want the strip wrapped onto 2 rows, got %d: %q", len(rows), stripANSI(strings.Join(rows, "|")))
	}
	want := []string{"off minimal low medium high", "xhigh max"}
	for i, w := range want {
		if got := stripANSI(rows[i]); got != w {
			t.Fatalf("row %d: got %q, want %q", i, got, w)
		}
		if w := lipgloss.Width(rows[i]); w > sideInnerW {
			t.Fatalf("row %d is %d cells, wider than the %d-cell sidebar", i, w, sideInnerW)
		}
	}
}

// Behaviour: every level stays readable — the wrap never truncates a name
// the model actually offers.
func TestSidebarThinkingLevelsNeverTruncated(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinkLvl = "xhigh"
	m.thinkLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	for _, row := range m.thinkLevelLines(sideInnerW) {
		if strings.Contains(row, "…") {
			t.Fatalf("a level name was truncated: %q", stripANSI(row))
		}
	}
}

// Behaviour: without a fetched list the block renders (and budgets) exactly
// as before — the model row keeps its "- off" suffix and no extra rows appear.
func TestSidebarThinkingLevelsEmptyRendersNothing(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "gpt-5"
	if got := m.thinkLevelLines(sideInnerW); len(got) != 0 {
		t.Fatalf("empty list must render no rows, got %d", len(got))
	}
	if n := m.sideModelRows(); n != sideModelRows {
		t.Fatalf("budget without levels: got %d, want %d", n, sideModelRows)
	}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	plain := stripSideBox(m.renderSidebar())
	modelRow := -1
	for i, ln := range plain {
		if strings.Contains(ln, "model · ") {
			modelRow = i
			break
		}
	}
	if modelRow < 0 {
		t.Fatal("rendered sidebar has no model row")
	}
	// The ctx bar sits right under the model row: no strip was inserted.
	if !strings.Contains(plain[modelRow+1], "ctx ") {
		t.Fatalf("row after the model row: got %q, want the ctx bar", plain[modelRow+1])
	}
}

// Behaviour: the SideModel row budget is derived — fixed rows plus the rows
// the horizontal strip actually wraps onto — so the derived click mapping
// follows the render.
func TestSideModelRowsDerivedFromLevels(t *testing.T) {
	var m Model
	if n := m.sideModelRows(); n != 4 {
		t.Fatalf("empty: got %d, want 4", n)
	}
	m.thinkLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	strip := len(m.thinkLevelLines(sideInnerW))
	if want := sideModelRows + strip; m.sideModelRows() != want {
		t.Fatalf("7 levels (%d strip rows): got %d, want %d", strip, m.sideModelRows(), want)
	}
	m.thinkLvl = "high"
	// The level list is model-specific; switching model without a refetch
	// keeps the old rows (nothing to render yet) rather than guessing.
	m.thinkLevels = nil
	if n := m.sideModelRows(); n != 4 {
		t.Fatalf("cleared list: got %d, want 4", n)
	}
}

// Regression: with a thinking-level list present, recentAt must still land
// on the rows renderSidebar draws (the budget is derived, not hardcoded).
func TestRecentAtMatchesRenderWithLevels(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "a"
	m.thinkLvl = "high"
	m.thinkLevels = []string{"off", "low", "high"}
	m.recentModels = []RecentModel{{ID: "a"}, {ID: "b"}}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	lines := strings.Split(stripANSI(m.renderSidebar()), "\n")
	head := -1
	for i, ln := range lines {
		if strings.Contains(ln, "RECENT MODELS") {
			head = i
			break
		}
	}
	if head < 0 {
		t.Fatal("rendered sidebar has no RECENT MODELS header")
	}
	// Same screen mapping the existing regression test uses: line 0 is the
	// box border, so the header line maps to the first model's screen row.
	x := m.mainW()
	for i, want := range []int{0, 1} {
		if idx, ok := m.recentAt(x, 1+head+i); !ok || idx != want {
			t.Fatalf("model %d: got %d,%v", want, idx, ok)
		}
	}
	if _, ok := m.recentAt(x, 1+head-3); ok {
		t.Fatal("a click inside the level list must not hit a recent model")
	}
}

// Behaviour: ThinkLevelsMsg fills the sidebar list; a failed fetch leaves the
// previous list alone (the /thinking picker is the surfacing surface).
func TestThinkLevelsMsg(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(ThinkLevelsMsg{Levels: []string{"low", "high"}})
	m = tm.(Model)
	if len(m.thinkLevels) != 2 || m.thinkLevels[1] != "high" {
		t.Fatalf("levels not stored: %+v", m.thinkLevels)
	}
	tm, _ = m.Update(ThinkLevelsMsg{Err: errFake})
	if got := tm.(Model).thinkLevels; len(got) != 2 {
		t.Fatalf("error must not clobber the list, got %+v", got)
	}
	// A model with no levels is a real answer and clears the block.
	tm, _ = m.Update(ThinkLevelsMsg{Levels: nil})
	if got := tm.(Model).sideModelRows(); got != sideModelRows {
		t.Fatalf("cleared list: got %d, want %d", got, sideModelRows)
	}
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "rpc down" }
