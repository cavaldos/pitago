package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const sampleSearch = `{"total":10943,"objects":[
	{"package":{"name":"pi-lens","version":"4.2.1","description":"Real-time  code\nfeedback","keywords":["pi","pi-package"],
		"links":{"repository":{"type":"git","url":"git+https://github.com/boris1993/pi-lens.git"},"homepage":"https://github.com/boris1993/pi-lens#readme"}},
		"downloads":{"weekly":22900}},
	{"package":{"name":"not-a-plugin","version":"1.0.0","description":"no keyword","keywords":["pi"]}},
	{"package":{"name":"","version":"1.0.0","description":"blank","keywords":["pi-package"]}}
]}`

func TestParseMarket(t *testing.T) {
	got, returned, err := parseMarket([]byte(sampleSearch))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pi-package entry, got %+v", got)
	}
	// returned counts raw objects (the paging signal), not filtered ones
	if returned != 3 {
		t.Errorf("want 3 raw objects, got %d", returned)
	}
	if got[0].Name != "pi-lens" || got[0].Version != "4.2.1" {
		t.Errorf("wrong entry: %+v", got[0])
	}
	if got[0].Desc != "Real-time code feedback" {
		t.Errorf("desc should collapse whitespace, got %q", got[0].Desc)
	}
	if got[0].Repo != "boris1993/pi-lens" {
		t.Errorf("repo should come from the registry links, got %q", got[0].Repo)
	}
	if got[0].Weekly != 22900 {
		t.Errorf("weekly downloads should decode, got %d", got[0].Weekly)
	}
	if entries, returned, err := parseMarket([]byte("{bad")); entries != nil || returned != 0 || err == nil {
		t.Error("bad JSON should give nil/0/err, not crash")
	}
}

// The registry link shapes npm packages actually ship.
func TestGhRepo(t *testing.T) {
	for _, tc := range []struct{ repo, home, want string }{
		{"git+https://github.com/o/r.git", "", "o/r"},
		{"https://github.com/o/r", "", "o/r"},
		{"git@github.com:o/r.git", "", "o/r"},
		{"github:o/r", "", "o/r"},
		{"", "https://github.com/o/r/", "o/r"},
		{"", "https://github.com/o/r#readme", "o/r"},
		{"https://gitlab.com/o/r", "https://bitbucket.org/o/r", ""},
		{"", "", ""},
	} {
		if got := ghRepo(tc.repo, tc.home); got != tc.want {
			t.Errorf("ghRepo(%q, %q) = %q, want %q", tc.repo, tc.home, got, tc.want)
		}
	}
}

// The query rides on the keyword search so npm ranks pi packages first.
func TestMarketSearchURL(t *testing.T) {
	if got := marketURL("", 0); got != "https://registry.npmjs.org/-/v1/search?text=keywords:pi-package&size=100&from=0" {
		t.Errorf("empty query should stay the plain keyword search, got %q", got)
	}
	if got := marketURL("pi lens", 0); !strings.Contains(got, "text=keywords:pi-package+pi+lens") {
		t.Errorf("space should escape to +, got %q", got)
	}
	if got := marketURL("@scope/pkg", 100); !strings.Contains(got, "text=keywords:pi-package+%40scope%2Fpkg") ||
		!strings.Contains(got, "from=100") {
		t.Errorf("scoped query should survive escaping with the page offset, got %q", got)
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
	oldPages, oldFly, oldSorted := marketPages, marketInflight, marketSorted
	marketPages, marketInflight, marketSorted = map[string]marketPage{}, false, map[string]bool{}
	defer func() { marketPages, marketInflight, marketSorted = oldPages, oldFly, oldSorted }()

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
	oldFly, oldFlyQ, oldSorted := marketInflight, marketInflightQuery, marketSorted
	// the browse page is the one in flight: a landing result only
	// unlatches the latch that owns its query
	marketInflight, marketInflightQuery, marketSorted = true, "", map[string]bool{}
	defer func() { marketInflight, marketInflightQuery, marketSorted = oldFly, oldFlyQ, oldSorted }()

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

// A trailing "load more" row appears while the last registry page was
// full; Enter on it appends the next page in place.
func TestMarketLoadMore(t *testing.T) {
	oldMore := marketMore
	marketMore = true
	defer func() { marketMore = oldMore }()

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
		More: true, Append: true})
	mv = mm.(Model)
	if len(mv.Market) != 2 || mv.Market[1].Name != "pi-old" {
		t.Fatalf("append should grow the list, got %+v", mv.Market)
	}

	// no marker once the registry says the last page was the last one
	mm, _ = mv.Update(MarketMsg{Query: "pi-old", Entries: []MarketEntry{{Name: "pi-a"}, {Name: "pi-b"}}})
	mv = mm.(Model)
	marketMore = false
	mv.LoadPsecRows(mv.Dialogs[0])
	if n := len(mv.Dialogs[0].Options); n != 2 {
		t.Fatalf("full list should have no more row, got %d rows", n)
	}
}

