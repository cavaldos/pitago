package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"pitago/src/pirpc"
)

// Streaming bursts share one paint per frame: the first delta paints,
// an immediate second one coalesces into a flush tick, and the flush
// paints without leaving stale state.
func TestStreamingCoalescesToFrame(t *testing.T) {
	m := Model{tools: make(map[string]int), curAsst: -1, curThink: -1, ready: true}
	m.vp = viewport.New(100, 20)
	m.sideVp = viewport.New(sideInnerW, 10)
	ev := piEventMsg{pirpc.Event{Type: "message_update",
		Raw: []byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`)}}

	um, _ := m.Update(ev)
	m = um.(Model)
	if m.pendingPaint {
		t.Fatal("first delta should paint immediately")
	}

	m.lastPaint = time.Now() // burst: no frame elapsed
	um, cmd := m.Update(ev)
	m = um.(Model)
	if !m.pendingPaint {
		t.Fatal("burst delta should coalesce, not repaint")
	}
	if cmd == nil {
		t.Fatal("coalesced delta should schedule a flush tick")
	}

	um, _ = m.Update(streamFlushMsg{})
	if um.(Model).pendingPaint {
		t.Fatal("flush should clear the pending paint")
	}
}

// History blocks keep their rendered string: re-rendering with an
// unchanged history hits the cache, and only the dirty tail re-renders.
func TestRenderBlocksCachesHistory(t *testing.T) {
	m := &Model{blocks: []Block{
		{Kind: "user", Text: "hi"},
		{Kind: "assistant", Text: "history answer"},
	}}
	m.vp = viewport.New(100, 20)
	first := m.renderBlocks()
	if len(m.renderCache) != 2 {
		t.Fatalf("cache should cover 2 blocks, got %d", len(m.renderCache))
	}
	if second := m.renderBlocks(); second != first {
		t.Fatal("unchanged render should hit the cache")
	}
	m.blocks[1].Text += " more"
	third := m.renderBlocks()
	if !strings.Contains(third, "more") || !strings.Contains(third, "hi") {
		t.Fatalf("dirty tail should re-render with history kept: %q", stripANSI(third))
	}
}
