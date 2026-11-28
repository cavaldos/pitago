package pirpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// quota tests are httptest-only: the endpoints are package vars precisely so
// a test can redirect them here. Nothing in this file may name a real
// provider host, which also keeps the "no real network I/O" promise visible
// in review: every URL below is a httptest.Server.

// quotaTestEndpoints redirects both usage endpoints and restores them with
// t.Cleanup, so one test's server URL can never leak into the next test.
func quotaTestEndpoints(t *testing.T, goURL, zenURL string) {
	t.Helper()
	oldGo, oldZen := quotaGoUsageURL, quotaZenBudgetURL
	quotaGoUsageURL, quotaZenBudgetURL = goURL, zenURL
	t.Cleanup(func() {
		quotaGoUsageURL, quotaZenBudgetURL = oldGo, oldZen
	})
}

// quotaNeutralEnv clears EVERY provider env var. A developer's shell often
// exports real keys (OPENROUTER_API_KEY, GEMINI_API_KEY, GROQ_API_KEY, …),
// and the env fallback would then turn a quota test into a real API call —
// for Groq even a real request of the reported budget.
func quotaNeutralEnv(t *testing.T) {
	t.Helper()
	for _, p := range ProviderEnvsAll() {
		t.Setenv(p.Env, "")
	}
}

// quotaTestKeys writes one provider key into a temp keystore and points pi's
// auth.json at an empty temp dir: the lookup must find the pitago key and
// never the developer's real one.
func quotaTestKeys(t *testing.T, provider, key string) string {
	t.Helper()
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	writePiAuth(t, agent, map[string]any{})
	// Neutralise ambient provider keys: the env fallback (B2) would otherwise
	// feed the twin the developer's shell variable and make these assertions
	// depend on who runs the suite.
	quotaNeutralEnv(t)
	env := LookupEnv(provider)
	if env == "" {
		t.Fatalf("no keystore slot for provider %q", provider)
	}
	keys := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveKey(keys, env, key); err != nil {
		t.Fatal(err)
	}
	return keys
}

// resetQuotaCache clears the process-wide TTL cache so one test's snapshot
// (and its "fresh" timestamp) cannot satisfy the next test's lookup.
func resetQuotaCache(t *testing.T) {
	t.Helper()
	quotaCache.mu.Lock()
	quotaCache.keyPath, quotaCache.at, quotaCache.snap = "", time.Time{}, nil
	quotaCache.mu.Unlock()
	groqProbeCache.mu.Lock()
	groqProbeCache.key, groqProbeCache.model = "", ""
	groqProbeCache.at, groqProbeCache.q = time.Time{}, ProviderQuota{}
	groqProbeCache.mu.Unlock()
	t.Cleanup(func() {
		quotaCache.mu.Lock()
		quotaCache.keyPath, quotaCache.at, quotaCache.snap = "", time.Time{}, nil
		quotaCache.mu.Unlock()
		groqProbeCache.mu.Lock()
		groqProbeCache.key, groqProbeCache.model = "", ""
		groqProbeCache.at, groqProbeCache.q = time.Time{}, ProviderQuota{}
		groqProbeCache.mu.Unlock()
	})
}

