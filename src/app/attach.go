package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/image"
	"pitago/src/pirpc"
)

// Input image tray: dropped/pasted/@-completed image paths collapse into
// [Image N] chips above the textarea instead of clogging the prompt with
// long escaped absolute paths. Send loads them as vision (takeImages);
// Backspace on empty input pops the last one.
//
// State lives on Model (imgAtts/imgSeq); pure path scanning in
// components/image.

// imgAttach is one pending tray image.
type imgAttach struct {
	label int    // [Image N], stable — never renumbered
	name  string // basename for the chip
	path  string // as typed; resolved against cwd at send
}

// attachPaths adds refs to the tray (dedupe by resolved path, cap MaxCount).
func (m *Model) attachPaths(refs []string) {
	for _, ref := range refs {
		abs := image.Resolve(m.cwd, ref)
		dup := false
		for _, a := range m.imgAtts {
			if image.Resolve(m.cwd, a.path) == abs {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if len(m.imgAtts) >= image.MaxCount {
			m.AddBlock(Block{Kind: "notice", Text: "chỉ gửi được " + strconv.Itoa(image.MaxCount) + " ảnh/lần — bỏ qua " + filepath.Base(ref)})
			continue
		}
		m.imgSeq++
		m.imgAtts = append(m.imgAtts, imgAttach{label: m.imgSeq, name: filepath.Base(ref), path: ref})
	}
	m.applyPopupH()
	m.Refresh()
}

// collectDrops moves bare image paths already sitting in the input text
// (terminal drops/pastes) into the tray.
func (m *Model) collectDrops() {
	v := m.ta.Value()
	refs := image.ScanPaths(v, m.cwd)
	if len(refs) == 0 {
		return
	}
	abs := m.absCursor()
	stripped := image.StripRefs(v, refs)
	if n := len([]rune(stripped)); abs > n {
		abs = n
	}
	m.setValueAt(stripped, abs)
	m.attachPaths(refs)
}

// takeImages gathers tray images + leftover @refs for the send path,
// then clears the tray (mirrors ta.Reset on send). A tray load failure
// aborts (nil images): the tray is kept so nothing half-broken is sent.
func (m *Model) takeImages(text string) ([]pirpc.ImageContent, []string) {	var refs []string
	for _, a := range m.imgAtts {
		refs = append(refs, a.path)
	}
	atts, notes := image.LoadPaths(m.cwd, refs)
	if len(refs) > 0 && len(notes) > 0 {
		return nil, notes
	}
	if len(atts) < image.MaxCount {
		rest := image.MaxCount - len(atts)
		ex, n2 := image.Extract(text, m.cwd)
		for i, a := range ex {
			if i >= rest {
				notes = append(notes, "chỉ gửi được "+strconv.Itoa(image.MaxCount)+" ảnh/lần — phần dư để lại dạng @text")
				break
			}
			atts = append(atts, a)
		}
		notes = append(notes, n2...)
	}
	out := make([]pirpc.ImageContent, 0, len(atts))
	for _, a := range atts {
		out = append(out, pirpc.ImageContent{Type: "image", Data: a.Data, MimeType: a.Mime})
	}
	m.imgAtts = nil
	m.trayFocus = false
	m.applyPopupH()
	return out, notes
}

// chipH reserves one input-box row for the tray (0 when empty) — part of
// the fixed-frame height math with popupH/atPopupH.
func (m *Model) chipH() int {
	if len(m.imgAtts) == 0 {
		return 0
	}
	return 1
}

// chipRow renders the tray: 📷 [Image 1] shot.png · [Image 2] pic.jpg.
// The selected chip highlights while trayFocus.
func (m Model) chipRow(w int) string {
	parts := make([]string, 0, len(m.imgAtts))
	for i, a := range m.imgAtts {
		name := toolStyle.Render(Short(a.name, 28))
		lbl := fmt.Sprintf("[Image %d]", a.label)
		if m.trayFocus && i == m.imgCursor {
			parts = append(parts, cmdHiStyle.Render(lbl+" "+a.name))
		} else {
			parts = append(parts, lbl+" "+name)
		}
	}
	hint := "(↓ Pick · ⌫ Delete)"
	if m.trayFocus {
		hint = "(←→ Move · ⌫ Delete)"
	}
	row := "📷 " + strings.Join(parts, " · ") + "  " + toolStyle.Render(hint)
	return Short(row, w)
}

// onLastLine reports whether the input cursor sits on the last row.
func (m *Model) onLastLine() bool {
	row, _ := m.cursorPos()
	return row >= len(strings.Split(m.ta.Value(), "\n"))-1
}

// enterTray moves the cursor from the input into the chip row.
func (m *Model) enterTray() {
	m.trayRet = m.absCursor()
	m.trayFocus = true
	m.imgCursor = len(m.imgAtts) - 1
	if m.imgCursor < 0 {
		m.imgCursor = 0
	}
	m.Refresh()
}

// exitTray returns the cursor to the input.
func (m *Model) exitTray() {
	m.trayFocus = false
	m.setValueAt(m.ta.Value(), m.trayRet)
	m.Refresh()
}

// handleTrayKey handles keys while trayFocus. ok=false → not a tray key,
// exit focus and process normally (typing, Enter, Ctrl+V…).
func (m *Model) handleTrayKey(km tea.KeyMsg) (tea.Cmd, bool) {
	switch km.Type {
	case tea.KeyLeft:
		if m.imgCursor > 0 {
			m.imgCursor--
			m.Refresh()
		}
		return nil, true
	case tea.KeyRight:
		if m.imgCursor < len(m.imgAtts)-1 {
			m.imgCursor++
			m.Refresh()
		}
		return nil, true
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.imgAtts) == 0 {
			m.exitTray()
			return nil, true
		}
		m.imgAtts = append(m.imgAtts[:m.imgCursor], m.imgAtts[m.imgCursor+1:]...)
		if m.imgCursor >= len(m.imgAtts) {
			m.imgCursor = len(m.imgAtts) - 1
		}
		if len(m.imgAtts) == 0 {
			m.exitTray()
		} else {
			m.Refresh()
		}
		m.applyPopupH()
		return nil, true
	case tea.KeyUp, tea.KeyDown, tea.KeyEsc:
		m.exitTray()
		return nil, true
	case tea.KeyEnter:
		m.exitTray()
		return m.submitInput(), true
	}
	return nil, false
}

// absCursor converts cursorPos to an absolute rune offset.
func (m *Model) absCursor() int {
	row, col := m.cursorPos()
	abs := col
	for i, l := range strings.Split(m.ta.Value(), "\n") {
		if i >= row {
			break
		}
		abs += len([]rune(l)) + 1
	}
	return abs
}

// setValueAt replaces the input value and parks the cursor at abs
// (SetValue alone always jumps to the end).
func (m *Model) setValueAt(v string, abs int) {
	m.ta.SetValue(v)
	lines := strings.Split(v, "\n")
	row, col, rest := 0, 0, abs
	for i, l := range lines {
		if n := len([]rune(l)); rest <= n || i == len(lines)-1 {
			row, col = i, rest
			break
		} else {
			rest -= n + 1
		}
	}
	for i := len(lines) - 1; i > row; i-- {
		m.ta.CursorUp()
	}
	m.ta.SetCursor(col)
}