// Typing in the marketplace is a debounced REMOTE npm search, not a local
// filter: a tick is armed, stale ticks are dropped, and a live one fires
// exactly one fetch unless the page is already cached.
func TestMarketRemoteSearchDebounce(t *testing.T) {
	oldPages, oldFly, oldFlyQ := marketPages, marketInflight, marketInflightQuery
	marketPages, marketInflight, marketInflightQuery = map[string]marketPage{}, false, ""
	defer func() {
		marketPages, marketInflight, marketInflightQuery = oldPages, oldFly, oldFlyQ
	}()

	mv := *testPconfigModel()
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)

	mm, cmd := mv.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("le")}, d)
	mv = mm.(Model)
	if mv.Dialogs[0].Filter != "le" {
		t.Fatalf("typing should still edit the filter, got %q", mv.Dialogs[0].Filter)
	}
	if cmd == nil {
		t.Fatal("typing should arm the debounce tick")
	}
	if marketInflight {
		t.Error("typing must not fetch immediately")
	}
	// the armed tick resolves to the query, and a stale one is dropped
	if msg := cmd(); msg != (MarketSearchMsg{Query: "le"}) {
		t.Errorf("tick should carry the typed query, got %+v", msg)
	}

	mm, cmd = mv.Update(MarketSearchMsg{Query: "le"})
	mv = mm.(Model)
	if cmd == nil || !marketInflight || marketInflightQuery != "le" {
		t.Fatal("a tick matching the filter should fire one search")
	}
	mm, cmd = mv.Update(MarketSearchMsg{Query: "lens"}) // user kept typing
	mv = mm.(Model)
	if cmd != nil {
		t.Error("a stale tick must not fire a second search")
	}
	// once the page is cached fresh, the same query does not refetch
	marketStore("le", []MarketEntry{{Name: "pi-lens"}}, false)
	marketInflight, marketInflightQuery = false, ""
	mm, cmd = mv.Update(MarketSearchMsg{Query: "le"})
	mv = mm.(Model)
	if cmd != nil {
		t.Error("a cached query must not refetch")
	}
}

// A result only replaces the visible list while the hub still shows its
// query; otherwise it just warms the cache.
func TestMarketMsgQueryScoped(t *testing.T) {
	oldPages, oldFly := marketPages, marketInflight
	marketPages, marketInflight = map[string]marketPage{}, false
	defer func() { marketPages, marketInflight = oldPages, oldFly }()

	mv := *testPconfigModel()
	mv.Market = []MarketEntry{{Name: "browse"}}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)

	// visible: the filter matches
	d = mv.Dialogs[0]
	d.Filter = "lens"
	d.Reindex()
	mm, _ := mv.Update(MarketMsg{Query: "lens", Entries: []MarketEntry{{Name: "pi-lens"}}, More: true})
	mv = mm.(Model)
	if len(mv.Market) != 1 || mv.Market[0].Name != "pi-lens" {
		t.Fatalf("matching query should replace the list, got %+v", mv.Market)
	}
	if !marketMore {
		t.Error("more should follow the visible page")
	}

	// stale: the user typed on, so the old result only warms the cache
	d = mv.Dialogs[0]
	d.Filter = "lensx"
	d.Reindex()
	mm, _ = mv.Update(MarketMsg{Query: "lens", Entries: []MarketEntry{{Name: "pi-old"}}})
	mv = mm.(Model)
	if mv.Market[0].Name != "pi-lens" {
		t.Fatalf("a stale result must not replace the visible list, got %+v", mv.Market)
	}
	if c, _, fresh := marketCached("lens"); !fresh || len(c) != 1 {
		t.Error("a stale result should still warm the cache")
	}
}

// A search the user typed past that fails must stay quiet: it must not
// unlatch the fetch the user is actually waiting on, nor post a notice.
func TestMarketStaleFailureSilent(t *testing.T) {
	oldPages := marketPages
	marketPages = map[string]marketPage{}
	defer func() { marketPages = oldPages }()

	mv := *testPconfigModel()
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	d.Filter = "lensx"
	d.Reindex()
	mv.LoadPsecRows(d)

	// the live fetch is for "lensx"; the stale "le" search fails
	marketInflight, marketInflightQuery = true, "lensx"
	nToasts := len(mv.toasts)
	mm, _ := mv.Update(MarketMsg{Query: "le", Err: errors.New("registry: 500")})
	mv = mm.(Model)
	if !marketInflight || marketInflightQuery != "lensx" {
		t.Error("a superseded result must not unlatch the live fetch")
	}
	if mv.MarketErr != "" {
		t.Errorf("a stale failure must not set MarketErr, got %q", mv.MarketErr)
	}
	if len(mv.toasts) != nToasts {
		t.Errorf("a stale failure must not raise a notice, got %d new toasts", len(mv.toasts)-nToasts)
	}

	// the visible page failing still reports, exactly as the browse path did
	mm, _ = mv.Update(MarketMsg{Query: "lensx", Err: errors.New("registry: 500")})
	mv = mm.(Model)
	if marketInflight {
		t.Error("the live fetch should unlatch when its own result lands")
	}
	if mv.MarketErr == "" || len(mv.toasts) != nToasts+1 {
		t.Errorf("a visible failure should report, got err %q / %d new toasts", mv.MarketErr, len(mv.toasts)-nToasts)
	}
	if got := mv.LastNotice(); !strings.Contains(got, "marketplace failed:") {
		t.Errorf("the notice should name the marketplace failure, got %q", got)
	}
}

