package tviewrt

import (
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
	"pitago/src/tui/keys"
)

type settingsTestModel struct {
	barModel
	kind, filter string
	pointer      ev.SettingsPointerMsg
	palette      ev.NativePalette
}

type testPaletteMsg ev.NativePalette

func (m settingsTestModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	switch x := msg.(type) {
	case ev.SettingsPointerMsg:
		m.pointer = x
	case ev.SettingsFilterMsg:
		m.filter = string(x)
	case ev.KeyMsg:
		if x.Type == ev.KeyEsc {
			m.kind = ""
		}
	case testPaletteMsg:
		m.palette = ev.NativePalette(x)
	}
	return m, nil
}

func (m settingsTestModel) NativePalette() ev.NativePalette { return m.palette }

func (m settingsTestModel) NativeSettings() (ev.SettingsView, bool) {
	if m.kind == "" {
		return ev.SettingsView{}, false
	}
	v := ev.SettingsView{Kind: m.kind, Title: m.kind, Filter: m.filter, FilterLabel: "Filter: ", Panes: []ev.SettingsPane{
		{Title: "Sections", Rows: []ev.SettingsRow{{Index: 2, Label: "Display"}}, Selected: 2},
		{Title: "Options", Rows: []ev.SettingsRow{{Index: 8, Label: "Current theme"}, {Index: 42, Label: "Try theme"}}, Selected: 8},
	}}
	if m.kind == "Pitago settings" {
		v.Panes = append(v.Panes, ev.SettingsPane{Title: "Background", Rows: []ev.SettingsRow{{Index: 60, Label: "Transparent"}}})
	}
	return v, true
}

func TestSettingsPanelInput(t *testing.T) {
	for _, title := range []string{"Agent settings", "Pitago settings"} {
		t.Run(title, func(t *testing.T) {
			scr := tcell.NewSimulationScreen("UTF-8")
			r := New(settingsTestModel{kind: title}, Options{Screen: scr, Mouse: true, Key: keys.FromKey})
			scr.SetSize(80, 22)
			go func() { _, _ = r.Run() }()
			defer r.Stop()
			find := func(label string) (x, y int) {
				waitFor(t, func() bool {
					for row, text := range rows(r, scr) {
						if i := strings.Index(text, label); i >= 0 {
							x, y = len([]rune(text[:i])), row
							return true
						}
					}
					return false
				})
				return
			}
			find(title)
			click := func(x, y int) {
				scr.InjectMouse(x, y, tcell.ButtonPrimary, tcell.ModNone)
				scr.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)
			}
			if title == "Pitago settings" {
				x, y := find("Transparent")
				scr.InjectMouse(x, y, tcell.WheelDown, tcell.ModNone)
				waitFor(t, func() bool {
					return r.Model().(settingsTestModel).pointer == (ev.SettingsPointerMsg{Column: ev.SettingsBackgrounds, Step: 1})
				})
			}
			x, y := find("Try theme")
			click(x, y)
			want := ev.SettingsPointerMsg{Column: ev.SettingsValues, Index: 42}
			waitFor(t, func() bool { return r.Model().(settingsTestModel).pointer == want })
			click(x, y)
			want.Activate = true
			waitFor(t, func() bool { return r.Model().(settingsTestModel).pointer == want })
			scr.InjectKey(tcell.KeyRune, 'ế', tcell.ModNone)
			waitFor(t, func() bool { return r.Model().(settingsTestModel).filter == "ế" })
			scr.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
			waitFor(t, func() bool { return r.Model().(settingsTestModel).filter == "" })
			x, y = find("Close")
			click(x, y)
			waitFor(t, func() bool { return r.Model().(settingsTestModel).kind == "" })
		})
	}
}

