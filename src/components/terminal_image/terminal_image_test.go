package terminal_image

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
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

func TestIdEncoderUploadPlaceDelete(t *testing.T) {
	data := pngData(t, 1, 1)
	up := Upload(data, 7)
	if !strings.HasPrefix(up, "\x1b_Ga=t,f=100,q=2,i=7;") || !strings.HasSuffix(up, data+"\x1b\\") {
		t.Fatalf("upload = %q", up)
	}
	// Payloads larger than one chunk: first carries m=1, last carries m=0.
	big := strings.Repeat(data, 100)
	chunks := Upload(big, 8)
	if !strings.Contains(chunks, "\x1b_Ga=t,f=100,q=2,i=8,m=1;") || !strings.Contains(chunks, "\x1b_Gm=0;") {
		t.Fatalf("chunked upload framing lost: %q", chunks[:200])
	}
	place := PlaceUnicode(7, 20, 10)
	want := "\x1b_Ga=p,q=2,U=1,i=7,p=7,c=20,r=10,C=1\x1b\\"
	if place != want {
		t.Fatalf("place = %q, want %q", place, want)
	}
	// Place is payload-free: cheap to re-emit on every repaint.
	if len(place) > 64 {
		t.Fatalf("place must stay small, len=%d", len(place))
	}
}

func TestSizeMatchesRenderGeometry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		columns, rows int
	}{
		{"square", 1, 1, 20, 10},
		{"landscape", 2, 1, 20, 5},
		{"portrait", 1, 2, 10, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := pngData(t, tc.width, tc.height)
			c, r := Size(data, "image/png", 20)
			if c != tc.columns || r != tc.rows {
				t.Fatalf("size = %d,%d want %d,%d", c, r, tc.columns, tc.rows)
			}
			rr := Render(data, "image/png", Kitty, 20)
			if rr.Columns != c || rr.Rows != r {
				t.Fatalf("size/render disagree: %+v vs %d,%d", rr, c, r)
			}
		})
	}
	if c, r := Size("!!!", "image/png", 20); c != 0 || r != 0 {
		t.Fatalf("bad data size = %d,%d", c, r)
	}
	if c, r := Size(pngData(t, 1, 1), "image/png", 0); c != 0 || r != 0 {
		t.Fatalf("bad width size = %d,%d", c, r)
	}
}

func TestUnicodePlaceholders(t *testing.T) {
	if got := PlaceholderChar; got != "\U0010EEEE" {
		t.Fatalf("placeholder char = %q", got)
	}
	if got := PlaceUnicode(9, 20, 10); got != "\x1b_Ga=p,q=2,U=1,i=9,p=9,c=20,r=10,C=1\x1b\\" {
		t.Fatalf("unicode place = %q", got)
	}
	// Small enough to re-emit on every repaint without resending payloads.
	if len(PlaceUnicode(9, 20, 10)) > 64 {
		t.Fatalf("unicode place must stay small")
	}
}

func TestDiacriticTableOrder(t *testing.T) {
	// kitty spec vectors: value 0 → U+0305, 1 → U+030D, 2 → U+030E.
	for n, want := range map[int]rune{0: 0x0305, 1: 0x030D, 2: 0x030E} {
		if got, ok := Diacritic(n); !ok || got != want {
			t.Fatalf("diacritic(%d) = %U,%v want %U", n, got, ok, want)
		}
	}
	if _, ok := Diacritic(-1); ok {
		t.Fatal("negative diacritic must fail")
	}
	if _, ok := Diacritic(len(diacriticTable)); ok {
		t.Fatal("over-table diacritic must fail")
	}
	if len(diacriticTable) < 200 {
		t.Fatalf("table holds %d entries, need 200 for max-width placements", len(diacriticTable))
	}
}

func TestPlaceholderRowMatchesSpecVector(t *testing.T) {
	// kitty docs' own 2x2 example for image id 42, first row:
	// printf "\e[38;5;42m\U10EEEE\U0305\U0305\U10EEEE\U0305\U030D\e[39m"
	want := "\x1b[38;5;42m\U0010EEEE\u0305\u0305\U0010EEEE\u0305\u030D\x1b[39m"
	got, ok := PlaceholderRow(42, 0, 2)
	if !ok || got != want {
		t.Fatalf("row = %q,%v want %q", got, ok, want)
	}
	got1, ok := PlaceholderRow(42, 1, 2)
	want1 := "\x1b[38;5;42m\U0010EEEE\u030D\u0305\U0010EEEE\u030D\u030D\x1b[39m"
	if !ok || got1 != want1 {
		t.Fatalf("row1 = %q,%v want %q", got1, ok, want1)
	}
	if _, ok := PlaceholderRow(42, 0, 0); ok {
		t.Fatal("zero cols must fail")
	}
}

