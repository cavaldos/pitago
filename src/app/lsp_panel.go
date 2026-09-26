package app

// LSP sidebar panel: the workspace's language-server diagnostics, grouped for
// a 30-cell column.
//
// Data source. pitago never speaks LSP. The pi-lsp extension
// (npm:@narumitw/pi-lsp) owns the language servers and exposes one tool,
// lsp_diagnostics, whose result pi already streams to us over RPC as
// Block.ToolResult — the same route the Todos panel uses (todo tool calls over
// RPC, not a disk poll). pi-lsp publishes nothing else: it writes no cache
// file, and its only UI call is a transient ui.setStatus while a server runs
// (src/runner.ts), which says "gopls ready", not what is wrong where. So the
// last lsp_diagnostics result in the transcript is the whole data source, and
// parsing it is cheaper and more faithful than a second, invented channel.
//
// Wire format (pi-lsp runner.formatDiagnostics, one line per diagnostic):
//
//	gopls LSP diagnostics: 3 diagnostic(s) across 2 file(s).
//
//	src/app/x.go:12:5: error staticcheck: unused variable
//	src/app/y.go:3:1: warning govet S1000: should use x += 1
//
// Multi-server runs join their sections with "\n\n---\n\n", each prefixed by
// the route reason, and a skipped server appends a prose note. Anything that
// is not a diagnostic line is ignored, so the parser needs no special cases.
//
// Refresh. The snapshot is derived from m.blocks on every render, so it is
// current the moment the tool result lands — no tick, no poller, no goroutine
// to leak. Parsing is memoized on the payload fingerprint, so repeated frames
// between tool events cost one hash of the string and nothing else.

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// lspDiagnosticsTool is the pi-lsp tool whose result the panel reads.
const lspDiagnosticsTool = "lsp_diagnostics"

// lspRowsMax caps drawn diagnostic rows; the rest collapse into a "+N more"
// line (same convention as Todos / Workspace) so one bad file cannot push the
// rest of the sidebar out of the viewport.
const lspRowsMax = 20

// Severities as pi-lsp names them (runner.severityName: 1..4, else generic).
const (
	lspSevError   = "error"
	lspSevWarning = "warning"
	lspSevInfo    = "info"
	lspSevHint    = "hint"
	lspSevGeneric = "diagnostic"
)

// LspStatus is what the tool said *besides* diagnostics. pi-lsp is explicit
// when it has nothing to report and why, and swallowing that leaves the panel
// claiming "no diagnostics" for what is really a missing language server — the
// single most confusing state this panel can be in.
type LspStatus struct {
	Server  string   // server name from the summary header, e.g. "gopls"
	Clean   bool     // the server ran and reported a genuinely clean file
	Skipped []string // servers pi-lsp could not find
	Note    string   // a verbatim error line (bad server param, unknown server)
}

// lspSummaryHeader is "<server> LSP diagnostics: N diagnostic(s) across M file(s)."
var lspSummaryHeader = regexp.MustCompile(`^(\S+) LSP diagnostics: \d+ diagnostic\(s\) across \d+ file\(s\)\.$`)

// lspSkippedLine is pi-lsp's "Skipped unavailable default LSP server(s): a, b."
var lspSkippedLine = regexp.MustCompile(`^Skipped unavailable default LSP server\(s\):\s*(.+?)\.?$`)

// lspNotFoundLine is "<server> LSP command not found: <cmd>. Install ..."
var lspNotFoundLine = regexp.MustCompile(`^(\S+) LSP command not found:\s*(\S+)`)

