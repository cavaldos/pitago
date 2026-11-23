package app

import (
	"strings"

	"pitago/src/tui/ev"
)

func (m Model) nativeDocument(d *Dialog) ev.WorkspaceView {
	switch d.Kind {
	case treeKind, treeSummaryKind, treeCustomKind:
		return m.nativeTree(d)
	case focusKind:
		return ev.WorkspaceView{Document: m.nativeFocus(d)}
	case oauthKind:
		w := min(m.winW, 96)
		return ev.WorkspaceView{Document: m.centerDocument(w, m.oauthDialogBody(d, max(1, w-8)))}
	case "subagent-herd":
		size := subagentsDetailSizes[min(max(0, d.SubSize), len(subagentsDetailSizes)-1)]
		w := max(12, m.winW-size.pad)
		if d.SubSize == 0 {
			w = min(w, 100)
		}
		w = min(w, m.winW)
		return ev.WorkspaceView{Document: m.centerDocument(w, m.subagentDetailBody(d, max(1, w-8), size.maxH))}
	}
	return ev.WorkspaceView{}
}

func documentPart(x, y, w, h int, rows []string) ev.DocumentPart {
	return ev.DocumentPart{Rect: ev.Rect{X: x, Y: y, W: w, H: h}, Rows: rows}
}

func (m Model) centerDocument(w int, content string) *ev.DocumentView {
	rows := strings.Split(strings.TrimRight(content, "\n"), "\n")
	h := min(m.winH, len(rows)+4)
	return &ev.DocumentView{
		Bounds: ev.Rect{X: (m.winW - w) / 2, Y: (m.winH - h) / 2, W: w, H: h}, Border: true,
		Parts: []ev.DocumentPart{documentPart(4, 2, w-8, h-4, rows)},
	}
}

func (m Model) nativeFocus(d *Dialog) *ev.DocumentView {
	if d.focus == nil {
		return m.centerDocument(min(m.winW, 58), "No file selected")
	}
	l := m.focusLayoutOf(d.focus)
	content := m.focusContent(d, l)
	boxW, cw := focusGeom(m.winW)
	h := 9 + l.rows + d.focus.focusAskRows(cw)
	doc := &ev.DocumentView{
		Bounds: ev.Rect{X: l.x0 - 4, Y: l.y0 - 5, W: boxW + 2, H: h}, Border: true,
		Parts: []ev.DocumentPart{
			documentPart(4, 2, cw, 3, strings.Split(content.head, "\n")),
			documentPart(4+l.codeX(), 5, l.codeW, l.rows, content.right),
			documentPart(4, 5+l.rows, cw, h-l.rows-7, strings.Split(content.tail, "\n")),
		},
	}
	if l.treeW > 0 {
		divider := make([]string, l.rows)
		for i := range divider {
			divider[i] = content.divider
		}
		doc.Parts = append(doc.Parts,
			documentPart(4, 5, l.treeW, l.rows, content.left),
			documentPart(5+l.treeW, 5, 1, l.rows, divider))
	}
	return doc
}

func (m Model) nativeTree(d *Dialog) ev.WorkspaceView {
	var rows []string
	switch d.Kind {
	case treeKind:
		if d.tree != nil && d.tree.list != nil {
			m.treeFit(d)
			rows, _ = m.treeLines(d)
		}
	case treeSummaryKind:
		rows = m.treeSummaryLines(d)
	case treeCustomKind:
		if d.tree != nil && d.tree.edit != nil {
			rows = m.treeCustomLines(d)
		}
	}
	h := m.treeScreenH()
	rows = rows[max(0, len(rows)-h):]
	top := h - len(rows)
	return ev.WorkspaceView{MainWidth: m.winW, HideHeader: true,
		Chat: ev.TranscriptView{Rows: m.chatTail(top), Height: top}, Toasts: m.nativeToasts(),
		Document: &ev.DocumentView{
			Bounds: ev.Rect{Y: top, W: m.winW, H: len(rows)},
			Parts:  []ev.DocumentPart{documentPart(0, 0, m.winW, len(rows), rows)},
		},
	}
}

func (m Model) nativeTeam(d *Dialog) *ev.DocumentView {
	w := min(m.winW, teamPanelWidth(m.winW))
	rows := strings.Split(m.teamDashboardBody(d, max(1, w-8)), "\n")
	h := min(m.winH-1-m.inputHeight(), len(rows)+4)
	return &ev.DocumentView{Bounds: ev.Rect{X: m.winW - w, Y: 1, W: w, H: h}, Border: true,
		Parts: []ev.DocumentPart{documentPart(4, 2, w-8, h-4, rows)},
	}
}
