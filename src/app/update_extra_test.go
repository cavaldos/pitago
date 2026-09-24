package app

import (
	"encoding/json"
	"testing"

	"pitago/src/pirpc"
)

// agent_end is a per-round boundary (pi emits agent_start…agent_end per
// round, agent_settled once when fully idle): it must NOT clear the running
// state, otherwise the footer flickers to ready/Idle between thinking and
// tool rounds of the same turn.
func TestAgentEndKeepsRunning(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = true
	m.Status = "pi is running…"
	m.pet.status = petWorking
	m.pet.inTurn = true
	um, _ := m.Update(piEventMsg{Event: pirpc.Event{Type: "agent_end", Raw: json.RawMessage(`{}`)}})
	m2 := um.(Model)
	if !m2.thinking || m2.Status != "pi is running…" {
		t.Fatalf("agent_end must keep running, thinking=%v status=%q", m2.thinking, m2.Status)
	}
	if m2.pet.status != petWorking || !m2.pet.inTurn {
		t.Fatalf("agent_end must keep pet working: %+v", m2.pet)
	}
	// …while the terminal settled event still clears everything.
	um, _ = m2.Update(piEventMsg{Event: pirpc.Event{Type: "agent_settled", Raw: json.RawMessage(`{}`)}})
	m3 := um.(Model)
	if m3.thinking || m3.Status != "ready" {
		t.Fatalf("settled should reset, thinking=%v status=%q", m3.thinking, m3.Status)
	}
}
