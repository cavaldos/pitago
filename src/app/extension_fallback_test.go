package app

import (
	"strings"
	"testing"
)

// Generic fallback: unknown/future extension_ui_request methods surface as
// an error toast and cancel safely (Pi is nil here — must not panic),
// otherwise a new plugin hangs the agent with no visible reason.
func TestUnknownUIRequestFallsBack(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"r9","method":"frobnicate"}`))
	if len(m.toasts) != 1 || !m.toasts[0].Err {
		t.Fatalf("toasts = %+v, want one error toast", m.toasts)
	}
	if !strings.Contains(m.toasts[0].Text, "frobnicate") {
		t.Fatalf("toast must name the method: %q", m.toasts[0].Text)
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("unknown method must not open a dialog: %+v", m.Dialogs)
	}
}

// setWidget carries string arrays in RPC mode (e.g. plan-mode's PLAN
// widget — factories are ignored pi-side). A non-team, non-progress key now
// gets a real panel instead of a one-shot toast: downgrading it to a toast
// is what made pi-lens / plan-mode / web-activity vanish. Clear (omitted
// lines) retires that key alone and stays silent.
func TestSetWidgetGetsPanel(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"w1","method":"setWidget","widgetKey":"plan","widgetLines":["plan active","Step 1"]}`))
	if len(m.toasts) != 0 {
		t.Fatalf("a live widget must not become a toast: %+v", m.toasts)
	}
	p := m.extWidgetPanel("plan")
	if p == nil || len(p.Lines) != 2 || p.Lines[0] != "plan active" || p.Placement != "aboveEditor" {
		t.Fatalf("panel = %+v, want 2 lines aboveEditor", p)
	}
	m = m.handleUIRequest([]byte(`{"id":"w2","method":"setWidget","widgetKey":"plan"}`))
	if len(m.toasts) != 0 {
		t.Fatalf("clear must stay silent, toasts = %+v", m.toasts)
	}
	if m.extWidgetPanel("plan") != nil {
		t.Fatalf("clear must retire the panel, got %+v", m.extWidgetPanel("plan"))
	}
}

// setTitle has no TUI surface: ignored per protocol, never a toast.
func TestSetTitleIgnored(t *testing.T) {
	m := New(nil, t.TempDir())
	m = m.handleUIRequest([]byte(`{"id":"t1","method":"setTitle","title":"pi - x"}`))
	if len(m.toasts) != 0 || len(m.Dialogs) != 0 {
		t.Fatalf("setTitle must be silent: toasts=%+v dialogs=%+v", m.toasts, m.Dialogs)
	}
}
