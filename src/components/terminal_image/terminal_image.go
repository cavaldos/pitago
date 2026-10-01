// Package terminal_image renders supported images with Kitty or iTerm2
// protocols. Unsupported or invalid images use one square placeholder line.
package terminal_image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"strings"

	"golang.org/x/image/webp"
)

type Protocol string

const (
	Kitty  Protocol = "kitty"
	ITerm2 Protocol = "iterm2"
)

// MaxBytes bounds protocol and render-cache memory use.
const MaxBytes = 8 << 20

func DetectProtocol() Protocol {
	// Pi applies an explicit image override after conservative auto-detection.
	override := os.Getenv("PI_IMAGE_PROTOCOL")
	if override == "" { // pitago-local compatibility alias
		override = os.Getenv("PITAGO_IMAGE_PROTOCOL")
	}
	switch strings.ToLower(override) {
	case "kitty":
		return Kitty
	case "iterm2":
		return ITerm2
	case "none", "0":
		return ""
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
		return ""
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return ""
	}
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if os.Getenv("KITTY_WINDOW_ID") != "" || program == "kitty" {
		return Kitty
	}
	if program == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return Kitty
	}
	if os.Getenv("WEZTERM_PANE") != "" || program == "wezterm" ||
		program == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" ||
		os.Getenv("WARP_TERMINAL_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return Kitty
	}
	if os.Getenv("ITERM_SESSION_ID") != "" || program == "iterm.app" {
		return ITerm2
	}
	return ""
}

// RenderResult is one terminal-image component represented as logical lines.
// Fallback always has exactly one line and reserves no image rows.
type RenderResult struct {
	Lines   []string
	Columns int
	Rows    int
}

// Render returns image lines with the same accounting semantics as Pi:
// Kitty puts the sequence on the first line and uses empty logical lines for
// the remaining height; iTerm2 reserves empty lines, moves back, then emits
// the image on the final line. Fallback never emits cursor movement.
func Render(data, mime string, protocol Protocol, maxWidth int) RenderResult {
	dims, ok := dimensions(data, mime)
	if !ok || dims.width <= 0 || dims.height <= 0 || maxWidth < 1 || maxWidth > 200 {
		return fallbackResult(Fallback())
	}
	columns, rows := calculateImageCellSize(dims, maxWidth)
	switch protocol {
	case Kitty:
		lines := make([]string, rows)
		lines[0] = encodeKitty(data, columns, rows)
		return RenderResult{Lines: lines, Columns: columns, Rows: rows}
	case ITerm2:
		lines := make([]string, rows)
		if rows > 1 {
			lines[rows-1] = fmt.Sprintf("\x1b[%dA", rows-1) + encodeITerm2(data, columns)
		} else {
			lines[0] = encodeITerm2(data, columns)
		}
		return RenderResult{Lines: lines, Columns: columns, Rows: rows}
	default:
		return fallbackResult(Fallback())
	}
}

func fallbackResult(line string) RenderResult {
	return RenderResult{Lines: []string{line}}
}

// calculateImageCellSize mirrors Pi's 9x18 default cell dimensions and
// calculateImageCellSize: scale down to both width and height caps, then ceil.
func calculateImageCellSize(dims size, maxWidthCells int) (int, int) {
	maxWidthCells = max(1, min(200, int(math.Floor(float64(maxWidthCells)))))
	maxHeightCells := max(1, int(math.Ceil(float64(maxWidthCells*9)/18)))
	widthScale := float64(maxWidthCells*9) / float64(dims.width)
	heightScale := float64(maxHeightCells*18) / float64(dims.height)
	scale := min(widthScale, heightScale)
	columns := max(1, min(maxWidthCells, int(math.Ceil(float64(dims.width)*scale/9))))
	rows := max(1, min(maxHeightCells, int(math.Ceil(float64(dims.height)*scale/18))))
	return columns, rows
}

// Fallback is deliberately metadata-free, so hostile MIME/name data cannot
// inject terminal control sequences. One image always maps to one square.
func Fallback() string { return "□" }

func encodeKitty(data string, columns, rows int) string {
	params := fmt.Sprintf("a=T,f=100,q=2,C=1,c=%d,r=%d", columns, rows)
	return chunked(params, data)
}

