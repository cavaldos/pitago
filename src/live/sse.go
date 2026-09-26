// Package live discovers and consumes read-only Pi session bridges.
package live

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DescriptorVersion = 1

// Descriptor is the mode-0600 record written by the Pi extension.
// Descriptor is the mode-0600 record written by the Pi extension.
//
// SessionName and Model are additive and optional: a descriptor published by
// an older bridge simply lacks them and is still valid, which is why
// DescriptorVersion is unchanged and why they are omitempty.
type Descriptor struct {
	Version     int    `json:"version"`
	Endpoint    string `json:"endpoint"`
	Token       string `json:"token"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName,omitempty"`
	Model       string `json:"model,omitempty"`
	CWD         string `json:"cwd"`
	PID         int    `json:"pid"`
	StartedAt   int64  `json:"startedAt"`
}

// Snapshot replaces the rendered transcript after attach/reconnect.
type Snapshot struct {
	Revision      int64             `json:"revision"`
	SessionID     string            `json:"sessionId"`
	SessionName   string            `json:"sessionName,omitempty"`
	LeafID        string            `json:"leafId,omitempty"`
	CWD           string            `json:"cwd"`
	Model         string            `json:"model,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	ThinkingLevel string            `json:"thinkingLevel,omitempty"`
	IsStreaming   bool              `json:"isStreaming"`
	Messages      []json.RawMessage `json:"messages"`
}

// Event is one revisioned Pi lifecycle event.
type Event struct {
	Revision int64
	Raw      json.RawMessage
}

// Message is emitted by Bridge toward the Bubble Tea loop.
type Message struct {
	Descriptor   Descriptor
	Snapshot     *Snapshot
	Event        *Event
	Connected    bool
	Disconnected bool
	Err          error
}

// DescriptorDir returns the explicit override or the per-user runtime path.
func DescriptorDir() string {
	if d := strings.TrimSpace(os.Getenv("PITAGO_LIVE_DESCRIPTORS")); d != "" {
		return d
	}
	if d := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); d != "" {
		return filepath.Join(d, "pitago-live")
	}
	return filepath.Join(os.TempDir(), "pitago-live-"+strconv.Itoa(os.Getuid()))
}

// Discover returns descriptors for cwd, excluding the owned Pi PID. Invalid,
// non-loopback, incorrectly permissioned, or dead-PID records are ignored.
func Discover(cwd, dir string, ownPID int) ([]Descriptor, error) {
	if dir == "" {
		dir = DescriptorDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	want, _ := filepath.Abs(cwd)
	out := make([]Descriptor, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		info, err := ent.Info()
		if err != nil || info.Mode().Perm()&0077 != 0 {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var d Descriptor
		if json.Unmarshal(raw, &d) != nil || d.Version != DescriptorVersion || d.Token == "" || d.PID == ownPID {
			continue
		}
		// A SIGKILLed broadcasting pi leaves its descriptor behind (pi unlinks
		// it only on an orderly shutdown), and descriptors sort newest-first —
		// so every other window in that cwd would pick the dead one and retry
		// connect-refused forever. With /live that is the routine case, not a
		// rare one. The pid is the only liveness evidence the record carries.
		if !pidAlive(d.PID) {
			continue
		}
		u, err := url.Parse(d.Endpoint)
		if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || d.CWD == "" {
			continue
		}
		dc, _ := filepath.Abs(d.CWD)
		if !sameDir(dc, want) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt == out[j].StartedAt {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameDir reports whether two absolute paths name the same directory. Plain
// comparison is not enough: pi resolves its cwd to a physical path, so a
// descriptor published for a symlinked project directory (macOS /tmp,
// /var/folders, a symlinked checkout) would otherwise never match the window
// that opened it, and /live would silently find nothing. Resolved paths are
// compared first; unresolvable paths fall back to the lexical comparison so a
// not-yet-existing or permission-denied path still degrades to the old rule.
func sameDir(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	if errA != nil || errB != nil {
		return false
	}
	return ra == rb
}

// physicalPath resolves p the way pi resolves its own cwd, so a path that
// feeds a name derived from the path string (a session directory) names what
// pi named. A path that cannot be resolved — it does not exist, or a symlink
// loop — comes back cleaned but unresolved, so the caller degrades to the
// pre-existing behaviour instead of failing: physicalPath is best-effort by
// the same policy sameDir applies.
func physicalPath(p string) string {
	clean := filepath.Clean(p)
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return clean
	}
	return real
}

// SSE is one decoded server-sent event.
type SSE struct {
	ID    string
	Event string
	Data  []byte
}

// ReadSSE parses one SSE stream. Comments (including heartbeats) are ignored.
// bufio.Reader grows across fragments, so a single multi-megabyte snapshot is
// not limited by Scanner's token-size ceiling.
func ReadSSE(r io.Reader, fn func(SSE) error) error {
	br := bufio.NewReader(r)
	var id, event string
	var data []string
	dispatch := func() error {
		if len(data) == 0 {
			id, event, data = "", "", nil
			return nil
		}
		err := fn(SSE{ID: id, Event: event, Data: []byte(strings.Join(data, "\n"))})
		id, event, data = "", "", nil
		return err
	}
	for {
		rawLine, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if dispatchErr := dispatch(); dispatchErr != nil {
					return dispatchErr
				}
				return nil
			}
			return err
		}
		line := strings.TrimSuffix(rawLine, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			id = value
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
}

// Backoff is bounded exponential reconnect delay.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<uint(attempt-1)) * 250 * time.Millisecond
}

// Bridge discovers a foreign session and keeps one SSE connection converged.
type Bridge struct {
	CWD, Dir string
	OwnPID   int
	mu       sync.Mutex
	cancel   context.CancelFunc
	target   *Descriptor
}

// Start begins discovery/reconnect until ctx is cancelled.
func (b *Bridge) Start(ctx context.Context, emit func(Message)) func() any {
	cctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()
	return func() any { b.run(cctx, emit); return nil }
}

// Stop is idempotent.
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

// SetOwnPID updates the excluded child after a login/session respawn.
func (b *Bridge) SetOwnPID(pid int) {
	b.mu.Lock()
	b.OwnPID = pid
	b.mu.Unlock()
}

func (b *Bridge) ownPID() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.OwnPID
}

// SetTarget pins the Bridge to exactly one descriptor; nil restores
// discovery. Mutex discipline matches SetOwnPID, since both are called from
// the Bubble Tea loop while run() reads them from its own goroutine.
func (b *Bridge) SetTarget(d *Descriptor) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d == nil {
		b.target = nil
		return
	}
	cp := *d
	b.target = &cp
}

func (b *Bridge) targetDescriptor() *Descriptor {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.target
}

func (b *Bridge) run(ctx context.Context, emit func(Message)) {
	var lastRev int64
	selected := ""
	attempt := 0
	for ctx.Err() == nil {
		// A pinned target wins over discovery and is never replaced by
		// "newest discovered": silently hopping to a different session would
		// attach the user to a session they did not pick, and the worst case
		// is showing them someone else's terminal. If the pinned session
		// dies we report Disconnected and retry the SAME target under the
		// normal backoff, so a bridge restart on that pid is picked up.
		d, ok := b.nextDescriptor()
		if ok {
			key := d.SessionID + "\x00" + d.Endpoint
			if key != selected {
				selected = key
				lastRev = 0 // revisions are local to a bridge process
			}
			rev, _, err := b.runDescriptor(ctx, d, &lastRev, emit)
			lastRev = rev
			attempt++
			emit(Message{Descriptor: d, Disconnected: true, Err: err})
		} else {
			attempt++
		}
		delay := Backoff(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// nextDescriptor resolves the descriptor to follow: the pinned target when
// one is set, otherwise the newest discovered one.
//
// A pinned target is used as-is, without re-probing it here. The user chose
// it explicitly, and the reconnect path already reports a dead or vanished
// target as a normal Disconnected message and retries it under the usual
// backoff — so a bridge that restarts on the same pid reconnects, and one
// that stays dead surfaces as an error the app can report, which is the
// behaviour the pin is for.
func (b *Bridge) nextDescriptor() (Descriptor, bool) {
	if t := b.targetDescriptor(); t != nil {
		return *t, true
	}
	ds, err := Discover(b.CWD, b.Dir, b.ownPID())
	if err != nil || len(ds) == 0 {
		return Descriptor{}, false
	}
	return ds[0], true
}

const heartbeatTimeout = 35 * time.Second // server heartbeats arrive every 15s

// heartbeatReader refreshes a cancellation watchdog whenever bytes arrive.
// Any SSE line or heartbeat resets it; a silent/hung peer fails in 35s.
type heartbeatReader struct {
	r       io.Reader
	timeout time.Duration
	cancel  context.CancelFunc
	touch   chan struct{}
	stop    chan struct{}
	once    sync.Once
}

func newHeartbeatReader(r io.Reader, timeout time.Duration, cancel context.CancelFunc) *heartbeatReader {
	h := &heartbeatReader{r: r, timeout: timeout, cancel: cancel, touch: make(chan struct{}, 1), stop: make(chan struct{})}
	go h.watch()
	return h
}

func (r *heartbeatReader) watch() {
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	for {
		select {
		case <-r.touch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(r.timeout)
		case <-timer.C:
			r.cancel()
			return
		case <-r.stop:
			return
		}
	}
}

func (r *heartbeatReader) Read(p []byte) (int, error) {
	select {
	case r.touch <- struct{}{}:
	default:
	}
	n, err := r.r.Read(p)
	if n > 0 {
		select {
		case r.touch <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (r *heartbeatReader) Close() {
	r.once.Do(func() { close(r.stop) })
}

func (b *Bridge) runDescriptor(ctx context.Context, d Descriptor, lastRev *int64, emit func(Message)) (int64, bool, error) {
	return b.runDescriptorWithTimeout(ctx, d, lastRev, emit, heartbeatTimeout)
}

func (b *Bridge) runDescriptorWithTimeout(ctx context.Context, d Descriptor, lastRev *int64, emit func(Message), timeout time.Duration) (int64, bool, error) {
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, d.Endpoint, nil)
	if err != nil {
		return *lastRev, false, err
	}
	req.Header.Set("Authorization", "Bearer "+d.Token)
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return *lastRev, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return *lastRev, false, fmt.Errorf("live bridge: HTTP %s", resp.Status)
	}
	reader := newHeartbeatReader(resp.Body, timeout, cancelRequest)
	defer reader.Close()
	emit(Message{Descriptor: d, Connected: true})
	connected := true
	err = ReadSSE(reader, func(ev SSE) error {
		if ev.ID == "" {
			return nil
		}
		rev, err := strconv.ParseInt(ev.ID, 10, 64)
		if err != nil {
			return nil
		}
		if ev.Event == "snapshot" {
			// A same-revision snapshot is mandatory on reconnect: it restores
			// gaps without replaying already-rendered live events.
			var snap Snapshot
			if err := json.Unmarshal(ev.Data, &snap); err != nil {
				return err
			}
			*lastRev = rev
			emit(Message{Descriptor: d, Snapshot: &snap})
		} else if ev.Event == "pi" && rev > *lastRev {
			*lastRev = rev
			raw := append(json.RawMessage(nil), ev.Data...)
			emit(Message{Descriptor: d, Event: &Event{Revision: rev, Raw: raw}})
		}
		return nil
	})
	wasConnected := connected
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return *lastRev, wasConnected, err
}
