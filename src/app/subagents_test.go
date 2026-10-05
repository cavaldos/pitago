package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// A real frame, not the winH==0 fallback: the popup-sized cap
	// (teamPanelMaxRows = 10 = 8 content + 2 border rows) is the bound that
	// must hold, and at that budget the whole hierarchy fits.
	m := New(nil, t.TempDir())
	m.ready, m.winW, m.winH = true, 100, 40
	m.vp = viewport.New(98, 30)
	m.TeamWidgetLines, m.TeamStatus, m.TeamWidgetVisible, m.TeamWidgetSeen = raw, "\x1b[33mOrchestrator · Working...\x1b[0m", true, true
	m.setTeamWidget(raw, "aboveEditor")
	panel := stripANSI(m.renderTeamWidget())
	// The rounded frame's two rows come out of teamPanelMaxRows, not out of
	// the content: a full worker block (2 rows) must still fit next to the
	// status row, the title, the running line and the heading.
	for _, want := range []string{"Pi Agents Team", "▶ 1 running", "● Agents", "├ ◯ fixer w1", "│  └ status: running", "└ + 0 more"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("team panel missing %q: %q", want, panel)
		}
	}
	if h := lipgloss.Height(m.renderTeamWidget()); h > m.teamPanelHeightLimit() {
		t.Fatalf("framed panel = %d rows, over the %d-row budget: %q", h, m.teamPanelHeightLimit(), panel)
	}
	if got := lipgloss.Height(stripANSI(m.View())); got != m.winH {
		t.Fatalf("frame = %d rows, want exactly %d", got, m.winH)
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
	// 20 rows leaves the full teamPanelMaxRows=10 panel (8 content + 2
	// border rows); at 16 the frame reserve binds at 6 and the framed panel no
	// longer fits at all, which is the degradation case below.
	m.winW, m.winH = 60, 20
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
		input := strings.Index(view, "↵ send")
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

// lastLineMarker is the sentinel the paint-time regression tests hang at the
// end of the transcript: if the frame cuts rows from the bottom instead of
// pushing the transcript up, this line is the one that disappears.
const lastLineMarker = "pi is running"

// tailModel returns a live model whose transcript ends with lastLineMarker and
// whose viewport is following the tail (the normal state after streaming).
func tailModel(t *testing.T, winW, winH int) *Model {
	t.Helper()
	m := teamPanelModel(t, winW, winH)
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("chat line %d", i))
	}
	lines = append(lines, lastLineMarker)
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.vp.GotoBottom()
	if !m.vp.AtBottom() {
		t.Fatal("fixture must start at the transcript tail")
	}
	return m
}

// The paint-time panels (task widget, generic ext panels) shrink the chat
// viewport on a local copy, which used to drop rows from the bottom of the
// transcript and hide the newest chat line. Whatever the extra panel is, the
// last content line must still be painted.
func TestPaintTimePanelsKeepLastChatLine(t *testing.T) {
	cases := []struct {
		name  string
		arm   func(t *testing.T, m *Model)
		panel string
	}{
		{"team+task", func(t *testing.T, m *Model) {
			m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
			m.Todos = []TodoItem{{ID: "1", Content: "Active", Status: TodoInProgress}}
			m.syncTaskRuntime()
		}, "task widget"},
		{"team+ext", func(t *testing.T, m *Model) {
			m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
			m.setExtWidget("plan-mode-plan", []string{"survey", "implement"}, "aboveEditor")
		}, "[plan-mode-plan]"},
		{"ext only", func(t *testing.T, m *Model) {
			m.setExtWidget("plan-mode-plan", []string{"survey", "implement"}, "aboveEditor")
		}, "[plan-mode-plan]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tailModel(t, 120, 30)
			tc.arm(t, m)
			view := stripANSI(m.View())
			if !strings.Contains(view, lastLineMarker) {
				t.Fatalf("paint-time panel %q hid the last chat line:\n%s", tc.panel, view)
			}
			if got := lipgloss.Height(view); got != m.winH {
				t.Fatalf("frame = %d rows, want exactly %d", got, m.winH)
			}
		})
	}
}

