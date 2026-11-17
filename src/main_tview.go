package main

import (
	"fmt"
	"os"
	"strings"

	"pitago/src/app"
	"pitago/src/pirpc"
	"pitago/src/tui/keys"
	"pitago/src/tui/tviewrt"
)

// runTview runs the app on the tview runtime (PITAGO_UI=tview). Stage 1 of
// the migration: same model and Cmds as the Bubble Tea path, frame painted
// through src/components/cells; see docs/tview-migration.md.
func runTview(m app.Model, pi *pirpc.Client, mouse bool) {
	ensureUTF8Locale()
	var tracker keys.MouseTracker
	// Before New: the runtime asks the model for its menu bar at build time.
	app.NativeMenus = true
	rt := tviewrt.New(m, tviewrt.Options{
		Mouse:  mouse,
		Key:    keys.FromKey,
		MouseE: tracker.FromMouse,
		Filter: app.ScrollEventFilter,
	})
	app.Post = rt.Send
	app.ImageOut = rt.Raw()
	app.WireClient(pi)
	if _, err := rt.Run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

// ensureUTF8Locale makes tcell encode UTF-8. tcell picks its output charset
// from LC_ALL/LC_CTYPE/LANG; without a UTF-8 locale it drops combining marks
// and prints '?' for non-ASCII (kitty placeholders, box drawing, CJK).
// Bubble Tea wrote raw bytes and never cared, so many shells never set one.
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
