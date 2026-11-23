package tviewrt

import (
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cellwidth"
	"pitago/src/components/format"
	"pitago/src/tui/ev"
	"pitago/src/tui/widgets"
)

// settingsPanel renders the model's live snapshot using tview primitives.
// The frame retains keyboard focus so a nested dialog can replace the panel
// without stranding focus on the previous list or filter field.
type settingsPanel struct {
	r                                             *Runtime
	layout, body, footer                          *tview.Flex
	filter                                        *tview.InputField
	message, details, help                        *tview.TextView
	title                                         *tview.TextView
	header                                        *tview.Flex
	hdr                                           [3]*tview.TextView
	pipes                                         [2]*vline
	apply, close                                  *tview.Button
	lists                                         [3]*tview.List
	listWidths                                    [3]int
	view                                          ev.SettingsView
	detailsText                                   string
	lastClick                                     ev.SettingsPointerMsg
	hasClick, editing, active                     bool
	filterVisible, detailsVisible, buttonsVisible bool
}

// vline draws the vertical rule between two bare columns.
type vline struct {
	*tview.Box
	style tcell.Style
	bg    tcell.Color
}

func (v *vline) Draw(scr tcell.Screen) {
	v.Box.DrawForSubclass(scr, v)
	x, y, w, h := v.GetInnerRect()
	if w < 1 {
		return
	}
	_, bg, _ := v.style.Decompose()
	if v.bg != tcell.ColorDefault {
		bg = v.bg
	}
	fill := v.style.Background(bg)
	for i := 0; i < h; i++ {
		for j := 1; j < w; j++ {
			scr.SetContent(x+j, y+i, ' ', nil, fill)
		}
	}
	for i := 0; i < h; i++ {
		scr.SetContent(x, y+i, tview.Borders.Vertical, nil, fill)
	}
}

func newSettingsPanel(r *Runtime) *settingsPanel {
	p := &settingsPanel{r: r}
	p.layout = tview.NewFlex().SetDirection(tview.FlexRow)
	p.layout.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	p.body, p.footer = tview.NewFlex(), tview.NewFlex()
	p.message, p.details, p.help = tview.NewTextView(), tview.NewTextView(), tview.NewTextView()
	p.details.SetWordWrap(true)
	p.help.SetWrap(false)
	p.apply = tview.NewButton("Apply").SetSelectedFunc(func() {
		if p.view.Edit {
			p.key(ev.KeyEnter)
			return
		}
		col := max(p.view.Focus, ev.SettingsValues)
		if int(col) >= len(p.view.Panes) {
			return
		}
		if i := p.view.Panes[col].Selected; i >= 0 {
			p.endEdit()
			p.r.dispatch(ev.SettingsPointerMsg{Column: col, Index: i, Activate: true})
		}
	})
	p.close = tview.NewButton("Close").SetSelectedFunc(func() { p.key(ev.KeyEsc) })
	p.footer.AddItem(p.help, 0, 1, false).AddItem(p.apply, 8, 0, false).AddItem(p.close, 8, 0, false)
	p.filter = tview.NewInputField().SetPlaceholder("type to search")
	p.filter.SetChangedFunc(func(text string) {
		if text != p.view.Filter {
			p.r.dispatch(ev.SettingsFilterMsg(text))
		}
	})
	p.title = tview.NewTextView().SetWrap(false)
	p.header = tview.NewFlex()
	for i := range p.hdr {
		p.hdr[i] = tview.NewTextView().SetWrap(false)
	}
	for i := range p.pipes {
		p.pipes[i] = &vline{Box: tview.NewBox()}
	}
	for i := range p.lists {
		l := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true).SetUseStyleTags(false, false)
		l.SetBorder(true)
		p.lists[i] = l
	}
	return p
}

