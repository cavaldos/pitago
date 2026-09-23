// Package extension owns what pitago borrows from pi extensions at runtime.
//
// Pi's own extensions provide: repo commands (source "extension"), prompt
// templates ("prompt"), skills ("skill"), and interactive UI requests
// (extension_ui_request: select/confirm/input/editor/notify/setStatus/...).
// This package holds that protocol knowledge as pure helpers — it never
// touches UI state, so the dependency runs one way: app -> extension.
// UI state mutations stay in src/app (see handleUIRequest/answerDialog),
// builtin re-implementations in src/builtin.
package extension

import (
	"encoding/json"

	"pitago/src/pirpc"
)

// Sources an extension-provided command can come from.
const (
	SourceExtension = "extension"
	SourcePrompt    = "prompt"
	SourceSkill     = "skill"
	SourceBuiltin   = "builtin" // pitago-local (see src/builtin), never from RPC
)

// Summarize counts repo commands per source for the sidebar.
func Summarize(cmds []pirpc.RepoCommand) (ext, prompt, skill, builtin int) {
	for _, c := range cmds {
		switch c.Source {
		case SourceExtension:
			ext++
		case SourcePrompt:
			prompt++
		case SourceSkill:
			skill++
		case SourceBuiltin:
			builtin++
		}
	}
	return ext, prompt, skill, builtin
}

// ShouldAutoCancel reports UI requests pitago answers without asking:
// the editor falls back to agent defaults/timeout (free-text input gets a
// real dialog instead — extensions like pi-tasks drive creation flows
// through ui.input, which must not resolve empty).
func ShouldAutoCancel(method string) bool {
	return method == "editor"
}

// TitleFor fills the dialog title default per method.
func TitleFor(method, title string) string {
	if title != "" {
		return title
	}
	if method == "confirm" {
		return "Confirm"
	}
	return "Pi requests permission"
}

// OptionsFor fills the option defaults per method.
func OptionsFor(req pirpc.UIRequest) []string {
	if len(req.Options) > 0 {
		return req.Options
	}
	if req.Method == "confirm" {
		return []string{"Allow", "Decline"}
	}
	return []string{"Agree", "Decline"}
}

func boolPtr(b bool) *bool { return &b }

// Response builds the extension_ui_response for a permission dialog.
// choice < 0 means dismissed (cancelled); otherwise it indexes opts.
func Response(id, method string, choice int, opts []string) pirpc.Command {
	cmd := pirpc.Command{Type: "extension_ui_response", ID: id}
	switch method {
	case "select":
		if choice < 0 {
			cmd.Cancelled = boolPtr(true)
		} else if choice < len(opts) {
			v := opts[choice]
			cmd.Value = &v
		} else {
			cmd.Cancelled = boolPtr(true)
		}
	case "confirm":
		cmd.Confirmed = boolPtr(choice == 0)
	}
	return cmd
}

// IsDialogRequest reports extension_ui_request methods that open a dialog
// (select/confirm/input). Anything else (notify/setStatus/setWidget/…)
// never disrupts input routing. Unreadable payloads stay on the safe side
// (true = hold behind the open dialog, the historical behavior).
func IsDialogRequest(raw json.RawMessage) bool {
	var req struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return true
	}
	switch req.Method {
	case "select", "confirm", "input":
		return true
	}
	return false
}

// InputResponse builds the extension_ui_response for a free-text input
// dialog: Esc cancels, Enter submits the typed value (possibly empty — the
// extension decides what empty means).
func InputResponse(id, value string, cancelled bool) pirpc.Command {
	cmd := pirpc.Command{Type: "extension_ui_response", ID: id}
	if cancelled {
		cmd.Cancelled = boolPtr(true)
	} else {
		cmd.Value = &value
	}
	return cmd
}
