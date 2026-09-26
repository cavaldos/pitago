package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pitago/src/pirpc"
)

// useSessionDir points pi's session directory at a temp dir, so a test can
// never read — or be perturbed by — the developer's real session history.
func useSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	return dir
}

// writeSessionFile writes lines to dir/name and returns the path. The caller
// owns the content, so a test can also rewrite the file to simulate a
// truncation.
func writeSessionFile(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

// sessionHeader is a pi session file header line for cwd.
func sessionHeader(cwd, id string) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-01-01T10:00:00.000Z","cwd":%q}`, id, cwd)
}

// The picker only offers files pi is still appending to, newest first, with
// the header fields filled from the file itself.
func TestActiveSessionsListsRecentFilesNewestFirst(t *testing.T) {
	dir := useSessionDir(t)
	cwd := t.TempDir()
	recent := writeSessionFile(t, dir, "2026-01-02T10-00-00_recent.jsonl",
		sessionHeader(cwd, "sess-recent"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-x","timestamp":"2026-01-02T10:00:01.000Z"}`)
	old := writeSessionFile(t, dir, "2026-01-01T10-00-00_old.jsonl",
		sessionHeader(cwd, "sess-old"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-old","timestamp":"2026-01-01T10:00:01.000Z"}`)
	// Not a pi session at all: no header, so pi could never resume it.
	writeSessionFile(t, dir, "2026-01-03T10-00-00_junk.jsonl", `{"type":"message","id":"m1","message":{"role":"user","content":"x"}}`)
	// Right extension, wrong kind of file.
	writeSessionFile(t, dir, "notes.txt", "hello")
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	got := ActiveSessions(cwd, time.Hour)
	if len(got) != 1 {
		t.Fatalf("expected only the recently written session, got %#v", got)
	}
	f := got[0]
	if f.Path != recent || f.SessionID != "sess-recent" || f.Model != "claude-x" {
		t.Fatalf("session file fields not filled from the file: %#v", f)
	}
	if !sameDir(f.CWD, cwd) {
		t.Fatalf("cwd = %q, want %q", f.CWD, cwd)
	}
	if f.Size <= 0 || f.ModTime.IsZero() {
		t.Fatalf("size/mtime not reported: %#v", f)
	}
}

// Two live-looking files must come back newest first, and an explicit
// non-positive age falls back to the bounded default window rather than
// listing the whole history.
func TestActiveSessionsOrdersNewestFirst(t *testing.T) {
	dir := useSessionDir(t)
	cwd := t.TempDir()
	older := writeSessionFile(t, dir, "2026-01-01T10-00-00_a.jsonl", sessionHeader(cwd, "sess-a"))
	newer := writeSessionFile(t, dir, "2026-01-01T11-00-00_b.jsonl", sessionHeader(cwd, "sess-b"))
	t1 := time.Now().Add(-2 * time.Minute)
	t2 := time.Now().Add(-1 * time.Minute)
	if err := os.Chtimes(older, t1, t1); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, t2, t2); err != nil {
		t.Fatal(err)
	}
	got := ActiveSessions(cwd, 0) // 0 => default window, both are inside it
	if len(got) != 2 || got[0].SessionID != "sess-b" || got[1].SessionID != "sess-a" {
		t.Fatalf("expected newest first, got %#v", got)
	}
}

// An unreadable or absent session directory is a normal answer, not an error:
// /live still has its bridge candidates, so a broken file source must never be
// a way to break the command.
func TestActiveSessionsOnMissingDirectoryYieldsEmptyList(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(t.TempDir(), "nope"))
	if got := ActiveSessions(t.TempDir(), time.Hour); len(got) != 0 {
		t.Fatalf("missing session dir must yield an empty list, got %#v", got)
	}
}

// pi resolves its own cwd to a physical path before deriving the session
// directory name, so a symlinked cwd used to be looked up under a directory
// name pi never wrote to. The same session must be found through the symlink
// and through the real path, and a cwd that does not exist must degrade to an
// empty list (not an error, not a panic).
func TestActiveSessionsFindsSessionThroughSymlinkedCwd(t *testing.T) {
	// The per-project directory slug is the point of this test, so the flat
	// session-dir override must be OFF: SessionDirFor has to derive the name
	// from the resolved path. PI_CODING_AGENT_DIR keeps everything in a temp
	// dir, so the developer's real ~/.pi is never read.
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

	real := t.TempDir() // already physical on macOS (/var -> /private/var)
	// An explicit symlink, so the test is not a no-op on a platform where
	// TempDir is already physical: t.TempDir() as the symlink's parent keeps
	// both halves inside the test's own temp space.
	link := filepath.Join(t.TempDir(), "wd")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if filepath.Clean(link) == filepath.Clean(real) {
		t.Fatal("the symlink path must differ from the real path, or this test proves nothing")
	}
	if !sameDir(link, real) {
		t.Fatal("the symlink must resolve to the real path, or there is nothing to resolve")
	}

	// The session dir pi itself would have created: derived from the PHYSICAL
	// cwd, which is what the file's own header also records.
	phys := physicalPath(real)
	dir := pirpc.SessionDirFor(phys)
	if dir == pirpc.SessionDirFor(link) {
		t.Skipf("platform does not distinguish the symlink path, nothing to fix")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := writeSessionFile(t, dir, "2026-01-02T10-00-00_sym.jsonl",
		sessionHeader(phys, "sess-sym"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-x","timestamp":"2026-01-02T10:00:01.000Z"}`)

	// Both spellings of the same directory must find the one session.
	for _, cwd := range []string{link, real} {
		got := ActiveSessions(cwd, time.Hour)
		if len(got) != 1 {
			t.Fatalf("cwd %q: expected the one session, got %#v", cwd, got)
		}
		if got[0].Path != want || got[0].SessionID != "sess-sym" || got[0].Model != "claude-x" {
			t.Fatalf("cwd %q: wrong session: %#v", cwd, got[0])
		}
	}
	// A cwd that does not exist is a normal empty answer, like a missing
	// session dir: /live still has its bridge candidates.
	if got := ActiveSessions(filepath.Join(t.TempDir(), "gone"), time.Hour); len(got) != 0 {
		t.Fatalf("nonexistent cwd must yield an empty list, got %#v", got)
	}
}

// The whole point of the second source: a pi that never loaded the bridge is
// still followable, because it is still writing its session file.
func TestCandidatesAppendsActiveFileSessions(t *testing.T) {
	useDescDir(t)
	withProcs(t, nil)
	dir := useSessionDir(t)
	cwd := t.TempDir()
	path := writeSessionFile(t, dir, "2026-01-02T10-00-00_s.jsonl",
		sessionHeader(cwd, "sess-file"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-x","timestamp":"2026-01-02T10:00:01.000Z"}`)

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one file candidate, got %#v", got)
	}
	c := got[0]
	if c.Source != SourceFile || c.File == nil || c.File.Path != path {
		t.Fatalf("file candidate not shaped as one: %#v", c)
	}
	if c.PID != 0 || !c.Streamable || c.SessionID != "sess-file" || c.Model != "claude-x" {
		t.Fatalf("file candidate fields wrong: %#v", c)
	}
	if c.Descriptor != nil {
		t.Fatalf("a file candidate must not carry a bridge descriptor: %#v", c)
	}
}

// A session that has both a bridge and a file is ONE row, as its bridge:
// listing it twice would leave the user unable to tell which row is which.
func TestCandidatesListsFileSessionOnceWhenBridgeAlsoExists(t *testing.T) {
	dir := useDescDir(t)
	sessDir := useSessionDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	withProcs(t, []procInfo{{PID: pid, CWD: cwd}})
	writeForeignDescriptor(t, dir, pid, sseEndpoint(t), cwd, 11) // the same session id
	writeSessionFile(t, sessDir, "2026-01-02T10-00-00_dup.jsonl",
		sessionHeader(cwd, fmt.Sprintf("sess-%d", pid)))

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a session with both sources must be listed once, got %#v", got)
	}
	if got[0].Source != SourceBridge || got[0].File != nil || !got[0].Streamable {
		t.Fatalf("the bridge row must win: %#v", got[0])
	}
}

// A custom entry must become the exact row the bridge snapshot builds for a
// custom_message, or extension output (/team, /team-result) would render
// differently from a bridged session.
func TestTranscriptRowMapsCustomEntriesLikeTheBridge(t *testing.T) {
	raw := `{"type":"custom","id":"c1","parentId":"m2","timestamp":"2026-01-02T10:00:05.000Z",` +
		`"customType":"pi-agent-team/status","display":true,"content":"workers 2"}`
	var e sessionEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	row, ok := transcriptRow(&e)
	if !ok {
		t.Fatal("a custom entry with a customType must produce a transcript row")
	}
	var got customRow
	if err := json.Unmarshal(row, &got); err != nil {
		t.Fatal(err)
	}
	if got.Role != "custom" || got.CustomType != "pi-agent-team/status" || !got.Display {
		t.Fatalf("custom row not in the bridge shape: %s", row)
	}
	if got.Timestamp != time.Date(2026, 1, 2, 10, 0, 5, 0, time.UTC).UnixMilli() {
		t.Fatalf("timestamp = %d, want the entry's millis", got.Timestamp)
	}
	if string(got.Content) != `"workers 2"` {
		t.Fatalf("content = %s, want the entry content", got.Content)
	}
}

// The system preamble is what a resumed session does not replay, so a file
// tail must drop it too.
func TestTranscriptRowSkipsTheSystemPreamble(t *testing.T) {
	var e sessionEntry
	if err := json.Unmarshal([]byte(`{"type":"message","id":"m0","message":{"role":"system","content":"preamble"}}`), &e); err != nil {
		t.Fatal(err)
	}
	if _, ok := transcriptRow(&e); ok {
		t.Fatal("the system preamble must not become a transcript row")
	}
}

// A session file is a tree: the rows must follow the newest branch only, or a
// fork would render lines the user undid.
func TestActiveBranchFollowsTheNewestBranch(t *testing.T) {
	lines := []string{
		`{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"first"}}`,
		`{"type":"message","id":"b","parentId":"a","message":{"role":"assistant","content":["second"]}}`,
		`{"type":"message","id":"a2","parentId":"a","message":{"role":"user","content":"branch"}}`,
		`{"type":"message","id":"b2","parentId":"a2","message":{"role":"assistant","content":["on branch"]}}`,
	}
	var entries []sessionEntry
	for _, l := range lines {
		var e sessionEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	rows, leaf := activeBranch(entries)
	if leaf != "b2" || len(rows) != 3 {
		// b2 -> a2 -> a: the branch keeps the shared root and adds its own
		// pair, and drops the abandoned b ("second").
		t.Fatalf("branch = leaf %q with %d rows, want leaf b2 and 3 rows", leaf, len(rows))
	}
	if got := string(rows[0]); got != `{"role":"user","content":"first"}` {
		t.Fatalf("first row = %s, want the shared root message", got)
	}
	if got := string(rows[1]); got != `{"role":"user","content":"branch"}` {
		t.Fatalf("second row = %s, want the forked branch's user message", got)
	}
	if got := string(rows[2]); got != `{"role":"assistant","content":["on branch"]}` {
		t.Fatalf("third row = %s, want the forked branch's answer", got)
	}
}

// A session file with no message entries yet must still yield an explicit
// (empty) list, never nil: the app ranges over it directly.
func TestActiveBranchIsEmptyNotNilForAFreshSession(t *testing.T) {
	rows, leaf := activeBranch([]sessionEntry{{Type: "session", ID: "s"}})
	if rows == nil || len(rows) != 0 || leaf != "" {
		t.Fatalf("fresh session = %v (leaf %q), want an empty non-nil list", rows, leaf)
	}
}

// Bridge candidates keep their existing order and come before file candidates.
func TestCandidatesKeepsBridgeOrderBeforeFileCandidates(t *testing.T) {
	dir := useDescDir(t)
	sessDir := useSessionDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	withProcs(t, []procInfo{{PID: pid, CWD: cwd}})
	writeForeignDescriptor(t, dir, pid, sseEndpoint(t), cwd, 3)
	writeSessionFile(t, sessDir, "2026-01-02T10-00-00_f.jsonl", sessionHeader(cwd, "sess-file"))

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected bridge + file candidates, got %#v", got)
	}
	if got[0].Source != SourceBridge || got[1].Source != SourceFile {
		t.Fatalf("bridge candidate must come first: %#v", got)
	}
}
