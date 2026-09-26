package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func writeAgent(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentProgressRendersInChat(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"notify","message":"[subagent-async] worker is running"}`))

	if len(m.blocks) != 1 {
		t.Fatalf("subagent progress must be added to chat, got %+v", m.blocks)
	}
	if m.blocks[0].Kind != "notice" || m.blocks[0].Text != "[subagent-async] worker is running" {
		t.Fatalf("unexpected progress block: %+v", m.blocks[0])
	}
	if len(m.toasts) != 0 {
		t.Fatalf("subagent progress must not be a transient toast: %+v", m.toasts)
	}
}

func TestAgentTeamProgressRendersInChat(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"notify","message":"[pi-agent-team] w1 running"}`))

	if len(m.blocks) != 1 || m.blocks[0].Kind != "notice" {
		t.Fatalf("agent-team progress must be added to chat, got %+v", m.blocks)
	}
	if len(m.toasts) != 0 {
		t.Fatalf("agent-team progress must not be a transient toast: %+v", m.toasts)
	}
}

func TestAgentTeamWidgetRendersAsDashboard(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"Pi-Agent-Team","widgetLines":["w1 running","w2 running"]}`))

	if len(m.blocks) != 0 || len(m.toasts) != 0 {
		t.Fatalf("team widget must not enter chat: blocks=%+v toasts=%+v", m.blocks, m.toasts)
	}
	if got, want := m.TeamWidgetLines, []string{"w1 running", "w2 running"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("team lines = %q, want %q", got, want)
	}
	if !m.TeamWidgetVisible || m.TeamWidgetPlacement != "aboveEditor" {
		t.Fatalf("team should be visible above editor, got visible=%v placement=%q", m.TeamWidgetVisible, m.TeamWidgetPlacement)
	}
}

func TestAgentTeamWidgetUpdatesRespectHiddenPreference(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"agent-team","widgetPlacement":"belowEditor","widgetLines":["one"]}`))
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"agent-team","widgetPlacement":"belowEditor","widgetLines":["one","two"]}`))

	if len(m.blocks) != 0 || len(m.toasts) != 0 {
		t.Fatalf("team replacements must not add chat state: blocks=%+v toasts=%+v", m.blocks, m.toasts)
	}
	if len(m.TeamWidgetLines) != 2 || m.TeamWidgetPlacement != "belowEditor" {
		t.Fatalf("team state not replaced in place: lines=%q placement=%q", m.TeamWidgetLines, m.TeamWidgetPlacement)
	}
	m.ToggleTeamWidget()
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"agent-team","widgetLines":["one","two","three"]}`))
	if m.TeamWidgetVisible {
		t.Fatal("non-empty updates must respect an explicit hidden preference")
	}
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"agent-team"}`))
	if len(m.TeamWidgetLines) != 0 || m.TeamWidgetSeen {
		t.Fatalf("empty team widget must clear dashboard: lines=%q seen=%v", m.TeamWidgetLines, m.TeamWidgetSeen)
	}
	m = m.handleUIRequest([]byte(`{"method":"setWidget","widgetKey":"agent-team","widgetLines":["fresh"]}`))
	if !m.TeamWidgetVisible {
		t.Fatal("the first widget after a clear should show")
	}
}

func TestTeamWidgetRecognitionAndRender(t *testing.T) {
	if !isTeamWidget("AGENT-TEAM") || !isTeamWidget(" pi-agent-team ") {
		t.Fatal("team widget names must be recognized case-insensitively")
	}
	if isTeamWidget("agent-workers") || isAgentProgressWidget("agent-team") {
		t.Fatal("unknown/team widget classified as another live progress widget")
	}
	raw := []string{"\x1b[1mPi Agents Team\x1b[0m · active=1 · relays=0", "▶ 1 running", "● Agents · active=1 · tracked=1", "├ ◯ fixer w1 · task", "│  └ status: running", "└ + 0 more · /team to view"}
	m := Model{winW: 100, TeamWidgetLines: raw, TeamStatus: "\x1b[33mOrchestrator · Working...\x1b[0m", TeamWidgetVisible: true, TeamWidgetSeen: true}
	m.setTeamWidget(raw, "aboveEditor")
	panel := stripANSI(m.renderTeamWidget())
	for _, want := range []string{"Pi Agents Team", "▶ 1 running", "├ ◯ fixer w1", "│  └ status: running", "└ + 0 more"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("team panel missing %q: %q", want, panel)
		}
	}
	if !strings.Contains(panel, "TEAM  Orchestrator · Working...") {
		t.Fatalf("team widget must render the status row: %q", panel)
	}
	if got := m.TeamWidgetLines[0]; got != raw[0] {
		t.Fatalf("raw ANSI was not preserved: %q", got)
	}
	m.ToggleTeamWidget()
	if m.TeamWidgetVisible {
		t.Fatal("/team toggle should hide live panel")
	}
}

func TestTeamStatusUsesStatusKey(t *testing.T) {
	var m Model
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"PI-AGENT-TEAM","statusText":"\u001b[33mOrchestrator · Working...\u001b[0m"}`))
	if m.TeamStatus != "\x1b[33mOrchestrator · Working...\x1b[0m" || m.extStat != "" {
		t.Fatalf("team status routing: team=%q ext=%q", m.TeamStatus, m.extStat)
	}
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"pi-agent-team","statusText":""}`))
	if m.TeamStatus != "" {
		t.Fatalf("empty team status should clear, got %q", m.TeamStatus)
	}
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"pi-agent-team","statusText":"Orchestrator · Idle"}`))
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"other","statusText":"Working"}`))
	if m.TeamStatus != "Orchestrator · Idle" || m.extStat != "Working" {
		t.Fatalf("unrelated status must not clear team: team=%q ext=%q", m.TeamStatus, m.extStat)
	}
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"other","statusText":""}`))
	if m.TeamStatus != "Orchestrator · Idle" {
		t.Fatalf("unrelated empty status must not clear team, got %q", m.TeamStatus)
	}
	m = m.handleUIRequest([]byte(`{"method":"setStatus","statusKey":"pi-agent-team","statusText":""}`))
	if m.TeamStatus != "" {
		t.Fatalf("explicit team clear should clear status, got %q", m.TeamStatus)
	}
}

