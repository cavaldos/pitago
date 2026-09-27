package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// skillCmd builds a catalogue entry in the exact shape pi's get_commands
// answers (verified against the installed pi: source "skill", and
// sourceInfo.path is the file that entry loads).
func skillCmd(name, skillPath, desc string) pirpc.RepoCommand {
	return pirpc.RepoCommand{
		Name:        "skill:" + name,
		Description: desc,
		Source:      "skill",
		SourceInfo:  &pirpc.SourceInfo{Path: skillPath, Scope: "user"},
	}
}

// readSkill is a `read` tool call that loaded a skill, the way pi records the
// model loading SKILL.md (the args shape is pi's read schema).
func readSkill(dir string) Block {
	return Block{
		Kind:        "tool",
		ToolName:    "read",
		ToolStatus:  "done",
		ToolArgsRaw: `{"path":"` + dir + `/SKILL.md","offset":1,"limit":2000}`,
		ToolArgs:    dir + "/SKILL.md",
	}
}

func bashSkill(cmd string) Block {
	return Block{Kind: "tool", ToolName: "bash", ToolStatus: "done",
		ToolArgsRaw: `{"command":"` + cmd + `"}`}
}

func names(used []string) string { return strings.Join(used, ",") }

// bashExec is a user-run !command block: the command line, then its output,
// in one Text (addBashBlock, and the bashExecution arms of update.go).
func bashExec(cmd, output string) Block {
	return Block{Kind: "bash", Text: "$ " + cmd + "\n" + output}
}

// The panel must stay absent until something actually loads a skill: an empty
// section would advertise the installed catalogue it exists to hide.
func TestSkillsSectionHiddenUntilUsed(t *testing.T) {
	m := New(nil, t.TempDir())
	if out := m.renderSkillsSection(sideInnerW); out != "" {
		t.Fatalf("unused session must render no skills section, got:\n%s", out)
	}
	if out := stripANSI(m.buildSidebarContent()); strings.Contains(out, "SKILLS") {
		t.Fatalf("sidebar must not mention SKILLS before one is used:\n%s", out)
	}
	// A skill pi knows about but that was never loaded stays hidden too.
	m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/s/archify/SKILL.md", "diagrams")}
	if out := stripANSI(m.buildSidebarContent()); strings.Contains(out, "archify") {
		t.Fatalf("installed-but-unused skill must not show:\n%s", out)
	}
}

// The documented load path: the model reads the skill file.
func TestSkillsDetectedFromSkillMdRead(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{
		readSkill("/Users/x/.agents/skills/archify"),
		readSkill("/Users/x/.agents/skills/ask-user"),
	}
	if got := names(m.usedSkills()); got != "archify,ask-user" {
		t.Fatalf("want archify,ask-user, got %s", got)
	}
	out := stripANSI(m.buildSidebarContent())
	for _, want := range []string{"SKILLS (2)", "archify", "ask-user"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, out)
		}
	}
}

// A skill loaded twice is one row, not two.
func TestSkillsDedupeKeepsFirstUseOrder(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{readSkill("/s/beta"), readSkill("/s/alpha"), readSkill("/s/beta")}
	if got := names(m.usedSkills()); got != "beta,alpha" {
		t.Fatalf("want beta,alpha, got %s", got)
	}
}