func (p *settingsPanel) draw(scr tcell.Screen, x, y, w, h int, v ev.SettingsView, st widgets.MenuBarStyles) {
	old := p.view
	if !p.active || old.Kind != v.Kind {
		p.endEdit()
		p.hasClick = false
		for _, l := range p.lists {
			l.Clear()
		}
		old = ev.SettingsView{}
	}
	p.view, p.active = v, true
	if p.filter.GetText() != v.Filter {
		p.filter.SetText(v.Filter)
	}
	fg, bg, _ := st.Normal.Decompose()
	border, _, _ := st.Menu.Border.Decompose()
	accent, _, _ := st.Hover.Decompose()
	_, hiBg, _ := st.Open.Decompose()
	p.layout.SetTitle(tview.Escape(v.Title)).SetBorderColor(border).SetTitleColor(accent).SetBackgroundColor(bg)
	p.filter.SetLabel(v.FilterLabel).SetLabelStyle(st.Menu.Shortcut).SetFieldStyle(st.Normal).SetPlaceholderStyle(st.Menu.Shortcut)
	p.filter.SetPlaceholder(format.OrDefault(v.Placeholder, "type to search"))
	if v.Bare {
		// The painted picker showed a block caret on the filter line; the
		// field itself never takes focus, so the placeholder stands in for it.
		p.filter.SetPlaceholder("▌")
	}
	mask := rune(0)
	if v.Mask {
		mask = '•'
	}
	p.filter.SetMaskCharacter(mask).SetBackgroundColor(bg)
	p.message.SetWordWrap(true).SetTextStyle(st.Menu.Shortcut).SetText(v.Message).SetBackgroundColor(bg)
	p.details.SetTextStyle(st.Normal).SetBackgroundColor(bg)
	for _, pp := range p.pipes {
		pp.bg = bg
	}
	p.details.SetScrollable(v.ScrollDetails)
	help := format.OrDefault(v.Help, "↑↓ move · Tab switch · Enter apply · Esc close")
	p.apply.SetLabel(format.OrDefault(v.ApplyLabel, "Apply"))
	if v.Status != "" && v.Status != "ready" {
		help = v.Status + " · " + help
	}
	p.help.SetTextStyle(st.Menu.Shortcut).SetText(help).SetBackgroundColor(bg)
	for _, b := range []*tview.Button{p.apply, p.close} {
		b.SetStyle(st.Open).SetActivatedStyle(st.Hover)
	}

	p.layout.Clear()
	p.body.Clear().SetBackgroundColor(bg)
	if v.Bare {
		// The painted pickers capped their frame and centered it; keep that
		// so a wide terminal doesn't stretch the model column.
		if frameW := bareFrameCap(v.Kind); w > frameW {
			x += (w - frameW) / 2
			w = frameW
		}
	}
	padX := 1
	if v.Bare {
		padX = 2
	}
	p.layout.SetBorderPadding(0, 0, padX, padX)
	panelW := max(0, w-2-2*padX) // outer border and padding
	if v.Bare {
		// Title inside the frame, not on the border.
		p.layout.SetTitle("")
		p.title.SetTextStyle(st.Normal.Bold(true)).SetText(v.Title).SetBackgroundColor(bg)
		p.layout.AddItem(p.title, 1, 0, false)
	}
	if h >= 8 && v.Message != "" {
		n := min(max(1, h/4), len(tview.WordWrap(tview.Escape(v.Message), max(1, panelW))))
		if n > 0 {
			p.layout.AddItem(p.message, n, 0, false)
		}
	}
	p.filterVisible = v.FilterLabel != "" && h >= 5
	if p.filterVisible {
		p.layout.AddItem(p.filter, 1, 0, false)
	}
	detailMinWidth := 86
	if v.Kind == "model" {
		detailMinWidth = 72
	}
	wideDetails := v.WideDetails && w >= detailMinWidth && h >= 10 && v.Details != ""
	detailW := 0
	if wideDetails {
		if v.Bare {
			detailW = 40
		} else {
			detailW = min(44, panelW/3)
		}
	}
	var widths [3]int
	if v.Bare {
		widths, detailW = bareWidths(panelW, len(v.Panes), detailW, bareFirstCap(v.Kind, panelW))
		wideDetails = detailW > 0
		if len(v.Panes) > 0 {
			p.layout.AddItem(nil, 1, 0, false) // air under the filter
			p.drawBareHeader(v, st.Normal.Bold(true), st.Menu.Border, widths, detailW, bg)
			p.layout.AddItem(p.header, 1, 0, false)
		}
	} else {
		widths = settingsWidths(v.Panes, panelW-detailW, v.Kind == "model")
	}
	if len(v.Panes) > 0 {
		p.layout.AddItem(p.body, 0, 1, false)
	}
	for i, pane := range v.Panes {
		l := p.lists[i]
		if pane.Title == "" {
			continue
		}
		// Every row of a bare pane carries a two-cell gutter: "▸ " on the
		// cursor row, blanks elsewhere, so the labels stay aligned.
		chrome, prefix := 2, 0
		if v.Bare {
			chrome, prefix = 0, 2
		}
		selPos := slices.IndexFunc(pane.Rows, func(row ev.SettingsRow) bool { return row.Index == pane.Selected })
		stale := i >= len(old.Panes) || p.listWidths[i] != widths[i] || !slices.Equal(pane.Rows, old.Panes[i].Rows)
		if v.Bare && !stale && old.Panes[i].Selected != pane.Selected {
			stale = true // the marker rides the selected row
		}
		rowW := widths[i] - chrome
		if v.Bare {
			rowW = max(8, widths[i]-prefix-bareRightGap)
		}
		if stale {
			l.Clear()
			keyW := settingsKeyWidth(pane.Rows, rowW)
			for pos, row := range pane.Rows {
				label := settingsRowLabel(row, rowW, keyW)
				if v.Bare {
					label = rowMark(pos == selPos) + label
				}
				l.AddItem(label, "", 0, nil)
			}
		}
		p.listWidths[i] = widths[i]
		title := pane.Title
		if len(pane.Rows) == 0 {
			title += " · no matches"
		}
		if selPos >= 0 {
			l.SetCurrentItem(selPos)
		}
		selection, line, titleColor := st.Normal.Bold(true), border, fg
		if ev.SettingsColumn(i) == v.Focus {
			selection, line, titleColor = st.Open, accent, accent
		} else if v.Bare {
			selection = st.Normal.Foreground(accent).Background(hiBg).Bold(true)
		}
		if selPos < 0 {
			selection = st.Normal
		}
		l.SetMainTextStyle(st.Menu.Shortcut).SetSelectedStyle(selection)
		if v.Bare {
			// Borderless: the header row above carries the title.
			l.SetBorder(false).SetTitle("").SetBackgroundColor(bg)
		} else {
			l.SetBorder(true).SetTitle(tview.Escape(title)).SetTitleColor(titleColor).SetBorderColor(line).SetBackgroundColor(bg)
		}
		p.body.AddItem(l, widths[i], 0, false)
		if v.Bare && i < len(v.Panes)-1 {
			p.pipes[i].style = st.Menu.Border
			p.body.AddItem(p.pipes[i], barePipeW, 0, false)
		}
	}
	if v.Bare {
		p.details.SetBorder(false).SetTitle("")
	} else {
		p.details.SetBorder(wideDetails).SetTitle("Details").SetTitleAlign(tview.AlignLeft).SetTitleColor(fg).SetBorderColor(border)
	}
	p.details.SetWrap(!v.Bare).SetWordWrap(!v.Bare)
	if v.Bare {
		p.details.SetBorderPadding(0, 0, 2, 0)
	} else {
		p.details.SetBorderPadding(0, 0, 0, 0)
	}
	dw := detailW - 2
	if !wideDetails {
		dw = panelW - 2
	}
	if text := bareDetails(v, v.Details, dw); text != p.detailsText {
		p.detailsText = text
		p.details.SetText(text).ScrollToBeginning()
	}
	p.detailsVisible = v.Details != "" && h >= 10
	if p.detailsVisible {
		switch {
		case wideDetails:
			if v.Bare {
				p.pipes[len(v.Panes)-1].style = st.Menu.Border
				p.body.AddItem(p.pipes[len(v.Panes)-1], barePipeW, 0, false)
			}
			p.body.AddItem(p.details, detailW, 0, false)
		case v.Bare:
			p.layout.AddItem(nil, 1, 0, false)
			p.layout.AddItem(p.details, min(6, max(2, h/3)), 0, false)
		default:
			n := 2
			if v.WideDetails {
				n = min(6, h/3)
			}
			p.layout.AddItem(p.details, n, 0, false)
		}
	}
	if len(v.Panes) == 0 {
		p.layout.AddItem(nil, 0, 1, false)
	}
	p.buttonsVisible = !v.Bare && h >= 8 && w >= 34
	if p.buttonsVisible {
		p.layout.AddItem(p.footer, 1, 0, false)
	} else if h >= 8 {
		if v.Bare {
			p.layout.AddItem(nil, 1, 0, false)
		}
		p.layout.AddItem(p.help, 1, 0, false)
	}
	p.layout.SetRect(x, y, w, h)
	p.layout.Draw(scr)
}

