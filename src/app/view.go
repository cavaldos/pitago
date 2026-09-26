package app

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/format"
	"pitago/src/components/markdown"
	terminal_image "pitago/src/components/terminal_image"
	"pitago/src/extension"
	"pitago/src/pirpc"
)

func (m Model) showSide() bool { return !m.hideSide && m.winW >= 80 }

func (m Model) mainW() int {
	w := m.winW - 2 // single column margin
	if m.showSide() {
		w = m.winW - sideW - 5 // chat + gap + sidebar
	}
	if w < 30 {
		w = 30
	}
	return w
}

// blocks helpers ----------------------------------------------------------

// gutter prefixes a block with a left status icon: the first non-empty
// line gets the icon, continuation lines stay flush-left so wrapped text
// never looks indented (matches viewport soft-wrap, which starts at col
// 0). Blank separator lines stay empty.
func gutter(icon, body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	first := true
	for _, ln := range lines {
		if ln == "" {
			out = append(out, "")
			continue
		}
		if first {
			out = append(out, icon+" "+ln)
			first = false
		} else {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// startsPreformatted reports whether source opens with a markdown table or
// fenced code block. Those render as column-aligned rows, so the gutter
// icon must not shift only the first row (see gutterBox).
func startsPreformatted(s string) bool {
	t := strings.TrimLeft(s, " \t\n")
	return strings.HasPrefix(t, "|") || strings.HasPrefix(t, "```")
}

// gutterBox is gutter for bordered/full-bleed blocks (user box, tool
// Box, table/code-led assistant replies): continuation lines get a blank 2-cell gutter so every row stays
// exactly as wide as the first ("● "+box) and the left/right borders
// stay vertically aligned. Without it the top border sticks out 2 cells
// past the sides (flush-left continuation = w-2 vs first line w).
func gutterBox(icon, body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	first := true
	for _, ln := range lines {
		if ln == "" {
			out = append(out, "")
			continue
		}
		if first {
			out = append(out, icon+" "+ln)
			first = false
		} else {
			out = append(out, "  "+ln)
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) renderBlocks() string {
	var b strings.Builder
	w := m.vp.Width
	cw := w - 2 // gutter takes 2 cells
	if cw < 10 {
		cw = 10
	}
	if len(m.blocks) == 0 && m.connErr == "" {
		// fresh chat: pi-style startup header (logo + resources + ready)
		b.WriteString(m.welcomeView(w))
	}
	if m.connErr != "" {
		b.WriteString(gutter(errStyle.Render("×"), errStyle.Render("! "+m.connErr)+"\n"))
	}
	// Per-block cache: only the dirty block (streaming text or a fresh
	// tool status) re-renders; history reuses its last string. The key
	// covers every input of renderOneBlock, so a width/theme/flag change
	// misses everywhere and rebuilds once.
	if len(m.renderCache) != len(m.blocks) || len(m.renderCacheKey) != len(m.blocks) {
		nc := make([]string, len(m.blocks))
		nk := make([]uint64, len(m.blocks))
		copy(nc, m.renderCache)
		copy(nk, m.renderCacheKey)
		m.renderCache, m.renderCacheKey = nc, nk
	}
	hide, expand, theme := m.HideThinking, m.expandTools, m.ThemeName
	for i, bl := range m.blocks {
		// Image-bearing transcript blocks are always rebuilt as safe squares.
		// This purges any cache entry created by an older image-render path
		// before scroll/repaint can re-emit Kitty/iTerm placement escapes.
		if len(bl.Images) > 0 {
			m.renderCache[i], m.renderCacheKey[i] = "", 0
		}
		key := blockKey(bl, cw, hide, expand, theme)
		if m.renderCacheKey[i] == key {
			b.WriteString(m.renderCache[i])
			continue
		}
		s, skip := m.renderOneBlock(bl, cw)
		if skip {
			s = ""
		}
		m.renderCacheKey[i] = key
		m.renderCache[i] = s
		b.WriteString(s)
	}
	if m.thinking {
		b.WriteString(gutter(statusBarStyle.Render("○"), statusBarStyle.Render(m.Status)+"\n"))
	}
	return b.String()
}

// blockKey fingerprints one block's rendered output: every field
// renderOneBlock reads, plus the width and global render flags.
func blockKey(bl Block, cw int, hide, expand bool, theme string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(bl.Kind))
	h.Write([]byte{0})
	h.Write([]byte(bl.Text))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolName))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolArgs))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolArgsRaw))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolStatus))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolResult))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolDiff))
	h.Write([]byte{0})
	h.Write([]byte(bl.ToolCallID))
	h.Write([]byte{0})
	if bl.Err {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "\x00images\x00%d", len(bl.Images))
	fmt.Fprintf(h, "\x00%d\x00%v\x00%v\x00%s", cw, hide, expand, theme)
	return h.Sum64()
}

// renderOneBlock renders a single chat block (gutter included). It reports
// skip=true for content-less streaming leftovers and hidden thinking,
// which renderBlocks caches as empty.
func (m *Model) renderOneBlock(bl Block, cw int) (string, bool) {
	// Streaming can leave content-less assistant/thinking blocks behind
	// (e.g. bare newlines around a tool call). They render as stray
	// blank gaps, so skip them: they carry no information.
	if (bl.Kind == "assistant" || bl.Kind == "thinking") && strings.TrimSpace(bl.Text) == "" {
		return "", true
	}
	if bl.Kind == "thinking" && m.HideThinking {
		return "", true // /settings "Hide thinking" (pi parity)
	}
	var icon, body string
	boxed := false // bordered/full-bleed rows need the 2-cell gutter
	switch bl.Kind {
	case "user":
		icon = statusBarStyle.Render("●")
		// Transcript is fallback-only until an image-aware viewport can manage
		// Kitty/iTerm placement lifecycle. Never put real image escapes into
		// the scrollable Bubbletea output.
		body = strings.TrimSuffix(userStyle.Width(cw-2).Render(bl.Text), "\n")
		imageLines := make([]string, len(bl.Images))
		for i := range imageLines {
			imageLines[i] = terminal_image.Fallback()
		}
		if len(imageLines) > 0 {
			body += "\n" + strings.Join(imageLines, "\n")
		}
		body += "\n\n"
		boxed = true
	case "assistant":
		icon = statusBarStyle.Render("●")
		body = renderMarkdown(bl.Text, cw) + "\n\n"
		// Table/fence-led replies render as aligned rows: keep the 2-cell
		// gutter so "● " doesn't push the first row 2 cells past the rest.
		boxed = startsPreformatted(bl.Text)
	case "thinking":
		t := bl.Text
		if len(t) > 300 {
			t = t[:300] + "…"
		}
		icon = statusBarStyle.Render("○")
		body = toolStyle.Render(Short(t, 160)) + "\n\n"
	case "tool":
		// Every tool call is one bounded block: header, detail line and
		// body preview inside a rounded frame with a background fill, so
		// a tool execution never bleeds into the assistant prose around
		// it. renderToolBlock owns the frame; the status bullet stays on
		// the header row as a color-free fallback for terminals that
		// drop the fill entirely.
		return m.renderToolBlock(bl, cw) + "\n\n", false
	case "bash":
		// A local shell echo (!cmd) is output without an agent tool call,
		// so it has no execution state: quiet neutral block, reddened
		// when the command failed.
		class := format.StatusNeutral
		if bl.Err {
			class = format.StatusError
		}
		rows := strings.Split(markdown.Highlight("bash", Short(bl.Text, 400)), "\n")
		return framedBlock(rows, cw, blockThemeFor(class)) + "\n\n", false
	case "tree":
		icon = statusBarStyle.Render("●")
		body = codeStyle.Render(shortTree(bl.Text, 3000)) + "\n\n"
	case "session":
		icon = statusBarStyle.Render("●")
		body = renderSession(bl.Text) + "\n\n"
	case "notice":
		if bl.Err {
			icon = errStyle.Render("×")
			body = errStyle.Render("! "+bl.Text) + "\n\n"
		} else {
			icon = toolStyle.Render("·")
			body = toolStyle.Render(bl.Text) + "\n\n"
		}
	default:
		icon = statusBarStyle.Render("●")
		body = lipgloss.NewStyle().Foreground(cText).Render(bl.Text) + "\n\n"
	}
	if boxed {
		return gutterBox(icon, body), false
	}
	return gutter(icon, body), false
}

// renderMarkdown renders assistant output with the Go renderer (Glamour
// tables/lists/bold, Chroma fenced code) wrapped to the chat width. Plain text comes back unchanged from markdown.Render and keeps the
// old unstyled render.
func renderMarkdown(src string, width int) string {
	if out := markdown.Render(src, width); out != src {
		return out
	}
	return lipgloss.NewStyle().Foreground(cText).Width(width).Render(src)
}

// codeLang detects code in a one-line tool result: fenced block (with
// optional language), JSON, or a unified diff. Plain prose returns ok=false
// so it keeps the dim style instead of risking a wrong lexer. A fence
// without lang still returns ok=true with lang="": pi has no auto-detect
// and falls back to its mdCodeBlock color.
func codeLang(s string) (lang string, ok bool) {
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.IndexAny(rest, " \t⏎\n`"); j >= 0 {
			rest = rest[:j]
		}
		return rest, true // "" → Chroma auto-detect
	}
	t := strings.TrimSpace(s)
	if (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) ||
		(strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
		return "json", true
	}
	if strings.Contains(s, "diff --git") ||
		(strings.Contains(s, "+++") && strings.Contains(s, "---")) {
		return "diff", true
	}
	return "", false
}

// isShell reports whether a tool's output is command output, which gets
// the bordered "── Output ──" section instead of a result tree.
func isShell(tool string) bool {
	switch strings.ToLower(tool) {
	case "bash", "powershell":
		return true
	}
	return false
}

// toolDetail is the dim line under a tool header: a live marker while the
// call is still running, and an explicit "no output" when a finished call
// has nothing to show (read hides its payload on success by design).
//
// The state comes from format.ToolStatusClass, the same classifier the frame
// fill uses, so the label can never contradict the tint: an upstream rename
// to "ok"/"success" would otherwise paint a finished-green block that still
// claims to be running.
func toolDetail(bl Block) string {
	switch format.ToolStatusClass(bl.ToolStatus) {
	case format.StatusRunning, format.StatusNeutral:
		return "running…"
	}
	if strings.TrimSpace(bl.ToolResult) == "" && strings.TrimSpace(bl.ToolDiff) == "" {
		return "no output"
	}
	return ""
}

// tool bullet: the status glyph a block header carries as a color-free
// fallback. Inside the frame it is the only part of the block that still
// reads when the terminal drops the background fill.
func toolBullet(bl Block) string {
	switch format.ToolStatusClass(bl.ToolStatus) {
	case format.StatusSuccess:
		return okStyle.Render("●")
	case format.StatusError:
		return errStyle.Render("×")
	case format.StatusNeutral:
		return toolStyle.Render("·")
	}
	return statusBarStyle.Render("○")
}

// renderToolBlock frames one tool call — header row, detail line, body
// preview — as a single bounded block, pi style. The frame is flush
// left: the gutter bullet that used to indent a tool block is gone,
// because the border is now the left edge and an extra 2 cells would
// push the frame past its column.
//
// A shell call is the same block in a different shape (see
// renderShellBlock): command line, divider, output.
func (m Model) renderToolBlock(bl Block, w int) string {
	if isShell(bl.ToolName) {
		return m.renderShellBlock(bl, w)
	}
	inner := blockInner(w)
	rows := []string{toolHeaderRow(bl, toolHead(bl), inner)}
	if d := toolDetail(bl); d != "" {
		rows = append(rows, toolStyle.Render(d))
	}
	if r := m.renderToolBody(bl); r != "" {
		rows = append(rows, r)
	}
	return framedBlock(rows, w, blockThemeFor(format.ToolStatusClass(bl.ToolStatus)))
}

// toolHead is the header text after the tool name: the pretty args, plus
// pi's added-line-count suffix on a write ("write game.js +211").
func toolHead(bl Block) string {
	head := bl.ToolArgs
	if strings.ToLower(bl.ToolName) == "write" {
		if content, ok := format.WriteContent(bl.ToolArgsRaw); ok {
			if n := countLines(content); n > 0 {
				head += fmt.Sprintf(" +%d", n)
			}
		}
	}
	return head
}

