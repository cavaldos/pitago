package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "pitago/src/tui/ev"

	"pitago/src/pirpc"
)

// Quota tracking glue (increment 1: OpenCode Go + OpenCode Zen).
//
// The fetch itself lives in src/pirpc/quota.go; this file owns the two
// surfaces it feeds — the /quota dialog and the sidebar QUOTA panel — and the
// TTL policy around them. Everything here runs on the event loop, so the HTTP
// calls travel as a tea.Cmd and land as QuotaMsg.
//
// Both surfaces speak the same vocabulary, taken from the opencode-quota
// panel: a group line per provider and one row per window shaped
// `name [bar] percent% reset`, where the bar fills with the percent LEFT (the
// number the user actually spends), the percent is right-aligned in a fixed
// four-cell column, and a ┃ even-use mark sits inside the bar where a strictly
// linear burn would be right now. Showing "used" was the v1 behaviour; "left"
// reads better and matches the reference design, so the dialog says so out
// loud and nobody has to guess which direction a number points.

// QuotaMsg carries one snapshot back to the UI. Err is a snapshot-level
// failure only (none today); a provider that could not be read keeps its
// sentence in ProviderQuota.Err/Note and still gets a row.
//
// At is the newest ProviderQuota.FetchedAt in the snapshot (zero when the
// snapshot is empty). It is the message's own clock, not the arrival time:
// the sidebar's "fetched" age is then the age of the DATA, and a message that
// is older than the snapshot on screen can be dropped (a slow agent_settled
// fetch must never overwrite the user's explicit Enter refresh).
type QuotaMsg struct {
	Quotas []pirpc.ProviderQuota
	At     time.Time
	Err    error
}

// Quota returns the cached snapshot the sidebar renders and the dialog is
// rebuilt from.
func (m Model) Quota() []pirpc.ProviderQuota { return m.quota }

// QuotaAt is when that snapshot was FETCHED (zero = never fetched).
func (m Model) QuotaAt() time.Time { return m.quotaAt }

// quotaSnapshotAt is the newest fetch timestamp in a snapshot. A snapshot with
// no rows has no clock at all, which is honest: nothing was fetched, so the
// dialog must not print an age.
func quotaSnapshotAt(qs []pirpc.ProviderQuota) time.Time {
	var at time.Time
	for _, q := range qs {
		if q.FetchedAt.After(at) {
			at = q.FetchedAt
		}
	}
	return at
}

// RefreshQuota starts a fetch off the event loop. stale=true is the
// post-turn hook: it spends nothing while the 90s cache is fresh, so a
// reply arriving right after another one adds no latency and no traffic.
// stale=false is the dialog-open path, where the user is asking "what now?".
func (m *Model) RefreshQuota(stale bool) tea.Cmd {
	keyPath := m.KeyPath
	if stale && pirpc.QuotaFresh(keyPath) {
		return nil
	}
	model := m.quotaModel()
	return func() tea.Msg {
		var snap []pirpc.ProviderQuota
		if stale {
			snap = pirpc.QuotaCached(keyPath, model)
		} else {
			snap = pirpc.QuotaForce(keyPath, model)
		}
		return QuotaMsg{Quotas: snap, At: quotaSnapshotAt(snap)}
	}
}

// RefreshQuotaProvider refetches ONE row (Enter on the /quota dialog). The
// merge happens inside the Cmd, against the snapshot the caller was looking
// at: the other provider keeps its row and only this one costs HTTP.
func (m *Model) RefreshQuotaProvider(provider string) tea.Cmd {
	keyPath := m.KeyPath
	snap := append([]pirpc.ProviderQuota(nil), m.quota...)
	// Read the hint on the event loop, not inside the Cmd: the goroutine must
	// not touch the model while the loop is writing it.
	model := m.quotaModel()
	return func() tea.Msg {
		q := pirpc.FetchProviderQuota(provider, keyPath, model)
		merged := false
		for i := range snap {
			if snap[i].Provider == provider {
				snap[i], merged = q, true
				break
			}
		}
		if !merged {
			snap = append(snap, q)
		}
		return QuotaMsg{Quotas: snap, At: quotaSnapshotAt(snap)}
	}
}