// Star hydration: only the visible window's unresolved repos, once each,
// misses are scoped to the batch that asked, and a real 0-star repo is
// never refetched.
func TestMarketStarsHydration(t *testing.T) {
	oldStars, oldBad, oldBusy := marketStars, marketStarsBad, marketStarsBusy
	marketStars, marketStarsBad, marketStarsBusy = map[string]int{}, map[string]bool{}, map[string]bool{}
	defer func() {
		marketStars, marketStarsBad, marketStarsBusy = oldStars, oldBad, oldBusy
	}()

	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = []MarketEntry{{Name: "pi-a", Repo: "o/a"}, {Name: "pi-b", Repo: "o/b"}}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)

	cmd := mv.hydrateMarketStarsCmd(d)
	if cmd == nil {
		t.Fatal("unresolved visible repos should arm the fetch")
	}
	if !marketStarsBusy["o/a"] || !marketStarsBusy["o/b"] {
		t.Error("the requested repos should latch busy on the UI goroutine")
	}
	// a repo another batch is still fetching must not be re-asked
	marketStarsBusy["o/c"] = true

	// everything known → nothing to do
	marketStars["o/a"], marketStars["o/b"] = 1, 2
	mv.Market[0].Stars, mv.Market[0].StarsKnown = 1, true
	mv.Market[1].Stars, mv.Market[1].StarsKnown = 2, true
	if cmd := mv.hydrateMarketStarsCmd(d); cmd != nil {
		t.Error("resolved entries should not refetch stars")
	}

	// a batch that answered for o/b only marks o/b (asked), never the
	// unrelated in-flight o/c
	marketInflight = false
	mv.Market[0].StarsKnown, mv.Market[1].StarsKnown = false, false
	mm, _ := mv.Update(MarketStarsMsg{Stars: map[string]int{"o/b": 1565}, Asked: []string{"o/b"}})
	mv = mm.(Model)
	if !mv.Market[1].StarsKnown || mv.Market[1].Stars != 1565 {
		t.Fatalf("a real hit should patch the entry, got %+v", mv.Market[1])
	}
	if marketStarsBad["o/b"] {
		t.Error("a hit must not be marked bad")
	}
	if marketStarsBad["o/c"] {
		t.Error("a repo this batch never asked must not be marked bad")
	}
	if marketStarsBusy["o/b"] {
		t.Error("an answered repo should leave the busy latch")
	}
	if !marketStarsBusy["o/c"] {
		t.Error("another batch's repo should keep its busy latch")
	}
	delete(marketStarsBusy, "o/c")

	// a repo this batch asked for and got nothing: bad, never retried
	marketStars["o/a"], marketStars["o/b"] = 0, 0
	delete(marketStars, "o/a")
	delete(marketStars, "o/b")
	mv.Market[0].StarsKnown, mv.Market[1].StarsKnown = false, false
	marketStarsBusy["o/a"] = true
	mm, _ = mv.Update(MarketStarsMsg{Stars: map[string]int{"o/b": 1565}, Asked: []string{"o/a", "o/b"}})
	mv = mm.(Model)
	if mv.Market[0].StarsKnown {
		t.Error("a miss must not claim a count")
	}
	if !marketStarsBad["o/a"] {
		t.Error("a miss should be recorded so it is never retried")
	}
	if cmd := mv.hydrateMarketStarsCmd(mv.Dialogs[0]); cmd != nil {
		t.Error("a repo with no stars upstream should not be retried")
	}

	// a repo that genuinely has 0 stars is resolved, not a miss: presence
	// in marketStars is enough, so it is never refetched either
	delete(marketStarsBad, "o/a")
	marketStars["o/a"] = 0
	mv.Market[0].Stars, mv.Market[0].StarsKnown = 0, true
	if cmd := mv.hydrateMarketStarsCmd(mv.Dialogs[0]); cmd != nil {
		t.Error("a 0-star repo is resolved and must not be refetched")
	}
}

// Marketplace renders the 3-pane layout with install hints and a
// right-aligned star chip on the resolved rows; a pending row has no chip.
func TestMarketRender(t *testing.T) {
	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = []MarketEntry{
		{Name: "pi-new", Version: "2.0.0", Desc: "fresh",
			Repo: "o/pi-new", Stars: 1565, StarsKnown: true, Weekly: 22900},
		{Name: "pi-pending", Version: "0.1.0", Desc: "stars unknown", Repo: "o/pi-pending"},
	}
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
	for _, want := range []string{"MARKETPLACE", "DETAILS", "pi-new", "Enter: install", "★1,565", "22.9k/wk"} {
		if !strings.Contains(out, want) {
			t.Errorf("market render should show %q:\n%s", want, out)
		}
	}
	// the wide layout drops the desc column, so the chip rides the row
	if !rowHas(out, "pi-new", "★1,565") {
		t.Errorf("the resolved row should carry its star chip:\n%s", out)
	}
	// a row whose stars are not known yet renders exactly as before
	if rowHas(out, "pi-pending", "★") {
		t.Errorf("a pending row must not show a chip:\n%s", out)
	}
	if !strings.Contains(out, "pi-pending") {
		t.Errorf("the pending row should still render:\n%s", out)
	}
	if !strings.Contains(out, "Repository") || !strings.Contains(out, "https://github.com/o/pi-new") {
		t.Errorf("the detail column should show the GitHub link:\n%s", out)
	}
}

