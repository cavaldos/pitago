// Package theme holds pitago's TUI palettes (One Dark, Gruvbox, …).
// Pure + stdlib only: no lipgloss here, app converts strings via ApplyTheme.
package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ToolPalette is the chat tool-block palette: the frame color the block
// outline takes per execution state, plus the header accent per tool
// kind. An empty field falls back to the theme's own base slot (see
// Resolve), so a theme only states what it actually changes and every
// block still recolors when the theme is swapped.
type ToolPalette struct {
	Frame   string // neutral outline (also the success outline)
	Running string // outline while a call is in flight
	Error   string // outline of a failed call
	Read    string // header accent: read / view
	Write   string // header accent: write / create
	Edit    string // header accent: edit / patch
	Shell   string // header accent: bash / exec
	Dir     string // header accent: cd / workdir
	Search  string // header accent: grep / glob / ls
	Other   string // header accent: anything else
}

// Theme is one named palette. Fields map 1:1 to the color slots in
// src/app/styles.go (cAccent, cBorder, … + tool block frame/fill/accent
// slots + highlight row bg/fg).
type Theme struct {
	Name                                string
	Accent, Border, Muted, Text, Code   string
	Green, Red, Yellow, Cyan            string
	Plan                                string
	Input, InputDim                     string
	ToolPending, ToolSuccess, ToolError string
	ToolNeutral                         string // quiet fill: output with no execution state
	Tool                                ToolPalette
	HiBg, HiFg                          string
}