// quotaModel is the model the user is actually running, and only that: it is
// the hint the Groq probe uses, because Groq's rate limit is per model and a
// probe with another model answers a different question. For every other
// provider it stays empty — their endpoints do not care which model is loaded.
func (m Model) quotaModel() string {
	if m.ModelProv != "groq" {
		return ""
	}
	return m.ModelLbl
}

// quotaKeyChanged drops the cached snapshot and refetches. Every credential
// change (/login switch, add, delete, /logout, OAuth disconnect) calls it
// BEFORE the refresh: the cached numbers belong to the key that was active
// when they were fetched, so without the invalidate a switch from key A (90%
// used) to key B (5% used) kept showing key A's row for up to 90s.
func (m *Model) quotaKeyChanged() tea.Cmd {
	pirpc.QuotaInvalidate()
	return m.RefreshQuota(true)
}

// QuotaDialog builds the /quota picker. It lives here, next to the sidebar
// rows, because the dialog is REBUILT in place every time a QuotaMsg lands —
// one builder keeps the dialog and the panel from drifting apart.
//
// Providers parallels Options with the provider ids: Enter resolves the row
// through that id, never by index into a snapshot that a landing fetch may
// have replaced. fetchedAt is the snapshot's age: a provider number without
// "how old" invites reading a 90s-old cache as live.
func QuotaDialog(quotas []pirpc.ProviderQuota, fetchedAt time.Time) *Dialog {
	d := &Dialog{
		Kind:  "quota",
		Title: "Provider quota",
		Message: "read-only: each provider's own usage endpoint, cached 90s · " +
			"numbers are percent LEFT (the sidebar bar fills with what is left) · " +
			"Enter refetches the row · ↑↓ move · Esc closes",
	}
	if len(quotas) == 0 {
		d.Message = "no quota-tracked credentials yet — /login opencode-go · /login opencode"
	}
	for _, q := range quotas {
		d.Options = append(d.Options, quotaRowText(q))
		d.Descs = append(d.Descs, quotaRowDesc(q, fetchedAt))
		d.Providers = append(d.Providers, q.Provider)
	}
	d.Reindex()
	return d
}

// RefreshQuotaDialog rebuilds the open /quota dialog from the current
// snapshot, keeping the user's cursor: a refetch must not yank the selection
// out from under Enter. It scans the whole stack because the quota picker is
// not always on top (a rename prompt can be parked over it), and index 0 was
// a lucky assumption. There is no filter to preserve: "quota" is not a filter
// kind (see isFilterKind in pconfig.go), so typing never narrows this list.
func (m *Model) RefreshQuotaDialog() {
	for i, d := range m.Dialogs {
		if d.Kind != "quota" {
			continue
		}
		nd := QuotaDialog(m.quota, m.quotaAt)
		if d.Cursor >= 0 && d.Cursor < len(nd.FIdx) {
			nd.Cursor = d.Cursor
		}
		m.Dialogs[i] = nd
		return
	}
}

// quotaPanelText is one provider's panel as plain text (title, rows, verdict)
// for surfaces that cannot take styled rows, like the native details pane.
func quotaPanelText(q pirpc.ProviderQuota, inner int) string {
	pm := Model{ModelProv: q.Provider, quota: []pirpc.ProviderQuota{q}}
	rows := quotaPanelRows(q, inner)
	lines := []string{pm.sideQuotaTitle(rows), ""}
	if len(rows) == 0 {
		lines = append(lines, "no usage in the endpoint response")
	}
	for _, r := range rows {
		lines = append(lines, r.Text)
	}
	return strings.Join(lines, "\n")
}

