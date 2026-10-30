// Package snippet backs /focus: open one file, pick a line range, and hand
// the agent only those lines (with a path:start-end reference) instead of
// letting it read the whole file. The idea comes from lgtm's "point at exact
// lines" references.
//
// Everything here is pure (no TUI state): the window lives in src/app.
package snippet

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps the file size /focus opens. A bigger file is almost always
// generated or vendored, and the viewer keeps every line in memory.
const MaxBytes = 2 << 20

// File is one loaded text file.
type File struct {
	Path  string   // as shown and sent: relative to cwd when inside it
	Abs   string   // absolute path on disk
	Lang  string   // fence / highlighter language ("" = plain)
	Lines []string // file lines, no trailing newline, tabs kept
}

// Range is a 1-based inclusive line range.
type Range struct{ Start, End int }

// Lines reports how many lines the range covers.
func (r Range) Lines() int { return r.End - r.Start + 1 }

// Norm builds a range from two 0-based line indexes in any order, clamped
// to a file of n lines.
func Norm(a, b, n int) Range {
	if a > b {
		a, b = b, a
	}
	if a < 0 {
		a = 0
	}
	if b >= n {
		b = n - 1
	}
	if b < a {
		b = a
	}
	return Range{Start: a + 1, End: b + 1}
}

// Resolve turns a user path (relative, absolute or ~/) into an absolute
// path and the display path to send (relative to cwd when it is inside).
func Resolve(cwd, p string) (abs, rel string) {
	p = strings.TrimSpace(strings.Trim(strings.TrimPrefix(strings.TrimSpace(p), "@"), "\""))
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home, err := os.UserHomeDir(); err == nil {
			abs = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	case filepath.IsAbs(p):
		abs = filepath.Clean(p)
	default:
		base := cwd
		if base == "" {
			base = "."
		}
		abs = filepath.Join(base, p)
	}
	if a, err := filepath.Abs(abs); err == nil {
		abs = a
	}
	rel = abs
	if cwd != "" {
		if c, err := filepath.Abs(cwd); err == nil {
			if r, err := filepath.Rel(c, abs); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				rel = r
			}
		}
	}
	return abs, filepath.ToSlash(rel)
}

// Load reads a text file for the viewer. Directories, binaries and files
// over MaxBytes are refused with a readable reason.
func Load(cwd, p string) (*File, error) {
	abs, rel := Resolve(cwd, p)
	if abs == "" {
		return nil, errors.New("no path")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", rel)
	}
	if st.Size() > MaxBytes {
		return nil, fmt.Errorf("%s is too large (%d KB, max %d KB)", rel, st.Size()>>10, MaxBytes>>10)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, fmt.Errorf("%s looks like a binary file", rel)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	return &File{Path: rel, Abs: abs, Lang: Lang(abs), Lines: strings.Split(text, "\n")}, nil
}

// langByExt maps extensions whose name is not already a Chroma lexer alias.
var langByExt = map[string]string{
	"mjs": "js", "cjs": "js", "mts": "ts", "cts": "ts",
	"yml": "yaml", "h": "c", "hpp": "cpp", "cc": "cpp", "hh": "cpp",
	"md": "markdown", "sh": "bash", "zsh": "bash", "rs": "rust",
	"kt": "kotlin", "ex": "elixir", "exs": "elixir", "tf": "hcl",
}

// Lang guesses the fence/highlighter language from the file name.
func Lang(p string) string {
	base := strings.ToLower(filepath.Base(p))
	switch base {
	case "makefile", "gnumakefile":
		return "make"
	case "dockerfile":
		return "docker"
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(base)), ".")
	if l, ok := langByExt[ext]; ok {
		return l
	}
	return ext
}

// Ref is the lgtm-style pointer: path:line or path:start-end.
func Ref(path string, r Range) string {
	if r.Start == r.End {
		return fmt.Sprintf("%s:%d", path, r.Start)
	}
	return fmt.Sprintf("%s:%d-%d", path, r.Start, r.End)
}

// Excerpt returns the selected lines verbatim (exact text, so the agent can
// use it as an edit anchor without re-reading the file).
func Excerpt(f *File, r Range) string {
	if f == nil || len(f.Lines) == 0 {
		return ""
	}
	r = Norm(r.Start-1, r.End-1, len(f.Lines))
	return strings.Join(f.Lines[r.Start-1:r.End], "\n")
}

// fence picks a backtick fence longer than any backtick run in the code,
// so a snippet that itself holds ``` cannot close the block early.
func fence(code string) string {
	longest, run := 0, 0
	for _, c := range code {
		if c == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// Block is the reference plus the fenced excerpt: what /focus inserts into
// the editor and what Prompt wraps.
func Block(f *File, r Range) string {
	code := Excerpt(f, r)
	fc := fence(code)
	return fmt.Sprintf("`%s`\n%s%s\n%s\n%s", Ref(f.Path, r), fc, f.Lang, code, fc)
}

// scopeNote tells the agent the excerpt is the whole context it needs, which
// is the point of /focus: no full-file read, edits stay inside the range.
const scopeNote = "Work from this excerpt only — do not read the whole file unless it is truly required. " +
	"If you change code, edit only these lines (the excerpt is the exact current text)."

// Prompt is the message sent to the agent for one ask about one range.
func Prompt(ask string, f *File, r Range) string {
	ask = strings.TrimSpace(ask)
	var b strings.Builder
	if ask != "" {
		b.WriteString(ask)
		b.WriteString("\n\n")
	}
	b.WriteString(Block(f, r))
	b.WriteString("\n\n")
	b.WriteString(scopeNote)
	return b.String()
}

// Presets are the quick asks Tab cycles through in an empty ask box.
var Presets = []string{
	"Explain what this code does.",
	"Refactor this for readability, keep behaviour the same.",
	"Find bugs or edge cases in this code and fix them.",
	"Add comments/docs to this code.",
	"Write tests covering this code.",
}

// ParseTarget splits a /focus argument "path", "path:47" or "path:47-52"
// into the path and an optional 1-based range (ok=false: no range given).
func ParseTarget(arg string) (path string, r Range, ok bool) {
	arg = strings.TrimSpace(arg)
	i := strings.LastIndex(arg, ":")
	if i <= 0 || i == len(arg)-1 {
		return arg, Range{}, false
	}
	spec := arg[i+1:]
	a, b, dash := strings.Cut(spec, "-")
	var s, e int
	if _, err := fmt.Sscanf(a, "%d", &s); err != nil || s <= 0 || fmt.Sprint(s) != a {
		return arg, Range{}, false
	}
	e = s
	if dash {
		if _, err := fmt.Sscanf(b, "%d", &e); err != nil || e <= 0 || fmt.Sprint(e) != b {
			return arg, Range{}, false
		}
	}
	if e < s {
		s, e = e, s
	}
	return arg[:i], Range{Start: s, End: e}, true
}
