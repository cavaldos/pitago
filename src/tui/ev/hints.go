package ev

// Rect is a rectangle in frame coordinates (the model's View, row 0 = its
// first line; a runtime-drawn menu bar is not part of the frame).
type Rect struct{ X, Y, W, H int }

// TooltipProvider is implemented by a model that has hover help. The
// runtime calls Tooltip on its UI goroutine for pointer motion over the
// frame, with frame coordinates. key identifies the hovered thing (same key
// = same tooltip: the delay is not restarted); r is its rectangle — the
// tooltip hides when the pointer leaves it — and text the help (ANSI and
// newlines allowed). Return key "" (or text "") for nothing.
type TooltipProvider interface {
	Tooltip(x, y int) (key string, r Rect, text string)
}

// Divider is a draggable boundary the model draws itself (the sidebar
// edge): column X, rows Y..Y+H-1, in frame coordinates.
type Divider struct {
	ID      string
	X, Y, H int
}

// DividerProvider lets the runtime give the model's dividers hover and
// drag feedback: it restyles the divider column while the pointer is on it
// and while a drag that started on it lasts. The drag itself stays in the
// model, which receives the mouse events as usual. Called on the UI
// goroutine on every draw and mouse event.
type DividerProvider interface {
	Dividers() []Divider
}