// The no-fetch sort path (every head star already resolved, page not yet
// marked sorted) must still reorder AND repaint: rows map to m.Market by
// index, so a silent reorder would leave the hub pointing at — and
// installing — the wrong package.
func TestMarketSortNoFetchPath(t *testing.T) {
	oldStars, oldBad, oldBusy := marketStars, marketStarsBad, marketStarsBusy
	oldSorted, oldMore := marketSorted, marketMore
	marketStars, marketStarsBad, marketStarsBusy = map[string]int{}, map[string]bool{}, map[string]bool{}
	marketSorted, marketMore = map[string]bool{}, false
	defer func() {
		marketStars, marketStarsBad, marketStarsBusy = oldStars, oldBad, oldBusy
		marketSorted, marketMore = oldSorted, oldMore
	}()

	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = []MarketEntry{
		{Name: "low", Repo: "o/low", Stars: 5, StarsKnown: true},
		{Name: "high", Repo: "o/high", Stars: 500, StarsKnown: true},
	}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)
	// highlight "low": it is about to move from row 0 to row 1
	d = mv.Dialogs[0]
	for fi, ri := range d.FIdx {
		if mv.Market[ri].Name == "low" {
			d.Cursor = fi
		}
	}

	if cmd := mv.marketSortCmd(""); cmd != nil {
		t.Fatal("a fully resolved head needs no fetch")
	}
	if mv.Market[0].Name != "high" {
		t.Fatalf("the no-fetch path must reorder, got %v", marketNames(mv.Market))
	}
	if !marketSorted[""] {
		t.Error("the no-fetch path must latch the page as sorted")
	}
	dd := mv.Dialogs[0]
	// rows must match the new order...
	for fi, ri := range dd.FIdx {
		if ri >= len(mv.Market) {
			t.Fatalf("row %d maps to market index %d but only %d packages are loaded",
				fi, ri, len(mv.Market))
		}
		if dd.Options[ri] != mv.Market[ri].Name {
			t.Fatalf("row %d maps to %q but m.Market[%d] is %q: rows were not rebuilt",
				fi, dd.Options[ri], ri, mv.Market[ri].Name)
		}
	}
	// ...and the highlight must still be on "low"
	ri := psecCursor(dd)
	if ri < 0 || ri >= len(mv.Market) || mv.Market[ri].Name != "low" {
		t.Fatalf("the highlight should follow its package, got index %d", ri)
	}
	// the row payload must install the package the row now shows
	if ri < len(dd.Payload) {
		if p := dd.Payload[ri]; p != "market:low" {
			t.Errorf("payload should follow the row, got %q", p)
		}
	}
}

// Background market work must never steal the hub: a page, a star batch
// or a sort that lands while the user browsed another section leaves the
// left-pane cursor exactly where they put it.
func TestMarketAsyncReloadKeepsSection(t *testing.T) {
	oldFly, oldFlyQ := marketInflight, marketInflightQuery
	oldPages, oldSorted, oldMore := marketPages, marketSorted, marketMore
	oldStars, oldBad, oldBusy := marketStars, marketStarsBad, marketStarsBusy
	marketPages, marketSorted, marketMore = map[string]marketPage{}, map[string]bool{}, false
	marketStars, marketStarsBad, marketStarsBusy = map[string]int{}, map[string]bool{}, map[string]bool{}
	defer func() {
		marketInflight, marketInflightQuery = oldFly, oldFlyQ
		marketPages, marketSorted, marketMore = oldPages, oldSorted, oldMore
		marketStars, marketStarsBad, marketStarsBusy = oldStars, oldBad, oldBusy
	}()

	// the hub sits on Skills with an EMPTY marketplace still loading
	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = nil
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecSkill {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = true
	mv.LoadPsecRows(d)
	if mv.Dialogs[0].CurPsec() != PsecSkill {
		t.Fatalf("precondition: the hub should be on Skills, got %q", mv.Dialogs[0].CurPsec())
	}
	skills := len(mv.Dialogs[0].Options)

	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"page", MarketMsg{Query: "", Entries: []MarketEntry{{Name: "pi-x", Repo: "o/x"}}}},
		{"stars", MarketStarsMsg{Stars: map[string]int{"o/x": 5}, Asked: []string{"o/x"}}},
		{"sort", MarketSortMsg{Query: "", Stars: map[string]int{"o/x": 5}, Asked: []string{"o/x"}}},
	}
	for _, c := range cases {
		mm, _ := mv.Update(c.msg)
		mv = mm.(Model)
		if got := mv.Dialogs[0].CurPsec(); got != PsecSkill {
			t.Errorf("%s landing must not move the hub off Skills, got %q", c.name, got)
		}
		if mv.Dialogs[0].ProvFocus != true {
			t.Errorf("%s landing must not change the focused pane", c.name)
		}
		if n := len(mv.Dialogs[0].Options); n != skills {
			t.Errorf("%s landing must not rebuild another section's rows (%d vs %d)", c.name, n, skills)
		}
	}
	// the data still landed, it just did not hijack the view
	if len(mv.Market) != 1 || mv.Market[0].Stars != 5 {
		t.Errorf("the background data should still be stored, got %+v", mv.Market)
	}
}

// A reload while the hub IS on the marketplace keeps the selection on the
// same package even if the rows underneath it moved.
func TestMarketReloadKeepsSelection(t *testing.T) {
	oldSorted, oldMore := marketSorted, marketMore
	oldStars, oldBad, oldBusy := marketStars, marketStarsBad, marketStarsBusy
	marketSorted, marketMore = map[string]bool{}, false
	marketStars, marketStarsBad, marketStarsBusy = map[string]int{}, map[string]bool{}, map[string]bool{}
	defer func() {
		marketSorted, marketMore = oldSorted, oldMore
		marketStars, marketStarsBad, marketStarsBusy = oldStars, oldBad, oldBusy
	}()

	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = []MarketEntry{
		{Name: "pi-a", Repo: "o/a"}, {Name: "pi-b", Repo: "o/b"}, {Name: "pi-c", Repo: "o/c"},
	}
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)
	d = mv.Dialogs[0]
	for fi, ri := range d.FIdx {
		if mv.Market[ri].Name == "pi-c" {
			d.Cursor = fi
		}
	}

	mm, _ := mv.Update(MarketStarsMsg{Stars: map[string]int{"o/c": 42}, Asked: []string{"o/c"}})
	mv = mm.(Model)
	dd := mv.Dialogs[0]
	if dd.CurPsec() != PsecMarket {
		t.Fatalf("the hub should still be on the marketplace, got %q", dd.CurPsec())
	}
	ri := psecCursor(dd)
	if ri < 0 || ri >= len(mv.Market) || mv.Market[ri].Name != "pi-c" {
		t.Errorf("the selection should stay on pi-c, got index %d", ri)
	}
	if !strings.HasPrefix(dd.Options[ri], "pi-c") {
		t.Errorf("the highlighted row should be pi-c, got %q", dd.Options[ri])
	}
}

