# Bubble Tea → tview migration

Branch: `feat/tview-migration` (from `main`). Peer branches merge into it:
`tview/render` (render layer), `tview/spike` (keys, kitty, OSC52, mouse).

## Strategy

1. **Own the event vocabulary.** `src/tui/ev` mirrors the Bubble Tea v1 subset
   the app used (same key numbers and strings), so `src/app` is a runtime-free
   reducer: `Update(ev.Msg) (ev.Model, ev.Cmd)` + `View() string`.
2. **Two runtimes, one model.** `src/tui/teabridge` (Bubble Tea, current
   default) and `src/tui/tviewrt` (tview). Both execute the same Cmds.
3. **Whole-frame bridge first.** `tviewrt` paints the existing `View()` string
   through an ANSI→cell painter, so the full app runs on tview early.
4. **Then go native, region by region.** Replace chat, input, sidebar and
   dialogs with tview primitives / custom `Box.Draw`, keeping tests green.
5. **Delete Bubble Tea** (teabridge, bubbles, lipgloss) at the end.

## Status

| Phase | What | State |
|---|---|---|
| P0 | drop `src/stack`, charm v2 pins | done |
| P2a | `src/tui/ev` + `teabridge`; app/builtin/tests off bubbletea | done |
| P2b | `tviewrt` runtime (SimulationScreen tests) | done |
| P1a | keys + mouse parity (`src/tui/keys`) | done |
| P1b | kitty images on real terminal, OSC52, paste with LF | in progress (`tview/spike`) |
| P3a | `cellwidth`, `tstyle`, `cells` ANSI→cells, raw tty side channel | done |
| P3b | `PITAGO_UI=tview` switch (`src/main_tview.go`) | done — whole app runs on tview |
| P3c | lipgloss → own style layer in `src/app` | todo |
| P4 | native ChatView (`src/tui/chatview`, `tview/render`), TextArea input, sidebar, header, toasts | in progress |
| P5 | dialogs (pickers → config → dashboards → tree/focus/pet) | todo |
| P6 | mouse selection/hover/drag on cells; drop mouseleak/scroll_filter | todo |
| M1 | sidebar divider drag (prefs.sideWidth) | done |
| M2 | widget kit `src/tui/widgets`; Overlay is the tviewrt root | done |
| M3 | right-click context menus at the pointer: chat block, input (`ev.OpenMenuMsg` → `MenuResultMsg`) | done (tview); Bubble Tea keeps the dialog |
| M4 | clickable header/status segments, tooltips, `⋯` buttons, sidebar row menus | todo |
| P7 | tests on SimulationScreen goldens | ongoing |
| P8 | remove teabridge, bubbles, lipgloss; tview default | todo |

## Mouse-first UX (target: ttt-like, github.com/eugenioenko/ttt)

Already in pitago: wheel scroll, drag-select in chat, hover highlight,
clickable sidebar rows, right-click → block actions dialog.

| Interaction | Where in pitago | Built from |
|---|---|---|
| Drag divider to resize | chat ↔ sidebar width, chat ↔ input height (persist in prefs) | `widgets.Splitter` |
| Right-click context menu at pointer | chat block (copy/code/table/retry…), sidebar rows (session, recent model), input (paste/clear) | `widgets.ContextMenu` (replaces the full-screen block-actions dialog) |
| Click status/header segments | model → model picker, thinking level, ctx %, LSP, MCP, plugins | `widgets.ClickBar` |
| `⋯` menu buttons | sidebar sections, panels, dialogs | `widgets.MenuButton` |
| Hover popups | truncated text, tool call details, diagnostics, toasts | `widgets.Tooltip` |
| Click to place cursor / drag-select in input | input editor | app textarea + `PointAt` |
| Click list items, click outside to close | every dialog/picker | native tview lists (P5) |

Popups live in an overlay layer above the frame (`widgets.Overlay`, root of
`tviewrt`), so they work during the whole-frame bridge stage, before every
region is native. Geometry that the app owns (sidebar width, input height)
stays app state, changed by `ev` messages from the widgets.

## Known issues / watch list

- The `ev` import is still aliased `tea` in `src/app` (renaming collides with
  ~80 locals named `ev`); rename during P4.
- Paste under tview: tview keeps only runes/Enter/Tab during a bracketed paste,
  so LF-only newlines (KeyCtrlJ) and other C0 bytes are lost.
- Mouse: a wheel during a held drag yields a spurious release+press pair.
- Keys: physical-key parity, not byte parity (tcell enables CSI-u /
  modifyOtherKeys). Gaps are asserted in `src/tui/keys` tests.
- Kitty ids < 16 are emitted by tcell as ESC[3x/9x; truecolor ids need
  COLORTERM=truecolor. iTerm2 inline images are not supported under tview.
- `tview.Application.SetScreen` re-`Init`s the screen: size a
  SimulationScreen *after* `tviewrt.New`.
