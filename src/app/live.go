package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

type liveMsg struct {
	generation uint64
	message    live.Message
}

func (m Model) runLiveBridge() tea.Cmd {
	bridge := m.liveBridge
	if bridge == nil {
		bridge = &live.Bridge{CWD: m.cwd, OwnPID: m.Pi.PID()}
	}
	generation := m.liveGeneration
	return func() tea.Msg {
		cmd := bridge.Start(context.Background(), func(message live.Message) {
			if ProgRef != nil {
				ProgRef.Send(liveMsg{generation: generation, message: message})
			}
		})
		cmd()
		return nil
	}
}

// runLiveTail follows a foreign pi's session FILE instead of its bridge, for a
// pi that never loaded the extension. It is deliberately identical to
// runLiveBridge — same channel, same generation capture, same Bubble Tea
// goroutine — so the tail's messages need no rendering path of their own: the
// file is only a different wire, not a different feature.
func (m Model) runLiveTail() tea.Cmd {
	tail := m.liveTail
	if tail == nil {
		return nil
	}
	generation := m.liveGeneration
	return func() tea.Msg {
		tail.Start(context.Background(), func(message live.Message) {
			if ProgRef != nil {
				ProgRef.Send(liveMsg{generation: generation, message: message})
			}
		})()
		return nil
	}
}

// StopLiveTransport stops whichever transport is attached, whatever it is.
// Both are stopped unconditionally and idempotently rather than switching on
// the recorded source: at most one is ever live, and a tail that outlived a
// detach would keep pushing a foreign transcript into a window that is back
// on its own session. Exported because /quit is a quit path too (src/builtin).
func (m *Model) StopLiveTransport() {
	if m.liveBridge != nil {
		m.liveBridge.Stop()
	}
	if m.liveTail != nil {
		m.liveTail.Stop()
		m.liveTail = nil
	}
	m.liveSource = ""
	// Following rewrites sessionFile to the followed session's file (the team
	// roster is only on disk), so the owned one is put back here or the next
	// /team, respawn and resume would read a foreign session.
	if m.liveSessionFile != "" {
		m.sessionFile = m.liveSessionFile
		m.liveSessionFile = ""
	}
}

// sessionFileForID locates a pi session file by its session id in cwd's
// session dir. It returns "" when the session is not persisted yet (a
// --no-session run has no file at all), which is not an error: the caller
// simply keeps whatever sessionFile it had.
func sessionFileForID(cwd, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	for _, si := range pirpc.ListSessions(pirpc.SessionDirFor(cwd), cwd, 40, true) {
		if si.ID == sessionID {
			return si.Path
		}
	}
	return ""
}

