package viewport

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	bvp "github.com/charmbracelet/bubbles/viewport"

	"pitago/src/tui/ev"
	"pitago/src/tui/teabridge"
)

// TestParityWithBubbles drives this port and bubbles v1 viewport with the
// same random content/size/key/wheel sequences and requires identical state
// and View() after every step.
func TestParityWithBubbles(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	keys := []string{"pgdown", "pgup", " ", "f", "b", "u", "d", "ctrl+u", "ctrl+d", "down", "up", "j", "k", "x"}
	toKey := func(s string) ev.KeyMsg {
		switch s {
		case "pgdown":
			return ev.KeyMsg{Type: ev.KeyPgDown}
		case "pgup":
			return ev.KeyMsg{Type: ev.KeyPgUp}
		case "down":
			return ev.KeyMsg{Type: ev.KeyDown}
		case "up":
			return ev.KeyMsg{Type: ev.KeyUp}
		case "ctrl+u":
			return ev.KeyMsg{Type: ev.KeyCtrlU}
		case "ctrl+d":
			return ev.KeyMsg{Type: ev.KeyCtrlD}
		case " ":
			return ev.KeyMsg{Type: ev.KeySpace, Runes: []rune{' '}}
		}
		return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune(s)}
	}
	content := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "\x1b[3%dmline %d\x1b[0m %s", i%8, i, strings.Repeat("界", rng.Intn(30)))
			if i < n-1 || rng.Intn(2) == 0 {
				b.WriteByte('\n')
			}
		}
		return b.String()
	}
	for trial := 0; trial < 40; trial++ {
		w, h := 10+rng.Intn(60), 1+rng.Intn(25)
		mine, theirs := New(w, h), bvp.New(w, h)
		for step := 0; step < 120; step++ {
			var msg ev.Msg
			switch r := rng.Intn(10); {
			case r == 0:
				s := content(rng.Intn(80))
				mine.SetContent(s)
				theirs.SetContent(s)
			case r == 1:
				nh := 1 + rng.Intn(25)
				mine.Height, theirs.Height = nh, nh
			case r == 2:
				off := rng.Intn(100) - 10
				mine.SetYOffset(off)
				theirs.SetYOffset(off)
			case r == 3:
				mine.GotoBottom()
				theirs.GotoBottom()
			case r < 7:
				msg = toKey(keys[rng.Intn(len(keys))])
			default:
				btn := []ev.MouseButton{ev.MouseButtonWheelUp, ev.MouseButtonWheelDown}[rng.Intn(2)]
				msg = ev.MouseMsg{Action: ev.MouseActionPress, Button: btn}
			}
			if msg != nil {
				mine, _ = mine.Update(msg)
				theirs, _ = theirs.Update(teabridge.ToTea(msg))
			}
			if mine.YOffset != theirs.YOffset || mine.AtBottom() != theirs.AtBottom() ||
				mine.TotalLineCount() != theirs.TotalLineCount() || mine.ScrollPercent() != theirs.ScrollPercent() {
				t.Fatalf("trial %d step %d: state diverged: off %d/%d", trial, step, mine.YOffset, theirs.YOffset)
			}
			if mv, tv := mine.View(), theirs.View(); mv != tv {
				t.Fatalf("trial %d step %d: View diverged\nmine:\n%q\ntheirs:\n%q", trial, step, mv, tv)
			}
		}
	}
}