// An empty marketplace says "loading" while a fetch is in flight and
// "no match" once it is done — the two states are not the same thing.
func TestMarketLoadingPlaceholder(t *testing.T) {
	oldFly := marketInflight
	oldMore := marketMore
	marketMore = false
	defer func() { marketInflight, marketMore = oldFly, oldMore }()

	mv := *testPconfigModel()
	mv.Market = nil
	mv.MarketErr = ""
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false

	marketInflight = true
	mv.LoadPsecRows(d)
	if len(mv.Dialogs[0].Options) != 1 || !strings.Contains(mv.Dialogs[0].Options[0], "loading") {
		t.Fatalf("an in-flight fetch should show a loading row, got %v", mv.Dialogs[0].Options)
	}
	marketInflight = false
	mv.LoadPsecRows(d)
	if got := mv.Dialogs[0].Options[0]; !strings.Contains(got, "empty market") {
		t.Errorf("a finished fetch with no results should say empty, got %q", got)
	}
}

// The remove flow has to be visible INSIDE the hub: the spinner lives in
// the rows and the confirm prompt / result live in the section header,
// because m.Status and the chat notice are both hidden behind the modal
// dialog. This is the regression test for "deleting a plugin shows
// nothing at all": StartPluginOp must rebuild the rows itself.
func TestPluginRemoveFeedbackVisible(t *testing.T) {
	mv := *testPconfigModel() // one plugin: npm:pi-lens
	mv.winW, mv.winH = 140, 40
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)

	// 1st Delete arms the gate: the hub must say so, with the key to hit
	mm, cmd := mv.Update(tea.KeyMsg{Type: tea.KeyDelete})
	mv = mm.(Model)
	if cmd != nil {
		t.Fatal("the first Delete must not start a remove")
	}
	note := mv.Dialogs[0].Message
	if !strings.Contains(note, "Delete again to remove npm:pi-lens") {
		t.Errorf("the hub should show the confirm prompt, got %q", note)
	}
	if !strings.Contains(stripANSI(mv.renderPconfigDialog(mv.Dialogs[0])), "Delete again to remove") {
		t.Errorf("the prompt must reach the rendered dialog:\n%s",
			stripANSI(mv.renderPconfigDialog(mv.Dialogs[0])))
	}

	// 2nd Delete confirms: the row spins, the header names the op
	mm, cmd = mv.Update(tea.KeyMsg{Type: tea.KeyDelete})
	mv = mm.(Model)
	if cmd == nil {
		t.Fatal("the second Delete should start the remove")
	}
	dd := mv.Dialogs[0]
	if len(dd.Options) == 0 || !strings.HasPrefix(dd.Options[0], spinFrame(0)+" ") {
		t.Errorf("the running row should carry the spinner, got %q", dd.Options)
	}
	if !strings.Contains(dd.Descs[0], "removing") {
		t.Errorf("the row desc should say removing, got %q", dd.Descs[0])
	}
	if !strings.Contains(dd.Message, "removing npm:pi-lens") {
		t.Errorf("the header should name the running remove, got %q", dd.Message)
	}
	if strings.Contains(dd.Message, "Delete again") {
		t.Errorf("the answered prompt should be gone, got %q", dd.Message)
	}

	// a tick keeps the elapsed counter and the glyph moving, in the rows
	beforeTick := dd.Options[0] // copy: the dialog is a pointer, so dd
	mm, cmd = mv.Update(PluginTickMsg{})
	mv = mm.(Model)
	if cmd == nil {
		t.Error("the spinner should keep ticking while the remove runs")
	}
	if mv.Dialogs[0].Options[0] == beforeTick {
		t.Errorf("the spinner frame should advance, still %q", mv.Dialogs[0].Options[0])
	}

	// the result lands inside the hub too (the chat notice is hidden)
	mm, _ = mv.Update(PluginChangeMsg{Action: "remove", Spec: "npm:pi-lens"})
	mv = mm.(Model)
	if got := mv.Dialogs[0].Message; !strings.Contains(got, "removed npm:pi-lens") {
		t.Errorf("the hub should report the result, got %q", got)
	}
	if strings.Contains(mv.Dialogs[0].Options[0], spinFrame(1)) {
		t.Errorf("the spinner should be gone after the op, got %q", mv.Dialogs[0].Options[0])
	}
}

// A failed remove reports inside the hub as well, with the reason.
func TestPluginRemoveFailureVisible(t *testing.T) {
	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecPlugin {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)
	_ = mv.StartPluginOp("remove", "npm:pi-lens")
	mm, _ := mv.Update(PluginChangeMsg{Action: "remove", Spec: "npm:pi-lens",
		Out: "npm ERR! 404 not found", Err: errors.New("exit status 1")})
	mv = mm.(Model)
	note := mv.Dialogs[0].Message
	if !strings.Contains(note, "failed") || !strings.Contains(note, "npm:pi-lens") {
		t.Errorf("the hub should report the failure, got %q", note)
	}
	if !strings.Contains(note, "404") {
		t.Errorf("the hub should carry pi's reason, got %q", note)
	}
}

