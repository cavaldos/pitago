package app

import (
	"path/filepath"
	"strings"
	"testing"

	"pitago/src/components/theme"
	"pitago/src/pirpc"
)

// tidyBlock is a representative "edit" call: args name the file, and the
// result carries a real diff. Tidy mode must keep only the header.
func tidyEditBlock() Block {
	return Block{
		Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgs: "src/app/view.go",
		ToolDiff: "--- a\n+++ b\n@@ -1,3 +1,3 @@\n-old line\n+new line\n context",
	}
}

func tidyWriteBlock() Block {
	return Block{
		Kind: "tool", ToolName: "write", ToolStatus: "done",
		ToolArgs:    "game.js",
		ToolArgsRaw: `{"path":"game.js","content":"line1\nline2\nline3"}`,
		ToolResult:  "wrote 3 lines",
	}
}

func tidyShellBlock() Block {
	return Block{
		Kind: "tool", ToolName: "bash", Expanded: true, ToolStatus: "done",
		ToolArgs:   "go test ./...",
		ToolResult: strings.Repeat("ok  pitago/src/app  5.5s\n", 12),
	}
}

func tidyModel(t *testing.T) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.winW, m.winH = 120, 40
	m.Refresh()
	return m
}

// Tidy on: a tool block is one header line, with no args detail row, no
// diff, no result, no preview.
func TestTidyCollapsesToolBlocks(t *testing.T) {
	m := tidyModel(t)
	m.Tidy = true
	bl := tidyEditBlock()
	got := stripANSI(m.renderToolBlock(bl, 90))
	if !strings.Contains(got, "src/app/view.go") || strings.Contains(got, "new line") ||
		strings.Count(got, "\n") != 0 {
		t.Fatalf("collapsed edit must be one header line, got:\n%s", got)
	}
	bl.Expanded = true
	if got := stripANSI(m.renderToolBlock(bl, 90)); !strings.Contains(got, "new line") {
		t.Fatalf("clicked edit must show the diff, got:\n%s", got)
	}
}

// tidyContentLines counts the block's own rows, ignoring the frame rule.
func tidyContentLines(box string) int {
	n := 0
	for _, l := range strings.Split(box, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "╭") || strings.HasPrefix(t, "╰") {
			continue
		}
		n++
	}
	return n
}

// Tidy drops the write preview and the "no output"/"running…" detail rows.
func TestTidyDropsDetailAndWritePreview(t *testing.T) {
	m := tidyModel(t)
	m.Tidy = true

	out := m.renderToolBlock(tidyWriteBlock(), 90)
	if !strings.Contains(out, "game.js") {
		t.Errorf("tidy must keep the file name, got:\n%s", out)
	}
	if strings.Contains(out, "line1") || strings.Contains(out, "line2") {
		t.Errorf("tidy must drop the written content, got:\n%s", out)
	}
	// detail row ("no output") is gone too
	if strings.Contains(out, "no output") {
		t.Errorf("tidy must drop the detail row, got:\n%s", out)
	}

	// A running call keeps its header but not the "running…" row.
	run := tidyEditBlock()
	run.ToolStatus = "running"
	run.ToolDiff = ""
	r := m.renderToolBlock(run, 90)
	if strings.Contains(r, "running…") {
		t.Errorf("tidy must drop the running row, got:\n%s", r)
	}
}

// Shell blocks keep the command line and lose the output section.
func TestTidyCollapsesShellOutput(t *testing.T) {
	m := tidyModel(t)
	bl := tidyShellBlock()

	full := stripANSI(m.renderShellBlock(bl, 90))
	if !strings.Contains(full, "ok  pitago/src/app") {
		t.Fatalf("untidy shell block should show output, got:\n%s", full)
	}

	m.Tidy = true
	// The command row is chroma-highlighted, so escape codes sit between
	// tokens: strip before matching, or "go test ./..." never matches.
	tidy := stripANSI(m.renderShellBlock(bl, 90))
	if !strings.Contains(tidy, "go test ./...") {
		t.Errorf("tidy must keep the command, got:\n%s", tidy)
	}
	if strings.Contains(tidy, "ok  pitago/src/app") || strings.Contains(tidy, "Output") {
		t.Errorf("tidy must drop the shell output, got:\n%s", tidy)
	}
}

