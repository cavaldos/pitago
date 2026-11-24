package ev

// NativePalette shares the active project colors with native UI chrome.
// Colors are ANSI palette indices, hex RGB, or empty for terminal default.
type NativePalette struct {
	Surface, Text, Muted, Accent, Border, HiBg, HiFg, Green, Red string
}

type NativePaletteProvider interface{ NativePalette() NativePalette }

type SettingsColumn int

const (
	SettingsSections SettingsColumn = iota
	SettingsValues
	SettingsBackgrounds
)

type SettingsRow struct {
	Index       int // stable option index, not the filtered or scrolled position
	Label       string
	Description string // inline supplementary text when there is room
	Suffix      string // right-aligned counter or status marker
}

type SettingsPane struct {
	Title    string
	Rows     []SettingsRow
	Selected int // option index, -1 for no selection
}

// SettingsView is a snapshot of the top settings dialog. The model still
// owns navigation, previews, actions and persistence.
type SettingsView struct {
	Kind, Title, Message, Filter, FilterLabel, Details, Status string
	Panes                                                      []SettingsPane // in SettingsColumn order
	Focus                                                      SettingsColumn
	WideDetails                                                bool
	Help, ApplyLabel, Placeholder                              string
	Edit, Mask, ScrollDetails                                  bool
	// LocalKeys go to the dialog while the filter has not been clicked.
	LocalKeys string
	// Bare draws one frame with borderless columns, an inline header row and
	// a help line, the way the painted model picker looked: title inside the
	// frame, no per-column boxes and no Apply/Close buttons.
	Bare         bool
	DetailsTitle string
}

type SettingsProvider interface{ NativeSettings() (SettingsView, bool) }

// PaintedDialogProvider reports that the top dialog is drawn by the old
// painted renderer (model.View) instead of a native tview view, so the
// frame must skip the native panel and workspace for that frame.
type PaintedDialogProvider interface{ PaintedDialog() bool }

type SettingsFilterMsg string

type SettingsPointerMsg struct {
	Column   SettingsColumn
	Index    int
	Activate bool
	Step     int // wheel direction (-1/+1); ignores Index and Activate
}
