package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// Shell mode runs real local commands, so these tests need a POSIX sh.
func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mode drives a POSIX sh")
	}
}

// runShellLine types line into the editor, presses Enter, and returns the
// model after the result block landed.
func runShellLine(t *testing.T, m *Model, line string) Model {
	t.Helper()
	m.ta.SetValue(line)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	outMsg, ok := cmd().(shellOutMsg)
	if !ok {
		t.Fatalf("Enter in shell mode must run a shell line, got %T", cmd())
	}
	got := nm.(Model)
	nm2, _ := got.Update(outMsg)
	return nm2.(Model)
}

func lastBashBlock(t *testing.T, m Model) Block {
	t.Helper()
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "bash" {
			return m.blocks[i]
		}
	}
	t.Fatalf("expected a bash block, got kinds %v", kindsOf(m))
	return Block{}
}

func kindsOf(m Model) []string {
	out := []string{}
	for _, b := range m.blocks {
		out = append(out, b.Kind)
	}
	return out
}

// A lone "!" on an empty editor opens shell mode and is consumed: it must not
// become editor text, and nothing may reach pi.
func TestLoneBangOnEmptyEditorEntersShellMode(t *testing.T) {
	requireShell(t)
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	got := nm.(Model)
	if cmd != nil {
		t.Error("entering shell mode must not start any async work")
	}
	if !got.shellOn {
		t.Fatal("a lone \"!\" on an empty editor must enter shell mode")
	}
	if got.ta.Value() != "" {
		t.Errorf("editor = %q, want the \"!\" consumed", got.ta.Value())
	}
	if got.ta.Placeholder != shellPlaceholder {
		t.Errorf("placeholder = %q, want the shell hint", got.ta.Placeholder)
	}
	if lines := log(); countType(lines, "prompt") != 0 || countType(lines, "bash") != 0 {
		t.Errorf("entering shell mode must not talk to pi, got %v", typesInLog(lines))
	}
}

// pi's own "!cmd" escape is untouched: only a LONE bang on an empty editor
// opens shell mode.
func TestBangWithTextStaysPiBashEscape(t *testing.T) {
	requireShell(t)
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())

	// Typing "!" into a non-empty editor is ordinary text.
	m.ta.SetValue("echo hi")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	got := nm.(Model)
	if got.shellOn {
		t.Error("\"!\" typed into a non-empty editor must not enter shell mode")
	}
	if got.ta.Value() != "echo hi!" {
		t.Errorf("editor = %q, want the bang inserted as text", got.ta.Value())
	}

	// "!cmd" still goes to pi's bash RPC (the model sees that one).
	m2 := New(pi, t.TempDir())
	m2.ta.SetValue("!echo pi")
	msg, ok := m2.submitInput()().(PiOpMsg)
	if !ok {
		t.Fatal("\"!cmd\" must stay pi's bash escape")
	}
	if msg.Op != "bash" || msg.Command != "echo pi" {
		t.Errorf("got op %q command %q, want bash \"echo pi\"", msg.Op, msg.Command)
	}
	if countType(log(), "bash") != 1 {
		t.Errorf("expected pi's bash RPC, got %v", typesInLog(log()))
	}
}

// The whole point of shell mode: one long-lived shell, so cd persists.
func TestShellCwdPersistsAcrossCommands(t *testing.T) {
	requireShell(t)
	pi, log := spawnFakePi(t, nil)
	dir := t.TempDir()
	m := New(pi, dir)
	t.Cleanup(m.stopShell)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)
	if !m.shellOn {
		t.Fatal("shell mode must be on")
	}

	m = runShellLine(t, &m, "cd "+dir)
	if b := lastBashBlock(t, m); strings.Contains(b.Text, "no such file") {
		t.Fatalf("cd failed: %q", b.Text)
	}
	if m.shell == nil {
		t.Fatal("the shell must stay alive after a command")
	}
	first := m.shell

	m = runShellLine(t, &m, "pwd")
	b := lastBashBlock(t, m)
	if !strings.Contains(b.Text, dir) {
		t.Errorf("pwd output = %q, want the directory cd moved to (%s)", b.Text, dir)
	}
	if m.shell != first {
		t.Error("a second command must reuse the same shell process")
	}
	if lines := log(); countType(lines, "prompt") != 0 || countType(lines, "bash") != 0 {
		t.Errorf("shell lines must never reach pi, got %v", typesInLog(lines))
	}
}