func TestMenuAndSettingsFollowTheme(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	p := ev.NativePalette{Text: "252", HiFg: "15", HiBg: "238", Border: "240", Accent: "15"}
	r := New(settingsTestModel{kind: "Agent settings", palette: p}, Options{Screen: scr, Key: keys.FromKey})
	scr.SetSize(80, 22)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	check := func(p ev.NativePalette) {
		t.Helper()
		cells, w, _ := snap(r, scr)
		fg, bg, _ := cells[1].Style.Decompose() // 'F' on the menu bar
		border, panelBG, _ := cells[w].Style.Decompose()
		if fg != themeColor(p.Text) || bg != themeColor(p.Surface) || border != themeColor(p.Border) || panelBG != bg {
			t.Fatalf("bar %v/%v, panel border %v/%v for %+v", fg, bg, border, panelBG, p)
		}
	}
	check(p)
	scr.InjectKey(tcell.KeyF10, 0, tcell.ModNone)
	waitFor(t, func() bool { return strings.Contains(rows(r, scr)[2], "New") })
	p = ev.NativePalette{Surface: "#eff1f5", Text: "#4c4f69", Accent: "#8839ef", Border: "#ccd0da", Muted: "#9ca0b0", HiBg: "#bcc0cc", HiFg: "#4c4f69"}
	r.Send(testPaletteMsg(p))
	cells, w, _ := snap(r, scr)
	fg, bg, _ := cells[2*w+2].Style.Decompose() // highlighted dropdown row
	if fg != themeColor(p.HiFg) || bg != themeColor(p.HiBg) {
		t.Fatalf("dropdown kept old selected colors: %v/%v", fg, bg)
	}
	scr.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, func() bool { return !strings.Contains(rows(r, scr)[2], "New") })
	check(p)
}

// The hub shows each row's hint in a column beside its label, like the
// painted UI's description column did.
func TestSettingsRowLabelHintColumn(t *testing.T) {
	rows := []ev.SettingsRow{
		{Index: 0, Label: "/model", Description: "<provider/model> — Select model"},
		{Index: 1, Label: "/scoped-models", Description: "pi TUI-only (no RPC equivalent) [pi]"},
	}
	keyW := settingsKeyWidth(rows, 60)
	if keyW != settingsKeyCap {
		t.Fatalf("key column = %d, want the %d cap", keyW, settingsKeyCap)
	}
	a, b := settingsRowLabel(rows[0], 60, keyW), settingsRowLabel(rows[1], 60, keyW)
	if at, bt := strings.Index(a, "— "), strings.Index(b, "— "); at < 0 || at != bt {
		t.Fatalf("hints not in one column: %q vs %q", a, b)
	}
	if !strings.Contains(b, "/scoped-models") {
		t.Fatalf("label lost in %q", b)
	}
	if got := settingsKeyWidth([]ev.SettingsRow{{Label: "/model"}}, 60); got != 0 {
		t.Fatalf("no descriptions must not reserve a hint column, got %d", got)
	}
	if got := settingsKeyWidth(rows, 20); got != 10 {
		t.Fatalf("a narrow pane halves the column, got %d", got)
	}
	// A long package name keeps its text and starts its hint later instead
	// of pushing every other row's hint out.
	long := ev.SettingsRow{Label: "★ @dietrichgebert/ponytail", Description: "suggested · lazy-senior mode"}
	got := settingsRowLabel(long, 40, keyW)
	if !strings.Contains(got, "@dietrichgebert/ponytail") {
		t.Fatalf("long label truncated: %q", got)
	}
	if strings.Index(got, "— ") <= strings.Index(b, "— ") {
		t.Fatalf("outlier hint should start later, got %q", got)
	}
	// Suffix (the model picker's star) still wins over a description.
	if got := settingsRowLabel(ev.SettingsRow{Label: "gpt-5", Suffix: "★", Description: "x"}, 40, keyW); !strings.HasSuffix(got, "★") {
		t.Fatalf("suffix lost: %q", got)
	}
}

// bareSettingsModel is the /model picker's snapshot: one bare frame with a
// providers column, the scoped model list and the specs.
type bareSettingsModel struct {
	barModel
	ptrBox
}

// ptrBox records the last pointer message. The runtime writes it from its
// own goroutine while the test polls it, so access is locked.
type ptrBox struct {
	mu      sync.Mutex
	pointer ev.SettingsPointerMsg
}

func (b *ptrBox) set(p ev.SettingsPointerMsg) {
	b.mu.Lock()
	b.pointer = p
	b.mu.Unlock()
}

func (b *ptrBox) lastPointer() ev.SettingsPointerMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pointer
}

func (m *bareSettingsModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if p, ok := msg.(ev.SettingsPointerMsg); ok {
		m.set(p)
	}
	return m, nil
}

