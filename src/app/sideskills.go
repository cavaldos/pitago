package app

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/extension"
	"pitago/src/pirpc"
)

// SKILLS sidebar panel: the skills the agent actually used this session —
// not the installed catalogue.
//
// Data source. pi never emits a "skill used" event. It advertises every
// available skill in the system prompt (name + description) and pulls in the
// full SKILL.md only when the model decides the task matches
// (docs/skills.md, "Understand how skills load"), so the installed set is not
// observable as usage — only the load is. Two loads are visible, and both are
// already in the transcript we keep:
//
//  1. The model read the skill file. That is the documented path for automatic
//     loading; the file is <skillDir>/SKILL.md, or a standalone *.md for the
//     standalone form pi also accepts.
//  2. The user forced it with /skill:name. pi expands that command into the
//     user message before the turn runs (agent-session._expandSkillCommand),
//     so the message arrives starting with <skill name="archify"
//     location="…/archify/SKILL.md"> followed by the skill body.
//
// Naming. We never guess a name from a path when we do not have to. pi's
// get_commands already answers with one `skill:<name>` entry per skill whose
// sourceInfo.path is the exact file that entry loads, so a read path is
// resolved against that catalogue first: the name is pi's own, the
// description comes along for free, and a package skill whose frontmatter
// name differs from its directory still lands on one row. Directory
// inference survives only as the fallback for a skill pi no longer lists, and
// even then the path has to look like a real one — a root segment, a
// skill-shaped leaf, no shell variable, no glob — because without that,
// ordinary commands like `ls ~/.agents/skills/*/SKILL.md` or
// `grep -rn "/SKILL.md" .` mint rows called "*" and "grep -rn \"".
//
// A bash-execution block contributes only its first line. Its Text is the
// command and the command's OUTPUT; the output is data, and scanning it turns
// "which skills are installed?" into a claim that they all were used..
//
// Deriving from m.blocks — the same source invokedTools() uses — is what
// makes the panel free of extra state: live streaming, get_messages restore
// and /reload all land in the sidebar with no extra wiring. It is also why
// the section draws nothing at all until a skill is used: an empty section
// would advertise the very catalogue this panel exists to hide.

// skillsShowMax caps the list, like todosShowMax and lspRowsMax do for their
// sections: a long session can load a lot of skills, and the panel is a
// sidebar, not a log.
const skillsShowMax = 10

// skillFile is the filename of the portable (directory) skill form.
const skillFile = "SKILL.md"

// skillSlugRe is the shape a directory must have to be accepted as a skill
// name on inference alone. pi's spec is lowercase letters, numbers and
// hyphens; we stay a little looser to tolerate a real directory that
// disagrees, but the filter's job is to reject command syntax, not to
// re-validate the spec: a glob ("*", "**"), a relative hop (".."), or a
// quoted/tokenised fragment must never become a skill name.
var skillSlugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// skillBlockRe captures the name and location of a pi-expanded /skill:name
// user message. Anchored per line (not to the string) so a leading image
// chip or a whitespace-trimmed restore cannot hide it.
var skillBlockRe = regexp.MustCompile(`(?m)^<skill name="([^"]+)"(?: location="([^"]*)")?`)

// skillRef is one candidate "the agent loaded this skill file". known
// distinguishes pi's own name from one inferred off the directory, so a
// session-learned mapping can correct the latter without overriding the
// former.
type skillRef struct {
	Path  string // cleaned path pi loaded, "" when unknown
	Name  string // canonical name, "" when unknown
	known bool   // Name came from pi's catalogue
}

// skillCatalog is pi's own answer to "which skills exist and where": the
// get_commands entries with source "skill", keyed by the file each one loads.
type skillCatalog struct {
	byPath map[string]string // cleaned path -> canonical name
	names  map[string]bool   // canonical name set
	descs  map[string]string // canonical name -> one-line description
}

// newSkillCatalog indexes the skill entries of a command catalogue. Paths are
// cleaned the same way the transcript paths are, so the two sides compare.
func newSkillCatalog(cmds []pirpc.RepoCommand) skillCatalog {
	c := skillCatalog{
		byPath: make(map[string]string),
		names:  make(map[string]bool),
		descs:  make(map[string]string),
	}
	for _, cmd := range cmds {
		if cmd.Source != extension.SourceSkill {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(cmd.Name), "skill:")
		if name == "" {
			continue
		}
		c.names[name] = true
		if d := oneLineStr(strings.TrimSpace(cmd.Description)); d != "" {
			c.descs[name] = d
		}
		if cmd.SourceInfo == nil {
			continue
		}
		if p := cleanSkillPath(cmd.SourceInfo.Path); p != "" {
			c.byPath[p] = name
		}
	}
	return c
}

