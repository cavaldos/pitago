package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/ext"

	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"pitago/src/pirpc"
	"sort"
	"time"
)

// SubagentsNone — canonical value lives in ext (pi-extension domain).
const SubagentsNone = ext.SubagentsNone

// Progress notifications are emitted by the subagent integrations. These
// are running-state updates, not transient confirmations, so the UI keeps
// them in the conversation transcript.
const (
	agentTeamName           = "pi-agent-team"
	agentTeamWidgetName     = "agent-team"
	subagentAsyncWidget     = "subagent-async"
	subagentProgressPrefix  = "[" + subagentAsyncWidget + "]"
	agentTeamProgressPrefix = "[" + agentTeamName + "]"
)

// isSubagentProgressMessage recognizes the notification markers used by
// the subagent integrations. Strip ANSI first because extensions may colorize
// the prefix before sending it over RPC.
func isSubagentProgressMessage(message string) bool {
	message = strings.TrimSpace(stripANSI(message))
	return strings.HasPrefix(message, subagentProgressPrefix) ||
		strings.HasPrefix(message, agentTeamProgressPrefix)
}

// isTeamWidget recognizes both agent-team widget names used by the
// extension. Match case-insensitively; ordinary plugin keys never qualify.
func isTeamWidget(key string) bool {
	switch strings.ToLower(strings.TrimSpace(stripANSI(key))) {
	case agentTeamWidgetName, agentTeamName:
		return true
	default:
		return false
	}
}

// isAgentProgressWidget recognizes widget protocols that remain transcript
// blocks. The team widget is handled separately as a live editor dashboard.
func isAgentProgressWidget(key string) bool {
	return strings.EqualFold(strings.TrimSpace(stripANSI(key)), subagentAsyncWidget)
}

// setTeamWidget replaces the live team state. An empty line list clears it.
func (m *Model) setTeamWidget(lines []string, placement string) {
	// A widget frame is authoritative Pi output. Keep the snapshot and all
	// escape sequences intact; rendering owns width and overflow decisions.
	m.TeamWidgetLines = append(m.TeamWidgetLines[:0], lines...)
	m.TeamWidgetPlacement = strings.TrimSpace(placement)
	if !strings.EqualFold(m.TeamWidgetPlacement, "belowEditor") {
		m.TeamWidgetPlacement = "aboveEditor"
	}
	if len(m.TeamWidgetLines) == 0 {
		// A clear starts a new widget cycle. The next update is a first
		// widget and should show unless the user hides it again.
		m.TeamWidgetSeen = false
		m.TeamWidgetVisible = true
		m.applyTeamPanelH()
		return
	}
	if !m.TeamWidgetSeen {
		m.TeamWidgetVisible = true
		m.TeamWidgetSeen = true
	}
	// The panel's real height feeds the chat viewport height, and View() is
	// a value receiver, so the sync has to happen here on *Model.
	m.applyTeamPanelH()
}

func (m *Model) clearTeamWidgetState() {
	m.TeamWidgetLines = nil
	m.TeamStatus = ""
	m.TeamWidgetPlacement = ""
	m.TeamWidgetSeen = false
	m.TeamWidgetVisible = true
	// Hand the panel's rows back to the chat on the same sync path that took
	// them, so clearing the dashboard restores the full transcript at once.
	m.applyTeamPanelH()
}

// setTeamStatus captures pi-agents-team status by statusKey. Status keys are
// independent, so unrelated updates use extStat without changing the header.
func (m *Model) setTeamStatus(key, status string) bool {
	if !strings.EqualFold(strings.TrimSpace(stripANSI(key)), agentTeamName) {
		return false
	}
	m.TeamStatus = status
	// The status line paints as a 1-row panel (renderTeamWidget's statusOnly
	// path) and m.vp already reserved the rows for the *widget* panel. Resync
	// through the same chatVpHeight path so the status row is not paid for
	// twice: it is taken from the chat here, not at paint time — where the
	// shrink would silently cut the newest transcript line.
	m.applyTeamPanelH()
	return true
}

// ToggleTeamWidget toggles the live dashboard and reports whether live
// state is available.
func (m *Model) ToggleTeamWidget() bool {
	if len(m.TeamWidgetLines) == 0 {
		return false
	}
	m.TeamWidgetVisible = !m.TeamWidgetVisible
	m.Refresh()
	m.applyTeamPanelH()
	return true
}

// SubagentInfo — canonical type lives in ext; alias keeps call sites green.
type SubagentInfo = ext.SubagentInfo

