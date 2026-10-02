package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// flatBox strips the window's box chrome and collapses whitespace, so
// a phrase the renderer had to WRAP across rows still compares as one
// string. The window wraps rather than truncates (mcpBlockLines), so a
// narrow frame splits "No MCP servers configured" over two lines — the
// assertion must not depend on where that break lands.
func flatBox(s string) string {
	var b strings.Builder
	for _, r := range stripANSI(s) {
		switch r {
		case '│', '╭', '╮', '╰', '╯', '─':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// McpMsg opens the list, and a list with no rows reports the folder to
// edit instead of showing an empty box.
// mcpView renders the /mcp panel the way the app does: as a floating
// window over the transcript. The panel used to be returned straight
// out of renderInput, which meant /mcp overwrote the chat editor; it is
// now an overlay like every other dialog here.
func mcpView(m Model) string { return m.renderMcpOverlay(m.mcpTop()) }

// The per-server token estimate and the summary that sums it are what
// the adapter panel puts at the end of every row ("24/24  ~18,637")
// and under the list ("100 direct  ~57,456 tokens"). Without them the
// row cannot answer "what does this server cost me", which is the
// question the panel exists to answer.
//
// The estimate is only available when a schema cache supplied it, so
// the column must VANISH rather than print a fabricated 0.
func TestMcpRootShowsPerServerTokenEstimate(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 110, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)

	servers := mcpTestServers()
	servers[0].DirectTokens = 18637
	m.Update(mcpTestMsg())
	servers[1].DirectTokens = 1314
	m.SetMcpRows(McpMsg{Options: []string{"broken", "docs"}, Servers: servers})

	out := stripANSI(mcpView(m))
	// Thousands-grouped, with the adapter's leading ~.
	if !strings.Contains(out, "~18,637") {
		t.Errorf("the row must carry its grouped token estimate, got:\n%s", out)
	}
	if !strings.Contains(out, "~1,314") {
		t.Errorf("every server with an estimate must show it, got:\n%s", out)
	}
	// The summary counts the DIRECT tools and the tokens of those same
	// tools: "direct" server contributes 1/1, "codemode" contributes 0/1.
	if !strings.Contains(out, "1 direct  ~19,951 tokens") {
		t.Errorf("the census must sum direct tools and their tokens, got:\n%s", out)
	}

	// Drop every estimate and the whole column and the ~N form of the
	// census must go with it — a zero is not a measurement.
	plain := mcpTestServers()
	m.SetMcpRows(McpMsg{Options: []string{"broken", "docs"}, Servers: plain})
	if out := stripANSI(mcpView(m)); strings.Contains(out, "~18,637") || strings.Contains(out, "direct  ~") {
		t.Errorf("no cache must mean no estimate at all, got:\n%s", out)
	}
}

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
	if len(d.Options) != 3 || len(d.McpServers) != 3 {
		// Two servers plus the trailing add row, which is appended
		// ALWAYS: with servers present it is still how a new one gets
		// added from inside the window.
		t.Fatalf("rows = %d, servers = %d", len(d.Options), len(d.McpServers))
	}
	if len(d.Descs) != len(d.Options) {
		t.Fatalf("descs (%d) must parallel options (%d)", len(d.Descs), len(d.Options))
	}
	if d.Options[2] != McpAddRow {
		t.Fatalf("the last row must be the add form, got %q", d.Options[2])
	}
	if d.SelMcpRow(2).Name != "" {
		t.Errorf("the add row names no server, got %q", d.SelMcpRow(2).Name)
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
	// pi shows its "No MCP servers configured" line INSIDE the panel, so
	// /mcp always opens. The old chat notice left an empty list as a
	// dead end with no window to manage from.
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Fatalf("an empty list must still open the window, got %+v", m.Dialogs)
	}
	// The add row is the only row on an empty list, which is what makes
	// the window a place you can configure FROM rather than a dead end.
	if got := m.Dialogs[0].Options; len(got) != 1 || got[0] != McpAddRow {
		t.Errorf("an empty list must offer the add row, got %v", got)
	}
	if got := m.Dialogs[0].Message; !strings.Contains(got, "No MCP servers configured") {
		t.Errorf("the empty window must carry pi's empty line, got %q", got)
	}
	if got := mcpView(m); !strings.Contains(flatBox(mcpView(m)), "No MCP servers configured") {
		t.Errorf("the empty line must be visible in the window, got:\n%s", got)
	}
	// A refresh back to a populated list clears it.
	m.SetMcpRows(mcpTestMsg())
	if got := m.Dialogs[0].Message; got != "" {
		t.Errorf("a populated window must not keep the empty line, got %q", got)
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

// The /mcp screens draw as a window in the editor slot, not as the
// generic centered popup: pi replaces the editor container with the
// panel, so the transcript stays painted above it. Three things must
// hold for that to read as a window and not as a floating box — the
// title sits in the top border, the footer carries pi's per-screen
// verbs, and the row descriptions get the whole remaining width
// instead of the popup's squeezed "…2 resourc…" column.
func TestMcpWindowFloatsOverTheTranscript(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 110, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)

	if m.mcpTop() == nil {
		t.Fatal("an open /mcp list must own the window")
	}
	// It FLOATS over the transcript instead of replacing the editor:
	// returning the panel from renderInput made /mcp overwrite the chat
	// input box, which is not what the panel does anywhere else.
	if in := stripANSI(m.renderInput()); strings.Contains(in, "MCP Servers") {
		t.Errorf("the panel must not stand in for the editor:\n%s", in)
	}
	// The generic popup took the whole screen; the window must not.
	if strings.Contains(m.renderDialog(), "(1 more dialogs pending)") {
		t.Error("the window must not fall back to the generic popup renderer")
	}
	if got := stripANSI(m.renderDialog()); !strings.Contains(got, "MCP Servers") {
		t.Errorf("the dialog layer must draw the panel, got:\n%s", got)
	}

	out := stripANSI(mcpView(m))
	// The overlay is lipgloss.Place-d, so the panel arrives padded to
	// the terminal; the border's own shape is asserted on the trimmed
	// line, not on the placement around it.
	top := strings.TrimSpace(strings.Split(out, "\n")[0])
	// The root list is the redesigned frame: the title is CENTRED in the
	// top border and ruled on both sides, above the search line, the
	// N/M counts, the census and the dim key block. (The per-server
	// screens below keep the left-aligned title.)
	if !strings.Contains(top, " MCP Servers ") ||
		!strings.HasPrefix(top, "╭─") || !strings.HasSuffix(top, "─╮") {
		t.Errorf("the server list's title must be centred in a ruled top border, got %q", top)
	}
	if !strings.Contains(out, "◎  search") {
		t.Errorf("the root list must show the search line, got:\n%s", out)
	}
	if !strings.Contains(out, "0/0") || !strings.Contains(out, "1/1") {
		t.Errorf("every row must carry its direct/total tool count, got:\n%s", out)
	}
	// The row description is gone from the ROOT list: the dot says the
	// state and the count says the tool total, so repeating them after
	// the name only truncated on a narrow frame. The server's own screen
	// still shows them.
	if strings.Contains(out, "connected · 1 tool") || strings.Contains(out, "codemode · global") {
		t.Errorf("a root row must be dot + name + N/M only, got:\n%s", out)
	}
	if !strings.Contains(out, "2 servers · 1 tool") {
		t.Errorf("the census must report real counts, got:\n%s", out)
	}
	if !strings.Contains(out, "space toggle · ? descriptions · esc close") {
		t.Errorf("the hint block must name the keys the root list actually has, got:\n%s", out)
	}

	// Each screen names its own Enter verb and where Esc goes.
	menu := m.McpMenu(mcpTestServers()[0])
	menu.Options = []string{"Reconnect"}
	menu.Descs = []string{""}
	m.OpenMcpMenu(menu)
	if got := stripANSI(mcpView(m)); !strings.Contains(got, "enter select · esc back") {
		t.Errorf("the server menu's footer must say back, not close:\n%s", got)
	}
	if got := stripANSI(mcpView(m)); !strings.Contains(got, "State: failed: spawn npx ENOENT") {
		t.Errorf("the details block must stay in the window:\n%s", got)
	}

	// Leaving /mcp gives the input box back.
	m.PopMcpMenu()
	m.Dialogs = nil
	if got := stripANSI(m.renderInput()); !strings.Contains(got, "↵ send") {
		t.Errorf("the input box must return when /mcp closes, got:\n%s", got)
	}
}

// The window shares the editor slot's rows, so a long list or a short
// terminal must resize the CHAT and never overflow the frame. This is
// the one invariant that would fail silently: nothing in the window's
// own render would look wrong, the frame would just spill past the
// terminal edge.
func TestMcpWindowNeverOverflowsTheFrame(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 96, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)

	// 40 servers, so the row budget has to do real work.
	servers := make([]pirpc.McpServerInfo, 40)
	opts := make([]string, 40)
	for i := range servers {
		servers[i] = pirpc.McpServerInfo{Name: fmt.Sprintf("server-%02d", i),
			Scope: "global", Source: "/a/mcp.json", Enabled: true, State: "connected"}
		opts[i] = servers[i].Name
	}
	m.SetMcpRows(McpMsg{Options: opts, Servers: servers})
	m.Dialogs[0].Descs = make([]string, 40)

	for _, h := range []int{10, 16, 24, 40, 60} {
		m.winH = h
		if got := lipgloss.Height(m.View()); got > h {
			t.Errorf("winH=%d: frame is %d rows, overflows the terminal", h, got)
		}
		// The panel floats, so what must fit the terminal is the PANEL,
		// not a share carved out of the transcript.
		if ph := lipgloss.Height(m.renderMcpWindow(m.mcpTop())); ph > h {
			t.Errorf("winH=%d: panel is %d rows, overflows the terminal", h, ph)
		}
	}
}

// The add form is a /mcp WINDOW screen, not a floating popup: it is
// reached from inside the manager, so it has to draw in the editor slot
// with the syntax hint, the typed buffer (placeholder while empty) and
// its own footer — and Esc must land back on the list underneath with
// the add row still selected, not close the manager.
func TestMcpAddFormRendersInWindowAndEscReturnsToList(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 110, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	list := m.Dialogs[0]
	list.Cursor = len(list.Options) - 1 // the add row
	m.OpenMcpMenu(&Dialog{Kind: McpAddKind, Title: "Add MCP server",
		Message:     "<name> --url <url>\n<name> -- <command>",
		Placeholder: "docs --url https://mcp.example.com/mcp"})

	if m.mcpTop() == nil || m.mcpTop().Kind != McpAddKind {
		t.Fatal("the add form must own the editor slot like every other /mcp screen")
	}
	out := stripANSI(mcpView(m))
	if !strings.HasPrefix(strings.Split(out, "\n")[0], "╭─ Add MCP server ") {
		t.Errorf("title must sit in the top border, got:\n%s", out)
	}
	if !strings.Contains(out, "<name> --url <url>") || !strings.Contains(out, "<name> -- <command>") {
		t.Errorf("the form must show both accepted shapes, got:\n%s", out)
	}
	if !strings.Contains(out, "docs --url https://mcp.example.com/mcp") {
		t.Errorf("the empty buffer must show the dim example, got:\n%s", out)
	}
	if !strings.Contains(out, "▌") {
		t.Errorf("the typing line must carry a cursor, got:\n%s", out)
	}
	if !strings.Contains(out, "enter save · esc back") {
		t.Errorf("the footer must say save and back, got:\n%s", out)
	}
	// Typed text replaces the placeholder.
	m.Dialogs[0].Filter = "docs --url https://x.dev/mcp"
	if got := stripANSI(mcpView(m)); strings.Contains(got, "mcp.example.com") {
		t.Errorf("the placeholder must disappear once there is a buffer:\n%s", got)
	}

	// Esc pops the form only: the list is still there, same row.
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Fatalf("Esc must land back on the list, got %+v", m.Dialogs)
	}
	if m.Dialogs[0].Cursor != len(m.Dialogs[0].Options)-1 {
		t.Errorf("list cursor = %d, want the add row still selected", m.Dialogs[0].Cursor)
	}
	// PopMcpMenu tolerates the form too, so the action paths (which all
	// call it) can never strand it on the stack.
	m.OpenMcpMenu(&Dialog{Kind: McpAddKind, Title: "Add MCP server"})
	m.PopMcpMenu()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Errorf("PopMcpMenu must also drop the add form, got %+v", m.Dialogs)
	}
}

