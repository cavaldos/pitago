package pirpc

// The pi-mcp-adapter server list.
//
// `pi mcp list --json` only reads ~/.pi/agent/mcp.json, and in an
// install where the adapter package owns MCP (settings.json carrying
// `"-builtin:mcp"`, `npm:pi-mcp-adapter` installed) that file does not
// exist: the CLI reports zero servers while the adapter has six live.
// Reading the adapter's own two files is therefore not a nicer source,
// it is the only one that has the servers.
//
// Both files are the same ones the adapter's panel reads (config.ts and
// metadata-cache.ts), and the numbers are the ones the panel prints:
//
//   - <agentDir>/mcp-adapter.json — the servers, their transport and
//     their `disabled` / `directTools` flags.
//   - <agentDir>/mcp-cache.json — the discovered tool schemas, which is
//     where the panel's per-server token estimate comes from.
//
// The result is normalized into McpServerInfo, the same record the
// `pi mcp list` path produces, so everything above the transport layer
// (ordering, rows, menus, rendering) is shared by both sources.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// McpAdapterConfigName is the adapter's config file inside the agent dir.
const McpAdapterConfigName = "mcp-adapter.json"

// McpAdapterCacheName is the adapter's tool-schema cache. Same path
// constant as the adapter's metadata-cache.ts.
const McpAdapterCacheName = "mcp-cache.json"

// AdapterScope is the Scope value given to adapter-owned servers. It
// marks them as not living in a pi mcp.json, which is also what makes
// SetMcpServerConfig target the adapter file (both use a `mcpServers`
// envelope, so the same writer works unchanged).
const AdapterScope = "adapter"

// mcpAdapterConfig is the subset of mcp-adapter.json the panel reads.
// Names are kept in DECLARATION order: a Go map would give a random
// iteration order, so the rows would reshuffle on every refresh, and
// the adapter's panel lists them as configured.
type mcpAdapterConfig struct {
	Servers map[string]mcpAdapterServer `json:"mcpServers"`
	order   []string
}

// UnmarshalJSON decodes the servers AND records their order, which a
// map cannot carry. json object key order is preserved in the file and
// is what the panel shows.
func (c *mcpAdapterConfig) UnmarshalJSON(raw []byte) error {
	var body struct {
		Servers map[string]mcpAdapterServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(stripPiBOM(raw), &body); err != nil {
		return err
	}
	c.Servers = body.Servers
	c.order = mcpServerNamesInOrder(stripPiBOM(raw))
	return nil
}

// mcpServerNamesInOrder walks the document's tokens and returns the
// mcpServers keys in file order. A hand-rolled walk is the only way to
// get this: encoding/json has no ordered-map type, and the alternative
// (sorting the names) would not match the panel.
func mcpServerNamesInOrder(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var skip json.RawMessage
	// Into the top-level object.
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := t.(string)
		if key != "mcpServers" {
			if dec.Decode(&skip) != nil {
				return nil
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return nil
		}
		var names []string
		for dec.More() {
			t, err := dec.Token()
			if err != nil {
				return nil
			}
			name, _ := t.(string)
			names = append(names, name)
			if dec.Decode(&skip) != nil {
				return nil
			}
		}
		return names
	}
	return nil
}

// mcpAdapterServer is one server entry. directTools is `true` for every
// tool of the server and a []string for a named subset (config.ts:
// entry.directTools is written either way); `disabled` is the adapter's
// negation of pi's `enabled`.
type mcpAdapterServer struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	URL         string            `json:"url"`
	Disabled    bool              `json:"disabled"`
	DirectTools directToolSetting `json:"directTools"`
}

// directToolSetting decodes adapter `directTools`, which is `true`, a
// []string of tool names, or absent.
type directToolSetting struct {
	all   bool
	names map[string]bool
	set   bool
}

func (d *directToolSetting) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("true")) {
		d.all, d.set = true, true
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil // false / a number / an object: treat as unset
	}
	d.set = true
	d.names = make(map[string]bool, len(names))
	for _, n := range names {
		d.names[n] = true
	}
	return nil
}

// direct reports whether one tool of this server is declared to the
// model directly.
func (d directToolSetting) direct(tool string) bool {
	if !d.set {
		return false
	}
	if d.all {
		return true
	}
	return d.names[tool]
}

// mcpAdapterCache is the subset of mcp-cache.json the panel reads.
type mcpAdapterCache struct {
	Servers map[string]mcpAdapterCached `json:"servers"`
}

// mcpAdapterCached holds one server's discovered tools.
type mcpAdapterCached struct {
	Tools []mcpCachedTool `json:"tools"`
}

// mcpCachedTool is one cached tool schema. Description is a *string so
// a tool with no description counts as length 0 rather than failing
// the decode, which is how the adapter's own estimateTokens reads it.
type mcpCachedTool struct {
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Schema      json.RawMessage `json:"inputSchema"`
}

