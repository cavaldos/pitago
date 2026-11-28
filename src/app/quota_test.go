package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "pitago/src/tui/ev"

	"pitago/src/components/pet"
	"pitago/src/pirpc"
)

// quotaSnapshot is the plain two-provider snapshot: one Go window set (28/6/59
// percent USED, i.e. 72/94/41 left) and one Zen budget row. Resets are zero so
// the rows carry no clock and the expectations stay exact.
func quotaSnapshot() []pirpc.ProviderQuota {
	return []pirpc.ProviderQuota{
		{Provider: "opencode-go", Label: "OpenCode Go", Windows: []pirpc.QuotaWindow{
			{Name: "rolling", UsedPct: 28, PercentKnown: true, Status: "ok", WindowSecs: 5 * 60 * 60},
			{Name: "weekly", UsedPct: 6, PercentKnown: true, Status: "ok", WindowSecs: 7 * 24 * 60 * 60},
			{Name: "monthly", UsedPct: 59, PercentKnown: true, Status: "ok"},
		}},
		{Provider: "opencode", Label: "OpenCode Zen", Budgets: []pirpc.QuotaBudget{
			{LimitUSD: 100, SpentUSD: 12.4},
		}},
	}
}

// quotaPanelSnapshot is the fixture the target design is drawn from: reset
// clocks 20s past a whole minute so minute-rounding cannot flip a digit.
func quotaPanelSnapshot() []pirpc.ProviderQuota {
	base := time.Now().Add(20 * time.Second)
	return []pirpc.ProviderQuota{
		{Provider: "opencode-go", Label: "OpenCode Go", Windows: []pirpc.QuotaWindow{
			{Name: "rolling", UsedPct: 28, PercentKnown: true, Status: "ok",
				WindowSecs: 5 * 60 * 60, ResetsAt: base.Add(2*time.Hour + 10*time.Minute)},
			{Name: "weekly", UsedPct: 6, PercentKnown: true, Status: "ok",
				WindowSecs: 7 * 24 * 60 * 60, ResetsAt: base.Add(3*24*time.Hour + 4*time.Hour)},
			{Name: "monthly", UsedPct: 59, PercentKnown: true, Status: "ok",
				ResetsAt: base.Add(12 * 24 * time.Hour)},
		}},
		{Provider: "opencode", Label: "OpenCode Zen", Budgets: []pirpc.QuotaBudget{
			{LimitUSD: 100, SpentUSD: 12.4, ResetsAt: base.Add(3*24*time.Hour + 4*time.Hour)},
		}},
	}
}

// sideModel builds a renderable model at a given sidebar width: the row
// budget and the render both need a live viewport, so these tests go through
// Update like the running TUI does.
func quotaSideModel(t *testing.T, sideW int) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.PetStyle = pet.StyleClassic
	m.Status = "ready"
	m.recentModels = []RecentModel{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	m.ModelLbl = "a"
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	if sideW > 0 {
		m.SideWidth = sideW
	}
	m.Refresh()
	return m
}

// quotaSectionText extracts the rendered QUOTA block (header + rows, no
// separator) from the real sidebar content.
func quotaSectionText(t *testing.T, m Model) string {
	t.Helper()
	lines := strings.Split(stripANSI(m.buildSidebarContent()), "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), "QUOTA") {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "──") {
				end = j
				break
			}
		}
		return strings.Join(lines[i:end], "\n")
	}
	return ""
}

