package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/theme"
	"pitago/src/pirpc"
)

// Pitago settings hub (/pitago-setting): two-pane dialog like the /model
// picker — left = sections, right = the selected section's rows. All rows
// come from Model state (no RPC), so the hub opens instantly.
// Keys: ↑↓ move in the focused pane, ←/→/Tab switch pane, typing filters
// the right pane, Enter runs/opens, Esc closes.

// Hub section ids (Dialog.PsecIDs parallels the left pane).
const (
	PsecAgent  = "agent"
	PsecCmd    = "command"
	PsecKeys   = "shortcut"
	PsecSkill  = "skill"
	PsecPrompt = "prompt"
	PsecExt    = "extension"
	PsecPlugin = "plugin"
	PsecMarket = "market"
	PsecMCP    = "mcp"
	PsecTool   = "tool"
	PsecTasks  = "tasks"
	PsecSide   = "side"
	PsecTheme  = "theme"
	PsecLogin  = "login"
)

// Payload markers for non-runnable right rows: "@<section>" opens the
// classic single dialog, "" is info-only (toast hint on Enter). Runnables
// that mutate in place use their own prefix ("side:", "tasks:", "theme:") —
// see confirmPconfig in src/builtin.
const (
	psecActAgent = "@agent"
	psecActLogin = "@login"
	// psecActBg marks a background row in the hub's Theme section. A
	// background is not a theme, so it rides its own payload prefix.
	psecActBg = "bg:"
)

// PsecBgPrefix is psecActBg for src/builtin, which confirms hub rows by
// payload prefix (see confirmPconfig).
func PsecBgPrefix() string { return psecActBg }

// CountCmds tallies extension catalog commands per source (skill/prompt/
// extension) for the section labels.
func CountCmds(cmds []pirpc.RepoCommand) (skill, prompt, ext int) {
	for _, c := range cmds {
		switch c.Source {
		case "skill":
			skill++
		case "prompt":
			prompt++
		case "extension":
			ext++
		}
	}
	return skill, prompt, ext
}

// psecCount returns the left-pane badge for a section (-1 = no badge).
func psecCount(m *Model, id string) int {
	sk, pr, ex := CountCmds(m.Cmds)
	switch id {
	case PsecCmd:
		return len(m.builtinCmdRows())
	case PsecKeys:
		return len(m.shortcutRows())
	case PsecSkill:
		return sk
	case PsecPrompt:
		return pr
	case PsecExt:
		return ex
	case PsecPlugin:
		return len(m.Plugins)
	case PsecMarket:
		if m.Market == nil && m.MarketErr == "" {
			return -1 // not loaded yet: no badge
		}
		return len(m.Market)
	case PsecMCP:
		return len(m.MCP)
	case PsecTool:
		return m.Stats.ToolCalls
	}
	return -1
}

// OpenPconfig pushes the two-pane settings hub (focus starts on sections).
func (m *Model) OpenPconfig() {
	d := &Dialog{Kind: "pconfig", Title: "Pitago settings",
		Provs:     []string{"Agent", "Commands", "Shortcuts", "Skills", "Prompts", "Extensions", "Plugins", "Marketplace", "MCP", "Tools", "Tasks", "Sidebar", "Theme", "Login"},
		PsecIDs:   []string{PsecAgent, PsecCmd, PsecKeys, PsecSkill, PsecPrompt, PsecExt, PsecPlugin, PsecMarket, PsecMCP, PsecTool, PsecTasks, PsecSide, PsecTheme, PsecLogin},
		ProvFocus: true}
	m.LoadPsecRows(d)
	m.Dialogs = append(m.Dialogs, d)
	d.PalRow, d.BgRow = 0, -1 // palette "default" is a sane first row; backgrounds start unvisited
	m.Refresh()
}

// tasksSettingsJump reports the /tasks-menu Settings row.
func tasksSettingsJump(d *Dialog, choice int) bool {
	return d != nil && d.Kind == "ui" && d.Title == "Tasks" &&
		choice >= 0 && choice < len(d.Options) && d.Options[choice] == "Settings"
}

// openTasksSettings opens the hub focused on the native Tasks tab
// (right pane focused, ready to cycle values).
func (m *Model) openTasksSettings() {
	m.OpenHubSection(PsecTasks)
}

// OpenKeysSettings opens the settings hub on the Shortcuts section. The
// standalone shortcuts dialog is gone — /shortcuts lands here.
func (m *Model) OpenKeysSettings() {
	m.OpenHubSection(PsecKeys)
}

// OpenHubSection opens the hub with the right pane already focused on
// one section (/shortcuts → Shortcuts, /tasks → Settings).
func (m *Model) OpenHubSection(id string) {
	m.OpenPconfig()
	if n := len(m.Dialogs); n > 0 {
		if hd := m.Dialogs[n-1]; hd.Kind == "pconfig" {
			for i, sid := range hd.PsecIDs {
				if sid == id {
					hd.ProvCursor = i
				}
			}
			hd.ProvFocus = false
			m.LoadPsecRows(hd)
		}
	}
}

// reloadHubRows rebuilds the open settings hub's right pane in place
// (section=="" keeps the current section): async results (market fetch,
// install/remove) refresh the rows without closing the hub or losing
// the cursor.
// refreshHubSection rebuilds one section's rows in place, but ONLY when
// the hub is already showing that section. Background work (a registry
// page landing, stars, a sort) must never yank the hub to another
// section: reloadHubRows(PsecMarket) moves the left-pane cursor, so a
// result that lands while the user browsed elsewhere steals the focus.
// The selection follows its package, not its old row index.
func (m *Model) refreshHubSection(id string) {
	d := m.hubDialog()
	if d == nil || d.CurPsec() != id {
		return
	}
	sel := ""
	if ri := psecCursor(d); ri >= 0 && id == PsecMarket && ri < len(m.Market) {
		sel = m.Market[ri].Name
	}
	m.reloadHubRows("")
	if sel != "" {
		if d := m.hubDialog(); d != nil {
			for fi, ri := range d.FIdx {
				if ri < len(m.Market) && m.Market[ri].Name == sel {
					d.Cursor = fi
					break
				}
			}
		}
	}
}

func (m *Model) reloadHubRows(section string) {
	if len(m.Dialogs) == 0 || m.Dialogs[0].Kind != "pconfig" {
		return
	}
	d := m.Dialogs[0]
	if section != "" {
		for i, id := range d.PsecIDs {
			if id == section {
				d.ProvCursor = i
			}
		}
	}
	cur := d.Cursor
	m.LoadPsecRows(d)
	if cur < len(d.FIdx) {
		d.Cursor = cur
	}
}

// hubDialog returns the open settings hub, or nil when none is up.
func (m *Model) hubDialog() *Dialog {
	if len(m.Dialogs) == 0 {
		return nil
	}
	if d := m.Dialogs[0]; d.Kind == "pconfig" {
		return d
	}
	return nil
}

// hubWindow is the hub's fixed row window (both panes): the render clamps
// it, and the star hydration reads the same window off the model so the
// background fetch only ever covers what is on screen.
func hubWindow(m *Model) int {
	win := m.winH - 14
	if win < 12 {
		win = 12
	}
	if win > 24 {
		win = 24
	}
	return win
}

// hubBoxW is the hub dialog's outer width. One source of truth: the
// renderer lays the box out at this width, and psecRows clamps its
// section message to it so a long line cannot wrap and stretch the box.
func hubBoxW(m *Model) int {
	w := m.winW - 10
	if w < 70 {
		w = 70
	}
	if w > 150 {
		w = 150
	}
	return w
}