// Tidy does not disturb non-tool blocks.
func TestTidyLeavesPlainTextAlone(t *testing.T) {
	m := tidyModel(t)
	m.Tidy = true
	body := "here is a long assistant answer that should stay fully visible"
	off, _ := m.renderOneBlock(Block{Kind: "assistant", Text: body}, 90)
	on, _ := m.renderOneBlock(Block{Kind: "assistant", Text: body}, 90)
	if off != on {
		t.Error("tidy must not change assistant text rendering")
	}
	if !strings.Contains(on, "long assistant answer") {
		t.Errorf("assistant text should render, got:\n%s", on)
	}
}

// The render cache must key on Tidy, or flipping it would repaint stale rows.
func TestTidyInvalidatesRenderCache(t *testing.T) {
	bl := tidyEditBlock()
	bl.Expanded = true
	k1 := blockKey(bl, 90, false, false, "default", false)
	k2 := blockKey(bl, 90, false, false, "default", true)
	if k1 == k2 {
		t.Error("blockKey must differ when Tidy changes (stale cache otherwise)")
	}
}

// SetTidy persists to the global pref; ToggleTidy flips and reports.
func TestTidyPersistsGlobally(t *testing.T) {
	m := tidyModel(t)
	if got := m.SetTidy(true); !got || !m.Tidy {
		t.Fatal("SetTidy(true) should turn it on")
	}
	// A fresh read of the same prefs file sees it — that is what makes the
	// mode follow the user into every other project.
	if !LoadPrefs(m.prefsPath).Tidy {
		t.Error("tidy should be persisted to prefs.json")
	}
	if got := m.ToggleTidy(); got {
		t.Error("ToggleTidy should turn it off")
	}
	if LoadPrefs(m.prefsPath).Tidy {
		t.Error("tidy off should be persisted too")
	}
}

// Tidy on at startup is what Configure does. Configure derives its prefs
// path from $HOME, so redirect HOME rather than passing a path.
func TestTidyLoadedFromPrefs(t *testing.T) {
	defer ApplyTheme(theme.Get("default")) // Configure mutates palette globals
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, ".config", "pitago", "prefs.json")
	if err := SavePrefs(path, Prefs{Tidy: true}); err != nil {
		t.Fatal(err)
	}
	m := New(nil, home)
	m.Configure(pirpc.Options{}, filepath.Join(home, "keys.json"))
	if !m.Tidy {
		t.Errorf("Configure should restore tidy mode from %s", path)
	}

	// And a pref file without the key must not force it on.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	m2 := New(nil, home2)
	m2.Configure(pirpc.Options{}, filepath.Join(home2, "keys.json"))
	if m2.Tidy {
		t.Error("tidy should default to off when the pref is absent")
	}
}

// Tidy hides tool output, but a clicked-open block still shows it.
func TestTidyClickedBlockShowsDetail(t *testing.T) {
	m := tidyModel(t)
	m.Tidy = true
	bl := Block{Kind: "tool", ToolName: "read", ToolStatus: "done",
		ToolArgs: "test.go:1-80", ToolResult: "package main\nfunc main() {}"}
	if got := stripANSI(m.renderToolBlock(bl, 90)); strings.Contains(got, "package main") {
		t.Fatalf("tidy must hide output until clicked: %q", got)
	}
	bl.Expanded = true
	if got := stripANSI(m.renderToolBlock(bl, 90)); !strings.Contains(got, "package main") {
		t.Fatalf("clicked block must show output in tidy mode: %q", got)
	}
}
