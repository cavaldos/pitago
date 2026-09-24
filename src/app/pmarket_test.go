package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const sampleSearch = `{"total":5123,"objects":[
	{"package":{"name":"pi-lens","version":"4.2.1","description":"Real-time  code\nfeedback","keywords":["pi","pi-package"]}},
	{"package":{"name":"not-a-plugin","version":"1.0.0","description":"no keyword","keywords":["pi"]}},
	{"package":{"name":"","version":"1.0.0","description":"blank","keywords":["pi-package"]}}
]}`

func TestParseMarket(t *testing.T) {
	got, total := parseMarket([]byte(sampleSearch))
	if len(got) != 1 {
		t.Fatalf("want 1 pi-package entry, got %+v", got)
	}
	if total != 5123 {
		t.Errorf("want total 5123, got %d", total)
	}
	if got[0].Name != "pi-lens" || got[0].Version != "4.2.1" {
		t.Errorf("wrong entry: %+v", got[0])
	}
	if got[0].Desc != "Real-time code feedback" {
		t.Errorf("desc should collapse whitespace, got %q", got[0].Desc)
	}
	if entries, total := parseMarket([]byte("{bad")); entries != nil || total != 0 {
		t.Error("bad JSON should give nil/0, not crash")
	}
}

func TestMarketInstalled(t *testing.T) {
	m := &Model{Plugins: []Plugin{{Spec: "npm:@scope/pi-foo", Name: "@scope/pi-foo"}}}
	if !marketInstalled(m, "@scope/pi-foo") {
		t.Error("scoped spec suffix should match the registry name")
	}
	if marketInstalled(m, "pi-bar") {
		t.Error("missing plugin should not match")
	}
}

// Entering the marketplace with a stale cache fires the background fetch
// (rows reload when MarketMsg lands).
func TestMarketSectionEnterFetches(t *testing.T) {
	oldData, oldAt, oldFly := marketCacheData, marketCacheAt, marketInflight
	marketCacheData, marketCacheAt, marketInflight = nil, time.Time{}, false
	defer func() { marketCacheData, marketCacheAt, marketInflight = oldData, oldAt, oldFly }()

	mv := *testPconfigModel()
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs { // Plugins index, one above Marketplace
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = true
	mm, cmd := mv.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, d)
	mv = mm.(Model)
	if mv.Dialogs[0].CurPsec() != PsecMarket {
		t.Fatalf("down from Plugins should land on Marketplace, got %q", mv.Dialogs[0].CurPsec())
	}
	if cmd == nil {
		t.Error("first market visit should fire the fetch cmd")
	}
	if !marketInflight {
		t.Error("fetch should latch inflight so a second visit does not refire")
	}
	mm, cmd = mv.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, mv.Dialogs[0])
	mv = mm.(Model)
	_ = cmd
}

func TestMarketMsgReloadsRows(t *testing.T) {
	oldFly := marketInflight
	marketInflight = true
	defer func() { marketInflight = oldFly }()

	mv := *testPconfigModel()
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	mv.LoadPsecRows(d)
	mm, _ := mv.Update(MarketMsg{Entries: []MarketEntry{
		{Name: "pi-new", Version: "2.0.0", Desc: "fresh"},
	}})
	mv = mm.(Model)
	if len(mv.Market) != 1 || mv.MarketErr != "" {
		t.Fatalf("market should store entries, got %+v err %q", mv.Market, mv.MarketErr)
	}
	if marketInflight {
		t.Error("MarketMsg should unlatch inflight")
	}
	d = mv.Dialogs[0]
	if len(d.Options) != 1 || d.Options[0] != "pi-new" {
		t.Fatalf("hub rows should reload in place, got %v", d.Options)
	}

	mm, _ = mv.Update(MarketMsg{Err: errors.New("boom")})
	mv = mm.(Model)
	if mv.MarketErr == "" {
		t.Error("fetch error should surface in MarketErr")
	}
}

