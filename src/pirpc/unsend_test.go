package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallUnsendExtWritesOnceAndKeepsMtime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pi-ext")
	path, err := InstallUnsendExtIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(unsendExtTS) {
		t.Fatal("installed file must be the embedded extension")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallUnsendExtIn(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Error("unchanged content must not be rewritten")
	}
	// A stale copy is replaced.
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallUnsendExtIn(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(unsendExtTS) {
		t.Error("a stale copy must be replaced by the embedded extension")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("install must leave only the extension behind, got %d files", len(entries))
	}
}

func TestHideInternalCommands(t *testing.T) {
	got := hideInternalCommands([]RepoCommand{{Name: "team"}, {Name: UnsendCommand}, {Name: "review"}})
	if len(got) != 2 || got[0].Name != "team" || got[1].Name != "review" {
		t.Errorf("hideInternalCommands = %+v, want team and review only", got)
	}
}

func TestHasUserMessageSince(t *testing.T) {
	msgs := []AgentMessage{
		{Role: "user", Timestamp: 100},
		{Role: "assistant", Timestamp: 300},
	}
	if !hasUserMessageSince(msgs, 100) {
		t.Error("a user message stamped at the send time is still there")
	}
	if hasUserMessageSince(msgs, 101) {
		t.Error("an older user message and a newer assistant one must not count")
	}
}

// One odd timestamp must never fail the whole get_messages decode.
func TestMillisDecodesLeniently(t *testing.T) {
	var msgs []AgentMessage
	raw := `[{"role":"user","timestamp":1792980011819},{"role":"custom","timestamp":"2026-10-26T02:00:11.821Z"},{"role":"user","timestamp":12.7},{"role":"user","timestamp":null}]`
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []Millis{1792980011819, 0, 12, 0}
	for i, w := range want {
		if msgs[i].Timestamp != w {
			t.Errorf("msgs[%d].Timestamp = %d, want %d", i, msgs[i].Timestamp, w)
		}
	}
}