func quotaJSONServer(t *testing.T, body string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		if got := r.Header.Get("Authorization"); got == "" || !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing bearer auth, got %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func quotaStatusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Go usage payload with integer percents: three buckets in console order.
func TestQuotaGoUsageWindows(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-int-key")
	srv := quotaJSONServer(t, `{"usage":{
		"rolling":{"status":"ok","percent":23,"resetsAt":"2026-11-23T10:02:56.000Z"},
		"weekly":{"status":"ok","percent":9,"resetsAt":""},
		"monthly":{"status":"ok","percent":52,"resetsAt":"2026-12-01T00:00:00.000Z"}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if q.Err != "" {
		t.Fatalf("unexpected err: %q", q.Err)
	}
	if q.Provider != "opencode-go" || q.Label != "OpenCode Go" {
		t.Fatalf("provider/label: %+v", q)
	}
	if len(q.Windows) != 3 {
		t.Fatalf("want 3 windows, got %+v", q.Windows)
	}
	for i, want := range []struct {
		name string
		pct  int
	}{{"rolling", 23}, {"weekly", 9}, {"monthly", 52}} {
		w := q.Windows[i]
		if w.Name != want.name || w.UsedPct != want.pct || w.Status != "ok" {
			t.Fatalf("window %d = %+v, want %s %d%% ok", i, w, want.name, want.pct)
		}
	}
	if got := q.Windows[0].ResetsAt.UTC(); !got.Equal(time.Date(2026, 11, 23, 10, 2, 56, 0, time.UTC)) {
		t.Fatalf("resetsAt = %v", got)
	}
	if !q.Windows[1].ResetsAt.IsZero() {
		t.Fatalf("empty resetsAt must stay zero, got %v", q.Windows[1].ResetsAt)
	}
	if got := QuotaWindowLabel("rolling"); got != "5h" {
		t.Fatalf("rolling label = %q", got)
	}
}

// Percent may arrive as float: it must round, not fail the payload.
func TestQuotaGoFloatPercent(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-float-key")
	srv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":23.6}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if q.Err != "" {
		t.Fatalf("float percent must parse, got err %q", q.Err)
	}
	if len(q.Windows) != 1 || q.Windows[0].UsedPct != 24 {
		t.Fatalf("want rolling 24%%, got %+v", q.Windows)
	}
}

// A rate-limited bucket keeps the provider's own status word: the UI colours
// the row red from it even when the percent is still low.
func TestQuotaGoRateLimited(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-limited-key")
	srv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"rate-limited","percent":41,"resetsAt":"2026-11-23T10:02:56.000Z"}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if q.Err != "" {
		t.Fatalf("rate-limited is a status, not an error: %q", q.Err)
	}
	if len(q.Windows) != 1 || q.Windows[0].Status != "rate-limited" || q.Windows[0].UsedPct != 41 {
		t.Fatalf("rate-limited window lost: %+v", q.Windows)
	}
}

// 401 with api_key_expired is a dead credential, not 0%: the sentence must
// send the user to /login.
func TestQuotaGoExpiredKey(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-expired-key")
	srv := quotaStatusServer(t, http.StatusUnauthorized, `{"error":{"code":"api_key_expired"}}`)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if !strings.Contains(q.Err, "re-login") {
		t.Fatalf("401 must tell the user to re-login, got %q", q.Err)
	}
	if !strings.Contains(q.Err, "expired") {
		t.Fatalf("api_key_expired must be named, got %q", q.Err)
	}
	if len(q.Windows) != 0 {
		t.Fatalf("a dead key must not produce windows: %+v", q.Windows)
	}
}

// 403 on Go = the workspace has no subscription on this key.
func TestQuotaGoEntitlement(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-nosub-key")
	srv := quotaStatusServer(t, http.StatusForbidden, `{"error":"EntitlementError"}`)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if !strings.Contains(q.Err, "subscription") {
		t.Fatalf("403 must mention the missing subscription, got %q", q.Err)
	}
}

// Zen budgets: an array of rules, dollars as numbers or strings.
func TestQuotaZenBudgets(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode", "zen-key")
	srv := quotaJSONServer(t, `[
		{"ruleID":"r1","limitDollars":50,"spentDollars":12.4,"scope":"monthly","resetsAt":"2026-12-01T00:00:00.000Z"},
		{"ruleID":"r2","limitDollars":"25.0","spentDollars":"5.5","resetsAt":""}]`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode", keys, "")
	if q.Err != "" {
		t.Fatalf("unexpected err: %q", q.Err)
	}
	if len(q.Budgets) != 2 {
		t.Fatalf("want 2 budgets, got %+v", q.Budgets)
	}
	if q.Budgets[0].LimitUSD != 50 || q.Budgets[0].SpentUSD != 12.4 {
		t.Fatalf("budget 0 = %+v", q.Budgets[0])
	}
	if q.Budgets[1].LimitUSD != 25 || q.Budgets[1].SpentUSD != 5.5 {
		t.Fatalf("string dollars must parse too: %+v", q.Budgets[1])
	}
	if !q.Budgets[0].ResetsAt.UTC().Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("budget reset = %v", q.Budgets[0].ResetsAt)
	}
}

// Zen 403 is a NORMAL answer: the row must survive with a tolerated sentence
// so the provider is visible, not silently dropped.
func TestQuotaZenForbiddenIsTolerated(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode", "zen-gated-key")
	srv := quotaStatusServer(t, http.StatusForbidden, "Forbidden")
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode", keys, "")
	if q.Note != "budget not visible for this key" || q.Err != "" {
		t.Fatalf("403 budget note = %q err = %q", q.Note, q.Err)
	}
	if q.Provider != "opencode" || q.Label != "OpenCode Zen" {
		t.Fatalf("a gated key must still identify its provider: %+v", q)
	}
	// QuotaSnapshot keeps the row: a hidden provider reads as "quota went away".
	snap := QuotaSnapshot(keys, "")
	if len(snap) != 1 || snap[0].Provider != "opencode" || snap[0].Note == "" {
		t.Fatalf("snapshot must keep the gated row: %+v", snap)
	}
}

// Providers without a credential are skipped entirely (no error rows for
// what the user never configured), and their endpoint is never called.
func TestQuotaSnapshotSkipsUnconfigured(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "only-go-key")
	var goHits, zenHits int32
	goSrv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":1}}}`, &goHits)
	zenSrv := quotaJSONServer(t, `[]`, &zenHits)
	quotaTestEndpoints(t, goSrv.URL, zenSrv.URL)

	snap := QuotaSnapshot(keys, "")
	if len(snap) != 1 || snap[0].Provider != "opencode-go" {
		t.Fatalf("want only the configured provider, got %+v", snap)
	}
	if atomic.LoadInt32(&goHits) != 1 {
		t.Fatalf("go endpoint hits = %d, want 1", goHits)
	}
	if atomic.LoadInt32(&zenHits) != 0 {
		t.Fatalf("unconfigured provider must not be fetched, zen hits = %d", zenHits)
	}
}

// The TTL cache serves the second call without a second HTTP hit; forcing
// bypasses it, and an expired timestamp falls back to a fetch.
func TestQuotaTTLCache(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "cache-key")
	var hits int32
	srv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":7}}}`, &hits)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	first := QuotaCached(keys, "")
	if len(first) != 1 || first[0].Windows[0].UsedPct != 7 {
		t.Fatalf("first snapshot = %+v", first)
	}
	if !QuotaFresh(keys) {
		t.Fatal("a just-fetched snapshot must be fresh")
	}
	QuotaCached(keys, "")
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("cached call hit the endpoint: %d requests", got)
	}
	QuotaForce(keys, "")
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("force must refetch: %d requests", got)
	}
	// Age the cache past the TTL: the next cached read must fetch again.
	quotaCache.mu.Lock()
	quotaCache.at = time.Now().Add(-2 * quotaTTL)
	quotaCache.mu.Unlock()
	if QuotaFresh(keys) {
		t.Fatal("an aged snapshot must not count as fresh")
	}
	QuotaCached(keys, "")
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("expired cache must refetch: %d requests", got)
	}
	// A different keystore can never be served from another one's cache.
	other := quotaTestKeys(t, "opencode-go", "cache-other-key")
	QuotaCached(other, "")
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("other keystore must refetch: %d requests", got)
	}
}

