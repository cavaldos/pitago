// Package format groups the shared string/number helpers used across the
// TUI (sidebar rows, token counts, durations). Pure stdlib + lipgloss only.
package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// FmtDur formats durations like pi: 2m37s, 3m0s, 5s.
func FmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h, mm, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, mm)
	}
	if mm > 0 {
		return fmt.Sprintf("%dm%ds", mm, s)
	}
	return fmt.Sprintf("%ds", s)
}

// TwoCol pads a two-column sidebar row (left col 17 wide).
func TwoCol(l, r string, inner int) string {
	const lw = 17
	l, r = Short(l, lw-1), Short(r, inner-lw-1)
	p := lw - lipgloss.Width(l)
	if p < 1 {
		p = 1
	}
	return l + strings.Repeat(" ", p) + r
}

// CtxBar renders a fixed-width context usage bar like pi's session panel.
func CtxBar(pct float64, w int) string {
	if w < 1 {
		w = 1
	}
	f := int(pct/100*float64(w) + 0.5)
	if f > w {
		f = w
	}
	if f < 0 {
		f = 0
	}
	return strings.Repeat("█", f) + strings.Repeat("░", w-f)
}

func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func Short(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if lipgloss.Width(s) <= n {
		return s
	}
	return truncWidth(s, n)
}

