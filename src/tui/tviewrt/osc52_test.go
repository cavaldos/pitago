package tviewrt

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"pitago/src/components/clipboard"
	"pitago/src/tui/ev"
	"pitago/src/tui/internal/ttytest"
	"pitago/src/tui/keys"
)

// copyModel copies on "y" from Update — on the UI goroutine, like every
// copy site in src/app — and repaints a busy frame on every key.
type copyModel struct {
	n      int
	status clipboard.Channel
}

func (copyModel) Init() ev.Cmd { return nil }
func (m copyModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if k, ok := msg.(ev.KeyMsg); ok {
		m.n++
		if k.String() == "y" {
			m.status = clipboard.Write("copied ✓ text").Channel
		}
	}
	return m, nil
}
func (m copyModel) View() string {
	return strings.Repeat("\x1b[1;38;5;33mframe ✓\x1b[0m ", m.n%5+1) + "\n" + strings.Repeat("─", m.n%20)
}

// TestOsc52ThroughRaw: with the clipboard's OSC 52 writer set to the
// runtime's Raw side channel, a copy made in Update reaches the real tty
// (tcell terminfo screen over a captured tty) intact, once, in a write of
// its own between frames.
func TestOsc52ThroughRaw(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "hosted") // forces the OSC 52 transport
	tty := ttytest.NewTty(40, 6)
	scr := ttytest.Uninitialized(t, tty, "xterm-kitty")
	r := New(copyModel{}, Options{Screen: scr, Key: keys.FromKey})
	defer clipboard.SetOsc52Writer(r.Raw())()
	done := make(chan struct{})
	go func() { _, _ = r.Run(); close(done) }()
	defer func() { r.Stop(); <-done }()

	for i, c := range "abyabcyab" {
		for scr.PostEvent(tcell.NewEventKey(tcell.KeyRune, c, tcell.ModNone)) != nil {
			time.Sleep(time.Millisecond)
		}
		want := i + 1
		waitFor(t, func() bool { return r.Model().(copyModel).n == want })
	}
	waitFor(t, func() bool { return r.Model().(copyModel).status == clipboard.Osc52 })
	r.App().QueueUpdateDraw(func() {})

	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("copied ✓ text")) + "\x07"
	out := tty.Bytes()
	if n := bytes.Count(out, []byte(want)); n != 2 {
		t.Fatalf("OSC 52 sequence appears %d times, want 2 (two copies)", n)
	}
	d := ttytest.Decode(out)
	if len(d.Malformed) > 0 {
		t.Fatalf("malformed sequences: %v", d.Malformed)
	}
	for _, w := range tty.Writes() {
		if bytes.Contains(w, []byte("\x1b]52;")) && !bytes.Equal(w, []byte(want)) {
			t.Errorf("OSC 52 shared a write with other output: %q", w[:min(60, len(w))])
		}
	}
}
