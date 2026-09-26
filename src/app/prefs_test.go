package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	terminal_image "pitago/src/components/terminal_image"
	"pitago/src/pirpc"
)

func TestPrefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := SavePrefs(path, Prefs{HideThinking: true, AutocompleteMax: 5,
		Side: map[string]bool{"mcp": true, "plugins": false}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p := LoadPrefs(path)
	if !p.HideThinking || p.AutocompleteMax != 5 {
		t.Errorf("round trip failed: %+v", p)
	}
	if !p.SideVisible("mcp") || p.SideVisible("plugins") {
		t.Errorf("side round trip failed: %+v", p.Side)
	}
	if got := (Prefs{}).EffectiveAutocompleteMax(); got != 10 {
		t.Errorf("unset max should be pitago default 10, got %d", got)
	}
	if got := (Prefs{AutocompleteMax: 99}).EffectiveAutocompleteMax(); got != 10 {
		t.Errorf("out-of-range max should clamp to 10, got %d", got)
	}
}

func TestSideDefaults(t *testing.T) {
	var p Prefs // nothing ever toggled: MCP + Plugins + Commands hide, the rest show
	for _, k := range []string{"mcp", "plugins", "commands"} {
		if p.SideVisible(k) {
			t.Errorf("%s should hide by default", k)
		}
	}
	for _, k := range []string{"pet", "session", "model", "stats", "cost", "recent", "todos", "workspace"} {
		if !p.SideVisible(k) {
			t.Errorf("%s should show by default", k)
		}
	}
}

func TestImageSettingsLoadAndInvalidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Dir(path))
	if err := pirpc.SetPiSettingAt(path, "terminal.showImages", false); err != nil {
		t.Fatal(err)
	}
	if err := pirpc.SetPiSettingAt(path, "terminal.imageWidthCells", 80); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "kitty")
	m := Model{renderCache: []string{"old"}, renderCacheKey: []uint64{1}}
	m.ApplyImageSettings()
	if m.ShowImages || m.ImageWidthCells != 80 || m.ImageProtocol != terminal_image.Kitty {
		t.Fatalf("settings = %+v", m)
	}
	if m.renderCache != nil || m.renderCacheKey != nil {
		t.Fatal("image settings must invalidate rendered image cache")
	}
}

func TestHideThinkingSkipsBlocks(t *testing.T) {
	m := &Model{}
	m.AddBlock(Block{Kind: "thinking", Text: "hmm"})
	m.AddBlock(Block{Kind: "assistant", Text: "done"})
	if got := m.renderBlocks(); !strings.Contains(got, "hmm") {
		t.Fatal("thinking should render by default")
	}
	m.HideThinking = true
	got := m.renderBlocks()
	if strings.Contains(got, "hmm") {
		t.Error("thinking should be hidden")
	}
	if !strings.Contains(got, "done") {
		t.Error("assistant text must still render")
	}
}

func TestSettingsMsgPassesDialog(t *testing.T) {
	m := &Model{}
	m.Dialogs = []*Dialog{{Kind: "settings", Title: "Agent settings",
		Options: []string{"Steering: one-at-a-time"}, Descs: []string{"x"}}}
	mm, _ := m.Update(SettingsMsg{Opts: []string{"Steering: all"}, Descs: []string{"y"}})
	if got := mm.(Model).Dialogs[0].Options[0]; got != "Steering: all" {
		t.Errorf("settings rows should refresh behind the dialog, got %q", got)
	}
}

// Typing in /settings filters rows by label, description, or section.
func TestSettingsFilter(t *testing.T) {
	d := &Dialog{Kind: "settings", Title: "Agent settings",
		Options:   []string{"Model: m", "Theme: default", "Transport: auto"},
		Descs:     []string{"Enter: open model picker", "Enter: open theme picker", "Enter: next · reconnects pi"},
		Providers: []string{"Agent", "Display", "Network"},
	}
	d.Reindex()
	if len(d.FIdx) != 3 {
		t.Fatalf("unfiltered must show all rows, got %d", len(d.FIdx))
	}
	d.Filter = "network"
	d.Reindex()
	if len(d.FIdx) != 1 || d.Options[d.FIdx[0]] != "Transport: auto" {
		t.Fatalf("filter by section must match transport, got %v", d.FIdx)
	}
	d.Filter = "theme"
	d.Reindex()
	if len(d.FIdx) != 1 || d.Options[d.FIdx[0]] != "Theme: default" {
		t.Fatalf("filter by label must match theme, got %v", d.FIdx)
	}
	if !isFilterKind("settings") {
		t.Error("settings must be a filter kind so typing filters")
	}
}

