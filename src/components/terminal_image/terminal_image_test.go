package terminal_image

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngData(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.Transparent)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func clearTerminalEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "ITERM_SESSION_ID",
		"TERM_PROGRAM", "TERM", "TMUX", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY",
		"PI_IMAGE_PROTOCOL", "PITAGO_IMAGE_PROTOCOL", "WARP_SESSION_ID",
		"WARP_TERMINAL_SESSION_ID", "WARP_TERMINAL_SESSION_UUID",
	} {
		t.Setenv(key, "")
	}
}

func TestDetectProtocolConservative(t *testing.T) {
	clearTerminalEnv(t)
	if got := DetectProtocol(); got != "" {
		t.Fatalf("unknown terminal must fall back, got %q", got)
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	if got := DetectProtocol(); got != Kitty {
		t.Fatalf("kitty = %q", got)
	}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "iterm2")
	if got := DetectProtocol(); got != ITerm2 {
		t.Fatalf("iterm2 = %q", got)
	}
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "")
	t.Setenv("TMUX", "/tmp/tmux")
	if got := DetectProtocol(); got != "" {
		t.Fatalf("tmux = %q", got)
	}
	t.Setenv("TMUX", "")
	t.Setenv("SSH_TTY", "/dev/ttys001")
	if got := DetectProtocol(); got != "" {
		t.Fatalf("ssh = %q", got)
	}
}

func TestDetectProtocolPiCompatibleOverridesAndWarp(t *testing.T) {
	clearTerminalEnv(t)
	t.Setenv("PI_IMAGE_PROTOCOL", "ITerm2")
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "kitty")
	if got := DetectProtocol(); got != ITerm2 {
		t.Fatalf("canonical override = %q", got)
	}
	t.Setenv("TMUX", "/tmp/tmux")
	if got := DetectProtocol(); got != ITerm2 {
		t.Fatalf("Pi override under tmux = %q", got)
	}
	t.Setenv("TMUX", "")
	t.Setenv("PI_IMAGE_PROTOCOL", "")
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "kitty")
	if got := DetectProtocol(); got != Kitty {
		t.Fatalf("pitago alias = %q", got)
	}
	t.Setenv("PITAGO_IMAGE_PROTOCOL", "")
	t.Setenv("WARP_TERMINAL_SESSION_UUID", "warp-session")
	if got := DetectProtocol(); got != Kitty {
		t.Fatalf("warp UUID = %q", got)
	}
}

func TestMalformedWebPAndUnsafeMimeFallback(t *testing.T) {
	malformed := make([]byte, 30)
	copy(malformed, "RIFF")
	copy(malformed[8:], "WEBPVP8 ")
	// Both dimensions are zero: parser must reject before Render divides.
	data := base64.StdEncoding.EncodeToString(malformed)
	if got := Render(data, "image/webp", Kitty, 20); got.Rows != 0 || len(got.Lines) != 1 || got.Lines[0] != "□" {
		t.Fatalf("malformed WebP layout = %+v", got)
	}

	short := make([]byte, 24)
	copy(short, "RIFF")
	copy(short[8:], "WEBPVP8L")
	if got := Render(base64.StdEncoding.EncodeToString(short), "image/webp", Kitty, 20); got.Rows != 0 || len(got.Lines) != 1 || got.Lines[0] != "□" {
		t.Fatalf("short WebP layout = %+v", got)
	}

	unsafe := "image/png\x1b]1337;File=evil\x07"
	if got := Render(pngData(t, 1, 1), unsafe, "", 20); got.Rows != 0 || len(got.Lines) != 1 || got.Lines[0] != "□" {
		t.Fatalf("unsafe MIME fallback = %+v", got)
	}
	invalid := base64.StdEncoding.EncodeToString([]byte("not an image"))
	if got := Render(invalid, unsafe, Kitty, 20); got.Rows != 0 || len(got.Lines) != 1 || got.Lines[0] != "□" || strings.ContainsAny(got.Lines[0], "\x1b") {
		t.Fatalf("undecodable unsafe image fallback = %+v", got)
	}
}

func TestRenderProtocolsAndFallback(t *testing.T) {
	data := pngData(t, 1, 1)
	kitty := Render(data, "image/png", Kitty, 20)
	if kitty.Rows != 10 || len(kitty.Lines) != 10 || !strings.HasPrefix(kitty.Lines[0], "\x1b_Ga=T") || !strings.Contains(kitty.Lines[0], "C=1,c=20,r=10") {
		t.Fatalf("kitty lines = %+v", kitty)
	}
	for i, line := range kitty.Lines[1:] {
		if line != "" {
			t.Fatalf("kitty logical blank line %d = %q", i+1, line)
		}
	}
	iterm := Render(data, "image/png", ITerm2, 20)
	if iterm.Rows != 10 || len(iterm.Lines) != 10 {
		t.Fatalf("iterm rows = %+v", iterm)
	}
	for i, line := range iterm.Lines[:9] {
		if line != "" {
			t.Fatalf("iterm logical blank line %d = %q", i+1, line)
		}
	}
	if !strings.HasPrefix(iterm.Lines[9], "\x1b[9A\x1b]1337;File=") || !strings.Contains(iterm.Lines[9], "width=20") {
		t.Fatalf("iterm final line = %q", iterm.Lines[9])
	}
	fallback := Render(data, "image/png", "", 20)
	if fallback.Rows != 0 || len(fallback.Lines) != 1 || fallback.Lines[0] != "□" || strings.ContainsAny(fallback.Lines[0], "\x1b") {
		t.Fatalf("fallback = %+v", fallback)
	}
}

func TestRenderAspectDimensionsMatchPiCellMath(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		columns, rows int
	}{
		{"square", 1, 1, 20, 10},
		{"landscape", 2, 1, 20, 5},
		{"portrait", 1, 2, 10, 10},
		{"very wide", 4, 1, 20, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(pngData(t, tc.width, tc.height), "image/png", Kitty, 20)
			if got.Columns != tc.columns || got.Rows != tc.rows || len(got.Lines) != tc.rows {
				t.Fatalf("layout = %+v, want columns=%d rows=%d", got, tc.columns, tc.rows)
			}
		})
	}
}
