package extension

import (
	"encoding/base64"
	"encoding/json"
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

func TestAskUserOptionNormalizationAndResponse(t *testing.T) {
	payload, _ := json.Marshal(pirpc.SelectOption{Title: "Keep", Description: "Preserve compatibility"})
	encoded := pirpc.SelectOptionPrefix + base64.RawURLEncoding.EncodeToString(payload)
	req := pirpc.UIRequest{Method: "select", Options: []string{"Legacy", encoded}}

	if !pirpc.IsAskUserSelect(req) {
		t.Fatal("rich Ask User marker missing")
	}
	if got := OptionsFor(req); len(got) != 2 || got[0] != "Legacy" || got[1] != "Keep" {
		t.Fatalf("normalized options = %#v", got)
	}
	if got := DescriptionsFor(req); len(got) != 2 || got[0] != "" || got[1] != "Preserve compatibility" {
		t.Fatalf("descriptions = %#v", got)
	}
	resp := Response("r1", "select", 1, OptionsFor(req))
	if resp.Value == nil || *resp.Value != "Keep" || resp.Cancelled != nil {
		t.Fatalf("rich response = %+v", resp)
	}
}

func TestLegacySelectAndConfirmResponsesUnchanged(t *testing.T) {
	req := pirpc.UIRequest{Method: "select", Options: []string{"Alpha", "Beta"}}
	if pirpc.IsAskUserSelect(req) || len(DescriptionsFor(req)) != 0 {
		t.Fatal("legacy select unexpectedly marked rich")
	}
	selectResp := Response("r2", "select", 1, OptionsFor(req))
	if selectResp.Value == nil || *selectResp.Value != "Beta" || selectResp.Confirmed != nil {
		t.Fatalf("legacy select response = %+v", selectResp)
	}
	confirmResp := Response("r3", "confirm", 0, OptionsFor(pirpc.UIRequest{Method: "confirm"}))
	if confirmResp.Confirmed == nil || !*confirmResp.Confirmed || confirmResp.Value != nil {
		t.Fatalf("confirm response = %+v", confirmResp)
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
