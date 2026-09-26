package app

// cmux pane-host binding for the subagent herd. cmux addresses panes by a
// WORKSPACE-QUALIFIED handle — `workspace:2/pane:3` or `workspace:2/<uuid>` —
// which is a different namespace from Orca's `term_<uuid>` handles; ownership
// is claimed by pattern, not by prefix. A bare ref or bare uuid is still
// claimed (so routing stays right) but is not actionable, and is refused. The ONE verb bound is focus; close is refused on
// live evidence (see cmuxHandleActionable), and no interrupt verb is bound.
// Nothing here bulk-closes and nothing here kills processes.
//
// Every cmux target is WORKSPACE-SCOPED, verified live against cmux 0.64.25:
//   focus-pane --pane pane:1                        -> OK pane:1 workspace:1
//   focus-pane --pane pane:2   (owned by ws2)       -> Error: not_found
//   focus-pane --pane <pane-uuid from ws2>          -> Error: not_found
//   focus-pane --pane workspace:2/pane:2            -> Error: Invalid pane handle
//   focus-pane --workspace window:1/workspace:2 ... -> Error: Invalid workspace handle
//   focus-pane --window window:1 --workspace ws2 --pane pane:2 -> OK
// A uuid is therefore NOT globally unique for addressing, a short ref
// silently resolves against the FOCUSED workspace (so it can act on the
// wrong pane), and a path-style handle is rejected outright. The only form
// that addresses a specific target is one that CARRIES its workspace, sent
// as separate flags. A handle without that scope is refused, not guessed at.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// cmuxUUID matches 8-4-4-4-12 hex.
var cmuxUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// cmuxRef matches cmux short refs and short-ref paths:
// `pane:3`, `window:1/workspace:2/pane:3`, optionally with a `tab:` or
// `surface:` segment. Paths are fixed-width integers only, so a hostile
// handle cannot smuggle a flag.
var cmuxRef = regexp.MustCompile(`^(?:[a-z]+:\d+/)*[a-z]+:\d+$`)

// cmuxScopedHandle matches a handle that carries the workspace it lives in:
// `workspace:2/pane:3`, `window:1/workspace:2/pane:3`, or `workspace:2/<uuid>`.
// This is the only form cmux can address, and it must be SPLIT here into
// --window/--workspace + target, because cmux rejects a path in the
// --pane/--surface value itself.
var cmuxScopedHandle = regexp.MustCompile(`^(?:(window:\d+)/)?workspace:(\d+)/([^/]+)$`)

// cmuxShortTarget matches one in-workspace target segment that is a short
// ref. A uuid target is matched by cmuxUUID rather than a second copy of the
// pattern: a duplicated uuid regex once carried an extra group and silently
// rejected every uuid, which no eyeball check catches.
var cmuxShortTarget = regexp.MustCompile(`^[a-z]+:\d+$`)

// cmuxIsTarget reports whether a single segment is something cmux accepts as
// a --pane/--surface value: a short ref, or a uuid. Single segment by
// construction — cmux's own error text says it expects "UUID, ref like
// pane:1, or index", so anything containing a slash is unaddressable and
// must be refused rather than forwarded.
func cmuxIsTarget(seg string) bool {
	return cmuxShortTarget.MatchString(seg) || cmuxUUID.MatchString(seg)
}

// cmuxHandleParts is a handle decomposed into what cmux actually accepts:
// an optional window, a required workspace, and a single target segment.
type cmuxHandleParts struct {
	window    string
	workspace string
	target    string
}

// cmuxSplitHandle decomposes a handle and VALIDATES every part, so safety is
// a property of this function rather than of the call order that happens to
// guard it. A handle cmux cannot address is reported as not-scoped, and the
// caller refuses it with the actionable message.
//
// Multi-segment tails are rejected on purpose. `tab:4/surface:5` is a legal
// cmux path, but no verb accepts it: `focus-pane --pane "tab:4/surface:5"`
// answers "Invalid pane handle", and cmux exposes no --tab flag for these
// verbs. Only the LAST segment is ever addressable, so a handle that buries
// its target under more path is refused instead of half-honoured.
func cmuxSplitHandle(handle string) (cmuxHandleParts, bool) {
	m := cmuxScopedHandle.FindStringSubmatch(handle)
	if m == nil {
		return cmuxHandleParts{}, false
	}
	parts := cmuxHandleParts{window: m[1], workspace: "workspace:" + m[2], target: m[3]}
	if !cmuxIsTarget(parts.target) {
		return cmuxHandleParts{}, false
	}
	return parts, true
}

