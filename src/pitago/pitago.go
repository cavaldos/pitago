package pitago

import "strings"

// ScopeOf maps the /trajectory arg to a step tab. Unknown args become
// free-text pre-filter (the /login pattern), never an error.
// Canonical impl lives here (origin pitago); builtin/trajectory.go delegates.
func ScopeOf(arg string) (scope, filter string) {
	switch a := strings.ToLower(strings.TrimSpace(arg)); a {
	case "", "all":
		return "all", ""
	case "tools", "tool":
		return "tools", ""
	case "messages", "message", "user-only", "user":
		return "messages", ""
	default:
		return "all", strings.TrimSpace(arg)
	}
}

// ParseMouseArg parses "/mouse [on|off]" — empty toggles.
func ParseMouseArg(arg string) (want *bool) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on", "1", "true", "enable":
		b := true
		return &b
	case "off", "0", "false", "disable":
		b := false
		return &b
	default:
		return nil
	}
}
