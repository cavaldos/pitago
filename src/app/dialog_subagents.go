package app

// Centered /subagents management overlay (Phase 2). Native pitago dialog
// (Kind "subagent-herd") over the Phase-1 data layer (m.Subagents): list with
// active/finished scopes + type-to-filter, detail pane with activity header
// + session transcript tail, and model-mediated manage actions (the overlay
// composes prompts that invoke the model's existing subagent-family tools —
// no pi-RPC protocol changes).
//
// Keymap follows /sessions + /pitago-setting: ↑↓ select, Tab scope,
// type-to-filter, Enter/→ open, Esc/← back. Action letters dispatch on the
// focused row: x stop · w wait · R resume · X dismiss · f surface · s steer
// (each gated on row state by subagentActions — letters that cannot run
// stay hidden and never dispatch).

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// subagentsScopeActive / Finished are the Dialog.Scope values (Tab toggles).
const (
	subagentsScopeActive   = "active"
	subagentsScopeFinished = "finished"
)

// subagentsDetailSizes parity with pi-agents/footer.ts: overlay width presets cycled
// with ^O in the detail pane.
var subagentsDetailSizes = []struct{ pad, maxH int }{
	{pad: 10, maxH: 0}, // adaptive (renderDialog default: winW-10, 62..100)
	{pad: 6, maxH: 6},  // wide
	{pad: 2, maxH: 2},  // near-fullscreen
}

// subagentResultViewChars bounds how much of a result the detail pane
// shows. Enough for a review verdict and its findings; the rest is in the
// session file, and the pane scrolls.
const subagentResultViewChars = 4000

// subagentResultWrapCols is the fixed wrap width for the detail pane's
// result section. The pane scrolls horizontally-independently, so one
// width serves every pane size and a long report reads the same everywhere.
const subagentResultWrapCols = 400

// wrapSubagentText splits a subagent result into display lines: hard wraps
// at the given width and keeps blank lines, so a report reads as a report.
// Wrapping is rune-aware — a byte slice would split UTF-8 (emoji in a
// review, a box-drawing character) into mojibake.
func wrapSubagentText(text string, maxChars int) []string {
	if maxChars <= 0 {
		return nil
	}
	// Truncate on runes, not bytes: a multi-byte result would otherwise be
	// cut early (and mid-character) long before the requested char count.
	runes := []rune(text)
	if len(runes) > maxChars {
		runes = append(runes[:maxChars:maxChars], '…')
	}
	var out []string
	for _, raw := range strings.Split(string(runes), "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			out = append(out, "")
			continue
		}
		for r := []rune(line); len(r) > 0; {
			n := min(len(r), subagentResultWrapCols)
			out = append(out, string(r[:n]))
			r = r[n:]
		}
	}
	return out
}

// subagentRowByID finds a live row by its dialog payload ID.
func subagentRowByID(m Model, id string) (SubagentRow, bool) {
	for _, r := range m.Subagents {
		if r.ID == id {
			return r, true
		}
	}
	return SubagentRow{}, false
}

// subagentsInScope splits rows into the Tab scopes: active holds
// starting/active/waiting, finished holds done/stalled/error.
func subagentsInScope(rows []SubagentRow, scope string) []SubagentRow {
	var out []SubagentRow
	for _, r := range rows {
		finished := r.Status == SubagentDone || r.Status == SubagentStalled || r.Status == SubagentError
		if scope == subagentsScopeFinished && finished {
			out = append(out, r)
		} else if scope != subagentsScopeFinished && !finished {
			out = append(out, r)
		}
	}
	return out
}

// subagentsRowDesc is the right-hand description per row.
func subagentsRowDesc(r SubagentRow) string {
	end := r.StartedAt
	done := false
	if r.DoneAt != nil {
		end = *r.DoneAt
		done = true
	}
	el := formatSubagentElapsed(end.Sub(r.StartedAt).Milliseconds())
	bits := []string{}
	if r.StatusLabel != "" {
		bits = append(bits, r.StatusLabel)
	} else {
		bits = append(bits, string(r.Status))
	}
	bits = append(bits, el)
	if r.AgentName != "" {
		bits = append(bits, r.AgentName)
	}
	if done && r.Result != "" {
		bits = append(bits, firstLine(r.Result, 60))
	} else if r.Task != "" {
		bits = append(bits, firstLine(r.Task, 60))
	}
	return strings.Join(bits, " · ")
}

