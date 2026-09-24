package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"pitago/src/components/format"
	"pitago/src/components/palette"

	"pitago/src/pirpc"
)

// "/" must match everything (no 8-item cap); the popup window scrolls.
func TestRefreshCmdsShowsAll(t *testing.T) {
	var m Model
	builtins := []Builtin{
		{Name: "model", Desc: "Select model"},
		{Name: "recent", Desc: "Switch recent model"},
	}
	for i := 0; i < 15; i++ {
		builtins = append(builtins, Builtin{Name: "fake" + string(rune('a'+i)), Desc: "fake"})
	}
	m.UseBuiltins(builtins, nil)
	m.Cmds = append(BuiltinRepo(m.builtins), []pirpc.RepoCommand{
		{Name: "mcp", Description: "Show MCP server status", Source: "extension"},
		{Name: "council", Description: "Run a council", Source: "prompt"},
		{Name: "skill:archify", Description: "Diagrams", Source: "skill"},
	}...)
	m.ta = textarea.New()
	m.ta.SetValue("/")
	m.winW, m.winH = 120, 30 // tall enough that cmdWin() keeps palette.Win
	m.refreshCmds()
	if !m.cmdOpen {
		t.Fatal("popup must open on /")
	}
	if len(m.cmdItems) != len(m.Cmds) {
		t.Fatalf("want all %d cmds, got %d", len(m.Cmds), len(m.cmdItems))
	}
	if h := m.popupH(); h > palette.Win+5 {
		t.Fatalf("popup height %d exceeds window %d", h, palette.Win)
	}
	m.cmdCursor = len(m.cmdItems) - 1
	m.ensureCmdVisible()
	if m.cmdOffset+m.cmdWin() != len(m.cmdItems) {
		t.Fatalf("last row must scroll into view, offset=%d", m.cmdOffset)
	}
}

// Builtins are intercepted locally; RPC commands (extension/prompt/skill)
// must fall through to Prompt forwarding.
func TestFindBuiltinRouting(t *testing.T) {
	var m Model
	m.UseBuiltins([]Builtin{{Name: "model"}, {Name: "export"}}, nil)
	if _, _, ok := m.FindBuiltin("/mcp"); ok {
		t.Error("/mcp is an RPC command, must not be intercepted")
	}
	if b, arg, ok := m.FindBuiltin("/model anthropic/x"); !ok || b.Name != "model" || arg != "anthropic/x" {
		t.Errorf("want model+arg, got %q %q %v", b.Name, arg, ok)
	}
	if b, _, ok := m.FindBuiltin("/export"); !ok || b.Name != "export" {
		t.Error("/export must be intercepted (pi-TUI-only, never chat text)")
	}
	if m.RunBuiltin("nope", "") != nil {
		t.Error("unknown builtin must return nil cmd")
	}
}

// Extension commands show pi's "[u:npm:ext] desc" tag and match by extension
// name; builtins keep the trailing [builtin].
func TestCmdExtensionTag(t *testing.T) {
	var m Model
	m.UseBuiltins([]Builtin{{Name: "model", Desc: "Select model"}}, nil)
	m.Cmds = append(BuiltinRepo(m.builtins), []pirpc.RepoCommand{
		{Name: "subagents", Description: "Administer subagents", Source: "extension",
			SourceInfo: &pirpc.SourceInfo{Scope: "user", Source: "npm:pi-subagents"}},
		{Name: "council", Description: "Run a council", Source: "prompt"},
	}...)
	m.ta = textarea.New()
	m.winW, m.winH = 120, 30 // tall enough that cmdWin() keeps palette.Win

	m.ta.SetValue("/pi-subagents")
	m.refreshCmds()
	if len(m.cmdItems) != 1 || m.Cmds[m.cmdItems[0]].Name != "subagents" {
		t.Fatalf("filter by extension must match /subagents, got %v", m.cmdItems)
	}

	m.ta.SetValue("/")
	m.refreshCmds()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	out := m.renderCmdPopup()
	plain := format.StripANSI(out)
	for _, want := range []string{"/subagents — [u:npm:pi-subagents]", "/model — Select model [builtin]", "/council — Run a council [prompt]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("popup missing %q\n%s", want, plain)
		}
	}
	if !strings.Contains(out, "[36m") {
		t.Errorf("command names must render cyan, got\n%s", out)
	}
}

