// Package image extracts image paths and loads them as pi RPC vision
// attachments (pi parity with the CLI `@image.png` path).
//
// Two entry styles: @refs (`@shot.png`, handled at send) and bare dropped
// paths (Finder/terminal drops land as escaped absolute paths — those are
// collected into the input tray as [Image N] chips so the long path never
// clutters the prompt).
//
// Pi's TUI only sends raw "@path" text (the model reads files via tools),
// but pi's RPC protocol accepts images: [{type:"image", data:<base64>,
// mimeType}] on prompt/steer/follow_up. pitago uses that channel so a
// png/jpg/gif/webp is seen directly (vision) instead of costing an extra
// read-tool roundtrip. Non-image @refs are left untouched — the model
// still reads those with its tools, exactly like before.
//
// Pure stdlib, no TUI state: tray/send wiring lives in src/app.
package image

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxCount caps attachments per message (RPC JSONL has no hard limit,
	// but every image rides every subsequent provider request).
	MaxCount = 5
	// MaxBytes caps one file (base64 inflates ~33%; pi resizes server-side
	// only for tool results, not user images, so stay conservative).
	MaxBytes = 8 << 20
)

// Attach is one loaded vision attachment.
type Attach struct {
	Name string // display path as typed (for the 📷 echo line)
	Data string // base64 payload
	Mime string // image/png | image/jpeg | image/gif | image/webp
}