// The endpoint override is a test-only seam; t.Cleanup must put the real
// URLs back so one test cannot silently redirect another.
func TestQuotaEndpointOverridesRestored(t *testing.T) {
	origGo, origZen := quotaGoUsageURL, quotaZenBudgetURL
	t.Run("redirected", func(t *testing.T) {
		srv := quotaJSONServer(t, `{}`, nil)
		quotaTestEndpoints(t, srv.URL, srv.URL+"/zen")
		if quotaGoUsageURL != srv.URL {
			t.Fatalf("override not applied: %q", quotaGoUsageURL)
		}
	})
	if quotaGoUsageURL != origGo || quotaZenBudgetURL != origZen {
		t.Fatalf("override leaked: go=%q zen=%q", quotaGoUsageURL, quotaZenBudgetURL)
	}
}

// Unknown providers are the increment-2 seam: a clear sentence, no HTTP.
func TestQuotaUnknownProvider(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "seam-key")
	var hits int32
	srv := quotaJSONServer(t, `{}`, &hits)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("not-a-provider", keys, "")
	if q.Err != "quota tracking not supported yet" {
		t.Fatalf("unknown provider err = %q", q.Err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("unknown provider must not call an endpoint, hits = %d", hits)
	}
}

// A1: a payload whose windows carry no usable percent is NOT "0% used". The
// whole Go row must turn into a sentence, and no window may survive with the
// zero value that used to paint a confident green bar.
func TestQuotaGoMissingPercent(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing", `{"usage":{"rolling":{"status":"ok","resetsAt":"2026-11-23T10:02:56.000Z"}}}`},
		{"null", `{"usage":{"rolling":{"status":"ok","percent":null},"weekly":{"status":"ok","percent":null}}}`},
		{"garbage", `{"usage":{"rolling":{"status":"ok","percent":"lots"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetQuotaCache(t)
			keys := quotaTestKeys(t, "opencode-go", "go-nopct-key")
			srv := quotaJSONServer(t, tc.body, nil)
			quotaTestEndpoints(t, srv.URL, srv.URL)

			q := FetchProviderQuota("opencode-go", keys, "")
			if q.Err != "usage payload had no usable percent" {
				t.Fatalf("err = %q, want the missing-percent sentence", q.Err)
			}
			if len(q.Windows) != 0 {
				t.Fatalf("an Err row must carry no numbers: %+v", q.Windows)
			}
		})
	}
}

// A1 (partial): one unknown percent must not erase the buckets that DID carry
// a number, and the unknown one keeps PercentKnown=false so the row shows a
// dash instead of 0%.
func TestQuotaGoPartialPercent(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "go-mixed-key")
	srv := quotaJSONServer(t, `{"usage":{
		"rolling":{"status":"ok","percent":28},
		"weekly":{"status":"ok"}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode-go", keys, "")
	if q.Err != "" {
		t.Fatalf("one known percent must keep the row: %q", q.Err)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if !q.Windows[0].PercentKnown || q.Windows[0].PercentLeft() != 72 {
		t.Fatalf("rolling = %+v", q.Windows[0])
	}
	if q.Windows[1].PercentKnown || q.Windows[1].UsedPct != 0 {
		t.Fatalf("weekly must stay unknown, not 0%%: %+v", q.Windows[1])
	}
}

// A2: resetsAt may arrive as an epoch number (seconds OR milliseconds) or as a
// string. A number must not destroy the payload: before this, one numeric
// resetsAt failed the whole unmarshal and every window disappeared.
func TestQuotaResetsAtShapes(t *testing.T) {
	sec := int64(1795000000) // 2026-11-18T…Z
	for _, tc := range []struct {
		name string
		raw  string
		want time.Time
	}{
		{"seconds", `1795000000`, time.Unix(sec, 0)},
		{"milliseconds", `1795000000000`, time.Unix(sec, 0)},
		{"fractional seconds", `1795000000.5`, time.Unix(sec, 500_000_000)},
		{"rfc3339", `"2026-11-18T00:00:00.000Z"`, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{"garbage stays zero", `"later"`, time.Time{}},
		{"null stays zero", `null`, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetQuotaCache(t)
			keys := quotaTestKeys(t, "opencode-go", "go-shape-key")
			body := `{"usage":{"rolling":{"status":"ok","percent":28,"resetsAt":` + tc.raw + `},` +
				`"weekly":{"status":"ok","percent":6}}}`
			srv := quotaJSONServer(t, body, nil)
			quotaTestEndpoints(t, srv.URL, srv.URL)

			q := FetchProviderQuota("opencode-go", keys, "")
			if q.Err != "" {
				t.Fatalf("a numeric resetsAt must not fail the payload: %q", q.Err)
			}
			if len(q.Windows) != 2 {
				t.Fatalf("payload lost windows: %+v", q.Windows)
			}
			got := q.Windows[0].ResetsAt
			if tc.want.IsZero() {
				if !got.IsZero() {
					t.Fatalf("unparsable reset = %v, want zero", got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Fatalf("reset = %v, want %v", got, tc.want)
			}
		})
	}
}

// Zen's budget rule resetsAt takes the same shapes (it is the same field name
// from the same console), and the budget survives a numeric one.
func TestQuotaZenResetsAtNumber(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode", "zen-epoch-key")
	srv := quotaJSONServer(t, `[{"limitDollars":100,"spentDollars":12.4,"resetsAt":1795000000000}]`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	q := FetchProviderQuota("opencode", keys, "")
	if q.Err != "" || len(q.Budgets) != 1 {
		t.Fatalf("budget payload = %+v", q)
	}
	if !q.Budgets[0].ResetsAt.Equal(time.Unix(1795000000, 0)) {
		t.Fatalf("budget reset = %v", q.Budgets[0].ResetsAt)
	}
}

// A6: Zen's gated budget endpoint answers 403 for a healthy key, so that is a
// Note (dim), never an Err (red). Real failures stay Err.
func TestQuotaStatusSentences(t *testing.T) {
	cases := []struct {
		name     string
		zen      bool
		status   int
		body     string
		wantErr  string // "" = no Err
		wantNote string
	}{
		{"zen 403 is a note", true, http.StatusForbidden, "Forbidden", "", "budget not visible for this key"},
		{"401 names expired", false, http.StatusUnauthorized, `{"error":{"code":"api_key_expired"}}`, "expired", ""},
		{"401 plain re-login", true, http.StatusUnauthorized, `{"error":"nope"}`, "key rejected (401)", ""},
		{"402 payment", false, http.StatusPaymentRequired, "pay", "402", ""},
		{"429 rate limit", false, http.StatusTooManyRequests, "slow down", "429", ""},
		{"500 server", true, http.StatusInternalServerError, "boom", "500", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetQuotaCache(t)
			provider := "opencode-go"
			if tc.zen {
				provider = "opencode"
			}
			keys := quotaTestKeys(t, provider, "status-key")
			srv := quotaStatusServer(t, tc.status, tc.body)
			quotaTestEndpoints(t, srv.URL, srv.URL)

			q := FetchProviderQuota(provider, keys, "")
			if tc.wantNote != "" {
				if q.Note != tc.wantNote || q.Err != "" {
					t.Fatalf("note = %q err = %q, want note %q", q.Note, q.Err, tc.wantNote)
				}
				if len(q.Windows) != 0 || len(q.Budgets) != 0 {
					t.Fatalf("a noted provider must carry no numbers: %+v", q)
				}
				return
			}
			if q.Err == "" || !strings.Contains(q.Err, tc.wantErr) {
				t.Fatalf("err = %q, want it to name %q", q.Err, tc.wantErr)
			}
			if q.Note != "" {
				t.Fatalf("a real failure must not be a note: %q", q.Note)
			}
		})
	}
}

// B2: the environment is the LAST-resort credential. A user who only exports a
// key (never /login) must still get a row — and the twins must not both read
// one shared var. OPENCODE_GO_API_KEY can only mean Go; OPENCODE_API_KEY is
// pi's shared twin name and is claimed by Zen.
func TestQuotaEnvFallback(t *testing.T) {
	envOnly := func(t *testing.T) string {
		t.Helper()
		agent := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agent)
		writePiAuth(t, agent, map[string]any{})
		quotaNeutralEnv(t)
		return filepath.Join(t.TempDir(), "keys.json")
	}

	t.Run("go env alone", func(t *testing.T) {
		resetQuotaCache(t)
		keys := envOnly(t)
		t.Setenv("OPENCODE_GO_API_KEY", "env-go-key")
		var goHits, zenHits int32
		goSrv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":28}}}`, &goHits)
		zenSrv := quotaJSONServer(t, `[]`, &zenHits)
		quotaTestEndpoints(t, goSrv.URL, zenSrv.URL)

		snap := QuotaSnapshot(keys, "")
		if len(snap) != 1 || snap[0].Provider != "opencode-go" {
			t.Fatalf("env-only Go key should yield one Go row: %+v", snap)
		}
		if len(snap[0].Windows) != 1 || snap[0].Windows[0].UsedPct != 28 {
			t.Fatalf("env key was not used: %+v", snap[0])
		}
		if atomic.LoadInt32(&goHits) != 1 || atomic.LoadInt32(&zenHits) != 0 {
			t.Fatalf("hits go=%d zen=%d, want 1/0", goHits, zenHits)
		}
	})

	t.Run("zen env alone", func(t *testing.T) {
		resetQuotaCache(t)
		keys := envOnly(t)
		t.Setenv("OPENCODE_API_KEY", "env-zen-key")
		var goHits, zenHits int32
		goSrv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":28}}}`, &goHits)
		zenSrv := quotaJSONServer(t, `[{"limitDollars":100,"spentDollars":12.4}]`, &zenHits)
		quotaTestEndpoints(t, goSrv.URL, zenSrv.URL)

		snap := QuotaSnapshot(keys, "")
		if len(snap) != 1 || snap[0].Provider != "opencode" {
			t.Fatalf("the shared env var belongs to Zen, got %+v", snap)
		}
		if atomic.LoadInt32(&goHits) != 0 {
			t.Fatalf("the Go adapter must not read the shared twin var: go hits = %d", goHits)
		}
		if atomic.LoadInt32(&zenHits) != 1 {
			t.Fatalf("zen hits = %d, want 1", zenHits)
		}
	})

	t.Run("nothing set", func(t *testing.T) {
		resetQuotaCache(t)
		keys := envOnly(t)
		var hits int32
		srv := quotaJSONServer(t, `{}`, &hits)
		quotaTestEndpoints(t, srv.URL, srv.URL)
		if snap := QuotaSnapshot(keys, ""); len(snap) != 0 {
			t.Fatalf("no credential should yield no row: %+v", snap)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Fatalf("no credential must not call an endpoint: %d", hits)
		}
	})
}

// A saved key wins over the environment: the env fallback is LAST, because a
// key the user selected through /login is the one they meant.
func TestQuotaKeystoreBeatsEnv(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "saved-key")
	t.Setenv("OPENCODE_GO_API_KEY", "env-key")
	got := quotaKey("opencode-go", keys)
	if got != "saved-key" {
		t.Fatalf("quotaKey = %q, want the saved key", got)
	}
}

// Windowing: the verified durations drive both the compact label and the
// even-use mark, and monthly stays UNKNOWN (0) on purpose.
func TestQuotaWindowSecsAndLabels(t *testing.T) {
	if got := quotaWindowSecs["rolling"]; got != 5*3600 {
		t.Fatalf("rolling secs = %d", got)
	}
	if got := quotaWindowSecs["weekly"]; got != 7*24*3600 {
		t.Fatalf("weekly secs = %d", got)
	}
	if got := quotaWindowSecs["monthly"]; got != 0 {
		t.Fatalf("monthly duration must stay unknown, got %d", got)
	}
	for _, tc := range []struct {
		w    QuotaWindow
		want string
	}{
		{QuotaWindow{Name: "rolling", WindowSecs: 5 * 3600}, "5h"},
		{QuotaWindow{Name: "weekly", WindowSecs: 7 * 24 * 3600}, "week"},
		{QuotaWindow{Name: "monthly"}, "month"},
		// codex-style buckets: derived from the duration, 300min/10080min/
		// 43200min map to the same short words.
		{QuotaWindow{Name: "primary", WindowSecs: 300 * 60}, "5h"},
		{QuotaWindow{Name: "secondary", WindowSecs: 10080 * 60}, "week"},
		{QuotaWindow{Name: "tertiary", WindowSecs: 43200 * 60}, "month"},
		{QuotaWindow{Name: "custom", WindowSecs: 8 * 3600}, "8h"},
		{QuotaWindow{Name: "unknown-bucket"}, "unknown-bucket"},
	} {
		if got := tc.w.ShortLabel(); got != tc.want {
			t.Fatalf("%+v label = %q, want %q", tc.w, got, tc.want)
		}
	}
}

// B3: a returned snapshot is a deep copy. Mutating it must not poison the
// cache that the next reader gets.
func TestQuotaSnapshotIsDeepCopy(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "copy-key")
	srv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":7}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	first := QuotaForce(keys, "")
	if len(first) != 1 || len(first[0].Windows) != 1 {
		t.Fatalf("snapshot = %+v", first)
	}
	first[0].Windows[0].UsedPct = 99
	first[0].Label = "poisoned"
	first[0].Windows = append(first[0].Windows, QuotaWindow{Name: "x"})

	second := QuotaCached(keys, "")
	if second[0].Windows[0].UsedPct != 7 || second[0].Label == "poisoned" || len(second[0].Windows) != 1 {
		t.Fatalf("the cache was poisoned by the caller's copy: %+v", second)
	}
}

// B3/A3: QuotaCached, QuotaForce and QuotaInvalidate run concurrently (the UI
// reads the cache from Update and View while a fetch Cmd replaces it). The
// race detector is the assertion; this test just has to exercise all three.
func TestQuotaCacheConcurrent(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "opencode-go", "race-key")
	srv := quotaJSONServer(t, `{"usage":{"rolling":{"status":"ok","percent":7}}}`, nil)
	quotaTestEndpoints(t, srv.URL, srv.URL)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if snap := QuotaCached(keys, ""); len(snap) > 0 {
					snap[0].Windows[0].UsedPct = 42 // caller-owned copy
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				QuotaForce(keys, "")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				QuotaInvalidate()
				QuotaFresh(keys)
			}
		}()
	}
	wg.Wait()
}

