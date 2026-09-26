package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func nowUnix() int64 { return time.Now().Unix() }

// sanitizeKey trims paste artifacts: spaces plus one layer of []/quotes.
// API keys never start/end with those; "[gsk-...]" pastes stay usable.
func sanitizeKey(k string) string {
	k = strings.TrimSpace(k)
	for len(k) >= 2 {
		if (k[0] == '[' && k[len(k)-1] == ']') || (k[0] == '"' && k[len(k)-1] == '"') || (k[0] == '\'' && k[len(k)-1] == '\'') {
			k = strings.TrimSpace(k[1 : len(k)-1])
			continue
		}
		break
	}
	return k
}

// FormatAdded renders AddedAt as dd/mm HH:MM ("—" when unset).
// ponytail: stdlib only, no extra dep for one date.
func FormatAdded(ts int64) string {
	if ts <= 0 {
		return "—"
	}
	return time.Unix(ts, 0).Format("02/01 15:04")
}

// ProviderEnv maps a pi provider (auth.json key) to its API-key env var.
// Source: pi docs/providers.md "Environment Variables or Auth File".
type ProviderEnv struct {
	Provider string // pi provider id
	Label    string // human label
	Env      string // env var holding the API key
}

// ProviderEnvs is the STATIC fallback catalog of single-env-var API-key
// providers. At runtime ProviderEnvsAll() prefers pi's live catalog parsed
// from its installed bundle (see piproviders.go), so pi updates flow through
// with no pitago change; this list only matters when pi can't be found.
var ProviderEnvs = []ProviderEnv{
	{"anthropic", "Anthropic", "ANTHROPIC_API_KEY"},
	{"openai", "OpenAI", "OPENAI_API_KEY"},
	{"google", "Google Gemini", "GEMINI_API_KEY"},
	{"deepseek", "DeepSeek", "DEEPSEEK_API_KEY"},
	{"mistral", "Mistral", "MISTRAL_API_KEY"},
	{"groq", "Groq", "GROQ_API_KEY"},
	{"cerebras", "Cerebras", "CEREBRAS_API_KEY"},
	{"xai", "xAI", "XAI_API_KEY"},
	{"openrouter", "OpenRouter", "OPENROUTER_API_KEY"},
	{"zai", "ZAI Coding Plan", "ZAI_API_KEY"},
	{"opencode", "OpenCode Zen", "OPENCODE_API_KEY"},
	{"kimi-coding", "Kimi For Coding", "KIMI_API_KEY"},
	{"minimax", "MiniMax", "MINIMAX_API_KEY"},
	{"together", "Together AI", "TOGETHER_API_KEY"},
	{"fireworks", "Fireworks", "FIREWORKS_API_KEY"},
	{"huggingface", "Hugging Face", "HF_TOKEN"},
	{"nvidia", "NVIDIA NIM", "NVIDIA_API_KEY"},
	{"vercel-ai-gateway", "Vercel AI Gateway", "AI_GATEWAY_API_KEY"},
	{"moonshotai", "Moonshot AI", "MOONSHOT_API_KEY"},
	{"moonshotai-cn", "Moonshot AI (China)", "MOONSHOT_API_KEY"},
	{"qwen-token-plan", "Qwen Token Plan", "QWEN_TOKEN_PLAN_API_KEY"},
	{"qwen-token-plan-cn", "Qwen Token Plan (China)", "QWEN_TOKEN_PLAN_CN_API_KEY"},
	{"qwen-token-plan-individual", "Qwen Token Plan (Individual)", "QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY"},
	{"minimax-cn", "MiniMax (China)", "MINIMAX_CN_API_KEY"},
	{"zai-coding-cn", "ZAI Coding Plan (China)", "ZAI_CODING_CN_API_KEY"},
	{"xiaomi", "Xiaomi MiMo", "XIAOMI_API_KEY"},
	{"xiaomi-token-plan-cn", "Xiaomi Token Plan (China)", "XIAOMI_TOKEN_PLAN_CN_API_KEY"},
	{"xiaomi-token-plan-ams", "Xiaomi Token Plan (Amsterdam)", "XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
	{"xiaomi-token-plan-sgp", "Xiaomi Token Plan (Singapore)", "XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
	{"ant-ling", "Ant Ling", "ANT_LING_API_KEY"},
	{"azure-openai-responses", "Azure OpenAI", "AZURE_OPENAI_API_KEY"},
	{"baseten", "Baseten", "BASETEN_API_KEY"},
	{"cloudflare-ai-gateway", "Cloudflare AI Gateway", "CLOUDFLARE_AI_GATEWAY_API_KEY"},
	{"cloudflare-workers-ai", "Cloudflare Workers AI", "CLOUDFLARE_WORKERS_AI_API_KEY"},
	{"google-vertex", "Google Vertex AI", "GOOGLE_VERTEX_API_KEY"},
	{"meta", "Meta", "META_API_KEY"},
	{"opencode-go", "OpenCode Go", "OPENCODE_GO_API_KEY"},
	{"radius", "Radius", "RADIUS_API_KEY"},
}

// OAuthProvider is a subscription/OAuth-only provider (no API-key env).
// Source: pi docs/providers.md "Subscriptions" — login happens in stock
// pi, but pitago lists, tracks and disconnects them.
type OAuthProvider struct {
	Provider string // pi provider id (auth.json key)
	Label    string // human label
}

// OAuthProviders covers pi subscription logins without an API-key env.
var OAuthProviders = []OAuthProvider{
	{"openai-codex", "OpenAI Codex"},
	{"github-copilot", "GitHub Copilot"},
	{"radius", "Radius"},
}

// ProviderLabel returns the human label for any known provider id.
func ProviderLabel(provider string) string {
	for _, p := range ProviderEnvsAll() {
		if p.Provider == provider {
			return p.Label
		}
	}
	for _, p := range OAuthProviders {
		if p.Provider == provider {
			return p.Label
		}
	}
	return provider
}

// AllLoginProviders lists every provider /login can manage: API-key
// providers plus OAuth-only ones, sorted for a stable picker.
func AllLoginProviders() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ProviderEnvsAll() {
		if !seen[p.Provider] {
			seen[p.Provider] = true
			out = append(out, p.Provider)
		}
	}
	for _, p := range OAuthProviders {
		if !seen[p.Provider] {
			seen[p.Provider] = true
			out = append(out, p.Provider)
		}
	}
	return out
}

// LookupEnv returns the env var for a provider id, or "".
func LookupEnv(provider string) string {
	for _, p := range ProviderEnvsAll() {
		if p.Provider == provider {
			return p.Env
		}
	}
	return ""
}

// LookupProvider returns the provider id + label for an env var.
func LookupProvider(env string) (provider, label string) {
	for _, p := range ProviderEnvsAll() {
		if p.Env == env {
			return p.Provider, p.Label
		}
	}
	return "", ""
}

// KeyPath is ~/.config/pitago/keys.json (0600): {ENV_VAR: key}.
func KeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "keys.json")
}

