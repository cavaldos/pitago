package textarea

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	bta "github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"pitago/src/tui/ev"
	"pitago/src/tui/teabridge"
)

// TestMain forces a truecolor profile: under `go test` lipgloss detects no
// TTY and renders every style as plain text, which would make the View
// comparisons blind to colors, the reversed cursor and the placeholder style.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// pair drives the bubbles v1 textarea (reference) and this port in lockstep.
type pair struct {
	t    *testing.T
	ref  bta.Model
	port Model
}

// configure applies the same setup to both, in one of two flavors: the
// stock New() defaults, or src/app's input box (model.go New()).
func newPair(t *testing.T, app bool, width int) *pair {
	t.Helper()
	ref, port := bta.New(), New()
	if app {
		muted, input := lipgloss.Color("243"), lipgloss.Color("#61AFEF")
		prompt := func(i int) string {
			if i == 0 {
				return "❯ "
			}
			return "  "
		}
		ref.Placeholder, port.Placeholder = "Type a message… (/ commands · ^V paste)", "Type a message… (/ commands · ^V paste)"
		ref.Focus()
		port.Focus()
		ref.SetHeight(3)
		port.SetHeight(3)
		ref.ShowLineNumbers, port.ShowLineNumbers = false, false
		ref.Prompt, port.Prompt = "❯ ", "❯ "
		ref.SetPromptFunc(2, prompt)
		port.SetPromptFunc(2, prompt)
		ref.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(input)
		port.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(input)
		ref.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted)
		port.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted)
		ref.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(muted)
		port.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(muted)
		ref.FocusedStyle.CursorLine, port.FocusedStyle.CursorLine = lipgloss.NewStyle(), lipgloss.NewStyle()
		ref.BlurredStyle.CursorLine, port.BlurredStyle.CursorLine = lipgloss.NewStyle(), lipgloss.NewStyle()
	} else {
		ref.Focus()
		port.Focus()
	}
	ref.SetWidth(width)
	port.SetWidth(width)
	p := &pair{t: t, ref: ref, port: port}
	p.check("setup")
	return p
}

// wordLeftHangs reports whether upstream wordLeft would spin forever from
// the current state: every rune before the cursor is whitespace and the
// first rune of the buffer is whitespace or missing (see the fix in
// wordLeft). The port is checked against that case in TestWordLeftNoHang.
func (p *pair) wordLeftHangs() bool {
	m := p.port
	for r := 0; r <= m.row; r++ {
		end := len(m.value[r])
		if r == m.row {
			end = min(m.col, end)
		}
		for _, c := range m.value[r][:end] {
			if !unicode.IsSpace(c) {
				return false
			}
		}
	}
	return len(m.value[0]) == 0 || unicode.IsSpace(m.value[0][0])
}

func isWordLeft(k ev.KeyMsg) bool {
	s := k.String()
	return s == "alt+left" || s == "alt+b"
}

func (p *pair) key(k ev.KeyMsg) {
	p.t.Helper()
	if isWordLeft(k) && p.wordLeftHangs() {
		return // upstream bug: the reference would never return
	}
	var c1 tea.Cmd
	var c2 ev.Cmd
	p.ref, c1 = p.ref.Update(teabridge.ToTea(k))
	p.port, c2 = p.port.Update(k)
	if (c1 == nil) != (c2 == nil) {
		p.t.Fatalf("after %q: cmd nil-ness differs (ref %v, port %v)", k.String(), c1 == nil, c2 == nil)
	}
	p.check(fmt.Sprintf("after %q", k.String()))
}

// check asserts every observable the app reads is identical.
func (p *pair) check(when string) {
	p.t.Helper()
	r, o := p.ref, p.port
	if r.Value() != o.Value() {
		p.t.Fatalf("%s: Value\nref  %q\nport %q", when, r.Value(), o.Value())
	}
	if r.Line() != o.Line() || r.Length() != o.Length() || r.LineCount() != o.LineCount() ||
		r.Width() != o.Width() || r.Height() != o.Height() || r.Focused() != o.Focused() {
		p.t.Fatalf("%s: geometry ref line=%d len=%d lines=%d w=%d h=%d f=%v / port line=%d len=%d lines=%d w=%d h=%d f=%v",
			when, r.Line(), r.Length(), r.LineCount(), r.Width(), r.Height(), r.Focused(),
			o.Line(), o.Length(), o.LineCount(), o.Width(), o.Height(), o.Focused())
	}
	if a, b := fmt.Sprintf("%+v", r.LineInfo()), fmt.Sprintf("%+v", o.LineInfo()); a != b {
		p.t.Fatalf("%s: LineInfo\nref  %s\nport %s", when, a, b)
	}
	if a, b := r.View(), o.View(); a != b {
		p.t.Fatalf("%s: View\nref:\n%s\nport:\n%s\nref  %q\nport %q", when, a, b, a, b)
	}
}

