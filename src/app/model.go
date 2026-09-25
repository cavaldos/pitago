package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/chat"
	"pitago/src/components/favorite"
	"pitago/src/components/mention"
	"pitago/src/components/palette"
	"pitago/src/components/recent"
	"pitago/src/components/theme"
	"pitago/src/pirpc"
)

// Block is one rendered unit in the chat column (see components/chat).
type Block = chat.Block

// Dialog is a modal: extension permission prompt or native picker/settings.

type Dialog struct {
	ID             string
	Method         string // select | confirm (extension UI)
	Kind           string // "ui" | "model" | "thinking" | "settings" | "pconfig" | "login" | "loginDone" | "secret" | "rename" | "sessions" | ...
	Title          string
	Message        string
	Options        []string
	Descs          []string
	Providers      []string          // model picker: parallel provider per option
	Models         []pirpc.ModelInfo // model picker: full specs parallel to Options
	Provs          []string          // model picker: left pane (unique providers, [0]="All")
	ProvConn       map[string]bool   // model picker: connected providers (green dot)
	ProvCursor     int               // model picker: left-pane cursor
	ProvFocus      bool              // model picker: true = providers focused
	PsecIDs        []string          // pitago-setting: section id parallel to Provs (left pane)
	Paths          []string          // sessions picker: parallel session file per option
	Scope          string            // sessions picker: "current" | "all" (Tab toggles)
	Payload        []string          // yank picker: full text per option; login: raw keys ("" for action rows)
	Cursor         int
	Filter         string // picker filter / secret buffer / rename buffer
	FIdx           []int
	FavSet         map[string]bool // model picker: starred provider\x00id (★ column, sorted first)
	PIdx           []int           // login: filtered provider indices into Provs
	KeyCursor      int             // login: cursor in the right (keys) pane
	KeyActive      int             // login: active key index within keys (-1 = none)
	ShowKeys       bool            // login: reveal full keys (s toggles, never persisted)
	RenameIdx      int             // rename flow: key index being renamed (-1 = none)
	LoginActions   []string        // login: action kinds parallel to trailing rows ("add","guide","disconnect","reload")
	LoginOAuth     bool            // login: selected provider has OAuth in pi
	LoginOAuthExp  int64           // login: oauth expiry ms epoch (0 = unknown)
	LoginOAuthAcct string          // login: oauth account id ("" = unknown)
	OAuthConn      map[string]bool // login: provider -> oauth connected in pi
	LoginCounts    map[string]int  // login: provider -> saved key count
	Settings       SettingsState
	LoginProvider  string // login flow: provider id
	LoginEnv       string // login flow: env var
	TrajOff        int    // trajectory dialog: detail-column scroll offset (lines)
	UpdateTo       string // update flow: target tag (Kind "update")
	ShortcutCmd    string // cmdshortcut capture: /command being assigned ("" = none)
}

// SettingsState snapshots tunable agent settings.

type SettingsState struct {
	Steering, FollowUp     string
	AutoCompact, AutoRetry bool
	Thinking, Model        string
	Theme                  string
	Vals                   map[string]string // file-backed pi rows (dotted path → display)
	HideThinking           bool              // pitago-local "Hide thinking" row
	AutocompleteMax        int               // pitago-local "Autocomplete max" row
}

// RecentModel is one entry of the sidebar list (see components/recent).
type RecentModel = recent.RecentModel

// FavEntry is one starred model (see components/favorite).
type FavEntry = favorite.Fav