// RecentPath is ~/.config/pitago/recent_models.json: [{provider, id, label}].
func RecentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "recent_models.json")
}

// FavPath is ~/.config/pitago/favorite_models.json: [{provider, id, label}].
func FavPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "favorite_models.json")
}

// KeyItem is one saved key with optional label + creation time.
// Stored as {"key":"...","name":"...","addedAt":...}; plain strings from
// older files still load (name empty, addedAt 0).
type KeyItem struct {
	Key     string `json:"key"`
	Name    string `json:"name,omitempty"`
	AddedAt int64  `json:"addedAt,omitempty"`
}

// UnmarshalJSON accepts {"key":...} objects or bare "key" strings.
func (k *KeyItem) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		k.Key, k.Name, k.AddedAt = s, "", 0
		return nil
	}
	type alias KeyItem
	var a alias
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	*k = KeyItem(a)
	return nil
}

// KeyEntry is one provider's keys: several saved, one active (exported to
// env for the pi child + written to pi's auth.json).
// Stored as {"keys":[...],"active":n} per env var.
type KeyEntry struct {
	Keys   []KeyItem `json:"keys"`
	Active int       `json:"active"`
}

// LoadStore reads the keystore with old-format migration:
// old {"ENV":"key"} → {ENV:{keys:[key],active:0}}. Missing file → empty.
func LoadStore(path string) map[string]*KeyEntry {
	out := make(map[string]*KeyEntry)
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	// new format first
	var nv map[string]*KeyEntry
	if err := json.Unmarshal(raw, &nv); err == nil && nv != nil {
		for env, e := range nv {
			if e == nil {
				continue
			}
			// drop empties, clamp active
			keys := make([]KeyItem, 0, len(e.Keys))
			for _, k := range e.Keys {
				k.Key = sanitizeKey(k.Key)
				k.Name = strings.TrimSpace(k.Name)
				if r := []rune(k.Name); len(r) > 40 {
					k.Name = string(r[:40])
				}
				if k.Key != "" {
					keys = append(keys, k)
				}
			}
			if len(keys) == 0 {
				continue
			}
			if e.Active < 0 || e.Active >= len(keys) {
				e.Active = 0
			}
			out[env] = &KeyEntry{Keys: keys, Active: e.Active}
		}
		// empty new-format file (e.g. "{}") unmarshals fine; an old-format
		// file fails above and falls through below. Distinguish by content:
		// if raw has a string value it wasn't new format — retry as old.
		if len(out) > 0 || strings.Contains(string(raw), `"keys"`) || strings.TrimSpace(string(raw)) == "{}" {
			return out
		}
	}
	var ov map[string]string
	if err := json.Unmarshal(raw, &ov); err != nil {
		return out
	}
	for env, k := range ov {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out[env] = &KeyEntry{Keys: []KeyItem{{Key: strings.TrimSpace(k)}}, Active: 0}
	}
	return out
}

