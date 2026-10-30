package app

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"pitago/src/components/filetree"
	"pitago/src/components/markdown"
	"pitago/src/components/mention"
	"pitago/src/components/snippet"
)

// /focus: open one file, select lines, and ask the agent about only those
// lines (lgtm-style "point at exact lines"). The agent gets path:start-end
// plus the exact excerpt, so it does not need to read the whole file — or
// the codebase — to answer or edit.
//
//	┌ tree (neo-tree) ┐│┌ code ─────────────────────────┐
//	│ ▾ src/        M ││  41 │ func A() {               │
//	│     a.go      M ││▌ 42 │     return 1             │  v / drag: select
//	│ ▸ docs/         ││▸ 43 │ }                        │  Enter: ask box
//	└─────────────────┘│└──────────────────────────────┘
//	 Tab / h ⇄ panes · Ctrl+E hide tree · / fuzzy search
//
// File loading, ranges and the prompt text live in components/snippet, the
// tree in components/filetree; this file is the window around them.

const focusKind = "focus"

// Stages of the /focus window. The tree/code panes are both "focusCode";
// pane says which one has the keys.
const (
	focusPick = iota // fuzzy file search in the right column
	focusCode        // tree + code panes
	focusAsk         // ask box under the code
)

const (
	paneTree = iota
	paneCode
)

// highlightMax caps syntax highlighting: Chroma on a huge file stalls the
// event loop, and a plain view is still usable.
const highlightMax = 256 << 10

// focusPos is where the cursor was in one file, so switching files and
// coming back lands on the same lines (vim buffer style).
type focusPos struct{ cur, top, anchor int }

type focusState struct {
	stage    int
	pane     int
	showTree bool

	// fuzzy search
	query *lineEdit
	items []mention.Item
	pcur  int

	// tree sidebar
	tree  *filetree.Tree
	trows []filetree.Row
	tcur  int
	ttop  int

	// code
	file     *snippet.File
	hl       []string // display lines (tabs expanded, maybe highlighted)
	plain    []string // display lines without colour (selected rows)
	cur      int      // cursor line, 0-based
	top      int      // first visible line
	anchor   int      // selection anchor, 0-based; -1 = no selection
	pos      map[string]focusPos
	dragging bool // left button held after a press on a code line
	dragFrom int

	// ask
	ask    *lineEdit
	preset int
}

// focusLast remembers the window between runs so a bare /focus reopens it.
type focusLast struct {
	path     string
	pos      map[string]focusPos
	showTree bool
}

// OpenFocus runs /focus [path[:start[-end]]].
func (m *Model) OpenFocus(arg string) tea.Cmd {
	_, cw := focusGeom(m.winW)
	fs := &focusState{stage: focusCode, pane: paneTree, query: newLineEdit(""), anchor: -1,
		pos: map[string]focusPos{}, showTree: cw >= 70}
	if last := m.focusLast; last != nil {
		for k, v := range last.pos {
			fs.pos[k] = v
		}
		fs.showTree = last.showTree
	}
	fs.tree = filetree.Load(m.cwd)
	fs.trows = fs.tree.Rows()
	arg = strings.TrimSpace(arg)
	switch {
	case arg != "":
		path, r, hasRange := snippet.ParseTarget(arg)
		if err := m.focusOpen(fs, path); err != nil {
			// Not a file: search for it instead.
			fs.query = newLineEdit(arg)
			fs.stage = focusPick
			m.focusRefreshPick(fs)
			break
		}
		if hasRange {
			n := len(fs.file.Lines)
			fs.anchor = min(r.Start-1, n-1)
			fs.cur = min(r.End-1, n-1)
			if fs.anchor == fs.cur {
				fs.anchor = -1
			}
		}
	case m.focusLast != nil:
		_ = m.focusOpen(fs, m.focusLast.path) // gone since: start on the tree
	}
	if fs.file == nil {
		fs.pane, fs.showTree = paneTree, true
	}
	m.Dialogs = append(m.Dialogs, &Dialog{Kind: focusKind, Title: "Focus", focus: fs})
	m.focusScroll(fs)
	m.focusScrollTree(fs)
	m.Refresh()
	return nil
}