// Multi-line and stderr output land in the same block, and a failing command
// is marked as an error with its exit code.
func TestShellReportsStderrAndExitCode(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	t.Cleanup(m.stopShell)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)

	// The command runs in a child shell so the persistent one survives to
	// report a non-zero status.
	m = runShellLine(t, &m, `sh -c 'echo out; echo err 1>&2; exit 3'`)
	b := lastBashBlock(t, m)
	if !strings.Contains(b.Text, "out") || !strings.Contains(b.Text, "err") {
		t.Errorf("block = %q, want stdout and stderr", b.Text)
	}
	if !b.Err {
		t.Error("a non-zero exit must mark the block as an error")
	}
}

// Enter on an empty shell line is a no-op, not an empty command run.
func TestShellEmptyLineIsIgnored(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)
	m.ta.SetValue("   ")
	nm2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := nm2.(Model)
	if cmd != nil {
		t.Error("an empty shell line must not run anything")
	}
	if got.shellRunning {
		t.Error("an empty shell line must not latch the running flag")
	}
	if got.shell != nil {
		t.Error("an empty shell line must not start a shell")
	}
}

// Esc leaves shell mode, restores the pi placeholder and kills the process.
func TestEscLeavesShellModeAndKillsShell(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)
	m = runShellLine(t, &m, "cd /")
	if m.shell == nil {
		t.Fatal("expected a live shell")
	}
	proc := m.shell

	nm2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := nm2.(Model)
	if got.shellOn {
		t.Error("Esc must leave shell mode")
	}
	if got.shell != nil {
		t.Error("Esc must drop the shell handle")
	}
	if got.ta.Placeholder != promptPlaceholder {
		t.Errorf("placeholder = %q, want the pi prompt hint back", got.ta.Placeholder)
	}
	// dead() is set from Wait, which is asynchronous: give it a moment.
	for i := 0; i < 200 && !proc.dead(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !proc.dead() {
		t.Error("the shell process must be killed when shell mode ends")
	}
}

// A shell that dies mid-session (the user typed "exit") restarts on the next
// line and the exit is reported, not swallowed.
func TestDeadShellRestartsOnNextLine(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	dir := t.TempDir()
	m := New(pi, dir)
	t.Cleanup(m.stopShell)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)

	m = runShellLine(t, &m, "exit")
	// The failure surfaces as an error toast (notice blocks route there).
	if len(m.toasts) == 0 {
		t.Fatalf("a dead shell must report an error, got blocks %v and no toast", kindsOf(m))
	}
	last := m.toasts[len(m.toasts)-1]
	if !last.Err || !strings.Contains(last.Text, "shell exited") {
		t.Errorf("toast = %+v, want an error about the shell exiting", last)
	}
	if !m.shellOn {
		t.Error("a dead shell must not leave shell mode on its own")
	}

	m = runShellLine(t, &m, "pwd")
	if !strings.Contains(lastBashBlock(t, m).Text, dir) {
		t.Errorf("the restarted shell must start in the session cwd %s", dir)
	}
	if m.shell == nil {
		t.Error("the restarted shell must be kept for later lines")
	}
}

// A result that lands after Esc is dropped: the shell it belongs to is gone,
// so its output must not appear in the transcript.
func TestStaleShellResultIsDropped(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)

	// Simulate the in-flight result of a command that was already orphaned.
	stale := shellOutMsg{gen: m.shellGen - 1, command: "echo ghost", out: "ghost"}
	nm2, _ := m.Update(stale)
	got := nm2.(Model)
	for _, b := range got.blocks {
		if strings.Contains(b.Text, "ghost") {
			t.Fatalf("a stale shell result must not reach the transcript: %q", b.Text)
		}
	}
}

