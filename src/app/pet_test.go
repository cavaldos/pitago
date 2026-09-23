package app

import (
	"testing"
	"time"

	"pitago/src/pirpc"
)

func TestPetSetNoop(t *testing.T) {
	var m Model
	m.pet.inTurn = true
	if cmd := m.petSet(petThinking); cmd == nil {
		t.Fatal("first transition must schedule tick cmd")
	}
	gen := m.pet.gen
	if cmd := m.petSet(petThinking); cmd != nil {
		t.Fatal("same-status repeat must be no-op (timer must not restart)")
	}
	if m.pet.gen != gen {
		t.Fatal("gen must not advance on no-op")
	}
	if m.pet.since.IsZero() || time.Since(m.pet.since) > time.Second {
		t.Fatal("busy transition must stamp since=now")
	}
}

func TestPetAnchorSettled(t *testing.T) {
	var m Model
	if cmd := m.petAnchor(); cmd == nil {
		t.Fatal("anchor from idle must go working")
	}
	if m.pet.status != petWorking || !m.pet.inTurn {
		t.Fatalf("anchor: %+v", m.pet)
	}
	// re-issued starts between rounds must not override thinking
	m.petSet(petThinking)
	if cmd := m.petAnchor(); cmd != nil {
		t.Fatal("anchor must not override thinking")
	}
	if m.pet.status != petThinking {
		t.Fatalf("anchor overrode thinking: %v", m.pet.status)
	}
	// settled without done(stop) — e.g. Esc-abort — decays quietly
	m.pet.sawStop = false
	if cmd := m.petSettled(); cmd != nil {
		t.Fatal("abort settle must go idle without cmds")
	}
	if m.pet.status != petIdle || m.pet.inTurn {
		t.Fatalf("abort settle: %+v", m.pet)
	}
	// settled after done(stop) flashes Done
	m.petAnchor()
	m.pet.sawStop = true
	if cmd := m.petSettled(); cmd == nil {
		t.Fatal("stop settle must schedule flash timer")
	}
	if m.pet.status != petSuccess {
		t.Fatalf("stop settle: %v", m.pet.status)
	}
	// flash must not be cut by a trailing settle
	if cmd := m.petSettled(); cmd != nil {
		t.Fatal("settle during flash must be ignored")
	}
}

func TestPetFlashGen(t *testing.T) {
	var m Model
	m.pet.inTurn = true
	m.petSet(petSuccess)
	gen := m.pet.gen
	updated, _ := m.Update(petFlashMsg{gen: gen - 1})
	m = updated.(Model)
	if m.pet.status != petSuccess {
		t.Fatal("stale flash timer must not cut a new status")
	}
	updated, _ = m.Update(petFlashMsg{gen: gen})
	m = updated.(Model)
	if m.pet.status != petIdle {
		t.Fatalf("current-gen flash must decay: %v", m.pet.status)
	}
}

func TestPetDeltas(t *testing.T) {
	var m Model
	m.tools = make(map[string]int)
	m.curAsst, m.curThink = -1, -1
	m.pet.inTurn = true
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"thinking_delta","delta":"hmm"}}`))
	if m.pet.status != petThinking {
		t.Fatalf("thinking_delta: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`))
	if m.pet.status != petWriting {
		t.Fatalf("text_delta: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"done","reason":"toolUse"}}`))
	if m.pet.status != petWriting {
		t.Fatalf("done(toolUse) must not change status: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"done","reason":"stop"}}`))
	if m.pet.status != petSuccess || !m.pet.sawStop {
		t.Fatalf("done(stop): %+v", m.pet)
	}
	// deltas outside a turn never pollute the status
	var m2 Model
	m2.tools = make(map[string]int)
	m2.curAsst, m2.curThink = -1, -1
	m2.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`))
	if m2.petLabel() != "Ready" || m2.petFace() == "" {
		t.Fatalf("out-of-turn delta must read as idle: %q %q", m2.petLabel(), m2.petFace())
	}
}

func TestPetError(t *testing.T) {
	var m Model
	m.tools = make(map[string]int)
	m.curAsst, m.curThink = -1, -1
	m.pet.inTurn = true
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"error"}}`))
	if m.pet.status != petError || m.pet.inTurn {
		t.Fatalf("stream error: %+v", m.pet)
	}
	var m2 Model
	m2.applyMessageEnd([]byte(`{"message":{"role":"assistant","stopReason":"error","errorMessage":"boom"}}`))
	if m2.pet.status != petError {
		t.Fatalf("message_end error: %v", m2.pet.status)
	}
}

func TestPetLabel(t *testing.T) {
	var m Model
	if got := m.petLabel(); got != "Ready" {
		t.Fatalf("idle label: %q", got)
	}
	m.pet.status = petWorking
	m.pet.since = time.Now().Add(-7 * time.Second)
	if got := m.petLabel(); got != "Working... 7s" {
		t.Fatalf("timed label: %q", got)
	}
	for st, faces := range petFaces {
		m.pet.status = st
		for i := 0; i < len(faces)+2; i++ {
			m.pet.tick = i
			if m.petFace() == "" {
				t.Fatalf("empty face %v tick %d", st, i)
			}
		}
	}
}

// A settle swallowed behind an open dialog must not stick the pet: the
// clock keeps ticking behind dialogs, and closing one re-checks get_state.
func TestPetTickPassesDialog(t *testing.T) {
	m := New(nil, t.TempDir())
	m.pet.status = petWorking
	m.pet.since = time.Now()
	m.Dialogs = []*Dialog{{Kind: "palette"}}
	um, _ := m.Update(petTickMsg{})
	m2 := um.(Model)
	if m2.pet.tick != 1 {
		t.Fatalf("tick behind dialog should advance, got %d", m2.pet.tick)
	}
	if len(m2.Dialogs) != 1 {
		t.Fatal("tick must not disturb the dialog")
	}
}

func TestStateRefreshSettlesMissedTurn(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = true
	m.Status = "pi is running…"
	m.pet.status = petWorking
	m.pet.inTurn = true
	um, _ := m.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x.jsonl"}})
	m2 := um.(Model)
	if m2.thinking || m2.Status != "ready" {
		t.Fatalf("idle pi should settle a stuck turn, thinking=%v status=%q", m2.thinking, m2.Status)
	}
	if m2.pet.status != petIdle || m2.pet.inTurn {
		t.Fatalf("pet should decay to idle: %+v", m2.pet)
	}
	// A running turn must not be disturbed.
	m3 := New(nil, t.TempDir())
	m3.thinking = true
	m3.pet.status = petWorking
	um, _ = m3.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x.jsonl", IsStreaming: true}})
	m4 := um.(Model)
	if !m4.thinking || m4.pet.status != petWorking {
		t.Fatal("streaming turn must stay working")
	}
}

func TestReconcileTurnCmdQuietWhenIdle(t *testing.T) {
	var m Model
	if m.ReconcileTurnCmd() != nil {
		t.Fatal("idle must not fetch")
	}
	m.thinking = true
	if m.ReconcileTurnCmd() != nil {
		t.Fatal("disconnected must not fetch")
	}
}
