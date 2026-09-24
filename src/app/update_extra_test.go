package app

import (
	"encoding/json"
	"testing"

	"pitago/src/pirpc"
)

// agent_end must clear the running state (no stuck "running" UI).
func TestAgentEndResetsRunning(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = true
	m.Status = "pi is running…"
	m.pet.status = petWorking
	m.pet.inTurn = true
	um, _ := m.Update(piEventMsg{Event: pirpc.Event{Type: "agent_end", Raw: json.RawMessage(`{}`)}})
	m2 := um.(Model)
	if m2.thinking || m2.Status != "ready" || !m2.escArm.IsZero() {
		t.Fatalf("agent_end should reset, thinking=%v status=%q", m2.thinking, m2.Status)
	}
	if m2.pet.status != petIdle {
		t.Fatalf("pet should decay to idle, got %v", m2.pet.status)
	}
}
