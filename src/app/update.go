package app

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/chat"
	"pitago/src/ext"
	"pitago/src/extension"
	"pitago/src/pirpc"
)

// sideScrollKey maps Ctrl+scroll keys to their plain equivalent for the
// sidebar viewport (whose KeyMap only matches plain keys). Plain ↑↓ stays
// on chat scroll, so Ctrl is the no-mouse sidebar path.
func sideScrollKey(t tea.KeyType) (tea.KeyType, bool) {
	switch t {
	case tea.KeyCtrlUp:
		return tea.KeyUp, true
	case tea.KeyCtrlDown:
		return tea.KeyDown, true
	case tea.KeyCtrlPgUp:
		return tea.KeyPgUp, true
	case tea.KeyCtrlPgDown:
		return tea.KeyPgDown, true
	case tea.KeyCtrlHome:
		return tea.KeyHome, true
	case tea.KeyCtrlEnd:
		return tea.KeyEnd, true
	}
	return t, false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Toasts expire by wall clock: prune on every message so a missed
	// dismissal tick still clears (the tick itself just triggers repaint).
	m.pruneToasts()
	// SGR mouse-report leakage: a trackpad/mouse-wheel burst can split
	// across input reads, losing the ESC prefix — the "[<65;50;31M…"
	// remainder then arrives as plain KeyRunes. Scrub it before dialogs,
	// popups, or the textarea can insert the noise as text; wheel reports
	// still scroll the chat.
	if km, ok := msg.(tea.KeyMsg); ok && !km.Paste && km.Type == tea.KeyRunes {
		// Sequence: a split tail ("[<65"…) buffered from the prior read
		// rejoins its continuation here (time-bound via mouseBurst, so a
		// stale buffer never eats later typing). Non-continuations drop
		// the buffer and process normally.
		if m.mouseBuf != "" {
			if m.mouseBurst() && len(km.Runes) > 0 {
				c := km.Runes[0]
				if c >= '0' && c <= '9' || c == ';' || c == 'M' || c == 'm' {
					km.Runes = []rune(m.mouseBuf + string(km.Runes))
					msg = km
				}
			}
			m.mouseBuf = ""
		}
		// A bracketed tail split across reads ("[<64;…M[<65"): hold the
		// incomplete tail for the next read instead of emitting a partial
		// scroll with wrong coordinates. "[<" alone is typing, not noise.
		if s := string(km.Runes); s != "" && !km.Alt {
			if loc := sgrTail.FindStringIndex(s); loc != nil && loc[1] == len(s) {
				if tail := s[loc[0]:]; tail != "[<" {
					m.mouseBuf = tail
					m.mouseLeakAt = time.Now()
					if prefix := s[:loc[0]]; prefix == "" {
						return m, nil // pure tail: swallow, wait for rest
					} else {
						km.Runes = []rune(prefix)
						msg = km
					}
				}
			}
		}
		// A split ESC[ arrives as a lone Alt+[ — mouse-report shrapnel,
		// not text (no binding uses it; the textarea would insert the "["
		// and picker filters would insert "alt+[").
		if len(km.Runes) == 1 && km.Runes[0] == '[' && km.Alt {
			m.mouseLeakAt = time.Now()
			return m, nil
		}
		if events, cleaned, isLeak := cleanMouseLeak(km.Runes); isLeak {
			m.mouseLeakAt = time.Now()
			if len(cleaned) == 0 {
				if len(m.Dialogs) > 0 {
					return m.applyWheelLeak(events), nil
				}
				return m, m.scrollLeak(events)
			}
			// Mixed burst + real typing: still scroll (don't drop the
			// wheel), then let the leftover text type normally. Wheel
			// never touches input history — scrollLeak only moves viewports.
			if len(m.Dialogs) > 0 {
				m = m.applyWheelLeak(events).(Model)
			} else {
				_ = m.scrollLeak(events)
			}
			km.Runes = cleaned
			km.Alt = false // the Alt bit is the eaten ESC, not the user
			msg = km
		} else if m.mouseBurst() {
			// Burst-armed: a lone continuation ("65;99;18M", ";50;31M")
			// whose head died in the prior read. Swallow it so it never
			// lands in the input.
			if events, frag := cleanMouseFrag(string(km.Runes)); frag {
				m.mouseLeakAt = time.Now()
				if len(m.Dialogs) > 0 {
					return m.applyWheelLeak(events), nil
				}
				return m, m.scrollLeak(events)
			}
			// Sequence break: normal typing while armed disarms, so a
			// later frag-like typing ("65;99") isn't swallowed.
			m.mouseLeakAt = time.Time{}
		}
	}
	// Live bridge transport bypasses dialog capture: an attach closes local
	// dialogs, and a reconnect/disconnect must always reach the model.
	if lm, ok := msg.(liveMsg); ok {
		return m, m.applyLive(lm.generation, lm.message)
	}
	// Detach and owned-child isolation are global, even behind a read-only
	// picker/dialog. In particular, Ctrl+D must never become "close dialog".
	if km, ok := msg.(tea.KeyMsg); ok && m.followRemote && km.Type == tea.KeyCtrlD {
		return m, m.detachLive()
	}
	if _, ok := msg.(piEventMsg); ok && m.followRemote {
		return m, nil
	}

	// A Replace picker targets the open dialog in place (Tab scope swap),
	// not a second dialog — it bypasses dialog capture to the main switch.
	// dialog captures all keys while open
	if len(m.Dialogs) > 0 && !replaceIntoOpen(m.Dialogs[0], msg) {
		// Wheel scrolls the settings hub list (locked to the dialog —
		// the chat/sidebar behind never moves); every other mouse event
		// stays swallowed while a dialog is open.
		if mm, ok := msg.(tea.MouseMsg); ok {
			if d := m.Dialogs[0]; d.Kind == "pconfig" && len(d.Provs) > 0 &&
				(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown) {
				return m.updatePconfigWheel(d, mm.Button == tea.MouseButtonWheelDown)
			}
			// Trace windows: wheel over the left list moves the selection,
			// wheel over the detail scrolls it (chat/sidebar stay behind).
			if d := m.Dialogs[0]; (d.Kind == "trajectory" || d.Kind == "tree") &&
				(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown) {
				if d.Kind == "tree" {
					return m.updateTreeWheel(d, mm)
				}
				return m.updateTrajWheel(d, mm)
			}
			if d := m.Dialogs[0]; d.Kind == "notification" &&
				(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown) {
				t := tea.KeyUp
				if mm.Button == tea.MouseButtonWheelDown {
					t = tea.KeyDown
				}
				return m.updateDialog(tea.KeyMsg{Type: t})
			}
			return m, nil
		}
		if km, ok := msg.(tea.KeyMsg); ok {
			// Alt+M toggles mouse even with a dialog open (filter boxes
			// would otherwise swallow it as the letter "m").
			if km.Alt && km.Type == tea.KeyRunes && len(km.Runes) == 1 &&
				(km.Runes[0] == 'm' || km.Runes[0] == 'M') {
				return m, m.ToggleMouse("")
			}
			nm, cmd := m.updateDialog(km)
			if km.Type == tea.KeyEsc {
				if um, ok := nm.(Model); ok {
					um.escArm = time.Time{} // Esc closed a dialog: reset cancel arm
					return um, cmd
				}
			}
			return nm, cmd
		}
		// A dialog captures keys and mouse, but pi keeps working behind it:
		// its events stay live (status/notify/pet/turn/todos) — except a
		// second dialog request, which is queued (FIFO) instead of stacked
		// and is promoted as soon as the stack empties. Other async
		// results stay swallowed — except paste
		// (Ctrl+V into the /login key field must land), the quit/esc
		// disarms (an arm must always expire, even behind a dialog), the
		// pet clock (elapsed/face animation is UI-only and must not
		// freeze), and the /login stay-open pipeline (save/rename → respawn →
		// reconnect must complete without closing the picker). The
		// deferred file-I/O messages belong to that same pipeline: they are
		// the second half of an action taken inside a dialog, so swallowing
		// them here would strand the dialog mid-action.
		if em, ok := msg.(piEventMsg); ok {
			if em.Event.Type != "extension_ui_request" ||
				!extension.IsDialogRequest(em.Event.Raw) {
				return m.handleEvent(em.Event)
			}
			// Park it. The old `return m, nil` dropped the payload on the
			// floor without answering, so pi's pending request never
			// resolved: the extension blocked on our UI until its own
			// timeout — forever, when it set none.
			m.queueDialogRequest(em.Event.Raw)
			return m, nil
		}
		switch msg.(type) {
		case tea.WindowSizeMsg, pasteDoneMsg, quitDisarmMsg, escDisarmMsg,
			petTickMsg, petFlashMsg, streamFlushMsg,
			LoginKeyMsg, RenameKeyMsg, respawnMsg, connectedMsg, CmdsRefreshMsg,
			SettingsMsg, SettingsRefreshMsg, MarketMsg, PluginChangeMsg,
			LoginSyncedMsg, LoginReloadMsg, OAuthGoneMsg, SettingWrittenMsg,
			LoginSwitchMsg, LoginDeleteMsg, LoginRenameOpenMsg, LogoutDoneMsg,
			LogoutListMsg:
		default:
			return m, nil
		}
	}

	// In external follow mode, only display/navigation and quit remain live.
	// This guard sits after dialog capture but before textarea/input and all
	// RPC control paths, making prompt/steer/abort/model/session impossible.
	if km, ok := msg.(tea.KeyMsg); ok {
		if nm, cmd, handled := m.handleFollowKey(km); handled {
			return nm, cmd
		}
	}

	// Wheel never touches input history: ↑↓ recalls when the input is
	// empty (mouse on only — with mouse off plain ↑↓ may be a wheel
	// scroll, see the KeyUp/KeyDown cases), wheel scrolls viewports only
	// (sidebar when hovered, chat otherwise). Early return so no
	// textarea/popup/history path below can see it.
	if mm, ok := msg.(tea.MouseMsg); ok && m.ready &&
		mm.Action == tea.MouseActionPress &&
		(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown ||
			mm.Button == tea.MouseButtonWheelLeft || mm.Button == tea.MouseButtonWheelRight) {
		var c tea.Cmd
		if m.overSide(mm.X) {
			m.sideVp, c = m.sideVp.Update(msg)
		} else {
			m.vp, c = m.vp.Update(msg)
		}
		m.Refresh()
		return m, c
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winW, m.winH = msg.Width, msg.Height
		mainW := m.mainW()
		// Layout fits winH exactly: header(1) + viewport + input(6 + tray:
		// textarea 3 + footer 1 + border 2 + image chips 0/1). Keep the
		// base budget independent of the current tray; applyPopupH owns
		// subtracting the tray when it changes without a resize.
		baseVpH := msg.Height - 7
		if baseVpH < 5 {
			baseVpH = 5
		}
		m.baseVpH = baseVpH
		vpH := baseVpH - m.chipH()
		if vpH < 5 {
			vpH = 5
		}
		if !m.ready {
			m.vp = viewport.New(mainW, vpH)
			m.sideVp = viewport.New(sideInnerW, m.sideContentH())
			m.ready = true
		} else {
			m.vp.Width = mainW
			m.vp.Height = vpH
			m.sideVp.Width = sideInnerW
			m.syncSideH() // reserves the quit-arm footer line while armed
			if m.cmdOpen || m.atOpen || m.isInlineUI() || m.inputOpen() {
				// Resizing with a popup open must keep the winH budget:
				// shrink the chat like a keystroke would (and clamp the
				// popup scroll offset to its new window).
				m.ensureCmdVisible()
				m.applyPopupH()
			}
		}
		m.ta.SetWidth(mainW - 6)
		m.Refresh()
		return m, nil

	case connectedMsg:
		if m.followRemote {
			return m, nil // an in-flight owned fetch must not replace remote history
		}
		if msg.err != nil {
			m.connErr = msg.err.Error()
			m.Status = "cannot connect to pi"
			return m, nil
		}
		m.ModelLbl = msg.state.Model.ID
		if m.ModelLbl == "" {
			m.ModelLbl = msg.state.Model.Name
		}
		m.thinkLvl = msg.state.ThinkingLevel
		m.autoCompact = msg.state.AutoCompaction
		m.ctxWindow = msg.state.Model.ContextWindow
		m.sessStart = time.Now()
		m.pushRecent(msg.state.Model.Provider, m.ModelLbl, m.ModelLbl)
		if msg.state.SessionName != "" {
			m.session = msg.state.SessionName
		} else {
			m.session = ShortID(msg.state.SessionID)
		}
		m.Stats = msg.stats
		m.sessBreak = pirpc.UsageBreakdown(msg.entries)
		m.Cmds = mergeCommands(m.builtins, msg.cmds)
		m.refreshCmds() // reconnect can replace Cmds under an open / popup (stale indices panic render)
		m.Todos = restoreTodos(msg.msgs)
		m.syncTaskRuntime()
		m.MCP = getMcpServers()
		m.Plugins = getPlugins()
		m.sessionFile = msg.state.SessionFile
		m.refreshPiTasks() // store file covers /tasks-menu edits (no RPC)
		m.blocks = nil
		m.tools = make(map[string]int)
		m.progressByKey = make(map[string]int)
		m.hist = nil
		m.histIdx = -1
		m.restore(msg.msgs)
		m.Status = "ready"
		m.planOn = false // fresh connect: plan latch is live-only
		m.clearTeamWidgetState()
		m.RefreshFollow()
		return m, m.ensureTaskTick()

	case piEventMsg:
		if m.followRemote {
			return m, nil // never mix the owned child's transcript into follow mode
		}
		return m.handleEvent(msg.Event)

	case statsMsg:
		if m.followRemote {
			return m, nil
		}
		if msg.err == nil {
			m.Stats = msg.stats
			if m.ctxWindow == 0 {
				m.ctxWindow = msg.stats.ContextWin
			}
			if m.pendSpeed {
				m.pendSpeed = false
				if !m.turnStart.IsZero() {
					d := time.Since(m.turnStart)
					m.lastDur = d.Round(time.Second)
					if secs := d.Seconds(); secs > 0 {
						if delta := msg.stats.Out - m.turnOutBase; delta > 0 {
							m.lastSpeed = float64(delta) / secs
						} else {
							m.lastSpeed = 0
						}
					}
				}
			}
			m.Refresh()
		}
		return m, nil

	case stateRefreshMsg:
		if m.followRemote {
			return m, nil
		}
		if msg.err == nil {
			lbl := msg.state.Model.ID
			if lbl == "" {
				lbl = msg.state.Model.Name
			}
			if lbl != "" {
				m.ModelLbl = lbl
			}
			m.thinkLvl = msg.state.ThinkingLevel
			m.autoCompact = msg.state.AutoCompaction
			if msg.state.Model.ContextWindow > 0 {
				m.ctxWindow = msg.state.Model.ContextWindow
			}
			m.pushRecent(msg.state.Model.Provider, m.ModelLbl, m.ModelLbl)
			if msg.state.SessionFile != "" {
				m.sessionFile = msg.state.SessionFile
				// New identity → new store file: re-read now, otherwise the
				// sidebar keeps the old session's list until the next tool
				// event or menu step (looks like a "delayed" update).
				m.refreshPiTasks()
			}
			if !msg.state.IsStreaming && m.thinking {
				// A settle swallowed behind an open dialog: the turn really
				// ended. (A just-sent prompt self-heals: agent_start
				// re-anchors the pet and flips the status back.)
				m.thinking = false
				m.Status = "ready"
				m.Refresh()
				return m, m.petSettled()
			}
			m.Refresh()
		}
		return m, nil

	case wsTickMsg:
		return m, tea.Batch(m.wsRefresh(), m.pollWs())

	case wsMsg:
		m.ws = msg.data
		m.MCP = getMcpServers()
		m.Plugins = getPlugins()
		m.Refresh()
		return m, nil

	case sentAckMsg:
		if msg.err != nil {
			m.AddBlock(Block{Kind: "notice", Text: msg.err.Error(), Err: true})
			m.thinking = false
			m.Status = "ready"
			m.Refresh()
		}
		return m, nil

	case extensionCmdAckMsg:
		// A synchronous extension command may own a visible lifecycle
		// (beginExtCmd/endExtCmd in src/app/extui_state.go). When it does,
		// the ack closes that window with the real error instead of printing
		// a bare inline notice — so a command never reports failure twice
		// and never looks silent. Commands with no lifecycle keep the
		// original inline notice.
		if msg.err != nil {
			if m.extCmdPending(msg.name) {
				m.endExtCmd(msg.name, msg.err, false)
				m.Refresh()
			} else {
				m.AddBlock(Block{Kind: "notice", Text: msg.err.Error(), Err: true})
				m.Refresh()
			}
		}
		return m, nil

	case teamWatchdogMsg:
		// The deadline for one dispatched /team. handleTeamWatchdog drops
		// stale ticks (already delivered, superseded, or never armed) and
		// only the live token can close the window with a visible reason.
		m.handleTeamWatchdog(msg)
		return m, nil

	case pasteDoneMsg:
		m.applyPaste(msg)
		return m, nil

	case toastTickMsg:
		m.Refresh()
		if len(m.toasts) > 0 {
			scheduleToastTick() // countdown keeps ticking; expiry prunes
		}
		return m, nil

	case quitDisarmMsg:
		if msg.gen == m.quitGen {
			m.quitArm = time.Time{}
			m.syncSideH()
			m.Refresh()
		}
		return m, nil
	case escDisarmMsg:
		if msg.gen == m.escGen {
			m.escArm = time.Time{}
			if m.thinking {
				m.Status = "pi is running…"
				m.Refresh()
			}
		}
		return m, nil
	case petTickMsg:
		// 500ms loop while busy/flashing or a task is active: one timer also
		// drives the task spinner and elapsed counter.
		if m.pet.status.Busy() || m.pet.status.Flashing() || m.taskActive() {
			m.pet.tick++
			m.Refresh()
			return m, petTickCmd()
		}
		m.pet.ticking = false
		return m, nil

	case petFlashMsg:
		// Stale timers (an older flash generation) must not cut a new status.
		if msg.gen == m.pet.gen && (m.pet.status == petSuccess || m.pet.status == petError) {
			m.pet.status = petIdle
			m.Refresh()
		}
		return m, nil

	case streamFlushMsg:
		// Trailing paint for a coalesced streaming burst.
		m.flushStreaming()
		return m, nil

	case SessionResetMsg:
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: msg.Err.Error(), Err: true})
		}
		m.blocks = nil
		m.tools = make(map[string]int)
		m.progressByKey = make(map[string]int)
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
		m.thinking = false
		m.escArm = time.Time{} // new session drops a stale cancel arm
		m.pet = petState{}
		m.Status = "ready"
		m.planOn = false // new session: plan latch is live-only
		m.Todos = nil
		m.task = taskRuntime{}
		// Drop the old session identity: its task store must not leak into
		// the new session via refreshPiTasks (re-adopted from get_state below).
		m.sessionFile = ""
		m.clearTeamWidgetState()
		m.imgAtts = nil // pending chips belong to the old session
		m.trayFocus = false
		m.histIdx = -1 // keep sent history across /new, back to live input
		m.applyPopupH()
		m.sessStart = time.Now()
		m.turnStart = time.Time{}
		m.pendSpeed = false
		m.lastDur, m.lastSpeed = 0, 0
		m.RefreshFollow()
		if prov, id := m.savedModel(); strings.TrimSpace(id) != "" {
			m.Status = "ready — restoring model…"
			m.Refresh()
			return m, tea.Batch(m.queryStats(), m.restoreModelCmd(prov, id))
		}
		return m, m.queryStats()

	case ModelCycleMsg:
		if msg.Err != nil {
			m.Status = "ready"
			m.AddBlock(Block{Kind: "notice", Text: "model switch failed: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		} else {
			m.ModelLbl = msg.Label
			id := msg.ID
			if id == "" {
				id = msg.Label
			}
			m.pushRecent(msg.Provider, id, msg.Label)
			m.rememberModel(msg.Provider, id, msg.Label)
			m.Status = "ready"
			verb := "model switched → "
			if msg.Restored {
				verb = "model restored → "
			}
			m.AddBlock(Block{Kind: "notice", Text: verb + msg.Label})
			m.Refresh()
			return m, m.fetchStateOnce()
		}

	case PickerMsg:
		m.Status = "ready"
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "failed to load list: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		title := "Select model"
		if msg.Kind == "thinking" {
			title = "Thinking level"
		}
		if msg.Kind == "theme" {
			title = "Select theme"
		}
		if msg.Kind == "sessions" {
			title = "Resume session (current)"
			if msg.Scope == "all" {
				title = "Resume session (all)"
			}
		}
		if msg.Replace && len(m.Dialogs) > 0 && m.Dialogs[0].Kind == msg.Kind {
			// Tab scope swap: keep the typed filter, reload rows in place.
			d := m.Dialogs[0]
			d.Title = title
			d.Scope = msg.Scope
			d.Options, d.Descs, d.Paths, d.Payload = msg.Options, msg.Descs, msg.Paths, msg.Payload
			d.Reindex()
			for i, ri := range d.FIdx {
				if ri < len(d.Paths) && d.Paths[ri] == msg.Current {
					d.Cursor = i
					break
				}
			}
			m.Status = "ready"
			m.Refresh()
			return m, nil
		}
		d := &Dialog{Kind: msg.Kind, Title: title, Options: msg.Options, Descs: msg.Descs, Providers: msg.Providers, Models: msg.Models, Paths: msg.Paths, Payload: msg.Payload, Filter: msg.Filter, Scope: msg.Scope}
		if msg.Kind == "model" {
			// two-pane picker: left = providers, right = their models
			d.Provs = buildProvs(msg.Providers)
			d.ProvConn = provConn(m.KeyPath, msg.Providers)
			sortProvsConn(d.Provs, d.ProvConn)
			d.FavSet = m.favSet
			d.ProvCursor, d.ProvFocus = 0, true
			for i, o := range d.Options {
				if o == msg.Current {
					if p := normProv(providerAt(msg.Providers, i)); p != "" {
						for pi, pv := range d.Provs {
							if pv == p {
								d.ProvCursor = pi
								break
							}
						}
					}
					break
				}
			}
			d.Reindex()
			for i, ri := range d.FIdx {
				if d.Options[ri] == msg.Current {
					d.Cursor = i
					break
				}
			}
			m.Dialogs = append(m.Dialogs, d)
			m.Refresh()
			return m, nil
		}
		// preselect the current value
		for i, o := range d.Options {
			if o == msg.Current {
				d.Cursor = i
				break
			}
		}
		d.Reindex()
		// keep cursor after reindex (sessions match by file path)
		for i, ri := range d.FIdx {
			if d.Options[ri] == msg.Current || (ri < len(d.Paths) && d.Paths[ri] == msg.Current) {
				d.Cursor = i
				break
			}
		}
		m.Dialogs = append(m.Dialogs, d)
		m.Refresh()
		return m, nil

	case SettingsMsg:
		m.Status = "ready"
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "settings error: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		opts, descs := msg.Opts, msg.Descs
		if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "settings" {
			d := m.Dialogs[0]
			cur := ""
			if d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) {
				cur = d.Provs[d.ProvCursor]
			}
			d.Options, d.Descs, d.Settings = opts, descs, msg.St
			if len(msg.Cats) > 0 {
				d.Providers = msg.Cats
				d.Provs = uniqueGroups(msg.Cats)
				d.ProvCursor = 0
				for i, g := range d.Provs {
					if g == cur {
						d.ProvCursor = i
						break
					}
				}
			}
			d.Reindex()
		} else {
			d := &Dialog{Kind: "settings", Title: "Agent settings",
				Message: "type to filter · Enter changes value · file rows reconnect pi",
				Options: opts, Descs: descs, Providers: msg.Cats, Settings: msg.St, Filter: msg.Filter}
			d.Provs = uniqueGroups(msg.Cats)
			d.ProvCursor, d.ProvFocus = 0, true
			if strings.TrimSpace(msg.Filter) != "" {
				// seeded filter (/settings network): search globally for
				// the first match, jump to its group, land on the matches.
				d.ProvFocus = true
				d.Reindex()
				if len(d.FIdx) > 0 {
					if g := providerAt(msg.Cats, d.FIdx[0]); g != "" {
						for i, gg := range d.Provs {
							if gg == g {
								d.ProvCursor = i
								break
							}
						}
					}
				}
				d.ProvFocus = false
				d.Cursor = 0
				d.Reindex()
			} else {
				d.Reindex()
			}
			m.Dialogs = append(m.Dialogs, d)
		}
		m.Refresh()
		return m, nil

	case TreeMsg:
		m.Status = "ready"
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "tree error: " + msg.Err.Error(), Err: true})
		} else if len(msg.Options) == 0 {
			m.AddBlock(Block{Kind: "notice", Text: "no tree entries yet"})
		} else {
			mode := msg.Mode
			if mode == "" {
				mode = "default"
			}
			d := &Dialog{Kind: "tree", Title: "Tree (" + mode + ")",
				Message: "session tree · ↑↓ move · Enter views the full entry · type filters",
				Options: msg.Options, Descs: msg.Descs, Payload: msg.Payload,
				Scope: mode, Filter: msg.Filter}
			d.Reindex()
			if msg.Current >= 0 {
				for i, ri := range d.FIdx {
					if ri == msg.Current {
						d.Cursor = i
						break
					}
				}
			}
			m.Dialogs = append(m.Dialogs, d)
		}
		m.Refresh()
		return m, nil

	case TrajectoryMsg:
		m.Status = "ready"
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "trajectory error: " + msg.Err.Error(), Err: true})
		} else if len(msg.Options) == 0 {
			m.AddBlock(Block{Kind: "notice", Text: "no trajectory entries yet"})
		} else {
			scope := msg.Scope
			if scope == "" {
				scope = "all"
			}
			d := &Dialog{Kind: "trajectory", Title: "Trajectory (" + scope + ")",
				Message: "harness-style run trace · ↑↓ move · Enter views the full step · type filters",
				Options: msg.Options, Descs: msg.Descs, Payload: msg.Payload, Scope: scope, Filter: msg.Filter}
			d.Reindex()
			m.Dialogs = append(m.Dialogs, d)
		}
		m.Refresh()
		return m, nil

	case SessionMsg:
		m.Status = "ready"
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "session error: " + msg.Err.Error(), Err: true})
		} else {
			m.sessBreak = msg.Break
			m.AddBlock(Block{Kind: "session", Text: msg.Text})
		}
		m.Refresh()
		return m, nil

	case SettingsRefreshMsg:
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: msg.Err.Error(), Err: true})
		} else {
			if msg.Level != "" {
				m.thinkLvl = msg.Level
			}
			if msg.Notice != "" {
				m.AddBlock(Block{Kind: "notice", Text: msg.Notice})
			}
		}
		m.Status = "ready"
		m.Refresh()
		return m, nil

	case LoginKeyMsg:
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "failed to save key: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		m.AddBlock(Block{Kind: "notice", Text: "saved key " + msg.Provider + " → pi — reconnecting…"})
		// Stay on /login: refresh the picker in place, reconnect behind it.
		if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "login" {
			d := m.Dialogs[0]
			m.RefreshLoginKeys(d)
			// cursor follows the new active key
			if nk := loginKeysLen(d); nk > 0 {
				d.KeyCursor = d.KeyActive
				if d.KeyCursor < 0 || d.KeyCursor >= nk {
					d.KeyCursor = nk - 1
				}
			}
			d.ProvFocus = false
			m.Refresh()
			return m, m.RespawnPi()
		}
		return m, m.RespawnPi()

	case RenameKeyMsg:
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "rename failed: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "login" {
			m.RefreshLoginKeys(m.Dialogs[0])
			m.Refresh()
		} else {
			m.Refresh()
		}
		return m, nil

	case LoginSwitchMsg:
		m.RefreshLoginKeys(msg.D)
		m.AddBlock(Block{Kind: "notice", Text: "switched " + msg.Prov + " to key " + msg.Masked + " → pi — reconnecting…"})
		m.Refresh()
		return m, m.RespawnPi()

	case LoginDeleteMsg:
		m.RefreshLoginKeys(msg.D)
		if msg.D.KeyCursor >= len(msg.D.Options) {
			msg.D.KeyCursor = 0
		}
		if !msg.Active {
			m.Refresh()
			return m, nil
		}
		if len(msg.Left) == 0 {
			m.AddBlock(Block{Kind: "notice", Text: "deleted key " + msg.Prov + " " + msg.Masked + " (last key) → pi — reconnecting…"})
		} else {
			m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("deleted key %s %s — switched to %s (%d left) → pi, reconnecting…",
				msg.Prov, msg.Masked, pirpc.MaskKey(msg.Left[0]), len(msg.Left))})
		}
		m.Refresh()
		return m, m.RespawnPi()

	case LoginRenameOpenMsg:
		// Push the rename prompt on top of /login (login stays at [1] so
		// Enter/Esc returns to it, never to the main screen).
		pre := ""
		if msg.Idx < len(msg.Items) {
			pre = msg.Items[msg.Idx].Name
		}
		r := &Dialog{Kind: "rename", Title: "Rename key — " + msg.Provider,
			Message:       "Name is display-only (pitago keystore). Empty clears it. Enter saves, Esc back to /login.",
			Filter:        pre,
			LoginProvider: msg.Provider, LoginEnv: msg.Env,
			RenameIdx: msg.Idx}
		m.Dialogs = append([]*Dialog{r}, m.Dialogs...)
		m.Refresh()
		return m, nil

	case LogoutListMsg:
		if len(msg.Opts) == 0 {
			m.AddBlock(Block{Kind: "notice", Text: "nothing to remove — no keys or pi logins"})
			m.Refresh()
			return m, nil
		}
		d := &Dialog{Kind: "logout", Title: "Remove login",
			Message: "Pick a provider: deletes its ACTIVE key, or disconnects OAuth (pi too).",
			Options: msg.Opts, Descs: msg.Descs}
		if msg.Arg != "" {
			d.Filter = msg.Arg
		}
		d.Reindex()
		m.Dialogs = append(m.Dialogs, d)
		m.Refresh()
		return m, nil

	case LogoutDoneMsg:
		switch msg.Kind {
		case "no-keys":
			m.AddBlock(Block{Kind: "notice", Text: "no saved keys for " + msg.Provider})
			m.Refresh()
			return m, nil
		case "failed":
			m.AddBlock(Block{Kind: "notice", Text: "failed to delete key: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		if len(msg.Left) == 0 {
			m.AddBlock(Block{Kind: "notice", Text: "deleted key " + msg.Provider + " " + msg.Masked + " (last key) → pi — reconnecting…"})
		} else {
			m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("deleted active key %s %s — switched to %s (%d left) → pi, reconnecting…",
				msg.Provider, msg.Masked, pirpc.MaskKey(msg.Left[0]), len(msg.Left))})
		}
		m.Refresh()
		return m, m.RespawnPi()

	case LoginSyncedMsg:
		// The picker build needs the real Model and the union-import that
		// just landed, so it re-enters src/builtin's hidden continuation
		// instead of running as another blocking step.
		return m, m.RunBuiltin(BuiltinLoginDialog, msg.Arg)

	case LoginReloadMsg:
		// In-place "reload models" on an open /login: refresh the picker
		// first, then re-count models (same order as before). D is nil when
		// the OAuth guide was closed over no /login dialog.
		if msg.D != nil {
			m.RefreshLoginKeys(msg.D)
		}
		m.Status = "reloading models…"
		m.Refresh()
		return m, func() tea.Msg {
			models, err := m.Pi.GetModels()
			if err != nil {
				return SettingsRefreshMsg{Err: err}
			}
			return SettingsRefreshMsg{Notice: fmt.Sprintf("pi sees %d models", len(models))}
		}

	case OAuthGoneMsg:
		if msg.Err != nil {
			m.AddBlock(Block{Kind: "notice", Text: "failed to disconnect " + msg.Prov + ": " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		if msg.D != nil {
			m.RefreshLoginKeys(msg.D)
			msg.D.KeyCursor = loginKeysLen(msg.D) // land on the guide row
		}
		m.AddBlock(Block{Kind: "notice", Text: "disconnected OAuth " + msg.Prov + " (pi + pitago) — reconnecting…"})
		m.Refresh()
		return m, m.RespawnPi()

	case SettingWrittenMsg:
		if msg.Err != nil {
			m.Status = "ready"
			m.AddBlock(Block{Kind: "notice", Text: "settings write failed: " + msg.Err.Error(), Err: true})
			m.Refresh()
			return m, nil
		}
		// The write already landed, so respawning here makes pi pick the
		// new value up — the same write-then-respawn order as before.
		if msg.Path == "terminal.showImages" || msg.Path == "terminal.imageWidthCells" {
			m.ApplyImageSettings() // this TUI owns rendering; invalidate it immediately
		}
		msg.D.Options, msg.D.Descs, msg.D.Providers, msg.D.Settings = msg.Opts, msg.Descs, msg.Cats, msg.St
		msg.D.Reindex()
		m.Status = "reconnecting pi…"
		m.Refresh()
		return m, m.RespawnPi()

	case UpdateCheckMsg:
		m.handleUpdateCheck(msg)
		return m, nil

	case UpdateDoneMsg:
		m.handleUpdateDone(msg)
		return m, nil

	case respawnMsg:
		m.respawning = false
		if msg.err != nil {
			m.Status = "ready"
			m.AddBlock(Block{Kind: "notice", Text: "pi reconnect failed: " + msg.err.Error() + " — restart the TUI", Err: true})
			m.Refresh()
			return m, nil
		}
		m.Pi = msg.client
		if m.liveBridge != nil {
			m.liveBridge.SetOwnPID(msg.client.PID())
		}
		msg.client.SetOnEvent(func(e pirpc.Event) { ProgRef.Send(piEventMsg{e}) })
		m.Status = "reloading…"
		m.Refresh()
		return m, m.fetchAll()

	case CmdsRefreshMsg:
		if msg.Err == nil {
			merged := mergeCommands(m.builtins, msg.Cmds)
			if cmdSig(merged) != cmdSig(m.Cmds) {
				if msg.Announce {
					old := make(map[string]bool, len(m.Cmds))
					for _, c := range m.Cmds {
						old[c.Source+"/"+c.Name] = true
					}
					var added []string
					for _, c := range merged {
						if !old[c.Source+"/"+c.Name] {
							added = append(added, "/"+c.Name)
						}
					}
					if len(added) == 0 {
						m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("no new commands (total %d)", len(merged))})
					} else {
						m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("new commands: %s (total %d)", strings.Join(added, ", "), len(merged))})
					}
				}
				m.Cmds = merged
				m.refreshCmds()
				m.refreshAt()
				m.Status = "ready"
				m.Refresh()
			} else if msg.Announce {
				m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("no new commands (total %d)", len(m.Cmds))})
				m.Status = "ready"
				m.Refresh()
			}
		} else if msg.Announce {
			m.AddBlock(Block{Kind: "notice", Text: "failed to load commands: " + msg.Err.Error(), Err: true})
			m.Status = "ready"
			m.Refresh()
		}
		return m, m.pollCmds()

	case MarketMsg:
		marketInflight = false
		if msg.Err != nil {
			m.MarketErr = Short(msg.Err.Error(), 80)
			m.AddBlock(Block{Kind: "notice", Text: "marketplace failed: " + m.MarketErr, Err: true})
		} else if msg.Append {
			m.Market = append(m.Market, msg.Entries...)
			m.MarketErr = ""
			marketCacheData, marketCacheAt, marketTotal = m.Market, time.Now(), msg.Total
			m.Status = "ready"
		} else {
			m.Market, m.MarketErr = msg.Entries, ""
			marketCacheData, marketCacheAt, marketTotal = msg.Entries, time.Now(), msg.Total
			m.Status = "ready"
		}
		m.reloadHubRows(PsecMarket)
		m.Refresh()
		return m, nil

	case PluginChangeMsg:
		pluginCacheAt = time.Time{} // force getPlugins to refetch
		m.Plugins = getPlugins()
		if msg.Err != nil {
			detail := msg.Out
			if detail == "" {
				detail = msg.Err.Error()
			}
			m.AddBlock(Block{Kind: "notice",
				Text: fmt.Sprintf("%s %s failed: %s", msg.Action, msg.Spec, detail), Err: true})
			m.Status = "ready"
			m.reloadHubRows("")
			m.Refresh()
			return m, nil
		}
		verb := "installed"
		if msg.Action == "remove" {
			verb = "removed"
		}
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("%s %s — reconnecting pi to load it…", verb, msg.Spec)})
		m.reloadHubRows("")
		m.Refresh()
		return m, m.RespawnPi()

	case tea.MouseMsg:
		// Click (release) on the sidebar: PLUGINS header collapses/expands,
		// a recent model switches to it. Terminals report release with
		// Button None (SGR `m` / X10 code 3 carry no button), so match any
		// Release — requiring Left never fires on a real terminal.
		if msg.Action == tea.MouseActionRelease {
			if m.pluginToggleAt(msg.X, msg.Y) {
				m.TogglePlugins()
				return m, nil
			}
			if idx, ok := m.recentAt(msg.X, msg.Y); ok {
				r := m.recentModels[idx]
				if r.ID == m.ModelLbl || r.DispLabel() == m.ModelLbl {
					return m, nil // already current
				}
				return m, m.SwitchToRecent(idx)
			}
		}
		// else: fall through so viewport/textarea get wheel-scroll etc.

	case tea.KeyMsg:
		// Alt+M: toggle mouse capture (mnemonic; Ctrl+M == Enter in terminals).
		if msg.Alt && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 &&
			(msg.Runes[0] == 'm' || msg.Runes[0] == 'M') {
			return m, m.ToggleMouse("")
		}
		// Alt+1..5: jump straight to a recent model (best-effort per terminal).
		if msg.Alt && len(msg.Runes) == 1 {
			if n := int(msg.Runes[0] - '1'); n >= 0 && n < len(m.recentModels) && n < maxRecent {
				return m, m.SwitchToRecent(n)
			}
		}
		// Hub-assigned Alt shortcuts: stage the /command for review
		// (Enter sends, like palette Enter). Stale entries (uninstalled
		// since) toast instead of firing into "unknown command".
		if label, ok := shortcutLabelOf(msg); ok && !shortcutReserved(label) {
			if cmd, ok := m.findCmdShortcut(label); ok {
				if m.hasCmd(cmd) {
					m.FillCommand(cmd)
				} else {
					m.AddBlock(Block{Kind: "notice", Text: "/" + cmd + " not installed — shortcut kept"})
					m.Refresh()
				}
				return m, nil
			}
		}
		// Alt+↑↓ PgUp PgDn Home End, or Ctrl+↑↓ PgUp PgDn Home End:
		// scroll sidebar without a mouse. Wheel needs --mouse; Alt is
		// broken on some terminals (macOS Option), so Ctrl is the
		// reliable default. Plain ↑↓ stays on chat scroll.
		if m.showSide() {
			if msg.Alt {
				switch msg.Type {
				case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
					km := msg
					km.Alt = false // viewport KeyMap matches "up", not "alt+up"
					var c tea.Cmd
					m.sideVp, c = m.sideVp.Update(km)
					return m, c
				}
			}
			if plain, ok := sideScrollKey(msg.Type); ok {
				km := msg
				km.Type = plain // viewport KeyMap matches plain keys
				var c tea.Cmd
				m.sideVp, c = m.sideVp.Update(km)
				return m, c
			}
		}
		if m.atOpen && m.handleAtKey(msg) {
			if msg.Type == tea.KeyEsc {
				m.escArm = time.Time{} // Esc closed a popup: reset cancel arm
			}
			return m, nil
		}
		if m.cmdOpen && m.handleCmdKey(msg) {
			if msg.Type == tea.KeyEsc {
				m.escArm = time.Time{} // Esc closed a popup: reset cancel arm
			}
			return m, nil
		}
		// trayFocus: nav keys stay in the tray, everything else exits it
		// and processes normally (typing lands in the input, Enter sends).
		if m.trayFocus {
			if cmd, done := m.handleTrayKey(msg); done {
				if msg.Type == tea.KeyEsc {
					m.escArm = time.Time{} // Esc left the tray: reset cancel arm
				}
				return m, cmd
			}
			m.exitTray()
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			// Double-press to quit within 3s: a stray Ctrl+C only arms
			// (warning pinned to the sidebar corner) and auto-disarms.
			// Esc is the double-press mid-turn cancel key (same window).
			if m.quitArmed() {
				return m, tea.Quit
			}
			m.quitArm = time.Now()
			m.quitGen++
			m.syncSideH()
			m.Refresh()
			return m, quitDisarmCmd(m.quitGen)
		case tea.KeyCtrlB:
			m.ToggleSide()
			return m, nil
		case tea.KeyCtrlY:
			return m, m.YankLast()
		case tea.KeyCtrlV:
			// Owned here (not the textarea): multi-backend read + visible
			// errors instead of the silent built-in paste.
			m.histIdx = -1 // pasting edits live input, leaves history browse
			return m, m.pasteCmd(false)
		case tea.KeyShiftUp:
			// Explicit recall: terminals never emit Shift+↑↓ for wheel
			// (with mouse reporting off wheel arrives as plain ↑↓), so
			// this is wheel-proof in both mouse modes.
			if m.tryHistPrev() {
				return m, nil
			}
		case tea.KeyShiftDown:
			if m.tryHistNext() {
				return m, nil
			}
		case tea.KeyUp:
			// Empty single-line input → recall previous sent message.
			// While browsing, ↑ keeps going older (stays at oldest).
			// Mouse-off guard: with reporting off the terminal turns
			// wheel scrolls into plain ↑↓ (indistinguishable from keys),
			// which must scroll the chat, never rewrite the input.
			if m.Mouse && m.tryHistPrev() {
				return m, nil
			}
		case tea.KeyDown:
			// While browsing, ↓ goes newer (tray waits — history wins).
			// Empty input + tray → cursor moves into the [Image N] row.
			// Same mouse-off guard as ↑ (plain ↓ may be a wheel scroll).
			if m.histBrowsing() {
				if m.Mouse && m.tryHistNext() {
					return m, nil
				}
			} else if len(m.imgAtts) > 0 && m.onLastLine() {
				m.enterTray()
				return m, nil
			} else if m.Mouse && m.tryHistNext() {
				return m, nil
			}
		case tea.KeyBackspace:
			// Empty input + tray → pop the last [Image N] chip.
			if m.ta.Value() == "" && len(m.imgAtts) > 0 {
				m.imgAtts = m.imgAtts[:len(m.imgAtts)-1]
				m.applyPopupH()
				m.Refresh()
				return m, nil
			}
		case tea.KeyCtrlO:
			return m, m.OpenYank()
		case tea.KeyCtrlG:
			m.expandTools = !m.expandTools
			m.RefreshFollow()
			return m, nil
		case tea.KeyCtrlR:
			return m, m.OpenRecents()
		case tea.KeyCtrlT:
			return m, m.CycleThinking()
		case tea.KeyCtrlN:
			m.Status = "opening new session…"
			m.Refresh()
			return m, func() tea.Msg {
				return SessionResetMsg{Err: m.Pi.NewSession()}
			}
		case tea.KeyCtrlP:
			m.Status = "switching model…"
			m.Refresh()
			return m, func() tea.Msg {
				label, err := m.Pi.CycleModel()
				return ModelCycleMsg{Label: label, Err: err}
			}
		case tea.KeyEsc:
			// History browse wins even mid-turn: first Esc leaves the
			// recalled message, it never aborts the turn.
			if m.histBrowsing() {
				m.clearHistInput()
				m.escArm = time.Time{}
				return m, nil
			}
			if m.thinking {
				// Double-press within 3s to cancel (Ctrl+C parity):
				// a stray Esc only arms + auto-disarms.
				if m.escArmed() {
					m.escArm = time.Time{}
					m.Status = "cancelling…"
					m.Refresh()
					return m, func() tea.Msg {
						steer, follow, _ := m.Pi.ClearQueue()
						restored := append(steer, follow...)
						if len(restored) > 0 {
							_ = restored // returned text, shown as notice for brevity
						}
						_, err := m.Pi.Abort()
						return sentAckMsg{err: err}
					}
				}
				m.escArm = time.Now()
				m.escGen++
				m.Status = "press Esc again to cancel…"
				m.Refresh()
				return m, escDisarmCmd(m.escGen)
			}
			return m, nil
		case tea.KeyEnter:
			return m, m.submitInput()
		}
	}

	var cmds []tea.Cmd
	var cmd tea.Cmd
	// Chat scroll: single-line input yields scroll keys to the viewport,
	// so input cursor and chat don't move together.
	if km, ok := msg.(tea.KeyMsg); ok && !strings.Contains(m.ta.Value(), "\n") {
		switch km.Type {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	// Mouse events never reach the textarea (it has no mouse handling —
	// feeding them in only risks echoing reports as text), just viewports.
	if _, isMouse := msg.(tea.MouseMsg); !isMouse {
		oldVal := ""
		wasBrowsing := m.histBrowsing()
		if wasBrowsing {
			oldVal = m.ta.Value()
		}
		m.ta, cmd = m.ta.Update(msg)
		cmds = append(cmds, cmd)
		if wasBrowsing && m.ta.Value() != oldVal {
			m.histIdx = -1 // edited a recalled message → back to live input
		}
		if km, ok := msg.(tea.KeyMsg); ok && km.Paste {
			m.collectDrops() // terminal drop/paste: long paths → [Image N] chips
		}
	}
	if m.ready {
		// wheel over the sidebar scrolls it, not the chat
		if mm, ok := msg.(tea.MouseMsg); ok && mm.Action == tea.MouseActionPress &&
			(mm.Button == tea.MouseButtonWheelUp || mm.Button == tea.MouseButtonWheelDown) &&
			m.overSide(mm.X) {
			m.sideVp, cmd = m.sideVp.Update(msg)
		} else {
			m.vp, cmd = m.vp.Update(msg)
		}
		cmds = append(cmds, cmd)
	}
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
	return m, tea.Batch(cmds...)
}

// quitDisarmCmd expires the arm after the window (gen guards stale ticks).
func quitDisarmCmd(gen int) tea.Cmd {
	return tea.Tick(quitArmWindow, func(time.Time) tea.Msg {
		return quitDisarmMsg{gen: gen}
	})
}

// escDisarmCmd expires the Esc cancel arm after the window (Ctrl+C parity).
func escDisarmCmd(gen int) tea.Cmd {
	return tea.Tick(escArmWindow, func(time.Time) tea.Msg {
		return escDisarmMsg{gen: gen}
	})
}

func (m Model) handleEvent(ev pirpc.Event) (tea.Model, tea.Cmd) {
	var pcmd tea.Cmd
	switch ev.Type {
	case "agent_start":
		m.thinking = true
		m.escArm = time.Time{} // fresh turn drops a stale cancel arm
		m.Status = "pi is running…"
		pcmd = m.petAnchor()
	case "turn_start":
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
		m.turnStart = time.Now()
		m.turnOutBase = m.Stats.Out
	case "message_update":
		pcmd = m.applyDelta(ev.Raw)
	case "message_end":
		pcmd = m.applyMessageEnd(ev.Raw)
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
	case "tool_execution_start":
		var p struct {
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Args       json.RawMessage `json:"args"`
			Details    json.RawMessage `json:"details"`
			Input      json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(ev.Raw, &p)
		i := m.ensureTool(p.ToolCallID, p.ToolName)
		m.setToolArgs(i, p.ToolName, string(p.Args))
		m.blocks[i].ToolStatus = "running"
		if isTodoTool(p.ToolName) {
			m.updateTodosFromRaw(p.Args, p.Details, p.Input)
		}
		pcmd = m.petSet(petWorking)
	case "tool_execution_update":
		var p struct {
			ToolCallID    string `json:"toolCallId"`
			PartialResult struct {
				Content []pirpc.ContentBlock `json:"content"`
			} `json:"partialResult"`
		}
		_ = json.Unmarshal(ev.Raw, &p)
		i := m.ensureTool(p.ToolCallID, "")
		m.blocks[i].ToolResult = joinText(p.PartialResult.Content)
	case "tool_execution_end":
		var p struct {
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
			Result     struct {
				Content []pirpc.ContentBlock `json:"content"`
				Details json.RawMessage      `json:"details"`
			} `json:"result"`
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal(ev.Raw, &p)
		i := m.ensureTool(p.ToolCallID, p.ToolName)
		if p.IsError {
			m.blocks[i].ToolStatus = "error"
		} else {
			m.blocks[i].ToolStatus = "done"
		}
		m.blocks[i].ToolResult = joinText(p.Result.Content)
		// Always keep the diff, even when content is non-empty: pi's edit
		// tool always reports a "Successfully replaced ..." line, so
		// falling back to the diff only when ToolResult is empty means the
		// change is never rendered.
		m.blocks[i].ToolDiff = diffOfDetails(p.Result.Details)
		if m.blocks[i].ToolResult == "" {
			m.blocks[i].ToolResult = m.blocks[i].ToolDiff
		}
		if isTodoTool(p.ToolName) {
			f := envFields(ev.Raw, "details", "result")
			m.updateTodosFromRaw(p.Result.Details, todosOf(p.Result.Details),
				f["details"], f["result"],
				json.RawMessage(joinText(p.Result.Content)))
			// pi-tasks single-task ops (no full list): fold in place.
			m.applyPiTaskResult(p.ToolName, m.blocks[i].ToolArgsRaw, m.blocks[i].ToolResult)
			if isPiTaskStoreTool(p.ToolName) {
				m.refreshPiTasks() // store file is fresh on disk by now
			}
		}
	case "agent_settled":
		m.thinking = false
		m.escArm = time.Time{} // turn over: cancel arm no longer applies
		m.Status = "ready"
		m.pendSpeed = true
		m.MCP = getMcpServers()
		m.Plugins = getPlugins()
		m.refreshPiTasks()
		m.Refresh()
		return m, tea.Batch(m.queryStats(), m.fetchCmdsOnce(), m.fetchStateOnce(), m.wsRefresh(), m.petSettled())
	case "agent_end":
		// Per-round boundary, NOT turn end: pi runs agent_start…agent_end
		// per round and agent_settled once when fully idle (see pi's
		// _runAgentPrompt: prompt/continue loop, settled in finally).
		// Keep thinking/Status/pet untouched so the footer keeps showing
		// "running" + ticking Working/Thinking across thinking↔tool
		// rounds; agent_settled (+ the IsStreaming reconciler) owns the
		// terminal reset.
		m.Refresh()
		return m, nil
	case "extension_ui_request":
		return m.handleUIRequest(ev.Raw), nil
	case "queue_update":
		m.queue = pirpc.ParseQueue(ev.Raw)
	case "compaction_start":
		m.AddBlock(Block{Kind: "notice", Text: "compacting context…"})
	case "compaction_end":
		m.AddBlock(Block{Kind: "notice", Text: "context compacted"})
	case "auto_retry_start":
		m.AddBlock(Block{Kind: "notice", Text: "provider error, retrying…"})
	case "auto_retry_end":
		var p struct {
			Success bool `json:"success"`
		}
		_ = json.Unmarshal(ev.Raw, &p)
		if !p.Success {
			m.AddBlock(Block{Kind: "notice", Text: "retry failed", Err: true})
		}
	case "extension_error":
		var p struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(ev.Raw, &p)
		m.AddBlock(Block{Kind: "notice", Text: "extension error: " + p.Error, Err: true})
	case "pi_exited":
		m.thinking = false
		m.escArm = time.Time{}
		m.pet = petState{}
		m.clearTeamWidgetState()
		if m.respawning {
			break // intentional reconnect, respawnMsg will follow
		}
		m.Status = "pi has exited"
		text := "pi has exited — Ctrl+C to close the TUI"
		if reason := pirpc.StderrTail(); reason != "" {
			text = "pi has exited (" + reason + ") — Ctrl+C to close the TUI"
		}
		m.AddBlock(Block{Kind: "notice", Text: text, Err: true})
	}
	taskCmd := m.ensureTaskTick()
	// High-frequency streaming events share one paint per frame (plus a
	// trailing flush tick); everything else repaints immediately.
	if ev.Type == "message_update" || ev.Type == "tool_execution_update" {
		if flush := m.refreshStreaming(); flush != nil {
			return m, tea.Batch(pcmd, taskCmd, flush)
		}
		return m, tea.Batch(pcmd, taskCmd)
	}
	m.Refresh()
	return m, tea.Batch(pcmd, taskCmd)
}

func (m *Model) applyDelta(raw []byte) tea.Cmd {
	var mu pirpc.MessageUpdate
	if err := json.Unmarshal(raw, &mu); err != nil {
		return nil
	}
	d := mu.Event
	switch d.Type {
	case "text_start":
		m.ensureAsst()
		if m.pet.inTurn {
			return m.petSet(petWriting)
		}
	case "text_delta":
		m.asstDelta = true
		m.blocks[m.ensureAsst()].Text += d.Delta
		if m.pet.inTurn {
			return m.petSet(petWriting)
		}
	case "thinking_start":
		m.ensureThink()
		if m.pet.inTurn {
			return m.petSet(petThinking)
		}
	case "thinking_delta":
		m.thinkDelta = true
		m.blocks[m.ensureThink()].Text += d.Delta
		if m.pet.inTurn {
			return m.petSet(petThinking)
		}
	case "toolcall_start":
		m.ensureTool(d.ID, d.ToolName)
		if m.pet.inTurn {
			return m.petSet(petWorking)
		}
	case "toolcall_end":
		if d.ToolCall != nil {
			i := m.ensureTool(d.ToolCall.ID, d.ToolCall.Name)
			m.setToolArgs(i, d.ToolCall.Name, string(d.ToolCall.Arguments))
		}
		if m.pet.inTurn {
			return m.petSet(petWorking)
		}
	case "done":
		// stop → the model is done for good; any other reason means
		// more rounds are coming (tools already flipped us to working).
		if d.Reason == "stop" {
			m.pet.sawStop = true
			if m.pet.inTurn {
				return m.petSet(petSuccess)
			}
		}
	case "error":
		m.pet.inTurn = false
		return m.petSet(petError)
	}
	return nil
}

func (m *Model) applyMessageEnd(raw []byte) tea.Cmd {
	var env struct {
		Message pirpc.AgentMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	msg := env.Message
	if msg.Role == "assistant" && msg.Usage != nil {
		// message_end is the authoritative per-message total; unlike the
		// streaming snapshot it also covers tool-only assistant messages.
		m.addTaskUsage(msg.Usage.Input, msg.Usage.Output)
	}
	switch msg.Role {
	case "user":
		if t := withImages(pirpc.TextOf(msg.Content), pirpc.ImageCount(msg.Content)); strings.TrimSpace(t) != "" {
			m.AddBlock(Block{Kind: "user", Text: t, Images: chatImages(msg.Content)})
		}
	case "custom":
		if !msg.Display {
			break
		}
		text := strings.TrimSpace(pirpc.TextOf(msg.Content))
		if text == "" {
			break
		}
		if isTeamDashboardText(text) {
			m.openTeamDashboard(text)
		} else if isTeamDetailText(text) {
			m.openTeamDetail(text)
		} else {
			m.addChatNotice(text, false)
		}
	case "assistant":
		if msg.StopReason == "error" && msg.ErrorMessage != "" {
			m.AddBlock(Block{Kind: "notice", Text: "pi error: " + msg.ErrorMessage, Err: true})
			m.pet.inTurn = false
			return m.petSet(petError)
		}
		for _, b := range pirpc.BlocksOf(msg.Content) {
			switch b.Type {
			case "text":
				if m.asstDelta {
					break // already streamed — re-adding would duplicate
				}
				if m.curAsst >= 0 && m.blocks[m.curAsst].Text == "" {
					m.blocks[m.curAsst].Text = b.Text
				} else if strings.TrimSpace(b.Text) != "" {
					m.curAsst = m.AddBlock(Block{Kind: "assistant", Text: b.Text})
				}
			case "thinking":
				if m.thinkDelta {
					break // already streamed — re-adding would duplicate
				}
				if m.curThink >= 0 && m.blocks[m.curThink].Text == "" {
					m.blocks[m.curThink].Text = b.Thinking
				} else if strings.TrimSpace(b.Thinking) != "" {
					m.curThink = m.AddBlock(Block{Kind: "thinking", Text: b.Thinking})
				}
			case "toolCall":
				i := m.ensureTool(b.ID, b.Name)
				m.setToolArgs(i, b.Name, string(b.Arguments))
			}
		}
	case "toolResult":
		text := joinTextBlocks(pirpc.BlocksOf(msg.Content))
		if text == "" {
			text = pirpc.TextOf(msg.Content)
		}
		if text == "" {
			text = diffOfDetails(msg.Details)
		}
		if isTodoTool(msg.ToolName) {
			m.updateTodosFromRaw(msg.Details, msg.Content)
			if t := strings.TrimSpace(text); t != "" {
				m.updateTodosFromRaw(json.RawMessage(t))
			}
			argsRaw := ""
			if i, ok := m.tools[msg.ToolCallID]; ok {
				argsRaw = m.blocks[i].ToolArgsRaw
			}
			m.applyPiTaskResult(msg.ToolName, argsRaw, text)
			if isPiTaskStoreTool(msg.ToolName) {
				m.refreshPiTasks()
			}
		}
		status := "done"
		if msg.IsError {
			status = "error"
		}
		if i, ok := m.tools[msg.ToolCallID]; ok {
			if m.blocks[i].ToolStatus == "running" {
				m.blocks[i].ToolStatus = status
			}
			// Same as the live path: keep the diff regardless of content so
			// reopening a live session shows the change, not the receipt.
			if d := diffOfDetails(msg.Details); d != "" {
				m.blocks[i].ToolDiff = d
			}
			if m.blocks[i].ToolResult == "" {
				m.blocks[i].ToolResult = text
			}
		} else if strings.TrimSpace(msg.ToolName) != "" {
			i := m.ensureTool(msg.ToolCallID, msg.ToolName)
			m.blocks[i].ToolStatus = status
			m.blocks[i].ToolResult = text
		}
	case "bashExecution":
		out := msg.Output
		if len(out) > 2000 {
			out = out[:2000] + "…"
		}
		m.AddBlock(Block{Kind: "bash", Text: "$ " + msg.Command + "\n" + out})
	}
	return nil
}

// restore converts get_messages into blocks (two-pass via m.tools map).

func (m *Model) restore(msgs []pirpc.AgentMessage) {
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if t := withImages(strings.TrimSpace(pirpc.TextOf(msg.Content)), pirpc.ImageCount(msg.Content)); t != "" {
				m.AddBlock(Block{Kind: "user", Text: t, Images: chatImages(msg.Content)})
			}
			m.pushHist(strings.TrimSpace(pirpc.TextOf(msg.Content)))
		case "custom":
			if text := strings.TrimSpace(pirpc.TextOf(msg.Content)); msg.Display && text != "" {
				m.addChatNotice(text, false)
			}
		case "assistant":
			for _, b := range pirpc.BlocksOf(msg.Content) {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						m.AddBlock(Block{Kind: "assistant", Text: b.Text})
					}
				case "toolCall":
					m.ensureTool(b.ID, b.Name)
					idx := m.tools[b.ID]
					m.blocks[idx].ToolStatus = "done"
					m.setToolArgs(idx, b.Name, string(b.Arguments))
				}
			}
			if t := strings.TrimSpace(pirpc.TextOf(msg.Content)); t != "" && len(pirpc.BlocksOf(msg.Content)) == 0 {
				m.AddBlock(Block{Kind: "assistant", Text: t})
			}
		case "toolResult":
			text := joinTextBlocks(pirpc.BlocksOf(msg.Content))
			if text == "" {
				text = pirpc.TextOf(msg.Content)
			}
			if text == "" {
				text = diffOfDetails(msg.Details)
			}
			if i, ok := m.tools[msg.ToolCallID]; ok {
				m.blocks[i].ToolStatus = "done"
				if msg.IsError {
					m.blocks[i].ToolStatus = "error"
				}
				m.blocks[i].ToolResult = text
				// Restoring a past session must show the diff too, not just
				// the receipt line the tool also reported.
				if d := diffOfDetails(msg.Details); d != "" {
					m.blocks[i].ToolDiff = d
				}
			}
		case "bashExecution":
			m.AddBlock(Block{Kind: "bash", Text: "$ " + msg.Command})
		}
	}
	m.curAsst, m.curThink = -1, -1
	m.asstDelta, m.thinkDelta = false, false
}