// The shell starts in the session cwd, not the process cwd of the terminal.
func TestShellStartsInSessionCwd(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	dir := t.TempDir()
	m := New(pi, dir)
	t.Cleanup(m.stopShell)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)
	m = runShellLine(t, &m, "pwd")
	b := lastBashBlock(t, m)
	if !strings.Contains(b.Text, dir) {
		t.Errorf("pwd = %q, want the session cwd %s", b.Text, dir)
	}
}

// Guard the fixture: a leaked `sh` per test would outlive the test binary.
func TestShellProcCleanupHelper(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	p, err := startShell(resolveShellBinary(""), dir, 1)
	if err != nil {
		t.Fatalf("startShell: %v", err)
	}
	t.Cleanup(p.close)
	if p.dead() {
		t.Error("a fresh shell must not report itself dead")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("session cwd must exist: %v", err)
	}
}

// shellModeModel is a model latched into shell mode, ready for key tests.
func shellModeModel(t *testing.T) Model {
	t.Helper()
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	got := nm.(Model)
	if !got.shellOn {
		t.Fatal("expected shell mode to be on")
	}
	return got
}

// ---- view -----------------------------------------------------------------

// The input box has to read as a shell, not as pi: the title names the mode
// and the shell that runs it, and the pi hints (send / plan / model) are
// gone — they are lies while nothing typed here reaches pi.
func TestShellModeLooksLikeShellMode(t *testing.T) {
	m := shellModeModel(t)
	m.winW = 100
	m.hideSide = true
	m.ta.SetWidth(m.mainW() - 6)
	out := stripANSI(m.renderInput())
	if !strings.Contains(out, "SHELL") {
		t.Errorf("input box must say SHELL: %q", out)
	}
	if !strings.Contains(out, filepath.Base(m.shellBin)) {
		t.Errorf("title must name the shell that runs (%s): %q", m.shellBin, out)
	}
	for _, lie := range []string{"↵ send", "Tab plan", "/ commands"} {
		if strings.Contains(out, lie) {
			t.Errorf("pi hint %q must not survive in shell mode: %q", lie, out)
		}
	}
	if !strings.Contains(out, "Esc exit") {
		t.Errorf("footer must offer the way out: %q", out)
	}

	// Back to pi's chrome after Esc.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	after := stripANSI(nm.(Model).renderInput())
	if strings.Contains(after, "SHELL") || !strings.Contains(after, "↵ send") {
		t.Errorf("leaving shell mode must restore the pi hints: %q", after)
	}
}

// The footer must stay one row: the input box is a fixed 6 rows and the
// viewport math in Update assumes it.
func TestShellModeFooterStaysOneRow(t *testing.T) {
	m := shellModeModel(t)
	m.winW, m.winH = 60, 24
	m.hideSide = true
	m.ta.SetWidth(m.mainW() - 6)
	if rows := strings.Split(m.renderInput(), "\n"); len(rows) != 6 {
		t.Errorf("input box = %d rows, want 6:\n%s", len(rows), m.renderInput())
	}
}

// ---- $SHELL / prefs -------------------------------------------------------

func TestResolveShellBinaryFollowsEnv(t *testing.T) {
	requireShell(t)
	t.Setenv("SHELL", "/bin/sh")
	if got := resolveShellBinary(""); got != "/bin/sh" {
		t.Errorf("unset pref must follow $SHELL, got %q", got)
	}
	if got := resolveShellBinary(shellEnvVal); got != "/bin/sh" {
		t.Errorf("%q must follow $SHELL, got %q", shellEnvVal, got)
	}
	t.Setenv("SHELL", "/nope/not-a-shell")
	if got := resolveShellBinary(""); got != "sh" {
		t.Errorf("a broken $SHELL must fall back to sh, got %q", got)
	}
}

