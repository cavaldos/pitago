package tviewrt

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
	"pitago/src/tui/keys"
)

type pingMsg struct{ n int }

// counter is a tiny ev.Model: it records sizes and keys and can quit.
type counter struct {
	w, h  int
	keys  []string
	pings int
}

func (c counter) Init() ev.Cmd {
	return ev.Batch(
		func() ev.Msg { return pingMsg{1} },
		func() ev.Msg { return pingMsg{2} },
	)
}

func (c counter) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	switch m := msg.(type) {
	case ev.WindowSizeMsg:
		c.w, c.h = m.Width, m.Height
	case pingMsg:
		c.pings++
	case ev.KeyMsg:
		c.keys = append(c.keys, m.String())
		if m.String() == "q" {
			return c, ev.Sequence(func() ev.Msg { return pingMsg{3} }, ev.Quit)
		}
	}
	return c, nil
}

func (c counter) View() string {
	return fmt.Sprintf("\x1b[1msize %dx%d\x1b[0m [x] pings=%d\nkeys=%s", c.w, c.h, c.pings, strings.Join(c.keys, ","))
}

func key(e *tcell.EventKey) (ev.KeyMsg, bool) {
	if e.Key() == tcell.KeyRune {
		return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune{e.Rune()}}, true
	}
	return ev.KeyMsg{}, false
}

// snap reads the simulated screen on the event loop (tview draws there).
func snap(r *Runtime, s tcell.SimulationScreen) ([]tcell.SimCell, int, int) {
	type res struct {
		c    []tcell.SimCell
		w, h int
	}
	ch := make(chan res, 1)
	r.App().QueueUpdate(func() { c, w, h := s.GetContents(); ch <- res{append([]tcell.SimCell(nil), c...), w, h} })
	x := <-ch
	return x.c, x.w, x.h
}

func rows(r *Runtime, s tcell.SimulationScreen) []string {
	cells, w, h := snap(r, s)
	out := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(string(c.Runes))
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRuntimeLifecycle(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	r := New(counter{}, Options{Screen: scr, Key: key}) // SetScreen calls Init
	scr.SetSize(40, 5)
	done := make(chan ev.Model, 1)
	go func() {
		m, err := r.Run()
		if err != nil {
			t.Error(err)
		}
		done <- m
	}()

	waitFor(t, func() bool { c := r.Model().(counter); return c.pings == 2 && c.w == 40 })
	r.Send(pingMsg{9})
	waitFor(t, func() bool { return r.Model().(counter).pings == 3 })

	// Draw is asynchronous; wait for the painted frame.
	waitFor(t, func() bool { return rows(r, scr)[0] == "size 40x5 [x] pings=3" })
	cells, _, _ := snap(r, scr)
	if _, _, attr := cells[0].Style.Decompose(); attr&tcell.AttrBold == 0 {
		t.Error("SGR bold lost")
	}

	scr.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
	waitFor(t, func() bool { return strings.HasPrefix(rows(r, scr)[1], "keys=a") })

	scr.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case m := <-done:
		c := m.(counter)
		if c.pings != 4 || strings.Join(c.keys, ",") != "a,q" {
			t.Fatalf("final model = %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quit via Sequence(…, Quit) did not stop the runtime")
	}
}

type imgModel struct{}

func (imgModel) Init() ev.Cmd                       { return nil }
func (m imgModel) Update(ev.Msg) (ev.Model, ev.Cmd) { return m, nil }
func (imgModel) View() string {
	return "\x1b_Ga=T,i=42;AAAA\x1b\\before\x1b[38;5;42m\U0010EEEE̅\x1b[0m after"
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestRawPassthrough(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	var out syncBuf
	r := New(imgModel{}, Options{Screen: scr, RawOut: &out})
	scr.SetSize(30, 2)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	waitFor(t, func() bool { return strings.Contains(out.String(), "\x1b_Ga=T,i=42;AAAA\x1b\\") })
	waitFor(t, func() bool { return strings.HasPrefix(rows(r, scr)[0], "before") })
	// Raw writes made by the app (app.ImageOut) take the same path.
	_, _ = r.Raw().Write([]byte("\x1b_Gq=2\x1b\\"))
	r.Send(nil)
	r.App().QueueUpdateDraw(func() {})
	waitFor(t, func() bool { return strings.Contains(out.String(), "q=2") })
}

type mouseModel struct{ got []string }

func (mouseModel) Init() ev.Cmd { return nil }
func (m mouseModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	if mm, ok := msg.(ev.MouseMsg); ok {
		m.got = append(m.got, fmt.Sprintf("%s@%d,%d", mm.String(), mm.X, mm.Y))
	}
	return m, nil
}
func (m mouseModel) View() string { return strings.Join(m.got, " ") }

func TestMouseDragReachesModel(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	var tr keys.MouseTracker
	r := New(mouseModel{}, Options{Screen: scr, Mouse: true, MouseE: tr.FromMouse})
	scr.SetSize(40, 4)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	waitFor(t, func() bool { return r.Model() != nil && func() bool { _, h := scr.Size(); return h == 4 }() })
	time.Sleep(50 * time.Millisecond) // let the first resize reach tcell's input processor
	scr.InjectMouse(3, 1, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(6, 2, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(6, 2, tcell.ButtonNone, tcell.ModNone)
	waitFor(t, func() bool { return len(r.Model().(mouseModel).got) >= 3 })
	got := strings.Join(r.Model().(mouseModel).got, " ")
	for _, want := range []string{"left press@3,1", "left motion@6,2", "release@6,2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("mouse stream %q lacks %q", got, want)
		}
	}
}
