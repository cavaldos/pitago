package sessiontree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"pitago/src/pirpc"
)

// Horizontal viewport constants (tree-selector.js top of file).
const (
	treeGutterWidth              = 2
	minVisibleAnchorContentWidth = 4
	maxVisibleAnchorContentWidth = 20
	minAnchorContextWidth        = 2
	maxAnchorContextWidth        = 12
)

type renderedRow struct {
	gutter    Line
	body      Line
	anchorCol int
	bodyWidth int
	selected  bool
}

// Render draws the visible window plus the "(i/n)" status line, each line
// clipped to width. It is pi's TreeList.render.
func (l *List) Render(width int) []Line {
	if len(l.filtered) == 0 {
		return []Line{
			Truncate(Line{{Text: "  No entries found", Style: StyleMuted}}, width),
			Truncate(Line{{Text: "  (0/0)" + l.StatusLabels(), Style: StyleMuted}}, width),
		}
	}
	start, end := l.VisibleRange()
	var rows []renderedRow
	for i := start; i < end; i++ {
		rows = append(rows, l.renderRow(l.filtered[i], i == l.selected))
	}
	lines := horizontalViewport(rows, width)
	status := fmt.Sprintf("  (%d/%d)%s", l.selected+1, len(l.filtered), l.StatusLabels())
	return append(lines, Truncate(Line{{Text: status, Style: StyleMuted}}, width))
}

func (l *List) renderRow(fn *flatNode, selected bool) renderedRow {
	e := fn.node.Entry
	cursor := Span{Text: "  "}
	if selected {
		cursor = Span{Text: "› ", Style: StyleAccent}
	}
	displayIndent := fn.indent
	if l.multipleRoots {
		displayIndent = max(0, fn.indent-1)
	}
	connector := fn.showConnector && !fn.isVirtualRootChild
	connectorPos := -1
	if connector {
		connectorPos = displayIndent - 1
	}
	folded := l.folded[e.ID]
	var prefix strings.Builder
	for i := 0; i < displayIndent*3; i++ {
		level, pos := i/3, i%3
		g, hasGutter := findGutter(fn.gutters, level)
		switch {
		case hasGutter:
			if pos == 0 && g.show {
				prefix.WriteString("│")
			} else {
				prefix.WriteString(" ")
			}
		case connector && level == connectorPos:
			switch pos {
			case 0:
				if fn.isLast {
					prefix.WriteString("└")
				} else {
					prefix.WriteString("├")
				}
			case 1:
				switch {
				case folded:
					prefix.WriteString("⊞")
				case l.isFoldable(e.ID):
					prefix.WriteString("⊟")
				default:
					prefix.WriteString("─")
				}
			default:
				prefix.WriteString(" ")
			}
		default:
			prefix.WriteString(" ")
		}
	}
	prefixPart := Line{{Text: prefix.String(), Style: StyleDim}}
	// Roots have no connector to carry the fold mark, so it goes in front.
	if folded && !connector {
		prefixPart = append(prefixPart, Span{Text: "⊞ ", Style: StyleAccent})
	}
	if l.activePath[e.ID] {
		prefixPart = append(prefixPart, Span{Text: "• ", Style: StyleAccent})
	}
	anchor := Width(prefixPart)
	body := append(Line(nil), prefixPart...)
	if lb := fn.node.Label; lb != "" {
		body = append(body, Span{Text: "[" + lb + "] ", Style: StyleWarning})
		if l.showLabelTimes && fn.node.LabelTimestamp != "" {
			body = append(body, Span{Text: l.formatLabelTimestamp(fn.node.LabelTimestamp) + " ", Style: StyleMuted})
		}
	}
	body = append(body, l.displayText(fn.node, selected)...)
	gut := Line{cursor}
	if selected {
		gut = markSelected(gut)
		body = markSelected(body)
	}
	return renderedRow{gutter: gut, body: body, anchorCol: anchor, bodyWidth: Width(body), selected: selected}
}

func findGutter(gs []gutter, level int) (gutter, bool) {
	for _, g := range gs {
		if g.position == level {
			return g, true
		}
	}
	return gutter{}, false
}

