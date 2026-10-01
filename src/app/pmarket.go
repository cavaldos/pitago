package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Marketplace: search and install new pi packages from the npm registry
// straight from the settings hub. Pi publishes no package index of its own;
// the "market" is npm filtered by the pi-package keyword every pi extension
// ships (verified across the installed packages), plus whatever free text
// the user types as a remote npm search.

const (
	marketTTL     = 5 * time.Minute
	marketTimeout = 15 * time.Second
	// One page per fetch: fast enough to keep the hub snappy, more pages
	// load on demand via the trailing "load more" row.
	marketPageSize = 100
	// Debounce for the remote search: long enough that typing "lens"
	// costs one request, short enough to still feel live.
	marketDebounce = 280 * time.Millisecond
	// npm installs are slow: same budget class as binary updates (120s).
	pluginChangeTimeout = 180 * time.Second
	// pluginConfirmWindow bounds the install/remove auth gate: the first
	// Enter/Delete arms, the second on the same spec within the window runs.
	pluginConfirmWindow = 10 * time.Second
	// Star lookups: ungh.cc is unauthenticated but slow sometimes, so keep
	// the budget tight and the batch small — the count is decoration.
	starsTimeout  = 4 * time.Second
	starsBatch    = 12
	starsParallel = 4
	// Per-query cache cap: browsing many queries must not grow forever.
	marketPagesMax = 24
	// How deep the "most starred first" browse ordering resolves stars.
	// npm search has no star field, so each package costs one ungh
	// request: only the head of the page is worth reordering, the tail
	// keeps registry order below it.
	marketSortDepth = 40
	// The plugin spinner ticks on its own clock: the pet loop only runs
	// when the pet section is on screen and would freeze the animation.
	pluginTickInterval = 150 * time.Millisecond
	// plugNoteResultWindow is how long the "✓ removed npm:x" / "✗ failed"
	// line stays inside the hub. The chat notice is hidden behind the
	// modal dialog, so without this the user closes the hub to find out
	// whether the op worked.
	plugNoteResultWindow = 6 * time.Second
)

// ConfirmPluginOp is the install/remove authorization gate: first press arms
// (status prompts, returns false = no exec), second press on the same
// action+spec within the window confirms (returns true). Any other spec
// re-arms instead of executing.
func (m *Model) ConfirmPluginOp(action, spec string) bool {
	if m.plugAction == action && m.plugSpec == spec && !m.plugAt.IsZero() &&
		time.Since(m.plugAt) < pluginConfirmWindow {
		m.plugAction, m.plugSpec, m.plugAt = "", "", time.Time{}
		m.clearPlugNote() // the prompt is answered
		return true
	}
	m.plugAction, m.plugSpec, m.plugAt = action, spec, time.Now()
	// m.Status is invisible while the hub is up (it paints on the input
	// border behind the dialog), so the arming prompt has to live inside
	// the hub too — otherwise the first Delete looks like a dead key.
	m.Status = fmt.Sprintf("press again to confirm %s %s", action, spec)
	m.setPlugNote(pluginConfirmHint(action)+" again to "+action+" "+spec+
		" — no second press in "+fmt.Sprintf("%.0fs", pluginConfirmWindow.Seconds())+
		" means no "+action, pluginConfirmWindow)
	m.Refresh()
	return false
}

// pluginConfirmHint names the key that confirms an armed op (the hub
// shows the gate prompt, so it has to say which key to hit).
func pluginConfirmHint(action string) string {
	if action == "remove" {
		return "Delete"
	}
	return "Enter"
}

// setPlugNote shows a short-lived line inside the hub and rebuilds its
// rows: psecRows reads plugNote, so a repaint alone would not show it.
func (m *Model) setPlugNote(note string, d time.Duration) {
	m.plugNote, m.plugNoteHide = note, time.Now().Add(d)
	m.reloadHubRows("")
	m.Refresh()
}

