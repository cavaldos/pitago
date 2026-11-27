package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"pitago/src/components/theme"
	tea "pitago/src/tui/ev"
)

func (m Model) NativePalette() tea.NativePalette {
	t := theme.Get(m.ThemeName)
	// A palette that ships its own surface (the TTT set) paints it
	// whenever it is the picked background, like Model.bgColor.
	surface := theme.BackgroundHex(m.Background, t.Light)
	if m.Background == bgOwn {
		surface = t.Bg
	}
	return tea.NativePalette{
		Surface: surface,
		Text:    t.Text, Muted: t.Muted, Accent: t.Accent, Border: t.Border,
		HiBg: t.HiBg, HiFg: t.HiFg, Green: t.Green, Red: t.Red,
	}
}

func (m Model) settingsDialog() *Dialog {
	if len(m.Dialogs) == 0 || m.nativePickerOpen() || m.isInlineUI() || m.inputOpen() {
		return nil
	}
	d := m.Dialogs[0]
	if isDocumentDialog(d) {
		return nil
	}
	return d
}

// PaintedDialog reports that the top dialog is drawn by the old painted
// renderer (model.View → renderDialog) instead of a native tview view.
// tviewrt's frame skips the native panel and workspace for that frame.
func (m Model) PaintedDialog() bool {
	if m.settingsDialog() == nil {
		return false
	}
	switch m.Dialogs[0].Kind {
	case "trajectory", "sessions", "notification": // trace window, /resume and /notification keep the painted two-column look
		return true
	case "btw": // the BTW window is a compact painted box (see btw_window.go)
		return true
	}
	return false
}

func (m Model) NativeSettings() (tea.SettingsView, bool) {
	d := m.settingsDialog()
	if d == nil {
		return tea.SettingsView{}, false
	}
	v := tea.SettingsView{Kind: d.Kind, Title: d.Title, Message: strings.TrimSpace(ansi.Strip(d.Message)), Filter: d.Filter,
		FilterLabel: "Filter: ", Status: m.Status, Focus: tea.SettingsValues}
	if d.ProvFocus && len(d.Provs) > 0 {
		v.Focus = tea.SettingsSections
	} else if d.CurPsec() == PsecTheme && d.BgFocus {
		v.Focus = tea.SettingsBackgrounds
	}
	switch d.Kind {
	case "model":
		return m.nativeModelPicker(d, v), true
	case "settings", "pconfig":
		// Same bare shape as the model picker: one frame, borderless
		// columns, headers in-line, a filter line and one help line.
		v.Bare = true
		v.FilterLabel = "filter: "
		if d.CurPsec() == PsecMarket {
			// the marketplace filter is a remote npm search, not a local one
			v.FilterLabel = "search: "
		}
		v.DetailsTitle = "DETAILS"
		return m.nativeSettingsHub(d, v), true
	case "trajectory", "sessions", "notification":
		// Painted on purpose: these keep the old bubble-tea look —
		// title/filter header, borderless list | DETAIL columns and the
		// hint footer — drawn by renderTraceDialog/renderResumeDialog/
		// renderNotificationDialog over the normal frame (keys and wheel
		// reach the model's dialog path, so Ctrl+Y copy works).
		return tea.SettingsView{}, false
	case "btw":
		// Painted on purpose: the BTW window is a compact centered box
		// (renderBtwDialog). The generic native mirror would turn it into
		// a fullscreen "Options · no matches" view — useless for a window
		// whose content is a transcript plus an input row.
		return tea.SettingsView{}, false
	default:
		return m.nativeDialog(d, v), true
	}
}