// DiscoverSubagents lists effective subagents: project > user > package >
// DiscoverSubagents — canonical discovery lives in ext (pi-extension
// domain); this keeps the (cwd) signature used by UI/tests.
func DiscoverSubagents(cwd string) []SubagentInfo {
	return ext.DiscoverSubagents(cwd, piAgentDir())
}

// scanAgentDir — canonical impl in ext.
func scanAgentDir(dir, source string) []SubagentInfo { return ext.ScanAgentDir(dir, source) }

// parseAgentFrontmatter — canonical impl in ext.
func parseAgentFrontmatter(raw string) (name, desc, model string) {
	return ext.ParseAgentFrontmatter(raw)
}

// CurrentSubagent is the last-picked subagent (persisted in prefs.json).
func (m *Model) CurrentSubagent() string {
	if m.CurAgent != "" {
		return m.CurAgent
	}
	return LoadPrefs(m.prefsPath).CurrentSubagent
}

// SetCurrentSubagent persists the picked subagent.
func (m *Model) SetCurrentSubagent(name string) {
	m.CurAgent = name
	prefs := LoadPrefs(m.prefsPath)
	prefs.CurrentSubagent = name
	_ = SavePrefs(m.prefsPath, prefs)
}

// subagentDesc — canonical impl in ext.
func subagentDesc(a SubagentInfo, current string) string { return ext.Desc(a, current) }

// OpenSubagents shows the native subagent picker (/subagents). RPC mode
// can't render pi-subagents' custom UI (ui.custom returns undefined), so
// the extension command silently does nothing — this replaces it. Enter
// selects (see confirmSubagents in src/builtin); /subagents <name> picks
// directly.
func (m *Model) OpenSubagents(arg string) tea.Cmd {
	agents := DiscoverSubagents(m.cwd)
	if len(agents) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no subagents found — pi install npm:pi-subagents"})
		m.Refresh()
		return nil
	}
	cur := m.CurrentSubagent()
	if a := strings.TrimSpace(arg); a != "" {
		switch strings.ToLower(a) {
		case "off", "none", "clear", "--none", "exit", "quit":
			m.SetCurrentSubagent("")
			m.AddBlock(Block{Kind: "notice", Text: "subagent off → back to initial state"})
			m.Refresh()
			return nil
		}
		for _, ag := range agents {
			if strings.EqualFold(ag.Name, a) {
				m.SetCurrentSubagent(ag.Name)
				m.AddBlock(Block{Kind: "notice", Text: "subagent → " + ag.Name + " [" + ag.Source + "]"})
				m.Refresh()
				return nil
			}
		}
		m.AddBlock(Block{Kind: "notice", Text: "subagent '" + a + "' not found", Err: true})
		m.Refresh()
		return nil
	}
	// First row clears the selection (no agent picked).
	noneDesc := "turn off subagent · back to initial state"
	if cur == "" {
		noneDesc = "● current · " + noneDesc
	}
	opts := make([]string, 0, len(agents)+1)
	descs := make([]string, 0, len(agents)+1)
	opts = append(opts, SubagentsNone)
	descs = append(descs, noneDesc)
	for _, ag := range agents {
		opts = append(opts, ag.Name)
		descs = append(descs, subagentDesc(ag, cur))
	}
	d := &Dialog{Kind: "subagents", Title: "Subagents",
		Message: "↑↓ pick · Enter select · type to filter · Esc close · ● = current",
		Options: opts, Descs: descs}
	d.Reindex()
	// Preselect the current agent so it's visible on open.
	for i, ri := range d.FIdx {
		if d.Options[ri] == cur {
			d.Cursor = i
			break
		}
	}
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}

// SubagentStatus mirrors pi-agents' SubagentStatusKind, plus a pitago-local
// error state for tool_execution_end isError.
type SubagentStatus string

const (
	SubagentStarting SubagentStatus = "starting"
	SubagentActive   SubagentStatus = "active"
	SubagentWaiting  SubagentStatus = "waiting"
	SubagentDone     SubagentStatus = "done"
	SubagentStalled  SubagentStatus = "stalled"
	SubagentError    SubagentStatus = "error"
)

// SubagentRow is one sidebar/overlay row.
type SubagentRow struct {
	ID          string // toolCallId (live) or disk-<hex> (artifact scan)
	Name        string // display name from the subagent tool args
	AgentName   string // profile/agent param (general-purpose, explore, …)
	Task        string // short task excerpt from args
	Tool        string // originating tool (subagent, run_agent, …)
	Status      SubagentStatus
	StatusLabel string // activity label / current tool (thinking, read, …)
	Surface     string // Orca pane ref for interactive runs ("" = in-process)
	SessionFile string // child session file when known
	ArtifactDir string
	StartedAt   time.Time
	DoneAt      *time.Time
	Result      string // one-line result summary when done (capped)
	IsError     bool
	Interactive bool // spawned into an Orca pane (keeps running past tool end)
	// Restored marks a row rebuilt from session history at connect. Its
	// StartedAt/DoneAt are the moment of the replay, not the run, so the
	// merge must not use them to decide whether a child session belongs to
	// it — and a matching session is the chance to correct the clock.
	Restored bool
}

