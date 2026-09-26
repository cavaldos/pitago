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
	SourceBuiltin   = "builtin" // pi-parity re-implements (see src/builtin), never from RPC
	SourcePitago    = "pitago"  // pitago-only commands ([pitago] in the popup)
)

// Summarize counts repo commands per source for the sidebar.
// Pitago-only commands fold into the builtin bucket (counts stay comparable).
func Summarize(cmds []pirpc.RepoCommand) (ext, prompt, skill, builtin int) {
	for _, c := range cmds {
		switch c.Source {
		case SourceExtension:
			ext++
		case SourcePrompt:
			prompt++
		case SourceSkill:
			skill++
		case SourceBuiltin, SourcePitago:
			builtin++
		}
	}
	return ext, prompt, skill, builtin
}

// IsTextMethod reports UI requests that ask the user to type a value, i.e.
// the ones pitago renders with the free-text dialog (typing, backspace,
// space, Ctrl+V paste, Enter submits / Esc cancels).
//
// pi parity: in RPC mode pi implements `editor` through
// createExtensionInputComponent (dist/modes/rpc/rpc-mode.js), exactly like
// `input` — it opens a real editor dialog and resolves {value}, a cancel
// resolves {cancelled:true}. It is therefore NOT auto-cancelled here:
// cancelling without asking hands the extension undefined and pushes it
// onto its default/timeout branch, which is a behaviour change, not a
// rendering one. Extensions like pi-tasks drive creation flows through
// ui.input for the same reason.
func IsTextMethod(method string) bool {
	return method == "input" || method == "editor"
}

// TextTitleFor fills the free-text dialog title default per method.
func TextTitleFor(method, title string) string {
	if title != "" {
		return title
	}
	if method == "editor" {
		return "Editor"
	}
	return "Input"
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
		out := make([]string, len(req.Options))
		for i, value := range req.Options {
			if option, ok := pirpc.DecodeSelectOption(value); ok {
				out[i] = option.Title
			} else {
				out[i] = value
			}
		}
		return out
	}
	if req.Method == "confirm" {
		return []string{"Allow", "Decline"}
	}
	return []string{"Agree", "Decline"}
}

// DescriptionsFor returns rich select descriptions parallel to OptionsFor.
// Legacy strings, confirms, and malformed private values deliberately get no
// description and therefore keep the generic dialog behavior.
func DescriptionsFor(req pirpc.UIRequest) []string {
	if !pirpc.IsAskUserSelect(req) {
		return nil
	}
	descs := make([]string, len(req.Options))
	for i, value := range req.Options {
		if option, ok := pirpc.DecodeSelectOption(value); ok {
			descs[i] = option.Description
		}
	}
	return descs
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

// IsDialogMethod reports extension_ui_request methods that block for an
// extension_ui_response (select/confirm/input/editor, per pi's RPC
// extension-UI subprotocol). Fire-and-forget methods
// (notify/setStatus/setWidget/setTitle/set_editor_text) never expect one.
func IsDialogMethod(method string) bool {
	switch method {
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// IsFireAndForget reports extension_ui_request methods pi never waits on:
// the client may display the info or ignore it, no response is expected.
func IsFireAndForget(method string) bool {
	switch method {
	case "notify", "setStatus", "setWidget", "setTitle", "set_editor_text":
		return true
	}
	return false
}

// FallbackResponse builds a safe cancellation for an extension_ui_request
// pitago cannot render (unknown future method, custom widget payload).
// Dialog callers receive undefined/false and fall back to defaults; pi
// ignores responses with no pending request, so sending this for a
// fire-and-forget-like method is a harmless no-op instead of a hang.
func FallbackResponse(id string) pirpc.Command {
	return pirpc.Command{Type: "extension_ui_response", ID: id, Cancelled: boolPtr(true)}
}

// IsDialogRequest reports extension_ui_request methods that open a dialog
// (select/confirm/input/editor). Anything else (notify/setStatus/setWidget/…)
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
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// TextResponse builds the extension_ui_response for a free-text dialog
// (ui.input and ui.editor, the same wire shape as pi's): Esc cancels, Enter
// submits the typed value (possibly empty — the extension decides what empty
// means). The extension reads it back as {value} or {cancelled:true}.
func TextResponse(id, value string, cancelled bool) pirpc.Command {
	cmd := pirpc.Command{Type: "extension_ui_response", ID: id}
	if cancelled {
		cmd.Cancelled = boolPtr(true)
	} else {
		cmd.Value = &value
	}
	return cmd
}
