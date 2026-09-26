// welcomeView renders the pi-style startup header for an empty chat:
// logo + version + key hints + ready, then a Resources line built from
// what `pi --mode rpc` actually exposes (get_commands counts).
//
// Why not the exact pi numbers (system 1 · context 1 · tools 19 ·
// skills 53 · extensions 18 · themes 25)? Those come from pi-droid-styling's
// in-process patch (startup-ui.ts → session.resourceLoader), which never
// runs over RPC — the daemon only serves get_state/get_messages/
// get_session_stats/get_commands. So system/context/tools/themes counts
// are invisible here; we show ext/prompt/skill/builtin instead.
// Exact parity needs an upstream `get_resources` RPC method in pi.
package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/extension"
)

// brandLogo is "PITAGO" in 5-row block letters (1-col gaps, padded even).
var brandLogo = buildBrandLogo()

// brandGlyphs are the 5x5 block glyphs for P I T A G O.
var brandGlyphs = [][5]string{
	{"\n     ", "     ", "     ", "     ", "     "}, // empty
	{"█████", "█   █", "█████", "█    ", "█    "},   // P
	{"█████", "  █  ", "  █  ", "  █  ", "█████"},   // I
	{"█████", "  █  ", "  █  ", "  █  ", "  █  "},   // T
	{"█████", "█   █", "█████", "█   █", "█   █"},   // A
	{"█████", "█    ", "█  ██", "█   █", "█████"},   // G
	{"█████", "█   █", "█   █", "█   █", "█████"},   // O
}

func buildBrandLogo() []string {
	rows := make([]string, 5)
	for r := range rows {
		parts := make([]string, len(brandGlyphs))
		for i, g := range brandGlyphs {
			parts[i] = g[r]
		}
		rows[r] = strings.TrimRight(strings.Join(parts, " "), " ")
	}
	w := 0
	for _, ln := range rows {
		if dw := lipgloss.Width(ln); dw > w {
			w = dw
		}
	}
	for i, ln := range rows {
		rows[i] = ln + strings.Repeat(" ", w-lipgloss.Width(ln))
	}
	return rows
}

// welcomeView is the empty-chat startup header (pi compactHeader parity,
// monochrome: white like the rest of the pitago theme).
func (m Model) welcomeView(w int) string {
	logo := make([]string, len(brandLogo))
	for i, ln := range brandLogo {
		logo[i] = lipgloss.NewStyle().Foreground(cText).Render(ln)
	}
	title := sideTitleStyle.Render("Pitago")
	if v := strings.TrimPrefix(m.AppVersion, "v"); v != "" && v != "dev" {
		title += statusBarStyle.Render(" v" + v)
	}
	hints := statusBarStyle.Render("/ commands · ! bash · ctrl+o more")
	ready := okStyle.Render("●") + " " + sideTitleStyle.Render("ready")
	details := []string{title, hints, ready}
	if m.UpdateAvail != "" {
		details = append(details,
			warnStyle.Render("⬆ "+m.UpdateAvail+" available — /update or pitago --update"))
	}

	ext, prm, skl, bin := extension.Summarize(m.Cmds)
	res := sideTitleStyle.Render("◆") + " " +
		sideTitleStyle.Render("Resources") +
		statusBarStyle.Render("  ·  ") + statusBarStyle.Render("ext ") +
		sideTitleStyle.Render(itoa(ext)) +
		statusBarStyle.Render("  ·  prompt ") +
		sideTitleStyle.Render(itoa(prm)) +
		statusBarStyle.Render("  ·  skill ") +
		sideTitleStyle.Render(itoa(skl)) +
		statusBarStyle.Render("  ·  builtin ") +
		sideTitleStyle.Render(itoa(bin))
	newSess := okStyle.Render("✓") + " " + statusBarStyle.Render("New session started")

	logoW := lipgloss.Width(brandLogo[0])
	detailW := 0
	for _, d := range details {
		if dw := lipgloss.Width(d); dw > detailW {
			detailW = dw
		}
	}
	// Side-by-side only when the joined rows truly fit: the old fixed
	// +30 guess was narrower than the hints line, so medium terminals
	// wrapped mid-logo and the block letters came out garbled.
	if w >= logoW+3+detailW {
		// side-by-side like pi, details bottom-aligned with the logo so
		// the title sits one row down with a blank lead-in above it
		start := len(logo) - len(details)
		if start < 0 {
			start = 0
		}
		rows := make([]string, len(logo))
		for i := range logo {
			row := logo[i]
			if j := i - start; j >= 0 && j < len(details) {
				row += "   " + details[j]
			}
			rows[i] = row
		}
		rows = append(rows, "", res, "", newSess)
		return gutter(statusBarStyle.Render("●"), strings.Join(rows, "\n")+"\n")
	}
	rows := append(append(append(append([]string{}, logo...), ""), details...), "", res, "", newSess)
	return gutter(statusBarStyle.Render("●"), strings.Join(rows, "\n")+"\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	p := len(b)
	for n > 0 {
		p--
		b[p] = byte('0' + n%10)
		n /= 10
	}
	return string(b[p:])
}