func TestFetchCodexQuota(t *testing.T) {
	var gotAuth, gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAcct = r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-Id")
		w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":12.4,"limit_window_seconds":18000,"reset_after_seconds":600},"secondary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_at":4102444800}}}`))
	}))
	defer srv.Close()
	old := quotaCodexURL
	quotaCodexURL = srv.URL
	t.Cleanup(func() { quotaCodexURL = old })
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"openai-codex":{"type":"oauth","access":"tok","accountId":"acc"}}`), 0o600)

	q := FetchProviderQuota("openai-codex", "", "")
	if q.Err != "" || len(q.Windows) != 2 || q.Plan != "plus" {
		t.Fatalf("q = %+v", q)
	}
	if gotAuth != "Bearer tok" || gotAcct != "acc" {
		t.Fatalf("headers %q %q", gotAuth, gotAcct)
	}
	if q.Windows[0].UsedPct != 12 || q.Windows[0].ShortLabel() != "5h" || q.Windows[1].ShortLabel() != "week" {
		t.Fatalf("windows = %+v", q.Windows)
	}
}

func TestFetchAnthropicAndOpenRouterQuota(t *testing.T) {
	var beta string
	an := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		w.Write([]byte(`{"five_hour":{"utilization":37.5,"resets_at":"2099-01-01T00:00:00Z"},"seven_day":{"utilization":10,"resets_at":null},"seven_day_opus":null}`))
	}))
	defer an.Close()
	or := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"usage":1.25,"limit":10,"limit_reset":null}}`))
	}))
	defer or.Close()
	oa, oo := quotaAnthropicURL, quotaOpenRouterURL
	quotaAnthropicURL, quotaOpenRouterURL = an.URL, or.URL
	t.Cleanup(func() { quotaAnthropicURL, quotaOpenRouterURL = oa, oo })

	q := fetchAnthropicQuota(ProviderQuota{Provider: "anthropic"}, "tok")
	if q.Err != "" || len(q.Windows) != 2 || q.Windows[0].ShortLabel() != "5h" || q.Windows[0].UsedPct != 38 || q.Windows[1].ShortLabel() != "week" || beta == "" {
		t.Fatalf("anthropic = %+v beta=%q", q, beta)
	}
	r := fetchOpenRouterQuota(ProviderQuota{Provider: "openrouter"}, "k")
	if r.Err != "" || len(r.Budgets) != 1 || r.Budgets[0].SpentUSD != 1.25 || r.Budgets[0].LimitUSD != 10 {
		t.Fatalf("openrouter = %+v", r)
	}
}

