# pitago — design notes (Go TUI frontend for `pi --mode rpc`)

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
- `pitago -ne` / `--no-extensions` forwards pi's own `-ne` to the child, so no discovered,
  configured or built-in pi extension loads. pitago's `src/builtin` re-implementations are not
  pi extensions and stay on — the switch only quiets the extension layer, exactly like in pi.
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
- `compaction_start` / `compaction_end` → pi's compaction status INDICATOR, not a chat line:
  `showStatusIndicator` embeds `CompactionStatusIndicator` in the input editor's TOP BORDER
  (setEditorWorkingStatusIndicator, interactive-mode.js:2888) — the `─ ⠿ Compacting context...
  (escape to cancel) ─────` bar. pitago splices the same thing into the input box: cGreen border,
  `spinFrame(m.pet.tick)` + one of pi's three labels verbatim (`compactionLabel`,
  status-indicator.js:44-51), taking precedence over the `m.thinking` branch because a mid-turn
  auto-compaction has both true and never over `inputStatus()`, whose pet-timed label would hide
  the wording. The chat status row is suppressed while compacting (pi shows the indicator in the
  border only), and `m.compacting` keeps `petLooping()` true so a manual `/compact` issued while
  idle still animates. The `(escape to cancel)` hint is HONEST: pi replaces the editor's Esc handler with
  `session.abortCompaction()` for the duration (interactive-mode.js:2883-2887), and pitago does
  the same with ONE press — `m.compacting` is the first Esc branch, above bash, auto-retry and the
  double-press turn cancel. There is no compaction-only RPC, so `abortCompactionCmd` sends pi's
  generic `abort` (rpc-mode.js:327-329 → session.abort(), agent-session.js:1841-1852) without
  `ClearQueue`. That leaves one difference, stated plainly: during an AUTOMATIC compaction pi
  cancels only the compaction, while this abort also stops the agent run (session.abort() calls
  agent.abort() too); a manual `/compact` behaves identically. `compactAborted` suppresses the
  `compact` command's rethrown error (agent-session.js:2223) so one cancellation is one report.
  pi also queues input during a compaction; pitago cannot, so no such promise is rendered. The latch and its
  reason are reconciled against `get_state.isCompacting` so a dropped event cannot freeze it. pi's own manual path
  (`chatContainer.clear(); renderSessionEntries(entries.slice(1)); addMessageToChat(...)`,
  interactive-mode.js:2913-2918) puts the summary block at the BOTTOM of the transcript even though
  `get_messages` hoists the compaction pseudo-message to the front, so `restore()` collects
  `compactionSummary`/`branchSummary` rows and appends them after the other roles — rendering them in
  place leaves the block far above the fold in a bottom-pinned chat. Mid-turn auto-compaction appends
  the block in place instead of rebuilding, because a rebuild would drop the in-flight tool blocks.
  A followed session (file tail) additionally projects the branch through pi's `buildContextEntries`
  (`firstKeptEntryId`), so a resumed compacted session shows the same surviving entries pi does.
- Enter: prompt (idle) / steer (streaming); Esc: dialog? close : clear_queue+abort+restore queued text into the input; Ctrl+N: new_session; Ctrl+C: quit + kill pi
- Copy: Ctrl+Y is context-sensitive. In chat it yanks the last assistant answer; in the
  `trajectory`, `notification` and `tree` dialogs it copies the selected row's full text and
  leaves the dialog open. Those three are the only copyable kinds — every unmodified rune is
  consumed by their type-to-filter path, so the action cannot be a bare letter, and `yank`
  already copies on Enter. Confirmation shows in the dialog footer, not as a toast: `View`
  returns the dialog before `overlayToasts` runs, so a notice raised inside a modal would
  surface as a stale popup after it closed. `notification` rows keep the full text in `Payload`
  because `Options` is truncated to 180 cells for display.
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
- `/thinking` level picker, `/tree` session-tree view + pi's per-row action menu (`Jump to message` / `View entry` / `Copy entry` / `Fork from here` / `Back to tree`)
- `/mcp` MCP server manager + pi's per-server action menu (`Sign in` / `Tools` / `Reconnect` / `Sign out` / `Exposure` / `Disable`, or `Enable` for a disabled server), plus the `/mcp login|logout|reconnect <server>` subcommands
- `/settings` overlay: model, thinking, steering/follow-up modes, auto-compact, auto-retry
- `/login` / `/logout`: API-key keystore (0600) + auto-respawn pi; OAuth guided via stock pi
- `/reload` + 45s background poll + post-turn refresh → auto-detect new pi commands

## Out of scope (YAGNI)

- Clipboard-paste / drag-drop images, setWidget custom rendering, manual compaction, multi-session tabs.
  Real session-tree navigation is out of reach too: pi's `navigateTree` (the `Summarize branch?`
  prompt in its own TUI) is not in the RPC command surface, so `/tree` actions are local
  (scroll the transcript, copy, fork) instead of moving pi's leaf.
- `@image.png` vision IS in scope (components/image → RPC `images`, pi CLI parity).
  Dropped/pasted/Tab-completed image paths collapse into an input-tray
  (`[Image N]` chips, src/app/attach.go) so long escaped paths never clog
  the prompt; Backspace on empty input pops the last chip.
- Deleted the old internal/{llm,agent,tools} (replaced by pi).

## Verify (no LLM spend)

- `go vet + build`; test script calls get_state / get_available_models / get_commands via the client
- 1 ultra-short live prompt on a free model, 120s timeout