// refreshTeamFromSession rebuilds the worker roster from the followed session's
// own team-state records. The records are durable on disk (the pi-agent-team
// extension appends one per worker change), which is the only place a file
// source can learn that workers existed: the per-worker live activity line
// lives in the extension UI and is bridge-only, so this deliberately shows
// profile, status and headline and nothing more.
func (m *Model) refreshTeamFromSession() {
	if m.sessionFile == "" {
		return
	}
	workers := readTeamSessionWorkers(m.sessionFile)
	if len(workers) == 0 {
		m.clearTeamWidgetState()
		return
	}
	ids := make([]string, 0, len(workers))
	for id := range workers {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable roster order: workers must not shuffle per update
	lines := make([]string, 0, len(ids)+1)
	lines = append(lines, fmt.Sprintf("Pi Agents Team · %d workers", len(ids)))
	for _, id := range ids {
		w := workers[id]
		profile := w.Profile
		if profile == "" {
			profile = "agent"
		}
		line := fmt.Sprintf("%s (%s) · %s", profile, id, w.Status)
		if w.Headline != "" {
			line += " · " + w.Headline
		}
		lines = append(lines, line)
	}
	m.setTeamWidget(lines, "")
}

// applyLive bridges the transport into existing transcript/event rendering.
// No remote path calls Pi.Send: remote events are observational only.
func (m *Model) applyLive(generation uint64, message live.Message) tea.Cmd {
	if generation != m.liveGeneration {
		return nil // queued before a detach/quit; never resurrect follow mode
	}
	var liveCmd tea.Cmd
	painted := false // handleEvent owns the repaint for forwarded events
	// A pinned target that is dead or refuses the connection reports here
	// instead of Connected: stay on the owned session rather than showing a
	// broken read-only view.
	if message.Err != nil && !m.followRemote {
		m.liveConnected = false
		m.AddBlock(Block{Kind: "notice", Text: "could not attach to the selected Pi session: " + message.Err.Error(), Err: true})
		m.Status = "live attach failed · still on the owned session"
		m.Refresh()
		return nil
	}
	if message.Connected {
		m.liveConnected = true
		m.followRemote = true
		m.remoteSession = message.Descriptor.SessionID
		m.session = ShortID(message.Descriptor.SessionID)
		m.Status = "following external Pi · read-only"
		m.Dialogs = nil
		m.clearTeamWidgetState()
		m.closeAt()
		m.cmdOpen = false
		m.ta.Blur()
	}
	if message.Disconnected {
		m.clearTeamWidgetState()
		m.liveConnected = false
		if m.followRemote {
			m.Status = "external Pi disconnected · reconnecting…"
		}
	}
	if snap := message.Snapshot; snap != nil {
		m.followRemote = true
		m.liveConnected = true
		m.remoteSession = snap.SessionID
		m.session = snap.SessionName
		if m.session == "" {
			m.session = ShortID(snap.SessionID)
		}
		m.ModelLbl = snap.Model
		if m.ModelLbl == "" {
			m.ModelLbl = "external"
		}
		m.thinkLvl = snap.ThinkingLevel
		m.blocks = nil
		m.clearTeamWidgetState()
		m.tools = make(map[string]int)
		m.progressByKey = make(map[string]int)
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
		messages := make([]pirpc.AgentMessage, 0, len(snap.Messages))
		for _, raw := range snap.Messages {
			var msg pirpc.AgentMessage
			if json.Unmarshal(raw, &msg) == nil {
				messages = append(messages, msg)
			}
		}
		m.restore(messages)
		// A followed session's worker roster lives in its own team-state
		// records, so show it as soon as the transcript is restored rather
		// than waiting for the next appended record.
		m.refreshTeamFromSession()
		liveCmd = m.setRemoteRunning(snap.IsStreaming)
	}
	if event := message.Event; event != nil {
		m.followRemote = true
		if jsonEventType(event.Raw) == "session_info_changed" {
			var info struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(event.Raw, &info) == nil {
				if info.Name != "" {
					m.session = info.Name
				} else {
					m.session = ShortID(m.remoteSession)
				}
			}
		}
		// handleEvent contains the canonical message/tool rendering and the
		// paint policy: streaming deltas share its frame coalescer, anything
		// else repaints once. A Refresh here on top of that gave every token
		// delta two uncached full repaints and stalled the SSE reader.
		eventType := jsonEventType(event.Raw)
		if eventType != "agent_start" && eventType != "agent_settled" {
			m.Status = "following external Pi · read-only" // before the single paint
		}
		if eventType == "team_state" {
			// A worker record was appended to the followed session's file.
			// handleEvent ignores unknown types, so the roster is rebuilt
			// here from that file.
			m.refreshTeamFromSession()
		}
		nm, evCmd := m.handleEvent(pirpc.Event{Type: eventType, Raw: event.Raw})
		*m = nm.(Model)
		m.liveConnected = true
		switch eventType {
		case "agent_start":
			liveCmd = m.setRemoteRunning(true)
		case "agent_settled":
			liveCmd = m.setRemoteRunning(false)
		}
		// handleEvent's command is the only delivery path for petTickCmd(),
		// so it must be batched, never dropped: a discarded tick latches
		// pet.ticking forever (F1). agent_settled is the one event that also
		// batches owned-pi RPCs (queryStats, fetchCmdsOnce, fetchStateOnce,
		// wsRefresh): follow mode must not issue those, so its pet work comes
		// from setRemoteRunning instead.
		if eventType != "agent_settled" {
			liveCmd = tea.Batch(liveCmd, evCmd)
		}
		// handleEvent owns the repaint for every forwarded event. Only these
		// two need one more: setRemoteRunning rewrote Status/pet AFTER
		// handleEvent painted, so their paint is stale without it.
		if eventType == "agent_start" || eventType == "agent_settled" {
			m.Refresh()
		}
		painted = true
	}
	if !painted {
		m.Refresh()
	}
	return liveCmd
}

// setRemoteRunning mirrors Pi's busy state into the sidebar and chat gutter.
// It returns only pet animation commands; it never schedules owned Pi RPCs.
func (m *Model) setRemoteRunning(running bool) tea.Cmd {
	m.thinking = running
	if running {
		m.Status = "external Pi running · read-only"
		m.pet.inTurn = true
		// A forwarded agent_start whose matching message_end was lost (SSE
		// reconnect) would otherwise keep appending remote deltas to a stale
		// block: owned mode gets this reset from turn_start, which the bridge
		// also forwards, but a mid-round reconnect can skip it.
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
		if m.pet.status.Busy() {
			if !m.pet.ticking {
				return m.armPetTick()
			}
			return nil
		}
		return m.petSet(petWorking)
	}
	m.Status = "following external Pi · read-only"
	m.pet.inTurn = false
	if m.pet.status.Busy() {
		return m.petSet(petIdle)
	}
	return nil
}

func jsonEventType(raw json.RawMessage) string {
	var envelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Type
}

// detachLive returns to the owned session and fetches a fresh owned snapshot.
func (m *Model) detachLive() tea.Cmd {
	m.liveGeneration++ // invalidate every transport message already in the UI queue
	m.StopLiveTransport()
	m.followRemote = false
	m.liveConnected = false
	m.remoteSession = ""
	// Remote events set the owned turn-timing fields inline (turn_start stamps
	// them, agent_settled arms pendSpeed while its queryStats is deliberately
	// dropped), so detaching with them still set hands the next owned turn a
	// garbage baseline for "last"/speed. The remote plugin status/widget
	// registry goes with the team widget: it was written by the tapped
	// setStatus/setWidget of a session we are no longer following.
	m.turnStart = time.Time{}
	m.turnOutBase = 0
	m.pendSpeed = false
	m.clearTeamWidgetState()
	m.clearExtUIState()
	m.Dialogs = nil
	m.closeAt()
	m.cmdOpen = false
	m.ta.Focus()
	m.Status = "detached external Pi · reconnecting owned session…"
	m.Refresh()
	return m.fetchAll()
}

// ToggleLiveSession is the whole /live command: while following it detaches,
// otherwise it offers the pi processes running in this directory to follow.
// pitago never publishes its OWN session — the child it spawns is excluded
// from discovery, and nothing here respawns it.
func (m *Model) ToggleLiveSession() tea.Cmd {
	if m.followRemote {
		m.AddBlock(Block{Kind: "notice", Text: "detached external Pi · back on the owned session"})
		return m.detachLive()
	}
	// The owned child is a pi process in this very directory: it is the
	// session the user is already driving here, so it is never a candidate
	// to follow.
	// The bridge is what makes a FUTURE pi self-broadcasting: pi auto-loads
	// extensions from ~/.pi/agent/extensions, so installing it here means the
	// next session the user starts needs no flag and no manual step. It used
	// to be installed only on the "no pi running" path, which a user with
	// sessions already open never reaches — so the install never happened and
	// every pi still had to be started by hand with --extension.
	if !m.liveBridgeInstalled {
		if _, err := live.InstallBridge(); err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "live bridge could not be installed: " + err.Error(), Err: true})
		} else {
			m.AddBlock(Block{Kind: "notice", Text: "live bridge installed — a pi started from now on broadcasts on its own, with no flag; one already running is still watchable through its session file, or restart it once for full live streaming"})
		}
		m.liveBridgeInstalled = true
	}
	var ownPID int
	if m.Pi != nil {
		ownPID = m.Pi.PID()
	}
	cands, err := live.Candidates(m.cwd, ownPID)
	if err != nil {
		m.AddBlock(Block{Kind: "notice", Text: "could not list pi sessions: " + err.Error(), Err: true})
		m.Refresh()
		return nil
	}
	if len(cands) == 0 {
		return m.reportNoLiveSessions()
	}
	// The single obvious choice needs no dialog; anything else is a decision.
	if len(cands) == 1 && followableLiveCandidate(cands[0]) {
		return m.attachLiveCandidate(cands[0])
	}
	m.liveCands = cands // parallel to the picker rows; Enter lands in src/builtin
	opts := make([]string, 0, len(cands))
	descs := make([]string, 0, len(cands))
	firstFollowable := -1
	for i, c := range cands {
		opts = append(opts, liveCandidateLabel(c))
		descs = append(descs, liveCandidateDesc(c))
		if firstFollowable < 0 && followableLiveCandidate(c) {
			firstFollowable = i
		}
	}
	d := &Dialog{Kind: "live", Title: "Live Pi sessions",
		Message: "↑↓ pick · Enter attach · Esc close",
		Options: opts, Descs: descs}
	d.Reindex()
	if firstFollowable >= 0 {
		// Bridges come first in the live layer's ordering, so the first
		// followable row is the richest one; only fall back to row 0 when
		// nothing here can be followed (Enter then explains why).
		d.Cursor = firstFollowable
	}
	// Re-pressing /live replaces the open picker in place (same idiom as the
	// team dashboard) so a refresh never stacks dialogs.
	if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "live" {
		*m.Dialogs[0] = *d
	} else {
		m.Dialogs = append(m.Dialogs, d)
	}
	m.applyPopupH()
	m.Refresh()
	return nil
}

