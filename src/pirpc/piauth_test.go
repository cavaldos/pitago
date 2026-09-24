package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePiAuth(t *testing.T, dir string, v map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// pitago -> pi: PushActiveToPi must write auth.json (env alone is not
// enough — pi's auth.json wins over env).
func TestPushActiveToPiWritesAuth(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	keys := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveKey(keys, "GROQ_API_KEY", "gsk-pitago-12345678"); err != nil {
		t.Fatal(err)
	}
	PushActiveToPi(keys, "GROQ_API_KEY")
	raw, err := os.ReadFile(filepath.Join(agent, "auth.json"))
	if err != nil {
		t.Fatalf("auth.json not written: %v", err)
	}
	var m map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["groq"].Key != "gsk-pitago-12345678" {
		t.Fatalf("pi key = %q", m["groq"].Key)
	}
	if os.Getenv("GROQ_API_KEY") != "gsk-pitago-12345678" {
		t.Fatalf("env = %q", os.Getenv("GROQ_API_KEY"))
	}
}

// pi -> pitago: SyncFromPi unions without overwriting (123 kept, 456 added,
// active stays where pitago left it).
func TestSyncFromPiMergesWithoutOverwrite(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	keys := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveKey(keys, "GROQ_API_KEY", "key-123-pitago-abcdefgh"); err != nil {
		t.Fatal(err)
	}
	writePiAuth(t, agent, map[string]any{
		"groq": map[string]any{"type": "api_key", "key": "key-456-pi-abcdefgh"},
	})
	if n := SyncFromPi(keys); n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}
	got, active := ListKeys(keys, "GROQ_API_KEY")
	if len(got) != 2 {
		t.Fatalf("keys = %v", got)
	}
	if got[0] != "key-123-pitago-abcdefgh" || got[1] != "key-456-pi-abcdefgh" {
		t.Fatalf("union = %v", got)
	}
	if active != 0 {
		t.Fatalf("active = %d, want 0 (preserve pitago)", active)
	}
	// re-sync is idempotent, no dupes
	if n := SyncFromPi(keys); n != 0 {
		t.Fatalf("resync imported = %d, want 0", n)
	}
	if got, _ := ListKeys(keys, "GROQ_API_KEY"); len(got) != 2 {
		t.Fatalf("after resync = %v", got)
	}
}

// OAuth entries are never imported or clobbered.
func TestSyncFromPiSkipsOAuth(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	keys := filepath.Join(t.TempDir(), "keys.json")
	writePiAuth(t, agent, map[string]any{
		"openai-codex": map[string]any{"type": "oauth", "access": "a", "refresh": "r", "expires": 999},
	})
	if n := SyncFromPi(keys); n != 0 {
		t.Fatalf("imported oauth = %d", n)
	}
	if err := SaveKey(keys, "GROQ_API_KEY", "gsk-x"); err != nil {
		t.Fatal(err)
	}
	// WritePiKey must not touch oauth provider entries
	writePiAuth(t, agent, map[string]any{
		"openai-codex": map[string]any{"type": "oauth", "access": "a", "refresh": "r", "expires": 999},
	})
	if err := WritePiKey("openai-codex", "sk-should-not-write"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(agent, "auth.json"))
	var m map[string]map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["openai-codex"]["type"] != "oauth" {
		t.Fatalf("oauth clobbered: %v", m["openai-codex"])
	}
}

func TestRenameAndAddedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveKey(path, "GROQ_API_KEY", "gsk-rename-12345678"); err != nil {
		t.Fatal(err)
	}
	items, _ := ListKeyItems(path, "GROQ_API_KEY")
	if len(items) != 1 || items[0].AddedAt <= 0 {
		t.Fatalf("addedAt missing: %+v", items)
	}
	if err := RenameKey(path, "GROQ_API_KEY", 0, "work"); err != nil {
		t.Fatal(err)
	}
	items, _ = ListKeyItems(path, "GROQ_API_KEY")
	if items[0].Name != "work" {
		t.Fatalf("name = %q", items[0].Name)
	}
}