func teamWidgetFixture(workers int) []string {
	lines := []string{"\x1b[1mPi Agents Team\x1b[0m · active=8 · relays=0", "\x1b[32m✓ 8 done\x1b[0m", "\x1b[36mΣ\x1b[0m turns=8 · in=1.2k · out=900", "\x1b[36m● Agents\x1b[0m · active=8 · tracked=8"}
	for i := 1; i <= workers; i++ {
		lines = append(lines, fmt.Sprintf("├ ◯ fixer w%d · worker %d", i, i), fmt.Sprintf("│  └ status: running · task: worker %d", i))
	}
	return append(lines, "└ + 0 more · /team to view")
}

func teamUIEvent(t *testing.T, method, key, status, placement string, lines []string) piEventMsg {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"method": method, "widgetKey": key, "statusKey": key,
		"statusText": status, "widgetPlacement": placement, "widgetLines": lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	return piEventMsg{Event: pirpc.Event{Type: "extension_ui_request", Raw: payload}}
}

func TestTeamWidgetStatusRowAndRPCUpdateFrame(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 100, 40
	m.vp = viewport.New(98, 30)
	status := "\x1b[33mOrchestrator · Working...\x1b[0m"
	um, _ := m.Update(teamUIEvent(t, "setStatus", "pi-agent-team", status, "", nil))
	m = um.(Model)
	um, _ = m.Update(teamUIEvent(t, "setWidget", "pi-agent-team", "", "aboveEditor", teamWidgetFixture(8)))
	m = um.(Model)
	view := m.View()
	if !strings.Contains(view, "TEAM") || !strings.Contains(view, "Orchestrator · Working...") || !strings.Contains(view, status) {
		t.Fatalf("View lost styled/raw TEAM status row: %q", view)
	}
	um, _ = m.Update(teamUIEvent(t, "setWidget", "pi-agent-team", "", "aboveEditor", []string{"\x1b[1mPi Agents Team\x1b[0m · A", "A"}))
	m = um.(Model)
	viewA := m.View()
	um, _ = m.Update(teamUIEvent(t, "setWidget", "pi-agent-team", "", "aboveEditor", []string{"\x1b[1mPi Agents Team\x1b[0m · B", "B"}))
	m = um.(Model)
	viewB := m.View()
	if viewA == viewB || !strings.Contains(stripANSI(viewB), " · B") {
		t.Fatalf("RPC A->B snapshot did not repaint immediately")
	}
	um, _ = m.Update(teamUIEvent(t, "setWidget", "pi-agent-team", "", "aboveEditor", nil))
	m = um.(Model)
	view = m.View()
	if !strings.Contains(view, "TEAM") || !strings.Contains(view, "Orchestrator · Working...") {
		t.Fatalf("status-only surface disappeared after empty widget: %q", view)
	}
}

