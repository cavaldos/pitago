package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PiAuthPath is pi's auth.json: $PI_CODING_AGENT_DIR/auth.json else
// ~/.pi/agent/auth.json (mirrors pi's getAgentDir).
func PiAuthPath() string {
	if d := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); d != "" {
		return filepath.Join(d, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "auth.json")
}

// piCred decodes one auth.json entry. OAuth fields are decoded separately
// (see piAuthMeta); the raw map below is what preserves them on write.
type piCred struct {
	Type string            `json:"type"`
	Key  string            `json:"key,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// loadPiRaw reads auth.json preserving every provider entry byte-for-byte
// (OAuth access/refresh tokens MUST survive our api_key edits).
func loadPiRaw(path string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}
	for k, v := range m {
		var t struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(v, &t); err != nil || t.Type == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func savePiRaw(path string, m map[string]json.RawMessage) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// 0600 like pi (user read/write only).
	return os.WriteFile(path, raw, 0o600)
}

func decodePiCred(raw json.RawMessage) piCred {
	var c piCred
	_ = json.Unmarshal(raw, &c)
	return c
}

// PiAuthEntry is one pi auth.json entry (api_key or oauth) for display.
type PiAuthEntry struct {
	Provider string
	Type     string // "api_key" | "oauth" | ...
	Expires  int64  // oauth expiry (ms epoch, 0 = unknown/none)
	Account  string // oauth account id/email when present
}

// piAuthMeta extracts display metadata without touching secrets.
func piAuthMeta(provider string, raw json.RawMessage) PiAuthEntry {
	e := PiAuthEntry{Provider: provider}
	var v struct {
		Type      string `json:"type"`
		Expires   int64  `json:"expires"`
		AccountID string `json:"accountId"`
		Account   string `json:"account"`
		Email     string `json:"email"`
	}
	_ = json.Unmarshal(raw, &v)
	e.Type = v.Type
	e.Expires = v.Expires
	e.Account = v.AccountID
	if e.Account == "" {
		e.Account = v.Account
	}
	if e.Account == "" {
		e.Account = v.Email
	}
	return e
}

// ListPiAuth returns every pi auth entry sorted by provider (stable UI).
func ListPiAuth() []PiAuthEntry {
	m := loadPiRaw(PiAuthPath())
	out := make([]PiAuthEntry, 0, len(m))
	for prov, raw := range m {
		out = append(out, piAuthMeta(prov, raw))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// PiOAuth reports the OAuth state for a provider (ok=false when none).
func PiOAuth(provider string) (expires int64, account string, ok bool) {
	if provider == "" {
		return 0, "", false
	}
	raw, found := loadPiRaw(PiAuthPath())[provider]
	if !found {
		return 0, "", false
	}
	e := piAuthMeta(provider, raw)
	if e.Type != "oauth" {
		return 0, "", false
	}
	return e.Expires, e.Account, true
}

// DeletePiAuth removes one provider entry (any type) from pi's auth.json.
// Missing → nil. Raw-preserving: other providers untouched byte-for-byte.
func DeletePiAuth(provider string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil
	}
	path := PiAuthPath()
	m := loadPiRaw(path)
	if _, ok := m[provider]; !ok {
		return nil
	}
	delete(m, provider)
	return savePiRaw(path, m)
}

// PiApiKey reads pi's api_key for a provider ("" = none/oauth/empty).
// OAuth entries are never treated as API keys.
func PiApiKey(provider string) string {
	if provider == "" {
		return ""
	}
	raw, ok := loadPiRaw(PiAuthPath())[provider]
	if !ok {
		return ""
	}
	c := decodePiCred(raw)
	if c.Type != "api_key" {
		return ""
	}
	return strings.TrimSpace(resolvePiKey(c))
}

// resolvePiKey unwraps "$ENV" indirection via cred env then process env.
func resolvePiKey(c piCred) string {
	k := strings.TrimSpace(c.Key)
	if k == "" {
		return ""
	}
	if strings.HasPrefix(k, "$") && len(k) > 1 {
		name := strings.TrimPrefix(strings.TrimPrefix(k, "${"), "$")
		name = strings.TrimSuffix(name, "}")
		name = strings.TrimSpace(name)
		if name == "" {
			return ""
		}
		if c.Env != nil {
			if v, ok := c.Env[name]; ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	return k
}

// WritePiKey stores an API key into pi's auth.json as
// {provider:{type:api_key,key}} — other providers stay byte-for-byte intact
// (OAuth tokens are never decoded/re-encoded). Never touches a non-api_key
// entry for this provider. Empty key removes the api_key entry.
func WritePiKey(provider, key string) error {
	provider = strings.TrimSpace(provider)
	key = strings.TrimSpace(key)
	if provider == "" {
		return nil
	}
	path := PiAuthPath()
	m := loadPiRaw(path)
	if key == "" {
		if raw, ok := m[provider]; ok && decodePiCred(raw).Type == "api_key" {
			delete(m, provider)
			return savePiRaw(path, m)
		}
		return nil
	}
	// Never clobber OAuth: only write when missing or already api_key.
	if raw, ok := m[provider]; ok && decodePiCred(raw).Type != "api_key" {
		return nil
	}
	nb, err := json.Marshal(piCred{Type: "api_key", Key: key})
	if err != nil {
		return err
	}
	m[provider] = nb
	return savePiRaw(path, m)
}

// PushActiveToPi writes pitago's ACTIVE key for one env to pi's auth.json
// + process env so the respawned pi inherits it. Last-key-deleted (no keys)
// removes the pi api_key entry + unsets env. Inactive deletes only ensure
// pi still matches the active key.
func PushActiveToPi(keyPath, env string) {
	if env == "" {
		return
	}
	provider, _ := LookupProvider(env)
	if provider == "" {
		return
	}
	keys, active := ListKeys(keyPath, env)
	if active < 0 || len(keys) == 0 {
		_ = WritePiKey(provider, "")
		_ = os.Unsetenv(env)
		return
	}
	_ = WritePiKey(provider, keys[active])
	_ = os.Setenv(env, keys[active])
}

// SyncFromPi merges pi's auth.json api_keys into pitago's keystore WITHOUT
// overwriting: unknown keys are appended, known keys just become visible,
// and the active pointer is NEVER moved here (pitago's selection wins; pi
// is updated explicitly via PushActiveToPi/EnsurePiHasActive).
// Returns number of newly imported keys.
// ponytail: union, never last-write-wins.
func SyncFromPi(keyPath string) int {
	if keyPath == "" {
		return 0
	}
	m := loadPiRaw(PiAuthPath())
	if len(m) == 0 {
		return 0
	}
	store := LoadStore(keyPath)
	n := 0
	dirty := false
	for _, p := range ProviderEnvsAll() {
		raw, ok := m[p.Provider]
		if !ok {
			continue
		}
		c := decodePiCred(raw)
		if c.Type != "api_key" {
			continue
		}
		k := strings.TrimSpace(resolvePiKey(c))
		if k == "" || strings.HasPrefix(k, "!") {
			continue // command-backed secrets stay in pi only
		}
		k = sanitizeKey(k)
		if k == "" {
			continue
		}
		e := store[p.Env]
		if e == nil {
			store[p.Env] = &KeyEntry{Keys: []KeyItem{{Key: k, AddedAt: nowUnix()}}, Active: 0}
			n++
			dirty = true
			continue
		}
		found := -1
		for i, it := range e.Keys {
			if it.Key == k {
				found = i
				break
			}
		}
		if found < 0 {
			e.Keys = append(e.Keys, KeyItem{Key: k, AddedAt: nowUnix()})
			n++
			dirty = true
		}
		// found >= 0: already tracked — active stays where pitago left it.
	}
	if dirty {
		_ = saveStore(keyPath, store)
	}
	return n
}

// EnsurePiHasActive pushes pitago's active keys to pi where pi has no
// entry at all (first-run pitago → pi). Existing pi entries (incl. OAuth)
// are never overwritten here — user selection pushes explicitly.
func EnsurePiHasActive(keyPath string) {
	if keyPath == "" {
		return
	}
	path := PiAuthPath()
	m := loadPiRaw(path)
	store := LoadStore(keyPath)
	dirty := false
	for _, p := range ProviderEnvsAll() {
		if _, ok := m[p.Provider]; ok {
			continue
		}
		e := store[p.Env]
		if e == nil || len(e.Keys) == 0 {
			continue
		}
		i := e.Active
		if i < 0 || i >= len(e.Keys) {
			i = 0
		}
		nb, err := json.Marshal(piCred{Type: "api_key", Key: e.Keys[i].Key})
		if err != nil {
			continue
		}
		m[p.Provider] = nb
		dirty = true
	}
	if dirty {
		_ = savePiRaw(path, m)
	}
}

// AuthStatePath is ~/.config/pitago/pi_auth.json (0600): pitago's mirror of
// pi logins (api_key presence + oauth account/expiry). OAuth login itself
// runs in stock pi; pitago saves the state here to list + manage it.
func AuthStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "pi_auth.json")
}

// AuthStateEntry is one mirrored pi login (no secrets — presence only).
type AuthStateEntry struct {
	Type      string `json:"type"`
	Account   string `json:"account,omitempty"`
	Expires   int64  `json:"expires,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// LoadAuthState reads pitago's pi-login mirror. Missing → empty.
func LoadAuthState(path string) map[string]AuthStateEntry {
	out := map[string]AuthStateEntry{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var m map[string]AuthStateEntry
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}
	for k, v := range m {
		if v.Type == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func saveAuthState(path string, m map[string]AuthStateEntry) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// SyncAuthStateFromPi mirrors pi's current logins into pitago's state file:
// new/changed entries recorded, entries removed in pi dropped. Secrets are
// never copied (api_key → presence only). Returns changed provider count.
func SyncAuthStateFromPi(path string) int {
	if path == "" {
		return 0
	}
	live := loadPiRaw(PiAuthPath())
	cur := LoadAuthState(path)
	n := 0
	now := nowUnix()
	for prov, raw := range live {
		meta := piAuthMeta(prov, raw)
		if meta.Type == "" {
			continue
		}
		want := AuthStateEntry{Type: meta.Type, Account: meta.Account, Expires: meta.Expires, UpdatedAt: now}
		if meta.Type == "api_key" {
			want.Account, want.Expires = "", 0
		}
		if old, ok := cur[prov]; ok && old.Type == want.Type && old.Account == want.Account && old.Expires == want.Expires {
			continue
		}
		cur[prov] = want
		n++
	}
	for prov := range cur {
		if _, ok := live[prov]; !ok {
			delete(cur, prov)
			n++
		}
	}
	if n > 0 {
		_ = saveAuthState(path, cur)
	}
	return n
}

// ForgetAuthState drops one provider from pitago's mirror (after a
// pitago-side disconnect). Missing → nil.
func ForgetAuthState(path, provider string) {
	if path == "" || provider == "" {
		return
	}
	cur := LoadAuthState(path)
	if _, ok := cur[provider]; !ok {
		return
	}
	delete(cur, provider)
	_ = saveAuthState(path, cur)
}
