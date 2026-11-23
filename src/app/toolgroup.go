package app

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/format"
)

// Consecutive thinking and tool activity folds into a clickable summary row.

// groupableTool reports whether a block can join a summary group.
func groupableTool(bl Block) bool {
	switch bl.Kind {
	case "thinking":
		return strings.TrimSpace(bl.Text) != ""
	case "bash":
		return true
	case "tool":
		return len(bl.Images) == 0
	default:
		return false
	}
}

// invisibleBlock reports blocks renderOneBlock skips, which must not break
// a run of tool calls (empty streaming leftovers, hidden thinking).
func (m *Model) invisibleBlock(bl Block) bool {
	if (bl.Kind == "assistant" || bl.Kind == "thinking") && strings.TrimSpace(bl.Text) == "" {
		return true
	}
	return bl.Kind == "thinking" && m.HideThinking
}

// computeToolGroups fills m.toolGroup: for every block in a run of two or
// more groupable tool calls, the index of the run's first block; -1 for
// every other block.
func (m *Model) computeToolGroups() {
	if len(m.toolGroup) != len(m.blocks) {
		m.toolGroup = make([]int, len(m.blocks))
	}
	for i := range m.toolGroup {
		m.toolGroup[i] = -1
	}
	head, count := -1, 0
	var members []int
	flush := func() {
		if count >= 2 {
			for _, k := range members {
				m.toolGroup[k] = head
			}
		}
		head, count, members = -1, 0, members[:0]
	}
	for i, bl := range m.blocks {
		switch {
		case groupableTool(bl):
			if head < 0 {
				head = i
			}
			count++
			members = append(members, i)
		case m.invisibleBlock(bl):
			// Transparent: keeps the run going.
		default:
			flush()
		}
	}
	flush()
}

// groupMembers lists the block indices of the group headed at head.
func (m *Model) groupMembers(head int) []int {
	var out []int
	for i := head; i < len(m.toolGroup); i++ {
		switch {
		case m.toolGroup[i] == head:
			out = append(out, i)
		case m.toolGroup[i] < 0 && m.invisibleBlock(m.blocks[i]):
			// skipped block inside the run
		default:
			return out
		}
	}
	return out
}

// groupCollapsed reports whether the group headed at head shows only its
// summary row.
func (m *Model) groupCollapsed(head int) bool {
	return !m.expandTools && !m.blocks[head].GroupOpen
}

// toolGroupKey fingerprints everything a group's rendering depends on, so
// the per-block render cache misses when a member changes.
func (m *Model) toolGroupKey(i int) uint64 {
	head := m.toolGroup[i]
	h := fnv.New64a()
	fmt.Fprintf(h, "group\x00%d\x00%v", i-head, m.groupCollapsed(head))
	if i == head {
		for _, k := range m.groupMembers(head) {
			bl := m.blocks[k]
			fmt.Fprintf(h, "\x00%s\x00%s", bl.ToolName, bl.ToolStatus)
		}
		if m.groupRunning(head) {
			fmt.Fprintf(h, "\x00blink\x00%v", blinkOn())
		}
	}
	return h.Sum64()
}

func (m *Model) groupRunning(head int) bool {
	for _, k := range m.groupMembers(head) {
		if format.ToolStatusClass(m.blocks[k].ToolStatus) == format.StatusRunning {
			return true
		}
	}
	return false
}

// renderToolGroupSummary renders the one-line summary for the group headed
// at head: "● Read 3 files, ran 1 shell command".
func (m *Model) renderToolGroupSummary(head, w int) string {
	members := m.groupMembers(head)
	running, failed := false, false
	type tally struct {
		label string
		count int
	}
	var order []string
	counts := map[string]*tally{}
	for _, k := range members {
		bl := m.blocks[k]
		switch format.ToolStatusClass(bl.ToolStatus) {
		case format.StatusRunning:
			running = true
		case format.StatusError:
			failed = true
		}
		key, label := "tool", "tool"
		switch {
		case bl.Kind == "thinking":
			key, label = "thought", "thought"
		case bl.Kind == "bash":
			key, label = "command", "command"
		case strings.EqualFold(strings.TrimSpace(bl.ToolName), "bash"):
			key, label = "command", "command"
		case strings.EqualFold(strings.TrimSpace(bl.ToolName), "read"):
			key, label = "read", "read"
		}
		if t, ok := counts[key]; ok {
			t.count++
		} else {
			counts[key] = &tally{label: label, count: 1}
			order = append(order, key)
		}
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		t := counts[key]
		label := t.label
		if t.count != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", t.count, label))
	}
	text := strings.Join(parts, ", ")
	if text != "" && running {
		text += "…"
	}

	dot := runningDot()
	switch {
	case running:
	case failed:
		dot = errStyle.Render("●")
	default:
		dot = okStyle.Render("●")
	}
	hint := " (click to expand)"
	if !m.groupCollapsed(head) {
		hint = " (click to collapse)"
	}
	nameSt := lipgloss.NewStyle().Bold(true).Foreground(cText)
	avail := max(w-2-lipgloss.Width(hint), 8)
	row := dot + " " + nameSt.Render(Short(text, avail))
	if lipgloss.Width(text) <= avail {
		row += toolStyle.Render(hint)
	}
	return row
}

// isGroupSummaryRow reports whether screen row screenY is the summary row
// of a tool group headed at block i, so a click there folds the group
// instead of opening the first call.
func (m *Model) isGroupSummaryRow(i, screenY int) bool {
	if i < 0 || i >= len(m.toolGroup) || m.toolGroup[i] != i || i >= len(m.blockRows) {
		return false
	}
	chatVp := m.chatViewport()
	return chatVp.YOffset+(screenY-1) == m.blockRows[i]
}
