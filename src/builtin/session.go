package builtin

import (
	"fmt"
	"strings"

	tea "pitago/src/tui/ev"

	"pitago/src/app"
	"pitago/src/components/format"
	"pitago/src/pirpc"
)

// loadSession builds pi's /session block: Session Info + Messages +
// Tokens + Cost (with per-model breakdown). Layout and thresholds mirror
// pi's handleSessionCommand; only Cache Warming is omitted (pi's RPC
// exposes no warming mode/status) and Cache Re-billed (needs pi's model
// cost tables, not just entry usage).
func loadSession(m *app.Model) tea.Cmd {
	return func() tea.Msg {
		st, err := m.Pi.GetState()
		if err != nil {
			return app.SessionMsg{Err: err}
		}
		stats, _ := m.Pi.GetStats()
		entries, _ := m.Pi.GetEntries()
		file := stats.SessionFile
		if file == "" {
			file = st.SessionFile
		}
		if file == "" {
			file = "In-memory"
		}
		br := pirpc.UsageBreakdown(entries)
		return app.SessionMsg{Text: sessionText(st.SessionName, file, stats, br), Break: br}
	}
}

// sessionText renders the block (pi's Plain-text lines; styling happens in
// the session view case: bold headers, dim labels).
func sessionText(name, file string, s pirpc.Stats, br []pirpc.CostBreak) string {
	var b strings.Builder
	b.WriteString("Session Info\n\n")
	if name != "" {
		b.WriteString("Name: " + name + "\n")
	}
	b.WriteString("File: " + file + "\n")
	b.WriteString("ID: " + s.SessionID + "\n\n")
	b.WriteString("Messages\n")
	b.WriteString("Total: " + format.FmtComma(s.TotalMessages) + "\n")
	b.WriteString("User: " + format.FmtComma(s.UserMsgs) + "\n")
	b.WriteString("Assistant: " + format.FmtComma(s.AsstMsgs) + "\n")
	fmt.Fprintf(&b, "Tools: %d calls, %d results\n\n", s.ToolCalls, s.ToolResults)
	b.WriteString("Tokens\n")
	prompt := s.In + s.CacheRead + s.CacheWrite
	b.WriteString("Input: " + format.FmtComma(prompt) + "\n")
	if prompt > 0 && (s.CacheRead > 0 || s.CacheWrite > 0) {
		rate := float64(s.CacheRead) / float64(prompt) * 100
		fmt.Fprintf(&b, "  Cached: %s (%.1f%%)\n", format.FmtComma(s.CacheRead), rate)
		uncached := "  Uncached: " + format.FmtComma(s.In+s.CacheWrite)
		if s.CacheWrite > 0 {
			uncached += fmt.Sprintf(" (%s written to cache)", format.FmtComma(s.CacheWrite))
		}
		b.WriteString(uncached + "\n")
	}
	b.WriteString("Output: " + format.FmtComma(s.Out) + "\n")
	b.WriteString("Total: " + format.FmtComma(s.TokensTotal) + "\n")
	if s.Cost > 0 {
		b.WriteString("\nCost\n")
		fmt.Fprintf(&b, "Total: $%.3f", s.Cost)
		if len(br) > 1 {
			for _, e := range br {
				fmt.Fprintf(&b, "\n  %s: $%.3f (%s tokens)", e.Key, e.Cost, shortToks(e.Tokens))
			}
		}
	}
	return b.String()
}

// shortToks compacts token counts like pi's formatTokens (1.2k, 3M).
func shortToks(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", n/1000)
	case n < 10000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	default:
		return fmt.Sprintf("%dM", n/1000000)
	}
}
