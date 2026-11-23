package kitty

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/terminal_image"
	"pitago/src/tui/internal/ttytest"
)

func diac(n int) rune { r, _ := terminal_image.Diacritic(n); return r }

func cluster(row, col int) string {
	return string([]rune{Placeholder, diac(row), diac(col)})
}

// (a.1) The cell buffer: what a SimulationScreen-based test of ChatView
// would see.
func TestSimulationCells(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(20, 6)
	for _, id := range []uint32{42, 0x0A0B0C} {
		sim.Clear()
		for r := 0; r < 2; r++ {
			if !DrawRow(sim, 3, 1+r, id, r, 4, tcell.StyleDefault) {
				t.Fatal("DrawRow refused a 2x4 grid")
			}
		}
		for r := 0; r < 2; r++ {
			for c := 0; c < 4; c++ {
				mainc, comb, st, w := sim.GetContent(3+c, 1+r)
				fg, _, _ := st.Decompose()
				if mainc != Placeholder || w != 1 || len(comb) != 2 ||
					comb[0] != diac(r) || comb[1] != diac(c) || fg != IDColor(id) {
					t.Errorf("id %d cell r%d c%d: %U %U w=%d fg=%v", id, r, c, mainc, comb, w, fg)
				}
			}
		}
		// Neighbors untouched: a grid never bleeds outside its columns.
		if m, _, _, _ := sim.GetContent(7, 1); m == Placeholder {
			t.Error("grid wrote past its width")
		}
	}
	if DrawRow(sim, 0, 0, 1, MaxGrid, 1, tcell.StyleDefault) {
		t.Error("row beyond the diacritic table must be refused")
	}
}

// (a.2) The bytes tcell really writes, decoded back into cells: for each
// placeholder cell, the right code points and the right SGR id color.
func TestRealTcellOutput(t *testing.T) {
	for _, tc := range []struct {
		term string
		id   uint32
		fg   string
	}{
		{"xterm-kitty", 42, "38;5;42"},
		{"xterm-kitty", 0x0A0B0C, "38;2;10;11;12"},
		{"xterm-ghostty", 200, "38;5;200"},
		{"xterm-ghostty", 0x123456, "38;2;18;52;86"},
		{"xterm-256color", 42, "38;5;42"},
		{"xterm-kitty", 5, "35"}, // ids < 16: setaf picks SGR 30-37 (see IDColor)
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.term, tc.id), func(t *testing.T) {
			tty := ttytest.NewTty(40, 10)
			s := ttytest.Screen(t, tty, tc.term)
			defer s.Fini()
			tty.Reset()
			s.Clear()
			s.SetContent(0, 0, 'x', nil, tcell.StyleDefault)
			for r := 0; r < 3; r++ {
				DrawRow(s, 5, 2+r, tc.id, r, 6, tcell.StyleDefault)
			}
			s.Show()
			d := ttytest.Decode(tty.Bytes())
			if len(d.Malformed) > 0 {
				t.Fatalf("malformed: %v", d.Malformed)
			}
			for r := 0; r < 3; r++ {
				for c := 0; c < 6; c++ {
					got := d.Cells[[2]int{2 + r, 5 + c}]
					if got.Text != cluster(r, c) || got.FG != tc.fg {
						t.Errorf("r%d c%d: %+q fg=%q, want %+q fg=%q", r, c, got.Text, got.FG, cluster(r, c), tc.fg)
					}
				}
			}

			// Partial repaint: change one cell in the middle of a row. tcell
			// re-emits only that cell, after a cursor move — so it must be
			// self-describing (row AND column diacritics, id color).
			tty.Reset()
			s.SetContent(7, 3, 'Z', nil, tcell.StyleDefault)
			s.Show()
			s.SetContent(7, 3, Placeholder, []rune{diac(1), diac(2)}, tcell.StyleDefault.Foreground(IDColor(tc.id)))
			tty.Reset()
			s.Show()
			out := tty.Bytes()
			d = ttytest.Decode(out)
			if n := strings.Count(string(out), string(Placeholder)); n != 1 {
				t.Errorf("partial repaint re-emitted %d placeholders, want 1: %q", n, out)
			}
			if got := d.Cells[[2]int{3, 7}]; got.Text != cluster(1, 2) || got.FG != tc.fg {
				t.Errorf("repainted cell: %+q fg=%q", got.Text, got.FG)
			}
		})
	}
}

func testPNG(t testing.TB, w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8((x*7919 + y*104729) ^ (x * y)) // noise, so large images stay large
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), n, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// imageBox is the shape a ChatView image line takes: Ensure during Draw,
// cells via DrawRow, bytes flushed by the Channel's after-draw hook.
type imageBox struct {
	*tview.Box
	ch      *Channel
	id      uint32
	data    string
	scroll  int
	mu      sync.Mutex
	counter int
}

