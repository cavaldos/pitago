package ev

import "strings"

// WorkspaceProvider supplies the independent surfaces of the main tview layout.
type WorkspaceProvider interface{ NativeWorkspace() WorkspaceView }

// WorkspaceView carries content, not a composed terminal frame. Rich chat and
// extension text may include ANSI styling; the widgets own geometry and chrome.
type WorkspaceView struct {
	MainWidth, SidebarWidth int
	Header                  string
	HideHeader              bool // bottom-docked session tree is transcript-only
	Chat                    TranscriptView
	Sidebar                 []string
	Editor                  EditorView
	Above, Below            []PanelView // live widgets and inline popups
	Toasts                  []ToastView
	Document                *DocumentView
}

// PanelView contains rich text; tview draws padding and borders around it.
// A zero Width fills the column.
type PanelView struct {
	Text               string
	Width              int
	PaddingX, PaddingY int
	Border             bool
}

func (p PanelView) Height() int {
	if p.Text == "" {
		return 0
	}
	h := strings.Count(p.Text, "\n") + 1 + 2*p.PaddingY
	if p.Border {
		h += 2
	}
	return h
}

// DocumentView is for custom tview surfaces whose content isn't a list, such
// as the file/code workspace or the branch tree with drawing connectors.
// Part rectangles use coordinates relative to Bounds, not screen coordinates.
type DocumentView struct {
	Bounds Rect
	Border bool
	Parts  []DocumentPart
}

type DocumentPart struct {
	Rect
	Rows []string
}

type TranscriptView struct {
	Rows                 []string // visible rich text rows
	Height, Offset       int
	HoverStart, HoverEnd int
	Selection            *TextSelection
	JumpLabel            string
	JumpX, JumpY         int // relative to the transcript
}

type TextPoint struct{ Line, Column int }

type TextSelection struct {
	Start, End TextPoint // normalized, end exclusive
	Gutters    []int     // first selectable column for each visible row
}

type EditorView struct {
	Title, Border string
	Hints, Status string
	Rows          []EditorRow
	Chips         string // empty if absent, otherwise one rich text row
	ChipsVisible  bool
	PromptColor   string
}

type EditorRow struct {
	Prompt, Text string
	Placeholder  bool
	Caret        int // cell column relative to Text, -1 when hidden
}

type ToastView struct {
	Text      string
	Remaining int
	Error     bool
}