func joinText(blocks []pirpc.ContentBlock) string {
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	// pi caps tool output at 51200 bytes / 2000 lines; the collapsible
	// preview handles display, so keep the full text here (a 211-line
	// file is ~8KB and must survive for expand).
	if len(out) > maxToolResultChars {
		out = out[:maxToolResultChars] + "…"
	}
	return out
}

func joinTextBlocks(blocks []pirpc.ContentBlock) string { return joinText(blocks) }

// chatImages preserves image payloads from RPC user blocks for terminal render.
func chatImages(raw json.RawMessage) []chat.Image {
	var out []chat.Image
	for _, img := range pirpc.ImagesOf(raw) {
		if img.Data == "" || img.MimeType == "" {
			continue
		}
		out = append(out, chat.NewImage(img.Data, img.MimeType))
	}
	return out
}

// withImages appends a 📷 suffix for vision echoes (pi returns user content
// as text + image blocks; TextOf drops the images, so count them back).
func withImages(t string, n int) string {
	if n <= 0 {
		return t
	}
	s := "📷 1 image attached"
	if n > 1 {
		s = fmt.Sprintf("📷 %d images attached", n)
	}
	if strings.TrimSpace(t) == "" {
		return s
	}
	return t + "\n" + s
}

// diffOfDetails pulls an edit diff out of a toolResult details payload
// ({"diff": "..."}), so edit blocks can preview the change like pi.
func diffOfDetails(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var d struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return ""
	}
	return d.Diff
}

