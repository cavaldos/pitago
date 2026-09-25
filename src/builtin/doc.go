// Package builtin holds command controllers (MVC Controller layer).
//
// Two origins, never mixed in one file without a tag:
//   - OriginPi ("pi"): pi-parity re-implements over RPC — model, tree,
//     thinking, settings, login/logout, session, resume, reload, quit.
//   - OriginPitago ("pitago"): pitago-only — trajectory, recent, yank/copy,
//     sidebar, plugins toggle, mouse, theme, update, shortcuts, subagents
//     picker, pitago-setting hub. Pure domain delegates to src/pitago;
//     pi-extension UI delegates to src/ext.
//
// Rule: builtin operates on *app.Model, never owns render or transport.
package builtin
