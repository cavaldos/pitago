// Package stack pins the TUI/CLI dependency stack and smoke-tests that every
// library resolves and compiles together. Import paths matter here: the Charm
// v2 modules moved from github.com/charmbracelet/* to charm.land/*.
//
// Chat markdown renders with pi first then Glamour v2 fallback
// (see src/pimark, src/gomark).
//
//	tea      // event loop + state management (Bubble Tea v2)
//	bubbles  // textarea, viewport, spinner, list (Bubbles v2)
//	lipgloss // styling (Lip Gloss v2)
//	cobra    // CLI commands
//	exec     // external tool runner (stdlib os/exec, no install needed)
package stack

import (
	"os/exec"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
)

// EventLoop touches the Bubble Tea v2 event-loop entry points.
func EventLoop() (tea.Cmd, tea.ProgramOption) {
	return tea.Quit, tea.WithoutSignalHandler()
}

// Widgets builds the Bubbles v2 inputs used by chat.
func Widgets() (textarea.Model, viewport.Model, spinner.Model) {
	ta := textarea.New()
	ta.Placeholder = "Nhap tin nhan..."
	ta.Focus()
	return ta, viewport.New(), spinner.New()
}

// ItemList builds an empty selection list.
func ItemList() list.Model {
	return list.New(nil, list.NewDefaultDelegate(), 0, 0)
}

// Style renders one styled string via Lip Gloss v2.
func Style(s string) string {
	return lipgloss.NewStyle().Bold(true).Render(s)
}

// RootCommand builds the Cobra CLI root.
func RootCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pitago",
		Short: "TUI frontend",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
}

// Tool builds an external command for the tool runner (os/exec, stdlib).
func Tool(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