// The detail column shows the GitHub URL the stars came from, wrapped on
// "/" so a long owner/repo path never truncates into a dead link; an
// entry without a repo shows no Repository block at all.
func TestMarketDetailRepoLink(t *testing.T) {
	d := &Dialog{Kind: "pconfig", PsecIDs: []string{PsecMarket}, ProvCursor: 0}
	mk := func(entries []MarketEntry) string {
		m := &Model{Market: entries, winW: 140, winH: 40}
		d.Options = make([]string, len(entries))
		d.FIdx = []int{}
		for i := range entries {
			d.Options[i] = entries[i].Name
			d.FIdx = append(d.FIdx, i)
		}
		d.Cursor = 0
		d.Reindex()
		return stripANSI(strings.Join(marketDetailLines(m, d, 42), "\n"))
	}
	out := mk([]MarketEntry{{Name: "pi-a", Repo: "nicobailon/pi-web-access", Desc: "d"}})
	// the URL wraps on "/" inside the 42-cell column, so the full link is
	// only whole once the lines are trimmed and joined back together
	flat := make([]string, 0, 8)
	for _, ln := range strings.Split(out, "\n") {
		flat = append(flat, strings.TrimSpace(ln))
	}
	if link := strings.Join(flat, ""); !strings.Contains(link, "https://github.com/nicobailon/pi-web-access") {
		t.Errorf("full URL should render:\n%s", out)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "github.com") && lipgloss.Width(strings.TrimSpace(ln)) > 40 {
			t.Errorf("URL line must fit the column: %q", ln)
		}
	}
	if bare := mk([]MarketEntry{{Name: "pi-b", Desc: "no repo"}}); strings.Contains(bare, "Repository") {
		t.Errorf("a repo-less entry must not render a Repository block:\n%s", bare)
	}
}

// wrapURL breaks only after "/", so every wrapped line is still the same
// link and a wide column never splits a path segment.
func TestWrapURL(t *testing.T) {
	lines := wrapURL("https://github.com/nicobailon/pi-web-access", 40)
	if len(lines) < 2 {
		t.Fatalf("a 42-cell URL must wrap at 40: %q", lines)
	}
	if got := strings.Join(lines, ""); got != "https://github.com/nicobailon/pi-web-access" {
		t.Errorf("wrapped pieces should rejoin into the URL, got %q", got)
	}
	for _, ln := range lines {
		if lipgloss.Width(ln) > 40 {
			t.Errorf("line %q exceeds 40 cells", ln)
		}
	}
	if one := wrapURL("https://github.com/o/r", 80); len(one) != 1 {
		t.Errorf("a short URL must stay on one line, got %q", one)
	}
}

// rowHas reports whether one stripped-ANSI output line carries both parts.
func rowHas(out, name, chip string) bool {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, name) && strings.Contains(ln, chip) {
			return true
		}
	}
	return false
}

// The marketplace header reads "search:" (it is a remote query); every
// other section keeps the local "filter:" label.
func TestPconfigMarketSearchHeader(t *testing.T) {
	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	d.Filter = "lens"
	d.Reindex()
	mv.LoadPsecRows(d)
	out := stripANSI(mv.renderPconfigDialog(mv.Dialogs[0]))
	if !strings.Contains(out, "search: lens▌") {
		t.Errorf("the marketplace header should say search, got:\n%s", out)
	}

	for i, id := range mv.Dialogs[0].PsecIDs {
		if id == PsecSkill {
			mv.Dialogs[0].ProvCursor = i
		}
	}
	mv.LoadPsecRows(mv.Dialogs[0])
	out = stripANSI(mv.renderPconfigDialog(mv.Dialogs[0]))
	if !strings.Contains(out, "filter: lens▌") {
		t.Errorf("other sections keep the filter label, got:\n%s", out)
	}
}

// marketRow is a fixed-width layout: the chip hugs the right edge, and a
// chip that cannot fit is dropped rather than wrapping the row.
func TestMarketRowChip(t *testing.T) {
	if got := marketRow("pi-lens", "", 20); got != "pi-lens             " {
		t.Errorf("no chip should pad the name, got %q", got)
	}
	got := marketRow("pi-lens", "★1,565", 20)
	if got != "pi-lens       ★1,565" {
		t.Errorf("chip should be right-aligned, got %q", got)
	}
	if w := lipgloss.Width(got); w != 20 {
		t.Errorf("row should be exactly 20 cells, got %d (%q)", w, got)
	}
	if got := marketRow("pi-lens", "★1,565", 5); got != "pi-l…" {
		t.Errorf("an unfittable chip should be dropped, got %q", got)
	}
}

