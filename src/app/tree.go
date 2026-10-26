package app

import (
	"errors"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"pitago/src/components/sessiontree"
	"pitago/src/pirpc"
)

// /tree is pi's Session Tree (pi 1.4.0, interactive-mode.js showTreeSelector
// + components/tree-selector.js). The list itself — branch layout, folding,
// filters, search, row text — is ported in src/components/sessiontree; this
// file is the screen around it and the flow behind Enter:
//
//	tree ── Enter ──▶ "Summarize branch?" ── pick ──▶ navigateTree
//	  ▲                   │ Esc                         (pitago-tree
//	  └───────────────────┘                              extension)
//
// pi draws the selector in place of its editor, under the transcript. pitago
// keeps that shape: the transcript fills the screen above and the selector
// sits at the bottom.

// TreeMsg carries a fetched session tree to the selector.
type TreeMsg struct {
	Nodes  []pirpc.TreeNode
	Leaf   string
	Mode   string // filter mode to open with
	Search string // search text to open with (/tree <text>)
	Select string // entry to select first; "" selects the leaf
	Err    error
}

// treeNavMsg reports a finished navigateTree.
type treeNavMsg struct {
	nav       treeNav
	summarize bool
	res       pirpc.TreeNavResult
	err       error
	restored  []string // queued steer/follow-up text pulled back by the abort
}

// treeLabelMsg reports a finished label edit.
type treeLabelMsg struct{ err error }

// treeNav is the entry the user picked, carried through the summary steps.
type treeNav struct {
	target     string
	editorText string // what pi puts back in the editor once there
}

// treeState is the per-dialog state of the three tree screens.
type treeState struct {
	list    *sessiontree.List // "tree"
	label   *lineEdit         // "tree": pi's label input, shown instead of the list
	labelID string
	nav     treeNav   // "treeSummary", "treeCustom"
	edit    *lineEdit // "treeCustom": the instructions editor
}

// Dialog kinds of the tree flow.
const (
	treeKind        = "tree"
	treeSummaryKind = "treeSummary"
	treeCustomKind  = "treeCustom"
)

// treeSummaryChoices are pi's "Summarize branch?" options, verbatim.
var treeSummaryChoices = []string{"No summary", "Summarize", "Summarize with custom prompt"}

const treeCustomTitle = "Custom summarization instructions"

// OpenTree runs /tree. An argument naming a filter mode opens in that mode;
// any other argument is typed into the search.
func (m *Model) OpenTree(arg string) tea.Cmd {
	m.Status = "loading session tree…"
	m.Refresh()
	return m.loadTreeCmd(arg, "")
}

func (m *Model) loadTreeCmd(arg, selectID string) tea.Cmd {
	pi := m.Pi
	return func() tea.Msg {
		if pi == nil {
			return TreeMsg{Err: errors.New("pi is not running")}
		}
		nodes, leaf, err := pi.GetTree()
		if err != nil {
			return TreeMsg{Err: err}
		}
		mode := pirpc.PiString(pirpc.ReadPiSettings(), "treeFilterMode", "default")
		if !sessiontree.ValidMode(mode) {
			mode = "default"
		}
		search := ""
		if a := strings.TrimSpace(arg); a != "" {
			if sessiontree.ValidMode(strings.ToLower(a)) {
				mode = strings.ToLower(a)
			} else {
				search = a
			}
		}
		return TreeMsg{Nodes: nodes, Leaf: leaf, Mode: mode, Search: search, Select: selectID}
	}
}

func (m Model) openTreeMsg(msg TreeMsg) (tea.Model, tea.Cmd) {
	m.Status = "ready"
	switch {
	case msg.Err != nil:
		m.AddBlock(Block{Kind: "notice", Text: "tree error: " + msg.Err.Error(), Err: true})
	case len(msg.Nodes) == 0:
		m.AddBlock(Block{Kind: "notice", Text: "No entries in session"})
	default:
		list := sessiontree.New(msg.Nodes, msg.Leaf, treeWantRows(m.winH), msg.Select, msg.Mode)
		if msg.Search != "" {
			list.Type(msg.Search)
		}
		d := &Dialog{Kind: treeKind, Title: "Session Tree", tree: &treeState{list: list}}
		m.treeFit(d)
		m.Dialogs = append(m.Dialogs, d)
	}
	m.Refresh()
	return m, nil
}

