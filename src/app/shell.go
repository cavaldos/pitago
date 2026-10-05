package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// shell.go — "!" on an empty editor switches the editor into shell mode.
//
// pi already owns a shell escape ("!cmd", see piBashCommand): the line is
// handed to pi over RPC, so it becomes a session entry the model can see and
// every command runs in a fresh process — "cd" is therefore forgotten by the
// next line, which makes the escape useless for directory work.
//
// This file is the local half of that idea. One long-lived "sh" is started on
// the first command with cwd = the session cwd and is reused for every later
// line, so cd/export/variables persist. Each submitted line is echoed as a
// chat block ("bash" kind, same shape as pi's own bash output) and NOTHING
// is sent to pi: shell mode is a local tool, never a turn.
//
// Lifecycle rules:
//   - "!" typed alone into an empty editor enters shell mode (the "!" itself
//     is consumed, never sent as text).
//   - Enter runs the current line in the persistent shell, once at a time.
//   - Esc leaves shell mode and kills the shell process.
//   - The shell is local only: m.Pi is never touched from this file.
//
// While shell mode is on the editor belongs to the shell: pi's own key
// bindings (plan mode, model switching, the yank dialogs, the /command
// popup, @mentions) are suppressed, so a half-typed command can never
// change pi's state or open a dialog over the shell. See piKeyTakenByShell
// and the popup gates in palette.go / mention.go.

const (
	// shellPlaceholder is the editor hint while shell mode is on.
	shellPlaceholder = "Shell mode — type a command, Enter runs it · Esc exits"
	// shellMarkerPrefix ends every command with a unique sentinel line
	// ("<prefix><gen>__ <exit code>") so the reader knows where one
	// command's output stops without waiting for the shell to go quiet.
	shellMarkerPrefix = "__pitago_shell_end_"
	// shellScanMax caps a single output line (a minified blob on one line
	// must not kill the reader and strand the shell).
	shellScanMax = 1 << 20
	// shellOutCap bounds how much of one command's output a shell-mode
	// block keeps. Measured reason (see capShellOutput): a block's WHOLE
	// text is hashed on every keystroke by blockKey, and the block only
	// ever *shows* its first 400 display cells, so storing megabytes buys
	// nothing visible and costs every later keystroke.
	shellOutCap = 2000
	// shellEnvVal is the "$SHELL" token of the /settings "Shell binary" row
	// (Pitago group): follow the login shell from the environment. It is
	// also the default when the pref is unset.
	shellEnvVal = "$SHELL"
)