func TestTeamWidgetRawSnapshotUpdateAndFullFrame(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 100, 40
	m.vp = viewport.New(98, 30)
	lines := teamWidgetFixture(8)
	m.setTeamWidget(lines, "aboveEditor")
	// The live panel is popup-sized now: a full snapshot is trimmed to the
	// cap and says so, instead of claiming every spare row of the frame.
	panel := m.renderTeamWidget()
	if h := lipgloss.Height(panel); h > teamPanelMaxRows {
		t.Fatalf("panel height = %d, want at most the popup-sized cap %d", h, teamPanelMaxRows)
	}
	if !strings.Contains(stripANSI(panel), "rows hidden") {
		t.Fatalf("trimmed panel must report what it dropped: %q", stripANSI(panel))
	}
	if !strings.Contains(stripANSI(panel), "Pi Agents Team") {
		t.Fatalf("trimmed panel must keep the panel title: %q", stripANSI(panel))
	}
	m.setTeamWidget([]string{"\x1b[1mPi Agents Team\x1b[0m · A", "A"}, "aboveEditor")
	viewA := m.View()
	m.setTeamWidget([]string{"\x1b[1mPi Agents Team\x1b[0m · B", "B"}, "aboveEditor")
	viewB := m.View()
	if viewA == viewB || !strings.Contains(stripANSI(viewB), " · B") {
		t.Fatalf("A->B snapshot did not repaint immediately")
	}
	if strings.Contains(m.renderTeamWidget(), "TEAM") {
		t.Fatal("team renderer added a TEAM header")
	}
}