// IsImageName reports whether name looks like a directly-sendable image.
// bmp is excluded: pi converts it via wasm, we can't — it falls back to
// the read-tool path with a notice.
func IsImageName(name string) bool {
	ext := strings.ToLower(filepath.Ext(strings.Trim(name, `"`)))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Refs scans text for @ file refs: @"quoted path" or @bare (until
// whitespace or a closing delimiter). Email-like a@b is skipped, mirroring
// components/mention.Token. Deduped, order preserved, @ stripped.
func Refs(text string) []string {
	var out []string
	seen := map[string]bool{}
	r := []rune(text)
	i := 0
	for i < len(r) {
		if r[i] != '@' || (i > 0 && !isDelim(r[i-1])) {
			i++
			continue
		}
		j := i + 1
		var raw string
		if j < len(r) && r[j] == '"' {
			k := j + 1
			for k < len(r) && r[k] != '"' {
				k++
			}
			if k >= len(r) { // unterminated quote — let the @ popup own it
				i++
				continue
			}
			raw = string(r[j+1 : k])
			i = k + 1
		} else {
			k := j
			for k < len(r) && !isEnd(r[k]) {
				k++
			}
			if k == j {
				i++
				continue
			}
			raw = strings.TrimRight(string(r[j:k]), ":,;!?")
			i = k
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out
}

func isDelim(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '"', '\'', '=', '(', '[', '{':
		return true
	}
	return false
}

func isEnd(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '"', '\'', ',', ';', '(', ')', '[', ']', '{', '}', '<', '>':
		return true
	}
	return false
}

// MimeOf sniffs magic bytes (port of pi's detectSupportedImageMimeType,
// minus bmp/apng which need conversion we don't have).
func MimeOf(b []byte) string {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		if len(b) > 3 && b[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4e && b[3] == 0x47 &&
		b[4] == 0x0d && b[5] == 0x0a && b[6] == 0x1a && b[7] == 0x0a {
		return "image/png" // ponytail: apng passes as png, provider rejects if animated
	}
	if len(b) >= 3 && b[0] == 'G' && b[1] == 'I' && b[2] == 'F' {
		return "image/gif"
	}
	if len(b) >= 12 && b[0] == 'R' && b[1] == 'I' && b[2] == 'F' && b[3] == 'F' &&
		b[8] == 'W' && b[9] == 'E' && b[10] == 'B' && b[11] == 'P' {
		return "image/webp"
	}
	return ""
}

// Resolve expands ~ and joins cwd, like the @ lookup does.
func Resolve(cwd, ref string) string {
	if strings.HasPrefix(ref, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ref[2:])
		}
	}
	if filepath.IsAbs(ref) {
		return ref
	}
	if cwd == "" {
		cwd = "."
	}
	return filepath.Join(cwd, ref)
}

// Unescape undoes shell-escaped drops (Finder/terminal paste
// "Copied\ Screenshots/x.png" back into "Copied Screenshots/x.png").
func Unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] == '\\' && i+1 < len(r) {
			i++
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

// Dequote strips one pair of surrounding quotes (drop paths with spaces
// sometimes arrive as '"/a/b c.png"').
func Dequote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// ExistsImage reports whether ref names an existing image file under cwd.
func ExistsImage(cwd, ref string) bool {
	if !IsImageName(ref) {
		return false
	}
	st, err := os.Stat(Resolve(cwd, ref))
	return err == nil && !st.IsDir()
}

// ScanPaths finds bare (no @) image file paths in text: drops and pastes
// arrive as escaped absolute paths, not @refs. Only tokens that resolve
// to a real file are returned (prose like "see a.png" never matches),
// deduped in order. @-mentions are skipped — Refs owns those.
func ScanPaths(text, cwd string) []string {
	var out []string
	seen := map[string]bool{}
	for _, sp := range lex(text) {
		cand := Unescape(Dequote(sp.raw))
		if ExistsImage(cwd, cand) && !seen[cand] {
			seen[cand] = true
			out = append(out, cand)
		}
	}
	return out
}

// StripRefs cuts the original tokens of refs out of text (span-based, so
// escaped/quoted forms are removed exactly as typed).
func StripRefs(text string, refs []string) string {
	set := make(map[string]bool, len(refs))
	for _, r := range refs {
		set[r] = true
	}
	r := []rune(text)
	var b strings.Builder
	prev := 0
	for _, sp := range lex(text) {
		if set[Unescape(Dequote(sp.raw))] {
			b.WriteString(string(r[prev:sp.start]))
			prev = sp.end
		}
	}
	b.WriteString(string(r[prev:]))
	return b.String()
}

// span is one lexical token with its rune offsets in the source.
type span struct {
	start, end int
	raw        string
}

// lex splits text into tokens: quoted spans stay whole, backslash-escaped
// chars belong to their token, @refs are skipped wholesale.
func lex(text string) []span {
	var out []span
	r := []rune(text)
	i := 0
	for i < len(r) {
		if r[i] == '@' {
			i = skipRef(r, i)
			continue
		}
		if isSpace(r[i]) {
			i++
			continue
		}
		if r[i] == '"' || r[i] == '\'' {
			q := r[i]
			k := i + 1
			for k < len(r) && r[k] != q {
				k++
			}
			if k < len(r) {
				out = append(out, span{i, k + 1, string(r[i : k+1])})
				i = k + 1
				continue
			}
			i++
			continue
		}
		k := i
		for k < len(r) && !isSpace(r[k]) && r[k] != '"' && r[k] != '\'' {
			if r[k] == '\\' && k+1 < len(r) {
				k += 2 // escaped char belongs to the token
				continue
			}
			k++
		}
		tok := strings.TrimRight(string(r[i:k]), ":,;!?()[]{}<>")
		if tok != "" {
			out = append(out, span{i, i + len([]rune(tok)), tok})
		}
		i = k
	}
	return out
}

func isSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// skipRef consumes one @ref starting at the @ (quoted or bare).
func skipRef(r []rune, i int) int {
	j := i + 1
	if j < len(r) && r[j] == '"' {
		k := j + 1
		for k < len(r) && r[k] != '"' {
			k++
		}
		if k < len(r) {
			return k + 1
		}
		return len(r)
	}
	for j < len(r) && !isSpace(r[j]) && r[j] != '"' && r[j] != '\'' {
		j++
	}
	return j
}

// Loader turns image refs into attachments for ONE outgoing message.
//
// It owns the dedupe set and the per-message budget, so a caller can feed
// refs from several sources (tray chips, then the @refs sitting in the
// text) and still get a single deterministic order, one dedupe and one
// MaxCount cap across all of them. Order is exactly the call order —
// the model sees images in the order we hand them to pi.
type Loader struct {
	cwd  string
	seen map[string]bool // resolved path -> already offered
	n    int             // attachments handed out so far
}

// NewLoader returns a Loader resolving refs under cwd.
func NewLoader(cwd string) *Loader {
	return &Loader{cwd: cwd, seen: map[string]bool{}}
}

// Load loads refs in order and returns the attachments, one notice per ref
// that could not be loaded (missing/oversize/unsupported/unreadable), and
// the refs skipped because the cap was already reached.
//
// Two refs naming the same file (`@shot.png` plus a dropped chip of the
// same picture, or `shot.png` and `./shot.png`) load once: the second is
// dropped silently, because a duplicate payload buys the model nothing.
// Cap skips are reported separately from failures: a caller whose cap is
// soft (an @ref that stays in the text) words them differently than one
// whose refs were consumed.
func (l *Loader) Load(refs []string) (atts []Attach, notes []string, overflow []string) {
	for _, ref := range refs {
		abs := Resolve(l.cwd, ref)
		if l.seen[abs] {
			continue
		}
		l.seen[abs] = true
		if l.n >= MaxCount {
			overflow = append(overflow, ref)
			continue
		}
		a, note := l.loadOne(ref, abs)
		if note != "" {
			notes = append(notes, note)
			continue
		}
		atts = append(atts, a)
		l.n++
	}
	return atts, notes, overflow
}

// loadOne reads one file; the second result is "" on success.
func (l *Loader) loadOne(ref, abs string) (Attach, string) {
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		return Attach{}, "image not found: " + ref
	}
	if st.Size() == 0 || st.Size() > MaxBytes {
		return Attach{}, "image skipped (empty/too large, max 8MB): " + ref
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return Attach{}, "cannot read image " + ref + ": " + err.Error()
	}
	mime := MimeOf(b)
	if mime == "" {
		return Attach{}, ref + ": unsupported image data (need png/jpg/gif/webp)"
	}
	return Attach{Name: ref, Data: base64.StdEncoding.EncodeToString(b), Mime: mime}, ""
}

// ImageRefs lists the @refs in text that name images, in text order.
func ImageRefs(text string) []string {
	var refs []string
	for _, ref := range Refs(text) {
		if IsImageName(ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// Extract loads the @-mentioned images of text through l (see Loader),
// in the order they appear in the text. Missing/oversize/unsupported refs
// only produce notices, never an error: the "@path" text stays in the
// message so the model still reads it with its tools.
func Extract(l *Loader, text string) ([]Attach, []string, []string) {
	return l.Load(ImageRefs(text))
}