type Model struct {
	vp             viewport.Model
	sideVp         viewport.Model // sidebar scroll: clips content to sideH, Ctrl/Alt+↑↓/PgUp/PgDn or wheel over it scrolls
	ta             textarea.Model
	Pi             *pirpc.Client
	blocks         []Block
	toasts         []Toast        // ephemeral popups (model switch, yank…): never in chat history
	tools          map[string]int // toolCallId -> block index
	curAsst        int
	curThink       int
	asstDelta      bool // text deltas streamed into curAsst (message_end must not re-add)
	thinkDelta     bool // thinking deltas streamed into curThink (same)
	thinking       bool
	Status         string
	extStat        string
	planOn         bool // plan-mode latch, live only: set on Start choice, cleared on /new (heuristic, extension has no plan flag in get_state)
	ready          bool
	winW           int
	winH           int
	hideSide       bool // Ctrl+B: hide sidebar for clean drag-select of chat
	Mouse          bool // --mouse: terminal reports clicks (sidebar recent switch)
	baseVpH        int
	cwd            string
	ModelLbl       string
	CurAgent       string // last-picked /subagents entry (shown on the input bar)
	AppVersion     string // pitago build version for the welcome header ("" = omit)
	UpdateAvail    string // latest tag when auto-check found newer ("" = up to date) — welcome banner + /update hint
	thinkLvl       string // thinking level from get_state
	autoCompact    bool   // auto-compaction from get_state
	ctxWindow      int    // model context window from get_state/stats
	session        string
	sessStart      time.Time // session clock for sidebar "time"
	turnStart      time.Time // last turn start (for "last" + speed)
	turnOutBase    int       // stats.Out at last turn_start
	pendSpeed      bool      // compute last/speed on next statsMsg
	lastDur        time.Duration
	lastSpeed      float64 // tok/s of last turn
	ws             wsData  // workspace git status (polled)
	Stats          pirpc.Stats
	sessBreak      []pirpc.CostBreak // sidebar COST section (connect + /session refresh)
	queue          pirpc.Queue
	Todos          []TodoItem      // tracked from todo-tool calls (sidebar)
	MCP            []McpServer     // pi agent-dir MCP snapshot (sidebar)
	Plugins        []Plugin        // installed pi packages (sidebar PLUGINS toggle)
	Market         []MarketEntry   // npm registry pi-package list (marketplace tab)
	MarketErr      string          // last marketplace fetch error ("" = ok/unloaded)
	showPlugins    bool            // PLUGINS expanded (click header or /plugins)
	Side           map[string]bool // sidebar section visibility (nil entry = default; MCP + Plugins + Commands hide)
	Dialogs        []*Dialog
	connErr        string
	AutoRetry      bool          // no RPC getter; tracked locally (default on)
	HideThinking   bool          // /settings: skip thinking blocks in chat (pi parity, pitago-local)
	respawning     bool          // reconnecting pi: skip pi_exited notice
	spawnOpts      pirpc.Options // for respawning pi (login)
	KeyPath        string        // keystore API keys
	AuthPath       string        // mirrored pi logins (oauth state pitago saves)
	sessionFile    string        // respawn keeps the same session
	Cmds           []pirpc.RepoCommand
	cmdOpen        bool
	cmdCursor      int
	cmdOffset      int   // first visible row of the scroll window
	cmdItems       []int // indices into cmds
	atOpen         bool
	atCursor       int
	atOffset       int // first visible row of the @ scroll window
	atRow          int // input row holding the @ token
	atStart        int // rune index where the @ token starts
	atPrefix       string
	atItems        []mention.Item
	imgAtts        []imgAttach // input tray: dropped/pasted/@-completed images as [Image N] chips
	imgSeq         int         // chip counter, never renumbered
	trayFocus      bool        // cursor moved into the tray (↓ from last input line)
	imgCursor      int         // selected chip while trayFocus
	trayRet        int         // input offset to restore on Esc
	pet            petState
	recentModels   []RecentModel
	recentPath     string // persisted recent models ("" = don't persist)
	favModels      []FavEntry
	favSet         map[string]bool // starred models lookup (see components/favorite)
	favPath        string          // persisted favorites ("" = don't persist)
	hist           []string        // sent messages, oldest→newest (↑↓ recall when input empty)
	histIdx        int             // -1 = live input, else index into hist while browsing
	CmdShortcuts   map[string]string // /command → "alt+x" (hub-assigned Alt shortcuts, persisted in prefs)
	ThemeName      string          // active TUI theme (/theme, --theme flag)
	themePath      string          // persisted theme ("" = don't persist)
	prefsPath      string          // persisted pitago-local prefs ("" = don't persist)
	builtins       []Builtin
	confirm        map[string]ConfirmFunc
	expandTools    bool      // Ctrl+G: expand every tool block (write/read/diff previews), pi-style
	quitArm        time.Time // first Ctrl+C timestamp (second press within window quits)
	quitGen        int       // arm generation (stale disarm ticks ignored)
	escArm         time.Time // first Esc timestamp while running (second press within window cancels)
	escGen         int       // arm generation (stale disarm ticks ignored)
	mouseLeakAt    time.Time // last SGR mouse-report burst (split fragments within window are residue)
	mouseBuf       string    // pending split tail ("[<65"…) waiting for its continuation (sequence, time-bound)
	plugAction     string    // pending plugin op awaiting second confirm (auth gate)
	plugSpec       string    // pending plugin spec (cleared on confirm/cancel/timeout)
	plugAt         time.Time // first press timestamp for the pending plugin op
	renderCache    []string  // per-block rendered output (renderBlocks reuses clean history)
	renderCacheKey []uint64  // fingerprint parallel to renderCache (see blockKey)
	sideCache      string    // last built sidebar content (streaming reuses within sideThrottle)
	sideCacheAt    time.Time // last sidebar rebuild
	lastPaint      time.Time // last chat viewport paint (streaming coalesces to streamFrame)
	pendingPaint   bool      // a coalesced paint is waiting on its flush tick
}