// lookup resolves one observed path to a skill, preferring pi's catalogue.
// The name is "" when the path is not a skill this panel should claim.
func (c skillCatalog) lookup(p string) skillRef {
	p = cleanSkillPath(p)
	if p == "" {
		return skillRef{}
	}
	if name, ok := c.byPath[p]; ok {
		return skillRef{Path: p, Name: name, known: true}
	}
	// Unknown to the catalogue: accept the portable directory form only, and
	// only when the directory is shaped like a skill.
	if !strings.HasSuffix(p, "/"+skillFile) && p != skillFile {
		return skillRef{}
	}
	// The path must carry a real root as well as a skill-shaped leaf. A
	// genuine load is at least <root>/<name>/SKILL.md, while the fragments
	// that survive tokenising a shell command are one segment deep: a
	// variable ("$d/SKILL.md" tokenises to "d/SKILL.md", minting a row named
	// "d"), a loop counter, a glob piece. The slug test below cannot see the
	// difference — "d" and "one" are perfectly good slugs — so the depth of
	// the path is what has to carry it. Corpus-wide this removed every
	// phantom (d/one/two) and kept the real relative hits
	// (english/toeic-essay, obsidian-cli).
	if countPathSegs(path.Dir(p)) < 2 {
		return skillRef{}
	}
	dir := path.Base(path.Dir(p))
	// skillSlugRe already rejects ".." (it must start alphanumeric), so the
	// explicit test below is defence in depth, not the guard doing the work:
	// if the regex is ever loosened, ".." must still not become a row.
	if dir == "." || dir == ".." || !skillSlugRe.MatchString(dir) {
		return skillRef{}
	}
	return skillRef{Path: p, Name: dir}
}

// countPathSegs counts the non-empty segments of a slash-separated path, so
// "/s/one" counts 2 and a bare "." counts 0.
func countPathSegs(p string) int {
	n := 0
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			n++
		}
	}
	return n
}

// desc returns pi's description for a skill, "" when it has none listed.
func (c skillCatalog) desc(name string) string { return c.descs[name] }

// cleanSkillPath normalises a path for comparison: separators unified, a
// grep-style ":line[:col]" suffix dropped, "./" and "~/" noise removed, and no
// trailing separator. It deliberately does not resolve "..", because the
// caller rejects those rather than resolving them against a cwd it may not
// share with the command.
func cleanSkillPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	// Cut a trailing :line or :line:col, but keep a Windows drive ("C:").
	for {
		i := strings.LastIndexByte(p, ':')
		if i <= 1 || !allDigits(p[i+1:]) {
			break
		}
		p = p[:i]
	}
	p = strings.TrimPrefix(p, "~/")
	p = strings.TrimPrefix(p, "./")
	return strings.TrimSuffix(p, "/")
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isSkillLoadTool reports whether a tool's arguments are paths the agent
// could be loading a skill through. write/edit author a file rather than load
// one, so they are out; the task-style tools (task, delegate_task, agent) are
// out too, because their arguments are a task brief in prose — a delegated
// brief that merely mentions "archify/SKILL.md" has loaded nothing, and
// scanning it would light the panel before the worker did any work. The one
// real signal in a task call is its explicit skills list, which
// delegatedSkillNames reads separately. Everything else (read, bash, grep,
// find, ls, and whatever pi adds next) is an inspection route.
func isSkillLoadTool(name string) bool {
	switch name {
	case "write", "edit", "multiedit", "apply_patch",
		"task", "delegate_task", "agent":
		return false
	}
	return true
}

// delegatedSkillNames reads the one argument of a task-style call that is
// evidence of a load: the "skills" list, naming skills actually handed to the
// worker. Each entry is a skill NAME, not a path, so it is checked against
// the catalogue's name set and the slug shape — never against the
// path→name lookup the rest of the scan uses. The rest of the arguments are
// deliberately left unread: they are prose, and prose is not a file read.
func delegatedSkillNames(raw string) []string {
	var args struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args.Skills
}