// OAuth entries must survive byte-for-byte when pitago edits another
// provider's api_key (regression: old struct rewrite wiped tokens).
func TestApiKeyWritePreservesOAuth(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	oauth := map[string]any{"type": "oauth", "access": "AAA", "refresh": "RRR", "expires": 1790857264866, "accountId": "acct-1"}
	writePiAuth(t, agent, map[string]any{"openai-codex": oauth})
	if err := WritePiKey("groq", "gsk-new-12345678"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(agent, "auth.json"))
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got := m["openai-codex"]
	if got["access"] != "AAA" || got["refresh"] != "RRR" || got["accountId"] != "acct-1" {
		t.Fatalf("oauth damaged: %v", got)
	}
	if m["groq"]["key"] != "gsk-new-12345678" {
		t.Fatalf("groq = %v", m["groq"])
	}
	// deleting the api_key leaves oauth alone
	if err := WritePiKey("groq", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(agent, "auth.json"))
	m = nil
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["groq"]; ok {
		t.Fatalf("groq should be gone: %v", m)
	}
	if m["openai-codex"]["access"] != "AAA" {
		t.Fatalf("oauth damaged after delete: %v", m["openai-codex"])
	}
}

func TestListPiAuthAndCatalog(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	writePiAuth(t, agent, map[string]any{
		"openai-codex": map[string]any{"type": "oauth", "access": "a", "refresh": "r", "expires": 1790857264866, "accountId": "acct-9"},
		"groq":         map[string]any{"type": "api_key", "key": "gsk-x"},
	})
	entries := ListPiAuth()
	if len(entries) != 2 || entries[0].Provider != "groq" || entries[1].Provider != "openai-codex" {
		t.Fatalf("entries = %+v", entries)
	}
	exp, acct, ok := PiOAuth("openai-codex")
	if !ok || exp != 1790857264866 || acct != "acct-9" {
		t.Fatalf("oauth = %d %q %v", exp, acct, ok)
	}
	if _, _, ok := PiOAuth("groq"); ok {
		t.Fatal("groq must not report oauth")
	}
	found := map[string]bool{}
	for _, p := range AllLoginProviders() {
		found[p] = true
	}
	for _, want := range []string{"groq", "openai-codex", "github-copilot", "radius"} {
		if !found[want] {
			t.Fatalf("catalog missing %q", want)
		}
	}
	if ProviderLabel("openai-codex") != "OpenAI Codex" {
		t.Fatalf("label = %q", ProviderLabel("openai-codex"))
	}
	if err := DeletePiAuth("openai-codex"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := PiOAuth("openai-codex"); ok {
		t.Fatal("should be disconnected")
	}
}

// pitago mirrors pi logins to pi_auth.json (no secrets): oauth recorded,
// api_key as presence, removals in pi dropped.
func TestAuthStateMirror(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	state := filepath.Join(t.TempDir(), "pi_auth.json")
	writePiAuth(t, agent, map[string]any{
		"openai-codex": map[string]any{"type": "oauth", "access": "a", "refresh": "r", "expires": 1790857264866, "accountId": "acct-9"},
		"groq":         map[string]any{"type": "api_key", "key": "gsk-x"},
	})
	if n := SyncAuthStateFromPi(state); n != 2 {
		t.Fatalf("synced = %d, want 2", n)
	}
	cur := LoadAuthState(state)
	if cur["openai-codex"].Account != "acct-9" || cur["openai-codex"].Expires != 1790857264866 {
		t.Fatalf("mirror = %+v", cur["openai-codex"])
	}
	if cur["groq"].Type != "api_key" {
		t.Fatalf("mirror groq = %+v", cur["groq"])
	}
	if fi, _ := os.Stat(state); fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(state)
	if strings.Contains(string(raw), "gsk-x") {
		t.Fatal("mirror must never store secrets")
	}
	// resync idempotent
	if n := SyncAuthStateFromPi(state); n != 0 {
		t.Fatalf("resync = %d, want 0", n)
	}
	// logout in pi drops the mirror entry
	_ = DeletePiAuth("openai-codex")
	if n := SyncAuthStateFromPi(state); n != 1 {
		t.Fatalf("after logout sync = %d, want 1", n)
	}
	if _, ok := LoadAuthState(state)["openai-codex"]; ok {
		t.Fatal("mirror should forget logged-out provider")
	}
	ForgetAuthState(state, "groq")
	if _, ok := LoadAuthState(state)["groq"]; ok {
		t.Fatal("forget should drop groq")
	}
}
