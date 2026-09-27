package app

import (
	"strings"
)

// Recently used /commands, floated to the top of the "/" popup.
//
// Recency is recorded when a command is actually *run* (submit), not when
// the popup merely stages one — so browsing with Tab or an Alt+shortcut
// that you then abandoned never counts. Persisted in prefs.json like
// CmdShortcuts, so the top rows survive a restart.

const (
	// recentCmdsKeep is how many commands prefs.json remembers. Kept well
	// above recentCmdsFloat so the displayed count can change later
	// without losing history.
	recentCmdsKeep = 10
	// recentCmdsFloat is how many recent commands lead the popup.
	recentCmdsFloat = 4
)

// noteCmdUse records one command run, most-recent-first and deduped
// (case-insensitive: "/Model" and "/model" are the same command). The
// prefs write is the same LoadPrefs → mutate → SavePrefs dance as
// setCmdShortcut, and is skipped when prefs are not configured.
func (m *Model) noteCmdUse(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	out := []string{name}
	for _, n := range m.RecentCmds {
		if len(out) >= recentCmdsKeep {
			break
		}
		if !strings.EqualFold(strings.TrimSpace(n), name) {
			out = append(out, strings.TrimSpace(n))
		}
	}
	m.RecentCmds = out
	prefs := LoadPrefs(m.prefsPath)
	prefs.RecentCmds = out
	_ = SavePrefs(m.prefsPath, prefs)
}

// floatRecentCmds moves up to recentCmdsFloat recently used commands to
// the front of the current match set. Only reorders: every row still has
// to match the typed prefix (a recent "/model" never appears under "/the"),
// the rest keep their catalog order, and a name maps to at most one row
// (the catalog is deduped by name, but a hand-merged list is not assumed).
//
// Pointer receiver on purpose: it rewrites m.cmdItems in place, and a
// value receiver would mutate a discarded copy and do nothing.
func (m *Model) floatRecentCmds() {
	if len(m.RecentCmds) == 0 || len(m.cmdItems) <= 1 {
		return
	}
	var recent, rest []int
	moved := map[int]bool{}
	for _, name := range m.RecentCmds {
		if len(recent) >= recentCmdsFloat {
			break
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, item := range m.cmdItems {
			if moved[item] {
				continue
			}
			if item < 0 || item >= len(m.Cmds) {
				continue // stale index: catalog shrank under us
			}
			if strings.EqualFold(m.Cmds[item].Name, name) {
				recent = append(recent, item)
				moved[item] = true
				break
			}
		}
	}
	if len(recent) == 0 {
		return
	}
	for _, item := range m.cmdItems {
		if !moved[item] {
			rest = append(rest, item)
		}
	}
	m.cmdItems = append(recent, rest...)
}