// The sidebar panel is the opencode-quota shape: QUOTA (n) header, one group
// line per provider, one row per window with the bar filled by the percent
// LEFT, a right-aligned percent column and the reset countdown, plus a ┃
// even-use mark where a linear burn would be. The golden strings below ARE
// the target design at the default inner width (30) and at minSideW (26 → 22).
func TestSideQuotaPanelGolden(t *testing.T) {
	for _, tc := range []struct {
		name, want   string
		sideW, inner int
	}{
		{
			name: "default", sideW: 0, inner: 30,
			want: "QUOTA (2) ▾\n" +
				"OpenCode Go\n" +
				"   5h ████┃██░░░  72%  2h10m\n" +
				" week █████┃███░  94%   3d4h\n" +
				"month ████░░░░░░  41%    12d\n" +
				"OpenCode Zen\n" +
				"  $12.40 / $100           3d4h",
		},
		{
			name: "minSideW", sideW: 26, inner: 22,
			want: "QUOTA (2) ▾\n" +
				"OpenCode Go\n" +
				"   5h █┃░  72%  2h10m\n" +
				" week █┃█  94%   3d4h\n" +
				"month █░░  41%    12d\n" +
				"OpenCode Zen\n" +
				"  $12.40 / $100   3d4h",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := quotaSideModel(t, tc.sideW)
			if got := m.sideInnerW(); got != tc.inner {
				t.Fatalf("inner width = %d, want %d", got, tc.inner)
			}
			m.quota = quotaPanelSnapshot()
			m.Refresh()
			got := quotaSectionText(t, m)
			t.Logf("rendered QUOTA block (inner %d):\n%s", tc.inner, got)
			if got != tc.want {
				t.Fatalf("QUOTA block (inner %d):\n got:\n%s\nwant:\n%s", tc.inner, got, tc.want)
			}
			// The budget rule must equal what is on screen: header + rows +
			// separator, plus the same i+1 click line every section above uses.
			if rows := m.sideQuotaRows(); m.sideRowsBeforeRecent() !=
				m.petRows()+sideSessionRows+m.sideModelRows()+sideStatsRows+2+len(rows) {
				t.Fatalf("sideRowsBeforeRecent does not match the rendered rows")
			}
		})
	}
}

// The sidebar section costs zero rows until the first fetch lands: a session
// that never configured OpenCode must not shift the sections below it, and it
// must not render a bare QUOTA header either.
func TestSideQuotaRowsEmptyBeforeFetch(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	before := m.sideRowsBeforeRecent()
	if rows := m.sideQuotaRows(); len(rows) != 0 {
		t.Fatalf("empty snapshot must render no rows, got %+v", rows)
	}
	if got := m.sideRowsBeforeRecent(); got != before {
		t.Fatalf("empty snapshot must add no rows, got %d want %d", got, before)
	}

	// ...and with a snapshot it is exactly header + rows + separator. The
	// rows are the group lines (2) plus the windows (3) and the budget (1).
	m.quota = quotaSnapshot()
	rows := m.sideQuotaRows()
	if got, want := len(rows), 6; got != want {
		t.Fatalf("rows = %d, want %d: %+v", got, want, rows)
	}
	if got := quotaGroupCount(rows); got != 2 {
		t.Fatalf("group count = %d, want 2", got)
	}
	if got, want := m.sideRowsBeforeRecent(), before+2+len(rows); got != want {
		t.Fatalf("quota rows = %d, want %d", got, want)
	}
}

// A rendered sidebar with a visible but EMPTY QUOTA section carries no header
// and no separator: the panel is invisible until there is something to say.
func TestSideQuotaEmptySectionRendersNothing(t *testing.T) {
	m := quotaSideModel(t, 0)
	if !m.SideVisible(SideQuota) {
		t.Fatal("SideQuota must be visible by default for this test")
	}
	before := m.sideRowsBeforeRecent()
	content := stripANSI(m.buildSidebarContent())
	if strings.Contains(content, "QUOTA") {
		t.Fatalf("empty section rendered a header:\n%s", content)
	}
	if got := m.sideRowsBeforeRecent(); got != before {
		t.Fatalf("empty section changed the budget: %d != %d", got, before)
	}
}

