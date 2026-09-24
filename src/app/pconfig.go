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
// classic single dialog, "" is info-only (toast hint on Enter).
const (
	psecActAgent = "@agent"
	psecActTheme = "@theme"
	psecActLogin = "@login"
)

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
		Provs:     []string{"Agent", "Skills", "Prompts", "Extensions", "Plugins", "Marketplace", "MCP", "Tools", "Tasks", "Sidebar", "Theme", "Login"},
		PsecIDs:   []string{PsecAgent, PsecSkill, PsecPrompt, PsecExt, PsecPlugin, PsecMarket, PsecMCP, PsecTool, PsecTasks, PsecSide, PsecTheme, PsecLogin},
		ProvFocus: true}
	m.LoadPsecRows(d)
	m.Dialogs = append(m.Dialogs, d)
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
	m.OpenPconfig()
	if n := len(m.Dialogs); n > 0 {
		if hd := m.Dialogs[n-1]; hd.Kind == "pconfig" {
			for i, id := range hd.PsecIDs {
				if id == PsecTasks {
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
		msg = "Enter opens the theme picker · Esc closes"
		return []string{"Open theme picker →"},
			[]string{"switch TUI theme"},
			[]string{psecActTheme}, msg
	case PsecLogin:
		msg = "Enter opens login · Esc closes"
		return []string{"Open login →"},
			[]string{"providers · keys · OAuth"},
			[]string{psecActLogin}, msg
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
		msg = "Delete uninstalls the plugin (pi remove) · Marketplace installs new ones · Esc closes"
		for _, p := range m.Plugins {
			opts = append(opts, p.Name)
			descs = append(descs, p.Spec)
			payload = append(payload, "")
		}
		if len(opts) == 0 {
			opts = []string{"— no packages —"}
			descs = []string{"Marketplace installs one"}
			payload = []string{""}
		}
	case PsecMarket:
		msg = fmt.Sprintf("showing %d of %d · Enter installs (pi install) · type filters · Esc closes",
			len(m.Market), marketTotal)
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
			opts = append(opts, e.Name)
			if marketInstalled(m, e.Name) {
				descs = append(descs, "✓ installed · "+shortDesc(ver+" "+e.Desc, 44))
				payload = append(payload, "")
			} else {
				descs = append(descs, shortDesc(strings.TrimSpace(ver+" — "+e.Desc), 48))
				payload = append(payload, "market:"+e.Name)
			}
		}
		if len(opts) == 0 {
			opts = []string{"— empty market —"}
			descs = []string{"fetch failed or registry has no pi packages"}
			payload = []string{""}
		} else if marketTotal > len(m.Market) {
			opts = append(opts, fmt.Sprintf("… load more (%d/%d) …", len(m.Market), marketTotal))
			descs = append(descs, "Enter loads the next 100")
			payload = append(payload, "marketmore")
		}
	case PsecMCP:
		msg = "Enter on a connected server fills /mcp · disabled ones change via mcp.json + /reload · Esc closes"
		for _, s := range m.MCP {
			switch {
			case s.Disabled:
				opts = append(opts, s.Name)
				descs = append(descs, "disabled in mcp.json")
				payload = append(payload, "")
			case s.Connected:
				opts = append(opts, s.Name)
				descs = append(descs, fmt.Sprintf("● %d/%d direct · ~%s tok", s.Direct, s.Total, fmtComma(s.Tokens)))
				payload = append(payload, "mcp")
			default:
				opts = append(opts, s.Name)
				descs = append(descs, "○ not connected")
				payload = append(payload, "")
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
	rows := [][2]string{
		{"Version", orDash(e.Version)},
		{"Spec", "npm:" + e.Name},
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
	lines = append(lines, "  "+toolStyle.Render(Fit("Status", lw))+" "+style.Render(Fit(status, valW)))
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
func psecCursor(d *Dialog) int {
	if len(d.FIdx) == 0 || d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return -1
	}
	return d.FIdx[d.Cursor]
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
// /command (skill/prompt/extension runnable rows only, "" otherwise).
func shortcutTarget(d *Dialog, ri int) string {
	switch d.CurPsec() {
	case PsecSkill, PsecPrompt, PsecExt:
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
	case "model", "thinking", "sessions", "login", "logout", "shortcuts", "trajectory":
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
				if d.CurPsec() == PsecMarket && !marketFresh() && !marketInflight {
					return m, m.fetchMarketCmd()
				}
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
		d.ProvFocus = true
		return m, nil
	case tea.KeyRight:
		d.ProvFocus = false
		return m, nil
	case tea.KeyTab:
		d.ProvFocus = !d.ProvFocus
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
			return m, nil
		}
		// Empty filter (forward-delete always): Delete removes the
		// highlighted plugin (sessions/login parity: ⌫ on empty
		// filter deletes). Other sections ignore it.
		if d.CurPsec() == PsecPlugin && !d.ProvFocus {
			if ri := psecCursor(d); ri >= 0 && ri < len(m.Plugins) {
				spec := m.Plugins[ri].Spec
				m.Status = "removing " + spec + "…"
				m.Refresh()
				return m, m.ChangePluginCmd("remove", spec)
			}
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
			return m, nil
		}
		return m.confirmDialog(d)
	}
	if km.Type == tea.KeyRunes {
		d.Filter += km.String()
		d.Reindex()
		return m, nil
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
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 150 {
		boxW = 150
	}
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
	listW := rightW
	if detailCol {
		listW = rightW - detW - 3
		if listW < 20 {
			listW = 20
			detW = rightW - listW - 3
		}
	}
	win := m.winH - 14
	if win < 12 {
		win = 12
	}
	if win > 24 {
		win = 24
	}

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
	total := len(d.FIdx)
	start, end, rAbove, rBelow := fixedWin(d.Cursor, total, win)
	var rightLines []string
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
		if !detailCol {
			if desc := DescOf(d, ri); desc != "" {
				row += "  " + psecDesc(payloadOf(d, ri), desc, listW-4-optW-3)
			}
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

	secName := d.CurPsec()
	if d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) {
		secName = d.Provs[d.ProvCursor]
	}
	sep := sepStyle.Render("│")
	if !detailCol {
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
