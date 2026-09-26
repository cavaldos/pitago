package app

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
	return m.armPetTick()
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
// Gated on hideTaskWidget (prefs.taskWidgetOff): the todos are already
// mirrored into the sidebar Todos panel, and this widget claims chat rows
// directly above the input, so users who keep tasks in the sidebar turn it
// off. The field is stored inverted so a zero Model{} still shows the
// widget — upstream behaviour, and what the existing tests assert.
func (m Model) renderTaskWidget() string {
	if m.hideTaskWidget || len(m.Todos) == 0 {
		return ""
	}
	width := max(10, m.mainW())
	accent := lipgloss.NewStyle().Foreground(cAccent)
	muted := statusBarStyle
	doneStyle := lipgloss.NewStyle().Foreground(cMuted).Strikethrough(true)
	// The header always totals every task, whatever the display settings do
	// to the body, so it reads m.Todos and not the truncated list.
	header := accent.Render("●") + " " + accent.Render(taskHeader(m.Todos))
	lines := []string{truncANSI(header, width)}

	// pi-tasks composes the display settings in a fixed order: sort, then
	// collapse, then truncate. Keep that order — collapsing before
	// truncating is what makes maxVisible count open tasks while the
	// completed ones sit in a single summary line.
	disp := m.taskDisplay
	ordered := orderTasks(m.Todos, disp.sortOrder)
	ordered, collapsed := collapseDone(ordered, disp.collapseCompleted)

	// Truncate last, and only if showAll is off (it overrides the cap).
	// hiddenAt "top" folds the overflow away from the top, which is what
	// pairs with the completed-first "status" sort.
	hidden := 0
	fromTop := disp.hiddenAt == "top"
	if !disp.showAll && len(ordered) > disp.maxRows() {
		hidden = len(ordered) - disp.maxRows()
		if fromTop {
			ordered = ordered[hidden:]
		} else {
			ordered = ordered[:disp.maxRows()]
		}
	}
	overflow := func() string {
		if hidden == 0 {
			return ""
		}
		return muted.Render(fmt.Sprintf("    … and %d more", hidden))
	}
	// The overflow line sits on the end the hidden tasks were taken from.
	if fromTop {
		if line := overflow(); line != "" {
			lines = append(lines, line)
		}
	}

	for _, t := range ordered {
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
	if !fromTop {
		if line := overflow(); line != "" {
			lines = append(lines, line)
		}
	}
	// collapseCompleted's replacement for the rows it removed, always last.
	if collapsed > 0 {
		lines = append(lines, okStyle.Render("✔")+" "+muted.Render(fmt.Sprintf("%d completed", collapsed)))
	}
	return strings.Join(lines, "\n")
}

// taskDisplay is the resolved display subset of tasks-config.json that the
// widget honours. Parsed once into the Model rather than per render:
// renderTaskWidget has a value receiver, so it cannot memoise, and the widget
// re-renders on every tick — a file read per paint is not acceptable.
// loadTaskDisplay therefore runs at Configure and after each settings write.
type taskDisplay struct {
	sortOrder         string
	collapseCompleted bool
	showAll           bool
	maxVisible        int
	hiddenAt          string
}

// defaultTaskDisplay is pi-tasks' documented defaults, which are also exactly
// what the widget hardcoded before the settings were wired — so an absent or
// unparseable config renders identically to the old code path.
func defaultTaskDisplay() taskDisplay {
	return taskDisplay{
		sortOrder:  "id",
		maxVisible: 10,
		hiddenAt:   "bottom",
	}
}

// loadTaskDisplay resolves the display settings from the merged global +
// project config and caches them on the Model.
func (m *Model) loadTaskDisplay() {
	d := defaultTaskDisplay()
	if m.cwd == "" {
		m.taskDisplay = d
		return
	}
	vals := loadTasksSettings(m.cwd, piAgentDir())
	if v, ok := vals["sortOrder"]; ok {
		switch v {
		case "id", "status", "active", "recent", "oldest":
			d.sortOrder = v
		}
	}
	if v, ok := vals["collapseCompleted"]; ok {
		d.collapseCompleted = v == "on"
	}
	if v, ok := vals["showAll"]; ok {
		d.showAll = v == "on"
	}
	if v, ok := vals["maxVisible"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			d.maxVisible = n // clamped by maxRows at use
		}
	}
	if v, ok := vals["hiddenAt"]; ok && v == "top" {
		d.hiddenAt = "top"
	}
	m.taskDisplay = d
}

// maxRows is the cap in force, normalised so the zero taskDisplay (a Model
// built without Configure) falls back to pi-tasks' default of 10 instead of
// truncating every task. Valid range is 5-100.
func (d taskDisplay) maxRows() int {
	if d.maxVisible < 5 || d.maxVisible > 100 {
		return 10
	}
	return d.maxVisible
}

// orderTasks applies sortOrder. Stable, so tasks the comparison cannot
// separate keep creation order — which is what the "then by id" in each
// pi-tasks preset means, and what keeps same-tick updatedAt ties in a
// deliberate order rather than an arbitrary one.
func orderTasks(tasks []TodoItem, order string) []TodoItem {
	out := append([]TodoItem(nil), tasks...)
	rank := func(status TodoStatus) int {
		switch status {
		case TodoInProgress:
			return 0
		case TodoPending:
			return 1
		default:
			return 2
		}
	}
	// "status" is completed-first (the reverse of rank) so it pairs with
	// hiddenAt "top", which folds finished work away from the top.
	statusRank := func(s TodoStatus) int { return 2 - rank(s) }
	switch order {
	case "status":
		sort.SliceStable(out, func(i, j int) bool {
			return statusRank(out[i].Status) < statusRank(out[j].Status)
		})
	case "active":
		sort.SliceStable(out, func(i, j int) bool {
			return rank(out[i].Status) < rank(out[j].Status)
		})
	case "recent":
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].UpdatedAt > out[j].UpdatedAt
		})
	case "oldest":
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].UpdatedAt < out[j].UpdatedAt
		})
	}
	return out
}

// collapseDone splits completed tasks out of the list when
// collapseCompleted is on, returning the remaining tasks and the completed
// count. The count still reaches the header, which always totals every task.
func collapseDone(tasks []TodoItem, collapse bool) ([]TodoItem, int) {
	if !collapse {
		return tasks, 0
	}
	kept := make([]TodoItem, 0, len(tasks))
	done := 0
	for _, t := range tasks {
		if t.Status == TodoCompleted {
			done++
			continue
		}
		kept = append(kept, t)
	}
	return kept, done
}

// TaskWidgetVisible reports whether the above-editor task widget paints.
func (m Model) TaskWidgetVisible() bool { return !m.hideTaskWidget }

// SetTaskWidget persists the above-editor task widget preference and
// repaints. Kept in prefs.json (not tasks-config.json) so the render path
// reads it from the cached Model field instead of a file per tick.
func (m *Model) SetTaskWidget(on bool) {
	m.hideTaskWidget = !on
	prefs := LoadPrefs(m.prefsPath)
	prefs.TaskWidgetOff = !on
	if err := SavePrefs(m.prefsPath, prefs); err != nil {
		m.AddBlock(Block{Kind: "notice", Text: "task widget preference not saved: " + err.Error(), Err: true})
		m.Refresh()
		return
	}
	state := "on"
	if !on {
		state = "off"
	}
	m.AddBlock(Block{Kind: "notice", Text: "task widget · above editor → " + state})
	m.Refresh()
}
