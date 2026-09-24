package app

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/format"
	"pitago/src/components/markdown"
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
	h.Write([]byte(bl.ToolCallID))
	h.Write([]byte{0})
	if bl.Err {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
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
		body = userStyle.Width(cw-2).Render(bl.Text) + "\n\n"
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
		switch bl.ToolStatus {
		case "done":
			icon = okStyle.Render("●")
		case "error":
			icon = errStyle.Render("×")
		default:
			icon = statusBarStyle.Render("○")
		}
		head := bl.ToolName
		switch strings.ToLower(bl.ToolName) {
		case "bash":
			head = "$"
			if bl.ToolArgs != "" {
				head += " " + bl.ToolArgs
			}
		case "powershell":
			head = "PS>"
			if bl.ToolArgs != "" {
				head += " " + bl.ToolArgs
			}
		default:
			if bl.ToolArgs != "" {
				head += " " + bl.ToolArgs
			}
		}
		// pi suffixes the write header with the added line count
		// ("write game.js +211").
		if strings.ToLower(bl.ToolName) == "write" {
			if content, ok := format.WriteContent(bl.ToolArgsRaw); ok {
				if n := countLines(content); n > 0 {
					head += fmt.Sprintf(" +%d", n)
				}
			}
		}
		if strings.TrimSpace(head) == "" {
			head = "tool"
		}
		body = lipgloss.NewStyle().Foreground(cText).Render(Short(head, 140)) + "\n"
		if r := m.renderToolBody(bl); r != "" {
			body += r
		}
		// pi wraps every tool execution in a status-colored Box
		// (pending → green → red). Width(cw) pads short lines so the
		// background spans the chat column full-bleed like pi.
		body = lipgloss.NewStyle().Background(toolBg(bl.ToolStatus)).Width(cw).
			Render(strings.TrimRight(body, "\n")) + "\n\n"
		boxed = true
	case "bash":
		icon = statusBarStyle.Render("●")
		body = markdown.Highlight("bash", Short(bl.Text, 400)) + "\n\n"
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

// toolBg is pi's tool Box background by execution status: pending while
// running, green on success, red on error.
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

// renderToolBody renders a tool's collapsible content like pi's TUI:
// write shows the file text from the call args (10 lines collapsed,
// ctrl+g expands), read shows the file content only when expanded (or on
// error), edit shows the diff, and bash/grep/ls show head/tail previews.
// Collapsed previews end with "... (N more lines[, T total], ctrl+g to
// expand)"; expanded blocks end with "(ctrl+g to collapse)" when lines
// were hidden.
func (m Model) renderToolBody(bl Block) string {
	expanded := m.expandTools
	switch strings.ToLower(bl.ToolName) {
	case "write":
		if content, ok := format.WriteContent(bl.ToolArgsRaw); ok && strings.TrimSpace(content) != "" {
			p := format.CallPreview(content, expanded)
			if p.Hidden || len(p.Lines) == 0 {
				return ""
			}
			return renderPreview(p, format.LangFromPath(toolPath(bl)), expanded)
		}
		return renderToolResultExpanded(bl.ToolName, bl.ToolStatus, bl.ToolResult, expanded)
	case "read":
		if bl.ToolStatus == "error" {
			return renderToolResultExpanded(bl.ToolName, bl.ToolStatus, bl.ToolResult, expanded)
		}
		if !expanded {
			if n := countLines(bl.ToolResult); n > 0 {
				return toolStyle.Render(fmt.Sprintf("  ... (%d lines, %s)", n, expandHint))
			}
			return ""
		}
		p := format.ToolResultPreviewExpanded(bl.ToolName, bl.ToolStatus, bl.ToolResult, true)
		if p.Hidden || len(p.Lines) == 0 {
			return ""
		}
		return renderPreview(p, format.LangFromPath(toolPath(bl)), true)
	case "edit":
		if bl.ToolStatus == "error" {
			return renderToolResultExpanded(bl.ToolName, bl.ToolStatus, bl.ToolResult, expanded)
		}
		text := strings.TrimSpace(bl.ToolResult)
		lang := ""
		if text == "" {
			// no details.diff reported: preview - old / + new from args
			text = format.EditDiffFallback(bl.ToolArgsRaw)
			lang = "diff"
		}
		if text == "" {
			return ""
		}
		p := format.CallPreview(text, expanded)
		if p.Hidden || len(p.Lines) == 0 {
			return ""
		}
		if lang == "" {
			lang = format.LangFromPath(toolPath(bl))
			if _, ok := codeLang(text); ok {
				lang = "diff"
			}
		}
		return renderPreview(p, lang, expanded)
	default:
		return renderToolResultExpanded(bl.ToolName, bl.ToolStatus, bl.ToolResult, expanded)
	}
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
// the dim └-tree style, and the matching pi-style hint closes the block.
func renderPreview(p format.ToolPreview, lang string, expanded bool) string {
	if p.Hidden || len(p.Lines) == 0 {
		return ""
	}
	if lang != "" && len(p.Lines) > 1 {
		if out := markdown.Highlight(lang, strings.Join(p.Lines, "\n")); out != strings.Join(p.Lines, "\n") {
			rows := strings.Split(out, "\n")
			for i := range rows {
				rows[i] = "  " + rows[i]
			}
			if hint := previewHint(p, expanded); hint != "" {
				rows = append(rows, toolStyle.Render(hint))
			}
			return strings.Join(rows, "\n")
		}
	}
	rows := make([]string, 0, len(p.Lines)+1)
	for i, ln := range p.Lines {
		pre := "    "
		if i == 0 {
			pre = "  └ "
		}
		rows = append(rows, toolStyle.Render(pre+ln))
	}
	if hint := previewHint(p, expanded); hint != "" {
		rows = append(rows, toolStyle.Render(hint))
	}
	return strings.Join(rows, "\n")
}

// previewHint is pi's trailing hint: collapsed shows what is hidden,
// expanded offers to collapse back (only when lines were hidden).
func previewHint(p format.ToolPreview, expanded bool) string {
	if p.Skipped > 0 && !expanded {
		hint := fmt.Sprintf("  ... (%d more lines", p.Skipped)
		if p.Total > 0 {
			hint += fmt.Sprintf(", %d total", p.Total)
		}
		return hint + ", " + expandHint + ")"
	}
	if expanded && p.Total > 0 {
		return "  (" + collapseHint + ")"
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
	if len(p.Lines) == 1 && p.Skipped == 0 {
		line := p.Lines[0]
		if lang, ok := codeLang(line); ok {
			if out := markdown.Highlight(lang, line); out != line {
				return "  └ " + out
			}
		}
		return toolStyle.Render("  └ " + line)
	}
	rows := make([]string, 0, len(p.Lines)+1)
	if p.Skipped > 0 && !expanded {
		hint := fmt.Sprintf("... (%d more lines)", p.Skipped)
		if p.Tail {
			hint = fmt.Sprintf("... (%d earlier lines)", p.Skipped)
		}
		rows = append(rows, toolStyle.Render("  "+hint+", "+expandHint+")"))
	}
	for i, ln := range p.Lines {
		pre := "    "
		if i == 0 {
			pre = "  └ "
		}
		rows = append(rows, toolStyle.Render(pre+ln))
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
// RECENT MODELS (clickable), COMMANDS, WORKSPACE, cwd. Content is built by
// buildSidebarContent and shown through sideVp, so a tall sidebar clips to
// the box and scrolls (wheel over it) instead of overflowing the layout.
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
	if m.Stats.ContextPct > 0 {
		return fmt.Sprintf("%s · %.0f%% · %s · $%.2f",
			Short(m.ModelLbl, 30), m.Stats.ContextPct, FmtNum(m.Stats.TokensTotal), m.Stats.Cost)
	}
	if m.Stats.Cost > 0 || m.Stats.TokensTotal > 0 {
		return fmt.Sprintf("%s · %s · $%.2f",
			Short(m.ModelLbl, 30), FmtNum(m.Stats.TokensTotal), m.Stats.Cost)
	}
	return Short(m.ModelLbl, 40)
}

func (m Model) renderHeader() string {
	left := Short(pirpc.Shorten(m.cwd), 48)
	if m.session != "" {
		left = Short(m.session+" · "+pirpc.Shorten(m.cwd), 48)
	}
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
	if m.thinking {
		if plan {
			border = cPlan
			title = "PLAN · " + spinFrame(m.pet.tick) + " " + m.inputStatus()
		} else {
			border = cGreen
			title = spinFrame(m.pet.tick) + " " + m.inputStatus()
		}
		if m.escArmed() {
			left = "press Esc again to cancel"
		} else {
			left = "↵ steer · Esc×2 cancel"
		}
	} else if plan {
		border = cPlan
		title = "PLAN"
	} else if len(m.Dialogs) > 0 && !m.isInlineUI() {
		border = cInputDim
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
	if d.Kind == "sessions" {
		return m.renderResumeDialog(d)
	}
	if d.Kind == "shortcuts" {
		return m.renderShortcutsDialog(d)
	}
	if d.Kind == shortcutKind {
		return m.renderShortcutDialog(d)
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	if isFilterKind(d.Kind) {
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
		b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
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
		for fi := start; fi < end; fi++ {
			ri := d.FIdx[fi]
			cursor := "  "
			style := statusBarStyle
			if fi == d.Cursor {
				cursor = "▸ "
				style = rowHiStyle
			}
			row := Short(d.Options[ri], 44)
			if desc := DescOf(d, ri); desc != "" {
				row += "  " + toolStyle.Render("— "+Short(desc, rowW-47))
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
	} else if isFilterKind(d.Kind) {
		foot = "type to filter · " + foot
	}
	if d.Kind == "sessions" {
		foot += " · Tab scope · Del delete"
	}
	if d.Kind == "settings" {
		foot = "↑↓ select · Enter change · Esc close"
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

func DescOf(d *Dialog, ri int) string {
	if ri < len(d.Descs) {
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

func (m Model) View() string {
	if !m.ready {
		return "starting…"
	}
	if len(m.Dialogs) > 0 && !m.isInlineUI() && m.Dialogs[0].Kind != "input" {
		return m.renderDialog()
	}
	inlineUI := m.isInlineUI()
	body := lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.renderInput())
	if m.cmdOpen || m.atOpen || inlineUI || m.inputOpen() {
		parts := []string{m.vp.View()}
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
	b.WriteString(cmdHiStyle.Render(d.Filter+"▌") + "\n")
	b.WriteString("\n" + toolStyle.Render("type · Enter save · Esc cancel"))
	boxW := 60
	if mw := m.mainW() - 4; mw < boxW && mw > 20 {
		boxW = mw
	}
	return dlgStyle.Width(boxW).Render(b.String())
}

// utils ------------------------------------------------------------------------

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
