// Package teabridge runs an ev.Model on Bubble Tea v1. It is the interim
// runtime while src/app moves to tview: native tea input is translated to
// ev types on the way in, and ev Cmds (including Batch/Sequence/Quit/mouse
// mode) are translated back on the way out.
//
// It also converts messages for the few remaining bubbles widgets
// (viewport, textarea) the app still embeds.
package teabridge

import (
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/tui/ev"
)

// Program wraps m for tea.NewProgram.
func Program(m ev.Model, opts ...tea.ProgramOption) *tea.Program {
	return tea.NewProgram(model{m}, opts...)
}

// Sender returns an ev.Sender that posts into p.
func Sender(p *tea.Program) ev.Sender {
	return func(msg ev.Msg) { p.Send(msg) }
}

type model struct{ m ev.Model }

func (w model) Init() tea.Cmd { return ToTeaCmd(w.m.Init()) }

func (w model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := w.m.Update(FromTea(msg))
	return model{nm}, ToTeaCmd(cmd)
}

func (w model) View() string { return w.m.View() }

// Inner returns the ev.Model held by a model returned from Program.Run.
func Inner(m tea.Model) ev.Model {
	if w, ok := m.(model); ok {
		return w.m
	}
	return nil
}

// FromTea converts a native tea message to its ev equivalent. Messages the
// app defined itself (and anything unknown) pass through unchanged.
func FromTea(msg tea.Msg) ev.Msg {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return ev.KeyMsg{Type: ev.KeyType(m.Type), Runes: m.Runes, Alt: m.Alt, Paste: m.Paste}
	case tea.MouseMsg:
		return ev.MouseMsg{
			X: m.X, Y: m.Y, Shift: m.Shift, Alt: m.Alt, Ctrl: m.Ctrl,
			Action: ev.MouseAction(m.Action), Button: ev.MouseButton(m.Button),
			Type: ev.MouseEventType(m.Type),
		}
	case tea.WindowSizeMsg:
		return ev.WindowSizeMsg{Width: m.Width, Height: m.Height}
	case tea.QuitMsg:
		return ev.QuitMsg{}
	}
	return msg
}

// ToTea converts an ev message back to tea's (for bubbles widgets and for
// Cmd results that tea's runtime must interpret).
func ToTea(msg ev.Msg) tea.Msg {
	switch m := msg.(type) {
	case ev.KeyMsg:
		return tea.KeyMsg{Type: tea.KeyType(m.Type), Runes: m.Runes, Alt: m.Alt, Paste: m.Paste}
	case ev.MouseMsg:
		return tea.MouseMsg{
			X: m.X, Y: m.Y, Shift: m.Shift, Alt: m.Alt, Ctrl: m.Ctrl,
			Action: tea.MouseAction(m.Action), Button: tea.MouseButton(m.Button),
			Type: tea.MouseEventType(m.Type),
		}
	case ev.WindowSizeMsg:
		return tea.WindowSizeMsg{Width: m.Width, Height: m.Height}
	case ev.QuitMsg:
		return tea.QuitMsg{}
	case ev.MouseModeMsg:
		if m.All {
			return tea.EnableMouseAllMotion()
		}
		return tea.DisableMouse()
	case ev.BatchMsg:
		out := make(tea.BatchMsg, len(m))
		for i, c := range m {
			out[i] = ToTeaCmd(c)
		}
		return out
	case ev.SequenceMsg:
		cmds := make([]tea.Cmd, len(m))
		for i, c := range m {
			cmds[i] = ToTeaCmd(c)
		}
		// tea's sequence type is unexported; rebuild it through Sequence.
		if seq := tea.Sequence(cmds...); seq != nil {
			return seq()
		}
		return nil
	}
	return msg
}

// ToTeaCmd wraps an ev.Cmd so tea's runtime can execute it.
func ToTeaCmd(c ev.Cmd) tea.Cmd {
	if c == nil {
		return nil
	}
	return func() tea.Msg { return ToTea(c()) }
}

// FromTeaCmd wraps a tea.Cmd (from a bubbles widget) as an ev.Cmd.
func FromTeaCmd(c tea.Cmd) ev.Cmd {
	if c == nil {
		return nil
	}
	return func() ev.Msg {
		msg := c()
		switch b := msg.(type) {
		case tea.BatchMsg:
			out := make(ev.BatchMsg, len(b))
			for i, x := range b {
				out[i] = FromTeaCmd(x)
			}
			return out
		}
		return FromTea(msg)
	}
}

// Filter adapts an ev-level message filter to tea.WithFilter.
func Filter(f func(ev.Model, ev.Msg) ev.Msg) func(tea.Model, tea.Msg) tea.Msg {
	return func(m tea.Model, msg tea.Msg) tea.Msg {
		out := f(Inner(m), FromTea(msg))
		if out == nil {
			return nil
		}
		return msg
	}
}
