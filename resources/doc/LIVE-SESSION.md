# Live sessions in pitago

Pitago can display a `pi` session that it does not control, live and read-only.
You start the `pi` sessions yourself (a `herdr` pane, another terminal); pitago
only ever *views* one. It never publishes its own session: its own
`pi --mode rpc` child is excluded from the list and is never attachable.

- Press `/live` to list the `pi` sessions running in the current directory and
  attach to the one you pick.
- Press `/live` again, or `Ctrl+D`, to detach and type prompts normally.
- Quitting is `Ctrl+C` twice, or `/quit`.

## The flow

```bash
# Terminal A (a herdr pane, or any terminal): your own Pi session
cd /path/to/your/project
pi

# Terminal B: pitago in the SAME directory
pitago --cwd /path/to/your/project
#   > /live            # lists the pi sessions running here
#   ↑↓ · Enter         # pick one
```

The picked session appears in real time and stays synchronized. While
following, the composer is blurred and the header shows `EXTERNAL · READ-ONLY`:
input and all prompt/steer/abort/model/session controls are blocked, because
following is one-way.

Detaching drops the foreign view, stops whichever transport was attached,
clears the remote turn timing and plugin state, and rebuilds the owned session.

## Two sources, one picker

`/live` lists both kinds of session together, so you pick whichever row you
meant. Both render in the same view and are interchangeable — detach and pick
the other row. A session that has both is listed once, as the bridge.

### bridge — ● streaming (full realtime)

The full stream: token-level deltas, subagent/team notices, busy spinner.
Requires the `pi` that is running to have loaded the live bridge extension.

Pitago installs the bridge into `~/.pi/agent/extensions/` on the first `/live`,
so you no longer pass `--extension` by hand and every `pi` you start from then
on is streamable in full. A `pi` that was **already running** before that
install cannot gain the extension retroactively — extensions load at process
start. That is harmless for following (it is listed as a session-file row
immediately); only the extras wait for a restart.

### session file — ○ session file (no restart, no cooperation)

Pitago tails the JSONL session file that the running `pi` is appending to.
**This works with any running `pi`**: no restart, no extension, nothing required
of that process. Messages and tool results render live.

**What it cannot do, and why.** `pi` appends entries to the session file only
when its `flushed` flag is set — at flush points such as a turn boundary or a
save, not as tokens stream (this is pi's own behaviour, see
`core/session-manager.js`). Two consequences, both structural rather than
tunable:

- Content appears in bursts, not token by token. Polling harder does not help;
  the tail already polls about 10× more often than the data arrives.
- A turn in progress writes nothing, so there is no reliable signal to drive a
  busy spinner. Growth-based hints are possible but would be guesses.

This source is therefore a faithful *viewer* of a session, not a live
activity feed. If you want token streaming and a spinner, the session has to
run the bridge.

## The worker roster

Worker/team state is available on **both** sources, because pitago keeps the
roster on disk: the `pi-agent-team` extension appends a `pi-agent-team/state`
record per worker change, and pitago reads the followed session's file
(`m.sessionFile` is pointed at it while following) to render the roster — who
existed, profile, status, headline, timings and usage.

**Live per-worker detail is not available.** The team widget drawn in the
`pi` window is a TUI *component factory* — a closure that renders into that
process's terminal — not data, so it cannot cross a process boundary. pi itself
drops factories in RPC mode ("factory functions are ignored"). The bridge
therefore reports such a widget as `widgetOpaque` instead of silently dropping
it, and pitago falls back to the on-disk roster. The consequences:

- You see the workers and their state, not the live `Working: <command>` line.
- A widget that is genuinely *cleared* (pi calling `setWidget(key, undefined)`)
  is still treated as cleared — that is why the two cases are flagged apart.

Reading the live activity line would require patching the bridge *inside* the
`pi` process to render the component to text and emit it.

## What follows a foreign session, and what does not

Follow mode is one-way. The bridge exports events; nothing is sent back.

- The bridge taps the shared extension UI object and re-emits `notify` /
  `setStatus` / `setWidget` / `setTitle` as `extension_ui_request` records, and
  forwards `turn_end`, `tool_call` and `tool_result`. pi has no subagent event,
  so this is the only channel for subagent/team traffic.
- Follow mode renders that allowlist and **nothing else**. Every other method
  is reported in the transcript and dropped:
  - dialog methods (`select` / `confirm` / `input` / `editor`) cannot be
    answered from follow mode, because the bridge is one-way — a remote prompt
    would wait forever;
  - a remote `setEditorText` prefill would otherwise overwrite the prompt you
    are typing in your own session.
- No forwarded event ever issues a command to pitago's own `pi` child; there is
  a test for that invariant covering both sources.

## Nothing running is a normal answer

With no `pi` in the directory, `/live` just says so in the transcript — no
dialog, no error, no hang. A session that is neither streaming nor has a
recent session file stays visible in the picker, marked `not streamable`, so a
dead end is visible before you press `Enter`. One obvious candidate is attached
immediately, with no dialog.

A freshly started `pi` that has not produced any conversation yet writes no
session file, so it is legitimately not followable by the file source — there
is nothing to render.

## How the bridge works

The extension binds an HTTP listener only to `127.0.0.1`, uses a random bearer
token, and writes a mode-`0600` descriptor. By default both sides use
`${XDG_RUNTIME_DIR}/pitago-live` (or the OS temporary directory). Set the same
explicit directory on both processes when needed:

```bash
export PITAGO_LIVE_DESCRIPTORS=/private/tmp/my-pitago-live
```

Pitago ignores its own child process, other working directories, and
descriptors whose process is no longer alive. Both descriptor and process
matching resolve symlinks, so a symlinked project path still matches. SSE
reconnects automatically, and a fresh active-branch snapshot on every
connection prevents gaps or duplicate replay.

## See also

- [ARCHITECTURE.md](ARCHITECTURE.md) — source tree and import rules
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution workflow