// resolveShellBinary picks the program that backs shell mode:
//   - a prefs override wins ("/bin/bash", ...), so a user can force one
//     shell regardless of how pitago was started;
//   - shellEnvVal (the default) is the user's login shell — the one whose
//     PATH and builtins they expect, which is the point of a shell escape
//     that keeps cd/env around;
//   - an unset, stale or non-runnable $SHELL degrades to "sh" instead of
//     failing every command. A wrong *override* is not silently corrected:
//     LookPath only proves the program is runnable, and a shell that then
//     fails to start is reported per command (see shellRun).
func resolveShellBinary(pref string) string {
	bin := strings.TrimSpace(pref)
	if bin == "" || bin == shellEnvVal {
		bin = strings.TrimSpace(os.Getenv("SHELL"))
	}
	if bin != "" {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return "sh"
}

// SetShellBinary persists the /settings "Shell binary" row and re-resolves
// the program for the next command. The shell already running is left
// alone: swapping it under an in-flight line would strand that line's
// output, so the new binary takes effect from the next (re)start — Esc
// and back if you want it now.
func (m *Model) SetShellBinary(pref string) {
	pref = strings.TrimSpace(pref)
	m.shellBinPref = pref
	m.shellBin = resolveShellBinary(pref)
	prefs := LoadPrefs(m.prefsPath)
	prefs.ShellBinary = pref
	_ = SavePrefs(m.prefsPath, prefs)
}

// ShellBinaryPref is the stored spelling of the "Shell binary" prefs row
// ("" = follow $SHELL). The /settings row cycles on this, not on the
// resolved program: resolving can only be read, and a row that displayed
// what the environment happened to hold would lose its position in the
// cycle on the next open.
func (m Model) ShellBinaryPref() string { return m.shellBinPref }

// shellProc is one long-lived `sh`. It owns the write end of the child's
// stdin and a line channel fed by a reader goroutine, so the child never
// blocks on a full pipe while pitago is between commands.
type shellProc struct {
	mu    sync.Mutex // serializes writes into the shell's stdin
	cmd   *exec.Cmd
	bin   string // the program this shell runs (decides the marker spelling)
	in    *os.File
	lines chan string // closed by the reader goroutine when the shell exits
	gen   int         // shell generation this process belongs to
	// exited is set once the child has been reaped; dead() reads it
	// instead of probing the output channel, which would steal a pending
	// output line from the command that is still running.
	exited atomic.Bool
}

func startShell(bin, dir string, gen int) (*shellProc, error) {
	cmd := exec.Command(bin)
	cmd.Dir = dir // the session cwd, so the shell starts where the user is
	// Own pipes rather than StdinPipe/StdoutPipe: exec closes the ones it
	// creates inside Wait, which would race the reader goroutine on a
	// process that outlives a single Wait by design.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW // one stream, in order: stderr is not reordered around stdout
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	inR.Close()  // the child holds its own copy now
	outW.Close() // ditto: the reader goroutine owns the read end
	p := &shellProc{cmd: cmd, bin: bin, in: inW, lines: make(chan string, 512), gen: gen}
	go p.read(outR)
	go func() {
		_ = cmd.Wait()
		p.exited.Store(true)
	}()
	return p, nil
}

// read forwards the shell's output line by line until the shell exits. The
// close of the channel is the exit signal every pending run waits on.
func (p *shellProc) read(r *os.File) {
	defer close(p.lines)
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), shellScanMax)
	for sc.Scan() {
		p.lines <- sc.Text()
	}
}

// dead reports that the shell exited (its output channel is closed).
func (p *shellProc) dead() bool { return p.exited.Load() }

// run writes one line into the persistent shell and collects everything up to
// this run's marker. The marker carries the exit status of the user's line,
// not of the shell, so a failing command is reported as failing.
func (p *shellProc) run(gen int, command string) shellOutMsg {
	marker := fmt.Sprintf("%s%d__", shellMarkerPrefix, gen)
	// printf, not echo: the marker must be a line of its own, and the
	// leading newline keeps output that lacked a trailing newline from
	// being glued to the sentinel.
	payload := command + "\n" + p.markerCmd(marker)
	p.mu.Lock()
	_, err := io.WriteString(p.in, payload)
	p.mu.Unlock()
	if err != nil {
		return shellOutMsg{gen: gen, command: command, err: fmt.Errorf("shell is not running: %w", err)}
	}
	out := make([]string, 0, 32)
	for line := range p.lines {
		if tail, ok := strings.CutPrefix(line, marker+" "); ok {
			code, _ := strconv.Atoi(strings.TrimSpace(tail))
			return shellOutMsg{
				gen:     gen,
				out:     strings.TrimRight(strings.Join(out, "\n"), "\n"),
				command: command,
				code:    code,
			}
		}
		out = append(out, line)
	}
	// The channel closed: the shell died under the command ("exit", or a
	// killed pid). Output collected so far still belongs on screen.
	return shellOutMsg{
		gen:     gen,
		out:     strings.TrimRight(strings.Join(out, "\n"), "\n"),
		command: command,
		err:     fmt.Errorf("shell exited before the command finished (it restarts on the next line)"),
	}
}

