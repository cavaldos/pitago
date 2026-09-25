package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Two columns: left = step names, right = the selected step's detail.
func TestRenderTrajectoryDialog(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{"#01 • user: hi", "#02 • assistant: ok"},
		Descs:   []string{"10:00:01 · user", "10:00:02 · assistant"},
		Payload: []string{"#01 • user: hi\nmeta\n\nhi", "#02 • assistant: ok\nmeta\n\nok"}}
	d.Reindex()
	got := stripANSI(m.renderTrajectoryDialog(d))
	for _, want := range []string{"STEPS", "DETAIL", "#01", "user: hi", "(1/2 · all)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// moving the cursor swaps the right column to that step's detail
	d.Cursor = 1
	got = stripANSI(m.renderTrajectoryDialog(d))
	if !strings.Contains(got, "assistant: ok") || !strings.Contains(got, "(2/2 · all)") {
		t.Errorf("cursor should drive detail + position, got:\n%s", got)
	}
}

// Long details wrap into the column and report the overflow.
func TestTrajWrap(t *testing.T) {
	lines, skipped := trajWrap("a\n\n"+strings.Repeat("word ", 30), 20, 4)
	if skipped == 0 {
		t.Fatalf("long detail should overflow 4 rows: %v", lines)
	}
	if len(lines) != 4 {
		t.Fatalf("want exactly 4 rows, got %d", len(lines))
	}
}

func trajWheelDialog() *Dialog {
	d := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{"#01 • user: hi", "#02 • assistant: ok"},
		Payload: []string{"line1\n" + strings.Repeat("detail word ", 200), "short"}}
	d.Reindex()
	return d
}

// Hit-testing follows the drawn columns: left of │ = steps, on/right = detail.
func TestTrajDetailAt(t *testing.T) {
	if trajDetailAt(120, 10) {
		t.Error("x=10 should be over STEPS")
	}
	if !trajDetailAt(120, 60) {
		t.Error("x=60 should be over DETAIL")
	}
}

// Wheel over DETAIL scrolls it (3 lines/notch, clamped); over STEPS moves
// the selection and resets the offset.
func TestUpdateTrajWheel(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := trajWheelDialog()
	m.Dialogs = append(m.Dialogs, d)

	down := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: 60, Y: 10}
	nm, _ := m.updateTrajWheel(d, down)
	mm := nm.(Model)
	_ = mm
	if d.TrajOff != 3 {
		t.Fatalf("detail wheel down should scroll 3 lines, got %d", d.TrajOff)
	}
	up := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 60, Y: 10}
	nm, _ = m.updateTrajWheel(d, up)
	_ = nm.(Model)
	if d.TrajOff != 0 {
		t.Fatalf("detail wheel up should scroll back, got %d", d.TrajOff)
	}

	// many downs clamp at the end, never past it
	for i := 0; i < 30; i++ {
		nm, _ = m.updateTrajWheel(d, down)
	}
	_ = nm.(Model)
	_, _, rightW := trajGeom(120)
	total := len(trajDetailLines(d.Payload[0], rightW-4))
	if want := total - trajWin(40); d.TrajOff != want {
		t.Fatalf("offset should clamp at %d, got %d", want, d.TrajOff)
	}

	// wheel over STEPS moves the cursor and resets the detail offset
	step := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: 10, Y: 10}
	nm, _ = m.updateTrajWheel(d, step)
	sm := nm.(Model)
	dd := sm.Dialogs[0]
	if dd.Cursor != 1 || dd.TrajOff != 0 {
		t.Fatalf("steps wheel should move cursor + reset offset, got cursor=%d off=%d", dd.Cursor, dd.TrajOff)
	}
}

// Rendered detail follows the offset and reports hidden lines above.
func TestRenderTrajectoryScrolled(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := trajWheelDialog()
	d.TrajOff = 3
	got := stripANSI(m.renderTrajectoryDialog(d))
	if !strings.Contains(got, "lines above") {
		t.Errorf("scrolled detail should report lines above, got:\n%s", got)
	}
}

// PgUp/PgDn page the detail; typing a filter resets the offset.
func TestTrajPageAndReindex(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := trajWheelDialog()
	m.trajPage(d, true)
	if d.TrajOff == 0 {
		t.Fatal("PgDn should advance the detail offset")
	}
	m.trajPage(d, false)
	if d.TrajOff != 0 {
		t.Fatalf("PgUp should return to top, got %d", d.TrajOff)
	}
	d.TrajOff = 5
	d.Filter = "user"
	d.Reindex()
	if d.TrajOff != 0 {
		t.Error("re-filter should reset the detail offset")
	}
}