// truncWidth cuts s to at most n display cells, reserving one cell for "…".
// Width-aware (no split multi-byte runes, no n+1 overflow like byte slicing).
func truncWidth(s string, n int) string {
	if n <= 1 {
		return "…"
	}
	var b strings.Builder
	cells := 0
	for _, r := range s {
		if c := lipgloss.Width(string(r)); cells+c > n-1 {
			break
		} else {
			cells += c
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

// Fit clamps s to exactly w display cells: truncates with "…" when too wide,
// pads with spaces when narrow. s must be unstyled (style after fitting) so
// no ANSI sequence can be cut in half. Guarantees rows never wrap and break
// the dialog layout.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if sw := lipgloss.Width(s); sw == w {
		return s
	} else if sw < w {
		return s + strings.Repeat(" ", w-sw)
	}
	return truncWidth(s, w)
}

func OrDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// StripANSI strips ANSI color codes (e.g. pi-lens extension status).
func StripANSI(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		if !in && s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			in = true
			i++
			continue
		}
		if in {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				in = false
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

func FmtNum(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func FmtComma(n int) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	r := len(s) % 3
	if r == 0 {
		r = 3
	}
	b.WriteString(s[:r])
	for i := r; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// CompactArgs collapses tool-call JSON args to one short line for display.
func CompactArgs(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// ToolPreview describes how to render a tool result like pi's TUI:
// read/write hide output on success, bash shows the last N lines,
// everything else shows the first N lines with a skipped count.
type ToolPreview struct {
	Hidden  bool
	Lines   []string
	Skipped int
	Tail    bool // true: tail lines (bash) → "earlier lines"; false → "more lines"
	Total   int  // total lines (for write-style "N more lines, T total" hints)
}

// Preview line budgets, mirroring pi's renderers.
const (
	bashPreviewLines  = 5
	grepPreviewLines  = 15
	headPreviewLines  = 20
	writePreviewLines = 10 // pi's write call shows 10 lines collapsed
	maxLineRunes      = 300
)

func truncLine(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func headPreview(lines []string, n int) ToolPreview {
	for i := range lines {
		lines[i] = truncLine(lines[i], maxLineRunes)
	}
	total := len(lines)
	if len(lines) <= n {
		return ToolPreview{Lines: lines, Total: total}
	}
	return ToolPreview{Lines: lines[:n], Skipped: len(lines) - n, Total: total}
}

func tailPreview(lines []string, n int) ToolPreview {
	for i := range lines {
		lines[i] = truncLine(lines[i], maxLineRunes)
	}
	total := len(lines)
	if len(lines) <= n {
		return ToolPreview{Lines: lines, Tail: true, Total: total}
	}
	return ToolPreview{Lines: lines[len(lines)-n:], Skipped: len(lines) - n, Tail: true, Total: total}
}

// ToolResultPreview splits a tool result into display lines like pi.
func ToolResultPreview(tool, status, result string) ToolPreview {
	return ToolResultPreviewExpanded(tool, status, result, false)
}

// ToolResultPreviewExpanded is ToolResultPreview with pi's ctrl+o toggle:
// expanded shows every line (no skip). read hides its file content until
// expanded (pi shows just the "read <path>" header collapsed); write/edit
// stay hidden on success because their interesting content (file text /
// diff) renders from the call args, not the "Successfully wrote…" receipt.
func ToolResultPreviewExpanded(tool, status, result string, expanded bool) ToolPreview {
	s := StripANSI(strings.ReplaceAll(result, "\r", ""))
	s = strings.ReplaceAll(s, "\t", "   ")
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return ToolPreview{Hidden: true}
	}
	switch strings.ToLower(tool) {
	case "read":
		if status != "error" {
			if !expanded {
				return ToolPreview{Hidden: true}
			}
			return allPreview(lines)
		}
		if expanded {
			return allPreview(lines)
		}
		return headPreview(lines, headPreviewLines)
	case "write", "edit":
		if status == "error" {
			if expanded {
				return allPreview(lines)
			}
			return headPreview(lines, headPreviewLines)
		}
		return ToolPreview{Hidden: true}
	case "bash", "powershell":
		if expanded {
			return allPreviewTail(lines)
		}
		return tailPreview(lines, bashPreviewLines)
	case "grep":
		if expanded {
			return allPreview(lines)
		}
		return headPreview(lines, grepPreviewLines)
	default:
		if expanded {
			return allPreview(lines)
		}
		return headPreview(lines, headPreviewLines)
	}
}

// allPreview shows every line (expanded state): no skip, no tail hint.
func allPreview(lines []string) ToolPreview {
	for i := range lines {
		lines[i] = truncLine(lines[i], maxLineRunes)
	}
	return ToolPreview{Lines: lines, Total: len(lines)}
}

// allPreviewTail is allPreview for tail-style tools (bash): the hint,
// if any, reads "earlier lines".
func allPreviewTail(lines []string) ToolPreview {
	p := allPreview(lines)
	p.Tail = true
	return p
}

// CallPreview splits a tool-call argument (write content) into display
// lines like pi's write renderer: 10 lines collapsed, all expanded, with
// the totals kept for the "... (N more lines, T total)" hint.
func CallPreview(content string, expanded bool) ToolPreview {
	lines := strings.Split(strings.ReplaceAll(content, "\r", ""), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ToolPreview{Hidden: true}
	}
	for i := range lines {
		lines[i] = truncLine(strings.ReplaceAll(lines[i], "\t", "   "), maxLineRunes)
	}
	total := len(lines)
	if expanded || total <= writePreviewLines {
		return ToolPreview{Lines: lines, Total: total}
	}
	return ToolPreview{Lines: lines[:writePreviewLines], Skipped: total - writePreviewLines, Total: total}
}

// PrettyArgs formats tool-call JSON args like pi's TUI instead of leaking
// raw JSON: bash → command only ("$ " prefix is added by the renderer),
// read/edit/write/ls → path, find/grep → pattern + path. Unknown tools fall
// back to CompactArgs.
func PrettyArgs(tool, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return CompactArgs(raw)
	}
	strField := func(keys ...string) (string, bool) {
		for _, k := range keys {
			v, ok := m[k]
			if !ok {
				continue
			}
			if string(v) == "null" {
				return "", false
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return "", false
			}
			return s, true
		}
		return "", false
	}
	numField := func(key string) (float64, bool) {
		v, ok := m[key]
		if !ok || string(v) == "null" {
			return 0, false
		}
		var n float64
		if err := json.Unmarshal(v, &n); err != nil {
			return 0, false
		}
		return n, true
	}
	oneLine := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	switch strings.ToLower(tool) {
	case "bash", "powershell":
		cmd, _ := strField("command")
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			cmd = "..."
		} else {
			cmd = oneLine(cmd)
		}
		if t, ok := numField("timeout"); ok && t > 0 {
			cmd += fmt.Sprintf(" (timeout %gs)", t)
		}
		return cmd
	case "read":
		p, _ := strField("file_path", "path")
		p = strings.TrimSpace(p)
		if p == "" {
			p = "..."
		}
		off, hasOff := numField("offset")
		lim, hasLim := numField("limit")
		if !hasOff && !hasLim {
			return p
		}
		start := 1
		if hasOff && off >= 1 {
			start = int(off)
		}
		if hasLim && lim >= 1 {
			return fmt.Sprintf("%s:%d-%d", p, start, start+int(lim)-1)
		}
		return fmt.Sprintf("%s:%d", p, start)
	case "edit", "write":
		p, _ := strField("file_path", "path")
		p = strings.TrimSpace(p)
		if p == "" {
			return "..."
		}
		return p
	case "ls":
		p, _ := strField("path")
		p = strings.TrimSpace(p)
		if p == "" {
			p = "."
		}
		if lim, ok := numField("limit"); ok && lim >= 1 {
			return fmt.Sprintf("%s (limit %g)", p, lim)
		}
		return p
	case "find":
		pat, _ := strField("pattern")
		pat = strings.TrimSpace(pat)
		if pat == "" {
			pat = "..."
		}
		p, _ := strField("path")
		p = strings.TrimSpace(p)
		if p == "" {
			p = "."
		}
		out := pat + " in " + p
		if lim, ok := numField("limit"); ok && lim >= 1 {
			out += fmt.Sprintf(" (limit %g)", lim)
		}
		return out
	case "grep":
		pat, patOK := strField("pattern")
		pat = strings.TrimSpace(pat)
		p, _ := strField("path")
		p = strings.TrimSpace(p)
		if p == "" {
			p = "."
		}
		var head string
		if !patOK {
			head = "[invalid arg]"
		} else if pat == "" {
			head = "..."
		} else {
			head = "/" + oneLine(pat) + "/"
		}
		out := head + " in " + p
		if g, ok := strField("glob"); ok && strings.TrimSpace(g) != "" {
			out += " (" + strings.TrimSpace(g) + ")"
		}
		if lim, ok := numField("limit"); ok && lim >= 1 {
			out += fmt.Sprintf(" limit %g", lim)
		}
		return out
	default:
		return CompactArgs(raw)
	}
}

// CollapsedBudget mirrors pi's per-tool preview budgets: write calls show
// 10 lines collapsed, bash tails 5, grep heads 15, everything else 20.
func CollapsedBudget(tool string) int {
	switch strings.ToLower(tool) {
	case "bash", "powershell":
		return bashPreviewLines
	case "grep":
		return grepPreviewLines
	case "write":
		return writePreviewLines
	default:
		return headPreviewLines
	}
}

// EditDiffFallback renders a simple - old / + new preview from edit
// tool-call args when pi reports no details.diff (e.g. older sessions).
// Empty when the args carry no usable edits.
func EditDiffFallback(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	type edit struct {
		OldText string `json:"oldText"`
		NewText string `json:"newText"`
	}
	var edits []edit
	if v, ok := m["edits"]; ok && string(v) != "null" {
		_ = json.Unmarshal(v, &edits)
	}
	if len(edits) == 0 {
		var single edit
		oldV, oldOK := m["oldText"]
		newV, newOK := m["newText"]
		if !oldOK || !newOK {
			return ""
		}
		if err := json.Unmarshal(oldV, &single.OldText); err != nil {
			return ""
		}
		if err := json.Unmarshal(newV, &single.NewText); err != nil {
			return ""
		}
		edits = []edit{single}
	}
	var b strings.Builder
	for _, e := range edits {
		for _, ln := range strings.Split(e.OldText, "\n") {
			if strings.TrimSpace(ln) != "" {
				b.WriteString("- " + ln + "\n")
			}
		}
		for _, ln := range strings.Split(e.NewText, "\n") {
			if strings.TrimSpace(ln) != "" {
				b.WriteString("+ " + ln + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// WriteDiffFallback renders a write call as an all-additions diff ("+ " per
// line) so a written file reads like the edit steps beside it instead of one
// escaped JSON blob. The previous content is not recoverable from the call
// args, so a rewrite shows the whole new file as added lines instead of
// guessing which lines were removed. Empty when the args carry no content.
func WriteDiffFallback(raw string) string {
	content, ok := WriteContent(raw)
	if !ok || strings.TrimSpace(content) == "" {
		return ""
	}
	var b strings.Builder
	for _, ln := range strings.Split(content, "\n") {
		if strings.TrimSpace(ln) != "" {
			b.WriteString("+ " + ln + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ArgPath extracts the file path from tool-call JSON args (file_path or
// path). Empty when absent so callers can fall back to the pretty header.
func ArgPath(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path"} {
		v, ok := m[k]
		if !ok || string(v) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue
		}
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// WriteContent extracts the file text from write tool-call JSON args.
// ok=false when there is no usable string content.
func WriteContent(raw string) (content string, ok bool) {
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", false
	}
	v, present := m["content"]
	if !present || string(v) == "null" {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

// LangFromPath maps a file path to a highlight.js language like pi's
// getLanguageFromPath. Unknown extensions return "" (dim style, no
// auto-detect — same rule as pi).
func LangFromPath(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 || dot == len(path)-1 {
		return ""
	}
	switch strings.ToLower(path[dot+1:]) {
	case "js", "jsx", "mjs", "cjs":
		return "javascript"
	case "ts", "tsx", "mts", "cts":
		return "typescript"
	case "go", "py", "rb", "java", "c", "h", "cpp", "hpp", "cc",
		"cs", "php", "rs", "swift", "kt", "scala", "sh", "bash",
		"yaml", "yml", "toml", "json", "xml", "html", "css",
		"scss", "sql", "md", "markdown", "vue", "svelte", "lua",
		"pl", "r", "dart", "elm", "ex", "exs", "erl", "hs", "ml",
		"clj", "groovy", "ps1", "dockerfile", "diff", "ini", "cfg":
		return strings.ToLower(path[dot+1:])
	}
	return ""
}
