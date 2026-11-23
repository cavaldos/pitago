package tviewrt

import (
	"fmt"
	"io"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
	"pitago/src/components/cellwidth"
	"pitago/src/components/format"
	"pitago/src/tui/chatview"
	"pitago/src/tui/ev"
	"pitago/src/tui/widgets"
)

// workspace lays out the header, transcript, composer and sidebar with tview.
// Navigation and editing still go through the reducer, not a second cursor or
// scroll state on these widgets.
type workspace struct {
	split, main *tview.Flex
	header      *tview.TextView
	chat        *chatview.Window
	sidebar     *richPane
	editor      *editorPane
	toasts      *toastPane
	panels      []*richPane
	document    *tview.Box
	docParts    []*richPane
	cache       *cells.Cache
	styles      widgets.MenuBarStyles
}

func newWorkspace(raw io.Writer) *workspace {
	cache := cells.NewCache(4096)
	v := &workspace{
		split: tview.NewFlex(), main: tview.NewFlex().SetDirection(tview.FlexRow),
		header: tview.NewTextView().SetWrap(false), cache: cache,
		chat:    chatview.NewWindow(cache, func(s string) { _, _ = io.WriteString(raw, s) }),
		sidebar: newRichPane(cache), editor: &editorPane{Box: tview.NewBox(), cache: cache},
		toasts: &toastPane{Box: tview.NewBox()}, document: tview.NewBox(),
	}
	v.sidebar.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	v.editor.SetBorder(true).SetBorderPadding(0, 0, 1, 1).SetTitleAlign(tview.AlignLeft)
	v.toasts.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	return v
}

func (p *workspace) draw(scr tcell.Screen, x, y, w, h int, v ev.WorkspaceView, st widgets.MenuBarStyles, palette ev.NativePalette) {
	if w <= 0 || h <= 0 {
		return
	}
	p.styles = st
	if v.MainWidth > 0 {
		p.drawMain(scr, x, y, w, h, v, st, palette)
	}
	if v.Document != nil {
		p.drawDocument(scr, x, y, w, h, *v.Document, st)
	}
}

func (p *workspace) drawMain(scr tcell.Screen, x, y, w, h int, v ev.WorkspaceView, st widgets.MenuBarStyles, palette ev.NativePalette) {
	_, bg, _ := st.Normal.Decompose()
	border, _, _ := st.Menu.Border.Decompose()
	p.split.Clear().SetBackgroundColor(bg)
	p.main.Clear().SetBackgroundColor(bg)
	mainW := min(w, v.MainWidth)
	if mainW <= 0 {
		return
	}
	p.split.AddItem(p.main, mainW, 0, false)
	if sw := min(w-mainW, v.SidebarWidth); sw > 0 {
		p.sidebar.rows, p.sidebar.style = v.Sidebar, st.Normal
		p.sidebar.SetBackgroundColor(bg).SetBorderColor(border)
		p.split.AddItem(p.sidebar, sw, 0, false)
	}
	headerH := 0
	if !v.HideHeader {
		headerH = 1
		p.header.SetText(v.Header).SetTextStyle(st.Menu.Shortcut).SetBackgroundColor(bg)
		p.main.AddItem(p.header, headerH, 0, false)
	}
	p.chat.View, p.chat.Base, p.chat.Jump = v.Chat, st.Normal, st.Open
	p.chat.SetBackgroundColor(bg)
	p.editor.view, p.editor.styles = v.Editor, st
	p.editor.SetBackgroundColor(bg).SetTitle(tview.Escape(v.Editor.Title)).SetTitleColor(themeColor(palette.Muted)).SetBorderColor(themeColor(v.Editor.Border))
	editorH := 0
	if len(v.Editor.Rows) > 0 {
		editorH = min(h-headerH, len(v.Editor.Rows)+3)
		if v.Editor.ChipsVisible {
			editorH = min(h-headerH, editorH+1)
		}
	}
	belowH := min(max(0, h-headerH-editorH), fragmentsHeight(v.Below))
	available := h - headerH - editorH - belowH
	chatH := min(available, v.Chat.Height)
	if chatH > 0 {
		p.main.AddItem(p.chat, chatH, 0, false)
	}
	used := 0
	above := p.addPanels(v.Above, &used, available-chatH, st.Normal, bg)
	if gap := available - chatH - above; gap > 0 {
		p.main.AddItem(nil, gap, 0, false)
	}
	if editorH > 0 {
		p.main.AddItem(p.editor, editorH, 0, false)
	}
	p.addPanels(v.Below, &used, belowH, st.Normal, bg)
	p.split.SetRect(x, y, w, h)
	p.split.Draw(scr)
	p.drawToasts(scr, x, y+headerH, mainW, chatH, v.Toasts, st, palette)
}

