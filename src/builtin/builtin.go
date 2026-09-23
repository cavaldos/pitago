package builtin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/components/palette"
	"pitago/src/pirpc"
)

func openLogin(m *app.Model, arg string) tea.Cmd {
	// Import pi → pitago first so logins added via stock `pi` (API keys
	// and OAuth) appear here (union, never overwrite). Opening /login is
	// the re-sync point; pitago also mirrors pi logins to pi_auth.json.
	pirpc.SyncFromPi(m.KeyPath)
	authPath := m.AuthPath
	if authPath == "" {
		authPath = pirpc.AuthStatePath()
	}
	pirpc.SyncAuthStateFromPi(authPath)
	// Two-pane picker: left = providers (API-key + OAuth-only + anything
	// pi knows), right = saved keys + auth actions.
	seen := map[string]bool{}
	var provs []string
	for _, p := range pirpc.AllLoginProviders() {
		if !seen[p] {
			seen[p] = true
			provs = append(provs, p)
		}
	}
	for _, e := range pirpc.ListPiAuth() {
		if !seen[e.Provider] {
			seen[e.Provider] = true
			provs = append(provs, e.Provider)
		}
	}
	store := pirpc.LoadStore(m.KeyPath)
	oauth := map[string]bool{}
	for _, e := range pirpc.ListPiAuth() {
		if e.Type == "oauth" {
			oauth[e.Provider] = true
		}
	}
	conn := map[string]bool{}
	counts := map[string]int{}
	for _, prov := range provs {
		if e, ok := store[pirpc.LookupEnv(prov)]; ok && e != nil && len(e.Keys) > 0 {
			conn[prov] = true
			counts[prov] = len(e.Keys)
		}
		if oauth[prov] {
			conn[prov] = true
		}
	}
	// Connected providers float above the rest (model-picker rule).
	sorted := append([]string(nil), provs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return conn[sorted[i]] && !conn[sorted[j]]
	})
	d := &app.Dialog{
		Kind: "login", Title: "Provider login",
		Message: "Left: providers · right: keys + auth. Enter uses/adds, ⌫ deletes, s shows/hides, r renames, ^P models. Stays open; pi syncs behind.",
		Provs:   sorted, ProvConn: conn, LoginCounts: counts, OAuthConn: oauth,
		ProvFocus: true,
	}
	d.Reindex()
	// Exact /login <provider> focuses that provider; otherwise filter.
	want := strings.TrimSpace(arg)
	if want != "" {
		for i, idx := range d.PIdx {
			if strings.EqualFold(sorted[idx], want) {
				d.ProvCursor = i
				break
			}
			// label/env match (e.g. /login openrouter, /login codex)
			if lbl := pirpc.ProviderLabel(sorted[idx]); strings.EqualFold(lbl, want) || strings.EqualFold(pirpc.LookupEnv(sorted[idx]), want) {
				d.ProvCursor = i
				break
			}
		}
		// no exact hit → filter text
		if d.SelLoginProv() == "" || !strings.EqualFold(d.SelLoginProv(), want) {
			matched := false
			for _, idx := range d.PIdx {
				if strings.EqualFold(sorted[idx], want) {
					matched = true
					break
				}
			}
			if !matched {
				d.Filter = want
				d.Reindex()
			}
		}
	}
	m.RefreshLoginKeys(d)
	d.KeyCursor = 0
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}

// openLoginMethod opens the method picker once a provider is chosen.

func openLoginMethod(m *app.Model, provider, env string) {
	d := &app.Dialog{
		Kind: "loginMethod", Title: "Login " + provider,
		Message:       "API key goes to pitago's private keystore (" + env + "), pi reconnects automatically. Do OAuth in stock pi.",
		Options:       []string{"Enter API key", "OAuth / subscription", "Logged in — reload"},
		Descs:         []string{"save key + reconnect pi", "guide", "refresh model list"},
		LoginProvider: provider, LoginEnv: env,
	}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
}

// openLogout opens the provider picker to delete the ACTIVE saved key or
// disconnect an OAuth subscription. Providers with several keys keep the
// rest (use /login to switch).