const (
	// subagentsShowMax caps sidebar rows like todosShowMax does for todos.
	subagentsShowMax = 8
	// subagentResultCap bounds stored result text (Phase-2 detail reuse).
	subagentResultCap = 4096
	// subagentTailBytes bounds session-file tail reads.
	subagentTailBytes = 64 * 1024
	// subagentStalledAfterMs mirrors pi-agents STALLED_AFTER_MS.
	subagentStalledAfterMs = int64(60_000)
	// subagentStaleActiveMs mirrors pi-agents STALE_ACTIVE_MS.
	subagentStaleActiveMs = int64(300_000)
	// subagentScanTTL mirrors the 1.5s MCP/plugins cache TTL.
	subagentScanTTL = 1500 * time.Millisecond
)

// isSubagentRowTool reports the tools that create rows. Query/control tools
// (subagent_interrupt, subagent_wait, subagents_list, subagent_resume) act
// on existing rows and never create one. Exact match only — unlike
// isTodoTool, substring matching would false-positive on unrelated names.
//
// "fork" is accepted as a family member for forward compatibility. It does not
// fire today: pi's own /fork is a session command rather than a tool and so
// never reaches the toolcall stream, and pi-fork finds its children by scanning
// os.tmpdir()/pi-fork-*/fork.jsonl instead of emitting a tool call. Supporting
// those children needs that scan, not this list.
func isSubagentRowTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "subagent", "run_agent", "run_workflow", "fork":
		return true
	}
	return false
}

// isSubagentTool reports any tool in the subagent family (row creators plus
// control/query tools). Used to trigger sidebar refreshes.
func isSubagentTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "subagent", "subagent_interrupt", "subagent_wait",
		"subagents_list", "subagent_resume", "run_agent", "run_workflow", "fork":
		return true
	}
	return false
}

// parseSubagentArgs extracts display fields from a subagent-family tool
// call's arguments JSON. Missing/foreign shapes yield empty strings.
func parseSubagentArgs(raw json.RawMessage) (name, agent, task string, interactive bool, mode string) {
	if len(raw) == 0 {
		return "", "", "", false, ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", "", false, ""
	}
	name, _ = m["name"].(string)
	agent, _ = m["agent"].(string)
	for _, k := range []string{"task", "prompt", "message"} {
		if s, _ := m[k].(string); strings.TrimSpace(s) != "" {
			task = s
			break
		}
	}
	interactive, _ = m["interactive"].(bool)
	mode, _ = m["mode"].(string)
	return name, agent, task, interactive, mode
}

// formatSubagentElapsed mirrors pi-agents state.ts#formatElapsed (mm:ss).
func formatSubagentElapsed(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	totalSec := ms / 1000
	return fmt.Sprintf("%02d:%02d", totalSec/60, totalSec%60)
}

// subagentActivity is the minimal projection of pi-agents'
// SubagentActivityState (activity.ts) that classification needs.
type subagentActivity struct {
	Phase       string
	ActiveScope string
	ToolName    string
	UpdatedAt   int64
}

// readSubagentActivity parses one
// <artifactDir>/subagent-activity/<runningChildId>.json file.
func readSubagentActivity(path string) (subagentActivity, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return subagentActivity{}, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return subagentActivity{}, false
	}
	a := subagentActivity{}
	a.Phase, _ = m["phase"].(string)
	a.ActiveScope, _ = m["activeScope"].(string)
	a.ToolName, _ = m["toolName"].(string)
	switch t := m["updatedAt"].(type) {
	case float64:
		a.UpdatedAt = int64(t)
	}
	if a.Phase == "" {
		return subagentActivity{}, false
	}
	return a, true
}

// subagentStatusFromActivity mirrors pi-agents state.ts#classifyStatus.
// ok=false means "no activity file" (starting, or stalled when old).
func subagentStatusFromActivity(a subagentActivity, ok bool, startTime, now int64) (SubagentStatus, string) {
	if !ok {
		if now-startTime > subagentStalledAfterMs {
			return SubagentStalled, "stalled"
		}
		return SubagentStarting, "starting"
	}
	switch a.Phase {
	case "done":
		return SubagentDone, ""
	case "waiting":
		return SubagentWaiting, "waiting"
	case "active":
		if a.UpdatedAt > 0 && now-a.UpdatedAt > subagentStaleActiveMs {
			return SubagentStalled, "stale"
		}
		switch a.ActiveScope {
		case "agent", "turn":
			return SubagentActive, "thinking"
		case "provider":
			return SubagentActive, "api"
		case "streaming":
			return SubagentActive, "streaming"
		case "tool":
			if a.ToolName != "" {
				return SubagentActive, a.ToolName
			}
			return SubagentActive, "tool"
		default:
			return SubagentActive, "active"
		}
	default:
		if now-startTime > subagentStalledAfterMs {
			return SubagentStalled, "stalled"
		}
		return SubagentStarting, "starting"
	}
}

