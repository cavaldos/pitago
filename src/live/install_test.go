package live

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// setHome points the user home at a temp dir. Tests must never write to the
// developer's real ~/.pi/agent/extensions, which is a live installation.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		// os.UserHomeDir reads %USERPROFILE% on Windows.
		t.Setenv("USERPROFILE", home)
	}
	return home
}

func installedPath(t *testing.T) string {
	t.Helper()
	home := setHome(t)
	return filepath.Join(home, ".pi", "agent", "extensions", "pitago-live-bridge.ts")
}

// The whole point of the feature: one command leaves the bridge where pi will
// pick it up, so the user never has to run a script by hand again.
func TestInstallBridgeWritesEmbeddedSource(t *testing.T) {
	path := installedPath(t)
	// The extensions directory usually does not exist on a fresh machine.
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Skipf("unexpected pre-existing extensions dir: %v", err)
	}
	current, err := InstallBridge()
	if err != nil {
		t.Fatalf("InstallBridge: %v", err)
	}
	if !current {
		t.Fatal("a fresh install must report current")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != BridgeSource() {
		t.Fatal("installed file is not the embedded bridge source")
	}
	// Program text loaded by pi as the same user: readable, and explicitly
	// NOT the 0600 session descriptor.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("installed mode %v, want 0644", perm)
	}
	if !BridgeInstalled() {
		t.Fatal("BridgeInstalled must report true after an install")
	}
}

// Idempotent: calling again must not rewrite, so pi is never handed a
// half-written module and the file's mtime does not churn.
func TestInstallBridgeIsIdempotent(t *testing.T) {
	path := installedPath(t)
	if _, err := InstallBridge(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := InstallBridge()
	if err != nil || !current {
		t.Fatalf("second install: current=%v err=%v", current, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("unchanged content must not be rewritten")
	}
}

// A stale or truncated copy of our own extension at our own filename is worse
// than none: pi would fail to load it and the user would see no /live at all
// with no clue why. Replacing it is safe precisely because the name is
// pitago's — nothing else should own pitago-live-bridge.ts in that directory.
func TestInstallBridgeReplacesPreexistingDifferentFile(t *testing.T) {
	path := installedPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// older, broken bridge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := InstallBridge()
	if err != nil || !current {
		t.Fatalf("replace: current=%v err=%v", current, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != BridgeSource() {
		t.Fatal("a different file at pitago's own path must be replaced by the embedded source")
	}
}

func TestBridgeInstalledFalseWhenAbsent(t *testing.T) {
	setHome(t)
	if BridgeInstalled() {
		t.Fatal("BridgeInstalled must be false before any install")
	}
}

// A container or service unit may have no HOME. Guessing the path from other
// variables risks writing outside the user's home, so this must be a clear
// error, never a silent write and never a panic.
func TestInstallBridgeFailsClearlyWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	// os.UserHomeDir falls back to other sources on some platforms; only
	// assert the failure when there genuinely is no home to find.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		t.Skipf("this platform resolves a home without HOME: %q", home)
	}
	if current, err := InstallBridge(); err == nil || current {
		t.Fatalf("expected a clear error with no home, got current=%v err=%v", current, err)
	}
}

// A directory sitting at the install path means something is badly wrong;
// truncating through it must be refused rather than attempted.
func TestInstallBridgeRefusesNonRegularTarget(t *testing.T) {
	path := installedPath(t)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBridge(); err == nil {
		t.Fatal("installing onto a directory must fail")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("the directory target was disturbed: %v", err)
	}
}

func TestExtensionsDirStaysInsideHome(t *testing.T) {
	home := setHome(t)
	dir, err := extensionsDir()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || len(rel) > 1 && rel[:3] == ".."+string(filepath.Separator) {
		t.Fatalf("extensions dir %q escapes home %q", dir, home)
	}
}