// toolHeaderRow is one header row: the status bullet, the tool name in
// its kind accent (bold, so the name is the block's title), and the
// args — dim, and truncated to whatever the frame's inner column has
// left so the row can never wrap out of the box.
func toolHeaderRow(bl Block, head string, inner int) string {
	name := bl.ToolName
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}
	name = Short(name, max(inner-6, 8))
	if strings.TrimSpace(head) == "" {
		head = "tool"
	}
	row := toolBullet(bl) + " " + toolNameStyleFor(bl.ToolName).Render(name)
	// Measure the unstyled text: the row is built before the accent is
	// applied, and ANSI must not eat into the args budget.
	avail := inner - lipgloss.Width(name) - len(" ● ") - 1
	if avail < 8 {
		avail = 8
	}
	return row + " " + toolStyle.Render(Short(head, avail))
}

// renderShellBlock frames one whole shell call — command, then a divider,
// then output — as one block. It stays bullet-free: the line already
// opens with the shell prompt, which is the row's own title. The box is
// still rendered while the call runs, holding just the command, so a
// long command does not pop into existence with its output.
func (m Model) renderShellBlock(bl Block, w int) string {
	rows := []string{shellCommandRow(bl)}
	inner := blockInner(w) // clamped to the same floor framedBlock uses
	if out := m.shellOutput(bl); out != "" {
		label := "Output"
		if bl.ToolStatus == "error" {
			label = "Error"
		}
		rows = append(rows, dividerRow(inner, label), out)
	}
	return framedBlock(rows, w, blockThemeFor(format.ToolStatusClass(bl.ToolStatus)))
}

// shellCommandRow is the box's first row: the bare prompt plus the
// chroma-highlighted command, so it reads like a shell line rather than
// a header. Falls back to dim text when chroma does not know the lexer.
func shellCommandRow(bl Block) string {
	row := codeStyle.Render(shellPrompt(bl.ToolName))
	args := strings.TrimSpace(bl.ToolArgs)
	if args == "" {
		return row
	}
	hl := markdown.Highlight(shellLang(bl.ToolName), args)
	if hl == args {
		hl = toolStyle.Render(args)
	}
	return row + " " + hl
}

// shellOutput is the framed output section, empty while the call is still
// running. The skip hint leads the section (so it is read before the
// windowed lines) and the collapse offer closes it, both inside the box.
func (m Model) shellOutput(bl Block) string {
	if bl.ToolStatus != "done" && bl.ToolStatus != "error" {
		return ""
	}
	p := format.ToolResultPreviewExpanded(strings.ToLower(bl.ToolName), bl.ToolStatus, bl.ToolResult, m.expandTools)
	if p.Hidden || (len(p.Lines) == 0 && p.Skipped == 0) {
		return ""
	}
	var rows []string
	if p.Skipped > 0 && !m.expandTools {
		rows = append(rows, toolStyle.Render("… ("+skipHint(p)+", "+expandHint+")"))
	}
	body := strings.Join(p.Lines, "\n")
	if lang, ok := codeLang(body); ok && len(p.Lines) > 1 {
		if out := markdown.Highlight(lang, body); out != body {
			body = out
		}
	}
	rows = append(rows, body)
	if m.expandTools && p.Total > 0 {
		rows = append(rows, toolStyle.Render("("+collapseHint+")"))
	}
	return strings.Join(rows, "\n")
}

// shellPrompt is the leading sigil for a shell tool's command line.
func shellPrompt(tool string) string {
	if strings.ToLower(tool) == "powershell" {
		return "PS>"
	}
	return "$"
}

// shellLang is the chroma lexer for a shell command. The highlighter has no
// powerShell lexer to match, so those fall through and render dim.
func shellLang(tool string) string {
	if strings.ToLower(tool) == "powershell" {
		return ""
	}
	return "bash"
}

// toolBorder colors a tool block's frame by raw execution status. It is
// the status-keyed shorthand over toolFrame; a shell block has no header
// bullet to carry the state, so its frame is the only signal.
func toolBorder(status string) lipgloss.Color {
	return toolFrame(format.ToolStatusClass(status))
}

// blockTheme is the palette one block paints with: the frame (border)
// color and the background fill, both resolved from theme tokens, so a
// /theme switch recolors every block and nothing is hardcoded here.
type blockTheme struct {
	frame lipgloss.Color
	fill  lipgloss.Color
}

// blockThemeFor resolves the frame/fill pair for one execution state
// (format.Status*). Status drives the block; the tool kind drives the
// header accent inside it (see toolNameStyleFor).
func blockThemeFor(class string) blockTheme {
	return blockTheme{frame: toolFrame(class), fill: toolFill(class)}
}

// Block geometry. A block is border (2 cells) + padding (1 per side), so
// a content row may use w-4 cells. 20 is the narrowest frame that still
// fits a header row and a "─── Output ──" divider without wrapping.
const (
	blockMinW   = 20
	blockChrome = 4
)

// blockInner is the usable content width inside a block of width w,
// clamped to the same floor framedBlock applies, so a divider row and
// the box that wraps it are always measured the same.
func blockInner(w int) int {
	if w < blockMinW {
		w = blockMinW
	}
	return w - blockChrome
}

// framedBlock is the one bordered renderer for the chat stream, and the
// only place a tool block turns into a box: a rounded frame exactly w
// cells wide, a low-contrast background fill, content inset one cell per
// side, and a hard width clamp so a block can never overflow its column
// and never break a border mid-line.
//
// Padding insets the content so it never touches the border; lipgloss
// counts padding inside Width, hence w-2 (the border adds the other 2
// back). MaxWidth is the backstop for a row that arrives wider than the
// column — a long unbreakable token, say: lipgloss wraps at the column
// first, and the clamp guarantees the invariant even if it ever does not.
func framedBlock(rows []string, w int, t blockTheme) string {
	if w < blockMinW {
		w = blockMinW
	}
	seq := fillSeq(t.fill)
	// Copy before painting: the caller's rows (a previews slice, a
	// highlight result) must come back unchanged for the next repaint.
	painted := make([]string, len(rows))
	for i, r := range rows {
		painted[i] = fillRow(r, seq)
	}
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.frame).
		Padding(0, 1).
		Width(w - 2).
		MaxWidth(w)
	if t.fill != "" {
		// Background colors the content block and its padding;
		// BorderBackground colors the frame glyphs themselves, so the
		// fill reaches the frame instead of stopping at the content.
		st = st.Background(t.fill).BorderBackground(t.fill)
	}
	return st.Render(strings.Join(painted, "\n"))
}

// fillSeq is the escape lipgloss emits for a background color under the
// active color profile, or "" when that profile has no color at all (a
// truecolor-less terminal, and the test binary). It is derived from
// lipgloss rather than termenv so the profile decision stays in one
// place: wherever lipgloss would drop the fill, the re-arm is a no-op
// too and the block degrades to a plain outline.
func fillSeq(c lipgloss.Color) string {
	if c == "" {
		return ""
	}
	s := lipgloss.NewStyle().Background(c).Render(" ")
	i := strings.IndexByte(s, ' ')
	if i <= 0 {
		return ""
	}
	return s[:i]
}

// fillRow paints one content row with the block's fill. lipgloss sets
// the fill once per line, but every styled span inside a row ends with
// a full reset — a syntax-highlighted token, a dim hint, a border color
// — which would switch the fill off for the rest of the line and leave
// transparent gaps between tokens. Re-arming after each reset is what
// makes the fill continuous under highlighted output.
func fillRow(row, seq string) string {
	if seq == "" || row == "" {
		return row
	}
	return seq + strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+seq) + "\x1b[0m"
}

// dividerRow is a full-width section rule with an inline label, the
// "─── Output ──────" rule from oh-my-pi. lipgloss v1's border renderer
// only repeats a single rune, so a labelled rule has to be a content row.
func dividerRow(inner int, label string) string {
	head := "─── " + label + " "
	if n := inner - lipgloss.Width(head); n > 0 {
		head += strings.Repeat("─", n)
	}
	return sepStyle.Render(head)
}

// renderResultRows renders result rows with or without tree glyphs: every
// row but the last is prefixed "├── " and the last "└── " when tree is set,
// so a file-list-shaped result reads as one block instead of a ragged list.
// st styles each row (pass an empty style for already-colored ANSI rows).
// Blank rows stay blank so a gap never grows a phantom branch.
//
// Rows carry no leading indent of their own: every tool block is wrapped
// in a frame whose padding is the single source of indentation. Indenting
// here too would push the tree two cells past the block edge.
func renderResultRows(rows []string, st lipgloss.Style, tree bool) string {
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		if strings.TrimSpace(r) == "" {
			out = append(out, "")
			continue
		}
		if tree {
			pre := "├── "
			if i == len(rows)-1 {
				pre = "└── "
			}
			r = pre + r
		}
		out = append(out, st.Render(r))
	}
	return strings.Join(out, "\n")
}

// skipHint is the shared "N earlier/more lines" clause for a truncated
// tool result. The caller owns the surrounding parens and the ctrl+g key
// hint, so this returns only the clause.
func skipHint(p format.ToolPreview) string {
	if p.Tail {
		return fmt.Sprintf("%d earlier lines", p.Skipped)
	}
	return fmt.Sprintf("%d more lines", p.Skipped)
}

// toolBg is pi's tool Box background by execution status: pending while
// running, green on success, red on error. framedBlock paints it as the
// block fill (via toolFill), and on a terminal whose color profile has no
// background the fill is dropped by lipgloss while the frame, the header
// bullet and all text stay — so a block degrades to a plain outline
// rather than to raw rows.
func toolBg(status string) lipgloss.Color {
	switch status {
	case "done":
		return cToolSuccess
	case "error":
		return cToolError
	default:
		return cToolPending
	}
}

// renderToolBody renders a tool's content like pi's TUI. File-changing
// tools stay detailed by default so additions and edits remain visible.
// Other successful tools collapse to one compact summary line and expand
// with ctrl+g; errors are always shown in full.
func (m Model) renderToolBody(bl Block) string {
	tool := strings.ToLower(bl.ToolName)
	if bl.ToolStatus == "error" {
		return renderToolResultFull(tool, bl.ToolResult)
	}

	switch tool {
	case "write":
		if content, ok := format.WriteContent(bl.ToolArgsRaw); ok && strings.TrimSpace(content) != "" {
			p := format.CallPreview(content, true)
			return renderPreview(p, format.LangFromPath(toolPath(bl)), false, false)
		}
		return renderToolResultFull(tool, bl.ToolResult)
	case "edit":
		// pi's edit tool always reports a one-line "Successfully replaced
		// ..." receipt, so the real change only lives in details.diff. Prefer
		// it; then a result that is itself a real diff, because the args
		// reconstruction below is a 2-line guess and must never mask
		// authoritative diff text; then the guess, then the bare receipt.
		if bl.ToolDiff != "" {
			return renderEditDiff(bl.ToolDiff)
		}
		if resultDiff := strings.TrimSpace(bl.ToolResult); codeLangIsDiff(resultDiff) {
			return renderPreview(format.CallPreview(resultDiff, true), "diff", false, false)
		}
		if fb := format.EditDiffFallback(bl.ToolArgsRaw); fb != "" {
			// The reconstructed diff is a bare -/+ pair, so the chroma `diff`
			// lexer still applies here (no gutter, no "..." markers).
			return renderPreview(format.CallPreview(fb, true), "diff", false, false)
		}
		text := strings.TrimSpace(bl.ToolResult)
		if text == "" {
			return ""
		}
		p := format.CallPreview(text, true)
		lang := format.LangFromPath(toolPath(bl))
		if _, ok := codeLang(text); ok {
			lang = "diff"
		}
		// The preview is already complete, so pass expanded=false only to
		// suppress the generic ctrl+g-to-collapse affordance.
		return renderPreview(p, lang, false, false)
	default:
		return renderToolResultCompact(tool, bl.ToolStatus, bl.ToolResult, m.expandTools)
	}
}

