package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

func subagentsTestModel(t *testing.T) Model {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", "")
	m := New(nil, t.TempDir())
	now := time.Now()
	done := now.Add(-time.Minute)
	m.Subagents = []SubagentRow{
		{ID: "c1", Name: "scout", AgentName: "explore", Task: "map auth", Tool: "subagent", Status: SubagentActive, StatusLabel: "read", StartedAt: now.Add(-90 * time.Second)},
		{ID: "c2", Name: "builder", Tool: "subagent", Status: SubagentDone, StartedAt: now.Add(-5 * time.Minute), DoneAt: &done, Result: "built it"},
	}
	return m
}

func openSubagents(t *testing.T, m Model) Model {
	t.Helper()
	m.OpenSubagentHerd()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagent-herd" {
		t.Fatalf("dialogs = %+v", m.Dialogs)
	}
	return m
}

func pressSubagents(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	tm, _ := m.Update(msg)
	return tm.(Model)
}

func TestOpenSubagentsList(t *testing.T) {
	m := openSubagents(t, subagentsTestModel(t))
	d := m.Dialogs[0]
	if d.Scope != subagentsScopeActive {
		t.Fatalf("scope = %q", d.Scope)
	}
	if len(d.Options) != 1 || d.Options[0] != "scout" {
		t.Fatalf("active options = %v", d.Options)
	}
	out := stripANSI(m.renderDialog())
	if !strings.Contains(out, "scout") || !strings.Contains(out, "read") {
		t.Fatalf("render missing row:\n%s", out)
	}
}

func TestSubagentsFilter(t *testing.T) {
	m := openSubagents(t, subagentsTestModel(t))
	m.Subagents = append(m.Subagents, SubagentRow{ID: "c3", Name: "wraith", Status: SubagentActive, StartedAt: time.Now()})
	m.OpenSubagentHerd()
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sc")})
	d := m.Dialogs[0]
	if len(d.FIdx) != 1 {
		t.Fatalf("FIdx = %v for filter %q", d.FIdx, d.Filter)
	}
	// no match → empty state row
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	if len(m.Dialogs[0].FIdx) != 0 {
		t.Fatalf("FIdx should be empty, got %v", m.Dialogs[0].FIdx)
	}
	if out := stripANSI(m.renderDialog()); !strings.Contains(out, "no subagents") {
		t.Fatalf("empty state missing:\n%s", out)
	}
}

func TestSubagentsTabScope(t *testing.T) {
	m := openSubagents(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyTab})
	d := m.Dialogs[0]
	if d.Scope != subagentsScopeFinished {
		t.Fatalf("scope = %q", d.Scope)
	}
	if len(d.Options) != 1 || d.Options[0] != "builder" {
		t.Fatalf("finished options = %v", d.Options)
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Dialogs[0].Scope != subagentsScopeActive {
		t.Fatal("second Tab should return to active")
	}
}

