package live

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// tailPollInterval is how often a tail re-reads its session file. It is short
// because the only thing it costs is a stat plus a read of the bytes pi has
// appended since the last poll, and long enough that an idle follow view is
// not a busy loop.
var tailPollInterval = 150 * time.Millisecond

// Tail follows one session file and emits exactly the live.Message stream the
// SSE bridge emits, so the app renders a followed pi with no second code
// path. It is the fallback for a pi that was already running when the bridge
// extension was installed: pi loads extensions at process start, so such a
// session can never become streamable, but it is still appending to a file we
// can read.
type Tail struct {
	// File is the session to follow. It is exported because the picker hands
	// the very same SessionFile back from Candidate.File.
	File SessionFile
	// Poll bounds the re-read interval; zero means tailPollInterval. It is a
	// field so a test can shrink it and stay deterministic.
	Poll time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewTail returns a tail for one session file.
func NewTail(f SessionFile) *Tail { return &Tail{File: f} }

// Start follows the file until ctx is cancelled or Stop is called. The
// returned command blocks for the lifetime of the follow, like Bridge's, so
// the caller can run it as a Bubble Tea command.
func (t *Tail) Start(ctx context.Context, emit func(Message)) func() any {
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	t.mu.Lock()
	t.cancel, t.done = cancel, done
	t.mu.Unlock()
	go func() {
		defer close(done)
		t.run(cctx, emit)
	}()
	return func() any {
		<-done
		return nil
	}
}

// Stop ends the follow and waits for its goroutine, so a detached follow
// view can never keep emitting into a newer attach. It is idempotent.
func (t *Tail) Stop() {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.cancel, t.done = nil, nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// run emits Connected, then the full snapshot, then one event per completed
// message the file gains.
func (t *Tail) run(ctx context.Context, emit func(Message)) {
	desc := Descriptor{
		SessionID: t.File.SessionID,
		CWD:       t.File.CWD,
		Model:     t.File.Model,
		// pi does not record its pid in the session file, and mtime is the
		// only honest start evidence a file carries.
		StartedAt: t.File.ModTime.UnixMilli(),
	}
	st := &tailState{path: t.File.Path, desc: desc}
	emit(Message{Descriptor: desc, Connected: true})
	if err := st.resnapshot(emit); err != nil {
		st.reportGone(err, emit)
		return
	}
	poll := t.Poll
	if poll <= 0 {
		poll = tailPollInterval
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := st.refresh(emit)
		if err == nil {
			continue
		}
		if errors.Is(err, errShrank) {
			// A truncation invalidates what we already rendered; the only
			// honest repair is a fresh snapshot, which the app treats exactly
			// like a reconnect.
			if err := st.resnapshot(emit); err != nil {
				st.reportGone(err, emit)
				return
			}
			continue
		}
		st.reportGone(err, emit)
		return
	}
}

// tailState is the follow state of one file. All of it is confined to the
// tail's own goroutine, so it needs no lock.
type tailState struct {
	path string
	desc Descriptor
	// offset is the byte position just past the last newline we consumed.
	// A half-written trailing line is never counted here.
	offset int64
	// rev is strictly increasing for the lifetime of this attach, matching
	// the bridge's event revisions.
	rev int64
}

// resnapshot re-reads the whole file and emits it as a fresh Snapshot.
func (s *tailState) resnapshot(emit func(Message)) error {
	entries, off, err := readSession(s.path)
	if err != nil {
		return err
	}
	s.offset = off
	rows, leaf := activeBranch(entries)
	snap := &Snapshot{
		Revision:  s.rev,
		SessionID: s.desc.SessionID,
		CWD:       s.desc.CWD,
		LeafID:    leaf,
		Messages:  rows,
		// A file-only follow cannot know whether pi is mid-turn, and
		// isStreaming drives the sidebar spinner: false is the honest answer.
		IsStreaming: false,
	}
	// The header is the last truth for these fields, and a session file is
	// the only place a file-only follow learns them.
	for _, e := range entries {
		switch e.Type {
		case "session":
			if e.ID != "" {
				snap.SessionID = e.ID
				s.desc.SessionID = e.ID
			}
		case "session_info":
			snap.SessionName = e.Name
		case "model_change":
			snap.Model, snap.Provider = e.ModelID, e.Provider
		case "thinking_level_change":
			snap.ThinkingLevel = e.ThinkingLevel
		}
	}
	emit(Message{Descriptor: s.desc, Snapshot: snap})
	return nil
}

// refresh emits one message_end event per completed entry appended since the
// last poll, and advances the offset over exactly those lines.
func (s *tailState) refresh(emit func(Message)) error {
	lines, off, err := readCompleteLines(s.path, s.offset)
	if err != nil {
		return err
	}
	for _, line := range lines {
		var e sessionEntry
		if json.Unmarshal(line, &e) != nil || e.Type == "" {
			continue
		}
		// A team-state entry is not a transcript row, so transcriptRow drops
		// it. It still has to reach the app: the worker roster it describes
		// lives in these entries and nowhere else on the file path, and the
		// app re-reads the file to reconcile it.
		if e.Type == "custom" && e.CustomType == teamStateType {
			s.rev++
			emit(Message{Descriptor: s.desc, Event: &Event{Revision: s.rev, Raw: teamStateEvent()}})
			continue
		}
		row, ok := transcriptRow(&e)
		if !ok {
			continue
		}
		raw, err := messageEndEvent(row)
		if err != nil {
			continue
		}
		s.rev++
		emit(Message{Descriptor: s.desc, Event: &Event{Revision: s.rev, Raw: raw}})
	}
	s.offset = off
	return nil
}

// reportGone ends the follow when the file can no longer be read at all. A
// missing file is the ordinary case (pi exited and removed it), and it is
// reported once instead of being retried in a hot loop.
func (s *tailState) reportGone(err error, emit func(Message)) {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		emit(Message{Descriptor: s.desc, Disconnected: true})
		return
	}
	emit(Message{Descriptor: s.desc, Disconnected: true, Err: err})
}

// teamStateType is the custom entry the pi-agent-team extension appends for
// every worker state change. The data lives in the entry's `data` field, not
// in the transcript shape, so the tail signals it and lets the app re-read the
// file rather than reimplementing the worker's parser here.
const teamStateType = "pi-agent-team/state"

// teamStateEvent tells the app a worker record was appended. It carries no
// payload on purpose: the app owns the parsing (readTeamSessionWorkers) and
// re-reads the file, so there is exactly one interpretation of the record.
func teamStateEvent() json.RawMessage {
	return json.RawMessage(`{"type":"team_state"}`)
}

// messageEndEvent wraps a transcript row in the exact event the bridge
// forwards for a completed message, so the app's handleEvent renders an
// assistant answer or a tool result from a file tail with no new code.
func messageEndEvent(row json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}{Type: "message_end", Message: row})
}
