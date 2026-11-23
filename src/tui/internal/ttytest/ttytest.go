// Package ttytest runs a real tcell terminfo screen over an in-memory tty
// and decodes what tcell actually wrote, so tests can assert on the bytes a
// terminal would receive rather than on tcell's cell buffer.
package ttytest

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"
)

// Tty is a tcell.Tty whose output is captured and whose input never
// arrives (Read blocks until Close).
type Tty struct {
	W, H int

	mu     sync.Mutex
	out    bytes.Buffer
	writes [][]byte
	closed chan struct{}
	wake   chan struct{}
	once   sync.Once
}

func NewTty(w, h int) *Tty {
	return &Tty{W: w, H: h, closed: make(chan struct{}), wake: make(chan struct{}, 1)}
}

func (t *Tty) Start() error { return nil }
func (t *Tty) Stop() error  { return nil }

// Drain wakes a blocked Read, as the real tty's read deadline does; tcell's
// Fini waits for its input loop to notice it is stopping.
func (t *Tty) Drain() error {
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return nil
}
func (t *Tty) NotifyResize(func()) {}
func (t *Tty) Read([]byte) (int, error) {
	select {
	case <-t.closed:
	case <-t.wake:
	}
	return 0, nil
}
func (t *Tty) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}
func (t *Tty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: t.W, Height: t.H}, nil
}

func (t *Tty) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out.Write(p)
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}

// Bytes is everything written so far.
func (t *Tty) Bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.out.Bytes()...)
}

// Writes is the output split by Write call.
func (t *Tty) Writes() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][]byte(nil), t.writes...)
}

// Reset forgets captured output.
func (t *Tty) Reset() {
	t.mu.Lock()
	t.out.Reset()
	t.writes = nil
	t.mu.Unlock()
}

// Screen builds and initializes a tcell screen over tty using the named
// terminfo entry (e.g. "xterm-kitty", "xterm-256color"), in a UTF-8 locale.
// tcell elides combining marks and prints '?' for non-ASCII under a
// non-UTF-8 locale, so the locale is part of what is being tested.
func Screen(t testing.TB, tty *Tty, term string) tcell.Screen {
	t.Helper()
	s := Uninitialized(t, tty, term)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

// Uninitialized is Screen without Init, for tview.Application.SetScreen,
// which calls Init itself. Initializing a tcell screen twice starts two
// main loops over the same state (a data race).
func Uninitialized(t testing.TB, tty *Tty, term string) tcell.Screen {
	t.Helper()
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LINES", "")
	t.Setenv("COLUMNS", "")
	ti, err := terminfo.LookupTerminfo(term)
	if err != nil {
		t.Fatalf("terminfo %s: %v", term, err)
	}
	s, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Cell is one decoded screen cell: its text (base rune plus combining
// marks) and the SGR foreground in effect when it was printed ("" =
// default, "38;5;42", "38;2;1;2;3", "31", …).
type Cell struct {
	Text string
	FG   string
}

// Decoded is the result of replaying terminal output.
type Decoded struct {
	Cells map[[2]int]Cell // [row, col] → cell
	APCs  []string        // APC payloads in order (without ESC _ / ESC \)
	OSCs  []string        // OSC payloads in order
	// Malformed lists escape sequences that were cut short by another
	// ESC: the signature of two writers interleaving.
	Malformed []string
}

// Decode replays output through a minimal VT parser: CUP, SGR foreground,
// CR/LF/BS, text with combining marks, and APC/OSC/CSI/charset framing.
// Everything else is consumed and ignored.
func Decode(b []byte) Decoded {
	d := Decoded{Cells: map[[2]int]Cell{}}
	row, col := 0, 0
	fg := ""
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b && i+1 < len(b):
			switch b[i+1] {
			case '[':
				j := i + 2
				for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
					if b[j] == 0x1b {
						break
					}
					j++
				}
				if j >= len(b) || b[j] == 0x1b {
					d.Malformed = append(d.Malformed, strconv.Quote(string(b[i:min(j, len(b))])))
					i = j
					continue
				}
				params, final := string(b[i+2:j]), b[j]
				switch final {
				case 'H', 'f':
					if strings.HasPrefix(params, "?") {
						break
					}
					r, c := 1, 1
					if p := strings.Split(params, ";"); params != "" {
						r, _ = strconv.Atoi(p[0])
						if len(p) > 1 {
							c, _ = strconv.Atoi(p[1])
						}
					}
					row, col = r-1, c-1
				case 'm':
					fg = applySGR(fg, params)
				}
				i = j + 1
			case '_', ']':
				kind := b[i+1]
				j := i + 2
				for j < len(b) && b[j] != 0x1b && !(kind == ']' && b[j] == 0x07) {
					j++
				}
				body := string(b[i+2 : min(j, len(b))])
				switch {
				case j < len(b) && b[j] == 0x07:
					i = j + 1
				case j+1 < len(b) && b[j+1] == '\\':
					i = j + 2
				default:
					d.Malformed = append(d.Malformed, strconv.Quote(string(b[i:min(j, len(b))])))
					i = j
					continue
				}
				if kind == '_' {
					d.APCs = append(d.APCs, body)
				} else {
					d.OSCs = append(d.OSCs, body)
				}
			case '(', ')', '*', '+':
				i += 3
			default:
				i += 2
			}
		case c == '\r':
			col = 0
			i++
		case c == '\n':
			row++
			i++
		case c == '\b':
			if col > 0 {
				col--
			}
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			r, n := utf8.DecodeRune(b[i:])
			i += n
			if unicode.Is(unicode.Mn, r) && col > 0 {
				k := [2]int{row, col - 1}
				cell := d.Cells[k]
				cell.Text += string(r)
				d.Cells[k] = cell
				continue
			}
			d.Cells[[2]int{row, col}] = Cell{Text: string(r), FG: fg}
			col++
		}
	}
	return d
}

// applySGR folds one SGR parameter list into the tracked foreground.
func applySGR(fg, params string) string {
	if params == "" {
		return ""
	}
	p := strings.Split(params, ";")
	for k := 0; k < len(p); k++ {
		n, _ := strconv.Atoi(p[k])
		switch {
		case n == 0 || n == 39:
			fg = ""
		case (n >= 30 && n <= 37) || (n >= 90 && n <= 97):
			fg = p[k]
		case n == 38 && k+2 < len(p) && p[k+1] == "5":
			fg = strings.Join(p[k:k+3], ";")
			k += 2
		case n == 38 && k+4 < len(p) && p[k+1] == "2":
			fg = strings.Join(p[k:k+5], ";")
			k += 4
		case n == 48 && k+1 < len(p) && p[k+1] == "5":
			k += 2
		case n == 48 && k+1 < len(p) && p[k+1] == "2":
			k += 4
		}
	}
	return fg
}