// Regression (P1): commands that merely mention SKILL.md must not mint skill
// names. `ls ~/.agents/skills/*/SKILL.md` is the single most likely bash call
// in this domain — a user asking which skills are installed — and it used to
// produce a skill literally named "*".
func TestSkillsRejectNonLoadCommands(t *testing.T) {
	for _, cmd := range []string{
		"ls ~/.agents/skills/*/SKILL.md",
		"ls ~/.agents/skills/**/SKILL.md",
		"cat ../SKILL.md",
		"find /Users/x/.agents -name '*/SKILL.md'",
		`grep -rn "/SKILL.md" .`,
		"git show HEAD:docs/SKILL.md",
		"ls ~/.agents/skills",
		"cat README.md",
		"grep -rn SKILL.md --include=*.go src/",
	} {
		m := New(nil, t.TempDir())
		// Give the panel a real catalogue, so a hit cannot be explained away
		// as "the catalogue was empty".
		m.Cmds = []pirpc.RepoCommand{
			skillCmd("archify", "/Users/x/.agents/skills/archify/SKILL.md", "diagrams"),
			skillCmd("herdr", "/Users/x/.agents/skills/herdr/SKILL.md", "terminal multiplexer"),
		}
		m.blocks = []Block{bashSkill(cmd)}
		if used := m.usedSkills(); len(used) != 0 {
			t.Errorf("cmd %q invented skills %v", cmd, used)
		}
		if out := stripANSI(m.buildSidebarContent()); strings.Contains(out, "SKILLS") {
			t.Errorf("cmd %q lit the panel:\n%s", cmd, out)
		}
	}
}

// Regression (P1): the same filter, reached through the fallback directory
// inference rather than the catalogue. These skills are not in the catalogue
// at all, so only the slug check stands between the command and a row.
func TestSkillCatalogFallbackRejectsBadDirs(t *testing.T) {
	cat := newSkillCatalog(nil)
	for _, bad := range []string{
		"/s/*/SKILL.md",
		"/s/*/*/SKILL.md",
		"/s/../SKILL.md",
		"/s/a b/SKILL.md",
		"/s/name'/SKILL.md",
		"/s/.hidden/SKILL.md",
	} {
		if ref := cat.lookup(bad); ref.Name != "" {
			t.Errorf("lookup(%q) = %q, want no skill", bad, ref.Name)
		}
	}
	if ref := cat.lookup("/s/pi-lens-ast-grep/SKILL.md"); ref.Name != "pi-lens-ast-grep" {
		t.Errorf("real skill dir rejected: %+v", ref)
	}
}

// One command can genuinely load two skills; both must be reported.
func TestSkillsDetectsMultipleInOneCommand(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{bashSkill("diff /s/one/SKILL.md /s/two/SKILL.md")}
	if got := names(m.usedSkills()); got != "one,two" {
		t.Fatalf("want one,two, got %s", got)
	}
}

// bash is not the only route: a user-run !command is stored as its own block
// shape, with the command and its output in Text.
func TestSkillsDetectedFromBashExecutionBlock(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{Kind: "bash", Text: "$ cat /s/archify/SKILL.md\n# Archify\n"}}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify from a bash-execution block, got %s", got)
	}
}

// Regression: only the first line of a bash-execution block is the command.
// The rest is the command's OUTPUT, and a file the command printed is data
// the panel has no business claiming — `!ls ~/.agents/skills/*/SKILL.md` is a
// user asking which skills exist, and scanning its output used to list every
// one of them as used.
func TestSkillsIgnoreBashOutputHalf(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{
		skillCmd("alpha", "/s/alpha/SKILL.md", "a"),
		skillCmd("beta", "/s/beta/SKILL.md", "b"),
	}
	m.blocks = []Block{bashExec("ls /s/*/SKILL.md", "/s/alpha/SKILL.md\n/s/beta/SKILL.md\n")}
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("output half is data, not a load: %v", used)
	}
	if out := stripANSI(m.buildSidebarContent()); strings.Contains(out, "SKILLS") {
		t.Fatalf("printed skill paths must not light the panel:\n%s", out)
	}
}

