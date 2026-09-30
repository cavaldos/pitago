package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// pi keeps a compaction and a branch summary in its context as pseudo
// messages (createCompactionSummaryMessage / createBranchSummaryMessage,
// dist/core/messages.js:40-55), so get_messages hands them back. restore()
// used to drop both by role, which is why /compact left nothing but a text
// notice in the chat.
func TestRestoreRendersCompactionAndBranchBlocks(t *testing.T) {
	m := Model{}
	m.restore([]pirpc.AgentMessage{
		{Role: "compactionSummary", Summary: "we were porting the parser", TokensBefore: 12345},
		{Role: "branchSummary", Summary: "the tree experiment", FromID: "e7"},
	})
	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want the two summary messages", len(m.blocks))
	}
	c, b := m.blocks[0], m.blocks[1]
	if c.Kind != "compaction" || c.Text != "we were porting the parser" || c.TokensBefore != 12345 {
		t.Errorf("compaction block = %+v", c)
	}
	if b.Kind != "branch" || b.Text != "the tree experiment" {
		t.Errorf("branch block = %+v", b)
	}
	// FromID only labels pi's own tree rows; the chat block does not show it,
	// but it must not leak into the summary text either.
	if strings.Contains(b.Text, "e7") {
		t.Errorf("branch block text = %q, want the summary only", b.Text)
	}
}

// Collapsed and expanded compaction blocks carry pi's wording, with pitago's
// ctrl+g in place of pi's ctrl+r (same key every other collapsible block uses).
func TestCompactionBlockRenderWording(t *testing.T) {
	bl := Block{Kind: "compaction", TokensBefore: 12345, Text: "we were porting the parser"}

	m := Model{}
	got, skip := m.renderOneBlock(bl, 60)
	if skip {
		t.Fatal("a compaction block must never be skipped")
	}
	plain := stripANSI(got)
	for _, want := range []string{"[compaction]", "Compacted from 12,345 tokens (ctrl+g to expand)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("collapsed compaction = %q, want %q", plain, want)
		}
	}
	if strings.Contains(plain, "we were porting") {
		t.Errorf("the collapsed block must hide the summary: %q", plain)
	}
	if rows := frameRows(t, got); !strings.HasPrefix(stripANSI(rows[0]), "╭─") {
		t.Errorf("the summary must sit in the custom-message box: %q", rows[0])
	}

	m.expandTools = true
	plain = stripANSI(mustRender(t, m, bl, 60))
	for _, want := range []string{"[compaction]", "Compacted from 12,345 tokens", "we were porting", "(ctrl+g to collapse)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded compaction = %q, want %q", plain, want)
		}
	}
}

// The branch summary is pi's twin: label [branch], a collapsed line with no
// token count (a branch has none), and "**Branch Summary**" once expanded.
func TestBranchBlockRenderWording(t *testing.T) {
	bl := Block{Kind: "branch", Text: "the tree experiment"}

	plain := stripANSI(mustRender(t, Model{}, bl, 60))
	for _, want := range []string{"[branch]", "Branch summary (ctrl+g to expand)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("collapsed branch = %q, want %q", plain, want)
		}
	}

	m := Model{expandTools: true}
	plain = stripANSI(mustRender(t, m, bl, 60))
	for _, want := range []string{"[branch]", "Branch Summary", "the tree experiment", "(ctrl+g to collapse)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded branch = %q, want %q", plain, want)
		}
	}
	// The header must be its own row: pi renders "**Branch Summary**\n\n" +
	// summary, and a header glued onto the summary text is what a dropped
	// blank line looks like on screen.
	rows := frameRows(t, plain)
	header := -1
	for i, r := range rows {
		if strings.Contains(r, "Branch Summary") {
			header = i
			break
		}
	}
	if header < 0 {
		t.Fatalf("expanded branch has no header row: %q", plain)
	}
	if joined := strings.Join(rows[header+1:], ""); strings.Contains(joined, "Branch Summary") {
		t.Fatalf("header ran into the summary: %q", plain)
	}
}

