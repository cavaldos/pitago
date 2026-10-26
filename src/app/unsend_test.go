package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// sentPrompt returns a model with one earlier, answered turn in the
// transcript and "hello" just sent as a plain prompt. acked delivers pi's
// answer to the prompt RPC (the run started); the user block stands in for
// pi's message_end for the prompt.
func sentPrompt(t *testing.T, env map[string]string, acked bool) (Model, func() []string) {
	t.Helper()
	pi, log := spawnFakePi(t, env)
	m := New(pi, t.TempDir())
	m.AddBlock(Block{Kind: "user", Text: "earlier"})
	m.AddBlock(Block{Kind: "assistant", Text: "earlier answer"})
	m.ta.SetValue("hello")
	cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("submit must send the prompt")
	}
	ack := cmd()
	if acked {
		um, _ := m.Update(ack)
		m = um.(Model)
	}
	m.AddBlock(Block{Kind: "user", Text: "hello"})
	return m, log
}

// doubleEsc presses Esc twice and runs the cancel round trip.
func doubleEsc(t *testing.T, m Model) (Model, escAbortMsg) {
	t.Helper()
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um, cmd := um.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("second Esc must run the cancel")
	}
	abort, ok := cmd().(escAbortMsg)
	if !ok {
		t.Fatal("Esc cancel must report escAbortMsg")
	}
	if abort.err != nil {
		t.Fatalf("abort: %v", abort.err)
	}
	um, _ = um.(Model).Update(abort)
	return um.(Model), abort
}

var unsendPrompt = regexp.MustCompile(`^/pitago-unsend \d+$`)

// unsendPrompts returns the /pitago-unsend prompts in the command log.
func unsendPrompts(t *testing.T, log func() []string) []string {
	t.Helper()
	var out []string
	for _, line := range log() {
		var c struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if c.Type == "prompt" && strings.HasPrefix(c.Message, "/pitago-unsend") {
			out = append(out, c.Message)
		}
	}
	return out
}

func blockTexts(m Model) []string {
	out := make([]string, 0, len(m.blocks))
	for _, b := range m.blocks {
		out = append(out, b.Kind+":"+b.Text)
	}
	return out
}

// Claude Code parity: a cancel before the model answered takes the prompt
// back. It leaves the transcript, and its text returns to the input.
func TestEscBeforeAnswerTakesPromptBack(t *testing.T) {
	m, log := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, true)
	if m.unsend == nil || !m.unsend.started {
		t.Fatal("an acked prompt must be ready to take back")
	}
	m, abort := doubleEsc(t, m)
	if abort.unsent == nil {
		t.Fatalf("pi took the prompt back, the cancel must report it (err %v)", abort.unsendErr)
	}
	if got := m.ta.Value(); got != "hello" {
		t.Errorf("input = %q, want the cancelled prompt back", got)
	}
	want := []string{"user:earlier", "assistant:earlier answer"}
	if got := blockTexts(m); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("transcript = %v, want only the earlier turn %v", got, want)
	}
	if m.unsend != nil {
		t.Error("nothing is left to take back after a take-back")
	}
	prompts := unsendPrompts(t, log)
	if len(prompts) != 1 || !unsendPrompt.MatchString(prompts[0]) {
		t.Errorf("want one /pitago-unsend <ms> prompt, got %q", prompts)
	}
}

// The taken-back prompt goes on top of queued text and of what the user
// typed while waiting, the same order pi uses for a restored queue.
func TestTakeBackKeepsQueuedAndTypedText(t *testing.T) {
	m, _ := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1", "FAKE_PI_QUEUE": "queued follow-up"}, true)
	m.ta.SetValue("typed later")
	m, abort := doubleEsc(t, m)
	if abort.unsent == nil {
		t.Fatal("prompt must be taken back")
	}
	if got, want := m.ta.Value(), "hello\n\nqueued follow-up\n\ntyped later"; got != want {
		t.Errorf("input = %q, want %q", got, want)
	}
}

// Once the model shows any output, the cancel is a plain abort: the prompt
// and the partial answer stay, and pi is never asked to take it back.
func TestEscAfterAnswerKeepsPrompt(t *testing.T) {
	for _, b := range []Block{
		{Kind: "assistant", Text: "partial answer"},
		{Kind: "thinking", Text: "let me see"},
		{Kind: "tool", ToolName: "bash"},
	} {
		t.Run(b.Kind, func(t *testing.T) {
			m, log := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, true)
			m.AddBlock(b)
			before := len(m.blocks)
			m, abort := doubleEsc(t, m)
			if abort.unsent != nil {
				t.Fatal("an answered prompt must not be taken back")
			}
			if len(m.blocks) != before || m.ta.Value() != "" {
				t.Errorf("plain cancel must keep the transcript and the input: %d blocks, input %q",
					len(m.blocks), m.ta.Value())
			}
			if p := unsendPrompts(t, log); len(p) != 0 {
				t.Errorf("pi must not be asked to take it back, got %q", p)
			}
		})
	}
}

// An empty assistant block (text_start before any delta) is not an answer.
func TestEmptyAssistantBlockIsNotAnAnswer(t *testing.T) {
	m, _ := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, true)
	m.AddBlock(Block{Kind: "assistant"})
	m, abort := doubleEsc(t, m)
	if abort.unsent == nil {
		t.Fatal("an empty reply block must not block the take-back")
	}
	if len(m.blocks) != 2 {
		t.Errorf("the empty reply block must leave with the prompt, got %v", blockTexts(m))
	}
}

