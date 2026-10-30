package app

import (
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// In-TUI OAuth / subscription login (Claude Pro/Max, ChatGPT, Copilot, ...).
// pi's own provider code runs in a node bridge (pirpc.StartOAuthLogin); this
// file is the dialog that shows its URL / device code / progress and answers
// its prompts. Success reconnects pi, exactly like adding an API key.

const oauthKind = "oauthFlow"

// oauthState is the live state of one oauthFlow dialog.
type oauthState struct {
	prov, name   string
	sess         *pirpc.OAuthSession
	url, instr   string
	code, verify string
	lines        []string          // info / progress, newest last
	prompt       *pirpc.OAuthEvent // pending prompt (nil = none)
	done         bool
	err          string
	opened       bool // browser already opened for this URL
}

type oauthStartedMsg struct {
	D   *Dialog
	S   *pirpc.OAuthSession
	Err error
}

type oauthEventMsg struct {
	D  *Dialog
	S  *pirpc.OAuthSession
	Ev pirpc.OAuthEvent
	OK bool // false = event stream closed
}

// OAuthLoggedInMsg lands after a successful login once pitago's auth
// mirror is synced; it refreshes /login and reconnects pi.
type OAuthLoggedInMsg struct {
	Prov, Name string
}

// OpenOAuthFlow pushes the sign-in dialog for prov on top of the stack and
// starts pi's login for it.
func (m *Model) OpenOAuthFlow(prov string) tea.Cmd {
	name := prov
	if info, ok := pirpc.OAuthLoginFor(prov); ok && info.Name != "" {
		name = info.Name
	}
	d := &Dialog{Kind: oauthKind, Title: "Sign in — " + name, LoginProvider: prov,
		oauth: &oauthState{prov: prov, name: name, lines: []string{"starting pi login…"}}}
	m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	m.Refresh()
	return func() tea.Msg {
		s, err := pirpc.StartOAuthLogin(prov)
		return oauthStartedMsg{D: d, S: s, Err: err}
	}
}

func waitOAuth(d *Dialog, s *pirpc.OAuthSession) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-s.Events
		return oauthEventMsg{D: d, S: s, Ev: ev, OK: ok}
	}
}

// dialogOpen reports whether d is still somewhere in the stack.
func (m *Model) dialogOpen(d *Dialog) bool {
	for _, x := range m.Dialogs {
		if x == d {
			return true
		}
	}
	return false
}

// openBrowserCmd opens url in the default browser (best effort, silent).
func openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
		go func() { _ = cmd.Wait() }()
		return nil
	}
}

