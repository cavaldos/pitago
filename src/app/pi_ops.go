package app

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// pi's RPC commands pitago drives directly, and the results they report.
//
// Everything here mirrors a command pi declares in
// dist/modes/rpc/rpc-types.d.ts — the same union pi's own TUI drives — so
// nothing here is an invention: it is the UI half of a command pitago used
// to either stub out ("needs pi's own TUI") or, worse, never reach.

// PiOpMsg reports the outcome of one pi RPC command pitago issued itself:
// compact, fork, clone, name, export, or a user bash command.
type PiOpMsg struct {
	Op     string // "compact" | "fork" | "clone" | "name" | "export" | "bash"
	Notice string // ready-made chat line ("" → nothing but the error)
	Err    error
	// Text is pi's own text for the new branch (fork): pi puts it back in
	// the editor after forking, so the user can keep going from there.
	Text string
	// Bash is pi's result for a "!cmd" run, kept whole: the exit code is
	// absent when pi killed the command, and Truncated/FullOutputPath say
	// where the rest went.
	Bash    *pirpc.BashResult
	Command string // the shell command, echoed into the block header
	Exclude bool   // excludeFromContext: output shown but not sent to the model
	// Reload marks a command that changed pi's session in place (fork,
	// clone, compact): the transcript, stats and session identity have to
	// be re-read instead of guessed.
	Reload bool
}

// runBashCmd runs a "!cmd" through pi's bash RPC. pi refuses a second
// command while one is running and keeps the text in the editor
// ("A bash command is already running. Press Esc to cancel it first.",
// interactive-mode.js:2591-2594); Esc on a running command calls
// session.abortBash() (interactive-mode.js:2336-2338), which pitago wires
// to the same place.
func (m *Model) runBashCmd(command string, exclude bool, text string) tea.Cmd {
	if m.bashRunning {
		m.AddBlock(Block{Kind: "notice", Text: "a bash command is already running — press Esc to cancel it first"})
		m.Refresh()
		return nil
	}
	m.pushHist(text)
	m.histIdx = -1
	m.bashRunning = true
	m.Status = "running " + Short(command, 60) + "…"
	m.ta.Reset()
	m.closeAt()
	m.Refresh()
	return m.bashCmd(command, exclude)
}

// bashCmd is the RPC half of runBashCmd: pi runs the command in the
// session cwd and returns its own BashResult.
func (m *Model) bashCmd(command string, exclude bool) tea.Cmd {
	return func() tea.Msg {
		res, err := m.Pi.Bash(command, exclude)
		msg := PiOpMsg{Op: "bash", Bash: &res, Command: command, Exclude: exclude}
		if err != nil {
			msg.Err = err
			return msg
		}
		if res.Cancelled {
			msg.Notice = "bash cancelled"
		}
		return msg
	}
}

// handlePiOp applies the outcome of a pi command pitago issued itself. It
// is the Update arm for PiOpMsg.
func (m Model) handlePiOp(msg PiOpMsg) (tea.Model, tea.Cmd) {
	if msg.Op == "bash" || msg.Op == "abort-retry" {
		m.bashRunning = false
		if msg.Op == "abort-retry" {
			m.retrying = false
		}
	}
	if msg.Bash != nil {
		m.addBashBlock(msg)
	}
	m.Status = "ready"
	if msg.Err != nil {
		m.AddBlock(Block{Kind: "notice", Text: msg.Err.Error(), Err: true})
		m.Refresh()
		return m, nil
	}
	if msg.Op == "fork" && strings.TrimSpace(msg.Text) != "" {
		// pi hands the forked branch's text back to the editor
		// (editor.setText(result.selectedText), interactive-mode.js:4477).
		m.restoreQueuedToEditor([]string{msg.Text})
	}
	if msg.Reload {
		// The session changed inside pi (fork/clone/compact): re-read state,
		// transcript and stats instead of keeping the old ones. The
		// connectedMsg arm clears the blocks, so the notice has to land
		// after it — tea.Sequence keeps that order.
		notice := msg.Notice
		return m, tea.Sequence(m.fetchAll(), func() tea.Msg {
			return SettingsRefreshMsg{Notice: notice}
		})
	}
	if msg.Notice != "" {
		m.AddBlock(Block{Kind: "notice", Text: msg.Notice})
	}
	m.Refresh()
	return m, nil
}

// addBashBlock renders a user "!cmd" the way pi renders its bash component:
// the command, then its output, then the exit code. Truncated output points
// at the file pi wrote the rest to, and excludeFromContext says the output
// is not in the model's context.
func (m *Model) addBashBlock(msg PiOpMsg) {
	header := "$ " + msg.Command
	if msg.Exclude {
		header += "  (excluded from context)"
	}
	body := header
	if msg.Bash != nil {
		if out := strings.TrimRight(msg.Bash.Output, "\n"); out != "" {
			body += "\n" + out
		}
		switch {
		case msg.Bash.Cancelled:
			body += "\n(cancelled)"
		case msg.Bash.ExitCode != nil:
			body += "\n(exit " + strconv.Itoa(*msg.Bash.ExitCode) + ")"
		default:
			body += "\n(killed)"
		}
		if msg.Bash.Truncated {
			more := "output truncated"
			if msg.Bash.FullOutputPath != "" {
				more += " — full output: " + msg.Bash.FullOutputPath
			}
			body += "\n(" + more + ")"
		}
	}
	m.AddBlock(Block{Kind: "bash", Text: body, Err: msg.Bash != nil && !msg.Bash.Cancelled &&
		msg.Bash.ExitCode != nil && *msg.Bash.ExitCode != 0})
	m.Refresh()
}

// abortBashCmd cancels a running "!cmd" (pi: Esc → session.abortBash()).
func (m *Model) abortBashCmd() tea.Cmd {
	m.bashRunning = false
	m.Status = "cancelling bash…"
	m.Refresh()
	return func() tea.Msg {
		return PiOpMsg{Op: "bash", Notice: "bash cancelled", Err: m.Pi.AbortBash()}
	}
}

// abortRetryCmd stops pi's automatic retry after a provider failure, so the
// user is not stuck in a retry loop (pi binds Esc to session.abortRetry()
// while auto_retry_start is live, interactive-mode.js:2921-2929).
func (m *Model) abortRetryCmd() tea.Cmd {
	m.retrying = false
	m.thinking = false
	m.Status = "cancelling retry…"
	m.Refresh()
	return func() tea.Msg {
		return PiOpMsg{Op: "abort-retry", Notice: "retry cancelled", Err: m.Pi.AbortRetry()}
	}
}