func (p *workspace) drawDocument(scr tcell.Screen, x, y, w, h int, doc ev.DocumentView, st widgets.MenuBarStyles) {
	b := doc.Bounds
	b.W, b.H = min(w-b.X, b.W), min(h-b.Y, b.H)
	if b.W <= 0 || b.H <= 0 {
		return
	}
	_, bg, _ := st.Normal.Decompose()
	border, _, _ := st.Menu.Border.Decompose()
	p.document.SetBorder(doc.Border).SetBorderColor(border).SetBackgroundColor(bg)
	p.document.SetRect(x+b.X, y+b.Y, b.W, b.H)
	p.document.Draw(scr)
	for i, part := range doc.Parts {
		if i >= len(p.docParts) {
			p.docParts = append(p.docParts, newRichPane(p.cache))
		}
		pw, ph := min(b.W-part.X, part.W), min(b.H-part.Y, part.H)
		if pw <= 0 || ph <= 0 {
			continue
		}
		pane := p.docParts[i]
		pane.rows, pane.style = part.Rows, st.Normal
		pane.SetBackgroundColor(bg)
		pane.SetRect(x+b.X+part.X, y+b.Y+part.Y, pw, ph)
		pane.Draw(scr)
	}
}

func fragmentsHeight(parts []ev.PanelView) int {
	h := 0
	for _, panel := range parts {
		h += panel.Height()
	}
	return h
}

func (p *workspace) addPanels(parts []ev.PanelView, used *int, room int, style tcell.Style, bg tcell.Color) int {
	total := 0
	for _, v := range parts {
		if room <= 0 {
			break
		}
		if *used >= len(p.panels) {
			p.panels = append(p.panels, newRichPane(p.cache))
		}
		panel := p.panels[*used]
		*used++
		panel.setText(v.Text)
		n := min(room, v.Height())
		panel.style, panel.maxW = style, v.Width
		border, _, _ := p.styles.Menu.Border.Decompose()
		panel.SetBackgroundColor(bg).SetBorder(v.Border).SetBorderColor(border).
			SetBorderPadding(v.PaddingY, v.PaddingY, v.PaddingX, v.PaddingX)
		p.main.AddItem(panel, n, 0, false)
		room -= n
		total += n
	}
	return total
}

// richPane displays preformatted Markdown, ANSI plugin output, or sidebar
// rows in a bounded tview region without interpreting tview color tags.
type richPane struct {
	*tview.Box
	cache *cells.Cache
	rows  []string
	text  string
	style tcell.Style
	maxW  int
}

func newRichPane(cache *cells.Cache) *richPane {
	return &richPane{Box: tview.NewBox(), cache: cache}
}

func (p *richPane) setText(s string) {
	if p.text != s || p.rows == nil {
		p.text, p.rows = s, strings.Split(s, "\n")
	}
}

func (p *richPane) Draw(scr tcell.Screen) {
	if p.maxW > 0 {
		x, y, w, h := p.GetRect()
		p.SetRect(x, y, min(w, p.maxW), h)
	}
	p.Box.DrawForSubclass(scr, p)
	x, y, w, h := p.GetInnerRect()
	if w > 0 && h > 0 {
		cells.Draw(scr, x, y, w, h, p.cache.ParseRows(p.rows[:min(len(p.rows), h)], p.style), 0)
	}
}

