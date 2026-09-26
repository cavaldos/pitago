package app

import (
	"context"
	"strings"
	"testing"
)

func TestCmuxClaimsHandleNamespaces(t *testing.T) {
	cmux := []string{
		"01a0da54-f68c-735c-91b2-cc70d48b8a12",
		"pane:3",
		"window:1/workspace:2/pane:3",
		"window:1/workspace:2/tab:4/surface:5",
	}
	for _, h := range cmux {
		if !cmuxClaimsHandle(h) {
			t.Errorf("cmux should claim %q", h)
		}
	}
	// Orca's namespace and anything flag-shaped belongs to neither.
	for _, h := range []string{
		"term_2ac3c979-8e04-41cd-bd5b-0aeeb74c46bf",
		"",
		"pane:3; rm -rf /",
		"pane:x",
		"$(whoami)",
		"/dev/tty",
	} {
		if cmuxClaimsHandle(h) {
			t.Errorf("cmux must not claim %q", h)
		}
	}
}

func TestSurfaceCtlForHandleRoutesByOwner(t *testing.T) {
	old := surfaceControllers
	defer func() { surfaceControllers = old }()
	up := func() error { return nil }
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{
			{name: "cmux", available: up, claim: cmuxClaimsHandle, run: func(string, string) error { return nil }},
			{name: "orca", available: up, claim: func(h string) bool { return strings.HasPrefix(h, "term_") }, run: func(string, string) error { return nil }},
		}
	}
	if ctl := surfaceCtlForHandle("term_2ac3c979-8e04-41cd-bd5b-0aeeb74c46bf"); ctl == nil || ctl.name != "orca" {
		t.Fatalf("orca handle routed to %+v", ctl)
	}
	if ctl := surfaceCtlForHandle("window:1/workspace:2/pane:3"); ctl == nil || ctl.name != "cmux" {
		t.Fatalf("cmux ref routed to %+v", ctl)
	}
	if ctl := surfaceCtlForHandle("01a0da54-f68c-735c-91b2-cc70d48b8a12"); ctl == nil || ctl.name != "cmux" {
		t.Fatalf("cmux uuid routed to %+v", ctl)
	}
	// A handle no binding claims: no host, so callers degrade.
	if ctl := surfaceCtlForHandle("ghost-handle"); ctl != nil {
		t.Fatalf("unknown handle should not resolve, got %+v", ctl)
	}
	// Right namespace but the host is gone: still no host.
	surfaceControllers = func() []surfaceCtl {
		return []surfaceCtl{{name: "cmux", available: func() error { return errNoSurfaceCtl }, claim: cmuxClaimsHandle, run: func(string, string) error { return nil }}}
	}
	if ctl := surfaceCtlForHandle("pane:3"); ctl != nil {
		t.Fatalf("uninstalled host must not be used, got %+v", ctl)
	}
}

func TestCmuxTermRunRejectsUnknownAction(t *testing.T) {
	oldAvail, oldRun := cmuxAvailable, cmuxTermRun
	defer func() { cmuxAvailable, cmuxTermRun = oldAvail, oldRun }()
	cmuxAvailable = func() error { return nil }
	// Closed verb set: only switch and close are bound, so a typo or a new
	// call site cannot smuggle an arbitrary cmux command through the herd.
	// The handle is a well-formed, addressable one so the check that guards
	// this call site is the verb set, not the handle grammar.
	if err := cmuxTermRun("delete-everything", "workspace:2/pane:3"); err == nil ||
		!strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("unknown action should be refused, got %v", err)
	}
}