// quitArmWindow is the double-press window for Ctrl+C quit.
const quitArmWindow = 3 * time.Second

// escArmWindow is the double-press window for Esc cancel (Ctrl+C parity:
// one press arms, second press within the window aborts the running turn).
const escArmWindow = 3 * time.Second

// mouseFragWindow is how long after an SGR mouse-report burst a lone
// coordinate fragment ("65;99;18M") still counts as split-read residue,
// plus how long a buffered split tail ("[<65"…) waits for its continuation.
// Sequence (consecutive reads) decides, the window only bounds staleness.
const mouseFragWindow = 1500 * time.Millisecond

// streamFrame caps streaming repaints (~30fps): text/thinking deltas mutate
// blocks on every event but SetContent runs at most once per frame, with a
// trailing flush tick so the UI never stays stale.
const streamFrame = 33 * time.Millisecond

// sideThrottle caps sidebar rebuilds while streaming: the sidebar barely
// moves mid-turn (status/pet tick), so bursts reuse sideCache briefly.
const sideThrottle = 500 * time.Millisecond

// streamFlushMsg paints a previously coalesced streaming update.
type streamFlushMsg struct{}

func streamFlushCmd(d time.Duration) tea.Cmd {
	if d <= 0 {
		d = streamFrame
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return streamFlushMsg{} })
}

// mouseBurst reports a wheel/click burst within the fragment window: split
// reads still arriving get scrubbed as residue, not typed as text.
func (m Model) mouseBurst() bool {
	return !m.mouseLeakAt.IsZero() && time.Since(m.mouseLeakAt) < mouseFragWindow
}

type connectedMsg struct {
	state   pirpc.State
	msgs    []pirpc.AgentMessage
	stats   pirpc.Stats
	cmds    []pirpc.RepoCommand
	entries []pirpc.SessionEntry // usage attribution for the COST breakdown
	err     error
}

type piEventMsg struct{ pirpc.Event }

type statsMsg struct {
	stats pirpc.Stats
	err   error
}

type stateRefreshMsg struct {
	state pirpc.State
	err   error
}

type wsTickMsg struct{}

type wsMsg struct {
	data wsData
}

type sentAckMsg struct{ err error }

type quitDisarmMsg struct{ gen int } // quit-arm window elapsed

type escDisarmMsg struct{ gen int } // esc-arm window elapsed

type SessionResetMsg struct{ Err error }

type ModelCycleMsg struct {
	Label    string
	Provider string // may be "" (cycle path); resolved label-only entry
	ID       string // model id when known, else ""
	Err      error
}