// CurPsec is the selected section id (left pane cursor).
func (d *Dialog) CurPsec() string {
	if len(d.PsecIDs) == 0 {
		return ""
	}
	if d.ProvCursor < 0 || d.ProvCursor >= len(d.PsecIDs) {
		return d.PsecIDs[0]
	}
	return d.PsecIDs[d.ProvCursor]
}

// LoadPsecRows rebuilds the right pane for the selected section.
func (m *Model) LoadPsecRows(d *Dialog) {
	opts, descs, payload, msg := psecRows(m, d.CurPsec())
	d.Options, d.Descs, d.Payload = opts, descs, payload
	d.Message = msg
	d.Cursor = 0
	d.Reindex()
}

// curatedPlugins are the pi packages pitago is built around. They ride the
// Plugins section as ★ rows: installed or not, the star marks the row as a
// suggestion. name is the npm name — pi install takes "npm:<name>".
var curatedPlugins = []pluginSuggestion{
	{"pi-subagents", "delegate work to background teams"},
	{"pi-ask-user", "handshake before risky decisions"},
	{"@dietrichgebert/ponytail", "lazy-senior mode: less code, fewer deps"},
}

// pluginSuggestion is one ★ package in the Plugins section.
type pluginSuggestion struct{ name, why string }

// suggestions returns every suggested package: the curated list plus the
// user's own picks (prefs.json, added with Ctrl+F), curated first and
// deduped. Static per row build — the model mirrors prefs.
func (m *Model) suggestions() []pluginSuggestion {
	out := make([]pluginSuggestion, 0, len(curatedPlugins)+len(m.SuggestPlugins))
	seen := map[string]bool{}
	for _, c := range curatedPlugins {
		if c.name != "" && !seen[c.name] {
			seen[c.name] = true
			out = append(out, c)
		}
	}
	for _, n := range m.SuggestPlugins {
		if n = pluginName(strings.TrimSpace(n)); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, pluginSuggestion{name: n, why: "your pick"})
		}
	}
	return out
}

// suggestAddKind is the free-text prompt Ctrl+F opens in the Plugins tab.
const suggestAddKind = "pluginadd"

// openSuggestAdd pushes the "add a suggested plugin" prompt over the hub
// (same stacking as the shortcut capture, so Esc returns to the tab).
func (m *Model) openSuggestAdd() {
	d := &Dialog{Kind: suggestAddKind, Title: "Suggest a plugin",
		Message: "npm name · Enter adds it to the ★ list · Esc cancels"}
	m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	m.Refresh()
}

// AddSuggestPlugin persists a user-picked suggested package (Ctrl+F). Bare
// npm name: an "npm:" prefix the user pasted is stripped, and anything pi
// would refuse as a spec never reaches prefs.json.
func (m *Model) AddSuggestPlugin(name string) {
	name = pluginName(strings.TrimSpace(name))
	if name == "" {
		return
	}
	if !validPluginSpec("npm:" + name) {
		m.AddBlock(Block{Kind: "notice", Text: "not an npm package name: " + name, Err: true})
		m.Refresh()
		return
	}
	prefs := LoadPrefs(m.prefsPath)
	for _, s := range m.suggestions() {
		if s.name == name {
			m.AddBlock(Block{Kind: "notice", Text: name + " is already suggested"})
			m.Refresh()
			return
		}
	}
	prefs.SuggestPlugins = append(prefs.SuggestPlugins, name)
	if err := SavePrefs(m.prefsPath, prefs); err != nil {
		m.AddBlock(Block{Kind: "notice", Text: "prefs.json: " + err.Error(), Err: true})
		m.Refresh()
		return
	}
	m.SuggestPlugins = prefs.SuggestPlugins
	m.AddBlock(Block{Kind: "notice", Text: "★ " + name + " suggested — Enter installs it"})
	m.Refresh()
}

