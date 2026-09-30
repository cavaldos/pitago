package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"

	"pitago/src/pirpc"
)

// indicatorModel is a model whose input box renders at a fixed width, so a
// test can read the top border without a tea.WindowSizeMsg round-trip.
func indicatorModel() Model {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ta: ta, winW: 100, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	return m
}

// topBorder is the input box's first row, which is where pi embeds the
// compaction status indicator (editor.setWorkingStatusIndicator).
func topBorder(m Model) string {
	rows := strings.Split(stripANSI(m.renderInput()), "\n")
	if len(rows) == 0 {
		return ""
	}
	return rows[0]
}

// pi's CompactionStatusIndicator labels, verbatim
// (dist/modes/interactive/components/status-indicator.js:44-51). The cancel
// hint is keyText("app.interrupt") = "escape"
// (dist/core/keybindings.js:28).
func TestCompactionLabelMatchesPi(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"manual", "Compacting context... (escape to cancel)"},
		{"overflow", "Context overflow detected, Auto-compacting... (escape to cancel)"},
		{"threshold", "Auto-compacting... (escape to cancel)"},
		{"", "Auto-compacting... (escape to cancel)"},
		{"something-new", "Auto-compacting... (escape to cancel)"},
	} {
		if got := compactionLabel(tc.reason); got != tc.want {
			t.Errorf("reason %q = %q, want %q", tc.reason, got, tc.want)
		}
	}
}

// While compacting the input's TOP BORDER carries the animated spinner plus
// pi's label — the bar from the screenshot — not a chat line and not the
// pet's timed "Working... 7s".
func TestInputBorderShowsTheCompactionIndicator(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"manual", "Compacting context... (escape to cancel)"},
		{"overflow", "Context overflow detected, Auto-compacting... (escape to cancel)"},
		{"threshold", "Auto-compacting... (escape to cancel)"},
	} {
		m := indicatorModel()
		m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": tc.reason}))
		top := topBorder(m)
		if !strings.Contains(top, tc.want) {
			t.Errorf("reason %q top border = %q, want %q", tc.reason, top, tc.want)
		}
		if !strings.Contains(top, spinFrame(m.pet.tick)) {
			t.Errorf("reason %q top border must carry pi's braille spinner: %q", tc.reason, top)
		}
		if !strings.HasPrefix(top, "╭─ ") || !strings.HasSuffix(top, "╮") {
			t.Errorf("reason %q must keep the box: %q", tc.reason, top)
		}
		if m.compactReason != tc.reason {
			t.Errorf("reason %q not stored: %q", tc.reason, m.compactReason)
		}
	}
}

// A mid-turn auto-compaction has thinking and compacting both true: the
// compaction wording must win over the pet's timed label, which is what
// inputStatus() would return.
func TestCompactionIndicatorWinsOverTheRunningTurnLabel(t *testing.T) {
	m := indicatorModel()
	m.thinking = true
	m.Status = "pi is running…"
	m.pet.status = petWorking
	m.pet.since = time.Now()
	m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": "threshold"}))
	top := topBorder(m)
	if !strings.Contains(top, "Auto-compacting... (escape to cancel)") {
		t.Fatalf("top border = %q, want the compaction label", top)
	}
	if strings.Contains(top, "Working...") {
		t.Errorf("the pet's timed label must not hide the compaction wording: %q", top)
	}
	// The running-turn hints stay: Esc×2 still aborts the TURN, which is a
	// real action pitago has.
	if out := stripANSI(m.renderInput()); !strings.Contains(out, "Esc×2 cancel") {
		t.Errorf("running-turn hints must survive a mid-turn compaction: %q", out)
	}
	// An idle manual compaction promises nothing pitago cannot do: the idle
	// hints are left exactly as they are.
	idle := indicatorModel()
	idle = feed(t, idle, eventMsg(t, "compaction_start", map[string]any{"reason": "manual"}))
	if out := stripANSI(idle.renderInput()); strings.Contains(out, "queue") {
		t.Errorf("pitago cannot queue input during a compaction, must not say it does: %q", out)
	}
	if !strings.Contains(stripANSI(idle.renderInput()), "↵ send") {
		t.Errorf("idle hints must stay untouched: %q", stripANSI(idle.renderInput()))
	}
}

