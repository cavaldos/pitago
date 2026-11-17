package ev

// PickerItem is one row of a native list picker.
type PickerItem struct {
	ID       string
	Label    string // may carry ANSI; first line only
	Hint     string
	Section  string
	Disabled bool
}

// OpenPickerMsg asks the runtime to show a native list picker (filterable,
// mouse-first). The pick comes back as PickerResultMsg with the same Tag.
type OpenPickerMsg struct {
	Tag        string
	Title      string
	Message    string
	Footer     string
	Items      []PickerItem
	Selected   string // initially highlighted item ID
	Filterable bool
	Filter     string // initial filter text
}

// PickerResultMsg reports the outcome of the picker opened with Tag.
// Cancelled is set for Esc / Ctrl+C / click outside / ClosePickerMsg.
type PickerResultMsg struct {
	Tag       string
	ID        string
	Cancelled bool
}

// ClosePickerMsg closes the picker opened with Tag, if it is still open
// (the model dropped the dialog behind it by other means).
type ClosePickerMsg struct{ Tag string }
