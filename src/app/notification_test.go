package app

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "pitago/src/tui/ev"
)

func TestPushToastCreatesHistoryOnce(t *testing.T) {
	var m Model
	m.AddBlock(Block{Kind: "notice", Text: "saved", Err: true})
	if len(m.notificationHistory) != 1 {
		t.Fatalf("history = %d, want 1", len(m.notificationHistory))
	}
	m.pruneToasts()
	m.pruneToasts()
	if len(m.notificationHistory) != 1 {
		t.Fatalf("pruning duplicated or removed history: %+v", m.notificationHistory)
	}
	got := m.notificationHistory[0]
	if got.Text != "saved" || !got.Err || got.At.IsZero() {
		t.Fatalf("history record = %+v", got)
	}
}

func TestNotificationHistorySurvivesTTLAndVisibleCap(t *testing.T) {
	var m Model
	for i := 0; i < maxToasts+1; i++ {
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("visible-%d", i)})
	}
	if len(m.notificationHistory) != maxToasts+1 {
		t.Fatalf("visible cap pruned history: got %d", len(m.notificationHistory))
	}
	m.notificationHistory[0].At = time.Now().Add(-toastTTL - time.Second)
	m.pruneToasts()
	if len(m.notificationHistory) != maxToasts+1 {
		t.Fatalf("toast TTL pruned history: got %d", len(m.notificationHistory))
	}
}

func TestNotificationHistoryCap(t *testing.T) {
	var m Model
	for i := 0; i < maxNotificationHistory+5; i++ {
		m.pushToast(fmt.Sprintf("n%d", i), false)
	}
	if len(m.notificationHistory) != maxNotificationHistory {
		t.Fatalf("history cap = %d, want %d", len(m.notificationHistory), maxNotificationHistory)
	}
	first := m.notificationHistory[0].Text
	if first != fmt.Sprintf("n%d", 5) {
		t.Fatalf("oldest retained record = %q", first)
	}
}

func TestNotificationDialogEmptyFilterAndEscape(t *testing.T) {
	m := Model{winW: 100, winH: 30}
	m.OpenNotifications("needle")
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "notification" {
		t.Fatalf("dialogs = %+v", m.Dialogs)
	}
	d := m.Dialogs[0]
	if len(d.FIdx) != 0 {
		t.Fatalf("empty filter should have no matches: %+v", d.FIdx)
	}
	out := stripANSI(m.renderNotificationDialog(d))
	if !strings.Contains(out, "no matching notifications") {
		t.Fatalf("empty state missing: %q", out)
	}

	var populated Model
	populated.winW, populated.winH = 100, 30
	populated.AddBlock(Block{Kind: "notice", Text: "needle found"})
	populated.AddBlock(Block{Kind: "notice", Text: "needle failed", Err: true})
	populated.OpenNotifications("needle")
	d = populated.Dialogs[0]
	if len(d.FIdx) != 2 || d.Descs[d.FIdx[0]] == "" {
		t.Fatalf("notification filter/reindex = %+v", d)
	}
	if out = stripANSI(populated.renderNotificationDialog(d)); !strings.Contains(out, "needle found") || !strings.Contains(out, "needle failed") ||
		!strings.Contains(out, "INFO") || !strings.Contains(out, "ERROR") {
		t.Fatalf("notification rows missing info/error distinction: %q", out)
	}
	updated, _ := populated.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	if len(updated.(Model).Dialogs) != 0 {
		t.Fatalf("Esc did not close dialog: %+v", updated.(Model).Dialogs)
	}
}