// An expanded summary that fits must not be marked as cut: Truncate ends in
// an unconditional "…", so a short summary rendered as "short summary…" would
// read as a truncated one on every single compaction.
func TestExpandedSummaryIsNotMarkedCut(t *testing.T) {
	bl := Block{Kind: "compaction", Text: "short summary", TokensBefore: 12000}
	plain := strings.TrimRight(stripANSI(mustRender(t, Model{expandTools: true}, bl, 60)), " \n")
	if strings.HasSuffix(plain, "…") {
		t.Fatalf("a summary that fits must not end in an ellipsis: %q", plain)
	}
	if !strings.Contains(plain, "short summary") {
		t.Fatalf("expanded compaction lost its summary: %q", plain)
	}
}

// blockKey fingerprints every field the renderer reads; a compaction block
// whose token count changed must not be served from the render cache.
func TestBlockKeyCoversTokensBefore(t *testing.T) {
	a := Block{Kind: "compaction", Text: "s", TokensBefore: 100}
	b := Block{Kind: "compaction", Text: "s", TokensBefore: 200}
	if blockKey(a, 60, false, false, "", false) == blockKey(b, 60, false, false, "", false) {
		t.Error("two token counts must not share a render-cache key")
	}
}

// A compaction that ends mid-turn lands in the chat itself: appending beats
// rebuilding from get_messages, which would drop the in-flight tool blocks.
func TestCompactionEndMidTurnAppendsTheBlock(t *testing.T) {
	m := feed(t, Model{thinking: true}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "threshold", "aborted": false, "willRetry": false,
		"result": map[string]any{"summary": "long thread, summarized", "tokensBefore": 42000},
	}))
	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want the compaction block", len(m.blocks))
	}
	if m.blocks[0].Kind != "compaction" || m.blocks[0].TokensBefore != 42000 {
		t.Errorf("block = %+v", m.blocks[0])
	}
	if m.compacting {
		t.Error("the latch must clear on compaction_end")
	}
	if m.Status != "pi is running…" {
		t.Errorf("status = %q, want the turn's own status back", m.Status)
	}
}

// Idle compaction (the /compact path) rebuilds the transcript from
// get_messages afterwards, so compaction_end must not add the block twice.
func TestCompactionEndIdleAddsNoBlock(t *testing.T) {
	m := feed(t, Model{}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "manual", "aborted": false, "willRetry": false,
		"result": map[string]any{"summary": "s", "tokensBefore": 100},
	}))
	if len(m.blocks) != 0 {
		t.Fatalf("blocks = %d, want none (the reload brings the block)", len(m.blocks))
	}
}

// pi's abort wording, and its severity split: a cancelled /compact is an
// error, a cancelled auto-compaction is informational.
func TestCompactionEndAborted(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   string
		isErr  bool
	}{
		{"manual", "Compaction cancelled", true},
		{"threshold", "Auto-compaction cancelled", false},
	} {
		m := feed(t, Model{compacting: true, Status: "compacting context…"},
			eventMsg(t, "compaction_end", map[string]any{
				"reason": tc.reason, "aborted": true, "willRetry": true,
			}))
		if got := m.LastNotice(); got != tc.want {
			t.Errorf("reason=%s notice = %q, want %q", tc.reason, got, tc.want)
		}
		if m.compacting || m.Status == "compacting context…" {
			t.Errorf("reason=%s left the compaction latch set", tc.reason)
		}
	}
}

// compaction_start is pi's status indicator: a status line, not a chat block.
// pi swaps Esc for abortCompaction(), but there is no abort_compaction RPC
// command, so pitago only says it is working.
func TestCompactionStartShowsStatusOnly(t *testing.T) {
	m := feed(t, Model{}, eventMsg(t, "compaction_start", map[string]any{"reason": "manual"}))
	if !m.compacting || m.Status != "compacting context…" {
		t.Fatalf("compacting = %v, status = %q", m.compacting, m.Status)
	}
	if len(m.blocks) != 0 || m.NoticeCount() != 0 {
		t.Error("the in-flight marker belongs in the status line, not the chat")
	}
}

