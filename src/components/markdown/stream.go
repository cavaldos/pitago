package markdown

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"pitago/src/gomark"
)

// Streaming render.
//
// A reply that is still arriving changes on every frame, so RenderCwd's
// caches never hit and every frame re-renders the whole reply: ~3ms for a
// short one, ~35ms once it reaches a few KB. That alone caps a long answer
// well below 30fps.
//
// RenderCwdStreaming instead cuts the reply into settled top-level blocks
// and renders each one on its own. Only the last segment, the one still
// growing, misses the cache, so a frame costs one paragraph, not the whole
// reply. It only cuts where Markdown itself starts a fresh block that does
// not depend on what came before (see settledSegments), so the joined
// result reads the same as a whole-document render.

var (
	segMu    sync.Mutex
	segCache = map[string]string{}
)

// segCacheMax bounds the segment cache. Clearing it only costs a re-render.
const segCacheMax = 2048

// RenderCwdStreaming renders like RenderCwd, but in settled segments that
// are cached one by one. Use it for a reply that is still growing. Once the
// reply is final, RenderCwd gives the canonical render.
func RenderCwdStreaming(src string, width int, cwd string) string {
	if strings.TrimSpace(src) == "" || !isMarkdown(src) || os.Getenv("PITAGO_RENDER") == "pi" {
		return RenderCwd(src, width, cwd)
	}
	if width < 20 {
		width = 80
	}
	// Raw HTML gives blank lines its own meaning; render such a reply whole.
	if strings.Contains(src, "\n<") || strings.HasPrefix(src, "<") {
		return RenderCwd(src, width, cwd)
	}
	segs := settledSegments(src)
	if len(segs) == 1 {
		return RenderCwd(src, width, cwd)
	}
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if strings.TrimSpace(s) == "" {
			continue
		}
		out := renderSegment(s, width, cwd)
		// A fence that has only just opened renders to nothing; joining
		// it would leave a stray blank gap the whole render does not have.
		if strings.TrimSpace(ansiSeq.ReplaceAllString(out, "")) == "" {
			continue
		}
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n\n")
}

// renderSegment is RenderCwd's Glamour path for one segment, cached. It
// always goes through Glamour, even for a segment with no Markdown syntax,
// because inside a Markdown reply that segment is a styled paragraph.
func renderSegment(s string, width int, cwd string) string {
	key := strconv.Itoa(width) + "\x00" + cwd + "\x00" + s
	segMu.Lock()
	out, ok := segCache[key]
	segMu.Unlock()
	if ok {
		return out
	}
	out = strings.Trim(rtrim(frameTables(gomark.Render(LinkifyPaths(s, cwd), width), width)), "\n")
	segMu.Lock()
	if len(segCache) >= segCacheMax {
		segCache = map[string]string{}
	}
	segCache[key] = out
	segMu.Unlock()
	return out
}

// settledSegments splits src into top-level Markdown blocks. The last
// element is the tail (possibly still growing); the others are settled.
//
// A cut is made only at a blank line, outside a fenced code block, where
// the next line is complete (newline-terminated) and starts a block that
// stands alone: it begins in column 0 and is not a list item, a table row,
// a blockquote or raw HTML. Those are exactly the cases where a blank line
// does not end the construct (a loose list, an indented continuation) or
// where the following block could fold into the previous one. When in
// doubt the segment just stays longer, which costs speed, not correctness.
func settledSegments(src string) []string {
	var segs []string
	start := 0          // start of the current segment
	blankAt := -1       // offset of the first blank line in the current run
	fence := ""         // open fence marker ("```" / "~~~"), "" when outside
	fenceBlank := false // last line inside the open fence was blank
	hold := false       // the block just closed must not end a segment
	pos := 0
	for pos < len(src) {
		end := strings.IndexByte(src[pos:], '\n')
		if end < 0 {
			break // last line is still partial: never a cut point
		}
		line := src[pos : pos+end]
		next := pos + end + 1
		trimmed := strings.TrimLeft(line, " ")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.TrimSpace(strings.TrimLeft(trimmed, fence[:1])) == "" {
				fence = ""
				// Glamour keeps a code block's trailing blank row inside a
				// document but trims it at a document's end, so a segment
				// may not end on such a block.
				hold = fenceBlank
			}
			fenceBlank = strings.TrimSpace(line) == ""
			pos = next
			continue
		}
		if strings.TrimSpace(line) == "" {
			if blankAt < 0 {
				blankAt = pos
			}
			pos = next
			continue
		}
		if blankAt > start && !hold && standsAlone(line) {
			segs = append(segs, strings.TrimRight(src[start:blankAt], "\n"))
			start = pos
		}
		blankAt = -1
		hold = false
		if f := fenceOpen(trimmed); f != "" {
			fence = f
			fenceBlank = false
		}
		pos = next
	}
	return append(segs, src[start:])
}

// fenceOpen returns the fence marker a line opens, or "".
func fenceOpen(trimmed string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			// Use the full run so a ```` fence is only closed by ````.
			n := len(trimmed) - len(strings.TrimLeft(trimmed, f[:1]))
			return strings.Repeat(f[:1], n)
		}
	}
	return ""
}

// standsAlone reports whether line can start a segment of its own.
func standsAlone(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '\t', '|', '>', '<', '-', '*', '+':
		// Indented continuation, table, quote, HTML, bullet. A thematic
		// break (---, ***) is excluded too; it is rare and could be a
		// setext underline.
		return false
	}
	// Ordered list item: digits followed by '.' or ')'.
	i := 0
	for i < len(line) && i < 10 && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		return false
	}
	// A reference definition can be used by any block, before or after it.
	if line[0] == '[' && strings.Contains(line, "]:") {
		return false
	}
	return true
}