// handleOAuthMsg routes the oauthFlow messages; ok=false = not ours.
func (m Model) handleOAuthMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case oauthStartedMsg:
		st := msg.D.oauth
		if msg.Err != nil {
			st.err = msg.Err.Error()
			m.Refresh()
			return m, nil, true
		}
		if !m.dialogOpen(msg.D) { // closed before node came up
			msg.S.Cancel()
			return m, nil, true
		}
		st.sess = msg.S
		st.lines = nil
		m.Refresh()
		return m, waitOAuth(msg.D, msg.S), true

	case oauthEventMsg:
		if !msg.OK {
			return m, nil, true
		}
		d, st := msg.D, msg.D.oauth
		var cmds []tea.Cmd
		switch ev := msg.Ev; ev.Type {
		case "auth_url":
			st.url, st.instr = ev.URL, ev.Instructions
			if !st.opened {
				st.opened = true
				cmds = append(cmds, openBrowserCmd(ev.URL))
			}
		case "device_code":
			st.code, st.verify = ev.UserCode, ev.VerificationURI
			if !st.opened {
				st.opened = true
				cmds = append(cmds, openBrowserCmd(ev.VerificationURI))
			}
			st.lines = append(st.lines, "waiting for authentication…")
		case "info", "progress":
			if strings.TrimSpace(ev.Message) != "" {
				st.lines = append(st.lines, ev.Message)
				if len(st.lines) > 4 {
					st.lines = st.lines[len(st.lines)-4:]
				}
			}
		case "prompt":
			p := ev
			st.prompt = &p
			d.Filter, d.Cursor = "", 0
		case "prompt_cancel":
			if st.prompt != nil && st.prompt.ID == ev.ID {
				st.prompt = nil
				d.Filter = ""
			}
		case "done":
			st.done, st.prompt = true, nil
			prov, name, authPath := st.prov, st.name, m.authPath()
			cmds = append(cmds, func() tea.Msg {
				pirpc.SyncAuthStateFromPi(authPath)
				return OAuthLoggedInMsg{Prov: prov, Name: name}
			})
		case "error":
			st.prompt = nil
			if !m.dialogOpen(d) { // user cancelled: nothing to show
				return m, nil, true
			}
			st.err = ev.Message
		}
		if ev := msg.Ev.Type; ev != "done" && ev != "error" {
			cmds = append(cmds, waitOAuth(d, msg.S))
		}
		m.applyPopupH()
		m.Refresh()
		return m, tea.Batch(cmds...), true

	case OAuthLoggedInMsg:
		// Drop the flow dialog, refresh /login underneath, reconnect pi.
		kept := m.Dialogs[:0]
		for _, x := range m.Dialogs {
			if x.Kind != oauthKind {
				kept = append(kept, x)
			}
		}
		m.Dialogs = kept
		for _, x := range m.Dialogs {
			if x.Kind == "login" && len(x.Provs) > 0 {
				m.RefreshLoginKeys(x)
			}
		}
		m.pushToast("✓ signed in — "+msg.Name, false)
		m.AddBlock(Block{Kind: "notice", Text: "signed in to " + msg.Name + " (OAuth, saved in pi auth.json) — reconnecting…"})
		m.drainQueuedDialogs()
		m.Refresh()
		return m, m.RespawnPi(), true
	}
	return m, nil, false
}

// closeOAuthFlow cancels a running login and pops the dialog.
func (m Model) closeOAuthFlow(d *Dialog) (tea.Model, tea.Cmd) {
	if st := d.oauth; st != nil && st.sess != nil && !st.done {
		st.sess.Cancel()
	}
	if len(m.Dialogs) > 0 && m.Dialogs[0] == d {
		m.Dialogs = m.Dialogs[1:]
	}
	m.drainQueuedDialogs()
	m.applyPopupH()
	m.Refresh()
	return m, nil
}

// oauthCopyText is what ^Y copies: the device code, else the login URL.
func oauthCopyText(st *oauthState) string {
	if st.code != "" {
		return st.code
	}
	if st.url != "" {
		return st.url
	}
	return st.verify
}

func (m Model) updateOAuthDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	st := d.oauth
	if st == nil {
		return m.closeOAuthFlow(d)
	}
	switch km.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m.closeOAuthFlow(d)
	case tea.KeyCtrlY:
		if t := oauthCopyText(st); t != "" {
			if err := clipWrite(t); err != nil {
				m.pushToast("copy failed: "+err.Error(), true)
			} else {
				m.pushToast("✓ copied to clipboard", false)
			}
			m.Refresh()
		}
		return m, nil
	case tea.KeyCtrlO:
		if u := st.url; u != "" {
			return m, openBrowserCmd(u)
		}
		if st.verify != "" {
			return m, openBrowserCmd(st.verify)
		}
		return m, nil
	}
	if st.err != "" || st.done {
		if km.Type == tea.KeyEnter {
			return m.closeOAuthFlow(d)
		}
		return m, nil
	}
	p := st.prompt
	if p == nil || st.sess == nil {
		return m, nil
	}
	if p.Kind == "select" {
		n := len(p.Options)
		switch km.Type {
		case tea.KeyUp, tea.KeyShiftTab:
			if n > 0 {
				d.Cursor = (d.Cursor - 1 + n) % n
			}
		case tea.KeyDown, tea.KeyTab:
			if n > 0 {
				d.Cursor = (d.Cursor + 1) % n
			}
		case tea.KeyEnter:
			if d.Cursor >= 0 && d.Cursor < n {
				_ = st.sess.Answer(p.ID, p.Options[d.Cursor].ID)
				st.prompt = nil
			}
		}
		m.Refresh()
		return m, nil
	}
	// text / secret / manual_code: free-text buffer in d.Filter.
	switch km.Type {
	case tea.KeyEnter:
		_ = st.sess.Answer(p.ID, strings.TrimSpace(d.Filter))
		st.prompt, d.Filter = nil, ""
	case tea.KeyBackspace:
		if r := []rune(d.Filter); len(r) > 0 {
			d.Filter = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		d.Filter = ""
	case tea.KeyCtrlV:
		return m, m.pasteCmd(true)
	case tea.KeySpace:
		d.Filter += " "
	case tea.KeyRunes:
		d.Filter += string(km.Runes) // Runes, not String(): a paste is not "[...]"-wrapped
	}
	m.applyPopupH()
	m.Refresh()
	return m, nil
}