// Before pi acked the prompt, an abort may land before the run starts and
// be lost, so a take-back could hide a turn that still runs.
func TestEscBeforeAckIsPlainCancel(t *testing.T) {
	m, log := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, false)
	_, abort := doubleEsc(t, m)
	if abort.unsent != nil {
		t.Fatal("an unacked prompt must not be taken back")
	}
	if p := unsendPrompts(t, log); len(p) != 0 {
		t.Errorf("pi must not be asked to take it back, got %q", p)
	}
}

// pi keeps the prompt when the answer started in the meantime: the cancel
// then stays a plain abort, silently.
func TestPiKeepingThePromptIsPlainCancel(t *testing.T) {
	m, _ := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "keep"}, true)
	m, abort := doubleEsc(t, m)
	if abort.unsent != nil || abort.unsendErr != nil {
		t.Fatalf("kept prompt: unsent=%v err=%v, want a plain cancel", abort.unsent, abort.unsendErr)
	}
	if len(m.blocks) != 3 || m.ta.Value() != "" {
		t.Errorf("plain cancel must keep the transcript and the input: %v, input %q", blockTexts(m), m.ta.Value())
	}
}

// Without the extension loaded, "/pitago-unsend" must never reach pi as a
// prompt: pi would start a model turn with it.
func TestUnsendUnavailableSendsNoPrompt(t *testing.T) {
	m, log := sentPrompt(t, nil, true)
	_, abort := doubleEsc(t, m)
	if abort.unsent != nil || abort.unsendErr != nil {
		t.Fatalf("no extension: unsent=%v err=%v, want a plain cancel", abort.unsent, abort.unsendErr)
	}
	if p := unsendPrompts(t, log); len(p) != 0 {
		t.Errorf("no prompt may be sent without the command, got %q", p)
	}
}

// A steer can join the turn as its own user message, so it turns the
// take-back off for that turn.
func TestSteerTurnsTakeBackOff(t *testing.T) {
	m, _ := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, true)
	m.ta.SetValue("also this")
	if cmd := m.submitInput(); cmd == nil {
		t.Fatal("steer must be sent")
	}
	if m.unsend != nil {
		t.Error("a steer must turn the take-back off")
	}
}

// A settled turn has nothing left to take back.
func TestSettleClearsTakeBack(t *testing.T) {
	m, _ := sentPrompt(t, map[string]string{"FAKE_PI_UNSEND": "1"}, true)
	um, _ := m.Update(piEventMsg{pirpc.Event{Type: "agent_settled"}})
	if um.(Model).unsend != nil {
		t.Error("agent_settled must clear the take-back")
	}
}

// Image chips sent with the prompt come back with its text.
func TestTakeBackRestoresImageChips(t *testing.T) {
	pi, _ := spawnFakePi(t, map[string]string{"FAKE_PI_UNSEND": "1"})
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(pi, dir)
	m.attachPaths([]string{"shot.png"})
	m.ta.SetValue("look")
	cmd := m.submitInput()
	if len(m.imgAtts) != 0 {
		t.Fatalf("a send must clear the tray, got %+v", m.imgAtts)
	}
	um, _ := m.Update(cmd())
	m = um.(Model)
	m.AddBlock(Block{Kind: "user", Text: "look"})
	m, abort := doubleEsc(t, m)
	if abort.unsent == nil {
		t.Fatal("prompt must be taken back")
	}
	if len(m.imgAtts) != 1 || m.imgAtts[0].name != "shot.png" {
		t.Errorf("tray = %+v, want the sent chip back", m.imgAtts)
	}
	if m.ta.Value() != "look" {
		t.Errorf("input = %q, want the prompt text back", m.ta.Value())
	}
}

// A refused send gives its image chips back, exactly once.
func TestRefusedSendRestoresImageChips(t *testing.T) {
	pi, _ := spawnFakePi(t, map[string]string{"FAKE_PI_PROMPT_FAIL": "1"})
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(pi, dir)
	m.attachPaths([]string{"shot.png"})
	m.ta.SetValue("look")
	cmd := m.submitInput()
	um, _ := m.Update(cmd())
	m = um.(Model)
	if len(m.imgAtts) != 1 || m.imgAtts[0].name != "shot.png" {
		t.Errorf("tray = %+v, want the refused chip back once", m.imgAtts)
	}
	if m.unsend != nil {
		t.Error("a refused prompt has nothing to take back")
	}
}

// Cutting the transcript drops every index that pointed past the cut.
func TestDropBlocksFromClearsStaleIndices(t *testing.T) {
	m := New(nil, t.TempDir())
	m.AddBlock(Block{Kind: "user", Text: "keep"})
	m.AddBlock(Block{Kind: "user", Text: "drop"})
	m.tools["kept"] = 0
	m.tools["gone"] = 1
	m.curAsst, m.curThink, m.jumpBlock, m.hoverBlock = 1, 1, 1, 2
	m.dropBlocksFrom(1)
	if len(m.blocks) != 1 || m.blocks[0].Text != "keep" {
		t.Fatalf("blocks = %v", blockTexts(m))
	}
	if _, ok := m.tools["gone"]; ok {
		t.Error("tool index past the cut must go")
	}
	if _, ok := m.tools["kept"]; !ok {
		t.Error("tool index before the cut must stay")
	}
	if m.curAsst != -1 || m.curThink != -1 || m.jumpBlock != -1 || m.hoverBlock != 0 {
		t.Errorf("stale indices: curAsst=%d curThink=%d jump=%d hover=%d",
			m.curAsst, m.curThink, m.jumpBlock, m.hoverBlock)
	}
}
