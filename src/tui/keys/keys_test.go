package keys

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
)

func tcellParse(seq string) []string {
	ch := make(chan tcell.Event, 64)
	ip := tcell.NewInputProcessor(ch)
	ip.ScanUTF8([]byte(seq))
	keys := make(chan string, 64)
	go func() {
		deadline := time.After(150 * time.Millisecond) // > tcell's lone-ESC timeout
		for {
			select {
			case ev := <-ch:
				switch e := ev.(type) {
				case *tcell.EventKey:
					if s := KeyString(e); s != "" {
						keys <- s
					}
				}
			case <-deadline:
				close(keys)
				return
			}
		}
	}()
	var out []string
	for k := range keys {
		out = append(out, k)
	}
	return out
}

func drain[T any](ch chan T, f func(T) string) []string {
	var out []string
	quiet := 40 * time.Millisecond
	for {
		select {
		case v := <-ch:
			out = append(out, f(v))
		case <-time.After(quiet):
			return out
		}
	}
}

// The fixture records the previous parser parity corpus, including the
// intentional differences for legacy terminals. No old runtime is needed.
func TestLegacyTerminalKeys(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-keys.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]string
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for seq, want := range cases {
		t.Run(seq, func(t *testing.T) {
			t.Parallel()
			if got := strings.Join(tcellParse(seq), " "); got != want {
				t.Errorf("%q: got %q, want %q", seq, got, want)
			}
		})
	}
}

// TestExtendedProtocols: tcell enables modifyOtherKeys(2) + kitty CSI-u(1)
// on xterm-like terminals, so these are the bytes a modern terminal really
// sends once tcell is in charge. want = what tea named the same physical
// keypress on a legacy terminal ("" = tea delivered no key).
func TestExtendedProtocols(t *testing.T) {
	cases := []struct{ seq, want string }{
		{"\x1b[104;5u", "ctrl+h"}, // distinct from backspace again
		{"\x1b[127u", "backspace"},
		{"\x1b[105;5u", "tab"}, // ctrl+i: legacy sent HT
		{"\x1b[109;5u", "enter"},
		{"\x1b[106;5u", "ctrl+j"},
		{"\x1b[13;2u", "enter"}, // shift+enter: legacy sent CR
		{"\x1b[13;5u", "enter"},
		{"\x1b[13;3u", "alt+enter"},
		{"\x1b[27u", "esc"},
		{"\x1b[97;3u", "alt+a"},
		{"\x1b[97;7u", "alt+ctrl+a"},
		{"\x1b[120;3u", "alt+x"},
		{"\x1b[49;3u", "alt+1"},
		{"\x1b[91;3u", "alt+["}, // the legacy gap closes under CSI-u
		{"\x1b[99;5u", "ctrl+c"},
		{"\x1b[100;5u", "ctrl+d"},
		{"\x1b[32;5u", "ctrl+@"},
		{"\x1b[47;5u", "ctrl+_"},
		{"\x1b[49;5u", "1"}, // ctrl+1: legacy sent a bare '1'
		{"\x1b[127;5u", "ctrl+h"},
		{"\x1b[9;2u", "shift+tab"},
		{"\x1b[27;5;106~", "ctrl+j"}, // modifyOtherKeys
		{"\x1b[27;2;13~", "enter"},
		{"\x1b[27;3;120~", "alt+x"},
		{"\x1b[27;5;104~", "ctrl+h"},
	}
	for _, c := range cases {
		ch := make(chan tcell.Event, 8)
		tcell.NewInputProcessor(ch).ScanUTF8([]byte(c.seq))
		select {
		case e := <-ch:
			if got := KeyString(e.(*tcell.EventKey)); got != c.want {
				t.Errorf("%q: got %q, want %q", c.seq, got, c.want)
			}
		case <-time.After(200 * time.Millisecond):
			t.Errorf("%q: tcell produced no key event", c.seq)
		}
	}
}

