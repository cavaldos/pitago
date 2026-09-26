package app

// extui_state.go — shared state and demux rules for extension UI traffic.
//
// Why this file exists: pi runs as an RPC child, so every extension UI
// surface has to be rebuilt host-side. Nine of the installed plugins call
// setStatus on their own statusKey, five call setWidget, and any number of
// them can raise a dialog at once. Routing all of that through one string
// (the old extStat) and one special-cased widget key (agent-team) made
// plugins silently overwrite each other. This file owns the *state* and the
// *rules*; src/app/update.go and src/app/view.go own the call sites and the
// rendering.
//
// Invariants kept here:
//   - A statusKey owns its slot until it clears it with an empty text.
//   - A widgetKey owns its panel until it clears it with no lines.
//   - LRU order is stable and deterministic, so the footer never flickers
//     between two equivalent renderings of the same set.

import (
	"strings"
)

// extWidgetPanel is one plugin-owned widget: the raw lines pi sent, plus
// where it wants to sit. Lines are kept verbatim (escape sequences intact)
// because the widget frame is authoritative Pi output — only width and
// overflow are the host's decision.
type extWidgetPanel struct {
	Key       string
	Lines     []string
	Placement string // aboveEditor (default) or belowEditor
	Seq       uint64 // LRU stamp, higher = more recent
}

// extStatusLimit caps how many plugin statuses the footer will show before
// the rest are folded away. The footer must stay exactly one visual row, so
// this is a hard ceiling, not a suggestion.
const extStatusLimit = 3

// widgetHeightLimit bounds a single widget panel. Panels are transient
// chrome; letting one grow unbounded would eat the chat viewport.
const extWidgetHeightLimit = 6

// ---------------------------------------------------------------- status --

// clearExtUIState drops the whole plugin status/widget registry, the way
// clearTeamWidgetState drops the team widget. Used when the sessions the
// registry describes are gone (leaving follow mode): the tapped setStatus /
// setWidget traffic belonged to a session this window is no longer following,
// and a stale panel or status line would otherwise sit in the owned footer
// and input box until that same key happened to be cleared by its own plugin.
func (m *Model) clearExtUIState() {
	m.extStatus = nil
	m.extStatusSeq = nil
	m.extWidget = nil
	m.extWidgetSeq = nil
	m.syncExtStat()
}

// setExtStatus routes a setStatus into the per-key registry and recomputes
// the one-line footer summary. An empty text clears that key only; it never
// clears a sibling plugin's status (the old single-slot behaviour did).
func (m *Model) setExtStatus(key, text string) {
	key = strings.TrimSpace(stripANSI(key))
	if key == "" {
		key = "plugin"
	}
	text = stripANSI(text)
	if m.extStatus == nil {
		m.extStatus = map[string]string{}
	}
	m.extStatusSeq = touchKey(m.extStatusSeq, key)
	if text == "" {
		delete(m.extStatus, key)
		m.extStatusSeq = dropKey(m.extStatusSeq, key)
	} else {
		m.extStatus[key] = text
	}
	m.syncExtStat()
}

// touchKey moves key to the end of seq, appending it when new.
func touchKey(seq []string, key string) []string {
	seq = dropKey(seq, key)
	return append(seq, key)
}

