package main

import (
	"os"
	"path/filepath"
	"testing"

	"pitago/src/app"
	"pitago/src/pirpc"
)

func TestResolveDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// no args → current directory
	if got, err := resolveDir("", nil); err != nil || got != cwd {
		t.Errorf("empty = %q, %v; want %q", got, err, cwd)
	}
	// explicit dir, relative resolves against launch cwd
	tmp := t.TempDir()
	rel, err := filepath.Rel(cwd, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveDir("", []string{rel}); err != nil || got != tmp {
		t.Errorf("relative = %q, %v; want %q", got, err, tmp)
	}
	// --cwd wins over positional
	other := t.TempDir()
	if got, err := resolveDir(other, []string{tmp}); err != nil || got != other {
		t.Errorf("--cwd = %q, %v; want %q", got, err, other)
	}
	// ~ expands to home
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveDir("~", nil); err != nil || got != home {
		t.Errorf("~ = %q, %v; want %q", got, err, home)
	}
	// missing path errors
	if _, err := resolveDir("", []string{filepath.Join(tmp, "nope")}); err == nil {
		t.Error("missing dir should error")
	}
	// file (not dir) errors
	f := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDir(f, nil); err == nil {
		t.Error("file path should error")
	}
}

func TestApplyRestoredModel(t *testing.T) {
	ref := &app.ModelRef{Provider: "opencode", ID: "space-bunny-free"}

	// empty spawn options + saved pick → restored
	opts := pirpc.Options{}
	if !applyRestoredModel(&opts, app.Prefs{CurrentModel: ref}) {
		t.Fatal("expected a restore")
	}
	if opts.Provider != "opencode" || opts.Model != "space-bunny-free" {
		t.Errorf("opts = %+v, want the saved model", opts)
	}

	// explicit flags win, per field
	opts = pirpc.Options{Provider: "anthropic", Model: "claude"}
	applyRestoredModel(&opts, app.Prefs{CurrentModel: ref})
	if opts.Provider != "anthropic" || opts.Model != "claude" {
		t.Errorf("opts = %+v, want the explicit flags untouched", opts)
	}
	// a single explicit flag still lets the other half restore
	opts = pirpc.Options{Model: "claude"}
	applyRestoredModel(&opts, app.Prefs{CurrentModel: ref})
	if opts.Provider != "opencode" || opts.Model != "claude" {
		t.Errorf("opts = %+v, want provider filled, model kept", opts)
	}

	// nothing saved → nothing applied
	opts = pirpc.Options{}
	if applyRestoredModel(&opts, app.Prefs{}) {
		t.Error("no saved model must report no restore")
	}
	if opts.Provider != "" || opts.Model != "" {
		t.Errorf("opts = %+v, want untouched", opts)
	}

	// a legacy prefs.json (flat model keys, no currentModel) → no restore
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, []byte(`{"modelProvider":"opencode","modelID":"space-bunny-free"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts = pirpc.Options{}
	if applyRestoredModel(&opts, app.LoadPrefs(path)) {
		t.Error("legacy prefs must not restore a model")
	}
	if opts.Provider != "" || opts.Model != "" {
		t.Errorf("opts = %+v, want untouched for legacy prefs", opts)
	}
}
