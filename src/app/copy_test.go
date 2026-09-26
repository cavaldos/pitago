package app

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubClip swaps the clipboard write for a recorder, restoring it after.
func stubClip(t *testing.T, fn func(string) error) *[]string {
	t.Helper()
	var got []string
	old := clipWrite
	clipWrite = func(text string) error {
		got = append(got, text)
		if fn != nil {
			return fn(text)
		}
		return nil
	}
	t.Cleanup(func() { clipWrite = old })
	return &got
}

// copyableDialog builds a dialog of the given kind with payload rows.
// Options must carry the real display text: Reindex filters on Options,
// so empty rows would make every filter match nothing.
func copyableDialog(kind string, payload ...string) *Dialog {
	d := &Dialog{Kind: kind, Payload: payload}
	for _, p := range payload {
		d.Options = append(d.Options, Short(p, 60))
	}
	d.Reindex()
	return d
}

func TestDialogCopyableKinds(t *testing.T) {
	for _, kind := range []string{"trajectory", "notification"} {
		if !dialogCopyable(copyableDialog(kind, "x")) {
			t.Errorf("%s must be copyable", kind)
		}
	}
	// "yank" copies on Enter already, and Ctrl+Y there would shadow the
	// global yank-last action.
	for _, kind := range []string{"yank", "model", "sessions", "login", ""} {
		if dialogCopyable(copyableDialog(kind, "x")) {
			t.Errorf("%s must not claim Ctrl+Y", kind)
		}
	}
}

func TestCtrlYCopiesSelectedRow(t *testing.T) {
	// Newlines and length prove the copy uses Payload, not the one-line
	// display row.
	const full = "multi\nline\npayload that outlives any display truncation"
	for _, kind := range []string{"trajectory", "notification"} {
		got := stubClip(t, nil)
		m := Model{Dialogs: []*Dialog{copyableDialog(kind, full, "second")}}
		um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
		mm := um.(Model)
		if len(*got) != 1 || (*got)[0] != full {
			t.Fatalf("%s: clipboard = %q, want the full selected payload", kind, *got)
		}
		if !strings.Contains(mm.copyHint, "copied") {
			t.Errorf("%s: footer hint missing: %q", kind, mm.copyHint)
		}
		if cmd == nil {
			t.Errorf("%s: want a cmd that retires the hint", kind)
		}
		// The dialog must survive a copy so browsing can continue.
		if len(mm.Dialogs) != 1 {
			t.Errorf("%s: copy closed the dialog", kind)
		}
	}
}

func TestCtrlYCopiesCursorRow(t *testing.T) {
	got := stubClip(t, nil)
	d := copyableDialog("notification", "alpha", "beta", "gamma")
	m := Model{Dialogs: []*Dialog{d}}
	// Move the cursor down twice, then copy.
	for i := 0; i < 2; i++ {
		um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyDown})
		m = um.(Model)
	}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	if len(*got) != 1 || (*got)[0] != "gamma" {
		t.Fatalf("copied %q, want the row under the cursor (%q)", *got, "gamma")
	}
	if len(um.(Model).Dialogs) != 1 {
		t.Fatalf("copy closed the dialog")
	}
}

func TestCtrlYRespectsFilter(t *testing.T) {
	got := stubClip(t, nil)
	d := copyableDialog("notification", "keep me", "drop me")
	d.Filter = "keep"
	d.Reindex()
	m := Model{Dialogs: []*Dialog{d}}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	mm := um.(Model)
	if len(*got) != 1 || (*got)[0] != "keep me" {
		t.Fatalf("copied %q, want the filtered row %q", *got, "keep me")
	}
	if mm.Dialogs[0].Cursor >= len(mm.Dialogs[0].FIdx) {
		t.Fatalf("copy must resolve through FIdx, cursor %d is out of range", mm.Dialogs[0].Cursor)
	}
}

func TestCtrlYOnNonCopyableDialogIsNoop(t *testing.T) {
	got := stubClip(t, nil)
	m := Model{Dialogs: []*Dialog{copyableDialog("yank", "picked")}}
	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	mm := um.(Model)
	if len(*got) != 0 {
		t.Fatalf("clipboard touched on a non-copyable dialog: %q", *got)
	}
	if cmd != nil {
		t.Fatalf("unexpected cmd on a non-copyable dialog")
	}
	if mm.copyHint != "" {
		t.Fatalf("hint raised on a non-copyable dialog: %q", mm.copyHint)
	}
}

func TestCtrlYEmptySelectionIsQuiet(t *testing.T) {
	got := stubClip(t, nil)
	m := Model{Dialogs: []*Dialog{copyableDialog("notification")}}
	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	if len(*got) != 0 {
		t.Fatalf("empty dialog wrote %q to the clipboard", *got)
	}
	if cmd != nil {
		t.Fatalf("empty dialog scheduled a hint timer")
	}
	if um.(Model).copyHint != "" {
		t.Fatalf("empty dialog raised a hint")
	}
}

func TestCopyFailureIsReportedInFooter(t *testing.T) {
	stubClip(t, func(string) error { return errors.New("no clipboard") })
	m := Model{Dialogs: []*Dialog{copyableDialog("notification", "text")}}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	hint := um.(Model).copyHint
	if !strings.Contains(hint, "no clipboard") {
		t.Fatalf("hint = %q, want the failure reason", hint)
	}
}

// A second copy must not be wiped by the first one's timer: gen guards it.
//
// This drives Model.Update on purpose. While a dialog is open, Update
// runs an allowlist and swallows every unlisted message, so a retirement
// message missing from that list leaves the hint stuck on screen until the
// dialog is dismissed. Going through Update is what catches that.
func TestCopyHintGenerationGuardsTimer(t *testing.T) {
	stubClip(t, nil)
	m := Model{Dialogs: []*Dialog{copyableDialog("notification", "one", "two")}}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	first := um.(Model)
	um, _ = first.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlY})
	second := um.(Model)
	if first.copyGen == second.copyGen {
		t.Fatalf("gen must advance per copy (%d)", second.copyGen)
	}
	// The stale timer (gen 1) must be ignored.
	um, _ = second.Update(clearCopyHintMsg{gen: first.copyGen})
	if um.(Model).copyHint == "" {
		t.Fatalf("stale timer cleared the newer hint")
	}
	// The current timer retires it.
	um, _ = second.Update(clearCopyHintMsg{gen: second.copyGen})
	if um.(Model).copyHint != "" {
		t.Fatalf("current timer did not clear the hint")
	}
}

func TestDialogFootPrefersHint(t *testing.T) {
	m := Model{copyHint: "✓ copied 5 chars"}
	if got := m.dialogFoot("type to filter"); got != "✓ copied 5 chars" {
		t.Fatalf("live hint must win, got %q", got)
	}
	if got := (Model{}).dialogFoot("type to filter"); got != "type to filter" {
		t.Fatalf("no hint must pass the key hints through, got %q", got)
	}
}

// The notification list truncates rows for display, so the copy source has
// to be Payload or long messages would copy truncated.
func TestNotificationPayloadIsNotTruncated(t *testing.T) {
	long := strings.Repeat("long-error-message ", 20) // > the 180-cell display cut
	m := Model{notificationHistory: []Toast{{Text: long}}}
	m.OpenNotifications("")
	d := m.Dialogs[0]
	if len(d.Payload) != 1 || d.Payload[0] != long {
		t.Fatalf("payload was truncated to %d cells, want the full %d", len(d.Payload[0]), len(long))
	}
	if len(d.Options[0]) >= len(long) {
		t.Fatalf("row should still be truncated for display, got %d cells", len(d.Options[0]))
	}
}
