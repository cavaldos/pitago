package app

import (
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/format"
	"pitago/src/components/recent"
	"pitago/src/components/theme"
)

const sideW = 34 // sidebar width

const sideInnerW = sideW - 4 // sidebar content width (box minus border 2 + padding 2)

const maxRecent = recent.MaxRecent // recent models kept in the sidebar

// defaultPalette is the built-in default theme, resolved once. Every
// slot below starts from it, so src/app holds no color literal of its
// own: the palettes live in src/components/theme and ApplyTheme only
// ever writes values that came from there.
var defaultPalette = theme.Get("")

// palette slots (vars: ApplyTheme swaps them at runtime; defaults are the
// opencode-like monochrome — subtle borders, dim text, no rainbow).
var (
	cAccent   = lipgloss.Color(defaultPalette.Accent) // selected / emphasis: white
	cBorder   = lipgloss.Color(defaultPalette.Border) // subtle borders (opencode BorderNormal)
	cMuted    = lipgloss.Color(defaultPalette.Muted)  // dim text
	cText     = lipgloss.Color(defaultPalette.Text)   // normal text
	cCode     = lipgloss.Color(defaultPalette.Code)   // code / output
	cGreen    = lipgloss.Color(defaultPalette.Green)  // success dot
	cRed      = lipgloss.Color(defaultPalette.Red)    // error
	cYellow   = lipgloss.Color(defaultPalette.Yellow) // warning (quit arm)
	cCyan     = lipgloss.Color(defaultPalette.Cyan)   // command names in the / popup
	cPlan     = lipgloss.Color(defaultPalette.Plan)   // plan mode input border (purple)
	cSide     = lipgloss.Color(defaultPalette.Border) // unused now, kept subtle
	cInput    = lipgloss.Color(defaultPalette.Input)  // input focus: white, not cyan
	cInputDim = lipgloss.Color(defaultPalette.InputDim)
	cHiBg     = lipgloss.Color(defaultPalette.HiBg) // selected-row background
	cHiFg     = lipgloss.Color(defaultPalette.HiFg) // selected-row foreground
	// tool block backgrounds, resolved from pi's dark theme vars:
	// toolPendingBg / toolSuccessBg / toolErrorBg (pi wraps every tool
	// execution in a Box with these: pending while running, green on
	// success, red on error). toolNeutralBg is the quiet fill for
	// output that has no execution state at all.
	cToolPending = lipgloss.Color(defaultPalette.ToolPending)
	cToolSuccess = lipgloss.Color(defaultPalette.ToolSuccess)
	cToolError   = lipgloss.Color(defaultPalette.ToolError)
	cToolNeutral = lipgloss.Color(defaultPalette.ToolNeutral)
	// tool block frames. The frame carries the failure signal only; a
	// successful block keeps the neutral outline and lets its fill say
	// "done", so a transcript does not turn into a row of green frames.
	cToolFrame    = lipgloss.Color(defaultPalette.Tool.Frame)
	cToolFrameRun = lipgloss.Color(defaultPalette.Tool.Running)
	cToolFrameErr = lipgloss.Color(defaultPalette.Tool.Error)
	// per-kind header accents: the tool name is tinted by what the tool
	// does, so a read never reads like a write.
	cToolRead   = lipgloss.Color(defaultPalette.Tool.Read)
	cToolWrite  = lipgloss.Color(defaultPalette.Tool.Write)
	cToolEdit   = lipgloss.Color(defaultPalette.Tool.Edit)
	cToolShell  = lipgloss.Color(defaultPalette.Tool.Shell)
	cToolDir    = lipgloss.Color(defaultPalette.Tool.Dir)
	cToolSearch = lipgloss.Color(defaultPalette.Tool.Search)
	cToolOther  = lipgloss.Color(defaultPalette.Tool.Other)
)

var (
	headerStyle = lipgloss.NewStyle().
			Foreground(cMuted)
	badgeStyle = lipgloss.NewStyle().
			Foreground(cMuted)
	statusBarStyle = lipgloss.NewStyle().Foreground(cMuted)
	userStyle      = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1).
			Foreground(cText)
	toolStyle     = lipgloss.NewStyle().Foreground(cMuted)
	toolNameStyle = lipgloss.NewStyle().Bold(true).Foreground(cText)
	codeStyle     = lipgloss.NewStyle().Foreground(cCode)
	sideStyle     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
	sideTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(cText)
	sepStyle       = lipgloss.NewStyle().Foreground(cBorder)
	cmdPopStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
	cmdHiStyle = lipgloss.NewStyle().
			Background(cHiBg).
			Foreground(cHiFg)
	cmdNameStyle = lipgloss.NewStyle().
			Foreground(cCyan)
	cmdNameHiStyle = lipgloss.NewStyle().
			Background(cHiBg).
			Foreground(cCyan)
	rowHiStyle = lipgloss.NewStyle().
			Background(cHiBg).
			Foreground(cHiFg)
	dlgStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(1, 3)
	errStyle  = lipgloss.NewStyle().Foreground(cRed)
	okStyle   = lipgloss.NewStyle().Foreground(cGreen)
	warnStyle = lipgloss.NewStyle().Foreground(cYellow)
)

// tool block palette resolvers -----------------------------------------

