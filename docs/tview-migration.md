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
| P1 | keys parity (`src/tui/keys`), kitty images, OSC52, mouse | in progress (`tview/spike`) |
| P3a | `cellwidth`, `tstyle`, `cells` ANSI→cells | in progress (`tview/render`) |
| P3b | `PITAGO_UI=tview` switch in `main.go` | after keys + cells |
| P3c | lipgloss → own style layer in `src/app` | todo |
| P4 | native ChatView, TextArea input, sidebar, header, toasts | todo |
| P5 | dialogs (pickers → config → dashboards → tree/focus/pet) | todo |
| P6 | mouse selection/hover/drag on cells; drop mouseleak/scroll_filter | todo |
| P7 | tests on SimulationScreen goldens | ongoing |
| P8 | remove teabridge, bubbles, lipgloss; tview default | todo |

## Known issues / watch list

- The `ev` import is still aliased `tea` in `src/app` (renaming collides with
  ~80 locals named `ev`); rename during P4.
- `PaintTranslate` (interim painter) drops OSC 8 links and kitty APC; replaced
  by the `cells` painter.
- `app.ImageOut` raw writes are not wired under tview yet (needs the kitty
  spike result).
- `tview.Application.SetScreen` re-`Init`s the screen: size a
  SimulationScreen *after* `tviewrt.New`.
