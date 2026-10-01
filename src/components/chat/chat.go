// Package chat holds the chat-column data model: Block is one rendered
// unit (user/assistant/thinking/tool/bash/notice). The TUI state machine
// in src/app builds and renders these; pure derivations (yank entries,
// last-answer lookup) live in components/yank.
package chat

import (
	"crypto/sha256"
	"encoding/binary"
)

// Image is an inline image carried by a chat block. Digest is computed once
// so render-cache keys never hash the base64 payload on every frame.
//
// The digest is SHA-256 truncated to 64 bits, not FNV: it keys the upload,
// geometry and transcode caches, and the image bytes come off the wire. A
// non-cryptographic hash lets a crafted image collide with another and
// render the wrong pixels (or poison a negative cache), so collision
// resistance is load-bearing here.
type Image struct {
	Data   string
	Mime   string
	Digest uint64
}

func NewImage(data, mime string) Image {
	h := sha256.New()
	_, _ = h.Write([]byte(mime))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(data))
	return Image{Data: data, Mime: mime, Digest: binary.BigEndian.Uint64(h.Sum(nil)[:8])}
}

// Block is one rendered unit in the chat column.
type Block struct {
	Kind        string // user, assistant, thinking, tool, bash, notice
	Text        string
	ToolName    string
	ToolArgs    string // pretty one-line args (e.g. path) for the header
	ToolArgsRaw string // raw JSON tool-call arguments (write content etc.)
	ToolStatus  string // running, done, error
	ToolResult  string
	// ToolDiff is the toolResult's details.diff — pi's display-oriented diff
	// (line-number gutter, -/+ rows, "..." elision), carried separately from
	// ToolResult so an edit can show the change instead of the one-line
	// "Successfully replaced ..." receipt. Plain text, no ANSI.
	ToolDiff   string
	ToolCallID string
	Err        bool
	Images     []Image
	// TokensBefore is the pre-compaction context size a compaction block
	// reports ("Compacted from 12,345 tokens"); Text carries the summary
	// markdown itself. Zero means unknown, and the block then shows the
	// summary alone.
	TokensBefore int
}