// Rows carry the panel vocabulary: percent LEFT (never used), the compact
// label and the provider group line; Zen stays in real dollars.
func TestSideQuotaRowsVocabulary(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	m.quota = quotaSnapshot()
	rows := m.sideQuotaRows()
	if got, want := rows[0].Text, "OpenCode Go"; got != want || !rows[0].Group {
		t.Fatalf("group line = %q", got)
	}
	for i, want := range []string{
		"   5h ███████░░░  72%",
		" week █████████░  94%",
		"month ████░░░░░░  41%",
	} {
		if got := rows[1+i].Text; got != want {
			t.Fatalf("window row %d = %q, want %q", i, got, want)
		}
	}
	if got, want := rows[4].Text, "OpenCode Zen"; got != want {
		t.Fatalf("zen group = %q", got)
	}
	if got, want := rows[5].Text, "  $12.40 / $100"; got != want {
		t.Fatalf("zen budget row = %q, want %q", got, want)
	}

	// Colour is about danger, so it follows the USED percent: 72% left is
	// 28% used (green), 6% left is 94% used (red), plus the provider's own
	// rate-limited word outranks any percent.
	cases := []struct {
		w    pirpc.QuotaWindow
		want string
	}{
		{pirpc.QuotaWindow{Name: "rolling", UsedPct: 28, PercentKnown: true, Status: "ok", WindowSecs: 18000}, okStyle.Render("x")},
		{pirpc.QuotaWindow{Name: "weekly", UsedPct: 25, PercentKnown: true, Status: "ok", WindowSecs: 18000}, warnStyle.Render("x")},
		{pirpc.QuotaWindow{Name: "weekly", UsedPct: 6, PercentKnown: true, Status: "ok", WindowSecs: 18000}, errStyle.Render("x")},
		{pirpc.QuotaWindow{Name: "rolling", UsedPct: 10, PercentKnown: true, Status: "rate-limited"}, errStyle.Render("x")},
	}
	for _, tc := range cases {
		rows := quotaWindowRows([]pirpc.QuotaWindow{tc.w}, 30)
		if len(rows) != 1 || rows[0].Style.Render("x") != tc.want {
			t.Fatalf("%+v: style mismatch (%+v)", tc.w, rows)
		}
	}
}

// A Note is a benign state the provider calls normal (Zen's gated budget
// endpoint): dim, no numbers. An Err is a real failure: red, no numbers.
func TestSideQuotaNoteAndError(t *testing.T) {
	m := Model{winW: 120, winH: 40, SideWidth: 60}
	m.quota = []pirpc.ProviderQuota{
		{Provider: "opencode", Label: "OpenCode Zen", Note: "budget not visible for this key"},
		{Provider: "opencode-go", Label: "OpenCode Go", Err: "no OpenCode Go subscription on this key"},
	}
	rows := m.sideQuotaRows()
	if len(rows) != 4 {
		t.Fatalf("want two groups of (label, sentence), got %+v", rows)
	}
	if rows[1].Text != "budget not visible for this key" || strings.Contains(rows[1].Text, "%") {
		t.Fatalf("note row = %q", rows[1].Text)
	}
	if rows[1].Style.Render("x") != toolStyle.Render("x") {
		t.Fatal("a Note must render dim, not red")
	}
	if rows[3].Style.Render("x") != errStyle.Render("x") {
		t.Fatal("an Err must render red")
	}
	if strings.Contains(rows[3].Text, "%") {
		t.Fatalf("an Err row must not invent a percent: %q", rows[3].Text)
	}

	// A provider with neither numbers nor a sentence contributes nothing: a
	// bare group line would read as "0% used".
	empty := Model{winW: 120, winH: 40}
	empty.quota = []pirpc.ProviderQuota{{Provider: "opencode-go", Label: "OpenCode Go"}}
	if rows := empty.sideQuotaRows(); len(rows) != 0 {
		t.Fatalf("a stateless provider must render nothing, got %+v", rows)
	}
}

// Unknown percent must never become "0% left": the row shows the label and a
// dash, no bar fill, no number.
func TestSideQuotaUnknownPercent(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	m.quota = []pirpc.ProviderQuota{{Provider: "opencode-go", Label: "OpenCode Go",
		Windows: []pirpc.QuotaWindow{{Name: "rolling", Status: "ok", WindowSecs: 18000}}}}
	rows := m.sideQuotaRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if got := rows[1].Text; got != "5h               —" {
		t.Fatalf("unknown-percent row = %q", got)
	}
	if strings.Contains(rows[1].Text, "0%") {
		t.Fatal("an unknown percent must never render as 0%")
	}
	// The dialog says the same thing, in the same vocabulary.
	d := QuotaDialog(m.quota, time.Time{})
	if d.Options[0] != "OpenCode Go · 5h —" {
		t.Fatalf("dialog row = %q", d.Options[0])
	}
}

