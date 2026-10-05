package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/format"
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
	// Compact is pi's compaction answer, kept so the billing notice can be
	// appended after the reload rebuilt the transcript. It is also the only
	// source of the summary pi rendered as a compaction block.
	Compact *pirpc.CompactionResult
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

// addCompactionCostNotice appends pi's billing line for a compaction or a
// branch summary.
//
// pi only shows it when its own showCacheMissNotices setting is on, and that
// setting defaults to false (dist/core/settings-manager.js:702), so the
// default pitago run stays silent exactly like pi. The wording is pi's
// (interactive-mode.js:3242-3256): "<label>: N tokens billed (~$X.XX)", the
// label being "Compaction" or "Branch summary" and the cost dropped below a
// cent.
//
// It is a chat row, not a toast: pi puts it in the chat container, where it
// stays with the summary it explains.
func (m *Model) addCompactionCostNotice(label string, u *pirpc.EntryUsage) {
	text := CompactionCostNotice(label, u)
	if text == "" {
		return
	}
	m.AddBlock(Block{Kind: "costnotice", Text: text})
}

// CompactionCostNotice is the notice text addCompactionCostNotice renders;
// "" means pi would show nothing (nil usage, or the notice setting is off).
func CompactionCostNotice(label string, u *pirpc.EntryUsage) string {
	if u == nil {
		return ""
	}
	if !pirpc.PiBool(pirpc.ReadPiSettings(), "showCacheMissNotices", false) {
		return ""
	}
	tokens := u.Input + u.Output + u.CacheRead + u.CacheWrite
	cost := ""
	if u.Cost.Total >= 0.01 {
		cost = fmt.Sprintf(" (~$%.2f)", u.Cost.Total)
	}
	return fmt.Sprintf("%s: %s tokens billed%s", label, format.FmtComma(tokens), cost)
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
	if msg.Op == "compact" && m.compactAborted && msg.Err != nil {
		// Esc on "(escape to cancel)" makes pi's abort land first:
		// compaction_end{reason:"manual",aborted:true} already reported "Compaction
		// cancelled", and then compact() rethrows (agent-session.js:2223), so this
		// command answers with an error too. Two surfaces for one cancellation
		// would look like two failures, so pi's own single report stands.
		msg.Err = nil
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
		// after it — tea.Sequence keeps that order. The compaction billing
		// line rides along in the same trailing message, for the same
		// reason.
		notice := msg.Notice
		cost := ""
		if msg.Compact != nil {
			cost = CompactionCostNotice("Compaction", msg.Compact.Usage)
		}
		return m, tea.Sequence(m.fetchAll(), func() tea.Msg {
			return SettingsRefreshMsg{Notice: notice, CostNotice: cost}
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
		// Same boundary strip as shell mode and bashExecution: a
		// "!cmd" can colour its own output too (see shell.go).
		if out := strings.TrimRight(stripANSI(msg.Bash.Output), "\n"); out != "" {
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

// abortCompactionCmd cancels an in-flight compaction with ONE Esc press,
// which is what the input-border indicator promises ("… (escape to cancel)").
//
// pi REPLACES the editor's Esc handler for the duration of a compaction
// (interactive-mode.js:2883-2887), so this is the highest-precedence Esc in
// the editor — above bash, auto-retry and the double-press turn cancel.
//
// There is no compaction-only RPC: pi's generic `abort` is the door
// (rpc-mode.js:327-329 → session.abort(), dist/core/agent-session.js:1841-1852),
// and that method also calls abortRetry/abortBranchSummary and agent.abort().
// pitago sends that same command, so the one remaining difference from pi is
// stated plainly: during an AUTOMATIC compaction pi would cancel only the
// compaction, while this abort also stops the agent run. For a manual
// /compact (the case the hint is aimed at) the two are identical.
//
// ClearQueue is deliberately NOT called: pi's compaction Esc aborts the
// compaction and never pulls queued steer/follow-up back into the editor,
// unlike the turn cancel below.
func (m *Model) abortCompactionCmd() tea.Cmd {
	m.escArm = time.Time{} // no double-press arm during a compaction
	m.Status = "cancelling compaction…"
	m.Refresh()
	return func() tea.Msg {
		// pi answers compaction_end{reason:"manual",aborted:true} on its own
		// event stream (agent-session.js:2205-2222); this command only has to
		// make the abort happen, so it reports nothing of its own.
		_, err := m.Pi.Abort()
		return PiOpMsg{Op: "abort-compaction", Err: err}
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