// focusOpen loads a file into the code pane, keeping each file's cursor,
// and reveals it in the tree (neo-tree's follow-current-file).
func (m *Model) focusOpen(fs *focusState, path string) error {
	f, err := snippet.Load(m.cwd, path)
	if err != nil {
		return err
	}
	if fs.file != nil {
		fs.pos[fs.file.Abs] = focusPos{fs.cur, fs.top, fs.anchor}
	}
	fs.file = f
	fs.plain = make([]string, len(f.Lines))
	for i, ln := range f.Lines {
		fs.plain[i] = expandTabs(ln)
	}
	fs.hl = fs.plain
	if f.Lang != "" && sizeOf(f.Lines) <= highlightMax {
		out := markdown.Highlight(f.Lang, strings.Join(fs.plain, "\n"))
		if lines := strings.Split(out, "\n"); len(lines) == len(fs.plain) {
			fs.hl = lines
		}
	}
	fs.stage, fs.pane, fs.cur, fs.top, fs.anchor, fs.dragging = focusCode, paneCode, 0, 0, -1, false
	if p, ok := fs.pos[f.Abs]; ok {
		n := len(f.Lines)
		fs.cur, fs.top = min(p.cur, n-1), min(p.top, n-1)
		if p.anchor >= 0 {
			fs.anchor = min(p.anchor, n-1)
		}
	}
	if fs.tree != nil {
		if node := fs.tree.Reveal(f.Path); node != nil {
			fs.trows = fs.tree.Rows()
			if i := filetree.IndexOf(fs.trows, node); i >= 0 {
				fs.tcur = i
			}
		}
	}
	m.focusScroll(fs)
	m.focusScrollTree(fs)
	return nil
}

