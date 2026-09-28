package app

// startup_test.go — the readiness probe that keeps a slow-starting pi from
// dead-ending the TUI.
//
// Every case drives the real fetchAll command against a real child (the
// stdlib fake in tests/fakepi, or a shell stub that speaks JSONL), because
// the failure this fixes is a timing failure: it cannot be reproduced by a
// struct literal. Timing margins are deliberately wide — the stubs answer on
// a fixed schedule and every deadline here is a multiple of it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// buildFakePi compiles tests/fakepi once per test and returns its path.
func buildFakePi(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakepi")
	cmd := exec.Command("go", "build", "-o", bin, "../../tests/fakepi")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build tests/fakepi: %v\n%s", err, out)
	}
	return bin
}

// spawnFakePi launches the stdlib fake with the given FAKEPI_* switches and
// returns the client plus its transcript path (for request counting).
func spawnProbeFakePi(t *testing.T, env map[string]string) (*pirpc.Client, string) {
	t.Helper()
	bin := buildFakePi(t)
	log := filepath.Join(t.TempDir(), "fakepi.jsonl")
	t.Setenv("FAKEPI_LOG", log)
	for k, v := range env {
		t.Setenv(k, v)
	}
	c, err := pirpc.Spawn(pirpc.Options{Bin: bin, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn fake pi: %v", err)
	}
	t.Cleanup(c.Close)
	return c, log
}

// probeStubBody is a JSONL child used where the fake's knobs are not enough:
// it answers every command, appends every received line to
// $PITAGO_TEST_PROBE_LOG, and answers get_state only after sleeping delay.
// When swallowFirst is set, the FIRST get_state is swallowed entirely
// (never answered) and every later one is answered immediately — a pi that
// needs a second try. The counter has to START at 1 for that branch to run
// at all: seeded at 0 the first get_state is answered straight away and the
// client connects without ever retrying, which turns "the retry worked" into
// "the runner was slow enough to need one".
func probeStubBody(delay string, swallowFirst bool) string {
	const state = `data='{"model":{"id":"stub-model","name":"Stub Model"},"thinkingLevel":"medium"}'`
	first := `sleep ` + delay + `
      ` + state
	seed := `first=0`
	if swallowFirst {
		seed = `first=1`
		first = `if [ "$first" = 1 ]; then
        first=0
        sleep ` + delay + `
        continue
      fi
      ` + state
	}
	return seed + `
while IFS= read -r line; do
  printf '%s\n' "$line" >>"$PITAGO_TEST_PROBE_LOG"
  case "$line" in
  *get_state*)
    ` + first + `
    ;;
  *) data='{}' ;;
  esac
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  [ -n "$id" ] || continue
  printf '{"type":"response","id":"%s","command":"x","success":true,"data":%s}\n' "$id" "$data"
done
`
}

// scriptStub writes an executable shell stub and returns its path.
func scriptStub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// runProbeCmd runs one tea.Cmd and returns its message (nil on overrun).
func runProbeCmd(cmd tea.Cmd, d time.Duration) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case m := <-ch:
		return m
	case <-time.After(d):
		return nil
	}
}

// countRequests counts the requests of one type the child received, from its
// own transcript. Retry assertions need it: an elapsed time alone cannot tell
// "retried and recovered" from "got lucky on the first attempt".
func countRequests(t *testing.T, log, typ string) int {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0 // nothing reached the wire
	}
	n := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.Contains(ln, `"type":"`+typ+`"`) {
			n++
		}
	}
	return n
}

