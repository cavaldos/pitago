package builtin

import (
	"testing"

	"pitago/src/app"
)

// Pi's TUI has 23 BUILTIN_SLASH_COMMANDS; pitago adds /recent + /yank on top
// (/copy is re-implemented as yank: chat-only clipboard, no pi TUI needed).
// The registry is the single source of truth (popup + Enter dispatch).
func TestAllCoversPi(t *testing.T) {
	got := map[string]string{}
	for _, c := range All() {
		got[c.Name] = c.Origin
	}
	for _, want := range []string{
		"settings", "model", "tree", "thinking", "scoped-models",
		"export", "import", "share", "copy", "name", "session",
		"changelog", "hotkeys", "fork", "clone", "trust",
		"login", "logout", "new", "compact", "resume", "reload", "quit",
		"recent", "yank", "sidebar", "update", "plugins", "mouse", "theme", "pitago-setting",
		"trajectory", "subagents",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("builtin /%s missing", want)
		}
	}
	if got["recent"] != OriginPitago {
		t.Error("/recent must be marked pitago origin")
	}
	if got["model"] != OriginPi {
		t.Error("/model must be marked pi origin")
	}
}

func TestConfirmSecretTrimsAndEmitsKey(t *testing.T) {
	m := &app.Model{}
	m.Dialogs = []*app.Dialog{{
		Kind: "secret", Title: "API key — groq",
		Filter: "  gsk-abc  ", LoginProvider: "groq", LoginEnv: "GROQ_API_KEY",
	}}
	_, cmd := confirmSecret(m, m.Dialogs[0], 0)
	if cmd == nil {
		t.Fatal("expected LoginKeyMsg cmd")
	}
	km, ok := cmd().(app.LoginKeyMsg)
	if !ok {
		t.Fatalf("expected LoginKeyMsg, got %T", cmd())
	}
	if km.Key != "gsk-abc" || km.Env != "GROQ_API_KEY" || km.Provider != "groq" {
		t.Errorf("unexpected LoginKeyMsg %+v", km)
	}
	if len(m.Dialogs) != 0 {
		t.Error("secret dialog should be popped")
	}
}
