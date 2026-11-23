# Bubble Tea → tview migration

`tview`/`tcell` is the only interactive runtime. There is no `PITAGO_UI` switch, and the Go module no longer depends on Bubble Tea or Bubbles (including in tests). The runtime translates terminal input into `src/tui/ev` messages and executes the app reducer's commands. The local textarea and viewport retain their upstream notices.

## What runs natively

- `src/tui/tviewrt` manages the terminal, input, overlays, menu bar, and native list pickers (including model selection). The menu bar and dropdowns follow the active Pitago theme and background.
- `/settings` and `/pitago-setting` use native tview lists, a filter field, and Apply/Close buttons. The app still owns settings actions, previews, and persistence.
- `/model`, `/login`, `/settings` and `/pitago-setting` keep the painted pickers' shape on native widgets: one frame titled inside the border, borderless columns with an inline header row, a `filter:` line and a single help line instead of Apply/Close buttons. Row clicks, wheel scrolling and the keyboard all drive the same reducer; `/model` moves its details column under the panes on narrow terminals, and the marketplace keeps its remote `search:` filter and DETAILS column.
- The main app still renders an ANSI view with Lip Gloss; `src/tui/cells` paints it onto the tcell screen. The native `src/tui/chatview` exists but is not wired into the main frame. The transcript, sidebar, input and complex dialogs such as MCP and the session tree still use the app's painted views. A fully native widget/layout migration is separate from the runtime cutover.
- Bracketed paste preserves LF/CRLF and tabs. tcell still normalizes some embedded control/escape sequences (see the paste tests). OSC 52 and Kitty graphics use the runtime's raw terminal channel. iTerm2 inline image protocol is not supported by the tview drawing path.

## Mouse interaction

Inspired by [TTT](https://github.com/eugenioenko/ttt):

- Open top-bar menus, right-click for actions at the pointer, and choose a row in a native picker. Clicking outside closes a native popup.
- In Settings, click a section or a row to select it, scroll over a list to move its selection, and use Apply or double-click a value to activate it. Tab and the arrow keys still navigate the lists.
- Click the prompt to place the cursor. Click the session header for session info and the prompt footer to pick a model.
- Drag the divider to resize the sidebar; double-click to reset. Wheel-scroll chat or sidebar, drag to select chat text, and click the jump-to-latest chip when reading history.
- Mouse capture is on by default. `--mouse=false` leaves terminal text selection to the terminal; `Alt+M` toggles capture. `F10` opens the menu without a mouse.

Input drag-selection, an input-height divider and conversion of the remaining painted regions to native primitives are not implemented.

## Validation

`go test ./...` runs widget and runtime tests with a tcell simulation screen, SGR mouse/paste parser regressions and integration tests. To smoke-test in your terminal, run `go run ./src`, then click a menu or within a multiline draft, right-click a chat block, and drag the sidebar divider, including while scrolling. Repeat with mouse capture toggled off and back on.