func jpegTestData(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 200, G: 50, B: 50, A: 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func gifTestData(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	if err := gif.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// 1bpp lossless webp from golang.org/x/image testdata (gopher-doc).
const webpLosslessTestData = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func TestToPNG(t *testing.T) {
	// PNG passes through byte-identical.
	png := pngData(t, 2, 2)
	if got, ok := ToPNG(png, "image/png"); !ok || got != png {
		t.Fatalf("png passthrough = %v", ok)
	}
	for _, tc := range []struct {
		name  string
		data  string
		mime  string
		wantW int
		wantH int
	}{
		{"jpeg", jpegTestData(t, 3, 2), "image/jpeg", 3, 2},
		{"gif", gifTestData(t, 3, 2), "image/gif", 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ToPNG(tc.data, tc.mime)
			if !ok {
				t.Fatalf("transcode failed")
			}
			raw, err := base64.StdEncoding.DecodeString(got)
			if err != nil {
				t.Fatal(err)
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil || format != "png" {
				t.Fatalf("output = %s,%v", format, err)
			}
			if cfg.Width != tc.wantW || cfg.Height != tc.wantH {
				t.Fatalf("dims = %dx%d", cfg.Width, cfg.Height)
			}
		})
	}
	t.Run("webp", func(t *testing.T) {
		raw, _ := base64.StdEncoding.DecodeString(webpLosslessTestData)
		want, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("test webp itself undecodable: %v", err)
		}
		got, ok := ToPNG(webpLosslessTestData, "image/webp")
		if !ok {
			t.Fatalf("webp transcode failed")
		}
		praw, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatal(err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(praw))
		if err != nil || format != "png" || cfg.Width != want.Width || cfg.Height != want.Height {
			t.Fatalf("webp output = %+v,%s,%v", cfg, format, err)
		}
	})
	for _, tc := range []struct{ name, data, mime string }{
		{"bad-base64", "!!!", "image/jpeg"},
		{"not-an-image", base64.StdEncoding.EncodeToString([]byte("hello")), "image/jpeg"},
		{"unsupported", pngData(t, 1, 1), "image/bmp"},
		{"empty", "", "image/jpeg"},
		{"mime-mismatch", jpegTestData(t, 2, 2), "image/webp"},
	} {
		t.Run("reject-"+tc.name, func(t *testing.T) {
			if _, ok := ToPNG(tc.data, tc.mime); ok {
				t.Fatalf("must reject")
			}
		})
	}
}

// A small file declaring an enormous raster must be rejected on its
// HEADER dimensions, before any decoder allocates it. ToPNG passes png
// through without decoding, so the cap that guards the png path lives in
// dimensions() — which gates sizing, and therefore gates upload too.
func TestPixelBombRejectedOnDeclaredDimensions(t *testing.T) {
	bomb := fakePNGBomb(t, 40000, 40000)
	if len(bomb) > MaxBytes {
		t.Fatalf("bomb fixture must stay under the wire cap, got %d bytes", len(bomb))
	}
	if c, r := Size(bomb, "image/png", 60); c != 0 || r != 0 {
		t.Fatalf("pixel bomb must not size, got %dx%d", c, r)
	}
	if got := Render(bomb, "image/png", Kitty, 60); got.Rows != 0 || got.Lines[0] != "□" {
		t.Fatalf("pixel bomb must render the fallback: %+v", got)
	}
	// A bomb relabelled as a transcodable mime is rejected too: the sniffed
	// format must match the promised mime.
	if got, ok := ToPNG(bomb, "image/jpeg"); ok {
		t.Fatalf("png bytes labelled jpeg must not transcode: %d bytes", len(got))
	}
}

// fakePNGBomb builds a valid PNG header/stream claiming w x h while
// carrying (almost) no pixel data, so the test stays cheap while the
// declared dimensions are absurd.
func fakePNGBomb(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	// Rewrite the IHDR width/height fields (bytes 16..24) in place.
	raw := buf.Bytes()
	binary.BigEndian.PutUint32(raw[16:20], uint32(w))
	binary.BigEndian.PutUint32(raw[20:24], uint32(h))
	return base64.StdEncoding.EncodeToString(raw)
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
