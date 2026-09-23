package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The /tasks "Create task" flow drives two ui.input requests (subject,
// description). Auto-cancelling them resolved empty and bounced straight
// back to the menu — the dialog below is the fix.
func TestInputDialogTyping(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"r1","method":"input","title":"Task subject"}`))
	if len(m.Dialogs) != 1 {
		t.Fatalf("dialogs = %d, want 1", len(m.Dialogs))
	}
	d := m.Dialogs[0]
	if d.Kind != "input" || d.Title != "Task subject" {
		t.Fatalf("dialog = %+v", d)
	}
	typeKeys := func(s string) {
		t.Helper()
		tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
		m = tm.(Model)
	}
	press := func(msg tea.KeyMsg) {
		t.Helper()
		tm, _ := m.Update(msg)
		m = tm.(Model)
	}
	typeKeys("Fix bug")
	if m.Dialogs[0].Filter != "Fix bug" {
		t.Fatalf("typed = %q", m.Dialogs[0].Filter)
	}
	// space is its own key (KeySpace, not Runes) and must type literally
	press(tea.KeyMsg{Type: tea.KeySpace})
	typeKeys("now")
	if m.Dialogs[0].Filter != "Fix bug now" {
		t.Fatalf("after space = %q", m.Dialogs[0].Filter)
	}
	press(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.Dialogs[0].Filter != "Fix bug no" {
		t.Fatalf("after backspace = %q", m.Dialogs[0].Filter)
	}
	// rendered dialog shows the buffer with a cursor (not the option list)
	out := stripANSI(m.renderDialog())
	if !strings.Contains(out, "Fix bu") || !strings.Contains(out, "Task subject") {
		t.Fatalf("render missing buffer/title:\n%s", out)
	}
	// compact popup: narrow bare box even on a wide terminal
	m.winW, m.winH = 140, 40
	widest := 0
	for _, line := range strings.Split(stripANSI(m.renderInputBox()), "\n") {
		if w := lipgloss.Width(strings.TrimRight(line, " ")); w > widest {
			widest = w
		}
	}
	if widest > 66 {
		t.Fatalf("input popup too wide: %d cells", widest)
	}
	// untitled input still opens (defaults the title)
	m2 := New(nil, t.TempDir())
	m2 = m2.handleUIRequest([]byte(`{"id":"r2","method":"input"}`))
	if len(m2.Dialogs) != 1 || m2.Dialogs[0].Title != "Input" {
		t.Fatalf("untitled dialog = %+v", m2.Dialogs)
	}
}

// The input popup floats above the chat like /commands popups: the frame
// keeps chat + sidebar visible instead of swapping to a fullscreen modal.
func TestInputDialogFloatsOverChat(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	m.Todos = []TodoItem{{ID: "8", Content: "Old task", Status: TodoCompleted}}
	m = m.handleUIRequest([]byte(`{"id":"r1","method":"input","title":"Task subject"}`))
	out := stripANSI(m.View())
	if !strings.Contains(out, "Task subject") {
		t.Fatal("floating popup missing from View")
	}
	if !strings.Contains(out, "Old task") {
		t.Fatal("sidebar must stay visible behind the popup")
	}
}

// Opening the popup (and typing wrapping lines into it) must not grow the
// frame: the chat shrinks so the whole frame stays exactly winH rows,
// otherwise the terminal scrolls and the sidebar looks pushed up.
func TestInputDialogKeepsFrameHeight(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	m = m.handleUIRequest([]byte(`{"id":"r1","method":"input","title":"Task subject"}`))
	if n := len(strings.Split(m.View(), "\n")); n != 30 {
		t.Fatalf("frame is %d rows with popup open, want 30", n)
	}
	// long input wraps the box taller: frame still exactly winH
	for _, r := range "Làm rõ mục tiêu và phạm vi công việc cần thực hiện ngay" {
		tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = tm.(Model)
	}
	if n := len(strings.Split(m.View(), "\n")); n != 30 {
		t.Fatalf("frame is %d rows after wrapping input, want 30", n)
	}
}
