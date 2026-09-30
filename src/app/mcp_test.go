package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

func mcpTestServers() []pirpc.McpServerInfo {
	return []pirpc.McpServerInfo{
		{Name: "broken", Scope: "global", Source: "/a/mcp.json", Enabled: true,
			Exposure: "codemode", Transport: "npx broken", State: "failed",
			Error: "spawn npx ENOENT\nsecond line"},
		{Name: "docs", Scope: "project", Source: "/p/.pi/mcp.json", Enabled: true,
			Exposure: "direct", Transport: "https://example.com/mcp", State: "connected",
			Tools: []string{"search"}, Resources: 1},
	}
}

func mcpTestMsg() McpMsg {
	s := mcpTestServers()
	return McpMsg{
		Options: []string{"broken", "docs"},
		Descs:   []string{"failed: spawn npx ENOENT · codemode · global", "connected · 1 tool · 1 resource · direct · project"},
		Payload: []string{"broken\nbroken\nnpx broken\nglobal: /a/mcp.json\nState: failed: spawn npx ENOENT"},
		Servers: s,
	}
}

// McpMsg opens the list, and a list with no rows reports the folder to
// edit instead of showing an empty box.
func TestMcpMsgOpensListAndReportsEmpty(t *testing.T) {
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Fatalf("dialogs = %+v, want the /mcp list", m.Dialogs)
	}
	d := m.Dialogs[0]
	if d.Title != "MCP servers" {
		t.Errorf("title = %q", d.Title)
	}
	if len(d.Options) != 2 || len(d.McpServers) != 2 {
		t.Fatalf("rows = %d, servers = %d", len(d.Options), len(d.McpServers))
	}
	if !strings.Contains(d.Descs[0], "failed") {
		t.Errorf("row 0 desc = %q", d.Descs[0])
	}
	// The list is filterable: typing narrows it.
	d.Filter = "docs"
	d.Reindex()
	if len(d.FIdx) != 1 || d.Options[d.FIdx[0]] != "docs" {
		t.Errorf("filter by name failed: %v", d.FIdx)
	}
	if !filterableDialog(d) {
		t.Error("the /mcp list must be filterable, like every other pitago picker")
	}

	fresh := Model{}
	um, _ = fresh.Update(McpMsg{})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Errorf("an empty list must not open a dialog, got %+v", m.Dialogs)
	}
	if !strings.Contains(m.LastNotice(), "mcp.json") {
		t.Errorf("the empty notice must name the file to edit, got %q", m.LastNotice())
	}
}

func TestMcpMsgErrorIsReportedNotRendered(t *testing.T) {
	m := Model{}
	um, _ := m.Update(McpMsg{Err: errFakeMcp{}})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Errorf("a failed read must not open the list, got %+v", m.Dialogs)
	}
	if !strings.Contains(m.LastNotice(), "mcp error") {
		t.Errorf("notice = %q", m.LastNotice())
	}
}

type errFakeMcp struct{}

func (errFakeMcp) Error() string { return "pi not found" }

// A refresh after an action must update the OPEN list in place, not
// stack a second copy of it, and must keep the same server selected:
// pi reorders rows (attention first), so a cursor kept by index would
// land on a different server.
func TestSetMcpRowsRefreshesInPlaceAndKeepsSelection(t *testing.T) {
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	// "docs" is row 1; select it.
	m.Dialogs[0].Cursor = 1
	// A refreshed list where "docs" needs attention and therefore
	// sorts FIRST: the cursor has to follow the name.
	refreshed := mcpTestMsg()
	refreshed.Servers[0].State = "needs-auth"
	refreshed.Servers[0].Enabled = true
	refreshed.Descs[0] = "needs sign-in · codemode · global"
	refreshed.Options = []string{"docs", "broken"}
	refreshed.Servers = []pirpc.McpServerInfo{refreshed.Servers[1], refreshed.Servers[0]}
	m.SetMcpRows(refreshed)
	if len(m.Dialogs) != 1 {
		t.Fatalf("a refresh must not stack a second list, got %d dialogs", len(m.Dialogs))
	}
	if m.Dialogs[0].Options[0] != "docs" {
		t.Fatalf("refreshed options = %v", m.Dialogs[0].Options)
	}
	if got := m.mcpSelectedName(m.Dialogs[0]); got != "docs" {
		t.Errorf("selected = %q, want docs (the same server, not the same index)", got)
	}
}

// The action menu opens IN FRONT of the list, and PopMcpMenu leaves the
// list with the same row selected — pi's Esc path.
func TestOpenMcpMenuAndPopKeepList(t *testing.T) {
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	m.Dialogs[0].Cursor = 1
	srv := m.Dialogs[0].SelMcpRow(1)
	if srv.Name != "docs" {
		t.Fatalf("row 1 = %q", srv.Name)
	}
	menu := m.McpMenu(srv)
	menu.Options = []string{"Tools", "Reconnect", "Sign out", "Exposure", "Disable"}
	m.OpenMcpMenu(menu)
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "mcpAction" {
		t.Fatalf("dialogs = %+v, want the menu in front", m.Dialogs)
	}
	m.PopMcpMenu()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Fatalf("PopMcpMenu must leave the list, got %+v", m.Dialogs)
	}
	if m.Dialogs[0].Cursor != 1 {
		t.Errorf("list cursor = %d, want it kept", m.Dialogs[0].Cursor)
	}
	// Popping twice is harmless: it never eats the list.
	m.PopMcpMenu()
	if len(m.Dialogs) != 1 {
		t.Errorf("the list must survive a second pop, got %d dialogs", len(m.Dialogs))
	}
}

