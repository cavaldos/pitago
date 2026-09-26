package pirpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain pins the package tests to a throwaway pi agent directory.
//
// It is structural isolation, not per-test hygiene: every test in this
// package (and every future one) runs with PI_CODING_AGENT_DIR and
// PI_CODING_AGENT_SESSION_DIR pointing at a temp tree that is removed on
// exit. PiAgentDir/PiSettingsPath/auth/session helpers resolve through those
// variables, so a test can no more read — or, before the refuse-on-garbage
// fix, overwrite — the developer's real ~/.pi/agent/settings.json (which
// holds pi's defaultProvider/defaultModel/defaultThinkingLevel/packages/
// theme). A test that forgets to set them is isolated by construction instead
// of by review.
//
// HOME is deliberately NOT overridden: the RPC tests spawn the real pi when
// it is installed, and a fresh HOME would leave pi with no configured
// provider. Only pi's own data directories are redirected.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pitago-pirpc-agent-")
	if err != nil {
		panic("pirpc tests: temp agent dir: " + err.Error())
	}
	os.Setenv("PI_CODING_AGENT_DIR", dir)
	os.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(dir, "sessions"))
	// The child-env overlay is process-global; start from a clean slate so
	// one test cannot leak a pushed key into the next.
	clearPiChildEnv()

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// The isolation above must actually hold: pi's settings path resolves inside
// the temp tree, and the child-environment overlay starts empty.
func TestTestEnvIsIsolatedFromRealPi(t *testing.T) {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		t.Fatal("PI_CODING_AGENT_DIR not pinned by TestMain")
	}
	if !strings.HasPrefix(PiAgentDir(), dir) {
		t.Errorf("PiAgentDir = %q, want under %q", PiAgentDir(), dir)
	}
	path := PiSettingsPath()
	if path == "" || !strings.HasPrefix(path, dir) {
		t.Fatalf("PiSettingsPath = %q, want under %q", path, dir)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("isolated settings.json already exists: %v", err)
	}
	if _, ok := PiChildEnv("OPENAI_API_KEY"); ok {
		t.Error("child env overlay is not empty at test start")
	}
}
