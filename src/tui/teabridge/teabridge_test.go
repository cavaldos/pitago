package teabridge

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/tui/ev"
)

func TestKeyStringParity(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyCtrlX}, {Type: tea.KeyEnter, Alt: true}, {Type: tea.KeyShiftTab},
		{Type: tea.KeyPgDown}, {Type: tea.KeySpace}, {Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("é")}, {Type: tea.KeyRunes, Runes: []rune("ab"), Paste: true},
		{Type: tea.KeyCtrlShiftEnd}, {Type: tea.KeyF12},
	} {
		got := FromTea(k).(ev.KeyMsg)
		if got.String() != k.String() {
			t.Errorf("%q: ev gives %q", k.String(), got.String())
		}
		if back := ToTea(got).(tea.KeyMsg); back.String() != k.String() {
			t.Errorf("%q: round-trip gives %q", k.String(), back.String())
		}
	}
}

func TestMouseRoundTrip(t *testing.T) {
	in := tea.MouseMsg{X: 3, Y: 7, Shift: true, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}
	got := FromTea(in).(ev.MouseMsg)
	if got.String() != tea.MouseEvent(in).String() || got.X != 3 || got.Y != 7 {
		t.Fatalf("mouse = %+v (%s)", got, got.String())
	}
	if ToTea(got).(tea.MouseMsg) != in {
		t.Fatal("mouse round-trip mismatch")
	}
}

func TestCmdTranslation(t *testing.T) {
	if _, ok := ToTeaCmd(ev.Quit)().(tea.QuitMsg); !ok {
		t.Fatal("Quit must become tea.QuitMsg")
	}
	type ping struct{}
	b := ToTeaCmd(ev.Batch(ev.Quit, func() ev.Msg { return ping{} }))()
	bm, ok := b.(tea.BatchMsg)
	if !ok || len(bm) != 2 {
		t.Fatalf("batch = %T %v", b, b)
	}
	if _, ok := bm[1]().(ping); !ok {
		t.Fatal("app msgs must pass through unchanged")
	}
	if ev.Batch(nil, nil) != nil || ToTeaCmd(nil) != nil {
		t.Fatal("nil compaction")
	}
	if ToTeaCmd(ev.Sequence(ev.Quit, ev.Quit))() == nil {
		t.Fatal("sequence must translate")
	}
}