func (m Model) renderOAuthDialog(d *Dialog) string {
	st := d.oauth
	boxW := m.winW - 10
	if boxW > 96 {
		boxW = 96
	}
	if boxW < 50 {
		boxW = 50
	}
	inner := boxW - 8 // dlgStyle border + padding
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	wrap := func(s string, style lipgloss.Style) {
		b.WriteString(style.Width(inner).Render(s) + "\n")
	}
	accent := lipgloss.NewStyle().Foreground(cAccent)
	if st == nil {
		return dlgStyle.Width(boxW).Render(b.String())
	}
	if st.url != "" {
		b.WriteString("\n")
		wrap("Open this URL in your browser (opened automatically):", statusBarStyle)
		wrap(st.url, accent.Underline(true))
		if st.instr != "" {
			wrap(st.instr, statusBarStyle)
		}
	}
	if st.code != "" {
		b.WriteString("\n")
		wrap("Go to "+st.verify+" and enter the code:", statusBarStyle)
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cHiFg).Background(cHiBg).
			Padding(0, 2).Render(st.code) + "\n")
	}
	if len(st.lines) > 0 {
		b.WriteString("\n")
		for _, l := range st.lines {
			wrap(l, toolStyle)
		}
	}
	hint := "^Y copy link · ^O open browser · Esc cancel"
	switch {
	case st.err != "":
		b.WriteString("\n")
		wrap("✗ "+st.err, errStyle)
		hint = "Enter / Esc close"
	case st.done:
		b.WriteString("\n" + okStyle.Render("✓ signed in — reconnecting pi…") + "\n")
		hint = ""
	case st.prompt != nil:
		p := st.prompt
		b.WriteString("\n")
		wrap(p.Message, lipgloss.NewStyle().Foreground(cText).Bold(true))
		if p.Kind == "select" {
			for i, o := range p.Options {
				if i == d.Cursor {
					b.WriteString(accent.Render("▸ ") + rowHiStyle.Render(" "+o.Label+" ") + "\n")
				} else {
					b.WriteString("  " + statusBarStyle.Render(" "+o.Label) + "\n")
				}
			}
			hint = "↑↓ choose · Enter confirm · Esc cancel"
		} else {
			val := d.Filter
			if p.Kind == "secret" {
				val = strings.Repeat("•", len([]rune(val)))
			}
			if val == "" && p.Placeholder != "" {
				b.WriteString(toolStyle.Render(p.Placeholder) + cmdHiStyle.Render("▌") + "\n")
			} else {
				wrap(val+"▌", cmdHiStyle)
			}
			hint = "Enter submit · ^V paste · " + hint
		}
	}
	if hint != "" {
		b.WriteString("\n" + toolStyle.Render(hint))
	}
	box := dlgStyle.Width(boxW).Render(b.String())
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		"",
	)
}