// The dialog must never exceed the terminal: no wrapped rows, no box wider
// than the screen, no box taller than the placement budget. Regression test
// for measured 100x30 -> 100x48, 80x24 -> 80x36, 40x12 -> 72x14.
func TestNotificationDialogFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}, {40, 12}, {200, 60}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			var m Model
			m.winW, m.winH = w, h
			// Long text, both kinds, more rows than any window can show.
			for i := 0; i < maxNotificationHistory; i++ {
				m.pushToast(strings.Repeat("verbose-notification-", 12)+fmt.Sprint(i), i%3 == 0)
			}
			m.OpenNotifications("")
			d := m.Dialogs[0]
			// Walk the cursor so both scroll markers are exercised.
			for _, c := range []int{0, 5, 99, d.Cursor} {
				d.Cursor = c
				out := m.renderNotificationDialog(d)
				plain := stripANSI(out)
				lines := strings.Split(plain, "\n")
				for i, ln := range lines {
					if got := lipgloss.Width(ln); got > w {
						t.Fatalf("cursor %d: row %d width %d > terminal %d: %q", c, i, got, w, ln)
					}
				}
				if got := lipgloss.Width(plain); got > w {
					t.Fatalf("cursor %d: frame width %d > terminal %d", c, got, w)
				}
				if got := lipgloss.Height(out); got > h-2 {
					t.Fatalf("cursor %d: frame height %d > budget %d (rows=%d)", c, got, h-2, len(lines))
				}
			}
			// Counter must describe the snapshot, not the live history.
			if !strings.Contains(stripANSI(m.renderNotificationDialog(d)),
				fmt.Sprintf("Latest %d of %d", maxNotificationHistory, maxNotificationHistory)) {
				t.Fatal("header counter does not match the dialog snapshot")
			}
			// A toast arriving while the window is open must not change it.
			m.pushToast("arrived later", true)
			if !strings.Contains(stripANSI(m.renderNotificationDialog(d)),
				fmt.Sprintf("Latest %d of %d", maxNotificationHistory, maxNotificationHistory)) {
				t.Fatal("header counter followed live history instead of the snapshot")
			}
		})
	}
}

// Notification rows must never be cut mid-ANSI: every marker, timestamp and
// word of an unselected row survives at readable widths.
func TestNotificationRowKeepsSegmentsIntact(t *testing.T) {
	var m Model
	m.winW, m.winH = 100, 30
	m.pushToast("kept whole", false)
	m.pushToast("failed whole", true)
	m.OpenNotifications("")
	d := m.Dialogs[0]
	d.Cursor = 0 // select the first row so the highlighted path is covered
	plain := stripANSI(m.renderNotificationDialog(d))
	for _, want := range []string{"● INFO", "× ERROR", "kept whole", "failed whole"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in:\n%s", want, plain)
		}
	}
	if !regexp.MustCompile(`\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(plain) {
		t.Fatalf("timestamp missing or malformed in:\n%s", plain)
	}
}

// The dialog must take the painted two-column path: as a native picker (or a
// native single-pane list) the picker swallows Ctrl+Y, which is exactly how
// copy was lost.
func TestNotificationUsesPaintedTwoColumnPath(t *testing.T) {
	var m Model
	m.winW, m.winH = 100, 30
	m.pushToast("hi", false)
	m.OpenNotifications("")
	if !m.PaintedDialog() {
		t.Fatal("notification must be painted so Ctrl+Y reaches the dialog")
	}
	if v, ok := m.NativeSettings(); ok || v.Kind != "" {
		t.Fatalf("notification must not use the native single-pane view: %+v", v)
	}
	if nativePickerKind(m.Dialogs[0]) {
		t.Fatal("notification must not open as a native picker (it swallows Ctrl+Y)")
	}
}

// Left column = title (truncated), right column = the whole notification: a
// long message must stay readable even though its row cannot show it.
func TestNotificationDetailColumnShowsFullText(t *testing.T) {
	const tail = "zzmarkerzz"
	var m Model
	m.winW, m.winH = 100, 30
	m.pushToast(strings.Repeat("long message about a broken pipe ", 3)+tail, true)
	m.OpenNotifications("")
	d := m.Dialogs[0]
	plain := stripANSI(m.renderNotificationDialog(d))
	if !strings.Contains(plain, "TITLES") || !strings.Contains(plain, "DETAIL") {
		t.Fatalf("two-column header missing:\n%s", plain)
	}
	if !strings.Contains(plain, tail) {
		t.Fatalf("detail column dropped the message tail:\n%s", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("title column should truncate the long row:\n%s", plain)
	}
}