// The default browse order becomes "most starred first" once the star
// counts land; a typed search keeps npm's relevance order.
func TestMarketSortByStars(t *testing.T) {
	oldStars, oldBad, oldBusy := marketStars, marketStarsBad, marketStarsBusy
	oldSorted, oldMore := marketSorted, marketMore
	marketStars, marketStarsBad, marketStarsBusy = map[string]int{}, map[string]bool{}, map[string]bool{}
	marketSorted, marketMore = map[string]bool{}, false
	defer func() {
		marketStars, marketStarsBad, marketStarsBusy = oldStars, oldBad, oldBusy
		marketSorted, marketMore = oldSorted, oldMore
	}()

	// resolved-high beats resolved-low, which beats the unresolved one;
	// equal counts keep registry order (stable)
	ents := []MarketEntry{
		{Name: "unresolved", Repo: "o/u"},
		{Name: "tie-a", Repo: "o/a", Stars: 10, StarsKnown: true},
		{Name: "high", Repo: "o/h", Stars: 900, StarsKnown: true},
		{Name: "tie-b", Repo: "o/b", Stars: 10, StarsKnown: true},
	}
	got := sortMarketStars(ents)
	want := []string{"high", "tie-a", "tie-b", "unresolved"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("sorted order = %v, want %v", marketNames(got), want)
		}
	}
	// the input is never mutated in place by the helper
	if ents[0].Name != "unresolved" {
		t.Error("sortMarketStars must not reorder its argument")
	}

	mv := *testPconfigModel()
	mv.winW, mv.winH = 140, 40
	mv.Market = ents
	mv.OpenPconfig()
	d := mv.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecMarket {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	mv.LoadPsecRows(d)

	// a typed search is never ordered by stars
	d.Filter = "lens"
	d.Reindex()
	if cmd := mv.marketSortCmd(mv.MarketQuery()); cmd != nil {
		t.Error("a non-empty MarketQuery must not be star-sorted")
	}
	if mv.Market[0].Name != "unresolved" {
		t.Fatalf("a non-empty MarketQuery must not reorder the list, got %v", marketNames(mv.Market))
	}
	d.Filter = ""
	d.Reindex()
	mv.LoadPsecRows(d)

	// the highlight sits on "unresolved"; after the reorder it must still
	// be on that package even though its row index changed
	d = mv.Dialogs[0]
	for fi, ri := range d.FIdx {
		if mv.Market[ri].Name == "unresolved" {
			d.Cursor = fi
		}
	}
	mm, _ := mv.Update(MarketSortMsg{Query: "",
		Stars: map[string]int{"o/u": 5000}, Asked: []string{"o/u"}})
	mv = mm.(Model)
	if mv.Market[0].Name != "unresolved" {
		t.Fatalf("5000 stars should put it on top, got %v", marketNames(mv.Market))
	}
	dd := mv.Dialogs[0]
	sel := ""
	if ri := psecCursor(dd); ri >= 0 {
		sel = mv.Market[ri].Name
	}
	if sel != "unresolved" {
		t.Errorf("the highlight should follow its package, got %q", sel)
	}
	if !marketSorted[""] {
		t.Error("the browse page should be marked sorted")
	}

	// a fully resolved, already sorted page needs no more work
	if cmd := mv.marketSortCmd(""); cmd != nil {
		t.Error("an already-sorted page must not re-scan stars")
	}

	// a fresh non-append page un-latches the flag so the next browse sorts
	marketSorted[""] = true
	mm, _ = mv.Update(MarketMsg{Entries: []MarketEntry{{Name: "fresh", Repo: "o/f"}}})
	mv = mm.(Model)
	if marketSorted[""] {
		t.Error("a new browse page must be eligible for sorting again")
	}
	// an appended page keeps the existing order
	marketSorted[""] = true
	mm, _ = mv.Update(MarketMsg{Entries: []MarketEntry{{Name: "more", Repo: "o/m"}}, Append: true})
	mv = mm.(Model)
	if !marketSorted[""] {
		t.Error("an appended page must not re-latch the sort")
	}
}