// The Gemini API has no quota endpoint at all: no usage payload, no
// rate-limit header. The row must therefore VERIFY the key and say where the
// numbers live — never print a percent nobody sent.
func TestQuotaGeminiRowIsASentence(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "google", "gemini-key")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if got := r.Header.Get("x-goog-api-key"); got != "gemini-key" {
			t.Errorf("x-goog-api-key = %q, want the key in the header", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-3.8-flash"}]}`)
	}))
	t.Cleanup(srv.Close)
	old := quotaGeminiModelsURL
	quotaGeminiModelsURL = srv.URL
	t.Cleanup(func() { quotaGeminiModelsURL = old })

	q := FetchProviderQuota("google", keys, "")
	if q.Err != "" {
		t.Fatalf("unexpected err: %q", q.Err)
	}
	if len(q.Windows) != 0 || len(q.Budgets) != 0 {
		t.Fatalf("Gemini must not invent numbers: %+v", q)
	}
	if !strings.Contains(q.Note, "no quota API") {
		t.Fatalf("note = %q, want it to name the missing quota API", q.Note)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}

// An invalid Gemini key answers 400 + "API key not valid", not 401: it is a
// dead key, never "0% used".
func TestQuotaGeminiInvalidKey(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "google", "gemini-key")
	srv := quotaStatusServer(t, http.StatusBadRequest,
		`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`)
	old := quotaGeminiModelsURL
	quotaGeminiModelsURL = srv.URL
	t.Cleanup(func() { quotaGeminiModelsURL = old })

	q := FetchProviderQuota("google", keys, "")
	if !strings.Contains(q.Err, "key rejected") {
		t.Fatalf("err = %q, want a rejected-key sentence", q.Err)
	}
	if len(q.Windows) != 0 {
		t.Fatalf("a failed lookup must not keep windows: %+v", q.Windows)
	}
}

