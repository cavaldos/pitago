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
	"testing"
	"time"
)

// TestLivePIDHelperProcess is not a test: it is a child that stays alive so a
// test can publish a descriptor for a pid that is genuinely running but is
// not our own (Discover excludes our own pid).
func TestLivePIDHelperProcess(t *testing.T) {
	if os.Getenv("PITAGO_LIVE_PID_HELPER") != "1" {
		t.Skip("helper process")
	}
	// Long enough for any test, short enough that a leaked helper is not a
	// permanently stray process.
	<-context.Background().Done()
}

// liveForeignPID starts a child that blocks, and returns its pid. The child
// is reaped on cleanup so no test leaks a process.
func liveForeignPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestLivePIDHelperProcess")
	cmd.Env = append(os.Environ(), "PITAGO_LIVE_PID_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start live helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// useDescDir points the descriptor directory at a temp dir so tests never
// read the developer's real runtime dir.
func useDescDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", dir)
	return dir
}

// sseEndpoint returns a loopback URL that answers 200, standing in for a
// bridge that is actually serving.
func sseEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/events"
}

// writeForeignDescriptor publishes a descriptor for an explicit live pid in
// the conventional single-descriptor file name.
func writeForeignDescriptor(t *testing.T, dir string, pid int, endpoint, cwd string, startedAt int64) {
	t.Helper()
	writeForeignDescriptorFile(t, dir, "session.json", pid, endpoint, cwd, startedAt)
}

// writeForeignDescriptorFile is the same under a chosen file name, so a test
// can publish two sessions at once (a dir holds one descriptor per pi).
func writeForeignDescriptorFile(t *testing.T, dir, name string, pid int, endpoint, cwd string, startedAt int64) {
	t.Helper()
	raw := fmt.Sprintf(`{"version":1,"endpoint":%q,"token":"secret","sessionId":"sess-%d",`+
		`"sessionName":"named","model":"gpt-x","cwd":%q,"pid":%d,"startedAt":%d}`,
		endpoint, pid, cwd, pid, startedAt)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withProcs replaces the process-scan seam for one test and restores it.
func withProcs(t *testing.T, procs []procInfo) {
	t.Helper()
	prev := scanProcs
	scanProcs = func(string) ([]procInfo, error) { return procs, nil }
	t.Cleanup(func() { scanProcs = prev })
}

func TestCandidatesEmptyWhenNoProcessesAndNoDescriptors(t *testing.T) {
	useDescDir(t)
	withProcs(t, nil)
	got, err := Candidates(t.TempDir())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no candidates, got %#v", got)
	}
}

// pitago's own pi child is a real process in this directory and is the
// session the user is already driving: it must never be offered as a session
// to follow, or the picker lists the window's own child back at it.
func TestCandidatesExcludesCallerSuppliedPIDs(t *testing.T) {
	dir := useDescDir(t)
	// /tmp is a cwd pi may really have sessions in; pin the session dir so
	// this test's expectation (exactly the other two pids) cannot drift with
	// the developer's own session history.
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
	keep, owned := liveForeignPID(t), liveForeignPID(t)
	withProcs(t, []procInfo{{PID: keep, CWD: "/tmp"}, {PID: owned, CWD: "/tmp"}})
	writeForeignDescriptorFile(t, dir, "keep.json", keep, sseEndpoint(t), "/tmp", 1)
	writeForeignDescriptorFile(t, dir, "owned.json", owned, sseEndpoint(t), "/tmp", 2)

	got, err := Candidates("/tmp", owned)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate after excluding one pid, got %#v", got)
	}
	if got[0].PID == owned {
		t.Fatal("excluded pid was still listed as a candidate")
	}
}