// Regression (the reported bug): a pi that needs ~1s to answer get_state must
// be waited out, not dead-ended. Under the old code this was a single hard
// window with no retry behind it.
func TestFetchAllSurvivesSlowPi(t *testing.T) {
	c, _ := spawnProbeFakePi(t, map[string]string{"FAKEPI_DELAY": "get_state=1s"})

	m := Model{Pi: c, probe: startupProbe{
		Timeout:    2 * time.Second,
		MaxTimeout: 2 * time.Second,
		Delays:     []time.Duration{200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond},
	}}
	start := time.Now()
	msg := runProbeCmd(m.fetchAll(), 20*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err != nil {
		t.Fatalf("slow pi was not waited out: %v", errText(got.err))
	}
	if got.state.Model.ID != "fake-model" {
		t.Fatalf("state not loaded: %+v", got.state)
	}
	if got.cmds == nil || got.stats.SessionID == "" {
		t.Fatalf("rest of fetchAll skipped: cmds=%v stats=%+v", got.cmds, got.stats)
	}
	if el := time.Since(start); el < time.Second {
		t.Fatalf("fake never delayed: finished in %s", el)
	}
}

// A pi that misses the per-attempt window is retried, not given up on. The
// stub swallows the FIRST get_state and answers every later one, so the test
// proves a retry happened (>= 2 requests on the wire) instead of relying on
// the first attempt losing a race.
func TestFetchAllRetriesPastFirstTimeout(t *testing.T) {
	log := filepath.Join(t.TempDir(), "probe.jsonl")
	t.Setenv("PITAGO_TEST_PROBE_LOG", log)
	// The drop of the first answer is what makes the retry deterministic, so
	// the stub's busy-sleep has to be OVER before the retry lands (it is sent
	// at Timeout+Delays[0] = 200ms and answered inside attempt 1's 200ms
	// window). Keep it far below that: a sleep long enough to still be running
	// when the retry arrives would hand this test back to wall-clock luck.
	c, err := pirpc.Spawn(pirpc.Options{Bin: scriptStub(t, probeStubBody("0.05", true)), Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn stub: %v", err)
	}
	t.Cleanup(c.Close)

	m := Model{Pi: c, probe: startupProbe{
		Timeout:    100 * time.Millisecond,
		MaxTimeout: 200 * time.Millisecond,
		Delays:     []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
	}}
	msg := runProbeCmd(m.fetchAll(), 20*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err != nil {
		t.Fatalf("retry did not recover a slow pi: %v", errText(got.err))
	}
	if got.state.Model.ID != "stub-model" {
		t.Fatalf("state not loaded after retry: %+v", got.state)
	}
	if n := countRequests(t, log, "get_state"); n < 2 {
		t.Fatalf("expected a retry (>= 2 get_state on the wire), saw %d", n)
	}
}

// Regression: a fixed per-attempt window made any pi whose steady-state
// get_state is slower than that window permanently unconnectable — the
// pre-probe code gave it one 15s shot and connected. The window has to grow
// per attempt, or that latency is lost. Here the stub answers get_state
// 700ms late, EVERY time: only the escalating window (400ms -> 800ms ->
// 1600ms) can catch those answers inside the budget.
func TestFetchAllEscalatesWindowForSlowAnswers(t *testing.T) {
	log := filepath.Join(t.TempDir(), "probe.jsonl")
	t.Setenv("PITAGO_TEST_PROBE_LOG", log)
	c, err := pirpc.Spawn(pirpc.Options{Bin: scriptStub(t, probeStubBody("0.7", false)), Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn stub: %v", err)
	}
	t.Cleanup(c.Close)

	m := Model{Pi: c, probe: startupProbe{
		Timeout:    400 * time.Millisecond,
		MaxTimeout: 2 * time.Second,
		Delays:     []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
	}}
	msg := runProbeCmd(m.fetchAll(), 20*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err != nil {
		t.Fatalf("a pi answering 700ms late must still connect: %v", errText(got.err))
	}
	if got.state.Model.ID != "stub-model" {
		t.Fatalf("state not loaded: %+v", got.state)
	}
	if n := countRequests(t, log, "get_state"); n < 2 {
		t.Fatalf("expected retries, saw %d get_state requests", n)
	}
}

// The production budget is a promise ("how patient is pitago with a cold
// pi?"), so pin it: the window escalates to the old 15s ceiling and the
// worst case is ~81s. Changing these numbers is a product decision, not a
// refactor.
func TestDefaultProbeBudget(t *testing.T) {
	p := defaultProbe()
	if p.MaxTimeout != pirpc.GetStateTimeout {
		t.Fatalf("probe cap %s, want the pre-probe window %s", p.MaxTimeout, pirpc.GetStateTimeout)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second, 15 * time.Second}
	for i, w := range want {
		if got := attemptWindow(p, i); got != w {
			t.Fatalf("attempt %d window = %s, want %s", i, got, w)
		}
	}
	if got := attemptWindow(p, 40); got != pirpc.GetStateTimeout {
		t.Fatalf("window must stay capped: %s", got)
	}
	const wantBudget = 80*time.Second + 750*time.Millisecond
	if got := probeBudget(p); got != wantBudget {
		t.Fatalf("probe budget = %s, want %s (defaultProbe's comment must follow)", got, wantBudget)
	}
	if got := probeBudget(defaultProbe()); got < 60*time.Second {
		t.Fatalf("production patience below a minute: %s", got)
	}
	if len(p.Delays) != 5 || p.RetryAfter <= 0 {
		t.Fatalf("retry shape: %d delays, RetryAfter %s", len(p.Delays), p.RetryAfter)
	}
}

// The happy path must not pay for the probe: an immediately-answering pi
// gets zero sleeps and returns well under the first retry delay.
func TestFetchAllFastPiDoesNotSleep(t *testing.T) {
	c, _ := spawnProbeFakePi(t, nil)
	m := Model{Pi: c, probe: defaultProbe()}
	start := time.Now()
	msg := runProbeCmd(m.fetchAll(), 10*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err != nil {
		t.Fatalf("fast pi failed: %v", errText(got.err))
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("probe added a sleep to the happy path: %s", el)
	}
}

// A pi that answers and REFUSES is not retried: the next attempt would get
// the same refusal, and burning the whole budget on it would hide pi's own
// reason behind a timeout.
func TestFetchAllDoesNotRetryRefusal(t *testing.T) {
	c, log := spawnProbeFakePi(t, map[string]string{"FAKEPI_FAIL": "get_state"})
	// The window must dwarf the fake's answer even on a loaded -race runner:
	// this case asserts a refusal is NOT retried, so a slow-but-in-time
	// answer would time out, retry, and fail the "exactly one request" count
	// for the wrong reason.
	m := Model{Pi: c, probe: startupProbe{
		Timeout:    5 * time.Second,
		MaxTimeout: 5 * time.Second,
		Delays:     []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond},
	}}
	msg := runProbeCmd(m.fetchAll(), 10*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "injected failure") {
		t.Fatalf("pi's own refusal must surface, got %v", got.err)
	}
	if pirpc.IsTimeout(got.err) {
		t.Fatalf("a refusal must not look like a timeout: %v", got.err)
	}
	if got.retry {
		t.Fatal("a refusal must not be retried")
	}
	if n := countRequests(t, log, "get_state"); n != 1 {
		t.Fatalf("refusal retried: %d get_state requests", n)
	}
}

// A pi that really dies must abort the probe at once and report the child,
// not "gave up after N attempts" — nothing is going to answer then.
func TestFetchAllGivesUpWhenPiExits(t *testing.T) {
	// Exits shortly after the handshake, so the failure is deterministic.
	c, err := pirpc.Spawn(pirpc.Options{Bin: scriptStub(t, "sleep 0.2\nexit 3\n"), Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn stub: %v", err)
	}
	t.Cleanup(c.Close)

	m := Model{Pi: c, probe: startupProbe{
		Timeout:    5 * time.Second,
		MaxTimeout: 5 * time.Second,
		Delays:     []time.Duration{time.Second, time.Second, time.Second, time.Second, time.Second, time.Second, time.Second, time.Second},
	}}
	start := time.Now()
	msg := runProbeCmd(m.fetchAll(), 10*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "pi exited during startup") {
		t.Fatalf("error must name the dead child, got %v", got.err)
	}
	if got.retry {
		t.Fatal("a dead pi must not be retried")
	}
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("probe kept polling a dead pi for %s", el)
	}
}

// A live-but-silent pi exhausts the budget: the error is retryable, and the
// handler re-arms fetchAll so the UI self-heals instead of dead-ending.
func TestFetchAllBudgetExhaustedIsRetryable(t *testing.T) {
	// A child that reads stdin and never answers: pi is alive, not ready.
	c, err := pirpc.Spawn(pirpc.Options{Bin: scriptStub(t, "exec cat >/dev/null\n"), Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn stub: %v", err)
	}
	t.Cleanup(c.Close)

	m := Model{Pi: c, probe: startupProbe{
		Timeout:    100 * time.Millisecond,
		MaxTimeout: 200 * time.Millisecond,
		Delays:     []time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
		RetryAfter: time.Hour, // the re-arm must not actually run here
	}}
	msg := runProbeCmd(m.fetchAll(), 10*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.err == nil {
		t.Fatal("silent pi must not report a successful connect")
	}
	if !got.retry {
		t.Fatalf("exhausted budget must be retryable: %v", got.err)
	}

	tm, cmd := m.Update(got)
	m = tm.(Model)
	if m.connErr == "" {
		t.Fatal("connErr not surfaced after a failed startup")
	}
	if m.Status != "cannot connect to pi" {
		t.Fatalf("status = %q", m.Status)
	}
	if cmd == nil {
		t.Fatal("no re-arm command: the session would dead-end")
	}
}

// Regression: the self-heal re-arm outlives a respawn. A fetch queued against
// the old client returns "pi exited during startup (broken pipe)" once that
// child is closed; applying it would paint a fake "cannot connect to pi" over
// a session that just respawned fine — the original symptom, re-created.
func TestStaleProbeResultDroppedAfterRespawn(t *testing.T) {
	dead, err := pirpc.Spawn(pirpc.Options{Bin: scriptStub(t, "exec cat >/dev/null\n"), Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("spawn silent child: %v", err)
	}
	t.Cleanup(dead.Close)
	m := Model{Pi: dead, probe: startupProbe{
		Timeout:    100 * time.Millisecond,
		MaxTimeout: 200 * time.Millisecond,
		Delays:     []time.Duration{20 * time.Millisecond, 20 * time.Millisecond},
	}}
	cmd := m.fetchAll()

	// Respawn while the probe is in flight (Ctrl+N, /model, /login all do
	// this): the model points at a healthy new child, the old one is closed.
	fresh, _ := spawnProbeFakePi(t, nil)
	m.Pi = fresh
	dead.Close()

	msg := runProbeCmd(cmd, 10*time.Second)
	got, ok := msg.(connectedMsg)
	if !ok {
		t.Fatalf("no connectedMsg: %#v", msg)
	}
	if got.client != dead {
		t.Fatalf("probe result must carry its own client, got %v", got.client)
	}
	tm, next := m.Update(got)
	m = tm.(Model)
	if m.connErr != "" {
		t.Fatalf("stale result painted an error over a healthy session: %q", m.connErr)
	}
	if m.Status == "cannot connect to pi" {
		t.Fatal("stale result flipped the status of a live session")
	}
	if next != nil {
		t.Fatal("stale result must not re-arm anything")
	}
}

// A later success clears the red error line instead of leaving it forever.
func TestConnectedMsgClearsConnErr(t *testing.T) {
	m := Model{connErr: "pi: get_state timed out", Status: "cannot connect to pi"}
	var st pirpc.State
	st.Model.ID = "m"
	tm, _ := m.Update(connectedMsg{state: st})
	m = tm.(Model)
	if m.connErr != "" {
		t.Fatalf("connErr survived a successful connect: %q", m.connErr)
	}
	if m.Status != "ready" {
		t.Fatalf("status = %q", m.Status)
	}
}

// Regression: latching the welcome header in the connect handler suppressed it
// on the NORMAL path, because a fast pi connects within the first frames —
// before the header was ever painted. The header must survive a fast connect
// with an empty session.
func TestWelcomeHeaderSurvivesFastConnect(t *testing.T) {
	var st pirpc.State
	st.Model.ID = "m"
	m := Model{AppVersion: "v0.0.1"}
	m.vp = viewport.New(80, 20)
	if !strings.Contains(stripANSI(m.renderBlocks()), "█████") {
		t.Fatal("pre-connect paint: welcome header missing")
	}
	tm, _ := m.Update(connectedMsg{state: st}) // pi answered immediately
	m = tm.(Model)
	if !m.connected {
		t.Fatal("connect did not latch the connected flag")
	}
	if out := stripANSI(m.renderBlocks()); !strings.Contains(out, "█████") {
		t.Fatalf("welcome header lost on the normal startup path:\n%s", out)
	}
	if !m.started {
		t.Fatal("header drawn after a connect must latch the welcome")
	}
}

// ...and once it has latched, clearing a mid-session error must not bring the
// logo back.
func TestWelcomeHeaderDoesNotReturnAfterRecovery(t *testing.T) {
	var st pirpc.State
	st.Model.ID = "m"
	m := Model{AppVersion: "v0.0.1", connErr: "pi: get_state timed out"}
	m.vp = viewport.New(80, 20)
	if out := stripANSI(m.renderBlocks()); !strings.Contains(out, "get_state timed out") {
		t.Fatal("test setup: the error line is not rendered")
	}
	tm, _ := m.Update(connectedMsg{state: st}) // the retry finally landed
	m = tm.(Model)
	if m.connErr != "" {
		t.Fatalf("connErr survived: %q", m.connErr)
	}
	if out := stripANSI(m.renderBlocks()); strings.Contains(out, "get_state timed out") {
		t.Fatalf("error line survived a successful connect:\n%s", out)
	}
	// The empty chat is a real empty chat here, so the header may show once
	// more — what must not happen is it coming back on later paints.
	stripANSI(m.renderBlocks())
	if out := stripANSI(m.renderBlocks()); strings.Contains(out, "█████") {
		t.Fatalf("welcome header reappeared after recovery:\n%s", out)
	}
}

// errText keeps the failure messages readable without leaking a nil error.
func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
