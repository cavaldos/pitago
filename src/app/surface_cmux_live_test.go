package app

// LIVE integration test for the cmux pane host. It is skipped unless
// CMUX_LIVE_TEST=1 AND a real cmux answers, because it creates and closes
// real surfaces. Everything asserted here was first established by hand
// against cmux 0.64.25; see the comment block in surface_cmux.go for the
// five probe results this test encodes.
//
// The reason this test exists at all: the unit tests fake the CLI, so they
// cannot catch a handle FORM cmux does not accept. Every one of the three
// handle forms a reasonable person would pick turned out to be
// context-dependent, and only a real socket reveals that.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Parse ONLY the `OK workspace:N` line. Taking the first `workspace:N` in
// CombinedOutput would be one refactor away from closing the USER's
// workspace.
var (
	cmuxLiveWorkspace = regexp.MustCompile(`(?m)^OK (workspace:\d+)`)
	cmuxLiveSurface   = regexp.MustCompile(`(?m)^OK .*\b(surface:\d+)`)
	cmuxLivePane      = regexp.MustCompile(`(?m)^OK .*\b(pane:\d+)`)
)

func cmuxLiveSkip(t *testing.T) {
	t.Helper()
	if os.Getenv("CMUX_LIVE_TEST") != "1" {
		t.Skip("CMUX_LIVE_TEST != 1 — live cmux test skipped")
	}
	if err := cmuxAvailable(); err != nil {
		t.Skipf("cmux not reachable from this process: %v", err)
	}
}

// cmuxLive runs a cmux verb in a test, returning stdout+stderr.
func cmuxLive(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// CMUX_QUIET=1 suppresses the legacy-alias banner, which otherwise
	// shares the output with the `OK <ref>` line we parse. It is an env var,
	// not a flag: `cmux --quiet` is rejected as an unknown command.
	t.Setenv("CMUX_QUIET", "1")
	full := append(cmuxAuthArgs(), args...)
	out, err := exec.CommandContext(ctx, "cmux", full...).CombinedOutput()
	return string(out), err
}

// A host can name the surface it is running in, which is what lets a row
// with no handle of its own (an in-process child) still be focused. Proved
// against the real socket: the handle must be the actionable form, and
// focusing it must work.
func TestCmuxLiveCurrentHandleIsFocusable(t *testing.T) {
	cmuxLiveSkip(t)

	// Read identify independently first, so the assertion ties the returned
	// handle to the caller's own object rather than to a well-formed string
	// that happens to be wrong.
	out, err := cmuxLive(t, "identify")
	if err != nil {
		t.Fatalf("identify: %v (%s)", err, out)
	}
	var doc struct {
		Caller struct {
			WindowRef    string `json:"window_ref"`
			WorkspaceRef string `json:"workspace_ref"`
			PaneRef      string `json:"pane_ref"`
		} `json:"caller"`
		Focused struct {
			PaneRef string `json:"pane_ref"`
		} `json:"focused"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parse identify: %v", err)
	}
	want := doc.Caller.WindowRef + "/" + doc.Caller.WorkspaceRef + "/" + doc.Caller.PaneRef

	handle, err := cmuxCurrentHandle()
	if err != nil {
		t.Fatalf("currentHandle: %v", err)
	}
	if handle != want {
		t.Fatalf("currentHandle = %q, want the caller surface %q", handle, want)
	}
	parts, ok := cmuxSplitHandle(handle)
	if !ok {
		t.Fatalf("currentHandle returned an unaddressable handle: %q", handle)
	}
	if parts.window == "" {
		t.Fatalf("handle %q has no window segment; a window-less workspace ref resolves against the FOCUSED window", handle)
	}
	// And focus must land on THAT pane, not merely return without error.
	if err := cmuxTermRun("switch", handle); err != nil {
		t.Fatalf("focus on the current surface %s: %v", handle, err)
	}
	out, err = cmuxLive(t, "list-panes", "--workspace", parts.workspace, "--id-format", "both")
	if err != nil {
		t.Fatalf("list-panes after focus: %v (%s)", err, out)
	}
	landed := false
	for _, line := range strings.Split(out, "\n") {
		// Token-anchored: a bare Contains would let "pane:1" match "pane:11",
		// which is exactly the wrong-target proof this assertion exists for.
		if !cmuxLiveLineHasField(line, parts.target) {
			continue
		}
		if strings.Contains(line, "[focused]") {
			landed = true
		}
	}
	if !landed {
		t.Fatalf("focus did not land on the caller's pane %s: %q (focused elsewhere: %s)", parts.target, out, doc.Focused.PaneRef)
	}
	// The matcher must be able to say no, or the assertion above is decorative.
	if cmuxLiveLineHasField("* pane:999  [focused]", "pane:9") {
		t.Fatal("pane matcher is prefix-loose: pane:9 matched pane:999")
	}
}

// cmuxLiveLineHasField reports whether a listing line names a ref, comparing
// whole fields so a shorter ref cannot match a longer one.
func cmuxLiveLineHasField(line, ref string) bool {
	for _, f := range strings.Fields(line) {
		if f == "*" {
			continue
		}
		if f == ref {
			return true
		}
	}
	return false
}

// cmuxLiveHasSurface reports whether a surface id appears in a
// list-pane-surfaces listing, anchored on a token boundary so "surface:1"
// cannot match "surface:12".
func cmuxLiveHasSurface(listing, id string) bool {
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == id || (f == "*" && i+1 < len(fields) && fields[i+1] == id) {
				return true
			}
		}
	}
	return false
}

// cmuxLiveFocusedPane reports the currently focused pane, as a workspace/pane
// pair that focus-pane can address.
func cmuxLiveFocusedPane(t *testing.T) (ref struct{ workspace, pane string }) {
	t.Helper()
	out, err := cmuxLive(t, "identify")
	if err != nil {
		return ref
	}
	var doc struct {
		Focused struct {
			WorkspaceRef string `json:"workspace_ref"`
			PaneRef      string `json:"pane_ref"`
		} `json:"focused"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return ref
	}
	ref.workspace, ref.pane = doc.Focused.WorkspaceRef, doc.Focused.PaneRef
	return ref
}