func sizeOf(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	return n
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

func (m *Model) focusRefreshPick(fs *focusState) {
	fs.items = mention.Candidates(m.cwd, fs.query.value(), false)
	if fs.pcur >= len(fs.items) {
		fs.pcur = 0
	}
}

// focusRange is the selection, or the cursor line when nothing is selected.
func (fs *focusState) focusRange() snippet.Range {
	a := fs.anchor
	if a < 0 {
		a = fs.cur
	}
	return snippet.Norm(a, fs.cur, len(fs.file.Lines))
}

func (fs *focusState) remember() *focusLast {
	last := &focusLast{pos: fs.pos, showTree: fs.showTree}
	if fs.file != nil {
		fs.pos[fs.file.Abs] = focusPos{fs.cur, fs.top, fs.anchor}
		last.path = fs.file.Abs
	}
	return last
}

// treeNode is the node under the tree cursor (nil on an empty tree).
func (fs *focusState) treeNode() *filetree.Node {
	if fs.tcur >= 0 && fs.tcur < len(fs.trows) {
		return fs.trows[fs.tcur].N
	}
	return nil
}

// --- layout -----------------------------------------------------------------

// focusGeom is the box width and the content width inside dlgStyle
// (padding 3 each side; lipgloss Width excludes the border).
func focusGeom(winW int) (boxW, cw int) {
	boxW = max(40, min(winW-4, 180))
	return boxW, boxW - 6
}

// focusCols splits the content width into tree | code (" │ " between).
func (fs *focusState) focusCols(cw int) (treeW, codeW int) {
	if !fs.showTree {
		return 0, cw
	}
	treeW = max(22, min(36, cw/4))
	return treeW, max(10, cw-treeW-3)
}

// focusAskRows is how many rows the ask box takes under the code.
func (fs *focusState) focusAskRows(cw int) int {
	if fs.stage != focusAsk || fs.ask == nil {
		return 0
	}
	return 3 + min(4, len(fs.ask.renderBlock(cw-2)))
}

// focusBodyRows is the pane height: the screen (winH-2) minus border and
// padding (4), header (3), footer (2) and the ask box.
func (m Model) focusBodyRows(fs *focusState) int {
	_, cw := focusGeom(m.winW)
	return max(5, m.winH-2-4-5-fs.focusAskRows(cw))
}

// focusLayout is where the panes sit on screen, for mouse hit-testing. It
// mirrors renderFocusDialog: the box is centred by lipgloss.Place in
// winW × winH-2, and content starts after border (1) + padding (3 / 1).
type focusLayout struct {
	treeW, codeW int
	x0, y0       int // screen cell of the first body row's first column
	rows         int
}

func (m Model) focusLayoutOf(fs *focusState) focusLayout {
	boxW, cw := focusGeom(m.winW)
	l := focusLayout{rows: m.focusBodyRows(fs)}
	l.treeW, l.codeW = fs.focusCols(cw)
	boxH := 3 + l.rows + fs.focusAskRows(cw) + 2 + 4
	l.x0 = max(0, (m.winW-(boxW+2))/2) + 4
	l.y0 = max(0, (m.winH-2-boxH)/2) + 2 + 3
	return l
}

// codeX is the content column where the code pane starts.
func (l focusLayout) codeX() int {
	if l.treeW == 0 {
		return 0
	}
	return l.treeW + 3
}

func scrollInto(cur, top, rows, total int) int {
	pad := min(3, rows/4)
	if cur < top+pad {
		top = cur - pad
	}
	if cur >= top+rows-pad {
		top = cur - rows + pad + 1
	}
	return max(0, min(top, total-rows))
}

// focusScroll keeps the code cursor in view with a little context.
func (m Model) focusScroll(fs *focusState) {
	if fs.file != nil {
		fs.top = scrollInto(fs.cur, fs.top, m.focusBodyRows(fs), len(fs.file.Lines))
	}
}

// focusScrollTree keeps the tree cursor in view.
func (m Model) focusScrollTree(fs *focusState) {
	fs.tcur = max(0, min(fs.tcur, len(fs.trows)-1))
	fs.ttop = scrollInto(fs.tcur, fs.ttop, m.focusBodyRows(fs), len(fs.trows))
}

// --- render -----------------------------------------------------------------

func (m Model) renderFocusDialog(d *Dialog) string {
	fs := d.focus
	boxW, cw := focusGeom(m.winW)
	rows := m.focusBodyRows(fs)
	treeW, codeW := fs.focusCols(cw)
	title := lipgloss.NewStyle().Bold(true).Foreground(cText)
	var b strings.Builder

	// Header: title, status, rule.
	if fs.file == nil {
		b.WriteString(title.Render(Fit("Focus", cw)) + "\n")
		b.WriteString(statusBarStyle.Render(Fit("Pick a file in the tree (Enter) or press / to search. Only the lines you select reach the agent.", cw)) + "\n")
	} else {
		r := fs.focusRange()
		sel := fmt.Sprintf("line %d", fs.cur+1)
		if fs.anchor >= 0 {
			sel = fmt.Sprintf("selected %d line(s)", r.Lines())
		}
		lang := fs.file.Lang
		if lang == "" {
			lang = "text"
		}
		b.WriteString(title.Render(Fit("Focus · "+fs.file.Path, cw)) + "\n")
		b.WriteString(statusBarStyle.Render(Fit(fmt.Sprintf("%s · %d lines · %s → %s", lang, len(fs.file.Lines), sel, snippet.Ref(fs.file.Path, r)), cw)) + "\n")
	}
	b.WriteString(sepStyle.Render(strings.Repeat("─", cw)) + "\n")

	// Body: tree | right column.
	var right []string
	switch {
	case fs.stage == focusPick:
		right = m.focusPickLines(fs, codeW, rows)
	case fs.file == nil:
		right = []string{"", toolStyle.Render(Fit("  No file open.", codeW)),
			toolStyle.Render(Fit("  ← choose one in the tree, or press / to search.", codeW))}
	default:
		right = m.focusCodeLines(fs, codeW, rows)
	}
	var left []string
	if treeW > 0 {
		left = m.focusTreeLines(fs, treeW, rows)
	}
	divider := sepStyle
	if fs.pane == paneTree && fs.stage == focusCode {
		divider = lipgloss.NewStyle().Foreground(cAccent)
	}
	for i := 0; i < rows; i++ {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		if treeW > 0 {
			l := ""
			if i < len(left) {
				l = left[i]
			}
			b.WriteString(trajPad(l, treeW) + " " + divider.Render("│") + " " + r + "\n")
		} else {
			b.WriteString(r + "\n")
		}
	}

	// Ask box.
	var foot string
	switch {
	case fs.stage == focusAsk && fs.ask != nil:
		r := fs.focusRange()
		b.WriteString(sepStyle.Render(strings.Repeat("─", cw)) + "\n")
		b.WriteString(sideTitleStyle.Render(Fit("Ask about "+snippet.Ref(fs.file.Path, r), cw)) + "\n")
		lines := fs.ask.renderBlock(cw - 2)
		if len(lines) > 4 {
			lines = lines[len(lines)-4:]
		}
		for _, ln := range lines {
			b.WriteString(cmdHiStyle.Render("▌") + " " + ln + "\n")
		}
		if fs.ask.value() == "" {
			b.WriteString(toolStyle.Render(Fit("  Tab: preset prompts", cw)) + "\n")
		} else {
			b.WriteString("\n")
		}
		foot = "Enter send · Ctrl+J newline · Tab presets · Esc back to code"
	case fs.stage == focusPick:
		foot = "type to search · ↑↓ move · Enter open (dir: enter it) · Esc back"
	case fs.pane == paneTree:
		foot = "↑↓ move · Enter open · Space preview · h/l fold · / search · Tab code · ^E hide · R refresh · q close"
	default:
		foot = "↑↓ move · v/drag select · Enter ask · i insert · y copy · Tab/h tree · / search · ^E tree · Esc close"
	}
	b.WriteString("\n" + toolStyle.Render(Fit(m.dialogFoot(foot), cw)))
	box := dlgStyle.Width(boxW).Render(b.String())
	return lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box)
}