// marketNames is a test helper: the package order of a market list.
func marketNames(entries []MarketEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

// The running plugin op drives its own spinner: StartPluginOp arms the
// state, PluginTickMsg advances the frame and re-arms the timer, and
// PluginChangeMsg tears the whole thing down.
func TestPluginBusySpinner(t *testing.T) {
	mv := *testPconfigModel()
	if cmd := mv.StartPluginOp("install", "npm:pi-lens"); cmd == nil {
		t.Fatal("StartPluginOp should return the op plus a tick")
	}
	if mv.plugBusyAction != "install" || mv.plugBusySpec != "npm:pi-lens" {
		t.Fatalf("busy state not armed: %q/%q", mv.plugBusyAction, mv.plugBusySpec)
	}
	if mv.plugBusyAt.IsZero() {
		t.Error("the elapsed counter needs a start time")
	}
	if mv.plugBusyFrame != 0 {
		t.Error("the spinner should start at frame 0")
	}
	if !strings.Contains(mv.Status, "installing npm:pi-lens") {
		t.Errorf("status should name the op, got %q", mv.Status)
	}
	if got := mv.pluginBusyElapsed(); !strings.HasSuffix(got, "s") {
		t.Errorf("elapsed should read like 12s, got %q", got)
	}
	if mv.pluginBusyFrame() != spinFrames[0] {
		t.Errorf("frame 0 should be the first spinner glyph, got %q", mv.pluginBusyFrame())
	}
	act, spec := mv.PluginBusy()
	if act != "install" || spec != "npm:pi-lens" {
		t.Errorf("PluginBusy should report the running op, got %q/%q", act, spec)
	}
	if lbl := mv.pluginBusyLabel(act, spec); !strings.Contains(lbl, "installing npm:pi-lens") {
		t.Errorf("label should name the op, got %q", lbl)
	}
	if d := mv.pluginBusyRowDesc(); !strings.HasPrefix(d, "installing") {
		t.Errorf("row desc should be a gerund, got %q", d)
	}

	// a tick advances the frame and keeps the clock alive
	mm, cmd := mv.Update(PluginTickMsg{})
	mv = mm.(Model)
	if cmd == nil {
		t.Error("a tick while busy should re-arm the timer")
	}
	if mv.plugBusyFrame != 1 {
		t.Errorf("the tick should advance the frame, got %d", mv.plugBusyFrame)
	}

	// the op landing clears all four fields and marks the model ready
	mm, _ = mv.Update(PluginChangeMsg{Action: "install", Spec: "npm:pi-lens"})
	mv = mm.(Model)
	if mv.plugBusyAction != "" || mv.plugBusySpec != "" ||
		!mv.plugBusyAt.IsZero() || mv.plugBusyFrame != 0 {
		t.Errorf("PluginChangeMsg should clear the busy state, got %q/%q/%v/%d",
			mv.plugBusyAction, mv.plugBusySpec, mv.plugBusyAt, mv.plugBusyFrame)
	}
	// the success path sets "ready" and then hands the status to the
	// respawn; what matters is that the spinner message is gone
	if strings.Contains(mv.Status, "installing npm:pi-lens") {
		t.Errorf("status should no longer show the op, got %q", mv.Status)
	}
	if mv.Status == "" {
		t.Error("status should be repainted after the op")
	}
	// the chain keeps ticking while the result note is still on screen,
	// and stops once its window lapses
	if mv.plugNote == "" {
		t.Error("a finished op should leave a result note in the hub")
	}
	mv.plugNoteHide = time.Now().Add(-time.Second) // window lapsed
	mm, cmd = mv.Update(PluginTickMsg{})
	mv = mm.(Model) // Update takes a value receiver: keep the result
	if cmd != nil {
		t.Error("a tick past the note window must not re-arm")
	}
	if mv.plugNote != "" {
		t.Errorf("the lapsed note should be cleared, got %q", mv.plugNote)
	}
	// the error path clears it too
	_ = mv.StartPluginOp("remove", "npm:pi-lens")
	mm, _ = mv.Update(PluginChangeMsg{Action: "remove", Spec: "npm:pi-lens", Err: errors.New("boom")})
	mv = mm.(Model)
	if mv.plugBusyAction != "" || mv.Status != "ready" {
		t.Errorf("a failed op must clear the state and set ready, got %q/%q",
			mv.plugBusyAction, mv.Status)
	}
}

// A running op paints the spinner on its row, in the section header and
// in DETAILS.
func TestPluginBusyRender(t *testing.T) {
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

	_ = mv.StartPluginOp("install", "npm:pi-new") // timer cmd never run
	mv.plugBusyFrame = 2                          // pin the glyph: ⠹

	// narrow layout: the row carries the spinner and the live elapsed
	mv.winW = 110 // boxW < 110 → no DETAILS column, the desc shows
	mv.LoadPsecRows(mv.Dialogs[0])
	out := stripANSI(mv.renderPconfigDialog(mv.Dialogs[0]))
	if !rowHas(out, "⠹ pi-new", "installing…") {
		t.Errorf("the busy row should show the spinner and its elapsed time:\n%s", out)
	}
	if !strings.Contains(out, "installing npm:pi-new") {
		t.Errorf("the header should name the running op:\n%s", out)
	}

	// wide layout: the row keeps the glyph, DETAILS gets the live Status
	mv.winW = 140
	mv.LoadPsecRows(mv.Dialogs[0])
	out = stripANSI(mv.renderPconfigDialog(mv.Dialogs[0]))
	if !strings.Contains(out, "⠹ pi-new") {
		t.Errorf("the busy row should lead with the spinner glyph:\n%s", out)
	}
	if !rowHas(out, "Status", "⠹ installing…") {
		t.Errorf("DETAILS should show the live status:\n%s", out)
	}

	// a remove paints "removing…" instead
	mv.plugBusyAction, mv.plugBusyFrame = "remove", 3
	mv.winW = 110
	mv.LoadPsecRows(mv.Dialogs[0])
	out = stripANSI(mv.renderPconfigDialog(mv.Dialogs[0]))
	if !rowHas(out, "⠸ pi-new", "removing…") {
		t.Errorf("a removal should read removing…:\n%s", out)
	}
	if !strings.Contains(out, "removing npm:pi-new") {
		t.Errorf("the header should follow the verb:\n%s", out)
	}
	mv.plugBusyAction, mv.plugBusySpec, mv.plugBusyFrame = "", "", 0
}

// The remove path must not fire a second `pi remove` while one runs.
func TestPluginBusyGate(t *testing.T) {
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
	mv := mkHub()
	_ = mv.StartPluginOp("install", "npm:pi-other") // an install is running
	before := mv.Status

	mm, cmd := mv.Update(tea.KeyMsg{Type: tea.KeyDelete})
	mv = mm.(Model)
	if cmd != nil {
		t.Error("Delete while an op is running must not issue a cmd")
	}
	if mv.plugBusyAction != "install" || mv.plugBusySpec != "npm:pi-other" {
		t.Errorf("the running op must be left alone, got %q/%q",
			mv.plugBusyAction, mv.plugBusySpec)
	}
	if !strings.Contains(mv.Status, "wait for it") {
		t.Errorf("the status should tell the user to wait, got %q", mv.Status)
	}
	if mv.Status == before {
		t.Error("the status should change, not stay silent")
	}
	if mv.plugAction != "" {
		t.Error("the gate must not even arm the confirm on a busy model")
	}

	// the refusal must name the op that is ACTUALLY running: a remove in
	// flight must never read "installing"
	mv = mkHub()
	_ = mv.StartPluginOp("remove", "npm:pi-other")
	mm, cmd = mv.Update(tea.KeyMsg{Type: tea.KeyDelete})
	mv = mm.(Model)
	if cmd != nil {
		t.Error("Delete while a remove runs must not issue a cmd")
	}
	if !strings.Contains(mv.Status, "removing") {
		t.Errorf("the status must name the running remove, got %q", mv.Status)
	}
	if strings.Contains(mv.Status, "installing") {
		t.Errorf("a running remove must not read as installing: %q", mv.Status)
	}
	if got, want := mv.PluginBusyMsg(), mv.Status; got != want {
		t.Errorf("the gate status should be PluginBusyMsg: %q vs %q", got, want)
	}
}
