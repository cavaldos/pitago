# Subagent surface hosts

The `/subagent-herd` overlay manages subagents. Most of it is host-agnostic:
rows, status, action gating, steering, waiting, resuming, dedupe and the
sidebar all work in any terminal. Two actions — **focus a subagent's pane**
(`f`) and **close that pane** (`X`) — can only cross into a pane, so they
need a *surface host*: a CLI that can list and address a pane.

## The contract

A host is one row in a table (`src/app/dialog_subagents.go`):

```go
type surfaceCtl struct {
    name      string                                    // used in prompts and notices
    available func() error                              // is this host usable here?
    claim     func(handle string) bool                  // does this handle address my surface?
    precheck  func(action, handle string) error         // can this handle drive this verb? (nil = no precheck)
    run       func(action, handle string) error         // do it: "switch" | "close"
    hints     surfaceHints                              // this host's own verb grammar
}
```

Prompt text is built from `hints`, never from `name` plus hardcoded verbs —
a host that binds no equivalent (cmux has no interrupt) leaves the field
empty and the herd falls back to the universal path.

Rules that keep the seam honest:

- `available()` and `run()` are separate. A probe must never be able to
  execute a real command; `activeSurfaceCtl()` only ever calls `available()`.
- Handles are **opaque** and their namespace belongs to the host. Orca
  reports `term_<uuid>`; cmux reports a workspace-qualified ref such as
  `window:1/workspace:2/pane:3` (see the cmux section — a bare ref is claimed
  but not actionable).
  `surfaceCtlForHandle` asks each host whether it claims the handle
  instead of baking a prefix into the parser, so two hosts can coexist and a
  third needs no parser change. The parser only enforces a conservative
  charset — the same string is rendered in the overlay and interpolated into
  prompts, so spaces, flags, and newlines are rejected up front.
- Handlers are `"switch"` and `"close"`. Anything else is refused, so a new
  call site cannot smuggle an arbitrary host command through the herd.
- `run` receives a handle the UI validated already. Do not interpolate
  handles into a shell string.
- Never bulk-close a workspace. Never kill processes. One matched pane, one
  action.
- The first available host that claims the handle wins. An unavailable host
  is skipped, not a veto: a namespace claimed by two bindings resolves to
  whichever is actually installed. Availability is decided by the impl, not
  by the caller, so tests can swap a fake in without touching `PATH`.

## Degradation

When no host is available, the overlay must still be useful:

| action | no host installed |
|---|---|
| `f` | reports the pane handle and that no installed host can focus it |
| `X` | dismisses the row locally; says there is no pane to close |
| `x` | prompt is host-free: SIGINT on the tool PID stops a pane in any terminal |

Notices name the host that actually failed (`orca terminal close failed …`),
never a hardcoded product, so a second host reports itself correctly.

## Hosts

### Orca (`src/app/dialog_subagents.go`)

Handles `term_<uuid>`; verbs `orca terminal switch|close`. Availability is
`exec.LookPath("orca")`.

### cmux (`src/app/surface_cmux.go`)

Handles a **workspace-qualified** ref: `workspace:2/pane:3`,
`window:1/workspace:2/pane:3`, or `workspace:2/<uuid>` — a workspace, an
optional window, and exactly ONE target segment. The **only** bound verb is
`cmux focus-pane [--window W] --workspace K --pane P`. Availability is
`cmux ping`. cmux binds no interrupt verb, so `x` falls through to the
host-agnostic SIGINT path, and `X` dismisses the row while leaving the pane
to cmux (see *Why close is not bound*).

**Every cmux target is workspace-scoped, and none of the obvious handle forms
is enough on its own.** Verified live against cmux 0.64.25:

| call | result |
|---|---|
| `focus-pane --pane pane:1` | `OK pane:1 workspace:1` — resolves against the **focused** workspace, whatever the handle meant |
| `focus-pane --pane pane:2` (owned by workspace 2) | `Error: not_found: Pane not found` |
| `focus-pane --pane <pane uuid from workspace 2>` | `Error: not_found: Pane not found` — a uuid is **not** globally unique for addressing |
| `focus-pane --pane workspace:2/pane:2` | `Error: Invalid pane handle: workspace:2/pane:2 (expected UUID, ref like pane:1, or index)` |
| `focus-pane --workspace window:1/workspace:2 --pane pane:2` | `Error: Invalid workspace handle` — a window segment cannot ride inside `--workspace` |
| `focus-pane --pane "tab:2/surface:23"` | `Error: Invalid pane handle` — no verb accepts a multi-segment target |
| `focus-pane --window window:1 --workspace workspace:2 --pane pane:2` | `OK pane:2 workspace:2` |

#### Why close is not bound

Focus is trustworthy; close is not. Against cmux 0.64.25, from inside cmux:

| call | result |
|---|---|
| `close-surface --workspace K --surface surface:50` | `OK surface:51 workspace:K` — reports a **different** surface than the one asked for |
| `close-surface --workspace K --surface 1` (index form) | `OK …` with the surface count unchanged |
| `close-surface --workspace K --surface 2` (index form) | `Error: Surface index not found` |
| `close-surface --surface surface:53` (no `--workspace`) | `Error: Surface ref not found: surface:53` |
| `close-pane …` | `Error: Unknown command 'close-pane'` |

So no form of the verb reliably closes the surface you name, and the failure
mode is the dangerous one: **it reports success, for a different surface**.

A note on how this was checked, because it nearly went the other way:
`cmux identify --workspace K --surface surface:50` exits 0 even for a surface
that does not exist, so "it still resolves" is not evidence of anything. The
live test asserts presence in `cmux list-pane-surfaces` instead, and also
asserts that a fabricated id is absent from that same listing — otherwise the
presence check would be decorative too. `close-workspace` does
work, but it is far too broad to ever bind to a single subagent. A verb that
can say "OK" while closing something else is worse than no verb, so the herd
refuses, and `X` says `pane left open: …` while still dismissing the row.
`TestCmuxLiveHandleIsWorkspaceScoped` asserts both halves of that: the
refusal, and that the refusal left the surface untouched.

So the binding splits a qualified handle into `--window`/`--workspace` +
target, refuses a window segment that is not addressable on its own, and
**refuses an unscoped or multi-segment handle** instead of passing it
through. Two reasons, and the second is the one that matters:

- passing a bare ref through fails with a raw `not_found`, which breaks the
  "never a raw CLI error" rule; and
- a bare ref that *does* resolve will act on whatever the focused workspace
  happens to call `pane:1` — a wrong-target focus, and the same hazard for
  `X` on a `surface:1` ref. Refusing is the only safe direction: a wrong
  close is not recoverable.

A host integration must therefore report a workspace-qualified handle whose
target is a single segment. cmux's own type check is a useful backstop, not a
substitute: passing a pane ref to `--surface` is refused by cmux itself
(`Surface ref not found: pane:2`), but the binding checks the target kind
itself so the no-raw-CLI-error rule stays in our hands.

**`X` still dismisses when the pane cannot be closed.** Dismissal is
bookkeeping and always succeeds; closing the pane needs a host that can be
trusted to address it. So `surfaceCtl.precheck` runs before the close and
the herd says `pane left open: <reason>` while still removing the row —
otherwise a host that cannot close a pane would leave the row stuck in the
overlay with no key that can remove it. That was a real review finding: an
earlier version refused to dismiss at all, which turned `X` into a no-op on
every cmux row.

