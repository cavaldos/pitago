package pirpc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteSessionRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	for _, p := range []string{
		"",
		"relative.jsonl",
		"../../etc/critical.jsonl",
		filepath.Join(dir, "..", "escape.jsonl"),
		"/etc/critical.jsonl",
		"/tmp/x.txt",
		filepath.Join(dir, "nope.txt"),
	} {
		if err := DeleteSession(p); err == nil {
			t.Fatalf("DeleteSession(%q) should refuse", p)
		}
	}
	// legit file under root deletes fine
	ok := filepath.Join(dir, "2026-09-20T08-00-00-000Z_aaa.jsonl")
	if err := os.WriteFile(ok, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(ok); err != nil {
		t.Fatalf("legit delete failed: %v", err)
	}
}
