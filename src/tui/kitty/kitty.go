// Package kitty draws kitty-graphics images on a tcell screen with unicode
// placeholders (U=1), and sends the image bytes through a side channel that
// is safe to use from a tview application.
//
// Two halves, mirroring src/app/images.go:
//
//   - Cells. An image is a grid of U+10EEEE placeholder cells whose
//     foreground color encodes the image id and whose combining marks
//     encode the row and column. They are ordinary screen cells: they
//     scroll, clip and diff like text, so there is no placement lifecycle.
//   - Bytes. The PNG upload (a=t) and the virtual placement (a=p,U=1) are
//     APC sequences that must reach the terminal before the first frame
//     that shows the cells, and must never land inside one of tcell's own
//     escape sequences.
//
// Unlike images.go, every cell carries BOTH diacritics (row and column).
// Bubble Tea repainted whole lines left to right, so cells could inherit
// row/column from the cell to their left. tcell repaints only dirty cells
// and moves the cursor to each one, so a cell may be printed with no left
// neighbor in the same write; it has to describe itself. The cost is two
// extra code points per changed cell, and unchanged cells are not
// re-emitted at all.
package kitty

import (
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/terminal_image"
)

// Placeholder is kitty's unicode placeholder code point.
const Placeholder = '\U0010EEEE'

// MaxGrid is the largest row or column index the diacritic table encodes.
const MaxGrid = 297

// IDColor is the foreground color that tells kitty which image a cell
// belongs to. Ids 1..255 use the 256-color palette (SGR 38;5;id). Larger
// ids use 24-bit color (SGR 38;2;r;g;b, id = r<<16|g<<8|b), which tcell
// emits only when it runs in truecolor mode (terminfo with RGB setters,
// e.g. xterm-kitty / xterm-ghostty, and TCELL_TRUECOLOR not "disable").
// Below 16, terminfo's setaf emits SGR 30-37/90-97. kitty treats those as
// palette indexes too, but allocate ids from 16 up to keep the wire form
// unambiguous.
func IDColor(id uint32) tcell.Color {
	if id < 256 {
		return tcell.PaletteColor(int(id))
	}
	return tcell.NewRGBColor(int32(id>>16&0xff), int32(id>>8&0xff), int32(id&0xff))
}

// DrawRow paints row `row` of image id's placeholder grid at (x, y),
// columns [0, cols). style's foreground is replaced by IDColor(id); keep
// its background so the image's transparent pixels match the chat
// background. Returns false when row or cols exceeds MaxGrid (callers fall
// back to a text placeholder, as images.go does with □).
func DrawRow(s tcell.Screen, x, y int, id uint32, row, cols int, style tcell.Style) bool {
	rd, ok := terminal_image.Diacritic(row)
	if !ok || cols > MaxGrid {
		return false
	}
	st := style.Foreground(IDColor(id))
	for c := 0; c < cols; c++ {
		cd, _ := terminal_image.Diacritic(c)
		s.SetContent(x+c, y, Placeholder, []rune{rd, cd}, st)
	}
	return true
}

// Channel queues the APC sequences an image needs and writes them to the
// terminal right before the frame that first shows it.
//
// Primitives call Ensure from Draw. Flush runs in the application's
// after-draw hook: tview calls it after root.Draw and immediately before
// screen.Show(), on the same goroutine, while tcell is not writing (tcell
// writes a frame only inside Show/Sync, under its own lock). So the bytes
// land between two frames, before the cells that reference them. A Draw
// always ends in Show in tview, so there is no "rendered but never painted"
// case to account for, unlike Bubble Tea's View.
//
// Every write to the terminal from outside tcell must happen on the draw
// goroutine (here, or inside QueueUpdate*). A goroutine that writes the
// tty directly can interleave with Show's frame write.
type Channel struct {
	mu      sync.Mutex
	pending []byte
	placed  map[placement]bool
	uploads map[uint32]bool
}

type placement struct {
	id         uint32
	cols, rows int
}

// Ensure queues the upload of pngBase64 under id (once per id) and a
// virtual placement of cols×rows cells (once per id and size). Re-placing
// the same id+size would make kitty rebuild the placement on every repaint.
func (c *Channel) Ensure(id uint32, pngBase64 string, cols, rows int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.placed == nil {
		c.placed = map[placement]bool{}
		c.uploads = map[uint32]bool{}
	}
	if !c.uploads[id] {
		c.pending = append(c.pending, terminal_image.Upload(pngBase64, uint64(id))...)
		c.uploads[id] = true
	}
	p := placement{id, cols, rows}
	if !c.placed[p] {
		c.pending = append(c.pending, terminal_image.PlaceUnicode(uint64(id), cols, rows)...)
		c.placed[p] = true
	}
}

// Forget drops what Ensure remembered, so the next Ensure resends. Call it
// after anything that may have cleared the terminal's image store (a
// terminal reset, or handing the tty to a child process).
func (c *Channel) Forget() {
	c.mu.Lock()
	c.placed, c.uploads = nil, nil
	c.mu.Unlock()
}

// Flush writes the queued bytes to the screen's tty in one write. Screens
// without a tty (SimulationScreen) drop them. Returns the bytes written.
func (c *Channel) Flush(s tcell.Screen) int {
	c.mu.Lock()
	buf := c.pending
	c.pending = nil
	c.mu.Unlock()
	if len(buf) == 0 {
		return 0
	}
	tty, ok := s.Tty()
	if !ok || tty == nil {
		return 0
	}
	n, _ := tty.Write(buf)
	return n
}

// Install chains Flush into app's after-draw hook, keeping any hook
// already set.
func (c *Channel) Install(app *tview.Application) {
	prev := app.GetAfterDrawFunc()
	app.SetAfterDrawFunc(func(s tcell.Screen) {
		if prev != nil {
			prev(s)
		}
		c.Flush(s)
	})
}
