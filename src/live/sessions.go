package live

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// psTimeout bounds the process scan. The scan is a UX convenience (it
// populates a picker list), so it must never be the reason /live feels
// broken: if ps is slow or wedged we fall through to descriptors alone.
const psTimeout = 2 * time.Second

// probeTimeout bounds the "does this descriptor still answer" check per
// candidate. Loopback connect-refused returns immediately; the bound is only
// there so a half-open listener cannot stall the picker.
const probeTimeout = 500 * time.Millisecond

// Candidate is one followable session: either a pi process that published a
// live bridge, or a session file of a pi that did not.
//
// Source is what the picker must branch on. A bridge candidate is followed
// over SSE; a file candidate is followed by tailing File, and its PID is 0
// because a session file does not record the process that wrote it.
type Candidate struct {
	Source      Source
	PID         int
	CWD         string
	StartedAt   int64        // bridge start time; 0 when unknown
	Streamable  bool         // published a live-bridge descriptor we can follow
	Model       string       // may be ""
	SessionName string       // may be ""
	SessionID   string       // may be ""
	Descriptor  *Descriptor  // non-nil exactly when Streamable and bridge-sourced
	File        *SessionFile // non-nil exactly when SourceFile
}

// procInfo is the minimum a process scan yields per candidate.
type procInfo struct {
	PID int
	CWD string
}

// scanProcs is the process-scan seam. It is a variable so unit tests can
// inject a synthetic process list and never require a real pi binary; in
// production it is the ps-based scanner.
var scanProcs = scanPiProcesses

// runPS is the helper-command seam, injectable for the degradation tests.
var runPS = psCommand

// psCommand lists pid and command for every process.
//
// Deliberately no cwd column: macOS ps has no cwd= keyword and fails the whole
// invocation on it (exiting non-zero with "cwd: keyword not found"), so asking
// for it would break the primary, macOS-first platform. The working directory
// is resolved separately by the platform probe, per GOOS.
func psCommand() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Candidates lists pi processes running in cwd, streamable ones first/newest.
//
// The session inventory is best-effort by design. If the process scan is
// unavailable (no ps, sandboxed, unexpected output) we still return the
// streamable candidates from Discover rather than an error: descriptors are
// the part /live actually needs, and an error here would make /live fail
// outright on a machine where only the ps-based listing was ever optional.
// exclude lists pids the caller does not want as candidates — pitago passes
// its OWN owned pi child, which is a real `pi` process in the same directory
// and would otherwise be listed as a session the user cannot stream. It is
// the session the user is already driving in this window.
//
// A file candidate carries no pid, so the owned child cannot be excluded by
// pid there; see fileCandidates.
//
// Every returned row is followable: a process is a hint, not a session, so a
// pid with neither a bridge descriptor that answers nor a recent session file
// is dropped rather than offered as a row the picker can only refuse. pi does
// not record the writing process in a session file, so a bare pid can never be
// enriched into a followable row and is never paired with a file by guesswork.
func Candidates(cwd string, exclude ...int) ([]Candidate, error) {
	own, derr := Discover(cwd, "", os.Getpid())
	if derr != nil {
		// A descriptor directory we cannot read is a real error worth
		// surfacing; there is nothing to fall back to.
		return nil, derr
	}
	procs, _ := scanProcs(cwd) // scan failure degrades to descriptors only

	// Merge by pid: the process scan and the descriptors are two views of the
	// same sessions, and either can be incomplete. Deduplicating here means a
	// session seen by both keeps its name/model and gains streamability.
	// A scanned process is seeded with NO Source: the scan knows a process
	// exists, not how it can be followed, and Source is what the picker
	// branches on. attachDescriptor is what promotes a row to a bridge.
	byPID := make(map[int]*Candidate, len(procs)+len(own))
	for _, p := range procs {
		byPID[p.PID] = &Candidate{PID: p.PID, CWD: p.CWD}
	}
	for i := range own {
		d := own[i]
		c, ok := byPID[d.PID]
		if !ok {
			// The process scan missed it (or was unavailable). The descriptor
			// is itself evidence the process exists, so keep the session and
			// trust the descriptor's cwd.
			c = &Candidate{Source: SourceBridge, PID: d.PID, CWD: d.CWD}
			byPID[d.PID] = c
		} else if !sameDir(d.CWD, c.CWD) {
			// Same pid reported with a different cwd: a recycled pid, not our
			// session. Trust the process listing and drop the descriptor.
			continue
		}
		attachDescriptor(c, &d)
	}
	for _, pid := range exclude {
		if pid > 0 {
			delete(byPID, pid)
		}
	}

	out := make([]Candidate, 0, len(byPID))
	for _, c := range byPID {
		// Followable or nothing: a row is only worth showing if the bridge
		// endpoint answers or a session file can be tailed. Dropping the rest
		// here is what keeps the picker free of "cannot be followed" rows.
		if !c.Streamable && c.File == nil {
			continue
		}
		out = append(out, *c)
	}
	sortCandidates(out)
	// Bridge sessions first, then the ones only a file can reach: a pi that
	// was already running when the bridge extension appeared. Appending
	// (rather than merging into one sorted list) keeps the bridge ordering
	// byte-for-byte what it was and keeps a streamed session above a
	// catch-up-only one in the picker.
	return append(out, fileCandidates(cwd, out)...), nil
}

