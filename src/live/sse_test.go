package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	raw := []byte(fmt.Sprintf(`{"version":%d,"endpoint":%q,"token":"secret","sessionId":"ext","cwd":%q,"pid":42,"startedAt":1}`, d.Version, d.Endpoint, d.CWD))
	if err := os.WriteFile(filepath.Join(dir, "session.json"), raw, mode); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFiltersCWDSecurityAndOwnedPID(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	writeDescriptor(t, dir, Descriptor{Version: 1, Endpoint: "http://127.0.0.1:1234/events", CWD: cwd}, 0600)
	ds, err := Discover(cwd, dir, 42)
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