// builtins: Default keeps today's opencode-like monochrome exactly.
var builtins = []Theme{
	{
		Name:   "default",
		Accent: "15", Border: "240", Muted: "243", Text: "252", Code: "250",
		Green: "114", Red: "203", Yellow: "11", Cyan: "6",
		Input: "252", InputDim: "240",
		ToolPending: "#282832", ToolSuccess: "#283228", ToolError: "#3c2828",
		ToolNeutral: "#2b2b33",
		HiBg:        "238", HiFg: "15",
		Plan: "13",
	},
	{
		Name:   "one-dark",
		Accent: "#61AFEF", Border: "#3E4451", Muted: "#5C6370", Text: "#ABB2BF", Code: "#ABB2BF",
		Green: "#98C379", Red: "#E06C75", Yellow: "#E5C07B", Cyan: "#56B6C2",
		Input: "#61AFEF", InputDim: "#5C6370",
		ToolPending: "#2C323C", ToolSuccess: "#2C3A2E", ToolError: "#3E2C2C",
		HiBg: "#3E4451", HiFg: "#ABB2BF",
		Plan: "#C678DD",
	},
	{
		Name:   "gruvbox",
		Accent: "#FABD2F", Border: "#504945", Muted: "#928374", Text: "#EBDBB2", Code: "#EBDBB2",
		Green: "#B8BB26", Red: "#FB4934", Yellow: "#FABD2F", Cyan: "#83A598",
		Input: "#FABD2F", InputDim: "#928374",
		ToolPending: "#3C3836", ToolSuccess: "#32361A", ToolError: "#3C1F1A",
		HiBg: "#3C3836", HiFg: "#EBDBB2",
		Plan: "#B16286",
	},
	{
		Name:   "dracula",
		Accent: "#BD93F9", Border: "#44475A", Muted: "#6272A4", Text: "#F8F8F2", Code: "#F8F8F2",
		Green: "#50FA7B", Red: "#FF5555", Yellow: "#F1FA8C", Cyan: "#8BE9FD",
		Input: "#BD93F9", InputDim: "#6272A4",
		ToolPending: "#343746", ToolSuccess: "#2C3E35", ToolError: "#422C33",
		HiBg: "#44475A", HiFg: "#F8F8F2",
		Plan: "#BD93F9",
	},
	{
		Name:   "tokyo-night",
		Accent: "#7AA2F7", Border: "#414868", Muted: "#565F89", Text: "#C0CAF5", Code: "#C0CAF5",
		Green: "#9ECE6A", Red: "#F7768E", Yellow: "#E0AF68", Cyan: "#7DCFFF",
		Input: "#7AA2F7", InputDim: "#565F89",
		ToolPending: "#24283B", ToolSuccess: "#26332A", ToolError: "#3A2733",
		HiBg: "#414868", HiFg: "#C0CAF5",
		Plan: "#BB9AF7",
	},
	{
		Name:   "nord",
		Accent: "#88C0D0", Border: "#3B4252", Muted: "#616E88", Text: "#ECEFF4", Code: "#D8DEE9",
		Green: "#A3BE8C", Red: "#BF616A", Yellow: "#EBCB8B", Cyan: "#88C0D0",
		Input: "#88C0D0", InputDim: "#616E88",
		ToolPending: "#3B4252", ToolSuccess: "#39452F", ToolError: "#463039",
		HiBg: "#434C5E", HiFg: "#ECEFF4",
		Plan: "#B48EAD",
	},
	{
		Name:   "catppuccin-mocha",
		Accent: "#CBA6F7", Border: "#313244", Muted: "#6C7086", Text: "#CDD6F4", Code: "#BAC2DE",
		Green: "#A6E3A1", Red: "#F38BA8", Yellow: "#F9E2AF", Cyan: "#89DCEB",
		Input: "#CBA6F7", InputDim: "#6C7086",
		ToolPending: "#313244", ToolSuccess: "#24382C", ToolError: "#3D2A35",
		HiBg: "#45475A", HiFg: "#CDD6F4",
		Plan: "#CBA6F7",
	},
	{
		Name:   "catppuccin-latte",
		Accent: "#8839EF", Border: "#CCD0DA", Muted: "#9CA0B0", Text: "#4C4F69", Code: "#5C5F77",
		Green: "#40A02B", Red: "#D20F39", Yellow: "#DF8E1D", Cyan: "#04A5E5",
		Input: "#8839EF", InputDim: "#9CA0B0",
		ToolPending: "#E6E9EF", ToolSuccess: "#DCEBDA", ToolError: "#F3DCE0",
		// Latte's pending panel is already a light gray; the quiet fill
		// steps one notch toward the surface so a neutral block still
		// separates from the terminal background.
		ToolNeutral: "#EFF1F5",
		HiBg:        "#BCC0CC", HiFg: "#4C4F69",
		Plan: "#8839EF",
	},
	{
		Name:   "solarized-dark",
		Accent: "#268BD2", Border: "#073642", Muted: "#586E75", Text: "#839496", Code: "#93A1A1",
		Green: "#859900", Red: "#DC322F", Yellow: "#B58900", Cyan: "#2AA198",
		Input: "#268BD2", InputDim: "#586E75",
		ToolPending: "#073642", ToolSuccess: "#0F2F1E", ToolError: "#331313",
		HiBg: "#073642", HiFg: "#93A1A1",
		Plan: "#6C71C4",
	},
	{
		Name:   "solarized-light",
		Accent: "#268BD2", Border: "#EEE8D5", Muted: "#93A1A1", Text: "#657B83", Code: "#586E75",
		Green: "#859900", Red: "#DC322F", Yellow: "#B58900", Cyan: "#2AA198",
		Input: "#268BD2", InputDim: "#93A1A1",
		ToolPending: "#EEE8D5", ToolSuccess: "#E6EBD3", ToolError: "#F0D9D2",
		HiBg: "#EEE8D5", HiFg: "#586E75",
		Plan: "#6C71C4",
	},
	{
		Name:   "monokai",
		Accent: "#AB9DF2", Border: "#403E41", Muted: "#727072", Text: "#FCFCFA", Code: "#F8F8F2",
		Green: "#A9DC76", Red: "#FF6188", Yellow: "#FFD866", Cyan: "#78DCE8",
		Input: "#AB9DF2", InputDim: "#727072",
		ToolPending: "#383733", ToolSuccess: "#2E3A24", ToolError: "#3E242C",
		HiBg: "#403E41", HiFg: "#FCFCFA",
		Plan: "#AB9DF2",
	},
	{
		Name:   "github-dark",
		Accent: "#4493F8", Border: "#30363D", Muted: "#7D8590", Text: "#E6EDF3", Code: "#E6EDF3",
		Green: "#3FB950", Red: "#F85149", Yellow: "#D29922", Cyan: "#39C5CF",
		Input: "#4493F8", InputDim: "#7D8590",
		ToolPending: "#161B22", ToolSuccess: "#12261A", ToolError: "#2E1517",
		HiBg: "#21262D", HiFg: "#E6EDF3",
		Plan: "#D2A8FF",
	},
	{
		Name:   "github-light",
		Accent: "#0969DA", Border: "#D1D9E0", Muted: "#59636E", Text: "#1F2328", Code: "#1F2328",
		Green: "#1A7F37", Red: "#D1242F", Yellow: "#9A6700", Cyan: "#0A7EA4",
		Input: "#0969DA", InputDim: "#59636E",
		ToolPending: "#EAEEF2", ToolSuccess: "#DAFBE1", ToolError: "#FFEBE9",
		HiBg: "#E3E8EE", HiFg: "#1F2328",
		Plan: "#8250DF",
	},
	{
		Name:   "ayu-dark",
		Accent: "#FF8F40", Border: "#1F2733", Muted: "#626A73", Text: "#B3B1AD", Code: "#B3B1AD",
		Green: "#AAD94C", Red: "#F26D78", Yellow: "#E6B450", Cyan: "#39BAE6",
		Input: "#FF8F40", InputDim: "#626A73",
		ToolPending: "#131721", ToolSuccess: "#1D2B1A", ToolError: "#2E1B1E",
		HiBg: "#1F2733", HiFg: "#B3B1AD",
		Plan: "#D2A6FF",
	},
	{
		Name:   "palenight",
		Accent: "#C792EA", Border: "#3A3F58", Muted: "#676E95", Text: "#A6ACCD", Code: "#A6ACCD",
		Green: "#C3E88D", Red: "#F07178", Yellow: "#FFCB6B", Cyan: "#89DDFF",
		Input: "#C792EA", InputDim: "#676E95",
		ToolPending: "#333747", ToolSuccess: "#2B3A28", ToolError: "#3D2A33",
		HiBg: "#3A3F58", HiFg: "#A6ACCD",
		Plan: "#C792EA",
	},
	{
		Name:   "material-darker",
		Accent: "#82AAFF", Border: "#2A2A2A", Muted: "#545454", Text: "#EEFFFF", Code: "#B2CCD6",
		Green: "#C3E88D", Red: "#F07178", Yellow: "#FFCB6B", Cyan: "#89DDFF",
		Input: "#82AAFF", InputDim: "#545454",
		ToolPending: "#2A2A2A", ToolSuccess: "#243026", ToolError: "#332527",
		Tool: ToolPalette{Other: "#8A8A8A"}, // Muted is near-invisible bold
		HiBg: "#303030", HiFg: "#EEFFFF",
		Plan: "#C792EA",
	},
	{
		Name:   "everforest",
		Accent: "#D699B6", Border: "#3D484D", Muted: "#859289", Text: "#D3C6AA", Code: "#D3C6AA",
		Green: "#A7C080", Red: "#E67E80", Yellow: "#DBBC7F", Cyan: "#7FBBB3",
		Input: "#D699B6", InputDim: "#859289",
		ToolPending: "#343F44", ToolSuccess: "#333D2A", ToolError: "#3E2F33",
		HiBg: "#3D484D", HiFg: "#D3C6AA",
		Plan: "#D699B6",
	},
	{
		Name:   "kanagawa",
		Accent: "#7E9CD8", Border: "#2A2A37", Muted: "#727169", Text: "#DCD7BA", Code: "#DCD7BA",
		Green: "#98BB6C", Red: "#E46876", Yellow: "#E6C384", Cyan: "#7FB4CA",
		Input: "#7E9CD8", InputDim: "#727169",
		ToolPending: "#23232D", ToolSuccess: "#263020", ToolError: "#332026",
		HiBg: "#2A2A37", HiFg: "#DCD7BA",
		Plan: "#957FB8",
	},
	{
		Name:   "rose-pine",
		Accent: "#C4A7E7", Border: "#26233A", Muted: "#6E6A86", Text: "#E0DEF4", Code: "#908CAA",
		Green: "#9CCFD8", Red: "#EB6F92", Yellow: "#F6C177", Cyan: "#31748F",
		Input: "#C4A7E7", InputDim: "#6E6A86",
		ToolPending: "#1F1D2E", ToolSuccess: "#1E2E2C", ToolError: "#2E222E",
		HiBg: "#26233A", HiFg: "#E0DEF4",
		Plan: "#C4A7E7",
	},
	{
		Name:   "rose-pine-dawn",
		Accent: "#907AA9", Border: "#DFDAD9", Muted: "#797593", Text: "#575279", Code: "#575279",
		Green: "#56949F", Red: "#B4637A", Yellow: "#EA9D34", Cyan: "#286983",
		Input: "#907AA9", InputDim: "#797593",
		ToolPending: "#F2E9DE", ToolSuccess: "#DDE8D5", ToolError: "#EAD9DB",
		HiBg: "#DFDAD9", HiFg: "#575279",
		Plan: "#907AA9",
	},
	{
		Name:   "gruvbox-light",
		Accent: "#AF3A03", Border: "#D5C4A1", Muted: "#928374", Text: "#3C3836", Code: "#3C3836",
		Green: "#79740E", Red: "#9D0006", Yellow: "#B57614", Cyan: "#076678",
		Input: "#AF3A03", InputDim: "#928374",
		ToolPending: "#EBDBB2", ToolSuccess: "#DEE3C0", ToolError: "#EACFC4",
		HiBg: "#D5C4A1", HiFg: "#3C3836",
		Plan: "#8F3F71",
	},
	{
		Name:   "one-light",
		Accent: "#4078F2", Border: "#E5E5E6", Muted: "#A0A1A7", Text: "#383A42", Code: "#383A42",
		Green: "#50A14F", Red: "#E45649", Yellow: "#C18401", Cyan: "#0184BC",
		Input: "#4078F2", InputDim: "#A0A1A7",
		ToolPending: "#ECECEC", ToolSuccess: "#DCEBDA", ToolError: "#F2DADA",
		HiBg: "#E5E5E6", HiFg: "#383A42",
		Plan: "#A626A4",
	},
	{
		Name:   "zenburn",
		Accent: "#8CD0D3", Border: "#4F4F4F", Muted: "#7F7F7F", Text: "#DCDCCC", Code: "#DCDCCC",
		Green: "#7F9F7F", Red: "#CC9393", Yellow: "#E0CF9F", Cyan: "#93E0E3",
		Input: "#8CD0D3", InputDim: "#7F7F7F",
		ToolPending: "#4A4A4A", ToolSuccess: "#43513F", ToolError: "#524242",
		// Muted is too dim to carry a bold header name here, so the
		// catch-all accent is raised one step.
		Tool: ToolPalette{Other: "#BFBFBF"},
		HiBg: "#4F4F4F", HiFg: "#DCDCCC",
		Plan: "#DC8CC3",
	},
	{
		Name:   "tomorrow-night",
		Accent: "#81A2BE", Border: "#282A2E", Muted: "#969896", Text: "#C5C8C6", Code: "#C5C8C6",
		Green: "#B5BD68", Red: "#CC6666", Yellow: "#F0C674", Cyan: "#8ABEB7",
		Input: "#81A2BE", InputDim: "#969896",
		ToolPending: "#282A2E", ToolSuccess: "#232B20", ToolError: "#2E1F20",
		HiBg: "#373B41", HiFg: "#C5C8C6",
		Plan: "#B294BB",
	},
	{
		Name:   "tokyo-storm",
		Accent: "#7AA2F7", Border: "#24283B", Muted: "#565F89", Text: "#C0CAF5", Code: "#C0CAF5",
		Green: "#9ECE6A", Red: "#F7768E", Yellow: "#E0AF68", Cyan: "#7DCFFF",
		Input: "#7AA2F7", InputDim: "#565F89",
		ToolPending: "#1F2335", ToolSuccess: "#222B1F", ToolError: "#2F2129",
		HiBg: "#292E42", HiFg: "#C0CAF5",
		Plan: "#BB9AF7",
	},
}