// psecRows builds one section's right pane. Payload parallels Options: the
// /command name for runnable rows, "@agent"/"@theme"/"@login" for action
// rows, "" for info-only rows.
func psecRows(m *Model, id string) (opts, descs, payload []string, msg string) {
	switch id {
	case PsecAgent:
		msg = "Enter opens the agent settings dialog · Esc closes"
		return []string{"Open agent settings →"},
			[]string{"model · thinking · steering · images · skills · … (pi parity)"},
			[]string{psecActAgent}, msg
	case PsecTheme:
		// Two columns: the palettes (↑↓ live-previews through previewRow,
		// Enter applies) and the background set behind them. Accent hex
		// rides the desc so a row stays filterable.
		msg = "↑↓ previews live · → background column · Enter applies · Esc closes"
		for _, n := range theme.Names() {
			opts = append(opts, n)
			desc := theme.Get(n).Accent
			if n == m.currentTheme() {
				desc = "✓ current · " + desc
			}
			descs = append(descs, desc)
			payload = append(payload, "theme:"+n)
		}
		for _, b := range theme.Backgrounds() {
			opts = append(opts, b)
			desc := theme.BackgroundHex(b, theme.Get(m.currentTheme()).Light)
			switch {
			case desc == "":
				desc = "terminal background"
			case b == m.currentBackground():
				desc = "✓ current · " + desc
			}
			descs = append(descs, desc)
			payload = append(payload, psecActBg+b)
		}
	case PsecLogin:
		msg = "Enter opens login · Esc closes"
		return []string{"Open login →"},
			[]string{"providers · keys · OAuth"},
			[]string{psecActLogin}, msg
	case PsecCmd:
		// The local registry only: pi's re-implemented builtins and
		// pitago's own commands. Extension/prompt/skill commands stay in
		// their own sections, so nothing third-party lands here.
		msg = "Enter fills /command · Ctrl+S assigns Alt-shortcut · Esc closes"
		for _, r := range m.builtinCmdRows() {
			opts = append(opts, "/"+r.name)
			descs = append(descs, shortDesc(r.desc, 42)+" ["+r.tag+"]"+m.shortcutSuffix(r.name))
			payload = append(payload, r.name)
		}
		if len(opts) == 0 {
			opts = []string{"— no commands —"}
			descs = []string{"the command registry is empty"}
			payload = []string{""}
		}
	case PsecKeys:
		// The /shortcuts reference, in the hub: every built-in key plus the
		// Alt shortcuts assigned from the Commands section. Custom rows
		// carry their /command as payload, so Ctrl+S re-opens the capture
		// dialog for it; the built-in keys are info-only.
		msg = "keyboard reference · type filters · Ctrl+S reassigns a custom row · Esc closes"
		for _, r := range m.shortcutRows() {
			opts = append(opts, r.key)
			descs = append(descs, shortDesc(r.desc, 40)+" ["+r.cat+"]")
			payload = append(payload, r.cmd)
		}
		if len(opts) == 0 {
			opts = []string{"— no shortcuts —"}
			descs = []string{"the shortcut table is empty"}
			payload = []string{""}
		}
	case PsecSkill:
		msg = "Enter fills /command · Ctrl+S assigns Alt-shortcut · Esc closes"
		for _, c := range m.Cmds {
			if c.Source != "skill" {
				continue
			}
			opts = append(opts, "/"+c.Name)
			descs = append(descs, shortDesc(c.Description, 48)+m.shortcutSuffix(c.Name))
			payload = append(payload, c.Name)
		}
		if len(opts) == 0 {
			opts = []string{"— no skills —"}
			descs = []string{"/reload refreshes the catalog"}
			payload = []string{""}
		}
	case PsecPrompt:
		msg = "Enter fills /command · Ctrl+S assigns Alt-shortcut · Esc closes"
		for _, c := range m.Cmds {
			if c.Source != "prompt" {
				continue
			}
			opts = append(opts, "/"+c.Name)
			descs = append(descs, shortDesc(c.Description, 48)+m.shortcutSuffix(c.Name))
			payload = append(payload, c.Name)
		}
		if len(opts) == 0 {
			opts = []string{"— no prompts —"}
			descs = []string{"/reload refreshes the catalog"}
			payload = []string{""}
		}
	case PsecExt:
		msg = "Enter fills /command · Ctrl+S assigns Alt-shortcut · Esc closes"
		for _, c := range m.Cmds {
			if c.Source != "extension" {
				continue
			}
			d := shortDesc(c.Description, 40)
			if tag := extTag(c); tag != "" {
				d += " [" + tag + "]"
			}
			opts = append(opts, "/"+c.Name)
			descs = append(descs, d+m.shortcutSuffix(c.Name))
			payload = append(payload, c.Name)
		}
		if len(opts) == 0 {
			opts = []string{"— no extensions —"}
			descs = []string{"/reload refreshes the catalog"}
			payload = []string{""}
		}
	case PsecPlugin:
		msg = "★ suggested · Enter installs · Delete uninstalls · Ctrl+F suggests your own · Esc closes"
		// The star is a property of the package, not of the install state:
		// a suggested package keeps its ★ after being installed, so the
		// user can still see it is one of ours. Uninstalling drops the row
		// back to the ★ suggestion list below (same row, installable).
		sug := m.suggestions()
		isSug := map[string]bool{}
		for _, s := range sug {
			isSug[s.name] = true
		}
		for _, p := range m.Plugins {
			name, desc := p.Name, p.Spec
			if isSug[p.Name] {
				name, desc = "★ "+p.Name, "suggested · "+p.Spec
			}
			opts = append(opts, name)
			if m.plugBusySpec != "" && p.Spec == m.plugBusySpec {
				// the running op owns this row: spinner + live elapsed
				opts[len(opts)-1] = m.pluginBusyFrame() + " " + p.Name
				descs = append(descs, m.pluginBusyRowDesc())
				payload = append(payload, "")
				continue
			}
			descs = append(descs, desc)
			payload = append(payload, "")
		}
		// The suggested-but-not-installed ones: the installable ★ rows.
		// Payload is the marketplace one, so Enter reuses the single
		// pi install path (busy gate + confirm + npm spec).
		for _, s := range sug {
			if marketInstalled(m, s.name) {
				continue // already listed above, star and all
			}
			opts = append(opts, "★ "+s.name)
			descs = append(descs, "suggested · "+s.why+" · Enter installs")
			payload = append(payload, "market:"+s.name)
		}
		if m.plugBusyAction != "" {
			msg = m.pluginBusyLabel(m.plugBusyAction, m.plugBusySpec) +
				" · runs in the background"
		}
		if note := m.plugNoteLine(); note != "" {
			msg = warnStyle.Render(Fit(note, hubBoxW(m)-2)) // the gate prompt / result
		}
	case PsecMarket:
		// The filter is a real remote npm search here (not a local
		// filter), so the header names the query and the row count.
		if q := m.MarketQuery(); q != "" {
			msg = fmt.Sprintf("search %q · %d results · ↑↓ select · Enter installs · Esc clears", q, len(m.Market))
		} else {
			msg = fmt.Sprintf("%d pi packages · type to search npm · ↑↓ select · Enter installs · Esc closes",
				len(m.Market))
		}
		if m.plugBusyAction != "" {
			msg = m.pluginBusyLabel(m.plugBusyAction, m.plugBusySpec) +
				" · runs in the background"
		}
		if note := m.plugNoteLine(); note != "" {
			msg = warnStyle.Render(Fit(note, hubBoxW(m)-2))
		}
		if m.MarketErr != "" {
			opts = []string{"— market unavailable —"}
			descs = []string{Short(m.MarketErr, 60)}
			payload = []string{""}
		}
		for _, e := range m.Market {
			ver := e.Version
			if ver != "" && !strings.HasPrefix(ver, "v") {
				ver = "v" + ver
			}
			// stars are decoration: prefix only once resolved
			stars := ""
			if e.StarsKnown {
				stars = "★" + fmtStars(e.Stars) + " "
			}
			opts = append(opts, e.Name)
			if m.plugBusySpec != "" && m.plugBusySpec == "npm:"+e.Name {
				// the running op owns this row: spinner + live elapsed
				opts[len(opts)-1] = m.pluginBusyFrame() + " " + e.Name
				descs = append(descs, m.pluginBusyRowDesc())
				payload = append(payload, "")
				continue
			}
			if marketInstalled(m, e.Name) {
				descs = append(descs, "✓ installed · "+stars+shortDesc(ver+" "+e.Desc, 44))
				payload = append(payload, "")
			} else {
				descs = append(descs, stars+shortDesc(strings.TrimSpace(ver+" — "+e.Desc), 48))
				payload = append(payload, "market:"+e.Name)
			}
		}
		if len(opts) == 0 {
			// an empty list is two different things: a fetch in flight
			// (rows arrive in a moment) and a registry with nothing
			// for this query. Say which one instead of guessing.
			if marketInflight {
				opts = []string{"— loading plugins… —"}
				descs = []string{"npm is searching; rows appear as soon as it lands"}
			} else {
				opts = []string{"— empty market —"}
				descs = []string{"no pi packages match — Esc clears the search"}
			}
			payload = []string{""}
		} else if marketMore {
			// npm's "total" is a constant, so "more" is just the
			// registry having filled the last page.
			opts = append(opts, fmt.Sprintf("… load more (%d shown) …", len(m.Market)))
			descs = append(descs, "Enter loads the next 100")
			payload = append(payload, "marketmore")
		}
	case PsecMCP:
		// /mcp is a real command now, so every server row is runnable:
		// the manager is where exposure, sign-in and enable/disable
		// live, and a disabled server is listed there precisely so it
		// can be enabled again.
		msg = "Enter fills /mcp, the server manager (state · tools · exposure · sign-in · enable/disable) · Esc closes"
		for _, s := range m.MCP {
			switch {
			case s.Disabled:
				opts = append(opts, s.Name)
				descs = append(descs, "disabled in mcp.json · /mcp can enable it")
				payload = append(payload, "mcp")
			case s.Connected:
				opts = append(opts, s.Name)
				descs = append(descs, fmt.Sprintf("● %d/%d direct · ~%s tok", s.Direct, s.Total, fmtComma(s.Tokens)))
				payload = append(payload, "mcp")
			default:
				opts = append(opts, s.Name)
				descs = append(descs, "○ not connected · /mcp shows why")
				payload = append(payload, "mcp")
			}
		}
		if len(opts) == 0 {
			opts = []string{"— no servers —"}
			descs = []string{"add one to ~/.pi/agent/mcp.json"}
			payload = []string{""}
		}
	case PsecTool:
		msg = "Per-tool calls this session (from the transcript) · Ctrl+G expands tool output · Esc closes"
		for _, t := range toolStats(m) {
			opts = append(opts, t.name)
			descs = append(descs, t.desc)
			payload = append(payload, "")
		}
		if len(opts) == 0 {
			opts = []string{"— no tool calls yet —"}
			descs = []string{"tools appear here as the agent works"}
			payload = []string{""}
		}
	case PsecTasks:
		// Native replacement for /tasks → Settings: the extension's custom
		// panel can't cross RPC (pi stubs ui.custom), so the values cycle
		// here and persist to the project .pi/tasks-config.json.
		msg = "Enter cycles a value · project .pi/tasks-config.json · Esc closes"
		vals := loadTasksSettings(m.cwd, piAgentDir())
		for _, td := range taskSettings {
			opts = append(opts, td.label)
			descs = append(descs, vals[td.key]+" · Enter: next")
			payload = append(payload, "tasks:"+td.key)
		}
		// The widget lives in prefs.json, not tasks-config.json: it is a
		// pitago render decision, and reading it from a file on every tick
		// would be a read per repaint.
		widget := "off"
		if m.TaskWidgetVisible() {
			widget = "on"
		}
		opts = append(opts, "Show task widget above editor")
		descs = append(descs, widget+" · Enter: toggle · todos also show in the sidebar")
		payload = append(payload, "taskwidget")
	case PsecSide:
		msg = "Enter shows/hides a sidebar section · MCP + Plugins + Commands start hidden · Esc closes"
		for _, k := range sideOrder {
			state := "shown"
			if !m.SideVisible(k) {
				state = "hidden"
			}
			opts = append(opts, sideLabel(k))
			descs = append(descs, state+" · Enter: toggle")
			payload = append(payload, "side:"+k)
		}
	}
	return opts, descs, payload, msg
}

