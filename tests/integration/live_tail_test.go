// Real-pi coverage for the second follow source: tailing the session file of
// a running `pi --mode rpc` that never loaded the live bridge extension.
//
// This is the exact situation the bridge cannot serve — pi loads extensions
// at process start, so a pi that was already running can never become
// streamable — yet it is still appending to a file on disk.
//
// Hermetic by construction: the child's HOME and PI_CODING_AGENT_DIR (and the
// test process's, which is what live.ActiveSessions reads) point at a
// t.TempDir(), and the child's cwd is another t.TempDir(). A developer's real
// ~/.pi/agent/sessions is therefore never listed, read or written.
//
// No model turn: pi is never prompted and no credentials are used. The child
// is started on a pre-seeded session file, which it appends to on startup
// (pi flushes a resumed session immediately), so the file is provably
// live-appended while ActiveSessions reports it active.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"pitago/src/live"
	"pitago/src/pirpc"
)

const (
	// seedTimeout bounds how long the resumed pi may take to append to the
	// seeded session file; its own startup (settings, index, resume) varies
	// per machine, the same reason descriptorTimeout exists for the bridge.
	seedTimeout = 30 * time.Second
	// tailAttachTimeout bounds one emitted tail message (Connected or the
	// snapshot), which only costs a stat plus a read of the file.
	tailAttachTimeout = 20 * time.Second
	// tailQuietWindow is how long we watch a stopped tail for emissions to
	// prove its goroutine really ended.
	tailQuietWindow = 2 * time.Second
)

// seededSessionID is the id written into the seeded session header; the tail
// must report exactly this id, proving it followed the file we handed pi
// rather than picking up some other session.
const seededSessionID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"

// appendMarker is text only a line the test appends carries, so a matching
// event can only come from the tail reading the file after it was started.
const appendMarker = "tail-follow-probe"

// filePi is a real `pi --mode rpc` child running on a pre-seeded session
// file, plus the pipe plumbing the child needs to stay alive.
type filePi struct {
	cmd        *exec.Cmd
	stdin      *os.File
	stdoutPath string
	stderrPath string
	cwd        string // child working directory, as a resolved physical path
	sessionDir string // hermetic pi session directory for that cwd
	session    string // the seeded session file pi resumes
	seedSize   int64  // its size before pi was started
	startedAt  time.Time
}

// seedSession writes a minimal but valid pi session (header + one user and
// one assistant message) and returns its path and size.
//
// The assistant message is what makes pi flush the file: a session with no
// assistant entry is kept in memory only (dist/core/session-manager.js
// _persist), so a real pi would never create a file we could follow. Seeding
// a finished exchange is also the honest shape of the scenario being tested:
// a pi that has been working and is still appending.
func seedSession(t *testing.T, sessionDir, cwd string) (string, int64) {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// pi's default session file name is <ISO timestamp>_<session id>.jsonl;
	// ListSessions reads the newest first by name, so keep it a real one.
	path := filepath.Join(sessionDir, "2026-01-01T00-00-00.000Z_"+seededSessionID+".jsonl")
	lines := []map[string]any{
		{
			"type": "session", "version": 3, "id": seededSessionID,
			"timestamp": "2026-01-01T00:00:00.000Z", "cwd": cwd,
		},
		{
			"type": "message", "id": "u1", "parentId": nil,
			"timestamp": "2026-01-01T00:00:01.000Z",
			"message":   map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hi"}}},
		},
		{
			"type": "message", "id": "a1", "parentId": "u1",
			"timestamp": "2026-01-01T00:00:02.000Z",
			"message":   map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
		},
	}
	var buf []byte
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, raw...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, int64(len(buf))
}