// The form is free text: every key the generic dialog path arms for
// (runes, space, Backspace) feeds the buffer, and Enter hands the
// trimmed line to the add verb — the same dispatch every other picker
// kind uses. An empty buffer is NOT a submit, so the form stays open
// rather than closing onto a usage notice.
func TestMcpAddTypingAndEnterDispatchTheAdd(t *testing.T) {
	var got string
	runs := 0
	newM := func() Model {
		m := Model{}
		m.UseBuiltins(nil, map[string]ConfirmFunc{McpAddKind: func(mm *Model, d *Dialog, _ int) (tea.Model, tea.Cmd) {
			runs++
			got = d.Filter
			return *mm, nil
		}})
		um, _ := m.Update(mcpTestMsg())
		m = um.(Model)
		m.OpenMcpMenu(&Dialog{Kind: McpAddKind, Title: "Add MCP server",
			Placeholder: "docs --url https://mcp.example.com/mcp"})
		return m
	}

	m := newM()
	for _, r := range "docs -" {
		um, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = um.(Model)
	}
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = um.(Model)
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("url")})
	m = um.(Model)
	if buf := m.Dialogs[0].Filter; buf != "docs - url" {
		t.Fatalf("typed buffer = %q, want the space to land too", buf)
	}
	// Backspace edits it, like every other free-text dialog.
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = um.(Model)
	if buf := m.Dialogs[0].Filter; buf != "docs - ur" {
		t.Fatalf("after Backspace = %q", buf)
	}

	// Enter on the empty-ish form still submits what was typed.
	m.Dialogs[0].Filter = "docs --url https://x.dev/mcp"
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if runs != 1 || got != "docs --url https://x.dev/mcp" {
		t.Fatalf("the add must run once with the typed line (runs=%d line=%q)", runs, got)
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Errorf("the form must pop back to the list, got %+v", m.Dialogs)
	}

	// Enter with nothing typed keeps the form open and runs nothing.
	m = newM()
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if runs != 1 {
		t.Errorf("an empty buffer must not submit (runs=%d)", runs)
	}
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != McpAddKind {
		t.Errorf("an empty submit must leave the form open, got %+v", m.Dialogs)
	}
}