// pluginMeta is the cached package.json snapshot for one npm plugin spec.
type pluginMeta struct {
	version, desc string
	ok            bool
}

var pluginMetaCache = map[string]pluginMeta{}

// pluginInstallPath maps a package spec to its local dir: "npm:pi-lens" →
// <agentDir>/npm/node_modules/pi-lens (scoped names keep their @scope/
// path). Git specs have no stable local path — "" there.
func pluginInstallPath(spec string) string {
	name, ok := strings.CutPrefix(spec, "npm:")
	if !ok || strings.TrimSpace(name) == "" {
		return ""
	}
	if dir := piAgentDir(); dir != "" {
		return filepath.Join(dir, "npm", "node_modules", name)
	}
	return ""
}

// pluginMetaFor reads version/description from the installed package.json
// (cached per spec so the render path never hits the disk twice).
func pluginMetaFor(spec string) (pluginMeta, string) {
	if m, ok := pluginMetaCache[spec]; ok {
		return m, pluginInstallPath(spec)
	}
	m := pluginMeta{}
	path := pluginInstallPath(spec)
	if path != "" {
		if raw, err := os.ReadFile(filepath.Join(path, "package.json")); err == nil {
			var pkg struct {
				Version     string `json:"version"`
				Description string `json:"description"`
			}
			if json.Unmarshal(raw, &pkg) == nil && (pkg.Version != "" || pkg.Description != "") {
				m = pluginMeta{version: pkg.Version, desc: pkg.Description, ok: true}
			}
		}
	}
	pluginMetaCache[spec] = m
	return m, path
}

// pluginSource labels a spec by its installer prefix (npm:/git:/…).
func pluginSource(spec string) string {
	if i := strings.Index(spec, ":"); i >= 0 {
		return spec[:i]
	}
	return "—"
}

// pluginCommands lists the /commands contributed by one plugin spec
// (get_commands sourceInfo.source matches the settings.json spec).
func pluginCommands(m *Model, spec string) []pirpc.RepoCommand {
	var out []pirpc.RepoCommand
	for _, c := range m.Cmds {
		if c.SourceInfo != nil && c.SourceInfo.Source == spec {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// pluginDetailLines builds the highlighted plugin's detail column, each
// line exactly w cells wide: title + Spec · Source · Version · Description
// · Path · Commands (name + short desc, like the model picker's DETAILS
// pane). One dim placeholder when nothing is selected.
func pluginDetailLines(m *Model, d *Dialog, w int) []string {
	if w < 30 {
		w = 30
	}
	ri := -1
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		ri = d.FIdx[d.Cursor]
	}
	if ri < 0 || ri >= len(m.Plugins) {
		return []string{"  " + toolStyle.Width(w-2).Render("— no selection —")}
	}
	p := m.Plugins[ri]
	var lines []string
	lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(p.Name, w-2)))
	meta, path := pluginMetaFor(p.Spec)
	rows := [][2]string{
		{"Spec", orDash(p.Spec)},
		{"Source", orDash(pluginSource(p.Spec))},
		{"Version", orDash(meta.version)},
		{"Path", orDash(path)},
	}
	const lw = 11
	valW := w - 2 - lw - 1
	if valW < 10 {
		valW = 10
	}
	for _, r := range rows {
		lab := toolStyle.Render(Fit(r[0], lw))
		style := lipgloss.NewStyle().Foreground(cText)
		if r[1] == "—" {
			style = statusBarStyle
		}
		lines = append(lines, "  "+lab+" "+style.Render(Fit(Short(r[1], valW), valW)))
	}
	// Description wraps onto its own lines (values above stay single-row
	// so the two-pane layout math holds).
	lines = append(lines, "  "+toolStyle.Render(Fit("Description", w-2)))
	if strings.TrimSpace(meta.desc) == "" {
		lines = append(lines, "  "+statusBarStyle.Render(Fit("—", w-2)))
	} else {
		for _, ln := range wrapWords(meta.desc, w-2) {
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(cText).Render(Fit(ln, w-2)))
		}
	}
	cmds := pluginCommands(m, p.Spec)
	lines = append(lines, "  "+toolStyle.Render(Fit(fmt.Sprintf("Commands (%d)", len(cmds)), w-2)))
	if len(cmds) == 0 {
		lines = append(lines, "  "+statusBarStyle.Render(Fit("— none —", w-2)))
		return lines
	}
	for _, c := range cmds {
		row := "/" + c.Name
		if d := strings.Join(strings.Fields(c.Description), " "); d != "" {
			row += " — " + d
		}
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(cText).Render(Fit(Short(row, w-2), w-2)))
	}
	return lines
}

// marketRow lays out one marketplace list row: the name, padded out, and
// the star chip right-aligned in the last cells. Exactly w display cells;
// the chip is dropped entirely when it does not fit (never a wrapped row).
func marketRow(name, chip string, w int) string {
	if w <= 0 {
		return ""
	}
	if chip == "" {
		return Fit(Short(name, w), w)
	}
	cw := lipgloss.Width(chip)
	if cw+1 >= w {
		return Fit(Short(name, w), w)
	}
	nw := w - cw - 1
	return Fit(Short(name, nw), nw) + " " + chip
}

// marketChip is the star chip for one row ("" while the count is unknown,
// so pending rows render exactly as before).
func marketChip(e MarketEntry) string {
	if !e.StarsKnown {
		return ""
	}
	return "★" + fmtStars(e.Stars)
}