// editorPane draws real widget chrome and the editor's exact visual rows.
// Caret is cell-based, including wrapped text and wide grapheme clusters.
type editorPane struct {
	*tview.Box
	cache  *cells.Cache
	view   ev.EditorView
	styles widgets.MenuBarStyles
}

func (p *editorPane) Draw(scr tcell.Screen) {
	p.Box.DrawForSubclass(scr, p)
	x, y, w, h := p.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	for i, row := range p.view.Rows {
		if i >= h-1 {
			break
		}
		prompt := p.cache.ParseStyle(row.Prompt, p.styles.Normal.Foreground(themeColor(p.view.PromptColor)))[0]
		used := cells.DrawLine(scr, x, y+i, w, prompt)
		style := p.styles.Normal
		if row.Placeholder {
			style = p.styles.Menu.Shortcut
		}
		line := p.cache.ParseStyle(row.Text, style)[0]
		cells.DrawLine(scr, x+used, y+i, w-used, line)
		if row.Caret >= 0 && used+row.Caret < w {
			// Use the terminal's real cursor as the caret. IMEs (Vietnamese
			// Telex/VNI, CJK, …) draw their composition text at the real
			// cursor; a painted caret with a hidden cursor leaves it wherever
			// the last cell was written, usually the bottom-right corner.
			scr.ShowCursor(x+used+row.Caret, y+i)
		}
	}
	if p.view.ChipsVisible && h > len(p.view.Rows)+1 {
		cells.DrawLine(scr, x, y+h-2, w, p.cache.ParseStyle(p.view.Chips, p.styles.Normal)[0])
	}
	status := format.Short(p.view.Status, w)
	left := max(0, w-cellwidth.Width(status))
	style := p.styles.Menu.Shortcut
	if left > 1 {
		cells.DrawLine(scr, x, y+h-1, left-1, p.cache.ParseStyle(format.Short(p.view.Hints, left-1), style)[0])
	}
	cells.DrawLine(scr, x+left, y+h-1, w-left, p.cache.ParseStyle(status, style)[0])
}

type toastPane struct {
	*tview.Box
	rows       []ev.ToastView
	muted      tcell.Color
	green, red tcell.Color
}

func (p *workspace) drawToasts(scr tcell.Screen, x, y, w, chatH int, rows []ev.ToastView, styles widgets.MenuBarStyles, palette ev.NativePalette) {
	width := min(w, max(24, w/3))
	count := min(len(rows), chatH-2)
	if count <= 0 || width < 8 {
		return
	}
	p.toasts.rows = rows[len(rows)-count:]
	p.toasts.muted, _, _ = styles.Menu.Shortcut.Decompose()
	p.toasts.green, p.toasts.red = themeColor(palette.Green), themeColor(palette.Red)
	_, bg, _ := styles.Normal.Decompose()
	border, _, _ := styles.Menu.Border.Decompose()
	p.toasts.SetBackgroundColor(bg).SetBorderColor(border)
	p.toasts.SetRect(x+w-width, y, width, count+2)
	p.toasts.Draw(scr)
}

func (p *toastPane) Draw(scr tcell.Screen) {
	p.Box.DrawForSubclass(scr, p)
	x, y, w, h := p.GetInnerRect()
	for i, row := range p.rows {
		if i >= h {
			break
		}
		glyph, color := "●", p.green
		if row.Error {
			glyph, color = "×", p.red
		}
		tview.Print(scr, glyph, x, y+i, 1, tview.AlignLeft, color)
		if w > 2 {
			tview.Print(scr, tview.Escape(fmt.Sprintf("%s · %ds", format.Short(row.Text, max(1, w-9)), row.Remaining)), x+2, y+i, w-2, tview.AlignLeft, p.muted)
		}
	}
}