// renderQuotaDialog draws /quota as the same panels as the sidebar, one block
// per provider: `OPENCODE GO QUOTA`, a bar row per window, then the verdict.
// The cursor (▸) marks the provider Enter will refetch.
func (m Model) renderQuotaDialog(d *Dialog) string {
	boxW := min(max(m.winW-10, 62), 100)
	inner := boxW - 10
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString("\n")
	if len(m.quota) == 0 {
		b.WriteString(toolStyle.Render("— nothing to show —") + "\n")
	}
	for i, q := range m.quota {
		mark := "  "
		if i == d.Cursor {
			mark = "▸ "
		}
		pm := m
		pm.ModelProv = q.Provider
		rows := quotaPanelRows(q, inner)
		b.WriteString(mark + sideTitleStyle.Render(pm.sideQuotaTitle(rows)) + "\n")
		if len(rows) == 0 {
			b.WriteString("  " + toolStyle.Render("no usage in the endpoint response") + "\n")
		}
		for _, r := range rows {
			b.WriteString("  " + r.Style.Render(r.Text) + "\n")
		}
		if i < len(m.quota)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n" + toolStyle.Render("↑↓ move · Enter refetch · Esc close"))
	box := dlgStyle.Width(boxW).Render(b.String())
	return lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box)
}

// quotaRowText is one dialog row, in the panel's vocabulary: every window the
// provider reported (a user comparing 5h against month needs all three), each
// as `label N% left`. A provider with only a sentence shows just its label
// here — the sentence is the desc — so no row can ever look like a real 0%.
func quotaRowText(q pirpc.ProviderQuota) string {
	if len(q.Windows) == 0 && len(q.Budgets) == 0 {
		return q.Label
	}
	parts := make([]string, 0, len(q.Windows)+len(q.Budgets)+1)
	parts = append(parts, q.Label)
	// The plan (a tier, or the model a Groq probe measured) belongs to the
	// row: without it the numbers answer "whose quota?" with nothing.
	if plan := strings.TrimSpace(q.Plan); plan != "" {
		parts = append(parts, plan)
	}
	for _, w := range q.Windows {
		if !w.PercentKnown {
			// No invented number: the row says the window exists and the
			// percent did not arrive.
			parts = append(parts, w.ShortLabel()+" —")
			continue
		}
		p := fmt.Sprintf("%s %d%% left", w.ShortLabel(), w.PercentLeft())
		if !quotaStatusOK(w.Status) {
			p += " " + w.Status
		}
		parts = append(parts, p)
	}
	for _, b := range q.Budgets {
		parts = append(parts, quotaBudgetText(b))
	}
	return strings.Join(parts, " · ")
}

// quotaRowDesc carries the reset countdowns (and, for a failed lookup, the
// whole sentence) plus the snapshot age: the desc column is where a user asks
// "when does this come back, and how old is this answer?".
func quotaRowDesc(q pirpc.ProviderQuota, fetchedAt time.Time) string {
	if q.Err != "" {
		return q.Err
	}
	if len(q.Windows) == 0 && len(q.Budgets) == 0 {
		if q.Note != "" {
			return q.Note
		}
		return "no usage in the endpoint response"
	}
	var parts []string
	for _, w := range q.Windows {
		if s := quotaResetPhrase(w.ShortLabel(), w.ResetsAt); s != "" {
			parts = append(parts, s)
		}
	}
	for _, b := range q.Budgets {
		if s := quotaResetPhrase("budget", b.ResetsAt); s != "" {
			parts = append(parts, s)
		}
	}
	if age := quotaAge(fetchedAt); age != "" {
		parts = append(parts, "snapshot "+age)
	}
	// A row that was measured earlier than the rest of the snapshot says so:
	// the Groq probe is reused for minutes (it spends a request of the very
	// budget it reports), and a stale number must not read as a live one.
	if q.FetchedAt.Before(fetchedAt.Add(-time.Minute)) {
		if age := quotaAge(q.FetchedAt); age != "" {
			parts = append(parts, "probed "+age)
		}
	}
	if len(parts) == 0 {
		return "no reset time reported"
	}
	return strings.Join(parts, " · ")
}

// quotaResetPhrase is one reset countdown as a sentence fragment.
func quotaResetPhrase(label string, t time.Time) string {
	c := quotaCountdown(t)
	switch c {
	case "":
		return ""
	case "now":
		return label + " reset"
	}
	return label + " resets in " + c
}

