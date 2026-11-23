package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"pitago/src/pirpc"
	"pitago/src/tui/ev"
)

// The specialized document views own these screens instead of the generic
// tview list. Everything else can use the same reducer-backed row selection.
func isDocumentDialog(d *Dialog) bool {
	switch d.Kind {
	case focusKind, treeKind, treeSummaryKind, treeCustomKind, oauthKind, "team":
		return true
	case "subagent-herd":
		return d.SubDetail
	}
	return false
}

func nativeEditKind(kind string) bool {
	switch kind {
	case "secret", "rename", "input", "subagents-steer", suggestAddKind, McpAddKind:
		return true
	}
	return false
}

func (m Model) nativeDialog(d *Dialog, v ev.SettingsView) ev.SettingsView {
	if d.Kind == "login" && len(d.Provs) > 0 {
		return m.nativeLogin(d, v)
	}
	v.Focus = ev.SettingsValues
	if !filterableDialog(d) {
		v.FilterLabel = ""
	}
	v.ApplyLabel = "Select"
	if nativeEditKind(d.Kind) {
		v.Edit, v.Mask = true, d.Kind == "secret"
		v.FilterLabel, v.Placeholder = "Input: ", d.Placeholder
		v.Help, v.ApplyLabel = "Enter submit · Esc cancel", "Submit"
		return v
	}
	if d.Kind == shortcutKind {
		v.Help = "Alt+key assign · Backspace clear · Esc cancel"
		v.Details = "Current: " + OrDefault(shortcutDisplay(m.shortcutForCmd(d.ShortcutCmd)), "— none —")
		return v
	}
	rows := ev.SettingsPane{Title: "Options", Selected: psecCursor(d)}
	for _, i := range d.FIdx {
		label := ansi.Strip(d.Options[i])
		if d.Kind == "askUser" {
			label = fmt.Sprintf("%d. %s", i+1, label)
		}
		if (d.Kind == "pet" && d.Current == d.Options[i]) || (d.Kind == "sessions" && strings.HasPrefix(DescOf(d, i), "current")) {
			label = "● " + label
		}
		row := ev.SettingsRow{Index: i, Label: label}
		if !longDetails(d.Kind) {
			row.Description = ansi.Strip(DescOf(d, i))
		}
		rows.Rows = append(rows.Rows, row)
	}
	v.Panes = []ev.SettingsPane{{}, rows}
	v.Help = "↑↓ move · Enter select · Esc close"
	if i := rows.Selected; i >= 0 {
		v.Details = ansi.Strip(DescOf(d, i))
	}
	switch d.Kind {
	case "sessions":
		v.Help = "↑↓ move · Enter resume · Tab scope · Delete session · Esc close"
		v.Message = "Scope: " + d.Scope
	case "trajectory":
		v.Help = "↑↓ steps · PgUp/PgDn / wheel detail · Enter view · Ctrl+Y copy · Esc close"
		rows := trajDetailRows(trajSelected(d), 38)
		var lines []string
		for _, row := range rows {
			lines = append(lines, ansi.Strip(row.text))
		}
		v.Details = strings.Join(lines[min(max(d.TrajOff, 0), max(0, len(lines)-1)):], "\n")
	case "askUser":
		v.Help = "↑↓ choose · Enter confirm · Esc cancel"
	case "quota":
		// The list names the providers; the details pane draws the selected
		// one as the same panel the sidebar shows (title, bars, verdict).
		v.Help = "↑↓ move · Enter refetch · Esc close"
		for i := range rows.Rows {
			if p := rows.Rows[i].Index; p < len(m.quota) && p < len(d.Providers) {
				rows.Rows[i].Label = m.quota[p].Label
			}
		}
		v.Panes = []ev.SettingsPane{{}, rows}
		v.Details = ""
		if sel := rows.Selected; sel >= 0 && sel < len(d.Providers) {
			for _, q := range m.quota {
				if q.Provider == d.Providers[sel] {
					v.Details = quotaPanelText(q, max(30, (m.winW-6)/2-4))
				}
			}
		}
	case "pet":
		if rows.Selected >= 0 {
			v.Details = strings.Join(petDemoLines(d.Options[rows.Selected], m.petShown().Name), "\n")
		}
	case "mcp":
		v.Help = mcpFooter(d)
		v.LocalKeys = "? "
		if len(d.Options) == 0 && v.Message == "" {
			v.Message = "No MCP servers configured"
		}
	case "mcpAction", "mcpExposure", "mcpTools":
		v.Help = mcpFooter(d)
	case "subagent-herd":
		v.Message = "Scope: " + d.Scope
		v.Help = "↑↓ move · Enter open · Tab scope · Esc close"
	}
	v.WideDetails = longDetails(d.Kind)
	v.ScrollDetails = v.WideDetails && d.Kind != "trajectory"
	return v
}

