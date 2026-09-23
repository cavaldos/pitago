package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/image"
)

// Clipboard paste (Ctrl+V), owned by pitago instead of the textarea.
//
// The textarea's built-in paste calls clipboard.ReadAll once and swallows
// the error (ta.Err, never shown) — on Wayland/SSH/bare servers Ctrl+V
// silently does nothing. Like pi we try platform backends first
// (wl-paste/termux/xclip/xsel/pbpaste), fall back to atotto, and surface
// the failure as a notice pointing at terminal-native paste.

// pasteDoneMsg carries a finished clipboard read: text for the input,
// imgPath for pasted image data (→ tray chip), secret for /login.
type pasteDoneMsg struct {
	text     string
	imgPath  string
	imgTried bool
	secret   bool
	err      error
}

// clipRead is the clipboard reader, overridable in tests.
var clipRead = readClipboard

// clipImage reads image data from the clipboard into a tmp file,
// overridable in tests. "" + errNoImage = no image available.
var clipImage = readClipboardImage

var errNoImage = errors.New("no image in clipboard")

// pasteCmd reads the clipboard off the UI thread. Empty text falls
// through to image data (copied screenshots), like pi's Ctrl+V.
func (m Model) pasteCmd(secret bool) tea.Cmd {
	return func() tea.Msg {
		s, err := clipRead()
		if err != nil {
			return pasteDoneMsg{secret: secret, err: err}
		}
		if strings.TrimSpace(s) == "" && !secret {
			if p, err := clipImage(); err == nil {
				return pasteDoneMsg{imgPath: p, imgTried: true, secret: secret}
			}
			return pasteDoneMsg{imgTried: true, secret: secret}
		}
		return pasteDoneMsg{text: s, secret: secret}
	}
}

// readClipboard tries platform paste commands (pi parity), then atotto.
func readClipboard() (string, error) {
	var cands [][]string
	if os.Getenv("TERMUX_VERSION") != "" {
		cands = append(cands, []string{"termux-clipboard-get"})
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cands = append(cands, []string{"wl-paste", "-n"})
	}
	if os.Getenv("DISPLAY") != "" {
		cands = append(cands,
			[]string{"xclip", "-selection", "clipboard", "-out"},
			[]string{"xsel", "--clipboard", "--output"},
		)
	}
	if runtime.GOOS == "darwin" {
		cands = append(cands, []string{"pbpaste"})
	}
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, c[0], c[1:]...).Output()
		cancel()
		if err == nil {
			return string(out), nil
		}
	}
	return clipboard.ReadAll()
}

// readClipboardImage saves clipboard image data to a tmp file (pi parity
// with its native paste-image; tmp files age out with the OS temp dir).
// Backends: pngpaste (macOS, brew), wl-paste (Wayland), xclip (X11).
func readClipboardImage() (string, error) {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("pngpaste"); err != nil {
			return "", errNoImage
		}
		f, err := os.CreateTemp("", "pitago-clip-*.png")
		if err != nil {
			return "", err
		}
		f.Close()
		if err := runCmd("pngpaste", f.Name()); err != nil {
			os.Remove(f.Name())
			return "", errNoImage
		}
		if st, err := os.Stat(f.Name()); err != nil || st.Size() == 0 {
			os.Remove(f.Name())
			return "", errNoImage
		}
		return f.Name(), nil
	}
	if runtime.GOOS == "windows" {
		return "", errNoImage
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			if types, err := runOut("wl-paste", "--list-types"); err == nil {
				if mime := pickImageMime(types); mime != "" {
					if out, err := runOut("wl-paste", "--type", mime); err == nil {
						return dumpImage(out)
					}
				}
			}
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			if types, err := runOut("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"); err == nil {
				if mime := pickImageMime(types); mime != "" {
					if out, err := runOut("xclip", "-selection", "clipboard", "-t", mime, "-o"); err == nil {
						return dumpImage(out)
					}
				}
			}
		}
	}
	return "", errNoImage
}

func runCmd(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

func runOut(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// pickImageMime prefers png over jpeg/gif/webp in a TARGETS-style listing.
func pickImageMime(types string) string {
	var fallback string
	for _, line := range strings.Split(types, "\n") {
		mime := strings.TrimSpace(strings.ToLower(line))
		switch mime {
		case "image/png":
			return mime
		case "image/jpeg", "image/gif", "image/webp":
			if fallback == "" {
				fallback = mime
			}
		}
	}
	return fallback
}

// dumpImage sniffs bytes (never trust the claimed mime) and stages them.
func dumpImage(out string) (string, error) {
	b := []byte(out)
	var ext string
	switch image.MimeOf(b) {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return "", errNoImage
	}
	f, err := os.CreateTemp("", "pitago-clip-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// hasPngpaste reports the macOS image-clipboard helper (hint only).
func hasPngpaste() bool {
	_, err := exec.LookPath("pngpaste")
	return err == nil
}
// insertAtCursor splices s into the textarea at the cursor (paste must not
// go through ta.Update: the key is already consumed, and only the input
// path owns cursor math — see cursorPos in mention.go).
func (m *Model) insertAtCursor(s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return
	}
	abs := m.absCursor()
	runes := []rune(m.ta.Value())
	if abs > len(runes) {
		abs = len(runes)
	}
	m.setValueAt(string(runes[:abs])+s+string(runes[abs:]), abs+len([]rune(s)))
}

// applyPaste inserts clipboard text / image or explains why paste failed.
func (m *Model) applyPaste(msg pasteDoneMsg) {
	if msg.err != nil {
		m.AddBlock(Block{Kind: "notice", Text: "paste failed: " + msg.err.Error() + " — hãy dán bằng Cmd+V/chuột phải của terminal", Err: true})
		m.Refresh()
		return
	}
	if msg.imgPath != "" {
		if msg.secret {
			m.AddBlock(Block{Kind: "notice", Text: "không dán ảnh vào ô API key được", Err: true})
			m.Refresh()
			return
		}
		m.attachPaths([]string{msg.imgPath})
		return
	}
	if strings.TrimSpace(msg.text) == "" {
		n := "clipboard is empty"
		if msg.imgTried && runtime.GOOS == "darwin" && !hasPngpaste() {
			n += " — cài pngpaste (brew install pngpaste) để dán ảnh chụp màn hình"
		}
		m.AddBlock(Block{Kind: "notice", Text: n})
		m.Refresh()
		return
	}
	if msg.secret {
		for _, d := range m.Dialogs {
			if d.Kind == "secret" || d.Kind == "input" {
				d.Filter += strings.TrimSpace(msg.text)
				m.applyPopupH() // long paste wraps: keep the winH budget
				m.Refresh()
				return
			}
		}
		return
	}
	m.insertAtCursor(msg.text)
	m.histIdx = -1 // pasted edit leaves history browse
	m.collectDrops() // pasted file paths collapse into [Image N] chips
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
}