// The root window is the redesigned frame: title centred in the top
// border, a search line, rules, one row per server (state dot, name,
// direct/total tools) and a census line whose every number comes from
// `pi mcp list --json`.
func TestMcpRootWindowShowsDotNameCountsAndCensus(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 110, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	servers := []pirpc.McpServerInfo{
		{Name: "auth", Scope: "global", Source: "/a/mcp.json", Enabled: true,
			Exposure: "codemode", State: "needs-auth", Transport: "https://x/mcp",
			Tools: []string{"one", "two", "three"}},
		{Name: "live", Scope: "global", Source: "/a/mcp.json", Enabled: true,
			Exposure: "direct", State: "connected", Transport: "npx live",
			Tools: []string{"a", "b", "c", "d"}},
		{Name: "off", Scope: "project", Source: "/p/.pi/mcp.json", Enabled: false,
			Exposure: "codemode", State: "connected", Transport: "npx off",
			Tools: []string{"z"}},
	}
	msg := McpMsg{
		Options: []string{"auth", "live", "off"},
		Descs:   []string{"needs sign-in · codemode · global", "connected · 4 tools · direct · global", "disabled · codemode · project"},
		Servers: servers,
	}
	um, _ := m.Update(msg)
	m = um.(Model)
	out := stripANSI(mcpView(m))

	if !strings.Contains(out, "◎  search...▌") {
		t.Errorf("the search line must show the empty buffer and its cursor:\n%s", out)
	}
	// The N/M column: auth is codemode (0 direct of 3), live is direct
	// (4 of 4), off is disabled with one tool.
	for _, want := range []string{"0/3", "4/4", "0/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the direct/total column %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "3 servers · 8 tools · 1 needs sign-in") {
		t.Errorf("the census must count servers, tools and the sign-in backlog:\n%s", out)
	}
	// A dot per row, and it is the only state signal left: connected is
	// the filled one, needs-auth and disabled are their own glyphs.
	for _, want := range []string{"◐ auth", "● live", "○ off"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing the state dot %q:\n%s", want, out)
		}
	}
	// The row is dot + name + count and NOTHING else: the description
	// repeated the dot and the count, and only ever truncated.
	for _, gone := range []string{"needs sign-in ·", "connected · 4 tools", "codemode · global"} {
		if strings.Contains(out, gone) {
			t.Errorf("a root row must not repeat %q:\n%s", gone, out)
		}
	}
	// A per-tool override is what the column is for.
	m.Dialogs[0].McpServers[1].Exposure = "codemode"
	m.Dialogs[0].McpServers[1].ToolExposure = map[string]string{"b": "direct"}
	if got := stripANSI(mcpView(m)); !strings.Contains(got, "1/4") {
		t.Errorf("a per-tool override must move the direct count:\n%s", got)
	}

	// The filter narrows the list and the search line shows it.
	m.Dialogs[0].Filter = "liv"
	m.Dialogs[0].Reindex()
	out = stripANSI(mcpView(m))
	if !strings.Contains(out, "◎  liv▌") {
		t.Errorf("the search line must show the typed filter:\n%s", out)
	}
	if strings.Contains(out, "off ") {
		t.Errorf("the filter must narrow the rows:\n%s", out)
	}
}