// focusMarkStyle colours a git mark the way neo-tree does.
func focusMarkStyle(mark string) lipgloss.Style {
	switch mark {
	case "M":
		return warnStyle
	case "A", "?":
		return okStyle
	case "D", "U":
		return errStyle
	case "R":
		return lipgloss.NewStyle().Foreground(cCyan)
	}
	return lipgloss.NewStyle().Foreground(cText)
}

func (m Model) focusTreeLines(fs *focusState, w, rows int) []string {
	var out []string
	curPath := ""
	if fs.file != nil {
		curPath = fs.file.Path
	}
	for i := fs.ttop; i < fs.ttop+rows && i < len(fs.trows); i++ {
		r := fs.trows[i]
		n := r.N
		icon := "  "
		name := n.Name
		if n.Dir {
			icon = "▸ "
			if n.Open {
				icon = "▾ "
			}
			name += "/"
		}
		label := Fit(strings.Repeat("  ", r.Depth)+icon+name, w-2)
		mark := Fit(n.Status, 1)
		if i == fs.tcur && fs.pane == paneTree && fs.stage == focusCode {
			out = append(out, rowHiStyle.Render(label+" "+mark))
			continue
		}
		style := focusMarkStyle(n.Status)
		switch {
		case n.Path == curPath:
			style = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
		case n.Dir && n.Status == "":
			style = sideTitleStyle
		}
		out = append(out, style.Render(label)+" "+focusMarkStyle(n.Status).Render(mark))
	}
	if len(fs.trows) == 0 {
		out = append(out, toolStyle.Render(Fit("— empty —", w)))
	}
	return out
}

func (m Model) focusPickLines(fs *focusState, w, rows int) []string {
	out := []string{fs.query.renderLine(sideTitleStyle.Render("search: "), w)}
	start, end, above, below := fixedWin(fs.pcur, len(fs.items), rows-1)
	if above {
		out = append(out, toolStyle.Render(Fit(fmt.Sprintf("  …(+%d above)", start), w)))
	}
	for i := start; i < end; i++ {
		it := fs.items[i]
		row := it.Desc
		if row == "" {
			row = it.Label
		}
		switch {
		case i == fs.pcur:
			out = append(out, statusBarStyle.Render("▸ ")+rowHiStyle.Render(Fit(row, w-2)))
		case it.Dir:
			out = append(out, "  "+sideTitleStyle.Render(Fit(row, w-2)))
		default:
			out = append(out, "  "+lipgloss.NewStyle().Foreground(cText).Render(Fit(row, w-2)))
		}
	}
	if below {
		out = append(out, toolStyle.Render(Fit(fmt.Sprintf("  …(+%d below)", len(fs.items)-end), w)))
	}
	if len(fs.items) == 0 {
		out = append(out, toolStyle.Render(Fit("  — no match —", w)))
	}
	return out
}

