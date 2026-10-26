package app

import "strings"

// --- jump to a transcript message --------------------------------------
//
// Positional matching is the only bridge available: the transcript is built
// from get_messages, which carries no session entry ids. A caller counts the
// user/assistant messages it saw in order and passes the ordinal; it is
// resolved back to a block index here. It is right whenever both views come
// from the same session. (/tree used to jump this way; it now navigates the
// session like pi, so nothing in pitago calls this today.)

// jumpMark is the dim marker drawn immediately above the block a jump landed
// on. Without it a scrolled transcript gives no evidence of where the jump
// went, since the tree entry itself is not in the chat.
const jumpMark = "▌ jumped here"

// JumpToEntry scrolls the transcript to the ordinal-th user/assistant block
// and marks it. It reports false when there is no such block — including
// before the first paint, when no line table exists; src/builtin uses that
// to fall back to showing the entry detail in the chat.
func (m *Model) JumpToEntry(ordinal int) bool {
	if !m.ready || ordinal < 0 {
		return false
	}
	// Ordinal resolution: count the transcript's user/assistant blocks in
	// order and take the ordinal-th, exactly as the tree rows counted them.
	// A block with no text is not counted: an aborted turn can leave an
	// empty assistant block behind (text_start opens one, the deltas never
	// arrive), and the tree side never numbered it. Counting it here would
	// shift every later ordinal by one and land the jump on the wrong
	// message.
	idx := -1
	n := 0
	for i, bl := range m.blocks {
		if bl.Kind != "user" && bl.Kind != "assistant" {
			continue
		}
		if strings.TrimSpace(bl.Text) == "" {
			continue
		}
		if n == ordinal {
			idx = i
			break
		}
		n++
	}
	if idx < 0 {
		return false
	}
	m.Refresh() // painted transcript ⇒ a block line table
	if len(m.blockLine) != len(m.blocks) {
		m.renderBlocks()
	}
	if idx >= len(m.blockLine) {
		return false
	}
	m.jumpBlock = idx
	m.Refresh() // the mark is in the content, so the line table is final
	// Land the block about a third from the top: the mark sits one line
	// above it and the tool/assistant lines that follow stay readable.
	off := m.blockLine[idx] - m.vp.Height/3
	if off < 0 {
		off = 0
	}
	m.vp.SetYOffset(off) // clamps so the last line stays reachable
	return true
}