func TestTeamWidgetStatusOnlyBudgetAndWidth(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.vp = viewport.New(8, 8)
	m.TeamStatus = "\x1b[33mOrchestrator · Working… raw status is long\x1b[0m"
	m.winW, m.winH = 40, 100
	fixedRows := 100 - m.teamWidgetHeightLimit() - 3
	for budget := 1; budget <= 5; budget++ {
		m.winH = fixedRows + 3 + budget
		m.TeamWidgetLines, m.TeamWidgetSeen = nil, false
		panel := m.renderTeamWidget()
		if lipgloss.Height(panel) != 1 || !strings.Contains(stripANSI(panel), "TEAM") {
			t.Fatalf("budget %d did not render safe status-only row: %q", budget, panel)
		}
		if strings.Contains(stripANSI(panel), "hidden") {
			t.Fatalf("budget %d emitted false overflow: %q", budget, panel)
		}
	}
	for _, width := range []int{10, 20, 24} {
		m.winW, m.winH = width, 20
		panel := m.renderTeamWidget()
		for _, line := range strings.Split(stripANSI(panel), "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("winW=%d produced row width %d: %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

func TestTeamWidgetStructuralOverflowAndBudgets(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 60, 16
	m.vp = viewport.New(58, 6)
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	panel := stripANSI(m.renderTeamWidget())
	if h := lipgloss.Height(panel); h > m.teamWidgetHeightLimit() {
		t.Fatalf("panel exceeds available frame: %d > %d", h, m.teamWidgetHeightLimit())
	}
	if !strings.Contains(panel, "● Agents") || !strings.Contains(panel, "worker blocks hidden") {
		t.Fatalf("overflow must be local, got %q", panel)
	}
	if strings.Contains(panel, "│  └ status") && !strings.Contains(panel, "├ ◯") {
		t.Fatalf("overflow orphaned activity without worker: %q", panel)
	}
	for _, winHeight := range []int{1, 2, 3} {
		m.winH = winHeight // header + input leave zero rows for the widget
		if got := m.renderTeamWidget(); got != "" {
			t.Fatalf("terminal height %d rendered a team panel with no safe budget: %q", winHeight, got)
		}
	}
	m.winW = 24
	m.winH = 40
	m.setTeamWidget([]string{strings.Repeat("wide ", 20), "next"}, "aboveEditor")
	for _, line := range strings.Split(stripANSI(m.renderTeamWidget()), "\n") {
		if lipgloss.Width(line) > m.mainW() {
			t.Fatalf("line width %d exceeds %d: %q", lipgloss.Width(line), m.mainW(), line)
		}
	}
}

func TestTeamWidgetPlacementWithPopup(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 60, 24
	m.baseVpH, m.vp = 17, viewport.New(58, 17)
	m.TeamWidgetLines, m.TeamWidgetVisible, m.TeamWidgetSeen = []string{"w1 running", "w2 running"}, true, true
	m.cmdOpen, m.cmdItems, m.Cmds = true, []int{0}, []pirpc.RepoCommand{{Name: "team"}}
	for _, placement := range []string{"aboveEditor", "belowEditor"} {
		m.TeamWidgetPlacement = placement
		m.applyPopupH()
		view := stripANSI(m.View())
		if h := lipgloss.Height(view); h > m.winH {
			t.Fatalf("%s placement overflowed terminal: %d > %d\n%s", placement, h, m.winH, view)
		}
		team := strings.Index(view, "w1 running")
		input := strings.Index(view, "ready ·")
		if team < 0 || input < 0 || (placement == "aboveEditor" && team > input) || (placement == "belowEditor" && team < input) {
			t.Fatalf("%s placement wrong: team=%d input=%d", placement, team, input)
		}
	}
}

func TestExtensionCommandDoesNotEnterWorkingState(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = false
	cmd := m.ForwardExtensionCommand("/team worker-1")
	if cmd == nil {
		t.Fatal("expected extension command")
	}
	if m.thinking || m.Status == "pi is running…" {
		t.Fatalf("extension command must not enter model turn: thinking=%v status=%q", m.thinking, m.Status)
	}
	if ack, ok := cmd().(extensionCmdAckMsg); !ok || ack.err == nil {
		t.Fatalf("nil pi should produce an error ack, got %#v", cmd())
	}
}

func TestMergeCommandsDeduplicatesNativeTeam(t *testing.T) {
	builtins := []Builtin{{Name: "team", Origin: "pitago"}}
	got := mergeCommands(builtins, []pirpc.RepoCommand{
		{Name: "team", Source: "extension", Description: "extension team"},
		{Name: "team-stop", Source: "extension"},
	})
	if len(got) != 2 || got[0].Source != "pitago" || got[1].Name != "team-stop" {
		t.Fatalf("unexpected merged commands: %+v", got)
	}
}

func TestSubagentAsyncWidgetRendersAndUpdatesInChat(t *testing.T) {
	var m Model
	first := `{"method":"setWidget","widgetKey":"subagent-async","widgetLines":["PI_SUBAGENT_ASYNC_JSON:{payload} · 2s"]}`
	second := `{"method":"setWidget","widgetKey":"subagent-async","widgetLines":["PI_SUBAGENT_ASYNC_JSON:{payload} · 3s"]}`
	m = m.handleUIRequest([]byte(first))
	m = m.handleUIRequest([]byte(second))

	if len(m.blocks) != 1 || m.blocks[0].Kind != "notice" {
		t.Fatalf("subagent-async widget must use one chat block, got %+v", m.blocks)
	}
	if !strings.Contains(m.blocks[0].Text, "3s") || strings.Contains(m.blocks[0].Text, "2s") {
		t.Fatalf("subagent-async widget must update in place: %q", m.blocks[0].Text)
	}
	if len(m.toasts) != 0 {
		t.Fatalf("subagent-async widget must not create toasts: %+v", m.toasts)
	}
}

func TestSubagentProgressRecognizesStyledMarker(t *testing.T) {
	if !isSubagentProgressMessage(" \x1b[33m[subagent-async]\x1b[0m working") {
		t.Fatal("styled subagent progress marker was not recognized")
	}
	if !isSubagentProgressMessage(" \x1b[33m[pi-agent-team]\x1b[0m worker 1") {
		t.Fatal("styled agent-team progress marker was not recognized")
	}
	if isSubagentProgressMessage("ordinary notification") {
		t.Fatal("ordinary notification was misclassified as subagent progress")
	}
}

func TestParseAgentFrontmatter(t *testing.T) {
	raw := "name: reviewer\ndescription: Review stuff\nmodel: anthropic/claude\n---\nbody"
	n, d, m := parseAgentFrontmatter(raw)
	if n != "reviewer" || d != "Review stuff" || m != "anthropic/claude" {
		t.Fatalf("got %q %q %q", n, d, m)
	}
}

func TestDiscoverSubagentsPriority(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: builtin scout\n---\n")
	writeAgent(t, filepath.Join(agentDir, "agents"), "scout.md",
		"name: scout\ndescription: user override\n---\n")
	cwd := t.TempDir()
	writeAgent(t, filepath.Join(cwd, ".pi", "agents"), "worker.md",
		"name: worker\ndescription: project worker\n---\n")

	got := DiscoverSubagents(cwd)
	if len(got) != 2 {
		t.Fatalf("want 2 (scout+worker), got %v", got)
	}
	for _, a := range got {
		if a.Name == "scout" && (a.Source != "user" || a.Description != "user override") {
			t.Fatalf("user must win over builtin: %+v", a)
		}
	}
}

func TestOpenSubagentsShowsCurrent(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "reviewer.md",
		"name: reviewer\ndescription: review\n---\n")

	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.SetCurrentSubagent("reviewer")

	m.OpenSubagents("")
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagents" {
		t.Fatalf("want subagents dialog, got %+v", m.Dialogs)
	}
	d := m.Dialogs[0]
	if len(d.Options) != 3 || d.Options[0] != SubagentsNone {
		t.Fatalf("want [none reviewer scout], got %v", d.Options)
	}
	// Current row must carry the ● marker in its desc.
	found := false
	for i, o := range d.Options {
		if o == "reviewer" && i < len(d.Descs) {
			if strings.HasPrefix(d.Descs[i], "● current") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("current agent must show ● marker, descs=%v", d.Descs)
	}
	// Cursor preselected on current.
	if d.Options[d.FIdx[d.Cursor]] != "reviewer" {
		t.Fatalf("cursor must land on current, got %v cursor=%d", d.Options, d.Cursor)
	}
}

func TestOpenSubagentsDirectPick(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.OpenSubagents("scout")
	if len(m.Dialogs) != 0 {
		t.Fatalf("direct pick must not open dialog")
	}
	if m.CurrentSubagent() != "scout" {
		t.Fatalf("current must persist, got %q", m.CurrentSubagent())
	}
}

func TestOpenSubagentsOff(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.SetCurrentSubagent("scout")
	m.OpenSubagents("off")
	if len(m.Dialogs) != 0 {
		t.Fatalf("off must not open dialog")
	}
	if m.CurrentSubagent() != "" {
		t.Fatalf("off must clear, got %q", m.CurrentSubagent())
	}
	// Picker with nothing selected marks the none row current.
	m.OpenSubagents("")
	d := m.Dialogs[0]
	if d.Options[0] != SubagentsNone {
		t.Fatalf("first row must be none, got %v", d.Options)
	}
	if !strings.HasPrefix(d.Descs[0], "● current") {
		t.Fatalf("none row must show ● when cleared, got %q", d.Descs[0])
	}
}

// teamPanelModel builds a live model whose frame math matches the real one:
// baseVpH = winH-7 and a viewport holding that budget.
func teamPanelModel(t *testing.T, winW, winH int) *Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = winW, winH
	m.baseVpH = winH - 7
	m.vp = viewport.New(winW-2, m.baseVpH)
	m.vp.SetContent(strings.Repeat("chat line\n", 200))
	return &m
}

// The live team panel must behave like the /command popup: popup-sized, and
// pushing the chat up by exactly the rows it paints — never covering it and
// never leaving the frame short of winH.
func TestTeamPanelIsPopupSizedAndPushesChat(t *testing.T) {
	for _, winH := range []int{24, 30, 40, 50} {
		m := teamPanelModel(t, 120, winH)
		quiet := m.vp.Height
		m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
		panelH := m.teamPanelH()
		if panelH < 1 || panelH > teamPanelMaxRows {
			t.Fatalf("winH=%d panel height = %d, want 1..%d", winH, panelH, teamPanelMaxRows)
		}
		if m.vp.Height != quiet-panelH {
			t.Fatalf("winH=%d vp.Height = %d, want %d (quiet %d - panel %d)",
				winH, m.vp.Height, quiet-panelH, quiet, panelH)
		}
		if got := lipgloss.Height(m.View()); got != winH {
			t.Fatalf("winH=%d frame = %d rows, want exactly %d", winH, got, winH)
		}
		if m.vp.Height < 3 {
			t.Fatalf("winH=%d left the chat %d rows", winH, m.vp.Height)
		}
	}
}

// Clearing the widget must hand the panel's rows straight back to the chat.
func TestClearTeamWidgetRestoresChatRows(t *testing.T) {
	m := teamPanelModel(t, 120, 40)
	quiet := m.vp.Height
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	if m.vp.Height >= quiet {
		t.Fatalf("panel did not shrink the chat: %d -> %d", quiet, m.vp.Height)
	}
	m.clearTeamWidgetState()
	if m.vp.Height != quiet {
		t.Fatalf("after clear vp.Height = %d, want %d", m.vp.Height, quiet)
	}
	view := stripANSI(m.View())
	if got := lipgloss.Height(view); got != m.winH {
		t.Fatalf("frame after clear = %d rows, want %d", got, m.winH)
	}
	if strings.Contains(view, "Pi Agents Team") {
		t.Fatal("cleared widget left its panel on screen")
	}
}

// A followed-session roster (live.go) has no "● Agents" heading. Trimming it
// to the cap must not blank it: the panel stays visible and reports the drop.
func TestTeamPanelCapTrimsFlatRosterInsteadOfBlanking(t *testing.T) {
	m := teamPanelModel(t, 120, 40)
	lines := []string{"Pi Agents Team · 12 workers"}
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("reviewer (w%d) · exited", i))
	}
	m.setTeamWidget(lines, "aboveEditor")
	panel := stripANSI(m.renderTeamWidget())
	if h := lipgloss.Height(panel); h > teamPanelMaxRows {
		t.Fatalf("flat roster panel = %d rows, want at most %d", h, teamPanelMaxRows)
	}
	if !strings.Contains(panel, "Pi Agents Team") || !strings.Contains(panel, "reviewer (w1)") {
		t.Fatalf("flat roster lost its title/first worker: %q", panel)
	}
	if !strings.Contains(panel, "hidden") {
		t.Fatalf("flat roster trim must be reported: %q", panel)
	}
	if got := lipgloss.Height(m.View()); got != m.winH {
		t.Fatalf("frame = %d rows, want %d", got, m.winH)
	}
}