// codeLangIsDiff reports whether s is itself a unified diff. codeLang also
// accepts JSON and fenced blocks, so match on the returned lang rather than on
// ok — a fenced edit result must not be mistaken for diff text.
func codeLangIsDiff(s string) bool {
	lang, ok := codeLang(s)
	return ok && lang == "diff"
}

// renderEditDiff renders pi's details.diff for an edit tool. That payload is
// display-oriented — line-number gutter, -/+ rows, "..." elision markers — and
// carries no ANSI of its own, so it must not go through the chroma `diff`
// lexer, which only understands unified patches (---/+++/@@) and would color
// it wrong. Rows are tinted by their marker here instead, and the gutter is
// left intact.
func renderEditDiff(text string) string {
	p := format.CallPreview(text, true) // expanded: never collapse a diff
	if p.Hidden || len(p.Lines) == 0 {
		return ""
	}
	rows := make([]string, len(p.Lines))
	for i, ln := range p.Lines {
		rows[i] = colorEditDiffLine(ln)
	}
	p.Lines = rows
	// lang "" keeps renderPreview off the highlighter; expanded=false only
	// suppresses the ctrl+g-to-collapse hint (CallPreview already returned
	// every line, so there is nothing to collapse). tree=false: a diff
	// already has its own +/- and line-number gutter, so branch glyphs on
	// top of it would just be noise.
	return renderPreview(p, "", false, false)
}

// colorEditDiffLine tints one diff row by its leading marker: removed lines
// red, added lines green, "..." elision markers dim, everything else (the
// gutter and context rows) normal. The marker sits after the gutter indent,
// so compare against the left-trimmed row; context rows start with the line
// number, which keeps a leading "-" inside the code itself from misfiring.
func colorEditDiffLine(ln string) string {
	body := strings.TrimLeft(ln, " ")
	switch {
	case strings.HasPrefix(body, "-"):
		return errStyle.Render(ln)
	case strings.HasPrefix(body, "+"):
		return okStyle.Render(ln)
	case body == "..." || body == "…":
		return toolStyle.Render(ln)
	}
	return codeStyle.Render(ln)
}

// renderToolResultCompact shows no partial output while a generic tool is
// collapsed: multi-line results become a single line-count hint, while a
// useful one-line result remains visible. Expanding still uses the shared
// full renderer and its collapse hint.
func renderToolResultCompact(tool, status, result string, expanded bool) string {
	n := countLines(result)
	if n == 0 {
		return ""
	}
	if expanded {
		return renderToolResultExpanded(tool, status, result, true)
	}
	if n == 1 {
		return renderResultRows([]string{strings.TrimSpace(result)}, toolStyle, status != "error")
	}
	return toolStyle.Render(fmt.Sprintf("… (%d lines, %s)", n, expandHint))
}

// renderToolResultFull keeps errors and edit/write receipts visible without
// advertising a collapse action that would not change their rendering.
// tree=false: an error is one logical unit, not a list of siblings.
func renderToolResultFull(tool, result string) string {
	p := format.ToolResultPreviewExpanded(tool, "error", result, true)
	return renderPreview(p, "", false, false)
}

// toolPath is the file path for highlight-language detection: raw args
// first (clean path), then the pretty header with any :offset-limit range
// stripped.
func toolPath(bl Block) string {
	if p := format.ArgPath(bl.ToolArgsRaw); p != "" {
		return p
	}
	p := bl.ToolArgs
	if i := strings.LastIndex(p, ":"); i > 0 {
		// "path:10-14" or "path:10" — strip the range, keep the path.
		num := true
		for _, c := range p[i+1:] {
			if (c < '0' || c > '9') && c != '-' {
				num = false
				break
			}
		}
		if num {
			return p[:i]
		}
	}
	return p
}