// rebuildSubagentsOptions re-derives the dialog list from m.Subagents,
// preserving the focused row by ID across live reordering.
func rebuildSubagentsOptions(m Model, d *Dialog) {
	sel := ""
	if len(d.Payload) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		if ri := d.FIdx[d.Cursor]; ri >= 0 && ri < len(d.Payload) {
			sel = d.Payload[ri]
		}
	}
	rows := subagentsInScope(m.Subagents, d.Scope)
	d.Options = make([]string, 0, len(rows))
	d.Descs = make([]string, 0, len(rows))
	d.Payload = make([]string, 0, len(rows))
	for _, r := range rows {
		d.Options = append(d.Options, r.Name)
		d.Descs = append(d.Descs, subagentsRowDesc(r))
		d.Payload = append(d.Payload, r.ID)
	}
	d.Reindex()
	if sel != "" {
		for i, fi := range d.FIdx {
			if d.Payload[fi] == sel {
				d.Cursor = i
				break
			}
		}
	}
	if d.Cursor >= len(d.FIdx) {
		d.Cursor = len(d.FIdx) - 1
	}
	if d.Cursor < 0 {
		d.Cursor = 0
	}
}

// OpenSubagentHerd opens the centered management overlay. It is the live
// view of running subagents, kept separate from OpenSubagents (the
// ext-backed picker that selects the current subagent): that one owns the
// "/subagents" command and dialog kind "subagents", so the herd takes its
// own kind and its own key routing rather than shadowing the picker.
func (m *Model) OpenSubagentHerd() {
	m.refreshSubagents(false)
	d := &Dialog{
		Kind:  "subagent-herd",
		Title: "Subagents",
		Scope: subagentsScopeActive,
	}
	m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	rebuildSubagentsOptions(*m, d)
	m.applyPopupH()
	m.Refresh()
}

// openSubagentsDetail flips the dialog into detail mode for one row:
// activity header + cached transcript lines for scrolling.
func openSubagentsDetail(m Model, d *Dialog, row SubagentRow) {
	d.SubDetail = true
	d.SubID = row.ID
	d.SubOffset = 0
	end := row.StartedAt
	if row.DoneAt != nil {
		end = *row.DoneAt
	}
	head := []string{string(row.Status)}
	if row.StatusLabel != "" {
		head = []string{row.StatusLabel}
	}
	head = append(head, formatSubagentElapsed(end.Sub(row.StartedAt).Milliseconds()))
	if row.AgentName != "" {
		head = append(head, row.AgentName)
	}
	if row.Surface != "" {
		head = append(head, row.Surface)
	}
	d.SubHeader = strings.Join(head, " · ")
	var lines []string
	if row.Task != "" {
		lines = append(lines, "task: "+row.Task)
	}
	if row.Result != "" {
		// A subagent result is usually a multi-line report. Keep the
		// heading on its own line and wrap the body, so the pane is
		// readable instead of one horizontally clipped line.
		lines = append(lines, "── result ──")
		lines = append(lines, wrapSubagentText(row.Result, subagentResultViewChars)...)
	}
	if row.SessionFile != "" {
		if transcript := readSubagentTranscript(row.SessionFile, subagentTailBytes); len(transcript) > 0 {
			lines = append(lines, "─ transcript ─")
			lines = append(lines, transcript...)
		} else {
			lines = append(lines, "─ transcript ─", "(no readable transcript yet)")
		}
	} else if row.Result == "" {
		lines = append(lines, "(no transcript yet)")
	}
	if len(lines) == 0 {
		lines = []string{"(no transcript yet)"}
	}
	d.SubLines = lines
	m.applyPopupH()
}

// subagentActionLabels is the canonical letter order for hints/dispatch.
var subagentActionLabels = []struct{ key, label string }{
	{"x", "stop"}, {"w", "wait"}, {"R", "resume"}, {"X", "dismiss"}, {"f", "surface"}, {"s", "steer"},
}

// subagentActions returns the action letters that can actually run on a row.
// Stop/steer need a live running row; wait additionally reports a live done
// row's stored result; resume needs a settled row (live rows resume by tool
// ID, disk rows by session file — active rows steer, never resume); dismiss
// and surface are always available.
func subagentActions(row SubagentRow) string {
	live := !strings.HasPrefix(row.ID, "disk-")
	running := row.Status == SubagentStarting || row.Status == SubagentActive || row.Status == SubagentWaiting
	settled := row.Status == SubagentDone || row.Status == SubagentStalled || row.Status == SubagentError
	var b strings.Builder
	if live && running {
		b.WriteString("xw")
	} else if live && row.Status == SubagentDone {
		b.WriteString("w")
	}
	if settled && (live || row.SessionFile != "") {
		b.WriteString("R")
	}
	b.WriteString("Xf")
	if subagentSteerable(row) {
		b.WriteString("s")
	}
	return b.String()
}