func TestResolveShellBinaryPrefOverride(t *testing.T) {
	requireShell(t)
	t.Setenv("SHELL", "/bin/sh")
	if got := resolveShellBinary("/bin/bash"); got != "/bin/bash" {
		t.Errorf("an explicit pref must win over $SHELL, got %q", got)
	}
	if got := resolveShellBinary("/nope/nope"); got != "sh" {
		t.Errorf("an unusable override must degrade to sh, got %q", got)
	}
}

// The /settings row is a real knob: it persists and re-resolves. The shell
// already running is deliberately left alone.
func TestSetShellBinaryPersistsAndResolves(t *testing.T) {
	requireShell(t)
	m := &Model{prefsPath: filepath.Join(t.TempDir(), "prefs.json")}
	m.SetShellBinary("/bin/bash")
	if m.ShellBinaryPref() != "/bin/bash" || m.shellBin != "/bin/bash" {
		t.Errorf("pref=%q resolved=%q, want /bin/bash", m.ShellBinaryPref(), m.shellBin)
	}
	if got := LoadPrefs(m.prefsPath).ShellBinary; got != "/bin/bash" {
		t.Errorf("prefs.json shellBinary = %q, want /bin/bash", got)
	}
	m.SetShellBinary(shellEnvVal)
	if got := LoadPrefs(m.prefsPath).ShellBinary; got != shellEnvVal {
		t.Errorf("prefs.json shellBinary = %q, want %q", got, shellEnvVal)
	}
}

// The sentinel must print one marker line with the exit status: printf
// REUSES its format while arguments are left over (POSIX), so a format
// with more specifiers than the shell passes prints the marker twice and
// the next command's output boundary lands on the wrong line.
func TestShellMarkerPrintsExactlyOnce(t *testing.T) {
	requireShell(t)
	p, err := startShell("/bin/sh", t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	got := p.markerCmd("MARK")
	if want := "printf '\\n%s %d\\n' MARK $?\n"; got != want {
		t.Errorf("markerCmd = %q, want %q", got, want)
	}
	out := p.run(4, "echo before")
	if out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	if out.out != "before" {
		t.Errorf("out = %q, want exactly %q", out.out, "before")
	}
}

// fish spells the last status $status; with "$?" every exit code would
// decode as 0 and every failure would render as a success.
func TestShellMarkerFishSpelling(t *testing.T) {
	if got := (&shellProc{bin: "/usr/local/bin/fish"}).markerCmd("M1"); !strings.Contains(got, "$status") {
		t.Errorf("fish marker = %q, want $status", got)
	}
	if got := (&shellProc{bin: "/bin/bash"}).markerCmd("M1"); !strings.Contains(got, "$?") {
		t.Errorf("posix marker = %q, want $?", got)
	}
}

// ---- input handling -------------------------------------------------------

// ↑↓ recall the shell's own history. Recalling a sent message here would be
// a foot-gun: Enter runs the line, so a prompt would be executed as a
// command.
func TestShellHistoryRecallsCommandsNotMessages(t *testing.T) {
	pi, log := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())
	m.Mouse = true
	m.hist = []string{"what does this repo do"}
	m.histIdx = -1
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)

	m = runShellLine(t, &m, "echo one")
	m = runShellLine(t, &m, "echo two")
	if len(m.shellHist) != 2 {
		t.Fatalf("shell history = %v, want two commands", m.shellHist)
	}

	// Shift+↑ works with mouse reporting off too (wheel-proof).
	m.Mouse = false
	nm2, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	m = nm2.(Model)
	if m.ta.Value() != "echo two" {
		t.Errorf("Shift+↑ = %q, want the last command", m.ta.Value())
	}
	nm3, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	m = nm3.(Model)
	if m.ta.Value() != "echo one" {
		t.Errorf("Shift+↑↑ = %q, want the older command", m.ta.Value())
	}
	if m.histBrowsing() {
		t.Error("shell mode must never browse the sent-message history")
	}
	nm4, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = nm4.(Model)
	if m.ta.Value() != "echo two" {
		t.Errorf("Shift+↓ = %q, want the newer command", m.ta.Value())
	}
	nm5, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = nm5.(Model)
	if m.ta.Value() != "" {
		t.Errorf("↓ past the newest must return to the empty line, got %q", m.ta.Value())
	}

	// Plain ↓ with no shell browse must not fall through to the tray or to
	// pi's message history either (mouse on: with reporting off a plain ↓
	// is a wheel scroll and stays one).
	m.Mouse = true
	nm6, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm6.(Model)
	if m.ta.Value() != "echo two" {
		t.Errorf("plain ↓ = %q, want the newest shell command", m.ta.Value())
	}
	if m.trayFocus {
		t.Error("plain ↓ must not enter the image tray in shell mode")
	}
	if m.histIdx != -1 {
		t.Errorf("histIdx = %d, want -1 (pi history untouched)", m.histIdx)
	}
	if lines := log(); countType(lines, "prompt") != 0 || countType(lines, "bash") != 0 {
		t.Errorf("history recall must stay local, got %v", typesInLog(lines))
	}
}

