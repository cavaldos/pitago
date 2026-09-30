package pirpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The exact document `pi mcp list --json` prints (pi 0.99.1): one
// connected server with tools and resources, one failed one with the
// full connection error, and the empty errors array.
const piMcpListJSON = `{
  "servers": [
    {
      "name": "notion",
      "scope": "global",
      "source": "/Users/x/.pi/agent/mcp.json",
      "enabled": true,
      "exposure": "codemode",
      "transport": "npx -y @notionhq/notion-mcp-server",
      "state": "failed",
      "tools": [],
      "error": "Failed to resolve MCP server \"notion\" env \"NOTION_TOKEN\" from environment variable: NOTION_TOKEN"
    },
    {
      "name": "notion-suekou",
      "scope": "global",
      "source": "/Users/x/.pi/agent/mcp.json",
      "enabled": true,
      "exposure": "codemode",
      "transport": "npx -y @suekou/mcp-notion-server",
      "state": "connected",
      "tools": ["notion_search", "notion_fetch"],
      "resources": 2,
      "resourceTemplates": 0
    }
  ],
  "errors": []
}`

func TestParseMcpListPiPayload(t *testing.T) {
	list, err := ParseMcpList([]byte(piMcpListJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(list.Servers))
	}
	failed := list.Servers[0]
	if failed.Name != "notion" || failed.State != "failed" || failed.Enabled != true {
		t.Errorf("failed server = %+v", failed)
	}
	if !strings.Contains(failed.Error, "NOTION_TOKEN") {
		t.Errorf("the full connection error must survive the decode, got %q", failed.Error)
	}
	live := list.Servers[1]
	if live.State != "connected" || len(live.Tools) != 2 || live.Resources != 2 {
		t.Errorf("connected server = %+v", live)
	}
	if live.Scope != "global" || live.Source == "" || live.Exposure != "codemode" {
		t.Errorf("source/scope/exposure not decoded: %+v", live)
	}
	if len(list.Errors) != 0 {
		t.Errorf("errors = %v, want empty", list.Errors)
	}
}

func TestParseMcpListRejectsGarbage(t *testing.T) {
	// A malformed document must be an error, not an empty list: showing
	// "no MCP servers" for a file that declares them would send the
	// user off editing the wrong mcp.json.
	if _, err := ParseMcpList([]byte("not json")); err == nil {
		t.Fatal("garbage must not decode as an empty list")
	}
}

func TestValidMcpServerName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"notion", true},
		{"notion-suekou", true},
		{"ctx_7", true},
		{"A1", true},
		{"", false},
		{"has space", false},
		{"semi;colon", false},
		{"../etc/passwd", false},
		{"$HOME", false},
		{"a\nb", false},
		{strings.Repeat("x", 65), false},
	}
	for _, tc := range tests {
		if got := ValidMcpServerName(tc.name); got != tc.ok {
			t.Errorf("ValidMcpServerName(%q) = %v, want %v", tc.name, got, tc.ok)
		}
	}
}

// The write-back must change exactly one key of the server entry and
// leave every sibling (and every other server) byte-identical.
func TestSetMcpServerConfigPreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	orig := `{
  "mcpServers": {
    "docs": {
      "url": "https://example.com/mcp",
      "headers": { "Authorization": "Bearer ${DOCS_TOKEN}" },
      "exposure": "deferred"
    },
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    }
  },
  "autoEnableCodemode": false
}`
	if err := os.WriteFile(file, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMcpServerConfig(file, "docs", mcpExposurePatch("direct")); err != nil {
		t.Fatal(err)
	}
	root := ReadPiSettingsAt(file)
	if root == nil {
		t.Fatal("the rewritten file must still parse")
	}
	servers, _ := GetPiSetting(root, "mcpServers").(map[string]any)
	docs, _ := servers["docs"].(map[string]any)
	if docs["exposure"] != "direct" {
		t.Errorf("exposure = %v, want direct", docs["exposure"])
	}
	if docs["url"] != "https://example.com/mcp" {
		t.Errorf("sibling url lost: %v", docs["url"])
	}
	hdrs, _ := docs["headers"].(map[string]any)
	if hdrs["Authorization"] != "Bearer ${DOCS_TOKEN}" {
		t.Errorf("nested headers lost: %v", hdrs)
	}
	// The other server and the top-level key must be untouched.
	fs, _ := servers["filesystem"].(map[string]any)
	if fs["command"] != "npx" {
		t.Errorf("sibling server lost: %v", fs)
	}
	if root["autoEnableCodemode"] != false {
		t.Errorf("top-level autoEnableCodemode lost: %v", root["autoEnableCodemode"])
	}
	// Key ORDER is preserved (a Go map would have sorted it).
	raw, _ := os.ReadFile(file)
	if i, j := strings.Index(string(raw), `"url"`), strings.Index(string(raw), `"exposure"`); i > j {
		t.Errorf("key order was reshuffled:\n%s", raw)
	}
}

