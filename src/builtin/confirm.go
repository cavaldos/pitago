package builtin

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// Confirmers runs the Enter action of each picker dialog kind.
// Wired into app via UseBuiltins; app's confirmDialog only dispatches.
func Confirmers() map[string]app.ConfirmFunc {
	return map[string]app.ConfirmFunc{
		"model":       confirmModel,
		"recent":      confirmRecent,
		"sessions":    confirmSessions,
		"thinking":    confirmThinking,
		"theme":       confirmTheme,
		"settings":    confirmSettings,
		"pconfig":     confirmPconfig,
		"login":       confirmLogin,
		"loginMethod": confirmLoginMethod,
		"loginOAuth":  confirmLoginOAuth,
		"logout":      confirmLogout,
		"secret":      confirmSecret,
		"yank":        confirmYank,
		"update":      confirmUpdate,
		"trajectory":  confirmTrajectory,
		"tree":        confirmTree,
		"fork":        confirmFork,
		"subagents":   confirmSubagents,
	}
}

// confirmSubagents selects the highlighted subagent: persists it as current
// (● marker next open) and toasts the choice. The "— none —" row clears
// back to the initial state. Details stay in the row descs.
func confirmSubagents(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	name := d.Options[ri]
	m.Dialogs = m.Dialogs[1:]
	if name == app.SubagentsNone {
		m.SetCurrentSubagent("")
		m.AddBlock(app.Block{Kind: "notice", Text: "subagent off → back to initial state"})
		m.Refresh()
		return m, nil
	}
	desc := ""
	if ri < len(d.Descs) {
		desc = d.Descs[ri]
	}
	text := "subagent → " + name
	if desc != "" {
		text += " " + desc
	}
	m.SetCurrentSubagent(name)
	m.AddBlock(app.Block{Kind: "notice", Text: text})
	m.Refresh()
	return m, nil
}

func confirmModel(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	prov, id := d.Providers[ri], d.Options[ri]
	m.Dialogs = m.Dialogs[1:]
	m.Status = "switching model…"
	m.Refresh()
	return m, func() tea.Msg {
		label, err := m.Pi.SetModelByID(prov, id)
		return app.ModelCycleMsg{Label: label, Provider: prov, ID: id, Err: err}
	}
}

// confirmFork branches the session at the picked user message (pi:
// runtimeHost.fork(entryId) → {text, cancelled}). pi hands the branch's
// text back to the editor, and an extension veto through
// session_before_switch is reported rather than read as "done".
func confirmFork(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Paths) {
		return m, nil
	}
	entryID := d.Paths[ri]
	m.Dialogs = m.Dialogs[1:]
	m.Status = "forking session…"
	m.Refresh()
	return m, func() tea.Msg {
		res, err := m.Pi.Fork(entryID)
		verr := res.VetoErr("fork", err)
		return app.PiOpMsg{Op: "fork", Notice: "forked to a new session", Text: res.Text,
			Err: verr, Reload: verr == nil}
	}
}

func confirmRecent(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	m.Dialogs = m.Dialogs[1:]
	return m, m.SwitchToRecent(ri)
}

// confirmSessions resumes the picked session file (Paths parallels Options).
func confirmSessions(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Paths) {
		return m, nil
	}
	path := d.Paths[ri]
	m.Dialogs = m.Dialogs[1:]
	return m, m.SwitchSession(path)
}

func confirmThinking(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	level := d.Options[ri]
	m.Dialogs = m.Dialogs[1:]
	m.Status = "switching thinking…"
	m.Refresh()
	return m, func() tea.Msg {
		if err := m.Pi.SetLevel(level); err != nil {
			return app.SettingsRefreshMsg{Err: err}
		}
		return app.SettingsRefreshMsg{Notice: "thinking → " + level, Level: level}
	}
}

func confirmSettings(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	return settingsAction(m, ri)
}