func saveStore(path string, store map[string]*KeyEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	// Atomic: the keystore holds the user's API keys, and /login reads it
	// right back after a write. A truncated file would drop saved keys.
	return writeFileAtomic(path, raw, filePerm(path, 0o600))
}

// LoadKeys returns the ACTIVE key per env (what pi inherits). Missing → empty.
func LoadKeys(path string) map[string]string {
	store := LoadStore(path)
	out := make(map[string]string, len(store))
	for env, e := range store {
		if e == nil || len(e.Keys) == 0 {
			continue
		}
		i := e.Active
		if i < 0 || i >= len(e.Keys) {
			i = 0
		}
		out[env] = e.Keys[i].Key
	}
	return out
}

// SaveKey appends a key (dedupe: existing key just becomes active) and makes
// it the active one. Creates dir 0700, file 0600.
func SaveKey(path, env, key string) error {
	return SaveKeyNamed(path, env, key, "")
}

// SaveKeyNamed is SaveKey with an optional label for a brand-new key.
// ponytail: one path for add + import, name only set on creation.
func SaveKeyNamed(path, env, key, name string) error {
	key = sanitizeKey(key)
	if env == "" || key == "" {
		return nil
	}
	store := LoadStore(path)
	e := store[env]
	if e == nil {
		store[env] = &KeyEntry{Keys: []KeyItem{{Key: key, Name: strings.TrimSpace(name), AddedAt: nowUnix()}}, Active: 0}
		return saveStore(path, store)
	}
	for i, k := range e.Keys {
		if k.Key == key {
			e.Active = i
			return saveStore(path, store)
		}
	}
	e.Keys = append(e.Keys, KeyItem{Key: key, Name: strings.TrimSpace(name), AddedAt: nowUnix()})
	e.Active = len(e.Keys) - 1
	return saveStore(path, store)
}

// DeleteKey removes a whole provider entry (missing → nil). Kept for
// /logout compat when the last key is gone.
func DeleteKey(path, env string) error {
	store := LoadStore(path)
	if _, ok := store[env]; !ok {
		return nil
	}
	delete(store, env)
	return saveStore(path, store)
}

// ListKeys returns a copy of one provider's keys + active index (-1 = none).
// Kept for compat; new code prefers ListKeyItems for name/date display.
func ListKeys(path, env string) ([]string, int) {
	items, active := ListKeyItems(path, env)
	if active < 0 {
		return nil, -1
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out, active
}

// ListKeyItems returns a copy of one provider's key items + active index.
func ListKeyItems(path, env string) ([]KeyItem, int) {
	store := LoadStore(path)
	e := store[env]
	if e == nil || len(e.Keys) == 0 {
		return nil, -1
	}
	out := append([]KeyItem(nil), e.Keys...)
	i := e.Active
	if i < 0 || i >= len(out) {
		i = 0
	}
	return out, i
}

// RenameKey sets a key's label (trimmed, max 40 runes). Missing → nil.
func RenameKey(path, env string, idx int, name string) error {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	store := LoadStore(path)
	e := store[env]
	if e == nil || idx < 0 || idx >= len(e.Keys) {
		return nil
	}
	e.Keys[idx].Name = name
	return saveStore(path, store)
}

// SetActive picks which saved key is exported to env (respawn to apply).
func SetActive(path, env string, idx int) error {
	store := LoadStore(path)
	e := store[env]
	if e == nil || idx < 0 || idx >= len(e.Keys) {
		return nil
	}
	e.Active = idx
	return saveStore(path, store)
}

// DeleteKeyAt removes one key; deleting the active one falls back to keys[0].
// Removing the last key drops the whole entry.
func DeleteKeyAt(path, env string, idx int) error {
	store := LoadStore(path)
	e := store[env]
	if e == nil || idx < 0 || idx >= len(e.Keys) {
		return nil
	}
	e.Keys = append(append([]KeyItem(nil), e.Keys[:idx]...), e.Keys[idx+1:]...)
	if len(e.Keys) == 0 {
		delete(store, env)
		return saveStore(path, store)
	}
	if e.Active == idx {
		e.Active = 0
	} else if e.Active > idx {
		e.Active--
	}
	return saveStore(path, store)
}

// MaskKey hides a secret for list display: first 4 + •••• + last 4.
// ponytail: no full key ever reaches the UI.
func MaskKey(k string) string {
	r := []rune(k)
	if len(r) <= 8 {
		return "••••••"
	}
	return string(r[:4]) + "••••" + string(r[len(r)-4:])
}