// clearPlugNote drops the hub note (no-op when none is showing).
func (m *Model) clearPlugNote() {
	if m.plugNote == "" {
		return
	}
	m.plugNote, m.plugNoteHide = "", time.Time{}
	m.reloadHubRows("")
	m.Refresh()
}

// plugNoteLine is the note when it is still within its window ("" when
// it lapsed or there never was one).
func (m Model) plugNoteLine() string {
	if m.plugNote == "" || time.Now().After(m.plugNoteHide) {
		return ""
	}
	return m.plugNote
}

// marketQuery is the normalized search text of one registry page (""
// = the plain pi-package browse page).
func marketQuery(q string) string { return strings.TrimSpace(q) }

// marketURL builds one registry search page (rank order, zero-based). The
// free-text query is appended to the keyword filter — npm ranks the keyword
// matches first, which is exactly the pi-package list we want.
func marketURL(query string, from int) string {
	base := fmt.Sprintf("https://registry.npmjs.org/-/v1/search?text=keywords:pi-package&size=%d&from=%d",
		marketPageSize, from)
	if q := marketQuery(query); q != "" {
		base = fmt.Sprintf("https://registry.npmjs.org/-/v1/search?text=keywords:pi-package+%s&size=%d&from=%d",
			url.QueryEscape(q), marketPageSize, from)
	}
	return base
}

// MarketEntry is one installable pi package from the npm registry.
type MarketEntry struct {
	Name       string
	Version    string
	Desc       string
	Repo       string // "owner/repo" from the registry links, "" when absent
	Stars      int    // GitHub stars (0 until StarsKnown)
	StarsKnown bool   // stars resolved (or looked up and found nowhere)
	Weekly     int    // npm weekly downloads
}

// marketPage is one cached registry page: the entries, whether the registry
// had more to give, and when it was fetched.
type marketPage struct {
	entries []MarketEntry
	more    bool
	at      time.Time
}

var (
	marketPages         = map[string]marketPage{} // query → page ("" = browse)
	marketMore          bool                      // visible list has another page
	marketInflight      bool
	marketInflightQuery string
	marketStars         = map[string]int{}  // repo → stars
	marketStarsBad      = map[string]bool{} // repo: no stars upstream, never retry
	marketStarsBusy     = map[string]bool{} // repo: lookup in flight
	marketSorted        = map[string]bool{} // query → browse page already ordered
)

// marketCached returns a cached page for query plus whether it is still
// fresh (render path never touches the network).
func marketCached(query string) (entries []MarketEntry, more bool, fresh bool) {
	p, ok := marketPages[marketQuery(query)]
	if !ok {
		return nil, false, false
	}
	return p.entries, p.more, time.Since(p.at) < marketTTL
}

// marketStore caches one page under query, bounding the map so a long
// browsing session cannot grow it without limit.
func marketStore(query string, entries []MarketEntry, more bool) {
	q := marketQuery(query)
	if _, seen := marketPages[q]; !seen && len(marketPages) >= marketPagesMax {
		marketPages = map[string]marketPage{}
	}
	marketPages[q] = marketPage{entries: entries, more: more, at: time.Now()}
}

// marketFresh reports whether the plain browse page is still usable (the
// first visit to the Marketplace section keys off this).
func marketFresh() bool {
	_, _, fresh := marketCached("")
	return fresh
}

// linkURL is npm's repository/homepage link: either a bare string or an
// object with a "url" field (both shapes ship in the wild).
type linkURL string

func (l *linkURL) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*l = linkURL(s)
		return nil
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil // not a link we understand: no repo, no crash
	}
	*l = linkURL(obj.URL)
	return nil
}