// extension UI ---------------------------------------------------------------

// fireUI answers an extension_ui_request. Pi is nil in tests
// (New(nil, …)) where nobody waits — skip the write instead of panicking.
func (m Model) fireUI(cmd pirpc.Command) {
	if m.Pi != nil {
		_ = m.Pi.Fire(cmd)
	}
}

// handleUIRequest routes extension_ui_request events.
// Protocol knowledge (methods, defaults, response shape) lives in
// src/extension; this only mutates UI state.
func (m Model) handleUIRequest(raw []byte) Model {
	var req pirpc.UIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// A payload we cannot read must still resolve on pi's side: the
		// extension stays blocked on this request until it is answered or
		// times out. Recover the id leniently and cancel. When even that
		// fails there is no id in the payload, so pi has nothing pending
		// under that request and there is nothing to answer.
		if id, ok := looseUIRequestID(raw); ok {
			m.AddBlock(Block{Kind: "notice", Text: "plugin UI không đọc được — đã hủy: " + id, Err: true})
			m.fireUI(extension.FallbackResponse(id))
			m.Refresh()
		}
		return m
	}
	switch {
	case extension.ShouldAutoCancel(req.Method):
		// MVP: auto-cancel so the agent uses defaults/timeout
		m.AddBlock(Block{Kind: "notice", Text: "plugin muốn mở editor — đã dùng mặc định"})
		m.fireUI(extension.FallbackResponse(req.ID))
	case req.Method == "select" || req.Method == "confirm":
		kind := "ui"
		title := extension.TitleFor(req.Method, req.Title)
		if pirpc.IsAskUserSelect(req) {
			kind = "askUser"
			if req.Title == "" {
				title = "Ask User"
			}
		}
		d := &Dialog{
			ID: req.ID, Method: req.Method, Kind: kind,
			Title: title, Message: req.Message,
			Options: extension.OptionsFor(req), Descs: extension.DescriptionsFor(req),
		}
		d.Reindex()
		m.Dialogs = append(m.Dialogs, d)
		m.applyPopupH()
	case req.Method == "input":
		// free-text prompt (e.g. pi-tasks createTask subject/description):
		// a real typing dialog, Esc cancels, Enter submits.
		title := req.Title
		if title == "" {
			title = "Input"
		}
		d := &Dialog{
			ID: req.ID, Method: req.Method, Kind: "input",
			Title: title, Message: req.Message, Filter: req.Text,
		}
		m.Dialogs = append(m.Dialogs, d)
		m.applyPopupH()
	case req.Method == "notify":
		// Subagent integrations report worker progress through notify. Keep
		// those updates in the chat transcript; ordinary extension notices
		// remain ephemeral toasts.
		if isSubagentProgressMessage(req.Message) {
			m.addChatNotice(req.Message, req.NotifyType == "error")
		} else {
			m.AddBlock(Block{Kind: "notice", Text: req.Message, Err: req.NotifyType == "error"})
		}
	case req.Method == "setStatus":
		// pi-agents-team keeps first refusal for its own key (its status
		// feeds the dashboard, not the footer). Everything else lands in
		// the per-key registry: the old single `m.extStat` slot was
		// last-writer-wins, so any plugin — pi-lens, lsp, fff, web-activity,
		// plan-mode, mcp — silently stole the footer from the team and
		// from every sibling.
		if !m.setTeamStatus(req.StatusKey, req.StatusText) {
			m.setExtStatus(req.StatusKey, req.StatusText)
		}
	case req.Method == "set_editor_text":
		m.ta.SetValue(req.Text)
		m.histIdx = -1
	case req.Method == "setWidget":
		// RPC mode carries string arrays only (component factories are
		// ignored pi-side). Two keys stay special-cased because they are
		// load-bearing and covered by tests: the team dashboard replaces
		// the editor block in place, and the async subagent progress
		// widget keeps its single in-place transcript block.
		// Every other key now gets a real panel — downgrading it to a
		// one-shot notice is what made pi-lens / plan-mode / web-activity
		// appear for an instant and then vanish.
		switch {
		case isTeamWidget(req.WidgetKey):
			m.setTeamWidget(req.WidgetLines, req.WidgetPlacement)
		case isAgentProgressWidget(req.WidgetKey):
			if len(req.WidgetLines) == 0 {
				m.clearChatProgressWidget(req.WidgetKey)
			} else {
				m.setChatProgressWidget(req.WidgetKey, extWidgetText(req.WidgetKey, req.WidgetLines), false)
			}
		default:
			// Empty lines is pi's "this widget is gone" signal, and
			// setExtWidget routes it to clearExtWidget: one key only.
			m.setExtWidget(req.WidgetKey, req.WidgetLines, req.WidgetPlacement)
		}
	case req.Method == "setTitle":
		// terminal window title: no TUI surface, ignore per protocol
		// (fire-and-forget methods may be ignored).
	default:
		// Unknown future method: toast so it stays visible, then cancel
		// so a dialog-like request never hangs the agent. Pi ignores
		// responses with no pending request, so this is equally safe
		// for fire-and-forget-likes.
		m.AddBlock(Block{Kind: "notice", Text: "plugin UI chưa hỗ trợ: " + req.Method, Err: true})
		m.fireUI(extension.FallbackResponse(req.ID))
	}
	// Any fresh extension prompt arrives after the extension ran code that
	// may have rewritten its store file (e.g. pi-tasks createTask writes
	// between the description answer and the reopened menu), so sync the
	// sidebar here — answering time is one step too early.
	m.refreshPiTasks()
	m.Refresh()
	return m
}