// The panel and an open popup share one row pool: together they must still fit
// the frame and leave the chat its floor rows.
func TestTeamPanelSharesRowPoolWithOpenPopup(t *testing.T) {
	m := teamPanelModel(t, 100, 24)
	m.cmdOpen = true
	m.cmdItems = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	for i := range m.cmdItems {
		m.Cmds = append(m.Cmds, pirpc.RepoCommand{Name: fmt.Sprintf("cmd%d", i)})
	}
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	m.applyPopupH()
	view := stripANSI(m.View())
	if got := lipgloss.Height(view); got > m.winH {
		t.Fatalf("panel + popup overflowed the frame: %d > %d\n%s", got, m.winH, view)
	}
	if m.vp.Height < 3 {
		t.Fatalf("panel + popup squeezed the chat to %d rows", m.vp.Height)
	}
	if h := m.teamPanelH(); h > teamPanelMaxRows {
		t.Fatalf("panel grew past the popup-sized cap with a popup open: %d", h)
	}
}

// Raw field writes bypass applyTeamPanelH (that is why View keeps a
// backstop). Even then the frame must never exceed winH.
func TestTeamPanelBackstopKeepsFrameWithinTerminal(t *testing.T) {
	m := teamPanelModel(t, 100, 30)
	m.vp.Height = m.baseVpH // a state change that skipped the sync
	m.TeamWidgetLines, m.TeamWidgetVisible, m.TeamWidgetSeen = teamWidgetFixture(8), true, true
	if got := lipgloss.Height(m.View()); got > m.winH {
		t.Fatalf("unsynced panel state overflowed the frame: %d > %d", got, m.winH)
	}
}