// groqQuotaServer serves the two calls the probe makes: the key's model list
// and one minimal completion that answers with rate-limit headers.
func groqQuotaServer(t *testing.T, hits *int32, probed *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = io.WriteString(w, `{"data":[
				{"id":"whisper-large-v3","output_modalities":["audio"]},
				{"id":"openai/gpt-oss-120b","output_modalities":["text"]}]}`)
		case "/chat/completions":
			if hits != nil {
				atomic.AddInt32(hits, 1)
			}
			var body struct {
				Model string `json:"model"`
			}
			raw, _ := io.ReadAll(io.LimitReader(r.Body, quotaBodyLimit))
			_ = json.Unmarshal(raw, &body)
			if probed != nil {
				*probed = body.Model
			}
			w.Header().Set("x-ratelimit-limit-requests", "1000")
			w.Header().Set("x-ratelimit-remaining-requests", "900")
			w.Header().Set("x-ratelimit-reset-requests", "1m26.4s")
			w.Header().Set("x-ratelimit-limit-tokens", "8000")
			w.Header().Set("x-ratelimit-remaining-tokens", "4000")
			w.Header().Set("x-ratelimit-reset-tokens", "90ms")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-1"}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Groq rate limit lives in the response headers of an inference call:
// the row is the used share of the request and token budgets.
func TestQuotaGroqReadsRateLimitHeaders(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "groq", "groq-key")
	var hits int32
	var probed string
	srv := groqQuotaServer(t, &hits, &probed)
	oldM, oldC := quotaGroqModelsURL, quotaGroqChatURL
	quotaGroqModelsURL, quotaGroqChatURL = srv.URL+"/models", srv.URL+"/chat/completions"
	t.Cleanup(func() { quotaGroqModelsURL, quotaGroqChatURL = oldM, oldC })

	q := FetchProviderQuota("groq", keys, "openai/gpt-oss-120b")
	if q.Err != "" {
		t.Fatalf("unexpected err: %q", q.Err)
	}
	if probed != "openai/gpt-oss-120b" {
		t.Fatalf("probed model = %q, want the model in use", probed)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("want 2 windows, got %+v", q.Windows)
	}
	if q.Windows[0].Name != "requests" || q.Windows[0].UsedPct != 10 || !q.Windows[0].PercentKnown {
		t.Fatalf("requests window = %+v", q.Windows[0])
	}
	if q.Windows[1].Name != "tokens" || q.Windows[1].UsedPct != 50 {
		t.Fatalf("tokens window = %+v", q.Windows[1])
	}
	if q.Windows[0].ResetsAt.IsZero() {
		t.Fatal("the reset clock must be read from the header")
	}
	// The limit belongs to a MODEL: the row has to say which one it measured.
	if q.Plan != "openai/gpt-oss-120b" {
		t.Fatalf("plan = %q, want the probed model", q.Plan)
	}
}