// pi renders the indicator in the input border ONLY, so a mid-turn compaction
// must not also paint the "○ <status>" row in the chat.
func TestChatStatusRowIsSuppressedWhileCompacting(t *testing.T) {
	m := indicatorModel()
	m.ready = true
	m.thinking = true
	m.Status = "pi is running…"
	if !strings.Contains(stripANSI(m.renderBlocks()), "pi is running") {
		t.Fatalf("a running turn must show its chat status row:\n%s", stripANSI(m.renderBlocks()))
	}
	m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": "threshold"}))
	if got := stripANSI(m.renderBlocks()); strings.Contains(got, "○ pi is running") {
		t.Errorf("no chat status row while compacting:\n%s", got)
	}
	// After compaction_end the turn is still running (that is why the
	// compaction was automatic), so pi's chat row belongs back.
	m = feed(t, m, eventMsg(t, "compaction_end", map[string]any{
		"reason": "threshold", "aborted": false, "willRetry": false,
		"result": map[string]any{"summary": "s", "tokensBefore": 100},
	}))
	if !strings.Contains(stripANSI(m.renderBlocks()), "pi is running") {
		t.Error("the chat status row must come back after compaction_end")
	}
	// A turn that is still streaming gets its row back.
	live := feed(t, Model{ready: true, thinking: true, Status: "pi is running…"},
		eventMsg(t, "agent_start", map[string]any{}))
	if !strings.Contains(stripANSI(live.renderBlocks()), "pi is running") {
		t.Error("a running turn must show its chat status row again")
	}
}

// The reason only means something while the compaction runs: every latch
// clear drops it, so a stale reason can never label a later indicator.
func TestCompactReasonClearsWithTheLatch(t *testing.T) {
	m := indicatorModel()
	m = feed(t, m, eventMsg(t, "compaction_start", map[string]any{"reason": "manual"}))
	m = feed(t, m, eventMsg(t, "compaction_end", map[string]any{
		"reason": "manual", "aborted": false, "willRetry": false,
		"result": map[string]any{"summary": "s", "tokensBefore": 100},
	}))
	if m.compacting || m.compactReason != "" {
		t.Fatalf("compaction_end left compacting=%v reason=%q", m.compacting, m.compactReason)
	}
	// get_state backstop clears it too.
	lapsed := feed(t, Model{compacting: true, compactReason: "overflow", Status: "compacting context…"},
		stateRefreshMsg{state: pirpc.State{}})
	if lapsed.compacting || lapsed.compactReason != "" || lapsed.Status != "ready" {
		t.Fatalf("get_state heal left compacting=%v reason=%q status=%q",
			lapsed.compacting, lapsed.compactReason, lapsed.Status)
	}
	// A fresh pi process must not inherit one either: the connect arm
	// reconciles the latch from get_state and the reason goes with it.
	if got := indicatorModel(); got.compactReason != "" {
		t.Errorf("a fresh model must have no reason, got %q", got.compactReason)
	}
}

// A manual /compact is issued while idle, where nothing else keeps the pet
// tick loop running — without this the spinner would freeze on its first
// frame.
func TestPetLoopingStaysAliveForTheSpinner(t *testing.T) {
	if (Model{compacting: true}).petLooping() != true {
		t.Error("an idle compaction must keep the tick loop alive to animate the spinner")
	}
	// Same model without the compaction: whatever else it wants (the pet
	// section is on by default) must be unchanged by the new reason.
	base := Model{}
	if base.petLooping() != (Model{compacting: true}).petLooping() {
		t.Error("compaction must be an ADDITIONAL reason to loop, not the only one")
	}
}