// quotaAge renders how old the snapshot is ("" = never fetched).
func quotaAge(fetchedAt time.Time) string {
	if fetchedAt.IsZero() {
		return ""
	}
	age := time.Since(fetchedAt)
	if age < 5*time.Second {
		return "just now"
	}
	return quotaLeftText(age) + " ago"
}

// quotaCountdown is the compact reset clock: as short as the fact allows
// (>=24h → "3d4h" / "12d", else "2h10m", <1h → "45m"), "now" once the window
// refilled, "" when the endpoint reported no clock. A month-long bucket needs
// its days (a clock time alone would read as already past) and the sidebar has
// no room for both, so days win.
func quotaCountdown(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if d := time.Until(t); d > 0 {
		return quotaLeftText(d)
	}
	return "now"
}

// quotaLeftText formats a remaining duration in the countdown vocabulary.
func quotaLeftText(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	}
	return "<1m"
}

// quotaSideRow is one sidebar QUOTA line: text plus the style the renderer
// applies. Group reports the provider group line (label), so the header's
// provider count and the click budget can both be derived from the same slice
// that gets rendered — row math only needs the count (see recent.go).
type quotaSideRow struct {
	Text  string
	Style lipgloss.Style
	Group bool
}

// Sidebar panel geometry. The percent column is fixed-width because "100%" is
// the widest value and a shifting column would make the bars ragged; the bar
// is clamped to [3,10] because shorter than three cells reads as noise and
// wider than ten leaves no room for the reset clock at minSideW.
const (
	quotaPctW   = 4
	quotaBarMin = 3
	quotaBarMax = 10
)

// sideQuotaRows formats the sidebar QUOTA panel: a group line per provider
// plus one row per window (or one sentence row when the provider has no
// numbers). Empty until the first fetch lands, so the section costs zero rows
// in a session that never configured a quota-tracked provider — the same rule
// the COST section follows.
func (m Model) sideQuotaRows() []quotaSideRow {
	if len(m.quota) == 0 {
		return nil
	}
	inner := m.sideInnerW()
	if q, ok := m.currentQuota(); ok {
		return quotaPanelRows(q, inner)
	}
	if m.ModelProv != "" {
		return nil // active model's provider has no quota adapter: show nothing
	}
	var out []quotaSideRow
	for _, q := range m.quota {
		out = append(out, quotaGroupRows(q, inner)...)
	}
	return out
}

// currentQuota is the snapshot row of the provider the active model belongs
// to. The panel follows the model: pick an OpenCode Go model and the panel is
// OpenCode Go's, pick a Codex model and it is Codex's.
func (m Model) currentQuota() (pirpc.ProviderQuota, bool) {
	if m.ModelProv == "" {
		return pirpc.ProviderQuota{}, false
	}
	for _, q := range m.quota {
		if q.Provider == m.ModelProv {
			return q, true
		}
	}
	return pirpc.ProviderQuota{}, false
}

// sideQuotaTitle is the panel header: "OPENCODE GO QUOTA" for the active
// provider, the multi-provider count form only when the model is unknown.
func (m Model) sideQuotaTitle(rows []quotaSideRow) string {
	if q, ok := m.currentQuota(); ok {
		name := strings.TrimSpace(q.Label)
		if name == "" {
			name = q.Provider
		}
		t := strings.ToUpper(name) + " QUOTA"
		if q.Plan != "" {
			t += " · " + strings.ToUpper(q.Plan)
		}
		return t
	}
	return fmt.Sprintf("QUOTA (%d)", quotaGroupCount(rows))
}

