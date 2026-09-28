// startup.go — the readiness probe pitago runs on a freshly spawned pi.
//
// pirpc.Spawn returns the moment fork/exec succeeds, so the first get_state
// races pi's own boot (extension load, session index, keystore). One hard 15s
// answer window used to decide that race, and a miss left the TUI dead-ended
// on a red "pi: get_state timed out" line that nothing ever cleared. Instead
// the probe spends a bounded budget of SHORT attempts: pi answering late
// costs one more round trip, not a dead session, and a pi that really died
// is detected through Client.Done and reported with its own stderr.
//
// Every sleep lives in the tea.Cmd goroutine that fetchAll returns, never on
// the UI thread and never on the pi event pump.
package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// startupProbe is the retry shape used to wait for pi to become ready.
// Timeout is the window of the FIRST get_state attempt; it doubles per
// attempt (capped at MaxTimeout) so a pi that is merely slow to answer is
// never punished for it; Delays holds the wait BEFORE each retry (so
// len(Delays) retries run, the first attempt is immediate); RetryAfter is
// how long a later fetchAll waits once the budget is spent.
type startupProbe struct {
	Timeout    time.Duration
	MaxTimeout time.Duration // ceiling for the escalating window (0 = pirpc.GetStateTimeout)
	Delays     []time.Duration
	RetryAfter time.Duration
}

// defaultProbe is the production shape: 6 attempts whose window grows
// 5s -> 10s -> 15s (the old single window) and stays there, with the wait
// before each retry backing off from 250ms to a 2s cap. probeBudget works
// out to 80.75s of patience for a cold pi start; a pi that answers on the
// first attempt never sleeps at all.
//
// One probe serves startup AND the mid-session reconnects (post-turn reload,
// /live detach, respawn). The escalation is why that is safe: the old code
// gave those callers one 15s shot, and they now get 5s first, then 10s,
// then 15s per attempt, retried. No latency that used to work is lost.
func defaultProbe() startupProbe {
	return startupProbe{
		Timeout:    5 * time.Second,
		MaxTimeout: pirpc.GetStateTimeout,
		Delays:     []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 2 * time.Second},
		RetryAfter: 5 * time.Second,
	}
}

// probeOrDefault keeps a zero-valued Model (tests, hand-built models) on the
// production probe instead of a degenerate no-retry one.
func (m Model) probeOrDefault() startupProbe {
	p := m.probe
	if p.Timeout <= 0 || len(p.Delays) == 0 {
		return defaultProbe()
	}
	if p.MaxTimeout <= 0 || p.MaxTimeout > pirpc.GetStateTimeout {
		p.MaxTimeout = pirpc.GetStateTimeout
	}
	return p
}

// attemptWindow is the get_state window of attempt i (0-based): it doubles
// from Timeout and stops at MaxTimeout, so a pi whose steady-state get_state
// is slower than the first window still connects — the pre-probe code gave
// it 15s in one shot and that must keep working.
func attemptWindow(p startupProbe, attempt int) time.Duration {
	w := p.Timeout
	for i := 0; i < attempt; i++ {
		w *= 2
		if w >= p.MaxTimeout {
			return p.MaxTimeout
		}
	}
	if w > p.MaxTimeout {
		return p.MaxTimeout
	}
	return w
}

// probeBudget is the worst case the probe can spend: every attempt runs its
// window out and every delay elapses. It is what the production numbers in
// defaultProbe's comment are derived from, and what the tests pin.
func probeBudget(p startupProbe) time.Duration {
	var total time.Duration
	for i := 0; i <= len(p.Delays); i++ {
		if i > 0 {
			total += p.Delays[i-1]
		}
		total += attemptWindow(p, i)
	}
	return total
}

// awaitState polls pi for get_state until it answers, pi exits, or the probe
// budget is spent. It returns the state or the error to report; a timeout
// that ran out of budget comes back with retryable=true so the caller can
// re-arm instead of dead-ending the UI.
func awaitState(pi *pirpc.Client, p startupProbe) (st pirpc.State, err error, retryable bool) {
	if pi == nil {
		return st, fmt.Errorf("pi: no client"), false
	}
	for attempt := 0; ; attempt++ {
		st, err = pi.GetStateWithin(attemptWindow(p, attempt))
		if err == nil {
			return st, nil, false
		}
		// pi is gone: no answer will ever come, so stop immediately and let
		// pi's own stderr explain the real reason.
		if exited(pi) {
			return st, exitedErr(err), false
		}
		// pi answered and refused: retrying gets the same refusal.
		if !pirpc.IsTimeout(err) {
			return st, err, false
		}
		if attempt >= len(p.Delays) {
			return st, fmt.Errorf("pi did not answer get_state after %d attempts: %w", attempt+1, err), true
		}
		// Sleep in this goroutine, but wake early if pi dies meanwhile.
		timer := time.NewTimer(p.Delays[attempt])
		select {
		case <-timer.C:
		case <-pi.Done():
			timer.Stop()
			return st, exitedErr(err), false
		}
	}
}

// exited reports whether the pi process is already gone.
func exited(pi *pirpc.Client) bool {
	select {
	case <-pi.Done():
		return true
	default:
		return false
	}
}

// exitedErr names the dead child and folds in pi's stderr, which is where
// the actual reason lives (bad session path, auth failure, crash).
func exitedErr(cause error) error {
	tail := pirpc.StderrTail()
	if tail == "" {
		return fmt.Errorf("pi exited during startup (%v)", cause)
	}
	return fmt.Errorf("pi exited during startup (%v): %s", cause, tail)
}

// fetchAll loads state/messages/stats/commands after (re)connect.
//
// model.go keeps the command; the readiness wait lives here so the probe
// policy (and its tests) stay in one file. Every result carries the client
// it came from: a re-arm queued before a respawn must not paint a stale
// "cannot connect" over a healthy session (see the connectedMsg handler).
func (m Model) fetchAll() tea.Cmd {
	p := m.probeOrDefault()
	pi := m.Pi
	return func() tea.Msg {
		state, err, retry := awaitState(pi, p)
		if err != nil {
			return connectedMsg{client: pi, err: err, retry: retry}
		}
		msgs, _ := pi.GetMessages()
		stats, _ := pi.GetStats()
		cmds, _ := pi.GetCommands()
		entries, _ := pi.GetEntries()
		return connectedMsg{client: pi, state: state, msgs: msgs, stats: stats, cmds: cmds, entries: entries}
	}
}

// fetchAllLater re-arms fetchAll after d. Used when the probe budget ran out
// on a live-but-slow pi: the UI shows the error and keeps trying instead of
// waiting for the user to respawn. The sleep is inside the Cmd goroutine.
func (m Model) fetchAllLater(d time.Duration) tea.Cmd {
	if d <= 0 {
		return m.fetchAll()
	}
	next := m.fetchAll()
	return func() tea.Msg {
		time.Sleep(d)
		return next()
	}
}