type PickerMsg struct {
	Kind                      string
	Options, Descs, Providers []string
	Models                    []pirpc.ModelInfo // model picker: full specs parallel to Options
	Paths                     []string          // sessions picker: parallel session file per option
	Payload                   []string          // sessions picker: parallel multi-line detail per option
	Filter                    string            // sessions picker: pre-typed filter (/resume <arg>)
	Scope                     string            // sessions picker: "current" | "all"
	Replace                   bool              // sessions picker: Tab scope swap into the open dialog
	Current                   string
	Err                       error
}

type SettingsMsg struct {
	St SettingsState
	// Opts/Descs/Cats are precomputed by src/builtin (this package must not
	// import builtin, so the producer ships them in the message).
	// Cats parallels Opts (section header per row); Filter seeds a new
	// dialog's filter (e.g. /settings network), refreshes keep typing.
	Opts, Descs, Cats []string
	Filter            string
	Err               error
}

type TreeMsg struct {
	Text string
	Err  error
}

// TrajectoryMsg carries harness-style run-trace rows for the /trajectory
// window (built by src/builtin; this package only holds the message).
type TrajectoryMsg struct {
	Scope                   string
	Options, Descs, Payload []string
	Filter                  string
	Err                     error
}

type SessionMsg struct {
	Text  string
	Break []pirpc.CostBreak // per-model cost (sidebar COST section)
	Err   error
}

type SettingsRefreshMsg struct {
	Notice string
	Level  string // thinking change: update sidebar immediately
	Err    error
}

type LoginKeyMsg struct {
	Provider, Env, Key string
}

type RenameKeyMsg struct {
	Provider, Env string
	Idx           int
	Name          string
}

type respawnMsg struct {
	client *pirpc.Client
	err    error
}

type CmdsRefreshMsg struct {
	Cmds     []pirpc.RepoCommand
	Err      error
	Announce bool // manual /reload: reports the result
}

func New(pi *pirpc.Client, cwd string) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message… (/ commands · ^V paste)"
	ta.Focus()
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = "❯ "
	// Prompt only on the first display line: bubbles repeats Prompt on
	// every wrapped/new line, which looked like an indent. Continuations
	// get blank padding instead (same width, so the text column stays
	// straight and SetWidth math is unchanged).
	ta.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(cInput)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(cMuted)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(cMuted)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	return Model{
		ta:          ta,
		Pi:          pi,
		tools:       make(map[string]int),
		curAsst:     -1,
		curThink:    -1,
		histIdx:     -1,
		Status:      "connecting to pi…",
		cwd:         cwd,
		ModelLbl:    "…",
		AutoRetry:   true,
		showPlugins: true, // PLUGINS starts expanded
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.pollCmds(), m.pollWs(), m.CheckUpdatesCmd(true))
}

// fetchAll loads state/messages/stats/commands after (re)connect.

func (m Model) fetchAll() tea.Cmd {
	return func() tea.Msg {
		state, err := m.Pi.GetState()
		if err != nil {
			return connectedMsg{err: err}
		}
		msgs, _ := m.Pi.GetMessages()
		stats, _ := m.Pi.GetStats()
		cmds, _ := m.Pi.GetCommands()
		entries, _ := m.Pi.GetEntries()
		return connectedMsg{state: state, msgs: msgs, stats: stats, cmds: cmds, entries: entries}
	}
}

func (m *Model) AddBlock(b Block) int {
	// Notices are transient popups, not transcript: route them to the
	// toast stack (auto-dismissing, above the input) instead of the
	// chat history. One interception here covers all ~30 call sites.
	if b.Kind == "notice" {
		m.pushToast(b.Text, b.Err)
		return -1
	}
	m.blocks = append(m.blocks, b)
	return len(m.blocks) - 1
}

func (m *Model) ensureAsst() int {
	if m.curAsst < 0 {
		m.curAsst = m.AddBlock(Block{Kind: "assistant"})
	}
	return m.curAsst
}

