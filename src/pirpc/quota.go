package pirpc

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider quota tracking (increment 1: OpenCode Go + OpenCode Zen).
//
// pi has no quota RPC and no quota concept at all: usage lives on each
// provider's own console API, so pitago asks those endpoints directly and
// keeps the answer in a short-lived cache. Only providers with a verified,
// documented-enough endpoint are listed; every other id falls through
// FetchProviderQuota's unknown arm so the UI can say "not supported yet"
// instead of printing numbers nobody checked.
//
// Increment 2 (OpenAI Codex, an OAuth subscription) is one more adapter
// function plus one more entry in quotaProviders — the seam is deliberate,
// nothing else here has to move.
//
// Everything in this file is READ-ONLY on pi's auth.json. That file is
// BOM-sensitive and raw-byte-preserving (see piauth.go): a quota refresh
// must never rewrite it, because a bad write drops the user's OAuth tokens.

// QuotaWindow is one usage bucket of a provider (the 5h/week/month windows
// of OpenCode Go): UsedPct is 0-100 used, Status is the provider's own word
// ("ok", "rate-limited"), ResetsAt the moment the bucket refills (zero when
// the endpoint omitted it).
//
// PercentKnown exists because 0 is a MEANINGFUL percent: a payload with a
// missing/null/unparsable percent used to arrive as UsedPct 0 and paint a
// confident green 0%-used bar for a number nobody sent. A window whose
// percent is unknown keeps PercentKnown=false and the UI omits the number
// instead of inventing one.
//
// WindowSecs is how long the bucket lasts, in seconds (0 = unknown). It is
// only set where the duration is VERIFIED (see quotaWindowSecs); the even-use
// mark in the sidebar needs it and a guessed duration would draw a made-up
// line.
type QuotaWindow struct {
	Name         string
	Label        string // optional display label; when set it overrides ShortLabel's derivation
	UsedPct      int
	PercentKnown bool
	Status       string
	ResetsAt     time.Time
	WindowSecs   int
}

