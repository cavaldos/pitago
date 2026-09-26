package pirpc

import (
	"testing"
)

// Minified-shape fixture: bare + quoted keys, decoys that must not match
// (lowercase values, non-env strings).
const piBundleFixture = `blah blah getApiKeyEnvVars(provider){if(provider==="github-copilot")return["COPILOT_GITHUB_TOKEN"];let envVar={"ant-ling":"ANT_LING_API_KEY","qwen-token-plan-cn":"QWEN_TOKEN_PLAN_CN_API_KEY",openai:"OPENAI_API_KEY",provider:"openai-codex",api:"openai-codex-responses",nvidia:"NVIDIA_API_KEY",meta:"META_API_KEY"};more js here}`

func TestParsePiEnvMap(t *testing.T) {
	got := parsePiEnvMap(piBundleFixture)
	want := map[string]string{
		"ant-ling":           "ANT_LING_API_KEY",
		"qwen-token-plan-cn": "QWEN_TOKEN_PLAN_CN_API_KEY",
		"openai":             "OPENAI_API_KEY",
		"nvidia":             "NVIDIA_API_KEY",
		"meta":               "META_API_KEY",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %d entries", got, len(want))
	}
	for _, p := range got {
		if want[p.Provider] != p.Env {
			t.Fatalf("provider %q env = %q", p.Provider, p.Env)
		}
		if p.Label == "" {
			t.Fatalf("provider %q has no label", p.Provider)
		}
	}
	// order preserved from bundle
	if got[0].Provider != "ant-ling" || got[2].Provider != "openai" {
		t.Fatalf("order not preserved: %v", got)
	}
}

func TestParsePiEnvMapNoAnchor(t *testing.T) {
	if got := parsePiEnvMap(`{"a":"B_C"}`); len(got) != 0 {
		t.Fatalf("expected nil without anchor, got %v", got)
	}
}

func TestMergeProvidersDedupes(t *testing.T) {
	primary := []ProviderEnv{{"openai", "OpenAI", "OPENAI_API_KEY"}}
	fallback := []ProviderEnv{
		{"openai", "OpenAI-old", "OPENAI_API_KEY"},
		{"moonshot", "Moonshot", "MOONSHOT_API_KEY"},
	}
	got := mergeProviders(primary, fallback)
	if len(got) != 2 || got[0].Label != "OpenAI" || got[1].Provider != "moonshot" {
		t.Fatalf("merge = %v", got)
	}
}

func TestDiscoverFallbackWithoutPi(t *testing.T) {
	t.Setenv("PI_BIN", "/nonexistent/pi-bin")
	// uncached path: bogus PI_BIN + no PATH hit for it → only specials
	got := discoverPiProviders()
	if len(got) != len(piSpecialEnvs) {
		t.Fatalf("fallback = %v, want only specials", got)
	}
}

func TestLiveCatalogCoversDocs(t *testing.T) {
	t.Setenv("PI_BIN", "") // use real PATH pi when present
	// discoverPiProviders always returns the special-auth providers even
	// without pi, so gate on the bundle itself (CI has no pi installed).
	if len(piBundleChunks()) == 0 {
		t.Skip("pi bundle not found (pi not installed)")
	}
	live := discoverPiProviders()
	for _, id := range []string{"openai", "anthropic", "google", "meta", "baseten", "ant-ling", "moonshotai", "qwen-token-plan-cn", "xiaomi-token-plan-sgp"} {
		if id == "anthropic" {
			continue // special-cased in pi, covered by static list
		}
		found := false
		for _, p := range live {
			if p.Provider == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("live catalog missing %q: %v", id, live)
		}
	}
}