// Regression: a shell variable is not a skill. pathTokenSep splits on '$', so
// "$d/SKILL.md" yields the token "d/SKILL.md" — a perfectly slug-shaped
// directory, one segment deep, and a row called "d". These are the shapes that
// showed up in real session transcripts: a loop over skill directories, a
// `dir=$(pwd)` preamble, a $HOME-relative path.
func TestSkillsRejectShellVariablePaths(t *testing.T) {
	for _, cmd := range []string{
		`cat "$d/SKILL.md"`,
		"for d in */; do f=\"$d/SKILL.md\"; echo $f; done",
		"dir=$(pwd); cat $dir/SKILL.md",
		"cat $HOME/x/SKILL.md",
		"cat ${d}/SKILL.md",
	} {
		m := New(nil, t.TempDir())
		m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/s/archify/SKILL.md", "d")}
		m.blocks = []Block{bashSkill(cmd)}
		if used := m.usedSkills(); len(used) != 0 {
			t.Errorf("cmd %q invented skills %v", cmd, used)
		}
	}
}

// The variable guard must not over-reject: a genuine relative load has a root
// of its own. `head -5 english/toeic-essay/SKILL.md` is two segments of real
// path and is a real load, which a corpus sweep over 210 session files
// confirmed alongside the phantom names it removes. One segment is not a root:
// `toeic-essay/SKILL.md` is what a $variable leaves behind, so it stays out.
func TestSkillsKeepGenuineRelativePath(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("toeic-essay", "/s/skills/toeic.md", "essays")}
	m.blocks = []Block{bashSkill("head -5 english/toeic-essay/SKILL.md")}
	if got := names(m.usedSkills()); got != "toeic-essay" {
		t.Fatalf("want toeic-essay from a relative path, got %s", got)
	}
	one := New(nil, t.TempDir())
	one.blocks = []Block{bashSkill("cat toeic-essay/SKILL.md")}
	if used := one.usedSkills(); len(used) != 0 {
		t.Fatalf("a single segment is a variable fragment, not a root: %v", used)
	}
}

// Regression: a task-style tool's arguments are a brief in prose, not a list
// of files. A delegated task that says "see .../archify/SKILL.md" has loaded
// nothing, and the panel used to claim archify before the worker started. The
// path is spelled out in full — and is a real catalogue path — so this fails on
// the task-tool guard alone, not on the weaker shape filters.
func TestSkillsIgnoreDelegatedPromptProse(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/Users/x/.agents/skills/archify/SKILL.md", "diagrams")}
	m.blocks = []Block{{Kind: "tool", ToolName: "delegate_task", ToolStatus: "done",
		ToolArgsRaw: `{"goal":"update the diagrams, see /Users/x/.agents/skills/archify/SKILL.md for the format","subagent_type":"worker"}`}}
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("a brief that mentions a skill file is not a load: %v", used)
	}
}

// The narrow exception that keeps the signal: naming a skill in a task call's
// "skills" argument really does hand it to the worker, so that is a load.
func TestSkillsDetectedFromDelegatedSkillsArg(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/s/archify/SKILL.md", "diagrams")}
	m.blocks = []Block{{Kind: "tool", ToolName: "delegate_task", ToolStatus: "done",
		ToolArgsRaw: `{"goal":"redraw the architecture","skills":["archify"]}`}}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify from a delegated skills list, got %s", got)
	}
}

// A skill that is not in pi's catalogue can still be handed to a worker by
// name; the slug shape is the only evidence there is, exactly as for a
// directory name.
func TestSkillsDelegatedSkillsArgUnlisted(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{Kind: "tool", ToolName: "task", ToolStatus: "done",
		ToolArgsRaw: `{"prompt":"go","skills":["obsidian-cli","not a skill",""]}`}}
	if got := names(m.usedSkills()); got != "obsidian-cli" {
		t.Fatalf("want obsidian-cli only, got %s", got)
	}
}

// The rendered header is a fallback for a raw pass that found nothing, not for
// a raw pass that merely mentioned a .md file. A streamed block can carry a
// header before the full raw args land, and that header is the same content.
func TestSkillsFallBackToHeaderWhenRawYieldsNothing(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{Kind: "tool", ToolName: "read", ToolStatus: "done",
		ToolArgsRaw: `{"path":"/s/notes/notes.md"}`,
		ToolArgs:    "read\n  path: /s/archify/SKILL.md"}}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify from the rendered header, got %s", got)
	}
}