func openLogout(m *app.Model, arg string) tea.Cmd {
	keys := pirpc.LoadKeys(m.KeyPath)
	oauth := map[string]bool{}
	for _, e := range pirpc.ListPiAuth() {
		if e.Type == "oauth" {
			oauth[e.Provider] = true
		}
	}
	var opts, descs []string
	for _, p := range pirpc.AllLoginProviders() {
		env := pirpc.LookupEnv(p)
		_, hasKey := keys[env]
		if env == "" {
			hasKey = false
		}
		if !hasKey && !oauth[p] {
			continue
		}
		opts = append(opts, p)
		desc := pirpc.ProviderLabel(p)
		if env != "" {
			desc += " · " + env
		}
		if ks, active := pirpc.ListKeys(m.KeyPath, env); env != "" && len(ks) > 0 {
			desc += fmt.Sprintf(" · %d key", len(ks))
			if len(ks) > 1 {
				desc += "s"
			}
			if active >= 0 && active < len(ks) {
				desc += " · active " + pirpc.MaskKey(ks[active])
			}
		}
		if oauth[p] {
			desc += " · OAuth"
		}
		descs = append(descs, desc)
	}
	// providers pi knows but pitago doesn't (custom oauth) still logout-able
	for _, e := range pirpc.ListPiAuth() {
		found := false
		for _, o := range opts {
			if o == e.Provider {
				found = true
				break
			}
		}
		if found {
			continue
		}
		opts = append(opts, e.Provider)
		descs = append(descs, e.Provider+" · "+e.Type+" (pi only)")
	}
	if len(opts) == 0 {
		m.AddBlock(app.Block{Kind: "notice", Text: "nothing to remove — no keys or pi logins"})
		m.Refresh()
		return nil
	}
	for i, o := range opts {
		if strings.EqualFold(o, arg) {
			return doLogout(m, o, descs[i])
		}
	}
	d := &app.Dialog{Kind: "logout", Title: "Remove login", Message: "Pick a provider: deletes its ACTIVE key, or disconnects OAuth (pi too).", Options: opts, Descs: descs}
	if arg != "" {
		d.Filter = arg
	}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}

// doLogout deletes the ACTIVE key then reconnects pi. With several keys
// left it switches to the next one and stays logged in. Providers with no
// saved keys but a pi OAuth entry get disconnected instead.

func doLogout(m *app.Model, provider, desc string) tea.Cmd {
	env := pirpc.LookupEnv(provider)
	authPath := m.AuthPath
	if authPath == "" {
		authPath = pirpc.AuthStatePath()
	}
	if env == "" {
		return doOAuthLogout(m, provider, authPath)
	}
	keysBefore, active := pirpc.ListKeys(m.KeyPath, env)
	if active < 0 {
		// No saved keys: maybe an OAuth login in pi — disconnect it.
		if _, _, ok := pirpc.PiOAuth(provider); ok {
			return doOAuthLogout(m, provider, authPath)
		}
		m.AddBlock(app.Block{Kind: "notice", Text: "no saved keys for " + provider})
		m.Refresh()
		return nil
	}
	masked := ""
	if active >= 0 && active < len(keysBefore) {
		masked = pirpc.MaskKey(keysBefore[active])
	}
	if err := pirpc.DeleteKeyAt(m.KeyPath, env, active); err != nil {
		m.AddBlock(app.Block{Kind: "notice", Text: "failed to delete key: " + err.Error(), Err: true})
		m.Refresh()
		return nil
	}
	// Keep pi in sync (auth.json wins over env): last key removes pi's
	// entry, otherwise pi follows the new active key.
	pirpc.PushActiveToPi(m.KeyPath, env)
	keys, _ := pirpc.ListKeys(m.KeyPath, env)
	if len(keys) == 0 {
		m.AddBlock(app.Block{Kind: "notice", Text: "deleted key " + provider + " " + masked + " (last key) → pi — reconnecting…"})
	} else {
		m.AddBlock(app.Block{Kind: "notice", Text: fmt.Sprintf("deleted active key %s %s — switched to %s (%d left) → pi, reconnecting…",
			provider, masked, pirpc.MaskKey(keys[0]), len(keys))})
	}
	return m.RespawnPi()
}

// doOAuthLogout disconnects a pi subscription login: drops pi's OAuth
// entry + pitago's mirror, then reconnects.
func doOAuthLogout(m *app.Model, provider, authPath string) tea.Cmd {
	if err := pirpc.DeletePiAuth(provider); err != nil {
		m.AddBlock(app.Block{Kind: "notice", Text: "failed to disconnect " + provider + ": " + err.Error(), Err: true})
		m.Refresh()
		return nil
	}
	pirpc.ForgetAuthState(authPath, provider)
	pirpc.SyncAuthStateFromPi(authPath)
	m.AddBlock(app.Block{Kind: "notice", Text: "disconnected OAuth " + provider + " (pi + pitago) — reconnecting…"})
	m.Refresh()
	return m.RespawnPi()
}

// respawnPi kills the old pi and respawns keeping the same session (to pick up added/removed keys).

// loadSettings fetches live state + pi settings file + pitago prefs to
// build the settings dialog (pi parity: the first rows are live agent
// state, the rest mirror stock pi's settings menu).
func loadSettings(m *app.Model) tea.Cmd {
	return func() tea.Msg {
		sst, err := loadSettingsState(m)
		if err != nil {
			return app.SettingsMsg{Err: err}
		}
		opts, descs := settingsOptions(sst)
		return app.SettingsMsg{St: sst, Opts: opts, Descs: descs}
	}
}