// A recalled command is dropped with the mode: it was never typed here, and
// leaving it in the editor would let the next Enter send shell syntax to pi.
func TestEscDropsRecalledShellCommand(t *testing.T) {
	requireShell(t)
	m := shellModeModel(t)
	m = runShellLine(t, &m, "echo one")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	m = nm.(Model)
	if m.ta.Value() != "echo one" {
		t.Fatalf("recall failed: %q", m.ta.Value())
	}
	nm2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := nm2.(Model)
	if got.shellOn {
		t.Error("Esc must leave shell mode")
	}
	if got.ta.Value() != "" {
		t.Errorf("editor = %q, want the recalled command dropped", got.ta.Value())
	}
}

// Hand-typed text survives Esc (pi's idle Esc leaves the editor alone).
func TestEscKeepsTypedShellCommand(t *testing.T) {
	m := shellModeModel(t)
	m.ta.SetValue("ls -la")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := nm.(Model)
	if got.ta.Value() != "ls -la" {
		t.Errorf("editor = %q, want the typed command kept", got.ta.Value())
	}
}

// pi's own bindings must not fire under a half-typed command.
func TestPiKeysAreBlockedInShellMode(t *testing.T) {
	m := shellModeModel(t)
	cmds := [][]string{{"a", "b"}}
	m.Cmds = nil
	_ = cmds

	for _, km := range []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyCtrlN},
		{Type: tea.KeyCtrlP},
		{Type: tea.KeyCtrlR},
		{Type: tea.KeyCtrlT},
		{Type: tea.KeyCtrlY},
		{Type: tea.KeyCtrlO},
		{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true},
	} {
		before := m.isPlanMode()
		nm, cmd := m.Update(km)
		got := nm.(Model)
		if cmd != nil {
			t.Errorf("%v must not start any pi work in shell mode", km.Type)
		}
		if !got.shellOn {
			t.Fatalf("%v left shell mode", km.Type)
		}
		if got.isPlanMode() != before {
			t.Errorf("%v toggled plan mode", km.Type)
		}
		if len(got.Dialogs) != 0 {
			t.Errorf("%v opened a dialog", km.Type)
		}
		if got.Status == "opening new session…" || got.Status == "switching model…" {
			t.Errorf("%v started a session/model switch (status %q)", km.Type, got.Status)
		}
	}
}

