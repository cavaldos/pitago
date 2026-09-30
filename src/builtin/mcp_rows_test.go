package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/pirpc"
)

func mcpSrv(mut ...func(*pirpc.McpServerInfo)) pirpc.McpServerInfo {
	s := pirpc.McpServerInfo{
		Name: "srv", Scope: "global", Source: "/home/u/.pi/agent/mcp.json",
		Enabled: true, Exposure: "codemode",
		Transport: "npx -y some-mcp", State: "connected",
	}
	for _, f := range mut {
		f(&s)
	}
	return s
}

// pi's serversMenu sorts by attentionRank and then by name: the servers
// that need the user first, in pi's exact rank order.
func TestMcpOrderingAttentionFirst(t *testing.T) {
	tests := []struct {
		name string
		in   []pirpc.McpServerInfo
		want []string
	}{
		{
			name: "needs-auth then failed then disconnected then starting then connected then disabled",
			in: []pirpc.McpServerInfo{
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "z-conn"; s.State = "connected" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "y-dis"; s.State = "disconnected" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "a-auth"; s.State = "needs-auth" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "b-fail"; s.State = "failed" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "c-start"; s.State = "" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "d-off"; s.Enabled = false; s.State = "connected" }),
			},
			want: []string{"a-auth", "b-fail", "y-dis", "c-start", "z-conn", "d-off"},
		},
		{
			name: "equal ranks fall back to the name, not the input order",
			in: []pirpc.McpServerInfo{
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "beta" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "alpha" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "gamma" }),
			},
			want: []string{"alpha", "beta", "gamma"},
		},
		{
			name: "a disabled server outranks nothing: it is listed last",
			in: []pirpc.McpServerInfo{
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "off"; s.Enabled = false; s.State = "needs-auth" }),
				mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "on"; s.State = "connected" }),
			},
			want: []string{"on", "off"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, s := range mcpOrdered(tc.in) {
				got = append(got, s.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("order = %v, want %v", got, tc.want)
			}
		})
	}
}

// Options, Descs and McpServers are indexed by the same number, so they
// must all be derived from the SAME ordered copy — a row that points at
// another server's state is worse than no list at all.
func TestMcpRowSlicesAreIndexParallel(t *testing.T) {
	ordered := mcpOrdered([]pirpc.McpServerInfo{
		mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "z" }),
		mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "a"; s.State = "needs-auth" }),
	})
	labels := mcpRowLabels(ordered)
	descs := mcpRowDescs(ordered)
	if len(labels) != len(ordered) || len(descs) != len(ordered) {
		t.Fatalf("parallel slices differ in length: %d/%d/%d", len(labels), len(descs), len(ordered))
	}
	if labels[0] != "a" || labels[1] != "z" {
		t.Fatalf("labels = %v", labels)
	}
	if !strings.HasPrefix(descs[0], "needs sign-in") {
		t.Errorf("row 0 desc = %q, want the needs-auth state", descs[0])
	}
	if !strings.Contains(descs[0], "codemode · global") {
		t.Errorf("row 0 desc = %q, want exposure and scope after the state", descs[0])
	}
}

// pi's row description is `${state} · ${exposure} · ${scope ?? source}`.
func TestMcpRowDescsMatchPi(t *testing.T) {
	tests := []struct {
		name string
		srv  pirpc.McpServerInfo
		want string
	}{
		{
			name: "connected counts tools and resources",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Tools = []string{"a", "b"}; s.Resources = 3 }),
			want: "connected · 2 tools · 3 resources · codemode · global",
		},
		{
			name: "one tool is singular",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Tools = []string{"a"} }),
			want: "connected · 1 tool · codemode · global",
		},
		{
			name: "failed carries the first line of the error",
			srv: mcpSrv(func(s *pirpc.McpServerInfo) {
				s.State = "failed"
				s.Error = "spawn npx ENOENT\nsecond line"
			}),
			want: "failed: spawn npx ENOENT · codemode · global",
		},
		{
			name: "disabled wins over the state",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Enabled = false }),
			want: "disabled · codemode · global",
		},
		{
			name: "a server with no scope falls back to the source path",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Scope = "" }),
			want: "connected · 0 tools · codemode · /home/u/.pi/agent/mcp.json",
		},
		{
			name: "a missing exposure reads as the default",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Exposure = "" }),
			want: "connected · 0 tools · codemode · global",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpRowDescs([]pirpc.McpServerInfo{tc.srv})[0]; got != tc.want {
				t.Errorf("desc = %q, want %q", got, tc.want)
			}
		})
	}
}