func settingsTwoPaneDialog() *Dialog {
	d := &Dialog{Kind: "settings", Title: "Agent settings",
		Options: []string{"Model: m", "Steering: all", "Theme: default", "Transport: auto", "Quiet startup: off"},
		Descs: []string{"Enter: open model picker", "Enter: switch all/one-at-a-time",
			"Enter: open theme picker", "Enter: next · reconnects pi", "Enter: toggle · reconnects pi"},
		Providers:  []string{"Agent", "Agent", "Display", "Network", "Display"},
		Provs:      []string{"Agent", "Display", "Network"},
		ProvCursor: 0, ProvFocus: true,
	}
	d.Reindex()
	return d
}

// Empty filter scopes the right pane to the selected group; typing on the
// left searches globally, typing on the right stays scoped.
func TestSettingsTwoPaneScopesGroup(t *testing.T) {
	d := settingsTwoPaneDialog()
	if len(d.FIdx) != 2 {
		t.Fatalf("Agent group must show 2 rows, got %v", d.FIdx)
	}
	d.ProvCursor = 1 // Display
	d.Reindex()
	if len(d.FIdx) != 2 || d.Options[d.FIdx[0]] != "Theme: default" {
		t.Fatalf("Display group must show theme + quiet, got %v", d.FIdx)
	}
	d.Filter = "transport" // left pane: global search
	d.Reindex()
	if len(d.FIdx) != 1 || d.Options[d.FIdx[0]] != "Transport: auto" {
		t.Fatalf("global filter must match transport, got %v", d.FIdx)
	}
	d.Filter = "theme" // right pane: scoped to Display
	d.ProvFocus = false
	d.Reindex()
	if len(d.FIdx) != 1 {
		t.Fatalf("scoped filter must match theme in Display, got %v", d.FIdx)
	}
	d.ProvCursor = 0 // right pane scoped to Agent: no theme there
	d.Reindex()
	if len(d.FIdx) != 0 {
		t.Fatalf("scoped filter must miss outside the group, got %v", d.FIdx)
	}
}

// ←/→/Tab moves between panes, typing filters, Enter on a group dives right.
func TestSettingsTwoPaneKeys(t *testing.T) {
	m := Model{}
	m.Dialogs = []*Dialog{settingsTwoPaneDialog()}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRight})
	m = um.(Model)
	if m.Dialogs[0].ProvFocus {
		t.Fatal("→ must dive into the rows pane")
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyDown})
	m = um.(Model)
	if m.Dialogs[0].Cursor != 1 {
		t.Fatalf("↓ must move the row cursor, got %d", m.Dialogs[0].Cursor)
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyLeft})
	m = um.(Model)
	if !m.Dialogs[0].ProvFocus {
		t.Fatal("← must return to the groups pane")
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("quiet")})
	m = um.(Model)
	if len(m.Dialogs[0].FIdx) != 1 {
		t.Fatalf("typing on the left must search globally, got %v", m.Dialogs[0].FIdx)
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if m.Dialogs[0].ProvFocus {
		t.Fatal("Enter on a group must dive into the rows pane")
	}
}

