package app

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

func TestFollowModeBlocksInputAndCtrlDDetaches(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.liveConnected = true
	m.Status = "following external Pi · read-only"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if cmd != nil || updated.(Model).ta.Value() != "" {
		t.Fatalf("follow mode accepted input: %+v", updated)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("follow mode returned a command for Enter")
	}
	m = updated.(Model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("Ctrl+D in follow mode must detach")
	}
	m = updated.(Model)
	if m.followRemote || m.liveConnected {
		t.Fatalf("Ctrl+D did not detach: follow=%v connected=%v", m.followRemote, m.liveConnected)
	}
}

func TestFollowModeCtrlQDetaches(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.liveConnected = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = updated.(Model)
	if m.followRemote || m.liveConnected {
		t.Fatalf("Ctrl+Q did not detach: follow=%v connected=%v", m.followRemote, m.liveConnected)
	}
}

func TestLiveDisconnectedClearsStaleTeamState(t *testing.T) {
	m := New(nil, t.TempDir())
	m.TeamWidgetLines = []string{"stale"}
	m.TeamStatus = "stale status"
	m.TeamWidgetPlacement = "belowEditor"
	m.TeamWidgetSeen = true
	m.applyLive(m.liveGeneration, live.Message{Disconnected: true})
	if len(m.TeamWidgetLines) != 0 || m.TeamStatus != "" || m.TeamWidgetPlacement != "" || m.TeamWidgetSeen {
		t.Fatalf("live disconnect retained stale team state: lines=%q status=%q placement=%q seen=%v", m.TeamWidgetLines, m.TeamStatus, m.TeamWidgetPlacement, m.TeamWidgetSeen)
	}
}

func TestLiveSnapshotRebuildsRemoteTranscript(t *testing.T) {
	m := New(nil, t.TempDir())
	msg := live.Message{Snapshot: &live.Snapshot{
		Revision: 2, SessionID: "remote-session", Model: "external-model",
		Messages: []json.RawMessage{
			json.RawMessage(`{"role":"user","content":"hello"}`),
			json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"hi"}]}`),
			json.RawMessage(`{"role":"custom","customType":"team-result","content":"custom result","display":true}`),
		},
	}}
	m.applyLive(m.liveGeneration, msg)
	if !m.followRemote || !m.liveConnected || m.session != ShortID("remote-session") {
		t.Fatalf("not attached: follow=%v connected=%v session=%q", m.followRemote, m.liveConnected, m.session)
	}
	if len(m.blocks) != 3 || m.blocks[0].Kind != "user" || m.blocks[1].Kind != "assistant" || m.blocks[2].Kind != "notice" {
		t.Fatalf("blocks = %+v", m.blocks)
	}
	m.winW, m.winH = 100, 30
	m.ready = true
	m.renderInput()
	if !strings.Contains(m.renderHeader(), "external ") {
		t.Fatalf("header does not identify follow mode: %q", m.renderHeader())
	}
}

func TestLiveSnapshotRestoresRunningIndicator(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyLive(m.liveGeneration, live.Message{Snapshot: &live.Snapshot{
		SessionID: "remote-running", IsStreaming: true,
		Messages: []json.RawMessage{},
	}})
	if !m.thinking || m.pet.status != petWorking {
		t.Fatalf("running snapshot not reflected: thinking=%v pet=%v", m.thinking, m.pet.status)
	}
	if !strings.Contains(m.petLabel(), "Working") {
		t.Fatalf("running label = %q", m.petLabel())
	}

	m.applyLive(m.liveGeneration, live.Message{Snapshot: &live.Snapshot{
		SessionID: "remote-idle", IsStreaming: false,
		Messages: []json.RawMessage{},
	}})
	if m.thinking || m.pet.status != petIdle {
		t.Fatalf("idle snapshot not reflected: thinking=%v pet=%v", m.thinking, m.pet.status)
	}
}

func TestLiveRunningStateRendersComposerSignal(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 120, 40 // real width: the footer is truncated on a narrow box
	m.ready = true
	m.renderInput()

	applyEvent := func(eventType string) {
		t.Helper()
		m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{
			Raw: json.RawMessage(`{"type":"` + eventType + `"}`),
		}})
	}
	assertRunning := func() {
		t.Helper()
		out := stripANSI(m.renderInput())
		top := strings.Split(out, "\n")[0]
		if !strings.Contains(top, "EXTERNAL · ") || !strings.Contains(top, spinFrame(m.pet.tick)) || !strings.Contains(top, "Working...") {
			t.Fatalf("running external composer missing signal: %q", out)
		}
		if !strings.Contains(out, "Ctrl+D detach") {
			t.Fatalf("running external composer lost read-only footer: %q", out)
		}
		if strings.Contains(out, "↵ send") || strings.Contains(out, "Esc×2 cancel") {
			t.Fatalf("running external composer exposed owned controls: %q", out)
		}
	}
	assertIdle := func() {
		t.Helper()
		out := stripANSI(m.renderInput())
		top := strings.Split(out, "\n")[0]
		if !strings.Contains(top, "EXTERNAL · READ-ONLY") || !strings.Contains(out, "Ctrl+D detach") {
			t.Fatalf("idle external composer changed: %q", out)
		}
	}

	m.applyLive(m.liveGeneration, live.Message{Snapshot: &live.Snapshot{
		SessionID: "remote-render", IsStreaming: true, Messages: []json.RawMessage{},
	}})
	assertRunning()

	m.applyLive(m.liveGeneration, live.Message{Snapshot: &live.Snapshot{
		SessionID: "remote-render", IsStreaming: false, Messages: []json.RawMessage{},
	}})
	assertIdle()

	applyEvent("agent_start")
	assertRunning()

	applyEvent("agent_end")
	assertRunning()

	applyEvent("agent_settled")
	assertIdle()
}

func TestOwnedFetchCannotOverwriteFollowTranscript(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.blocks = []Block{{Kind: "assistant", Text: "remote"}}
	updated, _ := m.Update(connectedMsg{msgs: []pirpc.AgentMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"owned"}]`)},
	}})
	got := updated.(Model)
	if len(got.blocks) != 1 || got.blocks[0].Text != "remote" {
		t.Fatalf("owned fetch replaced remote transcript: %+v", got.blocks)
	}
}