// reportNoLiveSessions answers the empty-directory case in the transcript.
// An empty result is a normal answer, not an error: it also installs the
// bridge extension so every pi started from now on streams in full without a
// manual flag. The notice points at the session file for a pi that is already
// running, because "restart it once" is exactly the friction the second
// source removes: such a pi is followable right now, just without the bridge's
// realtime extras.
func (m *Model) reportNoLiveSessions() tea.Cmd {
	m.AddBlock(Block{Kind: "notice", Text: "no pi session is running in " + m.cwd + " — start one with: pi"})
	if !live.BridgeInstalled() {
		// A one-off copy of one small file, only on the first /live of a
		// machine: worth the synchronous write to keep the notice order.
		if _, err := live.InstallBridge(); err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "live bridge could not be installed: " + err.Error(), Err: true})
		} else {
			m.AddBlock(Block{Kind: "notice", Text: "live bridge installed — a pi started from now on in this directory streams in full; a pi that is already running is followable immediately through its session file"})
		}
	}
	m.Refresh()
	return nil
}

// AttachLive follows the picked picker row (Enter on the /live dialog). The
// Enter action itself lives in src/builtin (see Confirmers), which only knows
// a row index, so the candidates are kept on the model.
func (m *Model) AttachLive(ri int) tea.Cmd {
	if ri < 0 || ri >= len(m.liveCands) {
		return nil
	}
	return m.attachLiveCandidate(m.liveCands[ri])
}