// setTeamStatus paints a 1-row status panel; it must take that row from the
// chat through the same sync path, not at paint time (which cut the tail).
func TestSetTeamStatusReservesChatRows(t *testing.T) {
	m := tailModel(t, 120, 30)
	before := m.vp.Height
	if !m.setTeamStatus(agentTeamName, "3 workers active") {
		t.Fatal("agent team status key must be accepted")
	}
	if m.vp.Height != before-1 {
		t.Fatalf("status row not reserved: vp.Height = %d, want %d", m.vp.Height, before-1)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, lastLineMarker) {
		t.Fatalf("status panel hid the last chat line:\n%s", view)
	}
	if !strings.Contains(view, "3 workers active") {
		t.Fatalf("status line missing:\n%s", view)
	}
}

// The re-pin in View() must not yank a user who scrolled up reading history:
// the offset they chose survives a paint-time-only panel.
func TestPaintTimePanelsKeepUserScrollPosition(t *testing.T) {
	m := tailModel(t, 120, 30)
	m.vp.SetYOffset(5)
	off := m.vp.YOffset
	// paint-time-only panels: raw field writes that skip applyTeamPanelH.
	m.TeamWidgetLines, m.TeamWidgetVisible, m.TeamWidgetSeen = teamWidgetFixture(8), true, true
	m.Todos = []TodoItem{{ID: "1", Content: "Active", Status: TodoInProgress}}
	m.syncTaskRuntime()
	_ = m.View()
	if m.vp.YOffset != off {
		t.Fatalf("View yanked the reader back: offset = %d, want %d", m.vp.YOffset, off)
	}
	if m.vp.AtBottom() {
		t.Fatal("fixture assumption broken: reader is at the bottom again")
	}
}

// The live TEAM panel must be framed in the same rounded box as the /command
// popup: same glyphs, same padding, and a content budget that pays for the two
// border rows out of the panel's own popup-sized cap.
func TestTeamPanelIsFramedLikeCommandPopup(t *testing.T) {
	m := teamPanelModel(t, 120, 30)
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	panel := m.renderTeamWidget()
	plain := stripANSI(panel)
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if !strings.Contains(plain, corner) {
			t.Fatalf("framed team panel missing %q:\n%s", corner, plain)
		}
	}
	if !strings.Contains(plain, "│ Pi Agents Team") {
		t.Fatalf("panel content must sit inside the frame:\n%s", plain)
	}
	// Padding(0,1) means every row — border and content alike — shares one
	// width: the chat column, never wider.
	want := m.teamPanelPaintedWidth()
	for _, line := range strings.Split(plain, "\n") {
		if got := lipgloss.Width(line); got != want {
			t.Fatalf("row width %d, want %d: %q", got, want, line)
		}
	}
	// The frame is paid for out of the popup-sized cap, never on top of it.
	if h := lipgloss.Height(panel); h > m.teamPanelHeightLimit() || h < 3 {
		t.Fatalf("framed panel = %d rows, want 3..%d", h, m.teamPanelHeightLimit())
	}
	if h := lipgloss.Height(panel); h != teamPanelMaxRows {
		t.Fatalf("panel = %d rows, want the full %d-row budget (8 content + 2 border)", h, teamPanelMaxRows)
	}
	// ...and because the border is inside the cap, the content is not paying
	// for it: a whole worker block (2 rows) survives an 8-worker snapshot.
	for _, want := range []string{"├ ◯ fixer w1 · worker 1", "│  └ status: running · task: worker 1", "worker blocks hidden"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("panel lost %q at the raised cap:\n%s", want, plain)
		}
	}
	if got := lipgloss.Height(stripANSI(m.View())); got != m.winH {
		t.Fatalf("frame = %d rows, want exactly %d", got, m.winH)
	}
}