// pi's per-server menu, with pi's gating: a disabled server offers only
// Enable; the OAuth-only rows appear only where they can work.
func TestMcpMenuRowsMatchPi(t *testing.T) {
	tests := []struct {
		name string
		srv  pirpc.McpServerInfo
		want []string
	}{
		{
			name: "disabled offers only Enable",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.Enabled = false; s.State = "failed" }),
			want: []string{McpActEnable},
		},
		{
			name: "connected stdio: tools, reconnect, exposure, disable (no sign-in, no sign-out)",
			srv:  mcpSrv(),
			want: []string{McpActTools, McpActReconnect, McpActExposure, McpActDisable},
		},
		{
			name: "needs sign-in puts Sign in first",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.State = "needs-auth" }),
			want: []string{McpActSignIn, McpActReconnect, McpActExposure, McpActDisable},
		},
		{
			name: "a failed stdio server offers no Tools and no sign-out",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.State = "failed" }),
			want: []string{McpActReconnect, McpActExposure, McpActDisable},
		},
		{
			name: "a connected HTTP server can sign out (it is the OAuth-capable shape)",
			srv: mcpSrv(func(s *pirpc.McpServerInfo) {
				s.Transport = "https://mcp.sentry.dev/mcp"
			}),
			want: []string{McpActTools, McpActReconnect, McpActSignOut, McpActExposure, McpActDisable},
		},
		{
			name: "a starting server offers neither Reconnect nor Tools",
			srv:  mcpSrv(func(s *pirpc.McpServerInfo) { s.State = "" }),
			want: []string{McpActExposure, McpActDisable},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, descs := mcpMenuRows(tc.srv)
			if strings.Join(opts, "|") != strings.Join(tc.want, "|") {
				t.Errorf("menu = %v, want %v", opts, tc.want)
			}
			if len(descs) != len(opts) {
				t.Errorf("descs (%d) must parallel options (%d)", len(descs), len(opts))
			}
		})
	}
}

// The two write rows say where the change lands, in pi's wording.
func TestMcpMenuSavedDescs(t *testing.T) {
	opts, descs := mcpMenuRows(mcpSrv(func(s *pirpc.McpServerInfo) { s.Scope = "project" }))
	for i, o := range opts {
		if o == McpActDisable && descs[i] != "saved to the project mcp.json" {
			t.Errorf("disable desc = %q", descs[i])
		}
	}
	extOpts, extDescs := mcpMenuRows(mcpSrv(func(s *pirpc.McpServerInfo) { s.Scope = "extension" }))
	for i, o := range extOpts {
		if o == McpActDisable && extDescs[i] != "for this session" {
			t.Errorf("an extension server is not persisted, desc = %q", extDescs[i])
		}
	}
}

// The exposure picker is pi's four values, in pi's order, with the ✓
// on the current one. `hidden` is not offered: pi's menu does not offer
// it either (its EXPOSURE_DESCRIPTIONS has four keys), even though
// mcp.json accepts it — pitago does not widen the set on the TUI.
func TestMcpExposureMenuMatchesPi(t *testing.T) {
	menu := mcpExposureMenu(mcpSrv())
	want := []string{"✓ codemode", "  codemode-deferred", "  deferred", "  direct"}
	if strings.Join(menu.Options, "|") != strings.Join(want, "|") {
		t.Errorf("exposure rows = %v, want %v", menu.Options, want)
	}
	if menu.Kind != "mcpExposure" || menu.McpServer.Name != "srv" {
		t.Errorf("menu kind/server = %q/%q", menu.Kind, menu.McpServer.Name)
	}
	if !strings.Contains(menu.Message, "/home/u/.pi/agent/mcp.json") {
		t.Errorf("the header must name the file the change is saved to: %q", menu.Message)
	}
	// A non-default current value carries its own ✓.
	menu = mcpExposureMenu(mcpSrv(func(s *pirpc.McpServerInfo) { s.Exposure = "deferred" }))
	if menu.Options[2] != "✓ deferred" {
		t.Errorf("the current value must carry the ✓: %v", menu.Options)
	}
	// `hidden` is legal in mcp.json but is NOT one of pi's four menu
	// values, so no row is checked and the header still says what the
	// server is actually set to. A menu that silently claimed a
	// different value would be worse than an unchecked list.
	menu = mcpExposureMenu(mcpSrv(func(s *pirpc.McpServerInfo) { s.Exposure = "hidden" }))
	for _, o := range menu.Options {
		if strings.HasPrefix(o, "✓") {
			t.Errorf("hidden is not in pi's menu and must not be pre-checked: %v", menu.Options)
		}
	}
	if !strings.Contains(menu.Message, "now hidden: registered but not callable") {
		t.Errorf("the header must state the real current value: %q", menu.Message)
	}
}