// trajBoxWidth asserts every box-content line (│ rows) shares one width
// and returns it: the window must never resize with its content. (Border
// ╭╰ lines are lipgloss's own width math, identical for any content, so
// they are excluded — only content rows can be stretched by long text.)
func trajBoxWidth(t *testing.T, out string) int {
	t.Helper()
	w := -1
	n := 0
	for _, ln := range strings.Split(stripANSI(out), "\n") {
		if !strings.Contains(ln, "│") {
			continue
		}
		n++
		if w < 0 {
			w = lipgloss.Width(ln)
		} else if lipgloss.Width(ln) != w {
			t.Fatalf("ragged box line (width %d != %d): %q", lipgloss.Width(ln), w, ln)
		}
	}
	if n == 0 {
		t.Fatalf("no box content in:\n%s", out)
	}
	return w
}

// Unbreakable words, a 120-char filter and a long footer at a narrow
// terminal must render the exact same box as short content.
func TestRenderTrajectoryFixedSize(t *testing.T) {
	m := Model{winW: 100, winH: 40}
	nasty := "#01 • user: " + strings.Repeat("x", 300)
	d := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{nasty, "#02 • [read: a.go]"},
		Descs:   []string{"10:00:01 · user", "10:00:02 · tool"},
		Payload: []string{nasty + "\nmeta\n\n" + strings.Repeat("y", 400), "short"},
		Filter:  strings.Repeat("f", 120)}
	d.Reindex()
	wNasty := trajBoxWidth(t, m.renderTrajectoryDialog(d))

	p := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{"#01 • user: hi"},
		Descs:   []string{"10:00:01 · user"},
		Payload: []string{"hi"}}
	p.Reindex()
	wPlain := trajBoxWidth(t, m.renderTrajectoryDialog(p))
	if wNasty != wPlain {
		t.Errorf("box resizes with content: nasty=%d plain=%d", wNasty, wPlain)
	}
}

// Box height never moves with scroll and the │ divider stays on one
// column (selected rows used to be 2 cells wider, jagging the line).
func TestRenderTrajectoryFixedHeightAndDivider(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := trajWheelDialog()
	heights := map[int]int{}
	for _, off := range []int{0, 3, 1000} {
		d.TrajOff = off
		out := stripANSI(m.renderTrajectoryDialog(d))
		heights[off] = len(strings.Split(strings.TrimRight(out, "\n"), "\n"))
	}
	if heights[0] != heights[3] || heights[3] != heights[1000] {
		t.Fatalf("height varies with scroll: %v", heights)
	}
	s := &Dialog{Kind: "trajectory", Title: "T", Scope: "all",
		Options: []string{"#01 • user: hi"}, Descs: []string{"t · user"}, Payload: []string{"hi"}}
	s.Reindex()
	hShort := len(strings.Split(strings.TrimRight(stripANSI(m.renderTrajectoryDialog(s)), "\n"), "\n"))
	if hShort != heights[0] {
		t.Fatalf("height varies short=%d long=%d", hShort, heights[0])
	}
	d2 := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{"#01 • user: hi", "#02 • assistant: ok", "#03 • user: yo"},
		Descs:   []string{"t · user", "t · assistant", "t · user"},
		Payload: []string{"a\nb\n\nc", "d\ne\n\nf", "g\nh\n\ni"}}
	d2.Reindex()
	d2.Cursor = 1
	want := -1
	for _, ln := range strings.Split(stripANSI(m.renderTrajectoryDialog(d2)), "\n") {
		if strings.Count(ln, "│") != 3 {
			continue
		}
		if idx := len(strings.Split(ln, "│")[0]); want < 0 {
			want = idx
		} else if idx != want {
			t.Fatalf("divider jags: got %d want %d in %q", idx, want, ln)
		}
	}
}

// Kind + [tag] spans carry color; visible cells are unchanged.
// (lipgloss mutes color without a TTY, so the test forces ANSI256 and
// restores the ambient profile after.)
func TestTrajColorRow(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)
	got := trajColorRow("#01 • user: hi [read: a.go]", "user")
	if !strings.Contains(got, "\x1b[") {
		t.Error("colored row should contain ANSI sequences")
	}
	if stripANSI(got) != "#01 • user: hi [read: a.go]" {
		t.Errorf("colors must not change cells, got %q", stripANSI(got))
	}
	asst := trajColorRow("#02 • assistant: ok", "assistant")
	if !strings.Contains(asst, "\x1b[") || stripANSI(asst) != "#02 • assistant: ok" {
		t.Errorf("assistant row miscolored: %q", asst)
	}
	if plain := trajColorRow("plain row", ""); !strings.Contains(plain, "\x1b[") {
		t.Error("unknown kind should still render styled")
	}
	if leg := trajLegend(70); !strings.Contains(leg, "\x1b[") || lipgloss.Width(leg) != 70 {
		t.Errorf("legend should be colored and exactly 70 cells, got width %d", lipgloss.Width(leg))
	}
}
