package app

import (
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/recent"
	"pitago/src/components/theme"
)

const sideW = 34 // sidebar width

const sideInnerW = sideW - 4 // sidebar content width (box minus border 2 + padding 2)

const maxRecent = recent.MaxRecent // recent models kept in the sidebar

// palette slots (vars: ApplyTheme swaps them at runtime; defaults are the
// opencode-like monochrome — subtle borders, dim text, no rainbow).
var (
	cAccent   = lipgloss.Color("15")  // selected / emphasis: white
	cBorder   = lipgloss.Color("240") // subtle borders (opencode BorderNormal)
	cMuted    = lipgloss.Color("243") // dim text
	cText     = lipgloss.Color("252") // normal text
	cCode     = lipgloss.Color("250") // code / output
	cGreen    = lipgloss.Color("114") // success dot
	cRed      = lipgloss.Color("203") // error
	cYellow   = lipgloss.Color("11")  // warning (quit arm)
	cCyan     = lipgloss.Color("6")   // command names in the / popup
	cPlan     = lipgloss.Color("13")  // plan mode input border (purple)
	cSide     = lipgloss.Color("240") // unused now, kept subtle
	cInput    = lipgloss.Color("252") // input focus: white, not cyan
	cInputDim = lipgloss.Color("240") // input idle: subtle gray
	cHiBg     = lipgloss.Color("238") // selected-row background
	cHiFg     = lipgloss.Color("15")  // selected-row foreground
	// tool block backgrounds, resolved from pi's dark theme vars:
	// toolPendingBg / toolSuccessBg / toolErrorBg (pi wraps every tool
	// execution in a Box with these: pending while running, green on
	// success, red on error).
	cToolPending = lipgloss.Color("#282832")
	cToolSuccess = lipgloss.Color("#283228")
	cToolError   = lipgloss.Color("#3c2828")
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
	toolStyle = lipgloss.NewStyle().Foreground(cMuted)
	codeStyle = lipgloss.NewStyle().Foreground(cCode)
	sideStyle = lipgloss.NewStyle().
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

// ApplyTheme swaps the palette + rebuilds every derived lipgloss style.
// Call once at startup (saved theme / --theme flag) and on /theme switch.
func ApplyTheme(t theme.Theme) {
	cAccent = lipgloss.Color(t.Accent)
	cBorder = lipgloss.Color(t.Border)
	cMuted = lipgloss.Color(t.Muted)
	cText = lipgloss.Color(t.Text)
	cCode = lipgloss.Color(t.Code)
	cGreen = lipgloss.Color(t.Green)
	cRed = lipgloss.Color(t.Red)
	cYellow = lipgloss.Color(t.Yellow)
	cCyan = lipgloss.Color(t.Cyan)
	if t.Plan == "" {
		t.Plan = "13"
	}
	cPlan = lipgloss.Color(t.Plan)
	cSide = lipgloss.Color(t.Border)
	cInput = lipgloss.Color(t.Input)
	cInputDim = lipgloss.Color(t.InputDim)
	cHiBg = lipgloss.Color(t.HiBg)
	cHiFg = lipgloss.Color(t.HiFg)
	cToolPending = lipgloss.Color(t.ToolPending)
	cToolSuccess = lipgloss.Color(t.ToolSuccess)
	cToolError = lipgloss.Color(t.ToolError)

	headerStyle = lipgloss.NewStyle().Foreground(cMuted)
	badgeStyle = lipgloss.NewStyle().Foreground(cMuted)
	statusBarStyle = lipgloss.NewStyle().Foreground(cMuted)
	userStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Foreground(cText)
	toolStyle = lipgloss.NewStyle().Foreground(cMuted)
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
