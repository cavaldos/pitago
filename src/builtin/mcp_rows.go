package builtin

// The local re-implementation of pi's /mcp server manager.
//
// pi's /mcp is a TUI builtin (dist/extensions/mcp/index.js driving
// McpManagerView in dist/extensions/mcp/ui.js), so it never arrives
// over the JSONL RPC and pitago has to re-implement it. The shape
// copied here is pi's own: a list of servers with their state, tool
// count, exposure and defining mcp.json, servers needing attention
// first, and a per-server action menu opened with Enter that Esc
// backs out of.
//
// The data comes from `pi mcp list --json` (see src/pirpc/mcp.go);
// exposure and enable/disable are written back into that server's
// mcp.json, because pi has no CLI verb for editing them.

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// mcpUsage is pi's own usage line for the subcommand form.
const mcpUsage = "Usage: /mcp, /mcp login [server], /mcp logout [server], " +
	"/mcp reconnect [server], /mcp add <name> [--url <url> | -- <command>...], /mcp remove <name>"

// Action row labels. Exported so the builder and the dispatcher cannot
// drift apart on a label, like the Tree action menu's.
const (
	McpActSignIn    = "Sign in"
	McpActTools     = "Tools"
	McpActReconnect = "Reconnect"
	McpActSignOut   = "Sign out"
	McpActExposure  = "Exposure"
	McpActDisable   = "Disable"
	McpActEnable    = "Enable"
	McpActBack      = "Back to servers"
)

// mcpExposureOrder is pi's exposure menu order
// (Object.keys(EXPOSURE_DESCRIPTIONS) in the extension). `hidden` is
// deliberately absent from pi's menu even though mcp.json accepts it,
// so the list matches pi: four selectable values.
var mcpExposureOrder = []string{"codemode", "codemode-deferred", "deferred", "direct"}

// mcpExposureDescs is pi's EXPOSURE_DESCRIPTIONS, verbatim.
var mcpExposureDescs = map[string]string{
	"codemode":          "called from codemode scripts, listed in the codemode description",
	"codemode-deferred": "called from codemode scripts, not listed; scripts find them with searchTools()",
	"deferred":          "not declared until tool_search loads them, then called directly; no codemode needed",
	"direct":            "declared to the model like built-in tools",
}

// mcpExposureValid reports whether v is an exposure pi's mcp.json
// accepts, including `hidden` (which pi's own /mcp menu cannot
// select but a hand-edited file may carry).
func mcpExposureValid(v string) bool {
	if v == "hidden" {
		return true
	}
	_, ok := mcpExposureDescs[v]
	return ok
}

// nextMcpExposure is the value after cur in pi's cycle, wrapping at the
// end. It backs the one-key arm of the exposure picker: Enter on the
// row that already carries the ✓ (a no-op save in pi) cycles to the
// next value instead, which is the same set of reachable values in a
// single keystroke. The four-item picker stays the primary arm.
func nextMcpExposure(cur string) string {
	for i, v := range mcpExposureOrder {
		if v == cur {
			return mcpExposureOrder[(i+1)%len(mcpExposureOrder)]
		}
	}
	return mcpExposureOrder[0] // unknown/missing → the default, codemode
}

// openMcp is the /mcp entry point. With no argument it opens the
// manager; the subcommands run their action directly, exactly
// like pi outside its TUI.
func openMcp(m *app.Model, arg string) tea.Cmd {
	// add/remove take a free-form tail (a stdio server is
	// `-- npx -y @scope/pkg`), so they are split on their own terms and
	// bypass parseMcpArg's two-word rule.
	switch firstWord(arg) {
	case "add":
		return mcpWrite(m, "add", arg)
	case "remove":
		return mcpWrite(m, "remove", arg)
	}
	action, server, extra := parseMcpArg(arg)
	if extra || (action != "" && !validMcpAction(action)) {
		m.AddBlock(app.Block{Kind: "notice", Text: mcpUsage, Err: true})
		m.Refresh()
		return nil
	}
	switch action {
	case "login", "logout":
		if server == "" {
			// pi picks a server interactively here; pitago's picker is
			// the manager itself, so a bare action opens it and says so
			// rather than guessing a name.
			m.AddBlock(app.Block{Kind: "notice", Text: "pick a server in /mcp, or name it: /mcp " + action + " <server>"})
			m.Refresh()
			return loadMcp(m)
		}
		if !pirpc.ValidMcpServerName(server) {
			m.AddBlock(app.Block{Kind: "notice",
				Text: fmt.Sprintf("%q is not an MCP server name — letters, digits, _ and - only", server), Err: true})
			m.Refresh()
			return nil
		}
		if action == "login" {
			// Long-running and browser-bound: the status line is the
			// only progress, and the CLI runs in the background slot so
			// the UI never blocks (pi's own default is a 300s wait).
			m.Status = "signing in to " + server + " — approve access in your browser…"
		} else {
			m.Status = "signing out of " + server + "…"
		}
		m.Refresh()
		if action == "login" {
			return m.McpLoginCmd(server)
		}
		return m.McpLogoutCmd(server)
	case "reconnect":
		// pi has no `pi mcp reconnect` verb: listing IS the connect
		// (loadMcpConfig + a fresh connect for every enabled server), so
		// a reconnect is a second list run.
		m.Status = "reconnecting " + server + "…"
		m.Refresh()
		return loadMcp(m)
	}
	return loadMcp(m)
}