// An unscoped handle is refused before any subprocess runs. cmux resolves
// targets against the FOCUSED workspace, so acting on a bare ref can hit the
// wrong pane — verified live against cmux 0.64.25, where `focus-pane --pane
// pane:1` returned `OK pane:1 workspace:1` for a completely different pane.
func TestCmuxTermRunRefusesUnscopedHandle(t *testing.T) {
	calls := 0
	oldExec := cmuxExec
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, _ ...string) (string, error) {
		calls++
		return "OK", nil
	}
	for _, action := range []string{"switch"} {
		err := cmuxTermRun(action, "pane:1")
		if err == nil {
			t.Fatalf("%s accepted an unscoped handle", action)
		}
		if !strings.Contains(err.Error(), "not addressable") {
			t.Fatalf("%s error is not actionable: %v", action, err)
		}
		for _, leak := range []string{"not_found", "Invalid pane handle", "exit status"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("%s leaked a raw CLI message %q: %v", action, leak, err)
			}
		}
	}
	if calls != 0 {
		t.Fatalf("unscoped handles reached the CLI %d times", calls)
	}
}

// A qualified handle must be SPLIT into --window/--workspace + target: cmux
// rejects a path in the --pane/--surface value itself ("Invalid pane
// handle"), and rejects a window: segment inside --workspace too ("Invalid
// workspace handle"). Verified live.
func TestCmuxTermRunSplitsScopedHandle(t *testing.T) {
	var got []string
	oldExec := cmuxExec
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, args ...string) (string, error) {
		got = args
		return "OK", nil
	}
	if err := cmuxTermRun("switch", "window:1/workspace:2/pane:3"); err != nil {
		t.Fatalf("switch on scoped handle: %v", err)
	}
	want := "focus-pane --window window:1 --workspace workspace:2 --pane pane:3"
	if strings.Join(got, " ") != want {
		t.Fatalf("switch args = %q, want %q", strings.Join(got, " "), want)
	}
	// A uuid target is valid for focus.
	if err := cmuxTermRun("switch", "workspace:2/3f2a1b8c-1111-2222-3333-444455556666"); err != nil {
		t.Fatalf("focus on uuid target: %v", err)
	}
	want = "focus-pane --workspace workspace:2 --pane 3f2a1b8c-1111-2222-3333-444455556666"
	if strings.Join(got, " ") != want {
		t.Fatalf("uuid focus args = %q, want %q", strings.Join(got, " "), want)
	}
}

// currentHandle composes the caller's OWN window/workspace/pane. A
// window-less workspace ref would resolve against the focused window — the
// same wrong-pane hazard the split exists to prevent — so all three segments
// are required and asserted here, not just in the live test.
func TestCmuxCurrentHandleUsesCallerWindow(t *testing.T) {
	var got []string
	oldExec, oldTree := cmuxExec, cmuxInTree
	defer func() { cmuxExec, cmuxInTree = oldExec, oldTree }()
	cmuxInTree = func() bool { return true }
	cmuxExec = func(_ context.Context, args ...string) (string, error) {
		got = args
		return `{"caller":{"window_ref":"window:2","workspace_ref":"workspace:3","pane_ref":"pane:4","surface_ref":"surface:9"},
		          "focused":{"window_ref":"window:1","workspace_ref":"workspace:1","pane_ref":"pane:1"}}`, nil
	}
	h, err := cmuxCurrentHandle()
	if err != nil {
		t.Fatalf("currentHandle: %v", err)
	}
	if h != "window:2/workspace:3/pane:4" {
		t.Fatalf("handle = %q, want the caller's window/workspace/pane", h)
	}
	parts, ok := cmuxSplitHandle(h)
	if !ok || parts.window != "window:2" || parts.workspace != "workspace:3" || parts.target != "pane:4" {
		t.Fatalf("composed handle does not split back: %+v ok=%v", parts, ok)
	}
	if len(got) == 0 || got[len(got)-1] != "identify" {
		t.Fatalf("argv = %v, want it to end in identify", got)
	}
}