// argStrings collects every string in a raw JSON tool-argument blob, however
// deeply nested, so a skill path is found whichever tool carried it — read's
// `path`, grep's `path`, bash's free-text `command`.
func argStrings(raw string, out *[]string) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		// Not JSON (or a shape we cannot parse): fall back to the raw text so
		// the scan still has something to look at. The caller is a catalogue
		// lookup plus a directory-shape test, so junk here is inert.
		*out = append(*out, raw)
		return
	}
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case string:
			*out = append(*out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
}

// mayNameSkill is the cheap pre-filter in front of the JSON parse: a skill
// load always names a markdown file, so arguments that mention none cannot
// resolve to one. This keeps the panel off the unmarshal path for the
// overwhelming majority of blocks in a real transcript. strings.Contains, not
// bytes.Contains([]byte(raw), …): the conversion would copy every argument
// string on every build, which is an allocation per block per frame.
func mayNameSkill(raw string) bool {
	return strings.Contains(raw, skillFile) || strings.Contains(raw, ".md")
}

// usedSkills returns the skills the agent loaded this session, in first-use
// order, building the catalogue for itself. Callers that also need the
// catalogue (the renderer wants pi's descriptions) should use usedSkillsFrom
// with one they already built, rather than paying for a second build per
// frame.
func (m Model) usedSkills() []string {
	return m.usedSkillsFrom(newSkillCatalog(m.Cmds))
}