// parseMcpArg splits "/mcp <action> [server]". It reports extra=true
// when there are more than two words, which pi answers with its usage
// line rather than acting on a truncated command.
func parseMcpArg(arg string) (action, server string, extra bool) {
	fields := strings.Fields(arg)
	switch len(fields) {
	case 0:
		return "", "", false
	case 1:
		return strings.ToLower(fields[0]), "", false
	case 2:
		return strings.ToLower(fields[0]), fields[1], false
	default:
		return strings.ToLower(fields[0]), fields[1], true
	}
}

// validMcpAction reports whether a is one of pi's subcommands.
func validMcpAction(a string) bool {
	switch a {
	case "login", "logout", "reconnect", "add", "remove":
		return true
	}
	return false
}

// firstWord is the leading lowercase word of an argument string.
func firstWord(arg string) string {
	w, _, _ := strings.Cut(strings.TrimSpace(arg), " ")
	return strings.ToLower(strings.TrimSpace(w))
}

// mcpWrite runs `pi mcp add|remove`, the two verbs that change which
// servers exist at all. Pi has no UI for either, so without this the
// window can only ever manage a list the user built by hand-editing
// mcp.json — an empty list was a dead end.
//
// The name is checked against pi's own rule BEFORE exec: RunMcp passes
// argv without a shell, but pi re-parses the name, and a name is
// untrusted input (a typed or pasted `/mcp add <string>`).
func mcpWrite(m *app.Model, verb, arg string) tea.Cmd {
	_, rest, _ := strings.Cut(strings.TrimSpace(arg), " ")
	rest = strings.TrimSpace(rest)
	// `-l`/`--local` is pi's scope flag, and it only counts as one in
	// the LEADING position. Anywhere else it belongs to whatever follows
	// the `--` separator and is part of the server's own command line
	// (`npx -l`, `docker -l`): scanning the tail for the substring
	// silently deleted those, and taking the first word as the name
	// turned `add -l docs …` into a server literally named "-l".
	// Anything not leading goes through untouched — pi reads its own
	// flags in either position.
	local := false
	if flag, tail, found := strings.Cut(rest, " "); found &&
		(flag == "-l" || flag == "--local") {
		rest, local = strings.TrimSpace(tail), true
	}
	name, tail, _ := strings.Cut(rest, " ")
	if name == "" {
		m.AddBlock(app.Block{Kind: "notice", Text: "Usage: /mcp " + verb +
			" <name> " + mcpWriteTail(verb), Err: true})
		m.Refresh()
		return nil
	}
	if !pirpc.ValidMcpServerName(name) {
		m.AddBlock(app.Block{Kind: "notice",
			Text: fmt.Sprintf("%q is not an MCP server name — letters, digits, _ and - only", name), Err: true})
		m.Refresh()
		return nil
	}
	argv := []string{}
	if local {
		argv = append(argv, "-l")
	}
	argv = append(argv, name)
	if tail = strings.TrimSpace(tail); tail != "" {
		argv = append(argv, strings.Fields(tail)...)
	}
	m.Status = verb + "ing " + name + "…"
	m.Refresh()
	// add does not connect and remove is a file write, so this is the
	// short timeout, not the browser wait a login needs.
	return m.McpWriteCmd(verb, argv)
}

// mcpWriteTail is the tail hint in the add/remove usage line.
func mcpWriteTail(verb string) string {
	if verb == "remove" {
		return "[-l]"
	}
	return "[--url <url> | -- <command>...]"
}