// npmSearch mirrors the registry search response (decoded fields only).
// Note: the top-level "total" is meaningless (npm returns a constant for
// every keyword query), so paging uses len(objects) instead.
type npmSearch struct {
	Objects []struct {
		Package struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Keywords    []string `json:"keywords"`
			Links       struct {
				Repository linkURL `json:"repository"`
				Homepage   linkURL `json:"homepage"`
			} `json:"links"`
		} `json:"package"`
		Downloads struct {
			Weekly int `json:"weekly"`
		} `json:"downloads"`
	} `json:"objects"`
}

// parseMarket decodes one registry search page, keeping rank order and
// dropping entries without the pi-package keyword. Returned counts the raw
// objects before filtering — that is the honest paging signal.
func parseMarket(raw []byte) (entries []MarketEntry, returned int, err error) {
	var res npmSearch
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, 0, err
	}
	for _, o := range res.Objects {
		p := o.Package
		if strings.TrimSpace(p.Name) == "" || !hasKeyword(p.Keywords, "pi-package") {
			continue
		}
		entries = append(entries, MarketEntry{Name: p.Name, Version: p.Version,
			Desc:   strings.Join(strings.Fields(p.Description), " "),
			Repo:   ghRepo(string(p.Links.Repository), string(p.Links.Homepage)),
			Weekly: o.Downloads.Weekly})
	}
	return entries, len(res.Objects), nil
}

func hasKeyword(kw []string, want string) bool {
	for _, k := range kw {
		if k == want {
			return true
		}
	}
	return false
}

// ghRepo extracts "owner/repo" from the registry's repository/homepage
// links. Handles the git+https, https, git@ and "github:" shorthands npm
// packages use; anything not on github.com yields "" (no star lookup).
func ghRepo(repository, homepage string) string {
	for _, raw := range []string{repository, homepage} {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		s = strings.TrimPrefix(s, "git+")
		switch {
		case strings.HasPrefix(s, "git@github.com:"):
			s = strings.TrimPrefix(s, "git@github.com:")
		case strings.HasPrefix(s, "github:"):
			s = strings.TrimPrefix(s, "github:")
		case strings.Contains(s, "github.com/"):
			s = s[strings.Index(s, "github.com/")+len("github.com/"):]
		default:
			continue
		}
		// drop query/fragment/anchor noise ("...#readme", "...?foo")
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		s = strings.Trim(s, "/")
		s = strings.TrimSuffix(s, ".git")
		s = strings.Trim(s, "/")
		if owner, repo, ok := strings.Cut(s, "/"); ok &&
			owner != "" && repo != "" && !strings.Contains(repo, "/") {
			return owner + "/" + repo
		}
	}
	return ""
}

// fetchMarketEntries loads one registry page for query. "More" is simply
// "the registry had a full page of raw objects" — the real total is junk.
func fetchMarketEntries(query string, from int) (entries []MarketEntry, more bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), marketTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", marketURL(query, from), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("registry: %s", res.Status)
	}
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	entries, returned, err := parseMarket([]byte(b.String()))
	if err != nil {
		return nil, false, err
	}
	return entries, returned >= marketPageSize, nil
}

// MarketMsg carries one marketplace page back to the UI (Append merges a
// "load more" page into the loaded list for the same query).
type MarketMsg struct {
	Query   string
	Entries []MarketEntry
	More    bool
	Append  bool
	Total   int // unused: npm's total is a constant
	Err     error
}

// MarketSearchMsg is the debounced "the user stopped typing" signal.
type MarketSearchMsg struct {
	Query string
}

// marketSearchTick arms the debounce after a filter keystroke. Only the
// Marketplace section searches remotely; other sections return nil and
// keep their local-filter behaviour.
func (m Model) marketSearchTick() tea.Cmd {
	d := m.hubDialog()
	if d == nil || d.CurPsec() != PsecMarket {
		return nil
	}
	q := marketQuery(d.Filter)
	return tea.Tick(marketDebounce, func(time.Time) tea.Msg {
		return MarketSearchMsg{Query: q}
	})
}