// A pi the user can see in another pane is listed only once one of the two
// sources can reach it: a bridge descriptor that answers, or a recent session
// file. A bare pid can never be followed by either, so it must not be a row at
// all — that row is the "pi N cannot be followed" refusal the picker used to
// offer for every plain pi.
func TestCandidatesDropsProcessOnlyRowsAndKeepsBothRealSources(t *testing.T) {
	descDir := useDescDir(t)
	sessDir := useSessionDir(t)
	cwd := t.TempDir()

	// Two plain pids: no descriptor, and pi's session dir is empty, so there
	// is no file to pair them with.
	plain1, plain2 := liveForeignPID(t), liveForeignPID(t)
	// One pid with a live bridge.
	bridged := liveForeignPID(t)
	// One file-only session: a pi that is still writing but never loaded the
	// bridge extension.
	fileSession := writeSessionFile(t, sessDir, "2026-01-02T10-00-00_s.jsonl",
		sessionHeader(cwd, "sess-file"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-x","timestamp":"2026-01-02T10:00:01.000Z"}`)
	// pitago's own child, excluded by pid.
	own := liveForeignPID(t)

	withProcs(t, []procInfo{
		{PID: plain1, CWD: cwd}, {PID: plain2, CWD: cwd},
		{PID: bridged, CWD: cwd}, {PID: own, CWD: cwd},
	})
	writeForeignDescriptor(t, descDir, bridged, sseEndpoint(t), cwd, 12)

	got, err := Candidates(cwd, own)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("only the bridged and the file session are followable, got %#v", got)
	}
	// A bare pid must never reach the picker.
	for _, c := range got {
		if c.PID == plain1 || c.PID == plain2 {
			t.Fatalf("process-only pid listed as a candidate: %#v", c)
		}
		if c.PID == own {
			t.Fatalf("excluded pid still listed: %#v", c)
		}
		if !c.Streamable {
			t.Fatalf("every returned row must be followable: %#v", c)
		}
		if c.Source == SourceBridge && (c.Descriptor == nil || c.File != nil) {
			t.Fatalf("bridge row must carry a descriptor and no file: %#v", c)
		}
		if c.Source == SourceFile && c.File == nil {
			t.Fatalf("file row must carry a file: %#v", c)
		}
	}
	// Exactly one bridge row (for the bridged pid) and exactly one file row.
	var bridges, files int
	for _, c := range got {
		switch c.Source {
		case SourceBridge:
			bridges++
			if c.PID != bridged || c.Descriptor == nil {
				t.Fatalf("unexpected bridge row: %#v", c)
			}
		case SourceFile:
			files++
			if c.File.Path != fileSession || c.SessionID != "sess-file" {
				t.Fatalf("unexpected file row: %#v", c)
			}
		}
	}
	if bridges != 1 || files != 1 {
		t.Fatalf("expected 1 bridge + 1 file row, got %d + %d in %#v", bridges, files, got)
	}
}

func TestCandidatesMarksStreamableWhenDescriptorAnswers(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	endpoint := sseEndpoint(t)
	withProcs(t, []procInfo{{PID: pid, CWD: cwd}})
	writeForeignDescriptor(t, dir, pid, endpoint, cwd, 42)

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one candidate, got %#v", got)
	}
	c := got[0]
	if !c.Streamable || c.Descriptor == nil {
		t.Fatalf("candidate with a live endpoint must be streamable: %#v", c)
	}
	// The picker shows a name and model, not just a pid.
	if c.SessionName != "named" || c.Model != "gpt-x" || c.SessionID != fmt.Sprintf("sess-%d", pid) || c.StartedAt != 42 {
		t.Fatalf("descriptor metadata not carried into the candidate: %#v", c)
	}
	if c.Descriptor.Endpoint != endpoint {
		t.Fatalf("candidate descriptor endpoint = %q, want %q", c.Descriptor.Endpoint, endpoint)
	}
}

// A descriptor whose process is gone is a crash artifact. pidAlive filtering in
// Discover already drops it, so it must not reach the picker either.
func TestCandidatesDropsDescriptorOfDeadPID(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	dead := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExists")
	if err := dead.Start(); err != nil {
		t.Skipf("cannot start reaper helper: %v", err)
	}
	deadPID := dead.Process.Pid
	_ = dead.Wait()

	withProcs(t, []procInfo{{PID: deadPID, CWD: cwd}})
	writeForeignDescriptor(t, dir, deadPID, sseEndpoint(t), cwd, 1)
	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	// The pid came from the scan, so it is a process hint only; the crash
	// artifact must not reach the picker at all, in any shape.
	for _, c := range got {
		if c.PID == deadPID {
			t.Fatalf("dead pid offered as a candidate: %#v", c)
		}
	}
}

// The point of graceful degradation: no usable ps must still leave the
// streamable sessions reachable, because those are the only ones /live can
// actually attach to. An error here would make /live fail outright.
func TestCandidatesDegradesToDescriptorsWhenProcessScanFails(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	writeForeignDescriptor(t, dir, pid, sseEndpoint(t), cwd, 7)

	prev := scanProcs
	scanProcs = func(string) ([]procInfo, error) { return nil, fmt.Errorf("ps: not found") }
	t.Cleanup(func() { scanProcs = prev })

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("a failed process scan must not surface an error: %v", err)
	}
	if len(got) != 1 || !got[0].Streamable || got[0].PID != pid {
		t.Fatalf("degraded listing lost the streamable session: %#v", got)
	}
}

