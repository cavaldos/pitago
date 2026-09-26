package live

import (
	"strings"
	"testing"
)

func TestBridgeSourceIsTheEmbeddedExtension(t *testing.T) {
	src := BridgeSource()
	if src == "" {
		t.Fatal("embedded bridge source is empty")
	}
	// The file is only useful if it is the real extension: it must bind the
	// loopback SSE server and publish a descriptor that Discover can parse.
	for _, want := range []string{"createServer", "127.0.0.1", "startedAt", "extension_ui_request"} {
		if !strings.Contains(src, want) {
			t.Fatalf("embedded bridge source does not contain %q", want)
		}
	}
}

// The tap must key on pi's camelCase UI method (setEditorText) and EMIT the
// wire spelling (set_editor_text) that pitago's Go side parses. Tapping the
// wire spelling matches nothing on pi's UI object, so remote editor prefills
// silently never reach a follower — a dead key that only shows up at runtime.
func TestBridgeTapKeysEditorTextMethodAndEmitsWireName(t *testing.T) {
	src := BridgeSource()
	if !strings.Contains(src, "setEditorText: (a) => ({ method: \"set_editor_text\"") {
		t.Fatal("the tap does not key setEditorText and emit the set_editor_text wire method")
	}
	if strings.Contains(src, "set_editor_text: (a)") {
		t.Fatal("the tap still keys the wire spelling, which pi's UI object does not expose")
	}
}

// The picker identifies a session by more than a pid, so the descriptor must
// carry the name and model. The tap and follow-mode re-tap are the behaviour
// most easily broken by an edit, so they are asserted by source too.
func TestBridgeStillFollowsAndRetapsOnEveryEvent(t *testing.T) {
	src := BridgeSource()
	if !strings.Contains(src, "sessionName") || !strings.Contains(src, "model:") {
		t.Fatal("descriptor does not publish sessionName/model for the picker")
	}
	if !strings.Contains(src, "setEditorText:") {
		t.Fatal("the follow-mode editor re-tap is gone")
	}
}
