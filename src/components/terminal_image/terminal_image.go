// Package terminal_image renders supported images with Kitty or iTerm2
// protocols. Unsupported or invalid images use one square placeholder line.
package terminal_image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"strings"
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
		return webpSize(raw)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return size{}, false
	}
	return size{cfg.Width, cfg.Height}, true
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