// Enter on the ✓ row cycles to the next value instead of saving the
// value already in force; the four-value set stays the only reachable
// one and it wraps.
func TestNextMcpExposureCycles(t *testing.T) {
	want := map[string]string{
		"codemode":          "codemode-deferred",
		"codemode-deferred": "deferred",
		"deferred":          "direct",
		"direct":            "codemode",
		"hidden":            "codemode", // not in the cycle: back to the default
		"":                  "codemode", // unset reads as the default
		"nonsense":          "codemode",
	}
	for cur, next := range want {
		if got := nextMcpExposure(cur); got != next {
			t.Errorf("nextMcpExposure(%q) = %q, want %q", cur, got, next)
		}
	}
	// Walking the cycle from any start visits all four and returns home.
	seen := map[string]bool{}
	cur := "codemode"
	for i := 0; i < 4; i++ {
		cur = nextMcpExposure(cur)
		seen[cur] = true
	}
	if len(seen) != 4 {
		t.Errorf("the cycle must cover every offered exposure, got %v", seen)
	}
}

func TestMcpExposureValid(t *testing.T) {
	for _, v := range []string{"codemode", "codemode-deferred", "deferred", "direct", "hidden"} {
		if !mcpExposureValid(v) {
			t.Errorf("%q must be a legal mcp.json exposure", v)
		}
	}
	for _, v := range []string{"", "CODEMODE", "Direct", "yes", ".."} {
		if mcpExposureValid(v) {
			t.Errorf("%q must not be accepted", v)
		}
	}
}

// `/mcp login <server>` and friends run the action directly, and a
// third word is pi's usage line rather than a truncated command.
func TestParseMcpArg(t *testing.T) {
	tests := []struct {
		arg                      string
		action, server, extraStr string
		extra                    bool
	}{
		{arg: "", action: "", server: ""},
		{arg: "login", action: "login", server: ""},
		{arg: "logout", action: "logout", server: ""},
		{arg: "reconnect", action: "reconnect", server: ""},
		{arg: "login sentry", action: "login", server: "sentry"},
		{arg: "  LOGOUT   ctx7 ", action: "logout", server: "ctx7"},
		{arg: "reconnect notion-suekou", action: "reconnect", server: "notion-suekou"},
		{arg: "login a b", action: "login", server: "a", extra: true, extraStr: "a"},
	}
	for _, tc := range tests {
		action, server, extra := parseMcpArg(tc.arg)
		if action != tc.action || server != tc.server || extra != tc.extra {
			t.Errorf("parseMcpArg(%q) = %q,%q,%v; want %q,%q,%v",
				tc.arg, action, server, extra, tc.action, tc.server, tc.extra)
		}
	}
}

func TestValidMcpAction(t *testing.T) {
	for _, a := range []string{"login", "logout", "reconnect"} {
		if !validMcpAction(a) {
			t.Errorf("%q is one of pi's three subcommands", a)
		}
	}
	for _, a := range []string{"", "add", "remove", "list", "signin", "enable"} {
		if validMcpAction(a) {
			t.Errorf("%q is not a subcommand and must not run an action", a)
		}
	}
}