// attachDescriptor fills a candidate from a descriptor and marks it a bridge
// candidate: a descriptor is the evidence that makes a pid followable, so it is
// what decides the row's Source.
//
// A descriptor whose endpoint no longer answers (process up, server gone) is
// not streamable, and Candidates then drops the row unless a session file makes
// it followable anyway — a stale descriptor is a crash artifact, not a session.
func attachDescriptor(c *Candidate, d *Descriptor) {
	c.Source = SourceBridge
	if c.CWD == "" {
		c.CWD = d.CWD
	}
	c.StartedAt = d.StartedAt
	c.Model = d.Model
	c.SessionName = d.SessionName
	c.SessionID = d.SessionID
	c.Streamable = endpointAnswers(d)
	if c.Streamable {
		cp := *d
		c.Descriptor = &cp
	}
}

// endpointAnswers checks that a descriptor still serves on its loopback
// endpoint, reusing the validation Discover already applies. Probing the
// real endpoint (rather than trusting the file) is what makes Streamable
// honest: a stale descriptor left by a crashed process fails fast with
// connect-refused instead of being offered to the user.
func endpointAnswers(d *Descriptor) bool {
	u, err := url.Parse(d.Endpoint)
	if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || d.Token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Endpoint, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+d.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// sortCandidates orders streamable sessions first, newest first within each
// group, then the rest. It is a stable order with a deterministic tiebreak so
// the picker does not reshuffle between two consecutive opens.
func sortCandidates(in []Candidate) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.Streamable != b.Streamable {
			return a.Streamable
		}
		if a.StartedAt != b.StartedAt {
			return a.StartedAt > b.StartedAt
		}
		return a.PID < b.PID
	})
}

// resolveCWDsFn is the cwd-probe seam, declared by exactly one platform file
// (cwprobe_linux.go / cwprobe_other.go) so the platform split is explicit at
// the declaration site rather than resolved through a shared symbol. Tests
// replace it to drive the filter logic hermetically; the real probe is covered
// separately by a test that runs it against a real child process, because
// faked seams alone cannot catch a platform whose probe cannot report a cwd.

// scanPiProcesses enumerates pi processes whose working directory is cwd.
//
// Two steps, because no single portable command returns both halves: `ps`
// gives pid + command everywhere, then the platform probe supplies the working
// directory per platform. Failure in either step degrades to a shorter list,
// never an error — see Candidates.
func scanPiProcesses(cwd string) ([]procInfo, error) {
	raw, err := runPS()
	if err != nil {
		return nil, err
	}
	cands := parsePSPi(raw)
	if len(cands) == 0 {
		return nil, nil
	}
	pids := make([]int, 0, len(cands))
	for _, c := range cands {
		pids = append(pids, c.PID)
	}
	// One batched probe for every candidate pid, never one call per pid.
	dirs := resolveCWDsFn(pids)
	out := make([]procInfo, 0, len(cands))
	for _, c := range cands {
		dir, ok := dirs[c.PID]
		// An unresolvable pid is skipped, never guessed: lsof omits processes
		// it may not inspect, and a wrong cwd would offer /live a session
		// from another project.
		if !ok || !sameDir(dir, cwd) {
			continue
		}
		c.CWD = dir
		out = append(out, c)
	}
	return out, nil
}

// parsePSPi extracts the pi processes from `ps -axo pid=,command=` output.
//
// Defensive by construction: unexpected lines are skipped rather than failing
// the scan. The command may carry pi's own flags, so the executable is
// fields[1] and the remainder of the line is ignored. A bare "pi" line has
// only two fields, so the guard must not demand three.
func parsePSPi(raw string) []procInfo {
	var out []procInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		if !isPiCommand(fields[1]) {
			continue
		}
		out = append(out, procInfo{PID: pid})
	}
	return out
}

// isPiCommand reports whether a command line is a pi process. It matches the
// bare binary and a path ending in /pi, but not a path that merely contains
// "pi" (a path like ~/dev/pitago/bin is pitago itself, not pi).
func isPiCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	if i := strings.LastIndexAny(cmd, "/\\"); i >= 0 {
		cmd = cmd[i+1:]
	}
	return cmd == "pi" || cmd == "pi.exe"
}