// marketSearchCmd turns a debounced keystroke into at most one fetch: the
// hub must still be on the marketplace with the same text, and the page
// must not already be cached or in flight.
func (m Model) marketSearchCmd(query string) tea.Cmd {
	d := m.hubDialog()
	if d == nil || d.CurPsec() != PsecMarket {
		return nil
	}
	q := marketQuery(query)
	if q != marketQuery(d.Filter) {
		return nil // user kept typing: this tick is stale
	}
	if _, _, fresh := marketCached(q); fresh {
		return nil
	}
	if marketInflight && marketInflightQuery == q {
		return nil
	}
	return m.fetchMarketQueryCmd(q)
}

// fetchMarketQueryCmd searches npm for query in the background (hub stays
// open; the result arrives as MarketMsg and reloads the rows in place).
func (m Model) fetchMarketQueryCmd(query string) tea.Cmd {
	q := marketQuery(query)
	marketInflight, marketInflightQuery = true, q
	if q == "" {
		m.Status = "loading marketplace…"
	} else {
		m.Status = "searching npm for " + q + "…"
	}
	m.Refresh()
	return func() tea.Msg {
		entries, more, err := fetchMarketEntries(q, 0)
		return MarketMsg{Query: q, Entries: entries, More: more, Err: err}
	}
}

// fetchMarketCmd loads the first (browse) market page in the background.
func (m Model) fetchMarketCmd() tea.Cmd { return m.fetchMarketQueryCmd("") }

// FetchMarketPageCmd loads the page after `loaded` entries for query.
// Exported: Enter actions live in src/builtin (see Confirmers).
func (m Model) FetchMarketPageCmd(query string, loaded int) tea.Cmd {
	q := marketQuery(query)
	marketInflight, marketInflightQuery = true, q
	m.Status = "loading more plugins…"
	m.Refresh()
	return func() tea.Msg {
		entries, more, err := fetchMarketEntries(q, loaded)
		return MarketMsg{Query: q, Entries: entries, More: more, Append: true, Err: err}
	}
}

// MarketQuery is the committed marketplace search for the open hub (the
// trimmed filter text; "" while another section is selected).
func (m *Model) MarketQuery() string {
	d := m.hubDialog()
	if d == nil || d.CurPsec() != PsecMarket {
		return ""
	}
	return marketQuery(d.Filter)
}

// MarketStarsMsg reports resolved GitHub star counts (repo → stars) plus
// the repos this batch actually asked for, so a miss is only ever judged
// against its own request (not some other in-flight batch).
type MarketStarsMsg struct {
	Stars map[string]int
	Asked []string
}

// MarketSortMsg reports the star counts used to order the browse page
// head. Query is the page it belongs to (only "" is ever sorted).
type MarketSortMsg struct {
	Query string
	Stars map[string]int
	Asked []string
}

// sortMarketVisible reorders the shown market list most-starred-first and
// keeps the highlight on the same package. Rows map to m.Market by index,
// so every reorder must go through here: a bare m.Market = sorted would
// leave the hub pointing at the wrong package (wrong install, wrong
// details). Callers patch the star counts first.
func (m *Model) sortMarketVisible() {
	sel := ""
	if d := m.hubDialog(); d != nil {
		if ri := psecCursor(d); ri >= 0 && ri < len(m.Market) {
			sel = m.Market[ri].Name
		}
	}
	m.Market = sortMarketStars(m.Market)
	// "" keeps the current section: the sort already knows the hub is on
	// the marketplace, and a reorder must never yank it elsewhere
	m.reloadHubRows("")
	if d := m.hubDialog(); d != nil && sel != "" {
		for fi, ri := range d.FIdx {
			if ri < len(m.Market) && m.Market[ri].Name == sel {
				d.Cursor = fi
				break
			}
		}
	}
	m.Refresh()
}

