package pirpc

// The adapter loader's own checks. These are the numbers pitago prints
// next to every /mcp row, so a regression here silently puts a wrong
// token cost in front of the user — hence the hand-computed estimate
// rather than a golden file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpAdapterFixture writes a config + cache pair into dir and points the
// agent dir at it for the duration of the test.
func mcpAdapterFixture(t *testing.T, dir, cfg, cache string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, McpAdapterConfigName), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if cache != "" {
		if err := os.WriteFile(filepath.Join(dir, McpAdapterCacheName), []byte(cache), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", dir)
}

func TestMcpAdapterServersReadsConfigInFileOrder(t *testing.T) {
	dir := t.TempDir()
	mcpAdapterFixture(t, dir, `{"mcpServers":{
		"notion":        {"command":"npx","args":["-y","@notionhq/notion-mcp-server"],"directTools":true},
		"parallel-search":{"url":"https://search.parallel.ai/mcp","directTools":true},
		"tailwindcss":   {"command":"npx","args":["-y","tailwindcss-mcp-server"],"directTools":true,"disabled":true}
	}}`, `{"version":1,"servers":{
		"notion":         {"tools":[{"name":"a","description":"abc"}]},
		"parallel-search":{"tools":[{"name":"b"},{"name":"c"}]},
		"tailwindcss":    {"tools":[{"name":"d"}]}
	}}`)

	servers, ok, err := McpAdapterServers()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want the adapter's servers", ok, err)
	}
	// Declaration order, NOT the attention-first sort the pi path uses:
	// the adapter panel lists them as configured.
	want := []string{"notion", "parallel-search", "tailwindcss"}
	if len(servers) != len(want) {
		t.Fatalf("got %d servers, want %d", len(servers), len(want))
	}
	for i, n := range want {
		if servers[i].Name != n {
			t.Errorf("row %d = %q, want %q (declaration order)", i, servers[i].Name, n)
		}
	}

	notion := servers[0]
	if !notion.Enabled {
		t.Error("a server with no `disabled` key must be enabled")
	}
	if d, total := notion.ToolCounts(); d != total || total != 1 {
		t.Errorf("directTools:true must make every tool direct, got %d/%d", d, total)
	}
	// ceil((1 name + 3 desc + 2 "{}") / 4) + 10 = 2 + 10 = 12
	if notion.DirectTokens != 12 {
		t.Errorf("token estimate = %d, want 12", notion.DirectTokens)
	}
	if notion.Scope != AdapterScope || notion.Source == "" {
		t.Errorf("adapter servers must name their scope and file, got %q / %q", notion.Scope, notion.Source)
	}
	if !strings.HasPrefix(notion.Transport, "npx ") {
		t.Errorf("stdio transport = %q, want the command line", notion.Transport)
	}
	if servers[1].Transport != "https://search.parallel.ai/mcp" {
		t.Errorf("http transport = %q, want the URL", servers[1].Transport)
	}
	if servers[2].Enabled {
		t.Error("`disabled\": true` must disable a server")
	}
}

func TestMcpAdapterServersCountsOnlyDirectTools(t *testing.T) {
	dir := t.TempDir()
	// directTools as a NAME LIST: "b" is direct, "a" and "c" are not.
	mcpAdapterFixture(t, dir, `{"mcpServers":{"docs":{"url":"https://x.dev/mcp","directTools":["b"]}}}`,
		`{"version":1,"servers":{"docs":{"tools":[
			{"name":"a","description":"aaaa"},
			{"name":"b"},
			{"name":"c"}]}}}`)

	servers, ok, err := McpAdapterServers()
	if err != nil || !ok || len(servers) != 1 {
		t.Fatalf("ok=%v err=%v servers=%d", ok, err, len(servers))
	}
	// "a" is NOT direct, so its estimate must not be charged:
	// b alone = ceil((1 + 0 + 2)/4)+10 = 11.
	if servers[0].DirectTokens != 11 {
		t.Errorf("only the named direct tool may count, got %d, want 11", servers[0].DirectTokens)
	}
	if len(servers[0].Tools) != 3 {
		t.Errorf("all three tools must still be listed, got %v", servers[0].Tools)
	}
}

func TestMcpAdapterServersWithoutCacheStillLists(t *testing.T) {
	dir := t.TempDir()
	mcpAdapterFixture(t, dir, `{"mcpServers":{"docs":{"url":"https://x.dev/mcp"}}}`, "")

	servers, ok, err := McpAdapterServers()
	if err != nil || !ok {
		t.Fatalf("a missing cache is not a failure: ok=%v err=%v", ok, err)
	}
	if len(servers) != 1 || len(servers[0].Tools) != 0 {
		t.Fatalf("want the server with no tools, got %+v", servers)
	}
	// A server the cache has never seen is "starting", not "connected":
	// claiming a live server with zero tools would be a lie.
	if servers[0].State != "starting" {
		t.Errorf("state = %q, want starting", servers[0].State)
	}
	// And with no estimate at all the ~N column must stay hidden.
	if servers[0].DirectTokens != 0 {
		t.Errorf("no cache must mean no estimate, got %d", servers[0].DirectTokens)
	}
}

func TestMcpAdapterAbsentFallsBackToPi(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	// ok=false is what tells loadMcp to use `pi mcp list --json` instead.
	if _, ok, err := McpAdapterServers(); ok || err != nil {
		t.Errorf("no adapter config must report ok=false, err=nil (got ok=%v err=%v)", ok, err)
	}
}

// The enable/disable round trip. This is the loop the row's dot depends
// on: the toggle writes a key, the list is re-read, and the dot must
// be the other glyph. It failed before by writing pi's `enabled` into
// the adapter's file, which the adapter ignores — so the write
// succeeded, the reload saw the old state, and the dot never moved.
func TestMcpAdapterToggleRoundTrips(t *testing.T) {
	dir := t.TempDir()
	mcpAdapterFixture(t, dir, `{"mcpServers":{
		"live":{"url":"https://a.dev/mcp"},
		"off":{"url":"https://b.dev/mcp","disabled":true}}}`, "")
	path := filepath.Join(dir, McpAdapterConfigName)

	byName := func() map[string]McpServerInfo {
		servers, ok, err := McpAdapterServers()
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		m := map[string]McpServerInfo{}
		for _, s := range servers {
			m[s.Name] = s
		}
		return m
	}
	if got := byName(); !got["live"].Enabled || got["off"].Enabled {
		t.Fatalf("fixture must start live/enabled, off/disabled, got live=%v off=%v",
			got["live"].Enabled, got["off"].Enabled)
	}

	// Enable "off": the adapter's rule removes the key entirely.
	enable := false
	if err := SetMcpServerConfig(path, "off", McpConfigPatch{Disabled: &enable}); err != nil {
		t.Fatal(err)
	}
	got := byName()
	if !got["off"].Enabled {
		t.Error("enabling must survive the reload, so the dot can turn solid")
	}
	// And the file must not have gained a pi `enabled` key the adapter
	// would ignore on its next start.
	if strings.Contains(string(mustRead(t, path)), `"enabled"`) {
		t.Errorf("the adapter's file must carry `disabled`, never `enabled`:\n%s",
			mustRead(t, path))
	}

	// Disable it again.
	disable := true
	if err := SetMcpServerConfig(path, "off", McpConfigPatch{Disabled: &disable}); err != nil {
		t.Fatal(err)
	}
	got = byName()
	if got["off"].Enabled {
		t.Error("disabling must survive the reload, so the dot can go hollow")
	}
	// The sibling the user never touched is still there, still enabled.
	if !got["live"].Enabled {
		t.Error("toggling one server must not disturb another")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