func (m *Model) ensureThink() int {
	if m.curThink < 0 {
		m.curThink = m.AddBlock(Block{Kind: "thinking"})
	}
	return m.curThink
}

func (m *Model) ensureTool(callID, name string) int {
	if i, ok := m.tools[callID]; ok && callID != "" {
		return i
	}
	i := m.AddBlock(Block{Kind: "tool", ToolName: name, ToolStatus: "running", ToolCallID: callID})
	if callID != "" {
		m.tools[callID] = i
	}
	return i
}

// setToolArgs records a tool call's arguments: the raw JSON (so write
// content can render a collapsible preview like pi) plus the pretty
// one-line header. Later (fuller) args overwrite earlier partials.
func (m *Model) setToolArgs(i int, name, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	m.blocks[i].ToolArgsRaw = raw
	if h := prettyArgs(name, raw); h != "" {
		m.blocks[i].ToolArgs = h
	}
}

// update -------------------------------------------------------------------

func (m *Model) sendCmd(steer bool, text string, images []pirpc.ImageContent) tea.Cmd {
	m.thinking = true
	m.escArm = time.Time{} // fresh turn drops a stale cancel arm
	m.Status = "pi is running…"
	m.RefreshFollow()
	return func() tea.Msg {
		var err error
		if steer {
			_, err = m.Pi.Steer(text, images...)
			if err != nil { // fallback: follow_up via plain prompt
				_, err = m.Pi.Prompt(text, images...)
			}
		} else {
			_, err = m.Pi.Prompt(text, images...)
		}
		return sentAckMsg{err: err}
	}
}

// submitInput sends the input (or steers mid-turn). Shared by Enter and
// tray-Enter. Empty text + tray sends the images alone.
func (m *Model) submitInput() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" && len(m.imgAtts) == 0 {
		return nil
	}
	m.pushHist(text)
	m.histIdx = -1
	if b, arg, ok := m.FindBuiltin(text); ok {
		m.ta.Reset()
		m.refreshCmds()
		m.refreshAt()
		m.Refresh()
		return b.Run(m, arg)
	}
	// @image.png → vision attachments (pi CLI parity); the @text
	// stays so history keeps the file ref, images ride the RPC.
	// Tray chips (drops/pastes/Tab-completed @) join in too.
	images, notes := m.takeImages(text)
	for _, n := range notes {
		m.AddBlock(Block{Kind: "notice", Text: n, Err: images == nil})
	}
	if images == nil {
		// Tray image failed to load: abort the send, keep input + tray
		// so the user can fix/remove the chip instead of half-sending.
		m.Refresh()
		return nil
	}
	if m.thinking {
		m.ta.Reset()
		m.closeAt()
		return m.sendCmd(true, text, images)
	}
	m.ta.Reset()
	m.closeAt()
	m.Refresh()
	return m.sendCmd(false, text, images)
}

func (m *Model) queryStats() tea.Cmd {
	return func() tea.Msg {
		s, err := m.Pi.GetStats()
		return statsMsg{stats: s, err: err}
	}
}

func (m Model) fetchStateOnce() tea.Cmd {
	return func() tea.Msg {
		st, err := m.Pi.GetState()
		return stateRefreshMsg{state: st, err: err}
	}
}

// ReconcileTurnCmd re-checks get_state after a dialog closes mid-turn: a
// settle swallowed behind the dialog would otherwise stick the pet on
// Working... forever. Quiet (nil) when idle or disconnected.
func (m Model) ReconcileTurnCmd() tea.Cmd {
	if !m.thinking || m.Pi == nil {
		return nil
	}
	return m.fetchStateOnce()
}

// workspace git status ------------------------------------------------------

// wsFile is one changed file with diff counts.

func (m *Model) RespawnPi() tea.Cmd {
	m.respawning = true
	m.Status = "reconnecting pi…"
	m.Refresh()
	opts := m.spawnOpts
	opts.Session = m.sessionFile
	if opts.NoSession {
		opts.Session = ""
	}
	old := m.Pi
	return func() tea.Msg {
		old.Close()
		c, err := pirpc.Spawn(opts)
		if err != nil {
			return respawnMsg{err: err}
		}
		return respawnMsg{client: c}
	}
}

