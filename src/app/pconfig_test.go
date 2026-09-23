package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func testPconfigModel() *Model {
	m := &Model{
		Cmds: []pirpc.RepoCommand{
			{Name: "skill:archify", Description: "arch diagrams", Source: "skill"},
			{Name: "council", Description: "advisor council", Source: "prompt"},
			{Name: "mcp", Description: "MCP status", Source: "extension",
				SourceInfo: &pirpc.SourceInfo{Source: "npm:pi-mcp-adapter", Scope: "user"}},
		},
		Plugins: []Plugin{{Spec: "npm:pi-lens", Name: "pi-lens"}},
		MCP:     []McpServer{{Name: "notion", Direct: 2, Total: 5, Tokens: 1200, Connected: true}},
		Stats:   pirpc.Stats{ToolCalls: 3},
	}
	m.AddBlock(Block{Kind: "tool", ToolName: "read", ToolStatus: "done", ToolArgs: "pi.json"})
	m.AddBlock(Block{Kind: "tool", ToolName: "read", ToolStatus: "done"})
	m.AddBlock(Block{Kind: "tool", ToolName: "bash", ToolStatus: "error"})
	return m
}

func TestOpenPconfigTwoPane(t *testing.T) {
	m := testPconfigModel()
	m.OpenPconfig()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "pconfig" {
		t.Fatalf("expected one pconfig dialog, got %+v", m.Dialogs)
	}
	d := m.Dialogs[0]
	if len(d.Provs) != 10 || len(d.PsecIDs) != 10 {
		t.Fatalf("left pane needs 10 sections, got %d/%d", len(d.Provs), len(d.PsecIDs))
	}
	if !d.ProvFocus {
		t.Error("focus should start on the left (sections) pane")
	}
	// initial section = Agent action row
	if len(d.Options) != 1 || d.Payload[0] != "@agent" {
		t.Fatalf("agent section should show one action row, got %v/%v", d.Options, d.Payload)
	}
}

func TestPconfigSectionSwitch(t *testing.T) {
	m := testPconfigModel()
	m.OpenPconfig()
	d := m.Dialogs[0]
	d.ProvCursor = 1 // Skills
	m.LoadPsecRows(d)
	if len(d.Options) != 1 || d.Options[0] != "/skill:archify" {
		t.Fatalf("unexpected skill rows: %v", d.Options)
	}
	if d.Payload[0] != "skill:archify" {
		t.Errorf("payload should carry the runnable name, got %q", d.Payload[0])
	}
	// filter narrows the right pane
	d.Filter = "arch"
	d.Reindex()
	if len(d.FIdx) != 1 {
		t.Errorf("filter should match 1 row, got %d", len(d.FIdx))
	}
	d.Filter = "zzz"
	d.Reindex()
	if len(d.FIdx) != 0 {
		t.Errorf("filter should match 0 rows, got %d", len(d.FIdx))
	}
	// Tools section aggregates the transcript
	for i, id := range d.PsecIDs {
		if id == PsecTool {
			d.ProvCursor = i
		}
	}
	m.LoadPsecRows(d)
	if len(d.Options) != 2 || d.Options[0] != "bash" || d.Options[1] != "read" {
		t.Fatalf("tool rows should be sorted names, got %v", d.Options)
	}
	if d.Descs[1] != "2x · 2 done — pi.json" {
		t.Errorf("read aggregate wrong: %q", d.Descs[1])
	}
}

func TestPconfigEmptySections(t *testing.T) {
	m := &Model{}
	m.OpenPconfig()
	d := m.Dialogs[0]
	d.ProvCursor = 1 // Skills, empty
	m.LoadPsecRows(d)
	if d.Options[0] != "— no skills —" || d.Payload[0] != "" {
		t.Errorf("empty skills should show an info placeholder, got %v/%v", d.Options, d.Payload)
	}
}

func TestPconfigCurPsecBounds(t *testing.T) {
	d := &Dialog{PsecIDs: []string{"a", "b"}, ProvCursor: 9}
	if d.CurPsec() != "a" {
		t.Errorf("out-of-range cursor should fall back to first, got %q", d.CurPsec())
	}
	if (&Dialog{}).CurPsec() != "" {
		t.Error("no sections should give empty id")
	}
}