func (m Model) focusCodeLines(fs *focusState, w, rows int) []string {
	n := len(fs.file.Lines)
	numW := len(fmt.Sprint(n))
	codeW := max(1, w-numW-5)
	r := fs.focusRange()
	hasSel := fs.anchor >= 0
	active := fs.pane == paneCode
	curNum := lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	var out []string
	for i := fs.top; i < fs.top+rows && i < n; i++ {
		inSel := hasSel && i+1 >= r.Start && i+1 <= r.End
		mark := "  "
		switch {
		case i == fs.cur && active:
			mark = cmdHiStyle.Render("▸ ")
		case i == fs.cur:
			mark = toolStyle.Render("› ")
		case inSel:
			mark = cmdHiStyle.Render("▌ ")
		}
		num := fmt.Sprintf("%*d", numW, i+1)
		if i == fs.cur || inSel {
			num = curNum.Render(num)
		} else {
			num = toolStyle.Render(num)
		}
		var code string
		if inSel {
			// Selected rows drop syntax colour: an inner reset would cut
			// the highlight background short.
			code = rowHiStyle.Render(Fit(fs.plain[i], codeW))
		} else {
			code = ansi.Truncate(fs.hl[i], codeW, "…") + "\x1b[0m"
		}
		out = append(out, mark+num+sepStyle.Render(" │ ")+code)
	}
	return out
}

// --- keys --------------------------------------------------------------------

func (m Model) updateFocusDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	if fs == nil {
		return m.dismissDialog(d)
	}
	if km.Type == tea.KeyCtrlC {
		return m.focusClose(d)
	}
	switch fs.stage {
	case focusAsk:
		return m.updateFocusAsk(km, d)
	case focusPick:
		return m.updateFocusPick(km, d)
	}
	// Keys shared by both panes.
	switch {
	case km.Type == tea.KeyTab:
		m.focusSwitchPane(fs)
		m.Refresh()
		return m, nil
	case km.Type == tea.KeyCtrlE:
		if fs.showTree && fs.file != nil {
			fs.showTree, fs.pane = false, paneCode
		} else {
			fs.showTree = true
		}
		m.focusScroll(fs)
		m.Refresh()
		return m, nil
	case runeKey(km, '/') || km.Type == tea.KeyCtrlP:
		fs.stage, fs.pcur = focusPick, 0
		m.focusRefreshPick(fs)
		m.Refresh()
		return m, nil
	case runeKey(km, 'q'):
		return m.focusClose(d)
	}
	if fs.pane == paneTree || fs.file == nil {
		return m.updateFocusTree(km, d)
	}
	return m.updateFocusCode(km, d)
}

// focusSwitchPane is Tab: tree ⇄ code (showing a hidden tree on the way).
func (m Model) focusSwitchPane(fs *focusState) {
	if fs.pane == paneTree {
		if fs.file != nil {
			fs.pane = paneCode
		}
		return
	}
	fs.showTree, fs.pane = true, paneTree
	m.focusScroll(fs)
}

func (m Model) focusClose(d *Dialog) (tea.Model, tea.Cmd) {
	m.focusLast = d.focus.remember()
	return m.dismissDialog(d)
}

