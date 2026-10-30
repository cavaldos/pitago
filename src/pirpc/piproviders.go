package pirpc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Live pi provider catalog.
//
// pitago used to hardcode ProviderEnvs from pi's docs, which rotted every
// time pi shipped a new provider. Instead we parse pi's own env map out of
// its installed bundle (getApiKeyEnvVars in dist/bundle/chunks/*.js), so pi
// updates flow through automatically. Static ProviderEnvs stays as fallback
// when pi can't be found or the bundle layout changes.

// piOpenAIAnchor locates pi's bundled env map: the openai entry is always a
// single-key API provider, quoted or bare (openai:"OPENAI_API_KEY").
var piOpenAIAnchor = regexp.MustCompile(`["']?openai["']?\s*:\s*"OPENAI_API_KEY"`)

// piEnvMapEntry matches one map entry: quoted or bare id keys, quoted
// UPPER_SNAKE env values. Lowercase values (model ids, api names) never match.
var piEnvMapEntry = regexp.MustCompile(`["']?([A-Za-z0-9_-]+)["']?\s*:\s*"([A-Z][A-Z0-9_]*)"`)

// piExtraLabels covers provider ids pi added after our static list was
// snapshotted. Unknown future ids fall back to prettified id (see below).
var piExtraLabels = map[string]string{
	"moonshotai":                 "Moonshot AI",
	"moonshotai-cn":              "Moonshot AI (China)",
	"minimax-cn":                 "MiniMax (China)",
	"zai-coding-cn":              "ZAI Coding Plan (China)",
	"qwen-token-plan-individual": "Qwen Token Plan (Individual)",
	"qwen-token-plan-cn":         "Qwen Token Plan (China)",
	"xiaomi":                     "Xiaomi MiMo",
	"xiaomi-token-plan-cn":       "Xiaomi Token Plan (China)",
	"xiaomi-token-plan-ams":      "Xiaomi Token Plan (Amsterdam)",
	"xiaomi-token-plan-sgp":      "Xiaomi Token Plan (Singapore)",
	"ant-ling":                   "Ant Ling",
	"azure":                      "Azure OpenAI",
	"typesafe":                   "TypeSafe",
	"amazon-bedrock":             "Amazon Bedrock",
	"baseten":                    "Baseten",
	"cloudflare-ai-gateway":      "Cloudflare AI Gateway",
	"cloudflare-workers-ai":      "Cloudflare Workers AI",
	"google-vertex":              "Google Vertex AI",
	"github-copilot":             "GitHub Copilot",
	"meta":                       "Meta",
	"opencode-go":                "OpenCode Go",
	"radius":                     "Radius",
}

// piSpecialEnvs are pi providers with auth outside the single-key env map:
// copilot takes a GitHub token, bedrock an AWS bearer token. (Anthropic's
// key is already in the static list.)
var piSpecialEnvs = []ProviderEnv{
	{"github-copilot", "GitHub Copilot", "COPILOT_GITHUB_TOKEN"},
	{"amazon-bedrock", "Amazon Bedrock", "AWS_BEARER_TOKEN_BEDROCK"},
}

// parsePiEnvMap extracts provider→env pairs from one pi bundle chunk.
// "" when the anchor (pi's env map) isn't in this chunk.
func parsePiEnvMap(src string) []ProviderEnv {
	loc := piOpenAIAnchor.FindStringIndex(src)
	if loc == nil {
		return nil
	}
	// The env map is a flat object literal (no nesting): last "{" before the
	// anchor opens it, first "}" after closes it.
	l := strings.LastIndex(src[:loc[0]], "{")
	if l < 0 {
		return nil
	}
	r := strings.Index(src[l:], "}")
	if r < 0 || r > 20000 {
		return nil
	}
	win := src[l : l+r+1]
	var out []ProviderEnv
	seen := map[string]bool{}
	for _, m := range piEnvMapEntry.FindAllStringSubmatch(win, -1) {
		id, env := m[1], m[2]
		if id != strings.ToLower(id) || !strings.Contains(env, "_") || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ProviderEnv{Provider: id, Label: piProviderLabel(id), Env: env})
	}
	return out
}

