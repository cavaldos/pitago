// Package app is the MVC shell (Bubble Tea Elm architecture).
//
//	M = model.go: Model struct (state) + messages (events as types) +
//	    thin state mutators (AddBlock, Refresh, Toggle*, Open*).
//	V = view.go + sidepanels.go (render* only) + palette.go (popup render) +
//	    trajectory.go (trajectory window render) + styles.go: pure string
//	    builders over Model, no RPC, no mutation.
//	C = update.go (Update event router) + shortcuts/cmdshortcuts/palette
//	    (input routing) + builtin/ (command controllers over RPC).
//	Backend = pirpc (pi --mode rpc transport) + update (self-update).
//	Middleware/protocol = extension (extension_ui_request helpers).
//
// Second axis — ownership (where the feature comes from):
//
//	core/pi-pure: chat, session, model, providers, settings, tree,
//	  login/logout, resume, reload — pi parity, origin "pi" in builtin.All.
//	ext/pi-extension: plan-mode, tasks/todos, subagents — third-party npm
//	  extensions, pure domain lives in src/ext, app keeps type aliases +
//	  thin wrappers (this keeps white-box tests green while ownership is
//	  unambiguous: canonical impl = ext.*).
//	pitago-only: mouse, trajectory, self-update, yank, recent, theme,
//	  sidebar toggles, pitago-setting hub — pure domain lives in
//	  src/pitago (+ src/update), origin "pitago" in builtin.All.
//
// Rules: app never holds pure domain logic (delegate to
// components/ext/pitago); ext/pitago/components never import app/builtin
// (one-way: app -> {ext,pitago,components,pirpc,extension}).
package app
