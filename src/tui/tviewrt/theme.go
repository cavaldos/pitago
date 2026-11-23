package tviewrt

import (
	"github.com/gdamore/tcell/v2"

	"pitago/src/components/tstyle"
	"pitago/src/tui/ev"
	"pitago/src/tui/picker"
	"pitago/src/tui/widgets"
)

func themeColor(value string) tcell.Color { return tstyle.Color(value) }

func paletteStyles(p ev.NativePalette) widgets.MenuBarStyles {
	base := tcell.StyleDefault.Foreground(themeColor(p.Text)).Background(themeColor(p.Surface))
	selected := base.Foreground(themeColor(p.HiFg)).Background(themeColor(p.HiBg)).Bold(true)
	muted := base.Foreground(themeColor(p.Muted))
	return widgets.MenuBarStyles{
		Normal: base,
		Hover:  base.Foreground(themeColor(p.Accent)).Bold(true),
		Open:   selected,
		Menu: widgets.MenuStyles{
			Normal: base, Selected: selected, Disabled: muted.Dim(true),
			Shortcut: muted, Border: base.Foreground(themeColor(p.Border)),
		},
	}
}

func (r *Runtime) syncNativeTheme() {
	provider, ok := r.model.(ev.NativePaletteProvider)
	if !ok {
		return
	}
	r.nativePalette = provider.NativePalette()
	styles := paletteStyles(r.nativePalette)
	if r.bar != nil {
		r.bar.Styles = styles
	}
	for _, popup := range r.ov.Popups() {
		switch p := popup.(type) {
		case *widgets.ContextMenu:
			p.Styles = styles.Menu
		case *widgets.Tooltip:
			if p.Style != styles.Normal || p.Border != styles.Menu.Border {
				p.Style, p.Border = styles.Normal, styles.Menu.Border
				p.SetText(p.Text())
			}
		case *picker.Picker:
			p.Styles = pickerStyles(styles)
		}
	}
	r.nativeStyles = styles
}

func pickerStyles(st widgets.MenuBarStyles) picker.Styles {
	base, muted, border := st.Normal, st.Menu.Shortcut, st.Menu.Border
	return picker.Styles{Box: base, Border: border, Title: st.Hover, Message: muted,
		Filter: muted, Row: base, Selected: st.Open, Disabled: st.Menu.Disabled,
		Hint: muted, Section: base.Bold(true), Footer: muted, Empty: muted, Track: border, Thumb: muted}
}