func TestPconfigKeys(t *testing.T) {
	m := testPconfigModel()
	m.OpenPconfig()
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	step := func(mm tea.Model) *Model {
		m2 := mm.(Model)
		return &m2
	}

	// ↓ on sections reloads the right pane to Skills
	m = step(mustUpdate(t, m, key(tea.KeyDown)))
	d := m.Dialogs[0]
	if d.CurPsec() != PsecSkill || len(d.Options) != 1 {
		t.Fatalf("down should select skills, got %s %v", d.CurPsec(), d.Options)
	}
	// Enter on the left focuses the right pane
	m = step(mustUpdate(t, m, key(tea.KeyEnter)))
	if m.Dialogs[0].ProvFocus {
		t.Error("enter on sections should focus the right pane")
	}
	// ← goes back to sections
	m = step(mustUpdate(t, m, key(tea.KeyLeft)))
	if !m.Dialogs[0].ProvFocus {
		t.Error("left should refocus sections")
	}
	// typing filters the right pane
	m = step(mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")}))
	if len(m.Dialogs[0].FIdx) != 0 {
		t.Error("typing should filter right rows to zero")
	}
}

func mustUpdate(t *testing.T, m *Model, km tea.KeyMsg) tea.Model {
	t.Helper()
	mm, _ := m.updatePconfigDialog(km, m.Dialogs[0])
	return mm
}

// Sidebar rows render table-like: every "—" starts at the same column,
// and the state word is colored (shown bright, hidden dark).
func TestPconfigSideTableAligned(t *testing.T) {
	m := testPconfigModel()
	m.winW, m.winH = 120, 40
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecSide {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)
	lines := strings.Split(stripANSI(m.renderPconfigDialog(d)), "\n")
	dash := -1
	n := 0
	for _, ln := range lines {
		if !strings.Contains(ln, "│") {
			continue
		}
		j := strings.Index(ln, "—")
		if j < 0 {
			continue
		}
		n++
		// display column, not byte index (marks like ▸ are multibyte)
		if col := lipgloss.Width(ln[:j]); dash < 0 {
			dash = col
		} else if col != dash {
			t.Fatalf("desc column drifts: col %d, want %d\n%s", col, dash, ln)
		}
	}
	if n != len(sideOrder) {
		t.Fatalf("want %d sidebar rows, got %d", len(sideOrder), n)
	}
}

// The Plugins section shows a third DETAILS column on wide terminals:
// spec + source + path + contributed /commands for the highlighted row.
func TestPconfigPluginDetailColumn(t *testing.T) {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	delete(pluginMetaCache, "npm:pi-fake-noexist")
	m := &Model{
		Cmds: []pirpc.RepoCommand{
			{Name: "review", Description: "code review", Source: "extension",
				SourceInfo: &pirpc.SourceInfo{Source: "npm:pi-fake-noexist", Scope: "user"}},
		},
		Plugins: []Plugin{{Spec: "npm:pi-fake-noexist", Name: "pi-fake-noexist"}},
	}
	m.winW, m.winH = 140, 40
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)
	out := stripANSI(m.renderPconfigDialog(d))
	if !strings.Contains(out, "DETAILS") {
		t.Fatalf("wide plugin tab should show a DETAILS column:\n%s", out)
	}
	for _, want := range []string{"pi-fake-noexist", "npm:pi-fake-noexist", "Commands (1)", "/review"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail column should show %q:\n%s", want, out)
		}
	}
}

// Wheel scrolls the hub list and never reaches the chat: contents pane
// moves Cursor, sections pane moves ProvCursor and reloads the rows.
func TestPconfigWheelScrollsDialog(t *testing.T) {
	mv := *testPconfigModel()
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	d.ProvFocus = false
	for i, id := range d.PsecIDs {
		if id == PsecTool { // two rows: bash, read
			d.ProvCursor = i
		}
	}
	mv.LoadPsecRows(d)
	wheel := func(down bool) {
		b := tea.MouseButtonWheelDown
		if !down {
			b = tea.MouseButtonWheelUp
		}
		mm, _ := mv.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: b})
		mv = mm.(Model)
	}
	if mv.Dialogs[0].Cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", mv.Dialogs[0].Cursor)
	}
	wheel(true)
	if mv.Dialogs[0].Cursor != 1 {
		t.Fatalf("wheel down should move to 1, got %d", mv.Dialogs[0].Cursor)
	}
	wheel(true) // wraps like ↓
	if mv.Dialogs[0].Cursor != 0 {
		t.Fatalf("wheel down should wrap to 0, got %d", mv.Dialogs[0].Cursor)
	}
	// sections pane: wheel moves sections and reloads the right pane
	mv.Dialogs[0].ProvFocus = true
	cur := mv.Dialogs[0].CurPsec()
	wheel(true)
	d = mv.Dialogs[0]
	if d.CurPsec() == cur {
		t.Fatalf("wheel on sections should change section, still %q", cur)
	}
	if len(d.Options) == 0 {
		t.Error("wheel on sections should reload the right pane rows")
	}
	// non-wheel mouse stays swallowed: no cursor move, no dialog change
	before := d.Cursor
	mm, _ := mv.Update(tea.MouseMsg{Action: tea.MouseActionRelease})
	mv = mm.(Model)
	if len(mv.Dialogs) != 1 || mv.Dialogs[0].Cursor != before {
		t.Error("click release behind the hub should change nothing")
	}
}