func (b *imageBox) Draw(s tcell.Screen) {
	b.Box.DrawForSubclass(s, b)
	x, y, w, h := b.GetInnerRect()
	b.mu.Lock()
	b.counter++
	n, off := b.counter, b.scroll
	b.mu.Unlock()
	tview.Print(s, fmt.Sprintf("frame %d", n), x, y, w, tview.AlignLeft, tcell.ColorWhite)
	const cols, rows = 8, 4
	b.ch.Ensure(b.id, b.data, cols, rows)
	for r := 0; r < rows; r++ {
		if yy := y + 2 + r - off; yy > y && yy < y+h {
			DrawRow(s, x+2, yy, b.id, r, cols, tcell.StyleDefault)
		}
	}
}

// (b) The side channel inside a real tview.Application over a real tcell
// terminfo screen, under concurrent QueueUpdateDraw load. Asserts:
//   - the upload and placement reach the tty before any placeholder cell;
//   - they are sent once, however many frames follow;
//   - no escape sequence in the stream is cut by another (no interleaving);
//   - each APC write is a single tty.Write, never split across frames.
func TestSideChannelInTview(t *testing.T) {
	tty := ttytest.NewTty(60, 16)
	s := ttytest.Uninitialized(t, tty, "xterm-kitty") // SetScreen calls Init
	app := tview.NewApplication().SetScreen(s)
	ch := &Channel{}
	ch.Install(app)
	box := &imageBox{Box: tview.NewBox(), ch: ch, id: 77, data: testPNG(t, 64, 32)}
	app.SetRoot(box, true)

	done := make(chan struct{})
	go func() { _ = app.Run(); close(done) }()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				app.QueueUpdateDraw(func() {
					box.mu.Lock()
					box.scroll = (box.scroll + 1) % 3
					box.mu.Unlock()
				})
				if i%10 == 0 {
					s.SetClipboard([]byte("noise")) // another writer, from a foreign goroutine
				}
			}
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	app.Stop()
	<-done

	out := tty.Bytes()
	d := ttytest.Decode(out)
	if len(d.Malformed) > 0 {
		t.Fatalf("interleaved/malformed sequences: %v", d.Malformed[:min(3, len(d.Malformed))])
	}
	var uploads, places int
	for _, a := range d.APCs {
		switch {
		case strings.HasPrefix(a, "Ga=t"):
			uploads++
		case strings.HasPrefix(a, "Ga=p"):
			places++
		}
	}
	if uploads != 1 || places != 1 {
		t.Errorf("uploads=%d places=%d, want 1/1 (frames drawn: %d)", uploads, places, box.counter)
	}
	firstAPC := bytes.Index(out, []byte("\x1b_Ga=t"))
	firstCell := bytes.Index(out, []byte(string(Placeholder)))
	if firstAPC < 0 || firstCell < 0 || firstAPC > firstCell {
		t.Errorf("upload at %d, first placeholder at %d: upload must come first", firstAPC, firstCell)
	}
	for _, w := range tty.Writes() {
		if bytes.Contains(w, []byte("\x1b_G")) && !bytes.HasSuffix(w, []byte("\x1b\\")) {
			t.Errorf("an APC write was split: %q…", w[:min(40, len(w))])
		}
	}
	if box.counter < 10 {
		t.Errorf("only %d frames drawn; stress did not run", box.counter)
	}
	t.Logf("%d frames, %d bytes, %d tty writes, %d OSC (clipboard noise)", box.counter, len(out), len(tty.Writes()), len(d.OSCs))
}

// A chunked upload (> 4096 base64 bytes) must still go out in one write:
// kitty requires the chunks of one transmission to be contiguous.
func TestLargeUploadIsOneWrite(t *testing.T) {
	tty := ttytest.NewTty(40, 10)
	s := ttytest.Screen(t, tty, "xterm-kitty")
	defer s.Fini()
	ch := &Channel{}
	data := testPNG(t, 400, 300)
	if len(data) <= 4096 {
		t.Fatalf("test image too small to chunk: %d", len(data))
	}
	ch.Ensure(90, data, 20, 8)
	tty.Reset()
	n := ch.Flush(s)
	w := tty.Writes()
	if len(w) != 1 || n != len(w[0]) || strings.Count(string(w[0]), "\x1b_G") < 3 {
		t.Errorf("writes=%d n=%d", len(w), n)
	}
	ch.Ensure(90, data, 20, 8)
	if ch.Flush(s) != 0 {
		t.Error("second Ensure of the same id+size must send nothing")
	}
	ch.Ensure(90, data, 10, 4)
	if got := ch.Flush(s); got == 0 || got > 200 {
		t.Errorf("new size must send only a placement, sent %d bytes", got)
	}
}
