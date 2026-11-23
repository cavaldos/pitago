package textarea

import (
	"strings"
	"testing"
	"time"

	"pitago/src/tui/ev"
)

func TestEditingAndPaste(t *testing.T) {
	m := New()
	m.Focus()
	m.SetWidth(20)
	m.SetHeight(3)
	for _, msg := range []ev.KeyMsg{
		{Type: ev.KeyRunes, Runes: []rune("日本語\nsecond"), Paste: true},
		{Type: ev.KeyHome},
		{Type: ev.KeyRunes, Runes: []rune("✓ ")},
		{Type: ev.KeyEnd},
		{Type: ev.KeyBackspace},
	} {
		m, _ = m.Update(msg)
	}
	if got := m.Value(); got != "日本語\n✓ secon" || m.Line() != 1 {
		t.Fatalf("edit = %q at line %d", got, m.Line())
	}
}

func TestSetCursorAt(t *testing.T) {
	m := New()
	m.Focus()
	m.ShowLineNumbers = false
	m.Prompt = "❯ "
	m.SetWidth(10)
	m.SetHeight(3)
	m.SetValue("日本語 abcde\nlast\nfar")
	// First logical line wraps: the second cell of 日 still points at 日.
	m.viewport.YOffset = 0
	m.SetCursorAt(3, 0)
	if m.row != 0 || m.col != 0 {
		t.Fatalf("wide glyph: got %d,%d", m.row, m.col)
	}
	m.SetCursorAt(2, 1)
	if m.row != 0 || m.col != 4 {
		t.Fatalf("soft wrap: got %d,%d, view %q", m.row, m.col, m.View())
	}
	m.viewport.YOffset = 1
	m.SetCursorAt(4, 1)
	if m.row != 1 || m.col != 2 {
		t.Fatalf("scrolled view: got %d,%d", m.row, m.col)
	}
	m.viewport.YOffset = 2
	m.SetCursorAt(2, 2)
	if m.row != 2 || m.col != 3 {
		t.Fatalf("blank row: got %d,%d", m.row, m.col)
	}
}

// TestWordLeftNoHang pins the fix for upstream's infinite loop: word-left
// with only whitespace (or an empty first line) before the cursor stops at
// the start of the buffer.
func TestWordLeftNoHang(t *testing.T) {
	for _, v := range []string{"", "   ", "   hello", "\nfoo", "\n\n  x", " \t "} {
		for _, key := range []ev.KeyMsg{{Type: ev.KeyLeft, Alt: true}, {Type: ev.KeyRunes, Runes: []rune{'b'}, Alt: true}} {
			m := New()
			m.Focus()
			m.SetWidth(30)
			m.SetValue(v)
			// Put the cursor just after the leading whitespace / blank rows.
			m.row, m.col = 0, 0
			for m.row < len(m.value)-1 && strings.TrimSpace(string(m.value[m.row])) == "" {
				m.row++
			}
			for m.col < len(m.value[m.row]) && m.value[m.row][m.col] == ' ' {
				m.col++
			}
			done := make(chan Model, 1)
			go func() { n, _ := m.Update(key); done <- n }()
			select {
			case n := <-done:
				if n.row != 0 || n.col != 0 {
					t.Errorf("%q %s: cursor (%d,%d), want (0,0)", v, key, n.row, n.col)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%q %s: wordLeft hung", v, key)
			}
		}
	}
}
