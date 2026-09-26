package app

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Semantic content of one chat block. Markdown is
// the raw source; the rest are derived views for the copy menu.
type BlockContent struct {
	Markdown   string
	CodeBlocks []string
	Tables     []string
	Plain      string
}

// collectBlockContent derives semantic content for a chat Block. Pitago
// stores the raw markdown on Block.Text (assistant) / ToolResult (tool), so
// reconstructMarkdown is one line here.
func collectBlockContent(bl Block) BlockContent {
	md := bl.Text
	if bl.Kind == "tool" && strings.TrimSpace(bl.ToolResult) != "" {
		md = bl.ToolResult
	}
	return BlockContent{
		Markdown:   md,
		CodeBlocks: extractCodeBlocks(md),
		Tables:     extractTables(md),
		Plain:      toPlainText(md),
	}
}

var (
	fenceOpenRe  = regexp.MustCompile("```[^\n`]*\n?")
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	tableSepRe   = regexp.MustCompile(`(?m)^\s*\|?[\s:|-]+\|?\s*$`)
	boldRe       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe     = regexp.MustCompile(`\*([^*]+)\*`)
	headingRe    = regexp.MustCompile(`(?m)^#{1,6}\s+`)
)

// extractCodeBlocks pulls every ``` fence (with language) from markdown,
// re-emitting the fence so the language tag survives copying.
func extractCodeBlocks(markdown string) []string {
	var blocks []string
	re := regexp.MustCompile("(?s)```([^\n`]*)\n(.*?)```")
	for _, m := range re.FindAllStringSubmatch(markdown, -1) {
		lang := strings.TrimSpace(m[1])
		body := strings.TrimRight(m[2], "\n")
		if lang != "" {
			blocks = append(blocks, "```"+lang+"\n"+body+"\n```")
		} else {
			blocks = append(blocks, "```\n"+body+"\n```")
		}
	}
	return blocks
}

// extractTables pulls every markdown table (consecutive | rows incl. a
// separator row), preserving the raw pipe syntax.
func extractTables(markdown string) []string {
	var tables []string
	lines := strings.Split(markdown, "\n")
	i := 0
	isSep := func(l string) bool {
		return tableSepRe.MatchString(l) && strings.Contains(l, "-")
	}
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "|") && i+1 < len(lines) && isSep(lines[i+1]) {
			rows := []string{line, lines[i+1]}
			i += 2
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				rows = append(rows, lines[i])
				i++
			}
			tables = append(tables, strings.Join(rows, "\n"))
			continue
		}
		i++
	}
	return tables
}

// toPlainText strips fences (keeping code content), inline code ticks,
// table pipes, emphasis and headings.
func toPlainText(markdown string) string {
	s := fenceOpenRe.ReplaceAllString(markdown, "")
	s = inlineCodeRe.ReplaceAllString(s, "$1")
	// Line-by-line table de-piping: avoids multiline anchor pitfalls.
	// Separator rows (|---|---|) carry no content and are dropped so plain
	// text never contains a stray " | " (we intentionally drop them for the
	// plain-copy spec).
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if tableSepRe.MatchString(trimmed) {
			lines[i] = ""
		} else if isTableLine(trimmed) {
			lines[i] = plainTableRow(trimmed)
		}
	}
	s = strings.Join(lines, "\n")
	s = boldRe.ReplaceAllString(s, "$1")
	s = italicRe.ReplaceAllString(s, "$1")
	s = headingRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// isTableLine reports whether a trimmed line is a markdown table row
// (leading + trailing pipe with cells, or a separator row).
func isTableLine(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return false
	}
	return strings.Contains(trimmed[1:len(trimmed)-1], "|") || tableSepRe.MatchString(trimmed)
}

// plainTableRow turns one | cell | row into "cell | cell".
func plainTableRow(trimmed string) string {
	row := strings.TrimPrefix(strings.TrimSpace(trimmed), "|")
	row = strings.TrimSuffix(row, "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return strings.Join(cells, " | ")
}

// buildBlockOptions lists only actions the block actually supports. Payload kinds run parallel.
func buildBlockOptions(c BlockContent) ([]string, []string) {
	var opts, payload []string
	if strings.TrimSpace(c.Markdown) != "" {
		opts = append(opts, "Copy markdown")
		payload = append(payload, "md")
	}
	if len(c.CodeBlocks) > 0 {
		opts = append(opts, fmt.Sprintf("Copy %d code block%s", len(c.CodeBlocks), plural(len(c.CodeBlocks))))
		payload = append(payload, "code")
	}
	if len(c.Tables) > 0 {
		opts = append(opts, fmt.Sprintf("Copy %d table%s", len(c.Tables), plural(len(c.Tables))))
		payload = append(payload, "tables")
	}
	if strings.TrimSpace(c.Plain) != "" {
		opts = append(opts, "Copy plain text")
		payload = append(payload, "plain")
	}
	return opts, payload
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// newBlockActionsDialog builds the semantic copy menu for a chat block
// (right-click entry). Reuses the modal Dialog machinery; Enter dispatches
// through the "blockactions" confirmer registered in src/builtin.
func newBlockActionsDialog(blocks []Block, idx int) *Dialog {
	if idx < 0 || idx >= len(blocks) {
		return nil
	}
	c := collectBlockContent(blocks[idx])
	opts, payload := buildBlockOptions(c)
	if len(opts) == 0 {
		return nil
	}
	d := &Dialog{
		Kind:     "blockactions",
		Title:    "Message actions",
		Options:  opts,
		Payload:  payload,
		BlockIdx: idx,
	}
	d.Reindex()
	return d
}

// RunBlockAction executes the picked action for a BlockActionsDialog
// (called from builtin confirmBlockActions on Enter).
func (m *Model) RunBlockAction(d *Dialog, ri int) tea.Cmd {
	if ri < 0 || ri >= len(d.Payload) {
		return nil
	}
	if d.BlockIdx < 0 || d.BlockIdx >= len(m.blocks) {
		m.AddBlock(Block{Kind: "notice", Text: "message no longer available", Err: true})
		m.Refresh()
		return nil
	}
	c := collectBlockContent(m.blocks[d.BlockIdx])
	kind := d.Payload[ri]
	switch kind {
	case "md":
		m.YankText(c.Markdown)
	case "tables":
		m.YankText(strings.Join(c.Tables, "\n\n"))
	case "code":
		m.YankText(strings.Join(c.CodeBlocks, "\n\n"))
	case "plain":
		m.YankText(c.Plain)
	}
	return nil
}

// CopyLastAs copies the last assistant message as a specific semantic kind
// (md | tables | code) — backing for the /copy-md|tables|code commands.
func (m *Model) CopyLastAs(kind string) tea.Cmd {
	idx := -1
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "assistant" {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no assistant message to copy yet"})
		m.Refresh()
		return nil
	}
	c := collectBlockContent(m.blocks[idx])
	var text string
	switch kind {
	case "md":
		text = c.Markdown
	case "tables":
		text = strings.Join(c.Tables, "\n\n")
	case "code":
		text = strings.Join(c.CodeBlocks, "\n\n")
	default:
		text = c.Markdown
	}
	if strings.TrimSpace(text) == "" {
		m.AddBlock(Block{Kind: "notice", Text: "nothing to copy in last message", Err: false})
		m.Refresh()
		return nil
	}
	m.YankText(text)
	return nil
}
