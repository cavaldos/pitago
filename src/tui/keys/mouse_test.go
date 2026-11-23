package keys

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/tui/ev"
)

func fmtOurs(m ev.MouseMsg) string {
	return fmt.Sprintf("%d,%d %s type=%d", m.X, m.Y, ev.MouseEvent(m).String(), m.Type)
}

// tviewMouse runs the reports through tcell's real parser into a real
// tview.Application (simulation screen) and maps what SetMouseCapture sees.
func tviewMouse(t *testing.T, reports []string) []string {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(200, 60)
	app := tview.NewApplication().SetScreen(NewPasteScreen(sim)).EnableMouse(true).
		SetRoot(tview.NewBox(), true)
	out := make(chan ev.MouseMsg, 256)
	var tr MouseTracker
	app.SetMouseCapture(func(e *tcell.EventMouse, a tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		for _, m := range tr.FromMouse(e, a) {
			out <- m
		}
		return e, a // pass through: a nil here also blanks later actions of the same report
	})
	done := make(chan struct{})
	go func() { _ = app.Run(); close(done) }()
	defer func() { app.Stop(); <-done }()
	time.Sleep(50 * time.Millisecond) // let Run install the screen

	evs := make(chan tcell.Event, 256)
	ip := sizedProcessor(t, evs)
	var got []string
	for _, rep := range reports {
		ip.ScanUTF8([]byte(rep))
		for len(evs) > 0 {
			_ = sim.PostEvent(<-evs)
		}
		got = append(got, drain(out, fmtOurs)...)
	}
	return got
}

// sizedProcessor returns a tcell input parser that knows the screen size.
// SetSize applies asynchronously (a goroutine that then posts a resize
// event), and until it lands every mouse coordinate clamps to 0,0 — the real
// screen does the same at startup, so tviewrt should not trust mouse
// positions before its first EventResize.
func sizedProcessor(t *testing.T, evs chan tcell.Event) tcell.InputProcessor {
	ip := tcell.NewInputProcessor(evs)
	ip.SetSize(200, 60)
	select {
	case e := <-evs:
		if _, ok := e.(*tcell.EventResize); !ok {
			t.Fatalf("want resize first, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no resize event")
	}
	return ip
}

func TestTerminalMouseReports(t *testing.T) {
	reports := []string{
		"\x1b[<0;10;5M", "\x1b[<32;11;5M", "\x1b[<32;12;6M", "\x1b[<0;12;6m", // drag-select
		"\x1b[<35;13;6M", "\x1b[<35;14;6M", // hover (all-motion, no button)
		"\x1b[<64;14;6M", "\x1b[<65;14;6M", "\x1b[<68;14;6M", "\x1b[<81;14;6M", // wheel, shift/ctrl+wheel
		"\x1b[<66;14;6M", "\x1b[<67;14;6M", // horizontal wheel
		"\x1b[<2;20;7M", "\x1b[<2;20;7m", // right click
		"\x1b[<1;21;7M", "\x1b[<1;21;7m", // middle click
		"\x1b[<16;5;5M", "\x1b[<16;5;5m", // ctrl+click
		"\x1b[<8;6;5M", "\x1b[<40;7;5M", "\x1b[<8;7;5m", // alt+drag
		"\x1b[<0;30;9M", "\x1b[<0;30;9m", // click (tview adds Click: dropped)
		"\x1b[<0;30;9M", "\x1b[<0;30;9m", // double click
		"\x1b[<0;1;1M", "\x1b[<0;1;1m", // origin → 0,0
		"\x1b[<0;200;60M", "\x1b[<32;199;60M", "\x1b[<0;199;60m", // far corner
	}
	want := []string{
		"9,4 left press type=1", "10,4 left motion type=1", "11,5 left motion type=1", "11,5 left release type=4",
		"12,5 motion type=11", "13,5 motion type=11",
		"13,5 wheel up type=5", "13,5 wheel down type=6", "13,5 shift+wheel up type=5", "13,5 ctrl+wheel down type=6",
		"13,5 wheel left type=7", "13,5 wheel right type=8",
		"19,6 right press type=2", "19,6 right release type=4", "20,6 middle press type=3", "20,6 middle release type=4",
		"4,4 ctrl+left press type=1", "4,4 ctrl+left release type=4",
		"5,4 alt+left press type=1", "6,4 alt+left motion type=1", "6,4 alt+left release type=4",
		"29,8 left press type=1", "29,8 left release type=4", "29,8 left press type=1", "29,8 left release type=4",
		"0,0 left press type=1", "0,0 left release type=4",
		"199,59 left press type=1", "198,59 left motion type=1", "198,59 left release type=4",
	}
	if got := tviewMouse(t, reports); !slices.Equal(got, want) {
		t.Errorf("mouse stream:\ngot  %q\nwant %q", got, want)
	}
}

func TestWheelDoesNotEndDrag(t *testing.T) {
	reports := []string{"\x1b[<0;10;5M", "\x1b[<64;10;5M", "\x1b[<32;11;5M", "\x1b[<65;11;5M", "\x1b[<0;11;5m"}
	want := []string{"9,4 left press type=1", "9,4 wheel up type=5", "10,4 left motion type=1", "10,4 wheel down type=6", "10,4 left release type=4"}
	if got := tviewMouse(t, reports); !slices.Equal(got, want) {
		t.Errorf("drag stream:\ngot  %q\nwant %q", got, want)
	}
}

// tcell keeps partial SGR reports buffered instead of leaking their bytes
// into the input editor as key events.
func TestNoSplitReportLeak(t *testing.T) {
	evs := make(chan tcell.Event, 1024)
	ip := sizedProcessor(t, evs)

	var burst strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&burst, "\x1b[<%d;%d;%dM", 64+i%2, 10+i%50, 5+i%20)
	}
	b := []byte(burst.String())
	// Feed in awkward chunks, splitting reports mid-sequence (and right after
	// ESC, the case that produced tea's lone alt+[ ).
	for i := 0; i < len(b); {
		n := 1 + (i*7)%13
		if i+n > len(b) {
			n = len(b) - i
		}
		ip.ScanUTF8(b[i : i+n])
		i += n
	}
	// A report split across a pause shorter than tcell's ESC timeout.
	ip.ScanUTF8([]byte("\x1b"))
	time.Sleep(10 * time.Millisecond)
	ip.ScanUTF8([]byte("[<0;3;4M"))
	time.Sleep(150 * time.Millisecond)

	mice, keys := 0, 0
	for len(evs) > 0 {
		switch e := (<-evs).(type) {
		case *tcell.EventMouse:
			mice++
		case *tcell.EventKey:
			keys++
			t.Errorf("leaked key: %s", e.Name())
		}
	}
	if mice != 201 || keys != 0 {
		t.Errorf("got %d mouse / %d key events, want 201 / 0", mice, keys)
	}
}
