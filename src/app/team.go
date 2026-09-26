package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	teamDashboardHeading = "Pi Agents Team Dashboard"
	teamSessionStateType = "pi-agent-team/state"
	teamSessionTailBytes = 8 << 20
)

type teamSessionEntry struct {
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`
}

type teamSessionRecord struct {
	Kind     string `json:"kind"`
	WorkerID string `json:"workerId"`
	Worker   struct {
		WorkerID    string `json:"workerId"`
		Profile     string `json:"profileName"`
		Status      string `json:"status"`
		Error       string `json:"error"`
		LastEvent   int64  `json:"lastEventAt"`
		LastSummary struct {
			Headline  string `json:"headline"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"lastSummary"`
	} `json:"worker"`
}

type teamSessionWorker struct {
	Profile   string
	Status    string
	Headline  string
	Error     string
	UpdatedAt int64
}

type teamWorker struct {
	Label    string
	Headline string
	Status   string
	Task     string
	Usage    string
	Group    string
}

type teamGroup struct {
	Label   string
	Workers []teamWorker
}

type teamDashboardData struct {
	Summary string
	Groups  []teamGroup
}

// Pi's TUI opens an overlay for /team. Pi runs as an RPC child inside Pitago,
// so the extension emits its dashboard as a visible custom message instead.
// Recognize that stable first line and present the same worker-first layout.
func isTeamDashboardText(text string) bool {
	line := strings.TrimSpace(stripANSI(text))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line) == teamDashboardHeading
}

func isTeamDetailText(text string) bool {
	line := strings.TrimSpace(stripANSI(text))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line) == "Pi Agents Team Detail"
}

// ------------------------------------------------ /team command lifecycle
//
// /team is pitago's own builtin, not a get_commands row: it Prompt-forwards
// "/team <arg>" and pi's extension answers out-of-band with a custom
// message. A bare forward is therefore invisible — the RPC ack carries no
// body, and when the command is issued mid-turn pi buffers the custom
// message into _pendingCustomMessages and emits nothing until the turn
// ends. ForwardTeamCommand wraps the forward in an observable window:
// a status while in flight, a delivered flag set from the two places the
// custom message actually enters the app, and a bounded watchdog so a
// missing reply is reported instead of swallowed.

const (
	teamCmdName = "/team"

	// Idle deadline. The extension answers synchronously and does not open
	// a model turn, so a round-trip that outlasts this never reached us.
	teamCmdDeadline = 2 * time.Second

	// Mid-turn deadline. A streaming turn makes pi hold the custom message
	// until the turn finishes, so the same command legitimately takes far
	// longer and must not be declared lost early.
	teamCmdDeadlineBusy = 45 * time.Second
)

// Request tokens. The Model struct cannot grow a field from this file, and
// pitago runs exactly one Model, so a package-level token gives the same
// guarantee a field would: teamCmdLive holds the token of the request still
// awaiting delivery (0 = none), and any tick carrying an older token is a
// no-op. Mirrors the existing package-level state in this package
// (marketInflight, marketCacheData in update.go).
var (
	teamCmdSeq  atomic.Uint64
	teamCmdLive atomic.Uint64
)

func nextTeamCmdToken() uint64 { return teamCmdSeq.Add(1) }

// teamWatchdogMsg is the deadline for one dispatched /team. Update routes
// it to handleTeamWatchdog; a tick carrying a stale token is a no-op rather
// than a false notice.
type teamWatchdogMsg struct {
	name  string
	epoch uint64
}

// ForwardTeamCommand dispatches an extension team command with a visible
// lifecycle. text is the full forwarded prompt, e.g. "/team w1".
func (m *Model) ForwardTeamCommand(text string) tea.Cmd {
	tok := nextTeamCmdToken()
	teamCmdLive.Store(tok)
	deadline := teamCmdDeadline
	if m.thinking {
		// DEFECT A: mid-turn, pi defers the custom message until the turn
		// ends, so a short deadline would report a false failure. Say the
		// command is queued and wait long enough for a real turn to land.
		deadline = teamCmdDeadlineBusy
		m.beginExtCmd(teamCmdName, "team queued…")
		m.pushToast("/team queued — the agent is mid-turn, so pi holds the dashboard until the turn finishes", false)
	} else {
		m.beginExtCmd(teamCmdName, "loading team…")
	}
	m.Refresh()
	return tea.Batch(m.forwardExtensionCommandNamed(text, teamCmdName), m.teamWatchdog(tok, deadline))
}

// teamWatchdog is the bounded deadline. It carries the request token so a
// tick that outlives delivery — or one belonging to a superseded request —
// is dropped instead of reporting a phantom failure.
func (m *Model) teamWatchdog(epoch uint64, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return teamWatchdogMsg{name: teamCmdName, epoch: epoch}
	})
}

// handleTeamWatchdog closes a request that delivered nothing. Only the
// current token may close a window, so a stale tick is a no-op.
func (m *Model) handleTeamWatchdog(msg teamWatchdogMsg) {
	if msg.name != teamCmdName {
		return
	}
	if teamCmdLive.Load() != msg.epoch {
		return // delivered, superseded, or never armed
	}
	teamCmdLive.CompareAndSwap(msg.epoch, 0)
	if !m.extCmdPending(msg.name) {
		return
	}
	m.endExtCmd(msg.name, nil, false)
	m.Status = "ready"
	m.teamNoReplyHint()
	m.Refresh()
}

// noteTeamDelivered closes a request whose custom message actually landed.
// Called from the two openers below, which are the only entry points for
// the extension's dashboard and detail messages. Delivery is a silent
// success: endExtCmd(delivered=true) prints nothing.
func (m *Model) noteTeamDelivered() {
	if !m.extCmdPending(teamCmdName) {
		return
	}
	if tok := teamCmdLive.Load(); tok != 0 {
		teamCmdLive.CompareAndSwap(tok, 0)
	}
	m.endExtCmd(teamCmdName, nil, true)
	// endExtCmd leaves the in-flight status in place (it only clears an
	// already-empty one), and no agent turn follows a synchronous command
	// to reset it, so the status is restored here.
	m.Status = "ready"
}

// teamNoReplyHint is DEFECT C: the palette lists /team unconditionally
// because mergeCommands puts builtins first, so a silent failure looks
// identical to "the plugin is not installed". m.Cmds carries what pi
// actually loaded, which is the only honest signal available here.
func (m *Model) teamNoReplyHint() {
	if m.teamPluginLoaded() {
		m.pushToast("pi-agents-team is loaded but did not answer in time — retry when the current turn ends", false)
		return
	}
	m.pushToast("pi-agents-team is not loaded, so /team cannot work — check the packages list in ~/.pi/agent/settings.json, then /reload", true)
}

// teamPluginLoaded reports whether the pi-agents-team extension registered
// any command in this process.
//
// It cannot simply look for an extension-sourced "team": mergeCommands
// (src/app/model.go) puts pitago's own builtin first, and pitago's /team is
// OriginPitago, so the merged list always carries the "pitago" row and the
// extension's "team" row is deduped away. Probing the exact name therefore
// reports "not loaded" even when the plugin is live — the wrong diagnosis
// for the most common failure. The extension also owns the team-* family
// (/team-copy, /team-init, /team-steer, …) which pitago does not shadow, so
// those are the reliable probe. The exact-name check is kept only for a
// future where the shadowing goes away.
func (m *Model) teamPluginLoaded() bool {
	has := func(name string, extOnly bool) bool {
		for _, c := range m.Cmds {
			if !strings.EqualFold(strings.TrimSpace(c.Name), name) {
				continue
			}
			if !extOnly {
				return true
			}
			switch strings.ToLower(strings.TrimSpace(c.Source)) {
			case "extension", "prompt", "skill":
				return true
			}
		}
		return false
	}
	if has("team-copy", true) || has("team-init", true) || has("team-steer", true) {
		return true
	}
	// Whole-family probe: any other team-* row the extension contributed.
	for _, c := range m.Cmds {
		name := strings.ToLower(strings.TrimSpace(c.Name))
		if !strings.HasPrefix(name, "team-") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(c.Source)) {
		case "extension", "prompt", "skill":
			return true
		}
	}
	return has("team", true)
}

func (m *Model) openTeamDashboard(text string) {
	m.noteTeamDelivered()
	text = strings.TrimSpace(text)
	data := parseTeamDashboard(text)
	if reconcileTeamDashboardFromSession(&data, m.sessionFile) {
		text = formatTeamDashboardText(data)
	}
	d := &Dialog{Kind: "team", Title: "Pi Agents Team · /team", Message: text, TeamTab: "workers"}
	if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "team" {
		*m.Dialogs[0] = *d
	} else {
		m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	}
	m.applyPopupH()
	m.Refresh()
}

func (m *Model) openTeamDetail(text string) {
	m.noteTeamDelivered()
	d := &Dialog{Kind: "team", Title: "Pi Agents Team · /team", Message: strings.TrimSpace(text), TeamTab: "inspect", TeamDetail: true}
	if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "team" {
		d.TeamReturnMessage = m.Dialogs[0].Message
		d.TeamReturnCursor = m.Dialogs[0].Cursor
		*m.Dialogs[0] = *d
	} else {
		m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	}
	m.applyPopupH()
	m.Refresh()
}

func normalizeTeamSummary(line string) string {
	parts := strings.Split(line, " · ")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "mode "):
			continue
		case strings.HasPrefix(part, "In progress "):
			part = "Working" + strings.TrimPrefix(part, "In progress")
		case strings.HasPrefix(part, "Completed or idle "):
			part = "Done" + strings.TrimPrefix(part, "Completed or idle")
		}
		out = append(out, part)
	}
	return strings.Join(out, " · ")
}

func teamGroupLabel(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, ")") || !strings.Contains(trimmed, " (") {
		return ""
	}
	open := strings.LastIndex(trimmed, " (")
	label := strings.TrimSpace(trimmed[:open])
	count := strings.TrimSpace(strings.TrimSuffix(trimmed[open+2:], ")"))
	switch label {
	case "Needs reply", "Needs recovery", "In progress", "Completed or idle", "Done":
		if label == "In progress" {
			label = "Working"
		}
		if label == "Completed or idle" {
			label = "Done"
		}
		return label + " (" + count + ")"
	default:
		return ""
	}
}

func parseTeamDashboard(text string) teamDashboardData {
	var data teamDashboardData
	var current *teamWorker
	for _, raw := range strings.Split(strings.TrimSpace(stripANSI(text)), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line == teamDashboardHeading || strings.HasPrefix(line, "Use /team") || strings.HasPrefix(line, "/team opens") {
			continue
		}
		if strings.HasPrefix(line, "workers ") {
			data.Summary = normalizeTeamSummary(line)
			continue
		}
		if label := teamGroupLabel(line); label != "" {
			data.Groups = append(data.Groups, teamGroup{Label: label})
			current = nil
			continue
		}
		if strings.HasPrefix(line, "- ") && len(data.Groups) > 0 {
			body := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			label, headline, hasHeadline := strings.Cut(body, " — ")
			if !hasHeadline {
				label, headline = body, ""
			}
			gi := len(data.Groups) - 1
			worker := teamWorker{Label: label, Headline: headline, Group: data.Groups[gi].Label}
			data.Groups[gi].Workers = append(data.Groups[gi].Workers, worker)
			current = &data.Groups[gi].Workers[len(data.Groups[gi].Workers)-1]
			continue
		}
		if current == nil || !strings.HasPrefix(raw, "  ") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "status: "):
			current.Status = strings.TrimSpace(strings.TrimPrefix(line, "status: "))
		case strings.HasPrefix(line, "task: "):
			current.Task = strings.TrimSpace(strings.TrimPrefix(line, "task: "))
		case strings.HasPrefix(line, "usage: "):
			current.Usage = strings.TrimSpace(strings.TrimPrefix(line, "usage: "))
		}
	}
	if data.Summary == "" {
		data.Summary = "no workers tracked"
	}
	return data
}

func teamWorkers(data teamDashboardData) []teamWorker {
	var out []teamWorker
	for _, group := range data.Groups {
		out = append(out, group.Workers...)
	}
	return out
}

func isDetachedTeamWorkerText(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "active ping returned registry snapshot only") ||
		strings.Contains(text, "worker rpc is not attached")
}

func readTeamSessionWorkers(path string) map[string]teamSessionWorker {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	start := max(int64(0), info.Size()-int64(teamSessionTailBytes))
	buf := make([]byte, info.Size()-start)
	n, _ := file.ReadAt(buf, start)
	buf = buf[:n]
	if start > 0 {
		newline := bytes.IndexByte(buf, '\n')
		if newline < 0 {
			return nil
		}
		buf = buf[newline+1:]
	}

	workers := make(map[string]teamSessionWorker)
	for _, raw := range bytes.Split(buf, []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var entry teamSessionEntry
		if json.Unmarshal(raw, &entry) != nil || entry.CustomType != teamSessionStateType {
			continue
		}
		var record teamSessionRecord
		if json.Unmarshal(entry.Data, &record) != nil || record.Kind != "worker_terminal" {
			continue
		}
		worker := record.Worker
		if worker.WorkerID == "" {
			continue
		}
		// A Pitago RPC process cannot attach to workers owned by another Pi
		// process. Its active ping replaces the durable result with this
		// synthetic warning; never let that warning erase newer session state.
		if isDetachedTeamWorkerText(worker.Error + " " + worker.LastSummary.Headline) {
			continue
		}
		updatedAt := max(worker.LastEvent, worker.LastSummary.UpdatedAt)
		candidate := teamSessionWorker{
			Profile:   worker.Profile,
			Status:    strings.ToLower(strings.TrimSpace(worker.Status)),
			Headline:  strings.TrimSpace(worker.LastSummary.Headline),
			Error:     strings.TrimSpace(worker.Error),
			UpdatedAt: updatedAt,
		}
		if previous, ok := workers[worker.WorkerID]; !ok || candidate.UpdatedAt >= previous.UpdatedAt {
			workers[worker.WorkerID] = candidate
		}
	}
	return workers
}

func baseTeamWorkerStatus(status string) string {
	status = strings.TrimSpace(status)
	if i := strings.Index(status, " ·"); i >= 0 {
		status = status[:i]
	}
	if i := strings.Index(status, " ("); i >= 0 {
		status = status[:i]
	}
	return strings.ToLower(strings.TrimSpace(status))
}

func baseTeamGroup(group string) string {
	group = strings.TrimSpace(group)
	if i := strings.Index(group, " ("); i >= 0 {
		group = group[:i]
	}
	return group
}

func teamWorkerAttentionGroup(worker teamWorker) string {
	previous := baseTeamGroup(worker.Group)
	if previous == "Needs reply" {
		return previous
	}
	status := baseTeamWorkerStatus(worker.Status)
	headline := strings.ToLower(worker.Headline)
	switch status {
	case "created", "starting", "running", "waiting_followup":
		return "Working"
	case "idle", "completed":
		return "Done"
	case "aborted", "error", "exited":
		return "Needs recovery"
	}
	if strings.Contains(headline, "recovery:") {
		return "Needs recovery"
	}
	switch previous {
	case "Needs reply", "Needs recovery", "Working", "Done":
		return previous
	default:
		return "Working"
	}
}

func teamRelayCount(summary string) string {
	for _, part := range strings.Split(summary, " · ") {
		if strings.HasPrefix(strings.TrimSpace(part), "relays ") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "relays "))
		}
	}
	return "0"
}

func rebuildTeamDashboard(data *teamDashboardData, workers []teamWorker) {
	labels := []string{"Needs reply", "Needs recovery", "Working", "Done"}
	grouped := make(map[string][]teamWorker, len(labels))
	for _, worker := range workers {
		worker.Group = teamWorkerAttentionGroup(worker)
		grouped[worker.Group] = append(grouped[worker.Group], worker)
	}
	data.Groups = nil
	counts := make(map[string]int, len(labels))
	for _, label := range labels {
		group := grouped[label]
		if len(group) == 0 {
			continue
		}
		counts[label] = len(group)
		data.Groups = append(data.Groups, teamGroup{Label: fmt.Sprintf("%s (%d)", label, len(group)), Workers: group})
	}
	data.Summary = fmt.Sprintf(
		"workers %d · relays %s · Needs reply %d · Needs recovery %d · Working %d · Done %d",
		len(workers), teamRelayCount(data.Summary), counts["Needs reply"], counts["Needs recovery"], counts["Working"], counts["Done"],
	)
}

func reconcileTeamDashboardFromSession(data *teamDashboardData, sessionFile string) bool {
	sessionWorkers := readTeamSessionWorkers(sessionFile)
	if len(sessionWorkers) == 0 {
		return false
	}
	workers := teamWorkers(*data)
	changed := false
	for i := range workers {
		worker := &workers[i]
		if !isDetachedTeamWorkerText(worker.Headline) {
			continue
		}
		currentStatus := baseTeamWorkerStatus(worker.Status)
		if currentStatus != "exited" && currentStatus != "error" && currentStatus != "aborted" {
			continue
		}
		workerID := teamWorkerID(worker.Label)
		if workerID == "" {
			continue
		}
		persisted, ok := sessionWorkers[workerID]
		if !ok || persisted.Status == "" {
			continue
		}
		if currentStatus == "exited" {
			worker.Status = persisted.Status
		}
		if persisted.Error != "" {
			worker.Headline = "recovery: " + persisted.Error
		} else if persisted.Headline != "" {
			worker.Headline = persisted.Headline
		}
		worker.Group = teamWorkerAttentionGroup(*worker)
		changed = true
	}
	if changed {
		rebuildTeamDashboard(data, workers)
	}
	return changed
}

func formatTeamDashboardText(data teamDashboardData) string {
	lines := []string{teamDashboardHeading, data.Summary}
	for _, group := range data.Groups {
		lines = append(lines, group.Label)
		for _, worker := range group.Workers {
			headline := strings.TrimSpace(worker.Headline)
			if headline == "" {
				headline = "status: " + baseTeamWorkerStatus(worker.Status)
			}
			status := strings.TrimSpace(worker.Status)
			if status == "" {
				status = "unknown"
			}
			lines = append(lines, fmt.Sprintf("- %s — %s", worker.Label, headline))
			lines = append(lines, "  status: "+status)
			if worker.Task != "" {
				lines = append(lines, "  task: "+worker.Task)
			}
			if worker.Usage != "" {
				lines = append(lines, "  usage: "+worker.Usage)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func teamWorkerID(label string) string {
	for _, field := range strings.FieldsFunc(label, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ')' || r == '[' || r == ']' || r == '·' || r == ',' || r == '/' || r == ':'
	}) {
		field = strings.TrimSpace(field)
		if len(field) > 1 && (field[0] == 'w' || field[0] == 'W') {
			if _, err := strconv.Atoi(field[1:]); err == nil {
				return field
			}
		}
	}
	return ""
}

func teamDashboardWindow(winH int) int {
	rows := winH - 21
	if rows < 4 {
		rows = 4
	}
	if rows > 16 {
		rows = 16
	}
	return rows
}

func teamSummaryLines(summary string, width int) []string {
	if lipgloss.Width(summary) <= width {
		return []string{summary}
	}
	workers, relays := "workers", "relays 0"
	reply, recovery, working, done := "0", "0", "0", "0"
	for _, part := range strings.Split(summary, " · ") {
		switch {
		case strings.HasPrefix(part, "workers "):
			workers = part
		case strings.HasPrefix(part, "relays "):
			relays = part
		case strings.HasPrefix(part, "Needs reply "):
			reply = strings.TrimPrefix(part, "Needs reply ")
		case strings.HasPrefix(part, "Needs recovery "):
			recovery = strings.TrimPrefix(part, "Needs recovery ")
		case strings.HasPrefix(part, "Working "):
			working = strings.TrimPrefix(part, "Working ")
		case strings.HasPrefix(part, "Done "):
			done = strings.TrimPrefix(part, "Done ")
		}
	}
	return []string{
		workers + " · " + relays,
		"Needs reply " + reply + " · Needs recovery " + recovery,
		"Working " + working + " · Done " + done,
	}
}

func teamPanelWidth(winW int) int {
	width := winW / 2
	if width < 42 {
		width = 42
	}
	if width > 88 {
		width = 88
	}
	if maxWidth := winW - 2; width > maxWidth {
		width = max(20, maxWidth)
	}
	return width
}

func teamWorkerGlyph(worker teamWorker) string {
	lower := strings.ToLower(worker.Status + " " + worker.Group)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "abort"), strings.Contains(lower, "exit"):
		return "×"
	case strings.Contains(lower, "done"), strings.Contains(lower, "complete"), strings.Contains(lower, "idle"):
		return "✓"
	case strings.Contains(lower, "reply"), strings.Contains(lower, "recovery"):
		return "?"
	default:
		return "▶"
	}
}

func teamTab(d *Dialog) string {
	if d.TeamTab == "" {
		return "workers"
	}
	return d.TeamTab
}

func teamTabsLine(active string) string {
	tabs := []struct{ key, name string }{{"1", "Workers"}, {"2", "Inspect"}, {"3", "Console"}, {"4", "Cost"}}
	parts := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := tab.key + " " + tab.name
		if active == strings.ToLower(tab.name) {
			parts = append(parts, cmdHiStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, statusBarStyle.Render(label))
		}
	}
	return strings.Join(parts, "  ")
}

func teamStatus(worker teamWorker) string {
	status := worker.Status
	if i := strings.Index(status, " ·"); i >= 0 {
		status = status[:i]
	}
	if status == "" {
		status = "unknown"
	}
	return status
}

func teamDetailLines(data teamDashboardData, worker teamWorker, tab string) []string {
	headline := worker.Headline
	if headline == "" {
		headline = worker.Task
	}
	if headline == "" {
		headline = "No headline available"
	}
	switch tab {
	case "inspect":
		return []string{
			"Status [" + teamStatus(worker) + "] —",
			"  " + worker.Label,
			"  Usage: " + fallbackTeamValue(worker.Usage, "unavailable in RPC summary"),
			"  Task: " + fallbackTeamValue(worker.Task, "unavailable in RPC summary"),
			"  Last tool: unavailable in RPC summary",
			"",
			"Recent activity —",
			"  • " + headline,
		}
	case "console":
		lines := []string{"Worker console", "  Raw diagnostics are available in Pi native TUI mode.", ""}
		for _, current := range teamWorkers(data) {
			lines = append(lines, fmt.Sprintf("  %s %s · %s", teamWorkerGlyph(current), current.Label, teamStatus(current)))
		}
		return lines
	case "cost":
		lines := []string{"Worker cost", "  " + data.Summary, ""}
		for _, current := range teamWorkers(data) {
			lines = append(lines, fmt.Sprintf("  %s · %s", current.Label, fallbackTeamValue(current.Usage, "usage unavailable")))
		}
		return lines
	default:
		return []string{"No tracked workers."}
	}
}

func fallbackTeamValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func renderTeamTabBody(m Model, d *Dialog, data teamDashboardData, workers []teamWorker, contentW int) string {
	tab := teamTab(d)
	if len(workers) == 0 {
		return toolStyle.Render("No tracked workers.")
	}
	if tab == "workers" {
		visibleWorkers := teamDashboardWindow(m.winH)
		maxStart := max(0, len(workers)-visibleWorkers)
		start := max(0, min(d.TrajOff, maxStart))
		if d.Cursor < start {
			start = d.Cursor
		}
		if d.Cursor >= start+visibleWorkers {
			start = d.Cursor - visibleWorkers + 1
		}
		start = max(0, min(start, maxStart))
		end := min(len(workers), start+visibleWorkers)
		d.TrajOff = start
		var b strings.Builder
		lastGroup := ""
		for i := start; i < end; i++ {
			worker := workers[i]
			if worker.Group != lastGroup {
				b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(runewidth.Truncate(worker.Group, contentW, "…")) + "\n")
				lastGroup = worker.Group
			}
			row := fmt.Sprintf("%s %s · %s", teamWorkerGlyph(worker), worker.Label, worker.Headline)
			if i == d.Cursor {
				b.WriteString(rowHiStyle.Width(contentW).Render("▸ "+runewidth.Truncate(row, contentW-2, "…")) + "\n")
			} else {
				b.WriteString(runewidth.Truncate(row, contentW, "…") + "\n")
			}
		}
		return strings.TrimSuffix(b.String(), "\n")
	}

	selected := workers[d.Cursor]
	lines := teamDetailLines(data, selected, tab)
	visible := teamDashboardWindow(m.winH)
	maxStart := max(0, len(lines)-visible)
	start := max(0, min(d.TrajOff, maxStart))
	if d.TeamFollow {
		start = maxStart
	}
	end := min(len(lines), start+visible)
	d.TrajOff = start
	var b strings.Builder
	b.WriteString(runewidth.Truncate("scroll "+fmt.Sprint(start+1)+"-"+fmt.Sprint(end)+" / "+fmt.Sprint(len(lines)), contentW, "…") + "\n")
	for _, line := range lines[start:end] {
		if strings.HasPrefix(line, "Status") || strings.HasPrefix(line, "Recent") || strings.HasPrefix(line, "Worker cost") {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(runewidth.Truncate(line, contentW, "…")) + "\n")
		} else {
			b.WriteString(runewidth.Truncate(line, contentW, "…") + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderFocusedTeamDetail(m Model, d *Dialog, data teamDashboardData, worker teamWorker, contentW int) string {
	headline := worker.Headline
	if headline == "" {
		headline = fallbackTeamValue(worker.Task, "No headline available")
	}
	status := teamStatus(worker)
	reusable := ""
	if strings.EqualFold(status, "idle") || strings.EqualFold(status, "completed") {
		reusable = " [reusable]"
	}
	follow := "[paused f/G]"
	if d.TeamFollow {
		follow = "[follow on]"
	}
	lines := []string{
		"Worker detail · " + worker.Label + " " + follow,
		"Status [" + status + "] —",
		"  " + worker.Label + reusable,
		"  Usage: " + fallbackTeamValue(worker.Usage, "unavailable in RPC summary"),
		"  Thinking: unavailable in RPC summary",
		"  Last tool: unavailable in RPC summary",
		"",
		"Recent activity —",
		"  • Thinking: " + headline,
		"  • Task: " + fallbackTeamValue(worker.Task, "unavailable in RPC summary"),
		"  • Usage: " + fallbackTeamValue(worker.Usage, "unavailable in RPC summary"),
		"",
		"Task —",
		"  " + fallbackTeamValue(worker.Task, "unavailable in RPC summary"),
		"",
		"Summary —",
		"  Headline: " + headline,
	}
	visible := teamDashboardWindow(m.winH)
	maxStart := max(0, len(lines)-visible)
	start := max(0, min(d.TrajOff, maxStart))
	if d.TeamFollow {
		start = maxStart
	}
	end := min(len(lines), start+visible)
	d.TrajOff = start
	var b strings.Builder
	for _, line := range lines[start:end] {
		if strings.HasPrefix(line, "Worker detail") || strings.HasPrefix(line, "Status") || strings.HasPrefix(line, "Recent") || strings.HasPrefix(line, "Task —") || strings.HasPrefix(line, "Summary —") {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(runewidth.Truncate(line, contentW, "…")) + "\n")
		} else {
			b.WriteString(runewidth.Truncate(line, contentW, "…") + "\n")
		}
	}
	b.WriteString("\n" + toolStyle.Render("Esc back · r refresh · q close"))
	return strings.TrimSuffix(b.String(), "\n")
}

func renderRawTeamDetail(m Model, d *Dialog, contentW int) string {
	lines := strings.Split(strings.TrimSpace(stripANSI(d.Message)), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "Pi Agents Team Detail" {
		lines = lines[1:]
	}
	var wrapped []string
	for _, line := range lines {
		parts := strings.Split(lipgloss.NewStyle().Width(contentW).Render(line), "\n")
		wrapped = append(wrapped, parts...)
	}
	visible := teamDashboardWindow(m.winH)
	maxStart := max(0, len(wrapped)-visible)
	start := max(0, min(d.TrajOff, maxStart))
	if d.TeamFollow {
		start = maxStart
	}
	end := min(len(wrapped), start+visible)
	d.TrajOff = start
	var b strings.Builder
	b.WriteString(toolStyle.Render(fmt.Sprintf("scroll %d-%d / %d", start+1, end, len(wrapped))) + "\n")
	for _, line := range wrapped[start:end] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Status") || strings.HasPrefix(trimmed, "Recent activity") || strings.HasPrefix(trimmed, "Task") || strings.HasPrefix(trimmed, "Summary") || strings.HasPrefix(trimmed, "Final answer") || strings.HasPrefix(trimmed, "Latest assistant") {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(runewidth.Truncate(line, contentW, "…")) + "\n")
		} else {
			b.WriteString(runewidth.Truncate(line, contentW, "…") + "\n")
		}
	}
	b.WriteString("\n" + toolStyle.Render("Esc back · r refresh · q close"))
	return strings.TrimSuffix(b.String(), "\n")
}

func (m Model) renderTeamDashboardPanel(d *Dialog, panelW int) string {
	contentW := max(20, panelW-8)
	if d.TeamDetail && isTeamDetailText(d.Message) {
		body := renderRawTeamDetail(m, d, contentW)
		return dlgStyle.Width(panelW).Render(runewidth.Truncate(d.Title+" · detail", contentW, "…") + "\n" + body)
	}
	data := parseTeamDashboard(d.Message)
	workers := teamWorkers(data)
	d.Cursor = max(0, min(d.Cursor, max(0, len(workers)-1)))
	d.TeamTab = teamTab(d)
	if d.TeamDetail && len(workers) > 0 {
		body := renderFocusedTeamDetail(m, d, data, workers[d.Cursor], contentW)
		return dlgStyle.Width(panelW).Render(runewidth.Truncate(d.Title+" · detail", contentW, "…") + "\n" + body)
	}

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(runewidth.Truncate(d.Title, contentW, "…")) + "\n")
	b.WriteString(runewidth.Truncate(teamTabsLine(d.TeamTab), contentW, "…") + "\n")
	for _, summary := range teamSummaryLines(data.Summary, contentW) {
		b.WriteString(runewidth.Truncate(summary, contentW, "…") + "\n")
	}
	b.WriteString(runewidth.Truncate("↑↓ scroll · f follow · space/b page · g/G top/bottom · 1-4 tabs", contentW, "…") + "\n")
	if len(workers) > 0 {
		selected := workers[d.Cursor]
		b.WriteString(runewidth.Truncate("selected: "+selected.Label+" · "+selected.Status, contentW, "…") + "\n")
		if selected.Headline != "" {
			b.WriteString(runewidth.Truncate("headline: "+selected.Headline, contentW, "…") + "\n")
		}
	}
	b.WriteString("\n" + renderTeamTabBody(m, d, data, workers, contentW))
	if d.TeamTab != "workers" {
		follow := "[paused f/G]"
		if d.TeamFollow {
			follow = "[follow on]"
		}
		b.WriteString("\n" + toolStyle.Render(follow))
	}
	b.WriteString("\n" + toolStyle.Render("Enter · 1-4 tabs · r refresh · q close"))
	return dlgStyle.Width(panelW).Render(strings.TrimSuffix(b.String(), "\n"))
}

// ansiWindow returns a display-cell window while preserving escape sequences,
// so overlaying one side does not strip colors from code on the other side.
func ansiWindow(s string, start, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x1b[0m")
	cells, kept := 0, 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[':
				j := i + 2
				for j < len(s) && !isCSIEnd(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			case ']':
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		cellWidth := lipgloss.Width(string(r))
		if cells < start {
			cells += cellWidth
			i += size
			continue
		}
		if kept+cellWidth > width {
			break
		}
		b.WriteRune(r)
		cells += cellWidth
		kept += cellWidth
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String() + strings.Repeat(" ", width-kept)
}

func (m Model) renderFloatingTeamDashboard() string {
	d := m.Dialogs[0]
	baseModel := m
	baseModel.Dialogs = nil
	baseLines := strings.Split(baseModel.View(), "\n")
	panelW := teamPanelWidth(m.winW)
	panelLines := strings.Split(m.renderTeamDashboardPanel(d, panelW), "\n")
	inputH := 6 + m.chipH()
	available := len(baseLines) - 1 - inputH
	if available <= 0 {
		return strings.Join(baseLines, "\n")
	}
	if len(panelLines) > available {
		panelLines = panelLines[:available]
	}
	for i, panelLine := range panelLines {
		row := 1 + i
		if row >= len(baseLines) {
			break
		}
		left := ansiWindow(baseLines[row], 0, m.winW-panelW)
		baseLines[row] = left + truncANSI(panelLine, panelW)
	}
	return strings.Join(baseLines, "\n")
}

func (m Model) updateTeamDashboardDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	workers := teamWorkers(parseTeamDashboard(d.Message))
	page := teamDashboardWindow(m.winH)
	switch km.String() {
	case "enter":
		if len(workers) > 0 {
			if workerID := teamWorkerID(workers[d.Cursor].Label); workerID != "" {
				m.Refresh()
				return m, m.ForwardExtensionCommand("/pitago-team-detail " + workerID)
			}
			d.TeamDetail = true
			d.TeamTab = "inspect"
			d.TrajOff = 0
		}
	case "1":
		d.TeamTab, d.TrajOff = "workers", 0
	case "2":
		d.TeamTab, d.TrajOff = "inspect", 0
	case "3":
		d.TeamTab, d.TrajOff = "console", 0
	case "4":
		d.TeamTab, d.TrajOff = "cost", 0
	case "up", "k":
		if teamTab(d) == "workers" {
			d.Cursor = max(0, d.Cursor-1)
		} else {
			d.TrajOff--
		}
	case "down", "j":
		if teamTab(d) == "workers" {
			d.Cursor = min(max(0, len(workers)-1), d.Cursor+1)
		} else {
			d.TrajOff++
		}
	case "pgup", "b":
		d.TrajOff -= page
	case "pgdown", " ":
		d.TrajOff += page
	case "g", "home":
		d.TrajOff = 0
	case "G", "end":
		d.TrajOff = int(^uint(0) >> 1)
	case "f":
		d.TeamFollow = !d.TeamFollow
	case "esc":
		if d.TeamDetail {
			if d.TeamReturnMessage != "" {
				returnMessage := d.TeamReturnMessage
				returnCursor := d.TeamReturnCursor
				*d = Dialog{Kind: "team", Title: "Pi Agents Team · /team", Message: returnMessage, TeamTab: "workers", Cursor: returnCursor}
				m.applyPopupH()
				m.Refresh()
			} else {
				m.Dialogs = m.Dialogs[1:]
				m.applyPopupH()
				m.Refresh()
			}
			return m, nil
		}
		m.Dialogs = m.Dialogs[1:]
		m.applyPopupH()
		m.Refresh()
		return m, nil
	case "c", "q":
		m.Dialogs = m.Dialogs[1:]
		m.applyPopupH()
		m.Refresh()
		return m, nil
	case "r":
		m.Dialogs = m.Dialogs[1:]
		m.applyPopupH()
		// Re-fetch the dashboard through the same lifecycle as the command
		// itself, so a refresh that stalls is reported instead of blank.
		return m, m.ForwardTeamCommand("/team")
	}
	d.TrajOff = max(0, d.TrajOff)
	return m, nil
}