// subagentCan reports whether an action letter runs on a row. Letters are
// single ASCII bytes; anything else never matches.
func subagentCan(row SubagentRow, action string) bool {
	return len(action) == 1 && strings.Contains(subagentActions(row), action)
}

// subagentActionHints renders the available letters for the detail footer.
// A pane-backed X dismisses and closes the tab, so it says so.
func subagentActionHints(row SubagentRow) string {
	avail := subagentActions(row)
	parts := []string{}
	running := row.Status == SubagentStarting || row.Status == SubagentActive || row.Status == SubagentWaiting
	for _, a := range subagentActionLabels {
		if !strings.Contains(avail, a.key) {
			continue
		}
		label := a.label
		if a.key == "X" && row.Surface != "" && (running || row.Interactive) {
			label = "dismiss+close"
		}
		parts = append(parts, a.key+" "+label)
	}
	parts = append(parts, "Esc back")
	return strings.Join(parts, " · ")
}

// subagentActionPrompt composes the model-mediated prompt per action letter.
func subagentActionPrompt(action string, row SubagentRow) (string, bool) {
	ref := fmt.Sprintf("\"%s\" (%s)", row.Name, row.ID)
	switch action {
	case "x":
		// Pane runs execute out-of-band: subagent_interrupt returns success
		// without reaching the Orca pane. Verified live: `terminal send
		// --interrupt` is accepted but cannot break a pane blocked inside
		// a tool call (its TUI isn't reading input mid-tool), so the
		// reliable stop is SIGINT on the tool PID — the tool aborts
		// (exit 130) and the pane survives.
		if row.Interactive || row.Surface != "" {
			// Host-agnostic stop: SIGINT on the tool PID works everywhere.
			// A pane host's interrupt is only a polite first try, and only
			// mentioned when the owning host actually binds one. Resolve
			// the host only when a handle actually exists, so an
			// in-process row never pays for an availability probe.
			first := ""
			if row.Surface != "" {
				if ctl := surfaceCtlForHandle(row.Surface); ctl != nil && ctl.hints.interrupt != nil {
					first = ctl.hints.interrupt(row.Surface)
				}
			}
			return fmt.Sprintf("Subagent %s is an interactive pane run — subagent_interrupt will not reach it. Stop it WITHOUT killing the pane. %sA pane blocked inside a tool call cannot hear interrupts at all: list processes (`ps aux`), match the full command line to that subagent's task (never PID 1 or unrelated system processes), and run `kill -INT <pid>` (SIGINT, never -9) so only the tool aborts and the pane survives. Then report the subagent's status.", ref, first), true
		}
		return fmt.Sprintf("Use the subagent_interrupt tool on subagent %s now.", ref), true
	case "w":
		return fmt.Sprintf("Use the subagent_wait tool on subagent %s and report its result.", ref), true
	case "R":
		return fmt.Sprintf("Use the subagent_resume tool on subagent %s and continue its task.", ref), true
	case "X":
		// Only reachable for a pane row with no recorded handle: find it,
		// then close it. Never close a handle you have not matched to this
		// subagent, and never bulk-close the workspace.
		ctl := surfaceCtlForHandle(row.Surface)
		if ctl == nil || ctl.hints.closePane == "" {
			// No pane host installed (plain terminal, Warp, Ghostty), or the
			// owning host binds no close-by-discovery flow: the dismissal
			// already happened and there is nothing left to drive.
			return fmt.Sprintf("Subagent %s was dismissed locally. No installed pane host can close that pane here, so the row is gone from the list and the run's session file still holds its transcript.", ref), true
		}
		return fmt.Sprintf("Subagent %s runs in a pane but its handle was not recorded. Find the matching pane and close it: %s. Close only the matched pane — never bulk-close the workspace and never kill processes. Report which handle you closed.", ref, ctl.hints.closePane), true
	case "s":
		return "", false // steer opens the nested input dialog instead
	}
	return "", false
}

// orcaTermDoneMsg reports an `orca terminal` call made for a herd action.
// Handlers must treat any error as "pane untouched" and say so.
type orcaTermDoneMsg struct {
	action, handle, label string
	host                  string
	hints                 surfaceHints
	err                   error
}