// quotaPanelRows is the single-provider panel in the opencode-quota shape:
//
//	rolling ██████░░░░ 24% used  76% left  resets in 1h19m
//	✓ all windows have headroom
//
// Columns are dropped right-to-left as the sidebar narrows (resets, used) down
// to the compact `label bar N% left reset` row.
func quotaPanelRows(q pirpc.ProviderQuota, inner int) []quotaSideRow {
	if len(q.Windows) == 0 {
		if q.Err == "" && q.Note == "" && len(q.Budgets) == 0 {
			return nil
		}
		var out []quotaSideRow
		if len(q.Budgets) == 0 {
			text, style := q.Note, toolStyle
			if q.Err != "" {
				text, style = q.Err, errStyle
			}
			return []quotaSideRow{{Text: Short(text, inner), Style: style}}
		}
		for _, b := range q.Budgets {
			out = append(out, quotaSideRow{Text: Short(quotaBudgetRow(b, inner), inner), Style: quotaStyle(quotaBudgetPct(b), false)})
		}
		return out
	}
	labelW, resetW := 0, 0
	for _, w := range q.Windows {
		labelW = max(labelW, lipgloss.Width(w.Name), lipgloss.Width(w.ShortLabel()))
		resetW = max(resetW, lipgloss.Width(quotaCountdown(w.ResetsAt)))
	}
	// label + sp + bar + "  100% used" + "  100% left" + "  resets in " + clock
	const used, left = 2 + 9, 2 + 9
	reset := 2 + len("resets in ") + resetW
	bar := inner - labelW - 1 - used - left - reset
	full := bar >= 6
	if !full {
		return append(quotaWindowRows(q.Windows, inner), quotaStatusRow(q, inner))
	}
	bar = min(bar, 24)
	out := make([]quotaSideRow, 0, len(q.Windows)+1)
	for _, w := range q.Windows {
		name := w.Name
		if name == "" || quotaWindowNameUnknown(w) {
			name = w.ShortLabel()
		}
		var b strings.Builder
		b.WriteString(padRight(name, labelW) + " " + quotaBar(w, bar))
		if w.PercentKnown {
			b.WriteString(fmt.Sprintf("  %3d%% used  %3d%% left", w.UsedPct, w.PercentLeft()))
		} else {
			b.WriteString("       —           —  ")
		}
		if c := quotaCountdown(w.ResetsAt); c == "now" {
			b.WriteString("  reset")
		} else if c != "" {
			b.WriteString("  resets in " + c)
		}
		text := b.String()
		limited := !quotaStatusOK(w.Status)
		if limited && w.PercentKnown {
			text += " " + w.Status
		}
		out = append(out, quotaSideRow{Text: Short(text, inner), Style: quotaStyle(w.UsedPct, limited)})
	}
	return append(out, quotaStatusRow(q, inner))
}

// quotaWindowNameUnknown: provider-neutral names ("primary"/"secondary") mean
// nothing to a user, so those take the duration-derived label (5h, week).
func quotaWindowNameUnknown(w pirpc.QuotaWindow) bool {
	return w.Name == "primary" || w.Name == "secondary" || strings.Contains(w.Name, "_")
}

// quotaStatusRow is the verdict line under the windows.
func quotaStatusRow(q pirpc.ProviderQuota, inner int) quotaSideRow {
	worst, worstName := -1, ""
	for _, w := range q.Windows {
		if !quotaStatusOK(w.Status) {
			return quotaSideRow{Text: Short("✗ "+w.ShortLabel()+" limit reached", inner), Style: errStyle}
		}
		if w.PercentKnown && w.UsedPct > worst {
			worst, worstName = w.UsedPct, w.ShortLabel()
		}
	}
	switch {
	case worst >= 90:
		return quotaSideRow{Text: Short("⚠ "+worstName+" almost exhausted", inner), Style: errStyle}
	case worst >= 70:
		return quotaSideRow{Text: Short("⚠ "+worstName+" running low", inner), Style: warnStyle}
	}
	return quotaSideRow{Text: Short("✓ all windows have headroom", inner), Style: okStyle}
}

// quotaGroupCount is the provider count the section header shows. Providers
// with nothing to say contribute no group line, so this can never promise a
// row that is not there.
func quotaGroupCount(rows []quotaSideRow) int {
	n := 0
	for _, r := range rows {
		if r.Group {
			n++
		}
	}
	return n
}