func TestSubagentsDetailOpenBack(t *testing.T) {
	m := openSubagents(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	d := m.Dialogs[0]
	if !d.SubDetail || d.SubID != "c1" {
		t.Fatalf("detail = %+v", d)
	}
	if d.SubHeader == "" || len(d.SubLines) == 0 {
		t.Fatalf("detail content missing: %+v", d)
	}
	out := stripANSI(m.renderDialog())
	if !strings.Contains(out, "scout") || !strings.Contains(out, "map auth") {
		t.Fatalf("detail render missing content:\n%s", out)
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.Dialogs[0].SubDetail {
		t.Fatal("Esc should close detail, not the dialog")
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.Dialogs) != 0 {
		t.Fatal("second Esc should close the dialog")
	}
}

func TestSubagentsDetailScrollSize(t *testing.T) {
	m := openSubagents(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.Dialogs[0].SubOffset != 20 {
		t.Fatalf("offset = %d", m.Dialogs[0].SubOffset)
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.Dialogs[0].SubOffset != 0 {
		t.Fatalf("offset = %d", m.Dialogs[0].SubOffset)
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.Dialogs[0].SubSize != 1 {
		t.Fatalf("size = %d", m.Dialogs[0].SubSize)
	}
}

func TestSubagentActionsGating(t *testing.T) {
	cases := []struct {
		name string
		row  SubagentRow
		want string
	}{
		{"live active", SubagentRow{ID: "c1", Status: SubagentActive}, "xwXfs"},
		{"live waiting", SubagentRow{ID: "c1", Status: SubagentWaiting}, "xwXfs"},
		{"live done", SubagentRow{ID: "c1", Status: SubagentDone}, "wRXf"},
		{"live stalled", SubagentRow{ID: "c1", Status: SubagentStalled}, "RXf"},
		{"live error", SubagentRow{ID: "c1", Status: SubagentError}, "RXf"},
		{"disk active", SubagentRow{ID: "disk-x", Status: SubagentActive}, "Xf"},
		{"disk done with file", SubagentRow{ID: "disk-x", Status: SubagentDone, SessionFile: "/a/subagent-x.jsonl"}, "RXf"},
	}
	for _, tc := range cases {
		if got := subagentActions(tc.row); got != tc.want {
			t.Errorf("%s: actions = %q want %q", tc.name, got, tc.want)
		}
	}
	if got := subagentActionHints(SubagentRow{ID: "c1", Status: SubagentActive}); got != "x stop · w wait · X dismiss · f surface · s steer · Esc back" {
		t.Errorf("hints = %q", got)
	}
}

func TestSubagentsBlockedActionStaysOpen(t *testing.T) {
	// x on a settled row cannot stop anything: notice, no send, dialog stays.
	m := subagentsTestModel(t)
	m.Subagents = []SubagentRow{m.Subagents[1]} // builder (done) only
	m = openSubagents(t, m)
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyTab}) // finished scope
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.Dialogs[0].SubDetail {
		t.Fatal("Enter should open detail on the done row")
	}
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || cmd != nil {
		t.Fatalf("blocked x: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
	// R on a running row cannot resume anything: same treatment.
	m = openDetail(t, subagentsTestModel(t)) // scout (active)
	tm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || cmd != nil {
		t.Fatalf("blocked R: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
}

func TestParseSubagentSurface(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Started subagent x (surface: term_abc123)", "term_abc123"},
		{"no marker here", ""},
		{"(surface: bogus)", "bogus"}, // opaque: hosts claim it
		{"(surface: 01a0da54-f68c-735c-91b2-cc70d48b8a12)", "01a0da54-f68c-735c-91b2-cc70d48b8a12"},
		{"(surface: window:1/workspace:2/pane:3)", "window:1/workspace:2/pane:3"},
		{"(surface: term_x --evil)", ""},    // no flags, no spaces
		{"(surface: term_x\nrm -rf /)", ""}, // no newlines
		{"(surface: term_x", ""},
		{"(surface:  term_9-8 )", "term_9-8"},
	}
	for _, tc := range cases {
		if got := parseSubagentSurface(tc.in); got != tc.want {
			t.Errorf("parse %q = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestOrcaTermCmd(t *testing.T) {
	always := func() error { return nil }
	ctl := &surfaceCtl{name: "testhost", available: always, claim: func(string) bool { return true }, run: func(action, handle string) error {
		if action != "switch" || handle != "term_x" {
			return errors.New("unexpected call")
		}
		return nil
	}}
	m := subagentsTestModel(t)
	done, ok := m.orcaTermCmd(ctl, "switch", "term_x", "lbl")().(orcaTermDoneMsg)
	if !ok || done.err != nil || done.label != "lbl" || done.action != "switch" || done.host != "testhost" {
		t.Fatalf("cmd msg = %+v %v", done, ok)
	}
	ctl.run = func(string, string) error { return errors.New("boom") }
	done, ok = m.orcaTermCmd(ctl, "close", "term_y", "lbl")().(orcaTermDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("fail cmd msg = %+v %v", done, ok)
	}
	// No host: the command reports the host-agnostic sentinel.
	done, ok = m.orcaTermCmd(nil, "switch", "term_z", "lbl")().(orcaTermDoneMsg)
	if !ok || !errors.Is(done.err, errNoSurfaceCtl) {
		t.Fatalf("no-host msg = %+v %v", done, ok)
	}
}

// TestSurfaceControllerTablePicksFirstAvailable proves the table is what
// selects a host, so a second implementation (cmux, cmh) is a data change.
func TestSurfaceControllerTablePicksFirstAvailable(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	missing := surfaceCtl{name: "missing", available: func() error { return errNoSurfaceCtl }, claim: func(string) bool { return true }, run: func(string, string) error { return nil }}
	working := surfaceCtl{name: "cmuxish", available: func() error { return nil }, claim: func(string) bool { return true }, run: func(string, string) error { return nil }}
	surfaceControllers = func() []surfaceCtl { return []surfaceCtl{missing, working} }
	ctl := activeSurfaceCtl()
	if ctl == nil || ctl.name != "cmuxish" {
		t.Fatalf("expected the second controller, got %+v", ctl)
	}
	msg := (Model{}).orcaTermCmd(ctl, "close", "term_a", "lbl")().(orcaTermDoneMsg)
	if msg.host != "cmuxish" || msg.err != nil {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestSubagentActionPrompt(t *testing.T) {
	row := SubagentRow{ID: "c1", Name: "scout"}
	for _, a := range []string{"x", "w", "R"} {
		text, ok := subagentActionPrompt(a, row)
		if !ok || !strings.Contains(text, "scout") || !strings.Contains(text, "c1") {
			t.Errorf("action %q = %q %v", a, text, ok)
		}
	}
	if _, ok := subagentActionPrompt("s", row); ok {
		t.Error("steer should open the nested dialog, not a prompt")
	}
	if _, ok := subagentActionPrompt("bogus", row); ok {
		t.Error("unknown action should not dispatch")
	}
}

// openDetail focuses the first row and opens detail (actions live there:
// list mode is navigation + filter only, so typing never collides with
// action letters in names like "explore").
func openDetail(t *testing.T, m Model) Model {
	t.Helper()
	m = openSubagents(t, m)
	return pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

func TestSubagentsActionDispatch(t *testing.T) {
	// x closes the dialog and yields a send command (not executed: no Pi)
	m := openDetail(t, subagentsTestModel(t))
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = tm.(Model)
	if len(m.Dialogs) != 0 || cmd == nil {
		t.Fatalf("x: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
	// s opens the nested steer input on top
	m = openDetail(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "subagents-steer" {
		t.Fatalf("steer dialogs = %+v", m.Dialogs)
	}
	// typing + Enter submits (command not executed here); the list
	// dialog stays underneath
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("keep going")})
	m = tm.(Model)
	tm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || cmd == nil {
		t.Fatalf("steer submit: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
	// f shows the surface and closes
	m = openDetail(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if len(m.Dialogs) != 0 {
		t.Fatal("f should close the dialog")
	}
	found := false
	for _, tst := range m.toasts {
		if strings.Contains(tst.Text, "scout") {
			found = true
		}
	}
	if !found {
		t.Fatalf("f should toast the surface, toasts=%+v", m.toasts)
	}
	// X dismisses locally with a notice, no turn spent (no send command),
	// and returns to the herd list instead of the chat transcript.
	m = openDetail(t, subagentsTestModel(t))
	tm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagent-herd" || m.Dialogs[0].SubDetail {
		t.Fatalf("X should return to the list: %+v", m.Dialogs)
	}
	if cmd != nil {
		t.Fatal("X should not spend a turn on a pane-less row")
	}
	if len(m.Subagents) != 1 {
		t.Fatalf("X should dismiss the row: %+v", m.Subagents)
	}
	// Esc from the returned list still closes the overlay.
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.Dialogs) != 0 {
		t.Fatalf("Esc after X: dialogs=%d", len(m.Dialogs))
	}
}

func TestSubagentSteerReturnsToList(t *testing.T) {
	m := openDetail(t, subagentsTestModel(t))
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "subagents-steer" {
		t.Fatalf("steer dialogs = %+v", m.Dialogs)
	}
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagent-herd" {
		t.Fatalf("steer submit should leave the steer box: %+v", m.Dialogs)
	}
	if m.Dialogs[0].SubDetail {
		t.Fatal("steer submit should land on the herd list, not detail")
	}
}

func TestRestoreSubagentsCarriesSurface(t *testing.T) {
	msgs := []pirpc.AgentMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"toolCall","id":"c1","name":"subagent","arguments":{"name":"w","interactive":true,"task":"t"}}]`)},
		{Role: "toolResult", ToolCallID: "c1", ToolName: "subagent", Content: json.RawMessage(`[{"type":"text","text":"Started subagent w (surface: term_abc)"}]`)},
	}
	rows := restoreSubagentsFromMessages(msgs)
	if len(rows) != 1 || rows[0].Surface != "term_abc" || rows[0].Status != SubagentDone {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestSubagentsMoreAtHitTest(t *testing.T) {
	m := subagentsTestModel(t)
	m.ready = true
	m.Mouse = true
	m.Side = map[string]bool{}
	m.Subagents = nil
	now := time.Now()
	for i := 0; i < subagentsShowMax+2; i++ {
		m.Subagents = append(m.Subagents, SubagentRow{
			ID:        fmt.Sprintf("s%d", i),
			Name:      fmt.Sprintf("agent-%d", i),
			Status:    SubagentActive,
			StartedAt: now.Add(-time.Duration(i) * time.Second),
		})
	}
	section := m.renderSubagentsSection(30)
	if !strings.Contains(stripANSI(section), "+2 more") {
		t.Fatalf("hint missing:\n%s", section)
	}
	if strings.Contains(stripANSI(section), "/subagent") {
		t.Fatalf("hint should not name a command:\n%s", section)
	}
	row := 0
	for i, l := range strings.Split(section, "\n") {
		if strings.Contains(stripANSI(l), "+2 more") {
			row = i
		}
	}
	m.sideCache = "filler\n" + section
	m.winW = 80
	// sideCache index 0 is the first sidebar content row, which renders on
	// screen row 1, so the hint's screen row is 1 + its cache index.
	idx := row + 1
	x := m.mainW() + 1 // first column inside the sidebar
	if !m.subagentsMoreAt(x, idx+1) {
		t.Fatalf("click should hit the truncation line:\n%s", section)
	}
	// Scrolled down: the same content row moves up the screen.
	m.sideVp.YOffset = 3
	if !m.subagentsMoreAt(x, idx+1-3) {
		t.Fatal("hit test must account for the sidebar scroll offset")
	}
	if m.subagentsMoreAt(x, idx+2) {
		t.Fatal("click one row below must miss")
	}
	m.Subagents = m.Subagents[:1]
	if m.subagentsMoreAt(x, idx+1) {
		t.Fatal("untruncated panel must not be clickable")
	}
}

func TestSubagentRowAtHitTest(t *testing.T) {
	m := subagentsTestModel(t)
	m.ready = true
	m.Mouse = true
	m.Side = map[string]bool{}
	m.winW = 80
	m.Subagents = nil
	now := time.Now()
	// Duplicate names on purpose: the hit test must map by position, not by
	// the first text match.
	for i := 0; i < 3; i++ {
		m.Subagents = append(m.Subagents, SubagentRow{
			ID: fmt.Sprintf("s%d", i), Name: "waiter-subagent",
			Status: SubagentActive, StatusLabel: "bash",
			StartedAt: now.Add(-time.Duration(i) * time.Second),
		})
	}
	m.sideCache = "filler\n" + m.renderSubagentsSection(30)
	lines, _, ok := m.subagentsRenderedRows()
	if !ok || len(lines) != 3 {
		t.Fatalf("expected 3 rendered rows, got %d (lines=%v)", len(lines), lines)
	}
	x := m.mainW() + 1
	for i, want := range []string{"s0", "s1", "s2"} {
		if got := m.subagentRowAt(x, lines[i]+1); got != want {
			t.Errorf("row %d = %q want %q", i, got, want)
		}
	}
	if got := m.subagentRowAt(x, lines[2]+2); got != "" {
		t.Errorf("click past the last row must miss, got %q", got)
	}
	if got := m.subagentRowAt(x, 1); got != "" {
		t.Errorf("click on the filler row must miss, got %q", got)
	}
	m.sideVp.YOffset = 2
	if got := m.subagentRowAt(x, lines[2]+1-2); got != "s2" {
		t.Errorf("scrolled hit = %q want s2", got)
	}
	m.sideCache = "filler\nSubagents (3)\n"
	if got := m.subagentRowAt(x, 3); got != "" {
		t.Errorf("mismatched render must miss, got %q", got)
	}
}

func TestNoSurfaceHostDegradesGracefully(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	// A machine without any pane host (plain terminal, Warp, Ghostty).
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{name: "orca", available: func() error { return errNoSurfaceCtl }, claim: func(string) bool { return true }, run: func(string, string) error { return nil }}}
	}
	if ctl := activeSurfaceCtl(); ctl != nil {
		t.Fatalf("no host expected, got %+v", ctl)
	}
	// f: says where the pane is, never claims focus.
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "pane", Status: SubagentActive, Interactive: true, Surface: "term_abc"}
	tm, _ := m.runSubagentAction(m.Dialogs[0], row, "f")
	m = tm.(Model)
	if len(m.Dialogs) != 0 {
		t.Fatalf("f should close the overlay, got dialogs=%d", len(m.Dialogs))
	}
	found := false
	for _, to := range m.toasts {
		if strings.Contains(to.Text, "term_abc") && strings.Contains(to.Text, "no installed host can focus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("f must explain the fallback, toasts=%+v", m.toasts)
	}
	// X: dismisses locally and drives nothing.
	m2 := subagentsTestModel(t)
	m2.OpenSubagentHerd()
	// The x prompt stays host-agnostic: SIGINT is always the instruction,
	// and it must not name a host that is not installed.
	text, ok := subagentActionPrompt("x", row)
	if !ok || !strings.Contains(text, "kill -INT") {
		t.Fatalf("x prompt = %q %v", text, ok)
	}
	if strings.Contains(strings.ToLower(text), "orca") {
		t.Errorf("prompt names a host that is not installed: %q", text)
	}
	tm, _ = m2.runSubagentAction(m2.Dialogs[0], row, "X")
	m2 = tm.(Model)
	if len(m2.Subagents) != 2 {
		t.Fatalf("X without a host must dismiss locally: rows=%d", len(m2.Subagents))
	}
}

func TestSubagentDetailShowsResultAndTranscript(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.jsonl")
	body := "## Review: thing\n\n**Verdict: not mergeable**\n\n- one\n- two"
	lines := []string{`{"type":"session"}`}
	for _, msg := range []struct{ role, text string }{
		{"user", "review the diff"},
		{"assistant", body},
	} {
		raw, err := json.Marshal(map[string]any{
			"type":    "message",
			"message": map[string]any{"role": msg.role, "content": []any{map[string]any{"type": "text", "text": msg.text}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(child, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A finished in-process child: result from the tool call, transcript
	// adopted from its session file.
	started := time.Now().Add(-90 * time.Second)
	done := time.Now()
	merged := mergeSubagentDisk(
		[]SubagentRow{{ID: "c1", Name: "herd-reviewer", Task: "review the diff", Status: SubagentDone, StartedAt: started, DoneAt: &done, Result: body}},
		// The child session started while the row was running.
		[]SubagentRow{{ID: "disk-x", Task: "review the diff", Status: SubagentDone, SessionFile: child, StartedAt: started.Add(5 * time.Second)}},
	)
	if len(merged) != 1 || merged[0].SessionFile == "" {
		t.Fatalf("finished child should adopt its transcript: %+v", merged)
	}
	if merged[0].Status != SubagentDone {
		t.Fatalf("adopting a transcript must not change status: %+v", merged[0])
	}
	m := subagentsTestModel(t)
	m.Subagents = merged
	m.OpenSubagentHerd()
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyTab}) // finished scope
	m = pressSubagents(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// The pane scrolls, so assert on the content it holds, not on the
	// window that happens to be visible right now.
	joined := strings.Join(m.Dialogs[0].SubLines, "\n")
	for _, want := range []string{"── result ──", "**Verdict: not mergeable**", "- one", "─ transcript ─", "assistant:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("detail content missing %q:\n%s", want, joined)
		}
	}
	// The result body must be multi-line, not one clipped line.
	blanks := 0
	for _, l := range m.Dialogs[0].SubLines {
		if strings.TrimSpace(l) == "" {
			blanks++
		}
	}
	if blanks == 0 {
		t.Error("result should keep its line structure")
	}
}

func TestRestoredRowAdoptsItsSessionInsteadOfDuplicating(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.jsonl")
	childStart := time.Now().Add(-20 * time.Minute)
	// A session that started well before the row was replayed.
	if err := os.WriteFile(child, []byte(`{"type":"session"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []pirpc.AgentMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"toolCall","id":"c1","name":"subagent","arguments":{"name":"herd-reviewer","task":"Review the diff"}}]`)},
		{Role: "toolResult", ToolCallID: "c1", ToolName: "subagent", Content: json.RawMessage(`[{"type":"text","text":"## Verdict: mergeable"}]`)},
	}
	rows := restoreSubagentsFromMessages(msgs)
	if len(rows) != 1 || !rows[0].Restored {
		t.Fatalf("history rows must be marked restored: %+v", rows)
	}
	// The replay stamps StartedAt = now, which is after the child's real
	// start: without the restored exemption the disk row cannot be adopted
	// and shows up as a duplicate in the overview.
	disk := []SubagentRow{{ID: "disk-x", Task: "Review the diff", Status: SubagentDone, SessionFile: child, StartedAt: childStart, DoneAt: &childStart}}
	merged := mergeSubagentDisk(rows, disk)
	if len(merged) != 1 {
		t.Fatalf("restored row duplicated: %+v", merged)
	}
	if merged[0].SessionFile == "" || merged[0].Restored {
		t.Errorf("restored row should adopt its session and clear the flag: %+v", merged[0])
	}
	if merged[0].StartedAt.After(childStart.Add(time.Second)) {
		t.Errorf("elapsed should come from the session, not the replay: %+v", merged[0].StartedAt)
	}
	if merged[0].Status != SubagentDone {
		t.Errorf("adoption must not change a settled status: %+v", merged[0])
	}
	// A disk row with no completion time (no shutdown marker) must not
	// leave the row spanning from the session start to now — that is the
	// replay gap rendered as elapsed time.
	live2 := restoreSubagentsFromMessages(msgs)
	disk2 := []SubagentRow{{ID: "disk-y", Task: "Review the diff", Status: SubagentActive, SessionFile: child, StartedAt: childStart}}
	merged2 := mergeSubagentDisk(live2, disk2)
	if len(merged2) != 1 || merged2[0].DoneAt != nil {
		t.Fatalf("a session with no completion time must not leave a bogus span: %+v", merged2)
	}
	if merged2[0].StartedAt.After(childStart.Add(time.Second)) {
		t.Errorf("elapsed should still come from the session: %+v", merged2[0].StartedAt)
	}
}

func TestSubagentsEmpty(t *testing.T) {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", "")
	m := New(nil, t.TempDir())
	m.OpenSubagentHerd()
	if len(m.Dialogs[0].Options) != 0 {
		t.Fatalf("options = %v", m.Dialogs[0].Options)
	}
	if out := stripANSI(m.renderDialog()); !strings.Contains(out, "no subagents") {
		t.Fatalf("empty state missing:\n%s", out)
	}
}

// A host that knows up front it cannot close this handle must stop the close
// BEFORE spawning it, while still dismissing the row. The row is bookkeeping
// and always succeeds; refusing to dismiss would leave the row stuck in the
// overlay with no key that can remove it — the bug an earlier version of this
// guard introduced.
func TestRunSubagentActionPreflightRefusalStillDismisses(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	var ran string
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{
			name:      "testhost",
			available: func() error { return nil },
			claim:     func(string) bool { return true },
			precheck: func(action, handle string) error {
				return fmt.Errorf("testhost cannot close %s", handle)
			},
			run: func(action, handle string) error { ran = action + ":" + handle; return nil },
		}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "pane", Status: SubagentActive, Interactive: true, Surface: "term_abc"}
	tm, cmd := m.runSubagentAction(m.Dialogs[0], row, "X")
	m = tm.(Model)

	if ran != "" {
		t.Fatalf("a refused close still called run: %q", ran)
	}
	if !m.dismissed["p1"] {
		t.Fatalf("row was not dismissed: dismissed=%v", m.dismissed)
	}
	var dismissed, paneLeftOpen bool
	for _, b := range m.toasts {
		if strings.Contains(b.Text, "dismissed") {
			dismissed = true
		}
		if strings.Contains(b.Text, "pane left open") && strings.Contains(b.Text, "cannot close term_abc") {
			paneLeftOpen = true
		}
	}
	if !dismissed || !paneLeftOpen {
		t.Fatalf("notices = %+v (want dismissed + pane left open)", m.toasts)
	}
	// The refusal must not hand back the close batch. Checking the type of
	// cmd() cannot do that -- a batch yields a BatchMsg, never the close msg
	// -- so this asserts the observable: the host's run was never reached
	// (checked above via `ran`) and the model carries no pending pane close.
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, isBatch := msg.(tea.BatchMsg); isBatch {
				t.Fatalf("refusal still returned a batch, which is the close path: %T", msg)
			}
		}
	}
}

// An in-process child has no pane of its own — it shares the parent's
// terminal — so `f` used to answer "in-process (no pane)" and stop. A host
// that can name the surface it is running in gives that row something real to
// focus, which is what makes the row useful outside Orca.
func TestFocusHandleLessRowUsesHostCurrentSurface(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	var got string
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{
			name:          "testhost",
			available:     func() error { return nil },
			claim:         func(h string) bool { return h == "workspace:1/pane:1" },
			precheck:      func(string, string) error { return nil },
			currentHandle: func() (string, error) { return "workspace:1/pane:1", nil },
			run: func(action, handle string) error {
				got = action + ":" + handle
				return nil
			},
		}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "inproc", Status: SubagentDone} // no Surface
	tm, cmd := m.runSubagentAction(m.Dialogs[0], row, "f")
	m = tm.(Model)
	if cmd == nil {
		t.Fatal("f returned no command")
	}
	if msg, ok := cmd().(orcaTermDoneMsg); !ok || msg.action != "switch" {
		t.Fatalf("expected a focus action, got %#v", cmd())
	}
	if got != "switch:workspace:1/pane:1" {
		t.Fatalf("focus ran %q, want the host's current surface", got)
	}
	found := false
	for _, b := range m.toasts {
		if strings.Contains(b.Text, "in-process (no pane)") {
			found = true
		}
	}
	if found {
		t.Fatalf("still reported 'in-process (no pane)': %+v", m.toasts)
	}
}

// `f` now runs the host precheck before dispatching, so a recorded handle
// the host cannot address is refused up front with a readable notice instead
// of an async failure. The notice must not claim success.
func TestFocusRefusesAHandleItsHostCannotAddress(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{
			name:      "testhost",
			available: func() error { return nil },
			claim:     func(string) bool { return true },
			precheck:  func(string, string) error { return errors.New("cannot address that surface") },
			run:       func(string, string) error { return nil },
		}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "pane", Status: SubagentActive, Interactive: true, Surface: "workspace:1/pane:1"}
	tm, _ := m.runSubagentAction(m.Dialogs[0], row, "f")
	m = tm.(Model)
	// The discriminator is the "focusing pane…" notice, which the code emits
	// only AFTER the precheck passes. Asserting that it is absent is what
	// proves the refusal short-circuited. There is deliberately no flag on the
	// host's run(): it fires inside the returned tea.Cmd, which this test does
	// not execute, so such a flag would be false forever and would read as
	// evidence that it is not.
	joined := ""
	for _, b := range m.toasts {
		joined += b.Text + " | "
	}
	if !strings.Contains(joined, "cannot address that surface") {
		t.Fatalf("no notice explaining the refusal: %s", joined)
	}
	if strings.Contains(joined, "focusing pane") {
		t.Fatalf("a refused focus still announced itself as focusing: %s", joined)
	}
}

// A host that cannot name a surface must never be PROBED to find that out:
// available() can cost a subprocess, and the answer is already in the struct.
// The order is the whole point, so it is asserted rather than described.
func TestNamingHostLookupSkipsProbingOtherHosts(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	probes := 0
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{
			{
				name:      "no-namer",
				available: func() error { probes++; return nil },
				claim:     func(string) bool { return true },
				run:       func(string, string) error { return nil },
				// currentHandle deliberately nil
			},
			{
				name:          "namer",
				available:     func() error { return nil },
				claim:         func(string) bool { return true },
				currentHandle: func() (string, error) { return "workspace:1/pane:1", nil },
				run:           func(string, string) error { return nil },
			},
		}
	}
	ctl := activeSurfaceCtlNamingSurface()
	if ctl == nil || ctl.name != "namer" {
		t.Fatalf("selected %v, want the host that can name a surface", ctl)
	}
	if probes != 0 {
		t.Fatalf("a host with no currentHandle was probed %d times; the capability check must come first", probes)
	}
}

// The handle a host names is driven BY THAT HOST. A second host claiming the
// same namespace must not take the action over — otherwise "which host owns
// this" is decided by table order rather than by who produced it.
func TestFocusUsesTheHostThatNamedTheSurface(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	var producer, thief string
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{
			{
				name:      "producer",
				available: func() error { return nil },
				// Deliberately does NOT claim the handle it names. That is what
				// makes this test discriminating: activeSurfaceCtl picks the
				// first AVAILABLE host (producer), while surfaceCtlForHandle
				// picks the first CLAIMING one (thief). With pinning, the
				// producer still runs it; without it, the thief would.
				claim:         func(string) bool { return false },
				currentHandle: func() (string, error) { return "shared/surface:1", nil },
				run:           func(action, handle string) error { producer = action; return nil },
			},
			{
				name:      "thief",
				available: func() error { return nil },
				claim:     func(string) bool { return true },
				run:       func(action, handle string) error { thief = action; return nil },
			},
		}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "inproc", Status: SubagentDone}
	tm, cmd := m.runSubagentAction(m.Dialogs[0], row, "f")
	_ = tm.(Model)
	if cmd == nil {
		t.Fatal("f returned no command")
	}
	cmd() // the run is async: it happens when the command executes
	if producer != "switch" {
		t.Fatalf("the host that named the surface did not run it (producer=%q)", producer)
	}
	if thief != "" {
		t.Fatalf("another host stole the action: %q", thief)
	}
}

// The mirror case: a host that cannot say where it is must degrade to the
// old honest message, not to an invented handle.
func TestFocusHandleLessRowDegradesWhenHostCannotSay(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{
			name:          "testhost",
			available:     func() error { return nil },
			claim:         func(string) bool { return false },
			currentHandle: func() (string, error) { return "", errors.New("cannot tell") },
			run:           func(string, string) error { t.Fatal("run must not be called"); return nil },
		}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "inproc", Status: SubagentDone}
	tm, _ := m.runSubagentAction(m.Dialogs[0], row, "f")
	m = tm.(Model)
	if !strings.Contains(m.toasts[len(m.toasts)-1].Text, "in-process (no pane)") {
		t.Fatalf("expected the honest no-pane notice, got %+v", m.toasts)
	}
}

// SAFETY: the host-reported surface is the USER'S OWN window. `X` must never
// close it, however the row got there.
func TestDismissNeverClosesTheHostCurrentSurface(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	var got string
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{
			name:          "testhost",
			available:     func() error { return nil },
			claim:         func(h string) bool { return true },
			precheck:      func(string, string) error { return nil },
			currentHandle: func() (string, error) { return "workspace:1/pane:1", nil },
			run:           func(action, handle string) error { got = action + ":" + handle; return nil },
		}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	row := SubagentRow{ID: "p1", Name: "inproc", Status: SubagentActive, Interactive: true}
	tm, _ := m.runSubagentAction(m.Dialogs[0], row, "X")
	m = tm.(Model)
	if got != "" {
		t.Fatalf("X reached the host with %q; a handle-less row must never close the parent surface", got)
	}
	if !m.dismissed["p1"] {
		t.Fatalf("row was not dismissed: %v", m.dismissed)
	}
}

func TestRunSubagentActionPaneFocusClose(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	var got string
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{name: "testhost", available: func() error { return nil }, claim: func(string) bool { return true }, run: func(action, handle string) error {
			got = action + ":" + handle
			return nil
		}}}
	}
	m := subagentsTestModel(t)
	m.OpenSubagentHerd()
	d := m.Dialogs[0]
	row := SubagentRow{ID: "p1", Name: "pane", Status: SubagentActive, Interactive: true, Surface: "term_abc"}
	tm, cmd := m.runSubagentAction(d, row, "f")
	m = tm.(Model)
	if len(m.Dialogs) != 0 || cmd == nil {
		t.Fatalf("f: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
	if msg := cmd(); msg.(orcaTermDoneMsg).action != "switch" || got != "switch:term_abc" {
		t.Fatalf("f cmd = %+v ran %q", msg, got)
	}
	m2 := subagentsTestModel(t)
	m2.OpenSubagentHerd()
	tm, cmd = m2.runSubagentAction(m2.Dialogs[0], row, "X")
	m2 = tm.(Model)
	if len(m2.Subagents) != 2 || cmd == nil {
		t.Fatalf("X: rows=%d cmd=%v", len(m2.Subagents), cmd != nil)
	}
	if msg := cmd(); msg.(orcaTermDoneMsg).action != "close" || got != "close:term_abc" {
		t.Fatalf("X cmd = %+v ran %q", msg, got)
	}
	m3 := subagentsTestModel(t)
	m3.OpenSubagentHerd()
	stale := SubagentRow{ID: "c2", Name: "builder", Status: SubagentDone, Surface: "term_old"}
	got = ""
	tm, _ = m3.runSubagentAction(m3.Dialogs[0], stale, "X")
	m3 = tm.(Model)
	if got != "" {
		t.Fatalf("X on settled row should not close, ran %q", got)
	}
}

func TestUpdateOrcaTermDoneMsg(t *testing.T) {
	m := subagentsTestModel(t)
	n := len(m.toasts)
	tm, _ := m.Update(orcaTermDoneMsg{action: "switch", handle: "term_x", label: "pane focused"})
	m = tm.(Model)
	if len(m.toasts) != n+1 || !strings.Contains(m.toasts[n].Text, "pane focused") {
		t.Fatalf("toasts=%+v want +1", m.toasts)
	}
	tm, _ = m.Update(orcaTermDoneMsg{action: "close", handle: "term_x", err: errors.New("boom")})
	m = tm.(Model)
	if len(m.toasts) != n+2 || !m.toasts[n+1].Err {
		t.Fatalf("fail notice missing: %+v", m.toasts)
	}
	// No pane host: neutral notice (not an error) naming the host that was
	// asked, so a second implementation reports itself correctly.
	tm, _ = m.Update(orcaTermDoneMsg{action: "close", handle: "term_x", host: "cmuxish", err: errNoSurfaceCtl})
	m = tm.(Model)
	if len(m.toasts) != n+3 || m.toasts[n+2].Err {
		t.Fatalf("no-host notice should be neutral: %+v", m.toasts)
	}
	if !strings.Contains(m.toasts[n+2].Text, "no pane host installed") {
		t.Fatalf("no-host notice text = %q", m.toasts[n+2].Text)
	}
	// A real host failure names that host, not a hardcoded one. The retry
	// hint is the FAILING host's own discovery verb, and a host that binds
	// none gets no invented command at all — the old code appended
	// "<host> terminal list --json", which is Orca's verb on a cmux row.
	tm, _ = m.Update(orcaTermDoneMsg{
		action: "close", handle: "term_x", host: "cmuxish",
		hints: surfaceHints{list: "cmux list-panes --id-format both"},
		err:   errors.New("boom"),
	})
	m = tm.(Model)
	if !strings.Contains(m.toasts[n+3].Text, "cmuxish close failed") ||
		!strings.Contains(m.toasts[n+3].Text, "retry: cmux list-panes --id-format both") {
		t.Fatalf("failure notice should name the host and its own retry verb: %q", m.toasts[n+3].Text)
	}
	if strings.Contains(m.toasts[n+3].Text, "terminal list --json") {
		t.Fatalf("failure notice leaked another host's grammar: %q", m.toasts[n+3].Text)
	}
	tm, _ = m.Update(orcaTermDoneMsg{action: "close", handle: "pane:1", host: "cmuxish", err: errors.New("boom")})
	m = tm.(Model)
	if !strings.Contains(m.toasts[n+4].Text, "cmuxish close failed") ||
		strings.Contains(m.toasts[n+4].Text, "retry:") {
		t.Fatalf("a host with no discovery verb must get no invented retry: %q", m.toasts[n+4].Text)
	}
}

func TestTrackSubagentEndSurface(t *testing.T) {
	m := subagentsTestModel(t)
	m.trackSubagentStart("c9", "subagent", json.RawMessage(`{"name":"w","interactive":true,"task":"t"}`))
	m.trackSubagentEnd("c9", "subagent", nil, "Started subagent w (surface: term_abc123)", false)
	row, ok := subagentRowByID(m, "c9")
	if !ok || row.Surface != "term_abc123" || row.Status != SubagentActive {
		t.Fatalf("row = %+v %v", row, ok)
	}
	m.trackSubagentEnd("c9", "subagent", nil, "done (surface: term_x --evil)", false)
	row, _ = subagentRowByID(m, "c9")
	if row.Surface != "term_abc123" {
		t.Fatalf("bad surface overwrote: %+v", row)
	}
}

func TestSubagentXPromptHandleFallback(t *testing.T) {
	text, _ := subagentActionPrompt("x", SubagentRow{ID: "c1", Name: "w", Interactive: true, Surface: "term_abc"})
	if !strings.Contains(text, "--terminal term_abc --interrupt") || !strings.Contains(text, "kill -INT") {
		t.Errorf("prompt = %q", text)
	}
	text, _ = subagentActionPrompt("x", SubagentRow{ID: "c1", Name: "w", Interactive: true})
	// No handle means no host to consult, so no host grammar appears — and
	// no availability probe is spent building the prompt.
	if strings.Contains(text, "terminal send") || strings.Contains(text, "orca") {
		t.Errorf("handle-less prompt should stay host-free: %q", text)
	}
	if !strings.Contains(text, "kill -INT") {
		t.Errorf("handle-less prompt must still offer SIGINT: %q", text)
	}
	// A host with no recorded handle (cmux binds no interrupt verb) must not
	// be handed Orca's grammar: only the universal SIGINT path remains.
	text, _ = subagentActionPrompt("x", SubagentRow{ID: "c1", Name: "w", Interactive: true, Surface: "pane:3"})
	if strings.Contains(text, "terminal send") {
		t.Errorf("cmux row leaked Orca grammar: %q", text)
	}
	if !strings.Contains(text, "kill -INT") {
		t.Errorf("cmux row must still offer SIGINT: %q", text)
	}
}