// loadSettingsState reads RPC state, settings.json and local prefs.
func loadSettingsState(m *app.Model) (app.SettingsState, error) {
	var sst app.SettingsState
	st, err := m.Pi.GetState()
	if err != nil {
		return sst, err
	}
	if st.ThinkingLevel == "" {
		st.ThinkingLevel = "off"
	}
	cfg := pirpc.ReadPiSettings()
	sst = app.SettingsState{
		Steering:        app.OrDefault(st.SteeringMode, pirpc.PiString(cfg, "steeringMode", "one-at-a-time")),
		FollowUp:        app.OrDefault(st.FollowUpMode, pirpc.PiString(cfg, "followUpMode", "one-at-a-time")),
		AutoCompact:     st.AutoCompaction,
		AutoRetry:       m.AutoRetry,
		Thinking:        st.ThinkingLevel,
		Model:           m.ModelLbl,
		Theme:           app.OrDefault(m.ThemeName, "default"),
		Vals:            fileSettingVals(cfg),
		HideThinking:    m.HideThinking,
		AutocompleteMax: palette.Win,
	}
	return sst, nil
}

func onoff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// fileSetting is one settings.json-backed (or pitago-local) /settings row.
// vals are the display values to cycle; toVal converts back to the file
// value; local rows apply instantly without reconnecting pi.
type fileSetting struct {
	label string
	path  string // dotted settings.json path ("" = pitago-local)
	vals  []string
	toVal func(disp string) any
	local bool
}

var httpTimeoutVals = []string{"30 sec", "1 min", "2 min", "5 min", "disabled"}
var httpTimeoutMs = []int{30000, 60000, 120000, 300000, 0}

var trustVals = []string{"Ask", "Always trust", "Never trust"}
var trustKeys = []string{"ask", "always", "never"}

// fileSettings mirrors the applicable rows of stock pi's settings menu, in
// pi's order (image block first, like the screenshot). Skipped as pi-TUI-only
// or submenu: hardware cursor, editor/output padding, clear-on-shrink,
// terminal progress, tui-mode, fullscreen×3, double-escape, mermaid,
// changelog, tree filter, warnings + per-model thinking (submenus),
// telemetry UI (pitago has its own updater — the key is still writable via
// the file).
var fileSettings = []fileSetting{
	{label: "Skill commands", path: "enableSkillCommands", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Show images", path: "terminal.showImages", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Image width", path: "terminal.imageWidthCells", vals: []string{"60", "80", "120"},
		toVal: func(d string) any { return atoiOr(d, 60) }},
	{label: "Auto-resize images", path: "images.autoResize", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Block images", path: "images.blockImages", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Transport", path: "transport", vals: []string{"auto", "sse", "websocket", "websocket-cached"},
		toVal: func(d string) any { return d }},
	{label: "HTTP idle timeout", path: "httpIdleTimeoutMs", vals: httpTimeoutVals,
		toVal: func(d string) any {
			for i, l := range httpTimeoutVals {
				if l == d {
					return httpTimeoutMs[i]
				}
			}
			return 300000
		}},
	{label: "Cache warming", path: "cacheWarming", vals: []string{"off", "streaming", "idle"},
		toVal: func(d string) any { return d }},
	{label: "Hide thinking", path: "hideThinkingBlock", vals: []string{"on", "off"}, local: true,
		toVal: func(d string) any { return d == "on" }},
	{label: "Cache-miss notices", path: "showCacheMissNotices", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Default project trust", path: "defaultProjectTrust", vals: trustVals,
		toVal: func(d string) any {
			for i, l := range trustVals {
				if l == d {
					return trustKeys[i]
				}
			}
			return "ask"
		}},
	{label: "Quiet startup", path: "quietStartup", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Install telemetry", path: "enableInstallTelemetry", vals: []string{"on", "off"},
		toVal: func(d string) any { return d == "on" }},
	{label: "Autocomplete max", path: "", vals: []string{"3", "5", "7", "10", "15", "20"}, local: true,
		toVal: func(d string) any { return atoiOr(d, 10) }},
	{label: "Tree filter mode", path: "treeFilterMode", vals: []string{"default", "no-tools", "user-only", "labeled-only", "all"}, local: true,
		toVal: func(d string) any { return d }},
}

// fileSettingVals stringifies every file row's current value for display.
func fileSettingVals(cfg map[string]any) map[string]string {
	out := map[string]string{}
	for _, fr := range fileSettings {
		switch fr.path {
		case "terminal.imageWidthCells":
			out[fr.path] = itoa(pirpc.PiInt(cfg, fr.path, 60))
		case "httpIdleTimeoutMs":
			ms := pirpc.PiInt(cfg, fr.path, 300000)
			disp := fmt.Sprintf("%d ms", ms)
			for i, m := range httpTimeoutMs {
				if m == ms {
					disp = httpTimeoutVals[i]
				}
			}
			out[fr.path] = disp
		case "defaultProjectTrust":
			key := pirpc.PiString(cfg, fr.path, "ask")
			disp := key
			for i, k := range trustKeys {
				if k == key {
					disp = trustVals[i]
				}
			}
			out[fr.path] = disp
		case "transport":
			out[fr.path] = pirpc.PiString(cfg, fr.path, "auto")
		case "cacheWarming":
			out[fr.path] = pirpc.PiString(cfg, fr.path, "streaming")
		case "treeFilterMode":
			out[fr.path] = pirpc.PiString(cfg, fr.path, "default")
		default:
			out[fr.path] = onoff(pirpc.PiBool(cfg, fr.path, fileSettingDef(fr.path)))
		}
	}
	return out
}