// hasSubagentShutdownMarker mirrors pi-agents
// child-sessions.ts#hasShutdownMarker: a session_shutdown entry marks a
// finished pi session.
func hasSubagentShutdownMarker(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	size := st.Size()
	const tail = 128 * 1024
	off := int64(0)
	if size > tail {
		off = size - tail
	}
	buf := make([]byte, size-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && n == 0 {
		return false
	}
	s := string(buf[:n])
	return strings.Contains(s, "session_shutdown")
}

// readSubagentTail reads the last maxBytes of a session file without loading
// it whole (mirrors child-sessions.ts#readTailBytes).
func readSubagentTail(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	size := st.Size()
	if size <= 0 {
		return ""
	}
	if size > maxBytes {
		size = maxBytes
	}
	off := st.Size() - size
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, off)
	if err != nil && n == 0 {
		return ""
	}
	return string(buf[:n])
}

// readSubagentTranscript converts the JSONL tail into readable user-visible
// messages. Session metadata and custom events are intentionally omitted.
func readSubagentTranscript(path string, maxBytes int64) []string {
	tail := readSubagentTail(path, maxBytes)
	if strings.TrimSpace(tail) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Type != "message" {
			continue
		}
		role := event.Message.Role
		if role != "user" && role != "assistant" && role != "toolResult" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(event.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			text := strings.TrimSpace(block.Text)
			if text == "" || (block.Type != "text" && role != "toolResult") {
				continue
			}
			label := role
			if role == "toolResult" {
				label = "tool"
			}
			out = append(out, label+": "+firstLine(text, 500))
		}
	}
	if len(out) > 150 {
		out = out[len(out)-150:]
	}
	return out
}

// subagentSessionStartedAt returns the session header timestamp when present.
func subagentSessionStartedAt(path string, fallback time.Time) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()
	var event struct {
		Timestamp string `json:"timestamp"`
	}
	if json.NewDecoder(f).Decode(&event) == nil {
		if t, err := time.Parse(time.RFC3339Nano, event.Timestamp); err == nil {
			return t
		}
	}
	return fallback
}

// subagentArtifactDir derives pi-agents' per-parent-session artifact dir:
// <dir(sessionFile)>/artifacts/<sessionId>, where sessionId comes from the
// <timestamp>_<id>.jsonl basename (same parse as piTaskSessionID).
func subagentArtifactDir(sessionFile string) string {
	id := piTaskSessionID(sessionFile)
	if id == "" || sessionFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(sessionFile), "artifacts", id)
}

// firstLine shortens result text to a one-line summary.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// upsertSubagentRow inserts or replaces a row by ID, newest-active first.
func upsertSubagentRow(rows []SubagentRow, row SubagentRow) []SubagentRow {
	for i, r := range rows {
		if r.ID == row.ID {
			rows[i] = row
			return rows
		}
	}
	return append([]SubagentRow{row}, rows...)
}

