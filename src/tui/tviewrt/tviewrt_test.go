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

type inputModeScreen struct {
	tcell.SimulationScreen
	mouse, paste bool
}

func (s *inputModeScreen) EnableMouse(...tcell.MouseFlags) { s.mouse = true }
func (s *inputModeScreen) EnablePaste()                    { s.paste = true }

func TestInputModesOnSuppliedScreen(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		s := &inputModeScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
		r := New(mouseModel{}, Options{Screen: s, Mouse: mouse})
		done := make(chan error, 1)
		go func() { _, err := r.Run(); done <- err }()
		r.Send(ev.QuitMsg{})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if s.mouse != mouse || !s.paste {
			t.Errorf("mouse=%v: screen modes mouse=%v paste=%v", mouse, s.mouse, s.paste)
		}
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

type menuModel struct{ picked string }

func (menuModel) Init() ev.Cmd { return nil }
func (m menuModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	switch x := msg.(type) {
	case ev.KeyMsg:
		return m, func() ev.Msg {
			return ev.OpenMenuMsg{Tag: "t", X: 2, Y: 1, Items: []ev.MenuItem{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}}
		}
	case ev.MenuResultMsg:
		m.picked = x.Tag + "/" + x.ID
	}
	return m, nil
}
func (m menuModel) View() string { return "base " + m.picked }

func TestNativeMenuRoundTrip(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	var tr keys.MouseTracker
	r := New(menuModel{}, Options{Screen: scr, Mouse: true, Key: key, MouseE: tr.FromMouse})
	scr.SetSize(40, 10)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	waitFor(t, func() bool { return strings.HasPrefix(rows(r, scr)[0], "base") })
	scr.InjectKey(tcell.KeyRune, 'm', tcell.ModNone)
	// The menu is drawn by the overlay at (2,1); find the "Beta" row.
	var by, bx int
	waitFor(t, func() bool {
		for y, row := range rows(r, scr) {
			if i := strings.Index(row, "Beta"); i >= 0 {
				by, bx = y, len([]rune(row[:i]))
				return true
			}
		}
		return false
	})
	// Keys go to the open menu, not the model: ↓ moves, it doesn't reopen.
	time.Sleep(30 * time.Millisecond)
	scr.InjectMouse(bx, by, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(bx, by, tcell.ButtonNone, tcell.ModNone)
	waitFor(t, func() bool { return r.Model().(menuModel).picked == "t/b" })
	waitFor(t, func() bool { return !strings.Contains(strings.Join(rows(r, scr), "\n"), "Alpha") })
}

type barModel struct {
	h        int
	picked   string
	clicks   []string
	releases []string
}

func (barModel) Init() ev.Cmd { return nil }
func (m barModel) Update(msg ev.Msg) (ev.Model, ev.Cmd) {
	switch x := msg.(type) {
	case ev.WindowSizeMsg:
		m.h = x.Height
	case ev.MenuResultMsg:
		m.picked = x.Tag + "/" + x.ID
	case ev.MouseMsg:
		if x.Action == ev.MouseActionPress {
			m.clicks = append(m.clicks, fmt.Sprintf("%d,%d", x.X, x.Y))
		} else if x.Action == ev.MouseActionRelease {
			m.releases = append(m.releases, fmt.Sprintf("%d,%d", x.X, x.Y))
		}
	}
	return m, nil
}
func (m barModel) View() string { return fmt.Sprintf("frame h=%d", m.h) }
func (barModel) MenuBar() []ev.Menu {
	return []ev.Menu{
		{Title: "File", Items: []ev.MenuItem{{ID: "new", Label: "New"}, {ID: "quit", Label: "Quit"}}},
		{Title: "Edit", Items: []ev.MenuItem{{ID: "paste", Label: "Paste"}}},
	}
}

// A MenuBarProvider gets a bar on row 0; the model sees a frame one row
// shorter and frame-relative mouse coordinates; a bar pick comes back as
// MenuResultMsg{MenuBarTag}.
func TestMenuBar(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	var tr keys.MouseTracker
	r := New(barModel{}, Options{Screen: scr, Mouse: true, Key: key, MouseE: tr.FromMouse})
	scr.SetSize(40, 10)
	go func() { _, _ = r.Run() }()
	defer r.Stop()
	waitFor(t, func() bool {
		rs := rows(r, scr)
		return strings.HasPrefix(rs[0], " File  Edit") && rs[1] == "frame h=9"
	})
	time.Sleep(30 * time.Millisecond)
	scr.InjectMouse(5, 4, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(5, 0, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(5, 0, tcell.ButtonNone, tcell.ModNone)
	waitFor(t, func() bool {
		m := r.Model().(barModel)
		return len(m.clicks) == 1 && m.clicks[0] == "5,3" && len(m.releases) == 1 && m.releases[0] == "5,-1"
	})
	// Click "Edit" → its menu drops below; click "Paste".
	scr.InjectMouse(8, 0, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(8, 0, tcell.ButtonNone, tcell.ModNone)
	var py, px int
	waitFor(t, func() bool {
		for y, row := range rows(r, scr) {
			if i := strings.Index(row, "Paste"); i >= 0 && y > 0 {
				py, px = y, len([]rune(row[:i]))
				return true
			}
		}
		return false
	})
	time.Sleep(30 * time.Millisecond)
	scr.InjectMouse(px, py, tcell.Button1, tcell.ModNone)
	scr.InjectMouse(px, py, tcell.ButtonNone, tcell.ModNone)
	waitFor(t, func() bool { return r.Model().(barModel).picked == ev.MenuBarTag+"/paste" })
	if c := r.Model().(barModel).clicks; len(c) != 1 {
		t.Fatalf("bar/menu clicks leaked to the model: %v", c)
	}
}

// Ctrl+C while a menu is open closes the menu; it must not stop the app
// (tview's default for an uncaptured Ctrl+C).
func TestCtrlCInMenuDoesNotQuit(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	r := New(menuModel{}, Options{Screen: scr, Mouse: true, Key: key})
	scr.SetSize(40, 10)
	done := make(chan struct{})
	go func() { _, _ = r.Run(); close(done) }()
	defer r.Stop()
	waitFor(t, func() bool { return strings.HasPrefix(rows(r, scr)[0], "base") })
	scr.InjectKey(tcell.KeyRune, 'm', tcell.ModNone)
	waitFor(t, func() bool { return strings.Contains(strings.Join(rows(r, scr), "\n"), "Alpha") })
	scr.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
	waitFor(t, func() bool { return !strings.Contains(strings.Join(rows(r, scr), "\n"), "Alpha") })
	select {
	case <-done:
		t.Fatal("Ctrl+C in a menu quit the app")
	case <-time.After(100 * time.Millisecond):
	}
}