// --- layout -----------------------------------------------------------------

// treeWantRows is pi's maxVisibleLines: half the terminal, at least 5.
func treeWantRows(winH int) int { return max(5, winH/2) }

// treeScreenH is the height every full-screen dialog is placed in.
func (m Model) treeScreenH() int { return max(m.winH-2, 1) }

func (m Model) treeScreenW() int { return max(m.winW, 20) }

// treeFit caps the list page so the whole selector fits the screen: pi's
// half-terminal page, or less on a short terminal. The chrome around the
// list is 9 lines plus the key help.
func (m Model) treeFit(d *Dialog) {
	if d == nil || d.tree == nil || d.tree.list == nil {
		return
	}
	room := m.treeScreenH() - 9 - len(treeHelpLines(m.treeScreenW()))
	d.tree.list.SetMaxVisible(min(treeWantRows(m.winH), room))
}

// treeHelpItems is pi's TREE_HELP_ITEMS with the default keys, formatted
// the way formatHelpKeys does (macOS spells alt "option").
func treeHelpItems() []string {
	branch := "ctrl+←/→"
	if runtime.GOOS == "darwin" {
		branch = "option+←/→"
	}
	return []string{"↑/↓ move", "←/→ page", branch + " branch", "ctrl+x copy",
		"shift+l label", "shift+t label time", "filters ctrl+d/t/u/l/a", "cycle ctrl+o/shift+ctrl+o"}
}

// treeHelpLines packs the help items into rows of width, pi's TreeHelp.
func treeHelpLines(width int) []string {
	width = max(1, width)
	const indent, sep = "  ", " · "
	var raw []string
	cur := ""
	for _, item := range treeHelpItems() {
		var cand string
		switch {
		case cur != "":
			cand = cur + sep + item
		case runewidth.StringWidth(indent+item) <= width:
			cand = indent + item
		default:
			cand = item
		}
		if cur == "" || runewidth.StringWidth(cand) <= width {
			cur = cand
			continue
		}
		raw = append(raw, strings.Split(ansi.Wrap(strings.TrimRight(cur, " "), width, ""), "\n")...)
		if runewidth.StringWidth(indent+item) <= width {
			cur = indent + item
		} else {
			cur = item
		}
	}
	if cur != "" {
		raw = append(raw, strings.Split(ansi.Wrap(strings.TrimRight(cur, " "), width, ""), "\n")...)
	}
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = toolStyle.Render(ln)
	}
	return out
}

func treeBorder(width int) string { return sepStyle.Render(strings.Repeat("─", max(1, width))) }

func treeFit(s string, width int) string { return ansi.Truncate(s, width, "") }

// treeHint is pi's keyHint: the key dim, the description muted.
func treeHint(key, desc string) string {
	return treeStyle(sessiontree.StyleDim).Render(key) + toolStyle.Render(" "+desc)
}

// treeStyle maps a pi theme token onto pitago's palette. pitago has no
// separate "dim" colour, so dim is the muted colour, faint.
func treeStyle(token string) lipgloss.Style {
	st := lipgloss.NewStyle()
	switch token {
	case sessiontree.StyleAccent:
		return st.Foreground(cAccent)
	case sessiontree.StyleSuccess:
		return st.Foreground(cGreen)
	case sessiontree.StyleMuted:
		return st.Foreground(cMuted)
	case sessiontree.StyleDim:
		return st.Foreground(cMuted).Faint(true)
	case sessiontree.StyleWarning:
		return st.Foreground(cYellow)
	case sessiontree.StyleError:
		return st.Foreground(cRed)
	case sessiontree.StyleBorderAccent:
		return st.Foreground(cCyan)
	case sessiontree.StyleCustomLabel:
		return st.Foreground(cPlan)
	}
	return st.Foreground(cText)
}

func renderTreeLine(ln sessiontree.Line) string {
	var b strings.Builder
	for _, s := range ln {
		st := treeStyle(s.Style)
		if s.Bold {
			st = st.Bold(true)
		}
		if s.Italic {
			st = st.Italic(true)
		}
		if s.Selected {
			st = st.Background(cHiBg)
		}
		b.WriteString(st.Render(s.Text))
	}
	return b.String()
}

