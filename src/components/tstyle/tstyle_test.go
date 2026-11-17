package tstyle

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/theme"
)

func TestColor(t *testing.T) {
	cases := []struct {
		in   string
		want tcell.Color
	}{
		{"", tcell.ColorDefault},
		{"  ", tcell.ColorDefault},
		{"#61AFEF", tcell.NewRGBColor(0x61, 0xAF, 0xEF)},
		{"#61afef", tcell.NewRGBColor(0x61, 0xAF, 0xEF)},
		{"#abc", tcell.NewRGBColor(0xAA, 0xBB, 0xCC)},
		{"#12345", tcell.ColorDefault},
		{"#zzzzzz", tcell.ColorDefault},
		{"0", tcell.ColorBlack},
		{"15", tcell.ColorWhite},
		{"240", tcell.PaletteColor(240)},
		{"256", tcell.ColorDefault},
		{"-1", tcell.ColorDefault},
		{"red", tcell.ColorRed},
		{"nonsense", tcell.ColorDefault},
	}
	for _, c := range cases {
		if got := Color(c.in); got != c.want {
			t.Errorf("Color(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestEveryThemeConverts guards the theme table: every slot of every
// built-in palette must parse (a typo would silently become ColorDefault).
func TestEveryThemeConverts(t *testing.T) {
	for _, name := range theme.Names() {
		th := theme.Resolve(theme.Get(name))
		slots := map[string]string{
			"Accent": th.Accent, "Border": th.Border, "Muted": th.Muted, "Text": th.Text,
			"Code": th.Code, "Green": th.Green, "Red": th.Red, "Yellow": th.Yellow,
			"Cyan": th.Cyan, "Plan": th.Plan, "Input": th.Input, "InputDim": th.InputDim,
			"ToolPending": th.ToolPending, "ToolSuccess": th.ToolSuccess,
			"ToolError": th.ToolError, "ToolNeutral": th.ToolNeutral,
			"HiBg": th.HiBg, "HiFg": th.HiFg,
			"Tool.Frame": th.Tool.Frame, "Tool.Running": th.Tool.Running,
			"Tool.Error": th.Tool.Error, "Tool.Read": th.Tool.Read,
			"Tool.Write": th.Tool.Write, "Tool.Edit": th.Tool.Edit,
			"Tool.Shell": th.Tool.Shell, "Tool.Dir": th.Tool.Dir,
			"Tool.Search": th.Tool.Search, "Tool.Other": th.Tool.Other,
		}
		for slot, v := range slots {
			if v == "" {
				t.Errorf("%s.%s is empty after Resolve", name, slot)
				continue
			}
			if Color(v) == tcell.ColorDefault {
				t.Errorf("%s.%s = %q does not parse", name, slot, v)
			}
		}
	}
}

func TestFromThemeOneDark(t *testing.T) {
	p := FromTheme(theme.Get("one-dark"))
	if p.Accent != tcell.NewHexColor(0x61AFEF) || p.Red != tcell.NewHexColor(0xE06C75) {
		t.Fatalf("one-dark accent/red = %v/%v", p.Accent, p.Red)
	}
	if p.Tool.Frame == tcell.ColorDefault {
		t.Fatal("Tool.Frame should be filled by theme.Resolve")
	}
	d := FromTheme(theme.Get("default"))
	if d.Border != tcell.PaletteColor(240) || d.Accent != tcell.ColorWhite {
		t.Fatalf("default border/accent = %v/%v", d.Border, d.Accent)
	}
}

func TestTviewTheme(t *testing.T) {
	th := theme.Get("dracula")
	got := TviewTheme(th, "")
	if got.PrimitiveBackgroundColor != tcell.ColorDefault {
		t.Fatalf("transparent bg = %v", got.PrimitiveBackgroundColor)
	}
	if got.PrimaryTextColor != Color(th.Text) || got.BorderColor != Color(th.Border) ||
		got.ContrastBackgroundColor != Color(th.HiBg) || got.TitleColor != Color(th.Accent) {
		t.Fatalf("dracula mapping wrong: %+v", got)
	}
	if got.InverseTextColor != Color(th.HiBg) {
		t.Fatalf("inverse with transparent bg = %v", got.InverseTextColor)
	}
	bg := theme.BackgroundHex("dracula", false)
	withBg := TviewTheme(th, bg)
	if withBg.PrimitiveBackgroundColor != Color(bg) || withBg.InverseTextColor != Color(bg) {
		t.Fatalf("bg mapping wrong: %+v", withBg)
	}
}

func TestApplyTview(t *testing.T) {
	saved := tview.Styles
	defer func() { tview.Styles = saved }()
	ApplyTview(theme.Get("nord"))
	if tview.Styles.PrimaryTextColor != tcell.NewHexColor(0xECEFF4) {
		t.Fatalf("ApplyTview did not set Styles: %+v", tview.Styles)
	}
	ApplyTviewBackground(theme.Get("nord"), "#2E3440")
	if tview.Styles.PrimitiveBackgroundColor != tcell.NewHexColor(0x2E3440) {
		t.Fatalf("ApplyTviewBackground bg = %v", tview.Styles.PrimitiveBackgroundColor)
	}
}