func TestCandidatesOrdersStreamableNewestFirst(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	newer := liveForeignPID(t)
	older := liveForeignPID(t)
	withProcs(t, []procInfo{{PID: older, CWD: cwd}, {PID: newer, CWD: cwd}})
	// The older file name sorts first; both are streamable via one endpoint
	// per descriptor, and streamable must beat the non-streamable process.
	writeForeignDescriptor(t, dir, newer, sseEndpoint(t), cwd, 200)
	raw := fmt.Sprintf(`{"version":1,"endpoint":%q,"token":"secret","sessionId":"s2",`+
		`"cwd":%q,"pid":%d,"startedAt":100}`, sseEndpoint(t), cwd, older)
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	// A plain process that no source can reach: it is a hint, not a row, so
	// it must not appear at all.
	withProcs(t, []procInfo{{PID: older, CWD: cwd}, {PID: newer, CWD: cwd}, {PID: liveForeignPID(t), CWD: cwd}})

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the 2 followable sessions, got %#v", got)
	}
	if !got[0].Streamable || got[0].PID != newer {
		t.Fatalf("newest streamable session must be first: %#v", got)
	}
	if !got[1].Streamable || got[1].PID != older {
		t.Fatalf("older streamable session must be second: %#v", got)
	}
}

// Deduplication: a session visible to both the scan and a descriptor is one
// row, not two.
func TestCandidatesDeduplicatesByPID(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	withProcs(t, []procInfo{{PID: pid, CWD: cwd}})
	writeForeignDescriptor(t, dir, pid, sseEndpoint(t), cwd, 5)
	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("session seen twice must be one candidate, got %#v", got)
	}
}

func TestIsPiCommandDoesNotMatchPitago(t *testing.T) {
	for _, cmd := range []string{"pi", "/usr/local/bin/pi", "pi.exe", `C:\bin\pi.exe`} {
		if !isPiCommand(cmd) {
			t.Fatalf("%q should be recognised as pi", cmd)
		}
	}
	// The critical false positive: pitago's own binary path contains "pi".
	for _, cmd := range []string{"pitago", "/Users/x/bin/pitago", "python3", "pip", "pi-extra"} {
		if isPiCommand(cmd) {
			t.Fatalf("%q must not be treated as a pi session", cmd)
		}
	}
}

// withCWDs replaces the cwd-probe seam for one test and restores it.
func withCWDs(t *testing.T, dirs map[int]string) {
	t.Helper()
	prev := resolveCWDsFn
	resolveCWDsFn = func([]int) map[int]string { return dirs }
	t.Cleanup(func() { resolveCWDsFn = prev })
}

// startChildIn spawns a real long-lived child in dir and returns its pid.
// The child is killed and reaped on cleanup, so nothing leaks.
func startChildIn(t *testing.T, dir string, name string, args ...string) int {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start %s helper: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// THIS is the regression test for the macOS defect: the scan asked ps for a
// cwd= column, which macOS ps does not have, so the command failed and the
// scan returned nothing on every mac. Nothing here is faked — it runs the
// REAL probe against a REAL child process, because that is the only way a
// platform whose probe cannot report a cwd at all gets caught.
func TestResolveCWDsReportsRealChildWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	pid := startChildIn(t, dir, "sleep", "30")

	// resolveCWDsFn still points at the real platform probe here: no test has
	// replaced it, and package tests run sequentially with cleanup restoring
	// each seam, so this exercises the real subprocess/proc path.
	got := resolveCWDsFn([]int{pid})[pid]
	if got == "" {
		t.Skipf("no usable cwd probe on this platform (linux: /proc/<pid>/cwd, " +
			"otherwise: lsof -a -d cwd -Fn); install lsof to exercise this path")
	}
	// The probe returns a physical path (/private/var/... for /var/...), so
	// this must be compared with sameDir, never with ==.
	if !sameDir(got, dir) {
		t.Fatalf("probe reported cwd %q for a child started in %q", got, dir)
	}
	// A pid that does not exist must be absent, not guessed.
	if _, ok := resolveCWDsFn([]int{-1, 0})[0]; ok {
		t.Fatal("a non-pid must never resolve")
	}
}

// The end-to-end shape of the bug: ps gives pid+command, the probe supplies
// cwd, and a real pi process in the cwd is found. Both halves are real here.
func TestScanPiProcessesFindsPiWithRealPSAndRealProbe(t *testing.T) {
	cwd := t.TempDir()
	// A child that looks like pi to the command filter.
	pid := startChildIn(t, cwd, "sleep", "30")
	prev := runPS
	t.Cleanup(func() { runPS = prev })
	// Only the command column is faked: the point is the real probe and the
	// real parser shape, not the process table.
	runPS = func() (string, error) {
		return fmt.Sprintf("%d /usr/local/bin/pi --mode rpc\n%d /bin/sleep 30\n", pid, pid+100000), nil
	}
	procs, err := scanPiProcesses(cwd)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected the pi in %s to be found, got %#v (probe=%v)", cwd, procs, resolveCWDsFn([]int{pid}))
	}
	if procs[0].PID != pid {
		t.Fatalf("wrong pid in %#v", procs)
	}
	// A pi in a different directory must not be offered.
	if other, err := scanPiProcesses(t.TempDir()); err != nil || len(other) != 0 {
		t.Fatalf("pi leaked from another cwd: %#v %v", other, err)
	}
}

