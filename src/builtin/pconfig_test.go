package builtin

import (
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/pirpc"
)

func hubModel() *app.Model {
	m := app.New(nil, "")
	m.Cmds = []pirpc.RepoCommand{
		{Name: "skill:archify", Description: "arch diagrams", Source: "skill"},
	}
	m.Plugins = []app.Plugin{{Spec: "npm:pi-lens", Name: "pi-lens"}}
	m.OpenPconfig()
	return &m
}

// selectPsec moves the hub to a section with focus on the right pane.
func selectPsec(m *app.Model, d *app.Dialog, id string) {
	for i, sid := range d.PsecIDs {
		if sid == id {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)
}

func TestConfirmPconfigRunsSkill(t *testing.T) {
	m := hubModel()
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecSkill)
	if len(d.FIdx) == 0 {
		t.Fatal("skill section should have a row")
	}
	mm, _ := confirmPconfig(m, d, d.FIdx[d.Cursor])
	if len(mm.(*app.Model).Dialogs) != 0 {
		t.Error("FillCommand should close all dialogs")
	}
}

func TestConfirmPconfigAgentPopsHub(t *testing.T) {
	m := hubModel()
	d := m.Dialogs[0] // Agent section, action row
	d.ProvFocus = false
	mm, cmd := confirmPconfig(m, d, d.FIdx[d.Cursor])
	m2 := mm.(*app.Model)
	if len(m2.Dialogs) != 0 {
		t.Fatalf("agent reuses the classic dialog: hub must pop, got %d", len(m2.Dialogs))
	}
	if cmd == nil {
		t.Error("agent should return the loadSettings cmd")
	}
}

func TestConfirmPconfigPluginEnterDoesNothing(t *testing.T) {
	m := hubModel()
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecPlugin)
	mm, cmd := confirmPconfig(m, d, d.FIdx[d.Cursor])
	if len(mm.(*app.Model).Dialogs) != 1 {
		t.Error("hub should stay open")
	}
	if cmd != nil {
		t.Error("plugin Enter must not uninstall (Delete does that)")
	}
}

func TestConfirmPconfigMarketInstalls(t *testing.T) {
	m := hubModel()
	m.Market = []app.MarketEntry{{Name: "pi-new", Version: "1.0.0", Desc: "fresh"}}
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecMarket)
	if len(d.FIdx) == 0 {
		t.Fatal("market section should have a row")
	}
	mm, cmd := confirmPconfig(m, d, d.FIdx[d.Cursor])
	if len(mm.(*app.Model).Dialogs) != 1 {
		t.Error("install should keep the hub open")
	}
	if cmd != nil {
		t.Error("first market Enter must arm the confirm gate, not exec")
	}
	if got := mm.(*app.Model).Status; !strings.Contains(got, "confirm install npm:pi-new") {
		t.Errorf("status should ask for confirm, got %q", got)
	}
	mm2, cmd := confirmPconfig(mm.(*app.Model), d, d.FIdx[d.Cursor])
	if len(mm2.(*app.Model).Dialogs) != 1 {
		t.Error("install should keep the hub open")
	}
	if cmd == nil {
		t.Error("second market Enter should issue the pi install cmd")
	}
	if got := mm2.(*app.Model).Status; !strings.Contains(got, "installing npm:pi-new") {
		t.Errorf("status should name the installed spec, got %q", got)
	}
}

func TestConfirmPconfigMarketMoreLoadsPage(t *testing.T) {
	m := hubModel()
	m.Market = []app.MarketEntry{{Name: "pi-new", Version: "1.0.0", Desc: "fresh"}}
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecMarket)
	// trailing more-row (as psecRows builds it past the loaded total)
	d.Options = append(d.Options, "… load more (1/250) …")
	d.Descs = append(d.Descs, "Enter loads the next 100")
	d.Payload = append(d.Payload, "marketmore")
	d.Reindex()
	mm, cmd := confirmPconfig(m, d, d.FIdx[len(d.FIdx)-1])
	if len(mm.(*app.Model).Dialogs) != 1 {
		t.Error("paging should keep the hub open")
	}
	if cmd == nil {
		t.Error("more row Enter should issue the next-page fetch")
	}
}