// Pitago-only commands show [pitago] in the popup, pi-parity stays [builtin].
func TestCmdPitagoTag(t *testing.T) {
	var m Model
	m.UseBuiltins([]Builtin{
		{Name: "model", Desc: "Select model", Origin: "pi"},
		{Name: "recent", Desc: "Switch recent model", Origin: "pitago"},
	}, nil)
	m.Cmds = BuiltinRepo(m.builtins)
	if m.Cmds[0].Source != "builtin" || m.Cmds[1].Source != "pitago" {
		t.Fatalf("sources = %q/%q, want builtin/pitago", m.Cmds[0].Source, m.Cmds[1].Source)
	}
	m.ta = textarea.New()
	m.winW, m.winH = 120, 30
	m.ta.SetValue("/")
	m.refreshCmds()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	plain := format.StripANSI(m.renderCmdPopup())
	for _, want := range []string{"/model — Select model [builtin]", "/recent — Switch recent model [pitago]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("popup missing %q\n%s", want, plain)
		}
	}
	// typing the origin narrows to it
	m.ta.SetValue("/pitago")
	m.refreshCmds()
	if len(m.cmdItems) != 1 || m.Cmds[m.cmdItems[0]].Name != "recent" {
		t.Fatalf("filter by origin must match /recent, got %v", m.cmdItems)
	}
}

// Regression (screenshot): the / popup was a fixed 10-row window, so on a
// short terminal it overflowed winH (or crushed the chat to 3 rows). The
// window must shrink so the frame stays exactly winH rows.
func TestCmdPopupFitsShortTerminal(t *testing.T) {
	for _, wh := range [][2]int{{100, 20}, {120, 24}} {
		m := New(nil, t.TempDir())
		m.Status = "ready"
		m.ModelLbl = "glm-4.7"
		for i := 0; i < 83; i++ {
			m.Cmds = append(m.Cmds, pirpc.RepoCommand{
				Name:        fmt.Sprintf("cmd%02d", i),
				Description: "Reload keybindings, extensions, skills, prompts, themes, and context files",
				Source:      "builtin",
			})
		}
		tm, _ := m.Update(tea.WindowSizeMsg{Width: wh[0], Height: wh[1]})
		m = tm.(Model)
		m.ta.SetValue("/")
		m.refreshCmds()
		if !m.cmdOpen {
			t.Fatalf("%dx%d: popup must open on /", wh[0], wh[1])
		}
		if m.cmdWin() >= palette.Win {
			t.Fatalf("%dx%d: popup window must shrink below %d, got %d", wh[0], wh[1], palette.Win, m.cmdWin())
		}
		if rows := strings.Split(stripANSI(m.renderCmdPopup()), "\n"); len(rows) != m.popupH() {
			t.Fatalf("%dx%d: popup renders %d rows, want popupH %d", wh[0], wh[1], len(rows), m.popupH())
		}
		if lines := strings.Split(m.View(), "\n"); len(lines) != wh[1] {
			t.Fatalf("%dx%d: frame is %d rows, want %d", wh[0], wh[1], len(lines), wh[1])
		}
	}
}

