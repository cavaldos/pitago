// Package tstyle adapts pitago's palettes (components/theme) to tcell/tview.
//
// theme.Theme stays the pure, string-valued source of truth; this package is
// the one place that turns those strings into tcell colors. Values follow
// lipgloss.Color's grammar, so a theme renders the same on both stacks:
//
//	""         → tcell.ColorDefault (terminal's own color)
//	"#RRGGBB"  → truecolor (tcell.NewHexColor); "#RGB" is expanded
//	"0".."255" → ANSI / xterm-256 palette index (tcell.PaletteColor)
//	other      → tcell.GetColor (W3C / X11 names), else ColorDefault
//
// Palette carries every theme slot as a tcell.Color for src/app's own
// drawing; TviewTheme/ApplyTview only seed tview.Styles, the defaults stock
// tview widgets (List, InputField, Form, Modal…) fall back to.
package tstyle

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/theme"
)

// Color converts one theme color string to a tcell color.
func Color(s string) tcell.Color {
	s = strings.TrimSpace(s)
	if s == "" {
		return tcell.ColorDefault
	}
	if s[0] == '#' {
		hex := s[1:]
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) != 6 {
			return tcell.ColorDefault
		}
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return tcell.ColorDefault
		}
		return tcell.NewHexColor(int32(v))
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n > 255 {
			return tcell.ColorDefault
		}
		return tcell.PaletteColor(n)
	}
	return tcell.GetColor(strings.ToLower(s))
}

// ToolPalette is theme.ToolPalette as tcell colors.
type ToolPalette struct {
	Frame, Running, Error                        tcell.Color
	Read, Write, Edit, Shell, Dir, Search, Other tcell.Color
}

// Palette is theme.Theme (after theme.Resolve) as tcell colors.
type Palette struct {
	Name                                string
	Accent, Border, Muted, Text, Code   tcell.Color
	Green, Red, Yellow, Cyan            tcell.Color
	Plan                                tcell.Color
	Input, InputDim                     tcell.Color
	ToolPending, ToolSuccess, ToolError tcell.Color
	ToolNeutral                         tcell.Color
	Tool                                ToolPalette
	HiBg, HiFg                          tcell.Color
	Light                               bool
}

// FromTheme resolves t (so empty slots take their fallbacks, exactly as
// src/app's ApplyTheme does) and converts every slot.
func FromTheme(t theme.Theme) Palette {
	t = theme.Resolve(t)
	return Palette{
		Name:   t.Name,
		Accent: Color(t.Accent), Border: Color(t.Border), Muted: Color(t.Muted),
		Text: Color(t.Text), Code: Color(t.Code),
		Green: Color(t.Green), Red: Color(t.Red), Yellow: Color(t.Yellow), Cyan: Color(t.Cyan),
		Plan:  Color(t.Plan),
		Input: Color(t.Input), InputDim: Color(t.InputDim),
		ToolPending: Color(t.ToolPending), ToolSuccess: Color(t.ToolSuccess),
		ToolError: Color(t.ToolError), ToolNeutral: Color(t.ToolNeutral),
		Tool: ToolPalette{
			Frame: Color(t.Tool.Frame), Running: Color(t.Tool.Running), Error: Color(t.Tool.Error),
			Read: Color(t.Tool.Read), Write: Color(t.Tool.Write), Edit: Color(t.Tool.Edit),
			Shell: Color(t.Tool.Shell), Dir: Color(t.Tool.Dir), Search: Color(t.Tool.Search),
			Other: Color(t.Tool.Other),
		},
		HiBg: Color(t.HiBg), HiFg: Color(t.HiFg),
		Light: t.Light,
	}
}

// TviewTheme maps t onto tview's widget defaults. background is a surface
// color string (e.g. theme.BackgroundHex(name, t.Light)); "" keeps the
// terminal's own background, pitago's default look.
func TviewTheme(t theme.Theme, background string) tview.Theme {
	p := FromTheme(t)
	bg := Color(background)
	inverse := bg
	if inverse == tcell.ColorDefault {
		// Text drawn on a Text-colored fill needs the surface color; with a
		// transparent surface the highlight bg is the closest stand-in.
		inverse = p.HiBg
	}
	return tview.Theme{
		PrimitiveBackgroundColor:    bg,
		ContrastBackgroundColor:     p.HiBg,
		MoreContrastBackgroundColor: p.Border,
		BorderColor:                 p.Border,
		TitleColor:                  p.Accent,
		GraphicsColor:               p.Border,
		PrimaryTextColor:            p.Text,
		SecondaryTextColor:          p.Muted,
		TertiaryTextColor:           p.Accent,
		InverseTextColor:            inverse,
		ContrastSecondaryTextColor:  p.InputDim,
	}
}

// ApplyTview sets tview.Styles from t with a transparent background. Call
// it before building widgets: tview copies Styles into each primitive when
// it is created, so existing primitives keep their old colors.
func ApplyTview(t theme.Theme) {
	tview.Styles = TviewTheme(t, "")
}

// ApplyTviewBackground is ApplyTview with an explicit surface color.
func ApplyTviewBackground(t theme.Theme, background string) {
	tview.Styles = TviewTheme(t, background)
}