// treeLines is pi's TreeSelectorComponent, top to bottom. listTop is the
// line the first list row sits on (the mouse hit test reads it).
func (m Model) treeLines(d *Dialog) (lines []string, listTop int) {
	width := m.treeScreenW()
	ts := d.tree
	border := treeBorder(width)
	title := " " + lipgloss.NewStyle().Bold(true).Foreground(cText).Render("  Session Tree")
	lines = append(lines, "", border, treeFit(title, width))
	lines = append(lines, treeHelpLines(width)...)
	search := "  " + toolStyle.Render("Type to search:")
	if q := ts.list.Query(); q != "" {
		search += " " + treeStyle(sessiontree.StyleAccent).Render(q)
	}
	lines = append(lines, treeFit(search, width), border, "")
	listTop = len(lines)
	if ts.label != nil {
		lines = append(lines,
			treeFit("  "+toolStyle.Render("Label (empty to remove):"), width),
			treeFit("  "+ts.label.renderLine("> ", width-2), width),
			treeFit("  "+treeHint("enter", "save")+"  "+treeHint("escape/ctrl+c", "cancel"), width))
	} else {
		for _, ln := range ts.list.Render(width) {
			lines = append(lines, renderTreeLine(ln))
		}
	}
	lines = append(lines, "", border)
	return lines, listTop
}

