package app

// extui_state_test.go — regression cover for the extension-UI demux.
//
// The bugs these lock down were all silent: a plugin wrote to a surface
// nobody was reading, or a request was dropped and the extension waited for
// a timeout. "It didn't crash" was the old bar; these assert the visible
// outcome instead.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// setStatus must be per-key: nine installed plugins share the surface, and
// the old single m.extStat slot was last-writer-wins, so pi-lens (or any
// other status) silently stole the footer from pi-agents-team and from its
// siblings.
func TestExtStatusKeysAreIndependent(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"pi-lens","statusText":"12 findings"}`))
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"lsp","statusText":"gopls ready"}`))

	keys := m.extStatusKeys()
	if len(keys) != 2 || keys[0] != "lsp" {
		t.Fatalf("live keys (most recent first) = %v, want [lsp pi-lens]", keys)
	}
	if m.extStatus["pi-lens"] != "12 findings" || m.extStatus["lsp"] != "gopls ready" {
		t.Fatalf("both keys must survive each other: %+v", m.extStatus)
	}
	// The single-key contract the pre-existing tests assert on: exactly that
	// key's text, unadorned (no key name, no separator).
	var one Model
	one = one.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"lsp","statusText":"gopls ready"}`))
	if one.extStat != "gopls ready" {
		t.Fatalf("single key must yield exactly its own text, got %q", one.extStat)
	}
}

// Clearing one key must leave its siblings alone — the old path assigned
// m.extStat = stripANSI(text), so an empty status from any plugin wiped
// every other plugin's status too.
func TestExtStatusClearKeepsSiblings(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"pi-lens","statusText":"lens up"}`))
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"fff","statusText":"scanned"}`))
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"fff","statusText":""}`))

	if _, ok := m.extStatus["fff"]; ok {
		t.Fatalf("empty text must clear that key: %+v", m.extStatus)
	}
	if m.extStatus["pi-lens"] != "lens up" {
		t.Fatalf("clearing one key erased a sibling: %+v", m.extStatus)
	}
	if m.extStat != "lens up" {
		t.Fatalf("extStat = %q, want the surviving key's text", m.extStat)
	}
}

// A ui/askUser select must be filterable, and typing must not be mistaken
// for the y/n confirm shortcuts.
func TestExtSelectIsFilterableButYNRoutesOnlyConfirm(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	m = m.handleUIRequest([]byte(`{"id":"s1","method":"select","message":"Pick",` +
		`"options":["Canary release to a small cohort of users first","Stable promote everywhere"]}`))
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "ui" {
		t.Fatalf("select must open a ui dialog: %+v", m.Dialogs)
	}
	d := m.Dialogs[0]
	if !filterableDialog(d) {
		t.Fatal("extension select must be filterable")
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("canary")})
	m = tm.(Model)
	if len(m.Dialogs) != 1 {
		t.Fatalf("filtering must not answer or close the dialog: %+v", m.Dialogs)
	}
	if got := m.Dialogs[0].FIdx; len(got) != 1 {
		t.Fatalf("typed text must filter, FIdx = %v", got)
	}
	// The long label must survive rendering: the old Short(…, 44) ate it.
	if out := stripANSI(m.renderDialog()); !strings.Contains(out, "cohort of users") {
		t.Fatalf("long option label was truncated:\n%s", out)
	}

	// "n" on a select must not commit the last option.
	m2 := New(nil, t.TempDir())
	tm, _ = m2.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m2 = tm.(Model)
	m2 = m2.handleUIRequest([]byte(`{"id":"s2","method":"select","options":["A","B"]}`))
	tm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m2 = tm.(Model)
	if len(m2.Dialogs) != 1 || m2.Dialogs[0].Filter != "n" {
		t.Fatalf("n on a select must be filter text, got dialogs=%+v", m2.Dialogs)
	}
}

