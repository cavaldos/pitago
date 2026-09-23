package pirpc

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SessionInfo is one *.jsonl session file, same fields pi's --resume picker
// shows (name/first message, message count, last activity).
type SessionInfo struct {
	Path         string
	ID           string
	Name         string // latest session_info name (--name / /name), may be ""
	Cwd          string
	FirstMessage string // first user text, "" when none
	MessageCount int
	Modified     time.Time // last entry time, fallback file mtime
}

// Title is the picker row: named session wins, else first user message.
func (s SessionInfo) Title() string {
	if strings.TrimSpace(s.Name) != "" {
		return strings.TrimSpace(s.Name)
	}
	if strings.TrimSpace(s.FirstMessage) != "" {
		return strings.TrimSpace(s.FirstMessage)
	}
	return "(no messages)"
}

// SessionDirFor mirrors pi's getDefaultSessionDirPath(cwd):
// $PI_CODING_AGENT_SESSION_DIR when set, else
// <agentDir>/sessions/--<cwd without leading slash, /:\ → ->--.
// agentDir is $PI_CODING_AGENT_DIR or ~/.pi/agent.
func SessionDirFor(cwd string) string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	agent := os.Getenv("PI_CODING_AGENT_DIR")
	if agent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		agent = filepath.Join(home, ".pi", "agent")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	abs = filepath.Clean(abs)
	slug := strings.TrimPrefix(abs, string(filepath.Separator))
	slug = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, slug)
	return filepath.Join(agent, "sessions", "--"+slug+"--")
}

// SessionRoot is the sessions base dir: $PI_CODING_AGENT_SESSION_DIR when
// set, else <agentDir>/sessions. Per-project dirs live underneath it
// (except the flat-override case, where it IS the session dir).
func SessionRoot() string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	agent := os.Getenv("PI_CODING_AGENT_DIR")
	if agent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		agent = filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(agent, "sessions")
}

// ListSessions reads the newest-first *.jsonl files in dir (cap files at
// maxFiles), skipping invalid ones. When sameCwdOnly, entries whose header
// cwd doesn't resolve to cwd are dropped (global session-dir case).
// Sessions whose stored cwd no longer exists are always dropped: pi can't
// resume them and exits instead.
func ListSessions(dir, cwd string, maxFiles int, sameCwdOnly bool) []SessionInfo {
	if dir == "" || maxFiles <= 0 {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		names = append(names, e.Name())
	}
	// filenames start with an ISO timestamp → reverse alpha = newest first
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > maxFiles {
		names = names[:maxFiles]
	}
	var out []SessionInfo
	for _, n := range names {
		si, ok := scanSession(filepath.Join(dir, n))
		if !ok {
			continue
		}
		if sameCwdOnly && cwd != "" && !sameDir(si.Cwd, cwd) {
			continue
		}
		out = append(out, si)
	}
	// newest activity first (pi's sortSessionInfos)
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// ListAllSessions is pi's "All" scope: newest-first *.jsonl across every
// project dir under root (cap maxFiles), no cwd filtering. A flat root
// ($PI_CODING_AGENT_SESSION_DIR override) reads as a single dir.
// Sessions whose stored cwd is gone are dropped (pi can't resume them).
func ListAllSessions(root string, maxFiles int) []SessionInfo {
	if root == "" || maxFiles <= 0 {
		return nil
	}
	var paths []string
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil
	}
	// flat dir case (env override): files sit directly in root
	if names, err := sessionFiles(root); err == nil && len(names) > 0 {
		for _, n := range names {
			paths = append(paths, filepath.Join(root, n))
		}
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		names, err := sessionFiles(sub)
		if err != nil {
			continue
		}
		for _, n := range names {
			paths = append(paths, filepath.Join(sub, n))
		}
	}
	// filenames start with an ISO timestamp → reverse alpha = newest first
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
	}
	out := make([]SessionInfo, 0, len(paths))
	for _, p := range paths {
		if si, ok := scanSession(p); ok {
			out = append(out, si)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// sessionFiles lists *.jsonl basenames in dir ("" dirs and errors → nil).
func sessionFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// DeleteSession removes one session file (picker delete). Only *.jsonl
// files are removed; anything else is refused.
func DeleteSession(path string) error {
	if path == "" || !strings.HasSuffix(path, ".jsonl") {
		return os.ErrInvalid
	}
	return os.Remove(path)
}

// Shorten renders a path like pi's All scope (~/ for home).
// Windows-safe: matches both / and \ separators; never errors,
// returns p unchanged when home is unknown or doesn't match.
func Shorten(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+"/") || strings.HasPrefix(p, home+"\\") {
			return "~" + p[len(home):]
		}
	}
	return p
}

// maxScanLines bounds picker cost on huge histories (name/count stay
// best-effort past this; first message is almost always early).
const maxScanLines = 20000

// scanSession parses one session file: header + first user text + latest
// session_info name + message count + last timestamp.
func scanSession(path string) (SessionInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, false
	}
	defer f.Close()
	var si SessionInfo
	si.Path = path
	si.Modified = time.Now()
	if st, err := f.Stat(); err == nil {
		si.Modified = st.ModTime()
	}
	br := bufio.NewReaderSize(f, 64*1024)
	var header json.RawMessage
	lines := 0
	for {
		line, err := br.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			lines++
			var env struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Timestamp string `json:"timestamp"`
				Cwd       string `json:"cwd"`
				Name      string `json:"name"`
				Message   *struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &env) == nil {
				switch env.Type {
				case "session":
					if header == nil {
						header = json.RawMessage(line)
						si.ID = env.ID
						si.Cwd = env.Cwd
						if t, err := time.Parse(time.RFC3339, env.Timestamp); err == nil {
							si.Modified = t
						}
					}
				case "session_info":
					// latest wins; an explicit clear empties the title
					si.Name = strings.TrimSpace(env.Name)
				case "message":
					if header == nil {
						return SessionInfo{}, false // not a pi session
					}
					si.MessageCount++
					if t, err := time.Parse(time.RFC3339, env.Timestamp); err == nil {
						if t.After(si.Modified) {
							si.Modified = t
						}
					}
					if si.FirstMessage == "" && env.Message != nil && env.Message.Role == "user" {
						if t := oneLine(TextOf(env.Message.Content)); t != "" {
							si.FirstMessage = t
						}
					}
				default:
					if t, err := time.Parse(time.RFC3339, env.Timestamp); err == nil {
						if t.After(si.Modified) {
							si.Modified = t
						}
					}
				}
			}
			if lines >= maxScanLines {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if header == nil {
		return SessionInfo{}, false
	}
	if si.Cwd != "" {
		// pi refuses to resume a session whose stored cwd is gone
		// ("Stored session working directory does not exist") and
		// exits — listing it would be a trap, so skip it.
		if fi, err := os.Stat(si.Cwd); err != nil || !fi.IsDir() {
			return SessionInfo{}, false
		}
	}
	return si, true
}

// oneLine collapses whitespace for picker rows.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// sameDir compares two cwds after Abs+Clean (+best-effort symlink resolve,
// so /tmp vs /private/tmp on macOS still matches like pi).
func sameDir(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		aa = a
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		bb = b
	}
	aa, bb = filepath.Clean(aa), filepath.Clean(bb)
	if aa == bb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(aa); err == nil {
		aa = ra
	}
	if rb, err := filepath.EvalSymlinks(bb); err == nil {
		bb = rb
	}
	return aa == bb
}

// Ago renders a modified time like pi's relative picker hints.
func Ago(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 30*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	default:
		return t.Format("Jan 02")
	}
}
