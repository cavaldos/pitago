# pi-wrap — Go TUI wrapping `pi --mode rpc`

## Idea

Pi (badlogic) has an ugly UI but a strong agent (multi-provider, tools, session, compaction).
`pi --mode rpc` speaks JSONL over stdin/stdout → our polished Go TUI is the frontend,
pi is the backend. No direct LLM calls anymore.

## Architecture

```
src/main.go (entry, wiring) ──uses──▶ src/app (Bubble Tea TUI shell)
                                          │ ▲
                    src/builtin (pi builtins over RPC) │ │ registry+confirmers
                    src/extension (extension protocol) ┘ │
src/pirpc/client.go  <-JSONL->  pi --mode rpc
```

- `src/app` never imports `builtin`/`extension` (wired in main via
  `UseBuiltins`); `builtin` operates on `*app.Model`, `extension` is pure.
- Origin rule: `src/builtin` = pi TUI builtins (`OriginPi`) + pitago's own
  (`OriginPitago`, e.g. `/recent`); everything from `get_commands`
  (extension/prompt/skill) is extension-side and runs via Prompt forwarding.

## src/pirpc (stdlib only: os/exec + encoding/json + bufio)

- Spawn `pi --mode rpc [-c] [--provider X] [--model Y]`, stderr → /tmp/pitago-pi-stderr.log
- Reader: ReadString('\n'), strip \r (per protocol; no Scanner — its 64k buffer is too small, Reader is safe)
- `type:response` + id → pending chan; everything else → OnEvent (calls prog.Send, thread-safe)
- Command struct has explicit fields + omitempty, no map[string]any

## TUI: event-driven rendering (opencode-like monochrome; chat + session sidebar)

- `message_update` text_delta → appended to the assistant block (true streaming)
- thinking_delta → gray block; toolcall_start/end + tool_execution_* → tool block (running → done + trimmed result)
- Tool blocks are chrome-first, not fill-first, and never use `Background()` — a terminal without
  truecolor used to drop the fill and leave flat raw rows, which is what this layout replaces.
  - bash/powershell: one rounded box, flush left with no status bullet, holding the highlighted
    command, then a `─── Output ──` content rule, then the output (the box appears on toolcall_start
    with just the command, so a long command never pops in with its output). Border color carries
    pending/done/error, which is what the bullet gives up: muted → border → red.
  - every other tool: status bullet + bold name + dim args, then a `├──`/`└──` result tree
    (glob/read/grep). Diffs and errors stay flat rows — a diff has its own +/−/line-number gutter
    and an error is one logical unit, not a list of siblings.
- message_end → finalizes the block (falls back to message text when no deltas arrived)
- `extension_ui_request` select/confirm → centered modal dialog (↑↓, Enter, Esc);
  input/editor → free-text dialog (Enter submits, Esc cancels); notify/setStatus/set_editor_text → shown/applied accordingly
- `agent_settled` → refresh get_session_stats (tokens, cost, context %) into the sidebar
- Enter: prompt (idle) / steer (streaming); Esc: dialog? close : clear_queue+abort+restore queued text into the input; Ctrl+N: new_session; Ctrl+C: quit + kill pi
- Copy: Ctrl+Y is context-sensitive. In chat it yanks the last assistant answer; in the
  `trajectory` and `notification` dialogs it copies the selected row's full text and leaves
  the dialog open. Those two are the only copyable kinds — every unmodified rune is consumed
  by their type-to-filter path, so the action cannot be a bare letter, and `yank` already
  copies on Enter. Confirmation shows in the dialog footer, not as a toast: `View` returns
  the dialog before `overlayToasts` runs, so a notice raised inside a modal would surface as
  a stale popup after it closed. `notification` rows keep the full text in `Payload` because
  `Options` is truncated to 180 cells for display.
- Model: pitago remembers the last model the user *explicitly* picked (`/model`, `/recent`,
  `Ctrl+P`) in `~/.config/pitago/prefs.json` (`currentModel`, single writer, user action only)
  and passes it to the pi child as `--provider/--model` at process start; explicit
  `--provider`/`--model` flags win. pi's settings.json `defaultProvider`/`defaultModel`
  stay the source of truth when nothing was ever picked. The saved value is never read back
  mid-session — the footer always comes from `get_state` — so it cannot disagree with pi.
  `/new` is not second-guessed: it keeps pi's own default until the next process start.
- Thinking stays pi's: pitago keeps no copy. `/model` and `/thinking` are session-scope,
  exactly like pi's picker and `set_model`/`set_thinking_level` over RPC. pitago writes pi's
  settings.json only for keys pi exposes no RPC for, and never mutates pi's auth.json or
  environment at launch.
- Startup: get_state (model) + get_messages (repaint history) + get_session_stats

## Native built-ins (pi built-ins don't run over RPC, so re-implemented)

- `/model` filterable picker (all configured/scoped models) + Ctrl+P quick cycle
- `/thinking` level picker, `/tree` session-tree view
- `/settings` overlay: model, thinking, steering/follow-up modes, auto-compact, auto-retry
- `/login` / `/logout`: API-key keystore (0600) + auto-respawn pi; OAuth guided via stock pi
- `/reload` + 45s background poll + post-turn refresh → auto-detect new pi commands

## Out of scope (YAGNI)

- Clipboard-paste / drag-drop images, setWidget custom rendering, fork/tree UI, manual compaction, multi-session tabs.
- `@image.png` vision IS in scope (components/image → RPC `images`, pi CLI parity).
  Dropped/pasted/Tab-completed image paths collapse into an input-tray
  (`[Image N]` chips, src/app/attach.go) so long escaped paths never clog
  the prompt; Backspace on empty input pops the last chip.
- Deleted the old internal/{llm,agent,tools} (replaced by pi).

## Verify (no LLM spend)

- `go vet + build`; test script calls get_state / get_available_models / get_commands via the client
- 1 ultra-short live prompt on a free model, 120s timeout
