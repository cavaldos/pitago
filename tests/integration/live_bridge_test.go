// Black-box end-to-end coverage for the /live broadcast path: a real `pi
// --mode rpc` child loads the embedded bridge extension, writes a mode-0600
// descriptor, and a second Go-side live.Bridge (what a second pitago window
// runs) discovers and follows it over loopback SSE.
//
// Hermetic by construction: PITAGO_LIVE_DESCRIPTORS and the child's cwd are
// t.TempDir(), so a developer's real pitago-live directory and their real
// project are never read or written.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"pitago/src/live"
)

// subagentExtensionTS is a stand-in for a real subagent/team extension: it
// registers a tool AND a `/` command that both emit a notice through the
// shared ctx.ui object. The command path needs no model call, so the tap is
// provable offline and deterministically; the tool path is the same shared
// emitNotice() the model would have driven.
const subagentExtensionTS = `import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

// Shared by the tool and the command: both are "a subagent extension talking
// to the user", which is the only thing the tap needs to see.
async function emitNotice(ctx: ExtensionContext, origin: string) {
  await ctx.ui.notify("[subagent-async] worker-1 started from " + origin, "info");
  ctx.ui.setWidget("subagent-async", ["worker-1 · running (" + origin + ")"]);
}

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "subagent_notice",
    description: "Emit a subagent/team notice through ctx.ui",
    parameters: { type: "object", properties: {}, additionalProperties: false },
    async execute(_args, ctx) {
      await emitNotice(ctx, "tool");
      return { content: "ok", details: undefined };
    },
  });
  pi.registerCommand("subagent-notice", {
    description: "Emit a subagent/team notice through ctx.ui",
    handler: async (_name, ctx) => {
      await emitNotice(ctx, "command");
    },
  });
}
`

// subagentMarker is the notice text only the test extension emits.
const subagentMarker = "[subagent-async] worker-1 started"

const (
	descriptorTimeout = 30 * time.Second // pi's own extension loading is slow
	noticeTimeout     = 20 * time.Second
)

// piChild is a live `pi --mode rpc` process plus the pipes the test drives.
type piChild struct {
	cmd        *exec.Cmd
	stdin      *os.File
	stdoutPath string
	stderrPath string
	dir        string // descriptor dir (PITAGO_LIVE_DESCRIPTORS)
	cwd        string // child working directory, matched exactly by live.Discover
}

// requirePi skips with a clear message when pi is not installed, matching the
// idiom in src/pirpc/client_test.go:17 — CI runners have no pi.
func requirePi(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi not in PATH (set PI_BIN or install pi to run the live bridge end-to-end tests)")
	}
	return bin
}

