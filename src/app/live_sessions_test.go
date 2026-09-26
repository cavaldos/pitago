package app

// live_sessions_test.go — /live as a picker + attach/detach toggle.
//
// The user runs their own `pi` in another terminal; pitago only ever FOLLOWS
// one, read-only, and never publishes its own child. These tests drive the
// real entry points (/live through the builtin and ToggleLiveSession) and pin
// the three outcomes the spec calls out: nothing running, nothing followable,
// and a successful attach.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
)

// notices is every transient transcript notice pushed so far.
func notices(m Model) []string {
	out := make([]string, 0, len(m.toasts))
	for _, t := range m.toasts {
		out = append(out, t.Text)
	}
	return out
}

func noticeContains(m Model, want string) bool {
	for _, got := range notices(m) {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

// /live while following is the detach key: same path as Ctrl+D, and it must
// not quit (quitting is Ctrl+C twice or /quit).
func TestLiveToggleDetachesWhileFollowing(t *testing.T) {
	m := New(nil, t.TempDir())
	m.followRemote = true
	m.liveConnected = true
	cmd := m.ToggleLiveSession()
	if cmd == nil {
		t.Fatal("detach returned no command: the owned session would never come back")
	}
	if m.followRemote || m.liveConnected {
		t.Fatalf("/live did not detach: follow=%v connected=%v", m.followRemote, m.liveConnected)
	}
	if !noticeContains(m, "detached external Pi") {
		t.Fatalf("detach was silent: %v", notices(m))
	}
}

// The empty case is a normal answer, never an error or a hung dialog: it
// reports the directory, installs the bridge extension so the NEXT pi
// streams in full without a manual flag, and points an already-running pi at
// the session-file source instead of at a restart.
func TestLiveToggleWithoutCandidatesReportsAndInstallsBridge(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)        // keep the install out of the real home
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", filepath.Join(dir, "desc"))

	m := New(nil, dir)
	if cmd := m.ToggleLiveSession(); cmd != nil {
		t.Fatal("the empty case must not start a transport or open work")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("the empty case opened a picker: %+v", m.Dialogs)
	}
	if m.followRemote {
		t.Fatal("the empty case attached to nothing")
	}
	if !noticeContains(m, "no pi session is running in "+dir) {
		t.Fatalf("no-session notice missing: %v", notices(m))
	}
	if !noticeContains(m, "live bridge installed") {
		t.Fatalf("bridge install notice missing: %v", notices(m))
	}
	if !noticeContains(m, "session file") {
		t.Fatalf("already-running-pi path is not mentioned: %v", notices(m))
	}
}

// A pi that never loaded the bridge cannot be followed. Enter on its row must
// say so and stay on the owned session instead of opening a dead view.
func TestLiveAttachRefusesNonStreamableCandidate(t *testing.T) {
	m := New(nil, t.TempDir())
	m.liveCands = []live.Candidate{{PID: 4242, CWD: t.TempDir()}}
	if cmd := m.AttachLive(0); cmd != nil {
		t.Fatal("attaching to an unstreamable session started a transport")
	}
	if m.followRemote || m.liveConnected {
		t.Fatal("attached to a session with no bridge")
	}
	if !noticeContains(m, "cannot be followed") || !noticeContains(m, "restart that pi once") {
		t.Fatalf("refusal was not explained: %v", notices(m))
	}
}

// A chosen streamable candidate pins the transport and starts it; the
// Connected/Snapshot path then flips follow mode on. Nothing is written to the
// selected session and our own child is never respawned.
func TestLiveAttachPinsThePickedDescriptor(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.liveCands = []live.Candidate{{
		PID: 4242, CWD: dir, Streamable: true, SessionID: "remote-session-1234",
		Descriptor: &live.Descriptor{Endpoint: "http://127.0.0.1:1/events", Token: "t", CWD: dir},
	}}
	before := m.liveGeneration
	cmd := m.AttachLive(0)
	if cmd == nil {
		t.Fatal("attach did not start the transport")
	}
	if m.liveGeneration == before {
		t.Fatal("attach did not retire the messages queued by a previous attach")
	}
	if m.followRemote {
		t.Fatal("follow mode must wait for the transport's Connected/Snapshot")
	}
	if m.liveCands != nil {
		t.Fatal("the picker rows outlived the attach")
	}
	if !noticeContains(m, "attaching external pi 4242") {
		t.Fatalf("attach was silent: %v", notices(m))
	}
	// The command is deliberately not executed: running it opens a real
	// loopback connection to a port nothing listens on. The transport is
	// covered end to end by tests/integration/live_bridge_test.go.
}

// /live is the only entry point, so a second /live after a detach must start
// the transport again rather than reuse the stopped one.
func TestLiveReattachesAfterDetach(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.followRemote = true
	m.liveConnected = true
	if cmd := m.ToggleLiveSession(); cmd == nil {
		t.Fatal("detach returned no command")
	}
	m.liveCands = []live.Candidate{{
		PID: 99, CWD: dir, Streamable: true, SessionID: "second-session",
		Descriptor: &live.Descriptor{Endpoint: "http://127.0.0.1:1/events", Token: "t", CWD: dir},
	}}
	if cmd := m.AttachLive(0); cmd == nil {
		t.Fatal("/live after a detach did not restart the transport")
	}
	if m.liveBridge == nil {
		t.Fatal("the transport is gone after a detach")
	}
}

// A dead or refusing target reports the failure and leaves the owned session
// usable: a broken read-only view would strand the window.
func TestLiveAttachFailureKeepsTheOwnedSession(t *testing.T) {
	m := New(nil, t.TempDir())
	if cmd := m.applyLive(m.liveGeneration, live.Message{Err: fmt.Errorf("connection refused")}); cmd != nil {
		t.Fatal("a failed attach must not schedule work")
	}
	if m.followRemote || m.liveConnected {
		t.Fatal("a failed attach entered follow mode anyway")
	}
	if !noticeContains(m, "could not attach to the selected Pi session") {
		t.Fatalf("attach failure was silent: %v", notices(m))
	}
	if !strings.Contains(m.Status, "owned session") {
		t.Fatalf("status does not say the owned session is still live: %q", m.Status)
	}
}

// The picker's two text helpers are what makes a row readable: which process,
// how old, which session/model/dir, and whether Enter can follow it at all.
func TestLiveCandidateRowRendering(t *testing.T) {
	c := live.Candidate{
		PID: 4242, CWD: "/tmp/proj", StartedAt: time.Now().Add(-90 * time.Second).UnixMilli(),
		Model: "sonnet", SessionName: "worker-1", Streamable: true,
	}
	label := liveCandidateLabel(c)
	for _, want := range []string{"pid 4242", "up 1m", "worker-1", "sonnet", "/tmp/proj"} {
		if !strings.Contains(label, want) {
			t.Fatalf("row %q is missing %q", label, want)
		}
	}
	if !strings.Contains(liveCandidateDesc(c), "streaming") {
		t.Fatalf("streamable row reads as unstreamable: %q", liveCandidateDesc(c))
	}
	plain := live.Candidate{PID: 7, CWD: "/tmp/proj", SessionID: "0123456789"}
	if got := liveCandidateLabel(plain); !strings.Contains(got, "01234567") {
		t.Fatalf("unnamed session does not fall back to its short id: %q", got)
	}
	if got := liveCandidateLabel(live.Candidate{PID: 8, CWD: "/tmp/proj"}); !strings.Contains(got, "age unknown") {
		t.Fatalf("unknown start time is guessed: %q", got)
	}
	if got := liveCandidateDesc(plain); !strings.Contains(got, "not streamable") {
		t.Fatalf("unstreamable row reads as followable: %q", got)
	}
}

// The owned composer must never claim a broadcast: pitago publishes nothing
// of its own session, so there is no such state to advertise.
func TestOwnedComposerHasNoBroadcastTitle(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 100, 30
	m.ready = true
	m.renderInput()
	if top := stripANSI(m.renderInput()); strings.Contains(top, "BROADCAST") {
		t.Fatalf("owned composer advertises a broadcast: %q", top)
	}
}

// Startup must not attach on its own. /live is the only entry point, so a
// window opened in a directory that has a streamable pi running stays on its
// OWNED session until the user asks. A real descriptor plus a real loopback
// server make "no connection was made" observable rather than assumed.
func TestInitDoesNotAttachOnStartup(t *testing.T) {
	dir := t.TempDir()
	descDir := filepath.Join(dir, "desc")
	if err := os.MkdirAll(descDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", descDir)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	raw, err := json.Marshal(live.Descriptor{
		Version: live.DescriptorVersion, Endpoint: srv.URL + "/events", Token: "test-token",
		SessionID: "remote-session", CWD: dir, PID: os.Getpid(), StartedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(descDir, "remote.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// The descriptor must really be discoverable, otherwise "no connection"
	// would prove nothing.
	found, err := live.Discover(dir, descDir, 0)
	if err != nil || len(found) != 1 {
		t.Fatalf("precondition: descriptor not discoverable: %d found, err=%v", len(found), err)
	}

	pi, _ := stdinTap(t)
	m := New(pi, dir)
	var msgs []tea.Msg
	runCmds(m.Init(), &msgs, time.Now().Add(time.Second))
	if got := hits.Load(); got != 0 {
		t.Fatalf("startup opened %d live connection(s); /live must be the only entry point", got)
	}
}
