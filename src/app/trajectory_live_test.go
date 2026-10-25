package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// trajLiveModel opens a /trajectory window and stubs the hidden live
// builtin, counting reloads and the scope each one asked for.
func trajLiveModel(t *testing.T, scopes *[]string) Model {
	t.Helper()
	m := Model{}
	m.UseBuiltins([]Builtin{{Name: BuiltinTrajectoryLive, Hidden: true,
		Run: func(_ *Model, scope string) tea.Cmd {
			*scopes = append(*scopes, scope)
			return func() tea.Msg { return nil }
		}}}, nil)
	um, _ := m.Update(TrajectoryMsg{Scope: "tools",
		Options: []string{"#01 a", "#02 b", "#03 c"},
		Descs:   []string{"t · tool", "t · tool", "t · tool"},
		Payload: []string{"A", "B", "C"},
		IDs:     []string{"e1", "e2", "e3"}, Newest: 2})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "trajectory" {
		t.Fatalf("trajectory window not opened: %+v", m.Dialogs)
	}
	return m
}

func trajLiveMsg(ids ...string) TrajectoryMsg {
	msg := TrajectoryMsg{Scope: "tools", Live: true}
	for _, id := range ids {
		msg.Options = append(msg.Options, "#"+id)
		msg.Descs = append(msg.Descs, "t · tool")
		msg.Payload = append(msg.Payload, "detail "+id)
		msg.IDs = append(msg.IDs, id)
	}
	msg.Newest = len(ids) - 1
	return msg
}

// A finished step reloads the open window with its own scope; steps that
// land while a reload is in flight coalesce into one follow-up reload.
func TestTrajLiveReloadsOnNewStep(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)

	nm, _ := m.handleEvent(pirpc.Event{Type: "message_update"})
	m = nm.(Model)
	if len(scopes) != 0 {
		t.Fatalf("streaming deltas must not reload, got %v", scopes)
	}
	nm, _ = m.handleEvent(pirpc.Event{Type: "message_end"})
	m = nm.(Model)
	if len(scopes) != 1 || scopes[0] != "tools" {
		t.Fatalf("message_end reloads = %v, want [tools]", scopes)
	}
	nm, _ = m.handleEvent(pirpc.Event{Type: "tool_execution_end"})
	m = nm.(Model)
	nm, _ = m.handleEvent(pirpc.Event{Type: "message_end"})
	m = nm.(Model)
	if len(scopes) != 1 {
		t.Fatalf("reload in flight must coalesce, got %v", scopes)
	}
	um, cmd := m.Update(trajLiveMsg("e1", "e2", "e3", "e4"))
	m = um.(Model)
	if cmd == nil || len(scopes) != 2 {
		t.Fatalf("dirty latch must run one follow-up reload, got %v", scopes)
	}
	um, cmd = m.Update(trajLiveMsg("e1", "e2", "e3", "e4"))
	m = um.(Model)
	if cmd != nil || len(scopes) != 2 || m.trajLoading {
		t.Fatalf("clean reload must stop, got %v loading=%v", scopes, m.trajLoading)
	}
}

// No window open: no reload.
func TestTrajLiveIdleWithoutWindow(t *testing.T) {
	var n int
	m := Model{}
	m.UseBuiltins([]Builtin{{Name: BuiltinTrajectoryLive, Hidden: true,
		Run: func(*Model, string) tea.Cmd { n++; return nil }}}, nil)
	m.handleEvent(pirpc.Event{Type: "message_end"})
	if n != 0 {
		t.Fatalf("reloaded with no trajectory window open")
	}
}

// The window opens on the newest step, which need not be the last row
// (branches): Newest picks it.
func TestTrajOpensOnNewest(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)
	if d := m.Dialogs[0]; d.Cursor != 2 {
		t.Fatalf("opened on cursor %d, want newest (2)", d.Cursor)
	}
	um, _ := Model{}.Update(TrajectoryMsg{Scope: "all", Options: []string{"a", "b", "c"},
		Descs: []string{"", "", ""}, Payload: []string{"", "", ""},
		IDs: []string{"x", "y", "z"}, Newest: 1})
	m = um.(Model)
	if d := m.Dialogs[0]; d.Cursor != 1 {
		t.Fatalf("opened on cursor %d, want Newest=1", d.Cursor)
	}
}

// A new step always pulls the cursor to it, even when the user moved away.
func TestTrajLiveJumpsToNewStep(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)
	m.Dialogs[0].Cursor = 0 // user browsed back to e1
	m.Dialogs[0].TrajOff = 4
	msg := trajLiveMsg("e1", "x9", "e2", "e3", "e4")
	msg.Newest = 1 // newest landed on another branch, mid-list
	um, _ := m.Update(msg)
	m = um.(Model)
	d := m.Dialogs[0]
	if got := d.TrajIDs[d.FIdx[d.Cursor]]; got != "x9" {
		t.Fatalf("cursor on %q, want newest x9", got)
	}
	if d.TrajOff != 0 {
		t.Errorf("new step must show from the top, TrajOff=%d", d.TrajOff)
	}
}

// A reload with no new step keeps the user's selection and scroll.
func TestTrajLiveKeepsSelectionWithoutNewStep(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)
	d := m.Dialogs[0]
	d.Cursor, d.TrajOff = 1, 2 // e2, scrolled
	msg := trajLiveMsg("e1", "e2", "e3")
	um, _ := m.Update(msg)
	m = um.(Model)
	d = m.Dialogs[0]
	if got := d.TrajIDs[d.FIdx[d.Cursor]]; got != "e2" || d.TrajOff != 2 {
		t.Fatalf("selection/scroll lost: %q off=%d", got, d.TrajOff)
	}
}

// When the filter hides the newest step, the last visible one is picked.
func TestTrajLiveNewestHiddenByFilter(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)
	d := m.Dialogs[0]
	d.Filter = "#e"
	d.Reindex()
	um, _ := m.Update(trajLiveMsg("e1", "e2", "e3", "zz"))
	m = um.(Model)
	d = m.Dialogs[0]
	if got := d.TrajIDs[d.FIdx[d.Cursor]]; got != "e3" {
		t.Fatalf("cursor on %q, want last visible e3", got)
	}
}

// A failed or empty reload keeps what the user is reading.
func TestTrajLiveErrorKeepsRows(t *testing.T) {
	var scopes []string
	m := trajLiveModel(t, &scopes)
	um, _ := m.Update(TrajectoryMsg{Scope: "tools", Live: true, Err: errTest})
	m = um.(Model)
	if len(m.Dialogs[0].Options) != 3 || len(m.blocks) != 0 {
		t.Fatalf("live error must be silent and keep rows: %v / %d blocks", m.Dialogs[0].Options, len(m.blocks))
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errTest = testErr("boom")
