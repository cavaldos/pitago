package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Notices must become ephemeral popups, never chat history: switching
// models mid-session shouldn't spam the transcript.
func TestNoticeRoutesToToast(t *testing.T) {
	var m Model
	m.AddBlock(Block{Kind: "notice", Text: "model switched → foo"})
	m.AddBlock(Block{Kind: "notice", Text: "boom", Err: true})
	m.AddBlock(Block{Kind: "user", Text: "hi"})
	if len(m.blocks) != 1 || m.blocks[0].Kind != "user" {
		t.Fatalf("notices leaked into chat: %+v", m.blocks)
	}
	if len(m.toasts) != 2 || m.toasts[0].Err || !m.toasts[1].Err {
		t.Fatalf("toasts = %+v", m.toasts)
	}
	if got := m.renderToasts(); got == "" {
		t.Fatal("renderToasts must draw the popup stack")
	}
}

func TestToastCapAndExpiry(t *testing.T) {
	var m Model
	for i := 0; i < maxToasts+1; i++ {
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("t%d", i)})
	}
	if len(m.toasts) != maxToasts || m.toasts[0].Text != "t1" {
		t.Fatalf("cap %d, oldest must drop: %+v", maxToasts, m.toasts)
	}
	m.toasts[0].At = time.Now().Add(-toastTTL - time.Second)
	m.pruneToasts()
	if len(m.toasts) != maxToasts-1 {
		t.Fatalf("expired toast must prune: %+v", m.toasts)
	}
	var empty Model
	if got := empty.renderToasts(); got != "" {
		t.Fatalf("no toasts must render nothing, got %q", got)
	}
}

// The popup must float: same line count in, same line count out — header
// and input rows byte-identical, toast text visible over the chat rows.
func TestTruncANSIKeepsStyle(t *testing.T) {
	styled := "\x1b[32m● hello world, this is long\x1b[0m"
	got := truncANSI(styled, 10)
	if lipgloss.Width(got) != 10 {
		t.Fatalf("width = %d, want 10 (got %q)", lipgloss.Width(got), got)
	}
	if !strings.Contains(got, "\x1b[32m") || !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("style must survive truncation, got %q", got)
	}
	if !strings.Contains(got, "● hello") {
		t.Fatalf("text must survive truncation, got %q", got)
	}
	short := truncANSI("hi", 10)
	if lipgloss.Width(short) != 10 || !strings.HasPrefix(short, "hi") {
		t.Fatalf("narrow must pad, got %q", short)
	}
}

// Changing thinking level (Ctrl+T or /thinking picker) must pop a toast,
// never a chat line: rapid Ctrl+T replaces one popup instead of spamming.
func TestThinkingChangeToasts(t *testing.T) {
	var m Model
	nm, _ := m.Update(SettingsRefreshMsg{Notice: "thinking → high", Level: "high"})
	m = nm.(Model)
	if m.thinkLvl != "high" {
		t.Fatalf("thinkLvl = %q, want high", m.thinkLvl)
	}
	if len(m.toasts) != 1 || m.toasts[0].Text != "thinking → high" {
		t.Fatalf("toasts = %+v", m.toasts)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("thinking change must not touch chat: %+v", m.blocks)
	}
}

// The popup must float: same line count in, same line count out — header
// and input rows byte-identical, toast text visible over the chat rows.
func TestToastOverlayKeepsFrame(t *testing.T) {
	m := Model{winW: 100, winH: 30}
	m.AddBlock(Block{Kind: "notice", Text: "theme → dark"})
	rows := []string{"hdr"}
	for i := 0; i < 10; i++ {
		rows = append(rows, "chat")
	}
	for i := 0; i < 6; i++ {
		rows = append(rows, "input")
	}
	got := strings.Split(m.overlayToasts(strings.Join(rows, "\n")), "\n")
	if len(got) != len(rows) {
		t.Fatalf("overlay shifted layout: %d rows in, %d out", len(rows), len(got))
	}
	if got[0] != "hdr" {
		t.Fatalf("header must stay intact, got %q", got[0])
	}
	for i := len(rows) - 6; i < len(rows); i++ {
		if got[i] != "input" {
			t.Fatalf("input row %d must stay intact, got %q", i, got[i])
		}
	}
	if !strings.Contains(strings.Join(got, "\n"), "theme → dark") {
		t.Fatal("toast text must show over the chat rows")
	}
	// top-right corner over the chat rows: left content survives, the box
	// hugs the right edge at full column width, with a dismiss countdown.
	boxTop := -1
	for i, r := range got {
		if strings.Contains(r, "╭") {
			boxTop = i
			break
		}
	}
	if boxTop != 1 {
		t.Fatalf("toast must float over the top chat rows, box top = %d", boxTop)
	}
	if !strings.HasPrefix(got[boxTop], "chat") {
		t.Fatalf("left content must stay visible, got %q", got[boxTop])
	}
	if lipgloss.Width(got[boxTop]) != m.mainW() {
		t.Fatalf("overlay row width = %d, want %d", lipgloss.Width(got[boxTop]), m.mainW())
	}
	if !strings.Contains(got[boxTop+1], "10s") {
		t.Fatalf("toast row must show the dismiss countdown, got %q", got[boxTop+1])
	}
}
