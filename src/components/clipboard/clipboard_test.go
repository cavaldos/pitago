package clipboard

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHostedSessionWorktreeID(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "w123::/tmp/fake-extensions")
	t.Setenv("ORCA_PANE_KEY", "")
	if !IsHostedSession() {
		t.Fatal("ORCA_WORKTREE_ID must mark a hosted session")
	}
}

func TestIsHostedSessionPaneKey(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "")
	t.Setenv("ORCA_PANE_KEY", "term_abc")
	if !IsHostedSession() {
		t.Fatal("ORCA_PANE_KEY must mark a hosted session")
	}
}

func TestIsHostedSessionLocal(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "")
	t.Setenv("ORCA_PANE_KEY", "")
	if IsHostedSession() {
		t.Fatal("no host env vars must mean local session")
	}
}

func TestOsc52StringEncodes(t *testing.T) {
	s := Osc52String("hello")
	if !strings.HasPrefix(s, "\x1b]52;c;") {
		t.Fatalf("expected OSC 52 clipboard prefix, got %q", s)
	}
	if !strings.Contains(s, "aGVsbG8=") { // base64("hello")
		t.Fatalf("expected base64 payload in %q", s)
	}
	if !strings.HasSuffix(s, "\x07") {
		t.Fatalf("expected BEL terminator in %q", s)
	}
}

// The OSC 52 sequence is captured, never emitted: a live one would replace
// the clipboard of the terminal running `go test`.
func TestWriteHostedUsesOsc52(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "w")
	t.Setenv("ORCA_PANE_KEY", "")
	var out bytes.Buffer
	defer captureOsc52(&out)()

	st := Write("hello")
	if st.Channel != Osc52 {
		t.Fatalf("hosted session must use OSC 52, got %q", st.Channel)
	}
	if st.Chars != 5 || st.Bytes != 5 {
		t.Fatalf("expected 5 chars/5 bytes, got %d/%d", st.Chars, st.Bytes)
	}
	if st.Err != nil {
		t.Fatalf("unexpected err: %v", st.Err)
	}
	if got := out.String(); got != Osc52String("hello") {
		t.Fatalf("sequence not written to the terminal stream: %q", got)
	}
}

// The local branch of writeClipboard: no host env vars, so it must reach the
// system clipboard and never the OSC 52 stream.
func TestWriteLocalDoesNotUseOsc52(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "")
	t.Setenv("ORCA_PANE_KEY", "")
	var out bytes.Buffer
	defer captureOsc52(&out)()
	// The atotto/CLI backends are stubbed via the real transport's own
	// helpers so the test cannot touch the developer's clipboard.
	prevAtotto := clipboardWriteAll
	defer func() { clipboardWriteAll = prevAtotto }()
	var wrote string
	clipboardWriteAll = func(text string) error {
		wrote = text
		return nil
	}

	st := writeClipboard("hello")
	if st.Channel != Atoto {
		t.Fatalf("local session must use the system clipboard, got %q", st.Channel)
	}
	if wrote != "hello" {
		t.Fatalf("text not passed to the clipboard backend: %q", wrote)
	}
	if out.Len() != 0 {
		t.Fatalf("local session must not emit an OSC 52 sequence: %q", out.String())
	}
	if st.Chars != 5 || st.Bytes != 5 {
		t.Fatalf("expected 5 chars/5 bytes, got %d/%d", st.Chars, st.Bytes)
	}
}

func TestOversizedOsc52FailsHonestly(t *testing.T) {
	var out bytes.Buffer
	defer captureOsc52(&out)()
	st := writeOsc52(strings.Repeat("x", MaxOsc52+1))
	if st.Channel != None {
		t.Fatalf("oversized OSC 52 must not report delivery, got %q", st.Channel)
	}
	if st.Err == nil {
		t.Fatal("oversized OSC 52 must explain the failure")
	}
	if out.Len() != 0 {
		t.Fatalf("oversized payload must not reach the terminal: %q", out.String())
	}
}

