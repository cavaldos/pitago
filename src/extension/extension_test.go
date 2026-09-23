package extension

import "testing"

func TestInputResponse(t *testing.T) {
	// Enter submits the typed value
	c := InputResponse("r1", "Fix bug", false)
	if c.Type != "extension_ui_response" || c.ID != "r1" {
		t.Fatalf("envelope = %+v", c)
	}
	if c.Cancelled != nil || c.Value == nil || *c.Value != "Fix bug" {
		t.Fatalf("submit = %+v", c)
	}
	// Esc cancels (empty value still submits — the extension treats it as back)
	c = InputResponse("r1", "", true)
	if c.Cancelled == nil || !*c.Cancelled || c.Value != nil {
		t.Fatalf("cancel = %+v", c)
	}
}

func TestShouldAutoCancel(t *testing.T) {
	// input drives creation flows (pi-tasks createTask) and must reach the
	// user; only the editor falls back to defaults/timeout.
	if ShouldAutoCancel("input") {
		t.Fatal("input must open a dialog, not auto-cancel")
	}
	if !ShouldAutoCancel("editor") {
		t.Fatal("editor must stay auto-cancelled")
	}
}