// The frame must not reintroduce the tail bug: with the border, a team panel
// plus a task widget plus an ext panel are all reserving rows, and the newest
// transcript line still has to be painted.
func TestFramedTeamPanelKeepsLastChatLine(t *testing.T) {
	m := tailModel(t, 120, 30)
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	m.Todos = []TodoItem{{ID: "1", Content: "Active", Status: TodoInProgress}}
	m.syncTaskRuntime()
	m.setExtWidget("plan-mode-plan", []string{"survey", "implement"}, "aboveEditor")
	view := stripANSI(m.View())
	if !strings.Contains(view, lastLineMarker) {
		t.Fatalf("framed panel hid the last chat line:\n%s", view)
	}
	if !strings.Contains(view, "╭") {
		t.Fatalf("team panel lost its border:\n%s", view)
	}
	if got := lipgloss.Height(view); got != m.winH {
		t.Fatalf("frame = %d rows, want exactly %d", got, m.winH)
	}
}

// Pre-colored TeamWidgetLines must be clipped by display cells, not by bytes:
// ANSI escapes are zero-width, so a long colored line has to lose exactly the
// same number of *painted* cells as an uncolored one and the box must still
// line up.
func TestFramedTeamPanelClipsColoredLinesByCellWidth(t *testing.T) {
	for _, winW := range []int{40, 80, 120} {
		m := teamPanelModel(t, winW, 30)
		colored := "\x1b[1;35m" + strings.Repeat("wide ", 40) + "\x1b[0m"
		m.setTeamWidget([]string{colored, "● Agents", "├ ◯ fixer w1", "│  └ status: running"}, "aboveEditor")
		panel := stripANSI(m.renderTeamWidget())
		want := m.teamPanelPaintedWidth()
		for _, line := range strings.Split(panel, "\n") {
			if got := lipgloss.Width(line); got != want {
				t.Fatalf("winW=%d row width %d, want %d: %q", winW, got, want, line)
			}
		}
		if !strings.Contains(panel, "╭") || !strings.Contains(panel, "╯") {
			t.Fatalf("winW=%d lost the border:\n%s", winW, panel)
		}
	}
}

// A terminal too narrow for border + padding + one line must degrade, not
// overflow: the panel either disappears or shrinks, and the frame stays inside
// the terminal.
func TestFramedTeamPanelDegradesOnNarrowTerminal(t *testing.T) {
	for _, winW := range []int{6, 10, 20, 30} {
		m := teamPanelModel(t, winW, 30)
		m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
		view := stripANSI(m.View())
		if got := lipgloss.Height(view); got > m.winH {
			t.Fatalf("winW=%d frame = %d rows, over %d", winW, got, m.winH)
		}
		for _, line := range strings.Split(stripANSI(m.renderTeamWidget()), "\n") {
			if got := lipgloss.Width(line); got > winW {
				t.Fatalf("winW=%d row width %d: %q", winW, got, line)
			}
		}
	}
}

// ---- fork (bmxburner/tars-main): subagent herd tests ----

func TestIsSubagentRowTool(t *testing.T) {
	for _, name := range []string{"subagent", "run_agent", "run_workflow", "fork", "SubAgent", " RUN_AGENT "} {
		if !isSubagentRowTool(name) {
			t.Errorf("%q should create rows", name)
		}
	}
	// "forklift" must not match: the match is exact, not a prefix.
	for _, name := range []string{"subagent_wait", "subagent_interrupt", "subagents_list", "subagent_resume", "read", "forklift", "fork_session", "", "my-subagent-tool"} {
		if isSubagentRowTool(name) {
			t.Errorf("%q should not create rows", name)
		}
	}
}