// marketDetailLines builds the highlighted market entry's detail column:
// title + Version · Status · Description + the Enter hint. Same fixed
// width contract as pluginDetailLines.
func marketDetailLines(m *Model, d *Dialog, w int) []string {
	if w < 30 {
		w = 30
	}
	ri := -1
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		ri = d.FIdx[d.Cursor]
	}
	if ri < 0 || ri >= len(m.Market) {
		return []string{"  " + toolStyle.Width(w-2).Render("— no selection —")}
	}
	e := m.Market[ri]
	installed := marketInstalled(m, e.Name)
	var lines []string
	title := e.Name
	if installed {
		title += " ✓"
	}
	lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(title, w-2)))
	status := "not installed"
	style := statusBarStyle
	if installed {
		status = "installed"
		style = okStyle
	}
	// stars: "—" without a repo or when the repo will never resolve,
	// "fetching…" only while the lookup is still genuinely unknown
	starVal := "—"
	switch {
	case e.Repo == "":
	case e.StarsKnown:
		starVal = marketChip(e)
	case marketStarsBad[e.Repo]:
		starVal = "—"
	default:
		starVal = "fetching…"
	}
	dlVal := "—"
	if e.Weekly > 0 {
		dlVal = fmtStars(e.Weekly) + "/wk"
	}
	rows := [][2]string{
		{"Version", orDash(e.Version)},
		{"Spec", "npm:" + e.Name},
		{"Stars", starVal},
		{"Downloads", dlVal},
	}
	const lw = 11
	valW := w - 2 - lw - 1
	if valW < 10 {
		valW = 10
	}
	for _, r := range rows {
		lab := toolStyle.Render(Fit(r[0], lw))
		st := lipgloss.NewStyle().Foreground(cText)
		if r[1] == "—" {
			st = statusBarStyle
		}
		lines = append(lines, "  "+lab+" "+st.Render(Fit(Short(r[1], valW), valW)))
	}
	// while this entry is the one being installed/removed the Status row
	// is the live spinner, not a static installed/not-installed word
	if m.plugBusyAction != "" && m.plugBusySpec == "npm:"+e.Name {
		lines = append(lines, "  "+toolStyle.Render(Fit("Status", lw))+" "+
			warnStyle.Render(Fit(m.pluginBusyFrame()+" "+m.pluginBusyRowDesc(), valW)))
	} else {
		lines = append(lines, "  "+toolStyle.Render(Fit("Status", lw))+" "+style.Render(Fit(status, valW)))
	}
	// Repository: the GitHub URL the star count came from, wrapped on
	// segment boundaries (the value column is too narrow for a full URL).
	if e.Repo != "" {
		lines = append(lines, "  "+toolStyle.Render(Fit("Repository", lw)))
		for _, ln := range wrapURL("https://github.com/"+e.Repo, w-2) {
			lines = append(lines, "  "+codeStyle.Render(Fit(ln, w-2)))
		}
	}
	lines = append(lines, "  "+toolStyle.Render(Fit("Description", w-2)))
	if strings.TrimSpace(e.Desc) == "" {
		lines = append(lines, "  "+statusBarStyle.Render(Fit("—", w-2)))
	} else {
		for _, ln := range wrapWords(e.Desc, w-2) {
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(cText).Render(Fit(ln, w-2)))
		}
	}
	hint := "Enter: install via pi install"
	if installed {
		hint = "already installed"
	}
	lines = append(lines, "  "+toolStyle.Render(Fit(hint, w-2)))
	return lines
}