// Every rendered row must fit the sidebar's inner width at minSideW, including
// the longest real sentences (Zen's tolerated 403 and a 401 re-login hint).
func TestSideQuotaRowsFitMinWidth(t *testing.T) {
	m := quotaSideModel(t, 26)
	inner := m.sideInnerW()
	m.quota = []pirpc.ProviderQuota{
		{Provider: "opencode", Label: "OpenCode Zen", Note: "budget not visible for this key"},
		{Provider: "opencode-go", Label: "OpenCode Go", Err: "key rejected (401) — re-login with /login opencode-go"},
		{Provider: "opencode-go", Label: "OpenCode Go", Plan: "pro", Windows: []pirpc.QuotaWindow{
			{Name: "rolling", UsedPct: 28, PercentKnown: true, Status: "ok", WindowSecs: 18000,
				ResetsAt: time.Now().Add(2*time.Hour + 20*time.Second)},
			{Name: "weekly", UsedPct: 6, PercentKnown: true, Status: "rate-limited", WindowSecs: 604800,
				ResetsAt: time.Now().Add(3*24*time.Hour + 20*time.Second)},
			{Name: "codex-300", UsedPct: 50, PercentKnown: true, Status: "ok", WindowSecs: 300 * 60,
				ResetsAt: time.Now().Add(90 * time.Minute)},
			// An endpoint bucket we have no label for: the raw name must be
			// clamped, never wrapped.
			{Name: "an-unknown-bucket-with-a-long-name", UsedPct: 50, PercentKnown: true, Status: "ok"},
		}},
		{Provider: "opencode", Label: "OpenCode Zen", Budgets: []pirpc.QuotaBudget{
			{LimitUSD: 100000, SpentUSD: 12345.67, ResetsAt: time.Now().Add(30 * 24 * time.Hour)},
		}},
	}
	m.Refresh()
	for _, r := range m.sideQuotaRows() {
		if w := lipgloss.Width(r.Text); w > inner {
			t.Fatalf("row %q is %d cells, inner is %d", r.Text, w, inner)
		}
	}
	content := stripANSI(m.buildSidebarContent())
	for _, ln := range strings.Split(content, "\n") {
		if w := lipgloss.Width(ln); w > inner {
			t.Fatalf("sidebar line %q is %d cells, inner is %d", ln, w, inner)
		}
	}
}

// Space is conceded in the reference order: the reset clock goes first (the
// bar is the point), and the bar itself goes last.
func TestQuotaWindowRowsDegrade(t *testing.T) {
	ws := []pirpc.QuotaWindow{{
		Name: "monthly", UsedPct: 59, PercentKnown: true, Status: "ok",
		ResetsAt: time.Now().Add(12*24*time.Hour + 20*time.Second),
	}}
	// 18 cells: label 5 + 1 + bar 6 + gap + percent = 18, reset dropped.
	if got := quotaWindowRows(ws, 18)[0].Text; got != "month ██░░░░  41%" {
		t.Fatalf("18-wide row = %q", got)
	}
	// 12 cells: no room for a readable bar, so label + percent only.
	if got := quotaWindowRows(ws, 12)[0].Text; got != "month  41%" {
		t.Fatalf("12-wide row = %q", got)
	}
}

// The even-use mark needs a VERIFIED duration: a window with a reset clock but
// WindowSecs 0 gets no mark at all (a guessed position would be a fabricated
// line).
func TestQuotaEvenUseMarkNeedsDuration(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour)
	known := pirpc.QuotaWindow{Name: "rolling", UsedPct: 40, PercentKnown: true, WindowSecs: 5 * 3600, ResetsAt: reset}
	if got := quotaBar(known, 10); !strings.Contains(got, "┃") {
		t.Fatalf("known duration must draw the mark: %q", got)
	}
	unknown := known
	unknown.WindowSecs = 0
	if got := quotaBar(unknown, 10); strings.Contains(got, "┃") {
		t.Fatalf("unknown duration must not guess a mark: %q", got)
	}
	if _, ok := quotaEvenMark(known, 10); !ok {
		t.Fatal("known duration + reset must yield a mark position")
	}
	if _, ok := quotaEvenMark(unknown, 10); ok {
		t.Fatal("unknown duration must not yield a mark position")
	}
	if got, want := quotaCountdown(time.Now().Add(-time.Minute)), "now"; got != want {
		t.Fatalf("past reset = %q, want %q", got, want)
	}
	if got := quotaCountdown(time.Time{}); got != "" {
		t.Fatalf("zero reset must render nothing, got %q", got)
	}
}

