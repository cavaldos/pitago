package builtin

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// loadTree fetches Pi's session tree and turns it into the native flat,
// searchable Session Tree screen. Rows are ordered by Pi's DFS traversal;
// the active leaf path is marked with •. Enter no longer prints the entry —
// it opens the "Tree action" menu (pi's showTreeSelector answers a row with
// a small follow-up selector too), so the rows carry the session entry id,
// its role and its transcript jump ordinal for that menu to dispatch on.
func loadTree(m *app.Model, arg string) tea.Cmd {
	return func() tea.Msg {
		nodes, leaf, err := m.Pi.GetTree()
		if err != nil {
			return app.TreeMsg{Err: err}
		}
		mode := pirpc.PiString(pirpc.ReadPiSettings(), "treeFilterMode", "default")
		search := ""
		if a := strings.TrimSpace(arg); a != "" {
			switch strings.ToLower(a) {
			case "default", "no-tools", "user-only", "labeled-only", "all":
				mode = strings.ToLower(a)
			default:
				search = a
			}
		}
		rows := buildTreeRows(nodes, leaf, mode)
		return app.TreeMsg{Mode: mode, Options: rows.opts, Descs: rows.descs, Payload: rows.payload,
			TreeIDs: rows.ids, TreeJump: rows.jump, TreeRole: rows.role,
			Filter: search, Current: rows.current}
	}
}

// treeRows is one flattened /tree screen: the display triples plus the
// per-row metadata the "Tree action" menu needs. Everything is index-parallel
// so a row index is a single handle for the menu's four arms.
type treeRows struct {
	opts, descs, payload []string
	ids                  []string // session entry id (the fork target)
	jump                 []int    // transcript user/assistant block ordinal, -1 = none
	role                 []string // "user" | "assistant" | ""
	current              int      // index of the active leaf row, -1 = none
}

// buildTreeRows flattens the visible tree DFS in Pi order. Native Pi's
// session-tree screen is a flat searchable list, so rows intentionally omit
// the old nested connector prefixes; the active path is marked with •.
func buildTreeRows(nodes []pirpc.TreeNode, leaf, filter string) treeRows {
	var out treeRows
	out.current = -1
	if len(nodes) == 0 {
		return out
	}
	tcm := buildToolCallMap(nodes)
	active := activePathIDs(nodes, leaf)
	ordinals := treeJumpOrdinals(nodes, leaf)
	count := 0
	var walk func([]pirpc.TreeNode)
	walk = func(ns []pirpc.TreeNode) {
		visible := make([]pirpc.TreeNode, 0, len(ns))
		for _, n := range ns {
			if treeSubtreeVisible(n, filter) {
				visible = append(visible, n)
			}
		}
		for _, n := range visible {
			if count >= 100 {
				return
			}
			if n.Entry.Type != "usage" && treePassesFilter(n, filter) {
				count++
				row := treeRow(n, tcm, active)
				out.opts = append(out.opts, row)
				out.descs = append(out.descs, treeDesc(n.Entry))
				out.payload = append(out.payload, treeDetail(n, row, tcm))
				out.ids = append(out.ids, n.Entry.ID)
				out.jump = append(out.jump, treeJumpOf(n, ordinals))
				out.role = append(out.role, treeRole(n.Entry))
				if n.Entry.ID == leaf {
					out.current = len(out.opts) - 1
				}
			}
			// Usage and filtered entries are structural only. Keep their
			// descendants in the flat list without adding a parent row.
			walk(n.Children)
		}
	}
	walk(nodes)
	if count >= 100 {
		out.opts = append(out.opts, "…(truncated)")
		out.descs = append(out.descs, "system · limit")
		out.payload = append(out.payload, "The tree contains more than 100 entries; use /tree <mode> to narrow it.")
		out.ids = append(out.ids, "")
		out.jump = append(out.jump, -1)
		out.role = append(out.role, "")
	}
	return out
}

