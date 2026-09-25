package builtin

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	componentformat "pitago/src/components/format"
	"pitago/src/pirpc"
	"pitago/src/pitago"
)

// Trajectory is pitago's harness-style run trace (deepseek-harness-like):
// one numbered step per session-tree entry in run order — user/assistant
// text, thinking, tool calls + results, compaction, model/thinking changes.
// /trajectory opens it as a filterable window; Enter posts the full step
// detail into the chat. Rows reuse treeRow so text matches /tree.

// trajScopeOf — canonical impl lives in pitago (pitago-only feature);
// kept here so existing tests/callers don't move.
func trajScopeOf(arg string) (scope, filter string) { return pitago.ScopeOf(arg) }

// loadTrajectory fetches the session tree and builds the dialog rows.
func loadTrajectory(m *app.Model, arg string) tea.Cmd {
	scope, filter := trajScopeOf(arg)
	return func() tea.Msg {
		nodes, leaf, err := m.Pi.GetTree()
		if err != nil {
			return app.TrajectoryMsg{Err: err}
		}
		opts, descs, payload := buildTrajectory(nodes, leaf, scope)
		return app.TrajectoryMsg{Scope: scope, Options: opts, Descs: descs, Payload: payload, Filter: filter}
	}
}

// buildTrajectory flattens the tree DFS (same order as /tree) into dialog
// rows: Options = "#07 • user: …" name, Descs = "10:02:11 · user" meta,
// Payload = full step detail for the right column + Enter. Only message
// entries render — usage/compaction/model/thinking changes, labels, titles
// and custom entries are harness noise, so they never become steps.
func buildTrajectory(nodes []pirpc.TreeNode, leaf, scope string) (opts, descs, payload []string) {
	tcm := buildToolCallMap(nodes)
	active := activePathIDs(nodes, leaf)
	idx := 0
	var walk func(ns []pirpc.TreeNode)
	walk = func(ns []pirpc.TreeNode) {
		for _, n := range ns {
			if n.Entry.Type != "message" {
				walk(n.Children)
				continue
			}
			if trajPass(n, scope) {
				idx++
				row := treeRow(n, tcm, active)
				if names := trajToolNames(n.Entry); len(names) > 0 && n.Entry.Message.Role == "assistant" {
					row += " → [" + strings.Join(names, ", ") + "]"
				}
				opts = append(opts, fmt.Sprintf("#%02d %s", idx, row))
				descs = append(descs, trajTime(n.Entry.Timestamp)+" · "+trajKind(n.Entry))
				payload = append(payload, trajDetail(idx, n, row, tcm))
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return opts, descs, payload
}

// trajPass filters steps per tab: tools = tool activity, messages = chat
// only, all = every message. Non-messages never reach here (see walk).
func trajPass(n pirpc.TreeNode, scope string) bool {
	e := n.Entry
	switch scope {
	case "tools":
		switch e.Message.Role {
		case "toolResult", "bashExecution":
			return true
		}
		return e.Message.Role == "assistant" && len(trajToolNames(e)) > 0
	case "messages":
		return e.Message.Role == "user" || e.Message.Role == "assistant"
	default:
		return true
	}
}

// trajKind is the short step kind for the desc column.
func trajKind(e pirpc.TreeEntry) string {
	switch e.Message.Role {
	case "toolResult":
		return "tool"
	case "bashExecution":
		return "bash"
	case "":
		return "message"
	default:
		return e.Message.Role
	}
}

// trajTime renders an entry timestamp as HH:MM:SS ("—" when absent).
func trajTime(ts string) string {
	if strings.TrimSpace(ts) == "" {
		return "—"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("15:04:05")
	}
	return trajCap(ts, 8)
}

// trajToolNames lists assistant toolCall names in a message.
func trajToolNames(e pirpc.TreeEntry) []string {
	var out []string
	for _, b := range pirpc.BlocksOf(e.Message.Content) {
		if b.Type == "toolCall" && b.Name != "" {
			out = append(out, b.Name)
		}
	}
	if len(out) > 3 {
		out = append(out[:3], "…")
	}
	return out
}

// trajThinking joins thinking blocks (hidden in /tree rows, shown here).
func trajThinking(e pirpc.TreeEntry) string {
	var parts []string
	for _, b := range pirpc.BlocksOf(e.Message.Content) {
		if b.Type == "thinking" && strings.TrimSpace(b.Thinking) != "" {
			parts = append(parts, strings.TrimSpace(b.Thinking))
		}
	}
	return strings.Join(parts, "\n")
}

// trajDetail is the full step text posted to chat on Enter: title + meta +
// uncapped body (text, thinking, tool args, result). Capped at 2000 runes
// per body part so one step never floods the chat.
func trajDetail(idx int, n pirpc.TreeNode, row string, tcm map[string]pirpc.ContentBlock) string {
	e := n.Entry
	var b strings.Builder
	fmt.Fprintf(&b, "#%02d %s\n", idx, row)
	meta := trajKind(e) + " · id " + app.ShortID(e.ID) + " · " + trajTime(e.Timestamp)
	if e.ParentID != nil && *e.ParentID != "" {
		meta += " · parent " + app.ShortID(*e.ParentID)
	}
	if n.Label != "" {
		meta += " · [" + n.Label + "]"
	}
	b.WriteString(meta + "\n\n")
	body := trajBody(e, tcm)
	if strings.TrimSpace(body) == "" {
		body = "(no content)"
	}
	b.WriteString(body)
	return b.String()
}

// trajBody extracts the full step content per message role.
func trajBody(e pirpc.TreeEntry, tcm map[string]pirpc.ContentBlock) string {
	msg := e.Message
	switch msg.Role {
	case "user", "assistant":
		var parts []string
		if t := strings.TrimSpace(pirpc.TextOf(msg.Content)); t != "" {
			parts = append(parts, trajCap(t, 2000))
		}
		if th := trajThinking(e); th != "" {
			parts = append(parts, "thinking:\n"+trajCap(th, 2000))
		}
		for _, b := range pirpc.BlocksOf(msg.Content) {
			if b.Type == "toolCall" && b.Name != "" {
				call := "tool: " + treeTool(b.Name, b.Arguments)
				if diff := trajEditDiff(b.Name, string(b.Arguments)); diff != "" {
					parts = append(parts, call+"\ndiff:\n"+trajCap(diff, 2000))
					continue
				}
				args := strings.TrimSpace(string(b.Arguments))
				if b.Name == "edit" && args != "" {
					parts = append(parts, call+"\nargs: "+trajCap(args, 1000))
				} else {
					parts = append(parts, call+"\n"+trajCap(args, 1000))
				}
			}
		}
		if msg.StopReason == "aborted" {
			parts = append(parts, "(aborted)")
		}
		if strings.TrimSpace(msg.ErrorMessage) != "" {
			parts = append(parts, "error: "+trajCap(msg.ErrorMessage, 500))
		}
		return strings.Join(parts, "\n\n")
	case "toolResult":
		var parts []string
		if msg.ToolCallID != "" {
			if tc, ok := tcm[msg.ToolCallID]; ok {
				parts = append(parts, "call: "+treeTool(tc.Name, tc.Arguments))
				if tc.Name == "edit" {
					if diff := trajEditDiff(tc.Name, string(tc.Arguments)); diff != "" {
						parts = append(parts, "diff:\n"+trajCap(diff, 2000))
					} else if args := strings.TrimSpace(string(tc.Arguments)); args != "" {
						parts = append(parts, "args: "+trajCap(args, 1000))
					}
				} else if args := strings.TrimSpace(string(tc.Arguments)); args != "" {
					parts = append(parts, "args: "+trajCap(args, 1000))
				}
			}
		}
		if t := strings.TrimSpace(pirpc.TextOf(msg.Content)); t != "" {
			parts = append(parts, "result:\n"+trajCap(t, 2000))
		} else if d := strings.TrimSpace(string(msg.Details)); d != "" && d != "null" {
			parts = append(parts, "details:\n"+trajCap(d, 2000))
		}
		if msg.IsError {
			parts = append(parts, "(error)")
		}
		return strings.Join(parts, "\n\n")
	case "bashExecution":
		var parts []string
		if strings.TrimSpace(msg.Command) != "" {
			parts = append(parts, "$ "+trajCap(msg.Command, 500))
		}
		if t := strings.TrimSpace(pirpc.TextOf(msg.Content)); t != "" {
			parts = append(parts, trajCap(t, 2000))
		} else if strings.TrimSpace(msg.Output) != "" {
			parts = append(parts, trajCap(msg.Output, 2000))
		}
		return strings.Join(parts, "\n\n")
	default:
		return trajCap(pirpc.TextOf(msg.Content), 2000)
	}
}

func trajEditDiff(tool, args string) string {
	if tool == "edit" {
		return componentformat.EditDiffFallback(args)
	}
	return ""
}

// trajCap trims to n runes (no flattening: detail keeps newlines).
func trajCap(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// confirmTrajectory posts the picked step's full detail into the chat as a
// tree-styled block (same codeStyle rendering, no new view path) and closes
// the window.
func confirmTrajectory(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
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
