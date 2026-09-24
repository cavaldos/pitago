package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// Resume picker (pi's /resume): current-project sessions newest-first, Tab
// toggles the All scope. "/resume <path>" resumes a file directly; any
// other arg pre-filters the picker.

// OpenResume loads the current-project scope (pi's Current Folder).
func (m *Model) OpenResume(arg string) tea.Cmd {
	if m.spawnOpts.NoSession {
		m.AddBlock(Block{Kind: "notice", Text: "sessions aren't saved in --no-session mode"})
		m.Refresh()
		return nil
	}
	dir := pirpc.SessionDirFor(m.cwd)
	if arg != "" {
		if p := resolveSessionArg(dir, arg); p != "" {
			return m.SwitchSession(p)
		}
	}
	m.Status = "loading sessions…"
	m.Refresh()
	cwd, current := m.cwd, m.sessionFile
	return func() tea.Msg {
		list := pirpc.ListSessions(dir, cwd, 40, true)
		if len(list) == 0 {
			return SettingsRefreshMsg{Notice: "no saved sessions for this project yet — Tab for all"}
		}
		return resumePickerMsg("current", list, current, arg, false)
	}
}

// ReloadResumeScope reloads the picker in the other scope (Tab), keeping the
// typed filter.
func (m *Model) ReloadResumeScope(d *Dialog) tea.Cmd {
	scope := "all"
	if d.Scope == "all" {
		scope = "current"
	}
	m.Status = "loading sessions…"
	m.Refresh()
	cwd, current, filter := m.cwd, m.sessionFile, d.Filter
	return func() tea.Msg {
		var list []pirpc.SessionInfo
		if scope == "all" {
			list = pirpc.ListAllSessions(pirpc.SessionRoot(), 100)
		} else {
			list = pirpc.ListSessions(pirpc.SessionDirFor(cwd), cwd, 40, true)
		}
		if len(list) == 0 {
			return SettingsRefreshMsg{Notice: "no sessions in this scope"}
		}
		return resumePickerMsg(scope, list, current, filter, true)
	}
}

// resumePickerMsg builds the picker rows: title + "N msgs · age" (+ cwd in
// the All scope, like pi), current session marked and preselected. Payload
// parallels Options with the multi-line detail for the right column.
func resumePickerMsg(scope string, list []pirpc.SessionInfo, current, filter string, replace bool) PickerMsg {
	opts := make([]string, 0, len(list))
	descs := make([]string, 0, len(list))
	paths := make([]string, 0, len(list))
	payload := make([]string, 0, len(list))
	for _, s := range list {
		opts = append(opts, s.Title())
		desc := fmt.Sprintf("%d msgs · %s", s.MessageCount, pirpc.Ago(s.Modified))
		if scope == "all" && s.Cwd != "" {
			desc = pirpc.Shorten(s.Cwd) + " · " + desc
		}
		if s.Path == current {
			desc = "current · " + desc
		}
		descs = append(descs, desc)
		paths = append(paths, s.Path)
		payload = append(payload, sessionDetail(s, current))
	}
	return PickerMsg{Kind: "sessions", Scope: scope, Options: opts, Descs: descs, Paths: paths, Payload: payload, Filter: filter, Current: current, Replace: replace}
}

// sessionDetail is the right-column text for one session: title, activity,
// file + project dir, current marker, and the first user message (capped —
// the renderer wraps it to the column).
func sessionDetail(s pirpc.SessionInfo, current string) string {
	var b strings.Builder
	b.WriteString(s.Title() + "\n")
	fmt.Fprintf(&b, "%d msgs · %s · %s\n", s.MessageCount, pirpc.Ago(s.Modified), s.Modified.Format("2006-01-02 15:04"))
	b.WriteString(pirpc.Shorten(s.Path) + "\n")
	if s.Cwd != "" {
		b.WriteString(pirpc.Shorten(s.Cwd) + "\n")
	}
	if s.Path == current {
		b.WriteString("current session\n")
	}
	if name := strings.TrimSpace(s.Name); name != "" {
		b.WriteString("name: " + name + "\n")
	}
	first := strings.TrimSpace(s.FirstMessage)
	if first == "" {
		first = "(no messages)"
	}
	if r := []rune(first); len(r) > 600 {
		first = string(r[:600]) + "…"
	}
	b.WriteString(first)
	return b.String()
}

// DeleteResumeSession deletes the highlighted session file (Del / ⌫ with
// empty filter / Ctrl+D). Stays on the picker; closes it when empty.
// The current session can't be deleted — switch first.
func (m *Model) DeleteResumeSession(d *Dialog) (tea.Model, tea.Cmd) {
	if len(d.FIdx) == 0 || d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return m, nil
	}
	ri := d.FIdx[d.Cursor]
	if ri < 0 || ri >= len(d.Paths) {
		return m, nil
	}
	path := d.Paths[ri]
	if path == "" {
		return m, nil
	}
	if path == m.sessionFile {
		m.Status = "can't delete the current session — switch first"
		m.Refresh()
		return m, nil
	}
	if err := pirpc.DeleteSession(path); err != nil {
		m.Status = "delete failed: " + err.Error()
		m.Refresh()
		return m, nil
	}
	title := ""
	if ri < len(d.Options) {
		title = d.Options[ri]
	}
	d.Options = append(d.Options[:ri], d.Options[ri+1:]...)
	if ri < len(d.Descs) {
		d.Descs = append(d.Descs[:ri], d.Descs[ri+1:]...)
	}
	d.Paths = append(d.Paths[:ri], d.Paths[ri+1:]...)
	if ri < len(d.Payload) {
		d.Payload = append(d.Payload[:ri], d.Payload[ri+1:]...)
	}
	d.Reindex()
	if d.Cursor >= len(d.FIdx) {
		d.Cursor = len(d.FIdx) - 1
	}
	if d.Cursor < 0 {
		d.Cursor = 0
	}
	if len(d.Options) == 0 {
		m.Dialogs = m.Dialogs[1:]
		m.AddBlock(Block{Kind: "notice", Text: "deleted session — no sessions left in this scope"})
		m.Refresh()
		return m, nil
	}
	if title == "" {
		title = path
	}
	m.Status = "deleted: " + Short(title, 50)
	m.Refresh()
	return m, nil
}