// A root row is exactly dot + name + N/M: no state, no exposure, no
// scope, no tool count spelled out. That information is on the server's
// own screen (Enter) and still searchable with `?`, so nothing is lost —
// but a row that says it twice is a row that truncates.
func TestMcpRootRowCarriesNoDescriptionText(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	// 80 columns: the width at which the old description clipped to
	// "direct · …".
	m := Model{ready: true, ta: ta, winW: 80, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	srv := pirpc.McpServerInfo{Name: "notion", Scope: "global", Source: "/a/mcp.json",
		Enabled: true, Exposure: "direct", State: "connected", Transport: "npx -y @notionhq/notion-mcp-server",
		Tools: []string{"a", "b", "c", "d"}, Resources: 4, ResourceTemplates: 2}
	um, _ := m.Update(McpMsg{
		Options: []string{"notion"},
		Descs:   []string{"connected · 4 tools · 4 resources · direct · global"},
		Servers: []pirpc.McpServerInfo{srv},
	})
	m = um.(Model)
	row := mcpRowLine(stripANSI(mcpView(m)), "notion")
	if row == "" {
		t.Fatalf("the server row is missing:\n%s", stripANSI(mcpView(m)))
	}
	if want := "▸ ● notion  4/4"; row != want {
		t.Errorf("row = %q, want exactly %q", row, want)
	}
	for _, gone := range []string{"connected", "direct", "global", "4 tools", "4 resources", "npx"} {
		if strings.Contains(row, gone) {
			t.Errorf("the row still carries %q: %q", gone, row)
		}
	}
	// The same data is still one Enter away, and still searchable.
	menu := m.McpMenu(srv)
	if !strings.Contains(menu.Message, "npx -y @notionhq/notion-mcp-server") ||
		!strings.Contains(menu.Message, "State: connected") {
		t.Errorf("the per-server screen must still carry the details: %q", menu.Message)
	}
	m.Dialogs[0].SearchDesc = true
	m.Dialogs[0].Filter = "resources"
	m.Dialogs[0].Reindex()
	if len(m.Dialogs[0].FIdx) != 1 {
		t.Errorf("? must still search the description: FIdx = %v", m.Dialogs[0].FIdx)
	}
}

// The counts line up: they are one right-aligned column, whatever the
// name lengths are, and the frame is never pushed open by a long one.
func TestMcpRootCountsShareOneColumn(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 100, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	names := []string{"a", "linear", "notion-with-a-long-name", "sentry"}
	msg := McpMsg{Servers: []pirpc.McpServerInfo{
		{Name: "a", Enabled: true, State: "connected", Tools: []string{"x"}},
		{Name: "linear", Enabled: true, State: "connected", Tools: []string{"x", "y", "z"}},
		{Name: "notion-with-a-long-name", Enabled: true, State: "connected",
			Tools: []string{"x", "y", "z", "w", "v", "u"}},
		{Name: "sentry", Enabled: true, State: "connected", Tools: []string{"x", "y"}},
	}}
	msg.Options = names
	um, _ := m.Update(msg)
	m = um.(Model)
	out := stripANSI(mcpView(m))
	edges := map[string][]int{}
	for _, n := range names {
		row := mcpRowLine(out, n)
		if row == "" {
			t.Fatalf("row %q is missing:\n%s", n, out)
		}
		// Every count is three cells here, so a shared right edge means
		// a shared column.
		for _, c := range []string{"1/1", "0/3", "0/6", "0/2"} {
			if i := strings.Index(row, " "+c); i >= 0 {
				edges[n] = append(edges[n], i+len(c))
			}
		}
	}
	for n, e := range edges {
		if len(e) != 1 {
			t.Fatalf("row %q has no unambiguous count: %v", n, e)
		}
		for other, oe := range edges {
			if oe[0] != e[0] {
				t.Errorf("row %q's count ends at %d, row %q's at %d: the column is ragged\n%s",
					n, e[0], other, oe[0], out)
			}
		}
	}
	// A pathological name is clipped instead of pushing the count out.
	m.Dialogs[0].McpServers[0].Name = strings.Repeat("n", 120)
	for i := range m.Dialogs[0].Options {
		m.Dialogs[0].Options[i] = m.Dialogs[0].McpServers[i].Name
	}
	m.Dialogs[0].Options = append(m.Dialogs[0].Options, McpAddRow)
	m.Dialogs[0].Reindex()
	if got := lipgloss.Width(stripANSI(mcpView(m))); got > 100 {
		t.Errorf("a 120-character name opened the frame to %d cells:\n%s", got, stripANSI(mcpView(m)))
	}
}

// With the row text gone the dot is the only state signal, so every
// state must be distinguishable in the RENDERED OUTPUT — glyph, not
// just colour: a piped or copied list has no colour at all.
func TestMcpRootDotDistinguishesEveryState(t *testing.T) {
	states := []struct {
		name string
		srv  pirpc.McpServerInfo
		want string
	}{
		{"connected", pirpc.McpServerInfo{Enabled: true, State: "connected"}, "●"},
		{"needs-auth", pirpc.McpServerInfo{Enabled: true, State: "needs-auth"}, "◐"},
		{"failed", pirpc.McpServerInfo{Enabled: true, State: "failed"}, "✕"},
		{"connecting", pirpc.McpServerInfo{Enabled: true, State: "connecting"}, "◌"},
		{"disabled", pirpc.McpServerInfo{Enabled: false, State: "connected"}, "○"},
		{"starting", pirpc.McpServerInfo{Enabled: true, State: ""}, "·"},
	}
	seen := map[string]string{}
	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(mcpStateDot(tc.srv))
			if got != tc.want {
				t.Errorf("dot = %q, want %q", got, tc.want)
			}
			if prev, dup := seen[got]; dup {
				t.Errorf("%q and %q share the glyph %q", prev, tc.name, got)
			}
			seen[got] = tc.name
			// And it survives into the rendered row, colour included.
			raw := mcpStateDot(tc.srv)
			if stripANSI(raw) != tc.want {
				t.Errorf("stripped dot = %q, want %q", stripANSI(raw), tc.want)
			}
		})
	}
}

