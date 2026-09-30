package pirpc

// The pi CLI's MCP surface, for pitago's local /mcp manager.
//
// Pi's own /mcp is a TUI builtin (dist/extensions/mcp/index.js +
// ui.js), so it never arrives over the JSONL RPC and pitago has to
// re-implement it. Two different sources of truth are involved and
// both live here, in the transport layer:
//
//   - `pi mcp list --json` is the only way to learn a server's state,
//     tools, exposure and defining mcp.json. Listing is also what
//     CONNECTS the servers (there is no `pi mcp reconnect` verb), so
//     "reconnect" is a second list run.
//   - exposure and enable/disable have no CLI verb at all, so the
//     mcp.json named by the entry's `source` is edited in place, with
//     the same key-removal rules pi's updateMcpServerConfig uses.
//
// The UI half (rows, ordering, action menu) lives in src/builtin and
// the dialog state in src/app, so this package stays free of both.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Timeouts for the `pi mcp` verbs. `pi mcp list` connects to every
// enabled server, so a cold `npx`/`uvx` download is normal and the
// bound is generous; the OAuth login waits for a human in a browser,
// which is pi's own 300s default.
const (
	McpListTimeout   = 90 * time.Second
	McpLoginTimeout  = 300 * time.Second
	McpActionTimeout = 60 * time.Second
)

// McpServerInfo is one entry of `pi mcp list --json`. The field set
// mirrors what pi prints (verified against pi 0.99.1): name, scope
// (global|project), source (the mcp.json that defines it), enabled,
// exposure, transport (the command line, or the URL for HTTP
// servers), state, the tool names, the resource counts and the full
// connection error.
type McpServerInfo struct {
	Name              string   `json:"name"`
	Scope             string   `json:"scope"`
	Source            string   `json:"source"`
	Enabled           bool     `json:"enabled"`
	Exposure          string   `json:"exposure"`
	Transport         string   `json:"transport"`
	State             string   `json:"state"`
	Tools             []string `json:"tools"`
	Resources         int      `json:"resources"`
	ResourceTemplates int      `json:"resourceTemplates"`
	Error             string   `json:"error"`
}

// IsHTTP reports whether the server is a streamable-HTTP server: pi
// renders its transport as the URL for those (describeTransport in
// dist/extensions/mcp/index.js) and as "command args…" for stdio.
// `pi mcp list --json` carries no OAuth flag, so this is how /mcp
// knows an OAuth sign-out is even possible.
func (s McpServerInfo) IsHTTP() bool {
	return strings.HasPrefix(s.Transport, "http://") || strings.HasPrefix(s.Transport, "https://")
}

// NeedsSignIn reports whether the server is an HTTP server pi could
// not get past without a token.
func (s McpServerInfo) NeedsSignIn() bool { return s.State == "needs-auth" }

// McpList is the decoded `pi mcp list --json` document: the servers
// plus the config errors pi reports for entries it skipped.
type McpList struct {
	Servers []McpServerInfo `json:"servers"`
	Errors  []string        `json:"errors"`
}

// ParseMcpList decodes pi's JSON. A malformed document is an error
// rather than an empty list: silently showing "no MCP servers" for a
// file that actually declares them would send the user off editing
// the wrong mcp.json.
func ParseMcpList(raw []byte) (McpList, error) {
	var out McpList
	if err := json.Unmarshal(stripPiBOM(raw), &out); err != nil {
		return out, fmt.Errorf("pi mcp list: %w", err)
	}
	return out, nil
}

// RunMcp runs `pi mcp <args...>` with a context timeout and returns
// its combined output. exec passes args without a shell, so nothing
// here needs quoting — but the server name still goes through
// ValidMcpServerName, because a name is user input that pi's own CLI
// re-interprets.
//
// The child gets piChildEnviron(), the same environment the live pi
// session runs in. Without it a /login key (published child-only, see
// pienv.go) would be missing here, and an MCP server whose `env` or
// token comes from the process env would be reported failed by
// `pi mcp list` while the session next to it uses it fine. The overlay
// is nil when pitago has nothing to publish, so the common case
// inherits the shell env verbatim.
func RunMcp(bin string, timeout time.Duration, args ...string) (string, error) {
	if bin == "" {
		bin = "pi"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"mcp"}, args...)...)
	cmd.Env = piChildEnviron()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mcpServerNameRe is pi's rule for a server name (docs/mcp.md: "may
// only contain letters, digits, _ and -"). ValidMcpServerName
// enforces it before the name reaches the pi CLI, so a pasted
// `; rm -rf` or a path can never become an argument pi acts on.
var mcpServerNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidMcpServerName reports whether name is a legal MCP server name.
func ValidMcpServerName(name string) bool { return mcpServerNameRe.MatchString(name) }

