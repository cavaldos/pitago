package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// compactingModel is a model driving a real client over the command-logging
// fake pi, already inside a manual compaction.
func compactingModel(t *testing.T, env map[string]string) (Model, func() []string) {
	t.Helper()
	pi, log := spawnFakePi(t, env)
	m := New(pi, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = tm.(Model)
	m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": "manual"}))
	if !m.compacting {
		t.Fatal("compaction_start did not arm the latch")
	}
	return m, log
}

func commandTypes(log []string) []string {
	var out []string
	for _, line := range log {
		if typ := typeOfCommand(line); typ != "" {
			out = append(out, typ)
		}
	}
	return out
}

func typeOfCommand(line string) string {
	var cmd struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		return ""
	}
	return cmd.Type
}

// pi replaces the editor's Esc handler for a whole compaction
// (interactive-mode.js:2883-2887), so ONE Esc press must reach
// session.abortCompaction(). pitago has no compaction-only RPC, so it sends
// pi's generic `abort` (rpc-mode.js:327-329) — and nothing else: the
// compaction Esc never pulls queued text back, unlike the turn cancel.
func TestEscDuringCompactionSendsOnlyAbort(t *testing.T) {
	m, log := compactingModel(t, nil)
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("Esc while compacting must run a command")
	}
	msg, ok := cmd().(PiOpMsg)
	if !ok {
		t.Fatalf("the command must report a PiOpMsg, got %T", cmd())
	}
	if msg.Op != "abort-compaction" || msg.Err != nil {
		t.Errorf("msg = %+v, want a clean abort-compaction", msg)
	}
	if !m.compacting {
		t.Error("the latch must survive the command: pi's own compaction_end clears it")
	}
	got := commandTypes(log())
	if len(got) != 1 || got[0] != "abort" {
		t.Fatalf("pi saw %v, want exactly one abort and no clear_queue", got)
	}
}

// pi does not arm a double-press during a compaction: the first Esc IS the
// action. The turn double-press branch sits below the compaction branch, so a
// mid-turn compaction must not fall into "press Esc again to cancel…".
func TestEscDuringCompactionSkipsTheTurnCancelArm(t *testing.T) {
	m, log := compactingModel(t, nil)
	m.thinking = true
	m.escArm = time.Now() // already armed by an earlier stray Esc
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if m.Status == "press Esc again to cancel…" || m.Status == "press Esc again to cancel" {
		t.Errorf("the compaction abort must not use the turn arm: status = %q", m.Status)
	}
	if !m.escArm.IsZero() {
		t.Error("the turn cancel arm must be dropped when the compaction abort runs")
	}
	if cmd == nil {
		t.Fatal("Esc must still abort")
	}
	cmd()
	if got := commandTypes(log()); len(got) != 1 || got[0] != "abort" {
		t.Fatalf("pi saw %v, want one abort and no clear_queue", got)
	}
}

// pi's abort makes compaction_end{reason:"manual",aborted:true} arrive and
// THEN compact() rethrows (agent-session.js:2205-2223). The notice is the
// event's job; the command's error must not print a second one.
func TestEscCancelledCompactIsReportedOnce(t *testing.T) {
	m, _ := compactingModel(t, nil)
	m = feed(t, m, eventMsg(t, "compaction_end", map[string]any{
		"reason": "manual", "aborted": true, "willRetry": false,
	}))
	if got := m.LastNotice(); got != "Compaction cancelled" {
		t.Fatalf("notice = %q, want pi's own compaction_end wording", got)
	}
	if !m.compactAborted {
		t.Fatal("a cancelled /compact must set the latch so the RPC error is suppressed")
	}
	if len(m.toasts) != 1 {
		t.Errorf("the cancellation surfaced %d times, want once", len(m.toasts))
	}

	// The rethrown compact() error arrives next as the command's own report.
	um, _ := m.Update(PiOpMsg{Op: "compact", Err: errors.New("aborted")})
	after := um.(Model)
	if len(after.toasts) != 1 {
		t.Errorf("the rethrown error was reported again: toasts = %d", len(after.toasts))
	}
	if after.LastNotice() != "Compaction cancelled" {
		t.Errorf("notice = %q, want pi's single report", after.LastNotice())
	}
	// Without the latch (a genuine compaction failure) the error must still
	// be reported — the suppression is for a cancellation only.
	failed := handlePiOpForTest(t, Model{}, PiOpMsg{Op: "compact", Err: errors.New("compact failed")})
	if failed.LastNotice() != "compact failed" {
		t.Errorf("a real compaction failure must be reported: %q", failed.LastNotice())
	}
}

// The latch must not leak into the next compaction: a fresh compaction_start
// re-arms it, so a genuine failure is still reported.
func TestCompactAbortedLatchIsPerRun(t *testing.T) {
	m := Model{compactAborted: true}
	m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": "manual"}))
	if m.compactAborted {
		t.Error("a new compaction must clear the cancellation latch")
	}
	// An automatic compaction does not consume it either: only a cancelled
	// /compact leaves the suppression standing.
	auto := feed(t, Model{compactAborted: true}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "threshold", "aborted": true, "willRetry": false,
	}))
	if auto.compactAborted {
		t.Error("an auto-compaction abort is not the /compact error and must not suppress it")
	}
}

// History browse still wins: the first Esc leaves a recalled message and must
// not cancel the compaction behind it.
func TestClearHistInputWinsOverTheCompactionAbort(t *testing.T) {
	m, log := compactingModel(t, nil)
	m.hist = []string{"first", "second"}
	m.histIdx = 0
	m.ta.SetValue("first")
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if cmd != nil {
		t.Error("Esc while recalling history must not run a command")
	}
	if m.histBrowsing() {
		t.Error("the recalled message must be cleared")
	}
	if !m.compacting {
		t.Error("the compaction must still be running")
	}
	if got := commandTypes(log()); len(got) != 0 {
		t.Fatalf("pi saw %v, want no command", got)
	}
}

// The hint the indicator shows must stay true after all of the above: the Esc
// branch really is reached from the editor while compacting, and it sends
// pi's own abort (the door to abortCompaction()).
func TestCompactionCancelHintIsHonest(t *testing.T) {
	if !strings.Contains(compactionLabel("manual"), "(escape to cancel)") {
		t.Fatal("pi's hint wording changed")
	}
	m, log := compactingModel(t, nil)
	if top := topBorder(m); !strings.Contains(top, "(escape to cancel)") {
		t.Fatalf("top border = %q, want the cancel hint", top)
	}
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("the advertised Esc must do something")
	}
	um, _ = um.(Model).Update(cmd())
	if _, ok := um.(Model); !ok {
		t.Fatal("update must return a Model")
	}
	if got := commandTypes(log()); len(got) != 1 || got[0] != "abort" {
		t.Fatalf("pi saw %v, want the advertised abort", got)
	}
}

// handlePiOpForTest runs handlePiOp and returns the resulting Model.
func handlePiOpForTest(t *testing.T, m Model, msg PiOpMsg) Model {
	t.Helper()
	um, _ := m.handlePiOp(msg)
	next, ok := um.(Model)
	if !ok {
		t.Fatalf("handlePiOp returned %T, want Model", um)
	}
	return next
}