// loadMcp runs `pi mcp list --json` and turns it into the manager's
// rows. It is the shared backend of the manager, "reconnect" and every
// post-action refresh (the hidden app.BuiltinMcpList continuation), so
// the row wording has exactly one source.
func loadMcp(m *app.Model) tea.Cmd {
	m.Status = "loading MCP servers…"
	m.Refresh()
	bin := m.PiBin()
	return func() tea.Msg {
		// The adapter is the MCP host whenever it is installed, because
		// `pi mcp list` only ever reads ~/.pi/agent/mcp.json — a file
		// that does not exist in that setup, so it reports zero servers
		// while six are live. Reading the adapter's own files is
		// therefore tried FIRST, and `pi mcp list` remains the fallback
		// for the install where pi's builtin mcp extension owns MCP.
		if servers, ok, err := pirpc.McpAdapterServers(); ok {
			if err != nil {
				return app.McpMsg{Err: err}
			}
			return mcpListMsg(servers, false, "")
		}
		out, err := pirpc.RunMcp(bin, pirpc.McpListTimeout, "list", "--json")
		// pi exits 1 while ANY server is wrong, but still prints the
		// whole document: a failed server is a row, not an error. So the
		// payload decides, and the exit status only matters when there
		// is no payload to read.
		list, perr := pirpc.ParseMcpList([]byte(out))
		if perr != nil {
			if err != nil {
				return app.McpMsg{Err: fmt.Errorf("pi mcp list: %v", err)}
			}
			return app.McpMsg{Err: perr}
		}
		return mcpListMsg(list.Servers, true, mcpNotice(list.Errors, err))
	}
}

// mcpListMsg builds the message from an already-collected server list.
// mcpListMsg is the ONE place the four parallel row slices are derived,
// so both data sources cannot produce rows that disagree.
//
// sort is false for the adapter, whose panel lists servers in the order
// the config declares them; pi's builtin list sorts attention-first.
func mcpListMsg(servers []pirpc.McpServerInfo, sort bool, notice string) app.McpMsg {
	// Order ONCE and derive every parallel slice from the ordered
	// copy: the dialog indexes Options, Descs and McpServers by the
	// same number, so sorting one and not the others would point a
	// row at the wrong server.
	ordered := servers
	if sort {
		ordered = mcpOrdered(servers)
	}
	return app.McpMsg{
		Options: mcpRowLabels(ordered),
		Descs:   mcpRowDescs(ordered),
		Payload: mcpRowDetails(ordered),
		Servers: ordered,
		Notice:  notice,
	}
}

// mcpNotice is pi's one-line summary of what went wrong outside the
// rows: its config errors, or the failure of the list call itself
// (a dead `pi` binary, a timeout). A non-empty exit with a readable
// document is not an error — that is just "a server is broken".
func mcpNotice(errs []string, exitErr error) string {
	if len(errs) > 0 {
		return "config errors: " + strings.Join(errs, "; ")
	}
	if exitErr != nil {
		return "pi mcp list exited with an error: " + exitErr.Error()
	}
	return ""
}

// mcpOrdered sorts the servers the way pi's serversMenu does:
// attentionRank first, then name. The rank is pi's attentionRank
// (dist/extensions/mcp/index.js): needs sign-in, then failed, then
// disconnected, then anything else, then connected, and a disabled
// server last of all — pi keeps disabled servers listed so they can be
// enabled again, but they never need attention.
func mcpOrdered(servers []pirpc.McpServerInfo) []pirpc.McpServerInfo {
	out := make([]pirpc.McpServerInfo, len(servers))
	copy(out, servers)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := mcpAttentionRank(out[i]), mcpAttentionRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// mcpAttentionRank mirrors pi's attentionRank. `starting` (no state
// yet) sits at pi's default rank 3, between disconnected and connected.
func mcpAttentionRank(s pirpc.McpServerInfo) int {
	if !s.Enabled {
		return 5
	}
	switch s.State {
	case "needs-auth":
		return 0
	case "failed":
		return 1
	case "disconnected":
		return 2
	case "connected":
		return 4
	default:
		return 3
	}
}

// mcpRowLabels is the row's left half: the server name, exactly as pi
// shows it.
func mcpRowLabels(servers []pirpc.McpServerInfo) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Name)
	}
	return out
}

// mcpRowDescs is pi's row description:
// `${describeState} · ${exposure} · ${scope ?? source}`.
func mcpRowDescs(servers []pirpc.McpServerInfo) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		scope := s.Scope
		if scope == "" {
			scope = s.Source // an extension-registered server has no scope
		}
		out = append(out, app.McpStateLine(s)+" · "+mcpExposureOf(s)+" · "+scope)
	}
	return out
}

