// Package pitago owns pitago-only features (origin "pitago").
//
// MVC position: Model helpers (pure domain, no Bubble Tea state).
// What belongs here: mouse capture, trajectory trace, self-update, yank,
// recent models, theme switching, sidebar toggles, pitago-setting hub —
// additions on top of pi with no pi parity intended. NOT pi-pure (see
// src/pirpc + src/builtin pi-origin), NOT pi-extension (see src/ext:
// plan-mode, tasks/todos, subagents from third-party npm extensions).
//
// Dependency rule: pitago imports pirpc/components/stdlib only, never
// app/builtin/ext. app/builtin import pitago (one-way); controllers call
// into these pure helpers (ScopeOf, ParseMouseArg, ResolveMouse).
package pitago
