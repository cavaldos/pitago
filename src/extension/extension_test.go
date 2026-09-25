package extension

import (
	"testing"

	"pitago/src/pirpc"
)

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

func TestSummarizeCountsPitagoAsBuiltin(t *testing.T) {
	ext, _, _, bin := Summarize([]pirpc.RepoCommand{
		{Name: "model", Source: "builtin"},
		{Name: "recent", Source: "pitago"},
		{Name: "mcp", Source: "extension"},
	})
	if bin != 2 || ext != 1 {
		t.Fatalf("pitago must fold into builtin, got builtin=%d ext=%d", bin, ext)
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

func TestMethodClassifiers(t *testing.T) {
	// pi's RPC extension-UI subprotocol: dialogs block for a response,
	// fire-and-forget never does. A method must be exactly one of them.
	for _, m := range []string{"select", "confirm", "input", "editor"} {
		if !IsDialogMethod(m) || IsFireAndForget(m) {
			t.Fatalf("%s must be dialog-only", m)
		}
	}
	for _, m := range []string{"notify", "setStatus", "setWidget", "setTitle", "set_editor_text"} {
		if !IsFireAndForget(m) || IsDialogMethod(m) {
			t.Fatalf("%s must be fire-and-forget-only", m)
		}
	}
}

func TestFallbackResponse(t *testing.T) {
	// Unknown future methods cancel safely: dialog callers get
	// undefined/false and use defaults; pi ignores responses with no
	// pending request, so fire-and-forget-likes stay harmless.
	c := FallbackResponse("r9")
	if c.Type != "extension_ui_response" || c.ID != "r9" {
		t.Fatalf("envelope = %+v", c)
	}
	if c.Cancelled == nil || !*c.Cancelled {
		t.Fatalf("must cancel = %+v", c)
	}
}