func TestIsSubagentTool(t *testing.T) {
	for _, name := range []string{"subagent", "subagent_interrupt", "subagent_wait", "subagents_list", "subagent_resume", "run_agent", "run_workflow", "fork"} {
		if !isSubagentTool(name) {
			t.Errorf("%q should be a subagent-family tool", name)
		}
	}
	// "fork" is accepted for forward compatibility even though nothing emits it
	// today; unrelated tools still must not match.
	if isSubagentTool("read") || isSubagentTool("fork_session") {
		t.Error("unrelated tools must not match")
	}
}

func TestParseSubagentArgs(t *testing.T) {
	raw := json.RawMessage(`{"name":"scout","agent":"explore","task":"map the auth flow","interactive":true,"mode":"worktree"}`)
	name, agent, task, interactive, mode := parseSubagentArgs(raw)
	if name != "scout" || agent != "explore" || task != "map the auth flow" || !interactive || mode != "worktree" {
		t.Fatalf("parsed = %q %q %q %v %q", name, agent, task, interactive, mode)
	}
	name, _, _, interactive, _ = parseSubagentArgs(json.RawMessage(`{}`))
	if name != "" || interactive {
		t.Fatalf("empty object should parse empty, got %q %v", name, interactive)
	}
	name, _, _, _, _ = parseSubagentArgs(json.RawMessage(`not json`))
	if name != "" {
		t.Fatalf("invalid json should parse empty, got %q", name)
	}
	name, _, _, _, _ = parseSubagentArgs(nil)
	if name != "" {
		t.Fatal("nil should parse empty")
	}
}

