package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/pet"
)

// The /pet picker is the same shape as /model: TWO panes. Left = the entry
// list (animals plus the two looks, "ascii" and "classic"), right = the demo
// of the entry under the cursor, re-rendered on every move from the same
// pass. The right pane is a passive preview, so there is no pane focus: the
// keys are the generic list picker's, nothing more.

// petDialogPage is the number of pane rows the window can show — the ONE
// budget both the render and PgUp/PgDn read, so a page is always exactly
// what is on screen.
//
// The box is 4 rows of chrome (border + vertical padding), 6 fixed content
// lines (title, filter, blank, pane header, blank, footer) and the dialog
// host adds one trailing row for the "(N more)" hint, so the pane rows are
// what is left of winH. One row is the floor (the list still scrolls) and
// 24 the ceiling (a taller box is mostly empty art).
func (m Model) petDialogPage() int {
	win := m.winH - 11
	if win < 1 {
		win = 1
	}
	if win > 24 {
		win = 24
	}
	return win
}

// petDialogBoxW follows renderModelDialog's adaptive box (two panes need
// room): winW-10, floored at 70 so the panes stay legible and capped at 120.
func (m Model) petDialogBoxW() int {
	boxW := m.winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 120 {
		boxW = 120
	}
	return boxW
}

// petDemoLines is the right pane: the demo art of entry. The header above it
// already names the entry, so the art is drawn bare — an inline name here
// would read twice — and the top padding a short drawing carries in the
// sidebar is trimmed, since in a preview pane it only pushes the art down.
// "ascii" has no art of its own: it previews the animal currently pinned,
// which is what picking it would leave on screen.
func petDemoLines(entry string, anchor string) []string {
	art := pet.Demo(entry)
	if art == nil {
		art = pet.Demo(anchor) // "ascii": the pinned animal
	}
	if art == nil {
		art = pet.Demo(pet.DefaultName)
	}
	start := 0
	for start < len(art)-1 && strings.TrimSpace(art[start]) == "" {
		start++
	}
	return art[start:]
}

// renderPetDialog draws the two-pane /pet picker inside the shared dlgStyle
// box: left list (▸ cursor, ● entry in use, "…(+N above/below)" window),
// right preview of the highlighted entry.
func (m Model) renderPetDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.petDialogBoxW()
	leftW := 24
	if boxW >= 110 {
		leftW = 28
	}
	rightW := boxW - 8 - leftW - 3
	if rightW < 24 {
		rightW = 24
	}
	win := m.petDialogPage()

	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	var leftLines []string
	if above {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d above)", start)))
	}
	for fi := start; fi < end; fi++ {
		name := d.Options[d.FIdx[fi]]
		mark := "  "
		dot := "  "
		if name == d.Current {
			dot = statusBarStyle.Render("● ")
		}
		style := statusBarStyle
		if fi == d.Cursor {
			mark = "▸ "
			style = rowHiStyle
		}
		leftLines = append(leftLines, mark+style.Width(leftW-2).Render(
			dot+Fit(name, leftW-4)))
	}
	if below {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	if total == 0 {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render("— no match —"))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Width(leftW-2).Render(""))
	}

	// Right pane: the demo of the cursor row, vertically centred against the
	// list window so the panes stay the same height.
	entry, demo := "", []string(nil)
	if d.Cursor >= 0 && d.Cursor < total {
		entry = d.Options[d.FIdx[d.Cursor]]
		demo = petDemoLines(entry, m.petShown().Name)
	}
	var rightLines []string
	if entry == "" {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render("— no pet —"))
	} else {
		head := "  " + sideTitleStyle.Render(Short(entry, rightW-4))
		rightLines = append(rightLines, head)
		for _, ln := range demo {
			rightLines = append(rightLines, "  "+statusBarStyle.Render(Short(ln, rightW-4)))
		}
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+statusBarStyle.Width(rightW-2).Render(""))
	}
	// Trim the preview block to the window (very short terminals): the
	// header stays, the art is what gives way.
	if len(rightLines) > win {
		rightLines = rightLines[:win]
	}

	sep := sepStyle.Render("│")
	b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("PETS") + " │ " +
		"  " + sideTitleStyle.Width(rightW-2).Render("PREVIEW") + "\n")
	for i := 0; i < win; i++ {
		b.WriteString(leftLines[i] + " " + sep + " " + rightLines[i] + "\n")
	}
	b.WriteString("\n")
	b.WriteString(toolStyle.Render("type to filter · ↑↓ select · Enter confirm · Esc cancel"))
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

// updatePetDialog is the GENERIC list picker's key set — ↑↓ with wrap,
// PgUp/PgDn, type to filter, Backspace to trim, Enter to apply, Esc/^C to
// dismiss. The right pane is passive, so ←/→ stay no-ops and there is no
// pane focus to cycle.
func (m Model) updatePetDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	n := len(d.FIdx)
	switch km.Type {
	case tea.KeyUp:
		if n > 0 {
			if d.Cursor > 0 {
				d.Cursor--
			} else {
				d.Cursor = n - 1
			}
		}
		return m, nil
	case tea.KeyDown:
		if n > 0 {
			if d.Cursor < n-1 {
				d.Cursor++
			} else {
				d.Cursor = 0
			}
		}
		return m, nil
	case tea.KeyPgDown:
		if n > 0 {
			d.Cursor += m.petDialogPage()
			if d.Cursor > n-1 {
				d.Cursor = n - 1
			}
		}
		return m, nil
	case tea.KeyPgUp:
		if n > 0 {
			d.Cursor -= m.petDialogPage()
			if d.Cursor < 0 {
				d.Cursor = 0
			}
		}
		return m, nil
	case tea.KeyCtrlC:
		// Dialog capture runs before the global ^C arm, so a picker this
		// tall needs its own way out (same reason as the generic path).
		return m.dismissDialog(d)
	case tea.KeyEsc:
		return m.dismissDialog(d)
	case tea.KeyEnter:
		return m.confirmDialog(d)
	case tea.KeyBackspace:
		if d.Filter != "" {
			r := []rune(d.Filter) // rune-wise: byte trim corrupts Vietnamese
			d.Filter = string(r[:len(r)-1])
			d.Reindex()
		}
		return m, nil
	}
	if km.Type == tea.KeySpace || km.Type == tea.KeyRunes {
		// Generic matching over Options (no pet-specific matcher): the
		// list just re-windows from d.FIdx.
		d.Filter += km.String()
		d.Reindex()
		return m, nil
	}
	return m, nil
}
