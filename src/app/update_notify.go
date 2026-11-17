package app

import (
	"context"
	"errors"
	"time"

	tea "pitago/src/tui/ev"

	"pitago/src/update"
)

// UpdateCheckMsg is the result of a version check against GitHub
// releases. Auto=true means the silent startup check (never errors
// loudly); Auto=false is the manual /update path.
type UpdateCheckMsg struct {
	Current, Latest string
	IsNew           bool
	Auto            bool
	Err             error
}

// UpdateDoneMsg is the result of downloading + replacing the binary.
type UpdateDoneMsg struct {
	From, To string
	Err      error
	Manual   string // fallback instructions when Err != nil
}

// CheckUpdatesCmd fetches the latest release, refreshes the check cache,
// and reports. Manual (/update) always hits the network.
func (m Model) CheckUpdatesCmd(auto bool) tea.Cmd {
	cur := m.AppVersion
	return func() tea.Msg {
		if auto {
			if c := update.LoadCache(update.CachePath()); update.Fresh(c, update.CheckTTL) {
				if update.NeedsUpdate(cur, c.LatestTag) && update.IsRelease(cur) {
					return UpdateCheckMsg{Current: cur, Latest: c.LatestTag, IsNew: true, Auto: true}
				}
				return UpdateCheckMsg{Current: cur, Latest: c.LatestTag, Auto: true}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		latest, err := update.FetchLatest(ctx)
		if err != nil {
			return UpdateCheckMsg{Current: cur, Auto: auto, Err: err}
		}
		_ = update.SaveCache(update.CachePath(), latest) // best-effort
		return UpdateCheckMsg{Current: cur, Latest: latest,
			IsNew: update.NeedsUpdate(cur, latest), Auto: auto}
	}
}

// InstallUpdateCmd downloads the latest asset and replaces this binary.
func (m Model) InstallUpdateCmd(target string) tea.Cmd {
	from := m.AppVersion
	return func() tea.Msg {
		asset := update.CurrentAsset()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		err := update.Install(ctx, update.LatestURL(asset))
		if err == nil {
			return UpdateDoneMsg{From: from, To: target}
		}
		manual := update.Manual(asset)
		if errors.Is(err, update.ErrDevBinary) {
			err = errors.New("dev build has no installed binary to replace — " + manual)
		}
		return UpdateDoneMsg{From: from, To: target, Err: err, Manual: manual}
	}
}

// handleUpdateCheck renders a check result: manual always reports,
// auto only whispers when an update is actually available.
func (m *Model) handleUpdateCheck(msg UpdateCheckMsg) {
	if msg.Err != nil {
		if msg.Auto {
			return // silent: offline / rate-limited
		}
		m.Status = "ready"
		m.AddBlock(Block{Kind: "notice", Text: "update check failed: " + msg.Err.Error(), Err: true})
		m.Refresh()
		return
	}
	m.Status = "ready"
	if !msg.IsNew {
		if !msg.Auto {
			m.AddBlock(Block{Kind: "notice", Text: "already on latest (" + verOrDev(msg.Current) + ")"})
			m.Refresh()
		} else if m.UpdateAvail != "" && !update.NeedsUpdate(msg.Current, m.UpdateAvail) {
			m.UpdateAvail = "" // cache said new, network says settled
			m.Refresh()
		}
		return
	}
	if msg.Auto {
		if !update.IsRelease(msg.Current) {
			return // source build: don't nag on every launch
		}
		m.UpdateAvail = msg.Latest // welcome banner survives, even with empty chat
		text := "⬆ " + msg.Latest + " available — run /update or pitago --update to install"
		if len(m.blocks) == 0 {
			// fresh open: welcomeView already shows the banner, a block
			// would hide the logo — just repaint.
			m.Refresh()
			return
		}
		m.AddBlock(Block{Kind: "notice", Text: text})
		m.Refresh()
		return
	}
	d := &Dialog{
		Kind: "update", Title: "Update available",
		Message:  verOrDev(msg.Current) + " → " + msg.Latest + "\nTerminal: pitago --update",
		Options:  []string{"Install " + msg.Latest, "Later"},
		Descs:    []string{"download + replace binary, then restart", "dismiss"},
		UpdateTo: msg.Latest,
	}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.UpdateAvail = msg.Latest // keeps the welcome banner after Later
	m.Refresh()
}

// handleUpdateDone renders an install result.
func (m *Model) handleUpdateDone(msg UpdateDoneMsg) {
	m.Status = "ready"
	if msg.Err != nil {
		text := "update failed: " + msg.Err.Error()
		if errors.Is(msg.Err, update.ErrNeedSudo) && msg.Manual != "" {
			text += "\n" + msg.Manual
		} else if msg.Manual != "" {
			text += "\nfallback: " + msg.Manual
		}
		m.AddBlock(Block{Kind: "notice", Text: text, Err: true})
		m.Refresh()
		return
	}
	m.UpdateAvail = "" // installed — no more nag, just restart
	m.AddBlock(Block{Kind: "notice",
		Text: "updated " + verOrDev(msg.From) + " → " + msg.To + " — restart pitago to use it"})
	m.Refresh()
}

func verOrDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}
