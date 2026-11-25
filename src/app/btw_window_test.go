package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	tea "pitago/src/tui/ev"

	"pitago/src/components/format"
)

// Under tviewrt the BTW window must stay painted: the generic native
// mirror would draw it as a fullscreen "Options · no matches" view that
// blocks the whole frame.
func TestBtwWindowStaysPainted(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 0 exchanges"]}`))
	if !m.PaintedDialog() {
		t.Fatal("the btw window must be painted, not mirrored natively")
	}
	if _, active := m.NativeSettings(); active {
		t.Fatal("the btw window must not be mirrored into the native settings view")
	}
}

// ↑/↓ scroll the transcript inside the box: older lines come into view with
// a below-marker, ↓ returns to the tail, the offset clamps so the oldest
// kept lines stay reachable, and Enter follows the incoming turn again.
func TestBtwWindowScrolls(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	lines := make([]string, 0, 30)
	for i := 0; i < 27; i++ {
		lines = append(lines, fmt.Sprintf("old-%02d", i))
	}
	lines = append(lines, "You: helo", "BTW: hi", "BTW · contextual · 1 exchange")
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 1 exchange"]}`))
	m.BtwLines = lines

	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyUp})
	m = um.(Model)
	if m.Dialogs[0].BtwOff != 1 {
		t.Fatalf("↑ must scroll one line, off = %d", m.Dialogs[0].BtwOff)
	}
	plain := format.StripANSI(m.renderBtwBox())
	if !strings.Contains(plain, "old-19") || !strings.Contains(plain, "(+1 below") {
		t.Errorf("scrolled window must show the older line and the below marker:\n%s", plain)
	}
	if strings.Contains(plain, "BTW · contextual") {
		t.Errorf("scrolled window must leave the newest line below the fold:\n%s", plain)
	}

	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyDown})
	m = um.(Model)
	plain = format.StripANSI(m.renderBtwBox())
	if m.Dialogs[0].BtwOff != 0 || !strings.Contains(plain, "BTW · contextual") || strings.Contains(plain, "below") {
		t.Errorf("↓ must return to the tail: off %d\n%s", m.Dialogs[0].BtwOff, plain)
	}

	for i := 0; i < 25; i++ {
		um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyUp})
		m = um.(Model)
	}
	if m.Dialogs[0].BtwOff != m.btwMaxOff() {
		t.Fatalf("offset must clamp at %d, got %d", m.btwMaxOff(), m.Dialogs[0].BtwOff)
	}
	plain = format.StripANSI(m.renderBtwBox())
	if !strings.Contains(plain, "old-00") {
		t.Errorf("fully scrolled window must show the oldest lines:\n%s", plain)
	}

	m.Dialogs[0].BtwOff = 3
	m.Dialogs[0].Filter = "next"
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if m.Dialogs[0].BtwOff != 0 || m.Dialogs[0].Filter != "" {
		t.Fatalf("a sent question must follow the incoming turn, off = %d filter = %q",
			m.Dialogs[0].BtwOff, m.Dialogs[0].Filter)
	}
	// Enter on an empty draft sends nothing: the scroll position stays.
	m.Dialogs[0].BtwOff = 2
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if m.Dialogs[0].BtwOff != 2 {
		t.Fatalf("empty-draft Enter must not move the view, off = %d", m.Dialogs[0].BtwOff)
	}
}

// The speaker labels ("You:"/"BTW:") escape the dim run: each is its own
// bright bold segment, so the thread reads like a chat.
func TestBtwWindowLabelsBright(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 1 exchange","You: helo","BTW: Hello! How can I help you?"]}`))
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	out := m.renderBtwBox()
	plain := format.StripANSI(out)
	for _, want := range []string{"You: helo", "BTW: Hello! How can I help you?"} {
		if !strings.Contains(plain, want) {
			t.Errorf("transcript lost %q\n%s", want, plain)
		}
	}
	bright := lipgloss.NewStyle().Bold(true).Foreground(cText)
	if n := strings.Count(out, bright.Render("You: ")); n != 1 {
		t.Errorf("the You label must be one bright segment, got %d\n%s", n, out)
	}
	if n := strings.Count(out, bright.Render("BTW: ")); n != 1 {
		t.Errorf("the BTW label must be one bright segment, got %d\n%s", n, out)
	}
}

// The btw plugin pushes its thread transcript under the reserved "btw"
// widget key; the first non-empty update opens the window (ask-user style).
func TestBtwWindowOpensOnWidgetUpdate(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 0 exchanges","No thread yet"]}`))
	if !m.btwOpen() {
		t.Fatal("first btw update must open the window")
	}
	if len(m.Dialogs) == 0 || m.Dialogs[0].Kind != "btw" {
		t.Fatalf("window must be the btw dialog, got %+v", m.Dialogs)
	}
	if len(m.BtwLines) != 2 {
		t.Fatalf("BtwLines = %v, want the pushed lines", m.BtwLines)
	}
	if m.extWidgetPanel("btw") != nil {
		t.Fatal("the btw key must not leak into the generic widget panels")
	}
}