// A server name pi would not accept never reaches the pi CLI: the
// action is refused with a message instead of being exec'd.
func TestOpenMcpRefusesInvalidServerName(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	for _, bad := range []string{"a;touch" + marker, "../x", "a;touch_" + filepath.Base(marker)} {
		m := &app.Model{}
		m.UseBuiltins(All(), Confirmers())
		if cmd := openMcp(m, "login "+bad); cmd != nil {
			t.Errorf("openMcp(login %q) must not schedule any command", bad)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the name reached the shell")
		}
		if m.NoticeCount() == 0 {
			t.Fatalf("a refusal must be reported: /mcp login %q", bad)
		}
		if !strings.Contains(m.LastNotice(), "is not an MCP server name") {
			t.Errorf("refusal for %q = %q", bad, m.LastNotice())
		}
	}
	// A name with a space is two words, so it is pi's usage line, not a
	// silent partial action.
	m := &app.Model{}
	if cmd := openMcp(m, "login has space"); cmd != nil {
		t.Error("a two-word argument must not run anything")
	}
	if !strings.Contains(m.LastNotice(), "Usage: /mcp") {
		t.Errorf("expected the usage line, got %q", m.LastNotice())
	}
}

func TestOpenMcpUnknownActionPrintsUsage(t *testing.T) {
	for _, arg := range []string{"add foo", "signin sentry", "login a b"} {
		m := &app.Model{}
		if cmd := openMcp(m, arg); cmd != nil {
			t.Errorf("/mcp %q must not run anything", arg)
		}
		if m.NoticeCount() == 0 || !strings.Contains(m.LastNotice(), "Usage: /mcp") {
			t.Errorf("/mcp %q must print pi's usage line, got %q", arg, m.LastNotice())
		}
	}
}

// Enter on a server row opens the action menu IN FRONT of the list
// (Dialogs[0] is the active dialog) and Esc then lands back on the list
// with the same row selected — pi's two-step shape.
func TestConfirmMcpOpensMenuOverList(t *testing.T) {
	srv := mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "sentry"; s.State = "needs-auth" })
	list := &app.Dialog{Kind: "mcp", Title: "MCP servers",
		Options: []string{"other", "sentry"}, McpServers: []pirpc.McpServerInfo{
			mcpSrv(func(s *pirpc.McpServerInfo) { s.Name = "other" }), srv}}
	list.Reindex()
	list.Cursor = 1 // the sentry row
	m := &app.Model{Dialogs: []*app.Dialog{list}}

	if _, cmd := confirmMcp(m, list, 1); cmd != nil {
		t.Error("opening the menu is synchronous")
	}
	if len(m.Dialogs) != 2 {
		t.Fatalf("dialogs = %d, want the menu in front of the list", len(m.Dialogs))
	}
	menu := m.Dialogs[0]
	if menu.Kind != "mcpAction" || menu.Title != "MCP server sentry" {
		t.Fatalf("menu = %q/%q", menu.Kind, menu.Title)
	}
	if menu.SelMcpServer().Name != "sentry" {
		t.Errorf("the menu belongs to %q, want sentry", menu.SelMcpServer().Name)
	}
	if !strings.Contains(menu.Message, "needs sign-in") {
		t.Errorf("the header must carry the state: %q", menu.Message)
	}
	// Esc pops the menu only: the list survives with its cursor.
	m.PopMcpMenu()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "mcp" {
		t.Fatalf("Esc must land back on the list, got %d dialogs", len(m.Dialogs))
	}
	if m.Dialogs[0].Cursor != 1 {
		t.Errorf("list cursor = %d, want the same row still selected", m.Dialogs[0].Cursor)
	}
}

// The failure text of a broken server is reachable: it is in the menu
// header, which is where pi puts it.
func TestMcpMenuDetailsCarryTheError(t *testing.T) {
	srv := mcpSrv(func(s *pirpc.McpServerInfo) {
		s.State = "failed"
		s.Error = "Failed to resolve MCP server \"notion\" env \"NOTION_TOKEN\""
	})
	details := app.McpMenuDetails(srv)
	for _, want := range []string{"npx -y some-mcp", "global: /home/u/.pi/agent/mcp.json", "State: failed", "NOTION_TOKEN"} {
		if !strings.Contains(details, want) {
			t.Errorf("details missing %q:\n%s", want, details)
		}
	}
	// A connected server's error is not shown (there is none).
	srv.State, srv.Error = "connected", "stale"
	if strings.Contains(app.McpMenuDetails(srv), "stale") {
		t.Error("a connected server must not show an error line")
	}
}

