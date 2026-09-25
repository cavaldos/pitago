package ext

import "strings"

// ContainsPlan matches plan-mode text (menu title/options, setStatus).
// Canonical impl — app/palette.go delegates here.
func ContainsPlan(s string) bool {
	return strings.Contains(strings.ToLower(s), "plan")
}

// MenuHasPlan reports whether an extension select/confirm menu is a
// plan-mode menu from title/message/options.
func MenuHasPlan(title, message string, options []string) bool {
	if ContainsPlan(title) || ContainsPlan(message) {
		return true
	}
	for _, o := range options {
		if ContainsPlan(o) {
			return true
		}
	}
	return false
}

// ShouldLatchPlan maps a plan-menu choice to the live latch value.
// Start/Enable/Enter latch on; Stop/Exit/Leave/End/Disable/Off latch off.
// Returns (value, ok): ok=false means the choice is not a plan toggle.
func ShouldLatchPlan(choice string) (bool, bool) {
	sel := strings.ToLower(choice)
	if !strings.Contains(sel, "plan") {
		return false, false
	}
	switch {
	case strings.Contains(sel, "start") || strings.Contains(sel, "enable") || strings.Contains(sel, "enter"):
		return true, true
	case strings.Contains(sel, "stop") || strings.Contains(sel, "exit") || strings.Contains(sel, "leave") ||
		strings.Contains(sel, "end") || strings.Contains(sel, "disable") || strings.Contains(sel, "off"):
		return false, true
	}
	return false, false
}