func (m Model) updateFocusTree(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	page := m.focusBodyRows(fs)
	n := fs.treeNode()
	move := func(delta int) { fs.tcur = max(0, min(len(fs.trows)-1, fs.tcur+delta)) }
	switch {
	case km.Type == tea.KeyEsc:
		if fs.file != nil {
			fs.pane = paneCode
			break
		}
		return m.focusClose(d)
	case km.Type == tea.KeyUp || runeKey(km, 'k'):
		move(-1)
	case km.Type == tea.KeyDown || runeKey(km, 'j'):
		move(1)
	case km.Type == tea.KeyPgUp || km.Type == tea.KeyCtrlB:
		move(-page)
	case km.Type == tea.KeyPgDown || km.Type == tea.KeyCtrlF:
		move(page)
	case km.Type == tea.KeyCtrlU:
		move(-page / 2)
	case km.Type == tea.KeyCtrlD:
		move(page / 2)
	case km.Type == tea.KeyHome || runeKey(km, 'g'):
		fs.tcur = 0
	case km.Type == tea.KeyEnd || runeKey(km, 'G'):
		fs.tcur = len(fs.trows) - 1
	case n == nil:
	case km.Type == tea.KeyEnter || runeKey(km, 'o') || runeKey(km, 'l') || km.Type == tea.KeyRight:
		if n.Dir {
			if (runeKey(km, 'l') || km.Type == tea.KeyRight) && n.Open {
				move(1) // already open: step into it
			} else {
				fs.tree.Toggle(n)
			}
			break
		}
		m.focusOpenNode(fs, n, true)
	case km.Type == tea.KeySpace:
		if n.Dir {
			fs.tree.Toggle(n)
			break
		}
		m.focusOpenNode(fs, n, false) // preview: keys stay on the tree
	case runeKey(km, 'h') || km.Type == tea.KeyLeft:
		if n.Dir && n.Open {
			fs.tree.Collapse(n)
			break
		}
		if p := n.Parent(); p != nil && p != fs.tree.Root {
			fs.trows = fs.tree.Rows()
			fs.tcur = filetree.IndexOf(fs.trows, p)
		}
	case runeKey(km, 'R'):
		fs.tree = filetree.Reload(fs.tree)
		fs.trows = fs.tree.Rows()
		if i := filetree.IndexOf(fs.trows, fs.tree.Reveal(n.Path)); i >= 0 {
			fs.tcur = i
		}
	}
	fs.trows = fs.tree.Rows()
	m.focusScrollTree(fs)
	m.Refresh()
	return m, nil
}

// focusOpenNode opens a tree file; focus=false keeps the keys on the tree.
func (m *Model) focusOpenNode(fs *focusState, n *filetree.Node, focus bool) {
	if fs.file == nil || fs.file.Path != n.Path {
		if err := m.focusOpen(fs, n.Path); err != nil {
			m.focusHint(errStyle.Render(err.Error()))
			fs.pane = paneTree
			return
		}
	}
	if focus {
		fs.pane = paneCode
	} else {
		fs.pane = paneTree
	}
}

// focusHint shows a short message in the footer (a toast would be hidden
// behind the dialog) and returns the command that clears it.
func (m *Model) focusHint(msg string) tea.Cmd {
	m.copyGen++
	m.copyHint = msg
	gen := m.copyGen
	return tea.Tick(copyHintTTL, func(time.Time) tea.Msg { return clearCopyHintMsg{gen: gen} })
}

func (m Model) updateFocusPick(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	var cmd tea.Cmd
	switch km.Type {
	case tea.KeyEsc:
		fs.stage = focusCode
	case tea.KeyUp, tea.KeyCtrlP:
		if len(fs.items) > 0 {
			fs.pcur = (fs.pcur - 1 + len(fs.items)) % len(fs.items)
		}
	case tea.KeyDown, tea.KeyCtrlN:
		if len(fs.items) > 0 {
			fs.pcur = (fs.pcur + 1) % len(fs.items)
		}
	case tea.KeyEnter, tea.KeyTab:
		if len(fs.items) == 0 {
			break
		}
		it := fs.items[fs.pcur]
		if it.Dir || km.Type == tea.KeyTab {
			fs.query = newLineEdit(it.Desc)
			fs.pcur = 0
			m.focusRefreshPick(fs)
			break
		}
		if err := m.focusOpen(fs, it.Desc); err != nil {
			cmd = m.focusHint(errStyle.Render(err.Error()))
		}
	default:
		before := fs.query.value()
		fs.query.handleKey(km, false)
		if fs.query.value() != before {
			fs.pcur = 0
			m.focusRefreshPick(fs)
		}
	}
	m.Refresh()
	return m, cmd
}

