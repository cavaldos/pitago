// Package gomark renders markdown/code in-process with Glamour v2 +
// Chroma (dark style). No node/pi needed. Default renderer; pi bridge
// remains only as opt-in via PITAGO_RENDER=pi.
package gomark

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	glamour "charm.land/glamour/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	mu        sync.Mutex
	renderers = map[int]*glamour.TermRenderer{}
	cache     = map[string]string{}
)

func renderer(width int) (*glamour.TermRenderer, error) {
	mu.Lock()
	defer mu.Unlock()
	if r, ok := renderers[width]; ok {
		return r, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	if len(renderers) > 8 {
		renderers = map[int]*glamour.TermRenderer{}
	}
	renderers[width] = r
	return r, nil
}

// Render converts markdown to ANSI, wrapped to width. Plain text or any
// error returns src unchanged so callers keep their old render.
func Render(src string, width int) string {
	if strings.TrimSpace(src) == "" {
		return src
	}
	if width < 20 {
		width = 80
	}
	key := fmt.Sprint(width) + "\x00" + src
	mu.Lock()
	if s, ok := cache[key]; ok {
		mu.Unlock()
		return s
	}
	mu.Unlock()

	r, err := renderer(width)
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil || strings.TrimSpace(out) == "" {
		return src
	}
	out = strings.Trim(out, "\n")

	mu.Lock()
	if len(cache) > 512 {
		cache = map[string]string{}
	}
	cache[key] = out
	mu.Unlock()
	return out
}

// Highlight colors code with Chroma (dracula, terminal256). No auto-detect:
// unknown/empty lang returns code unchanged, like pi's rule.
func Highlight(code, lang string) string {
	if code == "" || strings.TrimSpace(lang) == "" {
		return code
	}
	l := lexers.Get(lang)
	if l == nil {
		return code
	}
	it, err := l.Tokenise(nil, code)
	if err != nil {
		return code
	}
	st := styles.Get("dracula")
	if st == nil {
		st = styles.Fallback
	}
	var buf bytes.Buffer
	if err := formatters.Get("terminal256").Format(&buf, st, it); err != nil {
		return code
	}
	if out := buf.String(); strings.TrimSpace(out) != "" {
		return strings.TrimRight(out, "\n")
	}
	return code
}