func longDetails(kind string) bool {
	switch kind {
	case "sessions", "trajectory", "askUser", "pet", "quota":
		return true
	}
	return false
}

func (m Model) nativeLogin(d *Dialog, v ev.SettingsView) ev.SettingsView {
	// Same bare shape as the model picker: one frame, borderless columns,
	// headers in-line, a filter line and one help line.
	v.Bare = true
	v.FilterLabel = "filter: "
	v.Details = ""
	left := ev.SettingsPane{Title: "PROVIDERS", Selected: -1}
	for pos, i := range d.PIdx {
		id := d.Provs[i]
		dot := "○ "
		if d.ProvConn[id] {
			dot = "● "
		}
		count := "no key"
		switch n := d.LoginCounts[id]; {
		case n == 1:
			count = "1 key"
		case n > 1:
			count = fmt.Sprintf("%d keys", n)
		}
		if d.OAuthConn[id] {
			if count == "no key" {
				count = "OAuth"
			} else {
				count += "+OAuth"
			}
		}
		left.Rows = append(left.Rows, ev.SettingsRow{Index: pos, Label: dot + pirpc.ProviderLabel(id), Suffix: count})
	}
	if len(left.Rows) > 0 {
		left.Selected = d.ProvCursor
	}
	// right header names the selected provider and its key count, as the
	// painted picker did (KEYS when nothing is selected).
	right := ev.SettingsPane{Title: "KEYS", Selected: -1}
	if prov := d.SelLoginProv(); prov != "" {
		title := pirpc.ProviderLabel(prov)
		switch n := d.LoginCounts[prov]; {
		case n == 1:
			title += " · 1 key"
		case n > 1:
			title += fmt.Sprintf(" · %d keys", n)
		}
		if d.LoginOAuth {
			title += " · OAuth ✓"
		}
		right.Title = title
	}
	for i, o := range d.Options {
		right.Rows = append(right.Rows, ev.SettingsRow{Index: i, Label: ansi.Strip(o), Description: ansi.Strip(DescOf(d, i))})
	}
	if len(right.Rows) > 0 {
		right.Selected = d.KeyCursor
	}
	v.Panes = []ev.SettingsPane{left, right}
	v.Help = "↑↓ move · ←→/Tab switch · Enter use/add · ⌫ del · s show · r rename · ^P models · Esc close"
	if d.ProvFocus {
		v.Help = "↑↓ providers · → keys · type to filter · Enter open · Esc close"
	}
	v.ApplyLabel = "Select"
	if !d.ProvFocus {
		v.LocalKeys = "sSrR "
	}
	return v
}

func (m Model) updateNativeLoginPointer(d *Dialog, p ev.SettingsPointerMsg) (ev.Model, ev.Cmd) {
	d.ProvFocus = p.Column == ev.SettingsSections
	if p.Step != 0 {
		return m.updateLoginDialog(settingsStep(p.Step), d)
	}
	if d.ProvFocus {
		if p.Index >= 0 && p.Index < len(d.PIdx) {
			d.ProvCursor, d.KeyCursor = p.Index, 0
			m.RefreshLoginKeys(d)
		}
	} else if p.Index >= 0 && p.Index < len(d.Options) {
		d.KeyCursor = p.Index
		if p.Activate {
			return m.confirmLoginKey(d)
		}
	}
	return m, nil
}
