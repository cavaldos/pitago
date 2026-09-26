package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"time"

	"pitago/src/pirpc"
)

// errShrank marks a session file that is shorter than what we already
// consumed: pi rotated or truncated it, so the follow must re-snapshot
// instead of appending to a transcript that no longer exists.
var errShrank = errors.New("live: session file shrank")

// Source says where a followable session was discovered. A bridge session is
// live (events arrive over SSE); a file session is read off disk, so it can
// follow a pi that was started before the bridge extension existed.
type Source string

const (
	SourceBridge Source = "bridge"
	SourceFile   Source = "file"
)

// SessionFile is one pi session transcript on disk. It is a whole-session
// handle rather than a descriptor: there is no endpoint, no token and no pid
// to talk to, only the append-only file pi keeps writing.
type SessionFile struct {
	Path      string
	SessionID string
	CWD       string
	Model     string
	Size      int64
	ModTime   time.Time
}

const (
	// defaultActiveAge is the freshness window used when a caller passes a
	// non-positive maxAge. A pi appends to its session file as it works, so
	// mtime is a decent "is this still running" signal; the bound keeps the
	// picker from listing last week's sessions.
	defaultActiveAge = 10 * time.Minute

	// maxScannedFiles bounds how many session files are parsed per lookup.
	// Each parse is already bounded (pirpc stops at its line cap), so this
	// only bounds the number of files the /live picker may pay for.
	maxScannedFiles = 50

	// maxFileCandidates bounds how many file candidates Candidates appends
	// after the bridge ones, so a directory with hundreds of live-looking
	// session files cannot flood the picker.
	maxFileCandidates = 10

	// modelPrefixBytes bounds the read used to find the latest model. The
	// model_change entry is near the top of a session file, so a prefix read
	// is enough; the file itself can be tens of megabytes and the picker
	// must stay instant.
	modelPrefixBytes = 64 << 10
)

// ActiveSessions lists the session files of cwd that look like a running pi,
// newest first.
//
// Liveness is mtime, not the last entry's timestamp: only the file that is
// actually being appended to is a session in progress. A session that merely
// carries a late timestamp is finished and belongs in the resume picker, not
// in the follow list.
//
// A missing or unreadable session directory yields an empty list rather than
// an error. /live already has a working answer without files (the bridge
// candidates), so failing here would turn a secondary source into a way to
// break the command. The same holds for a cwd that does not exist: the
// physical-path lookup degrades to the cleaned path and the lookup simply
// finds nothing.
func ActiveSessions(cwd string, maxAge time.Duration) []SessionFile {
	if maxAge <= 0 {
		maxAge = defaultActiveAge
	}
	// pi resolves its own cwd to a physical path before deriving the session
	// directory name, so a symlinked cwd (macOS /tmp and /var, a symlinked
	// checkout) would otherwise be looked up under a directory name pi never
	// wrote to. sameDir cannot save us here: it is the NAME that differs.
	// The per-file header check below still compares with sameDir, which
	// resolves both sides.
	dir := pirpc.SessionDirFor(physicalPath(cwd))
	if dir == "" {
		return nil
	}
	// ListSessions does the bounded header parse and drops everything that is
	// not a resumable pi session of this cwd (no session header, stored cwd
	// gone), which is exactly the pre-filter this needs.
	infos := pirpc.ListSessions(dir, cwd, maxScannedFiles, true)
	now := time.Now()
	out := make([]SessionFile, 0, len(infos))
	for _, si := range infos {
		st, err := os.Stat(si.Path)
		if err != nil || now.Sub(st.ModTime()) > maxAge {
			continue
		}
		out = append(out, SessionFile{
			Path:      si.Path,
			SessionID: si.ID,
			CWD:       si.Cwd,
			Model:     sessionModel(si.Path),
			Size:      st.Size(),
			ModTime:   st.ModTime(),
		})
	}
	// Newest first, with the path as tiebreak so two files written in the
	// same filesystem tick do not swap places between two openings.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].Path < out[j].Path
		}
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out
}

// sessionModel returns the latest model the session switched to, read from a
// bounded prefix of the file: the model_change entry is written near the
// top, and a picker hint must not cost a full read of a huge transcript.
func sessionModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, modelPrefixBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return ""
	}
	data := buf[:n]
	lines := bytes.Split(data, []byte("\n"))
	if n > 0 && data[n-1] != '\n' {
		// The last line of the prefix is possibly half written; a model hint
		// must never come from an unparsable fragment.
		lines = lines[:len(lines)-1]
	}
	model := ""
	for _, line := range lines {
		var e sessionEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.Type == "model_change" && e.ModelID != "" {
			model = e.ModelID
		}
	}
	return model
}

// sessionEntry is one line of a pi session file. The vocabulary is pi's own
// (session / message / custom / model_change / thinking_level_change /
// session_info), so unknown types simply fall through every switch below.
type sessionEntry struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	ParentID      *string         `json:"parentId"`
	Timestamp     string          `json:"timestamp"`
	Provider      string          `json:"provider"`
	ModelID       string          `json:"modelId"`
	ThinkingLevel string          `json:"thinkingLevel"`
	Name          string          `json:"name"`
	CustomType    string          `json:"customType"`
	Content       json.RawMessage `json:"content"`
	Display       bool            `json:"display"`
	Details       json.RawMessage `json:"details"`
	Message       json.RawMessage `json:"message"`
}