func TestQueuedLiveMessagesCannotReattachAfterDetach(t *testing.T) {
	m := New(nil, t.TempDir())
	queuedGeneration := m.liveGeneration
	m.applyLive(queuedGeneration, live.Message{Snapshot: &live.Snapshot{
		SessionID: "remote", Messages: []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"queued"}`)},
	}})
	if !m.followRemote {
		t.Fatal("initial snapshot did not attach")
	}
	m.detachLive()
	before := append([]Block(nil), m.blocks...)

	updated, _ := m.Update(liveMsg{generation: queuedGeneration, message: live.Message{
		Snapshot: &live.Snapshot{SessionID: "stale", Messages: []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"stale"}`)}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(liveMsg{generation: queuedGeneration, message: live.Message{
		Event: &live.Event{Revision: 99, Raw: json.RawMessage(`{"type":"message_end","message":{"role":"assistant","content":"stale event"}}`)},
	}})
	m = updated.(Model)
	if m.followRemote || m.liveConnected {
		t.Fatalf("stale queued message reattached: follow=%v connected=%v", m.followRemote, m.liveConnected)
	}
	if len(m.blocks) != len(before) || (len(m.blocks) > 0 && m.blocks[0].Text != before[0].Text) {
		t.Fatalf("stale queued message changed transcript: %+v", m.blocks)
	}
}

func TestRemoteEventReusesExistingRenderer(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"remote answer"}]}}`)}})
	if len(m.blocks) != 1 || m.blocks[0].Text != "remote answer" {
		t.Fatalf("remote blocks = %+v", m.blocks)
	}
}

// A subagent/team notice only exists as an extension_ui_request notify on the
// wire, so follow mode is where it must become a transcript block.
func TestFollowModeRendersSubagentNotice(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(
		`{"type":"extension_ui_request","id":"r1","method":"notify","message":"[subagent-async] worker-2 finished","notifyType":"info"}`,
	)}})
	if len(m.blocks) != 1 || !strings.Contains(m.blocks[0].Text, "subagent-async") {
		t.Fatalf("subagent notice not rendered in follow mode: %+v", m.blocks)
	}
	if m.pet.ticking {
		t.Fatal("a fire-and-forget notice must not latch the pet tick loop")
	}
}

// Regression for the discarded petTickCmd: tool_execution_start returns a pet
// command, and dropping it used to latch pet.ticking forever (frozen pet,
// spinner and elapsed timer for the rest of the session).
func TestFollowModeToolStartKeepsPetTickAlive(t *testing.T) {
	m := New(nil, t.TempDir())
	cmd := m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(
		`{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a.go"}}`,
	)}})
	if !m.pet.ticking {
		t.Fatal("tool_execution_start must keep the pet tick loop latched")
	}
	if cmd == nil {
		t.Fatal("follow mode must deliver the pet tick command")
	}
}

// A remote extension dialog can never be answered from follow mode: the
// bridge is one-way and fireUI would reply on the OWNED pi. The prompt must
// be reported, not opened, and must not touch the owned client.
func TestFollowModeIgnoresRemoteDialogRequest(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.liveConnected = true
	nm := m.handleUIRequest([]byte(`{"id":"r2","method":"select","title":"Pick","options":["a","b"]}`))
	if len(nm.Dialogs) != 0 {
		t.Fatalf("follow mode opened a remote dialog: %+v", nm.Dialogs)
	}
	if len(nm.blocks) != 1 || !strings.Contains(nm.blocks[0].Text, "ignored while following") {
		t.Fatalf("remote prompt not reported: %+v", nm.blocks)
	}
}

// set_editor_text is fire-and-forget, so the fire-and-forget follow-mode guard
// used to let it through: a remote subagent's editor prefill overwrote the
// user's OWN prompt textbox (still rendered, still live) and they would
// unknowingly send it to their own pi. Follow mode renders an allowlist, so
// this one is refused like any dialog.
func TestFollowModeRefusesRemoteSetEditorText(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.liveConnected = true
	m.ta.SetValue("my own prompt")
	m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(
		`{"type":"extension_ui_request","id":"r3","method":"set_editor_text","text":"remote prefill"}`,
	)}})
	if got := m.ta.Value(); got != "my own prompt" {
		t.Fatalf("remote set_editor_text clobbered the owned composer: %q", got)
	}
	if len(m.blocks) != 1 || !strings.Contains(m.blocks[0].Text, "set_editor_text") ||
		!strings.Contains(m.blocks[0].Text, "ignored while following") {
		t.Fatalf("refused remote prefill was not reported: %+v", m.blocks)
	}
}