// restoreSubagentsFromMessages replays assistant toolCall blocks from session
// history (mirrors restoreTodos): rows for row-creating tools, marked done
// when a matching toolResult exists. Live events upsert over these.
func restoreSubagentsFromMessages(msgs []pirpc.AgentMessage) []SubagentRow {
	var out []SubagentRow
	seen := map[string]bool{}
	results := map[string]pirpc.AgentMessage{}
	for _, msg := range msgs {
		if msg.Role == "toolResult" && msg.ToolCallID != "" && isSubagentTool(msg.ToolName) {
			results[msg.ToolCallID] = msg
		}
	}
	for _, msg := range msgs {
		if msg.Role != "assistant" || len(msg.Content) == 0 {
			continue
		}
		for _, b := range pirpc.BlocksOf(msg.Content) {
			if !strings.EqualFold(b.Type, "toolCall") || b.ID == "" {
				continue
			}
			if !isSubagentRowTool(b.Name) || seen[b.ID] {
				continue
			}
			seen[b.ID] = true
			name, agent, task, interactive, _ := parseSubagentArgs(b.Arguments)
			if strings.TrimSpace(name) == "" {
				name = "subagent-" + b.ID
				if len(b.ID) > 8 {
					name = "subagent-" + b.ID[:8]
				}
			}
			row := SubagentRow{
				ID:          b.ID,
				Name:        name,
				AgentName:   agent,
				Task:        firstLine(task, 120),
				Tool:        b.Name,
				Status:      SubagentActive,
				StatusLabel: "active",
				Interactive: interactive,
				Restored:    true,
				StartedAt:   time.Now(),
			}
			if res, ok := results[b.ID]; ok {
				done := time.Now()
				row.DoneAt = &done
				row.Status = SubagentDone
				row.StatusLabel = ""
				resText := pirpc.TextOf(res.Content)
				// Pane spawns also resolve on restore: the launch result
				// carries "(surface: term_…)", so X can still close the
				// pane of a row rebuilt from history.
				row.Surface = parseSubagentSurface(resText)
				if res.IsError {
					row.Status = SubagentError
					row.IsError = true
				}
				if t := strings.TrimSpace(resText); t != "" {
					if len(t) > subagentResultCap {
						t = t[:subagentResultCap]
					}
					row.Result = t
				}
			}
			out = append(out, row)
		}
	}
	return out
}

// subagentDiskRecentMs bounds the disk supplement: only unfinished runs
// (no shutdown marker) that are recent (session file touched within a day)
// or live (fresh non-done activity file) surface. Old finished runs without
// markers would otherwise flood the list — history restore already covers
// the current session's finished rows.
const subagentDiskRecentMs = int64(24 * 3600 * 1000)

// subagentTaskKey normalizes a task string for identity matching: first
// line, lowercased, whitespace collapsed, capped length.
func subagentTaskKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(firstLine(s, 400)))
	return strings.Join(strings.Fields(s), " ")
}

// diskSubagentTask reads a child session file's first user message — the
// task the parent dispatched. Live rows carry the same text in Task, which
// is what lets a disk artifact be recognized as the same child instead of
// showing up as a duplicate row. Returns "" when unreadable.
func diskSubagentTask(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if dec.Decode(&event) != nil {
			return ""
		}
		if event.Type != "message" || event.Message.Role != "user" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(event.Message.Content, &blocks) != nil {
			return ""
		}
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
		return ""
	}
}