// The keys that make shell mode usable must keep working.
func TestShellKeysStillWork(t *testing.T) {
	requireShell(t)
	m := shellModeModel(t)
	m.ta.SetValue("echo typed")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := nm.(Model)
	if got.ta.Value() != "echo typedx" {
		t.Errorf("editor = %q, want the rune inserted", got.ta.Value())
	}
	// Paste is the way a long command gets in.
	if piKeyTakenByShell(tea.KeyMsg{Type: tea.KeyCtrlV}) {
		t.Error("Ctrl+V (paste) must work in shell mode")
	}
	if piKeyTakenByShell(tea.KeyMsg{Type: tea.KeyCtrlC}) {
		t.Error("Ctrl+C (clear, then double-press quit) must work in shell mode")
	}
	if piKeyTakenByShell(tea.KeyMsg{Type: tea.KeyCtrlE}) ||
		piKeyTakenByShell(tea.KeyMsg{Type: tea.KeyCtrlG}) ||
		piKeyTakenByShell(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}) {
		t.Error("view-only keys must keep working in shell mode")
	}
}

// "/" is a path and "@" a shell word in shell mode: neither popup may open.
func TestNoPaletteOrMentionInShellMode(t *testing.T) {
	m := shellModeModel(t)
	m.Cmds = []pirpc.RepoCommand{{Name: "tmp"}, {Name: "help"}}
	for _, r := range "/tmp" {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(Model)
	}
	if m.cmdOpen || len(m.cmdItems) > 0 {
		t.Errorf("/ must not open the command popup in shell mode (items %v)", m.cmdItems)
	}
	if m.atOpen {
		t.Error("@ must not open the mention popup in shell mode")
	}
	// Same for an @-prefixed line.
	m.ta.Reset()
	for _, r := range "@src" {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(Model)
	}
	if m.atOpen {
		t.Error("@ must not open the mention popup in shell mode")
	}
}

// ---- /shortcuts -----------------------------------------------------------

// shortcutsDb is the source of truth for /shortcuts: every shell-mode key
// must be discoverable there.
func TestShellShortcutsAreListed(t *testing.T) {
	m := shellModeModel(t)
	rows := m.shortcutRows()
	want := map[string]string{
		"! (empty input)":            "shell mode",
		"↵ (shell mode)":             "local shell",
		"↑↓ / Shift+↑↓ (shell mode)": "Recall an earlier shell command",
		"Esc (shell mode)":           "Leave shell mode",
	}
	found := map[string]bool{}
	for _, r := range rows {
		desc, ok := want[r.key]
		if !ok {
			continue
		}
		found[r.key] = true
		if !strings.Contains(r.desc, desc) {
			t.Errorf("shortcut %q = %q, want it to mention %q", r.key, r.desc, desc)
		}
		if r.cat != "Input" {
			t.Errorf("shortcut %q in category %q, want Input", r.key, r.cat)
		}
	}
	for k := range want {
		if !found[k] {
			t.Errorf("missing shell-mode shortcut row %q in /shortcuts", k)
		}
	}
}

// ---- ANSI ------------------------------------------------------------------

// A command that colours itself (ls --color, git diff --color, grep
// --color) must not push raw escape bytes into the transcript: they would
// be fed straight to the chroma lexer that renders bash blocks, so colour
// would land on colour.
func TestShellOutputIsStrippedOfANSI(t *testing.T) {
	requireShell(t)
	m := shellModeModel(t)
	// printf the escapes literally: the point is what the shell emits, not
	// what the test source looks like.
	m = runShellLine(t, &m, `printf '\033[31mred\033[0m plain\n'`)
	b := lastBashBlock(t, m)
	if strings.ContainsRune(b.Text, 0x1b) {
		t.Errorf("escape byte reached the transcript: %q", b.Text)
	}
	if !strings.Contains(b.Text, "red plain") {
		t.Errorf("block = %q, want the text with the colours removed", b.Text)
	}
}