// chatTail is the bottom of the transcript as it is on screen, padded at
// the top to n lines: what pi shows above its selector.
func (m Model) chatTail(n int) []string {
	if n <= 0 {
		return nil
	}
	var lines []string
	if m.vp.Height > 0 {
		lines = strings.Split(m.vp.View(), "\n")
	}
	for len(lines) > 0 && strings.TrimSpace(stripANSI(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append(make([]string, n-len(lines)), lines...)
}

// renderBottomPanel pins a pi editor-area component to the bottom of the
// screen with the transcript above it. top is the screen row of panel[0].
func (m Model) renderBottomPanel(panel []string) (frame string, top int) {
	h := m.treeScreenH()
	if len(panel) > h {
		panel = panel[len(panel)-h:]
	}
	top = h - len(panel)
	rows := append(m.overlayTreeToasts(m.chatTail(top)), panel...)
	return lipgloss.Place(m.winW, h, lipgloss.Left, lipgloss.Top, strings.Join(rows, "\n")), top
}

// overlayTreeToasts floats the toast stack over the top right of the
// transcript above the selector. A dialog normally hides toasts, but pi
// reports tree actions (copy, label errors) in the chat above its selector,
// so they have to stay visible here.
func (m Model) overlayTreeToasts(chat []string) []string {
	box := m.renderToasts()
	if box == "" || len(chat) == 0 {
		return chat
	}
	tl := strings.Split(box, "\n")
	if len(tl) > len(chat) {
		tl = tl[:len(chat)]
	}
	leftW := max(0, m.treeScreenW()-lipgloss.Width(tl[0]))
	for i, ln := range tl {
		chat[i] = truncANSI(chat[i], leftW) + ln
	}
	return chat
}

func (m Model) renderTreeDialog(d *Dialog) string {
	m.treeFit(d)
	lines, _ := m.treeLines(d)
	frame, _ := m.renderBottomPanel(lines)
	return frame
}

// treeSummaryLines is pi's ExtensionSelectorComponent for "Summarize branch?".
func (m Model) treeSummaryLines(d *Dialog) []string {
	width := m.treeScreenW()
	accent := treeStyle(sessiontree.StyleAccent)
	border := treeBorder(width)
	lines := []string{border, "", treeFit(" "+accent.Bold(true).Render(d.Title), width), ""}
	for i, opt := range d.Options {
		row := "   " + lipgloss.NewStyle().Foreground(cText).Render(opt)
		if i == d.Cursor {
			row = " " + accent.Render("→ ") + accent.Render(opt)
		}
		lines = append(lines, treeFit(row, width))
	}
	hint := " " + treeHint("↑↓", "navigate") + "  " + treeHint("enter", "select") + "  " + treeHint("escape/ctrl+c", "cancel")
	return append(lines, "", treeFit(hint, width), "", border)
}

// treeSummaryTop is the panel line of the first option.
const treeSummaryTop = 4

func (m Model) renderTreeSummaryDialog(d *Dialog) string {
	frame, _ := m.renderBottomPanel(m.treeSummaryLines(d))
	return frame
}

// treeCustomLines is pi's ExtensionEditorComponent. pitago has no external
// editor and cannot see Shift+Enter, so the hint keeps only the keys that
// work here.
func (m Model) treeCustomLines(d *Dialog) []string {
	width := m.treeScreenW()
	border := treeBorder(width)
	lines := []string{border, "", treeFit(" "+treeStyle(sessiontree.StyleAccent).Render(d.Title), width), ""}
	lines = append(lines, border)
	lines = append(lines, d.tree.edit.renderBlock(width)...)
	lines = append(lines, border)
	hint := " " + treeHint("enter", "submit") + "  " + treeHint("ctrl+j", "newline") + "  " + treeHint("escape/ctrl+c", "cancel")
	return append(lines, "", treeFit(hint, width), "", border)
}

func (m Model) renderTreeCustomDialog(d *Dialog) string {
	frame, _ := m.renderBottomPanel(m.treeCustomLines(d))
	return frame
}

// --- keys --------------------------------------------------------------------

func runeKey(km tea.KeyMsg, r rune) bool {
	return km.Type == tea.KeyRunes && !km.Alt && !km.Paste && len(km.Runes) == 1 && km.Runes[0] == r
}

// pi's app.tree.foldOrUp / unfoldOrDown: alt+←/→ or ctrl+←/→, plus the
// legacy ESC b / ESC f a terminal sends for option+←/→.
func treeFoldKey(km tea.KeyMsg) bool {
	return (km.Type == tea.KeyLeft && km.Alt) || km.Type == tea.KeyCtrlLeft ||
		(km.Type == tea.KeyRunes && km.Alt && len(km.Runes) == 1 && km.Runes[0] == 'b')
}

func treeUnfoldKey(km tea.KeyMsg) bool {
	return (km.Type == tea.KeyRight && km.Alt) || km.Type == tea.KeyCtrlRight ||
		(km.Type == tea.KeyRunes && km.Alt && len(km.Runes) == 1 && km.Runes[0] == 'f')
}

// updateTreeDialog is pi's TreeList.handleInput, binding for binding and in
// the same order. Shift+Ctrl+O cannot be told apart from Ctrl+O by the
// terminal input pitago reads, so both cycle forward.
func (m Model) updateTreeDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	ts := d.tree
	if ts == nil || ts.list == nil {
		return m.dismissDialog(d)
	}
	m.treeFit(d)
	if ts.label != nil {
		return m.updateTreeLabel(km, d)
	}
	l := ts.list
	switch {
	case km.Type == tea.KeyUp:
		l.Up()
	case km.Type == tea.KeyDown:
		l.Down()
	case treeFoldKey(km):
		l.FoldOrUp()
	case treeUnfoldKey(km):
		l.UnfoldOrDown()
	case km.Type == tea.KeyLeft, km.Type == tea.KeyCtrlB, km.Type == tea.KeyPgUp:
		l.PageUp()
	case km.Type == tea.KeyRight, km.Type == tea.KeyCtrlF, km.Type == tea.KeyPgDown:
		l.PageDown()
	case km.Type == tea.KeyEnter && !km.Alt:
		return m.treeConfirm(d)
	case km.Type == tea.KeyCtrlX:
		m.treeCopy(d)
	case km.Type == tea.KeyEsc, km.Type == tea.KeyCtrlC:
		if !l.Cancel() {
			return m.dismissDialog(d)
		}
	case km.Type == tea.KeyCtrlD:
		l.SetMode("default")
	case km.Type == tea.KeyCtrlT:
		l.ToggleMode("no-tools")
	case km.Type == tea.KeyCtrlU:
		l.ToggleMode("user-only")
	case km.Type == tea.KeyCtrlL:
		l.ToggleMode("labeled-only")
	case km.Type == tea.KeyCtrlA:
		l.ToggleMode("all")
	case km.Type == tea.KeyCtrlO:
		l.CycleMode(1)
	case km.Type == tea.KeyBackspace, km.Type == tea.KeyCtrlH:
		l.Backspace()
	case runeKey(km, 'L'):
		if n := l.Selected(); n != nil {
			ts.label = newLineEdit(n.Label)
			ts.labelID = n.ID()
		}
	case runeKey(km, 'T'):
		l.ToggleLabelTimestamps()
	case km.Type == tea.KeyRunes && !km.Alt:
		l.Type(string(km.Runes))
	case km.Type == tea.KeySpace:
		l.Type(" ")
	}
	m.Refresh()
	return m, nil
}

// updateTreeLabel is pi's LabelInput: Enter saves (blank clears), Esc
// returns to the list.
func (m Model) updateTreeLabel(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	ts := d.tree
	switch km.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(ts.label.value())
		id := ts.labelID
		ts.label, ts.labelID = nil, ""
		ts.list.UpdateNodeLabel(id, value, "")
		m.Refresh()
		pi := m.Pi
		return m, func() tea.Msg {
			if pi == nil {
				return treeLabelMsg{err: errors.New("pi is not running")}
			}
			return treeLabelMsg{err: pi.SetTreeLabel(id, value)}
		}
	case tea.KeyEsc, tea.KeyCtrlC:
		ts.label, ts.labelID = nil, ""
	default:
		ts.label.handleKey(km, false)
	}
	m.Refresh()
	return m, nil
}