// attachLiveCandidate starts the transport the picked row can actually feed:
// the SSE bridge when the session published a descriptor, otherwise the
// session-file tail. The two are interchangeable from the same picker, and
// both are read-only, so the follow-mode state below is identical for them.
func (m *Model) attachLiveCandidate(c live.Candidate) tea.Cmd {
	switch {
	case c.Source == live.SourceFile && c.File != nil:
		m.beginLiveAttach(c)
		m.liveSource = live.SourceFile
		m.liveTail = live.NewTail(*c.File)
		// The team roster and /team both read m.sessionFile. Pointing it at
		// the followed session is what makes the workers visible at all on
		// this source; the owned path is restored by StopLiveTransport.
		if m.liveSessionFile == "" {
			m.liveSessionFile = m.sessionFile
		}
		m.sessionFile = c.File.Path
		return m.runLiveTail()
	case c.Streamable && c.Descriptor != nil:
		m.beginLiveAttach(c)
		m.liveSource = live.SourceBridge
		// A bridge widget is a component factory, so its live detail cannot
		// cross the process boundary. The worker roster it draws still exists
		// on disk, so point sessionFile at the followed session's file and let
		// the opaque-widget path fall back to it.
		if c.Descriptor != nil {
			if path := sessionFileForID(m.cwd, c.Descriptor.SessionID); path != "" {
				if m.liveSessionFile == "" {
					m.liveSessionFile = m.sessionFile
				}
				m.sessionFile = path
			}
		}
		d := *c.Descriptor
		if d.PID == 0 {
			d.PID = c.PID
		}
		if d.StartedAt == 0 {
			d.StartedAt = c.StartedAt
		}
		if d.SessionID == "" {
			d.SessionID = c.SessionID
		}
		if m.liveBridge == nil {
			m.liveBridge = &live.Bridge{CWD: m.cwd, OwnPID: m.Pi.PID()}
		}
		m.liveBridge.SetTarget(&d)
		return m.runLiveBridge()
	default:
		// Neither source: the session is not streaming and has no recent
		// file to tail, so there is nothing to follow. Saying so beats
		// opening a view that can never update.
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("pi %d cannot be followed: it never loaded the live bridge and has no recent session file", c.PID)})
		m.AddBlock(Block{Kind: "notice", Text: "restart that pi once to stream it in full — a pi that has written to its session file is followable with no restart at all"})
		m.Refresh()
		return nil
	}
}

