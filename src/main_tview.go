package main

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/app"
	"pitago/src/components/clipboard"
	"pitago/src/pirpc"
	"pitago/src/tui/keys"
	"pitago/src/tui/tviewrt"
)

// runTview runs the interactive UI on tview and tcell.
func runTview(m app.Model, pi *pirpc.Client) error {
	ensureUTF8Locale()
	// Cache the terminal color probe before tcell starts reading the tty.
	// Otherwise both renderers can race for the reply to the same probe.
	lipgloss.HasDarkBackground()
	var tracker keys.MouseTracker
	// Before New: the runtime asks the model for its menu bar at build time.
	app.NativeMenus = true
	rt := tviewrt.New(m, tviewrt.Options{
		Mouse:  m.Mouse,
		Key:    keys.FromKey,
		MouseE: tracker.FromMouse,
		Filter: app.ScrollEventFilter,
	})
	app.Post = rt.Send
	app.ImageOut = rt.Raw()
	// OSC 52 copies ride the same raw channel: one tty write, on the UI
	// goroutine, before Show — never interleaved with tcell's own output.
	defer clipboard.SetOsc52Writer(rt.Raw())()
	app.WireClient(pi)
	_, err := rt.Run()
	return err
}

// ensureUTF8Locale makes tcell encode UTF-8. tcell picks its output charset
// from LC_ALL/LC_CTYPE/LANG; without a UTF-8 locale it drops combining marks
// and prints '?' for non-ASCII (kitty placeholders, box drawing, CJK).
func ensureUTF8Locale() {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.Contains(strings.ToUpper(v), "UTF-8") || strings.Contains(strings.ToUpper(v), "UTF8") {
				return
			}
			break // the first one set wins, and it is not UTF-8
		}
	}
	_ = os.Setenv("LC_CTYPE", "en_US.UTF-8")
	_ = os.Unsetenv("LC_ALL")
}
