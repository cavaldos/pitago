package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"pitago/src/pirpc"
)

func planToggleModel(t *testing.T, cmds ...pirpc.RepoCommand) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.Cmds = cmds
	return m
}

func pressTab(m Model) Model {
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	return tm.(Model)
}

// Tab is one key, no menu: it must forward /plan and flip the latch in the
// same frame, both directions.
func TestTabTogglesPlanModeForward(t *testing.T) {
	m := planToggleModel(t, pirpc.RepoCommand{Name: planToggleCmd, Source: "extension"})
	if m.isPlanMode() {
		t.Fatal("plan mode must start off")
	}
	m = pressTab(m)
	if !m.isPlanMode() {
		t.Fatal("first Tab must latch plan mode on")
	}
	if m.Status != "plan mode: on" {
		t.Errorf("status = %q, want %q", m.Status, "plan mode: on")
	}
	m = pressTab(m)
	if m.isPlanMode() {
		t.Fatal("second Tab must latch plan mode off again")
	}
}

// Tab must never leak a tab character into a half-typed prompt, and must work
// with text in the editor and while pi is streaming (opencode parity).
func TestTabDoesNotTypeIntoEditor(t *testing.T) {
	m := planToggleModel(t, pirpc.RepoCommand{Name: planToggleCmd, Source: "extension"})
	m.ta.SetValue("draft prompt")
	m.thinking = true
	m = pressTab(m)
	if got := m.ta.Value(); got != "draft prompt" {
		t.Errorf("editor = %q, want it untouched by Tab", got)
	}
	if !m.isPlanMode() {
		t.Error("Tab must toggle plan mode mid-turn too")
	}
}

// A missing extension must say so: a swallowed keypress is the one failure
// mode the user cannot debug.
func TestTabWithoutPlanExtensionShowsNotice(t *testing.T) {
	m := planToggleModel(t, pirpc.RepoCommand{Name: "team-enable", Source: "extension"})
	if cmd := m.togglePlanMode(); cmd != nil {
		t.Error("no plan command installed: nothing may be forwarded")
	}
	if m.isPlanMode() {
		t.Error("latch must not flip when the command is missing")
	}
	found := false
	for _, b := range m.blocks {
		if b.Kind == "notice" && strings.Contains(b.Text, planToggleCmd) {
			found = true
		}
	}
	if !found && !strings.Contains(m.LastNotice(), planToggleCmd) {
		t.Errorf("expected a notice naming %q", planToggleCmd)
	}
}
