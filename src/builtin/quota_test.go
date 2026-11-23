package builtin

import (
	"testing"
	"time"

	"pitago/src/app"
	"pitago/src/pirpc"
)

func quotaCommand(t *testing.T) app.Builtin {
	t.Helper()
	for _, c := range All() {
		if c.Name == "quota" {
			return c
		}
	}
	t.Fatal("/quota is not registered")
	return app.Builtin{}
}

// quotaKeyless neutralises every credential source, including the environment
// (the env fallback reads OPENCODE_*_API_KEY), so a fetch here can only report
// the missing key — the test never leaves the machine.
func quotaKeyless(t *testing.T) {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")
	t.Cleanup(pirpc.QuotaInvalidate)
	pirpc.QuotaInvalidate()
}

// /quota opens the picker and asks for a forced fetch at the same time:
// opening the dialog is the user asking "what now?", which is exactly when
// the 90s cache must be bypassed.
func TestQuotaCommandOpensDialog(t *testing.T) {
	c := quotaCommand(t)
	if c.Origin != OriginPitago {
		t.Fatalf("/quota origin = %q, want pitago", c.Origin)
	}
	m := &app.Model{}
	cmd := c.Run(m, "")
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "quota" {
		t.Fatalf("/quota did not open the quota dialog: %+v", m.Dialogs)
	}
	if cmd == nil {
		t.Fatal("/quota must start the fetch (the dialog open forces a refresh)")
	}
}

// quotaFixture is a two-provider snapshot: one Go window row and one Zen row.
func quotaFixture() []pirpc.ProviderQuota {
	return []pirpc.ProviderQuota{
		{Provider: "opencode-go", Label: "OpenCode Go", Windows: []pirpc.QuotaWindow{
			{Name: "rolling", UsedPct: 23, PercentKnown: true, Status: "ok", WindowSecs: 18000},
		}},
		{Provider: "opencode", Label: "OpenCode Zen", Note: "budget not visible for this key"},
	}
}

// Enter on a row refetches ONLY that provider and keeps the dialog open. The
// snapshot is app state, so it arrives the way the UI gets it: through
// QuotaMsg.
func TestConfirmQuotaRefetchesRowInPlace(t *testing.T) {
	quotaKeyless(t)
	v, _ := (&app.Model{}).Update(app.QuotaMsg{Quotas: quotaFixture()})
	m := v.(app.Model)
	m.Dialogs = []*app.Dialog{app.QuotaDialog(m.Quota(), m.QuotaAt())}

	nm, cmd := confirmQuota(&m, m.Dialogs[0], 0)
	got := nm.(*app.Model)
	if len(got.Dialogs) != 1 || got.Dialogs[0].Kind != "quota" {
		t.Fatalf("Enter must keep the dialog open: %+v", got.Dialogs)
	}
	if cmd == nil {
		t.Fatal("Enter must refetch the row")
	}
	msg, ok := cmd().(app.QuotaMsg)
	if !ok {
		t.Fatalf("refetch must report a QuotaMsg, got %T", cmd())
	}
	if len(msg.Quotas) != 2 {
		t.Fatalf("the other provider row must be merged, not dropped: %+v", msg.Quotas)
	}
	if msg.Quotas[1].Provider != "opencode" || msg.Quotas[1].Note == "" {
		t.Fatalf("untouched row lost its state: %+v", msg.Quotas[1])
	}
	if msg.Quotas[0].Err == "" {
		t.Fatalf("a keyless provider must report an Err, never numbers: %+v", msg.Quotas[0])
	}
}

// A5: Enter resolves the provider through the dialog's own provider ids, not
// by index into the live snapshot. A QuotaMsg can replace that snapshot
// between render and keypress, and resolving by position then refetched
// whichever provider happened to land at that index.
func TestConfirmQuotaResolvesProviderID(t *testing.T) {
	quotaKeyless(t)
	v, _ := (&app.Model{}).Update(app.QuotaMsg{Quotas: quotaFixture()})
	m := v.(app.Model)
	m.Dialogs = []*app.Dialog{app.QuotaDialog(m.Quota(), m.QuotaAt())}
	d := m.Dialogs[0]
	if d.Providers[0] != "opencode-go" {
		t.Fatalf("dialog providers = %+v", d.Providers)
	}
	// The live snapshot now holds the providers in the opposite order; the
	// dialog the user is looking at still says row 0 is opencode-go.
	reordered := quotaFixture()
	reordered[0], reordered[1] = reordered[1], reordered[0]
	v, _ = m.Update(app.QuotaMsg{Quotas: reordered, At: m.QuotaAt().Add(time.Minute)})
	m = v.(app.Model)

	nm, cmd := confirmQuota(&m, d, 0)
	if cmd == nil {
		t.Fatal("Enter must refetch the row")
	}
	msg := cmd().(app.QuotaMsg)
	_ = nm
	// The refetch replaced the Go row (index 1 in the merged snapshot) and
	// left Zen alone: refetching by position would have hit Zen instead.
	if msg.Quotas[1].Provider != "opencode-go" || msg.Quotas[1].Err == "" {
		t.Fatalf("wrong provider refetched: %+v", msg.Quotas[1])
	}
	if msg.Quotas[0].Provider != "opencode" || msg.Quotas[0].Note == "" {
		t.Fatalf("the untouched Zen row was disturbed: %+v", msg.Quotas[0])
	}
}