// A glob can never be a catalogue hit, so the catalogue plus the slug filter
// hold the line even when many skills are installed.
func TestSkillsGlobDoesNotExpandToCatalogue(t *testing.T) {
	m := New(nil, t.TempDir())
	for _, n := range []string{"alpha", "beta", "gamma"} {
		m.Cmds = append(m.Cmds, skillCmd(n, "/s/"+n+"/SKILL.md", "d"))
	}
	m.blocks = []Block{bashSkill("ls /s/*/SKILL.md")}
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("a glob must not claim installed skills: %v", used)
	}
}

// pi uses name = frontmatterName || parentDirName and does not require them to
// match, so the two detection paths can disagree. pi's own name wins, and the
// skill stays on one row with its description.
func TestSkillsCatalogNameWinsOverDirName(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("diagramming", "/s/archify/SKILL.md", "diagrams")}
	m.blocks = []Block{readSkill("/s/archify")}
	if got := names(m.usedSkills()); got != "diagramming" {
		t.Fatalf("want pi's canonical name, got %s", got)
	}
	out := stripANSI(m.buildSidebarContent())
	if strings.Contains(out, "archify") {
		t.Fatalf("directory name must not appear as a second row:\n%s", out)
	}
	if !strings.Contains(out, "diagrams") {
		t.Fatalf("description must follow the canonical name:\n%s", out)
	}
}

// Even without the catalogue, a /skill: block teaches the panel that its
// location and its name are the same skill, so the later read of that file
// must not open a second row under the directory name.
func TestSkillsBlockLocationUnifiesLaterRead(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{
		{Kind: "user", Text: "<skill name=\"diagramming\" location=\"/s/archify/SKILL.md\">\nbody\n</skill>"},
		readSkill("/s/archify"),
	}
	if got := names(m.usedSkills()); got != "diagramming" {
		t.Fatalf("want one row named diagramming, got %s", got)
	}
}

// A standalone *.md skill is still a real load, and the catalogue is what
// tells it apart from every other markdown file the agent reads.
func TestSkillsStandaloneMarkdownSkill(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("toeic-essay", "/s/skills/toeic.md", "essays")}
	m.blocks = []Block{
		{Kind: "tool", ToolName: "read", ToolStatus: "done", ToolArgsRaw: `{"path":"/s/notes.md"}`},
		{Kind: "tool", ToolName: "read", ToolStatus: "done", ToolArgsRaw: `{"path":"/s/skills/toeic.md"}`},
	}
	if got := names(m.usedSkills()); got != "toeic-essay" {
		t.Fatalf("want the catalogued standalone skill only, got %s", got)
	}
}

// /skill:name is expanded by pi into the user message; that block carries the
// canonical name, which wins over anything inferred from a path.
func TestSkillsDetectedFromExpandedSkillCommand(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{
		Kind: "user",
		Text: "<skill name=\"archify\" location=\"/x/skills/archify/SKILL.md\">\n" +
			"References are relative to /x/skills/archify.\n\n# Archify\n</skill>\n\nmake me a diagram",
	}}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify, got %s", got)
	}
}

// The block does not have to be the first line: withImages appends an image
// chip and restore() trims, so anchoring to the string is too brittle.
func TestSkillsBlockFoundMidMessage(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{
		Kind: "user",
		Text: "\n<skill name=\"council-mode\" location=\"/s/council-mode/SKILL.md\">\nbody\n</skill>",
	}}
	if got := names(m.usedSkills()); got != "council-mode" {
		t.Fatalf("want council-mode, got %s", got)
	}
}

// Authoring a skill is not using one, so write/edit must not light the panel.
func TestSkillsIgnoreAuthoringTools(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("brand-new", "/s/brand-new/SKILL.md", "d")}
	m.blocks = []Block{
		{Kind: "tool", ToolName: "write", ToolStatus: "done",
			ToolArgsRaw: `{"path":"/s/brand-new/SKILL.md","content":"---\nname: x\n---"}`},
		{Kind: "tool", ToolName: "edit", ToolStatus: "done",
			ToolArgsRaw: `{"path":"/s/brand-new/SKILL.md","oldString":"a","newString":"b"}`},
	}
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("write/edit must not count as skill use: %v", used)
	}
}