func countLines(s string) int {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// renderPreview renders one collapsed/expanded preview: code blocks go
// through pi's highlighter (falling back to dim rows), other lines keep
// the dim result style, and the matching pi-style hint closes the block.
// tree selects box-drawing branch glyphs, which belong to file-list-shaped
// results only — a diff already carries its own +/−/line-number gutter and
// an error is one logical unit, so both render as flat indented rows.
func renderPreview(p format.ToolPreview, lang string, expanded, tree bool) string {
	if p.Hidden || len(p.Lines) == 0 {
		return ""
	}
	plain := lipgloss.NewStyle()
	if lang != "" && len(p.Lines) > 1 {
		if out := markdown.Highlight(lang, strings.Join(p.Lines, "\n")); out != strings.Join(p.Lines, "\n") {
			body := renderResultRows(strings.Split(out, "\n"), plain, tree)
			if hint := previewHint(p, expanded); hint != "" {
				body += "\n" + toolStyle.Render(hint)
			}
			return body
		}
	}
	body := renderResultRows(p.Lines, toolStyle, tree)
	if hint := previewHint(p, expanded); hint != "" {
		body += "\n" + toolStyle.Render(hint)
	}
	return body
}

// previewHint is pi's trailing hint: collapsed shows what is hidden,
// expanded offers to collapse back (only when lines were hidden).
func previewHint(p format.ToolPreview, expanded bool) string {
	if p.Skipped > 0 && !expanded {
		hint := "... (" + skipHint(p)
		if p.Total > 0 {
			hint += fmt.Sprintf(", %d total", p.Total)
		}
		return hint + ", " + expandHint + ")"
	}
	if expanded && p.Total > 0 {
		return "(" + collapseHint + ")"
	}
	return ""
}

// renderToolResult renders tool output multi-line like pi: bash shows the
// last 5 lines with an "... (N earlier lines)" hint, ls/find/grep show the
// first 15-20 lines with an "... (N more lines)" hint, and read/write hide
// output on success (errors still show). Single-line code keeps pi's code
// highlight; anything else falls back to the dim style.
func renderToolResult(tool, status, s string) string {
	return renderToolResultExpanded(tool, status, s, false)
}

// renderToolResultExpanded is renderToolResult with the expand toggle:
// expanded shows every line instead of the head/tail window, with a hint
// to collapse back.
func renderToolResultExpanded(tool, status, s string, expanded bool) string {
	p := format.ToolResultPreviewExpanded(tool, status, s, expanded)
	if p.Hidden || len(p.Lines) == 0 {
		return ""
	}
	// Branch glyphs mean "these are sibling results". An error is one
	// logical unit, so it stays a flat block.
	tree := status != "error"
	if len(p.Lines) == 1 && p.Skipped == 0 {
		line := p.Lines[0]
		if lang, ok := codeLang(line); ok {
			if out := markdown.Highlight(lang, line); out != line {
				return renderResultRows([]string{out}, lipgloss.NewStyle(), tree)
			}
		}
		return renderResultRows([]string{line}, toolStyle, tree)
	}
	var rows []string
	if p.Skipped > 0 && !expanded {
		rows = append(rows, toolStyle.Render("... ("+skipHint(p)+", "+expandHint+")"))
	}
	if body := renderResultRows(p.Lines, toolStyle, tree); body != "" {
		rows = append(rows, body)
	}
	if hint := previewHint(p, expanded); hint != "" {
		// previewHint duplicates the collapsed top hint at the bottom —
		// keep only the expanded collapse offer here.
		if expanded {
			rows = append(rows, toolStyle.Render(hint))
		}
	}
	return strings.Join(rows, "\n")
}

// renderSidebar mirrors pi's session panel: SESSION, model+ctx, STATS,
// RECENT MODELS (clickable), COMMANDS, TOOLS, WORKSPACE, cwd. Content is
// built by buildSidebarContent and shown through sideVp, so a tall sidebar
// clips to the box and scrolls (wheel over it) instead of overflowing the layout.
// recentAt maps clicks with sideVp.YOffset, so it stays correct scrolled.

func (m Model) buildSidebarContent() string {
	inner := sideInnerW
	var b strings.Builder
	if m.SideVisible(SidePet) {
		b.WriteString(m.renderPet(inner))
	}
	if m.SideVisible(SideSession) {
		b.WriteString(sideTitleStyle.Render("SESSION") + "\n")
		first := m.firstUser()
		if first == "" {
			dot := statusBarStyle.Render("○")
			if m.thinking {
				dot = okStyle.Render("●")
			}
			first = dot + " " + Short(m.Status, inner-2)
		} else {
			first = Short(first, inner)
		}
		b.WriteString(statusBarStyle.Render(first) + "\n")
		sess := m.session
		if sess == "" {
			sess = "…"
		}
		b.WriteString(statusBarStyle.Render(Short(pirpc.Shorten(sess), inner)) + "\n")
		file := m.sessionFile
		if file == "" {
			file = "In-memory"
		} else {
			file = pirpc.Shorten(file)
		}
		b.WriteString(statusBarStyle.Render(Short(file, inner)) + "\n")
		b.WriteString(sep() + "\n")
	}

	if m.SideVisible(SideModel) {
		lvl := m.thinkLvl
		if lvl == "" {
			lvl = "off"
		}
		modelName := Short(m.ModelLbl+" - "+lvl, inner-8)
		b.WriteString(statusBarStyle.Render("model · ") + lipgloss.NewStyle().Foreground(cText).Render(modelName) + "\n")
		barW := inner - len("ctx ") - len(" 100%")
		if barW < 4 {
			barW = 4
		}
		pct := fmt.Sprintf("%3.0f%%", m.Stats.ContextPct)
		b.WriteString(statusBarStyle.Render("ctx "+ctxBar(m.Stats.ContextPct, barW)+" "+pct) + "\n")
		used := m.Stats.ContextToks
		if used == 0 {
			used = m.Stats.TokensTotal
		}
		win := m.ctxWindow
		if win == 0 {
			win = m.Stats.ContextWin
		}
		compact := "manual"
		if m.autoCompact {
			compact = "compact auto"
		}
		tokLine := "tok —"
		if win > 0 {
			tokLine = fmt.Sprintf("%s/%s tkns - %s", FmtNum(used), FmtNum(win), compact)
		} else if used > 0 {
			tokLine = "tok " + FmtNum(used)
		}
		b.WriteString(statusBarStyle.Render(Short(tokLine, inner)) + "\n")
		b.WriteString(sep() + "\n")
	}

	if m.SideVisible(SideStats) {
		b.WriteString(sideTitleStyle.Render(twoCol("Stats", "Tokens", inner)) + "\n")
		elapsed := "—"
		if !m.sessStart.IsZero() {
			elapsed = fmtDur(time.Since(m.sessStart))
		}
		last := "—"
		if m.lastDur > 0 {
			last = fmtDur(m.lastDur)
		}
		speed := "—"
		if m.lastSpeed > 0 {
			speed = fmt.Sprintf("%.0f tok/s", m.lastSpeed)
		}
		cache := "—"
		if m.Stats.TokensTotal > 0 && m.Stats.CacheRead > 0 {
			cache = fmt.Sprintf("%.0f%%", 100*float64(m.Stats.CacheRead)/float64(m.Stats.TokensTotal))
		}
		cost := "—"
		if m.Stats.Cost > 0 {
			cost = fmt.Sprintf("$%.2f", m.Stats.Cost)
		}
		b.WriteString(statusBarStyle.Render(twoCol("time "+elapsed, "in "+FmtNum(m.Stats.In), inner)) + "\n")
		b.WriteString(statusBarStyle.Render(twoCol("last "+last, "out "+FmtNum(m.Stats.Out), inner)) + "\n")
		b.WriteString(statusBarStyle.Render(twoCol("speed "+speed, "total "+FmtNum(m.Stats.TokensTotal), inner)) + "\n")
		b.WriteString(statusBarStyle.Render(twoCol(fmt.Sprintf("turns %d", m.Stats.UserMsgs), "cache "+cache, inner)) + "\n")
		left := "—"
		if m.Stats.ContextPct > 0 {
			left = fmt.Sprintf("%.0f%%", 100-m.Stats.ContextPct)
		}
		b.WriteString(statusBarStyle.Render(twoCol("left "+left, "cost "+cost, inner)) + "\n")
		b.WriteString(statusBarStyle.Render(Short(fmt.Sprintf("msgs %s · u %s a %s",
			fmtComma(m.Stats.TotalMessages), fmtComma(m.Stats.UserMsgs), fmtComma(m.Stats.AsstMsgs)), inner)) + "\n")
		cached, uncached := "—", "—"
		if m.Stats.TokensTotal > 0 {
			cached = fmtComma(m.Stats.CacheRead)
			uncached = fmtComma(m.Stats.In + m.Stats.CacheWrite)
		}
		b.WriteString(statusBarStyle.Render(Short("cached "+cached+" · uncached "+uncached, inner)) + "\n")
		b.WriteString(sep() + "\n")
	}

	if m.SideVisible(SideCost) {
		if rows := m.sideCostRows(); len(rows) > 0 {
			b.WriteString(sideTitleStyle.Render("COST") + "\n")
			for _, r := range rows {
				b.WriteString(statusBarStyle.Render(r) + "\n")
			}
			b.WriteString(sep() + "\n")
		}
	}

	if m.SideVisible(SideRecent) {
		b.WriteString(sideTitleStyle.Render("RECENT MODELS") + "\n")
		if len(m.recentModels) == 0 {
			b.WriteString(toolStyle.Render("—") + "\n")
		} else {
			for i, r := range m.recentModels {
				cur := r.ID == m.ModelLbl || r.DispLabel() == m.ModelLbl
				mark, style := "○ ", statusBarStyle
				row := style
				if cur {
					mark, style = "● ", okStyle
					row = lipgloss.NewStyle().Foreground(cText)
				}
				b.WriteString(style.Render(mark) + row.Render(fmt.Sprintf("%d. %s", i+1, Short(r.DispLabel(), inner-5))) + "\n")
			}
		}
		b.WriteString(toolStyle.Render(m.recentHint()) + "\n")
		b.WriteString(sep() + "\n")
	}

	if m.SideVisible(SideCommands) {
		b.WriteString(sideTitleStyle.Render("COMMANDS") + "\n")
		if len(m.Cmds) == 0 {
			b.WriteString(toolStyle.Render("—") + "\n")
		} else {
			// Per-source counts: extension vs prompt vs skill vs builtin
			// (source taxonomy owned by src/extension).
			ext, prm, skl, bin := extension.Summarize(m.Cmds)
			b.WriteString(statusBarStyle.Render(Short(fmt.Sprintf("%d ext · %d prompt · %d skill · %d builtin", ext, prm, skl, bin), inner)) + "\n")
		}
		if len(m.queue.Steering)+len(m.queue.FollowUp) > 0 {
			b.WriteString(statusBarStyle.Render(fmt.Sprintf("queue: %d steer · %d follow",
				len(m.queue.Steering), len(m.queue.FollowUp))) + "\n")
		}
	}
	if m.SideVisible(SidePlugins) {
		b.WriteString(m.renderPluginsSection(inner))
	}
	if m.SideVisible(SideMCP) {
		b.WriteString(m.renderMcpSection(inner))
	}
	if m.SideVisible(SideTodos) {
		b.WriteString(m.renderTodosSection(inner))
	}
	if m.SideVisible(SideLSP) {
		b.WriteString(m.renderLspSection(inner))
	}
	if m.SideVisible(SideTools) {
		if tools := m.invokedTools(); len(tools) > 0 {
			b.WriteString(sideTitleStyle.Render("TOOLS") + "\n")
			for _, bl := range tools {
				var mark, state string
				switch bl.ToolStatus {
				case "done":
					mark, state = okStyle.Render("✓"), bl.ToolStatus
				case "error":
					mark, state = errStyle.Render("×"), bl.ToolStatus
				default:
					mark, state = statusBarStyle.Render("●"), "running"
				}
				row := mark + " " + statusBarStyle.Render(bl.ToolName) + toolStyle.Render(" · "+state)
				b.WriteString(truncANSI(row, inner) + "\n")
			}
			b.WriteString(sep() + "\n")
		}
	}
	if m.SideVisible(SideWorkspace) {
		if m.ws.ok {
			b.WriteString(sideTitleStyle.Render(Short("WORKSPACE · "+m.ws.branch, inner)) + "\n")
			for _, f := range m.ws.files {
				stat := fmt.Sprintf("+%d -%d", f.add, f.del)
				gap := inner - lipgloss.Width(f.path) - lipgloss.Width(stat)
				name := f.path
				if gap < 1 {
					name = Short(f.path, inner-lipgloss.Width(stat)-1)
					gap = 1
				}
				b.WriteString(statusBarStyle.Render(name+strings.Repeat(" ", gap)) + okStyle.Render(stat) + "\n")
			}
			if m.ws.more > 0 {
				b.WriteString(toolStyle.Render(fmt.Sprintf("… %d more files", m.ws.more)) + "\n")
			}
			if m.ws.untracked > 0 {
				b.WriteString(toolStyle.Render(fmt.Sprintf("?%d untracked", m.ws.untracked)) + "\n")
			}
		}
		b.WriteString(toolStyle.Render(Short(pirpc.Shorten(m.cwd), inner)) + "\n")
	}
	return b.String()
}

// invokedTools returns only tool calls represented by transcript blocks.
// Block order is the stable invocation order; m.tools is only an index used
// to update a call in place and intentionally cannot define sidebar order.
func (m Model) invokedTools() []Block {
	tools := make([]Block, 0)
	for _, bl := range m.blocks {
		if bl.Kind == "tool" && bl.ToolName != "" {
			tools = append(tools, bl)
		}
	}
	return tools
}

// renderSidebar draws the sidebar box around the visible sideVp slice.
// lipgloss Height covers the content only (border adds 2), so size it by
// sideContentH to keep the outer box exactly sideH tall.
func (m Model) renderSidebar() string {
	h := m.sideContentH()
	body := m.sideVp.View()
	if !m.quitArmed() {
		return sideStyle.Width(sideW).Height(h).Render(body)
	}
	// armed: viewport already shrunk by one line (syncSideH), so the
	// warning pins to the bottom-right corner of the box.
	return sideStyle.Width(sideW).Height(h).Render(body + "\n" + quitArmFooter())
}

// quitArmFooter is the 1-line bottom-right sidebar warning (yellow).
func quitArmFooter() string {
	s := "press Ctrl+C again to quit"
	if w := lipgloss.Width(s); w < sideInnerW {
		s = strings.Repeat(" ", sideInnerW-w) + s
	}
	return warnStyle.Render(s)
}

func sep() string {
	return sepStyle.Render(strings.Repeat("─", sideW-6))
}

func (m Model) sideH() int {
	// sidebar box fills the full terminal height edge-to-edge, so no gap
	// stays above (header row) or below (input box) it
	h := m.winH
	if h < 6 {
		h = 6
	}
	return h
}

// sideContentH is the visible sidebar content height (box minus border).
func (m Model) sideContentH() int {
	h := m.sideH() - 2
	if h < 1 {
		h = 1
	}
	return h
}

// overSide reports whether screen x is over the sidebar column.
func (m Model) overSide(x int) bool {
	return m.showSide() && x >= m.mainW()+1 && x <= m.winW
}

// statsLine mirrors opencode's status footer: model · ctx% · cost, no sidebar.

func (m Model) statsLine() string {
	agent := ""
	if m.CurAgent != "" {
		agent = " · @" + Short(m.CurAgent, 20)
	}
	if m.Stats.ContextPct > 0 {
		return fmt.Sprintf("%s%s · %.0f%% · %s · $%.2f",
			Short(m.ModelLbl, 30), agent, m.Stats.ContextPct, FmtNum(m.Stats.TokensTotal), m.Stats.Cost)
	}
	if m.Stats.Cost > 0 || m.Stats.TokensTotal > 0 {
		return fmt.Sprintf("%s%s · %s · $%.2f",
			Short(m.ModelLbl, 30), agent, FmtNum(m.Stats.TokensTotal), m.Stats.Cost)
	}
	return Short(m.ModelLbl, 40) + agent
}

func (m Model) renderHeader() string {
	left := Short(pirpc.Shorten(m.cwd), 48)
	if m.session != "" {
		left = m.session + " · " + pirpc.Shorten(m.cwd)
	}
	if m.followRemote {
		left = "external · " + left
	}
	left = Short(left, 48)
	// Status lives on the pet row (sidebar) — header keeps cwd/session only.
	return headerStyle.Render(left)
}

func (m Model) renderInput() string {
	mainW := m.mainW()
	innerW := mainW - 4
	if innerW < 10 {
		innerW = 10
	}
	border, title := cInput, ""
	left := "○ ready · ↵ send · / commands · @ files · ^P model · ^R recents · ^C quit"
	plan := m.isPlanMode()
	agentTag := ""
	if m.CurAgent != "" {
		agentTag = " · @" + Short(m.CurAgent, 20)
	}
	if m.followRemote {
		border = cInputDim
		title = "EXTERNAL · READ-ONLY"
		left = "Ctrl+D detach · read-only"
		if m.thinking {
			border = cGreen
			title = "EXTERNAL · " + spinFrame(m.pet.tick) + " " + m.inputStatus()
		}
	} else if m.thinking {
		if plan {
			border = cPlan
			title = "PLAN · " + spinFrame(m.pet.tick) + " " + m.inputStatus() + agentTag
		} else {
			border = cGreen
			title = spinFrame(m.pet.tick) + " " + m.inputStatus() + agentTag
		}
		if m.escArmed() {
			left = "press Esc again to cancel"
		} else {
			left = "↵ steer · ⌥↵ follow-up · Esc×2 cancel"
		}
	} else if plan {
		border = cPlan
		title = "PLAN" + agentTag
	} else if len(m.Dialogs) > 0 && !m.isInlineUI() {
		border = cInputDim
	} else if m.CurAgent != "" {
		title = "@" + Short(m.CurAgent, 30)
	}
	right := m.statsLine()
	if m.extStat != "" {
		right = Short(m.extStat, 30) + " · " + right
	}
	// The footer must stay exactly one visual row: the input box is a
	// fixed 6 rows (textarea 3 + footer 1 + border 2) and the viewport
	// math in Update assumes it. A long model/stats line used to wrap
	// the footer to 2 rows, growing the left column past winH and
	// leaving a gap under the top-aligned sidebar. Hints yield to stats.
	right = Short(right, innerW)
	if room := innerW - lipgloss.Width(right) - 1; room <= 0 {
		left = ""
	} else {
		left = Short(left, room)
	}
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	foot := statusBarStyle.Render(left + strings.Repeat(" ", gap) + right)
	lines := []string{m.ta.View()}
	if m.chipH() > 0 {
		lines = append(lines, m.chipRow(innerW))
	}
	lines = append(lines, foot)
	return inputBox(title, lines, innerW, border)
}

// spinFrames is pi's working spinner: braille dots cycling on the input
// border while a turn runs. Indexed by pet.tick (500ms loop already
// Refresh()es), so no extra timer — the frame advances for free.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinFrame(tick int) string {
	return spinFrames[tick%len(spinFrames)]
}

// inputStatus is the live turn status for the input border: the pet's timed
// label ("Working... 7s" / "Thinking... 3s" / "Writing... 2s") while busy,
// falling back to m.Status before the first stream event anchors the pet.
func (m Model) inputStatus() string {
	if m.pet.status.Busy() {
		return m.petLabel()
	}
	return m.Status
}

// quitArmed reports a live quit arm: one Ctrl+C landed within
// quitArmWindow, so a second one quits.
func (m Model) quitArmed() bool {
	return !m.quitArm.IsZero() && time.Since(m.quitArm) < quitArmWindow
}

// escArmed reports a live cancel arm: one Esc landed within escArmWindow
// while running, so a second one aborts the turn.
func (m Model) escArmed() bool {
	return !m.escArm.IsZero() && time.Since(m.escArm) < escArmWindow
}

// syncSideH reserves one sidebar line for the quit-arm footer while armed.
func (m *Model) syncSideH() {
	if !m.ready {
		return
	}
	h := m.sideContentH()
	if m.quitArmed() && h > 1 {
		h--
	}
	m.sideVp.Height = h
}