// mcpIndentRe finds the file's own indentation, like pi's editMcpServers
// (the first indented line of the document). The rewrite keeps it, so
// a tab-indented mcp.json does not come back space-indented.
var mcpIndentRe = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// ErrUnreadableMcpConfig is returned when an mcp.json exists but does
// not parse as a JSON object. Rewriting it would drop every server in
// it, so the write is refused and the file is left byte-for-byte alone.
var ErrUnreadableMcpConfig = errors.New("pi mcp: refusing to rewrite an unreadable mcp.json")

// McpConfigPatch is the part of a server entry /mcp edits. A nil
// field is left untouched; pi's own rules for the two are mirrored in
// applyMcpPatch (the default value removes the key instead of writing
// it, so a server never carries redundant config).
type McpConfigPatch struct {
	Enabled  *bool
	Exposure *string
}

// SetMcpServerConfig writes patch into the mcp.json at file for the
// server named name, keeping every other key (and their order) and the
// file's indentation, BOM and permissions.
//
// The write is atomic with a compare-and-swap, because pi owns the
// same file and re-reads it on every start: a half-written mcp.json
// would drop the user's servers, and a blind rename could drop an
// edit pi made in between.
func SetMcpServerConfig(file, name string, patch McpConfigPatch) error {
	if file == "" {
		return os.ErrNotExist
	}
	if !ValidMcpServerName(name) {
		return fmt.Errorf("pi mcp: %q is not a valid MCP server name", name)
	}
	perm := filePerm(file, 0o644)
	for attempt := 0; attempt < settingsWriteAttempts; attempt++ {
		orig, root, err := readMcpConfigForWrite(file)
		if err != nil {
			return err
		}
		servers := newOrderedObject()
		if raw, ok := root.get("mcpServers"); ok && !isJSONNull(raw) {
			if err := servers.UnmarshalJSON(raw); err != nil {
				return fmt.Errorf("pi mcp: %s: mcpServers is not an object (%v); the file was left exactly as it is", file, err)
			}
		}
		raw, ok := servers.get(name)
		if !ok || isJSONNull(raw) {
			return fmt.Errorf("pi mcp: %s does not define MCP server %q", file, name)
		}
		entry := newOrderedObject()
		if err := entry.UnmarshalJSON(raw); err != nil {
			return fmt.Errorf("pi mcp: %s: server %q is not an object (%v); the file was left exactly as it is", file, name, err)
		}
		entry.applyMcpPatch(patch)
		if err := servers.setObject(name, entry); err != nil {
			return err
		}
		if err := root.setObject("mcpServers", servers); err != nil {
			return err
		}
		indent := "  "
		if orig != nil {
			if m := mcpIndentRe.FindSubmatch(orig); m != nil {
				indent = string(m[1])
			}
		}
		out, err := json.MarshalIndent(root, "", indent)
		if err != nil {
			return err
		}
		if hasPiBOM(file) {
			out = append([]byte(piBOM), out...)
		}
		swapped, err := writeFileAtomicCAS(file, orig, append(out, '\n'), perm)
		if err != nil {
			return err
		}
		if swapped {
			return nil
		}
		// pi won the race: re-read and re-apply on top of its newer file.
	}
	return fmt.Errorf("pi mcp: %s keeps changing underneath the write; nothing was written", file)
}

// applyMcpPatch mirrors pi's updateMcpServerConfig
// (dist/extensions/mcp/config.js): `enabled: true` and the default
// `exposure: "codemode"` DELETE the key instead of writing it, so the
// file keeps carrying only what differs from the default.
func (o *orderedObject) applyMcpPatch(patch McpConfigPatch) {
	if patch.Enabled != nil {
		if *patch.Enabled {
			o.del("enabled")
		} else {
			o.set("enabled", json.RawMessage("false"))
		}
	}
	if patch.Exposure != nil {
		if *patch.Exposure == "" || *patch.Exposure == "codemode" {
			o.del("exposure")
		} else {
			v, err := json.Marshal(*patch.Exposure)
			if err == nil {
				o.set("exposure", v)
			}
		}
	}
}

// readMcpConfigForWrite loads the mcp.json pitago is about to rewrite,
// returning the exact bytes read (for the compare-and-swap) and the
// parsed object. A missing or empty file yields an empty object; an
// unparseable one is refused (ErrUnreadableMcpConfig) rather than
// replaced, which would delete every server in it.
func readMcpConfigForWrite(file string) ([]byte, *orderedObject, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newOrderedObject(), nil
		}
		return nil, nil, err // permissions, a directory: do not touch it
	}
	if len(strings.TrimSpace(string(stripPiBOM(raw)))) == 0 {
		return raw, newOrderedObject(), nil
	}
	root := newOrderedObject()
	if err := root.UnmarshalJSON(stripPiBOM(raw)); err != nil {
		return nil, nil, fmt.Errorf("%w (%v): %s was left exactly as it is", ErrUnreadableMcpConfig, err, file)
	}
	return raw, root, nil
}