// mcpRowLine returns the frame line carrying a server name, without the
// box chrome, for the row-shape assertions above.
func mcpRowLine(box, name string) string {
	for _, ln := range strings.Split(box, "\n") {
		trimmed := strings.TrimRight(strings.TrimLeft(strings.TrimSpace(ln), "│ "), " │")
		if strings.Contains(trimmed, name) {
			return strings.TrimRight(trimmed, " ")
		}
	}
	return ""
}

// The census must never invent a number: with nothing configured it
// says zero, and the trailing add row is not a server.
func TestMcpSummaryCountsOnlyRealServers(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	m := Model{ready: true, ta: ta, winW: 100, winH: 40, hideSide: true, ModelLbl: "test"}
	m.ta.SetWidth(m.mainW() - 6)
	um, _ := m.Update(McpMsg{})
	m = um.(Model)
	out := stripANSI(mcpView(m))
	if !strings.Contains(out, "0 servers · 0 tools") {
		t.Errorf("an empty list must report zeros, not a fake count:\n%s", out)
	}
	if strings.Contains(out, "needs sign-in") {
		t.Errorf("no server needs sign-in, so the clause must be absent:\n%s", out)
	}
	if !strings.Contains(out, McpAddRow) {
		t.Errorf("the add row is the way out of an empty list:\n%s", out)
	}
	if !strings.Contains(out, "No MCP servers configured") {
		t.Errorf("pi's empty line must stay in the window:\n%s", out)
	}
}

