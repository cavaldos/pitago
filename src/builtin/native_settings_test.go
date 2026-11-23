package builtin

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/components/theme"
	"pitago/src/pirpc"
	"pitago/src/tui/ev"
)

func TestNativeSettingsApplyPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Cleanup(func() { app.ApplyTheme(theme.Get("default")) })
	m := app.New(nil, home)
	m.Configure(pirpc.Options{}, "")
	m.UseBuiltins(All(), Confirmers())
	var state ev.Model = m
	send := func(msg ev.Msg) ev.Cmd {
		var cmd ev.Cmd
		state, cmd = state.Update(msg)
		return cmd
	}
	view := func() ev.SettingsView {
		t.Helper()
		v, ok := state.(ev.SettingsProvider).NativeSettings()
		if !ok {
			t.Fatal("settings disappeared")
		}
		return v
	}
	pick := func(col ev.SettingsColumn, label string, apply bool) ev.Cmd {
		t.Helper()
		v := view()
		pos := slices.IndexFunc(v.Panes[col].Rows, func(r ev.SettingsRow) bool { return strings.HasPrefix(r.Label, label) })
		if pos < 0 {
			t.Fatalf("%s not in %+v", label, v.Panes[col])
		}
		return send(ev.SettingsPointerMsg{Column: col, Index: v.Panes[col].Rows[pos].Index, Activate: apply})
	}

	st := app.SettingsState{Theme: "default", Vals: fileSettingVals(nil)}
	opts, descs, cats := settingsOptions(st)
	send(app.SettingsMsg{St: st, Opts: opts, Descs: descs, Cats: cats})
	send(ev.SettingsFilterMsg("Hide thinking"))
	if got := view().Panes[ev.SettingsValues]; got.Title != "Matches across sections" || len(got.Rows) != 1 {
		t.Fatalf("expected a single global search result, got %+v", got)
	}
	cmd := pick(ev.SettingsValues, "Hide thinking: off", true)
	if cmd == nil {
		t.Fatal("a real settings toggle must write prefs")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("settings save failed: %+v", msg)
	}
	if !app.LoadPrefs(app.PrefsPath()).HideThinking || !pirpc.PiBool(pirpc.ReadPiSettings(), "hideThinkingBlock", false) {
		t.Fatal("agent settings toggle did not reach both existing config stores")
	}
	if v := view(); v.Kind != "settings" || v.Panes[ev.SettingsValues].Rows[0].Label != "Hide thinking: on" {
		t.Fatalf("settings did not stay open with the updated row: %+v", v)
	}
	send(ev.SettingsFilterMsg("Theme"))
	pick(ev.SettingsSections, "All", false)
	pick(ev.SettingsValues, "Theme: default", true)
	if v := view(); v.Kind != "pconfig" || len(v.Panes) != 3 || v.Panes[ev.SettingsBackgrounds].Title != "BACKGROUND" {
		t.Fatalf("agent theme row did not open Pitago Theme: %+v", v)
	}
	pick(ev.SettingsValues, "02 one-dark", false)
	if p := state.(ev.NativePaletteProvider).NativePalette(); p.Accent != theme.Get("one-dark").Accent {
		t.Fatalf("theme preview is not exposed to the menu: %+v", p)
	}
	pick(ev.SettingsBackgrounds, "opencode", false)
	if bg := app.LoadPrefs(app.PrefsPath()).Background; bg != "" {
		t.Fatalf("background preview wrote prefs before Apply: %q", bg)
	}
	pick(ev.SettingsBackgrounds, "✓ opencode", true)
	if bg := app.LoadPrefs(app.PrefsPath()).Background; bg != "opencode" {
		t.Fatalf("background Apply did not persist: %q", bg)
	}
}

// The native hub shows each row's hint beside its label, not only the
// highlighted row's Details line: Commands, Shortcuts and Plugins lost that
// text when the painted description column went away.
func TestNativeSettingsHubRowHints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Cleanup(func() { app.ApplyTheme(theme.Get("default")) })
	m := app.New(nil, home)
	m.Configure(pirpc.Options{}, "")
	m.UseBuiltins(All(), Confirmers())
	m.OpenPconfig()
	var state ev.Model = m
	for _, tc := range []struct {
		section, wantDesc string
	}{
		{"Commands", "[pi"},
		{"Shortcuts", "[Global"},
		{"Plugins", "suggested"},
	} {
		t.Run(tc.section, func(t *testing.T) {
			v, ok := state.(ev.SettingsProvider).NativeSettings()
			if !ok {
				t.Fatal("settings hub disappeared")
			}
			pos := slices.IndexFunc(v.Panes[ev.SettingsSections].Rows,
				func(r ev.SettingsRow) bool { return strings.HasPrefix(r.Label, tc.section) })
			if pos < 0 {
				t.Fatalf("%s not in the sections pane: %+v", tc.section, v.Panes[ev.SettingsSections])
			}
			state, _ = state.Update(ev.SettingsPointerMsg{
				Column: ev.SettingsSections, Index: v.Panes[ev.SettingsSections].Rows[pos].Index})
			v, _ = state.(ev.SettingsProvider).NativeSettings()
			rows := v.Panes[ev.SettingsValues].Rows
			if len(rows) == 0 {
				t.Fatalf("%s has no rows", tc.section)
			}
			for _, r := range rows {
				if r.Description == "" {
					t.Fatalf("%s row %q renders without its hint", tc.section, r.Label)
				}
			}
			if !strings.Contains(rows[0].Description, tc.wantDesc) {
				t.Fatalf("%s hint = %q, want %q", tc.section, rows[0].Description, tc.wantDesc)
			}
		})
	}
}