// fileSettingDef is pi's default for each bool row (from pi's bundle).
func fileSettingDef(path string) bool {
	switch path {
	case "enableSkillCommands", "terminal.showImages", "images.autoResize":
		return true
	}
	return false
}

func atoiOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// nextVal cycles to the value after cur (wraps; unknown cur restarts at 0).
func nextVal(vals []string, cur string) string {
	for i, v := range vals {
		if v == cur {
			return vals[(i+1)%len(vals)]
		}
	}
	return vals[0]
}

func settingsOptions(st app.SettingsState) ([]string, []string) {
	opts := []string{
		"Model: " + st.Model,
		"Thinking: " + st.Thinking,
		"Steering: " + st.Steering,
		"Follow-up: " + st.FollowUp,
		"Auto-compact: " + onoff(st.AutoCompact),
		"Auto-retry: " + onoff(st.AutoRetry),
		"Theme: " + st.Theme,
	}
	descs := []string{
		"Enter: open model picker",
		"Enter: open thinking picker",
		"Enter: switch all/one-at-a-time",
		"Enter: switch all/one-at-a-time",
		"Enter: toggle",
		"Enter: toggle",
		"Enter: open theme picker",
	}
	for _, fr := range fileSettings {
		disp := st.Vals[fr.path]
		if fr.path == "" { // pitago-local rows
			if fr.label == "Hide thinking" {
				disp = onoff(st.HideThinking)
			} else {
				disp = itoa(st.AutocompleteMax)
			}
		}
		foot := "Enter: toggle"
		if len(fr.vals) > 2 {
			foot = "Enter: next"
		}
		if !fr.local {
			foot += " · reconnects pi"
		}
		opts = append(opts, fr.label+": "+disp)
		descs = append(descs, foot)
	}
	return opts, descs
}

// settingsAction handles Enter on each settings row.
func settingsAction(m *app.Model, ri int) (tea.Model, tea.Cmd) {
	if len(m.Dialogs) == 0 {
		return m, nil
	}
	d := m.Dialogs[0]
	st := d.Settings
	refresh := func() tea.Msg {
		sst, err := loadSettingsState(m)
		if err != nil {
			return app.SettingsMsg{Err: err}
		}
		opts, descs := settingsOptions(sst)
		return app.SettingsMsg{St: sst, Opts: opts, Descs: descs}
	}
	// file-backed rows (index 7+): cycle the value. Local rows apply
	// instantly; the rest write settings.json and reconnect pi (the
	// /login stay-open pattern: rows update optimistically, respawnMsg
	// lands behind the open dialog).
	if ri >= 7 {
		return settingsFileAction(m, d, st, ri-7)
	}
	switch ri {
	case 0: // model picker
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.RunBuiltin("model", "")
	case 1: // thinking picker
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.RunBuiltin("thinking", "")
	case 2:
		next := "all"
		if st.Steering == "all" {
			next = "one-at-a-time"
		}
		m.Status = "switching steering…"
		m.Refresh()
		return m, func() tea.Msg {
			if err := m.Pi.SetSteering(next); err != nil {
				return app.SettingsMsg{Err: err}
			}
			_ = pirpc.SetPiSetting("steeringMode", next) // next start; live now via RPC
			return refresh()
		}
	case 3:
		next := "all"
		if st.FollowUp == "all" {
			next = "one-at-a-time"
		}
		m.Status = "switching follow-up…"
		m.Refresh()
		return m, func() tea.Msg {
			if err := m.Pi.SetFollowUp(next); err != nil {
				return app.SettingsMsg{Err: err}
			}
			_ = pirpc.SetPiSetting("followUpMode", next)
			return refresh()
		}
	case 4:
		m.Status = "switching auto-compact…"
		m.Refresh()
		return m, func() tea.Msg {
			if err := m.Pi.SetAutoCompact(!st.AutoCompact); err != nil {
				return app.SettingsMsg{Err: err}
			}
			_ = pirpc.SetPiSetting("compaction.enabled", !st.AutoCompact)
			return refresh()
		}
	case 5:
		m.AutoRetry = !st.AutoRetry
		m.Status = "switching auto-retry…"
		m.Refresh()
		auto := m.AutoRetry
		return m, func() tea.Msg {
			if err := m.Pi.SetAutoRetry(auto); err != nil {
				m.AutoRetry = !auto
				return app.SettingsMsg{Err: err}
			}
			_ = pirpc.SetPiSetting("retry.enabled", auto)
			return refresh()
		}
	case 6: // theme picker
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.OpenTheme()
	}
	return m, nil
}

