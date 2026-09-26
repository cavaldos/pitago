package app

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// typesInLog lists the command types pi received, in order.
func typesInLog(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		var c struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			continue
		}
		out = append(out, c.Type)
	}
	return out
}

func countType(lines []string, typ string) int {
	n := 0
	for _, t := range typesInLog(lines) {
		if t == typ {
			n++
		}
	}
	return n
}

// A prompt pi refuses because it is still streaming must not cost the user
// their message: pitago queues it as a follow-up (pi's own Alt+Enter
// behaviour) instead of reporting a dead end.
func TestBusyPromptIsHealedIntoAFollowUp(t *testing.T) {
	pi, log := spawnFakePi(t, map[string]string{"FAKE_PI_BUSY": "1"})
	m := New(pi, t.TempDir())
	m.ta.SetValue("are you still there?")

	msg, ok := m.submitInput()().(sentAckMsg)
	if !ok {
		t.Fatal("submit must report a sentAckMsg")
	}
	if msg.err != nil {
		t.Fatalf("a busy refusal must heal, not fail: %v", msg.err)
	}
	if !msg.queued {
		t.Error("the message must land in pi's follow-up queue, not be dropped")
	}
	lines := log()
	if countType(lines, "follow_up") != 1 {
		t.Fatalf("expected exactly one follow_up, got %v", typesInLog(lines))
	}
	if countType(lines, "prompt") != 1 {
		t.Fatalf("expected the original prompt then the healing follow_up, got %v", typesInLog(lines))
	}
	var last struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Type != "follow_up" || last.Message != "are you still there?" {
		t.Errorf("healed send = %q %q, want the submitted text as a follow_up", last.Type, last.Message)
	}
}

// A send pi refuses for any other reason gives the text back: the input is
// emptied on submit, so without this the user's words would vanish.
func TestRefusedSendRestoresTheText(t *testing.T) {
	pi, _ := spawnFakePi(t, map[string]string{"FAKE_PI_PROMPT_FAIL": "1"})
	m := New(pi, t.TempDir())
	m.ta.SetValue("keep this text")

	msg := m.submitInput()().(sentAckMsg)
	if msg.err == nil {
		t.Fatal("a refused prompt must surface as an error")
	}
	um, _ := m.Update(msg)
	got := um.(Model)
	if got.ta.Value() != "keep this text" {
		t.Errorf("editor after a refused send = %q, want the text back", got.ta.Value())
	}
	if !strings.Contains(lastNotice(got), "refused") {
		t.Errorf("pi's reason must be shown, notices = %q", lastNotice(got))
	}
	if got.thinking {
		t.Error("a refused send must not leave the TUI in the running state")
	}
}

// lastNotice returns the text of the most recent notice. "notice" blocks
// are routed to the toast stack (AddBlock), not the transcript.
func lastNotice(m Model) string {
	for i := len(m.toasts) - 1; i >= 0; i-- {
		if m.toasts[i].Text != "" {
			return m.toasts[i].Text
		}
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "notice" {
			return m.blocks[i].Text
		}
	}
	return ""
}

// Alt+Enter while streaming queues a follow-up (pi parity: prompt(text,
// {streamingBehavior:'followUp'})), and only that — no steer, no plain
// prompt.
func TestAltEnterQueuesAFollowUpWhileStreaming(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.thinking = true
	m.ta.SetValue("queue me")

	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("Alt+Enter must submit the input")
	}
	if msg := cmd().(sentAckMsg); !msg.queued || msg.err != nil {
		t.Fatalf("Alt+Enter while streaming = %+v, want a queued follow-up", msg)
	}
	lines := log()
	if countType(lines, "follow_up") != 1 || countType(lines, "prompt") != 0 {
		t.Errorf("commands = %v, want exactly one follow_up", typesInLog(lines))
	}
}

// Idle, Alt+Enter is an ordinary submit (pi: handleFollowUp falls through to
// onSubmit).
func TestAltEnterIsAPlainSubmitWhileIdle(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.ta.SetValue("just a normal message")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if cmd == nil {
		t.Fatal("Alt+Enter must submit the input")
	}
	if msg := cmd().(sentAckMsg); msg.err != nil || msg.queued {
		t.Fatalf("idle Alt+Enter = %+v, want a plain prompt", msg)
	}
	lines := log()
	if countType(lines, "prompt") != 1 || countType(lines, "follow_up") != 0 {
		t.Errorf("commands = %v, want exactly one prompt", typesInLog(lines))
	}
}

