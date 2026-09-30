# Architecture — Layout (MVC + core/ext/pitago)

Reference for how the pitago source tree is organized and which layers may import
which. This document is the single source of truth for the layout rules; the
enforcement script is `script/check-layers.sh`.

Throughout this document, **pure pi** means the upstream `pi` agent core — the
model, session tree, thinking levels, and the stock pi commands — as opposed to
the pitago-specific code we add on top of it.

## Directory tree

```
src/main.go       # composition root: flags, spawn pi, wire packages, run
src/app/          # MVC shell (Bubble Tea Elm): model.go=M (state+msgs),
                  # update.go=C (event router), view.go=V (render only),
                  # thin wrappers over components/ext/pitago (no pure logic)
src/components/  # V primitives (pure, testable): chat, mention, image,
                  # palette, pet, recent, yank, format, theme, markdown,
                  # clipboard (local + OSC 52)
src/builtin/      # C: command controllers over RPC.
                  # Origin "pi" = pure pi (model/tree/thinking/settings/
                  # login/session/resume/reload); "pitago" = ours (delegate
                  # to pitago/ext, never pure logic here)
src/extension/    # middleware/protocol: extension_ui_request helpers
                  # (select/confirm/input/editor), command sources
src/ext/          # pi-extension domain (NOT pure pi, NOT pitago):
                  # plan-mode (latch/heuristic), tasks/todos (parse/restore),
                  # subagents (discovery/frontmatter) — pure, no UI state
src/pitago/       # pitago-only domain (NOT pure pi): mouse, trajectory,
                  # self-update, yank, recent, theme, sidebar — pure helpers
src/pirpc/        # Backend: JSONL transport for `pi --mode rpc` (core/pure pi)
src/update/       # Backend: self-update (pitago-only)
tests/            # integration tests (black-box, public API only).
                  # Unit white-box tests stay next to code as *_test.go
                  # (Go requires this for private access) — see tests/README.md
```

## Rules

Enforced by `script/check-layers.sh`:

- `app`/`builtin` → `{ext,pitago,components,pirpc,extension}` one-way.
- `ext`/`pitago`/`components`/`pirpc` never import `app`/`builtin`.
- `ext` ⇄ `pitago` never cross-import (pi-extension vs pitago-only stay
  separate).
- `app` never imports `builtin` (wired in main via `UseBuiltins`).

## Why the split

- **MVC shell (`app`)** keeps the Bubble Tea program thin: it owns state
  (`model.go`), routes events (`update.go`), and renders (`view.go`) only. It
  delegates every real behaviour to `components`, `ext`, or `pitago`, so no
  domain logic leaks into the render path.
- **`components`** holds pure, directly testable view primitives.
- **`ext` vs `pitago`** keeps two different kinds of domain code apart:
  `ext` implements pi-extension behaviour (plan mode, todos, subagents) that
  must remain valid independently of the pitago feature set, while `pitago`
  holds pitago-only helpers. They must not depend on each other.
- **`builtin`** is only a command surface: it translates a command into RPC
  calls and delegates to `ext`/`pitago`.
- **`pirpc`/`update`** are the backend edges (JSONL transport to `pi`, and
  self-update), with no UI concerns.

## Adding a new directory or package

1. Decide which role it plays: view primitive (`components`), extension domain
   (`ext`), pitago-only domain (`pitago`), command surface (`builtin`), or
   backend (`pirpc`/`update`). Do not add a new import edge that contradicts the
   rules above.
2. Keep logic pure and free of UI state wherever the layer allows it.
3. Run `script/check-layers.sh` before opening a change — it is the same check
   CI runs.
4. Add white-box unit tests next to the code as `*_test.go` (Go requires this for
   private access); black-box integration tests belong in `tests/` and use only
   the public API.