// quotaGroupRows renders one provider group: the label line (so a
// two-provider panel never leaves a row unattributed) plus one row per window
// and per budget. A provider whose only state is a Note/Err prints the label
// and ONE sentence row, dim for a Note (a benign state the provider calls
// normal) and red for an Err — never a number.
func quotaGroupRows(q pirpc.ProviderQuota, inner int) []quotaSideRow {
	label := strings.TrimSpace(q.Label)
	if label == "" {
		label = q.Provider
	}
	if label == "" {
		return nil
	}
	if plan := strings.TrimSpace(q.Plan); plan != "" {
		label += " · " + plan
	}
	hasNumbers := len(q.Windows) > 0 || len(q.Budgets) > 0
	if !hasNumbers && q.Err == "" && q.Note == "" {
		// The endpoint answered with neither numbers nor a sentence: a bare
		// group line would read as "0% used", so the provider contributes
		// nothing at all.
		return nil
	}
	// A style built per call (not a package var) so a theme swap, which
	// rewrites cText, is picked up by the next render.
	group := lipgloss.NewStyle().Foreground(cText)
	out := []quotaSideRow{{Text: Short(label, inner), Style: group, Group: true}}
	if !hasNumbers {
		text, style := q.Note, toolStyle
		if q.Err != "" {
			text, style = q.Err, errStyle
		}
		return append(out, quotaSideRow{Text: Short(text, inner), Style: style})
	}
	if len(q.Windows) > 0 {
		out = append(out, quotaWindowRows(q.Windows, inner)...)
	}
	for _, b := range q.Budgets {
		// Clamped like the window rows: a five-figure limit plus a reset clock
		// cannot be allowed to widen the sidebar column.
		out = append(out, quotaSideRow{Text: Short(quotaBudgetRow(b, inner), inner), Style: quotaStyle(quotaBudgetPct(b), false)})
	}
	return out
}

// quotaWindowRows lays out one provider's windows. The bar width is computed
// once for the whole group so the bars line up (a per-row width would make
// every row a different length), and the space is conceded in the order the
// reference panel does: the reset clock goes first, because the bar is the
// point of the panel and a clock-less bar still answers "how much is left?".
func quotaWindowRows(ws []pirpc.QuotaWindow, inner int) []quotaSideRow {
	labels := make([]string, len(ws))
	resets := make([]string, len(ws))
	labelW, resetW := 0, 0
	for i, w := range ws {
		labels[i] = w.ShortLabel()
		resets[i] = quotaCountdown(w.ResetsAt)
		labelW = max(labelW, lipgloss.Width(labels[i]))
		resetW = max(resetW, lipgloss.Width(resets[i]))
	}
	bar := inner - labelW - 1 - 2 - quotaPctW - 2 - resetW
	if bar < quotaBarMin {
		resetW = 0
		bar = inner - labelW - 1 - 2 - quotaPctW
	}
	if bar < quotaBarMin {
		bar = 0 // no room for a readable bar: keep the label + percent only
	}
	bar = min(bar, quotaBarMax)

	out := make([]quotaSideRow, 0, len(ws))
	for i, w := range ws {
		limited := !quotaStatusOK(w.Status)
		text := quotaWindowRow(labels[i], labelW, w, bar, resets[i], resetW)
		if limited && w.PercentKnown {
			// The provider's own limit word outranks the clock, so it is
			// appended and then clamped with the rest of the row: an
			// over-long bucket name or status must never widen the column.
			text += " " + w.Status
		}
		out = append(out, quotaSideRow{Text: Short(text, inner), Style: quotaStyle(w.UsedPct, limited)})
	}
	return out
}

// quotaWindowRow is one `name [bar] percent% reset` row.
func quotaWindowRow(label string, labelW int, w pirpc.QuotaWindow, bar int, reset string, resetW int) string {
	var b strings.Builder
	b.WriteString(padLeft(label, labelW))
	b.WriteString(" ")
	if bar > 0 {
		b.WriteString(quotaBar(w, bar))
		// One literal space plus the right-aligned percent column: the column
		// itself supplies the rest of the two-cell gap, so a 3-digit percent
		// keeps its right edge and a 4-digit one uses the last spare cell.
		b.WriteString(" ")
	}
	if w.PercentKnown {
		b.WriteString(padLeft(fmt.Sprintf("%d%%", w.PercentLeft()), quotaPctW))
	} else {
		// A dash is not a number: the percent cell stays aligned without
		// claiming a value the payload never carried.
		b.WriteString(padLeft("—", quotaPctW))
	}
	if reset != "" && resetW > 0 {
		b.WriteString("  ")
		b.WriteString(padLeft(reset, resetW))
	}
	return b.String()
}