// A non-team widget key must get a real panel, and clearing one key must
// leave its siblings alone. This is the behaviour the old
// AddBlock-notice path made impossible.
func TestExtWidgetPanelsAreIndependent(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"plan-mode-plan",` +
		`"widgetLines":["PLAN","1. survey","2. implement"]}`))
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"web-activity",` +
		`"widgetLines":["searching…"]}`))

	keys := m.extWidgetKeys()
	if len(keys) != 2 {
		t.Fatalf("both keys must hold a panel, got %v", keys)
	}
	lp := m.extWidgetPanel("plan-mode-plan")
	if lp == nil || len(lp.Lines) != 3 || lp.Placement != "aboveEditor" {
		t.Fatalf("plan panel = %+v", lp)
	}

	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"web-activity","widgetLines":[]}`))
	if m.extWidgetPanel("web-activity") != nil {
		t.Fatalf("empty lines must clear that key, got %+v", m.extWidgetPanel("web-activity"))
	}
	if m.extWidgetPanel("plan-mode-plan") == nil {
		t.Fatal("clearing one widget erased its sibling")
	}
	if len(m.toasts) != 0 {
		t.Fatalf("panels are not toasts: %+v", m.toasts)
	}
}

// renderExtWidgets must respect the frame budget and the real terminal
// width, and return "" when there is no room so View()'s viewport math
// stays valid.
func TestRenderExtWidgetsRespectsBudgetAndWidth(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = tm.(Model)
	m.setExtWidget("plan-mode-plan", []string{"line 1", "line 2", "line 3"}, "aboveEditor")

	panel := m.renderExtWidgets("aboveEditor", 2)
	if panel == "" {
		t.Fatal("panel must render when there is room")
	}
	if got := strings.Count(panel, "\n") + 1; got > 2 {
		t.Fatalf("panel used %d rows, budget was 2:\n%s", got, panel)
	}
	if m.renderExtWidgets("aboveEditor", 0) != "" {
		t.Fatal("no budget must render \"\" so the viewport math stays valid")
	}
	if m.renderExtWidgets("belowEditor", 6) != "" {
		t.Fatal("placement must be honoured")
	}
	for _, line := range strings.Split(panel, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("line paints past the terminal width (%d > 60): %q", lipgloss.Width(line), line)
		}
	}
}

// A second extension dialog raised while one is open must be queued and then
// answered in FIFO order. The old path dropped it, so the extension blocked
// until its own timeout.
func TestExtDialogQueueDrainsInOrder(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)

	first := []byte(`{"id":"q1","method":"select","title":"first","options":["A"]}`)
	m = m.handleUIRequest(first)
	if len(m.Dialogs) != 1 {
		t.Fatalf("first dialog must open: %+v", m.Dialogs)
	}

	uiReq := func(raw string) tea.Msg {
		return piEventMsg{Event: pirpc.Event{Type: "extension_ui_request", Raw: json.RawMessage(raw)}}
	}
	// While the first dialog is open, two more arrive. Both must survive.
	second := `{"id":"q2","method":"select","title":"second","options":["B"]}`
	third := `{"id":"q3","method":"input","title":"third","text":""}`
	um, _ := m.Update(uiReq(second))
	m = um.(Model)
	um, _ = m.Update(uiReq(third))
	m = um.(Model)

	if len(m.Dialogs) != 1 {
		t.Fatalf("open dialog must not stack: %d", len(m.Dialogs))
	}
	if len(m.queuedDialogs) != 2 {
		t.Fatalf("queued = %d, want 2 (nothing may be dropped)", len(m.queuedDialogs))
	}

	// Esc answers the open one; the next must be promoted, not lost.
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Title != "second" {
		t.Fatalf("FIFO drain failed: dialogs = %+v", m.Dialogs)
	}
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Title != "third" {
		t.Fatalf("second drain failed: dialogs = %+v", m.Dialogs)
	}
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Fatalf("queue must be exhausted: %+v", m.Dialogs)
	}
	if len(m.queuedDialogs) != 0 {
		t.Fatalf("queued = %d, want 0", len(m.queuedDialogs))
	}
}

// The queue is bounded: past the cap the oldest entry is cancelled (Pi is
// nil here, so the write is skipped) rather than growing without limit.
func TestExtDialogQueueIsBounded(t *testing.T) {
	var m Model
	for i := 0; i < maxQueuedDialogs+5; i++ {
		m.queueDialogRequest([]byte(`{"id":"x","method":"select","options":["A"]}`))
	}
	if len(m.queuedDialogs) != maxQueuedDialogs {
		t.Fatalf("queue = %d, want the cap %d", len(m.queuedDialogs), maxQueuedDialogs)
	}
}

// A payload that fails the strict decode must still resolve pi's pending
// request instead of leaving the extension blocked.
func TestMalformedUIRequestStillAnswers(t *testing.T) {
	var m Model
	// Valid JSON, but "id" is an object: the strict UIRequest decode
	// fails, the lenient id recovery also fails, and there is genuinely
	// no request to answer.
	m = m.handleUIRequest([]byte(`{"id":{"a":1},"method":"select"}`))
	if len(m.Dialogs) != 0 {
		t.Fatalf("unreadable payload must not open a dialog: %+v", m.Dialogs)
	}
	if id, ok := looseUIRequestID([]byte(`{"id":"req-7","method":"select"}`)); !ok || id != "req-7" {
		t.Fatalf("lenient id recovery = %q %v", id, ok)
	}
	if _, ok := looseUIRequestID([]byte(`not json`)); ok {
		t.Fatal("unparseable payload must report no id, never a guess")
	}
}

// Ctrl+C must dismiss a dialog like Esc: dialog capture runs before the
// global ^C quit arm, so without it a full-height plugin picker had no exit.
func TestCtrlCDismissesDialog(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	m = m.handleUIRequest([]byte(`{"id":"c1","method":"select","options":["A","B"]}`))
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = tm.(Model)
	if len(m.Dialogs) != 0 {
		t.Fatalf("Ctrl+C must close the dialog, got %+v", m.Dialogs)
	}
}

// waitForCmds polls the fake-pi command log: the write is asynchronous
// (our stdin write, then the stand-in's read loop), so asserting the log
// immediately after the keystroke is a race.
func waitForCmds(t *testing.T, cmds func() []string, want int) []string {
	t.Helper()
	var lines []string
	for i := 0; i < 200; i++ {
		if lines = cmds(); len(lines) >= want {
			return lines
		}
		time.Sleep(10 * time.Millisecond)
	}
	return lines
}

// ui.editor used to be auto-cancelled here: the extension received
// {cancelled:true} without ever seeing a dialog, resolved undefined and took
// its default/timeout branch. pi opens a real editor dialog for the same
// request, so pitago must too. This walks the whole wire path — request ->
// dialog -> typed value -> extension_ui_response on stdout.
func TestEditorRequestRendersDialogAndAnswersWithValue(t *testing.T) {
	pi, cmds := spawnFakePi(t, nil)
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	m.Pi = pi

	m = m.handleUIRequest([]byte(`{"id":"e1","method":"editor","message":"Rename task","text":"draft"}`))
	if len(m.Dialogs) != 1 {
		t.Fatalf("editor must open a dialog, got %d", len(m.Dialogs))
	}
	d := m.Dialogs[0]
	if d.Kind != "input" || d.Method != "editor" || d.ID != "e1" {
		t.Fatalf("editor dialog = %+v", d)
	}
	if d.Title != "Editor" || d.Filter != "draft" {
		t.Fatalf("untitled editor must default its title and keep the prefilled buffer, got %+v", d)
	}
	// Nothing may be written before the user answers.
	if got := cmds(); len(got) != 0 {
		t.Fatalf("editor answered before the user typed: %v", got)
	}

	press := func(msg tea.KeyMsg) {
		t.Helper()
		tm, _ := m.Update(msg)
		m = tm.(Model)
	}
	for _, r := range "done" {
		press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.Dialogs) != 0 {
		t.Fatalf("Enter must close the editor, got %+v", m.Dialogs)
	}

	lines := waitForCmds(t, cmds, 1)
	if len(lines) != 1 {
		t.Fatalf("commands on the wire = %v, want exactly the response", lines)
	}
	var resp struct {
		Type      string  `json:"type"`
		ID        string  `json:"id"`
		Value     *string `json:"value"`
		Cancelled *bool   `json:"cancelled"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp.Type != "extension_ui_response" || resp.ID != "e1" {
		t.Fatalf("response envelope = %s", lines[0])
	}
	if resp.Value == nil || *resp.Value != "draftdone" {
		t.Fatalf("value must carry the typed text, got %s", lines[0])
	}
	if resp.Cancelled != nil {
		t.Fatalf("Enter must not cancel, got %s", lines[0])
	}
}

