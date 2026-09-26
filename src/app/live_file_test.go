package app

// live_file_test.go — /live on a pi that never loaded the bridge.
//
// The session FILE is the second source: pitago tails the file a running pi
// is appending to, so any pi can be followed with no restart and no
// cooperation. These tests pin the three things that make that true and keep
// it honest — the picker never calls such a row unfollowable, exactly one
// transport is live at a time, and a tailed session is as read-only as a
// streamed one.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

// fileCandidate is one row as the live layer hands it over: no descriptor, no
// bridge, just the file of a pi that is working right now.
func fileCandidate(dir string) live.Candidate {
	path := filepath.Join(dir, "2026-09-20T08-00-00-000Z_aaa.jsonl")
	return live.Candidate{
		CWD:    dir,
		Source: live.SourceFile,
		File: &live.SessionFile{
			Path: path, SessionID: "file-session-1", CWD: dir,
			Model: "sonnet", Size: 128, ModTime: time.Now(),
		},
	}
}

// The regression this whole source exists for: a pi with no bridge used to be
// refused with "cannot be followed … restart that pi once". Enter on that row
// must now start the tail, with no restart and nothing sent to the session.
func TestLiveAttachFileCandidateStartsTailWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.liveCands = []live.Candidate{fileCandidate(dir)}
	before := m.liveGeneration

	cmd := m.AttachLive(0)
	if cmd == nil {
		t.Fatal("attaching to an active session file started no transport")
	}
	if m.liveTail == nil || m.liveSource != live.SourceFile {
		t.Fatalf("the tail is not the attached source: tail=%v source=%q", m.liveTail, m.liveSource)
	}
	if m.liveGeneration == before {
		t.Fatal("attach did not retire the messages queued by a previous attach")
	}
	if m.liveCands != nil {
		t.Fatal("the picker rows outlived the attach")
	}
	if m.followRemote {
		t.Fatal("follow mode must wait for the transport's Connected/Snapshot")
	}
	for _, want := range []string{"cannot be followed", "restart that pi"} {
		if noticeContains(m, want) {
			t.Fatalf("a tailable row was refused with %q: %v", want, notices(m))
		}
	}
	if !noticeContains(m, "attaching external session") || !noticeContains(m, "read-only") {
		t.Fatalf("attach was silent or did not say read-only: %v", notices(m))
	}
	if !strings.Contains(m.Status, "attaching external Pi") {
		t.Fatalf("status does not announce the attach: %q", m.Status)
	}
	// The command is deliberately not run: it opens the file for tailing
	// and would outlive the test. The tail itself is covered end to end by
	// TestTailTransportForwardsNoCommandToOwnedClient below.
}

// The picker is where the user learns the two sources differ, so a file row
// must read as a first-class option and never as the old dead end.
func TestLiveFileRowIsLabelledAsFollowable(t *testing.T) {
	dir := t.TempDir()
	row := fileCandidate(dir)
	desc := liveCandidateDesc(row)
	if strings.Contains(desc, "cannot be followed") || strings.Contains(desc, "not streamable") {
		t.Fatalf("a followable row reads as a dead end: %q", desc)
	}
	if !strings.Contains(desc, "messages") || !strings.Contains(desc, "no restart") {
		t.Fatalf("file row does not say what it gives and that it needs nothing: %q", desc)
	}
	if bridge := liveCandidateDesc(live.Candidate{PID: 1, Streamable: true, Descriptor: &live.Descriptor{Token: "t"}}); !strings.Contains(bridge, "streaming") {
		t.Fatalf("bridge row does not read as the full live stream: %q", bridge)
	}
	label := liveCandidateLabel(row)
	if strings.Contains(label, "pid 0") {
		t.Fatalf("a session file has no pid and must not claim one: %q", label)
	}
	for _, want := range []string{"session file", "sonnet", dir, ShortID("file-session-1")} {
		if !strings.Contains(label, want) {
			t.Fatalf("row %q is missing %q", label, want)
		}
	}
	// A file row is being appended to right now, so its mtime is its age.
	if !strings.Contains(label, "up ") {
		t.Fatalf("file row has no age from its mtime: %q", label)
	}
	// The refusal is still correct for a row with neither source.
	if dead := liveCandidateDesc(live.Candidate{PID: 9}); !strings.Contains(dead, "not streamable") {
		t.Fatalf("a row with no source at all does not read as unfollowable: %q", dead)
	}
}

// Exactly one transport may be live: switching sources has to stop the
// previous one, or a tail keeps pushing a foreign transcript into a window that
// has already moved on to a streamed session.
func TestLiveSwitchingSourceStopsThePreviousTransport(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)

	m.liveCands = []live.Candidate{fileCandidate(dir)}
	if cmd := m.AttachLive(0); cmd == nil || m.liveTail == nil {
		t.Fatal("the file attach did not start a tail")
	}
	stale := m.liveGeneration

	m.liveCands = []live.Candidate{{
		PID: 4242, CWD: dir, Streamable: true, Source: live.SourceBridge, SessionID: "bridged",
		Descriptor: &live.Descriptor{Endpoint: "http://127.0.0.1:1/events", Token: "t", CWD: dir},
	}}
	if cmd := m.AttachLive(0); cmd == nil {
		t.Fatal("the bridge attach did not start a transport")
	}
	if m.liveTail != nil {
		t.Fatal("the tail outlived the switch to the bridge")
	}
	if m.liveSource != live.SourceBridge {
		t.Fatalf("two sources are live: recorded %q", m.liveSource)
	}
	if m.liveGeneration == stale {
		t.Fatal("the switch did not retire the tail's queued messages")
	}

	// And back the other way.
	m.liveCands = []live.Candidate{fileCandidate(dir)}
	if cmd := m.AttachLive(0); cmd == nil {
		t.Fatal("the second file attach did not start a transport")
	}
	if m.liveTail == nil || m.liveSource != live.SourceFile {
		t.Fatalf("the bridge outlived the switch to the file: tail=%v source=%q", m.liveTail, m.liveSource)
	}
}

