package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// runWithin runs one line on p and fails instead of blocking the suite when
// the line never reaches its end marker.
func runWithin(t *testing.T, p *shellProc, line string) shellOutMsg {
	t.Helper()
	ch := make(chan shellOutMsg, 1)
	go func() { ch <- p.run(p.gen, line) }()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		p.close()
		t.Fatalf("%q hung the shell (end marker never seen)", line)
		return shellOutMsg{}
	}
}

// Lines that used to strand the marker: an unfinished quote or trailing
// backslash (the shell read the marker as part of the line), and commands
// that read stdin (they ate the marker as input). Each must finish, and the
// shell must still run the next line in the same cwd.
func TestShellLinesThatUsedToHang(t *testing.T) {
	requireShell(t)
	for _, bin := range []string{"/bin/sh", "zsh", "bash", "dash"} {
		if resolveShellBinary(bin) != bin {
			continue // not installed here
		}
		t.Run(bin, func(t *testing.T) {
			dir := t.TempDir()
			p, err := startShell(bin, "/", 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.close)
			runWithin(t, p, "cd "+dir)
			for _, line := range []string{`echo "abc`, `echo it's`, `echo a \`, "cat", "read x; echo got=$x"} {
				runWithin(t, p, line)
			}
			if r := runWithin(t, p, `echo "it's" ok`); r.out != "it's ok" {
				t.Errorf("quoting: out = %q, want %q", r.out, "it's ok")
			}
			r := runWithin(t, p, "pwd")
			if r.err != nil || !strings.Contains(r.out, dir) {
				t.Errorf("after the odd lines: pwd = %q (err %v), want the shell alive in %s", r.out, r.err, dir)
			}
		})
	}
}

// Ctrl+C while a line runs interrupts that command only: the line ends with
// a failing status and the same shell, still in its cwd, runs the next one.
func TestCtrlCInterruptsRunningShellLine(t *testing.T) {
	requireShell(t)
	pi, _ := spawnFakePi(t, nil)
	dir := t.TempDir()
	m := New(pi, t.TempDir())
	t.Cleanup(m.stopShell)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = nm.(Model)
	m = runShellLine(t, &m, "cd "+dir)
	first := m.shell

	m.ta.SetValue("sleep 30")
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if !m.shellRunning || cmd == nil {
		t.Fatal("Enter must start the line")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	// A line typed while one runs stays in the editor, not dropped.
	m.ta.SetValue("echo next")
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.ta.Value() != "echo next" {
		t.Errorf("editor = %q, want the typed-ahead line kept", m.ta.Value())
	}

	time.Sleep(200 * time.Millisecond) // let sleep start
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = nm.(Model)
	if !m.shellOn || !m.quitArm.IsZero() {
		t.Error("Ctrl+C on a running line must interrupt it, not leave the mode or arm quit")
	}
	var out tea.Msg
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		m.stopShell()
		t.Fatal("Ctrl+C did not stop the running command")
	}
	nm, _ = m.Update(out)
	m = nm.(Model)
	if m.shellRunning || !lastBashBlock(t, m).Err {
		t.Error("the interrupted line must finish and be marked failed")
	}

	m.ta.Reset()
	m = runShellLine(t, &m, "pwd")
	if m.shell != first || !strings.Contains(lastBashBlock(t, m).Text, dir) {
		t.Error("the shell must survive the interrupt with its cwd")
	}
}

// Esc kills what the shell started too: a background child must not keep
// running (and holding the output pipe) after shell mode is gone.
func TestShellCloseKillsChildren(t *testing.T) {
	requireShell(t)
	p, err := startShell("/bin/sh", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	r := runWithin(t, p, "sleep 60 >/dev/null 2>&1 & echo $!")
	if r.out == "" {
		t.Fatal("no child pid")
	}
	p.close()
	for i := 0; i < 200; i++ {
		if !processAlive(t, r.out) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("background child %s outlived the shell", r.out)
}

// A shell echo keeps its line breaks: `ls` output used to be squeezed onto
// one row with ⏎ markers, which broke anything laid out in columns.
func TestBashBlockKeepsLineBreaks(t *testing.T) {
	text := "$ ls\napp.test\nbin\ndocs"
	rows := bashBlockRows(text, 60, false)
	if len(rows) != 4 {
		t.Fatalf("rows = %q, want the command plus one row per output line", rows)
	}
	for _, r := range rows {
		if strings.Contains(r, "⏎") {
			t.Errorf("row %q still carries a ⏎ marker", r)
		}
	}
	if rows[1] != "app.test" || rows[3] != "docs" {
		t.Errorf("output rows = %q, want them as printed", rows[1:])
	}

	// Long output folds behind a hint; expanded shows every line.
	for i := 0; i < 30; i++ {
		text += "\nline"
	}
	collapsed := bashBlockRows(text, 60, false)
	if len(collapsed) != 1+bashPreviewLines+1 || !strings.Contains(collapsed[len(collapsed)-1], "+21 lines") {
		t.Errorf("collapsed tail = %q, want %d preview lines and a +21 lines hint", collapsed[len(collapsed)-1], bashPreviewLines)
	}
	if got := bashBlockRows(text, 60, true); len(got) != 34 {
		t.Errorf("expanded rows = %d, want all 34", len(got))
	}

	// A row wider than the frame is cut, not wrapped, so column art holds.
	wide := bashBlockRows("$ x\n"+strings.Repeat("M", 200), 40, false)
	if w := len([]rune(wide[1])); w > 40 {
		t.Errorf("wide row is %d cells, want at most 40", w)
	}
}
