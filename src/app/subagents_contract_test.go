package app

// The generic contract: any backend that emits a subagent-family tool gets
// presence rows, whatever its argument schema. These tests pin that contract
// so it cannot quietly narrow to one extension's tool names.

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Row creators vs family-only tools. A control/query tool acts on an existing
// row and must never create one, or every wait/interrupt would spawn a row.
func TestSubagentToolClassification(t *testing.T) {
	rows := []string{"subagent", "run_agent", "run_workflow", "fork"}
	for _, name := range rows {
		if !isSubagentRowTool(name) {
			t.Errorf("%q should create a row", name)
		}
		if !isSubagentTool(name) {
			t.Errorf("%q should be in the subagent family", name)
		}
	}
	control := []string{"subagent_interrupt", "subagent_wait", "subagents_list", "subagent_resume"}
	for _, name := range control {
		if isSubagentRowTool(name) {
			t.Errorf("%q acts on an existing row and must not create one", name)
		}
		if !isSubagentTool(name) {
			t.Errorf("%q is in the subagent family", name)
		}
	}
	// Exact match only: substring matching would false-positive on unrelated
	// tools, so names that merely contain a family word must be ignored.
	for _, name := range []string{"bash", "read", "subagent_config", "agent_pool", "fork_session", ""} {
		if isSubagentTool(name) || isSubagentRowTool(name) {
			t.Errorf("%q is not a subagent-family tool", name)
		}
	}
	// Case and padding are normalized, so a differently-cased name still counts.
	if !isSubagentRowTool("  SubAgent ") {
		t.Error("tool names should be matched case-insensitively and trimmed")
	}
}

// A backend with its own argument schema still gets a usable row. pi's fork tool
// sends {task, effort, mode} with no name, so the row is named after the task
// rather than an opaque toolCallId; when there is no usable name at all the
// toolCallId is the last resort.
func TestTrackSubagentStartTolerantOfForeignArgSchemas(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		args    string
		wantSub string
	}{
		{"pi-agents schema", "subagent", `{"name":"scout","agent":"scout","task":"find bugs","interactive":true}`, "scout"},
		{"run_agent with prompt", "run_agent", `{"agent":"explore","prompt":"look at the parser"}`, "look at the parser"},
		{"fork tool shape names the row after the task", "fork", `{"task":"map the auth flow","effort":"fast","mode":"research"}`, "map the auth flow"},
		{"unknown shape falls back to tool id", "run_workflow", `{"weird":[1,2,3]}`, "run_workflow-abcdef12"},
		{"no args at all", "fork", ``, "fork-abcdef12"},
		{"malformed json", "subagent", `{not json`, "subagent-abcdef12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{}
			m.trackSubagentStart("abcdef1234567890", tc.tool, json.RawMessage(tc.args))
			if len(m.Subagents) != 1 {
				t.Fatalf("expected one row, got %d", len(m.Subagents))
			}
			row := m.Subagents[0]
			if row.Name == "" {
				t.Fatal("a row must never render blank; the name falls back to tool-id")
			}
			if tc.wantSub != "" && row.Name != tc.wantSub {
				t.Fatalf("name = %q, want %q", row.Name, tc.wantSub)
			}
			if row.ID != "abcdef1234567890" {
				t.Fatalf("row is keyed by toolCallId, got %q", row.ID)
			}
		})
	}
}

// A non-family tool must not appear at all.
func TestTrackSubagentStartIgnoresOtherTools(t *testing.T) {
	for _, tool := range []string{"bash", "read", "grep", "todo_write"} {
		m := &Model{}
		m.trackSubagentStart("id1", tool, json.RawMessage(`{"name":"x"}`))
		if len(m.Subagents) != 0 {
			t.Errorf("%q created a subagent row", tool)
		}
	}
	// An empty toolCallId cannot key a row, so it is dropped rather than stored.
	m := &Model{}
	m.trackSubagentStart("", "subagent", nil)
	if len(m.Subagents) != 0 {
		t.Error("a row with no toolCallId cannot be tracked")
	}
}

// Stock pi with no subagent extension at all: the section is hidden by default,
// the overlay opens, and an empty list is a normal state rather than a crash.
func TestNoSubagentExtensionIsANoOp(t *testing.T) {
	if DefaultSideVisible(SideSubagents) {
		t.Error("the SUBAGENTS section should be hidden by default; there is nothing to show")
	}
	m := &Model{}
	m.OpenSubagentHerd()
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagent-herd" {
		t.Fatalf("the overlay should still open with no rows: %+v", m.Dialogs)
	}
	if got := len(m.Dialogs[0].Options); got != 0 {
		t.Fatalf("expected an empty list, got %d options", got)
	}
	// Rendering and the refresh tick must survive the empty case.
	if m.renderSubagentsDialog(m.Dialogs[0]) == "" {
		t.Error("an empty overlay should still render its frame")
	}
	m.Refresh()
}

// Every key path that touches a row must tolerate having none.
func TestHerdOverlayKeysAreSafeWithNoRows(t *testing.T) {
	m := &Model{}
	m.OpenSubagentHerd()
	for _, r := range []rune("jkwxfXstR") {
		if len(m.Dialogs) == 0 {
			return // the overlay closed, which is a valid outcome for these keys
		}
		// updateSubagentsDialog takes and returns a Model by value.
		updated, _ := m.updateSubagentsDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, m.Dialogs[0])
		next, ok := updated.(Model)
		if !ok {
			t.Fatalf("unexpected model type %T", updated)
		}
		m = &next
	}
}