func TestFormatSubagentElapsed(t *testing.T) {
	cases := map[int64]string{0: "00:00", 59000: "00:59", 60000: "01:00", 125000: "02:05", -5: "00:00"}
	for ms, want := range cases {
		if got := formatSubagentElapsed(ms); got != want {
			t.Errorf("formatSubagentElapsed(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestSubagentStatusFromActivity(t *testing.T) {
	now := int64(1_000_000_000)
	start := now - 5_000
	if s, _ := subagentStatusFromActivity(subagentActivity{}, false, start, now); s != SubagentStarting {
		t.Errorf("missing activity should be starting, got %q", s)
	}
	if s, l := subagentStatusFromActivity(subagentActivity{}, false, now-120_000, now); s != SubagentStalled || l != "stalled" {
		t.Errorf("old missing activity should stall, got %q/%q", s, l)
	}
	if s, _ := subagentStatusFromActivity(subagentActivity{Phase: "done"}, true, start, now); s != SubagentDone {
		t.Errorf("done phase should be done, got %q", s)
	}
	if s, l := subagentStatusFromActivity(subagentActivity{Phase: "waiting"}, true, start, now); s != SubagentWaiting || l != "waiting" {
		t.Errorf("waiting phase mismatch, got %q/%q", s, l)
	}
	if s, l := subagentStatusFromActivity(subagentActivity{Phase: "active", ActiveScope: "tool", ToolName: "read", UpdatedAt: now - 1_000}, true, start, now); s != SubagentActive || l != "read" {
		t.Errorf("active tool scope mismatch, got %q/%q", s, l)
	}
	if s, l := subagentStatusFromActivity(subagentActivity{Phase: "active", ActiveScope: "turn", UpdatedAt: now - 1_000}, true, start, now); s != SubagentActive || l != "thinking" {
		t.Errorf("active turn scope mismatch, got %q/%q", s, l)
	}
	if s, l := subagentStatusFromActivity(subagentActivity{Phase: "active", ActiveScope: "tool", UpdatedAt: now - 400_000}, true, start, now); s != SubagentStalled || l != "stale" {
		t.Errorf("stale active should stall, got %q/%q", s, l)
	}
	if s, _ := subagentStatusFromActivity(subagentActivity{Phase: "bogus"}, true, start, now); s != SubagentStarting {
		t.Errorf("unknown phase should be starting, got %q", s)
	}
}

func TestReadSubagentActivity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.json")
	content := `{"version":1,"phase":"active","activeScope":"tool","toolName":"bash","updatedAt":12345}`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, ok := readSubagentActivity(p)
	if !ok || a.Phase != "active" || a.ToolName != "bash" || a.UpdatedAt != 12345 {
		t.Fatalf("activity = %+v %v", a, ok)
	}
	if _, ok := readSubagentActivity(filepath.Join(dir, "missing.json")); ok {
		t.Error("missing file should not parse")
	}
	if err := os.WriteFile(p, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSubagentActivity(p); ok {
		t.Error("invalid json should not parse")
	}
	if err := os.WriteFile(p, []byte(`{"phase":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSubagentActivity(p); ok {
		t.Error("empty phase should not parse")
	}
}

func TestHasSubagentShutdownMarker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(p, []byte("{\"type\":\"message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasSubagentShutdownMarker(p) {
		t.Error("no marker should be false")
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{\"type\":\"custom\",\"customType\":\"session_shutdown\"}\n")
	_ = f.Close()
	if !hasSubagentShutdownMarker(p) {
		t.Error("marker should be true")
	}
	if hasSubagentShutdownMarker(filepath.Join(dir, "missing.jsonl")) {
		t.Error("missing file should be false")
	}
}

func TestSubagentArtifactDir(t *testing.T) {
	got := subagentArtifactDir("/tmp/sess/2026-01-01_abc123.jsonl")
	want := filepath.Join("/tmp/sess", "artifacts", "abc123")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if subagentArtifactDir("/tmp/sess/noid.jsonl") != "" {
		t.Error("basename without id should yield empty dir")
	}
	if subagentArtifactDir("") != "" {
		t.Error("empty session file should yield empty dir")
	}
}

func toolCallMsg(id, name, args string) pirpc.AgentMessage {
	return pirpc.AgentMessage{
		Role:    "assistant",
		Content: json.RawMessage(`[{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":` + args + `}]`),
	}
}

func TestRestoreSubagentsFromMessages(t *testing.T) {
	msgs := []pirpc.AgentMessage{
		toolCallMsg("c1", "subagent", `{"name":"scout","agent":"explore","task":"map auth"}`),
		{Role: "toolResult", ToolCallID: "c1", ToolName: "subagent", Content: json.RawMessage(`[{"type":"text","text":"done: mapped"}]`)},
		toolCallMsg("c2", "read", `{"path":"x"}`),
		toolCallMsg("c3", "subagent", `{"task":"still running"}`),
	}
	rows := restoreSubagentsFromMessages(msgs)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Name != "scout" || rows[0].Status != SubagentDone || !strings.Contains(rows[0].Result, "mapped") {
		t.Errorf("row0 = %+v", rows[0])
	}
	if rows[1].Status != SubagentActive || !strings.HasPrefix(rows[1].Name, "subagent-") {
		t.Errorf("row1 = %+v", rows[1])
	}
	if len(restoreSubagentsFromMessages(nil)) != 0 {
		t.Error("nil messages should yield no rows")
	}
	// error result → error status
	msgs = []pirpc.AgentMessage{
		toolCallMsg("e1", "run_agent", `{"name":"w1"}`),
		{Role: "toolResult", ToolCallID: "e1", ToolName: "run_agent", IsError: true, Content: json.RawMessage(`[{"type":"text","text":"boom"}]`)},
	}
	rows = restoreSubagentsFromMessages(msgs)
	if len(rows) != 1 || rows[0].Status != SubagentError || !rows[0].IsError {
		t.Fatalf("error row = %+v", rows)
	}
}

func TestScanSubagentArtifacts(t *testing.T) {
	now := time.Now()
	agentDir := t.TempDir()
	sessDir := t.TempDir()
	sessFile := filepath.Join(sessDir, "2026-01-01_parent1.jsonl")
	if err := os.WriteFile(sessFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(sessDir, "artifacts", "parent1")
	actDir := filepath.Join(artDir, "subagent-activity")
	if err := os.MkdirAll(actDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// live pane spawn with fresh activity
	if err := os.WriteFile(filepath.Join(artDir, "subagent-ab12.jsonl"), []byte("{\"type\":\"session\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actContent := map[string]any{"version": 1, "phase": "active", "activeScope": "tool", "toolName": "bash", "updatedAt": now.UnixMilli()}
	raw, _ := json.Marshal(actContent)
	if err := os.WriteFile(filepath.Join(actDir, "ab12.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// finished spawn with shutdown marker, no activity → skipped (history covers it)
	if err := os.WriteFile(filepath.Join(artDir, "subagent-cd34.jsonl"), []byte("{\"type\":\"custom\",\"customType\":\"session_shutdown\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// stale run from another session (old mtime, no marker) → skipped
	stale := filepath.Join(artDir, "subagent-ef56.jsonl")
	if err := os.WriteFile(stale, []byte("{\"type\":\"session\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	// in-process session file
	if err := os.MkdirAll(filepath.Join(agentDir, "subagent-sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "subagent-sessions", "ip-1.jsonl"), []byte("{\"type\":\"session\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := scanSubagentArtifacts(sessFile, agentDir, now)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	byID := map[string]SubagentRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	live, ok := byID["disk-ab12"]
	if !ok || live.Status != SubagentActive || live.StatusLabel != "bash" {
		t.Errorf("live row = %+v %v", live, ok)
	}
	if _, ok := byID["disk-cd34"]; ok {
		t.Error("shutdown-marked file should be skipped")
	}
	if _, ok := byID["disk-ef56"]; ok {
		t.Error("stale file should be skipped")
	}
	// empty session file → no rows
	if rows := scanSubagentArtifacts("", "", now); len(rows) != 0 {
		t.Errorf("empty inputs should yield no rows, got %d", len(rows))
	}
}

func TestMergeSubagentDisk(t *testing.T) {
	live := []SubagentRow{{ID: "c1", Name: "scout", SessionFile: "/a/subagent-x.jsonl"}}
	disk := []SubagentRow{
		{ID: "disk-x", Name: "subagent-x", SessionFile: "/a/subagent-x.jsonl"},
		{ID: "disk-y", Name: "subagent-y", SessionFile: "/a/subagent-y.jsonl"},
	}
	merged := mergeSubagentDisk(live, disk)
	if len(merged) != 2 || merged[1].ID != "disk-y" {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestMergeSubagentDiskByTaskIdentity(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.jsonl")
	if err := os.WriteFile(child, []byte(
		"{\"type\":\"session\",\"id\":\"x\"}\n"+
			"{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"Review the Diff NOW\"}]}}\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if got := subagentTaskKey(diskSubagentTask(child)); got != "review the diff now" {
		t.Fatalf("task key = %q", got)
	}
	// A live row and its disk artifact share the task: one row survives and
	// adopts the file, so the sidebar stops showing a duplicate.
	live := []SubagentRow{{ID: "c1", Name: "herd-fixes-reviewer", Task: "Review the diff now", Status: SubagentActive}}
	disk := []SubagentRow{{ID: "disk-x", Name: "subagent-x", Task: "Review the diff now", Status: SubagentActive, SessionFile: child}}
	merged := mergeSubagentDisk(live, disk)
	if len(merged) != 1 {
		t.Fatalf("duplicate row: %+v", merged)
	}
	if merged[0].Name != "herd-fixes-reviewer" || merged[0].SessionFile != child {
		t.Fatalf("live row not adopted: %+v", merged[0])
	}
	// A finished live row is the authority: the task match does not apply,
	// so its own state is never touched by a stale disk snapshot.
	doneAt := time.Now()
	live2 := []SubagentRow{{ID: "c2", Name: "r", Task: "Review the diff now", Status: SubagentDone, DoneAt: &doneAt}}
	merged2 := mergeSubagentDisk(live2, []SubagentRow{{ID: "disk-y", Task: "Review the diff now", Status: SubagentActive, SessionFile: child}})
	if len(merged2) == 0 || merged2[0].Status != SubagentDone || merged2[0].DoneAt == nil {
		t.Fatalf("settled row must stand: %+v", merged2)
	}
	// A previous run of the same task must not hijack the live row: a disk
	// session older than the live row is appended, not adopted.
	older := time.Now().Add(-time.Hour)
	liveOld := []SubagentRow{{ID: "c4", Name: "r", Task: "Review the diff now", Status: SubagentActive, StartedAt: time.Now()}}
	stale := []SubagentRow{{ID: "disk-z", Task: "Review the diff now", Status: SubagentDone, SessionFile: child, StartedAt: older, DoneAt: &older}}
	got := mergeSubagentDisk(liveOld, stale)
	if got[0].Status != SubagentActive || got[0].SessionFile != "" || got[0].DoneAt != nil {
		t.Fatalf("stale disk row must not settle the live row: %+v", got)
	}
	// Different tasks never merge.
	live3 := []SubagentRow{{ID: "c3", Name: "r", Task: "totally other work", Status: SubagentActive}}
	if got := mergeSubagentDisk(live3, disk); len(got) != 2 {
		t.Fatalf("unrelated rows must not merge: %+v", got)
	}
	// A live row that already has a file still folds in a same-task disk
	// row carrying a different one: the task key, not the file, is identity.
	withFile := []SubagentRow{{ID: "c5", Name: "r", Task: "Review the diff now", Status: SubagentActive, SessionFile: "/a/one.jsonl"}}
	if got := mergeSubagentDisk(withFile, []SubagentRow{{ID: "disk-q", Task: "Review the diff now", Status: SubagentActive, SessionFile: "/a/two.jsonl"}}); len(got) != 1 {
		t.Fatalf("same-task disk row must not duplicate a linked live row: %+v", got)
	}
	// A settled row refuses a session from after it finished (a later run).
	started := time.Now().Add(-90 * time.Second)
	finished := time.Now()
	settled := []SubagentRow{{ID: "c6", Name: "r", Task: "later run", Status: SubagentDone, StartedAt: started, DoneAt: &finished}}
	if got := mergeSubagentDisk(settled, []SubagentRow{{ID: "disk-r", Task: "later run", Status: SubagentDone, SessionFile: child, StartedAt: finished.Add(time.Minute)}}); got[0].SessionFile != "" {
		t.Fatalf("a later run's session must not attach: %+v", got[0])
	}
}

func TestMergeSubagentDiskUpsertAndPreserve(t *testing.T) {
	doneAt := time.Now().Add(-time.Minute)
	// A stale non-terminal disk snapshot must not downgrade a settled live
	// row nor wipe its completion time, and must keep the real name.
	live := []SubagentRow{{ID: "c1", Name: "scout", Status: SubagentDone, DoneAt: &doneAt, SessionFile: "/a/subagent-x.jsonl"}}
	disk := []SubagentRow{{ID: "disk-x", Name: "subagent-x", Status: SubagentActive, StatusLabel: "bash", SessionFile: "/a/subagent-x.jsonl"}}
	merged := mergeSubagentDisk(live, disk)
	if len(merged) != 1 {
		t.Fatalf("merged = %+v", merged)
	}
	if merged[0].Status != SubagentDone || merged[0].Name != "scout" {
		t.Errorf("settled row clobbered: %+v", merged[0])
	}
	if merged[0].DoneAt == nil || !merged[0].DoneAt.Equal(doneAt) {
		t.Errorf("DoneAt wiped: %+v", merged[0])
	}
	// A fresh terminal disk snapshot flips a live pane row to done.
	diskDoneAt := time.Now()
	live2 := []SubagentRow{{ID: "c2", Name: "waiter", Status: SubagentActive, StatusLabel: "pane", Interactive: true, SessionFile: "/a/subagent-y.jsonl"}}
	disk2 := []SubagentRow{{ID: "disk-y", Name: "subagent-y", Status: SubagentDone, SessionFile: "/a/subagent-y.jsonl", DoneAt: &diskDoneAt}}
	merged2 := mergeSubagentDisk(live2, disk2)
	if len(merged2) != 1 || merged2[0].Status != SubagentDone || merged2[0].DoneAt == nil {
		t.Fatalf("pane row should resolve to done: %+v", merged2)
	}
	if merged2[0].Name != "waiter" {
		t.Errorf("real name lost: %+v", merged2[0])
	}
	// A re-scanned disk row refreshes the stored snapshot, not a duplicate.
	live3 := []SubagentRow{{ID: "disk-z", Name: "subagent-z", Status: SubagentStarting, SessionFile: "/a/subagent-z.jsonl"}}
	disk3 := []SubagentRow{{ID: "disk-z", Name: "subagent-z", Status: SubagentDone, SessionFile: "/a/subagent-z.jsonl", DoneAt: &diskDoneAt}}
	merged3 := mergeSubagentDisk(live3, disk3)
	if len(merged3) != 1 || merged3[0].Status != SubagentDone || merged3[0].DoneAt == nil {
		t.Fatalf("disk snapshot should refresh in place: %+v", merged3)
	}
}

func TestSideSubagentsAutoVisibility(t *testing.T) {
	var m Model
	if m.SideVisible(SideSubagents) {
		t.Error("empty should hide")
	}
	m.Subagents = []SubagentRow{{ID: "a", Name: "scout", Status: SubagentActive}}
	if !m.SideVisible(SideSubagents) {
		t.Error("rows should reveal")
	}
	m.Side = map[string]bool{SideSubagents: false}
	if m.SideVisible(SideSubagents) {
		t.Error("explicit toggle wins over auto")
	}
}

func TestRenderSubagentsSection(t *testing.T) {
	now := time.Now()
	done := now.Add(-time.Minute)
	m := Model{Subagents: []SubagentRow{
		{ID: "a", Name: "scout", Status: SubagentActive, StatusLabel: "read", StartedAt: now.Add(-90 * time.Second)},
		{ID: "b", Name: "builder", Status: SubagentDone, StartedAt: now.Add(-5 * time.Minute), DoneAt: &done},
	}}
	out := m.renderSubagentsSection(40)
	for _, want := range []string{"Subagents", "scout", "builder", "read", "01:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("section should contain %q, got:\n%s", want, out)
		}
	}
}

func TestUpsertSubagentRow(t *testing.T) {
	rows := upsertSubagentRow(nil, SubagentRow{ID: "a"})
	rows = upsertSubagentRow(rows, SubagentRow{ID: "b"})
	rows = upsertSubagentRow(rows, SubagentRow{ID: "a", Name: "n"})
	if len(rows) != 2 || rows[1].ID != "a" || rows[1].Name != "n" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDismissSubagentRowSticks(t *testing.T) {
	m := New(nil, t.TempDir())
	row := SubagentRow{ID: "disk-x", Name: "subagent-x", SessionFile: "/a/subagent-x.jsonl", Status: SubagentStalled}
	m.Subagents = []SubagentRow{row}
	m.dismissSubagentRow(row)
	if len(m.Subagents) != 0 {
		t.Fatalf("dismiss should remove the row, got %+v", m.Subagents)
	}
	// Disk rescan must not resurrect it: same session file under a
	// fresh disk ID is still filtered via the recorded session file.
	m.Subagents = mergeSubagentDisk(m.Subagents, []SubagentRow{
		{ID: "disk-x", Name: "subagent-x", SessionFile: "/a/subagent-x.jsonl"},
	})
	m.refreshSubagents(true)
	for _, r := range m.Subagents {
		if r.SessionFile == "/a/subagent-x.jsonl" {
			t.Fatalf("rescan resurrected dismissed row: %+v", m.Subagents)
		}
	}
}
