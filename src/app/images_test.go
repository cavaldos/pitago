package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/chat"
	terminal_image "pitago/src/components/terminal_image"
)

func jpegPayload(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 200, G: 50, B: 50, A: 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func kittyTestModel(img chat.Image) Model {
	return Model{
		blocks:          []Block{{Kind: "user", Text: "look", Images: []chat.Image{img}}},
		ShowImages:      true,
		ImageWidthCells: 20,
		ImageProtocol:   terminal_image.Kitty,
		imgRender:       newImageRenderState(),
	}
}

// The first render uploads the payload and places; later renders only
// re-place (payload-free), so repaints stay cheap.
func TestImageLinesUploadOncePlaceAlways(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := kittyTestModel(img)
	m.vp = viewport.New(50, 20)

	first := m.imageLines([]chat.Image{img}, 50)
	if len(first) != 10 {
		t.Fatalf("1x1 png at width 20 reserves 10 rows, got %d", len(first))
	}
	if n := strings.Count(first[0], "a=t,f=100"); n != 1 {
		t.Fatalf("first render must upload once: %q", first[0][:120])
	}
	if !strings.Contains(first[0], "a=p,q=2,U=1") || !strings.Contains(first[0], "c=20,r=10") {
		t.Fatalf("first row must place with geometry: %q", first[0][:160])
	}
	// Rows carry SGR color around the cells: reset at the end, placeholders
	// just before it; continuations are pure placeholder rows.
	if !strings.HasSuffix(first[0], "\x1b[39m") || !strings.Contains(first[0], terminal_image.PlaceholderChar) {
		t.Fatalf("first row must be colored placeholders: %q", first[0][len(first[0])-80:])
	}
	id := m.imgState().up[img.Digest]
	for i, row := range first[1:] {
		wantRow, _ := terminal_image.PlaceholderRow(id, i+1, 20)
		if row != wantRow {
			t.Fatalf("continuation row %d must be its placeholder row", i+1)
		}
	}

	second := m.imageLines([]chat.Image{img}, 50)
	if strings.Contains(strings.Join(second, "\n"), "a=t,f=100") {
		t.Fatalf("second render must not re-upload")
	}
	if !strings.Contains(second[0], "a=p,q=2,U=1") {
		t.Fatalf("second render must still place: %q", second[0][:120])
	}
	if len(m.imgState().up) != 1 {
		t.Fatalf("upload cache = %v", m.imgState().up)
	}
}

// A corrupt (not merely non-png) payload keeps the □ fallback and emits
// nothing. Valid non-png payloads transcode instead (see
// TestImageLinesTranscodesAndCaches).
func TestImageLinesCorruptJpegFallsBack(t *testing.T) {
	m := kittyTestModel(chat.NewImage("aaaa", "image/jpeg"))
	got := m.imageLines(m.blocks[0].Images, 50)
	if len(got) != 1 || got[0] != "□" {
		t.Fatalf("corrupt jpeg = %q", got)
	}
}

// A png declaring an enormous raster never reaches the terminal: it fails
// geometry, so it falls back to □ without uploading a single byte.
func TestPixelBombNeverUploads(t *testing.T) {
	bomb := chat.NewImage(oversizedPNGPayload(t), "image/png")
	m := kittyTestModel(bomb)
	got := m.imageLines([]chat.Image{bomb}, 50)
	if len(got) != 1 || got[0] != "□" {
		t.Fatalf("pixel bomb must fall back, got %q", got)
	}
	if strings.Contains(got[0], "a=t") {
		t.Fatal("pixel bomb must not be uploaded")
	}
}

// oversizedPNGPayload is a valid 1x1 png whose IHDR claims 40000x40000.
func oversizedPNGPayload(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	binary.BigEndian.PutUint32(raw[16:20], 40000)
	binary.BigEndian.PutUint32(raw[20:24], 40000)
	return base64.StdEncoding.EncodeToString(raw)
}

// Ids must stay in 1..255: the placeholder foreground color carries only
// 8 bits, so an out-of-range id would render as someone else's image.
func TestImageIDStaysInEightBitRange(t *testing.T) {
	st := newImageRenderState()
	for i := 0; i < 255; i++ {
		id := st.allocImageID()
		if id < 1 || id > 255 {
			t.Fatalf("id %d out of range at %d", id, i)
		}
		st.up[uint64(i)] = id
	}
	if id := st.allocImageID(); id != 0 {
		t.Fatalf("exhausted pool must return 0, got %d", id)
	}
}

// Past 255 distinct images the pool stays exhausted: existing placeholders
// keep pointing at their own digest (no re-minted id can show the wrong
// picture) and no payload is re-uploaded every frame.
func TestImageIDExhaustionFallsBackWithoutDisturbingExisting(t *testing.T) {
	first := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := kittyTestModel(first)
	before := m.imageLines(m.blocks[0].Images, 50)
	if !strings.Contains(before[0], "a=t,f=100") {
		t.Fatal("first image must upload")
	}
	// Burn the pool.
	st := m.imgState()
	for i := 0; i < 254; i++ {
		st.up[uint64(1<<32+i)] = st.allocImageID()
	}
	after := m.imageLines(m.blocks[0].Images, 50)
	if strings.Join(after, "\n") != strings.Join(before[0:], "\n") && strings.Contains(strings.Join(after, "\n"), "a=t,f=100") {
		t.Fatal("existing image re-uploaded after exhaustion")
	}
	if len(after) != 10 {
		t.Fatalf("existing image must still render 10 rows, got %d", len(after))
	}

	extra := chat.NewImage(jpegPayload(t, 2, 2), "image/jpeg")
	got := m.imageLines([]chat.Image{extra}, 50)
	if len(got) != 1 || got[0] != "□" {
		t.Fatalf("image past the id pool must fall back, got %q", got)
	}
	if strings.Contains(got[0], "a=t") {
		t.Fatal("fallback must not upload")
	}
}

// Hidden images, wrong protocol, or a zero-value model all fall back.
func TestImageLinesFallbackConditions(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	for _, tc := range []struct {
		name  string
		model Model
	}{
		{"hidden", Model{ShowImages: false, ImageWidthCells: 20, ImageProtocol: terminal_image.Kitty}},
		{"iterm2", Model{ShowImages: true, ImageWidthCells: 20, ImageProtocol: terminal_image.ITerm2}},
		{"none", Model{ShowImages: true, ImageWidthCells: 20}},
		{"zero", Model{}},
	} {
		got := tc.model.imageLines([]chat.Image{img}, 50)
		if len(got) != 1 || got[0] != "□" {
			t.Fatalf("%s = %q", tc.name, got)
		}
		if strings.Contains(strings.Join(got, "\n"), "\x1b_G") {
			t.Fatalf("%s emitted escapes", tc.name)
		}
	}
}

// Placeholder rows survive the viewport: content intact, padded to width,
// nothing appended inside the image columns.
func TestImageRowsSurviveViewport(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := kittyTestModel(img)
	m.vp = viewport.New(50, 20)
	rows := m.imageLines([]chat.Image{img}, 50)
	m.vp.SetContent("● look\n" + strings.Join(rows, "\n"))
	for i, l := range strings.Split(m.vp.View(), "\n") {
		if lipgloss.Width(l) != 50 {
			t.Fatalf("row %d width %d, want 50: %q", i, lipgloss.Width(l), l)
		}
	}
	if n := strings.Count(strings.Join(rows, "\n"), terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("want 200 placeholder cells, got %d", n)
	}
	if !strings.Contains(m.vp.View(), "a=p,q=2,U=1") {
		t.Fatalf("place sequence lost in viewport")
	}
}

// Selection copy drops placeholders (with diacritics) and kitty APC
// sequences alike.
func TestSelectionStripsPlaceholdersAndAPC(t *testing.T) {
	row, ok := terminal_image.PlaceholderRow(7, 3, 4)
	if !ok {
		t.Fatal("row build failed")
	}
	line := "  " + row + "   " + terminal_image.PlaceUnicode(7, 20, 10)
	got := stripSelectionANSI(line)
	if strings.Contains(got, terminal_image.PlaceholderChar) || strings.Contains(got, "\x1b_") || strings.Contains(got, "\x1b[") || strings.Contains(got, "a=p") {
		t.Fatalf("selection leak: %q", got)
	}
	// Combining diacritics must go too — not just the base placeholder.
	for _, r := range got {
		if r >= 0x300 && r <= 0x36F {
			t.Fatalf("diacritic leak U+%04X in %q", r, got)
		}
	}
}

// Non-png inputs transcode once and render as placeholders; broken inputs
// fall back and cache the failure instead of retrying every frame.
func TestImageLinesTranscodesAndCaches(t *testing.T) {
	jpeg := jpegPayload(t, 4, 2)
	img := chat.NewImage(jpeg, "image/jpeg")
	m := kittyTestModel(img)
	// 4x2 landscape at width 20 → 20 cols x 5 rows.
	got := m.imageLines([]chat.Image{img}, 50)
	if len(got) != 5 {
		t.Fatalf("want 5 rows, got %d", len(got))
	}
	if n := strings.Count(strings.Join(got, "\n"), terminal_image.PlaceholderChar); n != 20*5 {
		t.Fatalf("want 100 placeholder cells, got %d", n)
	}
	if len(m.imgState().png) != 1 {
		t.Fatalf("transcode must cache, cache = %d entries", len(m.imgState().png))
	}
	// First render uploads; steady-state renders only re-place.
	if !strings.Contains(got[0], "a=t,f=100") {
		t.Fatalf("first render must upload transcode")
	}
	again := m.imageLines([]chat.Image{img}, 50)
	if strings.Contains(strings.Join(again, "\n"), "a=t,f=100") {
		t.Fatalf("second render must not re-upload")
	}
	third := m.imageLines([]chat.Image{img}, 50)
	if strings.Join(third, "\n") != strings.Join(again, "\n") {
		t.Fatalf("steady-state renders must be identical")
	}

	broken := chat.NewImage("aaaa", "image/jpeg")
	m2 := kittyTestModel(broken)
	for i := 0; i < 2; i++ {
		if got := m2.imageLines([]chat.Image{broken}, 50); len(got) != 1 || got[0] != "□" {
			t.Fatalf("broken jpeg pass %d = %q", i, got)
		}
	}
	if !m2.imgState().pngBad[broken.Digest] {
		t.Fatal("broken transcode must cache negative")
	}
}

// Images returned inside a tool result ride the tool block as placeholders.
func TestToolResultImagesCarried(t *testing.T) {
	m := Model{curAsst: -1, curThink: -1, ShowImages: true, ImageWidthCells: 20,
		ImageProtocol: terminal_image.Kitty}
	m.tools = make(map[string]int)
	m.ensureTool("call-img", "read")
	m.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"call-img","toolName":"read","content":[{"type":"text","text":"shot"},{"type":"image","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+XwWjJwAAAABJRU5ErkJggg==","mimeType":"image/png"}]}}`))
	i := m.tools["call-img"]
	if len(m.blocks[i].Images) != 1 {
		t.Fatalf("tool block images = %+v", m.blocks[i])
	}
	m.vp = viewport.New(50, 20)
	got := m.renderBlocks()
	if n := strings.Count(got, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("tool image must render 200 placeholder cells, got %d", n)
	}
}

// An image block reserves exactly its geometry rows in the transcript so
// viewport height and mouse hit-testing stay honest.
func TestImageBlockRowAccounting(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := Model{ShowImages: true, ImageWidthCells: 20, ImageProtocol: terminal_image.Kitty}
	got, skip := m.renderOneBlock(Block{Kind: "user", Text: "look", Images: []chat.Image{img}}, 48)
	if skip {
		t.Fatal("user image block was skipped")
	}
	// Box frame (3 rows) + blank + 10 image rows (place + 9 continuations).
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 14 {
		t.Fatalf("want 14 rows, got %d: %q", len(lines), got)
	}
	if n := strings.Count(got, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("want 200 placeholder cells, got %d", n)
	}
}

// Image-bearing blocks bypass the render cache: the first render embeds a
// one-time upload that must never replay from cache.
func TestImageBlocksBypassRenderCache(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := Model{blocks: []Block{{Kind: "user", Text: "look", Images: []chat.Image{img}}},
		ShowImages: true, ImageWidthCells: 20, ImageProtocol: terminal_image.Kitty}
	m.vp = viewport.New(50, 20)
	first := m.renderBlocks()
	if n := strings.Count(first, "a=t,f=100"); n != 1 {
		t.Fatalf("first render must upload once, got %d", n)
	}
	second := m.renderBlocks()
	if strings.Contains(second, "a=t,f=100") {
		t.Fatalf("second render must not re-upload")
	}
	if third := m.renderBlocks(); third != second {
		t.Fatalf("steady-state renders must be identical")
	}
}
