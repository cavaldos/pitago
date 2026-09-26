package app

// Tests for the /team command lifecycle added to team.go. The invariant
// under test is the one the bug report turned on: a /team invocation must
// never be indistinguishable from a dead command. Every outcome — forward
// error, no reply, delivered dashboard — produces exactly one visible
// signal, and a watchdog tick that outlives its request produces none.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// teamToasts renders the current toast stack for substring assertions.
func teamToasts(m *Model) string {
	var b strings.Builder
	for _, t := range m.toasts {
		if t.Err {
			b.WriteString("[err] ")
		}
		b.WriteString(t.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func teamToastCount(m *Model) int { return len(m.toasts) }

// armTeamCmd dispatches through the real ForwardTeamCommand so beginExtCmd,
// the status and the request token are all exercised. It never runs the
// returned commands: the batch contains the watchdog timer, which blocks
// for the full deadline. Tests drive the two legs explicitly instead.
func armTeamCmd(t *testing.T, m *Model, text string) tea.Cmd {
	t.Helper()
	teamCmdLive.Store(0)
	cmd := m.ForwardTeamCommand(text)
	if cmd == nil {
		t.Fatal("ForwardTeamCommand returned nil")
	}
	// The batch must be the forward plus exactly one watchdog deadline, so
	// a command can never be dispatched without a bound on its silence.
	bm, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("ForwardTeamCommand must batch the forward with a watchdog tick")
	}
	if len(bm) != 2 {
		t.Fatalf("batch has %d commands, want 2 (forward + watchdog)", len(bm))
	}
	return cmd
}

// teamForwardMsg runs the forward leg alone. With a nil pi client it fails
// immediately, which is the case the "visible error" test needs.
func teamForwardMsg(t *testing.T, m *Model, text string) tea.Msg {
	t.Helper()
	msg := m.ForwardExtensionCommand(text)()
	if msg == nil {
		t.Fatal("forward returned no message")
	}
	return msg
}

// Test 1: a failed forward produces a visible error notice.
func TestTeamExtCmdFailedForwardShowsErrorNotice(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	if m.Status != "loading team…" {
		t.Errorf("in-flight status = %q, want %q", m.Status, "loading team…")
	}
	if !m.extCmdPending(teamCmdName) {
		t.Error("forward must mark the command in flight so the user sees progress")
	}

	// m.Pi is nil, so the forward reports "pi is not connected" through the
	// ack the app's Update loop already renders.
	ack, ok := teamForwardMsg(t, m, "/team").(extensionCmdAckMsg)
	if !ok {
		t.Fatal("expected an extensionCmdAckMsg from the forward")
	}
	if ack.err == nil {
		t.Fatal("forward with a nil pi client must report an error, not silence")
	}

	updated, _ := Model(*m).Update(ack)
	mm, ok := updated.(Model)
	if !ok {
		t.Fatal("Update must return a Model")
	}
	got := teamToasts(&mm)
	if !strings.Contains(got, ack.err.Error()) {
		t.Errorf("failed forward must surface %q, got:\n%s", ack.err.Error(), got)
	}
	if !strings.Contains(got, "pi is not connected") {
		t.Errorf("notice should explain the failure, got:\n%s", got)
	}
}

// Test 2: a delivered dashboard produces NO error notice.
func TestTeamExtCmdDeliveredDashboardIsSilent(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	before := teamToastCount(m)
	m.openTeamDashboard(teamDashboardFixture)

	got := teamToasts(m)
	if strings.Contains(got, "got no reply") || strings.Contains(got, "failed") {
		t.Errorf("a delivered dashboard must be silent, got:\n%s", got)
	}
	if teamToastCount(m) != before {
		t.Errorf("delivery added %d toast(es); a successful command must add none:\n%s", teamToastCount(m)-before, got)
	}
	if m.extCmdPending(teamCmdName) {
		t.Error("delivery must clear the in-flight marker")
	}
	if m.Status != "ready" {
		t.Errorf("status after delivery = %q, want %q (endExtCmd does not clear Status)", m.Status, "ready")
	}
	if tok := teamCmdLive.Load(); tok != 0 {
		t.Errorf("delivery must retire the request token, got %d", tok)
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "team" {
		t.Errorf("delivered dashboard must open the team overlay, got %+v", m.Dialogs)
	}
}

// The detail message counts as delivery too: /team w1 focuses a worker and
// the plugin answers with a detail message instead of a dashboard.
func TestTeamExtCmdDeliveredDetailIsSilent(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	m.openTeamDetail("Pi Agents Team Detail\nworker w1\ndone")

	if m.extCmdPending(teamCmdName) {
		t.Error("detail delivery must clear the in-flight marker")
	}
	if got := teamToasts(m); strings.Contains(got, "got no reply") {
		t.Errorf("detail delivery must be silent, got:\n%s", got)
	}
	if m.Status != "ready" {
		t.Errorf("status after detail = %q, want %q", m.Status, "ready")
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "team" || !m.Dialogs[0].TeamDetail {
		t.Errorf("detail delivery must open the inspect tab, got %+v", m.Dialogs)
	}
}

// Test 3: a stale watchdog tick after delivery is a no-op.
func TestTeamExtCmdStaleWatchdogTickIsNoOp(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	tok := teamCmdLive.Load()
	if tok == 0 {
		t.Fatal("forward must arm a request token")
	}
	m.openTeamDashboard(teamDashboardFixture)

	before := teamToastCount(m)
	// The timer from the completed request fires late. It must not invent a
	// failure for a command that already delivered.
	m.handleTeamWatchdog(teamWatchdogMsg{name: teamCmdName, epoch: tok})

	if teamToastCount(m) != before {
		t.Errorf("stale watchdog added %d toast(es), expected none:\n%s", teamToastCount(m)-before, teamToasts(m))
	}
	if m.Status != "ready" {
		t.Errorf("stale watchdog changed status to %q", m.Status)
	}
	if teamCmdLive.Load() != 0 {
		t.Error("stale watchdog must not resurrect the request token")
	}
	if m.extCmdPending(teamCmdName) {
		t.Error("stale watchdog must not disturb a completed command")
	}
}

// A watchdog for a name we do not own, or for a superseded token, is inert.
func TestTeamExtCmdWatchdogIgnoresForeignAndSupersededTicks(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")
	live := teamCmdLive.Load()

	before := teamToastCount(m)
	m.handleTeamWatchdog(teamWatchdogMsg{name: "/not-team", epoch: live})
	m.handleTeamWatchdog(teamWatchdogMsg{name: teamCmdName, epoch: live + 99})
	if teamToastCount(m) != before {
		t.Errorf("foreign/superseded ticks must be inert:\n%s", teamToasts(m))
	}

	// A second /team supersedes the first; the first request's tick is stale
	// even though a live token exists.
	armTeamCmd(t, m, "/team")
	before = teamToastCount(m)
	m.handleTeamWatchdog(teamWatchdogMsg{name: teamCmdName, epoch: live})
	if teamToastCount(m) != before {
		t.Errorf("a superseded request's tick must be inert:\n%s", teamToasts(m))
	}
}

// The live watchdog must close the window with a visible "no reply".
func TestTeamExtCmdLiveWatchdogReportsNoReply(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	m.handleTeamWatchdog(teamWatchdogMsg{name: teamCmdName, epoch: teamCmdLive.Load()})

	got := teamToasts(m)
	if !strings.Contains(got, "got no reply") {
		t.Errorf("a live watchdog must report the missing reply, got:\n%s", got)
	}
	// DEFECT C: the hint must name the actual cause.
	if !strings.Contains(got, "not loaded") {
		t.Errorf("with no extension /team in the catalog the hint must say the plugin is not loaded, got:\n%s", got)
	}
	if m.extCmdPending(teamCmdName) {
		t.Error("watchdog must clear the in-flight marker")
	}
	if m.Status != "ready" {
		t.Errorf("status after watchdog = %q, want %q", m.Status, "ready")
	}
	if teamCmdLive.Load() != 0 {
		t.Error("watchdog must retire the request token")
	}
}

// The endExtCmd error branch is what a forward failure routes through when
// the lifecycle closes it, so pin the text it must produce.
func TestTeamExtCmdEndWithErrorIsVisible(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	m.beginExtCmd(teamCmdName, "loading team…")
	m.endExtCmd(teamCmdName, errTeamTest, false)

	got := teamToasts(m)
	if !strings.Contains(got, teamCmdName+" failed: ") {
		t.Errorf("error close must be visible, got:\n%s", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("error text must be included, got:\n%s", got)
	}
	if m.extCmdPending(teamCmdName) {
		t.Error("endExtCmd must clear the in-flight marker")
	}
}

// DEFECT C, other branch: the plugin IS loaded, so the failure is "busy",
// not "not installed".
func TestTeamExtCmdNoReplyHintDistinguishesBusyFromMissing(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{Cmds: []pirpc.RepoCommand{{Name: "team", Source: "extension"}}}
	if !m.teamPluginLoaded() {
		t.Fatal("an extension-sourced /team means the plugin is loaded")
	}
	m.beginExtCmd(teamCmdName, "loading team…")
	m.teamNoReplyHint()
	if got := teamToasts(m); !strings.Contains(got, "did not answer in time") {
		t.Errorf("a loaded plugin must be reported as busy, got:\n%s", got)
	}

	// A pitago-source /team is pitago's own builtin, not the plugin.
	m2 := &Model{Cmds: []pirpc.RepoCommand{{Name: "team", Source: "pitago"}}}
	if m2.teamPluginLoaded() {
		t.Error("pitago's own builtin row must not count as the plugin being loaded")
	}
}

// DEFECT A: a mid-turn invocation is announced as queued, not lost.
func TestTeamExtCmdMidTurnAnnouncesQueued(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	m.thinking = true

	armTeamCmd(t, m, "/team")

	if m.Status != "team queued…" {
		t.Errorf("mid-turn status = %q, want %q", m.Status, "team queued…")
	}
	if got := teamToasts(m); !strings.Contains(got, "queued") {
		t.Errorf("mid-turn must tell the user the dashboard is deferred, got:\n%s", got)
	}
	if !m.extCmdPending(teamCmdName) {
		t.Error("mid-turn must still mark the command in flight")
	}
	if teamCmdLive.Load() == 0 {
		t.Error("mid-turn must still arm a watchdog")
	}
	// A mid-turn request must not be given the idle deadline: pi legitimately
	// holds the custom message until the turn ends.
	if teamCmdDeadlineBusy <= teamCmdDeadline {
		t.Fatalf("busy deadline %v must exceed idle deadline %v", teamCmdDeadlineBusy, teamCmdDeadline)
	}
}

// The idle path must not claim to be queued.
func TestTeamExtCmdIdleDoesNotClaimQueued(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")
	if got := teamToasts(m); strings.Contains(got, "queued") {
		t.Errorf("an idle request must not claim to be queued, got:\n%s", got)
	}
}

var errTeamTest = errTeam("boom")

type errTeam string

func (e errTeam) Error() string { return string(e) }