// Degradation: the probe resolving nothing (missing lsof, or a process we may
// not inspect) must cost the non-streamable rows and nothing else —
// Candidates still returns the streamable descriptors, with no error.
func TestCandidatesDegradesWhenCWDProbeFindsNothing(t *testing.T) {
	dir := useDescDir(t)
	cwd := t.TempDir()
	pid := liveForeignPID(t)
	writeForeignDescriptor(t, dir, pid, sseEndpoint(t), cwd, 9)

	prev := runPS
	t.Cleanup(func() { runPS = prev })
	withCWDs(t, map[int]string{}) // probe reports nothing

	got, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("a blind cwd probe must not surface an error: %v", err)
	}
	if len(got) != 1 || !got[0].Streamable || got[0].PID != pid {
		t.Fatalf("streamable session lost when the probe is blind: %#v", got)
	}
}

// Defensive parsing of the portable two-column ps output. The cwd is no longer
// in the line at all, and a bare "pi" has only two fields, so a guard that
// demanded three silently dropped everything.
func TestParsePSPiIsDefensive(t *testing.T) {
	raw := "notapid pi\n\n12\n"
	raw += "111 pi\n"                             // bare, no args
	raw += "222 /usr/local/bin/pi --mode rpc\n"   // path + args
	raw += "333 /Users/x/bin/pitago --mode rpc\n" // not pi
	raw += "444 python3 /tmp/pi.py\n"             // not the pi binary
	raw += "555 pi\n"                             // duplicate pid
	raw += "666 pi\n"
	got := parsePSPi(raw)
	pids := map[int]int{}
	for _, p := range got {
		pids[p.PID]++
	}
	if pids[111] != 1 || pids[222] != 1 {
		t.Fatalf("bare and path invocations must both parse: %#v", got)
	}
	if pids[333] != 0 || pids[444] != 0 {
		t.Fatalf("non-pi commands were accepted: %#v", got)
	}
	if pids[555] != 1 || pids[666] != 1 {
		t.Fatalf("distinct pids must not be collapsed: %#v", got)
	}
}

// The end-to-end shape of the review's bug, with a REAL pi. The scan used to
// ask ps for a cwd= column that macOS ps does not have, so on every mac the
// command failed and no real pi was ever found. The scan must still find a
// plain, bridgeless pi (that is what proves the probe works), but Candidates
// must not turn it into a row: it is followable by neither source until it
// writes its session file.
//
// Only the command column is faked (there is no way to inject a real pi into
// the process table); ps parsing, the cwd probe and the sameDir comparison are
// all real, which is the part that was broken.
func TestRealPiWithoutBridgeIsListedByScan(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("no pi binary on PATH")
	}
	cwd := t.TempDir()
	cmd := exec.Command(piBin, "--mode", "rpc")
	cmd.Dir = cwd
	// pi exits on EOF'd stdin, so it must be fed a live pipe for the whole
	// test — the same discipline tests/integration/live_bridge_test.go uses.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = stdinR
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start pi: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdinR.Close()
	})
	// Wait for pi to actually be in the process table rather than sleeping a
	// fixed amount: poll the real ps until the pid shows up.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := psCommand(); err == nil && strings.Contains(out, fmt.Sprintf("%d ", cmd.Process.Pid)) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	prev := runPS
	t.Cleanup(func() { runPS = prev })
	runPS = func() (string, error) {
		return fmt.Sprintf("%d pi\n", cmd.Process.Pid), nil
	}
	procs, err := scanPiProcesses(cwd)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(procs) != 1 || procs[0].PID != cmd.Process.Pid {
		t.Fatalf("a real pi in %s was not found by the scan: %#v", cwd, procs)
	}

	// And the public entry point must NOT surface it: a freshly started, idle
	// pi has published no descriptor and written no session file yet, so it
	// is genuinely not followable. A bare pid row would only be a refusal.
	// The session dir is pinned AFTER the child started (the child already
	// captured its env): the child keeps writing its real session file
	// outside the test's view, so the live pi's own file cannot add a
	// file-sourced row here.
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
	cands, err := Candidates(cwd)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("an idle bridgeless pi must produce no rows, got %#v", cands)
	}
}

func TestScanPiProcessesPropagatesCommandFailure(t *testing.T) {
	prev := runPS
	t.Cleanup(func() { runPS = prev })
	runPS = func() (string, error) { return "", fmt.Errorf("exec: \"ps\": executable file not found") }
	if _, err := scanPiProcesses(t.TempDir()); err == nil {
		t.Fatal("a missing ps must be reported so Candidates can degrade")
	}
}
