package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "pitago/src/tui/ev"
)

// piSpawnFlagDef is one toggleable pi spawn flag shown as a header chip.
// Add a new option by appending one entry here — parse, build, chips and
// hit-testing all read this table.
type piSpawnFlagDef struct {
	key   string   // prefs key + chip label ("[x]ne")
	args  []string // pi argv when ON (e.g. ["-ne"])
	match []string // argv forms recognized when reading existing opts
}

// piSpawnFlagDefs is the chip row, in display order. "sp" = --system-prompt ""
// (empty system prompt).
var piSpawnFlagDefs = []piSpawnFlagDef{
	{key: "ne", args: []string{"-ne"}, match: []string{"-ne", "--no-extensions"}},
	{key: "ns", args: []string{"-ns"}, match: []string{"-ns", "--no-skills"}},
	{key: "np", args: []string{"-np"}, match: []string{"-np", "--no-prompt-templates"}},
	{key: "nc", args: []string{"-nc"}, match: []string{"-nc", "--no-context-files"}},
	{key: "no-mcp", args: []string{"--no-mcp"}, match: []string{"--no-mcp"}},
	{key: "no-tools", args: []string{"--no-tools"}, match: []string{"--no-tools", "-nt"}},
	{key: "no-themes", args: []string{"--no-themes"}, match: []string{"--no-themes"}},
	{key: "sp", args: []string{"--system-prompt", ""}, match: []string{"--system-prompt"}},
}

// piSpawnFlagHints explains each flag in the Restart menu (same order as
// piSpawnFlagDefs).
var piSpawnFlagHints = map[string]string{
	"ne":        "no extensions — skip all pi extensions",
	"ns":        "no skills — skip skill loading",
	"np":        "no prompt templates",
	"nc":        "no context files — skip AGENTS.md / CLAUDE.md",
	"no-mcp":    "no MCP — no servers connect, no MCP tools",
	"no-tools":  "no tools — disable all built-in tools",
	"no-themes": "no themes — disable theme discovery",
	"sp":        "empty system prompt — override pi's coding prompt",
}

// restartMenuItems is the Restart tab: one checked item per pi spawn
// flag with a description (picking stays open), then the respawn action.
func (m Model) restartMenuItems() []tea.MenuItem {
	items := make([]tea.MenuItem, 0, len(piSpawnFlagDefs)+2)
	for _, d := range piSpawnFlagDefs {
		on := m.piSpawnFlags != nil && m.piSpawnFlags[d.key]
		items = append(items, tea.MenuItem{
			ID: "act:pi-flag-" + d.key, Label: check(on) + d.key,
			Hint: piSpawnFlagHints[d.key], Toggle: true,
		})
	}
	items = append(items,
		tea.MenuItem{Separator: true},
		tea.MenuItem{ID: "act:pi-restart", Label: "Restart pi…",
			Hint: "respawn pi with the checked flags"})
	return items
}

// headerNative is the header line for both renderers: session/cwd text,
// then the chip row. Painted and native paths must match headerFlagChipAt.
func (m Model) headerNative() string {
	left := m.headerText()
	if chips := m.headerFlagChips(); chips != "" {
		return left + "   " + chips
	}
	return left
}

// headerFlagChips renders "[x]ne [ ]ns … [restart]" for the header row.
func (m Model) headerFlagChips() string {
	if m.piSpawnFlags == nil {
		return ""
	}
	parts := []string{}
	for _, d := range piSpawnFlagDefs {
		box := "[ ]"
		if m.piSpawnFlags[d.key] {
			box = "[x]"
		}
		parts = append(parts, box+d.key)
	}
	parts = append(parts, "[restart]")
	return strings.Join(parts, " ")
}

// headerFlagChipAt returns the chip key (or "restart") at click x, or "".
func (m Model) headerFlagChipAt(x int) string {
	left := m.headerText()
	start := ansi.StringWidth(left) + 3
	if x < start {
		return ""
	}
	chips := m.headerFlagChips()
	if chips == "" {
		return ""
	}
	pos := start
	for _, part := range strings.Split(chips, " ") {
		w := ansi.StringWidth(part)
		if x >= pos && x < pos+w {
			return strings.TrimPrefix(strings.TrimPrefix(part, "[x]"), "[ ]")
		}
		pos += w + 1
	}
	return ""
}

// spawnFlagsFromExtraArgs reads the toggles out of an existing ExtraArgs list.
func spawnFlagsFromExtraArgs(args []string) map[string]bool {
	out := map[string]bool{}
	i := 0
	for i < len(args) {
		a := args[i]
		for _, d := range piSpawnFlagDefs {
			for _, form := range d.match {
				if a == form {
					out[d.key] = true
					// "--system-prompt" counts only with an empty value
					if d.key == "sp" && !(i+1 < len(args) && args[i+1] == "") {
						out[d.key] = false
					} else if d.key == "sp" {
						i++
					}
				}
			}
		}
		i++
	}
	return out
}

// ExtraArgsFromSpawnFlags builds the pi argv segment for the toggles.
// "ne" is excluded: Options.NoExtensions carries it (Spawn adds "-ne"
// itself), so it never also goes out through ExtraArgs.
func ExtraArgsFromSpawnFlags(flags map[string]bool) []string {
	var out []string
	for _, d := range piSpawnFlagDefs {
		if d.key != "ne" && flags[d.key] {
			out = append(out, d.args...)
		}
	}
	return out
}

// syncSpawnFlagsFromOpts populates m.piSpawnFlags from spawnOpts (CLI launch).
func (m *Model) syncSpawnFlagsFromOpts() {
	if m.piSpawnFlags == nil {
		m.piSpawnFlags = map[string]bool{}
	}
	for k, v := range spawnFlagsFromExtraArgs(m.spawnOpts.ExtraArgs) {
		m.piSpawnFlags[k] = v
	}
	if m.spawnOpts.NoExtensions {
		m.piSpawnFlags["ne"] = true
	}
}

// applySpawnFlagsToOpts updates spawnOpts (NoExtensions + ExtraArgs)
// from the toggles, so RespawnPi spawns with the checked flags.
func (m *Model) applySpawnFlagsToOpts() {
	m.spawnOpts.NoExtensions = m.piSpawnFlags["ne"]
	m.spawnOpts.ExtraArgs = ExtraArgsFromSpawnFlags(m.piSpawnFlags)
}