// chunked emits one (possibly multi-chunk) Kitty graphics control sequence
// with the given params, chunking at 4096 bytes with m=1 between chunks
// and m=0 on the final one — the same framing pi uses.
func chunked(params, data string) string {
	const chunk = 4096
	if len(data) <= chunk {
		return "\x1b_G" + params + ";" + data + "\x1b\\"
	}
	var b strings.Builder
	for off := 0; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		more := 0
		if end < len(data) {
			more = 1
		}
		if off == 0 {
			b.WriteString("\x1b_G" + params + fmt.Sprintf(",m=%d;", more) + data[off:end] + "\x1b\\")
		} else {
			b.WriteString(fmt.Sprintf("\x1b_Gm=%d;", more) + data[off:end] + "\x1b\\")
		}
	}
	return b.String()
}

// maxTranscodePixels bounds transcode memory: 16M pixels ≈ 64MB RGBA
// transient. Anything larger keeps the □ fallback.
const maxTranscodePixels = 16 << 20

// ToPNG transcodes jpeg/gif/webp payloads to PNG base64 for kitty's f=100
// wire format; PNG passes through untouched. GIF yields its first frame
// only. ok=false means "keep the □ fallback" (unsupported mime, corrupt
// data, absurd dimensions, or an output that would exceed MaxBytes).
func ToPNG(data, mime string) (string, bool) {
	if strings.EqualFold(mime, "image/png") {
		return data, true
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 || len(raw) > MaxBytes {
		return "", false
	}
	// Reject on the DECLARED header dimensions before decoding: a small
	// crafted file claiming 40000x40000 would otherwise allocate the full
	// raster (~6GB) and kill the process before any post-decode check.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return "", false
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxTranscodePixels {
		return "", false
	}
	// The sniffed format must match the promised mime: a jpeg labelled
	// image/webp would otherwise be decoded by the wrong decoder.
	wantFormat := map[string]string{
		"image/jpeg": "jpeg",
		"image/gif":  "gif",
		"image/webp": "webp",
	}[strings.ToLower(mime)]
	if wantFormat == "" || format != wantFormat {
		return "", false
	}
	var img image.Image
	switch {
	case strings.EqualFold(mime, "image/jpeg"):
		img, err = jpeg.Decode(bytes.NewReader(raw))
	case strings.EqualFold(mime, "image/gif"):
		img, err = gif.Decode(bytes.NewReader(raw))
	case strings.EqualFold(mime, "image/webp"):
		img, err = webp.Decode(bytes.NewReader(raw))
	default:
		return "", false
	}
	if err != nil || img == nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", false
	}
	if buf.Len() == 0 || buf.Len() > MaxBytes {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// Size returns the display cell size for data/mime capped at maxWidthCells,
// or (0,0) when the image is undecodable or the width is out of range.
// The geometry matches Render's fallback contract exactly.
func Size(data, mime string, maxWidthCells int) (int, int) {
	dims, ok := dimensions(data, mime)
	if !ok || dims.width <= 0 || dims.height <= 0 || maxWidthCells < 1 || maxWidthCells > 200 {
		return 0, 0
	}
	return calculateImageCellSize(dims, maxWidthCells)
}

// Upload transmits data to the terminal without displaying it, tagged with
// id, so later Place calls can show it without resending the payload.
// The payload is png (f=100); callers keep non-png on the □ path.
func Upload(data string, id uint64) string {
	params := fmt.Sprintf("a=t,f=100,q=2,i=%d", id)
	return chunked(params, data)
}

// PlaceholderChar is kitty's designated unicode placeholder cell (U+10EEEE).
// Rows of these anchor a U=1 placement to the text grid, so the image
// scrolls with content, dies with its cells, and can never smear or sit
// under text — no placement lifecycle tracking needed.
const PlaceholderChar = "\U0010EEEE"

// PlaceUnicode creates the virtual placement a U=1 placeholder grid
// refers to. The placement id rides along as p=id, so re-emitting the
// same placement every repaint replaces it in place instead of leaking
// duplicates (kitty spec: same image id + placement id replaces). The
// cursor must not move (C=1): the placeholder text printed after the
// sequence advances it across the row.
func PlaceUnicode(id uint64, columns, rows int) string {
	return fmt.Sprintf("\x1b_Ga=p,q=2,U=1,i=%d,p=%d,c=%d,r=%d,C=1\x1b\\", id, id, columns, rows)
}

// Diacritic returns the combining mark for a row/column number, in kitty
// gen/rowcolumn-diacritics.txt order (0 → U+0305, 1 → U+030D, 2 → U+030E).
func Diacritic(n int) (rune, bool) {
	if n < 0 || n >= len(diacriticTable) {
		return 0, false
	}
	return diacriticTable[n], true
}

// PlaceholderRow returns one row of a U=1 placeholder grid for image id:
// the foreground color encodes the id (8-bit form, so ids must stay under
// 256) and every cell carries explicit row+column diacritics — no reliance
// on the terminal's inheritance heuristics. Combining marks are zero-width,
// so the row measures exactly cols cells. ok=false when the address exceeds
// the diacritic table (caller falls back to □).
func PlaceholderRow(id uint64, row, cols int) (string, bool) {
	rd, ok := Diacritic(row)
	if !ok || cols < 1 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[38;5;%dm", id&0xff)
	for c := 0; c < cols; c++ {
		cd, ok := Diacritic(c)
		if !ok {
			return "", false
		}
		b.WriteString(PlaceholderChar)
		b.WriteRune(rd)
		b.WriteRune(cd)
	}
	b.WriteString("\x1b[39m")
	return b.String(), true
}

func encodeITerm2(data string, columns int) string {
	return fmt.Sprintf("\x1b]1337;File=inline=1;size=%d;width=%d;height=auto;preserveAspectRatio=1:%s\a",
		base64.StdEncoding.DecodedLen(len(data)), columns, data)
}

type size struct{ width, height int }

func dimensions(data, mime string) (size, bool) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 || len(raw) > MaxBytes {
		return size{}, false
	}
	if strings.EqualFold(mime, "image/webp") {
		s, ok := webpSize(raw)
		if !ok || !withinPixelCap(s) {
			return size{}, false
		}
		return s, true
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !withinPixelCap(size{cfg.Width, cfg.Height}) {
		return size{}, false
	}
	return size{cfg.Width, cfg.Height}, true
}

// withinPixelCap rejects absurd declared dimensions on every path, so a
// crafted file can never reach a decoder (or the terminal) with a raster
// large enough to OOM.
func withinPixelCap(s size) bool {
	return s.width >= 1 && s.height >= 1 && int64(s.width)*int64(s.height) <= maxTranscodePixels
}

func webpSize(b []byte) (size, bool) {
	if len(b) < 20 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return size{}, false
	}
	three := func(i int) (int, bool) {
		if i < 0 || i+3 > len(b) {
			return 0, false
		}
		return int(b[i]) | int(b[i+1])<<8 | int(b[i+2])<<16, true
	}
	valid := func(s size) bool { return s.width > 0 && s.height > 0 }
	switch string(b[12:16]) {
	case "VP8 ":
		if len(b) < 30 {
			return size{}, false
		}
		width, okW := three(26)
		height, okH := three(28)
		s := size{width & 0x3fff, height & 0x3fff}
		return s, okW && okH && valid(s)
	case "VP8L":
		if len(b) < 25 {
			return size{}, false
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		s := size{int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1}
		return s, valid(s)
	case "VP8X":
		if len(b) < 30 {
			return size{}, false
		}
		width, okW := three(24)
		height, okH := three(27)
		s := size{width + 1, height + 1}
		return s, okW && okH && valid(s)
	default:
		return size{}, false
	}
}

// diacriticTable maps a row/column number to its combining mark,
// in kitty gen/rowcolumn-diacritics.txt order (value 0 = U+0305).
var diacriticTable = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611,
	0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
	0x0659, 0x065A, 0x065B, 0x065D, 0x065E, 0x06D6, 0x06D7, 0x06D8,
	0x06D9, 0x06DA, 0x06DB, 0x06DC, 0x06DF, 0x06E0, 0x06E1, 0x06E2,
	0x06E4, 0x06E7, 0x06E8, 0x06EB, 0x06EC, 0x0730, 0x0732, 0x0733,
	0x0735, 0x0736, 0x073A, 0x073D, 0x073F, 0x0740, 0x0741, 0x0743,
	0x0745, 0x0747, 0x0749, 0x074A, 0x07EB, 0x07EC, 0x07ED, 0x07EE,
	0x07EF, 0x07F0, 0x07F1, 0x07F3, 0x0816, 0x0817, 0x0818, 0x0819,
	0x081B, 0x081C, 0x081D, 0x081E, 0x081F, 0x0820, 0x0821, 0x0822,
	0x0823, 0x0825, 0x0826, 0x0827, 0x0829, 0x082A, 0x082B, 0x082C,
	0x082D, 0x0951, 0x0953, 0x0954, 0x0F82, 0x0F83, 0x0F86, 0x0F87,
	0x135D, 0x135E, 0x135F, 0x17DD, 0x193A, 0x1A17, 0x1A75, 0x1A76,
	0x1A77, 0x1A78, 0x1A79, 0x1A7A, 0x1A7B, 0x1A7C, 0x1B6B, 0x1B6D,
	0x1B6E, 0x1B6F, 0x1B70, 0x1B71, 0x1B72, 0x1B73, 0x1CD0, 0x1CD1,
	0x1CD2, 0x1CDA, 0x1CDB, 0x1CE0, 0x1DC0, 0x1DC1, 0x1DC3, 0x1DC4,
	0x1DC5, 0x1DC6, 0x1DC7, 0x1DC8, 0x1DC9, 0x1DCB, 0x1DCC, 0x1DD1,
	0x1DD2, 0x1DD3, 0x1DD4, 0x1DD5, 0x1DD6, 0x1DD7, 0x1DD8, 0x1DD9,
	0x1DDA, 0x1DDB, 0x1DDC, 0x1DDD, 0x1DDE, 0x1DDF, 0x1DE0, 0x1DE1,
	0x1DE2, 0x1DE3, 0x1DE4, 0x1DE5, 0x1DE6, 0x1DFE, 0x20D0, 0x20D1,
	0x20D4, 0x20D5, 0x20D6, 0x20D7, 0x20DB, 0x20DC, 0x20E1, 0x20E7,
	0x20E9, 0x20F0, 0x2CEF, 0x2CF0, 0x2CF1, 0x2DE0, 0x2DE1, 0x2DE2,
	0x2DE3, 0x2DE4, 0x2DE5, 0x2DE6, 0x2DE7, 0x2DE8, 0x2DE9, 0x2DEA,
	0x2DEB, 0x2DEC, 0x2DED, 0x2DEE, 0x2DEF, 0x2DF0, 0x2DF1, 0x2DF2,
	0x2DF3, 0x2DF4, 0x2DF5, 0x2DF6, 0x2DF7, 0x2DF8, 0x2DF9, 0x2DFA,
	0x2DFB, 0x2DFC, 0x2DFD, 0x2DFE, 0x2DFF, 0xA66F, 0xA67C, 0xA67D,
	0xA6F0, 0xA6F1, 0xA8E0, 0xA8E1, 0xA8E2, 0xA8E3, 0xA8E4, 0xA8E5,
	0xA8E6, 0xA8E7, 0xA8E8, 0xA8E9, 0xA8EA, 0xA8EB, 0xA8EC, 0xA8ED,
	0xA8EE, 0xA8EF, 0xA8F0, 0xA8F1, 0xAAB0, 0xAAB2, 0xAAB3, 0xAAB7,
	0xAAB8, 0xAABE, 0xAABF, 0xAAC1, 0xFE20, 0xFE21, 0xFE22, 0xFE23,
	0xFE24, 0xFE25, 0xFE26, 0x10A0F, 0x10A38, 0x1D185, 0x1D186, 0x1D187,
	0x1D188, 0x1D189, 0x1D1AA, 0x1D1AB, 0x1D1AC, 0x1D1AD, 0x1D242, 0x1D243,
	0x1D244,
}