// A sibling plugin's widget keeps the generic panel behaviour.
func TestBtwKeyIsReservedOnlyForBtw(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w2","method":"setWidget","widgetKey":"lens","widgetLines":["lens line"]}`))
	if m.extWidgetPanel("lens") == nil {
		t.Fatal("other widget keys must keep their generic panels")
	}
	if m.btwOpen() {
		t.Fatal("a foreign widget must not open the btw window")
	}
}

// Esc hides the window but keeps the content. While hidden, live turn
// updates refresh the content without reopening; only a forwarded /btw
// command (explicit user intent) brings the window back.
func TestBtwWindowEscHidesUntilBtwCommand(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 0 exchanges"]}`))

	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if m.btwOpen() {
		t.Fatal("Esc must hide the window")
	}
	if len(m.BtwLines) != 1 {
		t.Fatalf("content must survive hiding, got %v", m.BtwLines)
	}

	m = m.handleUIRequest([]byte(`{"id":"w2","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 1 exchange","You: helo","BTW: hi"]}`))
	if m.btwOpen() {
		t.Fatal("a hidden window must not reopen on live updates")
	}
	if len(m.BtwLines) != 3 {
		t.Fatalf("hidden updates must still refresh content, got %v", m.BtwLines)
	}

	m.ForwardExtensionCommand("/btw hi")
	if !m.btwOpen() {
		t.Fatal("an explicit /btw forward must reopen the window")
	}
}

// Empty lines are the plugin's "thread cleared" signal: close and forget.
func TestBtwWindowClearClosesAndForgets(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 1 exchange"]}`))
	m = m.handleUIRequest([]byte(`{"id":"w2","method":"setWidget","widgetKey":"btw","widgetLines":[]}`))
	if m.btwOpen() {
		t.Fatal("cleared thread must close the window")
	}
	if len(m.BtwLines) != 0 {
		t.Fatalf("cleared thread must forget content, got %v", m.BtwLines)
	}
}

// Enter forwards a side question through the /btw command the plugin
// already routes, and clears the draft. btwSubmitText keeps leading
// "/"-commands verbatim so /btw:clear typed in the window still works.
func TestBtwWindowEnterSendsQuestion(t *testing.T) {
	d := &Dialog{Kind: "btw", Filter: "  helo "}
	if got := btwSubmitText(d); got != "/btw helo" {
		t.Errorf("plain text = %q, want %q", got, "/btw helo")
	}
	d = &Dialog{Kind: "btw", Filter: "/btw:clear"}
	if got := btwSubmitText(d); got != "/btw:clear" {
		t.Errorf("command text = %q, want verbatim", got)
	}
	d = &Dialog{Kind: "btw", Filter: "   "}
	if got := btwSubmitText(d); got != "" {
		t.Errorf("empty draft = %q, want no submit", got)
	}

	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 0 exchanges"]}`))
	d = m.Dialogs[0]
	d.Filter = "helo"
	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("Enter must return the forward command")
	}
	if m.Dialogs[0].Filter != "" {
		t.Fatalf("draft must clear after send, got %q", m.Dialogs[0].Filter)
	}
}

// The window overlays the chat's tail rows instead of reserving them: the
// chat height and the frame's row count never shift, and the covered rows
// keep their chat text beside the window.
func TestBtwWindowFloatsOverChat(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	// Newest last, like the live conversation: older filler folds above,
	// the real exchange stays visible in the tail.
	lines := make([]string, 0, 43)
	for i := 0; i < 40; i++ {
		lines = append(lines, "filler")
	}
	lines = append(lines, "BTW · contextual · 1 exchange", "You: helo", "BTW: Hello! How can I help you?")
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"btw","widgetLines":["BTW · contextual · 1 exchange"]}`))
	m.Dialogs[0].Filter = "next question"
	// BtwLines beyond the box budget must fold, not disappear.
	m.BtwLines = lines

	out := m.renderBtwBox()
	plain := format.StripANSI(out)
	for _, want := range []string{"BTW", "You: helo", "BTW: Hello!", "next question", "(+", "Enter send · Esc hide"} {
		if !strings.Contains(plain, want) {
			t.Errorf("floating window missing %q\n%s", want, plain)
		}
	}

	// The window must overlay rows, never reserve them: the chat viewport
	// height is identical open and hidden.
	openVpH := m.chatVpHeight()
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if got := m.chatVpHeight(); got != openVpH {
		t.Errorf("the window must not shift the chat height: open %d vs hidden %d", openVpH, got)
	}
	hiddenRows := strings.Count(format.StripANSI(m.view()), "\n") + 1

	// The composed frame keeps the box AND the rest of the UI around it,
	// with the exact same row count: zero layout shift (overlayToasts
	// parity — toast.go).
	m.ForwardExtensionCommand("/btw hi")
	full := format.StripANSI(m.view())
	if !strings.Contains(full, "Enter send · Esc hide") {
		t.Errorf("composed frame must contain the floating BTW window:\n%s", full)
	}
	if rows := strings.Count(full, "\n") + 1; rows != hiddenRows {
		t.Errorf("the window must overlay rows, never add them: open %d rows vs hidden %d", rows, hiddenRows)
	}
}
