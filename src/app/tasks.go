package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"pitago/src/components/format"
)

var taskSpinner = []string{"✳", "✴", "✵", "✶", "✷", "✸", "✹", "✺", "✻", "✼", "✽"}

const taskVisibleLimit = 10

// taskActive reports the current execution marker, but only while its task
// still has in_progress status. This mirrors pi pruning stale active ids.
func (m Model) taskActive() bool {
	if m.task.activeID == "" {
		return false
	}
	for _, t := range m.Todos {
		if t.ID == m.task.activeID && t.Status == TodoInProgress {
			return true
		}
	}
	return false
}

// syncTaskRuntime starts metrics on the most recently updated in_progress
// task and prunes them as soon as that task leaves the running state.
func (m *Model) syncTaskRuntime() {
	valid := false
	var newest TodoItem
	for _, t := range m.Todos {
		if t.Status != TodoInProgress {
			continue
		}
		if t.ID == m.task.activeID {
			valid = true
		}
		if newest.ID == "" || t.UpdatedAt >= newest.UpdatedAt {
			newest = t
		}
	}
	if valid {
		return
	}
	m.task = taskRuntime{}
	if newest.ID == "" {
		return
	}
	m.task.activeID = newest.ID
	started := time.UnixMilli(newest.UpdatedAt)
	if newest.UpdatedAt <= 0 {
		started = time.Now()
	}
	m.task.startedAt = started
}

func (m *Model) ensureTaskTick() tea.Cmd {
	if !m.taskActive() || m.pet.ticking {
		return nil
	}
	m.pet.ticking = true
	return petTickCmd()
}

func (m *Model) addTaskUsage(input, output int) {
	// message_end usage is already the complete per-message total. If no
	// task is active yet, deliberately drop it like pi-tasks does; buffering
	// would attribute a pre-task assistant message to the next task.
	if !m.taskActive() {
		return
	}
	if input > 0 {
		m.task.inputTokens += input
	}
	if output > 0 {
		m.task.outputTokens += output
	}
}

var compactDuration = regexp.MustCompile(`^(\d+h)?(\d+m)?(\d+[smh])$`)

// taskElapsed adapts the shared duration helper to pi-tasks' spaced format.
func taskElapsed(d time.Duration) string {
	s := format.FmtDur(d)
	m := compactDuration.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	var parts []string
	for _, p := range m[1:] {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

func taskHeader(tasks []TodoItem) string {
	done, running, open := 0, 0, 0
	for _, t := range tasks {
		switch t.Status {
		case TodoCompleted:
			done++
		case TodoInProgress:
			running++
		default:
			open++
		}
	}
	var parts []string
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d in progress", running))
	}
	if open > 0 {
		parts = append(parts, fmt.Sprintf("%d open", open))
	}
	return fmt.Sprintf("%d tasks (%s)", len(tasks), strings.Join(parts, ", "))
}

func taskStatusByID(tasks []TodoItem, id string) (TodoStatus, bool) {
	for _, t := range tasks {
		if t.ID == id {
			return t.Status, true
		}
	}
	return "", false
}

func taskBlockedSuffix(tasks []TodoItem, t TodoItem) string {
	if t.Status != TodoPending {
		return ""
	}
	var open []string
	for _, id := range t.BlockedBy {
		if st, ok := taskStatusByID(tasks, id); ok && st != TodoCompleted {
			open = append(open, "#"+id)
		}
	}
	if len(open) == 0 {
		return ""
	}
	return " › blocked by " + strings.Join(open, ", ")
}

// renderTaskWidget mirrors pi-tasks' borderless aboveEditor widget.
func (m Model) renderTaskWidget() string {
	if len(m.Todos) == 0 {
		return ""
	}
	width := max(10, m.mainW())
	accent := lipgloss.NewStyle().Foreground(cAccent)
	muted := statusBarStyle
	doneStyle := lipgloss.NewStyle().Foreground(cMuted).Strikethrough(true)
	header := accent.Render("●") + " " + accent.Render(taskHeader(m.Todos))
	lines := []string{truncANSI(header, width)}

	visible := m.Todos
	if len(visible) > taskVisibleLimit {
		visible = visible[:taskVisibleLimit]
	}
	for _, t := range visible {
		active := m.taskActive() && t.ID == m.task.activeID && t.Status == TodoInProgress
		var line string
		switch {
		case active:
			form := t.SubAct
			if form == "" {
				form = t.Content
			}
			stats := fmt.Sprintf(" (%s", taskElapsed(time.Since(m.task.startedAt)))
			parts := make([]string, 0, 2)
			if m.task.inputTokens > 0 {
				parts = append(parts, "↑ "+format.FmtNum(m.task.inputTokens))
			}
			if m.task.outputTokens > 0 {
				parts = append(parts, "↓ "+format.FmtNum(m.task.outputTokens))
			}
			if len(parts) > 0 {
				stats += " · " + strings.Join(parts, " ")
			}
			stats += ")"
			prefix := "  " + taskSpinner[m.pet.tick%len(taskSpinner)] + " " + muted.Render("#"+t.ID) + " "
			formMax := width - runewidth.StringWidth(prefix) - runewidth.StringWidth(stats) - 1
			form = runewidth.Truncate(form+"…", max(1, formMax), "...")
			line = prefix + accent.Render(form) + muted.Render(stats)
		case t.Status == TodoCompleted:
			line = "  " + okStyle.Render("✔") + " " + doneStyle.Render("#"+t.ID+" "+t.Content)
		case t.Status == TodoInProgress:
			line = "  " + accent.Render("◼") + " " + muted.Render("#"+t.ID) + " " + lipgloss.NewStyle().Foreground(cText).Render(t.Content)
		default:
			line = "  " + muted.Render("◻") + " " + muted.Render("#"+t.ID) + " " + lipgloss.NewStyle().Foreground(cText).Render(t.Content)
		}
		line += muted.Render(taskBlockedSuffix(m.Todos, t))
		lines = append(lines, truncANSI(line, width))
	}
	if hidden := len(m.Todos) - len(visible); hidden > 0 {
		lines = append(lines, muted.Render(fmt.Sprintf("    … and %d more", hidden)))
	}
	return strings.Join(lines, "\n")
}