// scanSubagentArtifacts supplements history rows with disk state: artifact
// dirs (<sessionDir>/artifacts/<parentId>/subagent-*.jsonl + sibling
// subagent-activity/<id>.json, written by pane spawns) and in-process runs
// (<agentDir>/subagent-sessions/*.jsonl). Callers merge with live rows via
// mergeSubagentDisk (live rows carry the real names).
func scanSubagentArtifacts(sessionFile, agentDir string, now time.Time) []SubagentRow {
	known := map[string]bool{}
	var files []string
	if dir := subagentArtifactDir(sessionFile); dir != "" {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				if !strings.HasPrefix(e.Name(), "subagent-") {
					continue
				}
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	if agentDir != "" {
		sdir := filepath.Join(agentDir, "subagent-sessions")
		if entries, err := os.ReadDir(sdir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				files = append(files, filepath.Join(sdir, e.Name()))
			}
		}
	}
	var out []SubagentRow
	for _, f := range files {
		if known[f] {
			continue
		}
		known[f] = true
		base := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		id := strings.TrimPrefix(base, "subagent-")
		if id == "" {
			id = base
		}
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		started := subagentSessionStartedAt(f, st.ModTime())
		startMs := started.UnixMilli()
		nowMs := now.UnixMilli()
		// Activity sibling: <artifactDir>/subagent-activity/<id>.json.
		actPath := filepath.Join(filepath.Dir(f), "subagent-activity", id+".json")
		act, ok := readSubagentActivity(actPath)
		status, label := subagentStatusFromActivity(act, ok, startMs, nowMs)
		if hasSubagentShutdownMarker(f) {
			continue // finished: history restore covers this session
		}
		live := ok && act.Phase != "done" && nowMs-act.UpdatedAt <= subagentStaleActiveMs
		if !live && nowMs-startMs > subagentDiskRecentMs {
			continue // stale run from another session: noise, skip
		}
		row := SubagentRow{
			ID:          "disk-" + id,
			Name:        "subagent-" + id,
			Tool:        "subagent",
			Status:      status,
			StatusLabel: label,
			SessionFile: f,
			ArtifactDir: filepath.Dir(f),
			StartedAt:   started,
		}
		row.Task = firstLine(diskSubagentTask(f), 120)
		if act.ToolName != "" {
			row.StatusLabel = act.ToolName
		}
		if status == SubagentDone {
			done := st.ModTime()
			row.DoneAt = &done
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// updateSubagentRowStatus refreshes lifecycle fields from a fresh disk
// snapshot without clobbering settled state: a terminal live row
// (done/error) never downgrades on a stale non-terminal snapshot, and a nil
// disk DoneAt never wipes a recorded completion time.
func updateSubagentRowStatus(dst *SubagentRow, d SubagentRow) {
	liveTerminal := dst.Status == SubagentDone || dst.Status == SubagentError
	diskTerminal := d.Status == SubagentDone || d.Status == SubagentError
	if d.Status != "" && (!liveTerminal || diskTerminal) {
		dst.Status = d.Status
		dst.StatusLabel = d.StatusLabel
	}
	if d.DoneAt != nil {
		dst.DoneAt = d.DoneAt
	}
}

// subagentAdoptable reports whether a disk session can be attributed to a
// live row. Both ends of the run are bounded: a session from before the
// row started is a previous run of the same task, and a session from after
// the row finished is a *later* run — either would attach the wrong
// transcript. Only the run that overlaps the live row qualifies.
func subagentAdoptable(d SubagentRow, live SubagentRow) bool {
	if live.Restored {
		// A replayed row's timestamps are the replay moment, so they say
		// nothing about which child session it was. The task key matched;
		// trust it and let the session file correct the clock.
		return true
	}
	if d.StartedAt.Before(live.StartedAt) {
		return false
	}
	if live.DoneAt != nil && d.StartedAt.After(*live.DoneAt) {
		return false
	}
	return true
}

// mergeSubagentDisk folds artifact rows into the live list. It upsets fresh
// disk status onto existing rows instead of only appending: a disk row whose
// session file is already claimed by a live row updates that row's
// Status/StatusLabel/DoneAt (keeping its real name), and a re-scanned disk
// row (same ID) refreshes the stored snapshot — otherwise first-seen states
// like "starting" or "pane" freeze forever and completions never surface.
func mergeSubagentDisk(live []SubagentRow, disk []SubagentRow) []SubagentRow {
	byFile := map[string]int{}
	byID := map[string]int{}
	byTask := map[string]int{}
	for i, r := range live {
		if r.SessionFile != "" {
			if _, ok := byFile[r.SessionFile]; !ok {
				byFile[r.SessionFile] = i
			}
		}
		if _, ok := byID[r.ID]; !ok {
			byID[r.ID] = i
		}
		// Task identity: an in-process child writes its session under
		// <agentDir>/subagent-sessions, a path the live row cannot link
		// by file, so the dispatched task text is what proves they are
		// the same run. Every key-bearing row registers, not just the ones
		// still missing a file: narrowing it would let a same-task disk row
		// append as a phantom duplicate. Whether a file is actually filled
		// in is decided at adoption time below. Two concurrent children
		// with identical task text share the key, so the second disk row is
		// folded into the first — a merged row beats a duplicate.
		if key := subagentTaskKey(r.Task); key != "" {
			if _, ok := byTask[key]; !ok {
				byTask[key] = i
			}
		}
	}
	for _, d := range disk {
		if d.SessionFile != "" {
			if idx, ok := byFile[d.SessionFile]; ok {
				updateSubagentRowStatus(&live[idx], d)
				continue
			}
		}
		if idx, ok := byID[d.ID]; ok {
			updateSubagentRowStatus(&live[idx], d)
			if live[idx].SessionFile == "" {
				live[idx].SessionFile = d.SessionFile
			}
			if live[idx].ArtifactDir == "" {
				live[idx].ArtifactDir = d.ArtifactDir
			}
			continue
		}
		if key := subagentTaskKey(d.Task); key != "" {
			if idx, ok := byTask[key]; ok && subagentAdoptable(d, live[idx]) {
				// Same child seen on disk. An unsettled row also takes the
				// disk status; a settled one keeps its own and only gains
				// the session file, so the detail pane can show the
				// transcript a finished in-process run left behind. A
				// session older than the live row is a previous run of the
				// same task and is never adopted.
				if live[idx].Status == SubagentStarting || live[idx].Status == SubagentActive || live[idx].Status == SubagentWaiting {
					updateSubagentRowStatus(&live[idx], d)
				}
				if live[idx].SessionFile == "" {
					live[idx].SessionFile = d.SessionFile
				}
				if live[idx].Restored {
					// Adopt the session's own timeline so elapsed time and
					// scope are real rather than "since reconnect". A disk
					// row with no completion time means the child session
					// carries no shutdown marker: keep the live row's own
					// end time rather than inventing a span that runs to
					// now, which is how the replay gap would show up as
					// elapsed time.
					live[idx].StartedAt = d.StartedAt
					if d.DoneAt != nil {
						live[idx].DoneAt = d.DoneAt
					} else if live[idx].DoneAt != nil {
						live[idx].DoneAt = nil
					}
					live[idx].Restored = false
				}
				if live[idx].ArtifactDir == "" {
					live[idx].ArtifactDir = d.ArtifactDir
				}
				continue
			}
		}
		live = append(live, d)
		if d.SessionFile != "" {
			if _, ok := byFile[d.SessionFile]; !ok {
				byFile[d.SessionFile] = len(live) - 1
			}
		}
		byID[d.ID] = len(live) - 1
	}
	return live
}

// refreshSubagents re-scans disk state (TTL-gated unless force) and merges
// into m.Subagents, preserving live rows (which carry the real names).
// X-dismissed rows stay dismissed: filtered here so the 2s tick rescan
// cannot re-add them.
func (m *Model) refreshSubagents(force bool) {
	now := time.Now()
	if !force && now.Sub(m.subagentsAt) < subagentScanTTL {
		return
	}
	m.subagentsAt = now
	disk := scanSubagentArtifacts(m.sessionFile, piAgentDir(), now)
	// Claim: live interactive rows start with no SessionFile, so the merge
	// below could never link them to their pane artifact (live+disk
	// duplicates, stuck "pane"). When the linkage is unambiguous — exactly
	// one unlinked interactive row and exactly one unclaimed pane artifact
	// from this parent session — attach it, and the merge then updates the
	// row in place instead of appending a duplicate. With 2+ panes,
	// positional matching would mislink transcripts, so leave them unlinked.
	if parentDir := subagentArtifactDir(m.sessionFile); parentDir != "" {
		claimed := map[string]bool{}
		for _, r := range m.Subagents {
			if r.SessionFile != "" {
				claimed[r.SessionFile] = true
			}
		}
		var unlinked []int
		for i, r := range m.Subagents {
			if r.SessionFile != "" || (!r.Interactive && r.Surface == "") {
				continue
			}
			if r.Status == SubagentDone || r.Status == SubagentStalled || r.Status == SubagentError {
				continue
			}
			unlinked = append(unlinked, i)
		}
		var candidates []int
		for j, d := range disk {
			if d.SessionFile == "" || claimed[d.SessionFile] {
				continue
			}
			if filepath.Dir(d.SessionFile) != parentDir {
				continue
			}
			if m.dismissed[d.ID] || m.dismissed[d.SessionFile] {
				continue
			}
			candidates = append(candidates, j)
		}
		if len(unlinked) == 1 && len(candidates) == 1 {
			i, j := unlinked[0], candidates[0]
			m.Subagents[i].SessionFile = disk[j].SessionFile
			m.Subagents[i].ArtifactDir = disk[j].ArtifactDir
		}
	}
	m.Subagents = mergeSubagentDisk(m.Subagents, disk)
	if len(m.dismissed) > 0 && len(m.Subagents) > 0 {
		kept := m.Subagents[:0]
		for _, r := range m.Subagents {
			if m.dismissed[r.ID] {
				continue
			}
			if r.SessionFile != "" && m.dismissed[r.SessionFile] {
				continue
			}
			kept = append(kept, r)
		}
		m.Subagents = kept
	}
}

// dismissSubagentRow removes a row from the sidebar/overlay and records it
// so refreshSubagents will not resurrect it via disk rescan. Both the row
// ID and session file are recorded: disk rows are keyed "disk-<hex>" while
// live rows carry the toolCallId for the same session file.
func (m *Model) dismissSubagentRow(row SubagentRow) {
	if m.dismissed == nil {
		m.dismissed = make(map[string]bool)
	}
	if row.ID != "" {
		m.dismissed[row.ID] = true
	}
	if row.SessionFile != "" {
		m.dismissed[row.SessionFile] = true
	}
	kept := m.Subagents[:0]
	for _, r := range m.Subagents {
		if r.ID == row.ID {
			continue
		}
		if row.SessionFile != "" && r.SessionFile == row.SessionFile {
			continue
		}
		kept = append(kept, r)
	}
	m.Subagents = kept
}

// subagentsTickMsg keeps file-backed lifecycle statuses live without relying
// on a user event to trigger refreshSubagents.
type subagentsTickMsg struct{}

func subagentsTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return subagentsTickMsg{} })
}