// cmuxLiveWorkspaceByName finds a scratch workspace by title, for cleanup
// when the create output could not be parsed.
func cmuxLiveWorkspaceByName(t *testing.T, name string) string {
	t.Helper()
	out, err := cmuxLive(t, "workspace", "list")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) {
			if m := regexp.MustCompile(`workspace:\d+`).FindString(line); m != "" {
				return m
			}
		}
	}
	return ""
}

func TestCmuxLiveHandleIsWorkspaceScoped(t *testing.T) {
	cmuxLiveSkip(t)

	// Step 3 really does move focus. Remember where the user was and put it
	// back, so running this test does not leave them in a scratch workspace.
	priorFocus := cmuxLiveFocusedPane(t)
	t.Cleanup(func() {
		ws, pane := priorFocus.workspace, priorFocus.pane
		if ws == "" || pane == "" {
			t.Log("cleanup: could not read the prior focus; the focused pane was NOT restored")
			return
		}
		if _, err := cmuxLive(t, "focus-pane", "--workspace", ws, "--pane", pane); err != nil {
			t.Logf("cleanup: could not restore focus to %s/%s: %v", ws, pane, err)
		}
	})

	// A scratch workspace keeps every destructive verb away from the
	// workspace the user is actually working in.
	out, err := cmuxLive(t, "new-workspace", "--name", "pitago-cmux-live", "--description", "surface host live test", "--command", "sleep 300", "--focus", "false")
	if err != nil {
		t.Fatalf("new-workspace failed: %v (%s)", err, out)
	}
	// Cleanup is registered BEFORE the ref is validated, so a parse failure
	// (exactly when it happens) still cannot leak a workspace. The name is
	// the fallback handle for that case.
	var wsM string
	t.Cleanup(func() {
		if wsM == "" {
			wsM = cmuxLiveWorkspaceByName(t, "pitago-cmux-live")
			if wsM == "" {
				t.Log("cleanup: could not resolve the scratch workspace; close it manually")
				return
			}
		}
		if _, err := cmuxLive(t, "close-workspace", "--workspace", wsM); err != nil {
			t.Logf("cleanup: close-workspace %s: %v", wsM, err)
		}
	})
	if m := cmuxLiveWorkspace.FindStringSubmatch(out); m != nil {
		wsM = m[1]
	}
	if wsM == "" {
		t.Fatalf("no workspace ref in %q", out)
	}

	// A surface inside it, to act on.
	out, err = cmuxLive(t, "new-surface", "--workspace", wsM, "--type", "terminal", "--command", "sleep 300", "--no-focus")
	if err != nil {
		t.Fatalf("new-surface failed: %v (%s)", err, out)
	}
	var surfaceM, paneM string
	if m := cmuxLiveSurface.FindStringSubmatch(out); m != nil {
		surfaceM = m[1]
	}
	if m := cmuxLivePane.FindStringSubmatch(out); m != nil {
		paneM = m[1]
	}
	if surfaceM == "" || paneM == "" {
		// A diagnostic, not an index panic: a banner or wording change
		// upstream should read as a failure, not a stack trace.
		t.Fatalf("no refs in %q", out)
	}
	scopedSurface := wsM + "/" + surfaceM
	scopedPane := wsM + "/" + paneM
	// The test never closes this surface — that is the point — so cleanup
	// closes the whole scratch workspace and this is belt-and-braces only.
	t.Cleanup(func() {
		if surfaceM == "" {
			return
		}
		if _, err := cmuxLive(t, "close-surface", "--workspace", wsM, "--surface", surfaceM); err != nil {
			t.Logf("cleanup: close-surface %s: %v", scopedSurface, err)
		}
	})

	// 1. A BARE handle is refused by the binding, with an actionable message
	//    and no subprocess — this is the whole point of the scoping work.
	err = cmuxTermRun("switch", paneM)
	if err == nil {
		t.Fatal("bare pane ref was accepted; cmux would resolve it against the focused workspace")
	}
	if !strings.Contains(err.Error(), "not addressable") {
		t.Fatalf("bare-ref error is not actionable: %v", err)
	}
	if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "Invalid pane handle") {
		t.Fatalf("bare-ref error leaked a raw CLI message: %v", err)
	}

	// 2. A wrong-kind or multi-segment target is refused by US, so the user
	//    never sees cmux's raw "Surface ref not found" / "Invalid pane
	//    handle". Assert on OUR message, and on no subprocess being spawned.
	//    (Close is separately refused in step 4 — it is refused for every
	//    handle, so it is not in this list.)
	calls := 0
	oldExec := cmuxExec
	// Restored via defer, not a trailing statement: a Fatalf inside the loop
	// would otherwise leave the fake installed for the rest of the package.
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, _ ...string) (string, error) { calls++; return "OK", nil }
	for _, bad := range []struct{ action, handle string }{
		{"switch", wsM + "/" + surfaceM},       // surface target for a pane verb
		{"switch", wsM + "/tab:1/" + surfaceM}, // multi-segment tail
	} {
		err := cmuxTermRun(bad.action, bad.handle)
		if err == nil {
			t.Fatalf("%s accepted %q", bad.action, bad.handle)
		}
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "Invalid") {
			t.Fatalf("%s leaked a raw cmux message: %v", bad.action, err)
		}
	}
	if calls != 0 {
		t.Fatalf("refused handles still spawned %d subprocesses", calls)
	}

	// 3. The QUALIFIED surface handle focuses for real — and the effect is
	// checked, not just the absence of an error. A host that accepted the
	// argv and then no-opped would pass an error-only assertion.
	if err := cmuxTermRun("switch", scopedPane); err != nil {
		t.Fatalf("focus on qualified handle %s: %v", scopedPane, err)
	}
	out, err = cmuxLive(t, "list-panes", "--workspace", wsM, "--id-format", "both")
	if err != nil {
		t.Fatalf("list-panes after focus: %v (%s)", err, out)
	}
	focusedLine := ""
	for _, line := range strings.Split(out, "\n") {
		// Token-anchored: a bare Contains would let "pane:1" match "pane:12".
		if strings.Contains(line, " "+paneM) || strings.HasPrefix(strings.TrimSpace(line), paneM) {
			focusedLine = line
			break
		}
	}
	if focusedLine == "" || !strings.Contains(focusedLine, "[focused]") {
		t.Fatalf("focus did not land on %s: %q", paneM, out)
	}

	// 4. `close` is REFUSED, and nothing real is closed. This is the whole
	//    reason the binding is narrow: live against cmux 0.64.25,
	//    close-surface answered "OK surface:51" when asked to close
	//    surface:50, and surface:50 still resolved afterwards — success
	//    reported, wrong surface (or none) affected. Proving the refusal
	//    keeps that from ever being wired up, and keeps the test from
	//    destroying anything while it checks.
	if err := cmuxTermRun("close", scopedSurface); err == nil {
		t.Fatal("cmux bound close; close-surface cannot be trusted to close a named surface")
	} else if !strings.Contains(err.Error(), "binds focus only") {
		t.Fatalf("close refusal is not explanatory: %v", err)
	}
	// The refusal must not have touched the surface we made. `identify` is
	// NOT an existence check — it exits 0 for a surface that does not exist —
	// so the proof is presence in a real listing: if the close had worked,
	// this id would be gone from it.
	out, err = cmuxLive(t, "list-pane-surfaces", "--workspace", wsM, "--pane", paneM)
	if err != nil {
		t.Fatalf("list-pane-surfaces: %v (%s)", err, out)
	}
	if !cmuxLiveHasSurface(out, surfaceM) {
		t.Fatalf("refused close still removed %s: %q", scopedSurface, out)
	}
	// The presence check must be able to FAIL, or the assertion above is
	// decorative: a fabricated id must be absent from the same listing. This
	// is the guard against exactly the vacuous check a previous version of
	// this file shipped (`identify` exits 0 for a surface that never existed).
	if cmuxLiveHasSurface(out, "surface:999999") {
		t.Fatalf("listing contains a fabricated id, so presence proves nothing: %q", out)
	}
}

