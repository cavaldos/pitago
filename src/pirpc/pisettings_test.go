package pirpc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPiSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")

	if err := SetPiSettingAt(file, "terminal.showImages", false); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := SetPiSettingAt(file, "enableSkillCommands", true); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := ReadPiSettingsAt(file)
	if PiBool(cfg, "terminal.showImages", true) {
		t.Error("showImages should be false")
	}
	if !PiBool(cfg, "enableSkillCommands", false) {
		t.Error("skillCommands should be true")
	}
	// absent keys fall back to pi defaults
	if PiBool(cfg, "images.blockImages", false) {
		t.Error("blockImages default should be false")
	}
	if got := PiInt(cfg, "terminal.imageWidthCells", 60); got != 60 {
		t.Errorf("imageWidth default should be 60, got %d", got)
	}
	if got := PiString(cfg, "transport", "auto"); got != "auto" {
		t.Errorf("transport default should be auto, got %q", got)
	}
	// existing keys survive a second write
	if err := SetPiSettingAt(file, "terminal.imageWidthCells", 120); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg = ReadPiSettingsAt(file)
	if PiBool(cfg, "terminal.showImages", true) {
		t.Error("showImages lost after second write")
	}
	if got := PiInt(cfg, "terminal.imageWidthCells", 60); got != 120 {
		t.Errorf("imageWidth should be 120, got %d", got)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("new file should be 0644, got %v", fi.Mode())
	}
	// An existing (possibly user-tightened) mode is preserved, like a plain
	// in-place write did.
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("existing mode should be kept, got %v", fi.Mode())
	}
}

// pi owns settings.json too, so a pitago write must not reshuffle the
// file: key order, unknown keys and untouched values survive verbatim, and
// a new key is appended instead of being sorted into the middle.
func TestSetPiSettingPreservesOrderAndForeignKeys(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	foreign := `{
  "zeta": {"b": 1, "a": [1,2,{"deep":true}]},
  "alpha": "keep me",
  "transport": "auto",
  "theme": {"name":"dark","nested":{"x":1}}
}
`
	if err := os.WriteFile(file, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "transport", "websocket"); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "theme.nested.y", 2); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "brandNew", true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	want := `{
  "zeta": {
    "b": 1,
    "a": [
      1,
      2,
      {
        "deep": true
      }
    ]
  },
  "alpha": "keep me",
  "transport": "websocket",
  "theme": {
    "name": "dark",
    "nested": {
      "x": 1,
      "y": 2
    }
  },
  "brandNew": true
}
`
	if got != want {
		t.Fatalf("settings.json reshuffled:\n got:\n%s\nwant:\n%s", got, want)
	}
	cfg := ReadPiSettingsAt(file)
	if !PiBool(cfg, "brandNew", false) {
		t.Error("brandNew should read back true")
	}
	if got := PiInt(cfg, "theme.nested.y", 0); got != 2 {
		t.Errorf("nested write = %d", got)
	}
	if PiString(cfg, "alpha", "") != "keep me" {
		t.Error("unrelated key lost")
	}
}

// The four session.* toggles pi persists itself must not be written twice:
// a second writer only races pi's SettingsManager save and can lose it.
func TestSetPiSettingSkipsPiPersistedPaths(t *testing.T) {
	for _, path := range []string{"steeringMode", "followUpMode", "compaction.enabled", "retry.enabled"} {
		if !PiPersistsSetting(path) {
			t.Fatalf("%s should be marked pi-persisted", path)
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "settings.json")
		seed := `{"steeringMode":"all","retry":{"enabled":true}}`
		if err := os.WriteFile(file, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SetPiSettingAt(file, path, false); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		raw, _ := os.ReadFile(file)
		if string(raw) != seed {
			t.Errorf("%s: pi-owned file was rewritten:\n got %s\nwant %s", path, raw, seed)
		}
	}
	if PiPersistsSetting("terminal.showImages") {
		t.Error("file-backed rows are pitago-written, not pi-persisted")
	}
}

// The settings write must be atomic: no temp file may survive a successful
// write, and a concurrent reader only ever sees a complete JSON document.
func TestSetPiSettingLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("stray temp files left behind: %v", names)
	}
	// An unparseable file is NOT replaced any more: settings.json is pi's
	// own configuration, so the write is refused and the bytes on disk stay
	// exactly as they were (see the refusal tests below).
	if err := os.WriteFile(file, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "transport", "sse"); err == nil {
		t.Fatal("expected a refusal for an unparseable settings.json")
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != "{not json" {
		t.Fatalf("unparseable settings.json was rewritten:\n%s", raw)
	}
}