// sortMarketStars puts resolved entries first, most starred first, and
// leaves everything unresolved behind in registry order (stable, so equal
// counts keep the npm ranking).
func sortMarketStars(entries []MarketEntry) []MarketEntry {
	out := append([]MarketEntry{}, entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StarsKnown != out[j].StarsKnown {
			return out[i].StarsKnown
		}
		if !out[i].StarsKnown {
			return false
		}
		return out[i].Stars > out[j].Stars
	})
	return out
}

// marketSortCmd resolves stars for the head of the browse page so it can
// be ordered most-starred-first. Nil for a typed search (npm relevance
// order is the point there), for an already-ordered page, or when every
// repo in the head is resolved — that last case still latches the page
// sorted and reorders in place, so the head is never re-scanned.
func (m *Model) marketSortCmd(query string) tea.Cmd {
	q := marketQuery(query)
	if q != "" {
		return nil // a typed search keeps npm relevance order
	}
	d := m.hubDialog()
	if d == nil || d.CurPsec() != PsecMarket || m.MarketQuery() != "" {
		return nil
	}
	if marketSorted[q] {
		return nil
	}
	var repos []string
	seen := map[string]bool{}
	for i, e := range m.Market {
		if i >= marketSortDepth {
			break
		}
		_, resolved := marketStars[e.Repo]
		if e.Repo == "" || e.StarsKnown || resolved || marketStarsBad[e.Repo] ||
			marketStarsBusy[e.Repo] || seen[e.Repo] {
			continue
		}
		seen[e.Repo] = true
		repos = append(repos, e.Repo)
	}
	if len(repos) == 0 {
		if len(m.Market) == 0 {
			return nil // nothing loaded yet: sort after the page lands
		}
		// a head repo another batch already claimed means the counts are
		// still coming: leave the page unsorted and try again next visit
		for i, e := range m.Market {
			if i >= marketSortDepth {
				break
			}
			if e.Repo != "" && marketStarsBusy[e.Repo] {
				return nil
			}
		}
		// nothing left to learn: the head is as ordered as it will get
		marketSorted[q] = true
		m.sortMarketVisible()
		return nil
	}
	// latch busy on the UI goroutine: the cmd runs off it and must not
	// write a map the UI reads
	for _, repo := range repos {
		marketStarsBusy[repo] = true
	}
	m.Status = "sorting by stars…"
	m.Refresh()
	return func() tea.Msg {
		out := map[string]int{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, starsParallel)
		for _, repo := range repos {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				n, err := fetchRepoStars(repo)
				if err == nil {
					mu.Lock()
					out[repo] = n
					mu.Unlock()
				}
			}(repo)
		}
		wg.Wait()
		return MarketSortMsg{Query: q, Stars: out, Asked: repos}
	}
}