// lspStatusDistilled collapses a tool result into the non-diagnostic facts the
// panel needs. It is deliberately forgiving: an unrecognised line becomes a
// note rather than being dropped, because silence is what made the empty panel
// undiagnosable in the first place.
func lspStatusDistilled(out string) LspStatus {
	var st LspStatus
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if m := lspSummaryHeader.FindStringSubmatch(trimmed); m != nil {
			st.Server = m[1]
			// A header that parsed means the server actually ran.
			st.Clean = true
			continue
		}
		if m := lspSkippedLine.FindStringSubmatch(trimmed); m != nil {
			for _, s := range strings.Split(m[1], ",") {
				if s = strings.TrimSpace(s); s != "" {
					st.Skipped = append(st.Skipped, s)
				}
			}
			continue
		}
		if lspNotFoundLine.MatchString(trimmed) {
			// Its own prose is already actionable; keep it as the note.
			if st.Note == "" {
				st.Note = trimmed
			}
			continue
		}
		// "<path>: no diagnostics" is the per-file clean line; the header
		// already covers that case, so it is not a note.
		if strings.HasSuffix(trimmed, ": no diagnostics") {
			st.Clean = true
			continue
		}
		if trimmed == "" || lspDiagLine.MatchString(line) || trimmed == "---" {
			continue
		}
		if st.Note == "" {
			st.Note = trimmed
		}
	}
	return st
}

// lspStatusSummary is the one-line reason the panel shows when there are no
// diagnostic rows: why it is empty, in the user's terms rather than a shrug.
func lspStatusSummary(st LspStatus, inner int) string {
	switch {
	case len(st.Skipped) > 0 && st.Note != "":
		// A named missing command beats the generic skip list.
		return " — " + Short(strings.TrimSuffix(st.Note, "."), inner-3)
	case len(st.Skipped) > 0:
		// The actionable fix is the same for every server on the list, so a
		// count that always fits beats names the column truncates away.
		return fmt.Sprintf(" — no LSP server available (%d skipped)", len(st.Skipped))
	case st.Note != "":
		// A verbatim tool error is more useful than any paraphrase, but the
		// tail is the advice, so keep the head.
		return " — " + Short(st.Note, inner-3)
	case st.Clean && st.Server != "":
		return " — " + st.Server + " clean"
	}
	return ""
}

// LspDiagnostic is one parsed diagnostic: a workspace-relative path, a 1-based
// line/column (LSP is 0-based; the tool already adds one), and the message
// with its source and optional code split out.
type LspDiagnostic struct {
	File     string
	Line     int
	Col      int
	Severity string
	Source   string
	Code     string
	Message  string
}

// lspSeverityRank orders rows inside a file: errors first, hints last.
var lspSeverityRank = map[string]int{
	lspSevError:   0,
	lspSevWarning: 1,
	lspSevInfo:    2,
	lspSevHint:    3,
	lspSevGeneric: 4,
}

func lspRank(sev string) int {
	if r, ok := lspSeverityRank[sev]; ok {
		return r
	}
	return len(lspSeverityRank)
}