// Descriptions come from pi's own catalogue, dimmed on the row. A skill pi no
// longer lists still renders name-only instead of vanishing.
func TestSkillsShowPiDescriptions(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{
		skillCmd("archify", "/s/archify/SKILL.md", "Diagrams"),
		{Name: "/template:x", Source: "prompt", Description: "not a skill"},
	}
	m.blocks = []Block{readSkill("/s/archify"), readSkill("/s/unlisted")}
	out := stripANSI(m.buildSidebarContent())
	if !strings.Contains(out, "SKILLS (2)") {
		t.Fatalf("want two skills:\n%s", out)
	}
	if !strings.Contains(out, "archify  Diagrams") {
		t.Fatalf("missing pi description (name column must stay aligned):\n%s", out)
	}
	if !strings.Contains(out, "unlisted") {
		t.Fatalf("skill missing from the catalogue must still render:\n%s", out)
	}
	if strings.Contains(out, "not a skill") {
		t.Fatalf("prompt templates must not leak into SKILLS:\n%s", out)
	}
}

// A long description is elided, not wrapped: the row stays one line and the
// column never overflows.
func TestSkillsDescriptionElides(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/s/archify/SKILL.md",
		"Create polished, validated architecture and workflow diagrams as explorable HTML")}
	m.blocks = []Block{readSkill("/s/archify")}
	row := strings.Split(strings.TrimRight(m.renderSkillsSection(sideInnerW), "\n"), "\n")[1]
	if !strings.Contains(stripANSI(row), "…") {
		t.Fatalf("long description must elide: %q", stripANSI(row))
	}
	if w := lipgloss.Width(row); w > sideInnerW {
		t.Fatalf("row is %d cells wide, over %d: %q", w, sideInnerW, row)
	}
}

// The list is capped like its sibling sections, and says how much it hid.
func TestSkillsCapsLongList(t *testing.T) {
	m := New(nil, t.TempDir())
	for i := 0; i < skillsShowMax+4; i++ {
		n := string(rune('a'+i)) + "-skill"
		m.Cmds = append(m.Cmds, skillCmd(n, "/s/"+n+"/SKILL.md", ""))
		m.blocks = append(m.blocks, readSkill("/s/"+n))
	}
	out := stripANSI(m.renderSkillsSection(sideInnerW))
	if !strings.Contains(out, "SKILLS (14)") {
		t.Fatalf("header must count every skill used:\n%s", out)
	}
	if !strings.Contains(out, "… +4 more") {
		t.Fatalf("capped list must say what it hid:\n%s", out)
	}
	if n := strings.Count(out, "• "); n != skillsShowMax {
		t.Fatalf("want %d rows, got %d", skillsShowMax, n)
	}
}

// The section is a normal sidebar section: it can be turned off from the
// /pitago-setting Sidebar tab, and it is on by default.
func TestSkillsSectionToggle(t *testing.T) {
	if !DefaultSideVisible(SideSkills) {
		t.Fatal("SKILLS must default to visible — it stays empty until used")
	}
	m := New(nil, t.TempDir())
	m.blocks = []Block{readSkill("/s/archify")}
	if !strings.Contains(stripANSI(m.buildSidebarContent()), "archify") {
		t.Fatal("used skill must render by default")
	}
	m.ToggleSideSection(SideSkills)
	if m.SideVisible(SideSkills) {
		t.Fatal("toggle must hide the section")
	}
	if out := stripANSI(m.buildSidebarContent()); strings.Contains(out, "SKILLS") {
		t.Fatalf("hidden section must not render:\n%s", out)
	}
	if !LoadPrefs(m.prefsPath).SideVisible(SideSkills) {
		t.Fatal("toggle must persist to prefs")
	}
	m.ToggleSideSection(SideSkills)
	if !strings.Contains(stripANSI(m.buildSidebarContent()), "archify") {
		t.Fatal("second toggle must restore the section")
	}
}