// settingsFileAction cycles one fileSettings row: local rows apply instantly
// (+ prefs.json), the rest persist to settings.json and respawn pi.
func settingsFileAction(m *app.Model, d *app.Dialog, st app.SettingsState, fi int) (tea.Model, tea.Cmd) {
	if fi < 0 || fi >= len(fileSettings) {
		return m, nil
	}
	fr := fileSettings[fi]
	cur := st.Vals[fr.path]
	if fr.path == "" {
		if fr.label == "Hide thinking" {
			cur = onoff(st.HideThinking)
		} else {
			cur = itoa(st.AutocompleteMax)
		}
	}
	next := nextVal(fr.vals, cur)
	if fr.local {
		applyLocalSetting(m, fr, next)
		st.HideThinking = m.HideThinking
		st.AutocompleteMax = palette.Win
		opts, descs := settingsOptions(st)
		d.Options, d.Descs, d.Settings = opts, descs, st
		d.Reindex()
		m.Refresh()
		return m, nil
	}
	m.Status = "saving " + strings.ToLower(fr.label) + "…"
	m.Refresh()
	if err := pirpc.SetPiSetting(fr.path, fr.toVal(next)); err != nil {
		m.Status = "ready"
		m.AddBlock(app.Block{Kind: "notice", Text: "settings write failed: " + err.Error(), Err: true})
		m.Refresh()
		return m, nil
	}
	if st.Vals == nil {
		st.Vals = map[string]string{}
	}
	st.Vals[fr.path] = next
	opts, descs := settingsOptions(st)
	d.Options, d.Descs, d.Settings = opts, descs, st
	d.Reindex()
	m.Status = "reconnecting pi…"
	m.Refresh()
	return m, m.RespawnPi()
}

// applyLocalSetting applies a pitago-local row instantly and persists it.
func applyLocalSetting(m *app.Model, fr fileSetting, next string) {
	prefs := app.LoadPrefs(m.PrefsPath())
	switch fr.label {
	case "Hide thinking":
		m.HideThinking = next == "on"
		prefs.HideThinking = m.HideThinking
		_ = pirpc.SetPiSetting(fr.path, fr.toVal(next)) // cross-compat with stock pi
		_ = app.SavePrefs(m.PrefsPath(), prefs)
	case "Autocomplete max":
		palette.Win = atoiOr(next, 10)
		prefs.AutocompleteMax = palette.Win
		_ = app.SavePrefs(m.PrefsPath(), prefs)
	case "Tree filter mode":
		_ = pirpc.SetPiSetting(fr.path, fr.toVal(next)) // /tree reads it live
	}
}

