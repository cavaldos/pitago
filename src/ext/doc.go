// Package ext owns pi-extension domain: third-party extensions that are
// NOT pure pi and NOT pitago-only.
//
//   - plan-mode (npm:@narumitw/pi-plan-mode): plan latch heuristic,
//     inline menu detection ("plan" text match)
//   - tasks/todos (npm:pi-tasks, manage_todo_list): todo tool detection,
//     list parsing, history restore
//   - subagents (npm:pi-subagents): agent discovery + frontmatter parse
//
// MVC position: Model helpers (pure domain, no Bubble Tea state).
// Dependency rule: ext imports pirpc/stdlib only, never app/builtin/pitago.
// app/builtin import ext (one-way) and keep thin wrappers for compat.
package ext
