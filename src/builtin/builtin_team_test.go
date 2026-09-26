package builtin

// Tests for the /team builtin wiring. /team is pitago's own command, not a
// get_commands row, so these pin the two things the bug report turned on:
// the arg contract is unchanged, and a dispatch is always observable (the
// old bare ForwardExtensionCommand left no status, no spinner and no
// deadline, so a stalled or buffered reply was indistinguishable from a
// dead command).

import (
	"testing"

	"pitago/src/app"
)

func teamBuiltin(t *testing.T) app.Builtin {
	t.Helper()
	for _, c := range All() {
		if c.Name == "team" {
			return c
		}
	}
	t.Fatal("builtin /team missing from the registry")
	return app.Builtin{}
}

// The single worker-id contract is unchanged: at most one field is a
// dispatch, anything longer is rejected before the RPC is touched.
func TestTeamCommandKeepsSingleWorkerIDArg(t *testing.T) {
	b := teamBuiltin(t)
	m := &app.Model{}

	if cmd := b.Run(m, "w1 extra"); cmd != nil {
		t.Error("two or more fields must be rejected, not dispatched")
	}
	if m.Status != "" {
		t.Errorf("a rejected /team must not change status, got %q", m.Status)
	}

	m2 := &app.Model{}
	if cmd := b.Run(m2, "w1"); cmd == nil {
		t.Error("a single worker-id must dispatch")
	}
	if cmd := b.Run(&app.Model{}, "  w1  "); cmd == nil {
		t.Error("surrounding whitespace must be trimmed, not treated as two fields")
	}
}

// Dispatch must arm the observable lifecycle. Status is the only field
// readable from this package, and it is exactly what the old bare forward
// left untouched — so this fails if someone reverts to
// ForwardExtensionCommand.
func TestTeamCommandArmsObservableLifecycle(t *testing.T) {
	b := teamBuiltin(t)
	m := &app.Model{}

	cmd := b.Run(m, "")
	if cmd == nil {
		t.Fatal("/team must dispatch")
	}
	if m.Status == "" {
		t.Error("/team must set an in-flight status; a silent forward is the reported bug")
	}
}
