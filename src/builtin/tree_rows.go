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
// the active leaf path is marked with • and Enter still opens the complete
// entry in the chat.
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
		opts, descs, payload, current := buildTreeRows(nodes, leaf, mode)
		return app.TreeMsg{Mode: mode, Options: opts, Descs: descs, Payload: payload, Filter: search, Current: current}
	}
}

// buildTreeRows flattens the visible tree DFS in Pi order. Native Pi's
// session-tree screen is a flat searchable list, so rows intentionally omit
// the old nested connector prefixes; the active path is marked with •.
func buildTreeRows(nodes []pirpc.TreeNode, leaf, filter string) (opts, descs, payload []string, current int) {
	if len(nodes) == 0 {
		return nil, nil, nil, -1
	}
	tcm := buildToolCallMap(nodes)
	active := activePathIDs(nodes, leaf)
	count := 0
	current = -1
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
				opts = append(opts, row)
				descs = append(descs, treeDesc(n.Entry))
				payload = append(payload, treeDetail(n, row, tcm))
				if n.Entry.ID == leaf {
					current = len(opts) - 1
				}
			}
			// Usage and filtered entries are structural only. Keep their
			// descendants in the flat list without adding a parent row.
			walk(n.Children)
		}
	}
	walk(nodes)
	if count >= 100 {
		opts = append(opts, "…(truncated)")
		descs = append(descs, "system · limit")
		payload = append(payload, "The tree contains more than 100 entries; use /tree <mode> to narrow it.")
	}
	return opts, descs, payload, current
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

// confirmTree posts the selected tree entry into the transcript and closes
// the tab. It deliberately does not attempt branch navigation: Pi's RPC
// exposes get_tree but no navigate_tree operation.
func confirmTree(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Payload) {
		return m, nil
	}
	detail := strings.TrimSpace(d.Payload[ri])
	if detail == "" && ri < len(d.Options) {
		detail = d.Options[ri]
	}
	m.Dialogs = m.Dialogs[1:]
	m.AddBlock(app.Block{Kind: "tree", Text: detail})
	m.Refresh()
	return m, nil
}