// surfaceCtl abstracts "control the subagent's surface" so pane hosts are a
// table, not a fork. Today only Orca implements it; every other terminal
// (plain shell, Warp, Ghostty, cmux without a pane host) resolves to nil and
// callers fall back to host-agnostic behavior. available() and run() are
// separate so "can I drive panes?" can never be confused with "do it".
// surfaceHints is a host's own vocabulary for the model's prompts. Keeping
// the grammar here stops Orca's verbs leaking into a cmux row's text, and
// lets a host that binds no equivalent leave the field empty.
type surfaceHints struct {
	interrupt func(handle string) string // polite first try for stopping a pane
	list      string                     // how to discover a handle
	closePane string                     // how to close a matched pane
}

type surfaceCtl struct {
	name      string
	available func() error
	claim     func(handle string) bool // does this handle address a surface of mine?
	// precheck reports whether an action can run on a handle, WITHOUT
	// spawning anything. The herd calls it before it dismisses a row, so a
	// handle this host cannot address never leaves an orphaned pane behind a
	// dismissed row. nil means "no precheck" — the host will decide at run
	// time instead.
	precheck func(action, handle string) error
	// currentHandle names the surface THIS process is running in, for rows
	// that have no handle of their own — an in-process child shares the
	// parent's terminal, so "where is this row" has a real answer even
	// though no pane was spawned for it. nil means the host cannot say.
	//
	// FOCUS ONLY. This is the user's own window, so a row action must never
	// close it; the herd uses it to focus and nothing else.
	currentHandle func() (string, error)
	run           func(action, handle string) error
	hints         surfaceHints
}

// surfaceControllers is read through a factory so tests can swap impls.
// Overridable in tests.
var surfaceControllers = func() []surfaceCtl {
	return []surfaceCtl{cmuxSurfaceCtl(), orcaSurfaceCtl()}
}

// activeSurfaceCtl returns the first controller that can drive panes, or nil
// when no pane host is installed (plain terminal, Warp, Ghostty).
func activeSurfaceCtl() *surfaceCtl {
	for _, c := range surfaceControllers() {
		if c.available == nil || c.run == nil {
			continue
		}
		if c.available() != nil {
			continue
		}
		found := c
		return &found
	}
	return nil
}

// activeSurfaceCtlNamingSurface is activeSurfaceCtl restricted to hosts that
// can actually name the surface this process is in. The capability check
// comes FIRST so a host without it is never probed: available() can cost a
// subprocess, and asking every installed host a question we already know the
// answer to is a per-keypress cost that grows with the table.
func activeSurfaceCtlNamingSurface() *surfaceCtl {
	for _, c := range surfaceControllers() {
		if c.currentHandle == nil || c.available == nil || c.run == nil {
			continue
		}
		if c.available() != nil {
			continue
		}
		found := c
		return &found
	}
	return nil
}

// surfaceCtlForHandle resolves which host owns a handle. Hosts use
// different handle namespaces (Orca `term_<uuid>`, cmux a uuid or a
// `window:1/workspace:2/pane:3` ref), so ownership is claimed by the host,
// not inferred from a prefix. An unavailable host is skipped rather than
// vetoing the rest of the table; only when no available host claims the
// handle is the answer nil, and the caller degrades.
func surfaceCtlForHandle(handle string) *surfaceCtl {
	if handle == "" {
		return activeSurfaceCtl()
	}
	for _, c := range surfaceControllers() {
		if c.claim == nil || c.available == nil || c.run == nil {
			continue
		}
		if !c.claim(handle) {
			continue
		}
		if c.available() != nil {
			continue // right namespace, but the host is not usable here
		}
		found := c
		return &found
	}
	return nil
}

// orcaSurfaceCtl is the Orca binding: handles are `term_<uuid>`.
func orcaSurfaceCtl() surfaceCtl {
	return surfaceCtl{
		name:      "orca",
		available: orcaAvailable,
		claim:     func(handle string) bool { return strings.HasPrefix(handle, "term_") },
		run:       orcaTermRun,
		precheck:  func(string, string) error { return nil }, // Orca handles are self-describing
		hints: surfaceHints{
			interrupt: func(handle string) string {
				return fmt.Sprintf("First try `orca terminal send --terminal %s --interrupt --json` (works only if the pane sits at a prompt — verify with `orca terminal read`: the turn Elapsed must freeze). If Elapsed keeps counting, skip to the signal. ", handle)
			},
			list:      "locate its pane with `orca terminal list --json` (match worktree/task to this subagent)",
			closePane: "run `orca terminal list --json`, match a terminal to this subagent (same worktree; preview/title mentions the task), then `orca terminal close --terminal <handle> --json`",
		},
	}
}