// piProviderLabel resolves a display label: static list first, then the
// extras above, then prettified id ("foo-bar-cn" → "Foo Bar Cn").
func piProviderLabel(id string) string {
	for _, p := range ProviderEnvs {
		if p.Provider == id && p.Label != "" {
			return p.Label
		}
	}
	if lbl := piExtraLabels[id]; lbl != "" {
		return lbl
	}
	var b strings.Builder
	for i, w := range strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' }) {
		if i > 0 {
			b.WriteByte(' ')
		}
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	if b.Len() == 0 {
		return id
	}
	return b.String()
}

// piBundleChunks locates pi's installed bundle chunks. npm/Homebrew: $PI_BIN
// (or PATH "pi") resolves to <pkg>/dist/bundle/cli.js with chunks/ alongside.
// pi's managed installer puts a shell launcher on PATH instead, so fall back
// to the package dir PiPackageRoot finds.
func piBundleChunks() []string {
	bin := strings.TrimSpace(os.Getenv("PI_BIN"))
	if bin == "" {
		if p, err := exec.LookPath("pi"); err == nil {
			bin = p
		}
	}
	if bin != "" {
		if rp, err := filepath.EvalSymlinks(bin); err == nil && rp != "" {
			bin = rp
		}
		if ms, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), "chunks", "*.js")); len(ms) > 0 {
			return ms
		}
	}
	if root, err := PiPackageRoot(); err == nil {
		ms, _ := filepath.Glob(filepath.Join(root, "dist", "bundle", "chunks", "*.js"))
		return ms
	}
	return nil
}

// discoverPiProviders reads the live catalog from pi's bundle (plus the
// special-auth providers). Empty when pi isn't installed — callers fall back
// to the static list.
func discoverPiProviders() []ProviderEnv {
	var out []ProviderEnv
	seen := map[string]bool{}
	add := func(p ProviderEnv) {
		if p.Provider == "" || p.Env == "" || seen[p.Provider] {
			return
		}
		seen[p.Provider] = true
		out = append(out, p)
	}
	for _, f := range piBundleChunks() {
		raw, err := os.ReadFile(f)
		if err != nil || len(raw) == 0 || len(raw) > 50<<20 {
			continue
		}
		parsed := parsePiEnvMap(string(raw))
		for _, p := range parsed {
			add(p)
		}
		if len(parsed) > 0 {
			break // the env map lives in a single chunk when anchor is present
		}
	}
	for _, p := range piSpecialEnvs {
		add(p)
	}
	return out
}

// uniqueEnvSlots gives every provider its own keystore slot. pi lets twins
// share one env var (opencode/opencode-go, moonshotai/moonshotai-cn, the two
// Cloudflare and Qwen token plan providers), but pitago's keystore is keyed
// by Env, so twins would show and switch each other's keys. The first
// provider keeps pi's env var; later twins get <ID>_API_KEY (matching the
// names pitago used before, e.g. OPENCODE_GO_API_KEY). pi itself reads the
// key from auth.json under the provider id, so the slot name never reaches it.
func uniqueEnvSlots(ps []ProviderEnv) []ProviderEnv {
	used := map[string]bool{}
	for i, p := range ps {
		if used[p.Env] {
			ps[i].Env = strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(p.Provider)) + "_API_KEY"
		}
		used[ps[i].Env] = true
	}
	return ps
}

// mergeProviders unions catalogs deduped by provider id, primaries first.
func mergeProviders(primary, fallback []ProviderEnv) []ProviderEnv {
	seen := map[string]bool{}
	var out []ProviderEnv
	for _, p := range primary {
		if p.Provider != "" && !seen[p.Provider] {
			seen[p.Provider] = true
			out = append(out, p)
		}
	}
	for _, p := range fallback {
		if p.Provider != "" && !seen[p.Provider] {
			seen[p.Provider] = true
			out = append(out, p)
		}
	}
	return out
}

var piProvidersOnce sync.Once
var piProvidersCache []ProviderEnv

// ProviderEnvsAll is the live provider catalog: pi's installed bundle first
// (pi updates flow through with no pitago change), static ProviderEnvs as
// fallback. OAuth-only providers are NOT here — see AllLoginProviders.
func ProviderEnvsAll() []ProviderEnv {
	piProvidersOnce.Do(func() {
		piProvidersCache = uniqueEnvSlots(mergeProviders(discoverPiProviders(), ProviderEnvs))
	})
	return piProvidersCache
}