// treeStatus is pi's showStatus/showError for the tree: a chat line, which
// stays readable above the selector.
func (m *Model) treeStatus(text string, isErr bool) {
	m.AddBlock(Block{Kind: "notice", Text: text, Err: isErr})
	m.Refresh()
}

// treeCopy is pi's onCopy: the selected entry's full text.
func (m *Model) treeCopy(d *Dialog) {
	text := sessiontree.CopyText(d.tree.list.Selected())
	if text == "" {
		m.treeStatus("Selected entry has no text to copy", true)
		return
	}
	if err := clipWrite(text); err != nil {
		m.treeStatus(err.Error(), true)
		return
	}
	m.treeStatus("Copied selected message to clipboard", false)
}

// popDialog removes d from the stack.
func (m *Model) popDialog(d *Dialog) {
	for i, x := range m.Dialogs {
		if x == d {
			m.Dialogs = append(m.Dialogs[:i:i], m.Dialogs[i+1:]...)
			return
		}
	}
}

// pushDialog makes d the active dialog (Dialogs[0] is the one on screen).
func (m *Model) pushDialog(d *Dialog) {
	m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
}

// treeConfirm is pi's onSelect: the current leaf is a no-op, anything else
// asks "Summarize branch?" unless branchSummary.skipPrompt is set.
func (m Model) treeConfirm(d *Dialog) (tea.Model, tea.Cmd) {
	n := d.tree.list.Selected()
	if n == nil {
		return m, nil
	}
	leaf := d.tree.list.LeafID()
	m.popDialog(d)
	if n.ID() == leaf {
		m.drainQueuedDialogs()
		m.treeStatus("Already at this point", false)
		return m, nil
	}
	nav := treeNav{target: n.ID(), editorText: sessiontree.EditorText(n)}
	if pirpc.PiBool(pirpc.ReadPiSettings(), "branchSummary.skipPrompt", false) {
		return m.startTreeNav(nav, false, nil)
	}
	m.openTreeSummary(nav)
	return m, nil
}

func (m *Model) openTreeSummary(nav treeNav) {
	m.pushDialog(&Dialog{Kind: treeSummaryKind, Title: "Summarize branch?",
		Options: append([]string(nil), treeSummaryChoices...), tree: &treeState{nav: nav}})
	m.Refresh()
}

// reopenTree is pi's showTreeSelector(entryId) after an Esc: a fresh tree
// with the same entry selected.
func (m Model) reopenTree(target string) (tea.Model, tea.Cmd) {
	m.Status = "loading session tree…"
	m.Refresh()
	return m, m.loadTreeCmd("", target)
}

// updateTreeSummary is pi's ExtensionSelectorComponent: ↑↓ (or k/j) clamp,
// Enter picks, Esc goes back to the tree.
func (m Model) updateTreeSummary(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	switch {
	case km.Type == tea.KeyUp || runeKey(km, 'k'):
		d.Cursor = max(0, d.Cursor-1)
	case km.Type == tea.KeyDown || runeKey(km, 'j'):
		d.Cursor = min(len(d.Options)-1, d.Cursor+1)
	case (km.Type == tea.KeyEnter && !km.Alt) || km.Type == tea.KeyCtrlJ:
		return m.treeSummaryPick(d)
	case km.Type == tea.KeyEsc || km.Type == tea.KeyCtrlC:
		m.popDialog(d)
		return m.reopenTree(d.tree.nav.target)
	}
	m.Refresh()
	return m, nil
}

