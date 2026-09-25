package pitago

// ResolveMouse maps "/mouse [on|off]" to the new state.
// Empty/unknown toggles (same rule as ParseMouseArg returning nil).
// Canonical impl — app.Model.ToggleMouse delegates here.
func ResolveMouse(arg string, current bool) bool {
	if want := ParseMouseArg(arg); want != nil {
		return *want
	}
	return !current
}