// treeJumpOf resolves a row's transcript ordinal: only entries that actually
// produce a user/assistant transcript block carry one, and only on the active
// branch (an abandoned row's message is not in the transcript at all).
func treeJumpOf(n pirpc.TreeNode, ordinals map[string]int) int {
	if o, ok := ordinals[n.Entry.ID]; ok {
		return o
	}
	return -1
}

// treeRole is the message role the fork arm gates on. Only user and
// assistant rows carry one: a toolResult is a message entry too, but pi's
// fork rejects it just as firmly as a compaction.
func treeRole(e pirpc.TreeEntry) string {
	if e.Type != "message" {
		return ""
	}
	if r := e.Message.Role; r == "user" || r == "assistant" {
		return r
	}
	return ""
}

// treeJumpOrdinals numbers the active branch's transcript blocks 0,1,2…
// root → leaf, the sequence JumpToEntry scrolls through. Only entries that
// really become a transcript block are counted: an assistant turn that only
// issued toolCalls (or was aborted) leaves no assistant block behind, so
// numbering it would shift every later ordinal.
func treeJumpOrdinals(nodes []pirpc.TreeNode, leaf string) map[string]int {
	byID := map[string]pirpc.TreeEntry{}
	var index func([]pirpc.TreeNode)
	index = func(ns []pirpc.TreeNode) {
		for _, n := range ns {
			byID[n.Entry.ID] = n.Entry
			index(n.Children)
		}
	}
	index(nodes)
	out := map[string]int{}
	next := 0
	for _, id := range activePathOrder(nodes, leaf) {
		e, ok := byID[id]
		if !ok {
			continue
		}
		// An entry can produce more than one block (an assistant message
		// with two text blocks becomes two), so the ordinal advances by
		// that count and the row keeps the first one.
		if n := treeBlockCount(e); n > 0 {
			out[id] = next
			next += n
		}
	}
	return out
}

// treeBlockCount mirrors the transcript's own block rule in
// app.JumpToEntry, so both sides count the SAME rows: restore() makes one
// block per non-empty text block (one user block per user message, images
// included), and a message with nothing to show leaves none behind. Any
// drift shifts every later ordinal and the jump lands on the wrong message.
func treeBlockCount(e pirpc.TreeEntry) int {
	if e.Type != "message" {
		return 0
	}
	switch e.Message.Role {
	case "user":
		// withImages still renders an image-only message as a block.
		if strings.TrimSpace(pirpc.TextOf(e.Message.Content)) != "" ||
			pirpc.ImageCount(e.Message.Content) > 0 {
			return 1
		}
	case "assistant":
		blocks := pirpc.BlocksOf(e.Message.Content)
		n := 0
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				n++
			}
		}
		// Plain-string content (BlocksOf yields nothing) is still a block
		// on the transcript side — restore() falls back to TextOf the same
		// way.
		if n == 0 && len(blocks) == 0 && strings.TrimSpace(pirpc.TextOf(e.Message.Content)) != "" {
			return 1
		}
		return n
	}
	return 0
}

func treeSubtreeVisible(n pirpc.TreeNode, filter string) bool {
	if n.Entry.Type != "usage" && treePassesFilter(n, filter) {
		return true
	}
	for _, child := range n.Children {
		if treeSubtreeVisible(child, filter) {
			return true
		}
	}
	return false
}

func treeDesc(e pirpc.TreeEntry) string {
	kind := e.Type
	if e.Type == "message" {
		kind = e.Message.Role
		if kind == "" {
			kind = "message"
		}
	}
	return kind + " · " + trajTime(e.Timestamp)
}