// orcaAvailable reports whether Orca's CLI is installed.
var orcaAvailable = func() error {
	if _, err := exec.LookPath("orca"); err != nil {
		return errNoSurfaceCtl
	}
	return nil
}

// orcaTermRun executes `orca terminal <action> --terminal <handle>`.
// Overridable in tests.
var orcaTermRun = func(action, handle string) error {
	if err := orcaAvailable(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "orca", "terminal", action, "--terminal", handle, "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s terminal %s: %w (%s)", "orca", action, err, firstLine(string(out), 300))
	}
	return nil
}

// orcaTermCmd runs the controller call off the UI thread. The controller is
// resolved by the caller and passed in, so one keypress probes once.
func (m Model) orcaTermCmd(ctl *surfaceCtl, action, handle, label string) tea.Cmd {
	return func() tea.Msg {
		msg := orcaTermDoneMsg{action: action, handle: handle, label: label}
		if ctl == nil {
			msg.err = errNoSurfaceCtl
			return msg
		}
		msg.host = ctl.name
		msg.hints = ctl.hints
		msg.err = ctl.run(action, handle)
		return msg
	}
}

// errNoSurfaceCtl marks "no pane host installed": callers degrade to the
// host-agnostic path instead of reporting a broken feature.
var errNoSurfaceCtl = errors.New("no pane host available (actions that need a pane are unavailable here)")

// runSubagentAction closes the overlay and sends the action prompt (or opens
// the nested steer input for "s"). Focus ("f") and dismiss ("X") act on the
// Orca pane natively via `orca terminal switch|close`; pi-agents
// auto-dismisses finished rows on the next user turn anyway.
func (m Model) runSubagentAction(d *Dialog, row SubagentRow, action string) (tea.Model, tea.Cmd) {
	switch action {
	case "f":
		m.Dialogs = m.Dialogs[1:]
		handle := row.Surface
		// The host that produced a handle is the host that drives it. When
		// the handle comes from currentHandle it is pinned to that host, so
		// a second host claiming the same namespace cannot take the action
		// over; a recorded handle still routes by owner, as before.
		var owner *surfaceCtl
		if handle == "" {
			// No pane was spawned for this row — an in-process child shares
			// the parent's terminal. A host that can name its own surface
			// still gives us something real to focus, so ask before giving
			// up. Never used to close: this is the user's own window.
			if ctl := activeSurfaceCtlNamingSurface(); ctl != nil {
				if h, err := ctl.currentHandle(); err == nil && h != "" {
					handle, owner = h, ctl
				}
			}
		}
		if handle == "" {
			// No pane, and no host that can say where we are: already at the
			// parent term, just say so.
			m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s → in-process (no pane)", row.Name)})
			m.Refresh()
			return m, m.ReconcileTurnCmd()
		}
		fctl := owner
		if fctl == nil {
			fctl = surfaceCtlForHandle(handle)
		}
		if fctl == nil {
			// No installed host owns this handle: either the pane host is
			// not installed (plain terminal, Warp, Ghostty) or no binding
			// claims this namespace. Focus cannot cross processes here.
			m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s → pane %s (no installed host can focus this pane)", row.Name, handle)})
			m.Refresh()
			return m, m.ReconcileTurnCmd()
		}
		if fctl.precheck != nil {
			if err := fctl.precheck("switch", handle); err != nil {
				m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s → %s", row.Name, err), Err: true})
				m.Refresh()
				return m, m.ReconcileTurnCmd()
			}
		}
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s → focusing pane…", row.Name)})
		m.Refresh()
		return m, tea.Batch(m.ReconcileTurnCmd(), m.orcaTermCmd(fctl, "switch", handle, fmt.Sprintf("%s → pane focused", row.Name)))
	case "X":
		// X has two jobs: stop tracking the row, and close the pane. The
		// first is bookkeeping and always succeeds; the second needs a host
		// that can actually address this handle. So the row is dismissed
		// either way — a host that cannot close the pane must not leave the
		// row stuck in the herd with no key that can remove it — and the
		// precheck runs first so the notice can say truthfully that the pane
		// was left open, without spawning a call that cannot succeed.
		m.dismissSubagentRow(row)
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s dismissed — will not rescan", row.Name)})
		running := row.Status == SubagentStarting || row.Status == SubagentActive || row.Status == SubagentWaiting
		m = m.backToHerdList(d)
		m.Refresh()
		if row.Surface == "" && !row.Interactive {
			// In-process row: nothing to drive. Return before resolving a
			// host — availability can cost a subprocess, and a dismissal
			// must stay instant.
			return m, m.ReconcileTurnCmd()
		}
		if !running && !row.Interactive {
			// A finished pane whose tab already went with it.
			return m, m.ReconcileTurnCmd()
		}
		// Resolve the host ONCE: available() can be a real subprocess (cmux
		// pings), and two pings per keypress is a cost the user pays.
		ctl := surfaceCtlForHandle(row.Surface)
		if row.Surface == "" && ctl != nil {
			// Pane row with no recorded handle (e.g. rebuilt from disk
			// state, which never saw the launch result): resolve the
			// handle at press time instead of leaving the pane behind.
			if text, ok := subagentActionPrompt("X", row); ok {
				return m, m.sendCmd(m.thinking, text, nil)
			}
			return m, m.ReconcileTurnCmd()
		}
		if row.Surface == "" || ctl == nil {
			// No handle recorded, or no installed host owns it.
			return m, m.ReconcileTurnCmd()
		}
		if ctl.precheck != nil {
			if err := ctl.precheck("close", row.Surface); err != nil {
				m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s → pane left open: %s", row.Name, err), Err: true})
				m.Refresh()
				return m, m.ReconcileTurnCmd()
			}
		}
		return m, tea.Batch(m.ReconcileTurnCmd(), m.orcaTermCmd(ctl, "close", row.Surface, fmt.Sprintf("%s → pane closed", row.Name)))
	case "s":
		if !subagentSteerable(row) {
			m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s is not a live steerable subagent", row.Name)})
			m.Refresh()
			return m, nil
		}
		sd := &Dialog{
			Kind:   "subagents-steer",
			Title:  fmt.Sprintf("Steer %s", row.Name),
			SubID:  row.ID,
			Filter: "",
		}
		m.Dialogs = append([]*Dialog{sd}, m.Dialogs...)
		m.applyPopupH()
		m.Refresh()
		return m, nil
	default:
		text, ok := subagentActionPrompt(action, row)
		if !ok {
			return m, nil
		}
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.sendCmd(m.thinking, text, nil)
	}
}

