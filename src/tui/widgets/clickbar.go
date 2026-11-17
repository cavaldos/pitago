package widgets

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
)

// Segment is one clickable piece of a ClickBar.
type Segment struct {
	ID         string
	Text       string      // may carry ANSI SGR; SGR resets return to Style / HoverStyle
	Style      tcell.Style // also fills the segment's cells
	HoverStyle tcell.Style // zero value: Style with reverse video
	Tooltip    string      // shown through the bar's Hover after the delay
	Right      bool        // laid out from the right edge, in slice order right-to-left
	Disabled   bool        // drawn, but no hover and no clicks
}

// ClickBar is a one-row bar of segments: a header or status line. Left
// segments run from the left edge, Right ones from the right edge; on
// overlap the left group wins. Hover highlights a segment and (with Hover
// set) shows its tooltip; left and right clicks report the segment ID and
// the pointer position — enough to open a ContextMenu right there.
type ClickBar struct {
	*tview.Box
	Segments []Segment
	Hover    *Hover
	Fill     tcell.Style // cells no segment covers

	OnClick      func(id string, x, y int)
	OnRightClick func(id string, x, y int)

	hovered string
	layout  []placed
}

type placed struct {
	seg  Segment
	x, w int
	line cells.Line
}

// NewClickBar builds a bar from segments.
func NewClickBar(segments ...Segment) *ClickBar {
	return &ClickBar{Box: tview.NewBox(), Segments: segments,
		Fill: tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor)}
}

// SetSegments replaces the segments (the hover highlight survives when the
// hovered ID still exists).
func (b *ClickBar) SetSegments(s ...Segment) { b.Segments = s }

// Hovered is the ID of the segment under the pointer ("" for none).
func (b *ClickBar) Hovered() string { return b.hovered }

func (s Segment) hoverStyle() tcell.Style {
	if s.HoverStyle == (tcell.Style{}) {
		return s.Style.Reverse(true)
	}
	return s.HoverStyle
}

// arrange lays segments out in the current width.
func (b *ClickBar) arrange() {
	x, _, w, _ := b.GetInnerRect()
	b.layout = b.layout[:0]
	left, right := x, x+w
	var rights []placed
	for _, s := range b.Segments {
		st := s.Style
		if s.ID == b.hovered && s.ID != "" && !s.Disabled {
			st = s.hoverStyle()
		}
		ln := cells.ParseStyle(s.Text, st)[0]
		sw := ln.Width()
		if s.Right {
			if right-sw < x {
				continue
			}
			right -= sw
			rights = append(rights, placed{s, right, sw, ln})
			continue
		}
		if left+sw > x+w {
			sw = x + w - left
		}
		if sw > 0 {
			b.layout = append(b.layout, placed{s, left, sw, ln})
			left += sw
		}
	}
	for _, r := range rights {
		if r.x >= left {
			b.layout = append(b.layout, r)
		}
	}
}

// HitTest returns the ID of the segment at screen column x ("" for none).
func (b *ClickBar) HitTest(x int) string {
	if p := b.at(x); p != nil {
		return p.seg.ID
	}
	return ""
}

// SegmentRect is the screen rectangle of segment id, for anchoring a menu.
func (b *ClickBar) SegmentRect(id string) (Rect, bool) {
	b.arrange()
	_, y, _, _ := b.GetInnerRect()
	for _, p := range b.layout {
		if p.seg.ID == id {
			return Rect{p.x, y, p.w, 1}, true
		}
	}
	return Rect{}, false
}

func (b *ClickBar) at(x int) *placed {
	b.arrange()
	for i := range b.layout {
		if p := &b.layout[i]; x >= p.x && x < p.x+p.w {
			return p
		}
	}
	return nil
}

// Draw implements tview.Primitive.
func (b *ClickBar) Draw(scr tcell.Screen) {
	x, y, w, h := b.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	fill(scr, Rect{x, y, w, 1}, b.Fill)
	b.arrange()
	for _, p := range b.layout {
		st := p.seg.Style
		if p.seg.ID == b.hovered && p.seg.ID != "" && !p.seg.Disabled {
			st = p.seg.hoverStyle()
		}
		fill(scr, Rect{p.x, y, p.w, 1}, st)
		cells.DrawLine(scr, p.x, y, p.w, p.line)
	}
}

// MouseHandler implements tview.Primitive.
func (b *ClickBar) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return b.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		if !b.InRect(x, y) {
			if b.hovered != "" {
				b.hovered = ""
				return true, nil
			}
			return false, nil
		}
		p := b.at(x)
		id := ""
		if p != nil && !p.seg.Disabled {
			id = p.seg.ID
		}
		if a == actMove {
			changed := id != b.hovered
			b.hovered = id
			if b.Hover != nil {
				if id != "" && p.seg.Tooltip != "" {
					_, ry, _, _ := b.GetInnerRect()
					b.Hover.Over("clickbar:"+id, Rect{p.x, ry, p.w, 1}, p.seg.Tooltip)
				} else {
					b.Hover.Out()
				}
			}
			return changed || id != "", nil
		}
		if id == "" {
			return false, nil
		}
		switch a {
		case actLeftClick:
			if b.OnClick != nil {
				b.OnClick(id, x, y)
			}
		case actRightDown:
			// Right-click menus open on press, so press-drag-release can pick.
			if b.OnRightClick != nil {
				b.OnRightClick(id, x, y)
			}
		}
		return true, nil
	})
}