// The probe spends one request of the budget it reports, so the automatic
// snapshot reuses it; only the explicit refresh probes again.
func TestQuotaGroqProbeIsCached(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "groq", "groq-key")
	var hits int32
	srv := groqQuotaServer(t, &hits, nil)
	oldM, oldC := quotaGroqModelsURL, quotaGroqChatURL
	quotaGroqModelsURL, quotaGroqChatURL = srv.URL+"/models", srv.URL+"/chat/completions"
	t.Cleanup(func() { quotaGroqModelsURL, quotaGroqChatURL = oldM, oldC })

	QuotaSnapshot(keys, "openai/gpt-oss-120b")
	QuotaSnapshot(keys, "openai/gpt-oss-120b")
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("probe hits = %d, want 1 (the snapshot must reuse the probe)", got)
	}
	// The explicit refresh is the user asking "what now?": it probes again.
	FetchProviderQuota("groq", keys, "openai/gpt-oss-120b")
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("probe hits = %d, want 2 (an explicit refresh must re-probe)", got)
	}
}

// Without a hint the probe still needs a model: it takes the first one that
// can answer a text prompt (an audio-only model cannot).
func TestQuotaGroqProbeFallsBackToATextModel(t *testing.T) {
	resetQuotaCache(t)
	keys := quotaTestKeys(t, "groq", "groq-key")
	var hits int32
	var probed string
	srv := groqQuotaServer(t, &hits, &probed)
	oldM, oldC := quotaGroqModelsURL, quotaGroqChatURL
	quotaGroqModelsURL, quotaGroqChatURL = srv.URL+"/models", srv.URL+"/chat/completions"
	t.Cleanup(func() { quotaGroqModelsURL, quotaGroqChatURL = oldM, oldC })

	FetchProviderQuota("groq", keys, "")
	if probed != "openai/gpt-oss-120b" {
		t.Fatalf("probed model = %q, want the text model", probed)
	}
}
