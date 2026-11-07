package pirpc

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func writePiOAuth(t *testing.T, prov, acct, refresh string) {
	t.Helper()
	m := loadPiRaw(PiAuthPath())
	b, _ := json.Marshal(map[string]any{"type": "oauth", "access": "a-" + refresh,
		"refresh": refresh, "expires": 1, "accountId": acct})
	m[prov] = b
	if err := savePiRaw(PiAuthPath(), m); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthMultiAccount(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	keyPath := filepath.Join(t.TempDir(), "keys.json")

	writePiOAuth(t, "anthropic", "one@x", "r1")
	SyncOAuthFromPi(keyPath, true)
	// a new login replaces pi's entry; the old account must survive
	writePiOAuth(t, "anthropic", "two@x", "r2")
	SyncOAuthFromPi(keyPath, true)

	accts, active := ListOAuthAccounts(keyPath, "anthropic")
	if len(accts) != 2 || active != 1 {
		t.Fatalf("want 2 accounts active=1, got %d active=%d", len(accts), active)
	}
	// refreshed tokens of a known account update in place
	writePiOAuth(t, "anthropic", "two@x", "r2b")
	SyncOAuthFromPi(keyPath, false)
	if accts, _ = ListOAuthAccounts(keyPath, "anthropic"); len(accts) != 2 {
		t.Fatalf("refresh duplicated the account: %d", len(accts))
	}

	if err := SwitchOAuth(keyPath, "anthropic", 0); err != nil {
		t.Fatal(err)
	}
	if _, acct, ok := PiOAuth("anthropic"); !ok || acct != "one@x" {
		t.Fatalf("pi should follow account 0, got %q ok=%v", acct, ok)
	}
	if _, active = ListOAuthAccounts(keyPath, "anthropic"); active != 0 {
		t.Fatalf("active = %d", active)
	}

	// deleting the active one falls back to the other; last one logs out
	if err := DeleteOAuthAt(keyPath, "anthropic", 0); err != nil {
		t.Fatal(err)
	}
	if _, acct, _ := PiOAuth("anthropic"); acct != "two@x" {
		t.Fatalf("fallback account = %q", acct)
	}
	if err := DropActiveOAuth(keyPath, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := PiOAuth("anthropic"); ok {
		t.Fatal("pi should be logged out")
	}
}
