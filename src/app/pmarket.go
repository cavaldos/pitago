package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Marketplace: install new pi packages from the npm registry straight
// from the settings hub. Pi publishes no package index of its own; the
// "market" is npm filtered by the pi-package keyword every pi extension
// ships (verified across the installed packages).

const (
	marketTTL     = 5 * time.Minute
	marketTimeout = 15 * time.Second
	// One page per fetch: fast enough to keep the hub snappy, more pages
	// load on demand via the trailing "load more" row.
	marketPageSize = 100
	// npm installs are slow: same budget class as binary updates (120s).
	pluginChangeTimeout = 180 * time.Second
)

// marketURL builds one registry search page (rank order, zero-based).
func marketURL(from int) string {
	return fmt.Sprintf("https://registry.npmjs.org/-/v1/search?text=keywords:pi-package&size=%d&from=%d",
		marketPageSize, from)
}

// MarketEntry is one installable pi package from the npm registry.
type MarketEntry struct {
	Name    string
	Version string
	Desc    string
}

var (
	marketCacheData []MarketEntry
	marketCacheAt   time.Time
	marketInflight  bool
	marketTotal     int // registry total across pages (0 = unknown yet)
)

// marketFresh reports whether the cached market list is still usable
// (render path never touches the network).
func marketFresh() bool {
	return marketCacheData != nil && time.Since(marketCacheAt) < marketTTL
}

// npmSearch mirrors the registry search response (decoded fields only).
type npmSearch struct {
	Total   int `json:"total"`
	Objects []struct {
		Package struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Keywords    []string `json:"keywords"`
		} `json:"package"`
	} `json:"objects"`
}

// parseMarket decodes one registry search page, keeping rank order and
// dropping entries without the pi-package keyword. Total is the registry
// match count across all pages.
func parseMarket(raw []byte) (entries []MarketEntry, total int) {
	var res npmSearch
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, 0
	}
	for _, o := range res.Objects {
		p := o.Package
		if strings.TrimSpace(p.Name) == "" || !hasKeyword(p.Keywords, "pi-package") {
			continue
		}
		entries = append(entries, MarketEntry{Name: p.Name, Version: p.Version,
			Desc: strings.Join(strings.Fields(p.Description), " ")})
	}
	return entries, res.Total
}

func hasKeyword(kw []string, want string) bool {
	for _, k := range kw {
		if k == want {
			return true
		}
	}
	return false
}

func fetchMarketEntries(from int) (entries []MarketEntry, total int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), marketTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", marketURL(from), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("registry: %s", res.Status)
	}
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	entries, total = parseMarket([]byte(b.String()))
	return entries, total, nil
}

// MarketMsg carries one marketplace page back to the UI (Append merges a
// "load more" page into the loaded list).
type MarketMsg struct {
	Entries []MarketEntry
	Total   int
	Append  bool
	Err     error
}

// fetchMarketCmd loads the first market page in the background (hub stays
// open; the result arrives as MarketMsg and reloads the rows in place).
func (m Model) fetchMarketCmd() tea.Cmd {
	marketInflight = true
	m.Status = "loading marketplace…"
	m.Refresh()
	return func() tea.Msg {
		entries, total, err := fetchMarketEntries(0)
		return MarketMsg{Entries: entries, Total: total, Err: err}
	}
}

// FetchMarketPageCmd loads the next page after `loaded` entries.
// Exported: Enter actions live in src/builtin (see Confirmers).
func (m Model) FetchMarketPageCmd(loaded int) tea.Cmd {
	marketInflight = true
	m.Status = "loading more plugins…"
	m.Refresh()
	return func() tea.Msg {
		entries, total, err := fetchMarketEntries(loaded)
		return MarketMsg{Entries: entries, Total: total, Append: true, Err: err}
	}
}

// PluginChangeMsg reports a finished pi install/remove.
type PluginChangeMsg struct {
	Action string // install | remove
	Spec   string
	Out    string // one-line tail of pi's output (for error toasts)
	Err    error
}

// piBin resolves the pi binary the same way Spawn does (explicit option,
// $PI_BIN, then PATH).
func (m Model) piBin() string {
	if m.spawnOpts.Bin != "" {
		return m.spawnOpts.Bin
	}
	if b := os.Getenv("PI_BIN"); b != "" {
		return b
	}
	return "pi"
}

// ChangePluginCmd runs `pi install|remove <spec>` in the background (hub
// stays open; PluginChangeMsg refreshes the lists when it lands).
// Exported: Enter actions live in src/builtin (see Confirmers).
func (m Model) ChangePluginCmd(action, spec string) tea.Cmd {
	bin := m.piBin()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pluginChangeTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, action, spec).CombinedOutput()
		return PluginChangeMsg{Action: action, Spec: spec, Out: shortOut(out), Err: err}
	}
}

// shortOut collapses command output to one short line for toasts.
func shortOut(out []byte) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(string(out))), " ")
	const maxOut = 160
	if r := []rune(s); len(r) > maxOut {
		return string(r[:maxOut]) + "…"
	}
	return s
}

// marketInstalled reports whether a market package is already installed
// (settings.json spec suffix matches the registry name).
func marketInstalled(m *Model, name string) bool {
	for _, p := range m.Plugins {
		if pluginName(p.Spec) == name {
			return true
		}
	}
	return false
}
