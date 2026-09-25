package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SubagentsNone is the first picker row: clear the selection.
const SubagentsNone = "— none —";

// SubagentInfo is one pickable subagent (pi-subagents discovery, trimmed).
type SubagentInfo struct {
	Name, Source, Description, Model, FilePath string
}

// DiscoverSubagents lists effective subagents: project > user > package >
// builtin (dedupe by name, sorted). Stdlib only; tolerant of missing dirs.
func DiscoverSubagents(cwd string) []SubagentInfo {
	agentDir := piAgentDir()
	var builtin, pkg, user, project []SubagentInfo

	if agentDir != "" {
		builtin = scanAgentDir(filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "builtin")
		// Other installed packages ship their own agents too.
		if roots, err := filepath.Glob(filepath.Join(agentDir, "npm", "node_modules", "*", "agents")); err == nil {
			for _, r := range roots {
				if strings.Contains(r, "pi-subagents") {
					continue
				}
				pkg = append(pkg, scanAgentDir(r, "package")...)
			}
		}
		if roots, err := filepath.Glob(filepath.Join(agentDir, "npm", "node_modules", "@*", "*", "agents")); err == nil {
			for _, r := range roots {
				pkg = append(pkg, scanAgentDir(r, "package")...)
			}
		}
		user = scanAgentDir(filepath.Join(agentDir, "agents"), "user")
	}
	if cwd != "" {
		project = scanAgentDir(filepath.Join(cwd, ".pi", "agents"), "project")
		project = append(project, scanAgentDir(filepath.Join(cwd, ".pi", "agent", "agents"), "project")...)
	}

	// First wins: project, user, package, builtin.
	seen := map[string]bool{}
	var out []SubagentInfo
	for _, group := range [][]SubagentInfo{project, user, pkg, builtin} {
		for _, a := range group {
			if a.Name == "" || seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scanAgentDir reads *.md agent definitions (frontmatter without opening
// "---", like pi-subagents ships: name/description/model/thinking lines
// before the first "---").
func scanAgentDir(dir, source string) []SubagentInfo {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SubagentInfo
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		name, desc, model := parseAgentFrontmatter(string(raw))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, SubagentInfo{Name: name, Source: source, Description: desc, Model: model, FilePath: p})
	}
	return out
}

// parseAgentFrontmatter scans key: value lines up to the first "---".
func parseAgentFrontmatter(raw string) (name, desc, model string) {
	for _, ln := range strings.Split(raw, "\n") {
		if strings.TrimSpace(ln) == "---" {
			break
		}
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "name":
			name = strings.TrimSpace(v)
		case "description":
			desc = strings.TrimSpace(v)
		case "model":
			model = strings.TrimSpace(v)
		}
	}
	return name, desc, model
}

// CurrentSubagent is the last-picked subagent (persisted in prefs.json).
func (m *Model) CurrentSubagent() string {
	if m.CurAgent != "" {
		return m.CurAgent
	}
	return LoadPrefs(m.prefsPath).CurrentSubagent
}

// SetCurrentSubagent persists the picked subagent.
func (m *Model) SetCurrentSubagent(name string) {
	m.CurAgent = name
	prefs := LoadPrefs(m.prefsPath)
	prefs.CurrentSubagent = name
	_ = SavePrefs(m.prefsPath, prefs)
}

// subagentDesc is one picker row: "● current · [source] · model — desc".
func subagentDesc(a SubagentInfo, current string) string {
	var b strings.Builder
	if a.Name == current {
		b.WriteString("● current · ")
	}
	b.WriteString("[" + a.Source + "]")
	if a.Model != "" {
		b.WriteString(" · " + a.Model)
	}
	if a.Description != "" {
		b.WriteString(" — " + a.Description)
	}
	return b.String()
}

// OpenSubagents shows the native subagent picker (/subagents). RPC mode
// can't render pi-subagents' custom UI (ui.custom returns undefined), so
// the extension command silently does nothing — this replaces it. Enter
// selects (see confirmSubagents in src/builtin); /subagents <name> picks
// directly.
func (m *Model) OpenSubagents(arg string) tea.Cmd {
	agents := DiscoverSubagents(m.cwd)
	if len(agents) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no subagents found — pi install npm:pi-subagents"})
		m.Refresh()
		return nil
	}
	cur := m.CurrentSubagent()
	if a := strings.TrimSpace(arg); a != "" {
		switch strings.ToLower(a) {
		case "off", "none", "clear", "--none", "exit", "quit":
			m.SetCurrentSubagent("")
			m.AddBlock(Block{Kind: "notice", Text: "subagent off → back to initial state"})
			m.Refresh()
			return nil
		}
		for _, ag := range agents {
			if strings.EqualFold(ag.Name, a) {
				m.SetCurrentSubagent(ag.Name)
				m.AddBlock(Block{Kind: "notice", Text: "subagent → " + ag.Name + " [" + ag.Source + "]"})
				m.Refresh()
				return nil
			}
		}
		m.AddBlock(Block{Kind: "notice", Text: "subagent '" + a + "' not found", Err: true})
		m.Refresh()
		return nil
	}
	// First row clears the selection (no agent picked).
	noneDesc := "turn off subagent · back to initial state"
	if cur == "" {
		noneDesc = "● current · " + noneDesc
	}
	opts := make([]string, 0, len(agents)+1)
	descs := make([]string, 0, len(agents)+1)
	opts = append(opts, SubagentsNone)
	descs = append(descs, noneDesc)
	for _, ag := range agents {
		opts = append(opts, ag.Name)
		descs = append(descs, subagentDesc(ag, cur))
	}
	d := &Dialog{Kind: "subagents", Title: "Subagents",
		Message: "↑↓ pick · Enter select · type to filter · Esc close · ● = current",
		Options: opts, Descs: descs}
	d.Reindex()
	// Preselect the current agent so it's visible on open.
	for i, ri := range d.FIdx {
		if d.Options[ri] == cur {
			d.Cursor = i
			break
		}
	}
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}