func settingsWidths(panes []ev.SettingsPane, width int, model bool) [3]int {
	var widths [3]int
	first := 0
	if len(panes) > 1 && panes[0].Title == "" {
		first = 1
	}
	for i := first; i < len(panes); i++ {
		w := width / (len(panes) - i)
		if i == 0 && len(panes) > 1 {
			if model {
				w = min(w, 34)
			} else {
				w = min(width/3, 23)
			}
		}
		widths[i] = w
		width -= w
	}
	return widths
}

// settingsKeyWidth is the label column shared by every described row in a
// pane, so the hints line up as a second column instead of stepping with
// each label (0 = no pane row carries a description). One long label (a
// "Right-click (assistant block)" shortcut) must not push every hint right:
// the column caps and that row alone starts its hint later.
func settingsKeyWidth(rows []ev.SettingsRow, width int) int {
	keyW := 0
	for _, row := range rows {
		if row.Description != "" {
			keyW = max(keyW, cellwidth.Width(row.Label))
		}
	}
	if keyW == 0 {
		return 0
	}
	return min(width/2, max(settingsKeyCap, keyW+2))
}

// settingsKeyCap is the widest shared label column; the painted hub used a
// fixed option column for the same reason.
const settingsKeyCap = 24

func settingsRowLabel(row ev.SettingsRow, width, keyW int) string {
	if row.Suffix != "" && width >= 8 {
		return format.Fit(row.Label, width-1-cellwidth.Width(row.Suffix)) + " " + row.Suffix
	}
	if row.Description == "" || keyW == 0 || width < 14 {
		return row.Label
	}
	// A label wider than the column keeps its text: naming the row matters
	// more than holding the hint column, so that row's hint starts later.
	keyW = max(keyW, cellwidth.Width(row.Label))
	hintW := width - keyW - 4
	if hintW <= 4 {
		return row.Label
	}
	return format.Fit(row.Label, keyW) + "  — " + format.Short(row.Description, hintW)
}