// Detach must stop a tail, not just a bridge — the bug this guards is a tail
// that keeps appending remote messages to the owned session after Ctrl+D.
func TestLiveDetachStopsTheFileTail(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.liveCands = []live.Candidate{fileCandidate(dir)}
	if cmd := m.AttachLive(0); cmd == nil {
		t.Fatal("the file attach did not start a transport")
	}
	m.followRemote = true
	m.liveConnected = true

	if cmd := m.ToggleLiveSession(); cmd == nil {
		t.Fatal("detach returned no command: the owned session would never come back")
	}
	if m.liveTail != nil || m.liveSource != "" {
		t.Fatalf("detach left a transport running: tail=%v source=%q", m.liveTail, m.liveSource)
	}
	if m.followRemote || m.liveConnected {
		t.Fatalf("/live did not detach: follow=%v connected=%v", m.followRemote, m.liveConnected)
	}
}

// Ctrl+D is the documented detach key and goes through the same path as
// /live, so it must stop the tail too.
func TestCtrlDStopsTheFileTail(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.liveCands = []live.Candidate{fileCandidate(dir)}
	if cmd := m.AttachLive(0); cmd == nil {
		t.Fatal("the file attach did not start a transport")
	}
	m.applyLive(m.liveGeneration, live.Message{Connected: true, Descriptor: live.Descriptor{SessionID: "file-session-1"}})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if m.liveTail != nil || m.followRemote {
		t.Fatalf("Ctrl+D did not stop the tail: tail=%v follow=%v", m.liveTail, m.followRemote)
	}
}

// The single-obvious-choice fast path must survive the second source: one
// active session file and no dialog. The fixture is a real session file in the
// real per-project session directory, so what makes it a candidate is the
// live layer's own discovery, not a hand-built row.
func TestLiveSingleFileCandidateAttachesWithoutDialog(t *testing.T) {
	proj := t.TempDir()
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(agent, "sessions"))
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", filepath.Join(t.TempDir(), "desc"))
	writeSessionFile(t, pirpc.SessionDirFor(proj), proj)

	m := New(nil, proj)
	cmd := m.ToggleLiveSession()
	if cmd == nil {
		t.Fatal("the only candidate was a file and nothing was started")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("a single followable candidate still opened a picker: %+v", m.Dialogs)
	}
	if m.liveTail == nil || m.liveSource != live.SourceFile {
		t.Fatalf("the tail is not the attached source: tail=%v source=%q", m.liveTail, m.liveSource)
	}
	// The command is not run here; the tail path is covered above and below.
}

// A tailed session is a real, live wire into the app, so the follow-mode
// invariants have to hold for it and not only for the bridge. This runs the
// REAL tail over a real session file and asserts the one invariant that must
// never bend: whatever the file produces, not one byte reaches our own pi.
// The control probe first proves the tap is live, so a green run means
// "nothing was sent", not "nothing was recorded".
func TestTailTransportForwardsNoCommandToOwnedClient(t *testing.T) {
	pi, read := stdinTap(t)
	dir := t.TempDir()
	if err := pi.Fire(pirpc.Command{Type: "control-probe"}); err != nil {
		t.Fatalf("control write: %v", err)
	}
	waitForBytes(t, read, 1)
	baseline := len(read())

	path := writeSessionFile(t, dir, dir)
	tail := live.NewTail(live.SessionFile{Path: path, SessionID: "tailed", CWD: dir, ModTime: time.Now()})
	emitted := make(chan live.Message, 256)
	generation := uint64(7) // any generation: the tap, not the model, is the subject
	go tail.Start(context.Background(), func(message live.Message) {
		emitted <- message
	})()
	time.Sleep(200 * time.Millisecond) // the whole file is already on disk
	tail.Stop()

	m := New(pi, dir)
	var seen int
	for {
		select {
		case message := <-emitted:
			seen++
			var msgs []tea.Msg
			runCmds(m.applyLive(generation, message), &msgs, time.Now().Add(time.Second))
		default:
			if seen == 0 {
				t.Fatal("the tail emitted nothing for a readable session file: the invariant would be vacuous")
			}
			if got := read(); len(got) != baseline {
				t.Fatalf("tailing a foreign session file wrote %d byte(s) to the OWNED pi: %q", len(got)-baseline, got[baseline:])
			}
			return
		}
	}
}

// writeSessionFile writes a pi session file in the format pi itself uses (the
// one src/pirpc's own scanSession parses) into dir, and returns its path. The
// assistant turn is what a tail has to turn into message_end, so a file that
// only had a header would prove nothing.
func writeSessionFile(t *testing.T, dir, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"session","id":"tailed","timestamp":"` + now + `","cwd":` + strconv.Quote(cwd) + `}`,
		`{"type":"message","id":"m1","timestamp":"` + now + `","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"message","id":"m2","timestamp":"` + now + `","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}
	path := filepath.Join(dir, "2026-09-20T08-00-00-000Z_tailed.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
