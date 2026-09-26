package pirpc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeystore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if got := LoadKeys(path); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
	if err := SaveKey(path, "OPENROUTER_API_KEY", "sk-test"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}
	keys := LoadKeys(path)
	if keys["OPENROUTER_API_KEY"] != "sk-test" {
		t.Fatalf("got %v", keys)
	}
	if LookupEnv("openrouter") != "OPENROUTER_API_KEY" {
		t.Fatal("lookup fail")
	}
	if LookupEnv("nope") != "" {
		t.Fatal("lookup should be empty")
	}
	if err := DeleteKey(path, "OPENROUTER_API_KEY"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(LoadKeys(path)) != 0 {
		t.Fatal("expected empty after delete")
	}
}

// Several keys per provider: SaveKey appends + activates, LoadKeys exposes
// the active one, SetActive switches, DeleteKeyAt drops one.
func TestKeystoreMulti(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	for _, k := range []string{"sk-first-1111", "sk-second-2222"} {
		if err := SaveKey(path, "GROQ_API_KEY", k); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	keys, active := ListKeys(path, "GROQ_API_KEY")
	if len(keys) != 2 || active != 1 {
		t.Fatalf("keys=%v active=%d", keys, active)
	}
	if got := LoadKeys(path)["GROQ_API_KEY"]; got != "sk-second-2222" {
		t.Fatalf("active = %q", got)
	}
	// re-saving an existing key dedupes + reactivates instead of duplicating
	if err := SaveKey(path, "GROQ_API_KEY", "sk-first-1111"); err != nil {
		t.Fatalf("save dup: %v", err)
	}
	if keys, active := ListKeys(path, "GROQ_API_KEY"); len(keys) != 2 || active != 0 {
		t.Fatalf("dedupe: keys=%v active=%d", keys, active)
	}
	if err := SetActive(path, "GROQ_API_KEY", 1); err != nil {
		t.Fatalf("setactive: %v", err)
	}
	if got := LoadKeys(path)["GROQ_API_KEY"]; got != "sk-second-2222" {
		t.Fatalf("after switch = %q", got)
	}
	// delete inactive key: stays open on the active one
	if err := DeleteKeyAt(path, "GROQ_API_KEY", 0); err != nil {
		t.Fatalf("delete at: %v", err)
	}
	if keys, active := ListKeys(path, "GROQ_API_KEY"); len(keys) != 1 || active != 0 {
		t.Fatalf("after delete: keys=%v active=%d", keys, active)
	}
	// delete last key drops the entry
	if err := DeleteKeyAt(path, "GROQ_API_KEY", 0); err != nil {
		t.Fatalf("delete last: %v", err)
	}
	if ks, active := ListKeys(path, "GROQ_API_KEY"); len(ks) != 0 || active != -1 {
		t.Fatalf("expected empty, got %v %d", ks, active)
	}
	if len(LoadKeys(path)) != 0 {
		t.Fatal("expected empty active map")
	}
}

// Old {"ENV":"key"} files migrate to multi-key with that key active.
func TestKeystoreMigratesOldFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"GROQ_API_KEY":"gsk-old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, active := ListKeys(path, "GROQ_API_KEY")
	if len(keys) != 1 || active != 0 || keys[0] != "gsk-old" {
		t.Fatalf("migrated: %v %d", keys, active)
	}
	if got := LoadKeys(path)["GROQ_API_KEY"]; got != "gsk-old" {
		t.Fatalf("active = %q", got)
	}
	if err := SaveKey(path, "GROQ_API_KEY", "gsk-new-12345678"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if keys, _ := ListKeys(path, "GROQ_API_KEY"); len(keys) != 2 {
		t.Fatalf("after append: %v", keys)
	}
}

func TestMaskKeyHidesSecret(t *testing.T) {
	full := "sk-ant-api03-abcdefgh12345678"
	m := MaskKey(full)
	if m == full || len(m) >= len(full) {
		t.Fatalf("not masked: %q", m)
	}
	for _, part := range []string{full[8:12], full[len(full)-8:]} {
		if len(part) > 4 && containsStr(m, part) {
			t.Fatalf("mask leaks %q in %q", part, m)
		}
	}
	if MaskKey("short") != "••••••" {
		t.Fatalf("short mask = %q", MaskKey("short"))
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// The keystore holds the user's API keys and is read right back after
// every write, so it is swapped atomically too (same reason as auth.json).
func TestSaveKeyIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	for i := 0; i < 20; i++ {
		if err := SaveKey(path, "GROQ_API_KEY", "gsk-iter-12345678"); err != nil {
			t.Fatal(err)
		}
		store := LoadStore(path)
		e := store["GROQ_API_KEY"]
		if e == nil || len(e.Keys) != 1 || e.Keys[0].Key != "gsk-iter-12345678" {
			t.Fatalf("iteration %d: keystore = %+v", i, store)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "keys.json" {
		t.Fatalf("stray temp files in the config dir: %d entries", len(entries))
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("keys.json perm = %o, want 600", fi.Mode().Perm())
	}
}