// SettingsMsg opens a two-pane dialog; refresh keeps the group cursor by
// name and preserves the typed filter.
func TestSettingsMsgBuildsTwoPane(t *testing.T) {
	opts := []string{"Model: m", "Theme: default", "Transport: auto"}
	descs := []string{"x", "y", "z"}
	cats := []string{"Agent", "Display", "Network"}
	m := &Model{}
	mm, _ := m.Update(SettingsMsg{Opts: opts, Descs: descs, Cats: cats})
	d := mm.(Model).Dialogs[0]
	if len(d.Provs) != 4 || d.Provs[0] != "All" || d.Provs[1] != "Agent" || !d.ProvFocus {
		t.Fatalf("must open two-pane on groups with All on top, got provs=%v focus=%v", d.Provs, d.ProvFocus)
	}
	if len(d.FIdx) != 3 {
		t.Fatalf("All must show every row, got %v", d.FIdx)
	}
	d.ProvCursor = 1 // Agent
	d.Reindex()
	if len(d.FIdx) != 1 || d.Options[d.FIdx[0]] != "Model: m" {
		t.Fatalf("right pane must scope to Agent, got %v", d.FIdx)
	}
	// seeded filter lands on the matches' group
	m2 := &Model{}
	mm2, _ := m2.Update(SettingsMsg{Opts: opts, Descs: descs, Cats: cats, Filter: "transport"})
	d2 := mm2.(Model).Dialogs[0]
	if d2.ProvFocus || len(d2.FIdx) != 1 || d2.Provs[d2.ProvCursor] != "Network" {
		t.Fatalf("seeded filter must focus Network matches, got focus=%v cursor=%v fidx=%v",
			d2.ProvFocus, d2.Provs[d2.ProvCursor], d2.FIdx)
	}
	// refresh preserves group + filter
	m3 := mm.(Model)
	m3.Dialogs[0].ProvCursor = 3
	m3.Dialogs[0].Filter = "auto"
	mm3, _ := m3.Update(SettingsMsg{Opts: opts, Descs: descs, Cats: cats})
	d3 := mm3.(Model).Dialogs[0]
	if d3.Provs[d3.ProvCursor] != "Network" || d3.Filter != "auto" {
		t.Fatalf("refresh must keep group + filter, got %v/%q", d3.Provs, d3.Filter)
	}
}

// Two-pane settings renders GROUPS + scoped rows without panicking.
func TestSettingsTwoPaneRenders(t *testing.T) {
	m := Model{}
	m.winW, m.winH = 120, 30
	m.Dialogs = []*Dialog{settingsTwoPaneDialog()}
	out := stripANSI(m.renderSettingsDialog(m.Dialogs[0]))
	for _, want := range []string{"GROUPS", "Agent", "Model", "Steering"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "Transport") {
		t.Errorf("Agent scope must hide Network rows\n%s", out)
	}
	// No wrapped cells: each group count stays on its group's line and
	// each row keeps its value (a pane overflow used to drop the count
	// onto its own line).
	lines := strings.Split(out, "\n")
	sameLine := func(name, want string) {
		t.Helper()
		found := false
		for _, ln := range lines {
			if strings.Contains(ln, name) && !strings.Contains(ln, "·") {
				found = true
				if !strings.Contains(ln, want) {
					t.Errorf("%q line lost %q (wrapped?)\n%s", name, want, out)
				}
			}
		}
		if !found {
			t.Errorf("no line for %q\n%s", name, out)
		}
	}
	sameLine("Network", "1")
	sameLine("Display", "2")
	sameLine("Steering", "all")

	// "All" on top shows every row with the total count on its line.
	dAll := settingsTwoPaneDialog()
	dAll.Provs = append([]string{"All"}, dAll.Provs...)
	dAll.ProvCursor = 0
	dAll.Reindex()
	if len(dAll.FIdx) != len(dAll.Options) {
		t.Fatalf("All must show every row, got %v", dAll.FIdx)
	}
	outAll := stripANSI(m.renderSettingsDialog(dAll))
	found := false
	for _, ln := range strings.Split(outAll, "\n") {
		if strings.Contains(ln, "All") && !strings.Contains(ln, "·") {
			found = true
			if !strings.Contains(ln, "5") {
				t.Errorf("All line lost its total (wrapped?)\n%s", outAll)
			}
		}
	}
	if !found {
		t.Errorf("no All line\n%s", outAll)
	}
}