// Names lists theme names in order.
func Names() []string {
	out := make([]string, 0, len(builtins))
	for _, t := range builtins {
		out = append(out, t.Name)
	}
	return out
}

// norm accepts "OneDark", "one_dark", "one dark" → "one-dark".
func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// Get returns the named theme, falling back to default. The result is
// always resolved (see Resolve), so callers can read every slot without
// a second step.
func Get(name string) Theme {
	t := find(name)
	return Resolve(t)
}

func find(name string) Theme {
	n := norm(name)
	for _, t := range builtins {
		if t.Name == n {
			return t
		}
	}
	// "onedark" without dash still matches.
	nospace := strings.ReplaceAll(n, "-", "")
	for _, t := range builtins {
		if strings.ReplaceAll(t.Name, "-", "") == nospace {
			return t
		}
	}
	return builtins[0]
}

// Resolve fills every unset slot from the theme's own base colors, so a
// palette that only overrides one accent still renders a complete, and
// still theme-derived, tool block:
//
//	fill     quiet/neutral → the pending panel, an already-neutral gray
//	frame    neutral      → Border; running → Muted; error → Red
//	accents  read → Cyan, write → Green, edit → Yellow, shell → Accent,
//	         cd → Code, search → Text, other → Muted
//
// A theme that states a slot keeps it, which is how a palette diverges
// from the default derivation (see zenburn, material-darker).
func Resolve(t Theme) Theme {
	if t.Plan == "" {
		t.Plan = "13"
	}
	if t.ToolNeutral == "" {
		t.ToolNeutral = t.ToolPending
	}
	fill := func(v, fallback string) string {
		if v == "" {
			return fallback
		}
		return v
	}
	t.Tool.Frame = fill(t.Tool.Frame, t.Border)
	t.Tool.Running = fill(t.Tool.Running, t.Muted)
	t.Tool.Error = fill(t.Tool.Error, t.Red)
	t.Tool.Read = fill(t.Tool.Read, t.Cyan)
	t.Tool.Write = fill(t.Tool.Write, t.Green)
	t.Tool.Edit = fill(t.Tool.Edit, t.Yellow)
	t.Tool.Shell = fill(t.Tool.Shell, t.Accent)
	t.Tool.Dir = fill(t.Tool.Dir, t.Code)
	t.Tool.Search = fill(t.Tool.Search, t.Text)
	t.Tool.Other = fill(t.Tool.Other, t.Muted)
	return t
}

// ThemePath is ~/.config/pitago/theme.json: {"theme":"one-dark"}.
func ThemePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "theme.json")
}

// Load reads the saved theme name ("": none saved).
func Load(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var v struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Theme)
}

// Save persists the theme name (0600, like keys.json).
func Save(path, name string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Store the canonical name so Load round-trips ("OneDark" → "one-dark").
	raw, _ := json.Marshal(map[string]string{"theme": Get(name).Name})
	return os.WriteFile(path, raw, 0o600)
}
