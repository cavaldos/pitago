package pirpc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Multi-account OAuth. pi's auth.json holds exactly ONE entry per provider,
// so a new /login used to overwrite the previous subscription. pitago keeps
// every OAuth login it has seen in its own store (next to the API keystore,
// 0600) and swaps the chosen one into pi's auth.json — the OAuth twin of the
// API-key list/rotate flow.

// OAuthAccount is one saved subscription login. Cred is pi's auth.json entry
// verbatim ({"type":"oauth","access":...,"refresh":...,...}), so provider
// specific fields survive the round trip untouched.
type OAuthAccount struct {
	Cred    json.RawMessage `json:"cred"`
	Name    string          `json:"name,omitempty"`
	Account string          `json:"account,omitempty"`
	Expires int64           `json:"expires,omitempty"`
	AddedAt int64           `json:"addedAt,omitempty"`
}

// OAuthEntry is one provider's accounts + the one pi currently uses.
type OAuthEntry struct {
	Accounts []OAuthAccount `json:"accounts"`
	Active   int            `json:"active"`
}

// Label is the display name: custom name, else account id/email, else "".
func (a OAuthAccount) Label() string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	return a.Account
}

// OAuthStorePath is the OAuth account store that sits beside keyPath.
func OAuthStorePath(keyPath string) string {
	if keyPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(keyPath), "oauth_accounts.json")
}