// The Tools view lists the tool names pi printed, prefixed so the model
// can see the name it would actually call.
func TestMcpToolsMenuListsOfferedTools(t *testing.T) {
	menu := mcpToolsMenu(mcpSrv(func(s *pirpc.McpServerInfo) {
		s.Tools = []string{"notion_search", "notion_fetch"}
	}))
	if menu.Kind != "mcpTools" || len(menu.Options) != 2 {
		t.Fatalf("tools menu = %q with %d rows", menu.Kind, len(menu.Options))
	}
	if !strings.Contains(menu.Message, "Exposure codemode") {
		t.Errorf("the header must name the server exposure: %q", menu.Message)
	}
	if menu.Descs[0] != "mcp__srv__notion_search" {
		t.Errorf("tool desc = %q", menu.Descs[0])
	}
	// A server that offers nothing says so rather than showing an
	// empty box.
	empty := mcpToolsMenu(mcpSrv())
	if len(empty.Options) != 1 || !strings.Contains(empty.Options[0], "no tools") {
		t.Errorf("empty tools view = %v", empty.Options)
	}
}

// Saving an exposure writes into the mcp.json the server came from and
// keeps every other key — the whole point of routing through `source`.
func TestConfirmMcpExposureWritesBack(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte(
		`{"mcpServers":{"srv":{"command":"npx","args":["-y","m"],"exposure":"deferred"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := mcpSrv(func(s *pirpc.McpServerInfo) { s.Source = file; s.Exposure = "deferred" })
	m := &app.Model{}
	menu := mcpExposureMenu(srv)
	m.Dialogs = []*app.Dialog{menu}
	// Pick "direct" (index 3).
	_, runCmd := confirmMcpExposure(m, menu, 3)
	if runCmd == nil {
		t.Fatal("picking a new exposure must schedule the write")
	}
	msg, ok := runCmd().(app.McpConfigMsg)
	if !ok {
		t.Fatalf("write must report app.McpConfigMsg, got %T", runCmd())
	}
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if !strings.Contains(msg.Notice, "exposure → direct") {
		t.Errorf("notice = %q", msg.Notice)
	}
	root := pirpc.ReadPiSettingsAt(file)
	servers, _ := pirpc.GetPiSetting(root, "mcpServers").(map[string]any)
	entry, _ := servers["srv"].(map[string]any)
	if entry["exposure"] != "direct" {
		t.Errorf("exposure not written: %v", entry)
	}
	if entry["command"] != "npx" {
		t.Errorf("sibling key lost: %v", entry)
	}
}

// The ✓ row is a cycle, not a no-op save.
func TestConfirmMcpExposureCyclesOnCurrent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{"srv":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := mcpSrv(func(s *pirpc.McpServerInfo) { s.Source = file }) // exposure = codemode
	m := &app.Model{}
	menu := mcpExposureMenu(srv)
	m.Dialogs = []*app.Dialog{menu}
	_, cmd := confirmMcpExposure(m, menu, 0) // the ✓ codemode row
	if cmd == nil {
		t.Fatal("Enter on the current exposure must cycle, not do nothing")
	}
	msg, ok := cmd().(app.McpConfigMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("cycle must save the next value: %+v", msg)
	}
	if !strings.Contains(msg.Notice, "→ codemode-deferred") {
		t.Errorf("cycle notice = %q, want the next exposure", msg.Notice)
	}
	root := pirpc.ReadPiSettingsAt(file)
	servers, _ := pirpc.GetPiSetting(root, "mcpServers").(map[string]any)
	entry, _ := servers["srv"].(map[string]any)
	if entry["exposure"] != "codemode-deferred" {
		t.Errorf("cycled exposure not written: %v", entry)
	}
}

// Enable/Disable route through the same write path, with pi's
// enable-true-removes-the-key rule.
func TestConfirmMcpActionEnableDisable(t *testing.T) {
	tests := []struct {
		action  string
		row     int
		enabled bool
		wantKey bool
	}{
		{action: McpActEnable, row: 0, enabled: true, wantKey: false},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "mcp.json")
			if err := os.WriteFile(file, []byte(
				`{"mcpServers":{"srv":{"command":"npx","enabled":false}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			srv := mcpSrv(func(s *pirpc.McpServerInfo) { s.Source = file; s.Enabled = false; s.State = "" })
			m := &app.Model{}
			menu := &app.Dialog{Kind: "mcpAction", Options: []string{McpActEnable}, McpServer: srv}
			_, cmd := confirmMcpAction(m, menu, tc.row)
			if cmd == nil {
				t.Fatal("Enable must schedule the write")
			}
			msg, ok := cmd().(app.McpConfigMsg)
			if !ok || msg.Err != nil {
				t.Fatalf("write failed: %+v", msg)
			}
			root := pirpc.ReadPiSettingsAt(file)
			servers, _ := pirpc.GetPiSetting(root, "mcpServers").(map[string]any)
			entry, _ := servers["srv"].(map[string]any)
			if _, has := entry["enabled"]; has != tc.wantKey {
				t.Errorf("enabled key present = %v, want %v (%v)", has, tc.wantKey, entry)
			}
		})
	}
	// Disable is the mirror: enabled:false is written.
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{"srv":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := mcpSrv(func(s *pirpc.McpServerInfo) { s.Source = file })
	m := &app.Model{}
	opts, _ := mcpMenuRows(srv)
	menu := &app.Dialog{Kind: "mcpAction", Options: opts, McpServer: srv}
	idx := -1
	for i, o := range opts {
		if o == McpActDisable {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("Disable must be on the menu")
	}
	_, cmd := confirmMcpAction(m, menu, idx)
	if cmd == nil {
		t.Fatal("Disable must schedule the write")
	}
	msg, ok := cmd().(app.McpConfigMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("disable write failed: %+v", msg)
	}
	root := pirpc.ReadPiSettingsAt(file)
	servers, _ := pirpc.GetPiSetting(root, "mcpServers").(map[string]any)
	entry, _ := servers["srv"].(map[string]any)
	if entry["enabled"] != false {
		t.Errorf("enabled:false not written: %v", entry)
	}
}

// /mcp must be INTERCEPTED: registered as a pi-origin builtin, so the
// literal text "/mcp" can never reach pi as a prompt. Before this the
// command fell through and the model got the string "/mcp".
func TestMcpIsInterceptedNotSentAsPrompt(t *testing.T) {
	var found *app.Builtin
	for i, b := range All() {
		if b.Name == "mcp" {
			found = &All()[i]
			break
		}
	}
	if found == nil {
		t.Fatal("/mcp is not registered: it would be sent to the model as prompt text")
	}
	if found.Origin != OriginPi {
		t.Errorf("Origin = %q, want %q (pi parity re-implementation)", found.Origin, OriginPi)
	}
	if found.Hidden {
		t.Error("/mcp is a user command and must appear in the / popup")
	}
	if !strings.Contains(found.Usage, "login") || !strings.Contains(found.Usage, "reconnect") {
		t.Errorf("usage must document pi's subcommands: %q", found.Usage)
	}
	// The row builder runs from a real Model through the registry, so
	// FindBuiltin resolves it exactly as the input path does.
	m := &app.Model{}
	m.UseBuiltins(All(), Confirmers())
	b, arg, ok := m.FindBuiltin("/mcp")
	if !ok || b.Name != "mcp" {
		t.Fatalf("FindBuiltin(/mcp) = %v,%q,%v", b, arg, ok)
	}
	if arg != "" {
		t.Errorf("arg = %q, want empty", arg)
	}
	// The hidden continuation is registered too, so the post-action
	// refresh can re-enter it.
	if _, _, ok := m.FindBuiltin("/" + app.BuiltinMcpList); ok {
		t.Error("the mcp-list continuation must stay out of slash interception")
	}
	hidden := false
	for _, b := range All() {
		if b.Name == app.BuiltinMcpList {
			hidden = b.Hidden
		}
	}
	if !hidden {
		t.Error("the mcp-list continuation must be Hidden")
	}
	if _, ok := Confirmers()["mcp"]; !ok {
		t.Error("the mcp list needs an Enter handler")
	}
	for _, kind := range []string{"mcpAction", "mcpExposure", "mcpTools"} {
		if _, ok := Confirmers()[kind]; !ok {
			t.Errorf("%s has no Enter handler, so Enter would do nothing", kind)
		}
	}
}