// The hint block may only name keys that work on the SELECTED server:
// ^A and ^R are gated on the state, exactly like the menu rows are.
func TestMcpRootHintsGateOnSelectedServer(t *testing.T) {
	d := &Dialog{Kind: "mcp", Options: []string{"a", "b", McpAddRow},
		McpServers: []pirpc.McpServerInfo{
			{Name: "a", Enabled: true, State: "connected", Tools: []string{"x"}},
			{Name: "b", Enabled: true, State: "needs-auth"},
			{}, // the add row carries no server record
		}}
	d.Reindex()
	if got := strings.Join(mcpHints(d), " "); strings.Contains(got, "ctrl+a") {
		t.Errorf("a connected server needs no sign-in:\n%s", got)
	}
	if got := strings.Join(mcpHints(d), " "); !strings.Contains(got, "ctrl+d disable") {
		t.Errorf("an enabled server is disabled by ^D:\n%s", got)
	}
	d.Cursor = 1
	got := strings.Join(mcpHints(d), " ")
	if !strings.Contains(got, "ctrl+a sign in") {
		t.Errorf("a needs-auth server must advertise ^A:\n%s", got)
	}
	// The add row names no server: no per-server key is advertised.
	d.Cursor = 2
	got = strings.Join(mcpHints(d), " ")
	if strings.Contains(got, "ctrl+d") || strings.Contains(got, "ctrl+r") {
		t.Errorf("the add row has no server to act on:\n%s", got)
	}
	if !strings.Contains(got, "enter add") || !strings.Contains(got, "Add a server") {
		t.Errorf("the add row's own key must be named:\n%s", got)
	}
	// Ctrl+S is never advertised: every change is written immediately.
	d.Cursor = 0
	if got := strings.Join(mcpHints(d), " "); strings.Contains(got, "ctrl+s") {
		t.Errorf("there is nothing to save, so ^S must not be advertised: %s", got)
	}
}

