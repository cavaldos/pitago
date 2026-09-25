package pitago

// Origin tag for this layer (mirrors builtin.OriginPitago).
const Origin = "pitago"

// State groups pitago-only fields currently living flat in app.Model.
// Target embed shape — not wired yet.
type State struct {
	Mouse        bool   // --mouse: terminal reports clicks
	UpdateAvail  string // latest tag when auto-check found newer
	HideThinking bool   // /settings: skip thinking blocks in chat
	ThemeName    string // active TUI theme
}
