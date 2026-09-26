package live

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bridgeFileName is the extension's basename. It is pitago's own name in
// pi's extensions directory, which is what makes replacing a foreign file at
// this exact path safe: nothing else should ever own "pitago-*" there, and a
// half-written or older copy of our own extension is worse than none at all
// (pi would fail to load it and the user would see no /live at all).
const bridgeFileName = "pitago-live-bridge.ts"

// extensionsDir is pi's user extension directory, where pi loads modules from
// at process start.
func extensionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		// Home is genuinely optional in a container or a systemd unit, and
		// guessing a path from other variables risks writing outside the
		// user's home. Fail loudly and let the caller explain.
		return "", errors.New("cannot locate the home directory (is HOME set?)")
	}
	return filepath.Join(home, ".pi", "agent", "extensions"), nil
}

// bridgeInstallPath is the resolved install target.
func bridgeInstallPath() (string, error) {
	dir, err := extensionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, bridgeFileName), nil
}

// BridgeInstalled reports whether the bridge extension file is already
// present. Presence only, deliberately: this gates whether /live should offer
// to install, and asking "and is it current?" would mean reading the file on
// every /live for a difference InstallBridge then reports anyway.
func BridgeInstalled() bool {
	path, err := bridgeInstallPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// InstallBridge copies the embedded bridge to ~/.pi/agent/extensions/ so any
// pi the user starts afterwards is streamable without a manual flag.
//
// It is idempotent: unchanged content is left alone (so the file's mtime does
// not churn and pi is never handed a half-written module) and still reports
// current=true.
//
// A pi process that is ALREADY running cannot gain the extension: pi resolves
// and loads its extensions once, at process start, so installing here does
// nothing for existing panes. The caller must tell the user to restart those
// sessions — otherwise the bridge looks installed while nothing streams.
//
// The file is 0644, not 0600: this is program text that pi loads as a module
// as the same user, in the same class as pitago's own binary. It carries no
// secret — the bearer token is minted by each pi process into its own
// descriptor, which is separately 0600.
func InstallBridge() (current bool, err error) {
	path, err := bridgeInstallPath()
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("cannot create %s: %w", dir, err)
	}
	// Refuse to continue if the target is not a regular file: a directory or
	// a device node there means something is badly wrong, and truncating
	// through it is exactly the mistake worth avoiding.
	if info, statErr := os.Lstat(path); statErr == nil && !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s exists and is not a regular file", path)
	}
	if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == bridgeTS {
		return true, nil
	}
	// Write to a sibling temp file and rename over the target, so a reader
	// (a pi starting concurrently) sees either the old file or the new one,
	// never a truncated extension.
	tmp, err := os.CreateTemp(dir, bridgeFileName+".*.tmp")
	if err != nil {
		return false, fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.WriteString(bridgeTS); err != nil {
		tmp.Close()
		cleanup()
		return false, fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		cleanup()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return false, err
	}
	// Windows cannot rename onto an existing file.
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return false, fmt.Errorf("cannot install %s: %w", path, err)
	}
	return true, nil
}
