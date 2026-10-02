package app

// The /mcp server manager's state and render half: the messages the
// dialogs answer to, the two dialogs themselves (the server list and
// the per-server action menu) and pi's state wording.
//
// The rows and the `pi mcp` invocations live in src/builtin (the pi
// parity layer, which may import both this package and pirpc); this
// file owns what a dialog IS.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// McpMsg carries a refreshed server list for the /mcp dialog: the
// display triples, the full server record per row (the action menu
// needs transport, source, state and tools), and one optional notice.
type McpMsg struct {
	Options, Descs, Payload []string
	Servers                 []pirpc.McpServerInfo // parallel to Options
	Notice                  string
	Err                     error
}

// McpActionMsg reports the result of one `pi mcp login|logout` run.
// Both change what a server shows (credentials decide needs-auth, a
// sign-out flips it back), so the handler re-reads the list.
type McpActionMsg struct {
	Action, Server, Notice string
	Err                    error
}

// McpActionKind names the verbs for the status line and the
// past-tense notices, matching pi's own wording. add/remove share
// this message type with login/logout because they share one handler:
// all four change which servers the manager shows, so all four re-read
// the list afterwards.
func (a McpActionMsg) Done() string {
	switch a.Action {
	case "logout":
		return "signed out of " + a.Server
	case "add":
		return "added MCP server " + a.Server
	case "remove":
		return "removed MCP server " + a.Server
	default:
		return "signed in to " + a.Server
	}
}

// McpConfigMsg reports a saved exposure / enable / disable change and
// asks for the list to be re-read: enabling a server is what actually
// connects it, and a fresh list is the only way to see that.
type McpConfigMsg struct {
	Server, Notice string
	Err            error
}

// --- the commands ----------------------------------------------------

// McpLoginCmd runs `pi mcp login <server>`. It opens the browser and
// waits for the user to approve, so it is deliberately long-lived
// (pi's own default is a 300s wait) and always runs in the background
// command slot: blocking the event loop would freeze the whole TUI
// for minutes. The status line the caller set is the only progress
// shown — pi shows the authorization URL in a notify, but the browser
// is opened by the CLI itself, so the user does not need it here.
func (m Model) McpLoginCmd(server string) tea.Cmd {
	return m.mcpAuthCmd("login", server, pirpc.McpLoginTimeout)
}

// McpLogoutCmd runs `pi mcp logout <server>`, which deletes the stored
// OAuth credentials in mcp-auth.json. Short: no browser involved.
// McpWriteCmd runs `pi mcp add|remove <name> …`. Pi has no UI for
// either verb, so without this an empty list is a dead end: the window
// can only ever manage servers the user hand-wrote into mcp.json.
//
// argv arrives already validated and split (src/builtin owns the
// parsing and the name check), and RunMcp passes it without a shell.
// The verb never reaches here unvalidated, but the name does get a
// second check because it is untrusted input either way.
func (m Model) McpWriteCmd(verb string, argv []string) tea.Cmd {
	if len(argv) == 0 || !pirpc.ValidMcpServerName(argv[0]) {
		return func() tea.Msg {
			return McpActionMsg{Action: verb, Err: McpNameError(argv[0])}
		}
	}
	server := argv[0]
	bin := m.piBin()
	return func() tea.Msg {
		out, err := pirpc.RunMcp(bin, pirpc.McpActionTimeout, append([]string{verb}, argv...)...)
		msg := McpActionMsg{Action: verb, Server: server}
		if tail := shortOut([]byte(out)); tail != "" {
			msg.Notice = tail
		}
		if err != nil {
			msg.Err = err
			if msg.Notice == "" {
				msg.Notice = verb + " " + server + " failed"
			}
		}
		return msg
	}
}

func (m Model) McpLogoutCmd(server string) tea.Cmd {
	return m.mcpAuthCmd("logout", server, pirpc.McpActionTimeout)
}

func (m Model) mcpAuthCmd(action, server string, timeout time.Duration) tea.Cmd {
	// Refuse an illegal name BEFORE exec. exec passes args without a
	// shell, so nothing could be injected here, but pi's CLI re-parses
	// the name and a name is untrusted input (a typed or pasted
	// `/mcp login <string>`): pi's own rule is letters, digits, _ and -.
	if !pirpc.ValidMcpServerName(server) {
		return func() tea.Msg {
			return McpActionMsg{Action: action, Server: server, Err: McpNameError(server)}
		}
	}
	bin := m.piBin()
	return func() tea.Msg {
		out, err := pirpc.RunMcp(bin, timeout, action, server)
		msg := McpActionMsg{Action: action, Server: server}
		if tail := shortOut([]byte(out)); tail != "" {
			msg.Notice = tail
		}
		if err != nil {
			msg.Err = err
			if msg.Notice == "" {
				msg.Notice = action + " " + server + " failed"
			}
		}
		return msg
	}
}