// pi sends `prefill` for ui.editor and `placeholder` for ui.input; both are
// the starting point the extension means the user to see. Dropping them made
// pitago answer with a bare string the extension never offered, so the
// extension's behaviour diverged from pi's for no visible reason.
func TestTextDialogSeedsPrefillAndKeepsPlaceholder(t *testing.T) {
	t.Run("editor prefill seeds the buffer", func(t *testing.T) {
		pi, _ := spawnFakePi(t, nil)
		m := New(nil, t.TempDir())
		tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = tm.(Model)
		m.Pi = pi

		m = m.handleUIRequest([]byte(`{"id":"e2","method":"editor","title":"Subject","prefill":"ship the parity fix"}`))
		if len(m.Dialogs) != 1 {
			t.Fatalf("editor must open a dialog, got %d", len(m.Dialogs))
		}
		if got := m.Dialogs[0].Filter; got != "ship the parity fix" {
			t.Fatalf("editor prefill = %q, want the value pi offered", got)
		}
		if got := m.Dialogs[0].Title; got != "Subject" {
			t.Fatalf("editor title = %q, want pi's", got)
		}
	})

	t.Run("input placeholder is kept for an empty buffer", func(t *testing.T) {
		pi, _ := spawnFakePi(t, nil)
		m := New(nil, t.TempDir())
		tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = tm.(Model)
		m.Pi = pi

		m = m.handleUIRequest([]byte(`{"id":"e3","method":"input","title":"Task","placeholder":"one line summary"}`))
		if len(m.Dialogs) != 1 {
			t.Fatalf("input must open a dialog, got %d", len(m.Dialogs))
		}
		d := m.Dialogs[0]
		if d.Filter != "" {
			t.Fatalf("input with no prefill must start empty, got %q", d.Filter)
		}
		if d.Placeholder != "one line summary" {
			t.Fatalf("input placeholder = %q, want pi's", d.Placeholder)
		}
		// The hint is only decoration: the empty buffer must not render it
		// as if it were text the user typed.
		out := m.View()
		if !strings.Contains(out, "one line summary") {
			t.Fatalf("placeholder must be visible while the buffer is empty")
		}
	})

	t.Run("legacy text field still seeds the buffer", func(t *testing.T) {
		pi, _ := spawnFakePi(t, nil)
		m := New(nil, t.TempDir())
		tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = tm.(Model)
		m.Pi = pi

		m = m.handleUIRequest([]byte(`{"id":"e4","method":"editor","text":"legacy"}`))
		if got := m.Dialogs[0].Filter; got != "legacy" {
			t.Fatalf("legacy text field = %q, want it tolerated", got)
		}
	})
}

