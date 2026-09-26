package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// modelAt reads the full specs for option i (false when the dialog was
// built without Models, e.g. tests or non-model pickers).
func (d *Dialog) modelAt(i int) (pirpc.ModelInfo, bool) {
	if i < 0 || i >= len(d.Models) || i >= len(d.Options) {
		return pirpc.ModelInfo{}, false
	}
	return d.Models[i], true
}

// specHay is the extra searchable text for a model row: API id, input
// kinds and host, so typing "image" or "anthropic-messages" finds models.
func (d *Dialog) specHay(i int) string {
	mi, ok := d.modelAt(i)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(mi.API) + " ")
	b.WriteString(strings.ToLower(strings.Join(mi.Input, " ")) + " ")
	b.WriteString(strings.ToLower(modelHost(mi.BaseURL)))
	return b.String()
}

// modelHost strips a base URL down to its host ("https://api.x.com/v1" →
// "api.x.com"). "" when unknown.
func modelHost(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// modelCostPair formats "$in / $out per 1M", "—" when both are zero.
func modelCostPair(a, b float64) string {
	if a == 0 && b == 0 {
		return "—"
	}
	return fmt.Sprintf("$%g / $%g per 1M", a, b)
}

// modelThinking lists the usable thinking levels ("low, medium, high").
// Reasoning-only models without a map report "on"; the rest "—".
func modelThinking(mi pirpc.ModelInfo) string {
	if len(mi.ThinkingLevelMap) > 0 {
		order := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
		var out []string
		for _, l := range order {
			if v, ok := mi.ThinkingLevelMap[l]; ok && v != nil {
				out = append(out, l)
			}
		}
		for l, v := range mi.ThinkingLevelMap {
			if v == nil {
				continue
			}
			found := false
			for _, o := range out {
				if o == l {
					found = true
					break
				}
			}
			if !found {
				out = append(out, l)
			}
		}
		sort.Strings(out)
		if len(out) > 0 {
			return strings.Join(out, ", ")
		}
	}
	if mi.Reasoning {
		return "on"
	}
	return "—"
}

// orDash blanks an empty string.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// detailLines builds the highlighted model's spec lines, each exactly w
// cells wide: title + Name · Provider · API · Host · Context · Max output ·
// In/Out · Cache · Thinking · Input · Key. One dim placeholder when the
// dialog carries no specs, so callers can pad to a fixed height.
func (d *Dialog) detailLines(w int) []string {
	ri := -1
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		ri = d.FIdx[d.Cursor]
	}
	mi, ok := d.modelAt(ri)
	if w < 30 {
		w = 30
	}
	if !ok {
		return []string{"  " + toolStyle.Width(w-2).Render("— no specs —")}
	}
	var lines []string
	title := mi.ID
	if title == "" && ri < len(d.Options) {
		title = d.Options[ri]
	}
	lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(title, w-2)))
	prov := mi.Provider
	if prov == "" && ri < len(d.Providers) {
		prov = d.Providers[ri]
	}
	provVal := prov
	if lbl := pirpc.ProviderLabel(prov); lbl != "" && lbl != prov {
		provVal = lbl + " (" + prov + ")"
	}
	ctxVal, maxVal := "—", "—"
	if mi.ContextWindow > 0 {
		ctxVal = FmtNum(mi.ContextWindow)
	}
	if mi.MaxTokens > 0 {
		maxVal = FmtNum(mi.MaxTokens)
	}
	inVal := "—"
	if len(mi.Input) > 0 {
		inVal = strings.Join(mi.Input, ", ")
	}
	rows := [][2]string{
		{"Name", orDash(mi.Name)},
		{"Provider", orDash(provVal)},
		{"API", orDash(mi.API)},
		{"Host", orDash(modelHost(mi.BaseURL))},
		{"Context", ctxVal},
		{"Max output", maxVal},
		{"In / Out", modelCostPair(mi.Cost.Input, mi.Cost.Output)},
		{"Cache r/w", modelCostPair(mi.Cost.CacheRead, mi.Cost.CacheWrite)},
		{"Thinking", modelThinking(mi)},
		{"Input", inVal},
	}
	const lw = 11
	valW := w - 2 - lw - 1
	if valW < 10 {
		valW = 10
	}
	for _, r := range rows {
		lab := toolStyle.Render(Fit(r[0], lw))
		val := r[1]
		style := lipgloss.NewStyle().Foreground(cText)
		if val == "—" {
			style = statusBarStyle
		} else if r[0] == "Thinking" {
			style = warnStyle
		}
		lines = append(lines, "  "+lab+" "+style.Render(Fit(Short(val, valW), valW)))
	}
	keyLab := toolStyle.Render(Fit("Key", lw))
	keyVal, keyStyle := "missing", warnStyle
	if d.ProvConn[normProv(prov)] {
		keyVal, keyStyle = "ready", okStyle
	}
	return append(lines, "  "+keyLab+" "+keyStyle.Render(Fit(keyVal, valW)))
}