// SwitchSession respawns pi onto another session file (the /resume picker).
// Same reconnect path as RespawnPi; fetchAll repopulates the chat. A
// startup ping turns a silently-dying pi into pi's own reason (e.g. the
// session's folder was deleted after listing) instead of "pi has exited".
func (m *Model) SwitchSession(path string) tea.Cmd {
	if path == "" || path == m.sessionFile {
		m.AddBlock(Block{Kind: "notice", Text: "already on this session"})
		m.Refresh()
		return nil
	}
	m.respawning = true
	m.Status = "switching session…"
	m.Refresh()
	opts := m.spawnOpts
	opts.Session = path
	old := m.Pi
	return func() tea.Msg {
		old.Close()
		c, err := pirpc.Spawn(opts)
		if err != nil {
			return respawnMsg{err: err}
		}
		if _, err := c.GetState(); err != nil {
			select {
			case <-c.Done():
				// pi died at startup: report its reason, not "pi has exited"
				c.Close()
				if reason := pirpc.StderrTail(); reason != "" {
					err = fmt.Errorf("resume failed: %s", reason)
				} else {
					err = fmt.Errorf("resume failed: %w", err)
				}
				return respawnMsg{err: err}
			default:
				// slow starter; fetchAll will confirm
			}
		}
		return respawnMsg{client: c}
	}
}

func (m *Model) Refresh() {
	if !m.ready {
		return
	}
	// only stick to bottom when already there — no jump while reading history
	follow := m.vp.AtBottom()
	m.vp.SetContent(m.renderBlocks())
	if follow {
		m.vp.GotoBottom()
	}
	now := time.Now()
	s := m.buildSidebarContent()
	m.sideCache = s
	m.sideCacheAt = now
	m.sideVp.SetContent(s)
	m.lastPaint = now
	m.pendingPaint = false
}

// ToggleSide hides/shows the sidebar and reflows chat+input widths
// (same math as the WindowSize handler).

func (m *Model) ToggleSide() {
	m.hideSide = !m.hideSide
	if !m.ready {
		return
	}
	w := m.mainW()
	m.vp.Width = w
	m.ta.SetWidth(w - 6)
	m.Refresh()
}

// TogglePlugins collapses/expands the sidebar PLUGINS list (click its
// header or /plugins). When the section is hidden (Sidebar tab default)
// the first toggle reveals it expanded instead of flipping blind state.
func (m *Model) TogglePlugins() {
	if !m.SideVisible(SidePlugins) {
		m.showPlugins = true
		m.setSideVisible(SidePlugins, true)
	} else {
		m.showPlugins = !m.showPlugins
	}
	if !m.ready {
		return
	}
	m.Refresh()
}

// ToggleMouse flips mouse capture at runtime (/mouse): on = clickable
// sidebar + wheel scroll (hold Option/Shift to select text), off = native
// text selection (sidebar still scrolls with Ctrl/Alt+↑↓). Returns the tea
// command that enables/disables terminal mouse reporting.
func (m *Model) ToggleMouse(arg string) tea.Cmd {
	on := !m.Mouse
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on", "enable", "true", "1":
		on = true
	case "off", "disable", "false", "0":
		on = false
	}
	m.Mouse = on
	if on {
		m.AddBlock(Block{Kind: "notice", Text: "mouse on — click sidebar · wheel scrolls · hold Option/Shift to select text"})
	} else {
		m.AddBlock(Block{Kind: "notice", Text: "mouse off — native text selection · ↑↓ scrolls · Shift+↑↓ recalls history · sidebar scrolls with Ctrl+↑↓"})
	}
	if !m.ready {
		return nil
	}
	m.Refresh()
	if on {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

// refreshFollow rebuilds content and jumps to bottom (for new content worth seeing).

func (m *Model) RefreshFollow() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.renderBlocks())
	m.vp.GotoBottom()
	now := time.Now()
	s := m.buildSidebarContent()
	m.sideCache = s
	m.sideCacheAt = now
	m.sideVp.SetContent(s)
	m.lastPaint = now
	m.pendingPaint = false
}

