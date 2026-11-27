package app

import (
	"strings"
	"testing"

	"pitago/src/pirpc"
)

func strp(s string) *string { return &s }

func testDialog() *Dialog {
	return &Dialog{
		Kind:      "model",
		Options:   []string{"claude-opus-4-5", "llama-3.1-8b-instant"},
		Descs:     []string{"Claude Opus 4.5", "Llama 3.1 8B"},
		Providers: []string{"opencode", "groq"},
		Models: []pirpc.ModelInfo{
			{
				ID: "claude-opus-4-5", Name: "Claude Opus 4.5", Provider: "opencode",
				API: "anthropic-messages", BaseURL: "https://opencode.ai/zen",
				Reasoning: true, Input: []string{"text", "image"},
				Cost:          pirpc.ModelCost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
				ContextWindow: 200000, MaxTokens: 64000,
			},
			{
				ID: "llama-3.1-8b-instant", Name: "Llama 3.1 8B", Provider: "groq",
				API: "openai-completions", BaseURL: "https://api.groq.com/openai/v1",
				Input:         []string{"text"},
				ContextWindow: 131072, MaxTokens: 131072,
			},
		},
		Provs:      []string{"All", "groq", "opencode"},
		ProvConn:   map[string]bool{"opencode": true},
		ProvCursor: 2,
	}
}

func TestModelHost(t *testing.T) {
	if got := modelHost("https://api.groq.com/openai/v1"); got != "api.groq.com" {
		t.Fatalf("host = %q", got)
	}
	if got := modelHost(""); got != "" {
		t.Fatalf("empty host = %q", got)
	}
}

func TestModelCostPair(t *testing.T) {
	if got := modelCostPair(0, 0); got != "—" {
		t.Fatalf("zero = %q", got)
	}
	if got := modelCostPair(5, 25); got != "$5 / $25 per 1M" {
		t.Fatalf("pair = %q", got)
	}
}

func TestModelThinking(t *testing.T) {
	mi := pirpc.ModelInfo{ThinkingLevelMap: map[string]*string{"off": nil, "low": strp("low"), "high": strp("high")}}
	if got := modelThinking(mi); got != "high, low" {
		t.Fatalf("thinking = %q", got)
	}
	if got := modelThinking(pirpc.ModelInfo{Reasoning: true}); got != "on" {
		t.Fatalf("reasoning = %q", got)
	}
	if got := modelThinking(pirpc.ModelInfo{}); got != "—" {
		t.Fatalf("plain = %q", got)
	}
}

func TestRenderModelDetail(t *testing.T) {
	d := testDialog()
	d.Reindex()
	out := strings.Join(d.detailLines(100), "\n")
	for _, want := range []string{"claude-opus-4-5", "Name", "Provider", "API", "Host",
		"opencode.ai", "Context", "Max output", "In / Out", "$5 / $25 per 1M",
		"Cache r/w", "$0.5 / $6.25 per 1M", "Thinking", "Input", "text, image", "Key", "ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
	}
	// second model: groq key not connected → missing
	d.ProvCursor = 0 // All scope so both models are visible
	d.Reindex()
	d.Cursor = 1
	out = strings.Join(d.detailLines(100), "\n")
	if !strings.Contains(out, "missing") {
		t.Fatalf("expected missing key:\n%s", out)
	}
	// no specs → placeholder, never an empty panel
	empty := &Dialog{Kind: "model"}
	empty.Reindex()
	if ph := empty.detailLines(100); len(ph) == 0 || !strings.Contains(ph[0], "no specs") {
		t.Fatalf("expected placeholder, got %q", ph)
	}
}

// Wide terminals get three columns: providers | names | details, with the
// middle column showing names only (no "— desc" suffix).
func TestModelDialogThreeColumns(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 130, 45
	d := testDialog()
	d.Title = "Select model"
	d.Reindex()
	out := m.renderModelDialog(d)
	for _, want := range []string{"PROVIDERS", "DETAILS", "claude-opus-4-5",
		"$5 / $25 per 1M", "opencode.ai", "ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("3-col dialog missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "— Claude Opus 4.5 · opencode") {
		t.Fatalf("middle column must be name-only:\n%s", out)
	}
}

// Narrow terminals fold the specs into a footer panel (no DETAILS column).
func TestModelDialogNarrowFooter(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 100, 40
	d := testDialog()
	d.Title = "Select model"
	d.ProvCursor = 0 // show both providers in the model list
	d.Reindex()
	out := m.renderModelDialog(d)
	if strings.Contains(out, "DETAILS") {
		t.Fatalf("narrow dialog must not have a details column:\n%s", out)
	}
	if !strings.Contains(out, "llama-3.1-8b-instant") || strings.Contains(out, "Llama 3.1 8B · groq") {
		t.Fatalf("model rows must not repeat the provider:\n%s", out)
	}
	if !strings.Contains(out, "In / Out") {
		t.Fatalf("narrow dialog must keep the footer specs:\n%s", out)
	}
}

func TestSpecHaySearchable(t *testing.T) {
	d := testDialog()
	d.Filter = "image"
	d.Reindex()
	if len(d.FIdx) != 1 || d.FIdx[0] != 0 {
		t.Fatalf("image filter = %v", d.FIdx)
	}
}