// startFilePi spawns `pi --mode rpc` on a freshly seeded session file inside a
// hermetic agent dir, with stdin held open for the whole test.
//
// SAFETY (process lifetime): identical to startPi — a pi fed a closed stdin
// exits on EOF, so the write end of an os.Pipe stays open until t.Cleanup,
// which then kills the process and waits for it, so no pi is ever leaked.
// stdout and stderr are drained continuously into temp files, because pi
// honours pipe backpressure and a stalled reader would freeze the child.
func startFilePi(t *testing.T) *filePi {
	t.Helper()
	bin := requirePi(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	agent := filepath.Join(home, ".pi", "agent")
	cwd := filepath.Join(base, "wd")
	for _, d := range []string{home, agent, cwd} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// pi publishes ctx.cwd as a realpath (/private/var/... on macOS) while
	// t.TempDir() hands back the logical TMPDIR path, and the seeded header
	// has to name the same directory pi will report. Resolve once and use it
	// for both, so the session directory slug matches on every platform.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	// Pin the agent/session locations for the CHILD (through its env) and for
	// the TEST PROCESS (through t.Setenv, which is what live.ActiveSessions
	// reads). Both point into the temp tree, so neither the developer's real
	// agent dir nor their real sessions can be seen.
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	// Cleared rather than set: an inherited override would point both pi and
	// pirpc.SessionDirFor at a directory outside the temp tree.
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

	p := &filePi{
		stdoutPath: filepath.Join(base, "stdout.jsonl"),
		stderrPath: filepath.Join(base, "stderr.txt"),
		cwd:        cwd,
		// The very directory ActiveSessions will look in: ask pirpc for it
		// rather than recomputing the slug, so the child and the follower
		// cannot disagree on where pi keeps sessions.
		sessionDir: pirpc.SessionDirFor(cwd), // after t.Setenv: <agent>/sessions/--slug--
	}
	if p.sessionDir == "" {
		t.Fatal("pirpc session dir is empty; PI_CODING_AGENT_DIR pinning failed")
	}
	p.session, p.seedSize = seedSession(t, p.sessionDir, p.cwd)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p.stdin = stdinW
	p.cmd = exec.Command(bin, "--mode", "rpc", "--session", p.session)
	p.cmd.Dir = p.cwd
	p.cmd.Env = os.Environ() // already carries the pinned HOME/PI_* above
	p.cmd.Stdin = stdinR
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stdout = stdoutW
	stderr, err := os.OpenFile(p.stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stderr = stderr
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		f, _ := os.Create(p.stdoutPath)
		if f == nil {
			_, _ = io.Copy(io.Discard, stdoutR)
			return
		}
		_, _ = io.Copy(f, stdoutR)
		_ = f.Close()
	}()
	p.startedAt = time.Now()
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	t.Cleanup(func() {
		_ = p.stdin.Close()
		done := make(chan struct{})
		go func() { _, _ = p.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
		_ = stdoutW.Close()
		<-drained
		_ = stdinR.Close()
		_ = stderr.Close()
	})
	return p
}

// diagnostics dumps the child's stdout/stderr to explain a failure.
func (p *filePi) diagnostics(t *testing.T) string {
	t.Helper()
	out, _ := os.ReadFile(p.stdoutPath)
	errOut, _ := os.ReadFile(p.stderrPath)
	return "\n--- pi stdout ---\n" + string(out) + "\n--- pi stderr ---\n" + string(errOut)
}

// awaitActive polls live.ActiveSessions until it reports the child's session
// file as active, then returns that report.
//
// The evidence that pi itself touched the file is a size past the seeded one
// plus an mtime after the spawn: the seeded file alone must never be enough,
// or the test would pass with a dead child that only ever listed its own seed.
func (p *filePi) awaitActive(t *testing.T) live.SessionFile {
	t.Helper()
	deadline := time.Now().Add(seedTimeout)
	var listed bool
	for {
		for _, f := range live.ActiveSessions(p.cwd, seedTimeout) {
			if f.Path != p.session {
				continue
			}
			listed = true
			if f.Size > p.seedSize && f.ModTime.After(p.startedAt) {
				return f
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("live.ActiveSessions(%s) never reported %s as appended-to within %s (dir %s, listed=%v, size %d vs seeded %d)%s",
				p.cwd, p.session, seedTimeout, p.sessionDir, listed, p.fileSize(), p.seedSize, p.diagnostics(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fileSize reports the session file's current size, 0 when unreadable.
func (p *filePi) fileSize() int64 {
	st, err := os.Stat(p.session)
	if err != nil {
		return 0
	}
	return st.Size()
}

// startTail follows f and records every message in a follower, which is the
// same record set a pitago follow view collects. The returned stop is
// registered on t.Cleanup so the goroutine can never outlive the test.
func startTail(t *testing.T, f live.SessionFile) *follower {
	t.Helper()
	sink := &follower{msgs: make(chan live.Message, 256), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	tail := live.NewTail(f)
	cmd := tail.Start(ctx, func(m live.Message) {
		select {
		case sink.msgs <- m:
		default:
		}
	})
	go func() { cmd(); close(sink.done) }()
	sink.stop = func() {
		tail.Stop()
		cancel()
		<-sink.done
	}
	t.Cleanup(sink.stop)
	return sink
}

// appendLine appends one complete session entry to the live session file and
// returns its raw JSON. Only the test writes here — pi is never prompted — so
// the transcript gains an entry no model produced.
func (p *filePi) appendLine(t *testing.T, entry map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p.session, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	return raw
}

// messageEnd is the event shape the bridge forwards for a completed message
// and the one the file tail must reuse, so the follow view needs no new
// rendering code.
type messageEnd struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// TestLiveTailFollowsRunningPiWithoutBridge proves the second follow source
// end to end against a real pi: a session file of a running `pi --mode rpc`
// is reported active by live.ActiveSessions, and live.Tail attaches to it,
// emitting the same Connected + Snapshot stream a bridged session does.
//
// Deliberately no model turn: a prompt would need credentials and network, so
// this only proves discovery and attach — the half of the feature that
// cannot be faked.
func TestLiveTailFollowsRunningPiWithoutBridge(t *testing.T) {
	child := startFilePi(t)
	f := child.awaitActive(t)

	if f.SessionID != seededSessionID {
		t.Errorf("active session id = %q, want the seeded id %q", f.SessionID, seededSessionID)
	}
	if f.CWD != child.cwd {
		t.Errorf("active session cwd = %q, want the hermetic child cwd %q", f.CWD, child.cwd)
	}
	if _, err := os.Stat(f.Path); err != nil {
		t.Fatalf("active session path is not readable: %v", err)
	}

	sink := startTail(t, f)
	// SAFETY: the attached view shows the discovered session, not a
	// placeholder — a follow view that cannot name its session is useless.
	desc := sink.await(t, "tail Connected", tailAttachTimeout, func(m live.Message) bool { return m.Connected }).Descriptor
	if desc.SessionID != seededSessionID {
		t.Errorf("connected descriptor session id = %q, want %q", desc.SessionID, seededSessionID)
	}
	if desc.CWD == "" {
		t.Error("connected descriptor carries no cwd")
	}

	snap := sink.await(t, "tail snapshot", tailAttachTimeout, func(m live.Message) bool { return m.Snapshot != nil }).Snapshot
	if snap.SessionID != seededSessionID {
		t.Errorf("snapshot session id = %q, want %q", snap.SessionID, seededSessionID)
	}
	if snap.CWD == "" {
		t.Error("snapshot carries no cwd")
	}
	// The seeded exchange is a real branch, so the snapshot must render it:
	// the user turn and the assistant answer, in that order.
	if len(snap.Messages) != 2 {
		t.Fatalf("snapshot messages = %d, want the 2 seeded rows: %s", len(snap.Messages), snap.Messages)
	}
	if !strings.Contains(string(snap.Messages[0]), `"role":"user"`) {
		t.Errorf("snapshot message 0 = %s, want the seeded user turn", snap.Messages[0])
	}
	if !strings.Contains(string(snap.Messages[1]), "hello") {
		t.Errorf("snapshot message 1 = %s, want the seeded assistant answer", snap.Messages[1])
	}

	// Stop must end the follow for good: a detached view that keeps emitting
	// into a newer attach is the bug this guards.
	sink.stop()
	child.appendLine(t, map[string]any{
		"type": "message", "id": "a2", "parentId": "a1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message":   map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "after stop"}}},
	})
	select {
	case m := <-sink.msgs:
		t.Fatalf("a stopped tail still emitted: %+v", m)
	case <-time.After(tailQuietWindow):
	}
}

// TestLiveTailStreamsAppendsFromRunningPi proves the attach is live, not a
// one-shot read: entries appended to a running pi's session file arrive as
// message_end events with strictly increasing revisions — the exact shape the
// bridge emits, so the follow view renders them with no new code.
func TestLiveTailStreamsAppendsFromRunningPi(t *testing.T) {
	child := startFilePi(t)
	f := child.awaitActive(t)

	sink := startTail(t, f)
	sink.await(t, "tail Connected", tailAttachTimeout, func(m live.Message) bool { return m.Connected })
	snap := sink.await(t, "tail snapshot", tailAttachTimeout, func(m live.Message) bool { return m.Snapshot != nil }).Snapshot

	// Two appends, so the strictly-increasing revision is provable rather
	// than just positive.
	var last int64 = snap.Revision
	for i := 1; i <= 2; i++ {
		text := appendMarker + " " + string(rune('0'+i))
		child.appendLine(t, map[string]any{
			"type": "message", "id": "probe" + string(rune('0'+i)), "parentId": "a1",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message":   map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}},
		})
		ev := sink.await(t, "tail message_end event for "+text, tailAttachTimeout, func(m live.Message) bool {
			return m.Event != nil && strings.Contains(string(m.Event.Raw), text)
		}).Event
		if ev.Revision <= last {
			t.Errorf("event revision = %d, want strictly greater than %d", ev.Revision, last)
		}
		last = ev.Revision
		var me messageEnd
		if err := json.Unmarshal(ev.Raw, &me); err != nil {
			t.Fatalf("event raw is not JSON: %v (%s)", err, ev.Raw)
		}
		if me.Type != "message_end" {
			t.Errorf("event type = %q, want %q (the bridge's shape, reused verbatim)", me.Type, "message_end")
		}
		if !strings.Contains(string(me.Message), `"role":"assistant"`) {
			t.Errorf("event message = %s, want the appended assistant row", me.Message)
		}
	}
}