// mcpRowDetails is the payload: the row's own detail block, so a copy
// of the selected row carries the transport, the mcp.json path and the
// full connection error.
func mcpRowDetails(servers []pirpc.McpServerInfo) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Name+"\n"+app.McpMenuDetails(s))
	}
	return out
}

// mcpExposureOf is pi's exposureOf(): the key, or codemode when absent.
func mcpExposureOf(s pirpc.McpServerInfo) string {
	if s.Exposure == "" {
		return "codemode"
	}
	return s.Exposure
}

// mcpMenuRows builds the per-server action menu, in pi's order and
// with pi's gating. A disabled server offers only Enable; an enabled
// one offers Sign in (only when it needs one), Tools (only when
// connected), Reconnect (for every state but a starting one),
// Sign out (only for a connected OAuth-capable HTTP server), Exposure
// and Disable. `hidden` is not offered by pi either.
func mcpMenuRows(srv pirpc.McpServerInfo) ([]string, []string) {
	var opts, descs []string
	add := func(label, desc string) {
		opts = append(opts, label)
		descs = append(descs, desc)
	}
	// Pi's `saved to the <scope> mcp.json` / `saved to mcp.json` note on
	// the two rows that write to the file.
	saved := "saved to mcp.json"
	switch srv.Scope {
	case "extension":
		saved = "for this session" // not persisted: an extension registered it
	case "global", "project":
		saved = "saved to the " + srv.Scope + " mcp.json"
	}
	if !srv.Enabled {
		add(McpActEnable, saved)
		return opts, descs
	}
	// Sign in is offered for an enabled server stuck at needs-auth —
	// the same predicate the root list's ^A key is gated on.
	if app.McpCanSignIn(srv) {
		add(McpActSignIn, "opens the browser")
	}
	if srv.State == "connected" {
		add(McpActTools, fmt.Sprintf("%d offered", len(srv.Tools)))
	}
	// The gating itself is app's (McpCanSignIn / McpCanReconnect), so the
	// menu rows and the one-key arms of the root list can never disagree
	// about what a server offers.
	if app.McpCanReconnect(srv) {
		add(McpActReconnect, "")
	}
	// Sign out deletes stored OAuth credentials, which only exist for an
	// HTTP server pi reached with OAuth (never with an Authorization
	// header of its own). `pi mcp list --json` carries no OAuth flag, so
	// the URL transport is the signal: a stdio server can never have
	// credentials to delete, and offering the row there would produce a
	// guaranteed-empty sign-out.
	if srv.State == "connected" && srv.IsHTTP() {
		add(McpActSignOut, "deletes the stored credentials")
	}
	add(McpActExposure, mcpExposureOf(srv))
	add(McpActDisable, saved)
	return opts, descs
}

// init wires the /mcp root list's own row keys. app owns the key
// routing and the gating (it prints the hint block, so it has to know
// which keys are live); the commands live here, with the rest of the
// pi-mcp parity layer.
func init() {
	app.RegisterMcpKeys(map[string]app.McpKeyFunc{
		app.McpKeySpace:  mcpRowKeyCmd,
		app.McpKeyToggle: mcpRowKeyCmd,
		app.McpKeySignIn: mcpRowKeyCmd,
		app.McpKeyReconn: mcpRowKeyCmd,
	})
}

// mcpRowKeyCmd is the one handler behind all four row keys, so space and
// ^D can never drift into two different writers: both go through
// mcpSaveCmd, which is also what the menu's Enable/Disable rows use.
// It assumes app has already gated ^A and ^R on the server's state
// (McpCanSignIn / McpCanReconnect), so reaching here means the key is
// legal for the selected row.
func mcpRowKeyCmd(m *app.Model, d *app.Dialog, ri int, key string) tea.Cmd {
	srv := d.SelMcpRow(ri)
	switch key {
	case app.McpKeySignIn:
		m.Status = "signing in to " + srv.Name + " — approve access in your browser…"
		m.Refresh()
		return m.McpLoginCmd(srv.Name)
	case app.McpKeyReconn:
		m.Status = "reconnecting " + srv.Name + "…"
		m.Refresh()
		// There is no `pi mcp reconnect`; listing is what connects.
		return m.RunBuiltin(app.BuiltinMcpList, "")
	default: // space / ^D: the enable toggle, both ways
		on := !srv.Enabled
		notice := "disabled " + srv.Name + " — its tools are no longer registered"
		if on {
			notice = "enabled " + srv.Name + " — connecting…"
		}
		return mcpSaveCmd(m, srv, mcpTogglePatch(srv, on), notice)
	}
}