// mcpKeyCall records what a registered handler was asked to do, so a
// test can assert the ROUTING (which key, which server) without
// running pi.
type mcpKeyCall struct{ key, server string }

var (
	mcpCallLog []mcpKeyCall
	mcpCallRun int
)

// mcpRecord is the stand-in handler: it notes the key and the row's
// server, and returns a command so the caller can tell a routed key
// from an ignored one.
func mcpRecord(_ *Model, d *Dialog, ri int, key string) tea.Cmd {
	srv := d.SelMcpRow(ri)
	mcpCallLog = append(mcpCallLog, mcpKeyCall{key: key, server: srv.Name})
	mcpCallRun++
	return func() tea.Msg { return McpConfigMsg{Server: srv.Name} }
}

// Every root key routes to the handler registered for it, with the
// selected row's server — the handler is builtin's, so this only
// asserts the routing and the row it hands over.
func TestMcpRootKeysRouteToTheRegisteredHandler(t *testing.T) {
	saved := mcpKeyFns
	t.Cleanup(func() { RegisterMcpKeys(saved) })
	mcpCallLog, mcpCallRun = nil, 0
	RegisterMcpKeys(map[string]McpKeyFunc{
		McpKeySpace: mcpRecord, McpKeySignIn: mcpRecord,
		McpKeyReconn: mcpRecord, McpKeyToggle: mcpRecord,
	})

	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	m.Dialogs[0].Cursor = 1 // "docs"
	for _, step := range []struct {
		key string
		km  tea.KeyMsg
	}{
		{McpKeySpace, tea.KeyMsg{Type: tea.KeySpace}},
		{McpKeyToggle, tea.KeyMsg{Type: tea.KeyCtrlD}},
		{McpKeyReconn, tea.KeyMsg{Type: tea.KeyCtrlR}},
		// ^A is gated on the state, so the row is put where pi would
		// offer Sign in before the key is pressed.
		{McpKeySignIn, tea.KeyMsg{Type: tea.KeyCtrlA}},
	} {
		if step.key == McpKeySignIn {
			m.Dialogs[0].McpServers[1].State = "needs-auth"
		}
		um, cmd := m.Update(step.km)
		m = um.(Model)
		if cmd == nil {
			t.Errorf("%s must schedule the handler", step.key)
			continue
		}
		if _, ok := cmd().(McpConfigMsg); !ok {
			t.Errorf("%s must return its command", step.key)
		}
	}
	if mcpCallRun != 4 {
		t.Fatalf("every key must reach its handler, ran %d times: %v", mcpCallRun, mcpCallLog)
	}
	for _, c := range mcpCallLog {
		if c.server != "docs" {
			t.Errorf("%s ran on %q, want the selected server", c.key, c.server)
		}
	}
	// The list survives every one of them (the handler is a command, not
	// a screen change).
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Errorf("a row key must leave the list open, got %+v", m.Dialogs)
	}
	// Space is a toggle, not a filter character, on this list.
	if m.Dialogs[0].Filter != "" {
		t.Errorf("space must not be typed into the filter, got %q", m.Dialogs[0].Filter)
	}
}

