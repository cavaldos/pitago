package main

import (
	"fmt"
	"os"

	"pitago/src/app"
	"pitago/src/pirpc"
	"pitago/src/tui/keys"
	"pitago/src/tui/tviewrt"
)

// runTview runs the app on the tview runtime (PITAGO_UI=tview). Stage 1 of
// the migration: same model and Cmds as the Bubble Tea path, frame painted
// through src/components/cells; see docs/tview-migration.md.
func runTview(m app.Model, pi *pirpc.Client, mouse bool) {
	var tracker keys.MouseTracker
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
