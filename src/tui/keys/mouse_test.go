package keys

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/tui/ev"
)

type mouseRecorder struct{ out chan tea.MouseMsg }

func (r mouseRecorder) Init() tea.Cmd { return nil }
func (r mouseRecorder) View() string  { return "" }
func (r mouseRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m, ok := msg.(tea.MouseMsg); ok {
		r.out <- m
	}
	return r, nil
}

func teaMouse(t *testing.T, reports []string) []string {
	r, w := io.Pipe()
	out := make(chan tea.MouseMsg, 256)
	p := tea.NewProgram(mouseRecorder{out}, tea.WithInput(r), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler())
	go func() { _, _ = p.Run() }()
	defer func() { p.Kill(); _ = w.Close() }()
	var got []string
	for _, rep := range reports {
		_, _ = w.Write([]byte(rep))
		got = append(got, drain(out, fmtTea)...)
	}
	return got
}

func fmtTea(m tea.MouseMsg) string {
	return fmt.Sprintf("%d,%d %s type=%d", m.X, m.Y, tea.MouseEvent(m).String(), m.Type)
}

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
	app := tview.NewApplication().SetScreen(sim).EnableMouse(true).
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

func TestMouseParityWithBubbleTea(t *testing.T) {
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
	want := teaMouse(t, reports)
	got := tviewMouse(t, reports)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("mouse stream differs\n--- tea (%d)\n%s\n--- ours (%d)\n%s",
			len(want), strings.Join(want, "\n"), len(got), strings.Join(got, "\n"))
	}
}

// TestMouseKnownGaps documents where tview's derived actions cannot match
// tea. Each case asserts the current (wrong) output so a tview fix shows up.
func TestMouseKnownGaps(t *testing.T) {
	// Wheel during a held drag: tcell's wheel report carries only the wheel
	// bit, so tview sees the held button vanish and fires LeftUp, then a
	// LeftDown on the next drag report. tea: press, wheel, motion, release.
	reports := []string{"\x1b[<0;10;5M", "\x1b[<64;10;5M", "\x1b[<32;11;5M", "\x1b[<0;11;5m"}
	want := teaMouse(t, reports)
	got := tviewMouse(t, reports)
	if len(want) != 4 {
		t.Fatalf("tea baseline changed: %v", want)
	}
	if strings.Join(got, "|") == strings.Join(want, "|") {
		t.Errorf("wheel-during-drag now matches tea: drop this gap")
	}
	t.Logf("wheel during drag\n tea:  %v\n ours: %v", want, got)
}

// TestNoSplitReportLeak is the tcell side of src/app/mouseleak.go: tea v1
// could split an SGR report across reads and deliver the tail as KeyRunes
// ("[<65;99;18M" typed into the textarea). tcell's parser keeps partial
// sequences buffered, so bursts and split reads yield only mouse events.
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
