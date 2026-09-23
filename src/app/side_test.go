package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Fresh models hide MCP + Plugins + Commands (Sidebar tab default) but keep
// the core sections: hiding must drop the whole block, not just its rows.
func TestSideDefaultsHidden(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH, m.ready = 120, 30, true // mainW=81, sidebar x>=82
	m.MCP = []McpServer{{Name: "alpha", Connected: true}}
	m.Plugins = []Plugin{{Spec: "npm:pi-lens", Name: "pi-lens"}}
	out := m.buildSidebarContent()
	for _, want := range []string{"MCP Servers", "PLUGINS", "COMMANDS", "alpha", "pi-lens"} {
		if strings.Contains(out, want) {
			t.Errorf("default sidebar must hide %q", want)
		}
	}
	for _, want := range []string{"SESSION", "RECENT MODELS", "Todos"} {
		if !strings.Contains(out, want) {
			t.Errorf("default sidebar must show %q", want)
		}
	}
	if m.pluginToggleAt(82, 1+m.pluginHeaderRow()) {
		t.Error("hidden PLUGINS header must not be clickable")
	}
}

// Toggling persists to prefs.json and reloads back through Configure.
func TestToggleSideSectionPersists(t *testing.T) {
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	if m.SideVisible(SideMCP) {
		t.Fatal("mcp should start hidden")
	}
	m.ToggleSideSection(SideMCP)
	if !m.SideVisible(SideMCP) {
		t.Fatal("toggle should show mcp")
	}
	if got := LoadPrefs(m.prefsPath); !got.SideVisible(SideMCP) {
		t.Fatalf("toggle should persist, got %+v", got.Side)
	}
	m2 := New(nil, t.TempDir())
	m2.prefsPath = m.prefsPath
	prefs := LoadPrefs(m2.prefsPath)
	m2.Side = prefs.Side
	if !m2.SideVisible(SideMCP) {
		t.Error("fresh model should pick up the persisted toggle")
	}
	m.ToggleSideSection(SideMCP)
	if m.SideVisible(SideMCP) {
		t.Fatal("second toggle should hide again")
	}
}

// Click mapping must track the render when upper sections hide: find the
// headers in the drawn box and compare with the computed content rows.
func TestSideHideKeepsClickMapping(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.recentModels = []RecentModel{{ID: "a"}, {ID: "b"}}
	m.ModelLbl = "a"
	m.Plugins = []Plugin{{Spec: "npm:pi-lens", Name: "pi-lens"}}
	m.Side = map[string]bool{
		SidePet: false, SideSession: false, SideModel: false,
		SideStats: false, SidePlugins: true,
	}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	lines := strings.Split(stripANSI(m.renderSidebar()), "\n")
	lineOf := func(needle string) int {
		for i, ln := range lines {
			if strings.Contains(ln, needle) {
				return i
			}
		}
		return -1
	}
	x := m.mainW() + 1
	// box line 0 is the border: content row = line-1, screen y = 1+content.
	if i := lineOf("RECENT MODELS"); i < 0 {
		t.Fatal("no RECENT MODELS header")
	} else if idx, ok := m.recentAt(x, i+1); !ok || idx != 0 {
		t.Fatalf("recentAt with hidden uppers: got %d,%v, want 0", idx, ok)
	}
	if i := lineOf("PLUGINS"); i < 0 {
		t.Fatal("no PLUGINS header")
	} else if !m.pluginToggleAt(x, i) {
		t.Fatalf("pluginToggleAt with hidden uppers missed y=%d (want row %d)", i, m.pluginHeaderRow())
	}
	// hiding recent too: recents miss, plugins header still hits.
	m.ToggleSideSection(SideRecent)
	if _, ok := m.recentAt(x, 1+m.recentContentRow()); ok {
		t.Fatal("hidden RECENT MODELS must not hit")
	}
	lines = strings.Split(stripANSI(m.buildSidebarContent()), "\n")
	prow := -1
	for i, ln := range lines {
		if strings.Contains(ln, "PLUGINS") {
			prow = i
			break
		}
	}
	if prow != m.pluginHeaderRow() {
		t.Fatalf("plugin header renders at %d, formula says %d", prow, m.pluginHeaderRow())
	}
}