// renderTree renders the session tree pi-style: branch connectors, a "• "
// prefix on the active leaf path, "[label] " bookmarks, and one pi-formatted
// row per entry (see treeRow). Usage entries are skipped like pi (their
// children still render). Read-only: pi's RPC has no navigate_tree, so
// branch switching stays in pi's own TUI.
func renderTree(nodes []pirpc.TreeNode, leaf, filter string) string {
	if len(nodes) == 0 {
		return "No entries in session"
	}
	tcm := buildToolCallMap(nodes)
	active := activePathIDs(nodes, leaf)
	var b strings.Builder
	count := 0
	var walk func(ns []pirpc.TreeNode, prefix string)
	walk = func(ns []pirpc.TreeNode, prefix string) {
		for i, n := range ns {
			if count >= 100 {
				return
			}
			if n.Entry.Type == "usage" {
				walk(n.Children, prefix)
				continue
			}
			if !treePassesFilter(n, filter) {
				walk(n.Children, prefix)
				continue
			}
			count++
			last := i == len(ns)-1
			branch, cont := "├── ", "│   "
			if last {
				branch, cont = "└── ", "    "
			}
			b.WriteString(prefix + branch + treeRow(n, tcm, active) + "\n")
			walk(n.Children, prefix+cont)
		}
	}
	walk(nodes, "")
	if count == 0 {
		return "No entries in session"
	}
	if count >= 100 {
		b.WriteString("…(truncated)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// treePassesFilter mirrors stock pi's tree list filter: settings entries
// (label/model_change/...) show only in "all"; toolResults drop out in
// "no-tools"; "user-only" keeps user messages; "labeled-only" keeps
// bookmarked rows. Default hides the settings noise like pi.
func treePassesFilter(n pirpc.TreeNode, filter string) bool {
	e := n.Entry
	isSettings := e.Type == "label" || e.Type == "context_edit" || e.Type == "custom" ||
		e.Type == "model_change" || e.Type == "thinking_level_change" || e.Type == "session_info"
	switch filter {
	case "user-only":
		return e.Type == "message" && e.Message.Role == "user"
	case "no-tools":
		return !isSettings && !(e.Type == "message" && e.Message.Role == "toolResult")
	case "labeled-only":
		return n.Label != ""
	case "all":
		return true
	default:
		return !isSettings
	}
}

// buildToolCallMap indexes assistant toolCall blocks by id so toolResult
// rows can show what ran (pi keeps the same map for its tree list).
func buildToolCallMap(nodes []pirpc.TreeNode) map[string]pirpc.ContentBlock {
	m := map[string]pirpc.ContentBlock{}
	var walk func(ns []pirpc.TreeNode)
	walk = func(ns []pirpc.TreeNode) {
		for _, n := range ns {
			if n.Entry.Type == "message" {
				for _, bl := range pirpc.BlocksOf(n.Entry.Message.Content) {
					if bl.Type == "toolCall" && bl.ID != "" {
						m[bl.ID] = bl
					}
				}
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return m
}

// activePathIDs marks the leaf and every ancestor up to the root (pi's
// buildActivePath: the "• " trail of the current branch).
func activePathIDs(nodes []pirpc.TreeNode, leaf string) map[string]bool {
	if leaf == "" {
		return nil
	}
	parent := map[string]string{}
	var walk func(ns []pirpc.TreeNode)
	walk = func(ns []pirpc.TreeNode) {
		for _, n := range ns {
			if n.Entry.ParentID != nil {
				parent[n.Entry.ID] = *n.Entry.ParentID
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	out := map[string]bool{leaf: true}
	for id := leaf; ; {
		p, ok := parent[id]
		if !ok || p == "" {
			break
		}
		out[p] = true
		id = p
	}
	return out
}

// treeRow is one pi tree-list row: "• " when on the active path,
// "[label] " bookmarks, then the entry text (pi's getEntryDisplayText,
// plain — colors stay in pi's TUI).
func treeRow(n pirpc.TreeNode, tcm map[string]pirpc.ContentBlock, active map[string]bool) string {
	e := n.Entry
	var content string
	switch e.Type {
	case "message":
		content = treeMessage(e, tcm)
	case "custom_message":
		content = "[" + e.CustomType + "]: " + treeNorm(pirpc.TextOf(e.Content))
	case "compaction":
		content = fmt.Sprintf("[compaction: %dk tokens]", (e.TokensBefore+500)/1000)
	case "branch_summary":
		content = "[branch summary]: " + treeNorm(e.Summary)
	case "model_change":
		content = "[model: " + app.OrDefault(e.ModelID, "?") + "]"
	case "thinking_level_change":
		content = "[thinking: " + app.OrDefault(e.ThinkingLevel, "?") + "]"
	case "custom":
		content = "[custom: " + e.CustomType + "]"
	case "label":
		lbl := e.Label
		if lbl == "" {
			lbl = "(cleared)"
		}
		content = "[label: " + lbl + "]"
	case "session_info":
		if e.Name != "" {
			content = "[title: " + e.Name + "]"
		} else {
			content = "[title: empty]"
		}
	default:
		content = e.Type + " " + app.ShortID(e.ID)
	}
	pre := ""
	if active[e.ID] {
		pre += "• "
	}
	if n.Label != "" {
		pre += "[" + n.Label + "] "
	}
	return pre + content
}

// treeMessage formats message entries like pi: "user: …", "assistant: …"
// (with (aborted)/error/(no content) fallbacks), toolResult as the tool
// call ("[read: path]", "[bash: cmd]", …) via the toolCall map, and
// bashExecution as "[bash]: command".
func treeMessage(e pirpc.TreeEntry, tcm map[string]pirpc.ContentBlock) string {
	msg := e.Message
	switch msg.Role {
	case "user":
		return "user: " + treeNorm(pirpc.TextOf(msg.Content))
	case "assistant":
		if t := treeNorm(pirpc.TextOf(msg.Content)); t != "" {
			return "assistant: " + t
		}
		if msg.StopReason == "aborted" {
			return "assistant: (aborted)"
		}
		if strings.TrimSpace(msg.ErrorMessage) != "" {
			err := treeNorm(msg.ErrorMessage)
			if r := []rune(err); len(r) > 80 {
				err = string(r[:80])
			}
			return "assistant: " + err
		}
		return "assistant: (no content)"
	case "toolResult":
		if msg.ToolCallID != "" {
			if tc, ok := tcm[msg.ToolCallID]; ok {
				return treeTool(tc.Name, tc.Arguments)
			}
		}
		return "[" + app.OrDefault(msg.ToolName, "tool") + "]"
	case "bashExecution":
		cmd := msg.Command
		if cmd == "" {
			cmd = pirpc.TextOf(msg.Content)
		}
		return "[bash]: " + treeNorm(cmd)
	default:
		if msg.Role == "" {
			return "[message]"
		}
		return "[" + msg.Role + "]"
	}
}

// treeTool formats one tool call like pi's tree list ("[read: path:1-3]",
// "[bash: cmd]", "[grep: /pat/ in path]", …).
func treeTool(name string, args json.RawMessage) string {
	shortPath := func(keys ...string) string { return pirpc.Shorten(treeArg(args, keys...)) }
	switch strings.ToLower(name) {
	case "read":
		p := shortPath("path", "file_path")
		off, hasOff := treeArgNum(args, "offset")
		lim, hasLim := treeArgNum(args, "limit")
		if !hasOff && !hasLim {
			return "[read: " + p + "]"
		}
		start := 1
		if hasOff && off >= 1 {
			start = int(off)
		}
		if hasLim && lim >= 1 {
			return fmt.Sprintf("[read: %s:%d-%d]", p, start, start+int(lim)-1)
		}
		return fmt.Sprintf("[read: %s:%d]", p, start)
	case "write":
		return "[write: " + shortPath("path", "file_path") + "]"
	case "edit":
		return "[edit: " + shortPath("path", "file_path") + "]"
	case "bash":
		cmd := treeNorm(treeArg(args, "command"))
		if r := []rune(cmd); len(r) > 50 {
			cmd = string(r[:50]) + "..."
		}
		return "[bash: " + cmd + "]"
	case "grep":
		pat := treeNorm(treeArg(args, "pattern"))
		if pat == "" {
			pat = "..."
		}
		return "[grep: /" + pat + "/ in " + app.OrDefault(shortPath("path"), ".") + "]"
	case "find":
		pat := treeNorm(treeArg(args, "pattern"))
		if pat == "" {
			pat = "..."
		}
		return "[find: " + pat + " in " + app.OrDefault(shortPath("path"), ".") + "]"
	case "ls":
		return "[ls: " + app.OrDefault(shortPath("path"), ".") + "]"
	default:
		s := strings.Join(strings.Fields(string(args)), " ")
		if s == "" || s == "null" {
			return "[" + name + "]"
		}
		if r := []rune(s); len(r) > 40 {
			return "[" + name + ": " + string(r[:40]) + "...]"
		}
		return "[" + name + ": " + s + "]"
	}
}

// treeArg reads one string field from tool-call JSON args ("": absent).
func treeArg(raw json.RawMessage, keys ...string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || string(v) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue
		}
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// treeArgNum reads one numeric field from tool-call JSON args.
func treeArgNum(raw json.RawMessage, key string) (float64, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || string(v) == "null" {
		return 0, false
	}
	var n float64
	if err := json.Unmarshal(v, &n); err != nil {
		return 0, false
	}
	return n, true
}

// treeNorm matches pi's row text: newline/tab → space, trimmed, 200 chars.
func treeNorm(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		return string(r[:200])
	}
	return s
}

// Origin marks where a builtin feature comes from.
const (
	// OriginPi re-implements one of pi's TUI-level builtins over RPC.
	// pi's own builtins never arrive via get_commands, so pitago intercepts
	// them locally instead of leaking "/..." text into the chat.
	OriginPi = "pi"
	// OriginPitago is pitago's own addition on top of pi.
	OriginPitago = "pitago"
)

// Builtin is one locally-executed slash command (implementation lives here,
// wire-up in src/app via UseBuiltins).
type Builtin = app.Builtin

// All lists every intercepted command: pi's BUILTIN_SLASH_COMMANDS plus
// pitago's own /recent. Anything else (extension/prompt/skill commands,
// chat text) falls through to pi via Prompt.
func All() []app.Builtin {
	pi := func(name, desc, usage string, run func(m *app.Model, arg string) tea.Cmd) app.Builtin {
		return app.Builtin{Name: name, Desc: desc, Usage: usage, Origin: OriginPi, Run: run}
	}
	all := []app.Builtin{
		pi("settings", "Open agent settings (model · thinking · steering · compact · retry)", "/settings", func(m *app.Model, arg string) tea.Cmd {
			m.Status = "loading settings…"
			m.Refresh()
			return loadSettings(m)
		}),
		{
			Name: "pitago-setting", Desc: "Open Pitago settings hub (agent · skills · plugins · MCP · tools · tasks · sidebar)", Usage: "/pitago-setting",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				m.OpenPconfig()
				return nil
			},
		},
		pi("model", "<provider/model> — Select model (opens selector UI)", "/model", func(m *app.Model, arg string) tea.Cmd {
			m.Status = "loading models…"
			m.Refresh()
			return func() tea.Msg {
				models, err := m.Pi.GetModels()
				if err != nil {
					return app.PickerMsg{Kind: "model", Err: err}
				}
				opts := make([]string, 0, len(models))
				descs := make([]string, 0, len(models))
				provs := make([]string, 0, len(models))
				for _, mi := range models {
					opts = append(opts, mi.ID)
					descs = append(descs, mi.Name+" · "+mi.Provider)
					provs = append(provs, mi.Provider)
				}
				return app.PickerMsg{Kind: "model", Options: opts, Descs: descs, Providers: provs, Models: models, Current: m.ModelLbl}
			}
		}),
		pi("tree", "Navigate session tree (switch branches)", "/tree", func(m *app.Model, arg string) tea.Cmd {
			m.Status = "loading session tree…"
			m.Refresh()
			return func() tea.Msg {
				nodes, leaf, err := m.Pi.GetTree()
				if err != nil {
					return app.TreeMsg{Err: err}
				}
				filter := pirpc.PiString(pirpc.ReadPiSettings(), "treeFilterMode", "default")
				if a := strings.ToLower(strings.TrimSpace(arg)); a != "" {
					switch a {
					case "default", "no-tools", "user-only", "labeled-only", "all":
						filter = a // one-shot override: /tree all
					}
				}
				return app.TreeMsg{Text: renderTree(nodes, leaf, filter)}
			}
		}),
		pi("thinking", "<level> — Set thinking level", "/thinking", func(m *app.Model, arg string) tea.Cmd {
			return m.OpenThinking()
		}),
		pi("reload", "Reload keybindings, extensions, skills, prompts, themes, and context files", "/reload", func(m *app.Model, arg string) tea.Cmd {
			// No "reload" RPC exists (pi returns "Unknown command"), and a
			// plain GetCommands re-reads the already-loaded list — a `pi
			// install` done outside (or in the hub) never appears until the
			// process restarts. Respawn keeps the session and reloads
			// everything, like the /login stay-open pattern.
			m.AddBlock(app.Block{Kind: "notice", Text: "reloading extensions — reconnecting pi…"})
			return m.RespawnPi()
		}),
		pi("login", "<provider> — Configure provider authentication", "/login [provider]", func(m *app.Model, arg string) tea.Cmd {
			return openLogin(m, arg)
		}),
		pi("logout", "Remove provider authentication", "/logout [provider]", func(m *app.Model, arg string) tea.Cmd {
			return openLogout(m, arg)
		}),
		pi("new", "Start a new session", "/new", func(m *app.Model, arg string) tea.Cmd {
			m.Status = "opening new session…"
			m.Refresh()
			return func() tea.Msg {
				return app.SessionResetMsg{Err: m.Pi.NewSession()}
			}
		}),
		pi("quit", "Quit pi", "/quit", func(m *app.Model, arg string) tea.Cmd {
			return tea.Quit
		}),
		pi("session", "Show session info and stats", "/session", func(m *app.Model, arg string) tea.Cmd {
			m.Status = "loading session info…"
			m.Refresh()
			return loadSession(m)
		}),
		pi("resume", "Resume a session (like pi)", "/resume [path]", func(m *app.Model, arg string) tea.Cmd {
			return m.OpenResume(arg)
		}),
		{
			Name: "recent", Desc: "Switch recent model (pitago)", Usage: "/recent",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				return m.OpenRecents()
			},
		},
		{
			Name: "yank", Desc: "Copy last assistant answer to clipboard (chat-only)", Usage: "/yank",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				return m.YankLast()
			},
		},
		{
			Name: "copy", Desc: "Copy last assistant answer to clipboard (chat-only)", Usage: "/copy",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				return m.YankLast()
			},
		},
		{
			Name: "sidebar", Desc: "Hide/show sidebar (hide for clean drag-select of chat)", Usage: "/sidebar",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				m.ToggleSide()
				return nil
			},
		},
		{
			Name: "plugins", Desc: "Collapse/expand installed pi plugins in the sidebar", Usage: "/plugins",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				m.TogglePlugins()
				return nil
			},
		},
		{
			Name: "mouse", Desc: "Toggle mouse — Alt+M · off for native text selection", Usage: "/mouse [on|off]",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				return m.ToggleMouse(arg)
			},
		},
		{
			Name: "theme", Desc: "Switch TUI theme (One Dark, Gruvbox, Catppuccin… — /theme lists all)", Usage: "/theme [name]",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				if arg == "" {
					return m.OpenTheme()
				}
				m.SetTheme(arg)
				return nil
			},
		},
		{
			Name: "update", Desc: "Check + install pitago update (pitago)", Usage: "/update",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				m.Status = "checking for updates…"
				m.Refresh()
				return m.CheckUpdatesCmd(false)
			},
		},
		{
			Name: "shortcuts", Desc: "Show keyboard shortcuts (/? or Ctrl+Shift+/)", Usage: "/shortcuts",
			Origin: OriginPitago,
			Run: func(m *app.Model, arg string) tea.Cmd {
				return m.OpenShortcuts()
			},
		},
	}
	// Pi builtins with no RPC equivalent stay intercepted so they report
	// instead of leaking into the chat (old runBuiltin default branch).
	for _, name := range []string{
		"scoped-models", "export", "import", "share", "name",
		"changelog", "hotkeys", "fork", "clone", "trust", "compact",
	} {
		name := name
		all = append(all, app.Builtin{
			Name: name, Desc: "pi TUI-only (no RPC equivalent)", Usage: "/" + name,
			Origin: OriginPi,
			Run: func(m *app.Model, arg string) tea.Cmd {
				m.AddBlock(app.Block{Kind: "notice", Text: fmt.Sprintf("/%s needs pi's own TUI — run it in pi directly", name)})
				m.Refresh()
				return nil
			},
		})
	}
	return all
}