// The dialog speaks the panel's vocabulary: percent LEFT, spelled out, with
// the reset countdowns and the snapshot age in the desc, and provider ids
// parallel to the rows so Enter never resolves a row by position.
func TestQuotaDialogRows(t *testing.T) {
	reset := time.Now().Add(2*time.Hour + 5*time.Minute + 20*time.Second)
	q := quotaSnapshot()
	q[0].Windows[0].ResetsAt = reset
	q[1].Budgets[0].ResetsAt = reset
	q[1].Budgets[0].LimitUSD = 100
	d := QuotaDialog(q, time.Now().Add(-2*time.Minute))
	if d.Kind != "quota" {
		t.Fatalf("kind = %q", d.Kind)
	}
	if !strings.Contains(d.Message, "percent LEFT") {
		t.Fatalf("the dialog must label its direction: %q", d.Message)
	}
	if len(d.Options) != 2 || len(d.Descs) != 2 || len(d.Providers) != 2 {
		t.Fatalf("one row per provider, got %+v / %+v / %+v", d.Options, d.Descs, d.Providers)
	}
	if got, want := d.Options[0], "OpenCode Go · 5h 72% left · week 94% left · month 41% left"; got != want {
		t.Fatalf("go row = %q, want %q", got, want)
	}
	if d.Providers[0] != "opencode-go" || d.Providers[1] != "opencode" {
		t.Fatalf("provider ids = %+v", d.Providers)
	}
	if !strings.Contains(d.Descs[0], "5h resets in 2h5m") || !strings.Contains(d.Descs[0], "snapshot 2m ago") {
		t.Fatalf("go desc = %q", d.Descs[0])
	}
	if !strings.Contains(d.Descs[1], "budget resets in 2h5m") {
		t.Fatalf("zen desc = %q", d.Descs[1])
	}
	if got, want := d.Options[1], "OpenCode Zen · $12.40 / $100"; got != want {
		t.Fatalf("zen row = %q, want %q", got, want)
	}

	// No credentials → a dialog with the /login hint and no rows.
	empty := QuotaDialog(nil, time.Time{})
	if len(empty.Options) != 0 || !strings.Contains(empty.Message, "/login") {
		t.Fatalf("empty dialog = %+v", empty)
	}
	// A note provider: label in the row, sentence in the desc, no numbers.
	noteQ := QuotaDialog([]pirpc.ProviderQuota{{Provider: "opencode", Label: "OpenCode Zen",
		Note: "budget not visible for this key"}}, time.Time{})
	if noteQ.Options[0] != "OpenCode Zen" || noteQ.Descs[0] != "budget not visible for this key" {
		t.Fatalf("note row = %q / %q", noteQ.Options[0], noteQ.Descs[0])
	}
}

// quotaMsgModel is a model with an OPEN /quota dialog parked under a rename
// prompt: the stack position A5 called out.
func quotaMsgModel(t *testing.T) Model {
	t.Helper()
	m := Model{}
	v, _ := m.Update(QuotaMsg{Quotas: quotaSnapshot(), At: time.Now().Add(-3 * time.Minute)})
	m = v.(Model)
	qd := QuotaDialog(m.Quota(), m.QuotaAt())
	qd.Cursor = 1
	m.Dialogs = []*Dialog{{Kind: "rename", Title: "Rename key", Filter: "x"}, qd}
	return m
}

// A QuotaMsg landing on an open /quota dialog (anywhere in the stack) keeps it
// open, updates the rows in place, keeps the cursor and refreshes the age line.
func TestQuotaMsgRefreshesOpenDialogInPlace(t *testing.T) {
	m := quotaMsgModel(t)
	next := quotaSnapshot()
	next[0].Windows[0].UsedPct = 3 // 97% left
	v, _ := m.Update(QuotaMsg{Quotas: next, At: time.Now().Add(-2 * time.Minute)})
	got := v.(Model)
	if len(got.Dialogs) != 2 || got.Dialogs[0].Kind != "rename" || got.Dialogs[1].Kind != "quota" {
		t.Fatalf("dialog stack changed: %+v", got.Dialogs)
	}
	d := got.Dialogs[1]
	if d.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", d.Cursor)
	}
	if !strings.Contains(d.Options[0], "5h 97% left") {
		t.Fatalf("rows not updated in place: %q", d.Options[0])
	}
	if !strings.Contains(d.Descs[0], "snapshot 2m ago") {
		t.Fatalf("age line not refreshed: %q", d.Descs[0])
	}
}