// refreshStreaming paints a high-frequency pi event (text/thinking delta,
// tool partial result) at most once per streamFrame: bursts mutate blocks
// on every event but share one SetContent, with a trailing flush tick so
// the last delta never stays stale. The sidebar rebuilds at most once per
// sideThrottle mid-burst.
func (m *Model) refreshStreaming() tea.Cmd {
	if !m.ready {
		return nil
	}
	now := time.Now()
	if !m.lastPaint.IsZero() && now.Sub(m.lastPaint) < streamFrame {
		if !m.pendingPaint {
			m.pendingPaint = true
			return streamFlushCmd(streamFrame - now.Sub(m.lastPaint))
		}
		return nil
	}
	follow := m.vp.AtBottom()
	m.vp.SetContent(m.renderBlocks())
	if follow {
		m.vp.GotoBottom()
	}
	if m.sideCache == "" || now.Sub(m.sideCacheAt) >= sideThrottle {
		s := m.buildSidebarContent()
		m.sideCache = s
		m.sideCacheAt = now
		m.sideVp.SetContent(s)
	}
	m.lastPaint = now
	m.pendingPaint = false
	return nil
}

// flushStreaming paints a previously coalesced streaming update.
func (m *Model) flushStreaming() {
	if !m.ready || !m.pendingPaint {
		return
	}
	m.pendingPaint = false
	follow := m.vp.AtBottom()
	m.vp.SetContent(m.renderBlocks())
	if follow {
		m.vp.GotoBottom()
	}
	now := time.Now()
	if m.sideCache == "" || now.Sub(m.sideCacheAt) >= sideThrottle {
		s := m.buildSidebarContent()
		m.sideCache = s
		m.sideCacheAt = now
		m.sideVp.SetContent(s)
	}
	m.lastPaint = now
}

// Cwd is the pi session working directory (picker loaders live outside
// this package and need it to find pi's session dir).
func (m *Model) Cwd() string { return m.cwd }

// ThinkLvl is the current thinking level for the /thinking picker.
func (m *Model) ThinkLvl() string { return m.thinkLvl }

// CycleThinking rotates to the next thinking level (Ctrl+T, no picker).
func (m *Model) CycleThinking() tea.Cmd {
	m.Status = "switching thinking…"
	m.Refresh()
	cur := m.ThinkLvl()
	return func() tea.Msg {
		levels, err := m.Pi.GetLevels()
		if err != nil {
			return SettingsRefreshMsg{Err: err}
		}
		if len(levels) == 0 {
			return SettingsRefreshMsg{Err: fmt.Errorf("no thinking levels")}
		}
		next := levels[0]
		for i, l := range levels {
			if l == cur {
				next = levels[(i+1)%len(levels)]
				break
			}
		}
		if err := m.Pi.SetLevel(next); err != nil {
			return SettingsRefreshMsg{Err: err}
		}
		// Toast, not chat: rapid Ctrl+T replaces one popup instead of
		// spamming one line per press — same Notice path as /thinking.
		return SettingsRefreshMsg{Notice: "thinking → " + next, Level: next}
	}
}

// OpenThinking shows the thinking-level picker (/thinking).
func (m *Model) OpenThinking() tea.Cmd {
	m.Status = "loading thinking levels…"
	m.Refresh()
	cur := m.ThinkLvl()
	return func() tea.Msg {
		levels, err := m.Pi.GetLevels()
		if err != nil {
			return PickerMsg{Kind: "thinking", Err: err}
		}
		return PickerMsg{Kind: "thinking", Options: levels, Current: cur}
	}
}

// SessionFile is the current pi session file ("": ephemeral/unknown).
func (m *Model) SessionFile() string { return m.sessionFile }