// The target check must accept a uuid, and it must not be a second copy of
// the uuid pattern: a duplicated one once carried an extra group and
// rejected every uuid while still looking correct in review.
func TestCmuxIsTargetAgreesWithUUIDPattern(t *testing.T) {
	u := "3f2a1b8c-1111-2222-3333-444455556666"
	if !cmuxUUID.MatchString(u) {
		t.Fatalf("fixture is not a uuid per cmuxUUID: %q", u)
	}
	if !cmuxIsTarget(u) {
		t.Fatalf("cmuxIsTarget rejected a valid uuid: %q", u)
	}
	for _, bad := range []string{"", "pane:", ":3", "pane:1/tab:2", "pane:1 ", "PANE:1", "../../x", "pane:1 --focus"} {
		if cmuxIsTarget(bad) {
			t.Errorf("cmuxIsTarget accepted %q", bad)
		}
	}
}

func TestCmuxSplitHandle(t *testing.T) {
	cases := []struct {
		handle    string
		window    string
		workspace string
		target    string
		wantOK    bool
	}{
		{"workspace:2/pane:3", "", "workspace:2", "pane:3", true},
		{"window:1/workspace:2/pane:3", "window:1", "workspace:2", "pane:3", true},
		{"workspace:2/3f2a1b8c-1111-2222-3333-444455556666", "", "workspace:2", "3f2a1b8c-1111-2222-3333-444455556666", true},
		// A multi-segment tail is not addressable by any cmux verb, so it is
		// not a split at all.
		{"workspace:2/tab:4/surface:5", "", "", "", false},
		{"pane:3", "", "", "", false},
		{"3f2a1b8c-1111-2222-3333-444455556666", "", "", "", false},
		// Hostile: a flag smuggled into the target, a traversal, a workspace
		// slot that is not a ref, an empty target.
		{"workspace:1/pane:1 --focus", "", "", "", false},
		{"workspace:1/../../x", "", "", "", false},
		{"workspace:1/pane:1/../../..", "", "", "", false},
		{"workspace:../etc/passwd", "", "", "", false},
		{"workspace:1/", "", "", "", false},
		{"workspace:1", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		got, ok := cmuxSplitHandle(c.handle)
		if ok != c.wantOK {
			t.Errorf("cmuxSplitHandle(%q) ok = %v, want %v", c.handle, ok, c.wantOK)
			continue
		}
		if ok && (got.window != c.window || got.workspace != c.workspace || got.target != c.target) {
			t.Errorf("cmuxSplitHandle(%q) = %+v, want window=%q workspace=%q target=%q", c.handle, got, c.window, c.workspace, c.target)
		}
	}
}
