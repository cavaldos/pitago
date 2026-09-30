// Package clipboard centralizes text copy for pitago: local system
// clipboard via atotto (+ platform helper fallbacks), and OSC 52 for
// hosted sessions (terminal multiplexers / remote panes) where the local
// clipboard belongs to the client machine, not the host.
package clipboard

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
)

// Channel reports which writer delivered the text.
type Channel string

const (
	None  Channel = "none"
	Atoto Channel = "atotto"
	Osc52 Channel = "osc52"
)

// Status describes one copy attempt. Bytes is the byte length of the
// requested text; Chars is the user-facing character count (toasts).
type Status struct {
	Channel Channel
	Bytes   int
	Chars   int
	Err     error
}

// MaxOsc52 is the cap for OSC 52 payloads. Oversized values fail
// explicitly rather than silently truncating.
const MaxOsc52 = 100_000

// IsHostedSession reports whether this process runs inside a host-managed
// session (terminal multiplexer / remote pane). ORCA_WORKTREE_ID and
// ORCA_PANE_KEY are the host's session markers; extend here as other hosts
// appear.
func IsHostedSession() bool {
	return os.Getenv("ORCA_WORKTREE_ID") != "" || os.Getenv("ORCA_PANE_KEY") != ""
}

// Transport is the clipboard write implementation.
//
// It is a variable so tests can capture what would be copied instead of
// performing the copy: a drag-release test that reaches the real Write would
// overwrite the clipboard of whoever is running `go test`, and an OSC 52
// test would emit a live set sequence to their terminal.
var Transport = writeClipboard

// Write copies text to the clipboard with the best available channel:
//
//  1. Hosted session       → OSC 52 only (the local system clipboard
//     belongs to the machine displaying the terminal).
//  2. Otherwise             → local clipboard (atotto), with platform CLI
//     fallbacks (pbcopy / wl-copy / xclip / clip) if atotto fails.
//
// OSC 52 is fire-and-forget: a non-nil Err is reported only when the
// terminal write itself fails, never as proof the clipboard changed.
func Write(text string) Status { return Transport(text) }

// clipboardWriteAll is the atotto entry point. It is a variable so a test can
// assert what would be handed to the system clipboard without writing to it:
// `go test` must not replace the clipboard of whoever is running it.
var clipboardWriteAll = clipboard.WriteAll

// writeClipboard is the real transport behind Write.
func writeClipboard(text string) Status {
	if IsHostedSession() {
		return writeOsc52(text)
	}
	if err := clipboardWriteAll(text); err == nil {
		return Status{Channel: Atoto, Bytes: len(text), Chars: len([]rune(text))}
	}
	if s := writeExternal(text); s.Channel != None {
		return s
	}
	return Status{
		Channel: None,
		Bytes:   len(text),
		Chars:   len([]rune(text)),
		Err:     fmt.Errorf("no clipboard backend available"),
	}
}

// Osc52String builds the OSC 52 escape sequence for text (pure, testable).
func Osc52String(text string) string {
	return osc52.New(text).String()
}

// osc52Out is where the OSC 52 sequence goes. Bubble Tea owns stdout and
// paints there, so the sequence is written to stderr; it is a variable so
// tests can capture the sequence rather than emit a live one that would
// replace the clipboard of the terminal running `go test`.
var osc52Out io.Writer = os.Stderr

// writeOsc52 emits the OSC 52 sequence to stderr (the terminal-facing
// stream in a Bubble Tea app). Returns Channel=Osc52 on success.
func writeOsc52(text string) Status {
	status := Status{Channel: None, Bytes: len(text), Chars: len([]rune(text))}
	if len(text) > MaxOsc52 {
		status.Err = fmt.Errorf("OSC 52 copy exceeds %d bytes; select a smaller range", MaxOsc52)
		return status
	}
	seq := osc52.New(text).String()
	if seq == "" {
		status.Err = fmt.Errorf("terminal produced an empty OSC 52 sequence")
		return status
	}
	if _, err := fmt.Fprint(osc52Out, seq); err != nil {
		status.Err = err
		return status
	}
	status.Channel = Osc52
	return status
}

// writeExternal runs a platform clipboard CLI as a fallback when atotto
// fails. Mirrors the paste-path fallback style (src/app/paste.go).
func writeExternal(text string) Status {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		for _, bin := range []string{"wl-copy", "xclip", "xsel"} {
			if path, err := exec.LookPath(bin); err == nil {
				args := []string(nil)
				if bin == "xclip" {
					args = []string{"-i", "-selection", "clipboard"}
				} else if bin == "xsel" {
					args = []string{"-i", "--clipboard"}
				}
				cmd = exec.Command(path, args...)
				break
			}
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		cmd = nil
	}
	if cmd == nil {
		return Status{Channel: None}
	}
	cmd.Stdin = bytes.NewReader([]byte(text))
	if err := cmd.Run(); err != nil {
		return Status{Channel: None, Err: err}
	}
	return Status{Channel: Atoto, Bytes: len(text), Chars: len([]rune(text))}
}