func treeDetail(n pirpc.TreeNode, row string, tcm map[string]pirpc.ContentBlock) string {
	e := n.Entry
	meta := e.Type + " · id " + app.ShortID(e.ID) + " · " + trajTime(e.Timestamp)
	if e.ParentID != nil && *e.ParentID != "" {
		meta += " · parent " + app.ShortID(*e.ParentID)
	}
	if n.Label != "" {
		meta += " · [" + n.Label + "]"
	}
	body := treeBody(e, tcm)
	if strings.TrimSpace(body) == "" {
		body = "(no content)"
	}
	return row + "\n" + meta + "\n\n" + body
}

func treeBody(e pirpc.TreeEntry, tcm map[string]pirpc.ContentBlock) string {
	switch e.Type {
	case "message":
		return trajBody(e, tcm)
	case "custom_message":
		return trajCap(pirpc.TextOf(e.Content), 2000)
	case "compaction":
		if strings.TrimSpace(e.Summary) != "" {
			return trajCap(e.Summary, 2000)
		}
		return fmt.Sprintf("tokens before compaction: %d", e.TokensBefore)
	case "branch_summary":
		return trajCap(e.Summary, 2000)
	case "model_change":
		return "provider: " + app.OrDefault(e.Provider, "?") + "\nmodel: " + app.OrDefault(e.ModelID, "?")
	case "thinking_level_change":
		return "thinking: " + app.OrDefault(e.ThinkingLevel, "?")
	case "custom":
		return "custom type: " + app.OrDefault(e.CustomType, "?")
	case "label":
		return "label: " + app.OrDefault(e.Label, "(cleared)")
	case "session_info":
		return "title: " + app.OrDefault(e.Name, "(empty)")
	default:
		return "id: " + app.ShortID(e.ID)
	}
}

// confirmTree does NOT post the entry any more: pi's showTreeSelector
// (interactive-mode.js) answers a picked row with a second, tiny selector
// ("Summarize branch?") and re-opens the tree on Esc. pi then drives
// session.navigateTree — which its RPC mode (rpc-mode.js) does not expose —
// so pitago keeps the two-step shape and replaces the follow-up with a local
// action menu: jump, copy, fork, back. Synchronous: the menu is built here.
func confirmTree(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	detail := ""
	if ri < len(d.Payload) {
		detail = strings.TrimSpace(d.Payload[ri])
	}
	if detail == "" {
		detail = d.Options[ri]
	}
	entryID, jump, role := "", -1, ""
	if ri < len(d.Paths) {
		entryID = d.Paths[ri]
	}
	if ri < len(d.TreeJump) {
		jump = d.TreeJump[ri]
	}
	if ri < len(d.TreeRole) {
		role = d.TreeRole[ri]
	}
	act := &app.Dialog{Kind: "treeAction", Title: "Tree action", Message: d.Options[ri],
		Payload: []string{detail}, Paths: []string{entryID}, TreeJump: []int{jump}}
	primary, primaryDesc := TreeActJump, "scroll the chat to this message"
	if jump < 0 {
		primary, primaryDesc = TreeActView, "show the full entry in the chat"
	}
	act.Options = append(act.Options, primary)
	act.Descs = append(act.Descs, primaryDesc)
	act.Options = append(act.Options, TreeActCopy)
	act.Descs = append(act.Descs, "copy this entry's text")
	// pi's runtimeHost.fork defaults to position "before" and throws
	// "Invalid entry ID for forking" for anything but a user message
	// (agent-session-runtime.js), so the row is only offered where it works.
	if role == "user" && entryID != "" {
		act.Options = append(act.Options, TreeActFork)
		act.Descs = append(act.Descs, "branch a new session at this message")
	}
	act.Options = append(act.Options, TreeActBack)
	act.Descs = append(act.Descs, "return to the session tree")
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog everywhere (View, renderDialog,
	// updateDialog and dismissDialog all index 0 and pop from the front),
	// so the menu goes in FRONT of the tree: it is the screen the user
	// interacts with, and Esc then falls through to the tree underneath.
	m.Dialogs = append([]*app.Dialog{act}, m.Dialogs...)
	m.Refresh()
	return m, nil
}