// extWidgetText formats a widget payload the way the transcript block
// expects: an owner tag, then the plugin's own lines verbatim.
func extWidgetText(key string, lines []string) string {
	label := strings.TrimSpace(stripANSI(key))
	if label == "" {
		label = "plugin"
	}
	return "[" + label + "]\n" + strings.Join(lines, "\n")
}

// maxQueuedDialogs bounds the parked extension dialog queue. Past this the
// oldest entry is cancelled, so a plugin that spams menus can never grow
// the queue without limit — and, more importantly, can never leave one of
// its requests unanswered.
const maxQueuedDialogs = 32

// queueDialogRequest parks a raw extension_ui_request while a dialog is
// open. FIFO: plugins ask permission in causal order (pi-tasks
// createTask → description → menu) and a reorder would answer the wrong
// question. Overflow cancels the oldest so nothing blocks forever.
func (m *Model) queueDialogRequest(raw []byte) {
	if len(raw) == 0 {
		return
	}
	if len(m.queuedDialogs) >= maxQueuedDialogs {
		oldest := m.queuedDialogs[0]
		m.queuedDialogs = m.queuedDialogs[1:]
		m.cancelQueuedRequest(oldest)
	}
	m.queuedDialogs = append(m.queuedDialogs, append([]byte(nil), raw...))
}