func loadOAuthStore(path string) map[string]*OAuthEntry {
	out := map[string]*OAuthEntry{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var m map[string]*OAuthEntry
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for prov, e := range m {
		if e == nil || len(e.Accounts) == 0 {
			continue
		}
		if e.Active < 0 || e.Active >= len(e.Accounts) {
			e.Active = 0
		}
		out[prov] = e
	}
	return out
}

func saveOAuthStore(path string, m map[string]*OAuthEntry) error {
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
	return writeFileAtomic(path, raw, filePerm(path, 0o600))
}

type oauthIdent struct{ id, refresh, access string }

func identOf(raw json.RawMessage) oauthIdent {
	var v struct {
		AccountID string `json:"accountId"`
		Account   string `json:"account"`
		Email     string `json:"email"`
		Refresh   string `json:"refresh"`
		Access    string `json:"access"`
	}
	_ = json.Unmarshal(raw, &v)
	id := v.AccountID
	if id == "" {
		id = v.Account
	}
	if id == "" {
		id = v.Email
	}
	return oauthIdent{id: id, refresh: v.Refresh, access: v.Access}
}

// sameOAuth reports whether two creds are the same subscription account
// (tokens rotate on refresh, so the stable id wins when both have one).
func sameOAuth(a, b json.RawMessage) bool {
	x, y := identOf(a), identOf(b)
	if x.id != "" && y.id != "" {
		return x.id == y.id
	}
	return (x.refresh != "" && x.refresh == y.refresh) || (x.access != "" && x.access == y.access)
}

// SyncOAuthFromPi imports pi's current OAuth logins into the store.
// A known account just gets its (possibly refreshed) tokens updated; an
// unknown one is APPENDED — never overwriting an older account — and becomes
// active. fresh=true marks a login the user just did, so an unmatched cred is
// always a new account; otherwise an id-less active account is assumed to be
// the same login with rotated tokens. Returns changed provider count.
func SyncOAuthFromPi(keyPath string, fresh bool) int {
	path := OAuthStorePath(keyPath)
	if path == "" {
		return 0
	}
	live := loadPiRaw(PiAuthPath())
	store := loadOAuthStore(path)
	n := 0
	for prov, raw := range live {
		meta := piAuthMeta(prov, raw)
		if meta.Type != "oauth" {
			continue
		}
		e := store[prov]
		if e == nil {
			store[prov] = &OAuthEntry{Accounts: []OAuthAccount{newOAuthAcct(meta, raw)}}
			n++
			continue
		}
		idx := -1
		for i, a := range e.Accounts {
			if sameOAuth(a.Cred, raw) {
				idx = i
				break
			}
		}
		if idx < 0 && !fresh && identOf(e.Accounts[e.Active].Cred).id == "" && identOf(raw).id == "" {
			idx = e.Active // refresh token rotated on an id-less account
		}
		if idx < 0 {
			e.Accounts = append(e.Accounts, newOAuthAcct(meta, raw))
			e.Active = len(e.Accounts) - 1
			n++
			continue
		}
		a := &e.Accounts[idx]
		if !bytes.Equal(a.Cred, raw) || e.Active != idx {
			a.Cred, a.Expires = append(json.RawMessage(nil), raw...), meta.Expires
			if a.Account == "" {
				a.Account = meta.Account
			}
			e.Active = idx
			n++
		}
	}
	if n > 0 {
		_ = saveOAuthStore(path, store)
	}
	return n
}

func newOAuthAcct(meta PiAuthEntry, raw json.RawMessage) OAuthAccount {
	return OAuthAccount{Cred: append(json.RawMessage(nil), raw...), Account: meta.Account,
		Expires: meta.Expires, AddedAt: nowUnix()}
}

// ListOAuthAccounts returns a provider's saved accounts + active index
// (-1 = none).
func ListOAuthAccounts(keyPath, provider string) ([]OAuthAccount, int) {
	e := loadOAuthStore(OAuthStorePath(keyPath))[provider]
	if e == nil || len(e.Accounts) == 0 {
		return nil, -1
	}
	return append([]OAuthAccount(nil), e.Accounts...), e.Active
}

// OAuthCounts maps provider → number of saved accounts.
func OAuthCounts(keyPath string) map[string]int {
	out := map[string]int{}
	for prov, e := range loadOAuthStore(OAuthStorePath(keyPath)) {
		out[prov] = len(e.Accounts)
	}
	return out
}

// putOAuthInPi makes cred the provider's live entry in pi's auth.json.
func putOAuthInPi(provider string, cred json.RawMessage) error {
	path := PiAuthPath()
	m := loadPiRaw(path)
	m[provider] = cred
	return savePiRaw(path, m)
}

// SwitchOAuth activates saved account idx: pi's current tokens are captured
// first (pi refreshes them), then idx's cred replaces pi's entry.
func SwitchOAuth(keyPath, provider string, idx int) error {
	SyncOAuthFromPi(keyPath, false)
	path := OAuthStorePath(keyPath)
	store := loadOAuthStore(path)
	e := store[provider]
	if e == nil || idx < 0 || idx >= len(e.Accounts) {
		return nil
	}
	if err := putOAuthInPi(provider, e.Accounts[idx].Cred); err != nil {
		return err
	}
	e.Active = idx
	return saveOAuthStore(path, store)
}

// DeleteOAuthAt removes one saved account. Deleting the active one falls back
// to the first remaining account (pushed to pi) or, if none is left, logs
// pi out of the provider.
func DeleteOAuthAt(keyPath, provider string, idx int) error {
	SyncOAuthFromPi(keyPath, false)
	path := OAuthStorePath(keyPath)
	store := loadOAuthStore(path)
	e := store[provider]
	if e == nil || idx < 0 || idx >= len(e.Accounts) {
		return nil
	}
	wasActive := idx == e.Active
	e.Accounts = append(append([]OAuthAccount(nil), e.Accounts[:idx]...), e.Accounts[idx+1:]...)
	if len(e.Accounts) == 0 {
		delete(store, provider)
		if err := saveOAuthStore(path, store); err != nil {
			return err
		}
		return DeletePiAuth(provider)
	}
	if wasActive {
		e.Active = 0
		if err := putOAuthInPi(provider, e.Accounts[0].Cred); err != nil {
			return err
		}
	} else if e.Active > idx {
		e.Active--
	}
	return saveOAuthStore(path, store)
}

// DropActiveOAuth is the /logout teardown: it deletes the active saved
// account (falling back to the next one) and, when nothing was saved, just
// logs pi out.
func DropActiveOAuth(keyPath, provider string) error {
	SyncOAuthFromPi(keyPath, false)
	if e := loadOAuthStore(OAuthStorePath(keyPath))[provider]; e != nil {
		return DeleteOAuthAt(keyPath, provider, e.Active)
	}
	return DeletePiAuth(provider)
}

// RenameOAuth labels a saved account (trimmed, max 40 runes).
func RenameOAuth(keyPath, provider string, idx int, name string) error {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	path := OAuthStorePath(keyPath)
	store := loadOAuthStore(path)
	e := store[provider]
	if e == nil || idx < 0 || idx >= len(e.Accounts) {
		return nil
	}
	e.Accounts[idx].Name = name
	return saveOAuthStore(path, store)
}
