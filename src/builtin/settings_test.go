package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// fakePiState answers get_state (and nothing else) so loadSettingsState can
// be driven end to end: retry is not part of get_state, it only ever lives in
// pi's settings.json.
const fakePiState = `#!/usr/bin/env python3
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    cmd = json.loads(line)
    resp = {"id": cmd.get("id", ""), "type": "response",
            "command": cmd.get("type", ""), "success": True}
    if cmd.get("type") == "get_state":
        resp["data"] = {"model": {"id": "m", "provider": "p"},
                        "thinkingLevel": "high", "steeringMode": "all"}
    print(json.dumps(resp), flush=True)
`

// settingsStateFor runs loadSettingsState against a fake pi and a settings.json
// holding `body` ("" = no file at all).
func settingsStateFor(t *testing.T, body string) app.SettingsState {
	t.Helper()
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	bin := filepath.Join(dir, "fake-pi")
	if err := os.WriteFile(bin, []byte(fakePiState), 0o755); err != nil {
		t.Fatal(err)
	}
	pi, err := pirpc.Spawn(pirpc.Options{Bin: bin, Dir: dir})
	if err != nil {
		t.Fatalf("spawn fake pi: %v", err)
	}
	t.Cleanup(pi.Close)

	m := app.New(pi, dir)
	st, err := loadSettingsState(&m)
	if err != nil {
		t.Fatalf("loadSettingsState: %v", err)
	}
	return st
}

// Auto-retry is a settings.json value (get_state does not carry it) and pi's
// default is enabled: the row must read pi's file, never a pitago mirror.
func TestAutoRetryRowReadsPiSettings(t *testing.T) {
	if got := settingsStateFor(t, "").AutoRetry; !got {
		t.Error("missing settings.json must fall back to pi's default (enabled)")
	}
	if got := settingsStateFor(t, `{"retry":{"enabled":false}}`).AutoRetry; got {
		t.Error("retry.enabled=false in pi's settings.json must show as off")
	}
	// The rest of the live rows still come from get_state.
	st := settingsStateFor(t, `{"steeringMode":"all"}`)
	if st.Thinking != "high" || st.Steering != "all" {
		t.Errorf("live rows must still come from get_state, got %+v", st)
	}
}

// Pi parity: 7 live rows + 15 file/local rows (image block first, like the
// stock pi screenshot: skill commands, show images, image width, ...),
// grouped into sections for scanning.
func TestSettingsOptionsPiParity(t *testing.T) {
	st := app.SettingsState{
		Model: "m", Thinking: "high", Steering: "all", FollowUp: "all",
		AutoCompact: true, AutoRetry: true, Theme: "default",
		Vals: map[string]string{
			"enableSkillCommands": "on", "terminal.showImages": "on",
			"terminal.imageWidthCells": "60", "images.autoResize": "on",
			"images.blockImages": "off", "transport": "auto",
			"httpIdleTimeoutMs": "5 min", "cacheWarming": "streaming",
			"hideThinkingBlock": "off", "showCacheMissNotices": "off",
			"defaultProjectTrust": "Ask", "quietStartup": "off",
			"enableInstallTelemetry": "off",
		},
		HideThinking: false, AutocompleteMax: 10,
	}
	opts, descs, cats := settingsOptions(st)
	if len(opts) != 7+len(fileSettings) {
		t.Fatalf("expected %d rows, got %d", 7+len(fileSettings), len(opts))
	}
	if len(cats) != len(opts) || len(descs) != len(opts) {
		t.Fatalf("opts/descs/cats must parallel each other, got %d/%d/%d", len(opts), len(descs), len(cats))
	}
	for i, want := range []string{"Skill commands: on", "Show images: on", "Image width: 60",
		"Auto-resize images: on", "Block images: off"} {
		if opts[7+i] != want {
			t.Errorf("row %d: expected %q, got %q", 7+i, want, opts[7+i])
		}
	}
	if !strings.Contains(descs[7], "reconnects pi") {
		t.Errorf("file rows should warn about reconnect, got %q", descs[7])
	}
	// sections: live agent rows first, theme display, skill agent,
	// images together, network together, pitago-local last
	for i, want := range []string{"Agent", "Agent", "Agent", "Agent", "Agent", "Agent", "Display"} {
		if cats[i] != want {
			t.Errorf("live row %d: expected group %q, got %q", i, want, cats[i])
		}
	}
	if cats[7] != "Agent" || cats[8] != "Images" {
		t.Errorf("skill should stay Agent and images grouped, got %q/%q", cats[7], cats[8])
	}
	seen := map[string]bool{}
	for _, c := range cats {
		seen[c] = true
	}
	for _, want := range []string{"Agent", "Display", "Images", "Network", "Privacy", "Pitago"} {
		if !seen[want] {
			t.Errorf("missing group %q in %v", want, cats)
		}
	}
}

func TestNextValWraps(t *testing.T) {
	if nextVal([]string{"60", "80", "120"}, "120") != "60" {
		t.Error("should wrap to first")
	}
	if nextVal([]string{"on", "off"}, "bogus") != "on" {
		t.Error("unknown current should restart at first")
	}
}

func TestFileSettingValsDefaults(t *testing.T) {
	vals := fileSettingVals(nil) // missing file → pi defaults
	for path, want := range map[string]string{
		"enableSkillCommands": "on", "terminal.showImages": "on",
		"terminal.imageWidthCells": "60", "images.autoResize": "on",
		"images.blockImages": "off", "transport": "auto",
		"httpIdleTimeoutMs": "5 min", "cacheWarming": "streaming",
		"defaultProjectTrust": "Ask",
	} {
		if vals[path] != want {
			t.Errorf("%s: expected pi default %q, got %q", path, want, vals[path])
		}
	}
}