func (m *bareSettingsModel) NativeSettings() (ev.SettingsView, bool) {
	return ev.SettingsView{
		Kind: "model", Title: "Select model", Bare: true, DetailsTitle: "DETAILS",
		FilterLabel: "filter: ",
		Panes: []ev.SettingsPane{
			{Title: "PROVIDERS", Selected: 1, Rows: []ev.SettingsRow{
				{Index: 0, Label: "All", Suffix: "7"},
				{Index: 1, Label: "● openai-codex", Suffix: "7"},
				{Index: 2, Label: "○ azure", Suffix: "0"},
			}},
			{Title: "openai-codex · 2", Selected: 4, Rows: []ev.SettingsRow{
				{Index: 4, Label: "gpt-5.6-luna", Suffix: "★"},
				{Index: 5, Label: "gpt-5.6-sol", Suffix: "☆"},
			}},
		},
		Focus:       ev.SettingsValues,
		Details:     "gpt-5.6-luna\nContext  272.0k",
		WideDetails: true, ScrollDetails: true,
		Help: "↑↓ models · ← providers · Tab switch · Enter select · Esc close",
	}, true
}

// The bare snapshot keeps the painted picker's shape: no per-column boxes, no
// buttons, headers in-line, the pointer gutter on the cursor rows and the
// favourite star against the right edge.
func TestBareModelPickerKeepsPaintedShape(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	m := &bareSettingsModel{}
	r := New(m, Options{Screen: scr, Mouse: true, Key: keys.FromKey})
	scr.SetSize(120, 30)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	var frame []string
	waitFor(t, func() bool {
		frame = rows(r, scr)
		return strings.Contains(strings.Join(frame, "\n"), "DETAILS")
	})
	text := strings.Join(frame, "\n")
	for _, want := range []string{"Select model", "filter:", "PROVIDERS", "openai-codex · 2", "DETAILS", "▸ gpt-5.6-luna", "272.0k"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bare picker lost %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"Close", "Apply", "[::b]"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("bare picker still draws %q:\n%s", unwanted, text)
		}
	}
	var header, models string
	for _, line := range frame {
		switch {
		case strings.Contains(line, "PROVIDERS"):
			header = line
		case strings.Contains(line, "gpt-5.6-sol"):
			models = line
		}
	}
	if strings.Contains(header, "┌") || strings.Contains(header, "└") {
		t.Fatalf("columns should be borderless: %q", header)
	}
	if !strings.Contains(header, "│") {
		t.Fatalf("columns lost their rule: %q", header)
	}
	// The star sits two cells off the rule, as the painted rows had it.
	star := strings.Index(models, "☆")
	if star < 0 {
		t.Fatalf("favourite star lost: %q", models)
	}
	if rest := models[star+len("☆"):]; !strings.HasPrefix(rest, "  │") {
		t.Fatalf("star not right-aligned off the rule: %q", models)
	}
	// Borderless panes still take clicks on their rows.
	x, y := 0, 0
	for row, line := range frame {
		if i := strings.Index(line, "gpt-5.6-sol"); i >= 0 {
			x, y = len([]rune(line[:i])), row
		}
	}
	scr.InjectMouse(x, y, tcell.ButtonPrimary, tcell.ModNone)
	scr.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)
	want := ev.SettingsPointerMsg{Column: ev.SettingsValues, Index: 5}
	waitFor(t, func() bool { return m.lastPointer() == want })
}

// List rows are muted until they are selected: the row you are not on must
// not read as loudly as the highlighted one.
func TestSettingsRowsAreMutedUntilSelected(t *testing.T) {
	p := ev.NativePalette{Surface: "#1e1e2e", Text: "#cdd6f4", Muted: "#6c7086",
		Accent: "#89b4fa", Border: "#313244", HiBg: "#45475a", HiFg: "#cdd6f4"}
	scr := tcell.NewSimulationScreen("UTF-8")
	r := New(settingsTestModel{kind: "Pitago settings", palette: p}, Options{Screen: scr, Key: keys.FromKey})
	scr.SetSize(80, 22)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	waitFor(t, func() bool { return strings.Contains(strings.Join(rows(r, scr), "\n"), "Try theme") })
	cells, w, _ := snap(r, scr)
	lines := rows(r, scr)
	styleAt := func(text string) tcell.Style {
		t.Helper()
		for y, line := range lines {
			if x := strings.Index(line, text); x >= 0 {
				return cells[y*w+x].Style
			}
		}
		t.Fatalf("%q not on screen:\n%s", text, strings.Join(lines, "\n"))
		return tcell.StyleDefault
	}
	if fg, _, _ := styleAt("Try theme").Decompose(); fg != themeColor(p.Muted) {
		t.Fatalf("unselected row fg = %v, want muted %v", fg, themeColor(p.Muted))
	}
	// The current row keeps the foreground text colour (the pane above owns
	// the highlight), so it still reads louder than its muted neighbours.
	if fg, _, _ := styleAt("Current theme").Decompose(); fg != themeColor(p.Text) {
		t.Fatalf("selected row fg = %v, want %v", fg, themeColor(p.Text))
	}
}