// McpAdapterServers reads the adapter's configured servers, in file
// order (the panel shows them as declared), with their cached tools and
// the adapter's token estimate. It reports ok=false when there is no
// adapter config — the caller then falls back to `pi mcp list --json`,
// which is the right source when pi's builtin mcp extension is the one
// in charge.
//
// A cache that is missing or stale is NOT an error: the servers are
// still listed, they just carry no tools, which is what the adapter's
// own panel shows as "(not cached)".
func McpAdapterServers() (servers []McpServerInfo, ok bool, err error) {
	path := filepath.Join(PiAgentDir(), McpAdapterConfigName)
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return nil, false, nil // no adapter install: not an error
	}
	var cfg mcpAdapterConfig
	if err := json.Unmarshal(stripPiBOM(raw), &cfg); err != nil {
		return nil, true, err // it IS the adapter's, and it is broken
	}
	if len(cfg.Servers) == 0 {
		return nil, true, nil
	}
	cache := readMcpAdapterCache()
	out := make([]McpServerInfo, 0, len(cfg.Servers))
	// Walk the declaration order, falling back to a sorted walk if the
	// order scan came back empty (a hand-built map in a test, say).
	names := cfg.order
	if len(names) != len(cfg.Servers) {
		names = make([]string, 0, len(cfg.Servers))
		for name := range cfg.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		def, ok := cfg.Servers[name]
		if !ok {
			continue
		}
		s := McpServerInfo{
			Name:      name,
			Scope:     AdapterScope,
			Source:    path,
			Enabled:   !def.Disabled,
			State:     "connected",
			Transport: mcpAdapterTransport(def),
		}
		for _, t := range cache.Servers[name].Tools {
			if t.Name == "" {
				continue
			}
			s.Tools = append(s.Tools, t.Name)
			if !def.DirectTools.direct(t.Name) {
				continue
			}
			// The adapter's `directTools` selection is recorded as a
			// PER-TOOL exposure override, which is exactly what
			// ToolExposure already means. Setting it here (rather than
			// setting the server's Exposure to "direct") keeps the N/M
			// column and the ~N column counting the SAME tools:
			// a named subset must be direct per tool, not per server.
			if s.ToolExposure == nil {
				s.ToolExposure = map[string]string{}
			}
			s.ToolExposure[t.Name] = "direct"
			s.DirectTokens += mcpEstimateTokens(t)
		}
		if len(s.Tools) == 0 {
			// Nothing cached for it: the panel cannot claim a tool
			// count, and "starting" is the state it shows rather than
			// a live server with zero tools.
			s.State = "starting"
		}
		if !s.Enabled {
			s.State = "disconnected"
		}
		out = append(out, s)
	}
	return out, true, nil
}

// readMcpAdapterCache loads the schema cache, or an empty one when it
// is missing or unparseable: the servers are still worth listing.
func readMcpAdapterCache() mcpAdapterCache {
	var c mcpAdapterCache
	raw, err := os.ReadFile(filepath.Join(PiAgentDir(), McpAdapterCacheName))
	if err != nil {
		return c
	}
	if json.Unmarshal(stripPiBOM(raw), &c) != nil {
		return mcpAdapterCache{}
	}
	return c
}

// mcpAdapterTransport is the adapter's transport line: the URL for an
// HTTP server, the command line for a stdio one (same shape pi prints,
// so IsHTTP and the menu rows keep working).
func mcpAdapterTransport(def mcpAdapterServer) string {
	if def.URL != "" {
		return def.URL
	}
	if def.Command == "" {
		return ""
	}
	return strings.TrimSpace(def.Command + " " + strings.Join(def.Args, " "))
}

// mcpEstimateTokens is the adapter's estimateTokens (mcp-panel.ts:67):
//
//	ceil((len(name) + len(description) + len(JSON.stringify(inputSchema ?? {}))) / 4) + 10
//
// The constant is chars-per-token, the 10 is the per-tool framing the
// adapter charges. Reproducing it exactly is what makes pitago's ~N
// column match the panel the user is looking at.
//
// The lengths are JAVASCRIPT lengths: `.length` counts UTF-16 code
// units, while Go's len() counts UTF-8 bytes. A schema containing one
// 3-byte character (an arrow or a currency sign in a description) is
// then 2 "longer" in Go, and the ceil() tips over into the next token.
func mcpEstimateTokens(t mcpCachedTool) int {
	desc := ""
	if t.Description != nil {
		desc = *t.Description
	}
	// The schema is read as json.RawMessage, i.e. the cache file's own
	// bytes, so its length IS what JSON.stringify wrote — the adapter
	// writes this cache compactly, with no insignificant whitespace to
	// discount. Re-encoding it through a map would be both slower and
	// wrong: Go sorts object keys, so the string would no longer be the
	// one that was measured.
	// A tool with no inputSchema is measured as the "{}" that
	// `JSON.stringify(tool.inputSchema ?? {})` would produce, not as an
	// empty string: dropping those two characters tips a real schema
	// over a ceil() boundary and shifts every server's estimate by a
	// token.
	schema := "{}"
	if len(t.Schema) > 0 {
		schema = string(t.Schema)
	}
	n := mcpJSLen(t.Name) + mcpJSLen(desc) + mcpJSLen(schema)
	return (n+3)/4 + 10 // ceil(n/4) for a non-negative n, without importing math
}

// mcpJSLen is a JavaScript String#length: the number of UTF-16 code
// units, which is one per character except that anything outside the
// BMP takes two (an astral character is a surrogate pair in JS).
func mcpJSLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}
