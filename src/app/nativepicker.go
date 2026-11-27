package app

import (
	"strconv"

	tea "pitago/src/tui/ev"
)

// Native pickers (tviewrt): simple list dialogs are shown as a mouse-first
// picker.Picker instead of the text-drawn dialog. The Dialog stays the
// source of truth on the stack; the picker is only its view. A pick is fed
// back through the dialog's own Enter path (cursor set to the picked row),
// a cancel through its Esc path, so every confirm/dismiss rule is shared
// with the Bubble Tea UI.

// nativePickerKind lists the single-pane list dialogs whose keys are fully
// covered by the picker (↑↓/PgUp/PgDn/Home/End, filter typing, Enter, Esc).
// Multi-pane and special-key dialogs use dedicated views instead.
func nativePickerKind(d *Dialog) bool {
	switch d.Kind {
	case "recent", "yank", "fork", "logout", "thinking", "blockactions", "palette":
		return len(d.Options) > 0
	case "ui":
		return d.Method == "select" && len(d.Options) > 0
	}
	return false
}

// nativePickerOpen reports whether the top dialog is shown natively (View
// then draws the normal frame behind it).
func (m Model) nativePickerOpen() bool {
	return len(m.Dialogs) > 0 && m.Dialogs[0].pickerTag != "" && m.Dialogs[0].pickerTag == m.nativePicker
}

// syncNativePicker closes a picker whose dialog is gone and opens one for a
// newly-topped eligible dialog.
func (m Model) syncNativePicker(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	var top *Dialog
	if len(m.Dialogs) > 0 {
		top = m.Dialogs[0]
	}
	if m.nativePicker != "" && (top == nil || top.pickerTag != m.nativePicker) {
		tag := m.nativePicker
		m.nativePicker = ""
		cmd = tea.Batch(cmd, func() tea.Msg { return tea.ClosePickerMsg{Tag: tag} })
	}
	if top != nil && top.pickerTag == "" && nativePickerKind(top) {
		m.pickerSeq++
		top.pickerTag = "dlg:" + strconv.Itoa(m.pickerSeq)
		m.nativePicker = top.pickerTag
		open := pickerFor(top)
		cmd = tea.Batch(cmd, func() tea.Msg { return open })
	}
	return m, cmd
}

func pickerFor(d *Dialog) tea.OpenPickerMsg {
	items := make([]tea.PickerItem, len(d.Options))
	for i, o := range d.Options {
		items[i] = tea.PickerItem{ID: strconv.Itoa(i), Label: o}
		if i < len(d.Descs) {
			items[i].Hint = d.Descs[i]
		}
	}
	sel := ""
	if d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		sel = strconv.Itoa(d.FIdx[d.Cursor])
	}
	return tea.OpenPickerMsg{
		Tag: d.pickerTag, Title: d.Title, Message: d.Message,
		Items: items, Selected: sel, Filterable: filterableDialog(d), Filter: d.Filter,
		Footer: "↑↓ move · Enter select · Esc close · click to pick",
	}
}

// pickerResult replays a native pick through the dialog's own key path.
func (m Model) pickerResult(r tea.PickerResultMsg) (tea.Model, tea.Cmd) {
	if len(m.Dialogs) == 0 || m.Dialogs[0].pickerTag != r.Tag {
		return m, nil // stale: the dialog already went away
	}
	d := m.Dialogs[0]
	m.nativePicker = "" // the runtime closed it
	if r.Cancelled {
		// Should Esc leave the dialog up (e.g. it only clears a filter),
		// the sync reopens it natively.
		d.pickerTag = ""
		return m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	ri, err := strconv.Atoi(r.ID)
	if err != nil || ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	d.Filter = ""
	d.Reindex()
	for i, idx := range d.FIdx {
		if idx == ri {
			d.Cursor = i
			break
		}
	}
	// If Enter leaves this same dialog on top (a list that stays open), it
	// is reopened natively by the sync after this Update.
	d.pickerTag = ""
	return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}