// McpNameError is the refusal message for a name pi's own CLI would
// not accept (docs/mcp.md: letters, digits, _ and - only).
func McpNameError(name string) error {
	return fmt.Errorf("%q is not an MCP server name — letters, digits, _ and - only", name)
}

// --- the dialogs -----------------------------------------------------

// openMcpListMsg reports a failed read as a notice and otherwise
// writes the rows into the window — including the empty case. pi shows
// its "No MCP servers configured" line INSIDE the panel, so /mcp always
// opens and the user always lands on the place the servers are managed
// from. Dumping the line into the chat instead left an empty list as a
// dead end with no way back in.
func (m *Model) openMcpListMsg(msg McpMsg) {
	m.Status = "ready"
	if msg.Err != nil {
		m.AddBlock(Block{Kind: "notice", Text: "mcp error: " + msg.Err.Error(), Err: true})
		m.Refresh()
		return
	}
	if msg.Notice != "" {
		m.AddBlock(Block{Kind: "notice", Text: msg.Notice})
	}
	m.SetMcpRows(msg)
	m.Refresh()
}

// McpEmptyLine is pi's serversMenu `empty` string: it names the two
// files to edit instead of leaving the user to guess where a server
// comes from.
func McpEmptyLine() string {
	return "No MCP servers configured. Add them to " + piAgentDir() +
		"/mcp.json or .pi/mcp.json — or run /mcp add <name> --url <url>."
}

// McpAddKind is the free-text form the list's trailing row opens. It is
// a window screen like the other /mcp kinds (mcpWindowKind), not a
// generic popup, because the user reaches it from inside the manager
// and must be able to type a server line there.
const McpAddKind = "mcpAdd"

// McpAddRow is the trailing row of every /mcp list, present whether or
// not servers are configured. Pi ships no add-server UI (only the
// `pi mcp add` CLI), so without this row an empty list is a dead end —
// and dialog capture swallows every key while the window is up, so the
// user could not even type `/mcp add …` from it. Exported because
// src/builtin recognizes the row by its label.
const McpAddRow = "＋ Add a server"

// mcpAddDesc is the add row's description: it names BOTH accepted
// forms, because the form is one line of free text and the two shapes
// (a URL and a command) are not guessable from each other.
const mcpAddDesc = "http: <name> --url <url> · stdio: <name> -- <command> [args…]"

// SetMcpRows writes the rows into the open /mcp list, or pushes the
// list when none is open. An action that ran on top of the list
// (exposure, enable, sign-in) therefore refreshes IN PLACE instead of
// stacking a second copy of the same list, and the selection follows
// the server by NAME: pi puts servers needing attention first, so a
// re-read reorders the rows and a cursor kept by index would silently
// select a different server.
func (m *Model) SetMcpRows(msg McpMsg) {
	keep := ""
	if i := m.mcpListIndex(); i >= 0 {
		keep = m.mcpSelectedName(m.Dialogs[i])
	}
	// One trailing row in all three slices: the add form. Options,
	// Descs and McpServers are indexed by the same number, so each is
	// padded to the widest of the three first — a short slice would
	// otherwise make a row index another row's state.
	n := max(len(msg.Options), len(msg.Descs), len(msg.Servers))
	opts := mcpPadRows(msg.Options, n)
	descs := mcpPadRows(msg.Descs, n)
	servers := mcpPadRows(msg.Servers, n)
	opts = append(opts, McpAddRow)
	descs = append(descs, mcpAddDesc)
	// The add row names no server, so its record is the zero value —
	// which is exactly what SelMcpRow reports for it.
	servers = append(servers, pirpc.McpServerInfo{})
	d := &Dialog{Kind: "mcp", Title: "MCP servers",
		// pi shows no hint line here: the title is in the window's top
		// border and the footer already names both keys (mcpFooter).
		Options:    opts,
		Descs:      descs,
		Payload:    msg.Payload,
		McpServers: servers}
	// With no servers the window body carries pi's empty line, so the
	// one screen that needs explaining explains itself where the user
	// is looking. A refresh back to a populated list clears it.
	if n == 0 {
		d.Message = McpEmptyLine()
	}
	d.Reindex()
	if keep != "" {
		for fi, ri := range d.FIdx {
			if d.SelMcpRow(ri).Name == keep {
				d.Cursor = fi
				break
			}
		}
	}
	if i := m.mcpListIndex(); i >= 0 {
		// Drop the action menu (and anything above it) so the refreshed
		// list is what the user is left looking at, as in pi: the menus
		// rebuild in place and the server list is always the parent.
		m.Dialogs = m.Dialogs[:i+1]
		m.Dialogs[i] = d
		return
	}
	m.Dialogs = append(m.Dialogs, d)
}