// cancelQueuedRequest answers a parked request we are refusing to show.
// Fire-and-forget payloads carry no dialog, so this is the only thing that
// keeps a dropped request from stalling the extension.
func (m *Model) cancelQueuedRequest(raw []byte) {
	var req pirpc.UIRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.ID == "" {
		return
	}
	m.AddBlock(Block{Kind: "notice", Text: "plugin menu bị bỏ qua (hàng đợi đầy): " + req.ID, Err: true})
	m.fireUI(extension.FallbackResponse(req.ID))
}

// drainQueuedDialogs promotes the oldest parked request once the dialog
// stack is empty. Fire-and-forget payloads never open a dialog, so the loop
// cannot spin: each pass either consumes one queue entry or leaves a dialog
// on the stack and stops. maxQueuedDialogs bounds it as a second guard.
func (m *Model) drainQueuedDialogs() {
	for i := 0; len(m.Dialogs) == 0 && i < maxQueuedDialogs; i++ {
		if len(m.queuedDialogs) == 0 {
			return
		}
		raw := m.queuedDialogs[0]
		m.queuedDialogs = m.queuedDialogs[1:]
		*m = m.handleUIRequest(raw)
	}
}

// looseUIRequestID recovers the request id from a payload that failed the
// strict UIRequest decode. Best effort on purpose: with no id there is no
// pending request to answer, and a guessed id would cancel someone else's
// dialog.
func looseUIRequestID(raw []byte) (string, bool) {
	var loose struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return "", false
	}
	switch v := loose.ID.(type) {
	case string:
		return v, v != ""
	case float64:
		return fmt.Sprintf("%.0f", v), true
	}
	return "", false
}