// confirmPconfig runs Enter on the hub's right pane. "@..." action rows pop
// the hub and reuse the classic single-dialog flows (settings/theme/login);
// "side:..." rows toggle a sidebar section in place (hub stays open);
// runnable rows (skill/prompt/extension, connected MCP) stage the /command
// in the input; info-only rows explain where to manage them.
func confirmPconfig(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Payload) {
		return m, nil
	}
	switch p := d.Payload[ri]; {
	case strings.HasPrefix(p, "side:"):
		m.ToggleSideSection(strings.TrimPrefix(p, "side:"))
		cur := d.Cursor // toggle rebuilds rows: keep the cursor where it was
		m.LoadPsecRows(d)
		if cur < len(d.FIdx) {
			d.Cursor = cur
		}
		m.Refresh()
		return m, nil
	case strings.HasPrefix(p, "tasks:"):
		m.CycleTasksSetting(strings.TrimPrefix(p, "tasks:"))
		cur := d.Cursor // cycle rebuilds rows: keep the cursor where it was
		m.LoadPsecRows(d)
		if cur < len(d.FIdx) {
			d.Cursor = cur
		}
		m.Refresh()
		return m, nil
	case p == "marketmore":
		m.Status = "loading more plugins…"
		m.Refresh()
		return m, m.FetchMarketPageCmd(len(m.Market))
	case strings.HasPrefix(p, "market:"):
		spec := "npm:" + strings.TrimPrefix(p, "market:")
		if !m.ConfirmPluginOp("install", spec) {
			return m, nil // first press arms the auth gate
		}
		m.Status = "installing " + spec + "…"
		m.Refresh()
		return m, m.ChangePluginCmd("install", spec)
	case p == "@agent":
		m.Dialogs = m.Dialogs[1:]
		m.Status = "loading settings…"
		m.Refresh()
		return m, loadSettings(m, "")
	case p == "@theme":
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, m.OpenTheme()
	case p == "@login":
		m.Dialogs = m.Dialogs[1:]
		m.Refresh()
		return m, openLogin(m, "")
	case p != "":
		m.FillCommand(p)
		return m, nil
	}
	switch d.CurPsec() {
	case app.PsecPlugin:
		m.AddBlock(app.Block{Kind: "notice", Text: "Delete uninstalls the plugin — Enter does nothing here"})
	case app.PsecMarket:
		m.AddBlock(app.Block{Kind: "notice", Text: "already installed — Enter on a new entry installs it"})
	case app.PsecMCP:
		m.AddBlock(app.Block{Kind: "notice", Text: "edit ~/.pi/agent/mcp.json, then /reload"})
	default:
		m.AddBlock(app.Block{Kind: "notice", Text: "nothing to run here"})
	}
	m.Refresh()
	return m, nil
}

// confirmTheme applies the picked palette (Options parallel the theme names).
func confirmTheme(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if ri < 0 || ri >= len(d.Options) {
		return m, nil
	}
	m.Dialogs = m.Dialogs[1:]
	m.SetTheme(d.Options[ri])
	return m, nil
}

func confirmLogin(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	if len(d.Provs) > 0 {
		// two-pane fallback (normally handled in updateLoginDialog):
		// focus the keys pane.
		d.ProvFocus = false
		m.Refresh()
		return m, nil
	}
	prov := d.Options[ri]
	m.Dialogs = m.Dialogs[1:]
	openLoginMethod(m, prov, pirpc.LookupEnv(prov))
	return m, nil
}