// startPi spawns the broadcast child: the embedded live bridge extension, plus
// any extra extension files, in a temp cwd with hermetic descriptor dir.
//
// SAFETY (process lifetime): stdin is an os.Pipe whose write end stays open
// for the whole test. A `pi` child fed a closed/EOF'd stdin exits immediately
// and its session_shutdown unlinks the descriptor it just wrote, so the child
// must always have a live stdin. t.Cleanup closes it and then kills the
// process group if pi ignores the orderly shutdown, so no pi is ever leaked.
func startPi(t *testing.T, extraExtensions ...string) *piChild {
	t.Helper()
	bin := requirePi(t)
	// The bridge is no longer materialized to a temp file: the shipped path
	// is the one-command install, so the test drives the real thing. HOME
	// points at a temp dir so the developer's actual ~/.pi/agent/extensions
	// is never written, and t.TempDir is removed with the test.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	if _, err := live.InstallBridge(); err != nil {
		t.Fatalf("install embedded bridge: %v", err)
	}
	bridge := filepath.Join(home, ".pi", "agent", "extensions", "pitago-live-bridge.ts")
	if _, err := os.Stat(bridge); err != nil {
		t.Fatalf("installed bridge is not where pi will be pointed: %v", err)
	}
	base := t.TempDir()
	child := &piChild{
		stdoutPath: filepath.Join(base, "stdout.jsonl"),
		stderrPath: filepath.Join(base, "stderr.txt"),
		dir:        filepath.Join(base, "descriptors"),
		cwd:        filepath.Join(base, "wd"),
	}
	if err := os.MkdirAll(child.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(child.cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// pi publishes ctx.cwd as a realpath (/private/var/... on macOS) while
	// t.TempDir() hands back the logical TMPDIR path (/var/...), and
	// live.Discover matches cwd exactly. Resolve it so the follower compares
	// the same string pi wrote; production is unaffected because os.Getwd
	// already returns a physical path.
	if resolved, err := filepath.EvalSymlinks(child.cwd); err == nil {
		child.cwd = resolved
	}
	args := []string{"--mode", "rpc", "--extension", bridge}
	for _, ext := range extraExtensions {
		args = append(args, "--extension", ext)
	}
	child.cmd = exec.Command(bin, args...)
	child.cmd.Dir = child.cwd
	child.cmd.Env = append(os.Environ(), "PITAGO_LIVE_DESCRIPTORS="+child.dir)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	child.stdin = stdinW
	child.cmd.Stdin = stdinR
	// Drain stdout continuously: pi honours pipe backpressure, so a test that
	// stops reading can stall the child mid-turn.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	child.cmd.Stdout = stdoutW
	stderr, err := os.OpenFile(child.stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	child.cmd.Stderr = stderr
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		f, _ := os.Create(child.stdoutPath)
		if f == nil {
			_, _ = io.Copy(io.Discard, stdoutR)
			return
		}
		_, _ = io.Copy(f, stdoutR)
		_ = f.Close()
	}()
	if err := child.cmd.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	t.Cleanup(func() {
		_ = child.stdin.Close()
		done := make(chan struct{})
		go func() { _, _ = child.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = child.cmd.Process.Kill()
			<-done
		}
		_ = stdoutW.Close()
		<-drained
		_ = stdinR.Close()
		_ = stderr.Close()
	})
	return child
}

// writeSubagentExtension materializes the test extension in a temp dir and
// returns its path, ready for `pi --extension`.
func writeSubagentExtension(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subagent-notice.ts")
	if err := os.WriteFile(path, []byte(subagentExtensionTS), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// send writes one JSONL RPC command to the child's stdin.
func (c *piChild) send(t *testing.T, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write to pi stdin: %v", err)
	}
}

// diagnostics dumps the child's stdout/stderr to explain a failure.
func (c *piChild) diagnostics(t *testing.T) string {
	t.Helper()
	out, _ := os.ReadFile(c.stdoutPath)
	errOut, _ := os.ReadFile(c.stderrPath)
	return "\n--- pi stdout ---\n" + string(out) + "\n--- pi stderr ---\n" + string(errOut)
}

// waitDescriptor polls until the bridge has published its mode-0600 descriptor.
// Poll with a deadline rather than sleeping a fixed interval: pi's own
// extension loading dominates startup time and varies per machine.
func (c *piChild) waitDescriptor(t *testing.T, ownPID int) live.Descriptor {
	t.Helper()
	deadline := time.Now().Add(descriptorTimeout)
	for {
		ds, err := live.Discover(c.cwd, c.dir, ownPID)
		if err != nil {
			t.Fatalf("discover: %v", err)
		}
		if len(ds) > 0 {
			return ds[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no live descriptor in %s within %s%s", c.dir, descriptorTimeout, c.diagnostics(t))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// uiRequest is the wire shape of the extension_ui_request record pitago
// already decodes (src/pirpc UIRequest / src/app handleUIRequest).
type uiRequest struct {
	Type        string   `json:"type"`
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Message     string   `json:"message"`
	NotifyType  string   `json:"notifyType"`
	WidgetKey   string   `json:"widgetKey"`
	WidgetLines []string `json:"widgetLines"`
}

// follower is the consumer side: a live.Bridge plus every record it received,
// i.e. exactly what a second pitago window running /live would render.
type follower struct {
	msgs chan live.Message
	stop func()
	done chan struct{}
}

// startFollower runs live.Bridge (discovery + SSE reconnect loop) against the
// child's descriptor dir, from a second in-process consumer. OwnPID is 0: this
// window owns no pi child, so the spawned broadcast child must be discovered.
func startFollower(t *testing.T, c *piChild) *follower {
	t.Helper()
	f := &follower{msgs: make(chan live.Message, 256), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &live.Bridge{CWD: c.cwd, Dir: c.dir, OwnPID: 0}
	cmd := bridge.Start(ctx, func(m live.Message) {
		select {
		case f.msgs <- m:
		default:
		}
	})
	go func() { cmd(); close(f.done) }()
	f.stop = func() {
		bridge.Stop()
		cancel()
		<-f.done
	}
	t.Cleanup(f.stop)
	return f
}

// await polls f.msgs until pred matches or the deadline passes.
func (f *follower) await(t *testing.T, what string, timeout time.Duration, pred func(live.Message) bool) live.Message {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-f.msgs:
			if pred(m) {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
	}
}

func (f *follower) awaitConnected(t *testing.T) live.Descriptor {
	t.Helper()
	m := f.await(t, "bridge Connected", noticeTimeout, func(m live.Message) bool { return m.Connected })
	return m.Descriptor
}

// TestLiveBroadcastReachesSecondWindow proves goal (1) end to end: a real pi
// child running the embedded bridge publishes a descriptor, and an independent
// live.Bridge — the same code path a second pitago window's /live uses —
// discovers it, authenticates, and receives the mandatory snapshot.
func TestLiveBroadcastReachesSecondWindow(t *testing.T) {
	child := startPi(t)
	desc := child.waitDescriptor(t, 0) // 0 = not the owner, so it must be discovered

	// SECURITY: the descriptor is same-user session data and must stay 0600;
	// live.Discover silently drops a group/other-readable one, so assert the
	// mode the bridge promises rather than trusting discovery to have run.
	if err := checkDescriptorMode(child.dir); err != nil {
		t.Fatal(err)
	}

	// SECURITY: the loopback endpoint is bearer-token protected, so a random
	// local process cannot read another window's transcript. Assert it here
	// rather than trusting the 200 the authorized follower got.
	if code := unauthenticatedStatus(t, desc.Endpoint); code != http.StatusUnauthorized {
		t.Fatalf("GET %s without Authorization = %d, want 401", desc.Endpoint, code)
	}

	follower := startFollower(t, child)
	if got := follower.awaitConnected(t); got.Endpoint != desc.Endpoint {
		t.Fatalf("connected to %q, want discovered endpoint %q", got.Endpoint, desc.Endpoint)
	}

	// A second window renders entirely from the snapshot, so it must be a
	// usable one: the discovered session id, a cwd, and an explicit list.
	// (Its revision is bridge-local and already advanced by pi's own startup
	// UI traffic, so it is not asserted to be 0.)
	snap := follower.await(t, "snapshot", noticeTimeout, func(m live.Message) bool { return m.Snapshot != nil }).Snapshot
	if snap.SessionID != desc.SessionID {
		t.Fatalf("snapshot session id = %q, want descriptor session id %q", snap.SessionID, desc.SessionID)
	}
	if snap.SessionID == "" {
		t.Fatal("snapshot carries no session id")
	}
	if snap.CWD == "" {
		t.Fatal("snapshot carries no cwd")
	}
	if snap.Messages == nil {
		t.Fatal("snapshot messages = nil, want an explicit list")
	}
}

// unauthenticatedStatus GETs the SSE endpoint with no bearer token.
func unauthenticatedStatus(t *testing.T, endpoint string) int {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unauthenticated GET: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
	return resp.StatusCode
}

// TestLiveBridgeTapsSubagentNoticeAsExtensionUIRequest proves goal (2) end to
// end: a notice emitted by ANOTHER extension through the shared ctx.ui object
// — the only channel pi has for subagent/team traffic, since pi has no
// subagent event — reaches the second window as an extension_ui_request
// record on the bridge's SSE stream.
//
// Variant proved: the extension's own `/` command, executed over the RPC
// stdin pipe. That is a genuine extension code path calling ctx.ui.notify and
// ctx.ui.setWidget, needs no model call, and is fully offline/deterministic.
// The registered tool shares the identical emitNotice() body, so the same tap
// covers the model-driven tool path.
func TestLiveBridgeTapsSubagentNoticeAsExtensionUIRequest(t *testing.T) {
	child := startPi(t, writeSubagentExtension(t))
	child.waitDescriptor(t, 0)

	follower := startFollower(t, child)
	// The mandatory connect-time snapshot is the transcript the follow view
	// renders; keep it so we can prove the notice is not in it.
	initial := follower.await(t, "snapshot", noticeTimeout, func(m live.Message) bool { return m.Snapshot != nil }).Snapshot
	// Drain the startup backlog: pi's own session_start traffic (titles,
	// statuses, unrelated extensions) legitimately races our notice.
	//
	// Extension commands run immediately and need no model, so no credentials
	// are required for this test.
	child.send(t, map[string]any{"id": "cmd-1", "type": "prompt", "message": "/subagent-notice"})

	deadline := time.After(noticeTimeout)
	var notify, widget *uiRequest
	var notifyRev, widgetRev int64
	for notify == nil || widget == nil {
		select {
		case m := <-follower.msgs:
			if m.Event == nil {
				continue
			}
			var rec uiRequest
			if json.Unmarshal(m.Event.Raw, &rec) != nil || rec.Type != "extension_ui_request" {
				continue
			}
			switch {
			case rec.Method == "notify" && strings.Contains(rec.Message, subagentMarker) && notify == nil:
				notify, notifyRev = &rec, m.Event.Revision
			case rec.Method == "setWidget" && rec.WidgetKey == "subagent-async" && widget == nil:
				widget, widgetRev = &rec, m.Event.Revision
			}
		case <-deadline:
			t.Fatalf("subagent notice never arrived on the SSE stream (notify=%+v widget=%+v)%s", notify, widget, child.diagnostics(t))
		}
	}

	// Both must be live revisioned SSE events, not snapshot content: the tap
	// re-emits into the same revisioned `pi` stream pitago already renders.
	if notifyRev <= 0 || widgetRev <= 0 {
		t.Errorf("notice revisions = notify:%d widget:%d, want both > 0", notifyRev, widgetRev)
	}
	if notify.ID == "" {
		t.Error("extension_ui_request notify has no id: the follow view cannot correlate a reply")
	}
	if notify.NotifyType != "info" {
		t.Errorf("notify type = %q, want %q (tap must preserve the argument)", notify.NotifyType, "info")
	}
	if !strings.Contains(notify.Message, "from command") {
		t.Errorf("notify message = %q, want the origin marker proving the command path ran", notify.Message)
	}
	// setWidget proves the tap covers the widget channel too, which is how
	// team/subagent progress rows actually reach a pitago follow view.
	if len(widget.WidgetLines) != 1 ||
		!strings.Contains(widget.WidgetLines[0], "worker-1 · running (command)") {
		t.Errorf("widget lines = %q, want one worker-1 line from the command path", widget.WidgetLines)
	}

	// The snapshot (re-sent on every reconnect) must NOT contain the notice:
	// extension_ui_request is an RPC-wire-only record that pi never writes to
	// the session transcript and never delivers to pi.on. This is the whole
	// reason the tap exists, so pin it: the notice is only observable live.
	for _, msg := range initial.Messages {
		if strings.Contains(string(msg), subagentMarker) {
			t.Errorf("notice leaked into the snapshot transcript: %s", msg)
		}
	}
}

// checkDescriptorMode asserts every published descriptor is mode 0600.
func checkDescriptorMode(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("descriptor %s mode = %v, want no group/other bits", ent.Name(), info.Mode().Perm())
		}
	}
	return nil
}
