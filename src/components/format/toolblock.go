// Tool-block classification: the two pure decisions the chat renderer
// needs before it can pick a frame color and a header accent — what kind
// of work a tool does, and what execution state it is in.
//
// Both are string mapping with no colors and no lipgloss, so the only
// place that resolves a theme token stays src/app. The kind is derived
// from the same tool name the block header label is derived from, so
// the two can never disagree.
package format

import "strings"

// Canonical tool kinds. The renderer tints a tool block's header with
// the accent its kind maps to, so a read never looks like a write.
const (
	KindRead   = "read"
	KindWrite  = "write"
	KindEdit   = "edit"
	KindShell  = "shell"
	KindDir    = "cd"
	KindSearch = "search"
	KindOther  = "other"
)

// Canonical execution states for a tool block. Neutral is the fourth
// treatment: output that carries no execution state at all (a local
// shell echo, a result whose status this build does not know).
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusError   = "error"
	StatusNeutral = "neutral"
)

// ToolKind maps a tool name onto the kind that drives its header
// accent. Unknown tools fall back to KindOther rather than guessing:
// a muted header is honest, a wrong-colored one is not.
func ToolKind(tool string) string {
	switch normTool(tool) {
	case "read", "view", "cat", "readfile", "readmanyfiles", "notebookread":
		return KindRead
	case "write", "create", "new", "createfile", "writefile", "mkdir":
		return KindWrite
	case "edit", "multiedit", "patch", "replace", "strreplace", "strreplaceeditor":
		return KindEdit
	case "bash", "powershell", "shell", "sh", "zsh", "run", "runcommand",
		"exec", "terminal", "bashoutput":
		return KindShell
	case "cd", "chdir", "changedir", "workdir":
		return KindDir
	case "grep", "ffgrep", "search", "glob", "fffind", "find", "ls",
		"list", "listdir", "tree":
		return KindSearch
	}
	return KindOther
}

// ToolStatusClass folds a raw tool status into one of the four render
// states. The app only ever writes "running" / "done" / "error"; the
// wider spellings are here so an upstream rename still colors right.
//
// An empty status counts as running, because a tool block is created
// running (see ensureTool) and its header detail line already says
// "running…" — the fill must not disagree with the text. Anything this
// build does not recognize is neutral, so an unknown state never
// paints a success or failure tint it has not earned.
func ToolStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "ok", "success", "succeeded", "complete", "completed":
		return StatusSuccess
	case "error", "failed", "failure", "fail":
		return StatusError
	case "", "running", "pending", "queued", "started", "streaming",
		"in_progress", "inprogress":
		return StatusRunning
	}
	return StatusNeutral
}

// normTool lowercases a tool name and drops a vendor prefix
// ("functions.read" → "read") so a namespaced MCP tool still classifies
// by its verb.
func normTool(tool string) string {
	t := strings.ToLower(strings.TrimSpace(tool))
	if i := strings.LastIndexAny(t, ".:/"); i >= 0 && i+1 < len(t) {
		t = t[i+1:]
	}
	return t
}
