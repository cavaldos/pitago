package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadSSECommentsMultilineAndIDs(t *testing.T) {
	input := ": heartbeat\n\nid: 7\nevent: pi\ndata: {\"type\":\ndata: \"agent_start\"}\n\n"
	var got []SSE
	if err := ReadSSE(strings.NewReader(input), func(ev SSE) error { got = append(got, ev); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "7" || got[0].Event != "pi" || string(got[0].Data) != "{\"type\":\n\"agent_start\"}" {
		t.Fatalf("events = %#v", got)
	}
}

func TestReadSSESupportsMultiMegabyteEvent(t *testing.T) {
	payload := strings.Repeat("x", 5<<20)
	var got []byte
	if err := ReadSSE(strings.NewReader("id: 9\nevent: snapshot\ndata: "+payload+"\n\n"), func(ev SSE) error {
		got = append([]byte(nil), ev.Data...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("large SSE payload length = %d, want %d", len(got), len(payload))
	}
}

func writeDescriptor(t *testing.T, dir string, d Descriptor, mode os.FileMode) {
	t.Helper()
	writeDescriptorPID(t, dir, d, mode, os.Getpid())
}

// writeDescriptorPID publishes a descriptor owned by an explicit pid, so a
// test can publish one for a process that no longer exists.
func writeDescriptorPID(t *testing.T, dir string, d Descriptor, mode os.FileMode, pid int) {
	t.Helper()
	raw := []byte(fmt.Sprintf(`{"version":%d,"endpoint":%q,"token":"secret","sessionId":"ext","cwd":%q,"pid":%d,"startedAt":1}`, d.Version, d.Endpoint, d.CWD, pid))
	if err := os.WriteFile(filepath.Join(dir, "session.json"), raw, mode); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFiltersCWDSecurityAndOwnedPID(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	writeDescriptorPID(t, dir, Descriptor{Version: 1, Endpoint: "http://127.0.0.1:1234/events", CWD: cwd}, 0600, os.Getpid())
	ds, err := Discover(cwd, dir, os.Getpid())
	if err != nil || len(ds) != 0 {
		t.Fatalf("owned: %#v %v", ds, err)
	}
	ds, err = Discover(cwd, dir, 7)
	if err != nil || len(ds) != 1 {
		t.Fatalf("foreign: %#v %v", ds, err)
	}
	if err := os.Chmod(filepath.Join(dir, "session.json"), 0644); err != nil {
		t.Fatal(err)
	}
	ds, err = Discover(cwd, dir, 7)
	if err != nil || len(ds) != 0 {
		t.Fatalf("world-readable descriptor accepted: %#v %v", ds, err)
	}
}

// A SIGKILLed broadcasting pi never unlinks its descriptor, and descriptors
// sort newest-first — so without a liveness check every other window in that
// cwd picks the dead one and loops on connect-refused. The pid is the only
// liveness evidence the record carries, so a dead one must be ignored while a
// live one is still followed.
func TestDiscoverSkipsDescriptorOfDeadPID(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	// A reaped child is definitely gone: no pid can be reused while it is
	// still ours, and kill(2) reports ESRCH immediately after Wait.
	dead := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExists")
	if err := dead.Start(); err != nil {
		t.Skipf("cannot start reaper helper: %v", err)
	}
	deadPID := dead.Process.Pid
	_ = dead.Wait()

	writeDescriptorPID(t, dir, Descriptor{Version: 1, Endpoint: "http://127.0.0.1:1234/events", CWD: cwd}, 0600, deadPID)
	if ds, err := Discover(cwd, dir, 7); err != nil || len(ds) != 0 {
		t.Fatalf("descriptor of a dead pid accepted: %#v %v", ds, err)
	}
	if pidAlive(deadPID) {
		t.Fatalf("pid %d reported alive after reaping", deadPID)
	}
	if !pidAlive(os.Getpid()) {
		t.Fatal("our own pid reported dead")
	}
	writeDescriptorPID(t, dir, Descriptor{Version: 1, Endpoint: "http://127.0.0.1:1234/events", CWD: cwd}, 0600, os.Getpid())
	ds, err := Discover(cwd, dir, 7)
	if err != nil || len(ds) != 1 {
		t.Fatalf("live descriptor ignored: %#v %v", ds, err)
	}
}

func TestDiscoverMatchesSymlinkedCWD(t *testing.T) {
	dir := t.TempDir()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// pi publishes a physical path, the window may hold the logical one: a
	// symlinked project directory must still be discovered by /live.
	writeDescriptor(t, dir, Descriptor{Version: 1, Endpoint: "http://127.0.0.1:1234/events", CWD: real}, 0600)
	if ds, err := Discover(link, dir, 7); err != nil || len(ds) != 1 {
		t.Fatalf("symlinked cwd not discovered: %#v %v", ds, err)
	}
	if ds, err := Discover(real, dir, 7); err != nil || len(ds) != 1 {
		t.Fatalf("physical cwd not discovered: %#v %v", ds, err)
	}
	other := t.TempDir()
	if ds, err := Discover(other, dir, 7); err != nil || len(ds) != 0 {
		t.Fatalf("unrelated cwd matched: %#v %v", ds, err)
	}
}

func TestRunDescriptorReconnectSnapshotConvergesWithoutDuplicateEvents(t *testing.T) {
	var connects atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := connects.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "id: 3\nevent: snapshot\ndata: {\"revision\":3,\"sessionId\":\"s\",\"messages\":[]}\n\n")
		if n == 1 {
			fmt.Fprint(w, "id: 4\nevent: pi\ndata: {\"type\":\"agent_start\"}\n\n")
		}
	}))
	defer srv.Close()
	d := Descriptor{Version: 1, Endpoint: srv.URL, Token: "t", SessionID: "s", CWD: "/tmp"}
	var messages []Message
	var last int64
	if rev, connected, err := (&Bridge{}).runDescriptor(context.Background(), d, &last, func(m Message) { messages = append(messages, m) }); err != nil || !connected || rev != 4 {
		t.Fatalf("first connection: rev=%d connected=%v err=%v", rev, connected, err)
	}
	if rev, connected, err := (&Bridge{}).runDescriptor(context.Background(), d, &last, func(m Message) { messages = append(messages, m) }); err != nil || !connected || rev != 3 {
		t.Fatalf("second connection: rev=%d connected=%v err=%v", rev, connected, err)
	}
	var snapshots, events int
	for _, m := range messages {
		if m.Snapshot != nil {
			snapshots++
		}
		if m.Event != nil {
			events++
		}
	}
	if snapshots != 2 || events != 1 {
		t.Fatalf("snapshots=%d events=%d", snapshots, events)
	}
}

func TestHungConnectionHitsReadWatchdog(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	d := Descriptor{Version: 1, Endpoint: srv.URL, Token: "t", SessionID: "s", CWD: "/tmp"}
	started := time.Now()
	_, connected, err := (&Bridge{}).runDescriptorWithTimeout(context.Background(), d, new(int64), func(Message) {}, 50*time.Millisecond)
	close(release)
	if err == nil || !connected {
		t.Fatalf("hung stream: connected=%v err=%v", connected, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("watchdog took too long: %v", elapsed)
	}
}

func TestHeartbeatsKeepLongLivedStreamOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		ticker := time.NewTicker(15 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": heartbeat\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	d := Descriptor{Version: 1, Endpoint: srv.URL, Token: "t", SessionID: "s", CWD: "/tmp"}
	_, connected, err := (&Bridge{}).runDescriptorWithTimeout(ctx, d, new(int64), func(Message) {}, 50*time.Millisecond)
	if !connected || err != context.DeadlineExceeded {
		t.Fatalf("heartbeat stream: connected=%v err=%v", connected, err)
	}
}

func TestBackoffBounded(t *testing.T) {
	if Backoff(0) != 250*time.Millisecond || Backoff(99) != 4*time.Second {
		t.Fatal("unexpected backoff")
	}
}

// The regression a pin exists to prevent: descriptors sort newest-first, so
// unpinned discovery would hop to the NEWER foreign session the moment it
// published. The user picked the older one, so that must never happen.
func TestPinnedTargetWinsOverNewerDiscoveredDescriptor(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()

	pinnedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\nevent: snapshot\ndata: {\"revision\":1,\"sessionId\":\"pinned\",\"messages\":[]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-time.After(300 * time.Millisecond)
	}))
	defer pinnedSrv.Close()
	newerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("connected to the newer un-picked session")
		w.WriteHeader(http.StatusOK)
	}))
	defer newerSrv.Close()

	pinnedPID := liveForeignPID(t)
	newerPID := liveForeignPID(t)
	// Newer startedAt: plain discovery would choose the newer descriptor.
	writeForeignDescriptor(t, dir, pinnedPID, pinnedSrv.URL+"/events", cwd, 100)
	if err := os.WriteFile(filepath.Join(dir, "newer.json"), []byte(fmt.Sprintf(
		`{"version":1,"endpoint":%q,"token":"secret","sessionId":"newer","cwd":%q,"pid":%d,"startedAt":999}`,
		newerSrv.URL+"/events", cwd, newerPID)), 0o600); err != nil {
		t.Fatal(err)
	}

	d := Descriptor{Version: 1, Endpoint: pinnedSrv.URL + "/events", Token: "secret",
		SessionID: "pinned", CWD: cwd, PID: pinnedPID, StartedAt: 100}
	b := &Bridge{CWD: cwd, Dir: dir}
	b.SetTarget(&d)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	var once sync.Once
	emit := func(m Message) {
		if m.Connected && m.Descriptor.SessionID != "pinned" {
			t.Errorf("attached to un-picked session %q", m.Descriptor.SessionID)
		}
		if m.Connected {
			once.Do(func() { close(done) })
		}
	}
	cmd := b.Start(ctx, emit) // returns a tea.Cmd; run() only starts once it is invoked
	go func() { _ = cmd() }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("never connected to the pinned descriptor")
	}
	cancel()
}

// nil restores plain discovery, so a user can unpin and go back to newest.
func TestSetTargetNilRestoresDiscovery(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\nevent: snapshot\ndata: {\"revision\":1,\"sessionId\":\"disc\",\"messages\":[]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-time.After(300 * time.Millisecond)
	}))
	defer srv.Close()
	writeForeignDescriptor(t, dir, liveForeignPID(t), srv.URL+"/events", cwd, 5)

	b := &Bridge{CWD: cwd, Dir: dir}
	b.SetTarget(&Descriptor{Endpoint: "http://127.0.0.1:1/events", Token: "t", PID: 1})
	b.SetTarget(nil)
	if b.targetDescriptor() != nil {
		t.Fatal("SetTarget(nil) must clear the pin")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	var once sync.Once
	cmd := b.Start(ctx, func(m Message) {
		if m.Connected {
			once.Do(func() { close(done) })
		}
	})
	go func() { _ = cmd() }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("after unpinning, discovery must attach the discovered session")
	}
	cancel()
}
