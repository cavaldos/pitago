package viewport

import (
	"testing"

	"pitago/src/tui/ev"
)

func TestScrollAndClip(t *testing.T) {
	m := New(4, 2)
	m.SetContent("日本語\n001\n002\n003\n004\n005")
	if got := m.View(); got != "日本\n001 " {
		t.Fatalf("initial view = %q", got)
	}
	check := func(want int) {
		t.Helper()
		if m.YOffset != want || m.AtTop() != (want == 0) || m.AtBottom() != (want == 4) || m.ScrollPercent() != float64(want)/4 {
			t.Fatalf("want offset %d, got offset %d (top=%v bottom=%v scroll=%v)", want, m.YOffset, m.AtTop(), m.AtBottom(), m.ScrollPercent())
		}
	}
	m, _ = m.Update(ev.MouseMsg{Button: ev.MouseButtonWheelDown, Action: ev.MouseActionPress})
	check(3)
	m, _ = m.Update(ev.KeyMsg{Type: ev.KeyPgDown})
	check(4)
	m, _ = m.Update(ev.KeyMsg{Type: ev.KeyPgUp})
	check(2)
	m, _ = m.Update(ev.MouseMsg{Button: ev.MouseButtonWheelUp, Action: ev.MouseActionPress})
	check(0)
}
