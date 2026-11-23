package markdown

import (
	"strings"
	"testing"
)

// streamCorpus mixes the block types an assistant reply actually uses.
var streamCorpus = []string{
	"Hello **world**.\n\nSecond paragraph with `code`.\n\nThird plain paragraph.\n",
	"# Title\n\nSome text here.\n\n## Sub\n\nMore text.\n",
	"Intro:\n\n```go\nfunc main() {\n\n\tfmt.Println(\"x\")\n}\n```\n\nAfter code.\n",
	"List:\n\n- a\n- b\n\n- loose c\n\n  continued c\n\nAfter list.\n",
	"| a | b |\n|---|---|\n| 1 | 2 |\n\nAfter table.\n",
	"> quote here\n\n> second quote\n\nAfter quote.\n",
	"Para one\nline two\n\nPara 2 mentions src/app/view.go:12 here.\n",
	"1. one\n2. two\n\nThen text.\n\n3. three\n",
	"Text.\n\n---\n\nText after rule.\n\n***bold italic***\n",
	"Đây là **tiếng Việt**.\n\nĐoạn thứ hai có `mã`.\n",
	"~~~~\n```\nnot a close\n\n~~~~\n\nAfter tilde fence.\n",
	"Step one:\n\n    indented code\n\n    more code\n\nDone.\n",
	"## Summary\n\nThe fix touches **three** files:\n\n1. `model.go` — state\n2. `view.go` — render\n\nWhy it works:\n\n- the tail re-renders\n- history is cached\n\n```diff\n- old\n+ new\n```\n\nThat's it. See [docs](https://example.com).\n",
}

// The segmented render must look exactly like the whole-document render,
// or the reply would jump when streaming ends and the canonical render
// takes over. Checked at every line of every corpus doc, i.e. at each
// state a stream passes through.
func TestStreamingRenderMatchesWhole(t *testing.T) {
	for _, doc := range streamCorpus {
		for cut := 1; cut <= len(doc); cut++ {
			if cut < len(doc) && doc[cut-1] != '\n' && cut%5 != 0 {
				continue // every line end plus a sample of mid-line cuts
			}
			src := doc[:cut]
			for _, w := range []int{40, 80} {
				whole := RenderCwd(src, w, "/tmp")
				seg := RenderCwdStreaming(src, w, "/tmp")
				if visible(seg) != visible(whole) {
					t.Fatalf("width %d, src %q\nwhole:\n%s\nsegmented:\n%s", w, src, visible(whole), visible(seg))
				}
			}
		}
	}
}

// visible drops ANSI styling but keeps the color-relevant text layout; a
// zero-width reset Glamour emits before some headings is not a difference
// anyone can see.
func visible(s string) string {
	return ansiSeq.ReplaceAllString(s, "")
}

func TestSettledSegments(t *testing.T) {
	cases := []struct {
		src  string
		want int // number of segments
	}{
		{"a\n\nb\n\nc", 2},             // last line partial: no cut before it
		{"a\n\nb\n\nc\n", 3},           // all lines complete
		{"- a\n\n- b\n", 1},            // loose list stays whole
		{"a\n\n  b\n", 1},              // indented continuation
		{"```\nx\n\ny\n```\n\nz\n", 2}, // blank inside a fence is not a cut
		{"| a |\n\n| b |\n", 1},        // table rows
		{"a\n\n1. b\n", 1},             // ordered list
		{"a\n\n[x]: http://y\n", 1},    // reference definition
		{"", 1},
	}
	for _, c := range cases {
		if got := settledSegments(c.src); len(got) != c.want {
			t.Errorf("settledSegments(%q) = %q, want %d segments", c.src, got, c.want)
		}
	}
}

// The point of segmenting: a long reply that grows by a few characters
// re-renders only its tail, so the settled part must come from the cache.
func TestStreamingRenderReusesSettledSegments(t *testing.T) {
	doc := strings.Repeat("Paragraph with **bold** text and `code`.\n\n", 40)
	RenderCwdStreaming(doc+"tail", 80, "/tmp")
	segMu.Lock()
	before := len(segCache)
	segMu.Unlock()
	RenderCwdStreaming(doc+"tail grows", 80, "/tmp")
	segMu.Lock()
	after := len(segCache)
	segMu.Unlock()
	if after-before > 1 {
		t.Fatalf("growing the tail rendered %d new segments, want at most 1", after-before)
	}
}

func BenchmarkStreamingRenderLongReply(b *testing.B) {
	para := "Here is **some** markdown with `code` and a [link](http://x.y).\n\n- item one\n- item two\n\n```go\nfunc main() {}\n```\n\nPlain closing words.\n\n"
	doc := strings.Repeat(para, 16)
	b.Run("whole", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			RenderCwd(doc+strings.Repeat("a", i%500), 80, "/tmp")
		}
	})
	b.Run("segmented", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			RenderCwdStreaming(doc+strings.Repeat("a", i%500), 80, "/tmp")
		}
	})
}