// inputBox draws a rounded box with an optional live-status title spliced
// into the top border (pi embeds its working status there while running).
// Content lines are wrapped/padded to innerW; only the frame carries the
// border color so typed text keeps its own colors. Total height matches
// the old style box (textarea rows + chips 0/1 + footer + 2 border rows)
// so the viewport math in Update stays valid.
func inputBox(title string, lines []string, innerW int, border lipgloss.Color) string {
	frame := lipgloss.NewStyle().Foreground(border)
	var b strings.Builder
	if title == "" {
		b.WriteString(frame.Render("╭"+strings.Repeat("─", innerW+2)+"╮") + "\n")
	} else {
		title = Short(title, innerW-1)
		fill := innerW - lipgloss.Width(title) - 1
		if fill < 0 {
			fill = 0
		}
		b.WriteString(frame.Render("╭─ ") + statusBarStyle.Render(title) +
			frame.Render(" "+strings.Repeat("─", fill)+"╮") + "\n")
	}
	wrap := lipgloss.NewStyle().Width(innerW)
	for _, ln := range lines {
		for _, wln := range strings.Split(wrap.Render(ln), "\n") {
			b.WriteString(frame.Render("│ ") + wln + frame.Render(" │") + "\n")
		}
	}
	b.WriteString(frame.Render("╰" + strings.Repeat("─", innerW+2) + "╯"))
	return b.String()
}

func (m Model) renderDialog() string {
	d := m.Dialogs[0]
	if d.Kind == "pconfig" && len(d.Provs) > 0 {
		return m.renderPconfigDialog(d)
	}
	if d.Kind == "model" && len(d.Provs) > 0 {
		return m.renderModelDialog(d)
	}
	if d.Kind == "login" && len(d.Provs) > 0 {
		return m.renderLoginDialog(d)
	}
	if d.Kind == "trajectory" {
		return m.renderTrajectoryDialog(d)
	}
	if d.Kind == "notification" {
		return m.renderNotificationDialog(d)
	}
	if d.Kind == "tree" {
		return m.renderTreeDialog(d)
	}
	if d.Kind == "sessions" {
		return m.renderResumeDialog(d)
	}
	if d.Kind == "askUser" {
		return m.renderAskUserDialog(d)
	}
	if d.Kind == "shortcuts" {
		return m.renderShortcutsDialog(d)
	}
	if d.Kind == "settings" && len(d.Provs) > 0 {
		return m.renderSettingsDialog(d)
	}
	if d.Kind == shortcutKind {
		return m.renderShortcutDialog(d)
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	if filterableDialog(d) {
		b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	}
	// Adaptive box: wide terminals get a wider dialog (settings rows
	// carry long values), small ones keep the old 62-cell box. Free-text
	// input stays a compact popup regardless of terminal width.
	boxW := m.winW - 10
	if boxW < 62 {
		boxW = 62
	}
	if boxW > 100 {
		boxW = 100
	}
	if d.Kind == "input" {
		boxW = 60
	}
	if d.Kind == "secret" {
		b.WriteString("\n")
		b.WriteString(cmdHiStyle.Render(strings.Repeat("•", len(d.Filter))+"▌") + "\n")
		b.WriteString("\n" + toolStyle.Render("Enter save · Esc cancel"))
	} else if d.Kind == "rename" {
		b.WriteString("\n")
		b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
		b.WriteString("\n" + toolStyle.Render("Enter rename · empty clears · Esc back to /login"))
	} else if d.Kind == "input" {
		b.WriteString("\n")
		// pi's ui.input placeholder: a dim hint while the buffer is empty,
		// so the offered context is visible instead of a bare cursor.
		if d.Filter == "" && d.Placeholder != "" {
			b.WriteString(toolStyle.Render(d.Placeholder) + cmdHiStyle.Render("▌") + "\n")
		} else {
			b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
		}
		b.WriteString("\n" + toolStyle.Render("Enter save · Esc cancel"))
	} else {
		b.WriteString("\n")
		rowW := boxW - 10 // cursor mark + dialog padding + border
		// Scroll window follows the cursor; tall screens show more rows
		// (box = title + filter + rows + footer must fit winH).
		win := 12
		if h := m.winH - 10; h > win {
			win = h
		}
		if win > 20 {
			win = 20
		}
		total := len(d.FIdx)
		start := d.Cursor - 4
		if start < 0 {
			start = 0
		}
		if start+win > total {
			start = total - win
		}
		if start < 0 {
			start = 0
		}
		end := start + win
		if end > total {
			end = total
		}
		if start > 0 {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
		}
		lastCat := ""
		for fi := start; fi < end; fi++ {
			ri := d.FIdx[fi]
			if d.Kind == "settings" && ri >= 0 && ri < len(d.Providers) {
				if cat := d.Providers[ri]; cat != "" && cat != lastCat {
					b.WriteString("  " + sideTitleStyle.Width(rowW-2).Render(strings.ToUpper(cat)) + "\n")
					lastCat = cat
				}
			}
			cursor := "  "
			style := statusBarStyle
			if fi == d.Cursor {
				cursor = "▸ "
				style = rowHiStyle
			}
			// Extension option labels are plugin-authored and routinely
			// longer than 44 columns, which the old constant silently ate.
			// Bound them by the box instead, and hand the description only
			// the room that is actually left so a row can never wrap.
			labelW := 44
			if d.Kind == "ui" || d.Kind == "askUser" {
				labelW = rowW - 2
			}
			row := Short(d.Options[ri], labelW)
			if desc := DescOf(d, ri); desc != "" {
				room := rowW - 47
				if d.Kind == "ui" || d.Kind == "askUser" {
					room = rowW - 2 - lipgloss.Width(row) - 3
				}
				if room > 4 {
					row += "  " + toolStyle.Render("— "+Short(desc, room))
				}
			}
			if fi == d.Cursor {
				b.WriteString(cursor + style.Width(rowW).Render(row) + "\n")
			} else {
				b.WriteString(cursor + style.Render(row) + "\n")
			}
		}
		if end < total {
			b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)) + "\n")
		}
		if total == 0 {
			b.WriteString(toolStyle.Render("— no match —") + "\n")
		}
	}
	foot := "↑↓ select · Enter confirm · Esc cancel"
	if d.Kind == "input" {
		foot = "type · Enter save · Esc cancel"
	} else if filterableDialog(d) {
		foot = "type to filter · " + foot
	}
	if d.Kind == "sessions" {
		foot += " · Tab scope · Del delete"
	}
	if d.Kind == "settings" {
		foot = "type to filter · ↑↓ select · Enter change · Esc close"
		if n := len(d.FIdx); n > 0 { // pi-style position (6/33)
			cur := d.Cursor + 1
			if cur > n {
				cur = n
			}
			foot += fmt.Sprintf(" (%d/%d)", cur, n)
		}
	}
	b.WriteString("\n" + toolStyle.Render(foot))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// fixedWin returns a scroll window of exactly win rows: data [start,end)
// plus above/below markers that consume budget rows, so the dialog box
// never resizes while scrolling or switching providers.
func fixedWin(cursor, total, win int) (start, end int, above, below bool) {
	start = cursor - 4
	if start < 0 {
		start = 0
	}
	if start+win > total {
		start = total - win
	}
	if start < 0 {
		start = 0
	}
	cap, above := win, start > 0
	if above {
		cap--
	}
	end = start + cap
	if end > total {
		end = total
	}
	below = end < total
	if below {
		cap--
		// the below-marker eats the last data row: shift the window
		// down so the cursor stays visible.
		if start+cap <= cursor {
			start = cursor - cap + 1
		}
		end = start + cap
		if end > total {
			end = total
		}
		above = start > 0
	}
	return start, end, above, below
}