// nativeSettingsHub builds the bare two/three-column snapshot shared by the
// agent settings and the Pitago settings hub: groups/sections on the left,
// the selected section's rows on the right, plus a background column on the
// Theme section and a DETAILS column for the marketplace and plugins.
func (m Model) nativeSettingsHub(d *Dialog, v tea.SettingsView) tea.SettingsView {
	section := d.CurPsec()
	sections := tea.SettingsPane{Selected: d.ProvCursor}
	if d.Kind == "settings" {
		sections.Title = "GROUPS"
	} else {
		sections.Title = "SECTIONS"
	}
	for i, label := range d.Provs {
		if i < len(d.PsecIDs) {
			if n := psecCount(&m, d.PsecIDs[i]); n >= 0 {
				label = fmt.Sprintf("%s · %d", label, n)
			}
		}
		sections.Rows = append(sections.Rows, tea.SettingsRow{Index: i, Label: label})
	}
	// Right header names the selected group/section and its row count,
	// as the painted pickers did (sections uppercase on the Pitago hub).
	values := tea.SettingsPane{Selected: psecCursor(d)}
	if len(d.Provs) > 0 && d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) {
		name := d.Provs[d.ProvCursor]
		if d.Kind == "pconfig" {
			name = strings.ToUpper(name)
		}
		values.Title = fmt.Sprintf("%s · %d", name, len(d.FIdx))
	}
	if d.Kind == "settings" && d.ProvFocus && d.Filter != "" {
		values.Title = "Matches across sections"
	}
	backgrounds := tea.SettingsPane{Title: "BACKGROUND", Selected: d.BgRow}
	if section == PsecTheme {
		values.Selected = d.PalRow
		if d.BgFocus {
			backgrounds.Selected = psecCursor(d)
		} else {
			values.Selected = psecCursor(d)
		}
	}
	for _, i := range d.FIdx {
		label := ansi.Strip(d.Options[i])
		// Every section's hint rides the row (the painted hub drew the same
		// Descs in its description column); Side and Tasks move their state
		// token into the label instead, so only the rest is left.
		desc := ansi.Strip(DescOf(d, i))
		switch section {
		case PsecTheme:
			if p := payloadOf(d, i); p == "theme:"+m.ThemeName || p == psecActBg+m.Background {
				label = "✓ " + label
			}
			if isBgRow(d, i) {
				backgrounds.Rows = append(backgrounds.Rows, tea.SettingsRow{Index: i, Label: label})
				continue
			}
		case PsecMarket:
			if i < len(m.Market) {
				if chip := marketChip(m.Market[i]); chip != "" {
					label += "  " + chip
				}
				if marketInstalled(&m, m.Market[i].Name) {
					label += "  ✓"
				}
			}
		case PsecSide, PsecTasks:
			if value, rest, ok := strings.Cut(desc, " · "); ok {
				label += ": " + value
				desc = rest
			}
		}
		values.Rows = append(values.Rows, tea.SettingsRow{Index: i, Label: label, Description: desc})
	}
	v.Panes = []tea.SettingsPane{sections, values}
	if section == PsecTheme {
		// The theme header counts only the palettes, like the hub did.
		if n := len(d.FIdx) - len(backgrounds.Rows); n > 0 {
			values.Title = fmt.Sprintf("THEME · %d", n)
		}
		v.Panes = append(v.Panes, backgrounds)
	}
	// The painted hubs showed market/plugin specs in a DETAILS column and
	// nothing else; the row hints already carry every other section's desc.
	v.Details = ""
	switch ri := psecCursor(d); {
	case section == PsecMarket && ri >= 0 && ri < len(m.Market):
		v.WideDetails, v.ScrollDetails = true, true
		v.Details = settingsDetails(marketDetailLines(&m, d, 42))
	case section == PsecPlugin && ri >= 0 && ri < len(m.Plugins):
		v.WideDetails, v.ScrollDetails = true, true
		v.Details = settingsDetails(pluginDetailLines(&m, d, 42))
	}
	v.Help = hubHelp(d, section)
	return v
}

// hubHelp is the single help line the painted settings hubs showed.
func hubHelp(d *Dialog, section string) string {
	if d.Kind == "settings" {
		if d.ProvFocus {
			return "↑↓ groups · → settings · type searches all · Enter change · Esc close"
		}
		foot := "↑↓ settings · ← groups · Tab switch · type filters here · Enter change · Esc close"
		if n := len(d.FIdx); n > 0 {
			cur := d.Cursor + 1
			if cur > n {
				cur = n
			}
			foot += fmt.Sprintf(" (%d/%d)", cur, n)
		}
		return foot
	}
	if d.ProvFocus {
		return "↑↓ sections · → contents · Enter open · Esc close"
	}
	switch section {
	case PsecMarket:
		return "↑↓ select · ← sections · Tab switch · type to search npm · Enter install · Esc clears"
	case PsecTheme:
		return "↑↓ previews · ←/→ switch column · Enter applies · type filters · Esc close"
	}
	return "↑↓ select · ← sections · Tab switch · type filters · Enter run · Esc close"
}

func (m Model) nativeModelPicker(d *Dialog, v tea.SettingsView) tea.SettingsView {
	// One frame, three borderless columns and a help line: the layout the
	// painted picker had, minus its two-pane fallback.
	v.Bare = true
	v.FilterLabel = "filter: "
	v.DetailsTitle = "DETAILS"
	v.ApplyLabel = "Select"
	providers := tea.SettingsPane{Title: "PROVIDERS", Selected: d.ProvCursor}
	counts := make(map[string]int, len(d.Provs))
	f := strings.ToLower(d.Filter)
	for i, option := range d.Options {
		provider := normProv(providerAt(d.Providers, i))
		if f == "" || strings.Contains(strings.ToLower(option), f) ||
			strings.Contains(strings.ToLower(DescOf(d, i)), f) ||
			strings.Contains(strings.ToLower(provider), f) || strings.Contains(d.specHay(i), f) {
			counts[provider]++
			counts["All"]++
		}
	}
	for i, provider := range d.Provs {
		label := provider
		if provider != "All" {
			dot := "○ "
			if d.ProvConn[provider] {
				dot = "● "
			}
			label = dot + label
		}
		providers.Rows = append(providers.Rows, tea.SettingsRow{Index: i, Label: label, Suffix: fmt.Sprint(counts[provider])})
	}
	// The header names the scope of the list, as the painted picker did:
	// "All" while nothing narrows it (the providers pane reads globally).
	models := tea.SettingsPane{Title: fmt.Sprintf("All · %d", len(d.FIdx)), Selected: psecCursor(d)}
	if scoped := d.selProv(); scoped != "" && (!d.ProvFocus || f == "") {
		models.Title = fmt.Sprintf("%s · %d", scoped, len(d.FIdx))
		if len(d.FIdx) == 0 && !d.ProvConn[scoped] {
			v.Message = "not connected · ^L login"
		}
	}
	for _, i := range d.FIdx {
		star := "☆"
		if d.isFavIdx(i) {
			star = "★"
		}
		models.Rows = append(models.Rows, tea.SettingsRow{Index: i, Label: ansi.Strip(d.Options[i]), Suffix: star})
	}
	v.Panes = []tea.SettingsPane{providers, models}
	v.Details = settingsDetails(d.detailLines(60, m.Probes))
	v.WideDetails, v.ScrollDetails = true, true
	v.Help = "↑↓ models · ← providers · Tab switch · type filters here · Enter select · ^F star · ^T test · ^L login · Esc close"
	if d.ProvFocus {
		v.Help = "↑↓ providers · → models · type to search all · Enter open · ^F star · ^T test · ^L login · Esc close"
	}
	return v
}

