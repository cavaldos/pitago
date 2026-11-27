package app

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
	tea "pitago/src/tui/ev"
)

// ModelProbeMsg reports one Ctrl+T probe from the model picker.
type ModelProbeMsg struct {
	Key   string // probe key: provider + "/" + id
	Label string
	Err   error
	Dur   time.Duration
}

// ModelProbe is one Ctrl+T probe result, shown under the picker details.
type ModelProbe struct {
	Ok      bool
	Pending bool // probe in flight: the details column shows "calling…"
	Err     string
	Dur     time.Duration
}

// probeKey builds the Model.Probes key for a model.
func probeKey(prov, id string) string {
	return prov + "/" + id
}

// probeModelCmd runs `pi -p -ne -ns -np -nc --no-mcp --no-tools
// --no-session --system-prompt "" --model provider/id -- "Reply with
// exactly: ok"`: a throwaway subprocess with the coding setup stripped,
// so the probe exercises the real endpoint cheaply without touching the
// live session (no transcript entry, no model switch, no session file).
func (m Model) probeModelCmd(prov, id, label string) tea.Cmd {
	bin := m.piBin()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		args := []string{"-p", "-ne", "-ns", "-np", "-nc", "--no-mcp", "--no-tools",
			"--no-session", "--system-prompt", "", "--model", prov + "/" + id,
			"--", "Reply with exactly: ok"}
		start := time.Now()
		cmd := exec.CommandContext(ctx, bin, args...)
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = fmt.Errorf("timed out after 60s")
			}
			return ModelProbeMsg{Key: probeKey(prov, id), Label: label, Dur: dur,
				Err: fmt.Errorf("%w: %s", err, oneLineTail(string(out)))}
		}
		return ModelProbeMsg{Key: probeKey(prov, id), Label: label, Dur: dur}
	}
}

// oneLineTail keeps the last non-empty output line: pi print errors are
// short ("no auth for provider …"), a full capture would flood the toast.
func oneLineTail(s string) string {
	s = strings.TrimRight(s, " \t\n\r")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// fmtProbeDur renders a probe duration for the details column.
func fmtProbeDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

// modelProbeTarget resolves the highlighted picker row to a probe target.
// ri < 0 when nothing is highlighted (empty or fully filtered list).
func (d *Dialog) modelProbeTarget() (prov, id, label string) {
	ri := -1
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		ri = d.FIdx[d.Cursor]
	}
	mi, ok := d.modelAt(ri)
	if !ok {
		return "", "", ""
	}
	prov, id = mi.Provider, mi.ID
	if prov == "" || id == "" {
		return "", "", ""
	}
	label = id
	if mi.Name != "" {
		label = mi.Name
	}
	return prov, id, label
}

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
// In/Out · Cache · Thinking · Input · Key · Test. One dim placeholder when
// the dialog carries no specs, so callers can pad to a fixed height.
// probes is the picker's last Ctrl+T results (nil ok).
func (d *Dialog) detailLines(w int, probes map[string]ModelProbe) []string {
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
	lines = append(lines, "  "+keyLab+" "+keyStyle.Render(Fit(keyVal, valW)))
	if pr, ok := probes[probeKey(prov, mi.ID)]; ok {
		lab := toolStyle.Render(Fit("Test", lw))
		val, style := "", warnStyle
		switch {
		case pr.Pending:
			val, style = "calling…", warnStyle
		case pr.Ok:
			val, style = "ok "+fmtProbeDur(pr.Dur), okStyle
		default:
			val, style = "failed: "+pr.Err, warnStyle
		}
		lines = append(lines, "  "+lab+" "+style.Render(Fit(Short(val, valW), valW)))
	}
	return lines
}