func (m Model) treeSummaryPick(d *Dialog) (tea.Model, tea.Cmd) {
	nav := d.tree.nav
	switch d.Cursor {
	case 0:
		return m.startTreeNav(nav, false, nil)
	case 1:
		return m.startTreeNav(nav, true, nil)
	}
	m.popDialog(d)
	m.pushDialog(&Dialog{Kind: treeCustomKind, Title: treeCustomTitle,
		tree: &treeState{nav: nav, edit: newLineEdit("")}})
	m.Refresh()
	return m, nil
}

// updateTreeCustom edits the custom instructions: Enter submits, Esc goes
// back to a fresh "Summarize branch?" (pi loops back to the selector).
func (m Model) updateTreeCustom(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	switch {
	case km.Type == tea.KeyEsc || km.Type == tea.KeyCtrlC:
		m.popDialog(d)
		m.openTreeSummary(d.tree.nav)
		return m, nil
	case km.Type == tea.KeyEnter && !km.Alt:
		text := d.tree.edit.value()
		return m.startTreeNav(d.tree.nav, true, &text)
	}
	d.tree.edit.handleKey(km, true)
	m.Refresh()
	return m, nil
}

// dropTreeDialogs closes every screen of the tree flow.
func (m *Model) dropTreeDialogs() {
	kept := m.Dialogs[:0:0]
	for _, d := range m.Dialogs {
		if d.Kind != treeKind && d.Kind != treeSummaryKind && d.Kind != treeCustomKind {
			kept = append(kept, d)
		}
	}
	m.Dialogs = kept
}

// startTreeNav commits to the navigation. Like pi it first stops a running
// turn and pulls the queued messages back into the editor.
func (m Model) startTreeNav(nav treeNav, summarize bool, custom *string) (tea.Model, tea.Cmd) {
	m.dropTreeDialogs()
	m.drainQueuedDialogs()
	streaming := m.thinking
	m.branchSummarizing = summarize
	m.summaryAborted = false
	m.Status = "navigating session tree…"
	m.Refresh()
	pi := m.Pi
	return m, func() tea.Msg {
		msg := treeNavMsg{nav: nav, summarize: summarize}
		if pi == nil {
			msg.err = errors.New("pi is not running")
			return msg
		}
		if streaming {
			steer, follow, _ := pi.ClearQueue()
			msg.restored = append(steer, follow...)
			_, _ = pi.Abort()
		}
		msg.res, msg.err = pi.NavigateTree(nav.target, pirpc.TreeNavOptions{Summarize: summarize, CustomInstructions: custom})
		return msg
	}
}

// handleTreeNav is the tail of pi's onSelect: report, then rebuild the
// transcript from the new branch and hand the picked message back to an
// empty editor.
func (m Model) handleTreeNav(msg treeNavMsg) (tea.Model, tea.Cmd) {
	aborted := m.summaryAborted
	m.branchSummarizing, m.summaryAborted = false, false
	m.Status = "ready"
	if len(msg.restored) > 0 {
		m.restoreQueuedToEditor(msg.restored)
	}
	if msg.err != nil {
		m.treeStatus(msg.err.Error(), true)
		return m, nil
	}
	if msg.res.Cancelled {
		if msg.summarize && aborted {
			m.treeStatus("Branch summarization cancelled", false)
			return m.reopenTree(msg.nav.target)
		}
		m.treeStatus("Navigation cancelled", false)
		return m, nil
	}
	if msg.nav.editorText != "" && strings.TrimSpace(m.ta.Value()) == "" {
		m.ta.SetValue(msg.nav.editorText)
		m.histIdx = -1
	}
	notice := "Navigated to selected point"
	if msg.res.Unknown {
		notice = ""
	}
	m.Refresh()
	return m, tea.Sequence(m.fetchAll(), func() tea.Msg {
		return SettingsRefreshMsg{Notice: notice}
	})
}