func runes(s string) ev.KeyMsg    { return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune(s)} }
func alt(r rune) ev.KeyMsg        { return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune{r}, Alt: true} }
func k(t ev.KeyType) ev.KeyMsg    { return ev.KeyMsg{Type: t} }
func altK(t ev.KeyType) ev.KeyMsg { return ev.KeyMsg{Type: t, Alt: true} }
func paste(s string) ev.KeyMsg    { return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune(s), Paste: true} }

// alphabet is every default binding except ctrl+v (reads the real
// clipboard), plus text input of several scripts and widths.
var alphabet = []ev.KeyMsg{
	runes("a"), runes("Z"), runes(" "), runes("hello"), runes("日本"), runes("👍"),
	runes("é"), runes("Tiếng"), runes("\t"), runes("-"), runes("x"),
	paste("line one\nline two"), paste("日本語\n\n👩‍💻 x"), paste("a\r\nb"),
	k(ev.KeyEnter), k(ev.KeyCtrlM), k(ev.KeyBackspace), k(ev.KeyCtrlH), k(ev.KeyDelete), k(ev.KeyCtrlD),
	k(ev.KeyLeft), k(ev.KeyRight), k(ev.KeyCtrlB), k(ev.KeyCtrlF), k(ev.KeyUp), k(ev.KeyDown),
	k(ev.KeyCtrlP), k(ev.KeyCtrlN), k(ev.KeyHome), k(ev.KeyEnd), k(ev.KeyCtrlA), k(ev.KeyCtrlE),
	k(ev.KeyCtrlK), k(ev.KeyCtrlU), k(ev.KeyCtrlW), k(ev.KeyCtrlT), k(ev.KeyCtrlHome), k(ev.KeyCtrlEnd),
	altK(ev.KeyLeft), altK(ev.KeyRight), altK(ev.KeyBackspace), altK(ev.KeyDelete),
	alt('b'), alt('f'), alt('d'), alt('c'), alt('l'), alt('u'), alt('<'), alt('>'),
	k(ev.KeyEsc), k(ev.KeyTab), k(ev.KeySpace), k(ev.KeyPgUp),
}

func TestParityScripted(t *testing.T) {
	for _, app := range []bool{true, false} {
		for _, w := range []int{12, 24, 40, 80} {
			t.Run(fmt.Sprintf("app=%v/w=%d", app, w), func(t *testing.T) {
				p := newPair(t, app, w)
				for _, m := range []ev.KeyMsg{
					runes("hello world, this is a long line that must wrap around"),
					altK(ev.KeyLeft), altK(ev.KeyLeft), alt('u'), alt('c'), alt('l'),
					k(ev.KeyCtrlA), alt('f'), k(ev.KeyCtrlK), k(ev.KeyCtrlE),
					k(ev.KeyEnter), runes("日本語のテキスト👍👍 é mixed widths and more text"),
					k(ev.KeyUp), k(ev.KeyEnd), k(ev.KeyDown), k(ev.KeyCtrlW), altK(ev.KeyBackspace),
					paste("p1\np2\np3\np4"), k(ev.KeyCtrlHome), alt('d'), k(ev.KeyCtrlT),
					k(ev.KeyCtrlEnd), k(ev.KeyCtrlU), k(ev.KeyBackspace),
				} {
					p.key(m)
				}
			})
		}
	}
}

func TestParityFuzz(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		app := seed%2 == 0
		w := []int{10, 17, 30, 55, 90}[seed%5]
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			p := newPair(t, app, w)
			for i := 0; i < 250; i++ {
				p.key(alphabet[rng.Intn(len(alphabet))])
			}
		})
	}
}

// TestParityMethods covers the non-key API the app calls.
func TestParityMethods(t *testing.T) {
	p := newPair(t, true, 30)
	both := func(name string, f func(r *bta.Model, o *Model)) {
		t.Helper()
		f(&p.ref, &p.port)
		p.check(name)
	}
	both("SetValue", func(r *bta.Model, o *Model) {
		r.SetValue("first line\nsecond 日本 line that is long enough to wrap\nthird")
		o.SetValue("first line\nsecond 日本 line that is long enough to wrap\nthird")
	})
	both("CursorUp", func(r *bta.Model, o *Model) { r.CursorUp(); o.CursorUp() })
	both("SetCursor", func(r *bta.Model, o *Model) { r.SetCursor(5); o.SetCursor(5) })
	both("CursorStart", func(r *bta.Model, o *Model) { r.CursorStart(); o.CursorStart() })
	both("CursorEnd", func(r *bta.Model, o *Model) { r.CursorEnd(); o.CursorEnd() })
	both("CursorDown", func(r *bta.Model, o *Model) { r.CursorDown(); o.CursorDown() })
	both("InsertString", func(r *bta.Model, o *Model) { r.InsertString("ins👍"); o.InsertString("ins👍") })
	both("InsertRune", func(r *bta.Model, o *Model) { r.InsertRune('é'); o.InsertRune('é') })
	both("SetWidth", func(r *bta.Model, o *Model) { r.SetWidth(14); o.SetWidth(14) })
	both("SetHeight", func(r *bta.Model, o *Model) { r.SetHeight(5); o.SetHeight(5) })
	both("Blur", func(r *bta.Model, o *Model) { r.Blur(); o.Blur() })
	both("Focus", func(r *bta.Model, o *Model) { r.Focus(); o.Focus() })
	both("Reset", func(r *bta.Model, o *Model) { r.Reset(); o.Reset() })
	both("Placeholder blurred", func(r *bta.Model, o *Model) { r.Blur(); o.Blur() })
	both("Placeholder focused", func(r *bta.Model, o *Model) { r.Focus(); o.Focus() })
	both("CharLimit", func(r *bta.Model, o *Model) {
		r.CharLimit, o.CharLimit = 8, 8
		r.SetValue("0123456789abc")
		o.SetValue("0123456789abc")
	})
	both("MaxHeight", func(r *bta.Model, o *Model) {
		r.CharLimit, o.CharLimit = 0, 0
		r.MaxHeight, o.MaxHeight = 4, 4
		r.SetHeight(10)
		o.SetHeight(10)
		r.SetValue(strings.Repeat("row\n", 12))
		o.SetValue(strings.Repeat("row\n", 12))
	})
	both("LineNumbers", func(r *bta.Model, o *Model) {
		r.ShowLineNumbers, o.ShowLineNumbers = true, true
		r.SetWidth(25)
		o.SetWidth(25)
	})
	both("EndOfBuffer", func(r *bta.Model, o *Model) {
		r.EndOfBufferCharacter, o.EndOfBufferCharacter = '~', '~'
		r.SetValue("x")
		o.SetValue("x")
	})
}