// renderModelDialog draws the model picker: left = providers, middle =
// their models (name only), right = the highlighted model's specs
// (oh-my-pi style; narrow terminals fold the specs into a footer panel).
// ↑↓ moves in the focused pane, ←/→/Tab switches pane, typing filters,
// Enter selects.
func (m Model) renderModelDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 150 {
		boxW = 150
	}
	leftW := 42
	if boxW < 120 {
		leftW = 36
	}
	if boxW < 100 {
		leftW = 28
	}
	// Three columns when the specs fit: providers | model names |
	// details. Narrow terminals keep the classic two panes with the
	// specs as a footer panel instead.
	detW := 40
	detailCol := len(d.Models) > 0 && boxW >= 110
	rightW := boxW - 8 - leftW - 3
	if detailCol {
		rightW -= detW + 3
		if rightW < 16 {
			rightW = 16
		}
	} else if rightW < 30 {
		rightW = 30
	}
	win := m.winH - 14
	if win < 12 {
		win = 12
	}
	if win > 24 {
		win = 24
	}
	if !detailCol {
		// The footer spec panel costs ~14 rows: steal from the panes
		// so the whole box still fits short terminals instead of
		// overflowing them.
		if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
			if _, ok := d.modelAt(d.FIdx[d.Cursor]); ok && win > m.winH-25 {
				win = m.winH - 25
				if win < 6 {
					win = 6
				}
			}
		}
	}

	f := strings.ToLower(d.Filter)
	matchText := func(i int) bool {
		return f == "" || strings.Contains(strings.ToLower(d.Options[i]), f) ||
			(i < len(d.Descs) && strings.Contains(strings.ToLower(d.Descs[i]), f)) ||
			strings.Contains(strings.ToLower(normProv(providerAt(d.Providers, i))), f) ||
			strings.Contains(d.specHay(i), f)
	}
	provCount := func(prov string) int {
		n := 0
		for i := range d.Options {
			if prov != "All" && normProv(providerAt(d.Providers, i)) != prov {
				continue
			}
			if matchText(i) {
				n++
			}
		}
		return n
	}

	// left window (providers): always win rows — markers take budget rows
	// so the box never resizes while scrolling.
	ptotal := len(d.Provs)
	pstart, pend, pAbove, pBelow := fixedWin(d.ProvCursor, ptotal, win)
	var leftLines []string
	if pAbove {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d above)", pstart)))
	}
	for pi := pstart; pi < pend; pi++ {
		name := d.Provs[pi]
		cntStr := fmt.Sprintf("%d", provCount(name))
		cw := leftW - 2
		dot := "  "
		if name != "All" {
			dot = statusBarStyle.Render("○ ")
			if d.ProvConn[name] {
				dot = okStyle.Render("● ")
			}
		}
		nm := Short(name, cw-2-len(cntStr)-1)
		pad := cw - 2 - lipgloss.Width(nm) - len(cntStr)
		if pad < 1 {
			pad = 1
		}
		// Fit clamps the unstyled tail so a long name can never wrap
		// the row and break the two-pane layout (dot stays styled).
		content := dot + Fit(nm+strings.Repeat(" ", pad)+cntStr, cw-2)
		mark := "  "
		style := statusBarStyle
		if provCount(name) == 0 {
			style = toolStyle
		}
		if pi == d.ProvCursor {
			mark = "▸ "
			if d.ProvFocus {
				style = rowHiStyle
			} else {
				style = lipgloss.NewStyle().Foreground(cText)
			}
		}
		leftLines = append(leftLines, mark+style.Width(leftW-2).Render(content))
	}
	if pBelow {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d below)", ptotal-pend)))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Width(leftW-2).Render(""))
	}

	// right window (models): same fixed-win rule as the left pane.
	total := len(d.FIdx)
	start, end, rAbove, rBelow := fixedWin(d.Cursor, total, win)
	var rightLines []string
	if rAbove {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d above)", start)))
	}
	optW := 28
	if rightW-10 < optW {
		optW = rightW - 10
	}
	if optW < 10 {
		optW = 10
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		mark := "  "
		style := statusBarStyle
		if fi == d.Cursor {
			mark = "▸ "
			if d.ProvFocus {
				style = lipgloss.NewStyle().Foreground(cText)
			} else {
				style = rowHiStyle
			}
		}
		row := Short(d.Options[ri], optW)
		if !detailCol {
			// Wide layout shows the specs in the details column, so
			// the middle column stays a clean name-only list.
			if desc := DescOf(d, ri); desc != "" {
				row += "  " + toolStyle.Render("— "+Short(desc, rightW-4-optW-3))
			}
		}
		star := toolStyle.Render("☆")
		if d.isFavIdx(ri) {
			star = warnStyle.Render("★")
		}
		rightLines = append(rightLines, mark+style.Width(rightW-4).Render(row)+" "+star)
	}
	if rBelow {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	if total == 0 {
		msg := "— no match —"
		if p := d.selProv(); p != "" && !d.ProvConn[p] {
			msg = "not connected · ^L login"
		}
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(msg))
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+statusBarStyle.Width(rightW-2).Render(""))
	}

	// headers (providers pane = global search, so show All; models pane
	// stays scoped to the selected provider)
	sel := "All"
	if p := d.selProv(); p != "" && (d.Filter == "" || !d.ProvFocus) {
		sel = p
	}
	sep := sepStyle.Render("│")
	if !detailCol {
		b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("PROVIDERS") + " │ " +
			"  " + sideTitleStyle.Width(rightW-2).Render(sel+" · "+fmt.Sprintf("%d", total)) + "\n")

		n := len(leftLines)
		if len(rightLines) > n {
			n = len(rightLines)
		}
		for i := 0; i < n; i++ {
			l, r := "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			} else {
				l = "  " + statusBarStyle.Width(leftW-2).Render("")
			}
			if i < len(rightLines) {
				r = rightLines[i]
			} else {
				r = "  " + statusBarStyle.Width(rightW-2).Render("")
			}
			b.WriteString(l + " " + sep + " " + r + "\n")
		}
	} else {
		b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("PROVIDERS") + " │ " +
			"  " + sideTitleStyle.Width(rightW-2).Render(sel+" · "+fmt.Sprintf("%d", total)) + " │ " +
			"  " + sideTitleStyle.Width(detW-2).Render("DETAILS") + "\n")

		detLines := d.detailLines(detW)
		for len(detLines) < win {
			detLines = append(detLines, "  "+statusBarStyle.Width(detW-2).Render(""))
		}
		n := len(leftLines)
		if len(rightLines) > n {
			n = len(rightLines)
		}
		if len(detLines) > n {
			n = len(detLines)
		}
		for i := 0; i < n; i++ {
			l, r, dt := "", "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			} else {
				l = "  " + statusBarStyle.Width(leftW-2).Render("")
			}
			if i < len(rightLines) {
				r = rightLines[i]
			} else {
				r = "  " + statusBarStyle.Width(rightW-2).Render("")
			}
			if i < len(detLines) {
				dt = detLines[i]
			} else {
				dt = "  " + statusBarStyle.Width(detW-2).Render("")
			}
			b.WriteString(l + " " + sep + " " + r + " " + sep + " " + dt + "\n")
		}
	}

	foot := "↑↓ providers · → models · type to search all · Enter open · ^F star · ^L login · Esc close"
	if !d.ProvFocus {
		foot = "↑↓ models · ← providers · Tab switch · type filters here · Enter select · ^F star · ^L login · Esc close"
	}
	if !detailCol && len(d.Models) > 0 {
		b.WriteString("\n" + strings.Join(d.detailLines(boxW-4), "\n") + "\n\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(toolStyle.Render(foot))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// renderSettingsDialog draws the two-pane /settings picker: left = groups
// with row counts, right = the selected group's rows (label + value +
// action hint, labels in one column so values align). ↑↓ moves in the
// focused pane, ←/→/Tab switches pane, typing filters, Enter changes
// the value.
func (m Model) renderSettingsDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 120 {
		boxW = 120
	}
	leftW := 22
	rightW := boxW - 8 - leftW - 3
	if rightW < 30 {
		rightW = 30
	}
	win := m.winH - 12
	if win < 8 {
		win = 8
	}
	if win > 20 {
		win = 20
	}

	f := strings.ToLower(d.Filter)
	matchText := func(i int) bool {
		return f == "" || strings.Contains(strings.ToLower(d.Options[i]), f) ||
			(i < len(d.Descs) && strings.Contains(strings.ToLower(d.Descs[i]), f)) ||
			strings.Contains(strings.ToLower(normProv(providerAt(d.Providers, i))), f)
	}
	groupCount := func(g string) int {
		n := 0
		for i := range d.Options {
			if g != "All" && normProv(providerAt(d.Providers, i)) != g {
				continue
			}
			if matchText(i) {
				n++
			}
		}
		return n
	}

	// left window (groups): always win rows — markers take budget rows
	// so the box never resizes while scrolling.
	ptotal := len(d.Provs)
	pstart, pend, pAbove, pBelow := fixedWin(d.ProvCursor, ptotal, win)
	var leftLines []string
	if pAbove {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d above)", pstart)))
	}
	for pi := pstart; pi < pend; pi++ {
		name := d.Provs[pi]
		cntStr := fmt.Sprintf("%d", groupCount(name))
		nm := Short(name, leftW-2-len(cntStr)-1)
		pad := leftW - 2 - lipgloss.Width(nm) - len(cntStr)
		if pad < 1 {
			pad = 1
		}
		content := Fit(nm+strings.Repeat(" ", pad)+cntStr, leftW-2)
		mark := "  "
		style := statusBarStyle
		if groupCount(name) == 0 {
			style = toolStyle
		}
		if pi == d.ProvCursor {
			mark = "▸ "
			if d.ProvFocus {
				style = rowHiStyle
			} else {
				style = lipgloss.NewStyle().Foreground(cText)
			}
		}
		leftLines = append(leftLines, mark+style.Width(leftW-2).Render(content))
	}
	if pBelow {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d below)", ptotal-pend)))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Width(leftW-2).Render(""))
	}

	// right window (rows): labels share one column so values align.
	labels := make([]string, len(d.FIdx))
	values := make([]string, len(d.FIdx))
	labelW := 0
	for fi, ri := range d.FIdx {
		lbl, val := d.Options[ri], ""
		if kv := strings.SplitN(d.Options[ri], ": ", 2); len(kv) == 2 {
			lbl, val = kv[0], kv[1]
		}
		labels[fi], values[fi] = Short(lbl, 24), val
		if w := lipgloss.Width(labels[fi]); w > labelW {
			labelW = w
		}
	}
	total := len(d.FIdx)
	start, end, rAbove, rBelow := fixedWin(d.Cursor, total, win)
	var rightLines []string
	if rAbove {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d above)", start)))
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		mark := "  "
		style := statusBarStyle
		if fi == d.Cursor {
			mark = "▸ "
			if d.ProvFocus {
				style = lipgloss.NewStyle().Foreground(cText)
			} else {
				style = rowHiStyle
			}
		}
		base := Fit(labels[fi], labelW) + "  " + Short(values[fi], rightW-4-labelW-2)
		row := base
		if desc := DescOf(d, ri); desc != "" {
			// remaining cells inside the pane: gap(2) + "— "(2)
			if dw := rightW - 2 - lipgloss.Width(base) - 4; dw >= 8 {
				row += "  " + toolStyle.Render("— "+Short(desc, dw))
			}
		}
		rightLines = append(rightLines, mark+style.Width(rightW-2).Render(row))
	}
	if rBelow {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	if total == 0 {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render("— no match —"))
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+statusBarStyle.Width(rightW-2).Render(""))
	}

	// headers (left pane = global search, so show All; right pane stays
	// scoped to the selected group)
	sel := "All"
	if d.ProvCursor >= 0 && d.ProvCursor < len(d.Provs) && (d.Filter == "" || !d.ProvFocus) {
		sel = d.Provs[d.ProvCursor]
	}
	sep := sepStyle.Render("│")
	b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("GROUPS") + " │ " +
		"  " + sideTitleStyle.Width(rightW-2).Render(sel+" · "+fmt.Sprintf("%d", total)) + "\n")
	n := len(leftLines)
	if len(rightLines) > n {
		n = len(rightLines)
	}
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		} else {
			l = "  " + statusBarStyle.Width(leftW-2).Render("")
		}
		if i < len(rightLines) {
			r = rightLines[i]
		} else {
			r = "  " + statusBarStyle.Width(rightW-2).Render("")
		}
		b.WriteString(l + " " + sep + " " + r + "\n")
	}

	foot := "↑↓ groups · → settings · type searches all · Enter change · Esc close"
	if !d.ProvFocus {
		foot = "↑↓ settings · ← groups · Tab switch · type filters here · Enter change · Esc close"
	}
	if n := len(d.FIdx); n > 0 { // pi-style position (6/33)
		cur := d.Cursor + 1
		if cur > n {
			cur = n
		}
		foot += fmt.Sprintf(" (%d/%d)", cur, n)
	}
	b.WriteString("\n" + toolStyle.Render(foot))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// renderAskUserDialog mirrors pi's rich single-select fallback: numbered,
// filterable choices on the left and the selected title/description on the
// right. Below a usable width it becomes a safe single column with the
// selected detail directly under the list.
func (m Model) renderAskUserDialog(d *Dialog) string {
	boxW := m.winW - 4
	if boxW < 20 {
		boxW = 20
	}
	if boxW > 100 {
		boxW = 100
	}
	placeH := max(1, m.winH-2)
	pending := len(m.Dialogs) > 1
	if pending {
		placeH = max(1, placeH-1)
	}
	// dlgStyle adds two border and two padding rows. The remaining body is
	// divided between the wrapped prompt, list/detail pane, and two-row footer.
	bodyBudget := max(1, placeH-5-5)
	messageLimit := min(8, max(1, bodyBudget/3))
	messageLines := wrapAskMessage(d.Message, boxW-8)
	if len(messageLines) > messageLimit {
		missing := len(messageLines) - messageLimit + 1
		messageLines = append(messageLines[:messageLimit-1], fmt.Sprintf("…(+%d context lines)", missing))
	}
	paneRows := min(14, max(1, bodyBudget-len(messageLines)))

	var head strings.Builder
	head.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	for _, line := range messageLines {
		head.WriteString(statusBarStyle.Render(line) + "\n")
	}
	head.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n\n")

	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, paneRows)
	selected := -1
	if d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		selected = d.FIdx[d.Cursor]
	}

	split := boxW >= 76
	leftW := boxW - 8
	rightW := boxW - 8 - leftW
	if split {
		leftW = (boxW - 8) * 42 / 100
		rightW = boxW - 8 - leftW
	}
	left := make([]string, 0, paneRows)
	if above {
		left = append(left, toolStyle.Render(fmt.Sprintf("…(+%d above)", start)))
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		mark, style := "  ", statusBarStyle
		if fi == d.Cursor {
			mark, style = "▸ ", rowHiStyle
		}
		titleStyle := lipgloss.NewStyle().Foreground(cText)
		if strings.Contains(strings.ToLower(d.Options[ri]), "type custom response") {
			titleStyle = lipgloss.NewStyle().Foreground(cAccent)
		}
		row := mark + titleStyle.Render(fmt.Sprintf("%d. %s", ri+1, Short(d.Options[ri], leftW-lipgloss.Width(mark)-3)))
		left = append(left, style.Width(leftW).Render(row))
	}
	if below {
		left = append(left, toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	if total == 0 {
		left = append(left, toolStyle.Render("— no match —"))
	}

	right := []string{}
	if selected >= 0 {
		selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(cText)
		if strings.Contains(strings.ToLower(d.Options[selected]), "type custom response") {
			selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
		}
		right = append(right, selectedStyle.Render(d.Options[selected]))
		desc := DescOf(d, selected)
		descWidth := rightW
		if !split {
			descWidth = leftW
		}
		descLines := []string{}
		if desc != "" {
			for _, line := range wrapWords(desc, descWidth) {
				descLines = append(descLines, statusBarStyle.Render(line))
			}
		}
		if split {
			descBudget := paneRows - 4 // title + gap + divider + Enter hint
			if descBudget > 0 {
				if len(descLines) > descBudget {
					descLines = append(descLines[:max(1, descBudget-1)], toolStyle.Render("…"))
				}
				right = append(right, "")
				right = append(right, descLines...)
			}
			right = append(right, "", sepStyle.Render(strings.Repeat("─", max(10, rightW))), toolStyle.Render("↵ Enter to select"))
		} else {
			rightBudget := max(0, paneRows-len(left))
			if rightBudget > 0 {
				if len(descLines) > rightBudget-1 {
					descLines = append(descLines[:max(0, rightBudget-2)], toolStyle.Render("…"))
				}
				right = append(right, descLines...)
			}
		}
	}

	content := []string{}
	if split {
		n := min(paneRows, max(len(left), len(right)))
		sep := sepStyle.Render("│")
		for i := 0; i < n; i++ {
			l, r := strings.Repeat(" ", leftW), ""
			if i < len(left) {
				l = statusBarStyle.Width(leftW).Render(left[i])
			}
			if i < len(right) {
				r = statusBarStyle.Width(rightW).Render(right[i])
			}
			content = append(content, l+" "+sep+" "+r)
		}
	} else {
		content = append(content, left...)
		if len(right) > 0 {
			content = append(content, right[:min(len(right), paneRows-len(content))]...)
		}
	}
	foot := "↑↓ select · type to filter · Enter confirm · Esc cancel"
	if !split {
		foot = "↑↓ select · type · Enter · Esc cancel"
	}
	content = append(content, "", toolStyle.Render(foot))
	box := dlgStyle.Width(boxW).Render(head.String() + strings.Join(content, "\n"))
	placed := lipgloss.Place(m.winW, max(1, m.winH-2), lipgloss.Center, lipgloss.Center, box)
	if pending {
		return lipgloss.JoinVertical(lipgloss.Center, placed, statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1)))
	}
	return placed
}