// backToHerdList leaves the detail pane and returns to the herd list,
// keeping the overlay open for the next action. Detail actions (dismiss,
// steer) are per-row bookkeeping, not a reason to drop the user back
// into the chat transcript. Falls back to closing the overlay when the
// caller has no herd dialog (or is already in list mode).
func (m Model) backToHerdList(d *Dialog) Model {
	if d == nil || len(m.Dialogs) == 0 || m.Dialogs[0] != d {
		if len(m.Dialogs) > 0 {
			m.Dialogs = m.Dialogs[1:]
		}
		return m
	}
	if !d.SubDetail {
		m.Dialogs = m.Dialogs[1:]
		return m
	}
	d.SubDetail = false
	d.SubID = ""
	d.SubHeader = ""
	d.SubLines = nil
	d.SubOffset = 0
	rebuildSubagentsOptions(m, d)
	m.applyPopupH()
	return m
}

// focusedSubagentRow resolves the dialog cursor to a live row.
func focusedSubagentRow(m Model, d *Dialog) (SubagentRow, bool) {
	if len(d.FIdx) == 0 || d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return SubagentRow{}, false
	}
	ri := d.FIdx[d.Cursor]
	if ri < 0 || ri >= len(d.Payload) {
		return SubagentRow{}, false
	}
	return subagentRowByID(m, d.Payload[ri])
}

