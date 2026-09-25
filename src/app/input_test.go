package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
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

func askOptionValue(t *testing.T, title, description string) string {
	t.Helper()
	raw, err := json.Marshal(pirpc.SelectOption{Title: title, Description: description})
	if err != nil {
		t.Fatal(err)
	}
	return pirpc.SelectOptionPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func TestAskUserDialogMappingFilteringAndRichRendering(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	raw := `{"id":"r1","method":"select","message":"Choose a rollout","options":[` +
		`"` + askOptionValue(t, "Canary", "Release to a small cohort first") + `",` +
		`"` + askOptionValue(t, "Stable", "Promote directly to all users") + `"]}`
	m = m.handleUIRequest([]byte(raw))
	if len(m.Dialogs) != 1 {
		t.Fatalf("dialogs = %d", len(m.Dialogs))
	}
	d := m.Dialogs[0]
	if d.Kind != "askUser" || d.Title != "Ask User" || len(d.Options) != 2 || len(d.Descs) != 2 {
		t.Fatalf("rich mapping = %+v", d)
	}
	if d.Options[0] != "Canary" || d.Descs[1] != "Promote directly to all users" {
		t.Fatalf("normalized details = %q / %q", d.Options, d.Descs)
	}

	wide := stripANSI(m.renderDialog())
	for _, want := range []string{"1. Canary", "2. Stable", "Canary", "Release to a small cohort first", "│", "↵ Enter to select"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide dialog missing %q:\n%s", want, wide)
		}
	}

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("stable")})
	m = tm.(Model)
	if got := m.Dialogs[0].FIdx; len(got) != 1 || got[0] != 1 {
		t.Fatalf("filtered indices = %v", got)
	}
	if out := stripANSI(m.renderDialog()); !strings.Contains(out, "2. Stable") || strings.Contains(out, "1. Canary") {
		t.Fatalf("filtered render:\n%s", out)
	}

	// The same private encoding collapses to one safe column on narrow
	// terminals, retaining both the selected title and its detail.
	m.winW = 64
	narrow := stripANSI(m.renderDialog())
	if strings.Contains(narrow, "▸ 2. Stable │ Stable") || !strings.Contains(narrow, "2. Stable") || !strings.Contains(narrow, "Promote directly to all users") {
		t.Fatalf("narrow fallback:\n%s", narrow)
	}
}

func TestAskUserDialogPreservesMultilineContextAndPendingHint(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = tm.(Model)
	value := askOptionValue(t, "Continue", "Keep the current plan")
	message := "Choose a rollout\n\nContext:\nFirst finding\nSecond finding"
	raw, err := json.Marshal(map[string]any{
		"id": "r1", "method": "select", "message": message, "options": []string{value},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = m.handleUIRequest(raw)
	m.Dialogs = append(m.Dialogs, &Dialog{Kind: "secret"}) // second request waits

	out := stripANSI(m.renderDialog())
	for _, want := range []string{"Choose a rollout", "Context:", "First finding", "Second finding", "(1 more dialogs pending)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Ask User dialog missing %q:\n%s", want, out)
		}
	}
}

func TestAskUserLongDescriptionStaysInsideFrame(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m = tm.(Model)
	long := strings.TrimSpace(strings.Repeat("detailed rollout guidance ", 40))
	value := askOptionValue(t, "Canary", long)
	raw, err := json.Marshal(map[string]any{
		"id": "r1", "method": "select", "message": "Choose\n\nContext:\nA long context", "options": []string{value},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = m.handleUIRequest(raw)

	out := stripANSI(m.renderDialog())
	if rows := len(strings.Split(out, "\n")); rows > m.winH-2 {
		t.Fatalf("long Ask User dialog is %d rows, frame budget is %d:\n%s", rows, m.winH-2, out)
	}
	if !strings.Contains(out, "detailed rollout guidance") || !strings.Contains(out, "…") {
		t.Fatalf("long description was not bounded with an overflow marker:\n%s", out)
	}
}

func TestAskUserEnterAndEscAreNilSafe(t *testing.T) {
	value := askOptionValue(t, "Continue", "Proceed")
	raw := `{"id":"r1","method":"select","options":["` + value + `"]}`

	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyEsc} {
		m := New(nil, t.TempDir())
		m = m.handleUIRequest([]byte(raw))
		tm, _ := m.Update(tea.KeyMsg{Type: key})
		m = tm.(Model)
		if len(m.Dialogs) != 0 {
			t.Fatalf("key %v left %d dialogs open", key, len(m.Dialogs))
		}
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