func (p *settingsPanel) endEdit() {
	p.editing = false
	p.filter.Blur()
}

func (p *settingsPanel) key(key ev.KeyType) {
	p.endEdit()
	p.r.dispatch(ev.KeyMsg{Type: key})
}

func (p *settingsPanel) input(e *tcell.EventKey) bool {
	p.hasClick = false
	if e.Key() == tcell.KeyCtrlC {
		p.key(ev.KeyEsc)
		return true
	}
	if !p.filterVisible || e.Modifiers()&(tcell.ModAlt|tcell.ModCtrl|tcell.ModMeta) != 0 {
		return false
	}
	switch e.Key() {
	case tcell.KeyRune:
		if !p.editing && strings.ContainsRune(p.view.LocalKeys, e.Rune()) {
			return false
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if p.filter.GetText() == "" {
			return false // on Plugins an empty Backspace still means remove
		}
	case tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyEnd, tcell.KeyDelete:
		if !p.editing && !p.view.Edit {
			return false
		}
	default:
		p.endEdit()
		return false
	}
	p.filter.InputHandler()(e, ignoreSettingsFocus)
	return true
}

func (p *settingsPanel) paste(text string) {
	if !p.filterVisible {
		return
	}
	text = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(text)
	p.filter.PasteHandler()(text, ignoreSettingsFocus)
}

func ignoreSettingsFocus(tview.Primitive) {}

func (p *settingsPanel) mouse(a tview.MouseAction, e *tcell.EventMouse) {
	x, y := e.Position()
	if p.filterVisible && p.filter.InRect(x, y) {
		if a == tview.MouseLeftDown {
			p.hasClick = false
			p.editing = true
			p.filter.Focus(ignoreSettingsFocus)
		}
		p.filter.MouseHandler()(a, e, ignoreSettingsFocus)
		return
	}
	if a == tview.MouseLeftDown {
		p.endEdit()
	}
	for i, pane := range p.view.Panes {
		l := p.lists[i]
		if len(pane.Rows) == 0 || !l.InRect(x, y) {
			continue
		}
		msg := ev.SettingsPointerMsg{Column: ev.SettingsColumn(i)}
		switch a {
		case tview.MouseScrollUp, tview.MouseScrollDown:
			p.hasClick = false
			msg.Step = 1
			if a == tview.MouseScrollUp {
				msg.Step = -1
			}
		case tview.MouseLeftClick, tview.MouseLeftDoubleClick:
			_, top, _, height := l.GetInnerRect()
			offset, _ := l.GetOffset()
			pos := y - top + offset
			if y < top || y >= top+height || pos >= len(pane.Rows) {
				p.hasClick = false
				return
			}
			msg.Index = pane.Rows[pos].Index
			// tview's double-click timer is global, even across different rows.
			msg.Activate = i != 0 && a == tview.MouseLeftDoubleClick && p.hasClick && p.lastClick == msg
			p.lastClick, p.hasClick = msg, !msg.Activate
		default:
			return
		}
		p.endEdit()
		p.r.dispatch(msg)
		return
	}
	if a == tview.MouseLeftClick || a == tview.MouseLeftDoubleClick {
		p.hasClick = false
	}
	if p.detailsVisible && p.details.InRect(x, y) {
		switch {
		case p.view.Kind == "trajectory" && a == tview.MouseScrollUp:
			p.key(ev.KeyPgUp)
		case p.view.Kind == "trajectory" && a == tview.MouseScrollDown:
			p.key(ev.KeyPgDown)
		default:
			p.details.MouseHandler()(a, e, ignoreSettingsFocus)
		}
	}
	if p.buttonsVisible {
		if a == tview.MouseLeftDoubleClick {
			a = tview.MouseLeftClick
		}
		if !p.apply.InRect(x, y) {
			p.close.MouseHandler()(a, e, ignoreSettingsFocus)
		} else {
			p.apply.MouseHandler()(a, e, ignoreSettingsFocus)
		}
	}
}

// bareFrameW caps the borderless picker's frame: the painted dialog it
// replaces was a 150-cell box centered on wider terminals.
const bareFrameW = 150

// barePipeW is the gutter between two bare columns: the rule plus one cell.
// bareRightGap keeps a row's right-most marker off the rule.
const (
	barePipeW    = 2
	bareRightGap = 2
)

// bareWidths lays out up to three borderless columns plus the details
// column in a bare frame. The details column is dropped when it would
// squeeze the lists; a third pane (the theme hub's background column) is
// capped at the width the painted hub gave it.
func bareWidths(panelW, panes, wantDetails, firstCap int) (widths [3]int, details int) {
	if panes <= 0 {
		return widths, 0
	}
	details = wantDetails
	widths[0] = firstCap
	seps := (panes - 1) * barePipeW
	if details > 0 {
		seps += barePipeW
	}
	if panelW < widths[0]+seps+2*16 {
		widths[0] = max(12, (panelW-seps)/4)
	}
	rest := max(0, panelW-widths[0]-seps-details)
	switch {
	case panes == 1:
		widths[0] += rest
	case panes == 2:
		widths[1] = rest
	default:
		widths[2] = min(30, max(12, rest/2))
		widths[1] = rest - widths[2]
	}
	if details > 0 && widths[panes-1] < 16 {
		details = 0
		widths[panes-1] += wantDetails + barePipeW
	}
	return widths, details
}

// bareFirstCap is the first column's width, straight from the painted
// pickers: 40/34/24 for the model hub, 22 for the agent settings groups,
// 30/24 for the Pitago hub's sections.
func bareFirstCap(kind string, panelW int) int {
	switch kind {
	case "settings":
		return 22
	case "pconfig":
		if panelW < 100 {
			return 24
		}
		return 30
	}
	if panelW < 96 {
		return 24
	}
	if panelW < 130 {
		return 34
	}
	return 40
}

// bareFrameCap is the widest bare frame a kind may draw.
func bareFrameCap(kind string) int {
	if kind == "settings" {
		return 120 // the painted settings box capped here
	}
	return bareFrameW
}

// drawBareHeader paints the in-line column headers (PROVIDERS · GROUPS ·
// SECTIONS … plus DETAILS when present) that replace per-column borders.
func (p *settingsPanel) drawBareHeader(v ev.SettingsView, style, pipeStyle tcell.Style, widths [3]int, detailW int, bg tcell.Color) {
	p.header.Clear()
	sep := 0
	for i := 0; i < len(v.Panes); i++ {
		title := v.Panes[i].Title
		if len(v.Panes[i].Rows) == 0 {
			title += " · no matches"
		}
		if i > 0 {
			p.pipes[sep].style = pipeStyle
			p.header.AddItem(p.pipes[sep], barePipeW, 0, false)
			sep++
		}
		p.hdr[i].SetTextStyle(style).SetText("  " + title).SetBackgroundColor(bg)
		p.header.AddItem(p.hdr[i], widths[i], 0, false)
	}
	if detailW > 0 && len(v.Panes) < 3 {
		p.pipes[sep].style = pipeStyle
		p.header.AddItem(p.pipes[sep], barePipeW, 0, false)
		p.hdr[len(v.Panes)].SetTextStyle(style).SetText("  " + format.OrDefault(v.DetailsTitle, "DETAILS")).SetBackgroundColor(bg)
		p.header.AddItem(p.hdr[len(v.Panes)], detailW, 0, false)
	}
}

// rowMark is the two-cell gutter every bare row carries; the cursor row gets
// the same pointer the painted list used.
func rowMark(selected bool) string {
	if selected {
		return "▸ "
	}
	return "  "
}

// bareDetails indents the spec lines under the DETAILS header.
func bareDetails(v ev.SettingsView, text string, width int) string {
	if !v.Bare || text == "" || width < 4 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = format.Short(l, width)
	}
	return strings.Join(lines, "\n")
}
