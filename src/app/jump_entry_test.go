package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func jumpTestModel(blocks ...Block) Model {
	m := Model{winW: 120, winH: 40, ready: true, jumpBlock: -1, blocks: blocks}
	m.vp = viewport.New(m.mainW(), 20)
	return m
}

func TestJumpToEntryScrollsAndMarks(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "one"},
		Block{Kind: "tool", ToolName: "bash", ToolStatus: "done", ToolArgs: "ls", ToolResult: "a\nb"},
		Block{Kind: "assistant", Text: "two"},
		Block{Kind: "user", Text: "three"},
	)
	want := []int{0, 2, 3}
	for ordinal, idx := range want {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: JumpToEntry reported no block", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d", ordinal, m.jumpBlock, idx)
		}
		// The block lands about a third from the top, clamped so the last
		// line of the transcript stays reachable.
		off := m.blockLine[idx] - m.vp.Height/3
		if maxOff := m.vp.TotalLineCount() - m.vp.Height; off > maxOff {
			off = maxOff
		}
		if off < 0 {
			off = 0
		}
		if m.vp.YOffset != off {
			t.Fatalf("ordinal %d: YOffset = %d, want %d", ordinal, m.vp.YOffset, off)
		}
		lines := strings.Split(m.renderBlocks(), "\n")
		if m.blockLine[idx] < 1 || !strings.Contains(stripANSI(lines[m.blockLine[idx]-1]), jumpMark) {
			t.Fatalf("ordinal %d: no %q mark above the block:\n%s", ordinal, jumpMark, m.renderBlocks())
		}
	}
}

func TestJumpToEntryRejectsBadOrdinalAndUnpaintedModel(t *testing.T) {
	m := jumpTestModel(Block{Kind: "user", Text: "one"}, Block{Kind: "assistant", Text: "two"})
	for _, bad := range []int{-1, 2, 99} {
		if m.JumpToEntry(bad) {
			t.Fatalf("ordinal %d must not resolve", bad)
		}
	}
	if m.jumpBlock != -1 {
		t.Fatalf("a failed jump armed the mark: %d", m.jumpBlock)
	}
	// Nothing is painted before the first WindowSizeMsg: there is no line
	// table and no viewport, so the caller must fall back to the detail.
	cold := Model{jumpBlock: -1}
	if cold.JumpToEntry(0) {
		t.Fatalf("an unpainted model must report failure")
	}
}

// An aborted turn leaves an EMPTY assistant block behind (text_start opens
// one, the deltas never arrive) while the tree side never numbered that
// message, so an empty block must not consume an ordinal: counting it would
// shift every later jump one message early.
func TestJumpToEntrySkipsEmptyLeftoverBlocks(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "assistant", Text: ""}, // aborted turn, never filled
		Block{Kind: "user", Text: "one"},
		Block{Kind: "assistant", Text: "two"},
	)
	for ordinal, idx := range map[int]int{0: 1, 1: 2} {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: JumpToEntry reported no block", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d (was the empty block counted?)", ordinal, m.jumpBlock, idx)
		}
	}
	if m.JumpToEntry(2) {
		t.Error("an ordinal past the last non-empty block must not resolve")
	}
}

// A user message that carries only images still becomes a transcript block
// ("📷 1 image attached", built from the image count), so it must keep its
// ordinal: this pins the count src/builtin's numbering has to agree with.
func TestJumpToEntryCountsImageOnlyUserBlock(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "📷 1 image attached"},
		Block{Kind: "assistant", Text: "one"},
	)
	for ordinal, idx := range []int{0, 1} {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: image-only user block was skipped", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d", ordinal, m.jumpBlock, idx)
		}
	}
}

func TestBlockLineTracksEveryBlock(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "one"},
		Block{Kind: "assistant", Text: ""}, // skipped: must not drift the table
		Block{Kind: "assistant", Text: "two"},
	)
	m.renderBlocks()
	if len(m.blockLine) != len(m.blocks) {
		t.Fatalf("blockLine has %d entries for %d blocks", len(m.blockLine), len(m.blocks))
	}
	if m.blockLine[2] <= m.blockLine[0] || m.blockLine[1] != m.blockLine[2] {
		t.Fatalf("skipped block drifted the table: %v", m.blockLine)
	}
}

func TestBlockCountSeesPostedBlocks(t *testing.T) {
	m := Model{jumpBlock: -1}
	if m.BlockCount() != 0 {
		t.Fatalf("fresh model has %d blocks", m.BlockCount())
	}
	m.AddBlock(Block{Kind: "user", Text: "posted by a tree action"})
	if m.BlockCount() != 1 {
		t.Fatalf("BlockCount = %d after AddBlock", m.BlockCount())
	}
}