// replaceIntoOpen reports a Tab scope-swap picker aimed at the open dialog:
// an in-place reload, not a second dialog, so it bypasses dialog capture
// (without this the swap message is swallowed and the picker never reloads).
func replaceIntoOpen(d *Dialog, msg tea.Msg) bool {
	pm, ok := msg.(PickerMsg)
	return ok && pm.Replace && d.Kind == pm.Kind
}

// renderResumeDialog draws the /resume window: left = session names (the
// current one dotted green), right = the selected session's detail. Same
// fixed-size two-column layout as /trajectory; Tab/Del/Enter keep their
// generic updateDialog behavior.
func (m Model) renderResumeDialog(d *Dialog) string {
	var b strings.Builder
	boxW, _, _ := trajGeom(m.winW)
	cw := trajContentW(m.winW)
	// resume splits the content evenly: left names, right detail (the
	// odd cell, if any, goes to the detail column)
	leftW := (cw - 3) / 2
	rightW := cw - 3 - leftW
	win := trajWin(m.winH)

	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(d.Title, cw)) + "\n")
	b.WriteString(statusBarStyle.Render(Fit("filter: "+d.Filter+"▌", cw)) + "\n")
	b.WriteString("\n")

	// Left window (session names): fixed rows so the box never resizes.
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	var leftLines []string
	if above {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d above)", start), leftW-2)))
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		title := ""
		if ri >= 0 && ri < len(d.Options) {
			title = d.Options[ri]
		}
		if fi == d.Cursor {
			// selected: full-width highlight, no inner colors (an inner
			// reset would kill the highlight background past the token)
			leftLines = append(leftLines, statusBarStyle.Render("▸ ")+rowHiStyle.Render(Fit(title, leftW-2)))
		} else if strings.HasPrefix(DescOf(d, ri), "current") {
			leftLines = append(leftLines, statusBarStyle.Render("  ")+okStyle.Render("● ")+statusBarStyle.Render(Fit(title, leftW-4)))
		} else {
			leftLines = append(leftLines, statusBarStyle.Render("  ")+statusBarStyle.Render(Fit(title, leftW-2)))
		}
	}
	if below {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d below)", total-end), leftW-2)))
	}
	if total == 0 {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit("— no match —", leftW-2)))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Render(Fit("", leftW-2)))
	}

	// Right window (selected session's detail, wrapped to the column).
	rows := trajDetailRows(trajSelected(d), rightW-4)
	if len(rows) > win {
		rows = rows[:win]
	}
	var rightLines []string
	for _, r := range rows {
		plain := Fit(r.text, rightW-4)
		switch {
		case r.title:
			rightLines = append(rightLines, "  "+trajPad(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(plain), rightW-2))
		case r.meta:
			rightLines = append(rightLines, "  "+trajPad(toolStyle.Render(plain), rightW-2))
		default:
			rightLines = append(rightLines, "  "+trajPad(lipgloss.NewStyle().Foreground(cText).Render(plain), rightW-2))
		}
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+trajPad("", rightW-2))
	}

	b.WriteString(trajPad("  "+sideTitleStyle.Render(Fit("SESSIONS", leftW-2))+" "+sepStyle.Render("│")+" "+"  "+sideTitleStyle.Render(Fit("DETAIL", rightW-2)), cw) + "\n")
	sep := sepStyle.Render("│")
	for i := 0; i < win; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		b.WriteString(trajPad(l+" "+sep+" "+r, cw) + "\n")
	}
	if skipped := len(trajDetailRows(trajSelected(d), rightW-4)) - win; skipped > 0 {
		b.WriteString(toolStyle.Render(Fit(fmt.Sprintf("…(+%d lines below: Enter resumes, detail stays in session)", skipped), cw)) + "\n")
	}
	b.WriteString("\n")
	foot := "type to filter · ↑↓ select · Enter resume · Tab scope · Del delete · Esc close"
	if n := len(d.FIdx); n > 0 {
		cur := d.Cursor + 1
		if cur > n {
			cur = n
		}
		foot += fmt.Sprintf(" (%d/%d · %s)", cur, n, d.Scope)
	}
	b.WriteString(toolStyle.Render(Fit(foot, cw)))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// resolveSessionArg maps "/resume <arg>" to a file: existing path as-is,
// else a name inside the session dir (pi --session <path|id> parity).
func resolveSessionArg(dir, arg string) string {
	if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
		return arg
	}
	if p := filepath.Join(dir, arg); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