// Delete uninstalls the highlighted plugin; Enter does nothing there.
// ⌫ with filter text edits the filter, with empty filter it deletes too.
func TestPconfigDeleteRemovesPlugin(t *testing.T) {
	mkHub := func() Model {
		mv := *testPconfigModel() // one plugin: npm:pi-lens
		mv.OpenPconfig()
		d := mv.Dialogs[0]
		for i, id := range d.PsecIDs {
			if id == PsecPlugin {
				d.ProvCursor = i
			}
		}
		d.ProvFocus = false
		mv.LoadPsecRows(d)
		return mv
	}
	key := func(typ tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: typ} }

	mv := mkHub()
	mm, cmd := mv.Update(key(tea.KeyDelete))
	mv = mm.(Model)
	if cmd != nil {
		t.Fatal("first Delete must arm the confirm gate, not exec")
	}
	if got := mv.Status; !strings.Contains(got, "confirm remove npm:pi-lens") {
		t.Errorf("status should ask for confirm, got %q", got)
	}
	mm, cmd = mv.Update(key(tea.KeyDelete))
	mv = mm.(Model)
	if cmd == nil {
		t.Fatal("second Delete should issue the pi remove cmd")
	}
	if got := mv.Status; !strings.Contains(got, "removing npm:pi-lens") {
		t.Errorf("status should name the removed spec, got %q", got)
	}
	if len(mv.Dialogs) != 1 {
		t.Error("uninstall should keep the hub open")
	}

	mv = mkHub() // ⌫ with filter text edits, never deletes
	mv.Dialogs[0].Filter = "lens"
	mv.Dialogs[0].Reindex()
	mm, cmd = mv.Update(key(tea.KeyBackspace))
	mv = mm.(Model)
	if cmd != nil {
		t.Error("⌫ with filter text must edit the filter, not delete")
	}
	if mv.Dialogs[0].Filter != "len" {
		t.Errorf("filter should shrink to %q, got %q", "len", mv.Dialogs[0].Filter)
	}

	mv = mkHub() // ⌫ with empty filter arms, second press deletes
	mm, cmd = mv.Update(key(tea.KeyBackspace))
	mv = mm.(Model)
	if cmd != nil {
		t.Error("first ⌫ with empty filter must arm, not delete")
	}
	mm, cmd = mv.Update(key(tea.KeyBackspace))
	if cmd == nil {
		t.Error("second ⌫ with empty filter should delete the plugin")
	}

	mv = mkHub() // sections pane: Delete does nothing
	mv.Dialogs[0].ProvFocus = true
	if _, cmd = mv.Update(key(tea.KeyDelete)); cmd != nil {
		t.Error("Delete on the sections pane must do nothing")
	}

	mv = mkHub() // other sections: Delete does nothing
	for i, id := range mv.Dialogs[0].PsecIDs {
		if id == PsecSkill {
			mv.Dialogs[0].ProvCursor = i
		}
	}
	mv.LoadPsecRows(mv.Dialogs[0])
	mv.Dialogs[0].ProvFocus = false
	if _, cmd = mv.Update(key(tea.KeyDelete)); cmd != nil {
		t.Error("Delete outside Plugins must do nothing")
	}
}

// A trailing "load more" row appears while the registry total exceeds
// the loaded pages; Enter on it appends the next page in place.
func TestMarketLoadMore(t *testing.T) {
	oldTotal := marketTotal
	marketTotal = 250
	defer func() { marketTotal = oldTotal }()

	mv := *testPconfigModel()
	mv.Market = []MarketEntry{{Name: "pi-new", Version: "2.0.0", Desc: "fresh"}}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)
	if len(d.Options) != 2 || d.Payload[1] != "marketmore" {
		t.Fatalf("want entry + load-more row, got %v/%v", d.Options, d.Payload)
	}
	if !strings.Contains(d.Descs[1], "next 100") {
		t.Errorf("more row should explain itself, got %q", d.Descs[1])
	}

	// appending a page keeps the loaded rows and drops the marker at the end
	mm, _ := mv.Update(MarketMsg{Entries: []MarketEntry{{Name: "pi-old", Version: "1.0.0"}},
		Total: 250, Append: true})
	mv = mm.(Model)
	if len(mv.Market) != 2 || mv.Market[1].Name != "pi-old" {
		t.Fatalf("append should grow the list, got %+v", mv.Market)
	}

	// no marker once everything is loaded
	marketTotal = 2
	mv.LoadPsecRows(mv.Dialogs[0])
	if n := len(mv.Dialogs[0].Options); n != 2 {
		t.Fatalf("full list should have no more row, got %d rows", n)
	}
}

// Marketplace renders the 3-pane layout with install hints; the failed
// fetch shows the unavailable placeholder instead.
func TestMarketRender(t *testing.T) {
	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = []MarketEntry{{Name: "pi-new", Version: "2.0.0", Desc: "fresh"}}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)
	out := stripANSI(mv.renderPconfigDialog(d))
	for _, want := range []string{"MARKETPLACE", "DETAILS", "pi-new", "Enter: install"} {
		if !strings.Contains(out, want) {
			t.Errorf("market render should show %q:\n%s", want, out)
		}
	}
}
