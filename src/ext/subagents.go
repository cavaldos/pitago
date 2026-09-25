package ext

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SubagentsNone is the first picker row: clear the selection.
const SubagentsNone = "— none —"

// SubagentInfo is one pickable subagent (pi-subagents discovery, trimmed).
type SubagentInfo struct {
	Name, Source, Description, Model, FilePath string
}

// DiscoverSubagents lists effective subagents: project > user > package >
// builtin (dedupe by name, sorted). Stdlib only; tolerant of missing dirs.
func DiscoverSubagents(cwd, agentDir string) []SubagentInfo {
	var builtin, pkg, user, project []SubagentInfo

	if agentDir != "" {
		builtin = ScanAgentDir(filepath.Join(agentDir, "npm", "node_modules", "pi-subagents", "agents"), "builtin")
		if roots, err := filepath.Glob(filepath.Join(agentDir, "npm", "node_modules", "*", "agents")); err == nil {
			for _, r := range roots {
				if strings.Contains(r, "pi-subagents") {
					continue
				}
				pkg = append(pkg, ScanAgentDir(r, "package")...)
			}
		}
		if roots, err := filepath.Glob(filepath.Join(agentDir, "npm", "node_modules", "@*", "*", "agents")); err == nil {
			for _, r := range roots {
				pkg = append(pkg, ScanAgentDir(r, "package")...)
			}
		}
		user = ScanAgentDir(filepath.Join(agentDir, "agents"), "user")
	}
	if cwd != "" {
		project = ScanAgentDir(filepath.Join(cwd, ".pi", "agents"), "project")
		project = append(project, ScanAgentDir(filepath.Join(cwd, ".pi", "agent", "agents"), "project")...)
	}

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

// ScanAgentDir reads *.md agent definitions (frontmatter without opening
// "---", like pi-subagents ships: name/description/model/thinking lines
// before the first "---").
func ScanAgentDir(dir, source string) []SubagentInfo {
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
		name, desc, model := ParseAgentFrontmatter(string(raw))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, SubagentInfo{Name: name, Source: source, Description: desc, Model: model, FilePath: p})
	}
	return out
}

// ParseAgentFrontmatter scans key: value lines up to the first "---".
func ParseAgentFrontmatter(raw string) (name, desc, model string) {
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

// Desc is one picker row: "● current · [source] · model — desc".
func Desc(a SubagentInfo, current string) string {
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