// bareLoginModel is the /login picker's bare snapshot.
type bareLoginModel struct {
	barModel
	ptrBox
}

func (m *bareLoginModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if p, ok := msg.(ev.SettingsPointerMsg); ok {
		m.set(p)
	}
	return m, nil
}

func (m *bareLoginModel) NativeSettings() (ev.SettingsView, bool) {
	return ev.SettingsView{
		Kind: "login", Title: "Select provider to log in", Bare: true,
		FilterLabel: "filter: ", Focus: ev.SettingsValues,
		Panes: []ev.SettingsPane{
			{Title: "PROVIDERS", Selected: 3, Rows: []ev.SettingsRow{
				{Index: 0, Label: "● Anthropic", Suffix: "2 keys"},
				{Index: 1, Label: "○ Azure", Suffix: "no key"},
				{Index: 3, Label: "● OpenAI", Suffix: "1 key"},
			}},
			{Title: "OpenAI · 1 key", Selected: 0, Rows: []ev.SettingsRow{
				{Index: 0, Label: "sk-…9f2c", Description: "added 2d ago"},
				{Index: 1, Label: "+ Add API key…"},
			}},
		},
		Help: "↑↓ move · ←→/Tab switch · Enter use/add · ⌫ del · s show · r rename · ^P models · Esc close",
	}, true
}

// The login dialog keeps the painted two-pane shape: one frame, an inline
// header row, one rule between the columns, no per-column boxes and no
// Apply/Close buttons; both panes still take clicks.
func TestBareLoginPickerKeepsPaintedShape(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	m := &bareLoginModel{}
	r := New(m, Options{Screen: scr, Mouse: true, Key: keys.FromKey})
	scr.SetSize(110, 24)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	var frame []string
	waitFor(t, func() bool {
		frame = rows(r, scr)
		return strings.Contains(strings.Join(frame, "\n"), "PROVIDERS")
	})
	text := strings.Join(frame, "\n")
	for _, want := range []string{"Select provider to log in", "filter:", "PROVIDERS", "OpenAI · 1 key", "▸ sk-…9f2c", "— added 2d ago"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bare login lost %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Close") || strings.Contains(text, "Apply") {
		t.Fatalf("bare login still draws buttons:\n%s", text)
	}
	var header, providers string
	for _, line := range frame {
		switch {
		case strings.Contains(line, "PROVIDERS") && strings.Contains(line, "OpenAI · 1 key"):
			header = line
		case strings.Contains(line, "● Anthropic"):
			providers = line
		}
	}
	if strings.Contains(header, "┌") || !strings.Contains(header, "│") {
		t.Fatalf("header should be borderless with one rule: %q", header)
	}
	if !strings.Contains(providers, "│") {
		t.Fatalf("rows lost the column rule: %q", providers)
	}
	click := func(needle string) {
		t.Helper()
		for row, line := range frame {
			if i := strings.Index(line, needle); i >= 0 {
				x, y := len([]rune(line[:i])), row
				scr.InjectMouse(x, y, tcell.ButtonPrimary, tcell.ModNone)
				scr.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)
				return
			}
		}
		t.Fatalf("no row with %q:\n%s", needle, text)
	}
	click("Azure")
	waitFor(t, func() bool { return m.lastPointer() == (ev.SettingsPointerMsg{Column: ev.SettingsSections, Index: 1}) })
	click("sk-…9f2c")
	waitFor(t, func() bool { return m.lastPointer() == (ev.SettingsPointerMsg{Column: ev.SettingsValues, Index: 0}) })
}

// bareHubModel feeds the settings hubs' bare snapshots: the agent settings
// two-column view and the Pitago hub's three-column Theme view.
type bareHubModel struct {
	barModel
	kind string
	ptrBox
}

func (m *bareHubModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if p, ok := msg.(ev.SettingsPointerMsg); ok {
		m.set(p)
	}
	return m, nil
}