// mcpTogglePatch is the enable/disable patch for one server, spelled in
// the dialect of the file that owns it: pi's mcp.json marks a server off
// with `enabled: false`, the pi-mcp-adapter file with `disabled: true`.
//
// Writing pi's spelling into the adapter's file is the bug this exists
// to prevent: the adapter never reads `enabled`, so the write succeeds,
// the refresh re-reads the unchanged `disabled`, and the row's dot does
// not move. The key test is Scope, because that is what the loader set
// when it read the adapter's file.
func mcpTogglePatch(srv pirpc.McpServerInfo, enabled bool) pirpc.McpConfigPatch {
	if srv.Scope == pirpc.AdapterScope {
		disabled := !enabled
		return pirpc.McpConfigPatch{Disabled: &disabled}
	}
	on := enabled
	return pirpc.McpConfigPatch{Enabled: &on}
}

// confirmMcp is Enter on a server row: it opens the action menu in
// FRONT of the list (Dialogs[0] is the active dialog everywhere, and
// Esc must then fall through to the list with the same row selected).
func confirmMcp(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	// The trailing row is not a server: it opens the add form, pushed in
	// front of the list like every other /mcp screen, so Esc comes back
	// here with the list (and this row) exactly as it was.
	if d.Options[ri] == app.McpAddRow {
		m.OpenMcpMenu(McpAddForm())
		return m, nil
	}
	srv := d.SelMcpRow(ri)
	if srv.Name == "" {
		return m, nil
	}
	menu := m.McpMenu(srv)
	menu.Options, menu.Descs = mcpMenuRows(srv)
	m.OpenMcpMenu(menu)
	return m, nil
}

// McpAddForm is the free-text add form the list's trailing row opens.
// The Message carries BOTH accepted shapes plus the -l flag, because
// the buffer is one line and the forms are not guessable from each
// other; the Placeholder is one concrete example, so the user sees the
// shape before typing anything.
//
// Exported so a test (and any future caller) can build the form without
// going through the list.
func McpAddForm() *app.Dialog {
	return &app.Dialog{Kind: app.McpAddKind, Title: "Add MCP server",
		Message: "<name> --url <url>            a streamable HTTP server\n" +
			"<name> -- <command> [args…]  a stdio server\n" +
			"add -l (or --local) to write the project's .pi/mcp.json instead of the global one",
		Placeholder: "docs --url https://mcp.example.com/mcp"}
}

// confirmMcpAdd submits the typed line to `pi mcp add`. It reuses
// mcpWrite — the same parse, the same pi name rule and the same exec
// path as the `/mcp add …` slash command — so the form and the command
// can never disagree about what a valid line is. app popped the form
// before dispatching, so a refusal lands as a notice over the list.
func confirmMcpAdd(m *app.Model, d *app.Dialog, _ int) (tea.Model, tea.Cmd) {
	line := strings.TrimSpace(d.Filter)
	if line == "" {
		return m, nil
	}
	return m, mcpWrite(m, "add", "add "+line)
}

// confirmMcpAction runs one row of the per-server menu. Every arm
// leaves the menu in place or pops it exactly as pi does: pi's manager
// loops on the server menu after an action, and returns to the list
// only when the user backs out of it.
func confirmMcpAction(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	srv := d.SelMcpServer()
	name := srv.Name
	switch d.Options[ri] {
	case McpActSignIn:
		m.PopMcpMenu()
		m.Status = "signing in to " + name + " — approve access in your browser…"
		m.Refresh()
		return m, m.McpLoginCmd(name)
	case McpActSignOut:
		m.PopMcpMenu()
		m.Status = "signing out of " + name + "…"
		m.Refresh()
		return m, m.McpLogoutCmd(name)
	case McpActReconnect:
		m.PopMcpMenu()
		m.Status = "reconnecting " + name + "…"
		m.Refresh()
		// There is no `pi mcp reconnect`; listing is what connects.
		return m, m.RunBuiltin(app.BuiltinMcpList, "")
	case McpActTools:
		m.OpenMcpMenu(mcpToolsMenu(srv))
		return m, nil
	case McpActExposure:
		m.OpenMcpMenu(mcpExposureMenu(srv))
		return m, nil
	case McpActEnable:
		return m, mcpSaveCmd(m, srv, mcpTogglePatch(srv, true),
			"enabled "+name+" — connecting…")
	case McpActDisable:
		return m, mcpSaveCmd(m, srv, mcpTogglePatch(srv, false),
			"disabled "+name+" — its tools are no longer registered")
	}
	return m, nil
}