// A long detail (many commands) must not stretch the dialog: overflow
// folds into a "…(+N more)" marker and the box keeps its fixed height.
func TestPconfigPluginDetailFixedHeight(t *testing.T) {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	delete(pluginMetaCache, "npm:pi-fake-noexist")
	m := &Model{
		Plugins: []Plugin{{Spec: "npm:pi-fake-noexist", Name: "pi-fake-noexist"}},
	}
	for i := 0; i < 30; i++ {
		m.Cmds = append(m.Cmds, pirpc.RepoCommand{
			Name: fmt.Sprintf("cmd%02d", i), Description: "x", Source: "extension",
			SourceInfo: &pirpc.SourceInfo{Source: "npm:pi-fake-noexist", Scope: "user"},
		})
	}
	m.winW, m.winH = 140, 40
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)
	out := stripANSI(m.renderPconfigDialog(d))
	if !strings.Contains(out, "more)") {
		t.Fatalf("overflowing detail should fold into a +N more marker:\n%s", out)
	}
	if strings.Contains(out, "/cmd29") {
		t.Errorf("tail commands should be cut off, not stretch the box:\n%s", out)
	}
	// same box height as a plugin with no commands
	m2 := &Model{Plugins: []Plugin{{Spec: "npm:pi-fake-noexist", Name: "pi-fake-noexist"}}}
	m2.winW, m2.winH = 140, 40
	m2.OpenPconfig()
	d2 := m2.Dialogs[0]
	for i, id := range d2.PsecIDs {
		if id == PsecPlugin {
			d2.ProvCursor = i
		}
	}
	d2.ProvFocus = false
	m2.LoadPsecRows(d2)
	rows := func(s string) int {
		n := 0
		for _, ln := range strings.Split(stripANSI(s), "\n") {
			if strings.Contains(ln, "│") {
				n++
			}
		}
		return n
	}
	if a, b := rows(m.renderPconfigDialog(d)), rows(m2.renderPconfigDialog(d2)); a != b {
		t.Errorf("box height drifts with content: %d rows vs %d rows", a, b)
	}
}

// Narrow terminals keep the classic two panes: no DETAILS column, the
// spec stays in the row desc instead.
func TestPconfigPluginNoDetailWhenNarrow(t *testing.T) {
	m := &Model{
		Plugins: []Plugin{{Spec: "npm:pi-fake-noexist", Name: "pi-fake-noexist"}},
	}
	m.winW, m.winH = 90, 40
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)
	out := stripANSI(m.renderPconfigDialog(d))
	if strings.Contains(out, "DETAILS") {
		t.Fatalf("narrow plugin tab should not show a DETAILS column:\n%s", out)
	}
	if !strings.Contains(out, "npm:pi-fa") {
		t.Errorf("narrow rows should keep the spec in the desc:\n%s", out)
	}
}

func TestPsecDescStateColors(t *testing.T) {
	shown := psecDesc("side:mcp", "shown · Enter: toggle", 40)
	hidden := psecDesc("side:mcp", "hidden · Enter: toggle", 40)
	if stripANSI(shown) != "— shown · Enter: toggle" || stripANSI(hidden) != "— hidden · Enter: toggle" {
		t.Fatalf("stripped descs wrong: %q %q", stripANSI(shown), stripANSI(hidden))
	}
	// shown = bright text, hidden = dark like the hint
	if got := sideStateStyle("shown").GetForeground(); got != cText {
		t.Errorf("shown should be bright (%v), got %v", cText, got)
	}
	if got := sideStateStyle("hidden").GetForeground(); got != toolStyle.GetForeground() {
		t.Errorf("hidden should be dark (%v), got %v", toolStyle.GetForeground(), got)
	}
	// truncation still applies before styling (no ANSI to cut)
	if got := stripANSI(psecDesc("side:mcp", "shown · Enter: toggle", 8)); got != "— "+Short("shown · Enter: toggle", 8) {
		t.Fatalf("truncated desc wrong: %q", got)
	}
}