// A snapshot fetched BEFORE the one on screen is dropped: a slow post-turn
// fetch must not overwrite the user's explicit Enter refresh with older data.
func TestQuotaMsgStaleDropped(t *testing.T) {
	m := quotaMsgModel(t)
	fresh := quotaSnapshot()
	fresh[0].Windows[0].UsedPct = 3
	newer := time.Now()
	v, _ := m.Update(QuotaMsg{Quotas: fresh, At: newer})
	m = v.(Model)
	if !m.QuotaAt().Equal(newer) {
		t.Fatalf("quotaAt = %v, want %v", m.QuotaAt(), newer)
	}
	old := quotaSnapshot()
	old[0].Windows[0].UsedPct = 90
	v, _ = m.Update(QuotaMsg{Quotas: old, At: newer.Add(-time.Minute)})
	got := v.(Model)
	if !got.QuotaAt().Equal(newer) {
		t.Fatalf("stale message moved quotaAt: %v", got.QuotaAt())
	}
	if got.Quota()[0].Windows[0].UsedPct != 3 {
		t.Fatalf("stale message replaced the snapshot: %+v", got.Quota())
	}
	if d := got.Dialogs[1]; !strings.Contains(d.Options[0], "5h 97% left") {
		t.Fatalf("stale message rebuilt the dialog: %q", d.Options[0])
	}
	// An empty snapshot (At zero = nothing fetched) still clears the panel:
	// deleting the last key must not leave the old rows on screen.
	v, _ = m.Update(QuotaMsg{Quotas: nil})
	if got := v.(Model); len(got.Quota()) != 0 || !got.QuotaAt().IsZero() {
		t.Fatalf("empty snapshot not applied: %+v at %v", got.Quota(), got.QuotaAt())
	}
}

// TestRecentAtMatchesQuotaSection: the QUOTA section sits above RECENT MODELS,
// so sideRowsBeforeRecent must count its header + rows + separator or every
// click lands high. The 3-window fixture is the tall case (6 rows) from the
// target design, and the scrolled case must move the mapping with it.
func TestRecentAtMatchesQuotaSection(t *testing.T) {
	m := quotaSideModel(t, 0)
	before := m.sideRowsBeforeRecent()
	m.quota = quotaPanelSnapshot()
	m.Refresh()
	rows := m.sideQuotaRows()
	if got, want := m.sideRowsBeforeRecent(), before+2+len(rows); got != want {
		t.Fatalf("sideRowsBeforeRecent = %d, want %d (%d quota rows)", got, want, len(rows))
	}
	if len(rows) < 6 {
		t.Fatalf("the 3-window provider must cost 6+ rows, got %d", len(rows))
	}
	lines := strings.Split(stripANSI(m.renderSidebar()), "\n")
	quotaHead, recentHead := -1, -1
	for i, ln := range lines {
		switch {
		case strings.Contains(ln, "QUOTA"):
			quotaHead = i
		case strings.Contains(ln, "RECENT MODELS"):
			recentHead = i
		}
	}
	if quotaHead < 0 || recentHead < 0 {
		t.Fatalf("sidebar missing QUOTA (%d) or RECENT MODELS (%d):\n%s", quotaHead, recentHead, strings.Join(lines, "\n"))
	}
	if got, want := quotaHead, 1+m.sideRowsBeforeRecent()-2-len(rows); got != want {
		t.Fatalf("QUOTA header at line %d, row budget says %d", got, want)
	}
	if recentHead != quotaHead+1+len(rows)+1 {
		t.Fatalf("RECENT header at %d, want quota head %d + %d rows + sep + 1", recentHead, quotaHead, len(rows))
	}
	x := m.mainW()
	at := func(y, want int) {
		t.Helper()
		if idx, ok := m.recentAt(x, y); !ok || idx != want {
			t.Fatalf("y=%d: got %d,%v, want %d", y, idx, ok, want)
		}
	}
	at(1+recentHead, 0)
	at(1+recentHead+1, 1)
	// Scrolled two rows down the same screen rows map two models further.
	m.sideVp.SetYOffset(2)
	if idx, ok := m.recentAt(x, 1+recentHead); !ok || idx != 2 {
		t.Fatalf("scrolled: got %d,%v, want 2", idx, ok)
	}
}