// A failed compaction reports pi's own errorMessage.
func TestCompactionEndErrorMessage(t *testing.T) {
	m := feed(t, Model{}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "overflow", "aborted": false, "willRetry": true,
		"errorMessage": "compaction failed: no room for a summary",
	}))
	if got := m.LastNotice(); !strings.Contains(got, "no room for a summary") {
		t.Errorf("notice = %q, want pi's errorMessage", got)
	}
}

// pi appends the billing line only when its showCacheMissNotices setting is
// on, and that setting defaults to false (settings-manager.js:702), so the
// default pitago run says nothing.
func TestCompactionCostNoticeFollowsPiSetting(t *testing.T) {
	u := &pirpc.EntryUsage{Input: 8000, Output: 900, CacheRead: 3000, CacheWrite: 445}
	u.Cost.Total = 0.0312

	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if got := CompactionCostNotice("Compaction", u); got != "" {
		t.Errorf("default settings must stay silent like pi, got %q", got)
	}
	if got := CompactionCostNotice("Compaction", nil); got != "" {
		t.Errorf("no usage, no notice, got %q", got)
	}

	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"showCacheMissNotices":true}`)
	want := "Compaction: 12,345 tokens billed (~$0.03)"
	if got := CompactionCostNotice("Compaction", u); got != want {
		t.Errorf("cost notice = %q, want %q", got, want)
	}
	// Under a cent pi shows the tokens without any cost.
	u.Cost.Total = 0.004
	if got := CompactionCostNotice("Branch summary", u); got != "Branch summary: 12,345 tokens billed" {
		t.Errorf("sub-cent cost notice = %q", got)
	}
}

// The billing line is a chat row in pi's warning colour, not a toast.
func TestCompactionCostNoticeIsAChatBlock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"showCacheMissNotices":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := feed(t, Model{thinking: true}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "threshold", "aborted": false, "willRetry": false,
		"result": map[string]any{
			"summary": "s", "tokensBefore": 100,
			"usage": map[string]any{"input": 10, "output": 2, "cost": map[string]any{"total": 0.5}},
		},
	}))
	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want the compaction block plus the cost line", len(m.blocks))
	}
	if m.blocks[1].Kind != "costnotice" || !strings.Contains(m.blocks[1].Text, "tokens billed") {
		t.Errorf("cost block = %+v", m.blocks[1])
	}
	if !strings.Contains(stripANSI(mustRender(t, m, m.blocks[1], 60)), "tokens billed") {
		t.Error("the cost line must render in the chat")
	}
}

// feed runs one message through Update and returns the updated Model: the
// event arms work on a Model copy (Model is a value receiver throughout), so
// the caller must take the model Update hands back.
func feed(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	um, _ := m.Update(msg)
	next, ok := um.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", um)
	}
	return next
}

// eventMsg feeds one pi event into Update the way the RPC client does.
func eventMsg(t *testing.T, typ string, payload map[string]any) piEventMsg {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return piEventMsg{Event: pirpc.Event{Type: typ, Raw: raw}}
}

func mustRender(t *testing.T, m Model, bl Block, w int) string {
	t.Helper()
	out, skip := m.renderOneBlock(bl, w)
	if skip {
		t.Fatalf("block %q must render", bl.Kind)
	}
	return out
}

// pi's get_messages hoists the compaction summary to the FRONT of the array
// (buildContextEntries, session-manager.js:194-230) while pi's own manual
// /compact renders the block at the BOTTOM (interactive-mode.js:2913-2918).
// A bottom-pinned transcript must end with the summary, or the user runs
// /compact and sees nothing but the toast.
func TestRestorePutsTheSummaryBlockLast(t *testing.T) {
	m := Model{}
	m.restore([]pirpc.AgentMessage{
		{Role: "compactionSummary", Summary: "older compaction", TokensBefore: 900},
		{Role: "user", Content: rawText("hello again")},
		{Role: "assistant", Content: rawText("hi")},
		{Role: "compactionSummary", Summary: "we were porting the parser", TokensBefore: 12345},
	})
	if len(m.blocks) != 4 {
		t.Fatalf("blocks = %d, want user + assistant + two summaries", len(m.blocks))
	}
	last := m.blocks[3]
	if last.Kind != "compaction" || last.Text != "we were porting the parser" || last.TokensBefore != 12345 {
		t.Fatalf("last block = %+v, want the newest compaction summary", last)
	}
	// The message that came AFTER the oldest compaction still renders in
	// place: only the summary blocks move to the end.
	if m.blocks[0].Kind != "user" || m.blocks[1].Kind != "assistant" {
		t.Errorf("blocks = %+v, want the conversation untouched", m.blocks[:2])
	}
}

// A followed session (src/live/tail.go) turns every transcript row into a
// message_end event, so the summary pseudo-messages reach applyMessageEnd.
// Without a case there they fall through a switch with no default and vanish.
func TestApplyMessageEndRendersSummaryBlocks(t *testing.T) {
	m := Model{}
	m.applyMessageEnd([]byte(`{"message":{"role":"compactionSummary","summary":"file tail","tokensBefore":4200}}`))
	m.applyMessageEnd([]byte(`{"message":{"role":"branchSummary","summary":"the tree experiment","fromId":"e7"}}`))
	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want both summary blocks", len(m.blocks))
	}
	if m.blocks[0].Kind != "compaction" || m.blocks[0].TokensBefore != 4200 {
		t.Errorf("compaction block = %+v", m.blocks[0])
	}
	if m.blocks[1].Kind != "branch" || m.blocks[1].Text != "the tree experiment" {
		t.Errorf("branch block = %+v", m.blocks[1])
	}
}

// pi derives the billing line from the usage persisted on the summary entry,
// so it comes back on every re-render (a reload, a resume, a file tail).
func TestRestoreRepeatsThePersistedCostLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"showCacheMissNotices":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &pirpc.EntryUsage{Input: 8000, Output: 900, CacheRead: 3000, CacheWrite: 445}
	u.Cost.Total = 0.0312
	msg := pirpc.AgentMessage{Role: "compactionSummary", Summary: "s", TokensBefore: 100, Usage: u}
	m := Model{}
	m.restore([]pirpc.AgentMessage{msg})
	if len(m.blocks) != 2 || m.blocks[1].Kind != "costnotice" {
		t.Fatalf("blocks = %+v, want the summary then the billing row", m.blocks)
	}
	if got, want := m.blocks[1].Text, "Compaction: 12,345 tokens billed (~$0.03)"; got != want {
		t.Errorf("cost line = %q, want %q", got, want)
	}
	// The same row arrives on the live tail's message_end path.
	m2 := Model{}
	m2.applyMessageEnd([]byte(`{"message":{"role":"compactionSummary","summary":"s","tokensBefore":100,` +
		`"usage":{"input":8000,"output":900,"cacheRead":3000,"cacheWrite":445,"cost":{"total":0.0312}}}}`))
	if len(m2.blocks) != 2 || m2.blocks[1].Kind != "costnotice" {
		t.Fatalf("tail blocks = %+v, want the billing row too", m2.blocks)
	}
}

// pi's compact() emits compaction_end{reason:"manual", errorMessage} and then
// rethrows, so the event ALWAYS precedes the RPC error. One failure, one
// toast: the automatic case has nothing behind it and renders here.
func TestCompactionErrorIsReportedOnce(t *testing.T) {
	manual := feed(t, Model{}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "manual", "aborted": false, "willRetry": false,
		"errorMessage": "summarizing failed",
	}))
	if manual.NoticeCount() != 0 {
		t.Errorf("manual failures are owned by handlePiOp's PiOpMsg.Err, got %d notice(s): %q",
			manual.NoticeCount(), manual.LastNotice())
	}
	auto := feed(t, Model{}, eventMsg(t, "compaction_end", map[string]any{
		"reason": "threshold", "aborted": false, "willRetry": true,
		"errorMessage": "summarizing failed",
	}))
	if len(auto.toasts) != 1 || auto.LastNotice() != "summarizing failed" {
		t.Errorf("automatic failure = %q (%d toast(s)), want pi's errorMessage once",
			auto.LastNotice(), len(auto.toasts))
	}
	if !auto.toasts[len(auto.toasts)-1].Err {
		t.Error("pi renders an automatic compaction failure in the error colour")
	}
}

// A summary without a token count must not print "Compacted from 0 tokens":
// pi's header and pitago's collapsed line agree on dropping it.
func TestSummaryHeaderSkipsAMissingTokenCount(t *testing.T) {
	bl := Block{Kind: "compaction", Text: "a summary"}
	m := Model{expandTools: true}
	plain := stripANSI(mustRender(t, m, bl, 60))
	if strings.Contains(plain, "0 tokens") {
		t.Errorf("expanded header must not invent a count: %q", plain)
	}
	if !strings.Contains(plain, "a summary") {
		t.Errorf("the summary itself must still render: %q", plain)
	}
}

// pi's billing row is a bare warning-coloured line: no status bullet.
func TestCostNoticeRendersWithoutAGutterBullet(t *testing.T) {
	got := stripANSI(mustRender(t, Model{}, Block{Kind: "costnotice", Text: "Compaction: 12 tokens billed"}, 60))
	if !strings.HasPrefix(got, "Compaction:") {
		t.Errorf("cost row = %q, want no gutter bullet", got)
	}
}

// A multi-byte summary must not be cut mid-rune by the size cap.
func TestSummaryTruncationIsRuneSafe(t *testing.T) {
	bl := Block{Kind: "compaction", TokensBefore: 10, Text: strings.Repeat("é", maxSummaryChars+500)}
	m := Model{expandTools: true}
	plain := stripANSI(mustRender(t, m, bl, 60))
	if strings.Contains(plain, "\uFFFD") || strings.ContainsRune(plain, 0xFFFD) {
		t.Fatal("the summary was cut mid-rune")
	}
	if !strings.HasSuffix(strings.TrimSpace(plain), "…") &&
		!strings.Contains(plain, "…") {
		t.Error("a truncated summary must keep the … marker")
	}
}

// pi reports compaction_end only as an event; get_state is the authority for
// whether one is in flight. A dropped compaction_end must not wedge the
// status line (mirrors how IsStreaming heals m.thinking).
func TestGetStateReconcilingClearsTheCompactionLatch(t *testing.T) {
	m := Model{compacting: true, Status: "compacting context…"}
	m2 := feed(t, m, stateRefreshMsg{state: pirpc.State{}})
	if m2.compacting || m2.Status != "ready" {
		t.Fatalf("compacting = %v, status = %q, want a healed latch", m2.compacting, m2.Status)
	}
	// A periodic refresh during a live turn must not stomp the running status.
	busy := feed(t, Model{compacting: true, thinking: true, Status: "pi is running…"},
		stateRefreshMsg{state: pirpc.State{IsStreaming: true}})
	if busy.compacting || busy.Status != "pi is running…" {
		t.Fatalf("compacting = %v, status = %q, want the turn's status intact", busy.compacting, busy.Status)
	}
	// And pi reporting a compaction we never saw start must set the latch.
	late := feed(t, Model{}, stateRefreshMsg{state: pirpc.State{IsCompacting: true}})
	if !late.compacting {
		t.Error("get_state isCompacting must arm the latch")
	}
}

func rawText(s string) json.RawMessage {
	raw, _ := json.Marshal(s)
	return raw
}