// mcpSaveCmd writes the patch into the server's mcp.json off the event
// loop (it is a file write into a file pi also owns) and reports back,
// which makes app re-read the list.
func mcpSaveCmd(m *app.Model, srv pirpc.McpServerInfo, patch pirpc.McpConfigPatch, notice string) tea.Cmd {
	m.PopMcpMenu()
	m.Status = "saving…"
	m.Refresh()
	return func() tea.Msg {
		if err := pirpc.SetMcpServerConfig(srv.Source, srv.Name, patch); err != nil {
			return app.McpConfigMsg{Server: srv.Name, Err: err}
		}
		return app.McpConfigMsg{Server: srv.Name, Notice: notice + " — saved to " + srv.Source}
	}
}

// mcpToolsMenu is pi's Tools view: one row per tool the server offers,
// with the server's exposure in the header. Pi cannot read a single
// tool's own `toolExposure` from the CLI (the list carries names only),
// so the per-tool override marker is not reproduced.
func mcpToolsMenu(srv pirpc.McpServerInfo) *app.Dialog {
	exposure := mcpExposureOf(srv)
	menu := &app.Dialog{Kind: "mcpTools", Title: "Tools of " + srv.Name,
		Message:   "Exposure " + exposure + ": " + mcpExposureDescs[exposure],
		McpServer: srv,
		Options:   append([]string{}, srv.Tools...)}
	for i := range menu.Options {
		menu.Descs = append(menu.Descs, "mcp__"+srv.Name+"__"+menu.Options[i])
	}
	if len(menu.Options) == 0 {
		menu.Options = []string{"The server offers no tools."}
		menu.Descs = []string{""}
		menu.Cursor = 0
	}
	return menu
}

// mcpExposureMenu is pi's Exposure picker: the four values with their
// descriptions and a ✓ on the current one. Pi rebuilds it after every
// save, so the ✓ follows immediately.
func mcpExposureMenu(srv pirpc.McpServerInfo) *app.Dialog {
	cur := mcpExposureOf(srv)
	menu := &app.Dialog{Kind: "mcpExposure", Title: "Exposure of " + srv.Name,
		Message:   "Saved to " + srv.Source,
		McpServer: srv}
	desc := "registered but not callable by the model"
	if d, ok := mcpExposureDescs[cur]; ok {
		desc = d
	}
	menu.Message += " · now " + cur + ": " + desc
	for _, v := range mcpExposureOrder {
		label := "  " + v
		rowDesc := mcpExposureDescs[v]
		if v == cur {
			label = "✓ " + v
			// Spelled out because Enter here cycles rather than saves.
			rowDesc = "current · enter cycles to " + nextMcpExposure(v)
			menu.Current = v
		}
		menu.Options = append(menu.Options, label)
		menu.Descs = append(menu.Descs, rowDesc)
	}
	return menu
}

// confirmMcpExposure saves the picked exposure into the mcp.json that
// defines the server. The key is REMOVED rather than written when the
// value is the default `codemode`, which is what pi's own
// updateMcpServerConfig does. Enter on the already-current value (the
// ✓ row) cycles to the next one instead of doing nothing, so the whole
// set is reachable in one keystroke from any state.
func confirmMcpExposure(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	choice := strings.TrimSpace(strings.TrimPrefix(d.Options[ri], "✓"))
	srv := d.SelMcpServer()
	if choice == mcpExposureOf(srv) {
		choice = nextMcpExposure(choice)
	}
	if !mcpExposureValid(choice) {
		m.PopMcpMenu()
		m.AddBlock(app.Block{Kind: "notice", Text: "unknown exposure: " + choice, Err: true})
		m.Refresh()
		return m, nil
	}
	return m, mcpSaveCmd(m, srv, pirpc.McpConfigPatch{Exposure: &choice},
		srv.Name+" exposure → "+choice)
}

// confirmMcpBack is the Esc-equivalent of the menu stack, and the
// Enter of the Tools view (pi labels that view's confirm action "back"
// too): it pops the menu and leaves the list underneath with the same
// row selected. No separate Back row is offered — pi's menu has no
// such row, it has a cancel label.
func confirmMcpBack(m *app.Model, _ *app.Dialog, _ int) (tea.Model, tea.Cmd) {
	m.PopMcpMenu()
	return m, nil
}