// cmuxHandleActionable checks that a handle can drive a specific verb before
// anything is spawned, and before the herd has already dismissed the row.
//
// Only `switch` (focus) is bound. `close` is refused, on evidence: against
// cmux 0.64.25, `close-surface --workspace K --surface S` returned
// `OK surface:51` when asked to close `surface:50`, and `surface:50` still
// resolved via `cmux identify` afterwards; an index form returned OK with no
// change at all; and there is no `close-pane` verb, with `close-workspace`
// far too broad to ever bind. A verb that can report success while closing
// something else is worse than no verb, so the herd says so and leaves the
// pane to cmux.
func cmuxHandleActionable(action, handle string) error {
	if !cmuxClaimsHandle(handle) {
		return fmt.Errorf("cmux: %q is not a cmux handle", handle)
	}
	switch action {
	case "switch":
	case "close":
		return fmt.Errorf("cmux binds focus only — close-surface cannot be trusted to close the surface you name (verified live against cmux 0.64.25: it can report OK while closing a different one), so the herd will not close panes for you. Close it in cmux; the row is still dismissed.")
	default:
		return fmt.Errorf("cmux: unsupported action %q", action)
	}
	parts, ok := cmuxSplitHandle(handle)
	if !ok {
		return fmt.Errorf("cmux: handle %q is not addressable — cmux resolves targets against the focused workspace and rejects a path in the target itself, so this handle could hit the wrong pane; re-bind the row with a workspace-qualified handle (e.g. workspace:2/pane:3)", handle)
	}
	if !strings.HasPrefix(parts.target, "pane:") && !cmuxUUID.MatchString(parts.target) {
		return fmt.Errorf("cmux: focus needs a pane:… target, but the handle points at %q", parts.target)
	}
	return nil
}

// cmuxAuthArgs returns the auth flag when CMUX_SOCKET_PASSWORD is set.
// cmux refuses any process that did not start inside cmux ("only processes
// started inside cmux can connect"), so a pitago hosted in another
// multiplexer needs the socket password to drive cmux panes. Without it the
// binding simply degrades: availability fails and the herd says so.
func cmuxAuthArgs() []string {
	if pw := strings.TrimSpace(os.Getenv("CMUX_SOCKET_PASSWORD")); pw != "" {
		return []string{"--password", pw}
	}
	return nil
}

// cmuxAvailable reports whether the cmux CLI is installed and its runtime
// is reachable. It runs on the UI thread at most once per action, so the
// budget is deliberately small: a host that cannot answer in 1s is treated
// as absent and the overlay degrades. Never fail the herd over it.
var cmuxAvailable = func() error {
	if _, err := exec.LookPath("cmux"); err != nil {
		return errNoSurfaceCtl
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	args := append(cmuxAuthArgs(), "ping")
	if out, err := exec.CommandContext(ctx, "cmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("cmux unreachable: %w (%s)", err, firstLine(string(out), 120))
	}
	return nil
}

// cmuxExec is the single subprocess seam used by cmuxTermRun. Unit tests
// swap it to assert the exact argv without needing a live cmux; the live
// path itself is covered by TestCmuxLiveHandleIsWorkspaceScoped, which only
// runs inside cmux.
var cmuxExec = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "cmux", args...).CombinedOutput()
	return string(out), err
}