// dropKey removes key from seq, preserving the order of the rest.
func dropKey(seq []string, key string) []string {
	out := seq[:0]
	for _, k := range seq {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}

// syncExtStat recomputes the derived footer string from the registry.
// Most-recently-set non-empty status wins the lead slot; the rest follow in
// LRU order up to extStatusLimit. Keeping extStat derived (rather than
// assigning it directly) is what stops one plugin from erasing another.
func (m *Model) syncExtStat() {
	parts := make([]string, 0, extStatusLimit)
	for i := len(m.extStatusSeq) - 1; i >= 0 && len(parts) < extStatusLimit; i-- {
		if text := strings.TrimSpace(m.extStatus[m.extStatusSeq[i]]); text != "" {
			parts = append(parts, text)
		}
	}
	// Reverse into LRU order so the strip reads left-to-right as the
	// plugins last spoke, and collapse the rest when we had to truncate.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	hidden := len(m.extStatus) - len(parts)
	if hidden > 0 {
		parts = append(parts, "+"+itoa(hidden))
	}
	m.extStat = strings.Join(parts, " · ")
}

// extStatusKeys lists the live status keys, most recent first. Used by
// tests and by /debug-ext to prove no key is being dropped.
func (m *Model) extStatusKeys() []string {
	out := make([]string, 0, len(m.extStatus))
	for i := len(m.extStatusSeq) - 1; i >= 0; i-- {
		out = append(out, m.extStatusSeq[i])
	}
	return out
}

// ---------------------------------------------------------------- widget --

// setExtWidget stores a widget panel for any key. Unlike the previous
// agent-team special case, an unknown key is no longer downgraded to a
// one-shot chat notice — it gets a real panel, which is what makes
// pi-lens / plan-mode / web-activity stop disappearing.
//
// Placement normalises to aboveEditor, matching the team widget's contract.
func (m *Model) setExtWidget(key string, lines []string, placement string) {
	key = strings.TrimSpace(stripANSI(key))
	if key == "" {
		key = "plugin"
	}
	placement = strings.TrimSpace(placement)
	if !strings.EqualFold(placement, "belowEditor") {
		placement = "aboveEditor"
	}
	if m.extWidget == nil {
		m.extWidget = map[string]*extWidgetPanel{}
	}
	if len(lines) == 0 {
		m.clearExtWidget(key)
		return
	}
	m.extWidgetSeq = touchKey(m.extWidgetSeq, key)
	// Truncate to the panel budget: the tail is the live/active part, so
	// keep the first lines as the header and the last lines as the tail.
	kept := lines
	if len(kept) > extWidgetHeightLimit {
		trimmed := make([]string, 0, extWidgetHeightLimit)
		head := (extWidgetHeightLimit + 1) / 2
		trimmed = append(trimmed, kept[:head]...)
		trimmed = append(trimmed, "  …")
		trimmed = append(trimmed, kept[len(kept)-(extWidgetHeightLimit-head-1):]...)
		kept = trimmed
	}
	m.extWidget[key] = &extWidgetPanel{
		Key:       key,
		Lines:     append([]string{}, kept...),
		Placement: placement,
	}
}

// clearExtWidget removes one plugin's panel and leaves its siblings alone.
func (m *Model) clearExtWidget(key string) {
	key = strings.TrimSpace(stripANSI(key))
	if key == "" {
		key = "plugin"
	}
	delete(m.extWidget, key)
	m.extWidgetSeq = dropKey(m.extWidgetSeq, key)
}

// extWidgetPanel returns the panel for key, or nil.
func (m *Model) extWidgetPanel(key string) *extWidgetPanel {
	if m.extWidget == nil {
		return nil
	}
	return m.extWidget[strings.TrimSpace(stripANSI(key))]
}

// extWidgetKeys lists live widget keys in LRU order (least recent first).
func (m *Model) extWidgetKeys() []string {
	out := make([]string, 0, len(m.extWidget))
	for _, k := range m.extWidgetSeq {
		if m.extWidget[k] != nil {
			out = append(out, k)
		}
	}
	return out
}

// ------------------------------------------------- synchronous ext cmds --

// beginExtCmd marks a synchronous extension command (one that does not open
// a model turn, e.g. /team) as in flight. It sets a visible status so the
// user gets feedback during the round-trip, which is the whole point: the
// old path forwarded silently and a buffered send looked identical to a
// dead command.
func (m *Model) beginExtCmd(name, status string) {
	if m.extCmdWait == nil {
		m.extCmdWait = map[string]bool{}
	}
	m.extCmdWait[name] = true
	m.Status = status
}

// endExtCmd clears the in-flight marker. err non-nil means the forward
// itself failed; delivered means the expected custom message actually
// arrived. Neither is silent, so a command can never look like it worked.
func (m *Model) endExtCmd(name string, err error, delivered bool) {
	delete(m.extCmdWait, name)
	if m.Status == "" {
		m.Status = ""
	}
	if err != nil {
		m.AddBlock(Block{Kind: "notice", Text: name + " failed: " + err.Error(), Err: true})
		return
	}
	if !delivered {
		m.AddBlock(Block{Kind: "notice", Text: name + " got no reply from the plugin (still busy, or the plugin is not loaded)"})
	}
}

// extCmdPending reports whether name is still awaiting a reply.
func (m *Model) extCmdPending(name string) bool { return m.extCmdWait[name] }
