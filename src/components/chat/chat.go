// Package chat holds the chat-column data model: Block is one rendered
// unit (user/assistant/thinking/tool/bash/notice). The TUI state machine
// in src/app builds and renders these; pure derivations (yank entries,
// last-answer lookup) live in components/yank.
package chat

import "hash/fnv"

// Image is an inline image carried by a chat block. Digest is computed once
// so render-cache keys never hash the base64 payload on every frame.
type Image struct {
	Data   string
	Mime   string
	Digest uint64
}

func NewImage(data, mime string) Image {
	h := fnv.New64a()
	_, _ = h.Write([]byte(mime))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(data))
	return Image{Data: data, Mime: mime, Digest: h.Sum64()}
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
	ToolCallID  string
	Err         bool
	Images      []Image
}