// mcpPadRows copies a row slice and pads it to n (the length of the
// widest of the three parallel slices). The caller appends the add
// row's own entry afterwards, so every slice ends up the same length —
// a row that indexes another row's state is worse than no list at all.
func mcpPadRows[T any](in []T, n int) []T {
	out := make([]T, 0, n+1)
	out = append(out, in...)
	for len(out) < n {
		var zero T
		out = append(out, zero)
	}
	return out
}

// mcpListIndex is the stack position of the /mcp list, or -1.
func (m *Model) mcpListIndex() int {
	for i := len(m.Dialogs) - 1; i >= 0; i-- {
		if m.Dialogs[i].Kind == "mcp" {
			return i
		}
	}
	return -1
}

// mcpSelectedName is the server name under the cursor ("" when none).
func (m *Model) mcpSelectedName(d *Dialog) string {
	if d == nil || len(d.FIdx) == 0 {
		return ""
	}
	ri := d.FIdx[d.Cursor]
	if ri < 0 || ri >= len(d.McpServers) {
		return ""
	}
	return d.McpServers[ri].Name
}

// SelMcpServer is the server an /mcp action or exposure dialog belongs
// to. Exported for src/builtin, whose confirm actions read it.
func (d *Dialog) SelMcpServer() pirpc.McpServerInfo {
	if d == nil {
		return pirpc.McpServerInfo{}
	}
	return d.McpServer
}

// SelMcpRow is the full server record behind list row ri.
func (d *Dialog) SelMcpRow(ri int) pirpc.McpServerInfo {
	if d == nil || ri < 0 || ri >= len(d.McpServers) {
		return pirpc.McpServerInfo{}
	}
	return d.McpServers[ri]
}

// PopMcpMenu drops the per-server menu (and any sub-menu on top of it)
// but keeps the list underneath, so Esc from the menu is the same
// journey as pi's cancelLabel "back".
func (m *Model) PopMcpMenu() {
	for len(m.Dialogs) > 0 {
		k := m.Dialogs[0].Kind
		if k == "mcp" {
			break
		}
		if k != "mcpAction" && k != "mcpExposure" && k != McpAddKind {
			break
		}
		m.Dialogs = m.Dialogs[1:]
	}
	m.Refresh()
}

// OpenMcpMenu pushes a ready-made menu in front of the list. The rows
// are built in src/builtin (pi parity); app only owns the stack.
func (m *Model) OpenMcpMenu(menu *Dialog) {
	menu.Reindex()
	m.Dialogs = append([]*Dialog{menu}, m.Dialogs...)
	m.Refresh()
}

// McpMenu builds the per-server action menu for srv. Kept here so the
// rows and the header the user reads come from one place.
func (m Model) McpMenu(srv pirpc.McpServerInfo) *Dialog {
	return &Dialog{Kind: "mcpAction", Title: "MCP server " + srv.Name,
		Message: McpMenuDetails(srv), McpServer: srv}
}

// McpMenuDetails is pi's details block for a server: the transport
// (the command line for stdio, the URL for HTTP), which mcp.json
// defines it, and its state. The connection error rides along, which
// is where the tail of a stdio server's stderr shows up.
func McpMenuDetails(srv pirpc.McpServerInfo) string {
	scope := srv.Scope
	if scope == "" {
		scope = "config"
	}
	lines := []string{
		srv.Transport,
		scope + ": " + srv.Source,
		"State: " + McpStateLine(srv),
	}
	if srv.Error != "" && srv.State != "connected" {
		lines = append(lines, srv.Error)
	}
	return strings.Join(lines, "\n")
}

// McpStateLine is pi's describeState() (dist/extensions/mcp/index.js):
// a disabled server says so first, needs-auth reads as "needs
// sign-in", a failure carries the first line of its error, and a live
// server counts its tools and resources. The row description and the
// menu header both use it, so they can never disagree.
func McpStateLine(srv pirpc.McpServerInfo) string {
	if !srv.Enabled {
		return "disabled"
	}
	switch srv.State {
	case "needs-auth":
		return "needs sign-in"
	case "failed":
		if line := McpFirstLine(srv.Error); line != "" {
			return "failed: " + line
		}
		return "failed"
	case "connected":
		out := "connected · " + mcpPlural(len(srv.Tools), "tool")
		if srv.Resources > 0 {
			out += " · " + mcpPlural(srv.Resources, "resource")
		}
		return out
	case "connecting":
		return "connecting…"
	case "":
		return "starting"
	default:
		return srv.State
	}
}

// mcpPlural is pi's `1 tool` / `2 tools` spelling.
func mcpPlural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return mcpItoa(n) + " " + word + "s"
}