// captureOsc52 redirects the OSC 52 stream for the duration of a test and
// returns a restore func. No test in this package may emit a live sequence.
func captureOsc52(w *bytes.Buffer) func() {
	prev := osc52Out
	osc52Out = w
	return func() { osc52Out = prev }
}

// stubExternal swaps the helper list, PATH lookup and command runner for the
// duration of a test, so the fallback chain can be driven from any platform.
func stubExternal(t *testing.T, helpers []externalHelper, installed map[string]bool, fails map[string]bool) *[]string {
	t.Helper()
	prevHelpers, prevLook, prevRun := externalHelpers, lookPath, runCommand
	t.Cleanup(func() { externalHelpers, lookPath, runCommand = prevHelpers, prevLook, prevRun })

	externalHelpers = func() []externalHelper { return helpers }
	var tried []string
	lookPath = func(bin string) (string, error) {
		if installed[bin] {
			return "/fake/" + bin, nil
		}
		return "", errors.New("not found")
	}
	runCommand = func(cmd *exec.Cmd) error {
		bin := filepath.Base(cmd.Path)
		tried = append(tried, bin)
		if fails[bin] {
			return errors.New("helper failed: " + bin)
		}
		return nil
	}
	return &tried
}

// A helper can be installed and still unusable — wl-copy on a box with no
// Wayland session is the real case. Stopping at the first installed helper
// would report failure over an xclip that would have worked.
func TestWriteExternalTriesEveryInstalledHelper(t *testing.T) {
	helpers := []externalHelper{
		{bin: "wl-copy"},
		{bin: "xclip", args: []string{"-i", "-selection", "clipboard"}},
		{bin: "xsel", args: []string{"-i", "--clipboard"}},
	}
	tried := stubExternal(t, helpers,
		map[string]bool{"wl-copy": true, "xclip": true, "xsel": true},
		map[string]bool{"wl-copy": true})

	st := writeExternal("hello")
	if st.Channel != Atoto {
		t.Fatalf("a later working helper must deliver, got %q (err %v)", st.Channel, st.Err)
	}
	if len(*tried) != 2 || (*tried)[0] != "wl-copy" || (*tried)[1] != "xclip" {
		t.Errorf("expected to fall through wl-copy to xclip, tried %v", *tried)
	}
	if st.Chars != 5 || st.Bytes != 5 {
		t.Errorf("expected 5 chars/5 bytes, got %d/%d", st.Chars, st.Bytes)
	}
}

// Every helper failing must still explain itself rather than reporting a
// bare failure.
func TestWriteExternalReportsLastErrorWhenAllFail(t *testing.T) {
	helpers := []externalHelper{{bin: "wl-copy"}, {bin: "xclip"}, {bin: "xsel"}}
	tried := stubExternal(t, helpers,
		map[string]bool{"wl-copy": true, "xclip": true, "xsel": true},
		map[string]bool{"wl-copy": true, "xclip": true, "xsel": true})

	st := writeExternal("hello")
	if st.Channel != None {
		t.Fatalf("all helpers failing must not report delivery, got %q", st.Channel)
	}
	if st.Err == nil || !strings.Contains(st.Err.Error(), "xsel") {
		t.Errorf("want the last helper's error, got %v", st.Err)
	}
	if len(*tried) != 3 {
		t.Errorf("expected all three helpers tried, got %v", *tried)
	}
}

// Nothing installed is not an error: the caller reports "no backend", which is
// a different message from "the backend failed".
func TestWriteExternalNoHelperInstalledIsNotAnError(t *testing.T) {
	helpers := []externalHelper{{bin: "wl-copy"}, {bin: "xclip"}}
	stubExternal(t, helpers, map[string]bool{}, nil)

	st := writeExternal("hello")
	if st.Channel != None || st.Err != nil {
		t.Fatalf("no installed helper must be None with no error, got %q / %v", st.Channel, st.Err)
	}
}