// abortBranchSummaryCmd is pi's Esc while a branch summary runs
// (session.abortBranchSummary). pi's RPC `abort` reaches it through
// session.abort(); navigateTree then answers cancelled and the tree reopens.
func (m *Model) abortBranchSummaryCmd() tea.Cmd {
	m.summaryAborted = true
	m.escArm = time.Time{}
	m.Status = "cancelling branch summary…"
	m.Refresh()
	pi := m.Pi
	return func() tea.Msg {
		if pi == nil {
			return PiOpMsg{Op: "abort-branch-summary"}
		}
		_, err := pi.Abort()
		return PiOpMsg{Op: "abort-branch-summary", Err: err}
	}
}

// branchSummaryLabel is pi's BranchSummaryStatusIndicator text.
const branchSummaryLabel = "Summarizing branch... (escape to cancel)"

// doubleEscapeWindow is pi's double-Esc window (interactive-mode.js onEscape).
const doubleEscapeWindow = 500 * time.Millisecond

// doubleEscape is pi's Esc on an idle, empty editor: a second press within
// 500ms runs doubleEscapeAction ("tree" by default, or "fork"). It reports
// false when the setting is "none" or the editor holds text.
func (m Model) doubleEscape() (tea.Model, tea.Cmd, bool) {
	if strings.TrimSpace(m.ta.Value()) != "" {
		return m, nil, false
	}
	action := pirpc.PiString(pirpc.ReadPiSettings(), "doubleEscapeAction", "tree")
	if action != "tree" && action != "fork" {
		return m, nil, false
	}
	now := time.Now()
	if !m.lastEsc.IsZero() && now.Sub(m.lastEsc) < doubleEscapeWindow {
		m.lastEsc = time.Time{}
		return m, m.RunBuiltin(action, ""), true
	}
	m.lastEsc = now
	return m, nil, true
}

// --- mouse -------------------------------------------------------------------

// updateTreeWheel moves the selection like ↑↓ (pi has no mouse; this is
// pitago's).
func (m Model) updateTreeWheel(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	if d.tree == nil || d.tree.list == nil || d.tree.label != nil {
		return m, nil
	}
	if mm.Button == tea.MouseButtonWheelDown {
		d.tree.list.Down()
	} else {
		d.tree.list.Up()
	}
	m.Refresh()
	return m, nil
}

// clickTreeDialog selects the clicked row; a click on the selected row is
// Enter. Clicks elsewhere are swallowed.
func (m Model) clickTreeDialog(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if mm.Action != tea.MouseActionPress || mm.Button != tea.MouseButtonLeft {
		return m, nil, false
	}
	if d.tree == nil || d.tree.list == nil || d.tree.label != nil {
		return m, nil, true
	}
	m.treeFit(d)
	lines, listTop := m.treeLines(d)
	_, top := m.renderBottomPanel(lines)
	l := d.tree.list
	start, end := l.VisibleRange()
	row := mm.Y - top - listTop
	if row < 0 || row >= end-start {
		return m, nil, true
	}
	if i := start + row; i != l.SelectedIndex() {
		l.Select(i)
		m.Refresh()
		return m, nil, true
	}
	nm, cmd := m.treeConfirm(d)
	return nm, cmd, true
}

// clickTreeSummary selects the clicked option; a click on the selected one
// picks it.
func (m Model) clickTreeSummary(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if mm.Action != tea.MouseActionPress || mm.Button != tea.MouseButtonLeft {
		return m, nil, false
	}
	_, top := m.renderBottomPanel(m.treeSummaryLines(d))
	i := mm.Y - top - treeSummaryTop
	if i < 0 || i >= len(d.Options) {
		return m, nil, true
	}
	if i != d.Cursor {
		d.Cursor = i
		m.Refresh()
		return m, nil, true
	}
	nm, cmd := m.treeSummaryPick(d)
	return nm, cmd, true
}

// updateListWheel moves a plain row list's selection one row, wrapping.
func (m Model) updateListWheel(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	n := len(d.FIdx)
	if n == 0 {
		return m, nil
	}
	if mm.Button == tea.MouseButtonWheelDown {
		d.Cursor = (d.Cursor + 1) % n
	} else {
		d.Cursor = (d.Cursor - 1 + n) % n
	}
	m.Refresh()
	return m, nil
}