// A response that omits any of the three segments is refused, not guessed at.
func TestCmuxCurrentHandleRefusesIncompleteIdentify(t *testing.T) {
	oldExec, oldTree := cmuxExec, cmuxInTree
	defer func() { cmuxExec, cmuxInTree = oldExec, oldTree }()
	cmuxInTree = func() bool { return true }
	for _, body := range []string{
		`{"caller":{}}`,
		`{"caller":{"window_ref":"window:1","pane_ref":"pane:1"}}`,
		`{"caller":{"window_ref":"window:1","workspace_ref":"workspace:1"}}`,
		`not json at all`,
		`{"caller":{"window_ref":"--focus","workspace_ref":"workspace:1","pane_ref":"pane:1"}}`,
	} {
		cmuxExec = func(_ context.Context, _ ...string) (string, error) { return body, nil }
		if h, err := cmuxCurrentHandle(); err == nil {
			t.Errorf("accepted %q as %q", body, h)
		}
	}
}

// The current surface is the user's own window: never closable, and never
// spawnable. This needs no cmux socket, so it lives here rather than behind
// the live gate.
func TestCmuxCurrentHandleIsNeverClosable(t *testing.T) {
	oldExec, oldTree := cmuxExec, cmuxInTree
	defer func() { cmuxExec, cmuxInTree = oldExec, oldTree }()
	cmuxInTree = func() bool { return true }
	cmuxExec = func(_ context.Context, args ...string) (string, error) {
		return `{"caller":{"window_ref":"window:1","workspace_ref":"workspace:1","pane_ref":"pane:1"}}`, nil
	}
	h, err := cmuxCurrentHandle()
	if err != nil {
		t.Fatalf("currentHandle: %v", err)
	}
	calls := 0
	cmuxExec = func(_ context.Context, _ ...string) (string, error) { calls++; return "OK", nil }
	if err := cmuxTermRun("close", h); err == nil {
		t.Fatal("the current surface was closable; it is the user's own window")
	}
	if calls != 0 {
		t.Fatalf("a refused close still spawned %d subprocesses", calls)
	}
}

// The socket password grants ACCESS, not identity. A pitago running under
// another terminal can open this socket, and identify will answer truthfully
// about a caller cmux does not host — so without the in-tree check, `f` on a
// handle-less row would focus a pane in a different application. Provenance
// comes from the environment cmux injects, which is the same evidence the
// socket ACL uses.
func TestCmuxCurrentHandleNeedsToActuallyBeInCmux(t *testing.T) {
	oldExec, oldTree := cmuxExec, cmuxInTree
	defer func() { cmuxExec, cmuxInTree = oldExec, oldTree }()
	asks := 0
	cmuxExec = func(_ context.Context, _ ...string) (string, error) {
		asks++
		return `{"caller":{"window_ref":"window:1","workspace_ref":"workspace:1","pane_ref":"pane:1","surface_type":"terminal","is_browser_surface":false}}`, nil
	}
	// Not in cmux, but the password lets the socket answer anyway.
	cmuxInTree = func() bool { return false }
	t.Setenv("CMUX_SOCKET_PASSWORD", "would-grant-access")
	h, err := cmuxCurrentHandle()
	if err == nil {
		t.Fatalf("a process outside cmux got a surface handle: %q", h)
	}
	if !strings.Contains(err.Error(), "not started inside cmux") {
		t.Fatalf("refusal is not explanatory: %v", err)
	}
	// It must not even have ASKED: the point is to skip a call whose answer
	// we know we are not entitled to. This is the claim in the comment above,
	// so it is asserted rather than described.
	if asks != 0 {
		t.Fatalf("identify ran %d times for a process outside cmux; the in-tree check must come first", asks)
	}
	cmuxInTree = func() bool { return true }
	if _, err := cmuxCurrentHandle(); err != nil {
		t.Fatalf("in-tree identify failed: %v", err)
	}
	if asks != 1 {
		t.Fatalf("in-tree identify ran %d times, want exactly 1", asks)
	}
}