`TestCmuxLiveHandleIsWorkspaceScoped` (`src/app/surface_cmux_live_test.go`)
exercises this against a real socket: create a scratch workspace, create a
surface, refuse a bare ref with no subprocess, refuse the wrong target kind
and a multi-segment tail, **focus for real and check the effect**
(`list-panes` shows `[focused]`), assert `close` is refused *and* that the
refusal left the surface alive, then clean up. It skips unless
`CMUX_LIVE_TEST=1` and a cmux answers, so it never runs in CI or on a machine
without cmux. Unit tests cannot cover this: they fake the CLI, and every wrong
handle form above is a form the fake happily accepted.

cmux restricts its socket to processes started inside cmux, so a pitago
running under another multiplexer gets
`Access denied - only processes started inside cmux can connect`. That is
reported as "host unavailable" and the overlay degrades — it is never a
herd failure. Set `CMUX_SOCKET_PASSWORD` in pitago's environment to allow
cross-tree control; the binding passes it through to every call. A pitago
launched *from* a cmux pane needs no password — it is already inside the
tree.

## Adding a host

1. Add a `surfaceCtl` entry in `surfaceControllers()`.
2. `claim` = how a handle in your namespace looks (uuid shape, ref shape —
   a regex is fine). This is what keeps two hosts from fighting over handles.
3. `available` = a cheap presence check (`exec.LookPath`, a `ping`, a socket
   stat). Never fail the herd when it fails.
4. `run` = one `exec.CommandContext` with a fixed argv, bounded timeout, and
   output truncated before it reaches a notice. Refuse unknown actions.
5. Optionally provide `currentHandle`, and only if your host can name the
   surface the process is running in. It is what lets a row with no handle of
   its own — an in-process child shares the parent's terminal — still be
   focused, instead of reporting "no pane". **Focus only, never close:** it
   is the user's own window, and a row action must not be able to shut it.
   cmux composes it from `cmux identify` and validates the result through the
   same split as every other handle. It also checks the environment for
   markers cmux injects into everything it spawns, and refuses without asking
   if they are absent: the socket password grants ACCESS, not identity. One
   limit is inherent to ancestry-as-identity and worth knowing — a host app
   launched *from* a cmux pane inherits those markers, so `identify` reports
   the cmux pane and `f` will focus across to it. That is the same answer
   cmux's own socket ACL gives.
6. Add cases to `TestSurfaceControllerTablePicksFirstAvailable` /
   `TestSurfaceCtlForHandleRoutesByOwner` for selection and routing, a
   no-host case for your wording, and — if your host binds different verbs —
   a prompt case proving your row never renders another host's grammar.
7. Then drive it against the real host once, from inside it. Fake-CLI tests
   cannot tell you whether your handle FORM is one the host accepts; every
   wrong form tried for cmux looked correct in a fake and failed in the
   socket.
No herd code needs to change: prompts, gating and notices are all derived
from `ctl.name` and `surfaceCtlForHandle`.

## Verified so far

- **Orca** — exercised end to end: focus, close, interrupt, steer, status
  sync, dedupe.
- **cmux, control path** — exercised end to end from a pitago running inside
  cmux: real `focus-pane` on a workspace-qualified handle with the effect
  verified, a bare handle and a multi-segment tail refused with no
  subprocess, and `close` refused as unbinding-worthy with the surface
  confirmed still alive afterwards
  (`TestCmuxLiveHandleIsWorkspaceScoped`, cmux 0.64.25). The cross-tree
  failure was also exercised for real: from a process outside cmux the
  binding fails closed with
  `cmux unreachable: exit status 1 (Error: ERROR: Access denied - only
  processes started inside cmux can connect)`.
- **cmux, per-child panes** — out of scope, deliberately. pi's subagent
  spawner is host-agnostic in name only: an interactive subagent launched from
  a pitago hosted by cmux still reported `(surface: term_6af8c01c-…)`, an
  **Orca** handle, so a per-child cmux pane needs a change in that spawner.
  It is not needed for the cmux path to be useful: a host's `currentHandle`
  covers in-process children, which is most rows, and it is verified live.
  Only a child that genuinely gets its own surface needs the spawner change.
