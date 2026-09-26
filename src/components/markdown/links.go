package markdown

import (
	"path/filepath"
	"regexp"
	"strings"
)

// LinkifyPaths converts plain file paths in assistant markdown into
// markdown links ([display](file://abs)) so the glamour renderer emits
// OSC 8 hyperlinks for them (most markdown renderers do this natively).
//
// Rules:
//   - Skip paths inside backtick code spans.
//   - Skip http://, https:// and www. prefixes.
//   - Resolve relative paths against cwd; leave absolute paths as-is.
//   - Keep optional trailing :line[:col] in the visible label; the link
//     target is the plain absolute path (line fragments are not portable
//     across terminal openers, so v1 opens the file at the top).
func LinkifyPaths(md, cwd string) string {
	if cwd == "" {
		cwd = "."
	}
	// Exclude code spans and fenced blocks before replacing prose paths.
	excluded := codeSpanRe.FindAllStringIndex(md, -1)
	for _, r := range fencedRanges(md) {
		excluded = append(excluded, []int{r[0], r[1]})
	}
	insideExcluded := func(i int) bool {
		for _, r := range excluded {
			if i >= r[0] && i < r[1] {
				return true
			}
		}
		return false
	}

	var b strings.Builder
	last := 0
	for _, m := range filePathRe.FindAllStringSubmatchIndex(md, -1) {
		start, end := m[0], m[1]
		if start < last || insideExcluded(start) {
			continue
		}
		body := md[m[4]:m[5]] // capture 2: the path itself (no prefix)
		label := body
		base := stripLineCol(body)
		if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") ||
			strings.HasPrefix(base, "www.") {
			continue
		}
		abs := base
		if !filepath.IsAbs(base) {
			if a, err := filepath.Abs(filepath.Join(cwd, base)); err == nil {
				abs = a
			} else {
				abs = base
			}
		} else {
			abs = filepath.Clean(base)
		}
		b.WriteString(md[last:start])
		b.WriteString("[" + label + "](file://" + abs + ")")
		last = end
	}
	b.WriteString(md[last:])
	return b.String()
}

var (
	codeSpanRe = regexp.MustCompile("`[^`]*`")
	// (^|\s) prefix + (./ ../ / path start) + (non-space, non-paren chars)
	// + optional :line[:col]. Capture: 1=prefix, 2=path, 3=line, 4=col.
	filePathRe = regexp.MustCompile(`(^|\s)((?:\.{1,2}/|/)[^\s)]+)(?::(\d+)(?::(\d+))?)?`)
	lineColRe  = regexp.MustCompile(`:(\d+)(?::(\d+))?$`)
)

// fencedRanges returns byte ranges for fenced blocks. An opening fence with
// no matching close extends through EOF, matching Markdown's unclosed-fence
// behavior.
func fencedRanges(md string) [][2]int {
	var out [][2]int
	start := -1
	delim := ""
	pos := 0
	for _, line := range strings.SplitAfter(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if start < 0 {
			if strings.HasPrefix(trimmed, "```") {
				start, delim = pos, "```"
			} else if strings.HasPrefix(trimmed, "~~~") {
				start, delim = pos, "~~~"
			}
		} else if trimmed == delim {
			out = append(out, [2]int{start, pos + len(line)})
			start, delim = -1, ""
		}
		pos += len(line)
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(md)})
	}
	return out
}

// stripLineCol removes a trailing :line[:col] suffix from a matched path so
// the OS can open the real file.
func stripLineCol(p string) string {
	return lineColRe.ReplaceAllString(p, "")
}