// wrapURL folds a URL into lines of at most n cells, breaking only after
// "/" so a long path splits on segment boundaries and every line still
// reads as the same link. Plain strings only — style after.
func wrapURL(s string, n int) []string {
	if n < 10 {
		n = 10
	}
	var out []string
	cur := ""
	// keep each segment with its trailing slash: "https://",
	// "github.com/", "owner/", "repo"
	for _, seg := range splitAfter(s, '/') {
		if cur == "" {
			cur = seg
			continue
		}
		if lipgloss.Width(cur+seg) > n {
			out = append(out, cur)
			cur = seg
			continue
		}
		cur += seg
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// splitAfter cuts s after every sep, so the separators stay attached to the
// piece before them.
func splitAfter(s string, sep rune) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == sep {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// wrapWords folds s into lines of at most n display cells (word-boundary,
// hard-splits one overlong word). Plain strings only — style after.
func wrapWords(s string, n int) []string {
	if n < 10 {
		n = 10
	}
	var out []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curW = 0
		}
	}
	for _, word := range strings.Fields(s) {
		ww := lipgloss.Width(word)
		if ww > n {
			// fallback: rune-slice the long word into n-wide pieces
			runes := []rune(word)
			for len(runes) > 0 {
				acc, accW := 0, 0
				for acc < len(runes) && accW+lipgloss.Width(string(runes[acc])) <= n {
					accW += lipgloss.Width(string(runes[acc]))
					acc++
				}
				if acc == 0 {
					acc = 1
				}
				out = append(out, string(runes[:acc]))
				runes = runes[acc:]
			}
			continue
		}
		add := ww
		if curW > 0 {
			add++ // space
		}
		if curW+add > n {
			flush()
		}
		if curW > 0 {
			cur.WriteByte(' ')
			curW++
		}
		cur.WriteString(word)
		curW += ww
	}
	flush()
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// psecCursor resolves the highlighted right-pane row to its option index
// (-1 when the list is empty or the cursor is out of range).
// psecCursor is the option index under the hub's right-pane cursor, or -1.
func psecCursor(d *Dialog) int {
	if len(d.FIdx) == 0 || d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return -1
	}
	return d.FIdx[d.Cursor]
}

// hubHead is one right-pane column header. The focused column carries
// the pointer and the highlight, the other stays dim, so the active
// column is obvious before the pointer reaches a row.
func hubHead(label string, n, w int, focused bool) string {
	mark, style := "  ", toolStyle
	if focused {
		mark, style = "▸ ", sideTitleStyle
	}
	return mark + style.Width(w-2).Render(fmt.Sprintf("%s · %d", label, n))
}

// isBgRow reports whether one option row is a background choice rather
// than a palette. The payload decides, so the two columns can never
// disagree with the label.
func isBgRow(d *Dialog, ri int) bool {
	return strings.HasPrefix(payloadOf(d, ri), psecActBg)
}

// hubStepRow moves the hub cursor one row, staying inside the column it
// is on (the Theme section has a palette column and a background
// column). Wraps around, like the single-column walk it replaces.
func hubStepRow(d *Dialog, down bool) int {
	n := len(d.FIdx)
	if n == 0 {
		return 0
	}
	want := func(int) bool { return true }
	if d.CurPsec() == PsecTheme {
		want = func(ri int) bool { return isBgRow(d, ri) == d.BgFocus }
	}
	step := 1
	if !down {
		step = -1
	}
	cur := d.Cursor % n
	for k := 1; k <= n; k++ {
		if j := ((cur+step*k)%n + n) % n; want(d.FIdx[j]) {
			return j
		}
	}
	return cur
}

// rememberColRow stores where the cursor sits in the Theme column it is
// on. d.Cursor is one shared pointer into FIdx, so without this each
// column forgets its row as soon as you browse the other one.
func rememberColRow(d *Dialog) {
	if d.CurPsec() != PsecTheme {
		return
	}
	if ri := psecCursor(d); ri >= 0 {
		if d.BgFocus {
			d.BgRow = ri
		} else {
			d.PalRow = ri
		}
	}
}

// gotoCol focuses one Theme column on the row it was last on (its first
// row on a first visit, or when the filter has hidden that row).
func gotoCol(d *Dialog, bg bool) {
	ri := d.PalRow
	if bg {
		ri = d.BgRow
	}
	d.BgFocus = bg
	if i := colFIdxIndex(d, ri); i >= 0 {
		d.Cursor = i
		return
	}
	palettes, backgrounds := themeCols(d)
	rows := palettes
	if bg {
		rows = backgrounds
	}
	for _, first := range rows {
		if i := colFIdxIndex(d, first); i >= 0 {
			d.Cursor = i
			return
		}
	}
	if len(d.FIdx) > 0 {
		d.Cursor = 0
	}
}

// colFIdxIndex is the FIdx position of one Theme-column row (an option
// index), or -1 when the filter has hidden it.
func colFIdxIndex(d *Dialog, ri int) int {
	if ri < 0 {
		return -1
	}
	for i, x := range d.FIdx {
		if x == ri {
			return i
		}
	}
	return -1
}

// themeCols splits the Theme section's filtered rows into its two
// columns: the palettes first, then the background set.
func themeCols(d *Dialog) (palettes, backgrounds []int) {
	for _, ri := range d.FIdx {
		if isBgRow(d, ri) {
			backgrounds = append(backgrounds, ri)
		} else {
			palettes = append(palettes, ri)
		}
	}
	return palettes, backgrounds
}

// hubRows builds one Theme-section column: the rows at idxs, windowed
// around the cursor row, rendered listW cells wide and padded to the
// hub's window height. bg renders the background look (colour swatch +
// slug + ✓ on the picked one), otherwise a palette row with its desc.
//
// The cursor shows in both columns, but only the focused one is
// highlighted: that is what tells the two columns apart while browsing.
func (m Model) hubRows(d *Dialog, idxs []int, curRi, listW, win int, bg, focused bool) []string {
	pos := 0
	for i, ri := range idxs {
		if ri == curRi {
			pos = i
			break
		}
	}
	start, end, above, below := fixedWin(pos, len(idxs), win)
	var lines []string
	if above {
		lines = append(lines, "  "+toolStyle.Width(listW-2).Render(fmt.Sprintf("…(+%d above)", start)))
	}
	optW := 28
	if listW-10 < optW {
		optW = listW - 10
	}
	if optW < 10 {
		optW = 10
	}
	for i := start; i < end; i++ {
		ri := idxs[i]
		mark, style := "  ", statusBarStyle
		if ri == curRi {
			mark = "▸ "
			if focused {
				style = rowHiStyle
			} else {
				style = lipgloss.NewStyle().Foreground(cMuted)
			}
		}
		var row string
		if bg {
			// background rows use the whole column: the slug must not
			// truncate before the swatch and the ✓
			row = m.bgRow(d.Options[ri], payloadOf(d, ri) == psecActBg+m.currentBackground(), listW-4)
		} else {
			row = Fit(Short(d.Options[ri], optW), optW)
			if desc := DescOf(d, ri); desc != "" {
				row += "  " + psecDesc(payloadOf(d, ri), desc, listW-4-optW-3)
			}
		}
		lines = append(lines, mark+style.Width(listW-2).Render(row))
	}
	if below {
		lines = append(lines, "  "+toolStyle.Width(listW-2).Render(fmt.Sprintf("…(+%d below)", len(idxs)-end)))
	}
	if len(idxs) == 0 {
		lines = append(lines, "  "+toolStyle.Width(listW-2).Render("— no match —"))
	}
	for len(lines) < win {
		lines = append(lines, "  "+statusBarStyle.Width(listW-2).Render(""))
	}
	return lines
}

// bgRow is one background row: a two-cell swatch in the surface colour
// (unpainted for transparent, which is the terminal's own), the slug,
// and ✓ on the picked one.
func (m Model) bgRow(name string, cur bool, w int) string {
	sw := lipgloss.NewStyle().Render("  ")
	if hex := theme.BackgroundHex(name, theme.Get(m.currentTheme()).Light); hex != "" {
		sw = lipgloss.NewStyle().Background(lipgloss.Color(hex)).Render("  ")
	}
	mark := ""
	if cur {
		mark = " ✓"
	}
	nameW := w - lipgloss.Width(sw) - 1 - lipgloss.Width(mark)
	if nameW < 4 {
		nameW = 4
	}
	return sw + " " + Fit(Short(name, nameW), nameW) + mark
}

// payloadOf parallels DescOf for the right pane's per-row payload.
func payloadOf(d *Dialog, ri int) string {
	if ri < len(d.Payload) {
		return d.Payload[ri]
	}
	return ""
}

// sideStateStyle is the sidebar state word's color: shown renders bright,
// hidden stays dark like the hint.
func sideStateStyle(state string) lipgloss.Style {
	if state == "shown" {
		return lipgloss.NewStyle().Foreground(cText)
	}
	return toolStyle
}

// psecDesc renders one right-pane description, truncated to maxW. Sidebar
// rows lead with their state word (shown bright, hidden dark). Styling
// happens after truncation so no ANSI sequence can be cut in half
// (Fit/Short require unstyled input).
func psecDesc(payload, desc string, maxW int) string {
	desc = Short(desc, maxW)
	if !strings.HasPrefix(payload, "side:") {
		return toolStyle.Render("— " + desc)
	}
	state, rest := desc, ""
	if i := strings.Index(desc, " "); i >= 0 {
		state, rest = desc[:i], desc[i:]
	}
	return toolStyle.Render("— ") + sideStateStyle(state).Render(state) + toolStyle.Render(rest)
}

// shortcutSuffix renders the assigned Alt-shortcut for a /command row
// (" · ⌥X", "" when none).
func (m *Model) shortcutSuffix(cmd string) string {
	if l := m.shortcutForCmd(cmd); l != "" {
		return " · " + shortcutDisplay(l)
	}
	return ""
}

// shortcutTarget resolves the highlighted right-pane row to its assignable
// /command (command/skill/prompt/extension rows, and the custom Alt rows
// of the Shortcuts section; "" otherwise).
func shortcutTarget(d *Dialog, ri int) string {
	switch d.CurPsec() {
	case PsecCmd, PsecSkill, PsecPrompt, PsecExt, PsecKeys:
	default:
		return ""
	}
	return payloadOf(d, ri)
}

// extTag shortens an extension source ("npm:pi-subagents" → "pi-subagents")
// for the row suffix.
func extTag(c pirpc.RepoCommand) string {
	if c.SourceInfo == nil {
		return ""
	}
	s := strings.TrimSpace(c.SourceInfo.Source)
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "/") {
		return s[i+1:]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func shortDesc(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// toolStat is one per-tool aggregate for the Tools section.
type toolStat struct {
	name string
	desc string
}

// toolStats aggregates the transcript's tool blocks by name (done/running/
// error split). Reads m.blocks directly — same source the chat renders.
func toolStats(m *Model) []toolStat {
	type agg struct {
		n, done, run, err int
		lastArgs          string
	}
	byName := map[string]*agg{}
	var order []string
	for _, b := range m.blocks {
		if b.Kind != "tool" || b.ToolName == "" {
			continue
		}
		a, ok := byName[b.ToolName]
		if !ok {
			a = &agg{}
			byName[b.ToolName] = a
			order = append(order, b.ToolName)
		}
		a.n++
		switch b.ToolStatus {
		case "done":
			a.done++
		case "error":
			a.err++
		default:
			a.run++
		}
		if b.ToolArgs != "" {
			a.lastArgs = b.ToolArgs
		}
	}
	sort.Strings(order)
	out := make([]toolStat, 0, len(order))
	for _, n := range order {
		a := byName[n]
		var parts []string
		parts = append(parts, fmt.Sprintf("%dx", a.n))
		if a.done > 0 {
			parts = append(parts, fmt.Sprintf("%d done", a.done))
		}
		if a.run > 0 {
			parts = append(parts, fmt.Sprintf("%d running", a.run))
		}
		if a.err > 0 {
			parts = append(parts, fmt.Sprintf("%d error", a.err))
		}
		d := strings.Join(parts, " · ")
		if a.lastArgs != "" {
			d += " — " + shortDesc(a.lastArgs, 32)
		}
		out = append(out, toolStat{name: n, desc: d})
	}
	return out
}

// FillCommand closes every dialog and stages a slash command in the input
// (palette parity: user reviews, then Enter sends).
func (m *Model) FillCommand(name string) {
	m.Dialogs = nil
	m.ta.SetValue("/" + name + " ")
	m.refreshPiTasks()
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
}

// isFilterKind reports pickers whose typing filters the list (generic
// updateDialog path).
func isFilterKind(kind string) bool {
	switch kind {
	case "model", "thinking", "sessions", "login", "logout", "trajectory", "tree", "settings", "subagents", "notification", "fork", "pet", "mcp", suggestAddKind:
		return true
	}
	return false
}

// updatePconfigDialog navigates the two-pane hub: ↑↓ moves in the focused
// pane (moving sections reloads the right pane), ←/→/Tab switches pane,
// typing filters the right pane, Enter on the left focuses the right,
// Enter on the right runs (confirm lives in src/builtin).
func (m Model) updatePconfigDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
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
				m.LoadPsecRows(d)
				// First visit to an unloaded marketplace fetches it in
				// the background (rows reload when MarketMsg lands).
				if d.CurPsec() == PsecMarket {
					// first visit: fetch a stale page, then let the
					// browse head order itself by stars. The sort is
					// built first so it claims the head before the
					// window hydration latches those repos busy.
					sortCmd := m.marketSortCmd("")
					hyd := m.hydrateMarketStarsCmd(d)
					if !marketFresh() && !marketInflight {
						return m, tea.Batch(m.fetchMarketCmd(), sortCmd, hyd)
					}
					return m, tea.Batch(sortCmd, hyd)
				}
			}
		} else if n := len(d.FIdx); n > 0 {
			if down {
				d.Cursor = hubStepRow(d, true)
			} else {
				d.Cursor = hubStepRow(d, false)
			}
			rememberColRow(d)
			// Theme rows are the one hub section that previews while
			// browsing, so ↑↓ repaints in the new palette or surface.
			m.previewRow(d)
			if d.CurPsec() == PsecTheme {
				m.refreshHubSection(PsecTheme)
			}
			// The marketplace hydrates GitHub stars for whatever the
			// moved cursor brought into view (never from render).
			if d.CurPsec() == PsecMarket {
				return m, m.hydrateMarketStarsCmd(d)
			}
		}
		return m, nil
	case tea.KeyLeft:
		// In the Theme section the right pane has two columns: ← walks
		// back out of the background column onto the row the palettes
		// were left on, then to the sections.
		if !d.ProvFocus && d.CurPsec() == PsecTheme && d.BgFocus {
			gotoCol(d, false)
			return m, nil
		}
		d.ProvFocus = true
		return m, nil
	case tea.KeyRight:
		if !d.ProvFocus && d.CurPsec() == PsecTheme && !d.BgFocus {
			gotoCol(d, true)
			return m, nil
		}
		d.ProvFocus = false
		return m, nil
	case tea.KeyTab:
		// Theme section: Tab cycles sections → palettes → backgrounds.
		if !d.ProvFocus && d.CurPsec() == PsecTheme {
			gotoCol(d, !d.BgFocus)
			return m, nil
		}
		d.BgFocus = false
		d.ProvFocus = !d.ProvFocus
		return m, nil
	case tea.KeyCtrlF:
		// Suggest your own plugin (Plugins tab only): a free-text prompt
		// over the hub, persisted to prefs.json when it submits.
		if d.CurPsec() == PsecPlugin {
			m.openSuggestAdd()
			return m, nil
		}
		return m, nil
	case tea.KeyCtrlS:
		// Assign an Alt-shortcut to the highlighted /command (hub stays
		// underneath the capture dialog; rows reload on close).
		if !d.ProvFocus {
			if ri := psecCursor(d); ri >= 0 {
				if cmd := shortcutTarget(d, ri); cmd != "" {
					m.openShortcutCapture(cmd)
					return m, nil
				}
			}
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if km.Type == tea.KeyBackspace && d.Filter != "" {
			d.Filter = d.Filter[:len(d.Filter)-1]
			d.Reindex()
			// marketplace typing is a remote search: re-arm the debounce
			return m, m.marketSearchTick()
		}
		// Empty filter (forward-delete always): Delete removes the
		// highlighted plugin (sessions/login parity: ⌫ on empty
		// filter deletes). Other sections ignore it.
		if d.CurPsec() == PsecPlugin && !d.ProvFocus {
			if ri := psecCursor(d); ri >= 0 && ri < len(m.Plugins) {
				spec := m.Plugins[ri].Spec
				// one plugin op at a time: pi remove is not safe to
				// double-fire while an install/uninstall is running
				if _, busy := m.PluginBusy(); busy != "" {
					// names the op that is running, not the one refused
					m.Status = m.PluginBusyMsg()
					m.Refresh()
					return m, nil
				}
				if !m.ConfirmPluginOp("remove", spec) {
					return m, nil // first Delete arms the auth gate
				}
				return m, m.StartPluginOp("remove", spec)
			}
		}
		return m, nil
	case tea.KeyEsc:
		// Esc in the marketplace clears the search first (one level,
		// like every other filter dialog); a second Esc closes.
		if d.CurPsec() == PsecMarket && d.Filter != "" {
			d.Filter = ""
			d.Reindex()
			m.MarketErr = ""
			if entries, more, fresh := marketCached(""); fresh {
				m.Market, marketMore = entries, more
				m.Status = "ready"
				m.refreshHubSection(PsecMarket)
				m.Refresh()
				return m, tea.Batch(m.marketSortCmd(""), m.hydrateMarketStarsCmd(d))
			}
			m.Refresh()
			return m, tea.Batch(m.fetchMarketCmd(), m.marketSortCmd(""), m.hydrateMarketStarsCmd(d))
		}
		m.Dialogs = m.Dialogs[1:]
		m.refreshPiTasks()
		m.Refresh()
		return m, m.ReconcileTurnCmd()
	case tea.KeyEnter:
		if d.ProvFocus {
			d.ProvFocus = false
			return m, nil
		}
		return m.confirmDialog(d)
	}
	if km.Type == tea.KeyRunes {
		d.Filter += km.String()
		d.Reindex()
		// marketplace typing debounces into a remote npm search
		return m, m.marketSearchTick()
	}
	return m, nil
}

