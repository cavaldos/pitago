package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pitago/src/live"
)

// writeTeamSession writes a pi session file holding pi-agent-team/state
// records, the durable form of a worker roster on a followed session.
func writeTeamSession(t *testing.T, dir string, extra string) string {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session","version":3,"id":"sess-1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/x"}
{"type":"custom","customType":"pi-agent-team/state","id":"r1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","data":{"version":2,"kind":"worker_terminal","recordId":"terminal:1","worker":{"workerId":"w1","profileName":"reviewer","status":"exited","startedAt":1,"lastEventAt":2,"usage":{"turns":3,"costUsd":0.5}}}}
{"type":"custom","customType":"pi-agent-team/state","id":"r2","parentId":null,"timestamp":"2026-01-01T00:00:02.000Z","data":{"version":2,"kind":"worker_terminal","recordId":"terminal:2","worker":{"workerId":"w2","profileName":"explorer","status":"idle","startedAt":1,"lastEventAt":2,"usage":{"turns":1,"costUsd":0}}}}
`
	if err := os.WriteFile(path, []byte(body+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Following a session file must make its workers visible: the roster only
// exists on disk, so the view has to read the followed session's own file.
func TestFollowModeShowsWorkerRosterFromSessionFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTeamSession(t, dir, "")
	m := New(nil, dir)
	m.sessionFile = path

	m.refreshTeamFromSession()

	if len(m.TeamWidgetLines) == 0 {
		t.Fatal("no worker roster rendered: the followed session's agents stay invisible")
	}
	joined := strings.Join(m.TeamWidgetLines, "\n")
	for _, want := range []string{"Pi Agents Team", "2 workers", "reviewer (w1)", "exited", "explorer (w2)", "idle"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("roster missing %q; got:\n%s", want, joined)
		}
	}
	// Stable order: the roster must not reshuffle between updates.
	if !strings.Contains(m.TeamWidgetLines[1], "(w1)") {
		t.Fatalf("workers not in stable order: %q", m.TeamWidgetLines[1])
	}
}

// An empty roster must not leave a stale widget from a previous session.
func TestRefreshTeamFromSessionClearsStaleRoster(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.sessionFile = writeTeamSession(t, dir, "")
	m.refreshTeamFromSession()
	if len(m.TeamWidgetLines) == 0 {
		t.Fatal("precondition: roster should be populated")
	}
	m.sessionFile = filepath.Join(dir, "no-such-session.jsonl")
	m.refreshTeamFromSession()
	if len(m.TeamWidgetLines) != 0 {
		t.Fatalf("stale roster survived: %q", strings.Join(m.TeamWidgetLines, " | "))
	}
}

// Following rewrites sessionFile; the owned one must come back on detach, or
// /team, respawn and resume would keep reading a foreign session.
func TestDetachRestoresOwnedSessionFile(t *testing.T) {
	dir := t.TempDir()
	foreign := writeTeamSession(t, dir, "")
	m := New(nil, dir)
	owned := filepath.Join(dir, "owned.jsonl")
	m.sessionFile = owned
	m.followRemote = true
	m.liveConnected = true

	m.attachLiveCandidate(live.Candidate{
		Source: live.SourceFile,
		File:   &live.SessionFile{Path: foreign, SessionID: "sess-1", CWD: dir},
	})
	if m.sessionFile != foreign {
		t.Fatalf("attach did not point at the followed session: %q", m.sessionFile)
	}
	m.detachLive()
	if m.sessionFile != owned {
		t.Fatalf("detach did not restore the owned session file: %q", m.sessionFile)
	}
	if m.liveSessionFile != "" {
		t.Fatalf("displaced session file left armed: %q", m.liveSessionFile)
	}
}