// "!cmd" runs through pi's bash RPC — pi owns the shell, the cwd and the
// session entry; pitago never spawns a shell of its own.
func TestBangCommandRunsThroughPi(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.ta.SetValue("!echo hi")

	cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("!cmd must run")
	}
	msg := cmd().(PiOpMsg)
	if msg.Err != nil {
		t.Fatalf("bash: %v", msg.Err)
	}
	if msg.Bash == nil || msg.Bash.Output != "hello\n" {
		t.Fatalf("pi's result must be surfaced whole, got %+v", msg.Bash)
	}
	var sent struct {
		Type               string `json:"type"`
		Command            string `json:"command"`
		ExcludeFromContext *bool  `json:"excludeFromContext"`
	}
	if err := json.Unmarshal([]byte(log()[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Type != "bash" || sent.Command != "echo hi" {
		t.Errorf("command = %+v, want bash \"echo hi\"", sent)
	}
	if sent.ExcludeFromContext == nil || *sent.ExcludeFromContext {
		t.Error("a single ! must not exclude the output from the model's context")
	}
	um, _ := m.Update(msg)
	m = um.(Model)
	if m.bashRunning {
		t.Error("the bash run must not leave the TUI thinking a command is running")
	}
	blocks := m.blocks
	if len(blocks) == 0 || blocks[len(blocks)-1].Kind != "bash" {
		t.Fatalf("a bash run must leave a bash block, got %+v", blocks)
	}
	if !strings.Contains(blocks[len(blocks)-1].Text, "$ echo hi") ||
		!strings.Contains(blocks[len(blocks)-1].Text, "exit 0") {
		t.Errorf("bash block = %q, want the command and its exit code", blocks[len(blocks)-1].Text)
	}
}

// "!!cmd" is pi's exclude-from-context bash.
func TestDoubleBangExcludesFromContext(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.ta.SetValue("!!git status")

	msg := m.submitInput()().(PiOpMsg)
	if msg.Err != nil {
		t.Fatalf("bash: %v", msg.Err)
	}
	if !msg.Exclude {
		t.Error("!!cmd must set excludeFromContext")
	}
	var sent struct {
		Command            string `json:"command"`
		ExcludeFromContext *bool  `json:"excludeFromContext"`
	}
	if err := json.Unmarshal([]byte(log()[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Command != "git status" || sent.ExcludeFromContext == nil || !*sent.ExcludeFromContext {
		t.Errorf("wire command = %+v, want bash \"git status\" with excludeFromContext", sent)
	}
}

// pi refuses a second bash while one runs and keeps the text (interactive-
// mode.js:2591-2594).
func TestSecondBashIsRefusedAndTheTextStays(t *testing.T) {
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.bashRunning = true
	m.ta.SetValue("!ls")

	if cmd := m.submitInput(); cmd != nil {
		t.Fatal("a second bash must not be sent while one is running")
	}
	if m.ta.Value() != "!ls" {
		t.Errorf("editor = %q, want the refused command kept", m.ta.Value())
	}
	if !strings.Contains(lastNotice(m), "already running") {
		t.Errorf("the refusal must be visible, notices = %q", lastNotice(m))
	}
}

// Esc cancels a running "!cmd" (pi: onEscape → session.abortBash()).
func TestEscAbortsARunningBashCommand(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.bashRunning = true

	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc must abort the running command")
	}
	if _, ok := cmd().(PiOpMsg); !ok {
		t.Fatal("the abort must report a PiOpMsg")
	}
	if countType(log(), "abort_bash") != 1 {
		t.Errorf("commands = %v, want one abort_bash", typesInLog(log()))
	}
	if um.(Model).bashRunning {
		t.Error("the TUI must stop showing a running command after the abort")
	}
}

// Esc stops pi's auto-retry loop (pi swaps Esc to session.abortRetry()
// while auto_retry_start is live).
func TestEscAbortsAutoRetry(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.retrying = true

	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc must abort the retry")
	}
	cmd()
	if countType(log(), "abort_retry") != 1 {
		t.Errorf("commands = %v, want one abort_retry", typesInLog(log()))
	}
	if um.(Model).retrying {
		t.Error("the retry latch must clear once the abort is sent")
	}
}

// A new session an extension vetoed through session_before_switch is
// reported, not silently read as "done" with the old chat still on screen.
func TestNewSessionVetoIsReportedAndKeepsTheChat(t *testing.T) {
	pi, _ := spawnFakePi(t, map[string]string{"FAKE_PI_VETO": "1"})
	m := New(pi, t.TempDir())
	m.AddBlock(Block{Kind: "assistant", Text: "still here"})

	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if cmd == nil {
		t.Fatal("Ctrl+N must ask pi for a new session")
	}
	msg := cmd().(SessionResetMsg)
	if !pirpc.IsVeto(msg.Err) {
		t.Fatalf("a cancelled new_session must surface as a veto, got %v", msg.Err)
	}
	um, _ = um.(Model).Update(msg)
	m = um.(Model)
	if len(m.blocks) == 0 || m.blocks[0].Kind != "assistant" {
		t.Errorf("a vetoed new session must keep the conversation, blocks = %+v", m.blocks)
	}
	if !strings.Contains(lastNotice(m), "session_before_switch") {
		t.Errorf("the veto must be visible, notices = %q", lastNotice(m))
	}
}

// The "pi TUI-only" blocklist must not swallow a command pi answers over
// RPC. The registry side of this lives in src/builtin; here we only pin
// that the messages exist and are wired into Update.
func TestPiOpMsgRendersTheResult(t *testing.T) {
	m := New(nil, t.TempDir())
	um, _ := m.Update(PiOpMsg{Op: "name", Notice: "session name set: work"})
	if got := lastNotice(um.(Model)); got != "session name set: work" {
		t.Errorf("notice = %q, want the command's own line", got)
	}
}