func TestConfirmPconfigMarketInstalledStays(t *testing.T) {
	m := hubModel() // pi-lens already in m.Plugins
	m.Market = []app.MarketEntry{{Name: "pi-lens", Version: "9.9.9", Desc: "have it"}}
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecMarket)
	mm, cmd := confirmPconfig(m, d, d.FIdx[d.Cursor])
	if len(mm.(*app.Model).Dialogs) != 1 {
		t.Error("installed row should keep the hub open")
	}
	if cmd != nil {
		t.Error("installed row must not issue an install cmd")
	}
}

func TestConfirmPconfigSideToggle(t *testing.T) {
	m := hubModel()
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecSide)
	// one row per sidebar section in app.sideOrder: pet, session, model, stats,
	// cost, recent, commands, plugins, mcp, todos, tools, workspace
	if len(d.Options) != 12 {
		t.Fatalf("sidebar section should list 12 rows, got %d", len(d.Options))
	}
	// MCP starts hidden
	mi := -1
	for i, o := range d.Options {
		if o == "MCP servers" {
			mi = d.FIdx[i]
		}
	}
	if mi < 0 {
		t.Fatal("no MCP servers row")
	}
	if !strings.Contains(d.Descs[mi], "hidden") {
		t.Fatalf("mcp should show hidden, got %q", d.Descs[mi])
	}
	// cursor sits on the MCP row: toggling must not jump it back to top
	d.Cursor = 0
	for i, fi := range d.FIdx {
		if fi == mi {
			d.Cursor = i
		}
	}
	wantCursor := d.Cursor
	mm, _ := confirmPconfig(m, d, mi)
	m2 := mm.(*app.Model)
	if got := m2.Dialogs[0].Cursor; got != wantCursor {
		t.Fatalf("toggle moved cursor to %d, want %d", got, wantCursor)
	}
	if len(m2.Dialogs) != 1 {
		t.Fatal("sidebar toggle should keep the hub open")
	}
	if !m2.SideVisible(app.SideMCP) {
		t.Error("enter should show mcp")
	}
	if !strings.Contains(m2.Dialogs[0].Descs[mi], "shown") {
		t.Fatalf("row should refresh to shown, got %q", m2.Dialogs[0].Descs[mi])
	}
}

func TestConfirmPconfigTasksCycle(t *testing.T) {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	m := app.New(nil, t.TempDir())
	m.OpenPconfig()
	d := m.Dialogs[0]
	pm := &m
	selectPsec(pm, d, app.PsecTasks)
	ri := -1
	for _, fi := range d.FIdx {
		if d.Payload[fi] == "tasks:taskScope" {
			ri = fi
		}
	}
	if ri < 0 {
		t.Fatal("no taskScope row")
	}
	// cursor sits on the row: cycling must keep it there, hub stays open
	d.Cursor = 0
	for i, fi := range d.FIdx {
		if fi == ri {
			d.Cursor = i
		}
	}
	wantCursor := d.Cursor
	mm, cmd := confirmPconfig(pm, d, ri)
	m2 := mm.(*app.Model)
	if cmd != nil {
		t.Error("tasks cycle is local, no cmd expected")
	}
	if len(m2.Dialogs) != 1 {
		t.Fatal("tasks cycle should keep the hub open")
	}
	if got := m2.Dialogs[0].Cursor; got != wantCursor {
		t.Fatalf("cycle moved cursor to %d, want %d", got, wantCursor)
	}
	if !strings.Contains(m2.Dialogs[0].Descs[ri], "session-global") {
		t.Fatalf("row should refresh to session-global, got %q", m2.Dialogs[0].Descs[ri])
	}
}