// fetchRepoStars reads one repo's star count from ungh.cc (no auth, the
// GitHub API is rate-limited from this host).
func fetchRepoStars(repo string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), starsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://ungh.cc/repos/"+repo, nil)
	if err != nil {
		return 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ungh: %s", res.Status)
	}
	var out struct {
		Repo struct {
			Stars int `json:"stars"`
		} `json:"repo"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Repo.Stars, nil
}

// hydrateMarketStarsCmd fetches star counts for the repos in the visible
// window that are not resolved yet. Never fires from render.
func (m Model) hydrateMarketStarsCmd(d *Dialog) tea.Cmd {
	if d == nil || d.CurPsec() != PsecMarket || len(d.FIdx) == 0 {
		return nil
	}
	start, end, _, _ := fixedWin(d.Cursor, len(d.FIdx), hubWindow(&m))
	var repos []string
	seen := map[string]bool{}
	for fi := start; fi < end && fi < len(d.FIdx); fi++ {
		ri := d.FIdx[fi]
		if ri < 0 || ri >= len(m.Market) {
			continue
		}
		e := m.Market[ri]
		_, resolved := marketStars[e.Repo]
		// presence, not value: a repo with a real 0 stars is resolved
		if e.Repo == "" || e.StarsKnown || resolved ||
			marketStarsBad[e.Repo] || marketStarsBusy[e.Repo] || seen[e.Repo] {
			continue
		}
		seen[e.Repo] = true
		repos = append(repos, e.Repo)
		if len(repos) >= starsBatch {
			break
		}
	}
	if len(repos) == 0 {
		return nil
	}
	// latch busy here, on the UI goroutine: the cmd below runs off it and
	// must not write a map the UI reads.
	for _, repo := range repos {
		marketStarsBusy[repo] = true
	}
	return func() tea.Msg {
		out := map[string]int{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, starsParallel)
		for _, repo := range repos {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				n, err := fetchRepoStars(repo)
				if err == nil {
					mu.Lock()
					out[repo] = n
					mu.Unlock()
				}
			}(repo)
		}
		wg.Wait()
		return MarketStarsMsg{Stars: out, Asked: repos}
	}
}

// fmtStars renders a count the way the rows do: "1,565" / "12.3k" / "1.2M".
func fmtStars(n int) string {
	if n < 0 {
		return "—"
	}
	switch {
	case n < 10000:
		return fmtComma(n)
	case n < 1000000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	default:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1000000)) + "M"
	}
}

// trimZero drops a trailing ".0" so 12000 reads "12k", not "12.0k".
func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

// PluginChangeMsg reports a finished pi install/remove.
type PluginChangeMsg struct {
	Action string // install | remove
	Spec   string
	Out    string // one-line tail of pi's output (for error toasts)
	Err    error
}

// piBin resolves the pi binary the same way Spawn does (explicit option,
// $PI_BIN, then PATH).
func (m Model) piBin() string {
	if m.spawnOpts.Bin != "" {
		return m.spawnOpts.Bin
	}
	if b := os.Getenv("PI_BIN"); b != "" {
		return b
	}
	return "pi"
}

// PiBin is piBin for src/builtin, whose /mcp rows need to run
// `pi mcp list --json` themselves (the row building is pi-parity code
// and cannot live in app, which may not import pirpc's callers).
func (m Model) PiBin() string { return m.piBin() }

// validPluginAction reports whether action is a known pi package op.
func validPluginAction(action string) bool {
	return action == "install" || action == "remove"
}

// validPluginSpec whitelists pi package specs (no shell metachars, no
// whitespace, known source prefix). exec passes args without a shell, but
// `pi install` still interprets the spec — arbitrary strings must not reach it.
func validPluginSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" || len(spec) > 256 {
		return false
	}
	if strings.ContainsAny(spec, " \t\n\r\v\f;&|<>$`'\"\\(){}!*#?~") {
		return false
	}
	if strings.HasPrefix(spec, "npm:") {
		name := strings.TrimPrefix(spec, "npm:")
		if name == "" || strings.Contains(name, ":") {
			return false
		}
		// npm name (scoped or not): lower, dots/underscores/dashes/slashes
		for _, r := range name {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
				r == '-' || r == '.' || r == '_' || r == '/' || r == '@' {
				continue
			}
			return false
		}
		return true
	}
	if strings.HasPrefix(spec, "git:") {
		rest := strings.TrimPrefix(spec, "git:")
		return rest != "" && !strings.Contains(rest, ":")
	}
	return false
}

// PluginTickMsg drives the running-plugin spinner frame.
type PluginTickMsg struct{}

// pluginTickCmd is the spinner's own timer: one tick per frame, chained
// by the update loop until PluginChangeMsg clears the busy state.
func pluginTickCmd() tea.Cmd {
	return tea.Tick(pluginTickInterval, func(time.Time) tea.Msg { return PluginTickMsg{} })
}

// PluginBusy reports the running plugin op ("" when idle). Exported:
// src/builtin gates duplicate installs on it.
func (m Model) PluginBusy() (action, spec string) { return m.plugBusyAction, m.plugBusySpec }

// pluginBusyFrame is the current spinner glyph.
func (m Model) pluginBusyFrame() string { return spinFrame(m.plugBusyFrame) }