// wrapAskMessage preserves logical lines and blank lines while wrapping each
// non-empty line to the dialog's available width.
func wrapAskMessage(message string, width int) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapWords(line, width)...)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func DescOf(d *Dialog, ri int) string {
	if ri >= 0 && ri < len(d.Descs) {
		return d.Descs[ri]
	}
	return ""
}

// renderLoginDialog draws the two-pane /login picker: left = providers,
// right = saved keys for the selected provider + actions (add / OAuth /
// reload). ↑↓ moves in the focused pane, ←/→/Tab switches pane, typing
// filters providers, Enter uses/adds, ⌫ deletes the selected key.
func (m Model) renderLoginDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 150 {
		boxW = 150
	}
	leftW := 42
	if boxW < 120 {
		leftW = 36
	}
	if boxW < 100 {
		leftW = 28
	}
	rightW := boxW - 8 - leftW - 3
	if rightW < 30 {
		rightW = 30
	}
	win := m.winH - 14
	if win < 12 {
		win = 12
	}
	if win > 24 {
		win = 24
	}

	loginLabel := func(prov string) (label, env string) {
		return pirpc.ProviderLabel(prov), pirpc.LookupEnv(prov)
	}

	// left window (providers, filtered via PIdx)
	ptotal := len(d.PIdx)
	pstart, pend, pAbove, pBelow := fixedWin(d.ProvCursor, ptotal, win)
	var leftLines []string
	if pAbove {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d above)", pstart)))
	}
	for pi := pstart; pi < pend; pi++ {
		raw := d.PIdx[pi]
		prov := ""
		if raw >= 0 && raw < len(d.Provs) {
			prov = d.Provs[raw]
		}
		label, _ := loginLabel(prov)
		cnt := d.LoginCounts[prov]
		cntStr := "no key"
		if cnt == 1 {
			cntStr = "1 key"
		} else if cnt > 1 {
			cntStr = fmt.Sprintf("%d keys", cnt)
		}
		if d.OAuthConn[prov] {
			if cntStr == "no key" {
				cntStr = "OAuth"
			} else {
				cntStr += "+OAuth"
			}
		}
		cw := leftW - 2
		dot := statusBarStyle.Render("○ ")
		if d.ProvConn[prov] {
			dot = okStyle.Render("● ")
		}
		nm := Short(label, cw-2-len(cntStr)-1)
		pad := cw - 2 - lipgloss.Width(nm) - len(cntStr)
		if pad < 1 {
			pad = 1
		}
		// Fit clamps the unstyled tail so a long name can never wrap
		// the row and break the two-pane layout (dot stays styled).
		content := dot + Fit(nm+strings.Repeat(" ", pad)+cntStr, cw-2)
		mark := "  "
		style := statusBarStyle
		if pi == d.ProvCursor {
			mark = "▸ "
			if d.ProvFocus {
				style = rowHiStyle
			} else {
				style = lipgloss.NewStyle().Foreground(cText)
			}
		}
		leftLines = append(leftLines, mark+style.Width(leftW-2).Render(content))
	}
	if pBelow {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render(fmt.Sprintf("…(+%d below)", ptotal-pend)))
	}
	if ptotal == 0 {
		leftLines = append(leftLines, "  "+toolStyle.Width(leftW-2).Render("— no match —"))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Width(leftW-2).Render(""))
	}

	// right window (keys + actions, unfiltered)
	total := len(d.Options)
	start, end, rAbove, rBelow := fixedWin(d.KeyCursor, total, win)
	var rightLines []string
	if rAbove {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d above)", start)))
	}
	for fi := start; fi < end; fi++ {
		mark := "  "
		style := statusBarStyle
		if fi == d.KeyCursor {
			mark = "▸ "
			if d.ProvFocus {
				style = lipgloss.NewStyle().Foreground(cText)
			} else {
				style = rowHiStyle
			}
		}
		row := Short(d.Options[fi], rightW-2)
		if fi < len(d.Descs) && d.Descs[fi] != "" {
			// keep the row + dim desc on one line within rightW
			desc := Short(d.Descs[fi], rightW-4)
			room := rightW - 2 - lipgloss.Width(row) - lipgloss.Width(desc) - 3
			if room >= 1 && lipgloss.Width(row)+3+lipgloss.Width(desc) <= rightW-2 {
				row += "  " + toolStyle.Render("— "+desc)
			} else {
				row = Short(d.Options[fi], rightW-2-len(desc)-4) + "  " + toolStyle.Render("— "+desc)
			}
		}
		rightLines = append(rightLines, mark+style.Width(rightW-2).Render(row))
	}
	if rBelow {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render(fmt.Sprintf("…(+%d below)", total-end)))
	}
	if total == 0 {
		rightLines = append(rightLines, "  "+toolStyle.Width(rightW-2).Render("— no keys —"))
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+statusBarStyle.Width(rightW-2).Render(""))
	}

	// headers: right shows the selected provider + key count
	sel, selLabel, selCount := "", "", 0
	if prov := d.SelLoginProv(); prov != "" {
		sel = prov
		selLabel, _ = loginLabel(prov)
		selCount = d.LoginCounts[prov]
	}
	rightHead := selLabel
	if rightHead == "" {
		rightHead = "KEYS"
	} else if selCount == 1 {
		rightHead += " · 1 key"
	} else {
		rightHead += fmt.Sprintf(" · %d keys", selCount)
	}
	if d.LoginOAuth {
		rightHead += " · OAuth ✓"
	}
	_ = sel
	b.WriteString("  " + sideTitleStyle.Width(leftW-2).Render("PROVIDERS") + " │ " +
		"  " + sideTitleStyle.Width(rightW-2).Render(rightHead) + "\n")

	n := len(leftLines)
	if len(rightLines) > n {
		n = len(rightLines)
	}
	sep := sepStyle.Render("│")
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		} else {
			l = "  " + statusBarStyle.Width(leftW-2).Render("")
		}
		if i < len(rightLines) {
			r = rightLines[i]
		} else {
			r = "  " + statusBarStyle.Width(rightW-2).Render("")
		}
		b.WriteString(l + " " + sep + " " + r + "\n")
	}

	foot := "↑↓ move · ←→/Tab switch · Enter use/add · ⌫ del · s show · r rename · ^P models · Esc close"
	if d.ProvFocus {
		foot = "↑↓ providers · → keys · type to filter · Enter open · Esc close"
	}
	b.WriteString("\n" + toolStyle.Render(foot))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// teamWidgetHeightLimit reserves the fixed frame, task widget, popup and at
// least three chat rows. renderInput already includes chip rows.
func (m Model) teamWidgetHeightLimit() int {
	if m.winH <= 0 {
		return len(m.TeamWidgetLines) + boolInt(m.TeamStatus != "")
	}
	fixed := lipgloss.Height(m.renderHeader()) + lipgloss.Height(m.renderInput()) + lipgloss.Height(m.renderTaskWidget())
	popup := m.popupH() + m.atPopupH() + m.uiPopupH() + m.inputPopupH()
	return max(0, m.winH-fixed-popup-3)
}

// teamPanelMaxRows is the hard ceiling on the compact live panel. The /command
// popup shows at most palette.Win rows, so the team dashboard gets the same
// popup-sized class: bounded, predictable, and never able to claim the rows
// the transcript needs. /team is unchanged and still renders the full
// dashboard, so nothing is lost by capping the live surface.
const teamPanelMaxRows = 8

// teamPanelHeightLimit is the pool the compact panel may draw from: the
// frame's own reserve (header, input, task widget, every popup, and at least
// three chat rows) intersected with the popup-sized cap. Both bounds matter —
// the cap keeps the panel small on tall terminals, the reserve keeps it from
// eating the chat on short ones.
func (m Model) teamPanelHeightLimit() int {
	return max(0, min(m.teamWidgetHeightLimit(), teamPanelMaxRows))
}

// teamPanelH is how many rows the compact panel actually paints. Zero means
// "no panel", which is what keeps the frame arithmetic in View exact.
func (m Model) teamPanelH() int { return panelHeight(m.renderTeamWidget()) }

// teamPanelActive reports whether the live panel is eligible to paint, so a
// window resize syncs the viewport for it exactly like an open popup does.
func (m Model) teamPanelActive() bool {
	return len(m.TeamWidgetLines) > 0 && m.TeamWidgetSeen && m.TeamWidgetVisible
}