// lspDiagLine matches "<path>:<line>:<col>: <severity> <source[ code]>".
// The path is lazy so a Windows drive letter or a path with a colon still
// leaves the trailing numbers to anchor the match.
var lspDiagLine = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+(error|warning|info|hint|diagnostic)\s+(.+)$`)

// parseLspDiagnostics turns one lsp_diagnostics result into diagnostics,
// ignoring the summary header, the "no diagnostics" per-file lines, the route
// reasons and the "skipped server" note. Unparseable lines are dropped, never
// guessed at.
func parseLspDiagnostics(out string) []LspDiagnostic {
	var diags []LspDiagnostic
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := lspDiagLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// Rest is "<source>[ <code>]: <message>"; the first ": " ends it,
		// so a message containing colons stays intact.
		head, msg, ok := strings.Cut(m[5], ": ")
		if !ok {
			head, msg = m[5], ""
		}
		line1, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		d := LspDiagnostic{
			File:     strings.TrimSpace(m[1]),
			Line:     line1,
			Col:      col,
			Severity: strings.ToLower(m[4]),
			Message:  strings.TrimSpace(msg),
		}
		if d.File == "" {
			continue
		}
		fields := strings.Fields(head)
		if len(fields) > 0 {
			d.Source = fields[0]
		}
		if len(fields) > 1 {
			d.Code = strings.Join(fields[1:], " ")
		}
		diags = append(diags, d)
	}
	return diags
}

// lastLspResult finds the newest finished lsp_diagnostics result in the
// transcript. A still-running call (partial result, no text yet) is skipped so
// the panel keeps showing the previous report instead of blanking out.
func lastLspResult(blocks []Block) (string, string, bool) {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if b.Kind != "tool" || b.ToolName != lspDiagnosticsTool {
			continue
		}
		out := strings.TrimSpace(b.ToolResult)
		if out == "" {
			continue
		}
		return b.ToolCallID, out, true
	}
	return "", "", false
}

// lastLspResultRaw is lastLspResult without the call id: the panel needs the
// payload text itself to explain an empty section, not just whether one exists.
func lastLspResultRaw(blocks []Block) (string, bool) {
	_, out, ok := lastLspResult(blocks)
	return out, ok
}

// lspCache memoizes the parse so a frame that redraws the sidebar (scroll,
// hover, resize) does not re-run the regex over the same payload.
var lspCache struct {
	mu    sync.Mutex
	sum   uint64
	n     int
	diags []LspDiagnostic
}

func lspFingerprint(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// lspDiagnostics returns the sidebar snapshot: diagnostics from the newest
// lsp_diagnostics result, nil when the agent has not run the tool yet. The
// returned slice is shared and must not be mutated.
func lspDiagnostics(blocks []Block) []LspDiagnostic {
	_, out, ok := lastLspResult(blocks)
	if !ok {
		return nil
	}
	sum, n := lspFingerprint(out), len(out)
	lspCache.mu.Lock()
	defer lspCache.mu.Unlock()
	if lspCache.diags != nil && lspCache.sum == sum && lspCache.n == n {
		return lspCache.diags
	}
	diags := parseLspDiagnostics(out)
	if diags == nil {
		diags = []LspDiagnostic{} // cache a miss too: empty is a real answer
	}
	lspCache.sum, lspCache.n, lspCache.diags = sum, n, diags
	return diags
}

// lspDiags is the sidebar's view of the snapshot.
func (m Model) lspDiags() []LspDiagnostic { return lspDiagnostics(m.blocks) }

// lspCounts splits a snapshot into the badge numbers: errors and warnings.
// Infos and hints ride along in the rows but never inflate the badge.
func lspCounts(diags []LspDiagnostic) (errs, warns int) {
	for _, d := range diags {
		switch d.Severity {
		case lspSevError:
			errs++
		case lspSevWarning:
			warns++
		}
	}
	return errs, warns
}

// LspFile is one file's diagnostics, ordered by severity then position.
type LspFile struct {
	Path  string
	Diags []LspDiagnostic
}

// groupLspDiagnostics groups by file, then orders: files with errors first
// (then warnings, then path), rows by severity, line and column.
func groupLspDiagnostics(diags []LspDiagnostic) []LspFile {
	files := make([]LspFile, 0, len(diags))
	at := map[string]int{}
	for _, d := range diags {
		i, ok := at[d.File]
		if !ok {
			at[d.File] = len(files)
			files = append(files, LspFile{Path: d.File})
			i = len(files) - 1
		}
		files[i].Diags = append(files[i].Diags, d)
	}
	for i := range files {
		rows := files[i].Diags
		sort.SliceStable(rows, func(a, b int) bool {
			if ra, rb := lspRank(rows[a].Severity), lspRank(rows[b].Severity); ra != rb {
				return ra < rb
			}
			if rows[a].Line != rows[b].Line {
				return rows[a].Line < rows[b].Line
			}
			if rows[a].Col != rows[b].Col {
				return rows[a].Col < rows[b].Col
			}
			return rows[a].Message < rows[b].Message
		})
		files[i].Diags = rows
	}
	sort.SliceStable(files, func(a, b int) bool {
		ea, wa := lspCounts(files[a].Diags)
		eb, wb := lspCounts(files[b].Diags)
		if ea != eb {
			return ea > eb
		}
		if wa != wb {
			return wa > wb
		}
		return files[a].Path < files[b].Path
	})
	return files
}

// lspCollapsed is the section's header collapse state (▸ closed / ▾ open),
// open by default like PLUGINS. atomic because View and Update both read it.
var lspCollapsed atomic.Bool

// ToggleLspCollapsed folds/unfolds the LSP section body.
func ToggleLspCollapsed() { lspCollapsed.Store(!lspCollapsed.Load()) }

// LspCollapsed reports the current fold state (header mark + row rendering).
func LspCollapsed() bool { return lspCollapsed.Load() }
