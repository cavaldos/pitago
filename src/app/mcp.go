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

// McpActionKind names the login/logout verbs for the status line and
// the past-tense notices, matching pi's own wording.
func (a McpActionMsg) Done() string {
	if a.Action == "logout" {
		return "signed out of " + a.Server
	}
	return "signed in to " + a.Server
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

// openMcpListMsg reports a failed read as a notice (the /mcp list is
// only opened when there is something to show, like /tree's "no tree
// entries yet") and otherwise writes the rows into the list.
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
	if len(msg.Options) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no MCP servers configured — add them to " +
			piAgentDir() + "/mcp.json or .pi/mcp.json"})
		m.Refresh()
		return
	}
	m.SetMcpRows(msg)
	m.Refresh()
}

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
	d := &Dialog{Kind: "mcp", Title: "MCP servers",
		Message:    "MCP servers · ↑↓ move · Enter manage · type filters",
		Options:    msg.Options,
		Descs:      msg.Descs,
		Payload:    msg.Payload,
		McpServers: msg.Servers}
	d.Reindex()
	if keep != "" {
		for fi, ri := range d.FIdx {
			if msg.Servers[ri].Name == keep {
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
		if k != "mcpAction" && k != "mcpExposure" {
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
