package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

const teamDashboardFixture = `Pi Agents Team Dashboard
workers 2 · mode team · relays 0 · Needs reply 0 · Needs recovery 0 · In progress 0 · Completed or idle 2
Use /team opens a keyboard-first overlay with the complete worker registry grouped by attention.
Use /team <worker-id> for direct focus, then inspect Workers / Inspect / Console / Cost tabs. Print mode stays summary-only.
Use /team-result <id> for the final deliverable block.

Done (2)
- designer w1 · Done (idle) — Repurpose Pitago visual system
  status: idle (idle) · action: Review result
  task: Redesign the dashboard
  usage: turns=2 input=1.2k output=900
- fixer w2 · Done (idle) — English developer portfolio implemented
  status: idle (idle) · action: Review result
  task: Build the portfolio
  usage: turns=3 input=2.0k output=1.4k`

func TestTeamDashboardCustomMessageOpensFloatingPanel(t *testing.T) {
	raw := []byte(`{"type":"message_end","message":{"role":"custom","customType":"pi-agent-team/status","display":true,"content":"Pi Agents Team Dashboard\nworkers 2 · mode team · relays 0\nDone (1)\n- fixer w2 · Done (idle) — Portfolio complete"}}`)
	var event struct {
		Message pirpc.AgentMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Message.Role != "custom" || !event.Message.Display || !isTeamDashboardText(pirpc.TextOf(event.Message.Content)) {
		t.Fatalf("invalid dashboard fixture: %+v", event.Message)
	}
	updated, _ := Model{}.handleEvent(pirpc.Event{Type: "message_end", Raw: raw})
	m, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "team" {
		t.Fatalf("dashboard custom message must open team panel: %+v", m.Dialogs)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("live dashboard must not be duplicated into chat: %+v", m.blocks)
	}
}

func TestTeamDashboardUsesPiStyleSummary(t *testing.T) {
	data := parseTeamDashboard(teamDashboardFixture)
	if data.Summary != "workers 2 · relays 0 · Needs reply 0 · Needs recovery 0 · Working 0 · Done 2" {
		t.Fatalf("unexpected summary: %q", data.Summary)
	}
	workers := teamWorkers(data)
	if len(workers) != 2 || workers[0].Label != "designer w1 · Done (idle)" || workers[1].Headline != "English developer portfolio implemented" {
		t.Fatalf("unexpected workers: %+v", workers)
	}

	m := Model{winW: 100, winH: 30, Dialogs: []*Dialog{{Kind: "team", Title: "Pi Agents Team · /team", Message: teamDashboardFixture}}}
	panel := stripANSI(m.renderTeamDashboardPanel(m.Dialogs[0], teamPanelWidth(100)))
	for _, want := range []string{
		"Pi Agents Team · /team", "[1 Workers]", "2 Inspect", "3 Console", "4 Cost",
		"Working 0", "Done 2", "selected: designer w1", "Done (2)", "fixer w2", "q close",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("team panel missing %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "Print mode stays summary-only") || strings.Contains(panel, "Use /team-result") {
		t.Fatalf("RPC fallback instructions leaked into the Pi-style panel:\n%s", panel)
	}
}

func TestTeamDashboardReconcilesDetachedRPCStateFromSession(t *testing.T) {
	sessionFile := filepath.Join(t.TempDir(), "session.jsonl")
	session := strings.Join([]string{
		`{"type":"custom","customType":"pi-agent-team/state","data":{"kind":"worker_terminal","worker":{"workerId":"w1","profileName":"explorer","status":"idle","lastEventAt":400,"lastSummary":{"headline":"Portfolio review complete","updatedAt":400}}}}`,
		`{"type":"custom","customType":"pi-agent-team/state","data":{"kind":"worker_terminal","worker":{"workerId":"w2","profileName":"designer","status":"idle","lastEventAt":300,"lastSummary":{"headline":"Visual pass complete","updatedAt":300}}}}`,
		`{"type":"custom","customType":"pi-agent-team/state","data":{"kind":"worker_terminal","worker":{"workerId":"w4","profileName":"fixer","status":"error","lastEventAt":500,"lastSummary":{"headline":"Hardening failed","updatedAt":500}}}}`,
		`{"type":"custom","customType":"pi-agent-team/state","data":{"kind":"worker_terminal","worker":{"workerId":"w5","profileName":"fixer","status":"error","lastEventAt":600,"lastSummary":{"headline":"Validation failed","updatedAt":600}}}}`,
		`{"type":"custom","customType":"pi-agent-team/state","data":{"kind":"worker_terminal","worker":{"workerId":"w1","profileName":"explorer","status":"exited","lastEventAt":999,"lastSummary":{"headline":"recovery: Active ping returned registry snapshot only for w1: worker RPC is not attached","updatedAt":999}}}}`,
	}, "\n")
	if err := os.WriteFile(sessionFile, []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}

	stale := `Pi Agents Team Dashboard
workers 4 · mode team · relays 0 · Needs reply 0 · Needs recovery 4 · In progress 0 · Completed or idle 0
Needs recovery (4)
- explorer (w1) — recovery: Active ping returned registry snapshot only for w1: worker RPC is not attached
  status: exited (Exited) · action: Delegate fresh
- designer (w2) — recovery: Active ping returned registry snapshot only for w2: worker RPC is not attached
  status: exited (Exited) · action: Delegate fresh
- fixer (w4) — recovery: Active ping returned registry snapshot only for w4: worker RPC is not attached
  status: error (Error) · action: Recover or delegate fresh
- fixer (w5) — recovery: Active ping returned registry snapshot only for w5: worker RPC is not attached
  status: error (Error) · action: Recover or delegate fresh`
	m := Model{winW: 120, winH: 40, sessionFile: sessionFile}
	m.openTeamDashboard(stale)

	data := parseTeamDashboard(m.Dialogs[0].Message)
	if data.Summary != "workers 4 · relays 0 · Needs reply 0 · Needs recovery 2 · Working 0 · Done 2" {
		t.Fatalf("session-backed summary = %q", data.Summary)
	}
	workers := teamWorkers(data)
	if len(workers) != 4 || baseTeamWorkerStatus(workers[0].Status) != "error" || baseTeamWorkerStatus(workers[1].Status) != "error" || baseTeamWorkerStatus(workers[2].Status) != "idle" || baseTeamWorkerStatus(workers[3].Status) != "idle" {
		t.Fatalf("session-backed worker order/status = %+v", workers)
	}
	panel := stripANSI(m.renderTeamDashboardPanel(m.Dialogs[0], teamPanelWidth(m.winW)))
	for _, want := range []string{"Needs recovery 2", "Done 2", "Needs recovery (2)", "Done (2)", "fixer (w4)", "explorer (w1)"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("reconciled panel missing %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "Active ping returned registry snapshot only") {
		t.Fatalf("detached RPC warning leaked into reconciled panel:\n%s", panel)
	}
}

func TestTeamDashboardFloatsRightAndKeepsCodeVisible(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 120, 30
	m.hideSide = true
	m.baseVpH = 23
	m.vp = viewport.New(116, 23)
	m.vp.SetContent(strings.Repeat("\n", 3) + strings.Repeat(" ", 5) + "CODE_VISIBLE")
	m.Dialogs = []*Dialog{{Kind: "team", Title: "Pi Agents Team · /team", Message: teamDashboardFixture}}

	view := stripANSI(m.View())
	if !strings.Contains(view, "Pi Agents Team · /team") || !strings.Contains(view, "[1 Workers]") {
		t.Fatalf("floating team panel missing:\n%s", view)
	}
	if !strings.Contains(view, "CODE_VISIBLE") {
		t.Fatalf("team panel covered the code on the right:\n%s", view)
	}
	if !strings.Contains(view, "Type a message") && !strings.Contains(view, "Type a message…") {
		t.Fatalf("floating panel covered the editor:\n%s", view)
	}
	if h := lipgloss.Height(view); h > m.winH {
		t.Fatalf("floating view overflows terminal: %d > %d", h, m.winH)
	}
}

func TestTeamResultCustomMessageStaysInChat(t *testing.T) {
	raw := []byte(`{"type":"message_end","message":{"role":"custom","customType":"pi-agent-team/status","display":true,"content":"fixer w2\nFinal answer:\nDone"}}`)
	updated, _ := Model{}.handleEvent(pirpc.Event{Type: "message_end", Raw: raw})
	m := updated.(Model)
	if len(m.Dialogs) != 0 || len(m.blocks) != 1 || m.blocks[0].Kind != "notice" {
		t.Fatalf("team result should stay in chat: dialogs=%+v blocks=%+v", m.Dialogs, m.blocks)
	}
}

func TestTeamDashboardTabsAndInspectInteractions(t *testing.T) {
	m := Model{winW: 100, winH: 30, Dialogs: []*Dialog{{
		Kind: "team", Title: "Pi Agents Team · /team", Message: teamDashboardFixture,
	}}}
	updated, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	if m.Dialogs[0].TeamTab != "inspect" {
		t.Fatalf("2 should open Inspect tab, got %q", m.Dialogs[0].TeamTab)
	}
	panel := stripANSI(m.renderTeamDashboardPanel(m.Dialogs[0], teamPanelWidth(100)))
	for _, want := range []string{"2 Inspect", "Status [idle (idle)]", "Recent activity", "Usage:", "Task:"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("Inspect tab missing %q:\n%s", want, panel)
		}
	}
	updated, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if !m.Dialogs[0].TeamFollow {
		t.Fatal("f should enable follow mode")
	}
	updated, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = updated.(Model)
	if m.Dialogs[0].TeamTab != "console" {
		t.Fatalf("3 should open Console tab, got %q", m.Dialogs[0].TeamTab)
	}
	updated, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = updated.(Model)
	if m.Dialogs[0].TeamTab != "cost" {
		t.Fatalf("4 should open Cost tab, got %q", m.Dialogs[0].TeamTab)
	}
}

func TestTeamDashboardEnterLoadsFullWorkerDetail(t *testing.T) {
	m := Model{winW: 100, winH: 30, Dialogs: []*Dialog{{
		Kind: "team", Title: "Pi Agents Team · /team", Message: teamDashboardFixture,
	}}}
	updated, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || len(m.Dialogs) != 1 {
		t.Fatalf("Enter should request full worker detail without losing the dashboard: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}

	raw := []byte(`{"type":"message_end","message":{"role":"custom","customType":"pi-agent-team/status","display":true,"content":"Pi Agents Team Detail\nStatus [idle] -\nw1 · observer · idle [reusable]\nUsage: turns=3 in=8.4k out=2.4k\nThinking: low\nLast tool: read\nRecent activity -\n• Thinking: inspected the worker"}}`)
	var event struct {
		Message pirpc.AgentMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if !isTeamDetailText(pirpc.TextOf(event.Message.Content)) {
		t.Fatalf("invalid detail fixture: %q", pirpc.TextOf(event.Message.Content))
	}
	updated, _ = m.handleEvent(pirpc.Event{Type: "message_end", Raw: raw})
	m = updated.(Model)
	if len(m.Dialogs) != 1 || !m.Dialogs[0].TeamDetail {
		t.Fatalf("detail custom message should open focused panel: %+v", m.Dialogs)
	}
	panel := stripANSI(m.renderTeamDashboardPanel(m.Dialogs[0], teamPanelWidth(100)))
	for _, want := range []string{"Pi Agents Team · /team · detail", "Status [idle]", "Usage: turns=3", "Thinking: low", "Last tool: read", "Recent activity", "Esc back"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("focused detail missing %q:\n%s", want, panel)
		}
	}
	updated, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.Dialogs[0].TeamDetail || m.Dialogs[0].Message != teamDashboardFixture || m.Dialogs[0].TeamTab != "workers" {
		t.Fatalf("Esc should restore the selected dashboard: %+v", m.Dialogs[0])
	}
}

func TestTeamDashboardSelectRefreshAndClose(t *testing.T) {
	m := Model{winW: 100, winH: 30, Dialogs: []*Dialog{{
		Kind: "team", Title: "Pi Agents Team · /team", Message: teamDashboardFixture,
	}}}
	updated, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.Dialogs[0].Cursor != 1 {
		t.Fatalf("down should select the next worker, cursor=%d", m.Dialogs[0].Cursor)
	}

	updated, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if len(m.Dialogs) != 0 || cmd == nil {
		t.Fatalf("r should close and refresh dashboard: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}

	m.Dialogs = []*Dialog{{Kind: "team", Message: teamDashboardFixture}}
	updated, cmd = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if len(m.Dialogs) != 0 || cmd != nil {
		t.Fatalf("q should close dashboard without a command: dialogs=%d cmd=%v", len(m.Dialogs), cmd != nil)
	}
}

func TestTeamDashboardDetectionIgnoresWorkerResult(t *testing.T) {
	if !isTeamDashboardText("\x1b[1mPi Agents Team Dashboard\x1b[0m\nworkers 1") {
		t.Fatal("styled dashboard heading was not recognized")
	}
	if isTeamDashboardText("fixer w1\nFinal answer:\nDone") {
		t.Fatal("worker result was mistaken for dashboard")
	}
}

func TestANSIWindowPreservesRightSideText(t *testing.T) {
	line := "left" + strings.Repeat(" ", 10) + "right"
	got := stripANSI(ansiWindow(line, 8, 20))
	if !strings.HasSuffix(strings.TrimRight(got, " "), "right") {
		t.Fatalf("ansiWindow returned wrong visible window: %q", got)
	}
}