// --- text input ----------------------------------------------------------------

// lineEdit is the small editor behind the label input and the custom
// instructions box.
type lineEdit struct {
	buf []rune
	cur int
}

func newLineEdit(s string) *lineEdit {
	r := []rune(s)
	return &lineEdit{buf: r, cur: len(r)}
}

func (e *lineEdit) value() string { return string(e.buf) }

func (e *lineEdit) insert(s string) {
	r := []rune(s)
	e.buf = append(e.buf[:e.cur], append(r, e.buf[e.cur:]...)...)
	e.cur += len(r)
}

func (e *lineEdit) handleKey(km tea.KeyMsg, multiline bool) {
	switch km.Type {
	case tea.KeyRunes:
		if km.Alt {
			return
		}
		s := string(km.Runes)
		if !multiline {
			s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
		}
		e.insert(s)
	case tea.KeySpace:
		e.insert(" ")
	case tea.KeyBackspace, tea.KeyCtrlH:
		if km.Alt {
			e.deleteWord()
		} else if e.cur > 0 {
			e.buf = append(e.buf[:e.cur-1], e.buf[e.cur:]...)
			e.cur--
		}
	case tea.KeyCtrlW:
		e.deleteWord()
	case tea.KeyDelete, tea.KeyCtrlD:
		if e.cur < len(e.buf) {
			e.buf = append(e.buf[:e.cur], e.buf[e.cur+1:]...)
		}
	case tea.KeyLeft, tea.KeyCtrlB:
		e.cur = max(0, e.cur-1)
	case tea.KeyRight, tea.KeyCtrlF:
		e.cur = min(len(e.buf), e.cur+1)
	case tea.KeyHome, tea.KeyCtrlA:
		e.cur = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		e.cur = len(e.buf)
	case tea.KeyCtrlU:
		e.buf, e.cur = e.buf[e.cur:], 0
	case tea.KeyCtrlK:
		e.buf = e.buf[:e.cur]
	case tea.KeyCtrlJ:
		if multiline {
			e.insert("\n")
		}
	case tea.KeyEnter:
		if multiline && km.Alt {
			e.insert("\n")
		}
	}
}

func (e *lineEdit) deleteWord() {
	i := e.cur
	for i > 0 && e.buf[i-1] == ' ' {
		i--
	}
	for i > 0 && e.buf[i-1] != ' ' {
		i--
	}
	e.buf = append(e.buf[:i], e.buf[e.cur:]...)
	e.cur = i
}

var treeCursorStyle = lipgloss.NewStyle().Reverse(true)

// renderLine is pi's Input: a prompt, then the text scrolled so the
// (reverse-video) cursor stays in view.
func (e *lineEdit) renderLine(prompt string, width int) string {
	avail := width - runewidth.StringWidth(prompt)
	if avail <= 0 {
		return treeFit(prompt, width)
	}
	start := 0
	for runewidth.StringWidth(string(e.buf[start:e.cur]))+1 > avail && start < e.cur {
		start++
	}
	var b strings.Builder
	used := 0
	for i := start; i <= len(e.buf); i++ {
		ch := " "
		if i < len(e.buf) {
			ch = string(e.buf[i])
		}
		w := runewidth.StringWidth(ch)
		if used+w > avail {
			break
		}
		if i == e.cur {
			b.WriteString(treeCursorStyle.Render(ch))
		} else if i < len(e.buf) {
			b.WriteString(ch)
		}
		used += w
	}
	return prompt + b.String()
}

// renderBlock draws the multi-line buffer wrapped to width, cursor shown.
func (e *lineEdit) renderBlock(width int) []string {
	var b strings.Builder
	for i := 0; i <= len(e.buf); i++ {
		if i == e.cur {
			ch := " "
			if i < len(e.buf) && e.buf[i] != '\n' {
				ch = string(e.buf[i])
				b.WriteString(treeCursorStyle.Render(ch))
				continue
			}
			b.WriteString(treeCursorStyle.Render(ch))
		}
		if i < len(e.buf) {
			b.WriteRune(e.buf[i])
		}
	}
	var out []string
	for _, ln := range strings.Split(b.String(), "\n") {
		out = append(out, strings.Split(ansi.Wrap(ln, max(1, width), ""), "\n")...)
	}
	return out
}