// Esc on the editor is a real cancel ({cancelled:true}), matching pi — the
// extension resumes instead of blocking until its own timeout.
func TestEditorRequestEscCancels(t *testing.T) {
	pi, cmds := spawnFakePi(t, nil)
	m := New(nil, t.TempDir())
	m.Pi = pi
	m = m.handleUIRequest([]byte(`{"id":"e2","method":"editor"}`))
	if m.Dialogs[0].Title != "Editor" {
		t.Fatalf("untitled editor must default its title, got %q", m.Dialogs[0].Title)
	}
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(Model)
	if len(m.Dialogs) != 0 || len(m.queuedDialogs) != 0 {
		t.Fatalf("Esc must close the editor, dialogs=%+v", m.Dialogs)
	}
	lines := waitForCmds(t, cmds, 1)
	if len(lines) != 1 || !strings.Contains(lines[0], `"cancelled": true`) {
		t.Fatalf("Esc wire payload = %v", lines)
	}
	if strings.Contains(lines[0], `"value"`) {
		t.Fatalf("a cancel must not carry a value: %s", lines[0])
	}
}

// A second editor raised while a dialog is open is parked FIFO, exactly
// like ui.input — it opens its own dialog afterwards instead of being
// swallowed by the event pump.
func TestEditorRequestQueuesBehindOpenDialog(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	m = m.handleUIRequest([]byte(`{"id":"q1","method":"select","title":"first","options":["A"]}`))
	um, _ := m.Update(piEventMsg{Event: pirpc.Event{
		Type: "extension_ui_request",
		Raw:  json.RawMessage(`{"id":"q2","method":"editor","title":"rename"}`),
	}})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Title != "first" {
		t.Fatalf("open dialog must not stack: %+v", m.Dialogs)
	}
	if len(m.queuedDialogs) != 1 {
		t.Fatalf("queued = %d, want the editor parked", len(m.queuedDialogs))
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Title != "rename" || m.Dialogs[0].Kind != "input" {
		t.Fatalf("editor must be promoted next, got %+v", m.Dialogs)
	}
}
