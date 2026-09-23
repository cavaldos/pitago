// Package markdown renders chat output with pi's OWN renderer: assistant
// messages go through pi's Markdown (marked + highlight.js, dark theme),
// code through pi's highlightCode. No Glamour/Chroma — colors, fence
// borders and the no-auto-detect rule match pi exactly. Pure (no TUI state)
// except the shared node bridge; every entry point falls back to its input
// so rendering never fails (and stays hermetic without node/pi).
package markdown

import (
	"os"
	"regexp"
	"strings"

	"pitago/src/gomark"
	"pitago/src/pimark"
)

// ansiSeq matches one SGR escape; trailPad matches end-of-line padding:
// spaces/tabs padding pi adds to fill the wrap width.
var (
	ansiSeq  = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	trailPad = regexp.MustCompile(`(?:[ \t]|\x1b\[[0-9;]*[a-zA-Z])+$`)
)

// rtrimLine drops pi's wrap-width padding so mouse selection stays clean.
// Lines with nothing visible become "" (gutter keeps those empty).
func rtrimLine(ln string) string {
	if strings.TrimSpace(ansiSeq.ReplaceAllString(ln, "")) == "" {
		return ""
	}
	if !trailPad.MatchString(ln) {
		return ln
	}
	return trailPad.ReplaceAllString(ln, "\x1b[0m")
}

// rtrim applies rtrimLine per line.
func rtrim(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = rtrimLine(ln)
	}
	return strings.Join(lines, "\n")
}

// isMarkdown is a fast path so plain replies skip the bridge round-trip
// (and stream the same as before). Assistant text from the model is
// markdown, so this stays loose on purpose.
func isMarkdown(s string) bool {
	if strings.Contains(s, "```") {
		return true
	}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") ||
			strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") ||
			strings.HasPrefix(t, "> ") || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, "```") {
			return true
		}
	}
	return strings.ContainsAny(s, "`*_[") || strings.Contains(s, "**")
}

// Render turns markdown into ANSI for the chat column, wrapped to width.
// PITAGO_RENDER=go forces the Go renderer (trial). Default tries pi first
// then falls back to Go so tables still render without node/pi.
func Render(src string, width int) string {
	if strings.TrimSpace(src) == "" || !isMarkdown(src) {
		return src
	}
	if width < 20 {
		width = 80
	}
	if os.Getenv("PITAGO_RENDER") == "go" {
		return strings.Trim(gomark.Render(src, width), "\n")
	}
	out, err := pimark.Render(src, width, pimark.Assistant)
	if err != nil || strings.TrimSpace(out) == "" {
		return strings.Trim(gomark.Render(src, width), "\n")
	}
	return strings.Trim(rtrim(out), "\n")
}

// Highlight colors code with pi's highlightCode (highlight.js + pi theme).
// Unknown/empty lang falls back to pi's mdCodeBlock color (no auto-detect,
// unlike Chroma); any error falls back to code unchanged.
func Highlight(lang, code string) string {
	if code == "" {
		return code
	}
	out, err := pimark.Highlight(code, lang)
	if err != nil || out == "" {
		return code
	}
	return rtrim(out)
}
