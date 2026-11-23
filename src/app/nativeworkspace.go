package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"pitago/src/tui/ev"
	"pitago/src/tui/viewport"
)

// NativeWorkspace supplies content and the same geometry the reducer uses for
// mouse hits. The runtime lays out and paints the individual primitives.
func (m Model) NativeWorkspace() ev.WorkspaceView {
	if !m.ready {
		return ev.WorkspaceView{MainWidth: m.winW, Header: "starting…"}
	}
	var team *Dialog
	if len(m.Dialogs) > 0 && isDocumentDialog(m.Dialogs[0]) {
		d := m.Dialogs[0]
		if d.Kind != "team" {
			return m.nativeDocument(d)
		}
		team = d
	}
	layout := m.workspaceLayout()
	v := ev.WorkspaceView{MainWidth: m.mainW(), Header: m.headerText(),
		Chat: m.transcriptView(layout.chat), Above: layout.above, Below: layout.below}
	if m.showSide() {
		v.SidebarWidth = m.sideW()
		v.Sidebar = m.sideVp.VisibleLines()
		if m.hoverOn {
			v.Sidebar = strings.Split(m.applyHover(strings.Join(v.Sidebar, "\n")), "\n")
		}
		if m.quitArmed() {
			// Anchor the quit hint at the bottom, even if the sidebar was short.
			rows := make([]string, m.sideContentH())
			copy(rows, v.Sidebar)
			rows[len(rows)-1] = m.quitArmFooter()
			v.Sidebar = rows
		}
	}
	border, title, left, right := m.inputChrome()
	v.Editor = ev.EditorView{Title: title, Border: string(border), Hints: left, Status: right,
		Rows: m.ta.VisibleRows(), PromptColor: string(cInput)}
	if !m.ta.Focused() {
		v.Editor.PromptColor = string(cMuted)
	}
	if m.chipH() > 0 {
		v.Editor.ChipsVisible = true
		v.Editor.Chips = m.chipRow(max(10, m.mainW()-4))
	}
	v.Toasts = m.nativeToasts()
	if team != nil {
		v.Document = m.nativeTeam(team)
	}
	return v
}

func (m Model) nativeToasts() []ev.ToastView {
	var rows []ev.ToastView
	for _, t := range m.toasts {
		rows = append(rows, ev.ToastView{Text: t.Text, Error: t.Err, Remaining: toastRemain(t)})
	}
	return rows
}

func (m Model) transcriptView(vp viewport.Model) ev.TranscriptView {
	v := ev.TranscriptView{Rows: vp.VisibleLines(), Height: vp.Height, Offset: vp.YOffset}
	if idx := m.hoverBlock - 1; idx >= 0 && idx < len(m.blockRows) {
		v.HoverStart, v.HoverEnd = m.blockRows[idx], vp.TotalLineCount()
		if idx+1 < len(m.blockRows) {
			v.HoverEnd = m.blockRows[idx+1]
		}
	}
	if m.sel.Active {
		a, b := normalizePoints(m.sel.Anchor, m.sel.Focus)
		sel := &ev.TextSelection{Start: ev.TextPoint{Line: a.Line, Column: a.Col}, End: ev.TextPoint{Line: b.Line, Column: b.Col}, Gutters: make([]int, len(v.Rows))}
		for i := range v.Rows {
			sel.Gutters[i] = m.gutterFor(v.Offset + i)
		}
		v.Selection = sel
	}
	if c, ok := m.jumpLatestChip(vp); ok {
		v.JumpLabel, v.JumpX, v.JumpY = stripANSI(c.text), c.start, c.row
	}
	return v
}

type workspaceLayout struct {
	chat         viewport.Model
	above, below []ev.PanelView
}

func (m Model) workspaceLayout() workspaceLayout {
	inline := m.isInlineUI()
	team, framed := m.renderTeamPanelLines()
	teamView := ev.PanelView{Text: team, Border: framed}
	if framed {
		teamView.PaddingX = 1
		teamView.Width = m.teamPanelPaintedWidth()
	}
	tasks := m.renderTaskWidget()
	budget := m.extPanelBudget(teamView.Height())
	extAbove := m.renderExtWidgets("aboveEditor", budget)
	extBelow := m.renderExtWidgets("belowEditor", max(0, budget-panelHeight(extAbove)))
	out := workspaceLayout{chat: m.chatGeometryByHeight(inline, teamView.Height(), panelHeight(tasks)+panelHeight(extAbove)+panelHeight(extBelow))}
	add := func(dst *[]ev.PanelView, panel ev.PanelView) {
		if panel.Text != "" {
			*dst = append(*dst, panel)
		}
	}
	box := func(text string, width int) ev.PanelView {
		return ev.PanelView{Text: text, Width: width + 2, PaddingX: 1, Border: true}
	}
	if m.TeamWidgetPlacement != "belowEditor" {
		add(&out.above, teamView)
	}
	add(&out.above, ev.PanelView{Text: tasks})
	add(&out.above, ev.PanelView{Text: extAbove})
	if inline && !m.nativePickerOpen() {
		add(&out.above, box(m.uiDialogBody()))
	} else if !inline {
		if m.cmdOpen {
			add(&out.above, box(m.cmdPopupBody()))
		}
		if m.atOpen {
			add(&out.above, box(m.atPopupBody(), m.mainW()-2))
		}
	}
	if m.inputOpen() {
		add(&out.above, m.nativeInputPopup())
	}
	add(&out.below, ev.PanelView{Text: extBelow})
	if m.TeamWidgetPlacement == "belowEditor" {
		add(&out.below, teamView)
	}
	return out
}

func (m Model) nativeInputPopup() ev.PanelView {
	body, w := m.inputBoxBody()
	w = min(w+2, m.mainW())
	return ev.PanelView{Text: ansi.Wrap(body, max(1, w-8), ""), Width: w, Border: true, PaddingX: 3, PaddingY: 1}
}