// updateSubagentsDialog navigates the overlay (list + detail) and dispatches
// row actions. List mode refreshes its options from live state on every key
// so spawns completing behind the dialog appear without reopening.
func (m Model) updateSubagentsDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	// Nested steer input: typing + submit/cancel only.
	if d.Kind == "subagents-steer" {
		switch km.Type {
		case tea.KeyEsc:
			m.Dialogs = m.Dialogs[1:]
			m.applyPopupH()
			m.Refresh()
			return m, nil
		case tea.KeyEnter:
			text := strings.TrimSpace(d.Filter)
			id := d.SubID
			// Steer is per-row bookkeeping: drop the steer box and land
			// back on the herd list (or the row's detail pane) rather
			// than the chat transcript.
			var sd *Dialog
			for i, open := range m.Dialogs {
				if open.Kind == "subagents-steer" {
					sd = m.Dialogs[i]
					m.Dialogs = append(m.Dialogs[:i:i], m.Dialogs[i+1:]...)
					break
				}
			}
			if text == "" {
				if sd != nil && sd.SubID == id {
					for _, open := range m.Dialogs {
						if open.Kind == "subagent-herd" && open.SubID == id {
							open.SubDetail = true
							open.SubOffset = 0
							row, ok := subagentRowByID(m, id)
							if ok {
								openSubagentsDetail(m, open, row)
							}
							m.applyPopupH()
						}
					}
				}
				m.Refresh()
				return m, nil
			}
			// Non-empty submit: leave detail behind and land on the list.
			if sd != nil {
				for _, open := range m.Dialogs {
					if open.Kind == "subagent-herd" && open.SubDetail {
						m = m.backToHerdList(open)
						break
					}
				}
			}
			row, ok := subagentRowByID(m, id)
			name := id
			if ok {
				name = fmt.Sprintf("\"%s\" (%s)", row.Name, row.ID)
			}
			m.Refresh()
			return m, m.sendCmd(m.thinking, fmt.Sprintf("Send the following message to subagent %s via your subagent message path:\n\n%s", name, text), nil)
		case tea.KeyBackspace:
			if d.Filter != "" {
				r := []rune(d.Filter)
				d.Filter = string(r[:len(r)-1])
				m.applyPopupH()
			}
			return m, nil
		case tea.KeyRunes:
			d.Filter += string(km.Runes)
			m.applyPopupH()
			return m, nil
		case tea.KeySpace:
			d.Filter += " "
			m.applyPopupH()
			return m, nil
		}
		if km.Type == tea.KeyCtrlV {
			return m, m.pasteCmd(true)
		}
		return m, nil
	}
	// Detail mode: scroll + size + actions, no filter.
	if d.SubDetail {
		row, ok := subagentRowByID(m, d.SubID)
		switch km.Type {
		case tea.KeyEsc:
			d.SubDetail = false
			d.SubID = ""
			rebuildSubagentsOptions(m, d)
			m.applyPopupH()
			m.Refresh()
			return m, nil
		case tea.KeyUp:
			d.SubOffset += 5
			return m, nil
		case tea.KeyDown:
			if d.SubOffset > 0 {
				d.SubOffset -= 5
				if d.SubOffset < 0 {
					d.SubOffset = 0
				}
			}
			return m, nil
		case tea.KeyPgUp:
			d.SubOffset += 20
			return m, nil
		case tea.KeyPgDown:
			if d.SubOffset > 0 {
				d.SubOffset -= 20
				if d.SubOffset < 0 {
					d.SubOffset = 0
				}
			}
			return m, nil
		case tea.KeyCtrlO:
			d.SubSize = (d.SubSize + 1) % len(subagentsDetailSizes)
			m.applyPopupH()
			m.Refresh()
			return m, nil
		case tea.KeyRunes:
			if !ok {
				return m, nil
			}
			switch a := string(km.Runes); a {
			case "x", "w", "R", "X", "f", "s":
				if !subagentCan(row, a) {
					label := a
					for _, al := range subagentActionLabels {
						if al.key == a {
							label = al.label
						}
					}
					m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s unavailable for %s — %s", label, row.Name, row.Status)})
					m.Refresh()
					return m, nil
				}
				return m.runSubagentAction(d, row, a)
			}
			return m, nil
		}
		return m, nil
	}
	// List mode.
	rebuildSubagentsOptions(m, d)
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
	case tea.KeyBackspace:
		if d.Filter != "" {
			r := []rune(d.Filter)
			d.Filter = string(r[:len(r)-1])
			d.Reindex()
			m.applyPopupH()
		}
		return m, nil
	case tea.KeyTab:
		if d.Scope == subagentsScopeActive {
			d.Scope = subagentsScopeFinished
		} else {
			d.Scope = subagentsScopeActive
		}
		d.Cursor = 0
		rebuildSubagentsOptions(m, d)
		m.applyPopupH()
		m.Refresh()
		return m, nil
	case tea.KeyEsc:
		m.Dialogs = m.Dialogs[1:]
		m.refreshPiTasks()
		m.refreshSubagents(false)
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	case tea.KeyEnter:
		if row, ok := focusedSubagentRow(m, d); ok {
			openSubagentsDetail(m, d, row)
			m.Refresh()
		}
		return m, nil
	case tea.KeyRunes:
		// List mode is filter-only: action letters live in detail view.
		// ("x" in "explore" must filter, not interrupt.)
		d.Filter += string(km.Runes)
		d.Reindex()
		m.applyPopupH()
		return m, nil
	case tea.KeySpace:
		d.Filter += " "
		d.Reindex()
		m.applyPopupH()
		return m, nil
	}
	return m, nil
}