// mcpItoa formats a small non-negative count without importing strconv.
func mcpItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// McpFirstLine is pi's firstLine() helper: a one-line summary of a
// (possibly multi-line) connection error.
func McpFirstLine(s string) string {
	return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
}

// --- the window -----------------------------------------------------

// McpKeyFunc runs one of the /mcp root list's own keys (space, ^A,
// ^D, ^R) against the SELECTED server row and returns the command to
// run. The commands live in src/builtin (they write mcp.json and run
// `pi mcp login`, which this package may not import), so the wiring is
// a package-level registration from builtin's init rather than another
// argument to UseBuiltins.
type McpKeyFunc func(m *Model, d *Dialog, ri int, key string) tea.Cmd

// The keys the root list registers, and the names the hint block
// prints. Exported so a test can assert the footer and the handler
// table cannot drift apart.
const (
	McpKeySpace  = "space"
	McpKeySignIn = "ctrl+a"
	McpKeyReconn = "ctrl+r"
	McpKeyToggle = "ctrl+d"
)

// mcpKeyFns is the registered handler table (see McpKeyFunc). Empty
// until src/builtin's init registers one, and every key is then a safe
// no-op rather than a crash.
var mcpKeyFns = map[string]McpKeyFunc{}

// RegisterMcpKeys installs the /mcp root list's key handlers. Called
// once from src/builtin's init; a test may swap the table and restore
// it.
func RegisterMcpKeys(fns map[string]McpKeyFunc) { mcpKeyFns = fns }

// McpCanSignIn reports whether ^A has anything to do for this server:
// pi offers Sign in only for a server that is enabled and stuck at
// needs-auth (mcpMenuRows). Advertising it anywhere else would be a
// key that silently does nothing.
func McpCanSignIn(srv pirpc.McpServerInfo) bool { return srv.Enabled && srv.NeedsSignIn() }

// McpCanReconnect reports whether ^R has anything to do: listing is what
// connects, so a reconnect is worth offering for every state pi
// reconnects from but a starting one (mcpMenuRows).
func McpCanReconnect(srv pirpc.McpServerInfo) bool {
	if !srv.Enabled {
		return false
	}
	switch srv.State {
	case "failed", "disconnected", "connected", "needs-auth":
		return true
	}
	return false
}

// updateMcpList claims the /mcp root list's own keys and lets every
// other key through to the generic list behaviour (↑↓, Enter, typing,
// Backspace). handled=false means "not mine, carry on".
//
// `?` is a toggle rather than filter text here: a server name never
// contains one, and the alternative — searching the row descriptions,
// which carry the state — is exactly what the key turns on.
func (m Model) updateMcpList(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd, bool) {
	switch km.Type {
	case tea.KeySpace:
		return m.mcpRowKey(d, McpKeySpace)
	case tea.KeyCtrlA:
		return m.mcpRowKey(d, McpKeySignIn)
	case tea.KeyCtrlD:
		return m.mcpRowKey(d, McpKeyToggle)
	case tea.KeyCtrlR:
		return m.mcpRowKey(d, McpKeyReconn)
	}
	if km.Type == tea.KeyRunes && km.String() == "?" {
		d.SearchDesc = !d.SearchDesc
		d.Reindex()
		return m, nil, true
	}
	return m, nil, false
}

// mcpRowKey runs key against the selected row. Three cases are a safe
// no-op: no visible row, the trailing ＋ Add a server row (which names
// no server), and a key the selected server's state does not allow.
func (m Model) mcpRowKey(d *Dialog, key string) (tea.Model, tea.Cmd, bool) {
	ri := -1
	if d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		ri = d.FIdx[d.Cursor]
	}
	srv := d.SelMcpRow(ri)
	if srv.Name == "" {
		return m, nil, true // the add row, or a short/empty list
	}
	if key == McpKeySignIn && !McpCanSignIn(srv) {
		return m, nil, true
	}
	if key == McpKeyReconn && !McpCanReconnect(srv) {
		return m, nil, true
	}
	fn := mcpKeyFns[key]
	if fn == nil {
		return m, nil, true // no builtin registered: no-op, never a crash
	}
	return m, fn(&m, d, ri, key), true
}

// mcpWindowKind reports whether kind is one of the four screens pi's
// manager owns. All four draw as one window in the editor slot
// (renderInput returns it), never as the generic centered popup: pi
// replaces the editor container with the panel, so the transcript
// stays visible above it and the input box is simply gone.
func mcpWindowKind(kind string) bool {
	switch kind {
	case "mcp", "mcpAction", "mcpTools", "mcpExposure", McpAddKind:
		return true
	}
	return false
}