func markSelected(ln Line) Line {
	out := make(Line, len(ln))
	for i, s := range ln {
		s.Selected = true
		out[i] = s
	}
	return out
}

// horizontalViewport is pi's renderHorizontalViewport: the gutter stays put
// and the bodies shift left only when the selected row's text would start
// too far right to read.
func horizontalViewport(rows []renderedRow, width int) []Line {
	viewport := max(0, width-treeGutterWidth)
	maxBody := 0
	for _, r := range rows {
		maxBody = max(maxBody, r.bodyWidth)
	}
	maxScroll := max(0, maxBody-viewport)
	scroll := 0
	for _, r := range rows {
		if !r.selected || maxScroll == 0 {
			continue
		}
		minVisible := min(maxVisibleAnchorContentWidth, max(minVisibleAnchorContentWidth, viewport/3))
		if r.anchorCol > viewport-minVisible {
			context := min(maxAnchorContextWidth, max(minAnchorContextWidth, viewport/4))
			scroll = min(maxScroll, r.anchorCol-context)
		}
		break
	}
	out := make([]Line, 0, len(rows))
	for _, r := range rows {
		ln := append(Line(nil), r.gutter...)
		if scroll > 0 {
			ln = append(ln, SliceColumns(r.body, scroll, viewport)...)
		} else {
			ln = append(ln, r.body...)
		}
		out = append(out, Truncate(ln, width))
	}
	return out
}

// --- width helpers -----------------------------------------------------------

// Width is the display width of a line.
func Width(ln Line) int {
	w := 0
	for _, s := range ln {
		w += runewidth.StringWidth(s.Text)
	}
	return w
}

// Truncate clips a line to width cells, with no ellipsis (pi's
// truncateToWidth(line, width, "")).
func Truncate(ln Line, width int) Line {
	return SliceColumns(ln, 0, width)
}

// SliceColumns keeps the cells in [start, start+length). A wide rune that
// would straddle either edge is dropped (pi's strict sliceByColumn).
func SliceColumns(ln Line, start, length int) Line {
	if length <= 0 {
		return nil
	}
	end := start + length
	col := 0
	var out Line
	for _, s := range ln {
		var b strings.Builder
		for _, r := range s.Text {
			w := runewidth.RuneWidth(r)
			if col >= start && col+w <= end {
				b.WriteRune(r)
			}
			col += w
		}
		if b.Len() > 0 {
			s.Text = b.String()
			out = append(out, s)
		}
		if col >= end {
			break
		}
	}
	return out
}

// --- entry text ---------------------------------------------------------------

func normalize(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\t", " ").Replace(s))
}

// fullContent is pi's extractFullContent: a plain string, or the text
// blocks of a content array concatenated.
func fullContent(raw json.RawMessage) string { return pirpc.TextOf(raw) }