// renderSubagentsDialog draws the overlay: list mode (rows + scope footer)
// or detail mode (activity header + scrollable transcript).
func (m Model) renderSubagentsDialog(d *Dialog) string {
	// Nested steer input: compact prompt box.
	if d.Kind == "subagents-steer" {
		var b strings.Builder
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
		b.WriteString(statusBarStyle.Render("message to send (empty cancels)") + "\n\n")
		b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
		b.WriteString("\n" + toolStyle.Render("Enter send · Esc cancel"))
		box := dlgStyle.Width(60).Render(b.String())
		hint := ""
		if len(m.Dialogs) > 1 {
			hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
		}
		return lipgloss.JoinVertical(lipgloss.Center,
			lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
			hint,
		)
	}
	size := subagentsDetailSizes[0]
	if d.SubSize >= 0 && d.SubSize < len(subagentsDetailSizes) {
		size = subagentsDetailSizes[d.SubSize]
	}
	boxW := m.winW - size.pad
	if boxW < 62 {
		boxW = 62
	}
	if d.SubSize == 0 && boxW > 100 {
		boxW = 100
	}
	var b strings.Builder
	if d.SubDetail {
		row, _ := subagentRowByID(m, d.SubID)
		title := row.Name
		if title == "" {
			title = "Subagent"
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(title) + "\n")
		if d.SubHeader != "" {
			b.WriteString(statusBarStyle.Render(d.SubHeader) + "\n")
		}
		b.WriteString("\n")
		win := m.winH - 12 - size.maxH
		if win < 6 {
			win = 6
		}
		if win > 40 {
			win = 40
		}
		lines := d.SubLines
		total := len(lines)
		// SubOffset counts lines hidden below the window (0 = tail
		// visible); ↑/PgUp raise it toward older lines, ↓/PgDn lower it.
		maxOff := total - win
		if maxOff < 0 {
			maxOff = 0
		}
		off := d.SubOffset
		if off > maxOff {
			off = maxOff
		}
		if off < 0 {
			off = 0
		}
		start := total - win - off
		if start < 0 {
			start = 0
		}
		end := start + win
		if end > total {
			end = total
		}
		if start > 0 {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
		}
		rowW := boxW - 6
		for _, ln := range lines[start:end] {
			b.WriteString(statusBarStyle.Render(Short(ln, rowW)) + "\n")
		}
		if end < total {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)) + "\n")
		}
		b.WriteString("\n" + toolStyle.Render("↑↓/PgUp/PgDn scroll · ^O size · "+subagentActionHints(row)))
	} else {
		active := subagentsInScope(m.Subagents, subagentsScopeActive)
		finished := subagentsInScope(m.Subagents, subagentsScopeFinished)
		scopeName := "active"
		count := len(active)
		other := len(finished)
		if d.Scope == subagentsScopeFinished {
			scopeName = "finished"
			count = len(finished)
			other = len(active)
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(
			fmt.Sprintf("Subagents — %s (%d)", scopeName, count)) + "\n")
		if d.Filter != "" {
			b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
		} else {
			b.WriteString(statusBarStyle.Render(fmt.Sprintf("Tab: %s (%d) · type to filter", otherScopeName(d.Scope), other)) + "\n")
		}
		b.WriteString("\n")
		rowW := boxW - 10
		win := 12
		if h := m.winH - 12; h > win {
			win = h
		}
		if win > 20 {
			win = 20
		}
		total := len(d.FIdx)
		start := d.Cursor - 4
		if start < 0 {
			start = 0
		}
		if start+win > total {
			start = total - win
		}
		if start < 0 {
			start = 0
		}
		end := start + win
		if end > total {
			end = total
		}
		if start > 0 {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
		}
		for fi := start; fi < end; fi++ {
			ri := d.FIdx[fi]
			cursor := "  "
			style := statusBarStyle
			if fi == d.Cursor {
				cursor = "▸ "
				style = rowHiStyle
			}
			row := Short(d.Options[ri], 44)
			if desc := DescOf(d, ri); desc != "" {
				row += "  " + toolStyle.Render("— "+Short(desc, rowW-47))
			}
			if fi == d.Cursor {
				b.WriteString(cursor + style.Width(rowW).Render(row) + "\n")
			} else {
				b.WriteString(cursor + style.Render(row) + "\n")
			}
		}
		if end < total {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)) + "\n")
		}
		if total == 0 {
			b.WriteString(toolStyle.Render("— no subagents —") + "\n")
		}
		b.WriteString("\n" + toolStyle.Render("↑↓ select · Enter open · Tab scope · Esc close · (row actions inside)"))
	}
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

func otherScopeName(scope string) string {
	if scope == subagentsScopeFinished {
		return "active"
	}
	return "finished"
}
