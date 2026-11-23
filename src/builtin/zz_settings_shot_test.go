package builtin

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"pitago/src/app"
	"pitago/src/components/theme"
	"pitago/src/pirpc"
	"pitago/src/tui/ev"
	"pitago/src/tui/keys"
	"pitago/src/tui/tviewrt"
)

type viewProvider struct{ v ev.SettingsView }

func (p *viewProvider) Init() ev.Cmd                            { return nil }
func (p *viewProvider) Update(ev.Msg) (ev.Model, ev.Cmd)        { return p, nil }
func (p *viewProvider) View() string                            { return "" }
func (p *viewProvider) NativeSettings() (ev.SettingsView, bool) { return p.v, true }

func TestZZSettingsPipeScan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		w, h     int
		pconfig  bool
		themeRow bool
	}{
		{"settings-200-dump", 200, 104, false, false},
		{"login-200", 200, 104, false, false},
		{"login-200-40", 200, 40, false, false},
		{"settings-200", 200, 104, false, false},
		{"settings-140", 140, 40, false, false},
		{"settings-100", 100, 34, false, false},
		{"settings-80", 80, 28, false, false},
		{"settings-60", 60, 24, false, false},
		{"pconfig-theme-200", 200, 104, true, true},
		{"pconfig-theme-140", 140, 40, true, true},
		{"pconfig-theme-100", 100, 34, true, true},
		{"pconfig-market-200", 200, 104, true, false},
		{"pconfig-market-140", 140, 40, true, false},
		{"pconfig-140", 140, 40, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) { pipeScanFor(t, tc.w, tc.h, tc.pconfig, tc.themeRow) })
	}
}

func pipeScanFor(t *testing.T, w, h int, pconfig, themeRow bool) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Cleanup(func() { app.ApplyTheme(theme.Get("default")) })
	m := app.New(nil, home)
	m.Configure(pirpc.Options{}, "")
	m.UseBuiltins(All(), Confirmers())
	var state ev.Model = m
	if !pconfig {
		st := app.SettingsState{Theme: "default", Vals: fileSettingVals(nil)}
		opts, descs, cats := settingsOptions(st)
		state, _ = state.Update(app.SettingsMsg{St: st, Opts: opts, Descs: descs, Cats: cats})
	} else {
		m.OpenPconfig()
		state = m
		section := "Theme"
		if !themeRow {
			section = "Marketplace"
		}
		v, _ := m.NativeSettings()
		pos := -1
		for i, r := range v.Panes[ev.SettingsSections].Rows {
			if strings.HasPrefix(r.Label, section) {
				pos = i
				break
			}
		}
		if pos < 0 {
			t.Fatalf("no %s section", section)
		}
		state, _ = state.Update(ev.SettingsPointerMsg{Column: ev.SettingsSections, Index: pos})
	}
	v, ok := state.(ev.SettingsProvider).NativeSettings()
	if !ok {
		t.Fatal("no settings view")
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	r := tviewrt.New(&viewProvider{v: v}, tviewrt.Options{Screen: scr, Key: keys.FromKey})
	scr.SetSize(w, h)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	needle := "GROUPS"
	if pconfig {
		needle = "SECTIONS"
	}
	var rows []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		scr.Sync()
		c, w2, h2 := scr.GetContents()
		rows = make([]string, h2)
		for y := 0; y < h2; y++ {
			var b strings.Builder
			for x := 0; x < w2; x++ {
				cell := c[y*w2+x]
				if len(cell.Runes) == 0 {
					b.WriteByte(' ')
					continue
				}
				b.WriteString(string(cell.Runes))
			}
			rows[y] = b.String()
		}
		if strings.Contains(strings.Join(rows, "\n"), needle) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(strings.Join(rows, "\n"), needle) {
		t.Fatalf("no frame with %s", needle)
	}
	// Scan: the header row defines the pipe columns; every row from the
	// header down to the last piped row must carry every pipe.
	var headerRow int
	pipes := []int{}
	for y, line := range rows {
		if strings.Contains(line, needle) && strings.Contains(line, "·") && !strings.Contains(line, "filter") {
			headerRow = y
			for x, ch := range []rune(line) {
				if ch == '│' {
					pipes = append(pipes, x)
				}
			}
			break
		}
	}
	if len(pipes) == 0 {
		t.Fatalf("no pipes in header row %d: %q", headerRow, rows[headerRow])
	}
	holes := []string{}
	lastPiped := headerRow
	for y := headerRow + 1; y < len(rows); y++ {
		line := []rune(rows[y])
		hasPipe := false
		for _, x := range pipes {
			if x < len(line) && line[x] == '│' {
				hasPipe = true
			} else {
				holes = append(holes, fmt.Sprintf("row %d col %d: %q", y, x, strings.TrimRight(rows[y], " ")))
			}
		}
		if hasPipe {
			lastPiped = y
		}
		if !hasPipe && strings.Contains(rows[y], needle) {
			break
		}
	}
	fmt.Printf("### %s: header=%d pipes=%v lastPiped=%d holes=%d\n", t.Name(), headerRow, pipes, lastPiped, len(holes))
	for _, hole := range holes {
		fmt.Println("  HOLE", hole)
	}
	if strings.Contains(t.Name(), "dump") {
		fmt.Println(strings.Join(rows, "\n"))
	}
}
