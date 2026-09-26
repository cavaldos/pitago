package pirpc

import (
	"bytes"
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
	clearPiChildEnv()
	t.Cleanup(clearPiChildEnv)
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
	// The env half goes to the pi CHILD only: pitago's own environment must
	// stay exactly what the user's shell exported.
	if os.Getenv("GROQ_API_KEY") != "" {
		t.Fatalf("PushActiveToPi leaked into pitago's own env: %q", os.Getenv("GROQ_API_KEY"))
	}
	if v, ok := PiChildEnv("GROQ_API_KEY"); !ok || v != "gsk-pitago-12345678" {
		t.Fatalf("child env = %q (ok=%v)", v, ok)
	}
	// ...and the child actually receives it.
	if !containsEnv(piChildEnviron(), "GROQ_API_KEY=gsk-pitago-12345678") {
		t.Fatalf("pi child env missing the key: %v", piChildEnviron())
	}
	// last key deleted → the child var is dropped, not left behind
	if err := DeleteKeyAt(keys, "GROQ_API_KEY", 0); err != nil {
		t.Fatal(err)
	}
	PushActiveToPi(keys, "GROQ_API_KEY")
	if v, ok := PiChildEnv("GROQ_API_KEY"); !ok || v != "" {
		t.Fatalf("deleted key still in child env: %q (ok=%v)", v, ok)
	}
	if containsEnv(piChildEnviron(), "GROQ_API_KEY=") {
		t.Fatalf("dropped var still handed to the child: %v", piChildEnviron())
	}
}

// pitago must not mutate its own environment at launch: the key travels in
// the child overlay and is applied by Spawn. No overlay → the child
// inherits verbatim, exactly like launching pi from the same shell.
func TestChildEnvOverlayLeavesProcessEnvAlone(t *testing.T) {
	clearPiChildEnv()
	t.Cleanup(clearPiChildEnv)
	if env := piChildEnviron(); env != nil {
		t.Fatalf("empty overlay should inherit verbatim, got %v", env)
	}
	t.Setenv("PITAGO_ENV_PROBE", "ambient")
	SetPiChildEnv("PITAGO_ENV_PROBE", "pushed")
	SetPiChildEnv("PITAGO_ENV_EXTRA", "new")
	env := piChildEnviron()
	if !containsEnv(env, "PITAGO_ENV_PROBE=pushed") {
		t.Errorf("overlay must win for the child: %v", env)
	}
	if !containsEnv(env, "PITAGO_ENV_EXTRA=new") {
		t.Errorf("added var missing: %v", env)
	}
	if os.Getenv("PITAGO_ENV_PROBE") != "ambient" {
		t.Errorf("pitago's own env changed: %q", os.Getenv("PITAGO_ENV_PROBE"))
	}
	if os.Getenv("PITAGO_ENV_EXTRA") != "" {
		t.Errorf("pitago's own env gained a var: %q", os.Getenv("PITAGO_ENV_EXTRA"))
	}
	// dropping removes it from the child env
	SetPiChildEnv("PITAGO_ENV_PROBE", "")
	if containsEnv(piChildEnviron(), "PITAGO_ENV_PROBE=") {
		t.Errorf("dropped var still in child env: %v", piChildEnviron())
	}
}

// auth.json "$ENV" indirection resolves through the child overlay, so a
// key pushed by /login reads back exactly as it does inside pi.
func TestResolvePiKeyUsesChildOverlay(t *testing.T) {
	clearPiChildEnv()
	t.Cleanup(clearPiChildEnv)
	c := piCred{Type: "api_key", Key: "${PITAGO_ENV_PROBE}"}
	t.Setenv("PITAGO_ENV_PROBE", "")
	if got := resolvePiKey(c); got != "" {
		t.Fatalf("empty ambient should resolve empty, got %q", got)
	}
	SetPiChildEnv("PITAGO_ENV_PROBE", "sk-from-overlay-1234")
	if got := resolvePiKey(c); got != "sk-from-overlay-1234" {
		t.Fatalf("overlay lookup = %q", got)
	}
	// cred-level env still wins over the overlay, like pi
	c.Env = map[string]string{"PITAGO_ENV_PROBE": "sk-from-cred"}
	if got := resolvePiKey(c); got != "sk-from-cred" {
		t.Fatalf("cred env = %q", got)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
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

// pi parses auth.json as JSON.parse(stripBom(content)) as well, so a
// BOM-prefixed auth.json is a valid pi file. If pitago's reader rejected the
// mark it would see an EMPTY auth.json and the next /login write would
// rewrite the file with one entry — silently deleting the user's OAuth
// tokens. The BOM itself is kept on the rewrite.
func TestPiAuthWithBOMIsReadAndKept(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	seed := piBOM + `{"openai-codex":{"type":"oauth","access":"AAA","refresh":"RRR",` +
		`"expires":1790857264866,"accountId":"acct-1"}}`
	if err := os.WriteFile(filepath.Join(agent, "auth.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if exp, acct, ok := PiOAuth("openai-codex"); !ok || exp != 1790857264866 || acct != "acct-1" {
		t.Fatalf("BOM auth.json not read: %d %q %v", exp, acct, ok)
	}
	if entries := ListPiAuth(); len(entries) != 1 || entries[0].Provider != "openai-codex" {
		t.Fatalf("entries = %+v", entries)
	}
	if err := WritePiKey("groq", "gsk-bom-12345678"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(agent, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte(piBOM)) {
		t.Errorf("BOM must survive the rewrite: %q", raw)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(stripPiBOM(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m["openai-codex"]["access"] != "AAA" || m["openai-codex"]["refresh"] != "RRR" {
		t.Fatalf("oauth damaged: %v", m["openai-codex"])
	}
	if m["groq"]["key"] != "gsk-bom-12345678" {
		t.Fatalf("groq = %v", m["groq"])
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

// pi's auth.json holds OAuth tokens and login state, and pi reads it on
// every start: the write must swap the file atomically, never truncate it
// in place, and never leave a temp file behind.
func TestWritePiKeyIsAtomic(t *testing.T) {
	clearPiChildEnv()
	t.Cleanup(clearPiChildEnv)
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	writePiAuth(t, agent, map[string]any{
		"openai-codex": map[string]any{"type": "oauth", "access": "AAA", "refresh": "RRR", "expires": 1790857264866},
	})
	for i := 0; i < 20; i++ {
		if err := WritePiKey("groq", "gsk-iter-12345678"); err != nil {
			t.Fatal(err)
		}
		// every intermediate state is a complete, valid document
		raw, err := os.ReadFile(filepath.Join(agent, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("iteration %d: auth.json unreadable: %v", i, err)
		}
		if m["openai-codex"]["access"] != "AAA" || m["groq"]["key"] != "gsk-iter-12345678" {
			t.Fatalf("iteration %d: %v", i, m)
		}
	}
	entries, err := os.ReadDir(agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.json" {
		t.Fatalf("stray temp files in the agent dir: %d entries", len(entries))
	}
	if fi, _ := os.Stat(filepath.Join(agent, "auth.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json perm = %o, want 600", fi.Mode().Perm())
	}
}