// content is pi's extractContent: the first 200 characters.
func content(raw json.RawMessage) string { return firstRunes(fullContent(raw), 200) }

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func hasTextContent(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	for _, b := range pirpc.BlocksOf(raw) {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// displayText is pi's getEntryDisplayText. The whole text is bold on the
// selected row.
func (l *List) displayText(n *Node, selected bool) Line {
	e := n.Entry
	var out Line
	add := func(text, style string) { out = append(out, Span{Text: text, Style: style}) }
	switch e.Type {
	case "message":
		msg := e.Message
		switch msg.Role {
		case "user":
			add("user: ", StyleAccent)
			add(normalize(content(msg.Content)), StyleText)
		case "assistant":
			add("assistant: ", StyleSuccess)
			if t := normalize(content(msg.Content)); t != "" {
				add(t, StyleText)
			} else if msg.StopReason == "aborted" {
				add("(aborted)", StyleMuted)
			} else if msg.ErrorMessage != "" {
				add(firstRunes(normalize(msg.ErrorMessage), 80), StyleError)
			} else {
				add("(no content)", StyleMuted)
			}
		case "toolResult":
			if tc, ok := l.toolCalls[msg.ToolCallID]; ok && msg.ToolCallID != "" {
				add(l.formatToolCall(tc.name, tc.args), StyleMuted)
			} else {
				name := msg.ToolName
				if name == "" {
					name = "tool"
				}
				add("["+name+"]", StyleMuted)
			}
		case "bashExecution":
			add("[bash]: "+normalize(msg.Command), StyleDim)
		default:
			add("["+msg.Role+"]", StyleDim)
		}
	case "custom_message":
		add("["+e.CustomType+"]: ", StyleCustomLabel)
		add(normalize(fullContent(e.Content)), StyleText)
	case "compaction":
		tokens := int(math.Round(float64(e.TokensBefore) / 1000))
		add(fmt.Sprintf("[compaction: %dk tokens]", tokens), StyleBorderAccent)
	case "branch_summary":
		add("[branch summary]: ", StyleWarning)
		add(normalize(e.Summary), StyleText)
	case "model_change":
		add("[model: "+e.ModelID+"]", StyleDim)
	case "thinking_level_change":
		add("[thinking: "+e.ThinkingLevel+"]", StyleDim)
	case "custom":
		add("[custom: "+e.CustomType+"]", StyleDim)
	case "context_edit":
		kind := "replace"
		if bytes.Equal(bytes.TrimSpace(e.Replacement), []byte("null")) {
			kind = "omit"
		}
		add("[context "+kind+": "+e.TargetID+"]", StyleDim)
	case "label":
		lb := e.Label
		if lb == "" {
			lb = "(cleared)"
		}
		add("[label: "+lb+"]", StyleDim)
	case "session_info":
		add("[title: ", StyleDim)
		if e.Name != "" {
			add(e.Name, StyleDim)
		} else {
			out = append(out, Span{Text: "empty", Style: StyleDim, Italic: true})
		}
		add("]", StyleDim)
	}
	if selected {
		for i := range out {
			out[i].Bold = true
		}
	}
	return out
}

// searchableText is pi's getSearchableText: what the search tokens match.
func (l *List) searchableText(n *Node) string {
	e := n.Entry
	var parts []string
	if n.Label != "" {
		parts = append(parts, n.Label)
	}
	switch e.Type {
	case "message":
		parts = append(parts, e.Message.Role)
		if c := content(e.Message.Content); len(e.Message.Content) > 0 && c != "" {
			parts = append(parts, c)
		}
		if e.Message.Role == "bashExecution" && e.Message.Command != "" {
			parts = append(parts, e.Message.Command)
		}
	case "custom_message":
		parts = append(parts, e.CustomType, content(e.Content))
	case "compaction":
		parts = append(parts, "compaction")
	case "branch_summary":
		parts = append(parts, "branch summary", e.Summary)
	case "session_info":
		parts = append(parts, "title")
		if e.Name != "" {
			parts = append(parts, e.Name)
		}
	case "model_change":
		parts = append(parts, "model", e.ModelID)
	case "thinking_level_change":
		parts = append(parts, "thinking", e.ThinkingLevel)
	case "custom":
		parts = append(parts, "custom", e.CustomType)
	case "context_edit":
		kind := "replace"
		if bytes.Equal(bytes.TrimSpace(e.Replacement), []byte("null")) {
			kind = "omit"
		}
		parts = append(parts, "context edit", kind, e.TargetID)
	case "label":
		parts = append(parts, "label", e.Label)
	}
	return strings.Join(parts, " ")
}

// CopyText is pi's getEntryCopyText: the entry's full text, or "" when it
// has none worth copying.
func CopyText(n *Node) string {
	if n == nil {
		return ""
	}
	e := n.Entry
	text := ""
	switch e.Type {
	case "message":
		if e.Message.Role == "bashExecution" {
			text = e.Message.Command
		} else {
			text = fullContent(e.Message.Content)
			if text == "" && e.Message.Role == "assistant" {
				text = e.Message.ErrorMessage
			}
		}
	case "custom_message":
		text = fullContent(e.Content)
	case "compaction", "branch_summary":
		text = e.Summary
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// EditorText is what pi puts back in the editor after navigating to an
// entry: a user or custom message's text (navigateTree's editorText).
func EditorText(n *Node) string {
	if n == nil {
		return ""
	}
	switch {
	case n.Entry.Type == "message" && n.Entry.Message.Role == "user":
		return pirpc.TextOf(n.Entry.Message.Content)
	case n.Entry.Type == "custom_message":
		return pirpc.TextOf(n.Entry.Content)
	}
	return ""
}

func (l *List) formatLabelTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	t = t.Local()
	now := l.Now().Local()
	hm := fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	if t.Year() == now.Year() && t.Month() == now.Month() && t.Day() == now.Day() {
		return hm
	}
	if t.Year() == now.Year() {
		return fmt.Sprintf("%d/%d %s", int(t.Month()), t.Day(), hm)
	}
	return fmt.Sprintf("%02d/%d/%d %s", t.Year()%100, int(t.Month()), t.Day(), hm)
}

// --- tool calls ------------------------------------------------------------------

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

func (l *List) shortenPath(p string) string {
	if l.Home != "" && strings.HasPrefix(p, l.Home) {
		return "~" + p[len(l.Home):]
	}
	return p
}

// argString is JS String(args[key] || ...) for the keys tried in order.
func argString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := args[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				return x
			}
		case bool:
			if x {
				return "true"
			}
		case float64:
			if x != 0 {
				return strconv.FormatFloat(x, 'f', -1, 64)
			}
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	}
	return ""
}

// lineNumber is pi's parseLineNumber: a finite number or numeric string.
func lineNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		if strings.TrimSpace(x) == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// readLineRange is pi's formatReadLineRange.
func readLineRange(args map[string]any) string {
	off, hasOff := args["offset"]
	lim, hasLim := args["limit"]
	offNull := !hasOff || off == nil
	limNull := !hasLim || lim == nil
	if offNull && limNull {
		return ""
	}
	start := 1.0
	if !offNull {
		s, ok := lineNumber(off)
		if !ok {
			return ""
		}
		start = s
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	if limNull {
		return ":" + num(start)
	}
	count, ok := lineNumber(lim)
	if !ok {
		return ""
	}
	end := start + count - 1
	if end == 0 {
		return ":" + num(start)
	}
	return ":" + num(start) + "-" + num(end)
}

// formatToolCall is pi's tree label for a tool result's call.
func (l *List) formatToolCall(name string, raw []byte) string {
	args := map[string]any{}
	_ = json.Unmarshal(raw, &args)
	switch name {
	case "read":
		return "[read: " + l.shortenPath(argString(args, "path", "file_path")) + readLineRange(args) + "]"
	case "write":
		return "[write: " + l.shortenPath(argString(args, "path", "file_path")) + "]"
	case "edit":
		return "[edit: " + l.shortenPath(argString(args, "path", "file_path")) + "]"
	case "bash":
		raw := argString(args, "command")
		cmd := firstRunes(strings.TrimSpace(strings.NewReplacer("\n", " ", "\t", " ").Replace(raw)), 50)
		more := ""
		if len([]rune(raw)) > 50 {
			more = "..."
		}
		return "[bash: " + cmd + more + "]"
	case "grep":
		path := argString(args, "path")
		if path == "" {
			path = "."
		}
		return "[grep: /" + argString(args, "pattern") + "/ in " + l.shortenPath(path) + "]"
	case "find":
		path := argString(args, "path")
		if path == "" {
			path = "."
		}
		return "[find: " + argString(args, "pattern") + " in " + l.shortenPath(path) + "]"
	case "ls":
		path := argString(args, "path")
		if path == "" {
			path = "."
		}
		return "[ls: " + l.shortenPath(path) + "]"
	}
	js := compactJSON(raw)
	more := ""
	if len([]rune(js)) > 40 {
		more = "..."
	}
	return "[" + name + ": " + firstRunes(js, 40) + more + "]"
}

// compactJSON is JSON.stringify of the parsed args: compact, key order kept.
func compactJSON(raw []byte) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "{}"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}