// The thinking level stays pi's (settings.json defaultThinkingLevel): pitago
// must keep no copy of it. The model may be saved — but only under the
// nested currentModel key, never as the old flat modelProvider/modelID
// shadow, and never as a mid-session value the footer renders.
func TestPrefsHoldNoModelShadow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := SavePrefs(path, Prefs{HideThinking: true}); err != nil {
		t.Fatal(err)
	}
	m := New(nil, t.TempDir())
	m.prefsPath = path
	m.recentPath = filepath.Join(t.TempDir(), "recent.json")
	um, _ := m.Update(ModelCycleMsg{Label: "space-bunny-free", Provider: "opencode", ID: "space-bunny-free"})
	m = um.(Model)
	if m.ModelLbl != "space-bunny-free" {
		t.Fatalf("ModelLbl = %q, want the switched label", m.ModelLbl)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("prefs not written at all: %v", err)
	}
	for _, key := range []string{"modelProvider", "modelID", "thinkingLevel"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("prefs.json must not shadow %s: %s", key, raw)
		}
	}
}

// A legacy prefs.json (written before the model copy was removed) loads with
// the model keys ignored, and the next save prunes them.
func TestPrefsIgnoreLegacyModelKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	legacy := `{"modelProvider":"opencode","modelID":"space-bunny-free","hideThinking":true}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs(path)
	if !p.HideThinking {
		t.Error("unrelated prefs must still load")
	}
	if err := SavePrefs(path, p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "model") {
		t.Errorf("legacy model keys must be pruned on the next save: %s", raw)
	}
}

// /new must not re-apply a remembered model: pi's NewSession resets to its
// own default and the label comes back from get_state.
func TestNewSessionDoesNotRestoreModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, []byte(`{"modelProvider":"opencode","modelID":"space-bunny-free"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(nil, t.TempDir())
	m.prefsPath = path
	m.ModelLbl = "ollama/deepseek-coder:1.3b" // what pi's get_state reported
	um, _ := m.Update(SessionResetMsg{})
	if got := um.(Model).ModelLbl; got != "ollama/deepseek-coder:1.3b" {
		t.Errorf("after /new ModelLbl = %q, want pi's own default unchanged", got)
	}
	if um.(Model).Status == "ready — restoring model…" {
		t.Error("/new must not announce a pitago-side model restore")
	}
}

// The last user-picked model round-trips through prefs.json under one
// nested key (currentModel) and is readable back through LoadPrefs.
func TestSetCurrentModelRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := SavePrefs(path, Prefs{HideThinking: true}); err != nil {
		t.Fatal(err)
	}
	m := New(nil, t.TempDir())
	m.prefsPath = path
	m.SetCurrentModel("opencode", "space-bunny-free")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("prefs not written: %v", err)
	}
	if !strings.Contains(string(raw), `"currentModel"`) {
		t.Fatalf("currentModel missing from prefs.json: %s", raw)
	}
	p := LoadPrefs(path)
	if p.CurrentModel == nil {
		t.Fatal("currentModel did not load back")
	}
	if p.CurrentModel.Provider != "opencode" || p.CurrentModel.ID != "space-bunny-free" {
		t.Errorf("CurrentModel = %+v, want opencode/space-bunny-free", *p.CurrentModel)
	}
	if !p.HideThinking {
		t.Error("unrelated prefs must survive a model save")
	}
	if got := m.CurrentModelRef(); got == nil || got.ID != "space-bunny-free" {
		t.Errorf("CurrentModelRef() = %+v, want the saved model", got)
	}
}

// A ModelCycleMsg (only ever produced by an explicit user pick) persists
// the model so the next process start can restore it.
func TestModelCycleMsgPersistsModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	m := New(nil, t.TempDir())
	m.prefsPath = path
	m.recentPath = filepath.Join(t.TempDir(), "recent.json")
	um, _ := m.Update(ModelCycleMsg{Label: "space-bunny-free", Provider: "opencode", ID: "space-bunny-free"})
	_ = um.(Model)
	p := LoadPrefs(path)
	if p.CurrentModel == nil {
		t.Fatal("ModelCycleMsg did not persist the picked model")
	}
	if p.CurrentModel.Provider != "opencode" || p.CurrentModel.ID != "space-bunny-free" {
		t.Errorf("CurrentModel = %+v, want opencode/space-bunny-free", *p.CurrentModel)
	}
}

// A prefs.json with no currentModel (fresh install, or a legacy file) must
// not invent a restore.
func TestNoSavedModelNoRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, []byte(`{"hideThinking":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := LoadPrefs(path); p.CurrentModel != nil {
		t.Errorf("CurrentModel = %+v, want nil for prefs without the key", p.CurrentModel)
	}
}