// pi's describeState(), the exact wording the row and the menu header
// share.
func TestMcpStateLineMatchesPi(t *testing.T) {
	tests := []struct {
		name string
		srv  pirpc.McpServerInfo
		want string
	}{
		{"disabled wins", pirpc.McpServerInfo{Enabled: false, State: "connected", Tools: []string{"a"}}, "disabled"},
		{"needs sign-in", pirpc.McpServerInfo{Enabled: true, State: "needs-auth"}, "needs sign-in"},
		{"failed with error", pirpc.McpServerInfo{Enabled: true, State: "failed", Error: "boom\nmore"}, "failed: boom"},
		{"failed without error", pirpc.McpServerInfo{Enabled: true, State: "failed"}, "failed"},
		{"connected counts", pirpc.McpServerInfo{Enabled: true, State: "connected", Tools: []string{"a", "b"}, Resources: 4}, "connected · 2 tools · 4 resources"},
		{"connected one tool", pirpc.McpServerInfo{Enabled: true, State: "connected", Tools: []string{"a"}}, "connected · 1 tool"},
		{"connecting", pirpc.McpServerInfo{Enabled: true, State: "connecting"}, "connecting…"},
		{"no state yet", pirpc.McpServerInfo{Enabled: true}, "starting"},
		{"unknown state passes through", pirpc.McpServerInfo{Enabled: true, State: "closed"}, "closed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := McpStateLine(tc.srv); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// The server-name rule is enforced before exec, so an illegal name can
// never reach the pi CLI.
func TestMcpLoginRefusesInvalidNameBeforeExec(t *testing.T) {
	m := Model{}
	// A bin that would fail loudly if it ever ran: the refusal must
	// happen before the process is started.
	m.spawnOpts.Bin = "/nonexistent/pi-should-never-run"
	cmd := m.McpLoginCmd("bad;name")
	if cmd == nil {
		t.Fatal("a refusal still reports back as a command")
	}
	msg, ok := cmd().(McpActionMsg)
	if !ok {
		t.Fatalf("want McpActionMsg, got %T", cmd())
	}
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "is not an MCP server name") {
		t.Errorf("err = %v", msg.Err)
	}
}

// A signed-out / signed-in action pops the menu and re-reads the list,
// because both change what a server reports.
func TestMcpActionMsgRepopsMenuAndReloads(t *testing.T) {
	// app re-enters the hidden BuiltinMcpList continuation to re-read;
	// register a stub so the re-read is observable from here.
	reloaded := 0
	m := Model{}
	m.UseBuiltins([]Builtin{{Name: BuiltinMcpList, Hidden: true,
		Run: func(*Model, string) tea.Cmd { reloaded++; return func() tea.Msg { return nil } }}}, nil)
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	m.OpenMcpMenu(&Dialog{Kind: "mcpAction", Options: []string{"Sign out"}})
	if len(m.Dialogs) != 2 {
		t.Fatalf("dialogs = %d", len(m.Dialogs))
	}
	um, cmd := m.Update(McpActionMsg{Action: "logout", Server: "docs"})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Errorf("after a sign-out the list must be on top, got %+v", m.Dialogs)
	}
	if cmd == nil || reloaded != 1 {
		t.Errorf("the list must be re-read once: a sign-out changes the server's state (cmd=%v reloaded=%d)", cmd, reloaded)
	}
	if !strings.Contains(m.LastNotice(), "signed out of docs") {
		t.Errorf("notice = %q", m.LastNotice())
	}
	um, _ = m.Update(McpActionMsg{Action: "login", Server: "docs", Err: errFakeMcp{}})
	m = um.(Model)
	if !strings.Contains(m.LastNotice(), "mcp login failed") {
		t.Errorf("a failed sign-in must be reported, got %q", m.LastNotice())
	}
}

// A saved exposure / enable reports and re-reads, for the same reason.
func TestMcpConfigMsgRepopsMenuAndReloads(t *testing.T) {
	reloaded := 0
	m := Model{}
	m.UseBuiltins([]Builtin{{Name: BuiltinMcpList, Hidden: true,
		Run: func(*Model, string) tea.Cmd { reloaded++; return func() tea.Msg { return nil } }}}, nil)
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	m.OpenMcpMenu(&Dialog{Kind: "mcpExposure", Options: []string{"✓ codemode"}})
	um, cmd := m.Update(McpConfigMsg{Server: "docs", Notice: "docs exposure → direct — saved to /p/.pi/mcp.json"})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Errorf("after a save the list must be on top, got %+v", m.Dialogs)
	}
	if cmd == nil || reloaded != 1 {
		t.Errorf("a save must re-read the list so the row shows the new value (cmd=%v reloaded=%d)", cmd, reloaded)
	}
	if !strings.Contains(m.LastNotice(), "exposure → direct") {
		t.Errorf("notice = %q", m.LastNotice())
	}
	um, _ = m.Update(McpConfigMsg{Server: "docs", Err: errFakeMcp{}})
	m = um.(Model)
	if !strings.Contains(m.LastNotice(), "mcp: pi not found") {
		t.Errorf("a refused save must be reported, got %q", m.LastNotice())
	}
}

// piBin is the only place the pi binary is resolved (spawn option,
// $PI_BIN, then PATH), and the /mcp list must use the SAME one as the
// pi child, or it would read a different mcp.json.
func TestPiBinExportedMatchesResolution(t *testing.T) {
	m := Model{}
	if got := m.PiBin(); got != m.piBin() {
		t.Errorf("PiBin = %q, piBin = %q", got, m.piBin())
	}
}