// chatFrameRows is the chat's share of the frame. Everything the frame paints
// outside the chat — header, input, live panels, popups — is measured here so
// the parts sum to exactly winH. alloc is what m.vp already holds after
// applyTeamPanelH/applyPopupH; the min() is the backstop for state that
// changed without a sync (a raw field write, a resize that skipped it), where
// the chat gives rows back instead of letting the frame overflow.
func (m Model) chatFrameRows(alloc, panels int, inlineUI bool) int {
	if m.winH <= 0 {
		return alloc
	}
	overhead := lipgloss.Height(m.renderHeader()) + lipgloss.Height(m.renderInput()) + panels
	if inlineUI || m.cmdOpen || m.atOpen || m.inputOpen() {
		overhead += m.popupH() + m.atPopupH() + m.uiPopupH() + m.inputPopupH()
	}
	return max(0, min(alloc, m.winH-overhead))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// panelHeight is lipgloss.Height with the empty-string-is-one-row trap
// handled. A panel that rendered "" must cost zero rows, not one — the
// View() reserve/join math below is only exact if this holds.
func panelHeight(s string) int {
	if s == "" {
		return 0
	}
	return lipgloss.Height(s)
}

// extPanelBudget is the row pool the generic plugin panels may draw from.
// It mirrors teamWidgetHeightLimit (which already reserves the header, the
// input box, the task widget, every popup and three chat rows) and subtracts
// what the team dashboard already took, so the two panel families can never
// each claim the full budget and overflow the frame.
func (m Model) extPanelBudget(teamH int) int {
	return max(0, m.teamWidgetHeightLimit()-teamH)
}

// renderExtWidgets draws every generic (non-team) extension panel for one
// placement, in m.extWidgetKeys() order. This is what makes pi-lens,
// plan-mode, web-activity and any other setWidget plugin visible as a live
// surface instead of a one-shot notice that scrolled away.
//
// Constraints mirror the team panel: the frame budget is never exceeded, the
// real terminal width is never painted past (mainW has a small-terminal
// floor the widget must not inherit), every line is clipped with truncANSI
// so ANSI survives, and "" is returned when there is no room so the viewport
// math in View() stays valid.
func (m Model) renderExtWidgets(placement string, budget int) string {
	if budget <= 0 {
		return ""
	}
	// mainW has a small-terminal floor for the main layout; a panel must
	// never inherit that floor and paint past the actual terminal width.
	width := max(1, min(m.winW, m.mainW()))
	var keys []string
	for _, key := range m.extWidgetKeys() {
		if p := m.extWidgetPanel(key); p != nil && p.Placement == placement && len(p.Lines) > 0 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	var out []string
	rows, dropped := 0, 0
	for _, key := range keys {
		p := m.extWidgetPanel(key)
		room := budget - rows - 1 // one row for this panel's owner tag
		if room <= 0 {
			dropped += 1 + len(p.Lines)
			continue
		}
		lines := p.Lines
		if len(lines) > room {
			// A panel's tail is the live part, so overflow drops from the
			// head — but the head is the plugin's own summary, so say so
			// rather than silently swapping one for the other.
			dropped += len(lines) - room
			lines = lines[len(lines)-room:]
		}
		out = append(out, truncANSI(sideTitleStyle.Render("["+key+"]"), width))
		for _, line := range lines {
			out = append(out, truncANSI(line, width))
		}
		rows += 1 + len(lines)
	}
	if dropped > 0 && rows < budget {
		out = append(out, truncANSI(toolStyle.Render(fmt.Sprintf("… +%d plugin rows hidden", dropped)), width))
		rows++
	}
	if len(out) > budget {
		out = out[:budget]
	}
	return strings.Join(out, "\n")
}

func teamWorkerStart(line string) bool {
	plain := strings.TrimSpace(stripANSI(line))
	if teamWorkerSummary(line) {
		return false
	}
	return strings.HasPrefix(plain, "├ ") || strings.HasPrefix(plain, "└ ")
}

func teamWorkerSummary(line string) bool {
	plain := strings.TrimSpace(stripANSI(line))
	return strings.HasPrefix(plain, "└ +")
}

// renderTeamWidget preserves pi-agents-team's authoritative hierarchy. The
// package prefix and roster heading are structural anchors; only complete
// worker/activity blocks are selected when rows are scarce.
func (m Model) renderTeamWidget() string {
	budget := m.teamPanelHeightLimit()
	if budget <= 0 {
		return ""
	}
	// mainW has a small-terminal floor for the main layout; the widget must
	// never inherit that floor and paint past the actual terminal width.
	width := max(1, min(m.winW, m.mainW()))
	fit := func(line string) string { return truncANSI(line, width) }

	status := ""
	if m.TeamStatus != "" {
		status = toolStyle.Render("TEAM") + "  " + truncANSI(m.TeamStatus, max(1, width-6))
	}
	statusOnly := func() string {
		if status == "" {
			return ""
		}
		return fit(status)
	}
	if len(m.TeamWidgetLines) == 0 || !m.TeamWidgetSeen {
		return statusOnly()
	}
	if !m.TeamWidgetVisible {
		return statusOnly()
	}

	// Everything before the roster heading is a structural prefix. This
	// includes the optional standalone usage line emitted by pi-agents-team.
	agents := -1
	for i, line := range m.TeamWidgetLines {
		if strings.Contains(stripANSI(line), "● Agents") {
			agents = i
			break
		}
	}
	prefixEnd := len(m.TeamWidgetLines)
	if agents >= 0 {
		prefixEnd = agents
	} else if len(m.TeamWidgetLines) > 0 {
		// Snapshots without a roster heading (the followed-session roster
		// builds one line per worker) need the same hierarchy, otherwise the
		// popup-sized cap would blank the whole panel instead of trimming
		// it. The first line is the panel title, so it becomes the heading
		// and every remaining row is content that may be dropped.
		agents, prefixEnd = 0, 0
	}
	base := append([]string{}, m.TeamWidgetLines[:prefixEnd]...)
	if status != "" {
		base = append([]string{status}, base...)
	}
	if len(base) >= budget {
		return statusOnly()
	}

	// Legacy/test snapshots may not contain a roster heading; the title row
	// was promoted to one above, so every remaining row is a block and the
	// shared selection below trims it with an honest overflow marker.
	heading := m.TeamWidgetLines[agents]
	blocks := make([][]string, 0)
	summary := ""
	// openWorker records whether the box-drawing hierarchy owns the current
	// block; a flat snapshot has no such hierarchy, so its rows are
	// independent and must not be glued to the row above them.
	openWorker := false
	for _, line := range m.TeamWidgetLines[agents+1:] {
		switch {
		case teamWorkerSummary(line):
			summary = line
			openWorker = false
		case teamWorkerStart(line):
			blocks = append(blocks, []string{line})
			openWorker = true
		case openWorker:
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], line)
		default:
			// A flat snapshot (no box-drawing hierarchy) has independent
			// rows, so each one becomes its own block and the cap can still
			// drop it whole instead of silently discarding it.
			blocks = append(blocks, []string{line})
		}
	}

	all := append(append([]string{}, base...), heading)
	all = append(all, flattenTeamBlocks(blocks)...)
	if summary != "" {
		all = append(all, summary)
	}
	if len(all) <= budget {
		return strings.Join(mapLines(all, fit), "\n")
	}

	// The heading is mandatory whenever there is room for worker content. The
	// last row is always a local, truthful overflow marker; if the package
	// summary is meaningful and there is room, preserve it as an additional
	// row so `/team to view` remains discoverable.
	mandatory := append(append([]string{}, base...), heading)
	if len(mandatory) >= budget {
		return statusOnly()
	}
	available := budget - len(mandatory)
	reserveSummary := 0
	if summary != "" && available >= 2 {
		reserveSummary = 1
	}
	blockBudget := available - 1 - reserveSummary
	if blockBudget < 0 {
		blockBudget = 0
	}
	selected := 0
	used := 0
	for selected < len(blocks) && used+len(blocks[selected]) <= blockBudget {
		used += len(blocks[selected])
		selected++
	}

	lines := append([]string{}, mandatory...)
	lines = append(lines, flattenTeamBlocks(blocks[:selected])...)
	hiddenBlocks := len(blocks) - selected
	hiddenRows := teamRowsHidden(blocks, selected)
	if reserveSummary == 1 {
		lines = append(lines, summary)
	} else if summary != "" {
		hiddenRows++
	}
	overflow := fmt.Sprintf("  … %d rows hidden", hiddenRows)
	if hiddenBlocks > 0 {
		overflow = fmt.Sprintf("  … %d worker blocks hidden · %d rows hidden", hiddenBlocks, hiddenRows)
	}
	lines = append(lines, overflow)
	if len(lines) > budget {
		lines = lines[:budget]
	}
	return strings.Join(mapLines(lines, fit), "\n")
}

func mapLines(lines []string, fit func(string) string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fit(line)
	}
	return out
}

func flattenTeamBlocks(blocks [][]string) []string {
	rows := 0
	for _, block := range blocks {
		rows += len(block)
	}
	out := make([]string, 0, rows)
	for _, block := range blocks {
		out = append(out, block...)
	}
	return out
}

func teamRowsHidden(blocks [][]string, selected int) int {
	rows := 0
	for _, block := range blocks[selected:] {
		rows += len(block)
	}
	return rows
}

func (m Model) View() string {
	if !m.ready {
		return "starting…"
	}
	if len(m.Dialogs) > 0 && !m.isInlineUI() && m.Dialogs[0].Kind != "input" {
		if m.Dialogs[0].Kind == "team" {
			return m.renderFloatingTeamDashboard()
		}
		return m.renderDialog()
	}
	inlineUI := m.isInlineUI()
	teamPanel := m.renderTeamWidget()
	teamAbove := teamPanel != "" && m.TeamWidgetPlacement != "belowEditor"
	teamBelow := teamPanel != "" && m.TeamWidgetPlacement == "belowEditor"
	taskPanel := m.renderTaskWidget()
	// The team dashboard and the generic plugin panels share one row pool:
	// both measure against teamWidgetHeightLimit, so without this
	// subtraction each family would claim the full budget and the stack
	// would grow past winH.
	extBudget := m.extPanelBudget(panelHeight(teamPanel))
	extAbove := m.renderExtWidgets("aboveEditor", extBudget)
	extBelow := m.renderExtWidgets("belowEditor", max(0, extBudget-panelHeight(extAbove)))
	chatVp := m.vp
	// The team panel's rows are already reserved from m.vp by
	// applyTeamPanelH (same path as the popups), so the local copy only gives
	// up rows for the task widget and the generic plugin panels — the two
	// surfaces that are derived at paint time. lipgloss reports an empty
	// string as one row, so measure them with panelHeight.
	extra := panelHeight(taskPanel) + panelHeight(extAbove) + panelHeight(extBelow)
	chatVp.Height = m.chatFrameRows(max(0, chatVp.Height-extra), panelHeight(teamPanel)+extra, inlineUI)
	chatView := func() string { return padToHeight(chatVp.View(), chatVp.Height) }
	bodyParts := []string{chatView()}
	if teamAbove {
		bodyParts = append(bodyParts, teamPanel)
	}
	if taskPanel != "" {
		bodyParts = append(bodyParts, taskPanel)
	}
	if extAbove != "" {
		bodyParts = append(bodyParts, extAbove)
	}
	bodyParts = append(bodyParts, m.renderInput())
	if extBelow != "" {
		bodyParts = append(bodyParts, extBelow)
	}
	if teamBelow {
		bodyParts = append(bodyParts, teamPanel)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, bodyParts...)
	if m.cmdOpen || m.atOpen || inlineUI || m.inputOpen() {
		parts := []string{chatView()}
		if teamAbove {
			parts = append(parts, teamPanel)
		}
		if taskPanel != "" {
			parts = append(parts, taskPanel)
		}
		if extAbove != "" {
			parts = append(parts, extAbove)
		}
		if inlineUI {
			// extension menu (plan-mode) floats above chat like /commands;
			// cmd/@ popups stay hidden underneath until it closes.
			parts = append(parts, m.renderUIDialogPopup())
		} else {
			if m.cmdOpen {
				parts = append(parts, m.renderCmdPopup())
			}
			if m.atOpen {
				parts = append(parts, m.renderAtPopup())
			}
		}
		if m.inputOpen() {
			// free-text prompt floats above the input like /commands popups:
			// chat + sidebar stay visible behind it.
			parts = append(parts, m.renderInputBox())
		}
		parts = append(parts, m.renderInput())
		if extBelow != "" {
			parts = append(parts, extBelow)
		}
		if teamBelow {
			parts = append(parts, teamPanel)
		}
		body = lipgloss.JoinVertical(lipgloss.Left, parts...)
	}
	left := lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body)
	left = m.overlayToasts(left) // float above chat: never shifts the frame
	if m.showSide() {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", m.renderSidebar())
	}
	return left
}

// inputOpen reports a free-text extension prompt on top (floated above
// the input, not fullscreen like option dialogs).
func (m Model) inputOpen() bool {
	return len(m.Dialogs) > 0 && !m.isInlineUI() && m.Dialogs[0].Kind == "input"
}

// renderInputBox draws the free-text dialog as a bare compact box for the
// floating path in View (renderDialog keeps its own copy for direct use).
func (m Model) renderInputBox() string {
	d := m.Dialogs[0]
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString("\n")
	// pi's ui.input placeholder: a dim hint while the buffer is empty, so the
	// offered context is visible instead of a bare cursor.
	if d.Filter == "" && d.Placeholder != "" {
		b.WriteString(toolStyle.Render(d.Placeholder) + cmdHiStyle.Render("▌") + "\n")
	} else {
		b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
	}
	b.WriteString("\n" + toolStyle.Render("type · Enter save · Esc cancel"))
	boxW := 60
	if mw := m.mainW() - 4; mw < boxW && mw > 20 {
		boxW = mw
	}
	return dlgStyle.Width(boxW).Render(b.String())
}

// utils ------------------------------------------------------------------------

// padToHeight keeps a viewport's allocated rows visible even when its
// content is shorter (for example, the welcome screen in a new session).
// bubbles/viewport intentionally returns only the content rows in that
// case; without this padding, the editor and sidebar no longer share a
// bottom edge and the editor appears pushed up.
func padToHeight(s string, height int) string {
	if height <= 0 {
		return ""
	}
	rows := strings.Split(s, "\n")
	if len(rows) >= height {
		return s
	}
	return s + strings.Repeat("\n", height-len(rows))
}

// shortTree caps a multi-line block at n runes without touching newlines:
// Short would flatten the session-tree connectors into one ⏎ line.
func shortTree(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// renderSession styles the /session block like pi: bold section headers
// ("Session Info", "Messages", …), dim labels, plain values.
func renderSession(text string) string {
	valStyle := lipgloss.NewStyle().Foreground(cText)
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(cText)
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if j := strings.Index(ln, ":"); j >= 0 {
			lines[i] = statusBarStyle.Render(ln[:j+1]) + " " + valStyle.Render(strings.TrimSpace(ln[j+1:]))
		} else {
			lines[i] = headStyle.Render(ln)
		}
	}
	return strings.Join(lines, "\n")
}