// clearChatProgressWidget retires one plugin's in-place transcript block
// and leaves sibling progress widgets alone. Empty widgetLines is pi's
// "widget is gone" signal; the old len>0 guard swallowed it, so the block
// stayed on screen for the rest of the session. The row is marked done
// rather than spliced out: deleting a mid-transcript block would shift
// every index behind it, including the other plugins' progress rows.
func (m *Model) clearChatProgressWidget(key string) {
	key = strings.ToLower(strings.TrimSpace(stripANSI(key)))
	i, ok := m.progressByKey[key]
	if !ok {
		return
	}
	delete(m.progressByKey, key)
	if i >= 0 && i < len(m.blocks) && m.blocks[i].Kind == "notice" {
		label := strings.TrimSpace(stripANSI(key))
		if label == "" {
			label = "plugin"
		}
		m.blocks[i].Text = "[" + label + "] done"
	}
}

// filterableDialog reports pickers whose typing filters the option list.
//
// isFilterKind (src/app/pconfig.go) is deliberately NOT extended: it is the
// generic-list predicate shared with the settings/login/session pickers, and
// the extension pickers need one extra discriminator those do not have —
// method. A "ui" *select* is a long, plugin-authored option list that was
// unnavigable without typing; a "ui" *confirm* must not swallow y/n as
// filter text, which is how the shortcuts are scoped.
func filterableDialog(d *Dialog) bool {
	if d == nil {
		return false
	}
	if isFilterKind(d.Kind) || d.Kind == "askUser" {
		return true
	}
	return d.Kind == "ui" && d.Method == "select"
}

func (m Model) updateDialog(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.Dialogs[0]
	if d.Kind == "team" {
		return m.updateTeamDashboardDialog(km, d)
	}
	if d.Kind == "pconfig" && len(d.Provs) > 0 {
		return m.updatePconfigDialog(km, d)
	}
	if d.Kind == "model" && len(d.Provs) > 0 {
		return m.updateModelDialog(km, d)
	}
	if d.Kind == "login" && len(d.Provs) > 0 {
		return m.updateLoginDialog(km, d)
	}
	if d.Kind == "settings" && len(d.Provs) > 0 {
		return m.updateSettingsDialog(km, d)
	}
	if d.Kind == shortcutKind {
		return m.updateShortcutDialog(km, d)
	}
	n := len(d.FIdx)
	switch km.Type {
	case tea.KeyUp:
		if n > 0 {
			if d.Cursor > 0 {
				d.Cursor--
			} else {
				d.Cursor = n - 1
			}
			m.previewTheme(d)
			d.TrajOff = 0 // new step → detail back to top
		}
		return m, nil
	case tea.KeyDown:
		if n > 0 {
			if d.Cursor < n-1 {
				d.Cursor++
			} else {
				d.Cursor = 0
			}
			m.previewTheme(d)
			d.TrajOff = 0 // new step → detail back to top
		}
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		if d.Kind == "tree" && n > 0 {
			page := treePageSize(m.winH)
			if km.Type == tea.KeyRight {
				d.Cursor += page
			} else {
				d.Cursor -= page
			}
			if d.Cursor < 0 {
				d.Cursor = 0
			}
			if d.Cursor >= n {
				d.Cursor = n - 1
			}
		}
		return m, nil
	case tea.KeyPgUp, tea.KeyPgDown:
		if d.Kind == "tree" {
			m.treePage(d, km.Type == tea.KeyPgDown)
		} else if d.Kind == "trajectory" {
			m.trajPage(d, km.Type == tea.KeyPgDown)
		} else if d.Kind == "notification" && n > 0 {
			page := notificationWindow(m.winH)
			if km.Type == tea.KeyPgDown {
				d.Cursor += page
			} else {
				d.Cursor -= page
			}
			if d.Cursor >= n {
				d.Cursor = n - 1
			}
			if d.Cursor < 0 {
				d.Cursor = 0
			}
		}
		return m, nil
	case tea.KeyCtrlC:
		// Dialog capture runs before the global ^C quit arm, so without an
		// explicit case here a full-height plugin picker had no way out:
		// ^C fell through to the rune/junk tail and did nothing.
		return m.dismissDialog(d)
	case tea.KeyBackspace:
		if (filterableDialog(d) || d.Kind == "secret" || d.Kind == "rename" || d.Kind == "input") && d.Filter != "" {
			r := []rune(d.Filter) // rune-wise: byte trim corrupts Vietnamese
			d.Filter = string(r[:len(r)-1])
			d.Reindex()
			m.applyPopupH() // unwrapped lines give rows back to the chat
			return m, nil
		}
		if d.Kind == "sessions" {
			return m.DeleteResumeSession(d) // ⌫ on empty filter deletes (login parity)
		}
		return m, nil
	case tea.KeyDelete:
		if d.Kind == "sessions" {
			return m.DeleteResumeSession(d)
		}
		return m, nil
	case tea.KeyCtrlD:
		if d.Kind == "sessions" {
			return m.DeleteResumeSession(d)
		}
		return m, nil
	case tea.KeyCtrlV:
		if d.Kind == "secret" || d.Kind == "input" {
			return m, m.pasteCmd(true) // paste into the dialog buffer
		}
		return m, nil
	case tea.KeyEsc:
		return m.dismissDialog(d)
	case tea.KeyTab:
		if d.Kind == "sessions" {
			return m, m.ReloadResumeScope(d) // pi: Tab toggles Current/All
		}
		return m, nil
	case tea.KeyEnter:
		if d.Kind == "shortcuts" { // help page: Enter closes like Esc
			m.Dialogs = m.Dialogs[1:]
			m.refreshPiTasks()
			m.drainQueuedDialogs()
			m.Refresh()
			return m, m.ReconcileTurnCmd()
		}
		return m.confirmDialog(d)
	}
	if km.Type == tea.KeySpace {
		// space arrives as its own key (not Runes): typing contexts take
		// it literally, option lists keep ignoring it.
		if filterableDialog(d) {
			d.Filter += " "
			d.Reindex()
			return m, nil
		}
		if d.Kind == "secret" || d.Kind == "rename" || d.Kind == "input" {
			d.Filter += " "
			m.applyPopupH() // long input wraps: keep the winH budget
			return m, nil
		}
	}
	if km.Type == tea.KeyRunes {
		if filterableDialog(d) {
			// type to filter the picker
			d.Filter += km.String()
			d.Reindex()
			return m, nil
		}
		if d.Kind == "secret" || d.Kind == "rename" || d.Kind == "input" {
			d.Filter += km.String()
			m.applyPopupH() // long input wraps: keep the winH budget
			return m, nil
		}
		s := strings.ToLower(km.String())
		// y/n are confirm-only. Scoped to d.Method (not d.Kind) so they
		// never fire on a *select* — the old `d.Kind == "ui"` guard
		// answered a select with its last option, silently committing a
		// choice the user never looked at. Runes never reach here for a
		// select (filterableDialog), so this is belt-and-braces.
		if d.Method == "confirm" && s == "y" {
			m.answerDialog(d, 0)
			return m, nil
		}
		if d.Method == "confirm" && s == "n" {
			m.answerDialog(d, len(d.Options)-1)
			return m, nil
		}
	}
	return m, nil
}

// updateModelDialog navigates the two-pane model picker: left = providers,
// right = their models. ↑↓ moves in the focused pane, ←/→/Tab switches
// pane, typing filters, Enter on the left opens the right, Enter confirms.
func (m Model) updateModelDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	n := len(d.FIdx)
	switch km.Type {
	case tea.KeyUp:
		if d.ProvFocus {
			if len(d.Provs) > 0 {
				if d.ProvCursor > 0 {
					d.ProvCursor--
				} else {
					d.ProvCursor = len(d.Provs) - 1
				}
				d.Reindex()
				d.Cursor = 0
			}
		} else if n > 0 {
			if d.Cursor > 0 {
				d.Cursor--
			} else {
				d.Cursor = n - 1
			}
		}
		return m, nil
	case tea.KeyDown:
		if d.ProvFocus {
			if len(d.Provs) > 0 {
				if d.ProvCursor < len(d.Provs)-1 {
					d.ProvCursor++
				} else {
					d.ProvCursor = 0
				}
				d.Reindex()
				d.Cursor = 0
			}
		} else if n > 0 {
			if d.Cursor < n-1 {
				d.Cursor++
			} else {
				d.Cursor = 0
			}
		}
		return m, nil
	case tea.KeyLeft:
		if !d.ProvFocus {
			d.ProvFocus = true
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	case tea.KeyRight:
		if d.ProvFocus {
			d.ProvFocus = false
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	case tea.KeyBackspace:
		if d.Filter != "" {
			d.Filter = d.Filter[:len(d.Filter)-1]
			d.Reindex()
		}
		return m, nil
	case tea.KeyCtrlV:
		return m, nil
	case tea.KeyEsc:
		m.Dialogs = m.Dialogs[1:]
		m.refreshPiTasks()
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	case tea.KeyTab:
		d.ProvFocus = !d.ProvFocus
		d.Reindex()
		d.Cursor = 0
		return m, nil
	case tea.KeyCtrlL:
		// jump straight to provider login
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.RunBuiltin("login", "")
	case tea.KeyCtrlF:
		// star/unstar the highlighted model (stays in the picker)
		if d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
			ri := d.FIdx[d.Cursor]
			m.toggleFav(d, ri)
			d.Reindex()
			for i, v := range d.FIdx { // cursor follows the toggled model
				if v == ri {
					d.Cursor = i
					break
				}
			}
			m.Refresh()
		}
		return m, nil
	case tea.KeyEnter:
		if d.ProvFocus {
			d.ProvFocus = false
			d.Reindex()
			d.Cursor = 0
			return m, nil
		}
		return m.confirmDialog(d)
	}
	if km.Type == tea.KeyRunes {
		d.Filter += km.String()
		d.Reindex()
		// filter scope follows the focused pane (providers = global,
		// models = scoped), so typing stays where the user is.
		d.Cursor = 0
		return m, nil
	}
	return m, nil
}

