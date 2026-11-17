package keys

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// teaPaste returns the runes Bubble Tea v1 delivered for a bracketed paste
// of payload, plus the strings of any other keys it reported.
func teaPaste(t *testing.T, tp *teaParser, payload string) string {
	t.Helper()
	got := tp.parse("\x1b[200~" + payload + "\x1b[201~")
	return strings.Join(got, "⏎") // String() of a paste is "[" + runes + "]"
}

// pasteSink is a tview root that records what reaches PasteHandler and
// the keys typed outside pastes, in order.
type pasteSink struct {
	*tview.Box
	mu  sync.Mutex
	log []string
}

func (s *pasteSink) PasteHandler() func(string, func(tview.Primitive)) {
	return func(text string, _ func(tview.Primitive)) {
		s.mu.Lock()
		s.log = append(s.log, PasteMsg(text).String())
		s.mu.Unlock()
	}
}

func (s *pasteSink) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return func(e *tcell.EventKey, _ func(tview.Primitive)) {
		s.mu.Lock()
		s.log = append(s.log, KeyString(e))
		s.mu.Unlock()
	}
}

func (s *pasteSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.log
	s.log = nil
	return l
}

// tviewPaste runs bytes through tcell's real parser into a real
// tview.Application whose screen is wrapped in PasteScreen.
type tviewPaster struct {
	sim  tcell.SimulationScreen
	app  *tview.Application
	sink *pasteSink
	ip   tcell.InputProcessor
	evs  chan tcell.Event
	sent chan struct{}
}

func newTviewPaster(t *testing.T) *tviewPaster {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	p := &tviewPaster{sim: sim, sink: &pasteSink{Box: tview.NewBox()}, sent: make(chan struct{}, 1)}
	p.app = tview.NewApplication().SetScreen(NewPasteScreen(sim)).EnablePaste(true).SetRoot(p.sink, true)
	p.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF64 {
			p.sent <- struct{}{}
			return nil
		}
		return e
	})
	done := make(chan struct{})
	go func() { _ = p.app.Run(); close(done) }()
	t.Cleanup(func() { p.app.Stop(); <-done })
	p.evs = make(chan tcell.Event, 4096)
	p.ip = sizedProcessor(t, p.evs)
	return p
}

// feed parses b with tcell and returns what the app saw. The sim screen's
// event queue is small and PostEvent drops when it is full, so events are
// forwarded with retry, in order.
func (p *tviewPaster) feed(t *testing.T, b string) []string {
	t.Helper()
	fwd := make(chan struct{})
	go func() {
		defer close(fwd)
		quiet := time.NewTimer(80 * time.Millisecond) // outlasts a trailing lone ESC
		for {
			select {
			case e := <-p.evs:
				post(p.sim, e)
				quiet.Reset(80 * time.Millisecond)
			case <-quiet.C:
				return
			}
		}
	}()
	p.ip.ScanUTF8([]byte(b))
	<-fwd
	post(p.sim, tcell.NewEventKey(tcell.KeyF64, 0, tcell.ModNone))
	select {
	case <-p.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("app did not drain")
	}
	return p.sink.take()
}

func post(sim tcell.SimulationScreen, e tcell.Event) {
	for sim.PostEvent(e) != nil {
		time.Sleep(time.Millisecond)
	}
}

func pasteCorpus() []string {
	long := strings.Repeat("lorem ipsum dolor sit amet\n", 300)
	return []string{
		"hello",
		"a\nb\nc",    // LF only: tview's own path drops these
		"a\r\nb\r\n", // CRLF
		"a\rb",       // CR
		"col1\tcol2", // tab
		"unicode é 😀 → ✓ 中文",
		"ctl \x01\x02\x03\x07 \x1c\x1d\x1e\x1f end",
		"del\x7fx",
		"\x00nul",
		"private \U000F0001 \U000F00FE rune",
		"  leading and trailing  ",
		"bs\bx",         // BS 0x08
		"esc\x1bx",      // ESC + char
		"arrow\x1b[Aup", // a whole escape sequence
		"ss3\x1bOAup",
		"tilde\x1b[3~x",
		"trail esc\x1b",
		long,
	}
}

