package tviewrt

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
	"pitago/src/tui/keys"
)

type pasteModel struct{ got []ev.KeyMsg }

func (pasteModel) Init() ev.Cmd { return nil }
func (m pasteModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if k, ok := msg.(ev.KeyMsg); ok {
		m.got = append(m.got, k)
	}
	return m, nil
}
func (pasteModel) View() string { return "" }

// TestPasteExactThroughRuntime: bytes from tcell's real parser, through the
// runtime's wrapped screen and tview, reach the model as Bubble Tea's one
// paste KeyMsg with LF, CR and C0 bytes intact, in order with typed keys.
func TestPasteExactThroughRuntime(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	r := New(pasteModel{}, Options{Screen: scr, Key: keys.FromKey})
	scr.SetSize(20, 4)
	go func() { _, _ = r.Run() }()
	defer r.Stop()

	evs := make(chan tcell.Event, 256)
	ip := tcell.NewInputProcessor(evs)
	ip.ScanUTF8([]byte("a\x1b[200~x\ny\r\n\tz\x01\x1b[201~b"))
	deadline := time.After(2 * time.Second)
	for posted := 0; ; {
		select {
		case e := <-evs:
			if _, ok := e.(*tcell.EventResize); ok {
				continue
			}
			for scr.PostEvent(e) != nil {
				time.Sleep(time.Millisecond)
			}
			posted++
			continue
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("parser stalled")
		}
		if posted > 0 {
			break
		}
	}
	waitFor(t, func() bool { return len(r.Model().(pasteModel).got) == 3 })
	got := r.Model().(pasteModel).got
	if got[0].String() != "a" || got[2].String() != "b" {
		t.Fatalf("typed keys around the paste: %q, %q", got[0].String(), got[2].String())
	}
	p := got[1]
	if !p.Paste || p.Type != ev.KeyRunes || string(p.Runes) != "x\ny\r\n\tz\x01" {
		t.Fatalf("paste = %+v (%q)", p, string(p.Runes))
	}
}