// The / dropdown hugs its content instead of spanning the chat width:
// short matches → narrow box; long matches → capped at mainW.
func TestCmdPopupHugsContent(t *testing.T) {
	newPopModel := func(cmds []pirpc.RepoCommand, w, h int) Model {
		m := New(nil, t.TempDir())
		m.Status = "ready"
		m.ModelLbl = "test"
		m.Cmds = cmds
		tm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m = tm.(Model)
		m.ta.SetValue("/")
		m.refreshCmds()
		return m
	}
	popW := func(m Model) int {
		w := 0
		for _, ln := range strings.Split(stripANSI(m.renderCmdPopup()), "\n") {
			if x := lipgloss.Width(ln); x > w {
				w = x
			}
		}
		return w
	}
	// short matches → narrow box, footer intact, frame fits
	m := newPopModel([]pirpc.RepoCommand{
		{Name: "new", Description: "Start a new session", Source: "builtin"},
		{Name: "quit", Description: "Quit pi", Source: "builtin"},
	}, 120, 24)
	if !m.cmdOpen {
		t.Fatal("popup must open on /")
	}
	if w, mw := popW(m), m.mainW(); w >= mw {
		t.Fatalf("narrow popup width %d must be < chat width %d", w, mw)
	}
	if out := stripANSI(m.renderCmdPopup()); !strings.Contains(out, "Tab complete") {
		t.Fatalf("popup footer clipped:\n%s", out)
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) != 24 {
		t.Fatalf("frame is %d rows, want 24", len(lines))
	}
	// long matches → capped at mainW
	var long []pirpc.RepoCommand
	for i := 0; i < 83; i++ {
		long = append(long, pirpc.RepoCommand{Name: fmt.Sprintf("cmd%02d", i),
			Description: "Reload keybindings, extensions, skills, prompts, themes, and context files", Source: "builtin"})
	}
	m2 := newPopModel(long, 120, 24)
	if w, mw := popW(m2), m2.mainW(); w != mw {
		t.Fatalf("wide popup width %d must cap at chat width %d", w, mw)
	}
}

// Regression (crash): reconnect/respawn replaced m.Cmds under an open /
// popup — stale cmdItems indices panicked renderCmdPopup. connectedMsg
// must rebuild the match list.
func TestCmdPopupSurvivesReconnect(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "test"
	for i := 0; i < 20; i++ {
		m.Cmds = append(m.Cmds, pirpc.RepoCommand{Name: fmt.Sprintf("cmd%02d", i), Description: "d", Source: "builtin"})
	}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	m.ta.SetValue("/")
	m.refreshCmds()
	if !m.cmdOpen || len(m.cmdItems) != 20 {
		t.Fatalf("popup must list 20 cmds, got %d", len(m.cmdItems))
	}
	// reconnect with a shorter list, then render (panics before the fix)
	tm, _ = m.Update(connectedMsg{cmds: []pirpc.RepoCommand{{Name: "new", Description: "d", Source: "builtin"}}})
	m = tm.(Model)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("View panicked after reconnect: %v", r)
			}
		}()
		_ = m.View()
	}()
	if len(m.cmdItems) != 1 {
		t.Fatalf("popup must rebuild to 1 cmd, got %d", len(m.cmdItems))
	}
}

// Regression (crash): the width pass fed match VALUES into plain() as if
// they were POSITIONS (double index) — any filtered subset where values
// diverge from positions panicked renderCmdPopup.
func TestCmdPopupFilteredWidth(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "test"
	for i := 0; i < 83; i++ {
		m.Cmds = append(m.Cmds, pirpc.RepoCommand{Name: fmt.Sprintf("cmd%02d", i), Description: "d", Source: "builtin"})
	}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	m.ta.SetValue("/cmd8") // matches cmd08, cmd80-82: values != positions
	m.refreshCmds()
	if n := len(m.cmdItems); n == 0 || n >= len(m.Cmds) {
		t.Fatalf("expected a filtered subset, got %d of %d", n, len(m.Cmds))
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("renderCmdPopup panicked on filtered matches: %v", r)
			}
		}()
		_ = m.renderCmdPopup()
		_ = m.View()
	}()
}