func confirmLoginMethod(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	prov := d.LoginProvider
	switch ri {
	case 0: // enter API key
		env := d.LoginEnv
		m.Dialogs[0] = &app.Dialog{Kind: "secret", Title: "API key — " + prov,
			Message:       "Save to " + env + " (pitago keystore, file 0600). Pi reconnects automatically.",
			LoginProvider: prov, LoginEnv: env}
		m.Refresh()
		return m, nil
	case 1: // OAuth
		m.Dialogs[0] = &app.Dialog{Kind: "loginOAuth", Title: "OAuth — " + prov,
			Message:       "1. Open another terminal\n2. Run: pi\n3. Type: /login " + prov + " then follow the steps\n4. Come back here and reload",
			Options:       []string{"Done — reload", "Close"},
			LoginProvider: prov}
		m.Dialogs[0].Reindex()
		m.Refresh()
		return m, nil
	default: // reload models
		m.Dialogs = m.Dialogs[1:]
		m.Status = "reloading models…"
		m.Refresh()
		return m, func() tea.Msg {
			models, err := m.Pi.GetModels()
			if err != nil {
				return app.SettingsRefreshMsg{Err: err}
			}
			return app.SettingsRefreshMsg{Notice: fmt.Sprintf("pi sees %d models", len(models))}
		}
	}
}

func confirmLoginOAuth(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	// Pop OAuth revealing /login underneath (stays on /login, not main).
	m.Dialogs = m.Dialogs[1:]
	if ri == 0 {
		// User just ran /login in stock pi: import keys (union) + mirror
		// pi logins (OAuth included) into pitago, refresh the picker.
		// Both writes are blocking file I/O, so they run off the event loop
		// and land as app.LoginReloadMsg — the same continuation the
		// /login "reload models" action uses. ProvFocus is in-memory, so it
		// is set here and picked up by the handler's RefreshLoginKeys.
		var login *app.Dialog
		if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "login" {
			login = m.Dialogs[0]
			login.ProvFocus = false
		}
		keyPath, authPath := m.KeyPath, m.AuthPath
		if authPath == "" {
			authPath = pirpc.AuthStatePath()
		}
		return m, func() tea.Msg {
			pirpc.SyncFromPi(keyPath)
			pirpc.SyncAuthStateFromPi(authPath)
			return app.LoginReloadMsg{D: login}
		}
	}
	m.Refresh()
	return m, nil
}

func confirmLogout(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	prov := d.Options[ri]
	desc := app.DescOf(d, ri)
	m.Dialogs = m.Dialogs[1:]
	return m, doLogout(m, prov, desc)
}

func confirmSecret(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	key := strings.TrimSpace(d.Filter)
	if key == "" {
		return m, nil
	}
	prov, env := d.LoginProvider, d.LoginEnv
	m.Dialogs = m.Dialogs[1:]
	m.Refresh()
	keyPath := m.KeyPath
	return m, func() tea.Msg {
		// Both writes are blocking file I/O, so they run off the event loop
		// and the handler refreshes the picker and reconnects behind it.
		msg := app.LoginKeyMsg{Provider: prov, Env: env, Key: key}
		if msg.Err = pirpc.SaveKey(keyPath, env, key); msg.Err != nil {
			return msg
		}
		// Critical: pi's auth.json wins over env, so export alone is not
		// enough — write the active key to pi too or pi never sees models.
		pirpc.PushActiveToPi(keyPath, env)
		return msg
	}
}

// confirmUpdate installs the picked release (ri==0) or dismisses it.
func confirmUpdate(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	target := d.UpdateTo
	m.Dialogs = m.Dialogs[1:]
	if ri != 0 || target == "" {
		m.Refresh()
		return m, nil
	}
	m.Status = "updating to " + target + "…"
	m.Refresh()
	return m, m.InstallUpdateCmd(target)
}

// confirmYank copies the picked message's full text (Payload parallel to
// Options) to the clipboard.

func confirmYank(m *app.Model, d *app.Dialog, ri int) (tea.Model, tea.Cmd) {
	m.Dialogs = m.Dialogs[1:]
	text := ""
	if ri < len(d.Payload) {
		text = d.Payload[ri]
	}
	if strings.TrimSpace(text) == "" {
		m.AddBlock(app.Block{Kind: "notice", Text: "nothing to yank yet"})
		m.Refresh()
		return m, nil
	}
	m.YankText(text)
	return m, nil
}