// beginLiveAttach is the front half shared by both sources: stop whatever was
// attached, retire its queued messages, drop the picker rows and announce the
// switch. Order matters — the transport is stopped BEFORE the generation bump
// and before the new one starts, so exactly one source is ever live.
func (m *Model) beginLiveAttach(c live.Candidate) {
	m.StopLiveTransport()
	m.liveGeneration++ // retire transport messages queued by an earlier attach
	m.liveCands = nil
	m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("attaching %s · read-only · Ctrl+D detaches", liveCandidateSubject(c))})
	m.Status = "attaching external Pi · read-only"
	m.Refresh()
}

// followableLiveCandidate reports whether a row has a transport to start.
// A session file is as followable as a bridge: that is the whole point of the
// two sources, so "no bridge" must never be a dead end.
func followableLiveCandidate(c live.Candidate) bool {
	return (c.Source == live.SourceFile && c.File != nil) || (c.Streamable && c.Descriptor != nil)
}

// liveCandidateMeta returns the row's session id, model and directory,
// preferring the candidate and falling back to its session file. Which of
// those fields the live layer mirrors onto Candidate is the live layer's
// business; the picker just needs them to be non-empty.
func liveCandidateMeta(c live.Candidate) (id, model, dir string) {
	id, model, dir = c.SessionID, c.Model, c.CWD
	if c.File == nil {
		return id, model, dir
	}
	if id == "" {
		id = c.File.SessionID
	}
	if model == "" {
		model = c.File.Model
	}
	if dir == "" {
		dir = c.File.CWD
	}
	return id, model, dir
}

// liveCandidateLabel is one picker row: which process, for how long, which
// session, on what model, in which directory. A session-file row has no pid of
// its own, so it is labelled by the file it is being followed through.
func liveCandidateLabel(c live.Candidate) string {
	id, model, dir := liveCandidateMeta(c)
	name := strings.TrimSpace(c.SessionName)
	if name == "" {
		name = ShortID(id)
	}
	if name == "" {
		name = "unnamed session"
	}
	who := "session file"
	if c.PID > 0 {
		who = "pid " + strconv.Itoa(c.PID)
	}
	parts := []string{who, liveCandidateAge(c), name}
	if model = strings.TrimSpace(model); model != "" {
		parts = append(parts, model)
	}
	parts = append(parts, dir)
	return strings.Join(parts, " · ")
}

// liveCandidateSubject is what the transcript notice calls the picked row: the
// pid for a bridge row, the session id for a file row. "external pi 0" would be
// a lie, and the notice is the user's only record of what got attached.
func liveCandidateSubject(c live.Candidate) string {
	if c.PID > 0 {
		return "external pi " + strconv.Itoa(c.PID)
	}
	if id, _, _ := liveCandidateMeta(c); id != "" {
		return "external session " + ShortID(id)
	}
	return "external session file"
}

// liveCandidateDesc states what pressing Enter will get, so the difference
// between the two sources is visible before the user commits to one.
func liveCandidateDesc(c live.Candidate) string {
	switch {
	case c.Source == live.SourceFile && c.File != nil:
		return "○ session file · messages + tool results, live · no bridge, no restart"
	case c.Streamable:
		return "● streaming · full live stream · Enter follows it read-only"
	default:
		return "not streamable — no bridge, no recent session file"
	}
}

// liveCandidateAge renders the process age; a process whose start time is
// unknown (descriptor without startedAt) falls back to the session file's last
// write — a tailed file is being written right now, so its mtime IS the age.
func liveCandidateAge(c live.Candidate) string {
	if c.StartedAt > 0 {
		return "up " + fmtDur(time.Since(time.UnixMilli(c.StartedAt)))
	}
	if c.File != nil && !c.File.ModTime.IsZero() {
		return "up " + fmtDur(time.Since(c.File.ModTime))
	}
	return "age unknown"
}

// handleFollowKey preserves navigation/yank controls while blocking every path
// that could type, steer, abort, switch model/session, or open a command.
// Ctrl+D is deliberately absent: the global follow-mode check in Update owns
// it, and it must — handleFollowKey has a value receiver, so detachLive's
// liveGeneration++ (which retires every queued transport message) would be
// thrown away here and a detached window could re-attach.
func (m Model) handleFollowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.followRemote {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyCtrlQ:
		return m, m.detachLive(), true
	case tea.KeyCtrlC, tea.KeyCtrlB, tea.KeyCtrlY, tea.KeyCtrlO, tea.KeyCtrlG:
		return m, nil, false // handled by the normal switch below
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		return m, nil, false // viewport scrolling
	}
	m.Status = "external Pi is read-only · Ctrl+D detaches · Ctrl+Q detaches"
	m.Refresh()
	return m, nil, true
}