// TestParityBlinkCycle runs one real blink tick (530 ms) through both and
// checks the cursor toggles to its "text" frame identically.
func TestParityBlinkCycle(t *testing.T) {
	ref, port := bta.New(), New()
	ref.SetWidth(20)
	port.SetWidth(20)
	ref.SetValue("blink")
	port.SetValue("blink")
	c1 := ref.Focus()
	c2 := port.Focus()
	if c1 == nil || c2 == nil {
		t.Fatalf("Focus cmds: ref %v port %v", c1 != nil, c2 != nil)
	}
	if ref.View() != port.View() {
		t.Fatal("focused views differ")
	}
	var m1 tea.Msg
	var m2 ev.Msg
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m1 = c1() }()
	go func() { defer wg.Done(); m2 = c2() }()
	wg.Wait()
	before := port.View()
	var n1 tea.Cmd
	var n2 ev.Cmd
	ref, n1 = ref.Update(m1)
	port, n2 = port.Update(m2)
	if n1 == nil || n2 == nil {
		t.Fatal("blink did not schedule the next tick")
	}
	if ref.View() != port.View() {
		t.Fatalf("blinked views differ:\n%q\n%q", ref.View(), port.View())
	}
	if port.View() == before || !port.Cursor.Blink || ref.Cursor.Blink != port.Cursor.Blink {
		t.Fatalf("cursor did not blink (ref %v, port %v)", ref.Cursor.Blink, port.Cursor.Blink)
	}
	if !strings.Contains(before, "\x1b[7m") {
		t.Fatalf("focused cursor not reversed: %q", before)
	}
	// A stale tick (the pre-blink message again) is ignored by both.
	ref, _ = ref.Update(m1)
	port, _ = port.Update(m2)
	if ref.View() != port.View() {
		t.Fatal("stale tick handled differently")
	}
}

func TestMouseWheelScrollsLikeBubbles(t *testing.T) {
	p := newPair(t, true, 20)
	p.ref.SetValue(strings.Repeat("line\n", 10))
	p.port.SetValue(strings.Repeat("line\n", 10))
	p.check("filled")
	p.ref.CursorStart()
	p.port.CursorStart()
	for _, b := range []ev.MouseButton{ev.MouseButtonWheelUp, ev.MouseButtonWheelUp, ev.MouseButtonWheelDown} {
		m := ev.MouseMsg{Button: b, Action: ev.MouseActionPress, X: 1, Y: 1}
		p.ref, _ = p.ref.Update(teabridge.ToTea(m))
		p.port, _ = p.port.Update(m)
		p.check(fmt.Sprintf("wheel %v", b))
	}
}

// TestWordLeftNoHang pins the fix for upstream's infinite loop: word-left
// with only whitespace (or an empty first line) before the cursor stops at
// the start of the buffer.
func TestWordLeftNoHang(t *testing.T) {
	for _, v := range []string{"", "   ", "   hello", "\nfoo", "\n\n  x", " \t "} {
		for _, key := range []ev.KeyMsg{altK(ev.KeyLeft), alt('b')} {
			m := New()
			m.Focus()
			m.SetWidth(30)
			m.SetValue(v)
			// Put the cursor just after the leading whitespace / blank rows.
			m.row, m.col = 0, 0
			for m.row < len(m.value)-1 && strings.TrimSpace(string(m.value[m.row])) == "" {
				m.row++
			}
			for m.col < len(m.value[m.row]) && unicode.IsSpace(m.value[m.row][m.col]) {
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