// pi's own bash paths carry the same hazard and get the same treatment:
// bashExecution streams output into a bash block, and "!cmd" (addBashBlock)
// renders pi's BashResult. Both must drop escape bytes at the boundary.
func TestPiBashOutputIsStrippedOfANSI(t *testing.T) {
	pi, _ := spawnFakePi(t, nil)
	m := New(pi, t.TempDir())

	// A bashExecution message inside message_end (restoring a past
	// session): this is pi's own "!cmd" echo.
	nm, _ := m.Update(eventMsg(t, "message_end", map[string]any{
		"message": map[string]any{
			"role":    "bashExecution",
			"command": "ls --color",
			"output":  "\x1b[31mred\x1b[0m plain",
		},
	}))
	got := nm.(Model)
	if b := lastBashBlock(t, got); strings.ContainsRune(b.Text, 0x1b) {
		t.Errorf("bashExecution kept escape bytes: %q", b.Text)
	} else if !strings.Contains(b.Text, "red plain") {
		t.Errorf("bashExecution block = %q, want the text uncoloured", b.Text)
	}

	// pi's "!cmd" result.
	code := 0
	m.addBashBlock(PiOpMsg{Op: "bash", Command: "ls --color",
		Bash: &pirpc.BashResult{Output: "\x1b[32mgreen\x1b[0m\n", ExitCode: &code}})
	if b := lastBashBlock(t, m); strings.ContainsRune(b.Text, 0x1b) {
		t.Errorf("!cmd kept escape bytes: %q", b.Text)
	} else if !strings.Contains(b.Text, "green") {
		t.Errorf("!cmd block = %q, want the text uncoloured", b.Text)
	}
}

// ---- per-keystroke cost ----------------------------------------------------

// A shell block must stay bounded: blockKey hashes a block's WHOLE text on
// every keystroke, so a block that stores a megabyte of command output
// makes every later keystroke re-hash it (measured 17.9 ms/op for 5 x 2 MB
// blocks against 1.74 ms/op for the same transcript at 1 KB per block).
// Nothing visible is lost: a bash block renders Short(bl.Text, 400).
func TestShellOutputIsCappedInTheBlock(t *testing.T) {
	requireShell(t)
	m := shellModeModel(t)
	// 5 KB of output: past the cap, and enough to prove the head survives.
	m = runShellLine(t, &m, `for i in $(seq 1 200); do echo "line $i padded out a bit"; done`)
	b := lastBashBlock(t, m)
	// The cap bounds the OUTPUT; the block also carries "$ <command>\n".
	if limit := shellOutCap + 128; len(b.Text) > limit {
		t.Errorf("block stores %d bytes, want <= %d", len(b.Text), limit)
	}
	if !strings.Contains(b.Text, "line 1 ") {
		t.Errorf("the head of the output must survive: %q", b.Text[:60])
	}
	if !strings.HasSuffix(b.Text, "…") {
		t.Errorf("a truncated block must say so: %q", b.Text[len(b.Text)-20:])
	}
	// Short output is untouched.
	m2 := runShellLine(t, &m, "echo tiny")
	if got := lastBashBlock(t, m2).Text; got != "$ echo tiny\ntiny" {
		t.Errorf("short output changed: %q", got)
	}
}

// The cap cuts on bytes, so it must never leave half a rune behind: a
// broken tail would paint as a replacement glyph in the transcript.
func TestCapShellOutputKeepsRunesIntact(t *testing.T) {
	for _, fill := range []int{shellOutCap - 1, shellOutCap - 2, shellOutCap - 3, shellOutCap - 4} {
		full := strings.Repeat("é", fill/2) + strings.Repeat("x", shellOutCap) // multi-byte first
		got := capShellOutput(full)
		if len(got) > shellOutCap+4 {
			t.Errorf("fill=%d: capped to %d bytes", fill, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("fill=%d: capped output is not valid UTF-8", fill)
		}
	}
	if got := capShellOutput("short"); got != "short" {
		t.Errorf("short output must pass through: %q", got)
	}
	if got := capShellOutput(strings.Repeat("x", shellOutCap)); got != strings.Repeat("x", shellOutCap) {
		t.Error("output at exactly the cap must not be touched")
	}
}
