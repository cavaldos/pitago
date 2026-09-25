package app

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

func TestFollowModeBlocksInputAndDetaches(t *testing.T) {
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if m.followRemote || m.liveConnected {
		t.Fatalf("Ctrl+D did not detach: %+v", m)
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