func (m Model) usedSkillsFrom(cat skillCatalog) []string {
	order := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	// A pi-expanded /skill: block pairs a canonical name with the file it
	// loaded, which teaches this session the path->name link the catalogue
	// may not carry. It is read by note() above.
	pathName := make(map[string]string, 4)

	note := func(ref skillRef) {
		name := ref.Name
		// A pi-expanded /skill: block pairs a canonical name with the file it
		// loaded, which teaches this session the same path->name link the
		// catalogue would. Honour it over a name merely inferred from the
		// directory, so a package skill whose frontmatter name differs from
		// its directory stays on one row.
		if !ref.known && ref.Path != "" {
			if learned, ok := pathName[ref.Path]; ok {
				name = learned
			}
		}
		if name == "" {
			return
		}
		if ref.Path != "" {
			pathName[ref.Path] = name
		}
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	// scan resolves every path-shaped string in one argument blob. A single
	// command can load several skills (`diff a/SKILL.md b/SKILL.md`), so each
	// string is split on shell-ish separators and every token is considered.
	// strs is hoisted so the reused buffer is allocated once per sidebar
	// build, not once per tool block.
	var strs []string
	// Returns whether it found a skill, so a caller can tell an empty result
	// from "the raw pass simply found nothing here".
	scan := func(raw string) bool {
		if !mayNameSkill(raw) {
			return false
		}
		strs = strs[:0]
		argStrings(raw, &strs)
		found := false
		for _, s := range strs {
			for _, tok := range strings.FieldsFunc(trimHeredoc(s), pathTokenSep) {
				// A variable reference is not a path; see pathTokenSep.
				if tok == "" || hasShellVar(tok) {
					continue
				}
				if ref := cat.lookup(tok); ref.Name != "" {
					note(ref)
					found = true
				}
			}
		}
		return found
	}

	for _, bl := range m.blocks {
		switch bl.Kind {
		case "user":
			if mt := skillBlockRe.FindStringSubmatch(bl.Text); mt != nil {
				// known=true: this name came from pi itself, not from a path.
				note(skillRef{Path: cleanSkillPath(mt[2]), Name: strings.TrimSpace(mt[1]), known: true})
			}
		case "tool":
			if !isSkillLoadTool(bl.ToolName) {
				// Handing a worker a skill by name is a real load of that
				// skill, even though no file path ever appears; the rest of
				// the block is prose and stays unread.
				for _, n := range delegatedSkillNames(bl.ToolArgsRaw) {
					if n = strings.TrimSpace(n); n == "" {
						continue
					}
					if known := cat.names[n]; known || skillSlugRe.MatchString(n) {
						note(skillRef{Name: n, known: known})
					}
				}
				break
			}
			// Only fall through to the rendered header when the raw pass found
			// nothing: gating on mayNameSkill instead would skip the second
			// pass whenever the raw blob merely mentions a .md file anywhere
			// without naming a skill. The pass is cheap (the header is the
			// same content) and note() dedupes, so a redundant scan is inert.
			if !scan(bl.ToolArgsRaw) {
				// Live blocks can carry a rendered header before the raw args
				// land; it is the same content, so it is a safe second pass.
				scan(bl.ToolArgs)
			}
		case "bash":
			// A user-run !command is recorded as its own block shape, with the
			// command and output together in Text: "$ " + command + "\n" +
			// output. Only the command half is a command — the output is data
			// the tool produced, and scanning it mints rows for whatever the
			// command happened to print: `!ls ~/.agents/skills/*/SKILL.md`
			// would otherwise list every installed skill.
			scan(strings.SplitN(bl.Text, "\n", 2)[0])
		}
	}
	return order
}

// pathTokenSep splits a tool argument into candidate path tokens. Quoting and
// shell punctuation are separators so a quoted glob or a flag never survives
// into a token; "=" is included so --file=/s/x/SKILL.md still yields a path.
//
// '$' is deliberately NOT one of them. Splitting there does not remove the
// variable, it detaches it: "$d/SKILL.md" becomes "d/SKILL.md" and
// "$HOME/x/SKILL.md" becomes "HOME/x/SKILL.md", both of which then read as
// ordinary relative paths. Keeping the '$' attached lets the caller reject the
// reference as a unit instead of guessing at the fragment it leaves behind.
var pathTokenSep = func(r rune) bool {
	return strings.ContainsRune(" \t\n\r\"'`;|&<>(){}[],=*?!", r)
}

// hasShellVar reports a token that is really a variable reference with the
// variable text sliced off. No such token is a path pi ever loaded.
func hasShellVar(tok string) bool { return strings.Contains(tok, "$") }

// trimHeredoc cuts a shell command at its first heredoc opener. A heredoc
// body is content being written to a file, not a path being read, so an agent
// writing a test fixture — `cat > t.go <<'EOF' … /s/one/SKILL.md … EOF` — is
// authoring text about skills, not loading one. Only a string carrying a
// heredoc marker is cut, and no real path contains "<<", so a read's `path`
// argument passes through untouched.
func trimHeredoc(s string) string {
	if i := strings.Index(s, "<<"); i >= 0 {
		return s[:i]
	}
	return s
}

// renderSkillsSection draws the SKILLS panel: one row per skill the agent
// used this session, with pi's own description dimmed alongside. It returns
// "" — no header, no separator, no placeholder — until at least one skill has
// been used, so an untouched session shows no skills section at all instead
// of an empty list of every skill that happens to be installed.
func (m Model) renderSkillsSection(inner int) string {
	// One catalogue per render: usedSkills also needs it to resolve names,
	// and building it twice showed up in the sidebar frame budget.
	cat := newSkillCatalog(m.Cmds)
	used := m.usedSkillsFrom(cat)
	if len(used) == 0 {
		return ""
	}
	shown := used
	if len(shown) > skillsShowMax {
		shown = shown[:skillsShowMax]
	}

	// One name column so the descriptions line up under each other.
	nameW := 0
	for _, n := range shown {
		if w := lipgloss.Width(n); w > nameW {
			nameW = w
		}
	}
	// " • " prefix + name column + a single space before the description.
	gap := inner - 3 - nameW - 1

	var b strings.Builder
	b.WriteString(sideTitleStyle.Render(fmt.Sprintf("SKILLS (%d)", len(used))) + "\n")
	for _, n := range shown {
		// The bullet belongs inside the width budget, so truncANSI sees the
		// whole row (same shape as the TOOLS section).
		row := statusBarStyle.Render(" • ") + lipgloss.NewStyle().Foreground(cText).Render(n)
		if d := cat.desc(n); d != "" && gap >= 6 {
			row += toolStyle.Render(strings.Repeat(" ", nameW-lipgloss.Width(n)+1) + Short(d, gap))
		}
		b.WriteString(truncANSI(row, inner) + "\n")
	}
	if hidden := len(used) - len(shown); hidden > 0 {
		b.WriteString(toolStyle.Render(fmt.Sprintf(" … +%d more", hidden)) + "\n")
	}
	b.WriteString(sep() + "\n")
	return b.String()
}