// knownPasteGaps: payloads tcell cannot hand back byte-exact (its parser
// has already folded them), keyed by payload: {tea's String(), ours}.
var knownPasteGaps = map[string][2]string{
	// tcell folds BS (0x08) and DEL into one Backspace key; DEL is what a
	// Backspace key sends, so that is what comes back.
	"bs\bx": {"[bs\bx]", "[bs\x7fx]"},
	// Escape sequences inside a paste are parsed by tcell into named keys
	// and rebuilt in CSI form; an SS3 arrow comes back as CSI.
	"ss3\x1bOAup": {"[ss3\x1bOAup]", "[ss3\x1b[Aup]"},
}

func TestPasteParityWithBubbleTea(t *testing.T) {
	tp := newTeaParser(t)
	tp.parse("a")
	tv := newTviewPaster(t)

	for _, payload := range pasteCorpus() {
		want := teaPaste(t, tp, payload)
		got := strings.Join(tv.feed(t, "\x1b[200~"+payload+"\x1b[201~"), "⏎")
		if got == want {
			continue
		}
		if g, ok := knownPasteGaps[payload]; ok && g == [2]string{want, got} {
			continue
		}
		t.Errorf("paste %q\n tea:  %q\n ours: %q", trunc(payload), trunc(want), trunc(got))
	}
}

// TestPasteKeepsOrder: keys typed right before and after a paste stay in
// order, because the paste rides tview's event channel like the keys do.
func TestPasteKeepsOrder(t *testing.T) {
	tv := newTviewPaster(t)
	got := tv.feed(t, "x\x1b[200~a\nb\x1b[201~y")
	want := []string{"x", "[a\nb]", "y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	// ESC typed right before a paste: tcell folds it into the marker.
	got = tv.feed(t, "\x1b\x1b[200~p\x1b[201~z")
	want = []string{"esc", "[p]", "z"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("esc before paste: got %q, want %q", got, want)
	}
}

// TestPlainTviewDropsLF documents why PasteScreen exists.
func TestPlainTviewDropsLF(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	_ = sim.Init()
	var got string
	box := &pasteSinkRaw{Box: tview.NewBox(), out: &got}
	app := tview.NewApplication().SetScreen(sim).EnablePaste(true).SetRoot(box, true)
	done := make(chan struct{})
	go func() { _ = app.Run(); close(done) }()
	defer func() { app.Stop(); <-done }()
	evs := make(chan tcell.Event, 64)
	ip := sizedProcessor(t, evs)
	ip.ScanUTF8([]byte("\x1b[200~a\nb\x1b[201~"))
	time.Sleep(50 * time.Millisecond)
	for len(evs) > 0 {
		_ = sim.PostEvent(<-evs)
	}
	time.Sleep(100 * time.Millisecond)
	app.QueueUpdate(func() {})
	if got != "ab" {
		t.Errorf("plain tview paste = %q; expected the LF to be dropped (if tview fixed this, PasteScreen may be unnecessary)", got)
	}
}

type pasteSinkRaw struct {
	*tview.Box
	out *string
}

func (s *pasteSinkRaw) PasteHandler() func(string, func(tview.Primitive)) {
	return func(text string, _ func(tview.Primitive)) { *s.out = text }
}

func TestDecodeEscapesPrivateBlock(t *testing.T) {
	// Real pasted runes inside the private block used for control bytes
	// must survive: they are escaped on the way in.
	in := []rune{'x', ctrlBase, ctrlBase + 0x0a, escLiteral, ctrlLimit, ctrlLimit + 1, 'y'}
	var enc []rune
	for _, r := range in {
		for _, ev := range encodePasteKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)) {
			enc = append(enc, ev.(*tcell.EventKey).Rune())
		}
	}
	if got := string(DecodePaste(string(enc))); got != string(in) {
		t.Errorf("round trip %q -> %q", string(in), got)
	}
}

func trunc(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