// TestAppKeyVocabulary: every key string literal and tea.Key* type that
// src/app + src/builtin dispatch on (grep of feat/tview-migration @c4051b2)
// must come out of FromKey with tea's exact String().
func TestAppKeyVocabulary(t *testing.T) {
	ek := tcell.NewEventKey
	r := func(c rune, m tcell.ModMask) *tcell.EventKey { return ek(tcell.KeyRune, c, m) }
	alt, ctrl, shift := tcell.ModAlt, tcell.ModCtrl, tcell.ModShift
	cases := []struct {
		e    *tcell.EventKey
		want ev.KeyType
	}{
		{ek(tcell.KeyEsc, 0, 0), ev.KeyEsc}, {ek(tcell.KeyEnter, 0, 0), ev.KeyEnter},
		{ek(tcell.KeyTab, 0, 0), ev.KeyTab}, {ek(tcell.KeyBacktab, 0, 0), ev.KeyShiftTab},
		{ek(tcell.KeyBackspace2, 0, 0), ev.KeyBackspace}, {ek(tcell.KeyBackspace, 0, 0), ev.KeyBackspace},
		{ek(tcell.KeyDelete, 0, 0), ev.KeyDelete}, {r(' ', 0), ev.KeySpace}, {r('q', 0), ev.KeyRunes},
		{ek(tcell.KeyUp, 0, 0), ev.KeyUp}, {ek(tcell.KeyDown, 0, 0), ev.KeyDown},
		{ek(tcell.KeyLeft, 0, 0), ev.KeyLeft}, {ek(tcell.KeyRight, 0, 0), ev.KeyRight},
		{ek(tcell.KeyHome, 0, 0), ev.KeyHome}, {ek(tcell.KeyEnd, 0, 0), ev.KeyEnd},
		{ek(tcell.KeyPgUp, 0, 0), ev.KeyPgUp}, {ek(tcell.KeyPgDn, 0, 0), ev.KeyPgDown},
		{ek(tcell.KeyPgUp, 0, ctrl), ev.KeyCtrlPgUp}, {ek(tcell.KeyPgDn, 0, ctrl), ev.KeyCtrlPgDown},
		{ek(tcell.KeyUp, 0, ctrl), ev.KeyCtrlUp}, {ek(tcell.KeyDown, 0, ctrl), ev.KeyCtrlDown},
		{ek(tcell.KeyLeft, 0, ctrl), ev.KeyCtrlLeft}, {ek(tcell.KeyRight, 0, ctrl), ev.KeyCtrlRight},
		{ek(tcell.KeyHome, 0, ctrl), ev.KeyCtrlHome}, {ek(tcell.KeyEnd, 0, ctrl), ev.KeyCtrlEnd},
		{ek(tcell.KeyUp, 0, shift), ev.KeyShiftUp}, {ek(tcell.KeyDown, 0, shift), ev.KeyShiftDown},
	}
	for c := 'a'; c <= 'z'; c++ { // tea.KeyCtrlA … KeyCtrlZ, both tcell spellings
		cases = append(cases,
			struct {
				e    *tcell.EventKey
				want ev.KeyType
			}{ek(tcell.KeyCtrlA+tcell.Key(c-'a'), c, ctrl), ev.KeyCtrlA + ev.KeyType(c-'a')},
			struct {
				e    *tcell.EventKey
				want ev.KeyType
			}{r(c, ctrl|alt), ev.KeyCtrlA + ev.KeyType(c-'a')})
	}
	for _, c := range cases {
		k, ok := FromKey(c.e)
		if !ok || k.Type != c.want {
			t.Errorf("%s: got %q (type %d ok=%v), want type %d", c.e.Name(), k.String(), k.Type, ok, c.want)
		}
	}

	// String literals compared against KeyMsg.String() in src/app/builtin.
	lits := map[string]*tcell.EventKey{
		"alt+[": r('[', alt), "alt+1": r('1', alt), "alt+3": r('3', alt), "alt+5": r('5', alt),
		"alt+9": r('9', alt), "alt+a": r('a', alt), "alt+b": r('b', alt), "alt+c": r('c', alt),
		"alt+e": r('e', alt), "alt+g": r('g', alt), "alt+m": r('m', alt), "alt+x": r('x', alt),
		"alt+up": ek(tcell.KeyUp, 0, alt), "backspace": ek(tcell.KeyBackspace2, 0, 0),
		"ctrl+a": ek(tcell.KeyCtrlA, 'a', ctrl), "ctrl+d": ek(tcell.KeyCtrlD, 'd', ctrl),
		"ctrl+g": ek(tcell.KeyCtrlG, 'g', ctrl), "ctrl+j": ek(tcell.KeyCtrlJ, 'j', ctrl),
		"ctrl+r": ek(tcell.KeyCtrlR, 'r', ctrl), "ctrl+s": ek(tcell.KeyCtrlS, 's', ctrl),
		"down": ek(tcell.KeyDown, 0, 0), "end": ek(tcell.KeyEnd, 0, 0),
		"enter": ek(tcell.KeyEnter, 0, 0), "esc": ek(tcell.KeyEsc, 0, 0),
		"home": ek(tcell.KeyHome, 0, 0), "left": ek(tcell.KeyLeft, 0, 0),
		"pgdown": ek(tcell.KeyPgDn, 0, 0), "pgup": ek(tcell.KeyPgUp, 0, 0),
		"right": ek(tcell.KeyRight, 0, 0), "up": ek(tcell.KeyUp, 0, 0), "?": r('?', 0),
		"alt+enter": ek(tcell.KeyEnter, 0, alt), "alt+ ": r(' ', alt),
	}
	for want, e := range lits {
		if got := KeyString(e); got != want {
			t.Errorf("%s: got %q, want %q", e.Name(), got, want)
		}
	}
	// mcp.go's McpKeySpace = "space" is a display label, never a String()
	// result: tea (and FromKey) report space as " ".
	if KeyString(r(' ', 0)) != " " {
		t.Error(`space must stringify as " " like tea`)
	}
}