// quotaBar draws the bar filled by the percent LEFT, with the even-use mark
// where a linear burn would be right now. An unknown percent draws no fill at
// all instead of a confident empty (0%-left) bar.
func quotaBar(w pirpc.QuotaWindow, width int) string {
	if !w.PercentKnown {
		return strings.Repeat(" ", width)
	}
	fill := int(math.Round(float64(w.PercentLeft()) / 100 * float64(width)))
	fill = min(max(fill, 0), width)
	cells := make([]rune, width)
	for i := range cells {
		if i < fill {
			cells[i] = '█'
		} else {
			cells[i] = '░'
		}
	}
	if pos, ok := quotaEvenMark(w, width); ok {
		cells[pos] = '┃'
	}
	return string(cells)
}

// quotaEvenMark is where a strictly linear burn would sit inside the bar: the
// share of the window still ahead of us, mapped onto the bar. It needs BOTH a
// verified window duration and a reset clock — without either, any position is
// a guess, so the mark is skipped rather than approximated.
func quotaEvenMark(w pirpc.QuotaWindow, width int) (int, bool) {
	if w.WindowSecs <= 0 || w.ResetsAt.IsZero() || width <= 0 {
		return 0, false
	}
	left := time.Until(w.ResetsAt)
	if left <= 0 {
		return 0, true // the window already refilled: linear burn is at 0% left
	}
	if d := time.Duration(w.WindowSecs) * time.Second; left < d {
		pos := int(math.Round(float64(width) * float64(left) / float64(d)))
		return min(max(pos, 0), width-1), true
	}
	return width - 1, true
}

// quotaBudgetText is Zen's money row vocabulary: spent against the limit.
func quotaBudgetText(b pirpc.QuotaBudget) string {
	if b.LimitUSD > 0 {
		return "$" + quotaUSD(b.SpentUSD) + " / $" + quotaUSDLimit(b.LimitUSD)
	}
	return "$" + quotaUSD(b.SpentUSD) + " spent"
}

// quotaBudgetRow lays the money row out as `<dollars> ... <reset>`, the reset
// right-aligned against inner so it lines up with the window clocks' column
// when there is no window row above it. It carries no bar: dollars are an
// absolute amount, not a share.
func quotaBudgetRow(b pirpc.QuotaBudget, inner int) string {
	text := "  " + quotaBudgetText(b)
	reset := quotaCountdown(b.ResetsAt)
	if reset == "" {
		return text
	}
	gap := inner - lipgloss.Width(text) - lipgloss.Width(reset)
	if gap < 2 {
		return text + "  " + reset
	}
	return text + strings.Repeat(" ", gap) + reset
}

// quotaUSD formats a dollar amount with cents.
func quotaUSD(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

// quotaUSDLimit drops the ".00" on a whole limit: "… / $100" is what a budget
// is called in the console, and the trailing zeros only cost sidebar cells.
func quotaUSDLimit(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// padLeft/padRight are the panel's column helpers. They pad on display cells,
// so a wide rune in a provider name cannot shift the columns.
func padLeft(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

func padRight(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// quotaBudgetPct is how much of a budget is spent (0 when there is no limit).
func quotaBudgetPct(b pirpc.QuotaBudget) int {
	if b.LimitUSD <= 0 {
		return 0
	}
	return int(math.Round(100 * b.SpentUSD / b.LimitUSD))
}

// quotaStyle colours a quota row by how close it is to the wall: green
// under 70, yellow 70-89, red at 90+ or for a bucket the provider already
// flagged as limited. It reads the USED percent: colour is about danger, and
// danger grows with what is spent even though the numbers read as "left".
func quotaStyle(pct int, limited bool) lipgloss.Style {
	switch {
	case limited || pct >= 90:
		return errStyle
	case pct >= 70:
		return warnStyle
	}
	return okStyle
}

// quotaStatusOK reports the provider's "nothing to see here" status. The
// console omits the field for an ok bucket, which is why empty counts as ok.
func quotaStatusOK(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "" || s == "ok"
}
