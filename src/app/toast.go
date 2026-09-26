package app

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Toast is one ephemeral popup notification: transient confirmations like
// "model switched → …" or "yanked (N chars)". Toasts float top-right over
// the chat and auto-dismiss after 5s — they never enter the chat history
// (m.blocks), so switching models mid-session doesn't spam the transcript.
type Toast struct {
	Text string
	Err  bool
	At   time.Time
}

const (
	toastTTL  = 10 * time.Second // every popup auto-dismisses after 10s
	maxToasts = 10               // cap: oldest drops when full
)

// toastTickMsg fires every second while a popup is visible (live dismiss
// countdown) and on expiry, so Update can prune + repaint even when the
// user is idle (no keystrokes to trigger a refresh).
type toastTickMsg struct{}

// Notify shows an info popup. It rides AddBlock so every existing
// "notice" call site becomes a toast with no changes there.
func (m *Model) Notify(text string) { m.AddBlock(Block{Kind: "notice", Text: text}) }

// NotifyErr shows an error popup (red, same 10s TTL).
func (m *Model) NotifyErr(text string) { m.AddBlock(Block{Kind: "notice", Text: text, Err: true}) }

// scheduleToastTick repaints 1s later: it drives the live dismiss
// countdown and prunes on expiry. Each toastTickMsg reschedules while a
// popup remains, so the loop runs only while something is visible.
func scheduleToastTick() {
	if ProgRef == nil {
		return
	}
	time.AfterFunc(time.Second, func() { ProgRef.Send(toastTickMsg{}) })
}

// pushToast appends a toast, caps the stack, and starts the tick loop.
// Expiry also runs opportunistically on every Update, so a missed tick
// still clears (no timers in tests — ProgRef is nil there).
func (m *Model) pushToast(text string, isErr bool) {
	m.pruneToasts()
	now := time.Now()
	t := Toast{Text: text, Err: isErr, At: now}
	m.toasts = append(m.toasts, t)
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[len(m.toasts)-maxToasts:]
	}
	m.notificationHistory = append(m.notificationHistory, t)
	if len(m.notificationHistory) > maxNotificationHistory {
		m.notificationHistory = m.notificationHistory[len(m.notificationHistory)-maxNotificationHistory:]
	}
	scheduleToastTick()
}

// pruneToasts drops expired toasts (uniform 5s TTL).
func (m *Model) pruneToasts() {
	if len(m.toasts) == 0 {
		return
	}
	now := time.Now()
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Sub(t.At) < toastTTL {
			kept = append(kept, t)
		}
	}
	// ponytail: global slice reuse is fine here — toasts are UI-only,
	// single-goroutine (Bubble Tea), no shared ceiling.
	m.toasts = kept
}

// toastRemain is the live dismiss countdown in whole seconds (ceil, min 1).
func toastRemain(t Toast) int {
	rem := int((toastTTL - time.Since(t.At) + 999*time.Millisecond) / time.Second)
	if rem < 1 {
		rem = 1
	}
	return rem
}

// renderToasts draws the popup stack: a ~1/3-width box, one row per toast
// (● info / × error) plus its live dismiss countdown ("· 4s"), repainted
// every second by the toast tick. "" when empty.
func (m Model) renderToasts() string {
	if len(m.toasts) == 0 {
		return ""
	}
	w := m.mainW() / 3
	if w < 24 {
		w = 24
	}
	if w > m.mainW()-2 {
		w = m.mainW() - 2
	}
	lines := make([]string, 0, len(m.toasts))
	for _, t := range m.toasts {
		tag := fmt.Sprintf(" · %ds", toastRemain(t))
		body := Short(t.Text, w-4-len(tag))
		if t.Err {
			lines = append(lines, errStyle.Render("× "+body)+toolStyle.Render(tag))
		} else {
			lines = append(lines, okStyle.Render("● ")+statusBarStyle.Render(body)+toolStyle.Render(tag))
		}
	}
	box := cmdPopStyle.Width(w).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return box
}

// truncANSI fits s into exactly n display cells for overlaying: escape
// sequences (CSI colors, OSC hyperlinks) pass through whole and count zero
// width, printable runes stop at the budget, and a reset closes any cut
// mid-style so colors can't bleed into the toast box. Narrows pad with
// spaces so the popup always lands on the same right column.
func truncANSI(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if w := lipgloss.Width(s); w <= n {
		return s + strings.Repeat(" ", n-w)
	}
	var b strings.Builder
	cells := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[': // CSI: copy whole, zero width
				j := i + 2
				for j < len(s) && !isCSIEnd(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			case ']': // OSC hyperlink: ends with BEL or ESC\
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if w := lipgloss.Width(string(r)); cells+w > n {
			break
		} else {
			cells += w
		}
		b.WriteRune(r)
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String() + strings.Repeat(" ", n-cells)
}

func isCSIEnd(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// overlayToasts floats the popup over the top rows of the chat column,
// right-aligned at ~1/3 width. Only the right third is covered — the left
// 2/3 of each row keeps its text and colors (truncANSI), so the popup
// reads as transparent over the transcript instead of erasing it. Total
// line count never changes: the input box and sidebar stay put — zero
// layout shift. No-op when empty or the terminal is too short.
func (m Model) overlayToasts(left string) string {
	box := m.renderToasts()
	if box == "" {
		return left
	}
	lines := strings.Split(left, "\n")
	tlines := strings.Split(box, "\n")
	// Input box is fixed-frame: textarea 3 + chips 0/1 + footer 1 + border 2.
	inputH := 6 + m.chipH()
	// panelHeight, not lipgloss.Height: an empty string measures as one row
	// under lipgloss, so a hidden task widget (prefs.taskWidgetOff) or an
	// empty list would reserve a phantom row and shift the toast up.
	taskH := panelHeight(m.renderTaskWidget())
	avail := len(lines) - 1 - inputH - taskH // rows below header, above persistent blocks
	if avail <= 0 {
		return left
	}
	if len(tlines) > avail {
		tlines = tlines[:avail]
	}
	leftW := m.mainW() - lipgloss.Width(tlines[0])
	if leftW < 0 {
		leftW = 0
	}
	for i, tl := range tlines {
		lines[1+i] = truncANSI(lines[1+i], leftW) + tl
	}
	return strings.Join(lines, "\n")
}
