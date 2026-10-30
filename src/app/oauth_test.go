package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

func oauthTestModel() (Model, *Dialog) {
	m := Model{winW: 120, winH: 40}
	d := &Dialog{Kind: oauthKind, Title: "Sign in — Test", LoginProvider: "anthropic",
		oauth: &oauthState{prov: "anthropic", name: "Test", opened: true, sess: &pirpc.OAuthSession{}}}
	m.Dialogs = []*Dialog{d}
	return m, d
}

func feedOAuth(t *testing.T, m Model, d *Dialog, ev pirpc.OAuthEvent) Model {
	t.Helper()
	nm, _, ok := m.handleOAuthMsg(oauthEventMsg{D: d, S: &pirpc.OAuthSession{}, Ev: ev, OK: true})
	if !ok {
		t.Fatalf("event %q not handled", ev.Type)
	}
	return nm.(Model)
}

// The flow dialog shows the login URL, the device code and a select
// prompt's options, and ↓ moves the choice.
func TestOAuthDialogRendersEvents(t *testing.T) {
	m, d := oauthTestModel()
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "auth_url", URL: "https://example.com/auth"})
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "prompt", ID: "1", Kind: "select", Message: "Pick method",
		Options: []pirpc.OAuthOption{{ID: "browser", Label: "Browser login"}, {ID: "code", Label: "Copy code"}}})
	out := m.renderOAuthDialog(d)
	for _, want := range []string{"https://example.com/auth", "Pick method", "Browser login", "Copy code"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
	nm, _ := m.updateOAuthDialog(tea.KeyMsg{Type: tea.KeyDown}, d)
	m = nm.(Model)
	if d.Cursor != 1 {
		t.Fatalf("cursor = %d", d.Cursor)
	}
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "device_code", UserCode: "ABCD-1234", VerificationURI: "https://example.com/device"})
	if out := m.renderOAuthDialog(d); !strings.Contains(out, "ABCD-1234") {
		t.Fatalf("device code missing:\n%s", out)
	}
	if got := oauthCopyText(d.oauth); got != "ABCD-1234" {
		t.Fatalf("copy text = %q", got)
	}
}

// Text prompts collect typing (paste included, without "[...]" wrapping)
// and an error event is shown until the user closes it.
func TestOAuthDialogTextPromptAndError(t *testing.T) {
	m, d := oauthTestModel()
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "prompt", ID: "1", Kind: "manual_code", Message: "Paste redirect URL"})
	nm, _ := m.updateOAuthDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("http://x/cb?code=1"), Paste: true}, d)
	m = nm.(Model)
	if d.Filter != "http://x/cb?code=1" {
		t.Fatalf("buffer = %q", d.Filter)
	}
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "prompt_cancel", ID: "1"})
	if d.oauth.prompt != nil || d.Filter != "" {
		t.Fatalf("prompt not cleared")
	}
	m = feedOAuth(t, m, d, pirpc.OAuthEvent{Type: "error", Message: "boom"})
	if !strings.Contains(m.renderOAuthDialog(d), "boom") {
		t.Fatalf("error not rendered")
	}
	nm, _ = m.updateOAuthDialog(tea.KeyMsg{Type: tea.KeyEnter}, d)
	if len(nm.(Model).Dialogs) != 0 {
		t.Fatalf("Enter should close the failed flow")
	}
}

// Regression: flow messages arrive while the oauthFlow dialog is open and
// must get past Update's dialog gate (it used to swallow them, leaving the
// dialog stuck on "starting pi login…").
func TestOAuthMsgPassesDialogGate(t *testing.T) {
	m, d := oauthTestModel()
	nm, _ := m.Update(oauthStartedMsg{D: d, Err: errString("node not found")})
	if d.oauth.err != "node not found" {
		t.Fatalf("started msg swallowed: err=%q", d.oauth.err)
	}
	nm, _ = nm.(Model).Update(oauthEventMsg{D: d, S: d.oauth.sess, OK: true,
		Ev: pirpc.OAuthEvent{Type: "auth_url", URL: "https://example.com/a"}})
	if d.oauth.url != "https://example.com/a" {
		t.Fatalf("event msg swallowed")
	}
	_ = nm
}

type errString string

func (e errString) Error() string { return string(e) }
