package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func pasteTestModel() Model {
	var m Model
	m.ta = textarea.New()
	m.ta.Focus()
	m.ta.SetWidth(80)
	m.tools = make(map[string]int)
	return m
}

func runPasteCmd(t *testing.T, m Model, secret bool) pasteDoneMsg {
	t.Helper()
	cmd := m.pasteCmd(secret)
	if cmd == nil {
		t.Fatal("pasteCmd returned nil")
	}
	msg, ok := cmd().(pasteDoneMsg)
	if !ok {
		t.Fatalf("pasteCmd msg = %T", cmd())
	}
	return msg
}

// Ctrl+V must be owned by pitago (multi-backend + visible errors), not the
// textarea's silent built-in paste.
func TestCtrlVOwned(t *testing.T) {
	m := pasteTestModel()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = nm.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+V produced no command")
	}
	if got := m.ta.Value(); got != "" {
		t.Fatalf("Ctrl+V must paste async, value = %q", got)
	}
}

// End-to-end: Ctrl+V → clipboard text lands in the input.
func TestPasteInserts(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "pasted text", nil }
	defer func() { clipRead = old }()

	m := pasteTestModel()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = nm.(Model)
	nm, _ = m.Update(cmd())
	m = nm.(Model)
	if got := m.ta.Value(); got != "pasted text" {
		t.Fatalf("value = %q", got)
	}
}

// Mid-line + multi-line paste splices correctly and parks the cursor.
func TestPasteSplice(t *testing.T) {
	m := pasteTestModel()
	m.ta.SetValue("abXXcd")
	m.ta.SetCursor(2) // after "ab"
	m.insertAtCursor("1\n23")
	if got := m.ta.Value(); got != "ab1\n23XXcd" {
		t.Fatalf("value = %q", got)
	}
	row, col := m.cursorPos()
	if row != 1 || col != 2 {
		t.Fatalf("cursor = (%d,%d), want (1,2)", row, col)
	}
}

// Backend failure must explain itself (old behavior: silent nothing).
func TestPasteErrorNotice(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "", errors.New("xclip not found") }
	defer func() { clipRead = old }()

	m := pasteTestModel()
	m.applyPaste(runPasteCmd(t, m, false))
	if len(m.toasts) != 1 || !m.toasts[0].Err {
		t.Fatalf("toasts = %+v", m.toasts)
	}
	if !strings.Contains(m.toasts[0].Text, "Cmd+V") {
		t.Fatalf("notice = %q", m.toasts[0].Text)
	}
}

func TestPasteEmptyNotice(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "", nil }
	defer func() { clipRead = old }()
	oldImg := clipImage
	clipImage = func() (string, error) { return "", errNoImage }
	defer func() { clipImage = oldImg }()

	m := pasteTestModel()
	m.applyPaste(runPasteCmd(t, m, false))
	if len(m.toasts) != 1 || strings.Contains(m.ta.Value(), " ") {
		t.Fatalf("toasts=%+v value=%q", m.toasts, m.ta.Value())
	}
}

// Whitespace-only clipboard content is content, not "empty": a copied
// block ending in a newline must land with that newline intact.
func TestPasteKeepsWhitespaceOnly(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "  \n", nil }
	defer func() { clipRead = old }()
	oldImg := clipImage
	clipImage = func() (string, error) { return "", errNoImage }
	defer func() { clipImage = oldImg }()

	m := pasteTestModel()
	m.applyPaste(runPasteCmd(t, m, false))
	if got := m.ta.Value(); got != "  \n" {
		t.Fatalf("value = %q, want %q", got, "  \n")
	}
	if len(m.toasts) != 0 {
		t.Fatalf("must not claim an empty clipboard: %+v", m.toasts)
	}
}

// A pasted block keeps its trailing newline (no trimming at the splice).
func TestPasteKeepsTrailingNewline(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "npm test\n", nil }
	defer func() { clipRead = old }()

	m := pasteTestModel()
	m.applyPaste(runPasteCmd(t, m, false))
	if got := m.ta.Value(); got != "npm test\n" {
		t.Fatalf("value = %q, want %q", got, "npm test\n")
	}
}

// Wayland must be read exactly like pi: --no-newline (no synthetic
// trailing newline) and --type text (no mime guessing).
func TestClipCandidatesWayland(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	cands := clipCandidates()
	if len(cands) == 0 {
		t.Fatal("no wayland candidate")
	}
	got := strings.Join(cands[0], " ")
	if got != "wl-paste --no-newline --type text" {
		t.Fatalf("wayland command = %q", got)
	}
}

// Ctrl+V inside /login secret dialog pastes the API key.
func TestPasteSecret(t *testing.T) {
	old := clipRead
	clipRead = func() (string, error) { return "gsk-key-123\n", nil }
	defer func() { clipRead = old }()

	m := pasteTestModel()
	m.Dialogs = []*Dialog{{Kind: "secret", Title: "API key"}}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = nm.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+V in secret dialog produced no command")
	}
	nm, _ = m.Update(cmd())
	m = nm.(Model)
	if m.Dialogs[0].Filter != "gsk-key-123" {
		t.Fatalf("filter = %q", m.Dialogs[0].Filter)
	}
}