func settingsDetails(lines []string) string {
	for i, line := range lines {
		lines[i] = strings.TrimSpace(ansi.Strip(line))
	}
	return strings.Join(lines, "\n")
}

func (m Model) updateNativeSettingsFilter(d *Dialog, text string) (tea.Model, tea.Cmd) {
	d.Filter = text
	if nativeEditKind(d.Kind) {
		m.applyPopupH()
		return m, nil
	}
	d.Cursor = 0
	if d.Kind == "login" && len(d.Provs) > 0 {
		d.ProvFocus, d.ProvCursor, d.KeyCursor = true, 0, 0
		d.Reindex()
		m.RefreshLoginKeys(d)
		return m, nil
	}
	d.Reindex()
	if d.CurPsec() == PsecTheme {
		gotoCol(d, d.BgFocus)
	}
	return m, m.marketSearchTick()
}

func (m Model) updateNativeSettingsPointer(d *Dialog, p tea.SettingsPointerMsg) (tea.Model, tea.Cmd) {
	if d.Kind == "login" && len(d.Provs) > 0 {
		return m.updateNativeLoginPointer(d, p)
	}
	switch d.Kind {
	case "settings", "pconfig", "model":
	default:
		if p.Step != 0 {
			return m.updateDialog(settingsStep(p.Step))
		}
		if i := colFIdxIndex(d, p.Index); i >= 0 && p.Column == tea.SettingsValues {
			d.Cursor, d.TrajOff = i, 0
			if p.Activate {
				return m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
			}
		}
		return m, nil
	}
	if p.Column == tea.SettingsSections {
		if p.Step != 0 {
			d.ProvFocus = true
			return m.updateDialog(settingsStep(p.Step))
		}
		if p.Index < 0 || p.Index >= len(d.Provs) {
			return m, nil
		}
		changed := d.ProvCursor != p.Index
		d.ProvFocus = true
		if d.Kind == "pconfig" {
			if changed {
				cmd := m.selectPconfigSection(d, p.Index)
				return m, cmd
			}
		} else {
			d.ProvCursor = p.Index
			d.Reindex()
			d.Cursor = 0
		}
		return m, nil
	}
	if p.Column != tea.SettingsValues && (p.Column != tea.SettingsBackgrounds || d.CurPsec() != PsecTheme) {
		return m, nil
	}
	bg := p.Column == tea.SettingsBackgrounds
	if p.Step != 0 {
		wasFocused := d.ProvFocus
		d.ProvFocus = false
		if d.CurPsec() == PsecTheme && d.BgFocus != bg {
			gotoCol(d, bg)
		}
		if wasFocused && d.Kind != "pconfig" {
			d.Reindex()
		}
		return m.updateDialog(settingsStep(p.Step))
	}
	if colFIdxIndex(d, p.Index) < 0 || (d.CurPsec() == PsecTheme && isBgRow(d, p.Index) != bg) {
		return m, nil
	}
	if d.Kind != "pconfig" && d.ProvFocus && d.Filter != "" {
		// Search from Sections/Providers is global. Enter the clicked match's group
		// before focusing its row, so it stays visible when the list scopes.
		if group := normProv(providerAt(d.Providers, p.Index)); group != "" {
			for i, name := range d.Provs {
				if name == group {
					d.ProvCursor = i
					break
				}
			}
		}
		d.ProvFocus = false
		d.Reindex()
	}
	i := colFIdxIndex(d, p.Index)
	if i < 0 {
		return m, nil
	}
	d.ProvFocus, d.BgFocus, d.Cursor = false, bg, i
	if p.Activate {
		rememberColRow(d)
		return m.confirmDialog(d)
	}
	if d.Kind == "pconfig" {
		cmd := m.previewPconfigSelection(d)
		return m, cmd
	}
	return m, nil
}

func settingsStep(step int) tea.KeyMsg {
	if step < 0 {
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyDown}
}