// PercentLeft is the remaining share of the bucket, 0-100. The panel fills
// its bar with what is LEFT (that is what opencode-quota shows and what a
// user actually spends), so the conversion lives next to the field and every
// surface agrees on the clamp.
func (w QuotaWindow) PercentLeft() int {
	p := 100 - w.UsedPct
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// ShortLabel is the compact row label: an explicit Label when the adapter set
// one (e.g. a code-review or model-specific codex limit), else the endpoint's
// own bucket name where we know it (rolling→5h, weekly→week, monthly→month),
// else a label derived from the window duration when that is known (codex-style
// 300min→5h, 10080min→week, 43200min→month, otherwise Nh), else the raw name.
// Inventing a nicer name for an unknown bucket would hide that the endpoint
// changed.
func (w QuotaWindow) ShortLabel() string {
	if w.Label != "" {
		return w.Label
	}
	if lbl, ok := quotaWindowLabel[w.Name]; ok {
		return lbl
	}
	if w.WindowSecs > 0 {
		return quotaDurationLabel(w.WindowSecs)
	}
	return w.Name
}

// QuotaBudget is one pay-as-you-go budget (OpenCode Zen): dollars spent
// against the limit, and when the rule resets.
type QuotaBudget struct {
	LimitUSD float64
	SpentUSD float64
	ResetsAt time.Time
}

// ProviderQuota is one provider's group in the dialog and the sidebar.
//
// Err is a complete human sentence for a FAILED lookup (401, 402, 5xx,
// network); Note is the same shape for a BENIGN state the provider itself
// reports as normal (OpenCode Zen's permission-gated budget endpoint answers
// 403 for a healthy key). Both carry no numbers: when either is set on a
// provider with nothing else to show, Windows/Budgets must stay empty so a
// failed lookup can never render as "0%". The split exists because painting
// a tolerated 403 red contradicts the fetch code's own comment.
//
// Plan is the provider's plan name when an adapter learns one (no adapter
// reports one yet). The row builder appends it to the group line, so the day
// a response carries the plan the UI needs no new plumbing.
type ProviderQuota struct {
	Provider  string
	Label     string
	Plan      string
	Windows   []QuotaWindow
	Budgets   []QuotaBudget
	Err       string
	Note      string
	FetchedAt time.Time
}

// quotaProviders is the supported set and its display/fetch order: stable so
// the rows never shuffle between refreshes.
var quotaProviders = []string{"opencode-go", "opencode", "openai-codex", "anthropic", "openrouter"}

// quotaEndpoints are package vars, not consts, so tests can point them at an
// httptest server. No test may touch the real provider APIs, and redirecting
// through t.Cleanup keeps a leftover URL from leaking into the next test.
//
// opencode-go: GET /inference/go/v1/usage with a Bearer key returns
// {"usage":{"rolling":{status,percent,resetsAt},"weekly":{…},"monthly":{…}}}.
// opencode Zen: GET /console/api/policies/current-budgets returns a JSON
// array of budget rules. That one is UNDOCUMENTED and permission-gated, so
// 403 is a normal answer, not a bug (see fetchZenQuota).
var (
	quotaGoUsageURL   = "https://opencode.ai/inference/go/v1/usage"
	quotaZenBudgetURL = "https://opencode.ai/console/api/policies/current-budgets"
	// openai-codex: GET /backend-api/wham/usage with the ChatGPT OAuth access
	// token (+ ChatGPT-Account-Id) returns {plan_type, rate_limit:{
	// primary_window, secondary_window:{used_percent, limit_window_seconds,
	// reset_at}}}. Undocumented, same endpoint the Codex CLI's /status uses.
	quotaCodexURL = "https://chatgpt.com/backend-api/wham/usage"
	// anthropic (Claude Pro/Max OAuth): GET /api/oauth/usage returns
	// {five_hour:{utilization,resets_at}, seven_day:{…}, seven_day_opus:{…}}.
	// Undocumented, the endpoint Claude Code's /usage reads.
	quotaAnthropicURL = "https://api.anthropic.com/api/oauth/usage"
	// openrouter: GET /api/v1/key returns {data:{usage,limit,limit_reset,…}}.
	quotaOpenRouterURL = "https://openrouter.ai/api/v1/key"
)

const (
	// quotaTTL is how long a snapshot may be reused: short enough that a
	// turn's usage shows up, long enough that a burst of settles does not
	// hammer two endpoints on every reply.
	quotaTTL = 90 * time.Second
	// quotaHTTPTimeout bounds ONE endpoint call. Two providers are fetched
	// sequentially off the event loop, so the worst case is two windows.
	quotaHTTPTimeout = 6 * time.Second
	// quotaBodyLimit caps what we read from a console endpoint: a usage
	// payload is a few hundred bytes, and an HTML error page must not be
	// buffered whole.
	quotaBodyLimit = 1 << 20
)

// quotaHTTP has an explicit timeout because the default client has none:
// a console endpoint that accepts the connection and then stalls would keep
// the fetch Cmd (and its goroutine) alive for the rest of the session.
var quotaHTTP = &http.Client{Timeout: quotaHTTPTimeout}

// quotaWindowOrder is the display order of the Go usage buckets. "rolling"
// is the 5-hour bucket the console labels 5h; keys the endpoint adds later
// are appended alphabetically so a new bucket is visible, not dropped.
var quotaWindowOrder = []string{"rolling", "weekly", "monthly"}

// quotaWindowLabel maps the endpoint's bucket ids to the short panel label.
// An id we have no word for falls through QuotaWindow.ShortLabel, which uses
// the window duration when that is known and the raw name otherwise:
// inventing a nicer label would hide that the endpoint changed.
var quotaWindowLabel = map[string]string{"rolling": "5h", "weekly": "week", "monthly": "month",
	"five_hour": "5h", "seven_day": "week", "seven_day_opus": "opus wk", "seven_day_sonnet": "sonnet wk"}

// quotaWindowSecs is the VERIFIED duration of each known bucket.
//
// rolling is 5h and weekly is 7d (both documented by the console's own
// labels); monthly is deliberately 0/unknown — the endpoint reports a reset
// date but never the bucket length, and the sidebar's even-use mark would be
// a fabricated line if we guessed one.
var quotaWindowSecs = map[string]int{
	"rolling": 5 * 60 * 60,
	"weekly":  7 * 24 * 60 * 60,
	"monthly": 0,
}

// quotaDurationLabel names a window by its duration: the labels the reference
// panel uses (5h, week, month) for the durations it knows, else Nh.
func quotaDurationLabel(secs int) string {
	switch secs {
	case 300 * 60:
		return "5h"
	case 10080 * 60:
		return "week"
	case 43200 * 60:
		return "month"
	}
	if secs < 3600 {
		if m := secs / 60; m > 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dh", secs/3600)
}

// FetchProviderQuota reads one provider's usage. The caller must have
// resolved the credential situation already or accept the Err row: a
// provider that is simply not configured belongs to QuotaSnapshot's skip
// list, not here.
func FetchProviderQuota(provider, keyPath string) ProviderQuota {
	q, supported := newProviderQuota(provider)
	if !supported {
		return q
	}
	key := quotaKey(provider, keyPath)
	if key == "" {
		q.Err = "no saved key — /login " + provider
		return q
	}
	return fetchProviderQuota(q, key)
}

// newProviderQuota seeds one row and answers the supported question. It is
// split out so QuotaSnapshot can resolve the credential ONCE per provider and
// hand it to fetchProviderQuota (the keystore read used to happen twice).
func newProviderQuota(provider string) (ProviderQuota, bool) {
	q := ProviderQuota{Provider: provider, Label: ProviderLabel(provider), FetchedAt: time.Now()}
	for _, p := range quotaProviders {
		if p == provider {
			return q, true
		}
	}
	// The seam increment 2 plugs into: saying so is honest, and an empty
	// row would read as "0% used".
	q.Err = "quota tracking not supported yet"
	return q, false
}

// fetchProviderQuota dispatches one already-credentialed row to its adapter.
func fetchProviderQuota(q ProviderQuota, key string) ProviderQuota {
	switch q.Provider {
	case "opencode-go":
		return fetchGoQuota(q, key)
	case "opencode":
		return fetchZenQuota(q, key)
	case "openai-codex":
		return fetchCodexQuota(q, key)
	case "anthropic":
		return fetchAnthropicQuota(q, key)
	case "openrouter":
		return fetchOpenRouterQuota(q, key)
	}
	return q
}

// QuotaSnapshot fetches every supported provider that actually HAS a
// credential, in quotaProviders order. Providers without a key are skipped
// entirely: an error row for a provider the user never touched would be
// permanent noise for everyone who does not use OpenCode.
func QuotaSnapshot(keyPath string) []ProviderQuota {
	out := make([]ProviderQuota, 0, len(quotaProviders))
	for _, provider := range quotaProviders {
		// Resolve the key once: the old shape read the keystore here and
		// again inside FetchProviderQuota, i.e. twice per provider.
		key := quotaKey(provider, keyPath)
		if key == "" {
			continue
		}
		q, supported := newProviderQuota(provider)
		if !supported {
			continue
		}
		out = append(out, fetchProviderQuota(q, key))
	}
	return out
}

// quotaKey resolves the credential a usage call carries: pitago's keystore
// first (the ACTIVE key — a user can save several and switch), then pi's
// auth.json, then the environment.
//
// The env fallback is LAST on purpose: a key the user saved through /login (or
// pi's own login) is the one they selected, while an exported variable is a
// leftover the pi child merely inherits. Without it, though, a user who only
// exports a key renders no row at all, because nothing was ever saved to read.
//
// Twins are why the env name goes through LookupEnv(provider) and not the raw
// pi name: pi's env map names OPENCODE_API_KEY for BOTH opencode and
// opencode-go, so a bare OPENCODE_API_KEY cannot say which twin it was
// exported for. pitago's keystore splits that shared slot (see
// uniqueEnvSlots: opencode-go gets OPENCODE_GO_API_KEY), and the Go adapter
// reads only that dedicated name. Reading the shared var for Go would feed Go
// windows a key that was only ever meant for the Zen twin (or paint it as "no
// OpenCode Go subscription on this key"); a user who exported just
// OPENCODE_API_KEY gets the Zen row, which is the twin that name safely
// describes.
func quotaKey(provider, keyPath string) string {
	if provider == "openai-codex" || provider == "anthropic" {
		// OAuth subscription: the "key" is the access token pi keeps in
		// auth.json (read-only here; pi refreshes it on use).
		tok, _ := oauthCred(provider)
		return tok
	}
	env := LookupEnv(provider)
	if env != "" && keyPath != "" {
		if e := LoadStore(keyPath)[env]; e != nil && len(e.Keys) > 0 {
			i := e.Active
			if i < 0 || i >= len(e.Keys) {
				i = 0
			}
			if k := strings.TrimSpace(e.Keys[i].Key); k != "" {
				return k
			}
		}
	}
	// pi's auth.json is the fallback (and the only source for keys added by
	// stock pi). PiApiKey only reads, and only api_key entries: an OAuth
	// entry is not a Bearer key.
	if k := PiApiKey(provider); k != "" {
		return k
	}
	if env == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(env))
}

// fetchGoQuota reads OpenCode Go's usage windows. A missing/empty "usage"
// object is tolerated as zero windows (no error): the payload shapes are
// verified by hand, not by a contract, so a rename upstream must degrade to
// "no rows" instead of a permanent red error.
func fetchGoQuota(q ProviderQuota, key string) ProviderQuota {
	status, body, err := quotaGet(quotaGoUsageURL, key)
	if err != nil {
		q.Err = err.Error()
		return q
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		q.Err = quotaAuthErr(q.Provider, body)
		return q
	case http.StatusForbidden:
		q.Err = "no OpenCode Go subscription on this key"
		return q
	default:
		q.Err = quotaStatusErr(q.Provider, status)
		return q
	}
	var payload struct {
		Usage map[string]quotaUsageWindow `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		q.Err = "usage endpoint returned an unexpected payload"
		return q
	}
	known := 0
	for _, name := range quotaWindowNames(payload.Usage) {
		w := payload.Usage[name]
		qw := QuotaWindow{Name: name, Status: strings.TrimSpace(w.Status),
			ResetsAt: quotaTime(w.ResetsAt), WindowSecs: quotaWindowSecs[name]}
		if qw.Status == "" {
			qw.Status = "ok" // the console omits "ok"; every other word means a limit
		}
		if pct, ok := quotaNumber(w.Percent); ok {
			// 0 is a real percent, so clamp instead of treating out-of-range
			// values as unknown: a negative or >100 percent is still the
			// provider's own number, just badly bounded.
			qw.UsedPct = min(max(int(math.Round(pct)), 0), 100)
			qw.PercentKnown = true
			known++
		}
		q.Windows = append(q.Windows, qw)
	}
	if len(q.Windows) > 0 && known == 0 {
		// Every window is present but none carries a usable percent: the row
		// must SAY the numbers are missing. Dropping the windows keeps the
		// ProviderQuota invariant that an Err row carries no numbers, so no
		// surface can render a confident 0%.
		q.Windows = nil
		q.Err = "usage payload had no usable percent"
	}
	return q
}

// quotaUsageWindow is one raw bucket. Percent is kept raw because the
// endpoint sends int OR float (23 vs 23.5) and JSON would otherwise bounce
// the whole payload on the one shape not seen yet.
type quotaUsageWindow struct {
	Status   string          `json:"status"`
	Percent  json.RawMessage `json:"percent"`
	ResetsAt json.RawMessage `json:"resetsAt"`
}

// quotaWindowNames orders the buckets: the known three first, then anything
// new alphabetically, so a payload the endpoint grows still renders.
func quotaWindowNames(usage map[string]quotaUsageWindow) []string {
	out := make([]string, 0, len(usage))
	seen := map[string]bool{}
	for _, name := range quotaWindowOrder {
		if _, ok := usage[name]; ok {
			out = append(out, name)
			seen[name] = true
		}
	}
	extra := make([]string, 0, len(usage))
	for name := range usage {
		if !seen[name] && name != "" {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// QuotaWindowLabel is the short display name for a bucket name.
func QuotaWindowLabel(name string) string {
	if lbl, ok := quotaWindowLabel[name]; ok {
		return lbl
	}
	return name
}

// fetchZenQuota reads OpenCode Zen's pay-as-you-go budgets. 403 is the
// NORMAL answer for a key without console permission (the endpoint is
// undocumented and gated), so it becomes a tolerated sentence on a row that
// still exists — hiding the provider would look like it vanished, and
// treating it as a failure would show a red error for a healthy key.
func fetchZenQuota(q ProviderQuota, key string) ProviderQuota {
	status, body, err := quotaGet(quotaZenBudgetURL, key)
	if err != nil {
		q.Err = err.Error()
		return q
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		q.Err = quotaAuthErr(q.Provider, body)
		return q
	case http.StatusForbidden:
		// Tolerated, not failed: the endpoint is undocumented and
		// permission-gated, so 403 is the normal answer for a healthy key.
		// A Note renders dim; an Err would paint the key red and contradict
		// this exact case.
		q.Note = "budget not visible for this key"
		return q
	default:
		q.Err = quotaStatusErr(q.Provider, status)
		return q
	}
	var rows []quotaZenBudget
	if err := json.Unmarshal(body, &rows); err != nil {
		// Deliberately no second-guessing of other shapes or endpoints: the
		// console API is undocumented, and a guessed path would 404 forever.
		q.Err = "budget endpoint returned an unexpected payload"
		return q
	}
	for _, r := range rows {
		b := QuotaBudget{ResetsAt: quotaTime(r.ResetsAt)}
		if v, ok := quotaNumber(r.LimitDollars); ok {
			b.LimitUSD = v
		}
		if v, ok := quotaNumber(r.SpentDollars); ok {
			b.SpentUSD = v
		}
		q.Budgets = append(q.Budgets, b)
	}
	return q
}

// quotaZenBudget is one raw budget rule. Dollars are raw for the same reason
// as percent: the endpoint is undocumented, so "12.5" and 12.5 must both
// parse before a strict struct rejects the payload.
type quotaZenBudget struct {
	LimitDollars json.RawMessage `json:"limitDollars"`
	SpentDollars json.RawMessage `json:"spentDollars"`
	ResetsAt     json.RawMessage `json:"resetsAt"`
}

// quotaNumber parses a JSON number that may arrive as number or string, and
// may be int or float.
func quotaNumber(raw json.RawMessage) (float64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// quotaTime decodes one reset clock. The payload is not a contract: the
// verified shape is an RFC3339 string, but a numeric epoch appears in the wild
// (and sometimes as milliseconds), so both are accepted. Decoding into
// json.RawMessage matters: a strict `string` field made ONE numeric resetsAt
// fail the whole unmarshal and lose every window in the payload.
//
// An unparsable reset is a zero time — zero already means "no clock", and
// omitting a reset beats rendering a wrong one.
func quotaTime(raw json.RawMessage) time.Time {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		// Seconds vs milliseconds by magnitude: 1e11 seconds is the year 5138
		// while 1e11 milliseconds is 1973, so no plausible reset lands on the
		// wrong side of the split.
		if f >= 1e11 {
			return time.Unix(0, int64(f*float64(time.Millisecond)))
		}
		return time.Unix(0, int64(f*float64(time.Second)))
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// quotaGet performs one authenticated GET. The endpoint is passed in so the
// per-provider bodies stay readable.
func quotaGet(url, key string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("usage request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := quotaHTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("usage endpoint unreachable: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, quotaBodyLimit))
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("usage endpoint read: %v", err)
	}
	return res.StatusCode, body, nil
}

// quotaAuthErr turns a 401 into the one action that fixes it. pi's console
// reports an expired key with code "api_key_expired"; either way a 401 is
// "log in again", never "0% used" — the key is dead, the numbers are unknown.
func quotaAuthErr(provider string, body []byte) string {
	if strings.Contains(string(body), "api_key_expired") {
		return "key expired — re-login with /login " + provider
	}
	return "key rejected (401) — re-login with /login " + provider
}

// quotaStatusErr is the short sentence for any other non-200: the status is
// the only fact we have, so it is the whole message.
func quotaStatusErr(provider string, status int) string {
	if text := strings.TrimSpace(http.StatusText(status)); text != "" {
		return fmt.Sprintf("%s usage endpoint: %d %s", provider, status, text)
	}
	return fmt.Sprintf("%s usage endpoint: %d", provider, status)
}

// quotaCache holds the last snapshot for one keystore. Mutex-guarded because
// the UI reads it from Update AND from View: a fetch lands on the event
// loop, but a render can be queued behind it, and a race here would be a
// real data race on the slices, not just a stale row.
var quotaCache struct {
	mu      sync.Mutex
	keyPath string
	at      time.Time
	snap    []ProviderQuota
}

// QuotaCached returns the snapshot for keyPath, fetching only when the cache
// is empty, belongs to another keystore, or is older than quotaTTL.
func QuotaCached(keyPath string) []ProviderQuota {
	quotaCache.mu.Lock()
	if quotaCache.keyPath == keyPath && !quotaCache.at.IsZero() &&
		time.Since(quotaCache.at) < quotaTTL {
		// Clone inside the critical section: a concurrent QuotaForce may
		// replace quotaCache.snap the instant we unlock, and "nobody mutates
		// it after the lock" was an unwritten invariant, not a guarantee.
		snap := cloneQuotas(quotaCache.snap)
		quotaCache.mu.Unlock()
		return snap
	}
	quotaCache.mu.Unlock()
	return QuotaForce(keyPath)
}

// QuotaInvalidate drops the cached snapshot. Credential changes call this:
// the numbers belong to the key that was active when they were fetched, so
// switching/adding/deleting a key must not keep showing the previous key's
// usage for up to quotaTTL.
func QuotaInvalidate() {
	quotaCache.mu.Lock()
	quotaCache.keyPath, quotaCache.at, quotaCache.snap = "", time.Time{}, nil
	quotaCache.mu.Unlock()
}

// QuotaForce refetches every configured provider and replaces the cache. It
// is the dialog-open path: the user asking "what now?" is exactly the reason
// to spend two HTTP calls.
func QuotaForce(keyPath string) []ProviderQuota {
	snap := QuotaSnapshot(keyPath)
	quotaCache.mu.Lock()
	quotaCache.keyPath, quotaCache.at, quotaCache.snap = keyPath, time.Now(), snap
	quotaCache.mu.Unlock()
	return cloneQuotas(snap)
}

// QuotaFresh reports whether the cache holds a usable snapshot for keyPath.
// The post-turn hook asks this before spending a fetch: a settled turn
// within the TTL must not add latency or traffic.
func QuotaFresh(keyPath string) bool {
	quotaCache.mu.Lock()
	defer quotaCache.mu.Unlock()
	return quotaCache.keyPath == keyPath && !quotaCache.at.IsZero() &&
		time.Since(quotaCache.at) < quotaTTL
}

// cloneQuotas hands the UI its own copy: the cache outlives the caller, and
// a later fetch replaces the slices a render may still be walking.
func cloneQuotas(in []ProviderQuota) []ProviderQuota {
	out := make([]ProviderQuota, len(in))
	for i, q := range in {
		q.Windows = append([]QuotaWindow(nil), q.Windows...)
		q.Budgets = append([]QuotaBudget(nil), q.Budgets...)
		out[i] = q
	}
	return out
}

// codexCred reads the ChatGPT OAuth access token and account id from pi's
// auth.json. Read-only: see the file header.
func codexCred() (token, account string) { return oauthCred("openai-codex") }

// oauthCred is the same read for any OAuth entry (anthropic, openai-codex).
func oauthCred(provider string) (token, account string) {
	raw, ok := loadPiRaw(PiAuthPath())[provider]
	if !ok {
		return "", ""
	}
	var c struct {
		Type      string `json:"type"`
		Access    string `json:"access"`
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Type != "oauth" {
		return "", ""
	}
	return strings.TrimSpace(c.Access), strings.TrimSpace(c.AccountID)
}

// fetchCodexQuota reads every rate-limit window the ChatGPT subscription
// reports: the account's primary/secondary windows plus the code-review and
// any model-specific (additional) limits. Window names/labels are derived from
// the verified duration the endpoint reports, never guessed.
func fetchCodexQuota(q ProviderQuota, key string) ProviderQuota {
	_, acct := codexCred()
	req, err := http.NewRequest(http.MethodGet, quotaCodexURL, nil)
	if err != nil {
		q.Err = fmt.Sprintf("usage request: %v", err)
		return q
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if acct != "" {
		req.Header.Set("ChatGPT-Account-Id", acct)
	}
	res, err := quotaHTTP.Do(req)
	if err != nil {
		q.Err = fmt.Sprintf("usage endpoint unreachable: %v", err)
		return q
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, quotaBodyLimit))
	if err != nil {
		q.Err = fmt.Sprintf("usage endpoint read: %v", err)
		return q
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		q.Err = "ChatGPT token expired — send a message to refresh, or /login openai-codex"
		return q
	default:
		q.Err = quotaStatusErr(q.Provider, res.StatusCode)
		return q
	}
	type win struct {
		Used    json.RawMessage `json:"used_percent"`
		Secs    json.RawMessage `json:"limit_window_seconds"`
		ResetAt json.RawMessage `json:"reset_at"`
		After   json.RawMessage `json:"reset_after_seconds"`
	}
	type rateLimit struct {
		Reached   bool `json:"limit_reached"`
		Primary   *win `json:"primary_window"`
		Secondary *win `json:"secondary_window"`
	}
	var payload struct {
		Plan                 string     `json:"plan_type"`
		RateLimit            rateLimit  `json:"rate_limit"`
		CodeReviewRateLimit  *rateLimit `json:"code_review_rate_limit"`
		AdditionalRateLimits []struct {
			LimitName string    `json:"limit_name"`
			Metered   string    `json:"metered_feature"`
			RateLimit rateLimit `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		q.Err = "usage endpoint returned an unexpected payload"
		return q
	}
	q.Plan = strings.TrimSpace(payload.Plan)
	// appendCodexWindows turns one rate-limit object (primary + secondary)
	// into quota rows. prefix is empty for the account's main limit, or a
	// human label for code-review / model-specific limits, so every bucket
	// the endpoint reports is visible instead of only the first two.
	appendCodexWindows := func(rl rateLimit, prefix string, reached bool) {
		for i, w := range []*win{rl.Primary, rl.Secondary} {
			if w == nil {
				continue
			}
			pct, ok := quotaNumber(w.Used)
			if !ok {
				continue
			}
			secs, _ := quotaNumber(w.Secs)
			name := []string{"primary", "secondary"}[i]
			label := ""
			if prefix != "" {
				label = prefix
			}
			qw := QuotaWindow{Name: name, Label: label, PercentKnown: true,
				UsedPct: min(max(int(math.Round(pct)), 0), 100), Status: "ok",
				WindowSecs: int(secs), ResetsAt: quotaTime(w.ResetAt)}
			if qw.ResetsAt.IsZero() {
				if a, ok := quotaNumber(w.After); ok {
					qw.ResetsAt = time.Now().Add(time.Duration(a) * time.Second)
				}
			}
			if reached && qw.UsedPct >= 100 {
				qw.Status = "limited"
			}
			q.Windows = append(q.Windows, qw)
		}
	}
	appendCodexWindows(payload.RateLimit, "", payload.RateLimit.Reached)
	if payload.CodeReviewRateLimit != nil {
		appendCodexWindows(*payload.CodeReviewRateLimit, "code review", payload.CodeReviewRateLimit.Reached)
	}
	for _, ar := range payload.AdditionalRateLimits {
		prefix := strings.TrimSpace(ar.LimitName)
		if prefix == "" {
			prefix = strings.TrimSpace(ar.Metered)
		}
		if prefix == "" {
			prefix = "extra"
		}
		appendCodexWindows(ar.RateLimit, prefix, ar.RateLimit.Reached)
	}
	if len(q.Windows) == 0 {
		q.Err = "usage payload had no usable percent"
	}
	return q
}

// fetchAnthropicQuota reads the Claude subscription windows (5h, 7 day and the
// per-model weekly buckets when the account has them).
func fetchAnthropicQuota(q ProviderQuota, key string) ProviderQuota {
	req, err := http.NewRequest(http.MethodGet, quotaAnthropicURL, nil)
	if err != nil {
		q.Err = fmt.Sprintf("usage request: %v", err)
		return q
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	res, err := quotaHTTP.Do(req)
	if err != nil {
		q.Err = fmt.Sprintf("usage endpoint unreachable: %v", err)
		return q
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, quotaBodyLimit))
	if err != nil {
		q.Err = fmt.Sprintf("usage endpoint read: %v", err)
		return q
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		q.Err = "Claude token expired — send a message to refresh, or /login anthropic"
		return q
	default:
		q.Err = quotaStatusErr(q.Provider, res.StatusCode)
		return q
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		q.Err = "usage endpoint returned an unexpected payload"
		return q
	}
	type win struct {
		Util    json.RawMessage `json:"utilization"`
		ResetAt json.RawMessage `json:"resets_at"`
	}
	for _, k := range []struct {
		key, name string
		secs      int
	}{
		{"five_hour", "five_hour", 18000},
		{"seven_day", "seven_day", 604800},
		{"seven_day_opus", "seven_day_opus", 604800},
		{"seven_day_sonnet", "seven_day_sonnet", 604800},
	} {
		raw, ok := payload[k.key]
		if !ok || string(raw) == "null" {
			continue
		}
		var w win
		if json.Unmarshal(raw, &w) != nil {
			continue
		}
		pct, ok := quotaNumber(w.Util)
		if !ok {
			continue
		}
		q.Windows = append(q.Windows, QuotaWindow{Name: k.name, PercentKnown: true, Status: "ok",
			UsedPct: min(max(int(math.Round(pct)), 0), 100), WindowSecs: k.secs, ResetsAt: quotaTime(w.ResetAt)})
	}
	if len(q.Windows) == 0 {
		q.Err = "usage payload had no usable percent"
	}
	return q
}

// fetchOpenRouterQuota reads the key's spend against its credit limit. A key
// without a limit shows spend only.
func fetchOpenRouterQuota(q ProviderQuota, key string) ProviderQuota {
	status, body, err := quotaGet(quotaOpenRouterURL, key)
	if err != nil {
		q.Err = err.Error()
		return q
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		q.Err = quotaAuthErr(q.Provider, body)
		return q
	default:
		q.Err = quotaStatusErr(q.Provider, status)
		return q
	}
	var payload struct {
		Data struct {
			Usage      json.RawMessage `json:"usage"`
			Limit      json.RawMessage `json:"limit"`
			LimitReset json.RawMessage `json:"limit_reset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		q.Err = "usage endpoint returned an unexpected payload"
		return q
	}
	spent, ok := quotaNumber(payload.Data.Usage)
	if !ok {
		q.Err = "usage payload had no usable amount"
		return q
	}
	b := QuotaBudget{SpentUSD: spent}
	if l, ok := quotaNumber(payload.Data.Limit); ok {
		b.LimitUSD = l
	}
	b.ResetsAt = quotaTime(payload.Data.LimitReset)
	q.Budgets = []QuotaBudget{b}
	return q
}