// THE data-loss guard: settings.json holds pi's own configuration
// (defaultProvider, defaultModel, defaultThinkingLevel, packages, theme).
// The old behaviour started from an empty object when the file did not parse
// and rewrote it, so a single unparseable byte (a stray comma, a comment, a
// half-finished hand edit) silently deleted the user's whole pi setup. The
// write must now be refused with the file untouched.
func TestSetPiSettingRefusesUnreadableFile(t *testing.T) {
	for _, broken := range []string{
		"{not json",
		`{"theme":"dark",}`,        // trailing comma
		`{"a":1} trailing garbage`, // junk after the document
		`[1,2,3]`,                  // valid JSON, wrong shape
		`"just a string"`,
	} {
		dir := t.TempDir()
		file := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(file, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		err := SetPiSettingAt(file, "terminal.showImages", false)
		if !errors.Is(err, ErrUnreadableSettings) {
			t.Errorf("%q: err = %v, want ErrUnreadableSettings", broken, err)
		}
		raw, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatalf("%q: %v", broken, readErr)
		}
		if string(raw) != broken {
			t.Errorf("%q: file was modified:\n%s", broken, raw)
		}
		if fi, statErr := os.Stat(file); statErr != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%q: permissions changed: %v", broken, fi.Mode())
		}
		// An empty file is not a refusal: there is nothing to lose.
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
			t.Errorf("empty file should be writable, got %v", err)
		}
	}
}

// Overwriting a non-object with an object (writing "theme.name" when the
// user's settings have "theme":"dark") is a silent change of their pi
// configuration. It is refused instead.
func TestSetPiSettingRefusesNonObjectParent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	seed := `{"theme":"dark","terminal":{"showImages":true}}`
	if err := os.WriteFile(file, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	err := SetPiSettingAt(file, "theme.name", "light")
	if !errors.Is(err, ErrSettingConflict) {
		t.Fatalf("err = %v, want ErrSettingConflict", err)
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != seed {
		t.Errorf("file was modified:\n%s", raw)
	}
	// A JSON null is an absent value, not a conflict: the write lands.
	if err := os.WriteFile(file, []byte(`{"theme":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "theme.name", "light"); err != nil {
		t.Fatalf("null parent should be writable, got %v", err)
	}
	if got := PiString(ReadPiSettingsAt(file), "theme.name", ""); got != "light" {
		t.Errorf("theme.name = %q", got)
	}
}

// pi parses its JSON config as JSON.parse(stripBom(content)) (verified in
// the installed pi 0.87.1 bundle, the same stripBom for settings.json and
// auth.json), so a BOM-prefixed settings.json is a VALID pi file — editors
// and PowerShell's `>` write UTF-8-with-BOM by default. Treating it as
// garbage would hide the user's real configuration from /settings and then
// REFUSE the write with ErrUnreadableSettings, i.e. refuse a file pi itself
// edits. The BOM is also put back on a write: pitago changes the value it was
// asked to change, nothing else.
func TestSettingsWithBOMAreReadAndWritten(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	seed := piBOM + `{"transport":"auto","defaultProvider":"anthropic","theme":{"name":"dark"}}`
	if err := os.WriteFile(file, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ReadPiSettingsAt(file)
	if cfg == nil {
		t.Fatal("a BOM-prefixed settings.json is valid for pi and must be read, not dropped")
	}
	if got := PiString(cfg, "defaultProvider", ""); got != "anthropic" {
		t.Errorf("defaultProvider = %q", got)
	}
	if got := PiString(cfg, "theme.name", ""); got != "dark" {
		t.Errorf("theme.name = %q", got)
	}
	if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
		t.Fatalf("write to a BOM settings.json: %v", err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte(piBOM)) {
		t.Errorf("the BOM must be preserved on rewrite, got %q", raw)
	}
	if got := ReadPiSettingsAt(file); PiString(got, "transport", "") != "sse" ||
		PiString(got, "theme.name", "") != "dark" {
		t.Errorf("rewrite lost pi's own keys: %s", raw)
	}
	// A BOM is not a reason to refuse, but real garbage still is.
	if err := os.WriteFile(file, []byte(piBOM+"{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "transport", "sse"); !errors.Is(err, ErrUnreadableSettings) {
		t.Errorf("BOM + unparseable = %v, want ErrUnreadableSettings", err)
	}
	// A BOM-only file is what a crashed editor leaves: nothing to lose, and
	// the write must replace it (a bare BOM is not a JSON object pi reads).
	if err := os.WriteFile(file, []byte(piBOM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
		t.Errorf("BOM-only file should be writable, got %v", err)
	}
}

// pi saves settings.json itself, so a plain read-modify-write can rename
// over pi's change. The swap is conditional on the bytes we read, and a
// mismatch is retried on the newer file — pi's write is never dropped.
func TestSetPiSettingDoesNotLoseAConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(file, []byte(`{"transport":"auto"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// pi writes its own change after pitago read the file.
	updated := []byte(`{"transport":"auto","steeringMode":"one-at-a-time"}`)
	if err := os.WriteFile(file, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	// The swap must not happen against the stale bytes.
	swapped, err := writeFileAtomicCAS(file, orig, []byte(`{"transport":"sse"}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if swapped {
		t.Error("CAS overwrote a file that changed underneath")
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != string(updated) {
		t.Errorf("pi's write was lost:\n%s", raw)
	}
	// Retrying on the newer content keeps both changes.
	if err := SetPiSettingAt(file, "transport", "sse"); err != nil {
		t.Fatal(err)
	}
	cfg := ReadPiSettingsAt(file)
	if got := PiString(cfg, "transport", ""); got != "sse" {
		t.Errorf("transport = %q", got)
	}
	if got := PiString(cfg, "steeringMode", ""); got != "one-at-a-time" {
		t.Errorf("pi's steeringMode lost: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("stray files after a CAS miss: %d", len(entries))
	}
}
