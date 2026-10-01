package app

import tea "github.com/charmbracelet/bubbletea"

// plan_toggle.go — Tab toggles plan mode.
//
// Plan mode itself is owned by the plan-mode extension
// (~/.pi/agent/extensions/plan-mode): it registers the "plan" command, whose
// handler flips plan↔normal with no menu of its own — it only notifies and
// pushes a setStatus ("⏸ plan" / cleared) back to us. This file is the thin
// host-side half: the key is wired in src/app/update.go and everything it needs
// is here, so the whole feature stays one small file.

// planToggleCmd is the plan-mode extension's command name. Exact by design: a
// fuzzy "any command with plan in it" match could fire an unrelated plugin
// command, and the user typed Tab, not a slash command.
const planToggleCmd = "plan"

// togglePlanMode flips plan mode from the editor's Tab key.
//
// The latch is flipped BEFORE the forward so the PLAN border changes in this
// same frame; the extension's own setStatus arrives a moment later and
// isPlanMode() reads both, so the optimistic value and the authoritative one
// agree. A command that is not installed produces a notice rather than a
// swallowed keypress: Tab must never look like it did nothing.
func (m *Model) togglePlanMode() tea.Cmd {
	name := "/" + planToggleCmd
	if _, ok := m.FindPiCommand(name); !ok {
		m.AddBlock(Block{Kind: "notice", Text: "plan mode needs the plan-mode extension — Tab runs /" + planToggleCmd})
		m.Refresh()
		return nil
	}
	m.planOn = !m.isPlanMode()
	if m.planOn {
		m.Status = "plan mode: on"
	} else {
		m.Status = "plan mode: off"
	}
	m.Refresh()
	return m.ForwardExtensionCommand(name)
}