func subagentSteerable(row SubagentRow) bool {
	return !strings.HasPrefix(row.ID, "disk-") &&
		(row.Status == SubagentStarting || row.Status == SubagentActive || row.Status == SubagentWaiting)
}

// trackSubagentStart upserts a live row on tool_execution_start.
func (m *Model) trackSubagentStart(toolCallID, toolName string, args json.RawMessage) {
	if !isSubagentRowTool(toolName) || toolCallID == "" {
		return
	}
	name, agent, task, interactive, _ := parseSubagentArgs(args)
	// Name preference: an explicit name, then the task's first line, then the
	// toolCallId. Backends that send only a task (pi's own fork tool sends
	// {task, effort, mode}) would otherwise show an opaque "fork-a1b2c3d4" in
	// the sidebar with the useful text pushed to the right-hand description.
	if strings.TrimSpace(name) == "" {
		if fromTask := firstLine(strings.TrimSpace(task), 40); fromTask != "" {
			name = fromTask
		}
	}
	if strings.TrimSpace(name) == "" {
		name = toolName + "-" + toolCallID
		if len(toolCallID) > 8 {
			name = toolName + "-" + toolCallID[:8]
		}
	}
	m.Subagents = upsertSubagentRow(m.Subagents, SubagentRow{
		ID:          toolCallID,
		Name:        name,
		AgentName:   agent,
		Task:        firstLine(task, 120),
		Tool:        toolName,
		Status:      SubagentActive,
		StatusLabel: "active",
		Interactive: interactive,
		StartedAt:   time.Now(),
	})
}