func (m Model) updateFocusCode(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	n := len(fs.file.Lines)
	page := m.focusBodyRows(fs)
	move := func(delta int) { fs.cur = max(0, min(n-1, fs.cur+delta)) }
	switch {
	case km.Type == tea.KeyEsc:
		if fs.anchor >= 0 {
			fs.anchor = -1
			break
		}
		return m.focusClose(d)
	case km.Type == tea.KeyUp || runeKey(km, 'k'):
		move(-1)
	case km.Type == tea.KeyDown || runeKey(km, 'j'):
		move(1)
	case km.Type == tea.KeyPgUp || km.Type == tea.KeyCtrlB:
		move(-page)
	case km.Type == tea.KeyPgDown || km.Type == tea.KeyCtrlF:
		move(page)
	case km.Type == tea.KeyCtrlU:
		move(-page / 2)
	case km.Type == tea.KeyCtrlD:
		move(page / 2)
	case km.Type == tea.KeyHome || runeKey(km, 'g'):
		fs.cur = 0
	case km.Type == tea.KeyEnd || runeKey(km, 'G'):
		fs.cur = n - 1
	case runeKey(km, 'v') || runeKey(km, 'V') || km.Type == tea.KeySpace:
		if fs.anchor >= 0 {
			fs.anchor = -1
		} else {
			fs.anchor = fs.cur
		}
	case runeKey(km, 'h') || km.Type == tea.KeyLeft || runeKey(km, 'o'):
		fs.showTree, fs.pane = true, paneTree
	case runeKey(km, 'y'):
		return m, m.focusCopy(snippet.Block(fs.file, fs.focusRange()))
	case runeKey(km, 'i'):
		return m.focusInsert(d)
	case km.Type == tea.KeyEnter || runeKey(km, 'a'):
		fs.stage = focusAsk
		if fs.ask == nil {
			fs.ask = newLineEdit("")
		}
		fs.preset = -1
	}
	m.focusScroll(fs)
	m.Refresh()
	return m, nil
}

func (m Model) updateFocusAsk(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	switch km.Type {
	case tea.KeyEsc:
		fs.stage = focusCode
		m.focusScroll(fs)
	case tea.KeyTab:
		v := fs.ask.value()
		if v == "" || (fs.preset >= 0 && v == snippet.Presets[fs.preset]) {
			fs.preset = (fs.preset + 1) % len(snippet.Presets)
			fs.ask = newLineEdit(snippet.Presets[fs.preset])
		}
	case tea.KeyEnter:
		if km.Alt {
			fs.ask.handleKey(km, true)
			break
		}
		ask := strings.TrimSpace(fs.ask.value())
		if ask == "" {
			break
		}
		return m.focusSend(d, snippet.Prompt(ask, fs.file, fs.focusRange()))
	default:
		fs.ask.handleKey(km, true)
	}
	m.Refresh()
	return m, nil
}

// focusSend closes the window and sends the prompt like Enter in the editor
// (steers a running turn, otherwise starts one).
func (m Model) focusSend(d *Dialog, text string) (tea.Model, tea.Cmd) {
	d.focus.ask = nil
	nm, closeCmd := m.focusClose(d)
	mm := nm.(Model)
	if mm.Pi == nil {
		mm.restoreQueuedToEditor([]string{text})
		mm.AddBlock(Block{Kind: "notice", Text: "pi is not connected — the focus prompt is in the editor", Err: true})
		mm.Refresh()
		return mm, closeCmd
	}
	var send tea.Cmd
	if mm.thinking {
		mm.unsend = nil
		send = mm.sendCmd(true, text, nil)
	} else {
		mm.unsend = &pendingUnsend{text: text, at: time.Now(), from: len(mm.blocks)}
		send = mm.sendCmd(false, text, nil)
	}
	return mm, tea.Batch(closeCmd, send)
}

