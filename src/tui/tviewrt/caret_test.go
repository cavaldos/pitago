package tviewrt

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"pitago/src/components/cells"
	"pitago/src/tui/ev"
	"pitago/src/tui/widgets"
)

// The composer caret must be the terminal's real cursor so IME composition
// text (Vietnamese Telex, CJK) appears where the user types.
func TestEditorCaretUsesRealCursor(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(40, 6)
	scr.HideCursor()

	p := &editorPane{Box: newRichPane(nil).Box, cache: cells.NewCache(16), styles: widgets.DefaultMenuBarStyles()}
	p.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	p.view = ev.EditorView{Rows: []ev.EditorRow{{Prompt: "> ", Text: "xin chào", Caret: 3}}}
	p.SetRect(0, 0, 40, 4)
	p.Draw(scr)
	scr.Show()

	x, y, visible := scr.GetCursor()
	// border(1) + padding(1) + prompt(2) + caret(3)
	if !visible || x != 7 || y != 1 {
		t.Fatalf("cursor = (%d,%d) visible=%v, want (7,1) visible", x, y, visible)
	}
}
