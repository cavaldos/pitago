package markdown

import (
	"strings"
	"testing"
)

func TestLinkifyAbsolutePath(t *testing.T) {
	in := "check /private/tmp/pitago/src/main.go now"
	got := LinkifyPaths(in, "/work")
	wantTarget := "(file:///private/tmp/pitago/src/main.go)"
	if !strings.Contains(got, wantTarget) {
		t.Fatalf("absolute path not linkified:\n%s", got)
	}
	if !strings.Contains(got, "[") || !strings.Contains(got, "]") {
		t.Fatalf("expected markdown link syntax:\n%s", got)
	}
}

func TestLinkifyRelativeAgainstCwd(t *testing.T) {
	got := LinkifyPaths("open ./src/app/yank.go", "/work/proj")
	if !strings.Contains(got, "(file:///work/proj/src/app/yank.go)") {
		t.Fatalf("relative path not resolved against cwd:\n%s", got)
	}
	if !strings.Contains(got, "[./src/app/yank.go]") {
		t.Fatalf("label must keep the original relative path:\n%s", got)
	}
}

func TestLinkifyParentRelative(t *testing.T) {
	got := LinkifyPaths("see ../go.mod", "/work/proj/src")
	if !strings.Contains(got, "(file:///work/proj/go.mod)") {
		t.Fatalf("parent-relative path not resolved:\n%s", got)
	}
}

func TestLinkifyKeepsLineColInLabel(t *testing.T) {
	got := LinkifyPaths("at /work/proj/src/app/model.go:120:4", "/x")
	if !strings.Contains(got, "[/work/proj/src/app/model.go:120:4]") {
		t.Fatalf("label must keep :line:col:\n%s", got)
	}
	// Target is the clean path (no fragment in v1).
	if strings.Contains(got, ":120") {
		if strings.Contains(got, "](file:///work/proj/src/app/model.go:120:4)") {
			t.Fatalf("target must not embed line:col:\n%s", got)
		}
	}
}

func TestLinkifySkipsCodeSpans(t *testing.T) {
	in := "run `./script/build.sh` and ./src/main.go"
	got := LinkifyPaths(in, "/work")
	if strings.Contains(got, "[./script/build.sh]") {
		t.Fatalf("code-span path must not linkify:\n%s", got)
	}
	if !strings.Contains(got, "[./src/main.go]") {
		t.Fatalf("path outside code span must linkify:\n%s", got)
	}
}

func TestLinkifySkipsFencedBlocks(t *testing.T) {
	in := "before ./src/main.go\n```text\n./inside/fenced.go\n```\nafter ./src/other.go"
	got := LinkifyPaths(in, "/work")
	if strings.Contains(got, "[./inside/fenced.go]") {
		t.Fatalf("fenced path must not linkify:\n%s", got)
	}
	if !strings.Contains(got, "[./src/main.go]") || !strings.Contains(got, "[./src/other.go]") {
		t.Fatalf("prose paths must still linkify:\n%s", got)
	}
}

func TestRenderCwdSkipsLinkifyInPiRenderer(t *testing.T) {
	t.Setenv("PITAGO_RENDER", "pi")
	out := RenderCwd("see ./src/main.go", 80, "/work")
	if strings.Contains(out, "](file://") || strings.Contains(out, "(file://") {
		t.Fatalf("pi renderer must not receive injected link target: %q", out)
	}
}

func TestLinkifySkipsURLs(t *testing.T) {
	for _, in := range []string{"go to http://example.com/x", "go to https://example.com/x", "go to www.example.com/x"} {
		if got := LinkifyPaths(in, "/work"); strings.Contains(got, "(file://") {
			t.Fatalf("URL must not linkify: %q → %q", in, got)
		}
	}
}

func TestLinkifyNoMatch(t *testing.T) {
	in := "plain text, nothing to see here"
	if got := LinkifyPaths(in, "/work"); got != in {
		t.Fatalf("unchanged text must stay untouched: %q", got)
	}
}

// Glamour emits OSC 8 open/close pairs for the link we inject (the
// mechanism that lets terminals/Orca open the file). This pins the
// contract that replaces the plan's WrapOSC8 post-processor.
func TestGlamourEmitsOsc8ForLinkifiedPath(t *testing.T) {
	t.Setenv("PITAGO_RENDER", "go")
	// RenderCwd linkifies and renders through glamour; the resulting ANSI
	// must carry the OSC 8 open/close pair.
	src := LinkifyPaths("see ./src/app/yank.go", "/work/proj")
	out := RenderCwd(src, 80, "/work/proj")
	if !strings.Contains(out, "\x1b]8") {
		t.Fatalf("glamour must emit OSC 8 open; got:\n%q", out)
	}
	if !strings.Contains(out, "file:///work/proj/src/app/yank.go") {
		t.Fatalf("OSC 8 target missing:\n%q", out)
	}
	open := strings.Count(out, "\x1b]8")
	close_ := strings.Count(out, "\x1b]8;")
	if open < 2 || close_ < 1 {
		t.Fatalf("expected open and close markers, open=%d close=%d", open, close_)
	}
}

func TestRenderCwdLinksAssistant(t *testing.T) {
	t.Setenv("PITAGO_RENDER", "go")
	src := "# Hi\n\nsee ./src/app/yank.go"
	out := RenderCwd(src, 80, "/work")
	if !strings.Contains(out, "\x1b]8") {
		t.Fatalf("RenderCwd must produce OSC 8 for linkified path:\n%q", out)
	}
}