// subagentSurfaceHandle extracts the pane handle from a subagent tool
// result ("... (surface: <handle>)"). The handle is opaque here: Orca
// reports `term_<uuid>`, cmux a uuid or a `window:1/workspace:2/pane:3`
// ref, and which host owns it is decided by surfaceCtlForHandle, not by a
// prefix baked into the parser. Only a conservative charset is accepted —
// no spaces, flags, or newlines — because the same string is rendered in
// the overlay and interpolated into prompts.
func parseSubagentSurface(resultText string) string {
	i := strings.Index(resultText, "(surface:")
	if i < 0 {
		return ""
	}
	rest := resultText[i+len("(surface:"):]
	j := strings.Index(rest, ")")
	if j < 0 {
		return ""
	}
	s := strings.TrimSpace(rest[:j])
	// A handle is a value, never a flag: reject a leading dash so a hostile
	// result cannot become an option for whatever host consumes it.
	if s == "" || len(s) > 80 || s[0] == '-' {
		return ""
	}
	for _, c := range s {
		switch {
		case c == '-' || c == '_' || c == ':' || c == '.' || c == '/',
			c >= '0' && c <= '9',
			c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z':
		default:
			return ""
		}
	}
	return s
}

// trackSubagentEnd marks the row done/error on tool_execution_end.
// Interactive pane spawns return while the agent keeps running out-of-band,
// so those rows stay active (labeled "pane") instead of flipping to done.
func (m *Model) trackSubagentEnd(toolCallID, toolName string, args json.RawMessage, resultText string, isError bool) {
	if toolCallID == "" {
		return
	}
	for i, r := range m.Subagents {
		if r.ID != toolCallID {
			continue
		}
		if r.Interactive && !isError && isSubagentRowTool(toolName) {
			r.Status = SubagentActive
			r.StatusLabel = "pane"
		} else {
			done := time.Now()
			r.DoneAt = &done
			r.Status = SubagentDone
			r.StatusLabel = ""
			if isError {
				r.Status = SubagentError
				r.IsError = true
			}
			if t := strings.TrimSpace(resultText); t != "" {
				if len(t) > subagentResultCap {
					t = t[:subagentResultCap]
				}
				r.Result = t
			}
		}
		if s := parseSubagentSurface(resultText); s != "" {
			r.Surface = s
		}
		m.Subagents[i] = r
		return
	}
	// End without a recorded start (e.g. history gap): create a done row
	// when args carry a name, so the completion stays visible.
	if isSubagentRowTool(toolName) {
		name, agent, task, interactive, _ := parseSubagentArgs(args)
		if strings.TrimSpace(name) == "" {
			return
		}
		done := time.Now()
		m.Subagents = upsertSubagentRow(m.Subagents, SubagentRow{
			ID: toolCallID, Name: name, AgentName: agent,
			Task: firstLine(task, 120), Tool: toolName,
			Status: SubagentDone, Interactive: interactive,
			StartedAt: done, DoneAt: &done,
			Result:  firstLine(resultText, subagentResultCap),
			Surface: parseSubagentSurface(resultText),
		})
	}
}

// subagentGlyph is the sidebar status dot per row status.
func subagentGlyph(s SubagentStatus) (string, bool) {
	switch s {
	case SubagentDone:
		return "✓", false
	case SubagentActive:
		return "◐", false
	case SubagentWaiting:
		return "◌", false
	case SubagentStarting:
		return "○", false
	default: // stalled, error
		return "!", true
	}
}