// NoSession reports whether this TUI runs without persisting sessions.
func (m *Model) NoSession() bool { return m.spawnOpts.NoSession }

// Builtin is one locally-executed slash command.
//
// Origin tells where the feature comes from and is shown in /help-style
// surfaces: "pi" re-implements one of pi's TUI-level builtins over RPC
// (pi's own builtins never arrive via get_commands), "pitago" is ours.
// Implementations live in src/builtin; this package only holds the table.
type Builtin struct {
	Name, Desc, Usage, Origin string
	Run                       func(m *Model, arg string) tea.Cmd
}

// ConfirmFunc runs the Enter action of a picker dialog kind.
// Implementations live in src/builtin (see Confirmers).
type ConfirmFunc func(m *Model, d *Dialog, ri int) (tea.Model, tea.Cmd)

// UseBuiltins wires the command registry (call once from main).
func (m *Model) UseBuiltins(b []Builtin, c map[string]ConfirmFunc) {
	m.builtins = b
	m.confirm = c
}

// Configure wires spawn options + config paths (call once from main).
func (m *Model) Configure(opts pirpc.Options, keyPath string) {
	m.spawnOpts = opts
	m.KeyPath = keyPath
	m.AuthPath = pirpc.AuthStatePath()
	m.recentPath = pirpc.RecentPath()
	m.recentModels = recent.Load(m.recentPath)
	m.favPath = pirpc.FavPath()
	m.favModels = favorite.Load(m.favPath)
	m.favSet = favorite.Set(m.favModels)
	m.themePath = theme.ThemePath()
	saved := theme.Load(m.themePath)
	ApplyTheme(theme.Get(saved))
	m.ThemeName = theme.Get(saved).Name
	m.prefsPath = PrefsPath()
	prefs := LoadPrefs(m.prefsPath)
	m.HideThinking = prefs.HideThinking
	m.CurAgent = prefs.CurrentSubagent
	m.Side = prefs.Side
	m.CmdShortcuts = prefs.CmdShortcuts
	palette.Win = prefs.EffectiveAutocompleteMax()
}

// FindBuiltin matches "/name" or "/name args" against the registry.
// Anything else (extension/prompt/skill commands, chat text) falls through
// to pi via Prompt.
func (m *Model) FindBuiltin(text string) (Builtin, string, bool) {
	if len(text) < 2 || text[0] != '/' {
		return Builtin{}, "", false
	}
	rest := text[1:]
	name, arg := rest, ""
	if i := strings.Index(rest, " "); i >= 0 {
		name, arg = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	if strings.Contains(name, "\n") || name == "" {
		return Builtin{}, "", false
	}
	for _, b := range m.builtins {
		if b.Name == name {
			return b, arg, true
		}
	}
	return Builtin{}, "", false
}

// RunBuiltin executes a registry command by name (nil when unknown).
func (m *Model) RunBuiltin(name, arg string) tea.Cmd {
	for _, b := range m.builtins {
		if b.Name == name {
			return b.Run(m, arg)
		}
	}
	return nil
}

// BuiltinRepo exposes the registry as repo-style commands for the / popup.
// Pitago-only commands keep Source "pitago" so the popup shows [pitago]
// instead of [builtin] (pi-parity re-implements stay [builtin]).
func BuiltinRepo(builtins []Builtin) []pirpc.RepoCommand {
	out := make([]pirpc.RepoCommand, 0, len(builtins))
	for _, b := range builtins {
		src := "builtin"
		if b.Origin == "pitago" {
			src = "pitago"
		}
		out = append(out, pirpc.RepoCommand{Name: b.Name, Description: b.Desc, Source: src})
	}
	return out
}

// ProgRef lets respawns rewire the pi event stream (tea.Program.Send is
// thread-safe). Set once from main before Run.
var ProgRef *tea.Program

// WireClient routes a pi client's events into the UI program.
func WireClient(c *pirpc.Client) {
	c.OnEvent = func(e pirpc.Event) {
		if ProgRef != nil {
			ProgRef.Send(piEventMsg{e})
		}
	}
}