// pluginBusyElapsed is the running op's wall time ("12s"), clamped at 0
// so a zero/odd clock never renders a negative age.
func (m Model) pluginBusyElapsed() string {
	if m.plugBusyAt.IsZero() {
		return "0s"
	}
	d := time.Since(m.plugBusyAt)
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

// pluginBusyLabel is the header line while an op runs ("⠹ installing
// npm:pi-web-access 12s").
func (m Model) pluginBusyLabel(action, spec string) string {
	verb := "installing"
	if action == "remove" {
		verb = "removing"
	}
	return m.pluginBusyFrame() + " " + verb + " " + spec + " " + m.pluginBusyElapsed()
}

// pluginBusyRowDesc is the running op's row description
// ("installing… 12s") — shared by the market/plugin rows and DETAILS.
func (m Model) pluginBusyRowDesc() string {
	verb := "installing"
	if m.plugBusyAction == "remove" {
		verb = "removing"
	}
	return verb + "… " + m.pluginBusyElapsed()
}

// PluginBusyMsg is the status shown when the user tries to start another
// op while one is running: it names the op that is actually running, so
// a remove in flight never reads "installing" ("" when nothing runs).
func (m Model) PluginBusyMsg() string {
	if m.plugBusyAction == "" {
		return ""
	}
	return m.pluginBusyLabel(m.plugBusyAction, m.plugBusySpec) + " — wait for it"
}

// StartPluginOp arms the spinner and launches `pi install|remove`. The
// returned cmd runs the op and the spinner tick side by side; the caller
// has already passed ConfirmPluginOp (this is not the auth gate).
func (m *Model) StartPluginOp(action, spec string) tea.Cmd {
	if !validPluginAction(action) || !validPluginSpec(spec) {
		return func() tea.Msg {
			return PluginChangeMsg{Action: action, Spec: spec,
				Err: fmt.Errorf("refused invalid plugin spec")}
		}
	}
	m.plugBusyAction, m.plugBusySpec, m.plugBusyAt, m.plugBusyFrame = action, spec, time.Now(), 0
	verb := "installing"
	if action == "remove" {
		verb = "removing"
	}
	m.Status = verb + " " + spec + "…"
	// The spinner lives in the rows and the section header, and rows are
	// only recomputed by LoadPsecRows — a bare Refresh() leaves the hub
	// showing the old text, which is why a remove looked completely
	// silent. Rebuild the current section in place (no focus steal).
	m.clearPlugNote()
	m.reloadHubRows("")
	m.Refresh()
	return tea.Batch(m.ChangePluginCmd(action, spec), pluginTickCmd())
}

// ChangePluginCmd runs `pi install|remove <spec>` in the background (hub
// stays open; PluginChangeMsg refreshes the lists when it lands).
// Exported: Enter actions live in src/builtin (see Confirmers).
func (m Model) ChangePluginCmd(action, spec string) tea.Cmd {
	bin := m.piBin()
	if !validPluginAction(action) || !validPluginSpec(spec) {
		return func() tea.Msg {
			return PluginChangeMsg{Action: action, Spec: spec,
				Err: fmt.Errorf("refused invalid plugin spec")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pluginChangeTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, action, spec).CombinedOutput()
		return PluginChangeMsg{Action: action, Spec: spec, Out: shortOut(out), Err: err}
	}
}

// shortOut collapses command output to one short line for toasts.
func shortOut(out []byte) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(string(out))), " ")
	const maxOut = 160
	if r := []rune(s); len(r) > maxOut {
		return string(r[:maxOut]) + "…"
	}
	return s
}

// marketInstalled reports whether a market package is already installed
// (settings.json spec suffix matches the registry name).
func marketInstalled(m *Model, name string) bool {
	for _, p := range m.Plugins {
		if pluginName(p.Spec) == name {
			return true
		}
	}
	return false
}