// markerCmd is the sentinel line that closes one command's output, in the
// dialect of the shell actually running. "$?" is the POSIX spelling of the
// last exit status; fish spells it "$status", and there an unknown "$?"
// would leave the status argument empty — every exit code would decode as
// 0 and a failing command would render as a success.
func (p *shellProc) markerCmd(marker string) string {
	verb := "$?"
	if filepath.Base(p.bin) == "fish" {
		verb = "$status"
	}
	return fmt.Sprintf("printf '\\n%%s %%d\\n' %s %s\n", marker, verb)
}

// close kills the shell. It is safe to call twice (Esc then quit).
func (p *shellProc) close() {
	_ = p.in.Close()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// shellOutMsg is the result of one shell line. proc is handed back to the
// model so the (possibly just-started) shell is kept for the next line; the
// generation guards results from a shell that has since been replaced.
type shellOutMsg struct {
	gen     int
	proc    *shellProc
	command string
	out     string
	code    int
	err     error
}

// enterShellMode latches shell mode from the editor's "!" key.
func (m *Model) enterShellMode() {
	if m.shellOn {
		return
	}
	m.shellOn = true
	m.ta.Reset()
	m.ta.Placeholder = shellPlaceholder
	m.Status = "shell mode — Esc to exit"
	m.histIdx = -1 // pi's message history is not this editor's history
	m.exitTray()   // a tray left open would eat the keys shell mode owns
	m.closeAt()
	m.refreshCmds()
	m.Refresh()
}

// exitShellMode leaves shell mode and kills the shell process. Bumping the
// generation orphans any command still in flight: its result is dropped
// instead of landing in the transcript after the mode is gone.
func (m *Model) exitShellMode() {
	if !m.shellOn {
		return
	}
	// A RECALLED command is dropped with the mode: it was never typed
	// here, and leaving it in the editor would let the next Enter send
	// shell syntax to pi as a prompt. Hand-typed text stays, the same way
	// pi's idle Esc leaves the editor alone.
	if m.shellHistBrowsing() {
		m.shellHistIdx = -1
		m.ta.Reset()
	}
	m.shellOn = false
	m.shellRunning = false
	m.shellGen++
	m.ta.Placeholder = promptPlaceholder
	m.stopShell()
	m.Status = "ready"
	m.closeAt()
	m.refreshCmds()
	m.Refresh()
}

// stopShell kills the persistent shell, if any. Called on quit as well as on
// exit, so a leaked `sh` never outlives pitago.
func (m *Model) stopShell() {
	if m.shell != nil {
		m.shell.close()
		m.shell = nil
	}
}

// submitShell runs the editor line in the persistent shell. Nothing here
// goes to pi: no prompt, no steer, no bash RPC, no session entry.
func (m *Model) submitShell() tea.Cmd {
	command := strings.TrimSpace(m.ta.Value())
	if command == "" {
		return nil
	}
	m.ta.Reset()
	m.closeAt()
	m.refreshCmds()
	if m.shellRunning {
		// One line at a time: interleaving two commands would make the
		// marker boundary meaningless (their output would mix).
		m.AddBlock(Block{Kind: "notice", Text: "a shell command is still running — wait for it to finish"})
		m.Refresh()
		return nil
	}
	m.pushShellHist(command)
	m.shellRunning = true
	m.Status = "$ " + Short(command, 60) + "…"
	m.Refresh()
	return m.shellRun(command)
}

// shellRun is the tea.Cmd half of submitShell: it starts the shell on demand
// and blocks on the marker until the line is done.
func (m *Model) shellRun(command string) tea.Cmd {
	gen := m.shellGen
	cwd := m.cwd
	prev := m.shell
	// Captured by value: the Cmd runs off the event loop, where reading m
	// would race the model it was built from (same rule as the other Cmd
	// closures in this package).
	bin := m.shellBin
	return func() tea.Msg {
		p := prev
		if p == nil || p.dead() {
			np, err := startShell(bin, cwd, gen)
			if err != nil {
				return shellOutMsg{gen: gen, command: command, err: fmt.Errorf("cannot start shell: %w", err)}
			}
			p = np
		}
		out := p.run(gen, command)
		out.proc = p
		return out
	}
}

// handleShellOut renders one shell result as a chat block.
func (m Model) handleShellOut(msg shellOutMsg) (tea.Model, tea.Cmd) {
	// Stale: the shell was replaced (Esc then a new "!", or a quit) while
	// this line was running. Its output belongs to a mode that is gone.
	if !m.shellOn || msg.gen != m.shellGen {
		if msg.proc != nil {
			msg.proc.close()
		}
		return m, nil
	}
	m.shellRunning = false
	if msg.err != nil {
		if msg.proc != nil {
			msg.proc.close()
		}
		m.shell = nil
		m.AddBlock(Block{Kind: "notice", Text: msg.err.Error(), Err: true})
		m.Status = "shell mode — Esc to exit"
		m.Refresh()
		return m, nil
	}
	m.shell = msg.proc // keep the shell alive for the next line
	// Strip escape sequences at the boundary, not in the view: a local
	// command colours itself whenever it thinks it has a terminal ("ls
	// --color", "git diff --color", "grep --color"), and the raw bytes
	// would land in the transcript and then be fed to the chroma lexer
	// that renders bash blocks (view.go) — colour on colour. pi's own
	// bash output gets the same treatment where it enters
	// (update.go bashExecution, pi_ops.go bashResult).
	out := capShellOutput(stripANSI(msg.out))
	text := "$ " + msg.command
	if out != "" {
		text += "\n" + out
	}
	m.AddBlock(Block{Kind: "bash", Text: text, Err: msg.code != 0})
	m.Status = "shell mode — Esc to exit"
	m.Refresh()
	return m, nil
}

// Shell-mode input history (↑↓ recall), kept apart from the sent-message
// history in history.go. Same reason pi's own editor keeps them apart: a
// recalled message is a prompt for the model, a recalled command is
// something to run — and Enter in shell mode RUNS the line, so handing it
// a recalled prompt would execute English as a shell command.
//
// Recall only happens while the line is empty, and only while mouse
// reporting is on (a plain ↑↓ with reporting off is a wheel scroll — the
// same rule, and for the same reason, as history.go). Shift+↑↓ recalls in
// both modes.

// shellHistBrowsing reports a live shell-history browse (shellHistIdx is
// the only marker; the slice is empty while shell mode is off).
func (m *Model) shellHistBrowsing() bool {
	return m.shellHistIdx >= 0 && m.shellHistIdx < len(m.shellHist)
}

// pushShellHist records a submitted command, deduping an immediate repeat
// and capping the list at the same bound as sent-message history.
func (m *Model) pushShellHist(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	if n := len(m.shellHist); n > 0 && m.shellHist[n-1] == command {
		return
	}
	m.shellHist = append(m.shellHist, command)
	if len(m.shellHist) > histMax {
		m.shellHist = m.shellHist[len(m.shellHist)-histMax:]
	}
}

// setShellHist shows shellHist[shellHistIdx] in the editor.
func (m *Model) setShellHist() {
	m.ta.SetValue(m.shellHist[m.shellHistIdx])
	m.closeAt()
	m.refreshCmds()
	m.Refresh()
}

// tryShellHistPrev handles ↑ in shell mode: the previous command, staying
// at the oldest. True = consumed (even at the oldest, so ↑ cannot fall
// through to the chat scroll on every extra press).
func (m *Model) tryShellHistPrev() bool {
	if len(m.shellHist) == 0 {
		return false
	}
	if m.shellHistBrowsing() {
		if m.shellHistIdx > 0 {
			m.shellHistIdx--
			m.setShellHist()
		}
		return true
	}
	if strings.TrimSpace(m.ta.Value()) != "" || strings.Contains(m.ta.Value(), "\n") {
		return false
	}
	m.shellHistIdx = len(m.shellHist) - 1
	m.setShellHist()
	return true
}

// tryShellHistNext handles ↓ in shell mode: newer, and past the newest
// back to the empty live line.
func (m *Model) tryShellHistNext() bool {
	if len(m.shellHist) == 0 {
		return false
	}
	if m.shellHistBrowsing() {
		if m.shellHistIdx < len(m.shellHist)-1 {
			m.shellHistIdx++
			m.setShellHist()
		} else {
			m.shellHistIdx = -1
			m.ta.Reset()
			m.closeAt()
			m.refreshCmds()
			m.Refresh()
		}
		return true
	}
	if strings.TrimSpace(m.ta.Value()) != "" || strings.Contains(m.ta.Value(), "\n") {
		return false
	}
	m.shellHistIdx = len(m.shellHist) - 1
	m.setShellHist()
	return true
}

// capShellOutput trims a command's output to shellOutCap bytes, on a rune
// boundary.
//
// Why the cap exists, measured on a 5-block transcript (one keystroke =
// Update + View, ns/op, this tree before the fix):
//
//	block bytes stored   1 KB    40 KB   400 KB   2 MB
//	per keystroke      1.74 ms  1.93 ms 4.83 ms 17.9 ms
//
// The cost is blockKey: the per-block render cache is keyed by an FNV hash
// of every field of the block, so the whole Text is re-hashed on every
// keystroke (80.8% flat in the CPU profile of the 2 MB case, 3.29 ms for
// ONE 2 MB block). Nothing else in the keystroke path scales with the
// stored size — the cache hits, so the blocks are not re-rendered.
//
// 2000 matches the budget pi itself keeps for a bashExecution output
// (update.go), so the local shell stores no more than the remote one, and
// it is invisible: a bash block renders Short(bl.Text, 400) — the first
// 400 display cells — and copy/selection work off the rendered lines.
func capShellOutput(out string) string {
	if len(out) <= shellOutCap {
		return out
	}
	cut := out[:shellOutCap]
	// Never leave half a multi-byte rune behind: DecodeLastRuneInString
	// reports (RuneError, 1) when the tail is not a complete rune.
	if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n…"
}

// piKeyTakenByShell reports whether a key press belongs to one of pi's own
// editor bindings, which must not fire while shell mode owns the input
// box: shell mode is a local tool, so a half-typed command must not toggle
// plan mode, switch the model, or drop a pi dialog on top of the shell.
//
// Deliberately NOT blocked — they are either harmless locally or the only
// way to get a command in or out:
//   - Ctrl+C (clear the line, then double-press to quit: pi parity),
//   - Ctrl+E / Ctrl+G (sidebar and tool-block view toggles),
//   - Ctrl+V (pasting a command is the point),
//   - Alt+M (selecting the shell output needs the mouse),
//   - ↑↓ PgUp PgDn Home End (chat scroll), Esc and Enter (shell mode's).
func piKeyTakenByShell(msg tea.KeyMsg) bool {
	switch msg.Type {
	// Tab toggles plan mode, Ctrl+N/P/R/T are session + model actions, and
	// Ctrl+Y/O open the yank dialogs: none of them belong to a shell line,
	// and a Tab press in particular is near-certain to be accidental.
	case tea.KeyTab, tea.KeyCtrlN, tea.KeyCtrlP, tea.KeyCtrlR, tea.KeyCtrlT,
		tea.KeyCtrlY, tea.KeyCtrlO:
		return true
	}
	if msg.Alt && len(msg.Runes) == 1 {
		// Alt+digit jumps to a recent model, and a hub-assigned Alt key
		// stages a /command in the editor — both rewrite this input box.
		if msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
			return true
		}
		if label, ok := shortcutLabelOf(msg); ok && !shortcutReserved(label) {
			return true
		}
	}
	return false
}