// Rows must fit the sidebar column: the real 30-cell width for every drawn
// line, and the content rows alone at any width, since the shared separator is
// fixed to the box rather than the caller's width.
func TestSkillsRowsFitNarrowSidebar(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Cmds = []pirpc.RepoCommand{skillCmd("archify", "/s/archify/SKILL.md", "a fairly long description here")}
	m.blocks = []Block{readSkill("/s/archify"), readSkill("/s/another-skill")}
	for _, line := range strings.Split(strings.TrimRight(m.renderSkillsSection(sideInnerW), "\n"), "\n") {
		if w := lipgloss.Width(line); w > sideInnerW {
			t.Errorf("row is %d cells wide, over the %d-cell column: %q", w, sideInnerW, line)
		}
	}
	for _, inner := range []int{6, 8, 12, 20, sideInnerW} {
		rows := strings.Split(strings.TrimRight(m.renderSkillsSection(inner), "\n"), "\n")
		if len(rows) < 3 {
			t.Fatalf("inner=%d: expected header + 2 rows, got %q", inner, rows)
		}
		for _, line := range rows[1 : len(rows)-1] { // skip header and separator
			if w := lipgloss.Width(line); w > inner {
				t.Errorf("inner=%d: row width %d: %q", inner, w, line)
			}
		}
	}
}

// A heredoc body is content being written, not a path being read: an agent
// writing a test fixture that mentions skill paths is not loading a skill.
func TestSkillsIgnoreHeredocBody(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{{
		Kind: "tool", ToolName: "bash", ToolStatus: "done",
		ToolArgsRaw: `{"command":"cat > t.go <<'EOF'\nvar cases = []string{\"/s/one/SKILL.md\", \"/s/two/SKILL.md\"}\nEOF"}`,
	}}
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("heredoc body must not register skills: %v", used)
	}
	// The command in front of the heredoc still counts.
	m.blocks = []Block{{
		Kind: "tool", ToolName: "bash", ToolStatus: "done",
		ToolArgsRaw: `{"command":"cat /s/archify/SKILL.md <<'EOF'\nnoise\nEOF"}`,
	}}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify from the command half, got %s", got)
	}
}

// The panel is derived from the transcript, so a session swap must clear it and
// restoring a session must bring it back — with no state of its own to reset.
func TestSkillsFollowsTranscript(t *testing.T) {
	m := New(nil, t.TempDir())
	m.blocks = []Block{readSkill("/s/archify")}
	if got := names(m.usedSkills()); got != "archify" {
		t.Fatalf("want archify, got %s", got)
	}
	m.blocks = nil // new session
	if used := m.usedSkills(); len(used) != 0 {
		t.Fatalf("new session must clear the panel, got %v", used)
	}
}

// The scan runs on every sidebar build, and the overwhelming majority of blocks
// in a real transcript name no markdown file at all, so mayNameSkill has to
// keep them off the JSON-unmarshal path. "none" is the realistic arm; a
// transcript that is all skill loads is the worst case.
func BenchmarkUsedSkills(b *testing.B) {
	for _, kind := range []string{"none", "all"} {
		b.Run(kind, func(b *testing.B) {
			m := New(nil, b.TempDir())
			for i := 0; i < 2000; i++ {
				raw := `{"path":"src/app/file_%d.go","offset":1,"limit":200}`
				if kind == "all" {
					raw = `{"path":"/s/skill-%d/SKILL.md"}`
				}
				m.blocks = append(m.blocks, Block{
					Kind: "tool", ToolName: "read", ToolStatus: "done",
					ToolArgsRaw: fmt.Sprintf(raw, i),
				})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.usedSkills()
			}
		})
	}
}