// A key that cannot act must be a safe no-op: the add row, an empty
// list, a state where the key is not offered, and no handler
// registered at all.
func TestMcpRootKeysNoOpWhenNothingToActOn(t *testing.T) {
	saved := mcpKeyFns
	t.Cleanup(func() { RegisterMcpKeys(saved) })
	ran := 0
	count := func(*Model, *Dialog, int, string) tea.Cmd { ran++; return nil }
	RegisterMcpKeys(map[string]McpKeyFunc{
		McpKeySpace: count, McpKeySignIn: count,
		McpKeyReconn: count, McpKeyToggle: count,
	})
	send := func(m Model, km tea.KeyMsg) Model {
		um, _ := m.Update(km)
		return um.(Model)
	}
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)

	// The add row: Enter is the only key that means anything.
	m.Dialogs[0].Cursor = len(m.Dialogs[0].Options) - 1
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	// "docs" is connected: it needs no sign-in.
	m.Dialogs[0].Cursor = 1
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if ran != 0 {
		t.Errorf("a key with nothing to act on must not run, ran %d times", ran)
	}
	// A server that pi has not started yet offers no reconnect either.
	m.Dialogs[0].McpServers[1].State = "starting"
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if ran != 0 {
		t.Errorf("ctrl+r on a starting server must not run, ran %d times", ran)
	}
	// With no handler registered (a bare app.Model), no key panics.
	RegisterMcpKeys(nil)
	m = send(m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.Dialogs) != 1 {
		t.Errorf("the list must stay open, got %+v", m.Dialogs)
	}
}

// ? widens the search from names to the row descriptions (which carry
// the state), and back again. The other keys keep working with it on.
func TestMcpQuestionMarkTogglesDescriptionSearch(t *testing.T) {
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	d := m.Dialogs[0]
	if d.SearchDesc {
		t.Fatal("description search starts off")
	}
	// "connected" matches no NAME, and by default no description either:
	// a server's description carries its state, so one letter must not
	// select every live server.
	d.Filter = "connected"
	d.Reindex()
	if len(d.FIdx) != 0 {
		t.Fatalf("descriptions must not match by default, FIdx = %v", d.FIdx)
	}
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = um.(Model)
	if !m.Dialogs[0].SearchDesc {
		t.Fatal("? must turn description search on")
	}
	if len(m.Dialogs[0].FIdx) != 1 || m.Dialogs[0].Options[m.Dialogs[0].FIdx[0]] != "docs" {
		t.Fatalf("with descriptions searched, FIdx = %v, want the connected server", m.Dialogs[0].FIdx)
	}
	if !strings.Contains(stripANSI(mcpView(m)), "◎  desc: connected▌") {
		t.Errorf("the search line must show that descriptions are searched:\n%s", stripANSI(mcpView(m)))
	}
	// Back off again.
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = um.(Model)
	if m.Dialogs[0].SearchDesc || len(m.Dialogs[0].FIdx) != 0 {
		t.Errorf("? must toggle back, got SearchDesc=%v FIdx=%v", m.Dialogs[0].SearchDesc, m.Dialogs[0].FIdx)
	}
	// A server name has no "?", so nothing is lost by spending the key
	// on the toggle: filtering by name still works.
	m.Dialogs[0].Filter = "doc"
	m.Dialogs[0].Reindex()
	if len(m.Dialogs[0].FIdx) != 1 {
		t.Errorf("typing still filters by name, FIdx = %v", m.Dialogs[0].FIdx)
	}
	// Other pickers are untouched: their descriptions always match.
	other := &Dialog{Kind: "settings", Options: []string{"alpha"}, Descs: []string{"beta words"}}
	other.Reindex()
	other.Filter = "words"
	other.Reindex()
	if len(other.FIdx) != 1 {
		t.Errorf("only /mcp gates its descriptions, got FIdx = %v", other.FIdx)
	}
}

// Ctrl+C closes the window exactly like Esc, and neither one eats the
// list's own state.
func TestMcpCtrlCClosesTheWindow(t *testing.T) {
	m := Model{}
	um, _ := m.Update(mcpTestMsg())
	m = um.(Model)
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Errorf("ctrl+c must close the manager, got %+v", m.Dialogs)
	}
	um, _ = m.Update(mcpTestMsg())
	m = um.(Model)
	um, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Errorf("esc must close the manager, got %+v", m.Dialogs)
	}
}
