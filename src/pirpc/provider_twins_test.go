package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// pi lets twin providers share one env var; pitago's keystore is keyed by
// env, so every twin after the first gets its own <ID>_API_KEY slot.
func TestUniqueEnvSlotsSplitsTwins(t *testing.T) {
	got := uniqueEnvSlots([]ProviderEnv{
		{"opencode", "OpenCode Zen", "OPENCODE_API_KEY"},
		{"opencode-go", "OpenCode Go", "OPENCODE_API_KEY"},
		{"moonshotai", "Moonshot AI", "MOONSHOT_API_KEY"},
		{"moonshotai-cn", "Moonshot AI (China)", "MOONSHOT_API_KEY"},
		{"groq", "Groq", "GROQ_API_KEY"},
	})
	want := []string{"OPENCODE_API_KEY", "OPENCODE_GO_API_KEY", "MOONSHOT_API_KEY", "MOONSHOTAI_CN_API_KEY", "GROQ_API_KEY"}
	for i, w := range want {
		if got[i].Env != w {
			t.Fatalf("%s env = %q, want %q", got[i].Provider, got[i].Env, w)
		}
	}
}

// The static fallback must not carry ids pi no longer has, and the merged
// catalog never shares an env slot between providers.
func TestCatalogHasNoStaleIDsOrSharedSlots(t *testing.T) {
	seen := map[string]string{}
	for _, p := range ProviderEnvsAll() {
		if p.Provider == "azure-openai-responses" {
			t.Fatalf("stale provider id %q (pi 1.4 calls it azure)", p.Provider)
		}
		if other, ok := seen[p.Env]; ok {
			t.Fatalf("env slot %s shared by %s and %s", p.Env, other, p.Provider)
		}
		seen[p.Env] = p.Provider
	}
	for _, id := range []string{"azure", "typesafe"} {
		if LookupEnv(id) == "" {
			t.Fatalf("provider %s missing from catalog", id)
		}
	}
}

// PushActiveFor writes under the provider the user picked, even when its
// env var also belongs to a twin (the old reverse lookup wrote to the twin).
func TestPushActiveForTargetsPickedTwin(t *testing.T) {
	clearPiChildEnv()
	t.Cleanup(clearPiChildEnv)
	clearAmbientPiKeys(t, "OPENCODE_API_KEY")
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	keys := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveKey(keys, "OPENCODE_API_KEY", "oc-go-key-12345678"); err != nil {
		t.Fatal(err)
	}
	PushActiveFor(keys, "opencode-go", "OPENCODE_API_KEY")
	raw, err := os.ReadFile(filepath.Join(agent, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["opencode-go"]; !ok {
		t.Fatalf("key not written for opencode-go: %s", raw)
	}
	if _, ok := m["opencode"]; ok {
		t.Fatalf("key leaked into twin opencode: %s", raw)
	}
}
