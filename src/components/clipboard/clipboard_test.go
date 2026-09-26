package clipboard

import (
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

func TestWriteHostedUsesOsc52(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "w")
	t.Setenv("ORCA_PANE_KEY", "")
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
}

func TestStatusFields(t *testing.T) {
	st := Status{Channel: Atoto, Bytes: 7, Chars: 3}
	if st.Channel != Atoto || st.Bytes != 7 || st.Chars != 3 {
		t.Fatalf("status fields not preserved: %+v", st)
	}
}

func TestOversizedOsc52FailsHonestly(t *testing.T) {
	st := writeOsc52(strings.Repeat("x", MaxOsc52+1))
	if st.Channel != None {
		t.Fatalf("oversized OSC 52 must not report delivery, got %q", st.Channel)
	}
	if st.Err == nil {
		t.Fatal("oversized OSC 52 must explain the failure")
	}
}
