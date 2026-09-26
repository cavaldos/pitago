package app

// Wiring tests for the /team lifecycle. The unit tests in team_extcmd_test.go
// exercise handleTeamWatchdog directly; these pin the two hops that were
// missing in production, because a watchdog that is never routed in Update
// degrades to silence — the exact defect the whole change exists to remove.

import (
	"strings"
	"testing"

	"pitago/src/pirpc"
)

// Regression: mergeCommands puts pitago's own /team builtin (Source
// "pitago") ahead of the extension's row and dedupes the rest, so the merged
// list never contains an extension-sourced "team". Probing only that name
// reported "pi-agents-team is not loaded" while the plugin was live, sending
// the user to fix a package list that was already correct.
func TestTeamPluginLoadedDetectsShadowedExtension(t *testing.T) {
	teamCmdLive.Store(0)
	// Exactly what mergeCommands produces: pitago's builtin first, the
	// extension's same-named row already deduped away.
	shadowed := &Model{Cmds: []pirpc.RepoCommand{
		{Name: "team", Source: "pitago"},
		{Name: "team-copy", Source: "extension"},
		{Name: "team-init", Source: "extension"},
	}}
	if !shadowed.teamPluginLoaded() {
		t.Fatal("a shadowed /team with live team-* rows must count as loaded")
	}
	m := shadowed
	m.beginExtCmd(teamCmdName, "loading team…")
	m.teamNoReplyHint()
	if got := teamToasts(m); !strings.Contains(got, "did not answer in time") {
		t.Errorf("a loaded plugin must be reported as busy, not missing, got:\n%s", got)
	}

	// Genuinely absent: only pitago's builtin exists, no team-* rows.
	absent := &Model{Cmds: []pirpc.RepoCommand{{Name: "team", Source: "pitago"}}}
	if absent.teamPluginLoaded() {
		t.Error("with no extension-sourced team-* row the plugin is not loaded")
	}
	absent.beginExtCmd(teamCmdName, "loading team…")
	absent.teamNoReplyHint()
	if got := teamToasts(absent); !strings.Contains(got, "not loaded") {
		t.Errorf("an absent plugin must say so, got:\n%s", got)
	}
}

func teamVisibleText(m *Model) string {
	var b strings.Builder
	for _, blk := range m.blocks {
		b.WriteString(blk.Text)
		b.WriteString("\n")
	}
	for _, t := range m.toasts {
		b.WriteString(t.Text)
		b.WriteString("\n")
	}
	b.WriteString(m.Status)
	return b.String()
}

// The deadline message must be routed by Update. Before this case existed,
// ForwardTeamCommand armed a timer whose tick nothing consumed, so a /team
// that never got a reply stayed silent forever.
func TestTeamWatchdogIsRoutedByUpdate(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	tok := teamCmdLive.Load()
	if tok == 0 {
		t.Fatal("forward must arm a request token, otherwise there is nothing to expire")
	}

	updated, _ := Model(*m).Update(teamWatchdogMsg{name: teamCmdName, epoch: tok})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("Update must return a Model")
	}

	if teamCmdLive.Load() != 0 {
		t.Error("a routed watchdog must retire the live token")
	}
	if got.extCmdPending(teamCmdName) {
		t.Error("a routed watchdog must clear the in-flight marker")
	}
	text := teamVisibleText(&got)
	if !strings.Contains(text, "did not answer in time") &&
		!strings.Contains(text, "not loaded") {
		t.Errorf("a live watchdog routed through Update must report the missing reply, got:\n%s", text)
	}
}

// A watchdog tick for an already-delivered request must stay silent when it
// arrives through Update, so a late timer can never invent a failure.
func TestTeamWatchdogStaleTickThroughUpdateIsSilent(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	tok := teamCmdLive.Load()
	m.openTeamDashboard(teamDashboardFixture)

	updated, _ := Model(*m).Update(teamWatchdogMsg{name: teamCmdName, epoch: tok})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("Update must return a Model")
	}
	text := teamVisibleText(&got)
	if strings.Contains(text, "did not answer in time") || strings.Contains(text, "not loaded") {
		t.Errorf("a stale watchdog must stay silent after delivery, got:\n%s", text)
	}
}

// A forward failure must be reported exactly once, through the lifecycle
// that owns it — not as a bare inline block as well.
func TestTeamForwardErrorReportedOnceThroughLifecycle(t *testing.T) {
	teamCmdLive.Store(0)
	m := &Model{}
	armTeamCmd(t, m, "/team")

	// This is what ForwardTeamCommand produces when pi is not connected.
	updated, _ := Model(*m).Update(extensionCmdAckMsg{name: teamCmdName, err: errTeamTest})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("Update must return a Model")
	}

	text := teamVisibleText(&got)
	if !strings.Contains(text, "boom") {
		t.Errorf("a failed forward must surface the RPC error, got:\n%s", text)
	}
	if got.extCmdPending(teamCmdName) {
		t.Error("the ack must close the lifecycle it belongs to")
	}

	// The watchdog that follows must then stay quiet: the failure is
	// already reported, so it must not be reported a second time.
	after, _ := Model(got).Update(teamWatchdogMsg{name: teamCmdName, epoch: teamCmdLive.Load()})
	final, _ := after.(Model)
	text2 := teamVisibleText(&final)
	if strings.Contains(text2, "did not answer in time") {
		t.Errorf("a reported failure must not be re-reported by the watchdog, got:\n%s", text2)
	}
}