// The live listing matcher must be able to say both yes and no: a
// prefix-loose matcher makes the wrong-target proof in the live test
// decorative. Asserted here, ungated, so it guards CI too.
func TestCmuxLiveLineHasFieldIsTokenAnchored(t *testing.T) {
	// The real line shape carries a uuid between the ref and the marker.
	real := "* pane:1 37A66F91-2A93-49DF-9376-60F6BB4B34DC  [2 surfaces]  [focused]"
	if !cmuxLiveLineHasField(real, "pane:1") {
		t.Error("failed to match a real listing line")
	}
	if !cmuxLiveLineHasField("  surface:2  ~", "surface:2") {
		t.Error("failed to match an indented listing line")
	}
	for _, c := range []struct{ line, ref string }{
		{"* pane:11  [focused]", "pane:1"},  // prefix trap
		{"* pane:1  [focused]", "pane:11"},  // the other direction
		{"* pane:999  [focused]", "pane:9"}, // the live guard, ungated
		{"* surface:1  x", "surface:12"},
		{"", "pane:1"},
		{"* pane:1  [focused]", ""},
	} {
		if cmuxLiveLineHasField(c.line, c.ref) {
			t.Errorf("matched %q against %q, which it must not", c.line, c.ref)
		}
	}
}

// A handle buried under a multi-segment tail (tab:4/surface:5) is refused by
// US, not forwarded to cmux. No verb accepts it: `focus-pane --pane
// "tab:4/surface:5"` answers "Invalid pane handle", and there is no --tab
// flag. Refusing keeps the no-raw-CLI-error rule in our hands.
func TestCmuxTermRunRefusesUnaddressableTail(t *testing.T) {
	calls := 0
	oldExec := cmuxExec
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, _ ...string) (string, error) {
		calls++
		return "OK", nil
	}
	for _, h := range []string{
		"workspace:2/tab:4/surface:5",
		"window:1/workspace:2/pane:3/extra:9",
		"workspace:1/pane:1 --focus",
		"workspace:1/../../x",
		"workspace:1/pane:1/../../..",
		"workspace:../etc/passwd",
		"workspace:1/",
		"workspace:1",
	} {
		if err := cmuxTermRun("switch", h); err == nil {
			t.Errorf("%q was accepted", h)
		} else if strings.Contains(err.Error(), "Invalid") || strings.Contains(err.Error(), "not_found") {
			t.Errorf("%q would leak a raw cmux message: %v", h, err)
		}
	}
	if calls != 0 {
		t.Fatalf("unaddressable handles reached the CLI %d times", calls)
	}
}

// cmux binds focus only. `close` must be refused by US, on evidence: live
// against cmux 0.64.25, close-surface returned "OK surface:51" when asked to
// close surface:50, and surface:50 still resolved afterwards. A verb that
// reports success while closing something else must never be bound.
func TestCmuxTermRunRefusesClose(t *testing.T) {
	calls := 0
	oldExec := cmuxExec
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, _ ...string) (string, error) {
		calls++
		return "OK", nil
	}
	for _, h := range []string{"workspace:2/surface:4", "workspace:2/pane:3", "surface:4", "pane:3"} {
		err := cmuxTermRun("close", h)
		if err == nil {
			t.Fatalf("close was accepted for %q; cmux cannot be trusted to close a named surface", h)
		}
		if !strings.Contains(err.Error(), "binds focus only") {
			t.Fatalf("close refusal for %q is not explanatory: %v", h, err)
		}
	}
	if calls != 0 {
		t.Fatalf("a refused close still spawned %d subprocesses", calls)
	}
}

// Focus needs a pane target: a surface ref is refused here rather than
// reaching cmux, whose own answer ("Invalid pane handle") is a raw error.
func TestCmuxTermRunRefusesWrongTargetKind(t *testing.T) {
	calls := 0
	oldExec := cmuxExec
	defer func() { cmuxExec = oldExec }()
	cmuxExec = func(_ context.Context, _ ...string) (string, error) {
		calls++
		return "OK", nil
	}
	err := cmuxTermRun("switch", "workspace:2/surface:4")
	if err == nil || !strings.Contains(err.Error(), "pane:") {
		t.Fatalf("focus on a surface target should be refused, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("wrong-kind targets reached the CLI %d times", calls)
	}
}