// Hiding the section must drop its rows from the budget too, or clicks land
// low after the toggle.
func TestSideRowsBeforeRecentHonoursQuotaVisibility(t *testing.T) {
	m := Model{winW: 120, winH: 40, Side: map[string]bool{SideQuota: false}}
	m.quota = quotaSnapshot()
	hidden := m.sideRowsBeforeRecent()
	m.Side[SideQuota] = true
	if got, want := m.sideRowsBeforeRecent(), hidden+2+len(m.sideQuotaRows()); got != want {
		t.Fatalf("shown quota = %d rows, want %d (hidden cost %d)", got, want, hidden)
	}
}

// A credential change must invalidate the 90s cache BEFORE the refresh: after
// switching from key A to key B the panel must not keep key A's numbers.
func TestQuotaKeyChangedInvalidatesCache(t *testing.T) {
	// No credential anywhere, so the snapshot is empty and nothing touches the
	// network (QuotaSnapshot skips providers without a key). Every provider
	// env var is cleared: a developer's shell often exports real keys, and
	// one of them would turn this into a live API call.
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	for _, p := range pirpc.ProviderEnvsAll() {
		t.Setenv(p.Env, "")
	}
	t.Cleanup(pirpc.QuotaInvalidate)

	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	pirpc.QuotaForce(m.KeyPath, "")
	if !pirpc.QuotaFresh(m.KeyPath) {
		t.Fatal("the seed snapshot must be fresh")
	}
	if cmd := m.quotaKeyChanged(); cmd == nil {
		t.Fatal("a credential change must schedule a refetch")
	}
	if pirpc.QuotaFresh(m.KeyPath) {
		t.Fatal("a credential change must invalidate the cached snapshot")
	}
}

func TestQuotaPanelFollowsModelProvider(t *testing.T) {
	reset := time.Now().Add(80 * time.Minute)
	q := []pirpc.ProviderQuota{
		{Provider: "opencode-go", Label: "OpenCode Go", Windows: []pirpc.QuotaWindow{
			{Name: "rolling", UsedPct: 24, PercentKnown: true, Status: "ok", ResetsAt: reset}}},
		{Provider: "openai-codex", Label: "OpenAI Codex", Plan: "plus", Windows: []pirpc.QuotaWindow{
			{Name: "primary", UsedPct: 95, PercentKnown: true, Status: "ok", WindowSecs: 18000, ResetsAt: reset}}},
	}
	m := Model{quota: q, ModelProv: "opencode-go"}
	rows := m.sideQuotaRows()
	if got := m.sideQuotaTitle(rows); got != "OPENCODE GO QUOTA" {
		t.Fatalf("title = %q", got)
	}
	if len(rows) != 2 || !strings.Contains(rows[1].Text, "headroom") {
		t.Fatalf("rows = %+v", rows)
	}
	m.ModelProv = "openai-codex"
	rows = m.sideQuotaRows()
	if got := m.sideQuotaTitle(rows); got != "OPENAI CODEX QUOTA · PLUS" || !strings.Contains(rows[len(rows)-1].Text, "almost exhausted") {
		t.Fatalf("title=%q rows=%+v", got, rows)
	}
	m.ModelProv = "anthropic"
	if rows := m.sideQuotaRows(); len(rows) != 0 {
		t.Fatalf("unsupported provider must render nothing, got %+v", rows)
	}
}

// The model hint is Groq's alone: its rate limit is per model, so the probe
// has to measure the model in play. Every other provider gets no hint.
func TestQuotaModelHintIsGroqOnly(t *testing.T) {
	m := Model{ModelProv: "groq", ModelLbl: "openai/gpt-oss-120b"}
	if got := m.quotaModel(); got != "openai/gpt-oss-120b" {
		t.Fatalf("groq hint = %q", got)
	}
	m = Model{ModelProv: "google", ModelLbl: "gemini-3.8-flash"}
	if got := m.quotaModel(); got != "" {
		t.Fatalf("hint for a provider that ignores it = %q, want empty", got)
	}
}