// cmuxTermRun executes the one cmux verb the herd binds: "switch" focuses a
// pane. Availability is not re-probed here: routing already proved it, and
// probing again would put a second subprocess on the UI thread.
//
// The handle is validated by cmuxHandleActionable first — the same check the
// herd runs before dismissing a row — so this path cannot reach a wrong
// target even if a caller skips the precheck.
var cmuxTermRun = func(action, handle string) error {
	if err := cmuxHandleActionable(action, handle); err != nil {
		return err
	}
	parts, _ := cmuxSplitHandle(handle)
	var args []string
	switch action {
	case "switch":
		args = []string{"focus-pane"}
		if parts.window != "" {
			args = append(args, "--window", parts.window)
		}
		args = append(args, "--workspace", parts.workspace, "--pane", parts.target)
	default:
		return fmt.Errorf("cmux: unsupported action %q", action)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args = append(cmuxAuthArgs(), args...)
	out, err := cmuxExec(ctx, args...)
	if err != nil {
		return fmt.Errorf("cmux %s: %w (%s)", action, err, firstLine(out, 300))
	}
	return nil
}

// cmuxCurrentHandle names the surface THIS process is running in, so a row
// with no handle of its own — an in-process child, which shares the parent's
// terminal — can still be focused. `cmux identify` reports the caller's own
// window/workspace/pane, and all three segments are composed: a window-less
// `workspace:N` resolves against the FOCUSED window, which is the same
// wrong-pane hazard the whole scoping work exists to prevent. The result is
// validated through the same split as every other handle, so an unexpected
// response is refused rather than forwarded.
//
// The 1s budget matches cmuxAvailable and is deliberate: this runs on the UI
// thread inside a keypress handler, where a host that cannot answer promptly
// is worth treating as unable to answer. It is only consulted for a row with
// no handle, and only when cmux already answered a ping moments earlier — so
// the worst case for one keypress is ping(1s) + identify(1s) = 2s, against a
// measured cost of ~20ms for each.
var cmuxCurrentHandle = func() (string, error) {
	// Tree membership first, and it must be proven from the ENVIRONMENT.
	// cmux injects these into every process it spawns, so their presence is
	// the same evidence the socket ACL uses. Without this check the socket
	// password is enough to make a pitago running under Orca ask cmux where
	// it is: identify would answer, truthfully, about a caller cmux does not
	// host, and focusing that row would yank the user into another
	// application. Fails closed to the old "in-process (no pane)" notice.
	if !cmuxInTree() {
		return "", fmt.Errorf("cmux: this process was not started inside cmux, so it has no cmux surface of its own")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := cmuxExec(ctx, append(cmuxAuthArgs(), "identify")...)
	if err != nil {
		return "", fmt.Errorf("cmux: cannot identify the current surface: %s", firstLine(out, 160))
	}
	var doc struct {
		Caller struct {
			WindowRef        string `json:"window_ref"`
			WorkspaceRef     string `json:"workspace_ref"`
			PaneRef          string `json:"pane_ref"`
			SurfaceType      string `json:"surface_type"`
			IsBrowserSurface bool   `json:"is_browser_surface"`
		} `json:"caller"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return "", fmt.Errorf("cmux: cannot read the current surface: %s", firstLine(out, 160))
	}
	c := doc.Caller
	if c.WindowRef == "" || c.WorkspaceRef == "" || c.PaneRef == "" {
		return "", fmt.Errorf("cmux: identify did not report a window/workspace/pane for this process")
	}
	// Only a terminal caller is a surface a subagent can be said to live in.
	if c.IsBrowserSurface || (c.SurfaceType != "" && c.SurfaceType != "terminal") {
		return "", fmt.Errorf("cmux: this process is not in a terminal surface (surface_type=%q)", c.SurfaceType)
	}
	handle := c.WindowRef + "/" + c.WorkspaceRef + "/" + c.PaneRef
	if _, ok := cmuxSplitHandle(handle); !ok {
		return "", fmt.Errorf("cmux: the current surface %q is not addressable", handle)
	}
	return handle, nil
}

// cmuxInTree reports whether this process was started by cmux, from the
// environment cmux injects into everything it spawns. This is deliberately
// separate from CMUX_SOCKET_PASSWORD, which grants ACCESS to the socket and
// says nothing about whether cmux is hosting us.
var cmuxInTree = func() bool {
	for _, k := range []string{
		"CMUXLAYER_PATH",
		"CMUX_SHELL_INTEGRATION_DIR",
		"CMUX_BUNDLED_CLI_PATH",
		"CMUX_BUNDLE_ID",
	} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// cmuxSurfaceCtl is the cmux binding. Handles must be workspace-qualified
// (`workspace:2/pane:3`, `window:1/workspace:2/pane:3`, or `workspace:2/<uuid>`)
// to be actionable; a bare ref or uuid is still claimed as cmux's, so routing
// stays right and the refusal is the actionable one. precheck lets the herd
// refuse a doomed close BEFORE it dismisses the row, so a bad handle cannot
// leave an orphaned pane with no row to find it from. It binds no interrupt
// verb, so the x prompt falls through to the host-agnostic SIGINT path and
// the X fallback offers no close-by-discovery flow.
func cmuxSurfaceCtl() surfaceCtl {
	return surfaceCtl{
		name:          "cmux",
		available:     cmuxAvailable,
		claim:         cmuxClaimsHandle,
		precheck:      cmuxHandleActionable,
		currentHandle: cmuxCurrentHandle,
		run:           cmuxTermRun,
		hints: surfaceHints{
			// cmux has no "find my pane" verb that a human can run, and no
			// interrupt verb at all. The empty list hint is deliberate: the
			// failure notice then says the pane was left untouched with no
			// invented command, rather than showing a verb that does not
			// exist for this host.
			list: "cmux list-panes --id-format both (match the subagent, then re-bind with a workspace-qualified handle)",
		},
	}
}

// cmuxClaimsHandle reports whether a handle is in cmux's namespace. Both
// scoped and bare forms are claimed: ownership is a routing question, and
// the run path refuses an unscoped handle with an actionable message rather
// than pretending the namespace did not match.
func cmuxClaimsHandle(handle string) bool {
	if handle == "" || strings.HasPrefix(handle, "term_") {
		return false
	}
	if cmuxUUID.MatchString(handle) || cmuxRef.MatchString(handle) {
		return true
	}
	// A workspace-qualified handle whose target is a uuid is cmux's as well:
	// neither bare pattern can see a uuid sitting inside a path, so without
	// this the claim gate would reject a handle cmux can address perfectly
	// well. Routed through the same validating split, so this cannot widen
	// what is accepted.
	if _, ok := cmuxSplitHandle(handle); ok {
		return true
	}
	return false
}