// updatePconfigWheel scrolls the focused pane (wheel down = next row):
// sections when the left pane has focus (moving reloads the right pane,
// like ↑↓), contents otherwise. Lets mouse users scroll the hub without
// touching the chat/sidebar behind it.
func (m Model) updatePconfigWheel(d *Dialog, down bool) (tea.Model, tea.Cmd) {
	t := tea.KeyDown
	if !down {
		t = tea.KeyUp
	}
	return m.updatePconfigDialog(tea.KeyMsg{Type: t}, d)
}

// renderPconfigDialog draws the two-pane hub: left = sections with counts,
// right = the selected section's rows. The Plugins section adds a third
// DETAILS column on wide terminals (like the /model picker). Layout math
// mirrors the /model picker (fixed scroll windows so the box never
// resizes while scrolling).
func (m Model) renderPconfigDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	// the marketplace filter is a remote npm search, not a local filter
	filterLabel := "filter: "
	if d.CurPsec() == PsecMarket {
		filterLabel = "search: "
	}
	b.WriteString(statusBarStyle.Render(filterLabel+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := hubBoxW(&m)
	leftW := 30
	if boxW < 100 {
		leftW = 24
	}
	rightW := boxW - 8 - leftW - 3
	if rightW < 30 {
		rightW = 30
	}
	// Plugins and Marketplace get a third DETAILS column (oh-my-pi
	// style, like the /model picker): list + specs side by side. Narrow
	// terminals keep the classic two panes (spec lives in the row desc).
	detW := 42
	isPlugin := d.CurPsec() == PsecPlugin && len(m.Plugins) > 0
	isMarket := d.CurPsec() == PsecMarket && len(m.Market) > 0
	detailCol := boxW >= 110 && (isPlugin || isMarket)
	// Theme section: a second column of backgrounds next to the
	// palettes. Same width rule as the DETAILS column.
	bgW := 30
	bgCol := boxW >= 110 && d.CurPsec() == PsecTheme
	listW := rightW
	if detailCol {
		listW = rightW - detW - 3
		if listW < 20 {
			listW = 20
			detW = rightW - listW - 3
		}
	}
	if bgCol {
		listW = rightW - bgW - 3
		if listW < 20 {
			listW = 20
			bgW = rightW - listW - 3
		}
	}
	win := hubWindow(&m)

	// left window (sections): label + count badge.
	ptotal := len(d.Provs)
	pstart, pend, pAbove, pBelow := fixedWin(d.ProvCursor, ptotal, win)
	var leftLines []string
	if pAbove {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d above)", pstart)))
	}
	for pi := pstart; pi < pend; pi++ {
		cw := leftW - 2
		nm := Short(d.Provs[pi], cw-5)
		cnt := ""
		if pi < len(d.PsecIDs) {
			if n := psecCount(&m, d.PsecIDs[pi]); n >= 0 {
				cnt = fmt.Sprintf("%d", n)
			}
		}
		pad := cw - 2 - lipgloss.Width(nm) - len(cnt)
		if pad < 1 {
			pad = 1
		}
		content := Fit(nm+strings.Repeat(" ", pad)+cnt, cw-2)
		mark := "  "
		style := statusBarStyle
		if pi == d.ProvCursor {
			mark = "▸ "
			if d.ProvFocus {
				style = rowHiStyle
			} else {
				style = lipgloss.NewStyle().Foreground(cText)
			}
		}
		leftLines = append(leftLines, mark+style.Width(leftW-2).Render(content))
	}
	if pBelow {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d below)", ptotal-pend)))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Width(leftW-2).Render(""))
	}

	// right window (section rows): same fixed-win rule as the left pane.
	// In plugin detail mode the middle column is name-only — the spec
	// lives in the DETAILS column (like the /model picker's wide layout).
	var rightLines, bgLines []string
	bgCount := 0
	total := len(d.FIdx)
	if bgCol {
		// Theme section: the palettes and the background set each get
		// their own window over their own rows, one shared cursor.
		palRows, bgRows := themeCols(d)
		curRi := psecCursor(d)
		rightLines = m.hubRows(d, palRows, curRi, listW, win, false, !d.BgFocus)
		bgLines = m.hubRows(d, bgRows, curRi, bgW, win, true, d.BgFocus)
		total, bgCount = len(palRows), len(bgRows)
	} else {
		start, end, rAbove, rBelow := fixedWin(d.Cursor, total, win)
		if rAbove {
			rightLines = append(rightLines, "  "+toolStyle.Width(listW-2).Render(fmt.Sprintf("…(+%d above)", start)))
		}
		optW := 28
		if listW-10 < optW {
			optW = listW - 10
		}
		if optW < 10 {
			optW = 10
		}
		if detailCol {
			optW = listW - 4
			if optW < 10 {
				optW = 10
			}
		}
		for fi := start; fi < end; fi++ {
			ri := d.FIdx[fi]
			mark := "  "
			style := statusBarStyle
			if fi == d.Cursor {
				mark = "▸ "
				if d.ProvFocus {
					style = lipgloss.NewStyle().Foreground(cText)
				} else {
					style = rowHiStyle
				}
			}
			row := Fit(Short(d.Options[ri], optW), optW)
			switch {
			case !detailCol:
				if desc := DescOf(d, ri); desc != "" {
					row += "  " + psecDesc(payloadOf(d, ri), desc, listW-4-optW-3)
				}
			case isMarket:
				// the wide layout drops the desc column, so the star count
				// rides the row itself ("" while still unknown)
				chip := ""
				if ri >= 0 && ri < len(m.Market) {
					chip = marketChip(m.Market[ri])
				}
				row = marketRow(d.Options[ri], chip, optW)
			}
			rightLines = append(rightLines, mark+style.Width(listW-2).Render(row))
		}
		if rBelow {
			rightLines = append(rightLines, "  "+toolStyle.Width(listW-2).Render(fmt.Sprintf("…(+%d below)", total-end)))
		}
		if total == 0 {
			rightLines = append(rightLines, "  "+toolStyle.Width(listW-2).Render("— no match —"))
		}
		for len(rightLines) < win {
			rightLines = append(rightLines, "  "+statusBarStyle.Width(listW-2).Render(""))
		}
	}

	secName := d.CurPsec()
	if d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) {
		secName = d.Provs[d.ProvCursor]
	}
	sep := sepStyle.Render("│")
	if bgCol {
		b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("SECTIONS") + " │ " +
			hubHead(strings.ToUpper(secName), total, listW, !d.BgFocus && !d.ProvFocus) + " │ " +
			hubHead("BACKGROUND", bgCount, bgW, d.BgFocus && !d.ProvFocus) + "\n")

		n := len(leftLines)
		if len(rightLines) > n {
			n = len(rightLines)
		}
		if len(bgLines) > n {
			n = len(bgLines)
		}
		for i := 0; i < n; i++ {
			l, r, bg := "", "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			} else {
				l = "  " + statusBarStyle.Width(leftW-2).Render("")
			}
			if i < len(rightLines) {
				r = rightLines[i]
			} else {
				r = "  " + statusBarStyle.Width(listW-2).Render("")
			}
			if i < len(bgLines) {
				bg = bgLines[i]
			} else {
				bg = "  " + statusBarStyle.Width(bgW-2).Render("")
			}
			b.WriteString(l + " " + sep + " " + r + " " + sep + " " + bg + "\n")
		}
	} else if !detailCol {
		b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("SECTIONS") + " │ " +
			"  " + sideTitleStyle.Width(listW-2).Render(strings.ToUpper(secName)+" · "+fmt.Sprintf("%d", total)) + "\n")

		n := len(leftLines)
		if len(rightLines) > n {
			n = len(rightLines)
		}
		for i := 0; i < n; i++ {
			l, r := "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			} else {
				l = "  " + statusBarStyle.Width(leftW-2).Render("")
			}
			if i < len(rightLines) {
				r = rightLines[i]
			} else {
				r = "  " + statusBarStyle.Width(listW-2).Render("")
			}
			b.WriteString(l + " " + sep + " " + r + "\n")
		}
	} else {
		b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("SECTIONS") + " │ " +
			"  " + sideTitleStyle.Width(listW-2).Render(strings.ToUpper(secName)+" · "+fmt.Sprintf("%d", total)) + " │ " +
			"  " + sideTitleStyle.Width(detW-2).Render("DETAILS") + "\n")

		// Fixed box height: the detail column never stretches the
		// dialog — overflow folds into a "…(+N more)" marker, like the
		// scroll markers of the other two panes.
		var detLines []string
		if isMarket {
			detLines = marketDetailLines(&m, d, detW)
		} else {
			detLines = pluginDetailLines(&m, d, detW)
		}
		if len(detLines) > win {
			detLines = append(detLines[:win-1],
				"  "+toolStyle.Width(detW-2).Render(fmt.Sprintf("…(+%d more)", len(detLines)-win+1)))
		}
		for len(detLines) < win {
			detLines = append(detLines, "  "+statusBarStyle.Width(detW-2).Render(""))
		}
		n := len(leftLines)
		if len(rightLines) > n {
			n = len(rightLines)
		}
		if len(detLines) > n {
			n = len(detLines)
		}
		for i := 0; i < n; i++ {
			l, r, dt := "", "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			} else {
				l = "  " + statusBarStyle.Width(leftW-2).Render("")
			}
			if i < len(rightLines) {
				r = rightLines[i]
			} else {
				r = "  " + statusBarStyle.Width(listW-2).Render("")
			}
			if i < len(detLines) {
				dt = detLines[i]
			} else {
				dt = "  " + statusBarStyle.Width(detW-2).Render("")
			}
			b.WriteString(l + " " + sep + " " + r + " " + sep + " " + dt + "\n")
		}
	}

	foot := "↑↓ sections · → contents · Enter open · Esc close"
	if !d.ProvFocus {
		foot = "↑↓ select · ← sections · Tab switch · type filters · Enter run · Esc close"
		if d.CurPsec() == PsecMarket {
			// the filter is a remote npm search, not a local one
			foot = "↑↓ select · ← sections · Tab switch · type to search npm · Enter install · Esc clears"
		}
		if bgCol {
			foot = "↑↓ previews · ←/→ switch column · Enter applies · type filters · Esc close"
		}
	}
	b.WriteString("\n" + toolStyle.Render(foot))
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