// pi's rule: `enabled: true` and the default `exposure: "codemode"`
// DELETE the key rather than writing it, so the file only ever carries
// what differs from the default (dist/extensions/mcp/config.js).
func TestSetMcpServerConfigDefaultRemovesKey(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte(
		`{"mcpServers":{"a":{"command":"x","exposure":"hidden","enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMcpServerConfig(file, "a", mcpExposurePatch("codemode")); err != nil {
		t.Fatal(err)
	}
	root := ReadPiSettingsAt(file)
	servers, _ := GetPiSetting(root, "mcpServers").(map[string]any)
	a, _ := servers["a"].(map[string]any)
	if _, has := a["exposure"]; has {
		t.Errorf("the default exposure must be removed, not written: %v", a)
	}
	// The other half of the entry is untouched, including enabled:false.
	if a["command"] != "x" || a["enabled"] != false {
		t.Errorf("siblings changed: %v", a)
	}

	on := true
	if err := SetMcpServerConfig(file, "a", McpConfigPatch{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	root = ReadPiSettingsAt(file)
	servers, _ = GetPiSetting(root, "mcpServers").(map[string]any)
	a, _ = servers["a"].(map[string]any)
	if _, has := a["enabled"]; has {
		t.Errorf("enabled:true is the default and must be removed: %v", a)
	}
}

func TestSetMcpServerConfigRejectsUnknownAndInvalid(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{"a":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMcpServerConfig(file, "nope", mcpExposurePatch("direct")); err == nil {
		t.Error("an entry the file does not define must be refused")
	}
	if err := SetMcpServerConfig(file, "a; rm -rf /", mcpExposurePatch("direct")); err == nil {
		t.Error("an illegal name must be refused before any write")
	}
	// Refused means untouched.
	raw, _ := os.ReadFile(file)
	if string(raw) != `{"mcpServers":{"a":{"command":"x"}}}` {
		t.Errorf("a refused write modified the file: %s", raw)
	}
}

// An unparseable mcp.json must never be replaced: doing so would delete
// every server in it.
func TestSetMcpServerConfigRefusesUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	broken := "{ \"mcpServers\": { \"a\": { "
	if err := os.WriteFile(file, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetMcpServerConfig(file, "a", mcpExposurePatch("direct"))
	if err == nil {
		t.Fatal("an unparseable mcp.json must refuse the write")
	}
	if !strings.Contains(err.Error(), "left exactly as it is") {
		t.Errorf("the refusal must say the file is untouched, got %v", err)
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != broken {
		t.Errorf("the broken file was rewritten: %s", raw)
	}
}

// A tab-indented file comes back tab-indented, like pi's editMcpServers.
func TestSetMcpServerConfigKeepsIndentation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(file, []byte("{\n\t\"mcpServers\": {\n\t\t\"a\": {\n\t\t\t\"command\": \"x\"\n\t\t}\n\t}\n}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMcpServerConfig(file, "a", mcpExposurePatch("hidden")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(string(raw), "\n\t\t\t\"exposure\"") {
		t.Errorf("indentation was not preserved:\n%s", raw)
	}
}

func mcpExposurePatch(v string) McpConfigPatch {
	return McpConfigPatch{Exposure: &v}
}

// `pi mcp` must run with the same environment as the live pi child: a
// /login key is published child-only (piChildEnviron), so without the
// overlay a server that needs that env would be reported failed by
// `pi mcp list` while the session beside it works.
func TestRunMcpSeesThePiChildEnvOverlay(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fake-pi")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s' \"$PITAGO_MCP_ENV_PROBE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITAGO_MCP_ENV_PROBE", "ambient")
	out, err := RunMcp(fake, 10*time.Second, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ambient" {
		t.Errorf("without an overlay the child must inherit the shell env, got %q", out)
	}
	SetPiChildEnv("PITAGO_MCP_ENV_PROBE", "from-login")
	defer clearPiChildEnv()
	out, err = RunMcp(fake, 10*time.Second, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if out != "from-login" {
		t.Errorf("the /login overlay did not reach `pi mcp`, got %q", out)
	}
}