// mcpTop is the /mcp screen on top of the dialog stack, or nil when
// the user is anywhere else.
func (m Model) mcpTop() *Dialog {
	if len(m.Dialogs) == 0 || !mcpWindowKind(m.Dialogs[0].Kind) {
		return nil
	}
	return m.Dialogs[0]
}

// mcpVisibleRows is pi's MAX_VISIBLE_ITEMS (dist/extensions/mcp/ui.js:
// MAX_VISIBLE_ITEMS = 12): its SelectList shows twelve rows before it
// scrolls, so the window reserves the same budget.
const mcpVisibleRows = 12

// renderMcpOverlay floats the /mcp manager over the transcript: the
// panel is centred on the whole window, exactly the way every other
// dialog here is placed (see renderDialog), so the conversation stays
// painted behind it and the editor is never replaced by it.
func (m Model) renderMcpOverlay(d *Dialog) string {
	return lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center,
		m.renderMcpWindow(d))
}

// renderMcpWindow draws the /mcp manager as a window: the title in the
// top border, the screen's own details block, the rows, and a dim
// footer naming the two keys that matter on THIS screen. inputBox is
// pitago's copy of pi's frame(), so the chrome is already the same
// shape pi draws.
func (m Model) renderMcpWindow(d *Dialog) string {
	innerW := m.mainW() - 4
	if innerW < 20 {
		innerW = 20
	}
	if d.Kind == McpAddKind {
		return m.renderMcpAddWindow(d, innerW)
	}
	if d.Kind == "mcp" {
		return m.renderMcpRootWindow(d, innerW)
	}
	rowW := innerW - 2
	var lines []string

	// pi shows no filter box; pitago's lists are type-to-filter, so the
	// buffer only appears once there is something in it.
	if d.Filter != "" && filterableDialog(d) {
		lines = append(lines, toolStyle.Render("filter: "+d.Filter)+cmdHiStyle.Render("▌"))
	}
	// The details block: transport/source/state on the server menu, the
	// exposure note on Tools and Exposure. Bounded, because it carries a
	// file path and a stack trace that would otherwise wrap the frame.
	msg := mcpBlockLines(d.Message, rowW)
	if len(msg) > 0 {
		for _, ln := range msg {
			lines = append(lines, statusBarStyle.Render(ln))
		}
		lines = append(lines, "")
	}

	// The panel floats over the transcript rather than taking the chat's
	// share, so its ceiling is the SCREEN, not half of it. The adapter
	// shows twelve rows before it scrolls (MAX_VISIBLE_ITEMS), and that
	// is still the cap.
	overhead := 4 + len(msg) + 1 + lipgloss.Height(m.renderHeader())
	win := min(mcpVisibleRows, max(3, m.winH-overhead))
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	if above {
		lines = append(lines, toolStyle.Render(fmt.Sprintf("…(+%d above)", start)))
	}
	labelW := mcpLabelWidth(d, rowW)
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		row := mcpPadLabel(Short(d.Options[ri], labelW), labelW)
		if desc := DescOf(d, ri); desc != "" {
			if room := rowW - labelW - 2; room > 4 {
				row += "  " + toolStyle.Render(Short(desc, room))
			}
		}
		if fi == d.Cursor {
			lines = append(lines, "▸ "+rowHiStyle.Width(rowW).Render(row))
		} else {
			lines = append(lines, "  "+row)
		}
	}
	if below {
		lines = append(lines, toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	// "— no match —" only means something when a filter hid the rows.
	// With nothing configured the details block already says so, and
	// saying it twice reads as a broken list.
	if total == 0 && d.Filter != "" {
		lines = append(lines, toolStyle.Render("— no match —"))
	}
	lines = append(lines, "", toolStyle.Render(mcpFooter(d)))
	return inputBox(d.Title, lines, innerW, cBorder)
}

// renderMcpRootWindow draws the /mcp server LIST: the centred title
// ruled into the top border, the search line (pitago's lists are
// type-to-filter, so the buffer is always on screen), the rows as
// state-dot + name + direct/total tools, a summary line and the dim
// key block. The per-server screens keep the plain inputBox frame.
func (m Model) renderMcpRootWindow(d *Dialog, innerW int) string {
	rowW := innerW - 2
	// A short terminal pays for the load-bearing lines only: the search
	// line, the rules, the rows and the census. The dim hint block and
	// the blank spacers around the rules are the first to go, because a
	// hint block pushed off the bottom of the screen is not a hint.
	compact := m.winH < 28
	hints := mcpHints(d)
	if compact {
		hints = nil
	}
	lines := []string{mcpSearchLine(d)}
	if !compact {
		lines = append(lines, "")
	}
	// The empty-list line lives here, above the rows: pi shows it inside
	// the panel, and it is mostly a file path the user needs to read.
	msg := mcpBlockLines(d.Message, rowW)
	for _, ln := range msg {
		lines = append(lines, statusBarStyle.Render(ln))
	}
	if len(msg) > 0 && !compact {
		lines = append(lines, "")
	}
	lines = append(lines, mcpRule(rowW))

	// The panel floats over the transcript, so it may use the screen's
	// height; the adapter's twelve-row cap is still what bounds it. The
	// fixed chrome around the rows is charged first, so a long server
	// list shrinks the rows rather than the frame.
	chrome := 6 + len(hints) + lipgloss.Height(m.renderHeader()) // borders, search, rules, summary
	if !compact {
		chrome += 2 // the blank spacers
	}
	if len(msg) > 0 {
		chrome += len(msg) + 1
	}
	win := min(mcpVisibleRows, max(1, m.winH-chrome))
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	if above {
		lines = append(lines, toolStyle.Render(fmt.Sprintf("…(+%d above)", start)))
	}
	nameW, countW, tokenW := mcpRootColumns(d, rowW)
	for fi := start; fi < end; fi++ {
		lines = append(lines, mcpRootRow(d, d.FIdx[fi], nameW, countW, tokenW, rowW, fi == d.Cursor))
	}
	if below {
		lines = append(lines, toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	// "— no match —" only means something when a filter hid the rows.
	// With nothing configured the details block already says so, and
	// saying it twice reads as a broken list.
	if total == 0 && d.Filter != "" {
		lines = append(lines, toolStyle.Render("— no match —"))
	}
	lines = append(lines, mcpRule(rowW), mcpSummary(d))
	if !compact {
		lines = append(lines, "")
		for _, h := range hints {
			lines = append(lines, toolStyle.Render(h))
		}
	}
	return inputBoxCentered("MCP Servers", lines, innerW, cBorder)
}

// mcpRule is the in-frame horizontal separator, drawn with the box's
// own junction characters so the panel reads as one closed frame
// rather than as dashes floating inside it — the adapter panel's
// ├─┤ mid-rules (mcp-panel.ts uses "├"…"┤" for exactly these two).
// Same total width as before, so the frame math is unchanged.
func mcpRule(rowW int) string {
	if rowW < 2 {
		rowW = 2
	}
	return sepStyle.Render("├" + strings.Repeat("─", rowW-2) + "┤")
}

// mcpSearchLine is the filter line: what is typed now, and (with ? on)
// that the row descriptions are searched too. The adapter panel's own
// wording is an untyped `search...`, so that is what the empty buffer
// shows; the buffer keeps the generic cursor block, so the user can see
// where the next rune lands.
func mcpSearchLine(d *Dialog) string {
	line := "◎  "
	if d.SearchDesc {
		line += toolStyle.Render("desc: ")
	}
	if d.Filter == "" {
		return line + toolStyle.Render("search...") + cmdHiStyle.Render("▌")
	}
	return line + d.Filter + cmdHiStyle.Render("▌")
}

// mcpSummary is the one-line census under the rules. It has the
// adapter panel's shape — `${n} direct  ~${tokens} tokens` (mcp-panel.ts
// :243-270) — whenever a token estimate is available, and pi's own
// server/tool census otherwise, so a `pi mcp list` install (which
// prints tool names only, never a schema) still gets a line.
func mcpSummary(d *Dialog) string {
	var servers, tools, direct, tokens, auth int
	for _, s := range d.McpServers {
		if s.Name == "" {
			continue // the trailing ＋ Add a server row
		}
		servers++
		tools += len(s.Tools)
		dc, _ := s.ToolCounts()
		direct += dc
		tokens += s.DirectTokens
		if s.NeedsSignIn() {
			auth++
		}
	}
	if tokens > 0 {
		// "direct" is an adjective, not a noun: the adapter prints
		// "100 direct", never "100 directs". Hand-rolled here rather
		// than via mcpPlural, which would pluralize it.
		out := mcpItoa(direct) + " direct  ~" + mcpThousands(tokens) + " tokens"
		if auth > 0 {
			out += "  · " + mcpItoa(auth) + " need sign-in"
		}
		return out
	}
	out := mcpPlural(servers, "server") + " · " + mcpPlural(tools, "tool")
	switch auth {
	case 0:
	case 1:
		out += " · 1 needs sign-in"
	default:
		out += " · " + mcpItoa(auth) + " need sign-in"
	}
	return out
}

// mcpThousands groups an integer with commas, the en-US
// toLocaleString() the adapter panel prints its token counts with
// (`~18,637`, not `~18.637` and not `18637`).
func mcpThousands(n int) string {
	s := mcpItoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// mcpHints is the dim key block under the summary. It advertises ONLY
// keys that are actually wired: the movement/filter/Enter/Esc keys the
// generic list path gives every picker, and the row keys this window
// added — each of them gated on the SELECTED server, so a key that
// would do nothing is not printed.
//
// Ctrl+S is deliberately absent: pitago writes every change to
// mcp.json immediately, so there is nothing to save, and a footer that
// names a key which does not exist is worse than a shorter footer.
func mcpHints(d *Dialog) []string {
	srv := d.SelMcpRow(mcpSelRowIdx(d))
	if srv.Name == "" {
		// The add row (or an empty list): there is no server to act on,
		// so the block names Enter instead of a per-server key.
		return []string{"↑↓ move · enter add · ? descriptions · esc close",
			"ctrl+c close · " + strings.TrimPrefix(McpAddRow, "＋ ")}
	}
	head := "↑↓ move · enter manage · space toggle · ? descriptions · esc close"
	tail := "ctrl+c close · ctrl+d " + mcpToggleWord(srv)
	if McpCanSignIn(srv) {
		tail = "ctrl+a sign in · " + tail
	}
	if McpCanReconnect(srv) {
		tail += " · ctrl+r reconnect"
	}
	return []string{head, tail}
}

// mcpToggleWord names what ^D will do to the selected server.
func mcpToggleWord(srv pirpc.McpServerInfo) string {
	if srv.Enabled {
		return "disable"
	}
	return "enable"
}

// mcpSelRowIdx is the raw row index under the cursor, or -1.
func mcpSelRowIdx(d *Dialog) int {
	if d == nil || d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return -1
	}
	return d.FIdx[d.Cursor]
}

// mcpRootColumns sizes the two columns of a root row. The counts sit
// in ONE fixed, right-aligned column, so they line up down the list
// whatever the name lengths are — the reference list reads that way,
// and a ragged count column is what made the old rows hard to scan.
//
// The name column is as wide as the longest VISIBLE name (so a filter
// that hides the long ones gives the counts more room) but is clamped
// so the counts can never be pushed off the frame. The width of the
// count column is the widest "N/M" actually on screen, so a list of
// two-digit counts does not get a count column padded for six.
func mcpRootColumns(d *Dialog, rowW int) (nameW, countW, tokenW int) {
	countW = 3 // the narrowest possible "0/0"
	nameW = 4
	tokenW = 0
	for _, ri := range d.FIdx {
		if srv := d.SelMcpRow(ri); srv.Name != "" {
			if w := lipgloss.Width(Short(srv.Name, mcpNameWCap)); w > nameW {
				nameW = w
			}
			direct, total := srv.ToolCounts()
			if w := lipgloss.Width(mcpItoa(direct) + "/" + mcpItoa(total)); w > countW {
				countW = w
			}
			if srv.DirectTokens > 0 {
				if w := lipgloss.Width(mcpTokenCell(srv.DirectTokens)); w > tokenW {
					tokenW = w
				}
			}
		}
	}
	// 2 (cursor cell) + 1 (dot) + 1 (space) + nameW + 2 (gap) + countW,
	// plus the token cell and its own gap when any server carries an
	// estimate.
	fixed := 6 + countW
	if tokenW > 0 {
		fixed += tokenW + mcpRowGap
	}
	if max := rowW - fixed; nameW > max {
		nameW = max
	}
	if nameW < 4 {
		nameW = 4
	}
	return nameW, countW, tokenW
}

// mcpTokenCell is the per-server token estimate cell: the adapter panel
// prints `~18,637` (mcp-panel.ts), thousands-grouped. It is dim, like
// the counts beside it, because it is context rather than state.
func mcpTokenCell(n int) string {
	if n <= 0 {
		return ""
	}
	return "~" + mcpThousands(n)
}

// mcpNameWCap bounds how wide a name may make the column: a pathological
// 200-character server name must not eat the whole frame.
const mcpNameWCap = 40

// mcpRowGap is the space between the name and the count column.
const mcpRowGap = 2

// mcpRootRow is one list row: the state dot, the name, the
// direct/total tool count and — when an estimate is available — the
// token cost of the server's direct tools. The row description used to
// follow the count, but it repeated what the two leading columns
// already say (the state IS the dot, the tool total IS the N/M) and it
// was the first thing to truncate on a narrow frame. The same
// information is one Enter away on the server's own screen, and `?`
// still searches those descriptions.
//
// The trailing ＋ Add a server row has no server record, so it draws as
// the plain label with no dot and no counts.
func mcpRootRow(d *Dialog, ri, nameW, countW, tokenW, rowW int, sel bool) string {
	if ri < 0 || ri >= len(d.Options) {
		return ""
	}
	var body string
	srv := d.SelMcpRow(ri)
	if srv.Name == "" {
		body = toolStyle.Render(Short(d.Options[ri], rowW-2))
	} else {
		direct, total := srv.ToolCounts()
		body = mcpStateDot(srv) + " " +
			mcpPadLabel(Short(srv.Name, nameW), nameW) + strings.Repeat(" ", mcpRowGap) +
			mcpPadLeft(mcpItoa(direct)+"/"+mcpItoa(total), countW)
		if tokenW > 0 {
			body += strings.Repeat(" ", mcpRowGap) +
				toolStyle.Render(mcpPadLeft(mcpTokenCell(srv.DirectTokens), tokenW))
		}
	}
	if sel {
		return "▸ " + rowHiStyle.Width(rowW).Render(body)
	}
	return "  " + body
}

// mcpStateDot is the connection dot — now the ONLY at-a-glance state
// signal on a root row, since the row text is gone. Each state gets
// its OWN glyph as well as its own colour, because a colour is
// invisible to anyone reading a piped or copied list (and to anyone
// who cannot separate red from grey): connected and needs-sign-in
// would otherwise be the same dot to everyone but the colour-blind.
func mcpStateDot(srv pirpc.McpServerInfo) string {
	if !srv.Enabled {
		return toolStyle.Render("○") // hollow: switched off, not failing
	}
	switch srv.State {
	case "connected":
		return okStyle.Render("●")
	case "needs-auth":
		return lipgloss.NewStyle().Foreground(cYellow).Render("◐")
	case "failed":
		return errStyle.Render("✕")
	case "connecting":
		return lipgloss.NewStyle().Foreground(cYellow).Render("◌")
	default:
		return toolStyle.Render("·") // starting, disconnected, unknown
	}
}

// mcpPadLeft right-aligns a cell in a fixed column.
func mcpPadLeft(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// renderMcpAddWindow draws the one-line add form: the syntax hint, the
// buffer being typed, and the footer. There is no row list here — the
// filter buffer IS the input, so it is drawn with the same
// placeholder/cursor treatment renderInputBox gives a free-text dialog
// (dim placeholder until the first rune, a block cursor always).
func (m Model) renderMcpAddWindow(d *Dialog, innerW int) string {
	rowW := innerW - 2
	var lines []string
	for _, ln := range mcpBlockLines(d.Message, rowW) {
		lines = append(lines, statusBarStyle.Render(ln))
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if d.Filter == "" && d.Placeholder != "" {
		lines = append(lines, toolStyle.Render(d.Placeholder)+cmdHiStyle.Render("▌"))
	} else {
		lines = append(lines, d.Filter+cmdHiStyle.Render("▌"))
	}
	lines = append(lines, "", toolStyle.Render(mcpFooter(d)))
	return inputBox(d.Title, lines, innerW, cBorder)
}

// mcpFooter is pi's footer, per screen: each menu names its own Enter
// verb and whether Esc goes back or closes (ui.js footer =
// confirmLabel • cancelLabel).
func mcpFooter(d *Dialog) string {
	switch d.Kind {
	case "mcpAction":
		return "↑↓ move · enter select · esc back"
	case McpAddKind:
		return "enter save · esc back"
	case "mcpTools":
		return "↑↓ move · enter back · esc back"
	case "mcpExposure":
		return "↑↓ move · enter save · esc back"
	default:
		return "↑↓ move · enter manage · esc close"
	}
}

// mcpLabelWidth is the name column: as wide as the widest visible
// label, so the descriptions get every remaining column. pi's list has
// the same property, and it is what stops a state line from being cut
// to "12 tools · 2 resourc…" as the generic popup used to do.
func mcpLabelWidth(d *Dialog, rowW int) int {
	w := 0
	for _, ri := range d.FIdx {
		if lw := lipgloss.Width(Short(d.Options[ri], mcpVisibleRows*2)); lw > w {
			w = lw
		}
	}
	if w > rowW-8 {
		w = rowW - 8
	}
	if w < 8 {
		w = 8
	}
	return w
}

// mcpPadLabel pads a label to the name column, counting display width
// so a ✓ or an arrow cannot push the descriptions out of line.
func mcpPadLabel(label string, w int) string {
	if pad := w - lipgloss.Width(label); pad > 0 {
		return label + strings.Repeat(" ", pad)
	}
	return label
}

// mcpBlockLines is the details block, wrapped to rowW and bounded so a
// long path or a multi-line stderr trace cannot wrap the frame open.
// It WRAPS rather than truncates on purpose: the empty-list line is
// mostly a file path, and clipping it to "No MCP servers configur…"
// throws away the one thing the line exists to say.
func mcpBlockLines(msg string, rowW int) []string {
	if msg == "" {
		return nil
	}
	wrap := lipgloss.NewStyle().Width(rowW)
	var out []string
	for _, ln := range strings.Split(msg, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, strings.Split(wrap.Render(ln), "\n")...)
		if len(out) >= 8 {
			return out[:8]
		}
	}
	return out
}