// customRow is the row the bridge snapshot builds for a custom_message
// entry. The file tail must produce the identical shape or the follow view
// would render extension output differently from a bridged session.
type customRow struct {
	Role       string          `json:"role"`
	Timestamp  int64           `json:"timestamp,omitempty"`
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content,omitempty"`
	Display    bool            `json:"display,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// transcriptRow converts one session entry into the row shape the app
// unmarshals (pirpc.AgentMessage), reporting false for entries that must not
// become a transcript row.
//
// The system preamble is dropped: a resumed session does not replay it, so
// keeping it here would make a file tail show a block no bridged session
// ever shows.
func transcriptRow(e *sessionEntry) (json.RawMessage, bool) {
	switch e.Type {
	case "message", "custom_message", "custom":
		if e.Message != nil {
			var probe struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(e.Message, &probe) != nil || probe.Role == "" || probe.Role == "system" {
				return nil, false
			}
			return e.Message, true
		}
		if e.CustomType == "" {
			return nil, false
		}
		raw, err := json.Marshal(customRow{
			Role:       "custom",
			Timestamp:  timestampMillis(e.Timestamp),
			CustomType: e.CustomType,
			Content:    e.Content,
			Display:    e.Display,
			Details:    e.Details,
		})
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	return nil, false
}

// timestampMillis mirrors the bridge's Date.parse(timestamp) so a custom row
// carries the same numeric stamp; 0 when the entry has no parsable one.
func timestampMillis(ts string) int64 {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// activeBranch returns the transcript rows of the session's newest branch, in
// root-first order, plus the id of its leaf entry.
//
// A session file is a tree: every entry names its parent, and the newest
// entry on disk is the leaf the user is looking at. Walking up from that leaf
// and reversing reproduces exactly what pi's own get_branch() would hand the
// bridge, so a fork or a compaction-rewound session renders the same lines.
func activeBranch(entries []sessionEntry) ([]json.RawMessage, string) {
	byID := make(map[string]int, len(entries))
	leaf := -1
	for i, e := range entries {
		switch e.Type {
		case "message", "custom_message", "custom":
			if e.ID != "" {
				byID[e.ID] = i
			}
			leaf = i
		}
	}
	if leaf < 0 {
		return []json.RawMessage{}, ""
	}
	var chain []json.RawMessage
	// Bounded by the entry count, so a corrupt parent cycle terminates.
	for i, steps := leaf, 0; i >= 0 && steps <= len(entries); steps++ {
		row, ok := transcriptRow(&entries[i])
		if ok {
			chain = append(chain, row)
		}
		parent := entries[i].ParentID
		if parent == nil || *parent == "" {
			break
		}
		idx, known := byID[*parent]
		if !known {
			break
		}
		i = idx
	}
	rows := make([]json.RawMessage, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		rows = append(rows, chain[i])
	}
	return rows, entries[leaf].ID
}

// fileCandidates appends the active-file rows to the already-sorted bridge
// candidates.
//
// Order is deliberate: a bridge session streams token by token and a file
// session only catches up on completed messages, so the better source stays
// on top. A session that has both is listed once, as its bridge, and the
// duplicate file row is dropped — otherwise the same session would appear
// twice and the user could not tell which row is which.
func fileCandidates(cwd string, bridges []Candidate) []Candidate {
	seen := make(map[string]bool, len(bridges))
	for _, c := range bridges {
		seen["id:"+c.SessionID] = true
	}
	files := ActiveSessions(cwd, defaultActiveAge)
	out := make([]Candidate, 0, len(files))
	for _, f := range files {
		if len(out) >= maxFileCandidates {
			break
		}
		key := "id:" + f.SessionID
		if f.SessionID == "" {
			// Without a session id the file itself is the identity.
			key = "path:" + f.Path
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		fc := f
		out = append(out, Candidate{
			Source:    SourceFile,
			File:      &fc,
			CWD:       f.CWD,
			Model:     f.Model,
			SessionID: f.SessionID,
			// A file session is followable without a bridge, which is the
			// whole point of this second source. The pid is genuinely unknown
			// (pi does not record it in the session file), so the picker must
			// render the row from Source rather than from PID.
			StartedAt:  f.ModTime.UnixMilli(),
			Streamable: true,
		})
	}
	return out
}

// scanCompleteLines splits a raw block into newline-terminated lines,
// dropping a half-written trailing one.
func scanCompleteLines(raw []byte) [][]byte {
	var out [][]byte
	for len(raw) > 0 {
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			return out
		}
		out = append(out, raw[:i])
		raw = raw[i+1:]
	}
	return out
}

// readCompleteLines reads everything appended after off and returns the
// complete lines plus the offset just past the last newline.
//
// A partial trailing line is neither returned nor consumed: pi writes a line
// in more than one write often enough that emitting half a JSON record would
// show the user a truncated message. The offset therefore only ever advances
// over a newline, so the rest is picked up on the next poll.
func readCompleteLines(path string, off int64) ([][]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if st.Size() < off {
		return nil, 0, errShrank
	}
	if _, err := f.Seek(off, 0); err != nil {
		return nil, 0, err
	}
	// Bounded by what is left of the file, which is the part a follow view has
	// to read anyway.
	raw, err := io.ReadAll(io.LimitReader(f, st.Size()-off))
	if err != nil {
		return nil, 0, err
	}
	n := bytes.LastIndexByte(raw, '\n')
	if n < 0 {
		return nil, off, nil
	}
	return scanCompleteLines(raw[:n+1]), off + int64(n) + 1, nil
}

// readSession reads a whole session file: its entries plus the byte offset
// just past the last newline, so a half-written trailing line is never
// counted as consumed even on the initial snapshot.
func readSession(path string) ([]sessionEntry, int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	n := bytes.LastIndexByte(raw, '\n')
	if n < 0 {
		return nil, 0, nil
	}
	off := int64(n) + 1
	var entries []sessionEntry
	for _, line := range bytes.Split(raw[:off], []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e sessionEntry
		if json.Unmarshal(line, &e) != nil || e.Type == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries, off, nil
}
