package pirpc

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed herdr_ext.ts
var herdrExtTS []byte

// InstallHerdrExt writes the herdr agent-state extension under the user cache
// dir and returns its path, for Options.Extensions. It mirrors herdr's own
// pi integration but runs in RPC mode too, so herdr tracks pitago's turn
// boundaries instead of guessing from process activity.
func InstallHerdrExt() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return InstallHerdrExtIn(filepath.Join(dir, "pitago", "pi-ext"))
}

func InstallHerdrExtIn(dir string) (string, error) {
	path := filepath.Join(dir, "pitago-herdr-agent-state.ts")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, herdrExtTS) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".pitago-herdr-*.ts")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(herdrExtTS); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