func (m *bareHubModel) NativeSettings() (ev.SettingsView, bool) {
	v := ev.SettingsView{Kind: m.kind, Bare: true, FilterLabel: "filter: ",
		Focus: ev.SettingsValues, Help: "↑↓ settings · ← groups · Tab switch · Enter change · Esc close (2/12)",
		Panes: []ev.SettingsPane{
			{Title: "GROUPS", Selected: 1, Rows: []ev.SettingsRow{
				{Index: 0, Label: "Display"},
				{Index: 1, Label: "Editor"},
			}},
			{Title: "Editor · 12", Selected: 3, Rows: []ev.SettingsRow{
				{Index: 3, Label: "Word wrap: on", Description: "wrap long lines"},
				{Index: 4, Label: "Indent: 2", Description: "tab width"},
			}},
		}}
	if m.kind == "pconfig" {
		v.Title, v.Help = "Pitago settings", "↑↓ select · ← sections · Tab switch · type filters · Enter run · Esc close"
		v.Panes = []ev.SettingsPane{
			{Title: "SECTIONS", Selected: 2, Rows: []ev.SettingsRow{
				{Index: 1, Label: "Commands · 48"},
				{Index: 2, Label: "Theme"},
			}},
			{Title: "THEME · 5", Selected: 0, Rows: []ev.SettingsRow{
				{Index: 0, Label: "✓ default", Description: "#61AFEF"},
			}},
			{Title: "BACKGROUND", Rows: []ev.SettingsRow{
				{Index: 25, Label: "transparent"},
				{Index: 26, Label: "opencode"},
			}},
		}
	} else {
		v.Title = "Agent settings"
	}
	return v, true
}

// The settings hubs keep the painted shape too: borderless columns with one
// rule between them, headers in-line, a filter line and one help line — for
// two and three columns alike, and both still take clicks.
func TestBareSettingsHubKeepsPaintedShape(t *testing.T) {
	for _, kind := range []string{"settings", "pconfig"} {
		t.Run(kind, func(t *testing.T) {
			scr := tcell.NewSimulationScreen("UTF-8")
			m := &bareHubModel{kind: kind}
			r := New(m, Options{Screen: scr, Mouse: true, Key: keys.FromKey})
			scr.SetSize(120, 24)
			go func() { _, _ = r.Run() }()
			defer r.Stop()
			needle := "GROUPS"
			if kind == "pconfig" {
				needle = "BACKGROUND"
			}
			var frame []string
			waitFor(t, func() bool {
				frame = rows(r, scr)
				return strings.Contains(strings.Join(frame, "\n"), needle)
			})
			text := strings.Join(frame, "\n")
			if strings.Contains(text, "Apply") || strings.Contains(text, "Close") {
				t.Fatalf("bare hub still draws buttons:\n%s", text)
			}
			var header string
			for _, line := range frame {
				if strings.Contains(line, needle) && strings.Contains(line, "·") && !strings.Contains(line, "filter") {
					header = line
					break
				}
			}
			if strings.Contains(header, "┌") || !strings.Contains(header, "│") {
				t.Fatalf("header should be borderless with rules: %q", header)
			}
			if kind == "settings" {
				if !strings.Contains(header, "GROUPS") || !strings.Contains(header, "Editor · 12") {
					t.Fatalf("agent hub header lost its columns: %q", header)
				}
			} else if !strings.Contains(header, "SECTIONS") || !strings.Contains(header, "THEME · 5") || !strings.Contains(header, "BACKGROUND") {
				t.Fatalf("pitago hub header lost a column: %q", header)
			}
			click := func(col ev.SettingsColumn, want ev.SettingsPointerMsg, labels ...string) {
				t.Helper()
				for _, label := range labels {
					for row, line := range frame {
						if i := strings.Index(line, label); i >= 0 {
							x, y := len([]rune(line[:i])), row
							scr.InjectMouse(x, y, tcell.ButtonPrimary, tcell.ModNone)
							scr.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)
							waitFor(t, func() bool { return m.lastPointer() == want })
							return
						}
					}
				}
				t.Fatalf("no clickable row for %v:\n%s", labels, text)
			}
			click(ev.SettingsSections, ev.SettingsPointerMsg{Column: ev.SettingsSections, Index: 0}, "Display", "Commands · 48")
			if kind == "settings" {
				click(ev.SettingsValues, ev.SettingsPointerMsg{Column: ev.SettingsValues, Index: 4}, "Indent: 2")
			} else {
				click(ev.SettingsValues, ev.SettingsPointerMsg{Column: ev.SettingsValues, Index: 0}, "✓ default")
			}
			if kind == "pconfig" {
				click(ev.SettingsBackgrounds, ev.SettingsPointerMsg{Column: ev.SettingsBackgrounds, Index: 26}, "opencode")
			}
		})
	}
}
