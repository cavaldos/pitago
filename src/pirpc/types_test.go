package pirpc

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Prompt images must ride the RPC as pi expects:
// {"type":"prompt","message":...,"images":[{"type":"image",...}]}.
func TestPromptImagesMarshal(t *testing.T) {
	raw, err := json.Marshal(Command{
		Type:    "prompt",
		Message: "look @shot.png",
		Images:  []ImageContent{{Type: "image", Data: "aGk=", MimeType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{`"images"`, `"type":"image"`, `"mimeType":"image/png"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	var back Command
	if err := json.Unmarshal(raw, &back); err != nil || len(back.Images) != 1 {
		t.Fatalf("roundtrip: %v %+v", err, back)
	}
	// no images → field omitted (old pi parity, small lines)
	raw, _ = json.Marshal(Command{Type: "prompt", Message: "hi"})
	if strings.Contains(string(raw), "images") {
		t.Fatalf("images should be omitted: %s", raw)
	}
}

func TestPromptEmptyMessagePresent(t *testing.T) {
	// Regression: pi does command.message.startsWith(...) unguarded — an
	// omitted message crashes the prompt ("Cannot read properties of
	// undefined"). Tray-only sends (images, no text) must keep "message".
	raw, err := json.Marshal(Command{Type: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"message":""`) {
		t.Fatalf("empty message must be sent explicitly: %s", raw)
	}
}

func TestImageCount(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"hi"},{"type":"image","data":"aGk=","mimeType":"image/png"}]`)
	if n := ImageCount(raw); n != 1 {
		t.Fatalf("count = %d", n)
	}
	if got := ImagesOf(raw); len(got) != 1 || got[0].Data != "aGk=" || got[0].MimeType != "image/png" {
		t.Fatalf("images = %+v", got)
	}
	if n := ImageCount(json.RawMessage(`"plain"`)); n != 0 {
		t.Fatalf("plain count = %d", n)
	}
}

// SourceTag must mirror pi's getAutocompleteSourceTag.
func TestDecodeSelectOption(t *testing.T) {
	raw, _ := json.Marshal(SelectOption{Title: "Ship it", Description: "Deploy after tests"})
	encoded := SelectOptionPrefix + base64.RawURLEncoding.EncodeToString(raw)

	got, ok := DecodeSelectOption(encoded)
	if !ok || got.Title != "Ship it" || got.Description != "Deploy after tests" {
		t.Fatalf("decoded = %+v, ok=%v", got, ok)
	}
	if _, ok := DecodeSelectOption("legacy title"); ok {
		t.Fatal("legacy title must remain a plain string")
	}
	for _, bad := range []string{SelectOptionPrefix + "%%%", SelectOptionPrefix + base64.RawURLEncoding.EncodeToString([]byte(`{}`))} {
		if _, ok := DecodeSelectOption(bad); ok {
			t.Fatalf("malformed private value accepted: %q", bad)
		}
	}

	if !IsAskUserSelect(UIRequest{Method: "select", Options: []string{"plain", encoded}}) {
		t.Fatal("encoded select must carry the additive Ask User marker")
	}
	if IsAskUserSelect(UIRequest{Method: "confirm", Options: []string{encoded}}) {
		t.Fatal("confirm must never use rich Ask User rendering")
	}
}

func TestSourceTag(t *testing.T) {
	tag := func(scope, source string) string {
		return RepoCommand{Source: "extension",
			SourceInfo: &SourceInfo{Scope: scope, Source: source}}.SourceTag()
	}
	cases := map[string]string{
		"user|npm:pi-subagents":          "u:npm:pi-subagents",
		"project|npm:pi-subagents":       "p:npm:pi-subagents",
		"team|npm:x":                     "t:npm:x",
		"user|auto":                      "u",
		"project|local":                  "p",
		"team|cli":                       "t",
		"user|git:github.com/a/b.git#v1": "u:git:github.com/a/b@v1",
		"user|https://github.com/a/b":    "u:git:github.com/a/b",
		"user|plain-name":                "u",
		"user|":                          "u",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, "|", 2)
		if got := tag(parts[0], parts[1]); got != want {
			t.Errorf("tag(%q) = %q, want %q", in, got, want)
		}
	}
	if got := (RepoCommand{Source: "skill"}).SourceTag(); got != "" {
		t.Errorf("skill without sourceInfo = %q, want empty", got)
	}
	if got := (RepoCommand{Source: "builtin"}).SourceTag(); got != "" {
		t.Errorf("builtin = %q, want empty", got)
	}
}