// toolAccent is the header accent for a tool, picked by the kind the
// tool does (read / write / edit / shell / cd / search / other). The
// kind comes from format.ToolKind, i.e. from the very tool name the
// header label is built from, so label and color cannot drift apart.
func toolAccent(tool string) lipgloss.Color {
	switch format.ToolKind(tool) {
	case format.KindRead:
		return cToolRead
	case format.KindWrite:
		return cToolWrite
	case format.KindEdit:
		return cToolEdit
	case format.KindShell:
		return cToolShell
	case format.KindDir:
		return cToolDir
	case format.KindSearch:
		return cToolSearch
	}
	return cToolOther
}

// toolNameStyleFor is the header row style for one tool call: bold and
// tinted by its kind accent. Built per call because the accent depends
// on the tool; toolNameStyle is the kind-less fallback.
func toolNameStyleFor(tool string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(toolAccent(tool))
}

// toolFrame is the outline color for a tool block in the given
// execution state (format.Status*). Only a failure reddens the frame:
// success and neutral keep the theme's border color and let the fill —
// which pi's own Box tints per status — carry the state, so a long
// transcript does not read as a column of status-colored boxes.
func toolFrame(class string) lipgloss.Color {
	switch class {
	case format.StatusError:
		return cToolFrameErr
	case format.StatusRunning:
		return cToolFrameRun
	}
	return cToolFrame
}

// toolFill is the block background for an execution state. It reuses
// pi's three-way toolBg palette for the states pi has, and the quiet
// neutral fill for the one it does not.
func toolFill(class string) lipgloss.Color {
	switch class {
	case format.StatusSuccess:
		return cToolSuccess
	case format.StatusError:
		return cToolError
	case format.StatusRunning:
		return cToolPending
	}
	return cToolNeutral
}

// ApplyTheme swaps the palette + rebuilds every derived lipgloss style.
// Call once at startup (saved theme / --theme flag) and on /theme switch.
func ApplyTheme(raw theme.Theme) {
	// Resolve first: a caller may hand over a palette that only sets the
	// slots it cares about, and every slot read below must be non-empty
	// or the tool blocks would lose their frame and accents.
	t := theme.Resolve(raw)
	cAccent = lipgloss.Color(t.Accent)
	cBorder = lipgloss.Color(t.Border)
	cMuted = lipgloss.Color(t.Muted)
	cText = lipgloss.Color(t.Text)
	cCode = lipgloss.Color(t.Code)
	cGreen = lipgloss.Color(t.Green)
	cRed = lipgloss.Color(t.Red)
	cYellow = lipgloss.Color(t.Yellow)
	cCyan = lipgloss.Color(t.Cyan)
	cPlan = lipgloss.Color(t.Plan)
	cSide = lipgloss.Color(t.Border)
	cInput = lipgloss.Color(t.Input)
	cInputDim = lipgloss.Color(t.InputDim)
	cHiBg = lipgloss.Color(t.HiBg)
	cHiFg = lipgloss.Color(t.HiFg)
	cToolPending = lipgloss.Color(t.ToolPending)
	cToolSuccess = lipgloss.Color(t.ToolSuccess)
	cToolError = lipgloss.Color(t.ToolError)
	cToolNeutral = lipgloss.Color(t.ToolNeutral)
	cToolFrame = lipgloss.Color(t.Tool.Frame)
	cToolFrameRun = lipgloss.Color(t.Tool.Running)
	cToolFrameErr = lipgloss.Color(t.Tool.Error)
	cToolRead = lipgloss.Color(t.Tool.Read)
	cToolWrite = lipgloss.Color(t.Tool.Write)
	cToolEdit = lipgloss.Color(t.Tool.Edit)
	cToolShell = lipgloss.Color(t.Tool.Shell)
	cToolDir = lipgloss.Color(t.Tool.Dir)
	cToolSearch = lipgloss.Color(t.Tool.Search)
	cToolOther = lipgloss.Color(t.Tool.Other)

	headerStyle = lipgloss.NewStyle().Foreground(cMuted)
	badgeStyle = lipgloss.NewStyle().Foreground(cMuted)
	statusBarStyle = lipgloss.NewStyle().Foreground(cMuted)
	userStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Foreground(cText)
	toolStyle = lipgloss.NewStyle().Foreground(cMuted)
	toolNameStyle = lipgloss.NewStyle().Bold(true).Foreground(cText)
	codeStyle = lipgloss.NewStyle().Foreground(cCode)
	sideStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
	sideTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(cText)
	sepStyle = lipgloss.NewStyle().Foreground(cBorder)
	cmdPopStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
	cmdHiStyle = lipgloss.NewStyle().
		Background(cHiBg).
		Foreground(cHiFg)
	cmdNameStyle = lipgloss.NewStyle().
		Foreground(cCyan)
	cmdNameHiStyle = lipgloss.NewStyle().
		Background(cHiBg).
		Foreground(cCyan)
	rowHiStyle = lipgloss.NewStyle().
		Background(cHiBg).
		Foreground(cHiFg)
	dlgStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(1, 3)
	errStyle = lipgloss.NewStyle().Foreground(cRed)
	okStyle = lipgloss.NewStyle().Foreground(cGreen)
	warnStyle = lipgloss.NewStyle().Foreground(cYellow)
}

// messages ---------------------------------------------------------------