// focusInsert drops the reference + excerpt into the main editor and
// closes, so several excerpts can go into one message.
func (m Model) focusInsert(d *Dialog) (tea.Model, tea.Cmd) {
	fs := d.focus
	text := snippet.Block(fs.file, fs.focusRange()) + "\n"
	if v := m.ta.Value(); v != "" && !strings.HasSuffix(v, "\n") {
		text = "\n" + text
	}
	nm, cmd := m.focusClose(d)
	mm := nm.(Model)
	mm.ta.CursorEnd()
	mm.insertAtCursor(text)
	mm.Refresh()
	return mm, cmd
}

// focusCopy copies text and reports in the window footer.
func (m *Model) focusCopy(text string) tea.Cmd {
	msg := okStyle.Render(fmt.Sprintf("✓ copied %d chars (reference + excerpt)", len([]rune(text))))
	if err := clipWrite(text); err != nil {
		msg = errStyle.Render("copy failed: " + err.Error())
	}
	cmd := m.focusHint(msg)
	m.Refresh()
	return cmd
}

// --- mouse -------------------------------------------------------------------

// updateFocusMouse: wheel scrolls the pane under the pointer; a click on
// the tree opens/toggles; a click on code moves the cursor, shift+click or
// a drag selects lines.
func (m Model) updateFocusMouse(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	fs := d.focus
	if fs == nil {
		return m, nil
	}
	l := m.focusLayoutOf(fs)
	row, x := mm.Y-l.y0, mm.X-l.x0
	inBody := row >= 0 && row < l.rows
	inTree := l.treeW > 0 && x >= 0 && x < l.treeW
	inCode := x >= l.codeX() && x < l.codeX()+l.codeW
	browsing := fs.stage == focusCode

	switch {
	case mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown:
		step := 3
		if mm.Button == tea.MouseButtonWheelUp {
			step = -3
		}
		switch {
		case fs.stage == focusPick && !inTree:
			if len(fs.items) > 0 {
				fs.pcur = max(0, min(len(fs.items)-1, fs.pcur+step/3))
			}
		case inTree:
			fs.tcur = max(0, min(len(fs.trows)-1, fs.tcur+step))
			m.focusScrollTree(fs)
		case fs.file != nil:
			fs.cur = max(0, min(len(fs.file.Lines)-1, fs.cur+step))
			m.focusScroll(fs)
		}
	case mm.Action == tea.MouseActionPress && mm.Button == tea.MouseButtonLeft && inBody && inTree && fs.stage != focusAsk:
		i := fs.ttop + row
		if i >= len(fs.trows) {
			break
		}
		fs.stage, fs.tcur = focusCode, i
		n := fs.trows[i].N
		if n.Dir {
			fs.pane = paneTree
			fs.tree.Toggle(n)
			fs.trows = fs.tree.Rows()
		} else {
			m.focusOpenNode(fs, n, true)
		}
		m.focusScrollTree(fs)
	case mm.Action == tea.MouseActionPress && mm.Button == tea.MouseButtonLeft && inBody && inCode && browsing && fs.file != nil:
		line := fs.top + row
		if line >= len(fs.file.Lines) {
			break
		}
		if mm.Shift {
			if fs.anchor < 0 {
				fs.anchor = fs.cur
			}
		} else {
			fs.anchor = -1
		}
		fs.cur, fs.pane = line, paneCode
		fs.dragging, fs.dragFrom = true, line
	case mm.Action == tea.MouseActionMotion && mm.Button == tea.MouseButtonLeft && fs.dragging && fs.file != nil:
		// Dragging past the top/bottom edge scrolls one line at a time.
		line := max(0, min(len(fs.file.Lines)-1, fs.top+max(-1, min(row, l.rows))))
		if line != fs.dragFrom {
			fs.anchor = fs.dragFrom
		}
		fs.cur = line
		m.focusScroll(fs)
	case mm.Action == tea.MouseActionRelease:
		fs.dragging = false
	default:
		return m, nil
	}
	m.Refresh()
	return m, nil
}