// updateSettingsDialog navigates the two-pane /settings picker: left =
// groups, right = the selected group's rows. ↑↓ moves in the focused
// pane, ←/→/Tab switches pane, typing filters (left = global, right =
// scoped to the group), Enter on the left dives right, Enter on the
// right changes the value (confirm lives in src/builtin).
func (m Model) updateSettingsDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyUp, tea.KeyDown:
		down := km.Type == tea.KeyDown
		if d.ProvFocus {
			if n := len(d.Provs); n > 0 {
				if down {
					d.ProvCursor = (d.ProvCursor + 1) % n
				} else {
					d.ProvCursor = (d.ProvCursor - 1 + n) % n
				}
				d.Reindex()
				d.Cursor = 0
			}
		} else if n := len(d.FIdx); n > 0 {
			if down {
				d.Cursor = (d.Cursor + 1) % n
			} else {
				d.Cursor = (d.Cursor - 1 + n) % n
			}
		}
		return m, nil
	case tea.KeyLeft:
		if !d.ProvFocus {
			d.ProvFocus = true
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	case tea.KeyRight:
		if d.ProvFocus {
			d.ProvFocus = false
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	case tea.KeyTab:
		d.ProvFocus = !d.ProvFocus
		d.Reindex()
		d.Cursor = 0
		return m, nil
	case tea.KeyBackspace:
		if d.Filter != "" {
			r := []rune(d.Filter) // rune-wise: byte trim corrupts Vietnamese
			d.Filter = string(r[:len(r)-1])
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	case tea.KeyEsc:
		m.Dialogs = m.Dialogs[1:]
		m.refreshPiTasks()
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	case tea.KeyEnter:
		if d.ProvFocus {
			d.ProvFocus = false
			d.Reindex()
			d.Cursor = 0
			return m, nil
		}
		return m.confirmDialog(d)
	}
	if km.Type == tea.KeySpace {
		d.Filter += " "
		d.Reindex()
		d.Cursor = 0
		return m, nil
	}
	if km.Type == tea.KeyRunes {
		d.Filter += km.String()
		d.Reindex()
		d.Cursor = 0
		return m, nil
	}
	return m, nil
}

// uniqueGroups collects section names in first-appearance order, with an
// "All" entry on top that never scopes (model-picker parity).
func uniqueGroups(cats []string) []string {
	seen := map[string]bool{}
	out := []string{"All"}
	for _, c := range cats {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// confirmDialog handles Enter per dialog kind.

func (m Model) confirmDialog(d *Dialog) (tea.Model, tea.Cmd) {
	if d.Kind == "ui" || d.Kind == "askUser" {
		if len(d.FIdx) == 0 {
			return m, nil
		}
		choice := d.FIdx[d.Cursor]
		// /tasks → Settings can't cross RPC (pi stubs ui.custom as a
		// no-op, so answering it would just bounce back to the menu):
		// answer cancelled so the extension menu exits cleanly, and open
		// the native Tasks tab instead.
		if tasksSettingsJump(d, choice) {
			m.answerDialog(d, -1)
			m.openTasksSettings()
			m.Refresh()
			return m, nil
		}
		m.answerDialog(d, choice)
		return m, nil
	}
	if d.Kind == "secret" {
		// input dialog: no Options/FIdx, Enter saves the typed key
		if fn, ok := m.confirm["secret"]; ok {
			return fn(&m, d, 0)
		}
		return m, nil
	}
	if d.Kind == "rename" {
		// rename pops itself (login stays underneath) and saves via msg
		// so the pipeline works even with the picker open. The keystore
		// write is blocking file I/O, so it runs in the Cmd and reports
		// back with the same message.
		name := strings.TrimSpace(d.Filter)
		prov, env, idx := d.LoginProvider, d.LoginEnv, d.RenameIdx
		keyPath := m.KeyPath
		m.Dialogs = m.Dialogs[1:]
		m.drainQueuedDialogs()
		m.Refresh()
		return m, func() tea.Msg {
			msg := RenameKeyMsg{Provider: prov, Env: env, Idx: idx, Name: name}
			msg.Err = pirpc.RenameKey(keyPath, env, idx, name)
			return msg
		}
	}
	if d.Kind == "input" {
		// free-text prompt: Enter submits the typed value (even empty —
		// the extension treats empty as back/cancel).
		m.answerInput(d, false)
		return m, nil
	}
	if len(d.FIdx) == 0 {
		return m, nil
	}
	ri := d.FIdx[d.Cursor]
	if fn, ok := m.confirm[d.Kind]; ok {
		before := len(m.Dialogs)
		nm, cmd := fn(&m, d, ri)
		// A picker just closed mid-turn: re-check get_state so a settle
		// swallowed behind it can't stick the pet on Working....
		if um, ok := nm.(Model); ok && len(um.Dialogs) < before && um.thinking {
			if rc := um.ReconcileTurnCmd(); rc != nil {
				return um, tea.Batch(cmd, rc)
			}
		}
		return nm, cmd
	}
	return m, nil
}

// answerInput replies to an extension free-text input dialog (Enter submits
// the typed value, Esc cancels). Either way the extension resumes — e.g.
// pi-tasks createTask advances to the description prompt or back to its menu.
// dismissDialog closes the top dialog the way Esc does — used by Esc and by
// the new Ctrl+C case — and promotes the next parked extension request.
func (m Model) dismissDialog(d *Dialog) (tea.Model, tea.Cmd) {
	if d.Kind == "ui" || d.Kind == "askUser" {
		m.answerDialog(d, -1)
	} else if d.Kind == "input" {
		m.answerInput(d, true)
	} else {
		m.Dialogs = m.Dialogs[1:]
		// Events stay swallowed while a dialog is open: re-sync
		// the sidebar in case task writes landed meanwhile.
		m.refreshPiTasks()
		m.drainQueuedDialogs()
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	}
	m.drainQueuedDialogs()
	return m, nil
}

func (m *Model) answerInput(d *Dialog, cancelled bool) {
	m.Dialogs = m.Dialogs[1:]
	// fireUI, not m.Pi.Fire: Esc on a queued input dialog is now a routine
	// path (the drain promotes one after every close), and a nil client —
	// tests, or the window before a respawn reconnects — used to panic and
	// take the whole TUI down.
	m.fireUI(extension.InputResponse(d.ID, d.Filter, cancelled))
	m.refreshPiTasks() // /tasks-menu writes land in the store file
	m.applyPopupH()
	m.drainQueuedDialogs()
	m.Refresh()
}

// answerDialog replies to an extension permission dialog
// (response shape built by src/extension).
// Plan latch heuristic lives in ext (pi-extension domain, no plan flag in
// get_state); this stays the thin MVC controller.
func (m *Model) answerDialog(d *Dialog, choice int) {
	if (d.Kind == "ui" || d.Kind == "askUser") && choice >= 0 && choice < len(d.Options) {
		if v, ok := ext.ShouldLatchPlan(d.Options[choice]); ok {
			m.planOn = v
		}
	}
	m.Dialogs = m.Dialogs[1:]
	m.fireUI(extension.Response(d.ID, d.Method, choice, d.Options))
	m.applyPopupH()
	m.drainQueuedDialogs()
	m.Refresh()
}

// normProv labels an empty provider for the left pane.
func normProv(p string) string {
	if strings.TrimSpace(p) == "" {
		return "other"
	}
	return p
}

// providerAt safely reads the parallel provider per model option.
func providerAt(provs []string, i int) string {
	if i < 0 || i >= len(provs) {
		return ""
	}
	return provs[i]
}

// buildProvs collects the left pane: "All" + unique sorted providers from
// the model list plus every loginable provider (even with no models yet).
func buildProvs(providers []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = normProv(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range providers {
		add(p)
	}
	for _, p := range pirpc.ProviderEnvsAll() {
		add(p.Provider)
	}
	sort.Strings(out)
	return append([]string{"All"}, out...)
}

// sortProvsConn floats connected providers above the rest (after "All"),
// keeping alphabetical order inside each group.
func sortProvsConn(provs []string, conn map[string]bool) {
	if len(provs) < 2 {
		return
	}
	sort.SliceStable(provs[1:], func(i, j int) bool {
		return conn[provs[1+i]] && !conn[provs[1+j]]
	})
}

// provConn marks connected providers: listed models, a saved keystore key,
// a preset env var, or a pi auth entry (api_key or OAuth). Unknown ids
// (no env mapping) prove via models or pi auth.
func provConn(keyPath string, providers []string) map[string]bool {
	inModels := map[string]bool{}
	for _, p := range providers {
		inModels[normProv(p)] = true
	}
	keys := pirpc.LoadKeys(keyPath)
	piAuth := map[string]bool{}
	for _, e := range pirpc.ListPiAuth() {
		piAuth[e.Provider] = true
	}
	out := map[string]bool{}
	for _, prov := range buildProvs(providers) {
		if prov == "All" {
			continue
		}
		if inModels[prov] {
			out[prov] = true
			continue
		}
		if piAuth[prov] {
			out[prov] = true
			continue
		}
		if env := pirpc.LookupEnv(prov); env != "" {
			_, hasKey := keys[env]
			out[prov] = hasKey || os.Getenv(env) != ""
		}
	}
	return out
}

// selProv is the provider filter for the right pane ("" = All).
func (d *Dialog) selProv() string {
	if d.Kind != "model" || len(d.Provs) == 0 {
		return ""
	}
	if d.ProvCursor < 0 || d.ProvCursor >= len(d.Provs) {
		return ""
	}
	if d.Provs[d.ProvCursor] == "All" {
		return ""
	}
	return d.Provs[d.ProvCursor]
}

// reindex recomputes the visible list from Filter.
// Scope follows the focused pane: on the providers pane a non-empty
// Filter searches globally across all providers (the provider id itself
// is also matchable so typing "anthropic" finds its models); on the
// models pane the filter stays scoped to the selected provider. With an
// empty Filter the right pane previews the cursor provider's models.
// Login dialogs filter providers into PIdx (right pane is keys, unfiltered).
func (d *Dialog) Reindex() {
	if d.Kind == "login" {
		d.PIdx = d.PIdx[:0]
		f := strings.ToLower(strings.TrimSpace(d.Filter))
		for i, prov := range d.Provs {
			if f == "" {
				d.PIdx = append(d.PIdx, i)
				continue
			}
			label, env := pirpc.ProviderLabel(prov), pirpc.LookupEnv(prov)
			if strings.Contains(strings.ToLower(prov), f) ||
				strings.Contains(strings.ToLower(label), f) ||
				strings.Contains(strings.ToLower(env), f) {
				d.PIdx = append(d.PIdx, i)
			}
		}
		if d.ProvCursor >= len(d.PIdx) {
			d.ProvCursor = 0
		}
		return
	}
	d.FIdx = d.FIdx[:0]
	f := strings.ToLower(d.Filter)
	prov := d.selProv()
	if d.Kind == "settings" && len(d.Provs) > 0 {
		// two-pane groups: empty filter (or typing on the right) scopes
		// to the selected group; typing on the left searches globally.
		// "All" never scopes (like the model picker's All providers).
		if f == "" || !d.ProvFocus {
			if d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) {
				prov = d.Provs[d.ProvCursor]
				if prov == "All" {
					prov = ""
				}
			}
		}
	} else if f != "" && d.ProvFocus {
		prov = ""
	}
	for i := range d.Options {
		if prov != "" && normProv(providerAt(d.Providers, i)) != prov {
			continue
		}
		if f == "" || strings.Contains(strings.ToLower(d.Options[i]), f) ||
			(i < len(d.Descs) && strings.Contains(strings.ToLower(d.Descs[i]), f)) ||
			strings.Contains(strings.ToLower(normProv(providerAt(d.Providers, i))), f) ||
			strings.Contains(d.specHay(i), f) {
			d.FIdx = append(d.FIdx, i)
		}
	}
	// starred models float above the rest, stable (below keeps pi's order)
	if d.Kind == "model" && len(d.FavSet) > 0 {
		sort.SliceStable(d.FIdx, func(a, b int) bool {
			return d.isFavIdx(d.FIdx[a]) && !d.isFavIdx(d.FIdx[b])
		})
	}
	if d.Cursor >= len(d.FIdx) {
		d.Cursor = 0
	}
	d.TrajOff = 0 // re-filtered list → detail back to top (only trajectory reads it)
}

// SelLoginProv is the selected provider in the two-pane login dialog
// ("" = none). ProvCursor indexes the filtered PIdx, not raw Provs.
func (d *Dialog) SelLoginProv() string {
	if d.Kind != "login" || len(d.Provs) == 0 || len(d.PIdx) == 0 {
		return ""
	}
	if d.ProvCursor < 0 || d.ProvCursor >= len(d.PIdx) {
		return ""
	}
	i := d.PIdx[d.ProvCursor]
	if i < 0 || i >= len(d.Provs) {
		return ""
	}
	return d.Provs[i]
}

// loginKeysLen is the number of key rows: Payload holds raw keys for key
// rows and "" for action rows, so it stays correct with dynamic actions.
func loginKeysLen(d *Dialog) int {
	n := 0
	for _, p := range d.Payload {
		if p != "" {
			n++
		}
	}
	return n
}

// oauthDesc renders an OAuth state for display ("OAuth · acct · exp").
func oauthDesc(exp int64, acct string) string {
	s := "OAuth"
	if acct != "" {
		if r := []rune(acct); len(r) > 12 {
			acct = string(r[:12]) + "…"
		}
		s += " · " + acct
	}
	if exp > 0 {
		s += " · exp " + pirpc.FormatAdded(exp/1000)
	}
	return s
}

// RefreshLoginKeys rebuilds the right pane for the selected provider:
// key rows (name + masked/full key + date) + dynamic actions (add when the
// provider takes API keys, OAuth guide or disconnect, reload), plus
// per-provider key counts and OAuth state. ShowKeys reveals full secrets.
func (m *Model) RefreshLoginKeys(d *Dialog) {
	if d.Kind != "login" {
		return
	}
	store := pirpc.LoadStore(m.KeyPath)
	piOAuth := map[string]bool{}
	piOAuthExp := map[string]int64{}
	piOAuthAcct := map[string]string{}
	for _, e := range pirpc.ListPiAuth() {
		if e.Type == "oauth" {
			piOAuth[e.Provider] = true
			piOAuthExp[e.Provider] = e.Expires
			piOAuthAcct[e.Provider] = e.Account
		}
	}
	if d.LoginCounts == nil {
		d.LoginCounts = map[string]int{}
	}
	for k := range d.LoginCounts {
		delete(d.LoginCounts, k)
	}
	for _, prov := range d.Provs {
		env := pirpc.LookupEnv(prov)
		if env == "" {
			continue
		}
		if e, ok := store[env]; ok && e != nil {
			d.LoginCounts[prov] = len(e.Keys)
		}
	}
	if d.OAuthConn == nil {
		d.OAuthConn = map[string]bool{}
	}
	for k := range d.OAuthConn {
		delete(d.OAuthConn, k)
	}
	for _, prov := range d.Provs {
		d.OAuthConn[prov] = piOAuth[prov]
	}
	// dots: providers with keys first is done at open; here just mark conn
	if d.ProvConn == nil {
		d.ProvConn = map[string]bool{}
	}
	for _, prov := range d.Provs {
		d.ProvConn[prov] = d.LoginCounts[prov] > 0 || piOAuth[prov]
	}
	prov := d.SelLoginProv()
	env := pirpc.LookupEnv(prov)
	d.LoginProvider, d.LoginEnv = prov, env
	d.LoginOAuth = piOAuth[prov]
	d.LoginOAuthExp, d.LoginOAuthAcct = piOAuthExp[prov], piOAuthAcct[prov]
	var items []pirpc.KeyItem
	active := -1
	if env != "" {
		if e, ok := store[env]; ok && e != nil && len(e.Keys) > 0 {
			items = e.Keys
			active = e.Active
			if active < 0 || active >= len(items) {
				active = 0
			}
		}
	}
	d.KeyActive = active
	d.Options = d.Options[:0]
	d.Payload = d.Payload[:0]
	d.Descs = d.Descs[:0]
	d.LoginActions = d.LoginActions[:0]
	for i, it := range items {
		mark := "○ "
		desc := "Enter to use"
		if i == active {
			mark = "● "
			desc = "active"
		}
		secret := pirpc.MaskKey(it.Key)
		if d.ShowKeys {
			secret = it.Key
		}
		label := mark + secret
		if strings.TrimSpace(it.Name) != "" {
			label = mark + strings.TrimSpace(it.Name) + " · " + secret
		}
		if date := pirpc.FormatAdded(it.AddedAt); date != "—" {
			desc += " · " + date
		}
		d.Options = append(d.Options, label)
		d.Payload = append(d.Payload, it.Key)
		d.Descs = append(d.Descs, desc)
	}
	if env != "" {
		d.Options = append(d.Options, "＋ Add new key")
		d.Payload = append(d.Payload, "")
		d.Descs = append(d.Descs, "Enter to add")
		d.LoginActions = append(d.LoginActions, "add")
	}
	if d.LoginOAuth {
		d.Options = append(d.Options, "⊗ Disconnect ("+oauthDesc(d.LoginOAuthExp, d.LoginOAuthAcct)+")")
		d.Payload = append(d.Payload, "")
		d.Descs = append(d.Descs, "logout "+prov+" in pi too")
		d.LoginActions = append(d.LoginActions, "disconnect")
	} else {
		d.Options = append(d.Options, "OAuth / subscription")
		d.Payload = append(d.Payload, "")
		d.Descs = append(d.Descs, "guide in stock pi")
		d.LoginActions = append(d.LoginActions, "guide")
	}
	d.Options = append(d.Options, "↻ Reload models")
	d.Payload = append(d.Payload, "")
	d.Descs = append(d.Descs, "refresh model list")
	d.LoginActions = append(d.LoginActions, "reload")
	if d.KeyCursor >= len(d.Options) {
		d.KeyCursor = 0
	}
	if d.KeyCursor < 0 {
		d.KeyCursor = 0
	}
}

// updateLoginDialog navigates the two-pane /login picker: left = providers,
// right = saved keys + actions. ↑↓ moves in the focused pane, ←/→/Tab
// switches pane, typing filters providers (left pane), Enter uses/adds,
// ⌫ deletes a key, s shows/hides secrets, r renames. Everything stays on
// /login: mutations reconnect pi behind the open picker.
func (m Model) updateLoginDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyUp:
		if d.ProvFocus {
			if len(d.PIdx) > 0 {
				if d.ProvCursor > 0 {
					d.ProvCursor--
				} else {
					d.ProvCursor = len(d.PIdx) - 1
				}
				m.RefreshLoginKeys(d)
				d.KeyCursor = 0
			}
		} else if len(d.Options) > 0 {
			if d.KeyCursor > 0 {
				d.KeyCursor--
			} else {
				d.KeyCursor = len(d.Options) - 1
			}
		}
		return m, nil
	case tea.KeyDown:
		if d.ProvFocus {
			if len(d.PIdx) > 0 {
				if d.ProvCursor < len(d.PIdx)-1 {
					d.ProvCursor++
				} else {
					d.ProvCursor = 0
				}
				m.RefreshLoginKeys(d)
				d.KeyCursor = 0
			}
		} else if len(d.Options) > 0 {
			if d.KeyCursor < len(d.Options)-1 {
				d.KeyCursor++
			} else {
				d.KeyCursor = 0
			}
		}
		return m, nil
	case tea.KeyLeft:
		d.ProvFocus = true
		return m, nil
	case tea.KeyRight:
		d.ProvFocus = false
		return m, nil
	case tea.KeyTab:
		d.ProvFocus = !d.ProvFocus
		return m, nil
	case tea.KeyBackspace:
		if d.Filter != "" {
			d.Filter = d.Filter[:len(d.Filter)-1]
			d.Reindex()
			d.ProvCursor = 0
			d.ProvFocus = true
			m.RefreshLoginKeys(d)
			d.KeyCursor = 0
			return m, nil
		}
		if d.ProvFocus {
			return m, nil
		}
		return m.deleteLoginKey(d)
	case tea.KeyDelete:
		if !d.ProvFocus {
			return m.deleteLoginKey(d)
		}
		return m, nil
	case tea.KeyEsc:
		m.Dialogs = m.Dialogs[1:]
		m.refreshPiTasks()
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	case tea.KeyCtrlL:
		return m, nil
	case tea.KeyCtrlP:
		// jump straight to the model picker (mirror of ^L in /model;
		// ^M is Enter's keycode so it can't be used — ^P already means
		// model outside dialogs)
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.RunBuiltin("model", "")
	case tea.KeyCtrlS:
		// show/hide shortcut works from either pane
		d.ShowKeys = !d.ShowKeys
		m.RefreshLoginKeys(d)
		m.Refresh()
		return m, nil
	case tea.KeyEnter:
		if d.ProvFocus {
			d.ProvFocus = false
			return m, nil
		}
		return m.confirmLoginKey(d)
	}
	if km.Type == tea.KeyRunes {
		// Right-pane shortcuts (filter only on the left pane so s/r don't
		// get eaten by search while managing keys).
		if !d.ProvFocus {
			s := km.String()
			nk := loginKeysLen(d)
			onKey := d.KeyCursor >= 0 && d.KeyCursor < nk
			switch strings.ToLower(s) {
			case "s", " ":
				d.ShowKeys = !d.ShowKeys
				m.RefreshLoginKeys(d)
				m.Refresh()
				return m, nil
			case "r":
				if onKey {
					return m.openRenameDialog(d)
				}
			}
			// other runes fall through to provider filtering below
		}
		d.Filter += km.String()
		d.Reindex()
		d.ProvCursor = 0
		d.ProvFocus = true
		m.RefreshLoginKeys(d)
		d.KeyCursor = 0
		return m, nil
	}
	return m, nil
}

// openRenameDialog pushes a name prompt on top of /login (login stays at
// [1] so Enter/Esc returns to it, never to the main screen). The keystore
// read that prefills it runs off the event loop, so the prompt is pushed
// from the LoginRenameOpenMsg handler.
func (m Model) openRenameDialog(d *Dialog) (tea.Model, tea.Cmd) {
	nk := loginKeysLen(d)
	if d.KeyCursor < 0 || d.KeyCursor >= nk {
		return m, nil
	}
	prov, env, idx := d.LoginProvider, d.LoginEnv, d.KeyCursor
	keyPath := m.KeyPath
	return m, func() tea.Msg {
		items, _ := pirpc.ListKeyItems(keyPath, env)
		return LoginRenameOpenMsg{Provider: prov, Env: env, Items: items, Idx: idx}
	}
}

// confirmLoginKey runs Enter on the right pane: use a key, add one, guide
// OAuth, or reload models. Everything stays on /login: the picker refreshes
// in place while pi reconnects behind it.
func (m Model) confirmLoginKey(d *Dialog) (tea.Model, tea.Cmd) {
	nk := loginKeysLen(d)
	if d.KeyCursor < nk {
		prov, env, idx := d.LoginProvider, d.LoginEnv, d.KeyCursor
		keyPath := m.KeyPath
		// Keystore read, activation and the push to pi are blocking file
		// I/O: all of it runs in the Cmd, so the notice's key is the one
		// that was actually made active.
		return m, func() tea.Msg {
			keys, _ := pirpc.ListKeys(keyPath, env)
			masked := ""
			if idx >= 0 && idx < len(keys) {
				masked = pirpc.MaskKey(keys[idx])
			}
			_ = pirpc.SetActive(keyPath, env, idx)
			// Selecting a key pushes it to pi (auth.json wins over env).
			pirpc.PushActiveToPi(keyPath, env)
			return LoginSwitchMsg{Prov: prov, Masked: masked, D: d}
		}
	}
	ai := d.KeyCursor - nk
	if ai < 0 || ai >= len(d.LoginActions) {
		return m, nil
	}
	if d.LoginActions[ai] == "add" { // secret on top, login stays underneath
		prov, env := d.LoginProvider, d.LoginEnv
		if prov == "" || env == "" {
			return m, nil
		}
		s := &Dialog{Kind: "secret", Title: "API key — " + prov,
			Message:       "Save to " + env + " (pitago keystore 0600 + pi auth.json). New key becomes active; pi reconnects, picker stays open.",
			LoginProvider: prov, LoginEnv: env}
		m.Dialogs = append([]*Dialog{s}, m.Dialogs...)
		m.Refresh()
		return m, nil
	}
	return m.loginAction(d, ai)
}

// loginAction runs one trailing action row by kind (see RefreshLoginKeys).
// Everything stays on /login.
func (m Model) loginAction(d *Dialog, ai int) (tea.Model, tea.Cmd) {
	if ai < 0 || ai >= len(d.LoginActions) {
		return m, nil
	}
	switch d.LoginActions[ai] {
	case "add":
		return m.openOAuthGuide(d) // unreachable (add handled above); safe fallback
	case "guide":
		return m.openOAuthGuide(d)
	case "disconnect":
		return m.disconnectOAuth(d)
	default: // reload models — stay open, re-import pi first
		// SyncFromPi + the authState mirror are blocking file I/O; they run
		// off the event loop and land as LoginReloadMsg, which then refreshes
		// the picker and re-counts models in the original order.
		keyPath, authPath := m.KeyPath, m.authPath()
		return m, func() tea.Msg {
			pirpc.SyncFromPi(keyPath)
			pirpc.SyncAuthStateFromPi(authPath)
			return LoginReloadMsg{D: d}
		}
	}
}

// openOAuthGuide pushes the stock-pi OAuth guide on top of /login.
func (m Model) openOAuthGuide(d *Dialog) (tea.Model, tea.Cmd) {
	prov := d.LoginProvider
	o := &Dialog{Kind: "loginOAuth", Title: "OAuth — " + prov,
		Message:       "1. Open another terminal\n2. Run: pi\n3. Type: /login " + prov + " then follow the steps\n4. Come back here and reload",
		Options:       []string{"Done — reload", "Close"},
		LoginProvider: prov}
	o.Reindex()
	m.Dialogs = append([]*Dialog{o}, m.Dialogs...)
	m.Refresh()
	return m, nil
}

// disconnectOAuth logs a provider out of its pi subscription: drops pi's
// OAuth entry + pitago's mirror, stays on /login, reconnects behind it.
// The teardown is blocking file I/O, so it runs off the event loop and lands
// as OAuthGoneMsg (which refreshes the keys, then respawns).
func (m Model) disconnectOAuth(d *Dialog) (tea.Model, tea.Cmd) {
	prov := d.LoginProvider
	if prov == "" || !d.LoginOAuth {
		return m, nil
	}
	authPath := m.authPath()
	return m, func() tea.Msg {
		_ = pirpc.DeletePiAuth(prov)
		pirpc.ForgetAuthState(authPath, prov)
		pirpc.SyncAuthStateFromPi(authPath)
		return OAuthGoneMsg{Prov: prov, D: d}
	}
}

// authPath returns pitago's pi-login mirror path ("" when unconfigured).
func (m Model) authPath() string {
	if m.AuthPath != "" {
		return m.AuthPath
	}
	return pirpc.AuthStatePath()
}

// deleteLoginKey removes the selected key (⌫ with empty filter). Both
// active and inactive deletes stay on /login; deleting the active one
// reconnects pi behind the picker so the credential drops immediately.
// ⌫ on the Disconnect action logs the OAuth subscription out too.
func (m Model) deleteLoginKey(d *Dialog) (tea.Model, tea.Cmd) {
	nk := loginKeysLen(d)
	if d.KeyCursor < 0 {
		return m, nil
	}
	if d.KeyCursor >= nk {
		if ai := d.KeyCursor - nk; ai >= 0 && ai < len(d.LoginActions) && d.LoginActions[ai] == "disconnect" {
			return m.disconnectOAuth(d)
		}
		return m, nil
	}
	prov, env := d.LoginProvider, d.LoginEnv
	idx := d.KeyCursor
	isActive := idx == d.KeyActive
	keyPath := m.KeyPath
	// Delete + push + re-read all run in one Cmd: the trailing read must
	// never observe a keystore whose write is still in flight, since its
	// result decides the notice.
	if !isActive {
		return m, func() tea.Msg {
			_ = pirpc.DeleteKeyAt(keyPath, env, idx)
			return LoginDeleteMsg{Prov: prov, D: d}
		}
	}
	return m, func() tea.Msg {
		keysBefore, _ := pirpc.ListKeys(keyPath, env)
		masked := ""
		if idx >= 0 && idx < len(keysBefore) {
			masked = pirpc.MaskKey(keysBefore[idx])
		}
		_ = pirpc.DeleteKeyAt(keyPath, env, idx)
		// Keep pi in sync: last key removes pi's entry, otherwise pi follows
		// the new active key.
		pirpc.PushActiveToPi(keyPath, env)
		keys, _ := pirpc.ListKeys(keyPath, env)
		return LoginDeleteMsg{Prov: prov, Masked: masked, Left: keys, Active: true, D: d}
	}
}

// pollCmds periodically reloads pi commands (auto-detects new ones).
func (m Model) pollCmds() tea.Cmd {
	return tea.Tick(45*time.Second, func(time.Time) tea.Msg {
		cmds, err := m.Pi.GetCommands()
		return CmdsRefreshMsg{Cmds: cmds, Err: err}
	})
}

func (m Model) fetchCmdsOnce() tea.Cmd {
	return func() tea.Msg {
		cmds, err := m.Pi.GetCommands()
		return CmdsRefreshMsg{Cmds: cmds, Err: err}
	}
}

// cmdSig is the command-list signature used to detect changes.
func cmdSig(cmds []pirpc.RepoCommand) string {
	parts := make([]string, 0, len(cmds))
	for _, c := range cmds {
		parts = append(parts, c.Source+"/"+c.Name)
	}
	return strings.Join(parts, "\n")
}
