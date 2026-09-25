package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgent(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseAgentFrontmatter(t *testing.T) {
	raw := "name: reviewer\ndescription: Review stuff\nmodel: anthropic/claude\n---\nbody"
	n, d, m := parseAgentFrontmatter(raw)
	if n != "reviewer" || d != "Review stuff" || m != "anthropic/claude" {
		t.Fatalf("got %q %q %q", n, d, m)
	}
}

func TestDiscoverSubagentsPriority(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: builtin scout\n---\n")
	writeAgent(t, filepath.Join(agentDir, "agents"), "scout.md",
		"name: scout\ndescription: user override\n---\n")
	cwd := t.TempDir()
	writeAgent(t, filepath.Join(cwd, ".pi", "agents"), "worker.md",
		"name: worker\ndescription: project worker\n---\n")

	got := DiscoverSubagents(cwd)
	if len(got) != 2 {
		t.Fatalf("want 2 (scout+worker), got %v", got)
	}
	for _, a := range got {
		if a.Name == "scout" && (a.Source != "user" || a.Description != "user override") {
			t.Fatalf("user must win over builtin: %+v", a)
		}
	}
}

func TestOpenSubagentsShowsCurrent(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "reviewer.md",
		"name: reviewer\ndescription: review\n---\n")

	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.SetCurrentSubagent("reviewer")

	m.OpenSubagents("")
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "subagents" {
		t.Fatalf("want subagents dialog, got %+v", m.Dialogs)
	}
	d := m.Dialogs[0]
	if len(d.Options) != 3 || d.Options[0] != SubagentsNone {
		t.Fatalf("want [none reviewer scout], got %v", d.Options)
	}
	// Current row must carry the ● marker in its desc.
	found := false
	for i, o := range d.Options {
		if o == "reviewer" && i < len(d.Descs) {
			if strings.HasPrefix(d.Descs[i], "● current") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("current agent must show ● marker, descs=%v", d.Descs)
	}
	// Cursor preselected on current.
	if d.Options[d.FIdx[d.Cursor]] != "reviewer" {
		t.Fatalf("cursor must land on current, got %v cursor=%d", d.Options, d.Cursor)
	}
}

func TestOpenSubagentsDirectPick(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.OpenSubagents("scout")
	if len(m.Dialogs) != 0 {
		t.Fatalf("direct pick must not open dialog")
	}
	if m.CurrentSubagent() != "scout" {
		t.Fatalf("current must persist, got %q", m.CurrentSubagent())
	}
}

func TestOpenSubagentsOff(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgent(t, filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "scout.md",
		"name: scout\ndescription: recon\n---\n")
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.SetCurrentSubagent("scout")
	m.OpenSubagents("off")
	if len(m.Dialogs) != 0 {
		t.Fatalf("off must not open dialog")
	}
	if m.CurrentSubagent() != "" {
		t.Fatalf("off must clear, got %q", m.CurrentSubagent())
	}
	// Picker with nothing selected marks the none row current.
	m.OpenSubagents("")
	d := m.Dialogs[0]
	if d.Options[0] != SubagentsNone {
		t.Fatalf("first row must be none, got %v", d.Options)
	}
	if !strings.HasPrefix(d.Descs[0], "● current") {
		t.Fatalf("none row must show ● when cleared, got %q", d.Descs[0])
	}
}
