// Package sessiontree is a port of pi's Session Tree list: the TreeList class
// in pi-coding-agent dist/modes/interactive/components/tree-selector.js
// (pi 1.4.0). It owns everything that list does — branch ordering, indent and
// connector layout, folding, filter modes, search, the selection rules and
// the row text — and nothing else.
//
// It is pure logic: rows come out as spans tagged with pi's theme token
// names (accent, muted, …), and src/app maps those tokens onto its palette.
// Keeping the port free of lipgloss is what lets it be tested line for line
// against pi's own behaviour.
package sessiontree

import (
	"sort"
	"strings"
	"time"

	"pitago/src/pirpc"
)

// Theme tokens, named after pi's theme colors. "" is the terminal's text.
const (
	StyleText         = ""
	StyleAccent       = "accent"
	StyleSuccess      = "success"
	StyleMuted        = "muted"
	StyleDim          = "dim"
	StyleWarning      = "warning"
	StyleError        = "error"
	StyleBorderAccent = "borderAccent"
	StyleCustomLabel  = "customMessageLabel"
)

// Span is one run of text in a single style. Selected marks pi's
// selectedBg, which covers the cursor gutter and the row body.
type Span struct {
	Text     string
	Style    string
	Bold     bool
	Italic   bool
	Selected bool
}

// Line is one rendered row.
type Line []Span

// Modes are pi's filter modes in its cycle order.
var Modes = []string{"default", "no-tools", "user-only", "labeled-only", "all"}

// ValidMode reports whether mode is one of Modes.
func ValidMode(mode string) bool {
	for _, m := range Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// Node is one session entry with its children. Label is mutable: editing a
// label updates the node in place, exactly like pi's updateNodeLabel.
type Node struct {
	Entry          pirpc.TreeEntry
	Children       []*Node
	Label          string
	LabelTimestamp string
}

// ID is the entry id.
func (n *Node) ID() string { return n.Entry.ID }

func parentOf(e pirpc.TreeEntry) string {
	if e.ParentID == nil {
		return ""
	}
	return *e.ParentID
}

type gutter struct {
	position int
	show     bool
}

type flatNode struct {
	node               *Node
	indent             int
	showConnector      bool
	isLast             bool
	gutters            []gutter
	isVirtualRootChild bool
}

type toolCall struct {
	name string
	args []byte
}

// List is pi's TreeList. Entry ids are never empty, so "" stands for pi's
// null everywhere (no leaf, no parent, the virtual root).
type List struct {
	flat           []*flatNode
	filtered       []*flatNode
	byID           map[string]*flatNode
	selected       int
	leafID         string
	maxVisible     int
	mode           string
	query          string
	toolCalls      map[string]toolCall
	multipleRoots  bool
	showLabelTimes bool
	activePath     map[string]bool
	visParent      map[string]string
	visChildren    map[string][]string
	lastSelected   string
	folded         map[string]bool

	// Now and Home are pi's clock and $HOME, swappable for tests.
	Now  func() time.Time
	Home string
}

// New builds the list the way pi's constructor does: flatten, mark the
// active path, filter, then select initialSelectedID (or the leaf), walking
// up to the nearest visible ancestor when that entry is filtered out.
func New(roots []pirpc.TreeNode, leafID string, maxVisible int, initialSelectedID, mode string) *List {
	if !ValidMode(mode) {
		mode = "default"
	}
	l := &List{
		leafID:     leafID,
		maxVisible: maxVisible,
		mode:       mode,
		toolCalls:  map[string]toolCall{},
		activePath: map[string]bool{},
		folded:     map[string]bool{},
		Now:        time.Now,
		Home:       homeDir(),
	}
	nodes := convert(roots)
	l.multipleRoots = len(nodes) > 1
	l.flat = l.flattenTree(nodes)
	l.byID = make(map[string]*flatNode, len(l.flat))
	for _, fn := range l.flat {
		l.byID[fn.node.ID()] = fn
	}
	l.buildActivePath()
	l.applyFilter()
	target := initialSelectedID
	if target == "" {
		target = leafID
	}
	l.selected = l.findNearestVisibleIndex(target)
	if l.selected >= 0 && l.selected < len(l.filtered) {
		l.lastSelected = l.filtered[l.selected].node.ID()
	}
	return l
}

func convert(ns []pirpc.TreeNode) []*Node {
	out := make([]*Node, 0, len(ns))
	for _, n := range ns {
		out = append(out, &Node{Entry: n.Entry, Children: convert(n.Children),
			Label: n.Label, LabelTimestamp: n.LabelTimestamp})
	}
	return out
}

// findNearestVisibleIndex returns the index of entryID in the filtered rows,
// or of its nearest visible ancestor, or the last row as a fallback.
func (l *List) findNearestVisibleIndex(entryID string) int {
	if len(l.filtered) == 0 {
		return 0
	}
	visible := make(map[string]int, len(l.filtered))
	for i, fn := range l.filtered {
		visible[fn.node.ID()] = i
	}
	for cur := entryID; cur != ""; {
		if i, ok := visible[cur]; ok {
			return i
		}
		fn := l.byID[cur]
		if fn == nil {
			break
		}
		cur = parentOf(fn.node.Entry)
	}
	return len(l.filtered) - 1
}

// buildActivePath marks the entries from the leaf up to the root.
func (l *List) buildActivePath() {
	l.activePath = map[string]bool{}
	for cur := l.leafID; cur != ""; {
		l.activePath[cur] = true
		fn := l.byID[cur]
		if fn == nil {
			break
		}
		cur = parentOf(fn.node.Entry)
	}
}

type stackItem struct {
	node          *Node
	indent        int
	justBranched  bool
	showConnector bool
	isLast        bool
	gutters       []gutter
	virtual       bool
}

// flattenTree lays the tree out depth-first with the branch holding the
// active leaf first at every level, computing pi's indent, connector and
// gutter state for each row.
func (l *List) flattenTree(roots []*Node) []*flatNode {
	var result []*flatNode
	l.toolCalls = map[string]toolCall{}

	// Which subtrees contain the active leaf: pre-order list, then a
	// reverse (post-order) pass so children are known before parents.
	containsActive := map[*Node]bool{}
	{
		var all []*Node
		pre := append([]*Node(nil), roots...)
		for len(pre) > 0 {
			n := pre[len(pre)-1]
			pre = pre[:len(pre)-1]
			all = append(all, n)
			for i := len(n.Children) - 1; i >= 0; i-- {
				pre = append(pre, n.Children[i])
			}
		}
		for i := len(all) - 1; i >= 0; i-- {
			n := all[i]
			has := l.leafID != "" && n.ID() == l.leafID
			for _, c := range n.Children {
				if containsActive[c] {
					has = true
				}
			}
			containsActive[n] = has
		}
	}

	multipleRoots := len(roots) > 1
	ordered := append([]*Node(nil), roots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return containsActive[ordered[i]] && !containsActive[ordered[j]]
	})
	var stack []stackItem
	for i := len(ordered) - 1; i >= 0; i-- {
		stack = append(stack, stackItem{node: ordered[i], indent: boolInt(multipleRoots),
			justBranched: multipleRoots, showConnector: multipleRoots,
			isLast: i == len(ordered)-1, virtual: multipleRoots})
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		e := it.node.Entry
		if e.Type == "message" && e.Message.Role == "assistant" {
			for _, b := range pirpc.BlocksOf(e.Message.Content) {
				if b.Type == "toolCall" {
					l.toolCalls[b.ID] = toolCall{name: b.Name, args: b.Arguments}
				}
			}
		}
		result = append(result, &flatNode{node: it.node, indent: it.indent,
			showConnector: it.showConnector, isLast: it.isLast, gutters: it.gutters,
			isVirtualRootChild: it.virtual})

		children := it.node.Children
		multipleChildren := len(children) > 1
		var prioritized, rest []*Node
		for _, c := range children {
			if containsActive[c] {
				prioritized = append(prioritized, c)
			} else {
				rest = append(rest, c)
			}
		}
		orderedChildren := append(prioritized, rest...)
		childIndent := l.childIndent(it.indent, multipleChildren, it.justBranched)
		childGutters := l.childGutters(it.indent, it.showConnector, it.virtual, it.isLast, it.gutters)
		for i := len(orderedChildren) - 1; i >= 0; i-- {
			stack = append(stack, stackItem{node: orderedChildren[i], indent: childIndent,
				justBranched: multipleChildren, showConnector: multipleChildren,
				isLast: i == len(orderedChildren)-1, gutters: childGutters})
		}
	}
	return result
}

// childIndent: a branch point shifts its children right by one level, and
// so does the first generation after a branch; a single-child chain stays
// flat.
func (l *List) childIndent(indent int, multipleChildren, justBranched bool) int {
	switch {
	case multipleChildren:
		return indent + 1
	case justBranched && indent > 0:
		return indent + 1
	default:
		return indent
	}
}

// childGutters adds a gutter at the connector's column for the descendants
// of a row that displayed a connector.
func (l *List) childGutters(indent int, showConnector, virtual, isLast bool, gutters []gutter) []gutter {
	if !showConnector || virtual {
		return gutters
	}
	displayIndent := indent
	if l.multipleRoots {
		displayIndent = max(0, indent-1)
	}
	out := make([]gutter, 0, len(gutters)+1)
	out = append(out, gutters...)
	return append(out, gutter{position: max(0, displayIndent-1), show: !isLast})
}

// isSettingsEntry is the bookkeeping pi hides in the default view.
func isSettingsEntry(t string) bool {
	switch t {
	case "label", "context_edit", "custom", "model_change", "thinking_level_change", "session_info":
		return true
	}
	return false
}

// applyFilter recomputes the visible rows: usage entries and tool-call-only
// assistant turns are always hidden (the leaf excepted), then the filter
// mode, the search tokens and the folds apply. The selection stays on the
// same entry, or moves to its nearest visible ancestor.
func (l *List) applyFilter() {
	if len(l.filtered) > 0 && l.selected >= 0 && l.selected < len(l.filtered) {
		l.lastSelected = l.filtered[l.selected].node.ID()
	}
	tokens := strings.Fields(strings.ToLower(l.query))
	var out []*flatNode
	for _, fn := range l.flat {
		e := fn.node.Entry
		if e.Type == "usage" {
			continue
		}
		if e.Type == "message" && e.Message.Role == "assistant" && e.ID != l.leafID {
			sr := e.Message.StopReason
			errOrAborted := sr != "" && sr != "stop" && sr != "toolUse"
			if !hasTextContent(e.Message.Content) && !errOrAborted {
				continue
			}
		}
		pass := true
		switch l.mode {
		case "user-only":
			pass = e.Type == "message" && e.Message.Role == "user"
		case "no-tools":
			pass = !isSettingsEntry(e.Type) && !(e.Type == "message" && e.Message.Role == "toolResult")
		case "labeled-only":
			pass = fn.node.Label != ""
		case "all":
			pass = true
		default:
			pass = !isSettingsEntry(e.Type)
		}
		if !pass {
			continue
		}
		if len(tokens) > 0 {
			text := strings.ToLower(l.searchableText(fn.node))
			match := true
			for _, t := range tokens {
				if !strings.Contains(text, t) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, fn)
	}
	if len(l.folded) > 0 {
		skip := map[string]bool{}
		for _, fn := range l.flat {
			id, pid := fn.node.ID(), parentOf(fn.node.Entry)
			if pid != "" && (l.folded[pid] || skip[pid]) {
				skip[id] = true
			}
		}
		kept := out[:0:0]
		for _, fn := range out {
			if !skip[fn.node.ID()] {
				kept = append(kept, fn)
			}
		}
		out = kept
	}
	l.filtered = out
	l.recalculateVisualStructure()
	if l.lastSelected != "" {
		l.selected = l.findNearestVisibleIndex(l.lastSelected)
	} else if l.selected >= len(l.filtered) {
		l.selected = max(0, len(l.filtered)-1)
	}
	if len(l.filtered) > 0 && l.selected >= 0 && l.selected < len(l.filtered) {
		l.lastSelected = l.filtered[l.selected].node.ID()
	}
}

// recalculateVisualStructure re-lays the indent/connectors over the visible
// rows: hidden entries are skipped and descendants hang off their nearest
// visible ancestor, with the same rules flattenTree uses.
func (l *List) recalculateVisualStructure() {
	if len(l.filtered) == 0 {
		return
	}
	visible := make(map[string]bool, len(l.filtered))
	for _, fn := range l.filtered {
		visible[fn.node.ID()] = true
	}
	parentID := func(id string) string {
		if fn := l.byID[id]; fn != nil {
			return parentOf(fn.node.Entry)
		}
		return ""
	}
	visibleAncestor := func(id string) string {
		for cur := parentID(id); cur != ""; cur = parentID(cur) {
			if visible[cur] {
				return cur
			}
		}
		return ""
	}
	visParent := map[string]string{}
	visChildren := map[string][]string{"": nil}
	for _, fn := range l.filtered {
		id := fn.node.ID()
		anc := visibleAncestor(id)
		visParent[id] = anc
		visChildren[anc] = append(visChildren[anc], id)
	}
	rootIDs := visChildren[""]
	l.multipleRoots = len(rootIDs) > 1
	byID := make(map[string]*flatNode, len(l.filtered))
	for _, fn := range l.filtered {
		byID[fn.node.ID()] = fn
	}
	type item struct {
		id            string
		indent        int
		justBranched  bool
		showConnector bool
		isLast        bool
		gutters       []gutter
		virtual       bool
	}
	var stack []item
	for i := len(rootIDs) - 1; i >= 0; i-- {
		stack = append(stack, item{id: rootIDs[i], indent: boolInt(l.multipleRoots),
			justBranched: l.multipleRoots, showConnector: l.multipleRoots,
			isLast: i == len(rootIDs)-1, virtual: l.multipleRoots})
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fn := byID[it.id]
		if fn == nil {
			continue
		}
		fn.indent = it.indent
		fn.showConnector = it.showConnector
		fn.isLast = it.isLast
		fn.gutters = it.gutters
		fn.isVirtualRootChild = it.virtual
		children := visChildren[it.id]
		multipleChildren := len(children) > 1
		childIndent := l.childIndent(it.indent, multipleChildren, it.justBranched)
		childGutters := l.childGutters(it.indent, it.showConnector, it.virtual, it.isLast, it.gutters)
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, item{id: children[i], indent: childIndent,
				justBranched: multipleChildren, showConnector: multipleChildren,
				isLast: i == len(children)-1, gutters: childGutters})
		}
	}
	l.visParent = visParent
	l.visChildren = visChildren
}

// --- accessors -------------------------------------------------------------

// Len is the number of visible rows.
func (l *List) Len() int { return len(l.filtered) }

// SelectedIndex is the selected row (0 when the list is empty).
func (l *List) SelectedIndex() int { return l.selected }

// Selected is the selected entry, nil when nothing is visible.
func (l *List) Selected() *Node {
	if l.selected < 0 || l.selected >= len(l.filtered) {
		return nil
	}
	return l.filtered[l.selected].node
}

// Select moves the selection to visible row i (out of range is ignored).
func (l *List) Select(i int) {
	if i >= 0 && i < len(l.filtered) {
		l.selected = i
	}
}

// Query is the search text typed so far.
func (l *List) Query() string { return l.query }

// Mode is the active filter mode.
func (l *List) Mode() string { return l.mode }

// LeafID is the session's current leaf ("" = none).
func (l *List) LeafID() string { return l.leafID }

// MaxVisible is the number of rows one page shows.
func (l *List) MaxVisible() int { return l.maxVisible }

// SetMaxVisible resizes the page (a terminal resize).
func (l *List) SetMaxVisible(n int) {
	if n < 1 {
		n = 1
	}
	l.maxVisible = n
}

// Entry finds any entry of the tree by id, visible or not.
func (l *List) Entry(id string) *Node {
	if fn := l.byID[id]; fn != nil {
		return fn.node
	}
	return nil
}

// VisibleRange is the window of rows Render draws: [start, end).
func (l *List) VisibleRange() (start, end int) {
	n := len(l.filtered)
	start = max(0, min(l.selected-l.maxVisible/2, n-l.maxVisible))
	end = min(start+l.maxVisible, n)
	return start, end
}

// StatusLabels is the suffix pi appends to the "(i/n)" line.
func (l *List) StatusLabels() string {
	s := ""
	switch l.mode {
	case "no-tools":
		s += " [no-tools]"
	case "user-only":
		s += " [user]"
	case "labeled-only":
		s += " [labeled]"
	case "all":
		s += " [all]"
	}
	if l.showLabelTimes {
		s += " [+label time]"
	}
	return s
}

// UpdateNodeLabel sets or clears an entry's label in place. An empty ts with
// a label stamps the current time, as pi does.
func (l *List) UpdateNodeLabel(entryID, label, ts string) {
	fn := l.byID[entryID]
	if fn == nil {
		return
	}
	fn.node.Label = label
	switch {
	case label == "":
		fn.node.LabelTimestamp = ""
	case ts != "":
		fn.node.LabelTimestamp = ts
	default:
		fn.node.LabelTimestamp = l.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
}

// --- input -----------------------------------------------------------------
//
// One method per binding pi's handleInput dispatches on. src/app maps keys
// to them, so the key table stays next to the terminal code.

// Up moves the selection up, wrapping to the bottom.
func (l *List) Up() {
	if len(l.filtered) == 0 {
		return
	}
	if l.selected == 0 {
		l.selected = len(l.filtered) - 1
	} else {
		l.selected--
	}
}

// Down moves the selection down, wrapping to the top.
func (l *List) Down() {
	if len(l.filtered) == 0 {
		return
	}
	if l.selected == len(l.filtered)-1 {
		l.selected = 0
	} else {
		l.selected++
	}
}

// PageUp moves one page up.
func (l *List) PageUp() { l.selected = max(0, l.selected-l.maxVisible) }

// PageDown moves one page down.
func (l *List) PageDown() {
	if len(l.filtered) == 0 {
		return
	}
	l.selected = min(len(l.filtered)-1, l.selected+l.maxVisible)
}

// FoldOrUp folds the selected branch, or jumps to the previous branch start.
func (l *List) FoldOrUp() {
	if id := l.selectedID(); id != "" && l.isFoldable(id) && !l.folded[id] {
		l.folded[id] = true
		l.applyFilter()
		return
	}
	l.selected = l.findBranchSegmentStart(false)
}

// UnfoldOrDown unfolds the selected branch, or jumps to the next branch start.
func (l *List) UnfoldOrDown() {
	if id := l.selectedID(); id != "" && l.folded[id] {
		delete(l.folded, id)
		l.applyFilter()
		return
	}
	l.selected = l.findBranchSegmentStart(true)
}

// Cancel is Esc: it clears a search first and reports true; with no search
// it reports false and the caller closes the tree.
func (l *List) Cancel() bool {
	if l.query == "" {
		return false
	}
	l.query = ""
	l.folded = map[string]bool{}
	l.applyFilter()
	return true
}

// SetMode switches straight to mode (pi's Ctrl+D "default view").
func (l *List) SetMode(mode string) {
	if !ValidMode(mode) {
		return
	}
	l.mode = mode
	l.folded = map[string]bool{}
	l.applyFilter()
}

// ToggleMode flips between mode and default (pi's Ctrl+T/U/L/A).
func (l *List) ToggleMode(mode string) {
	if l.mode == mode {
		l.SetMode("default")
	} else {
		l.SetMode(mode)
	}
}

// CycleMode steps through Modes (pi's Ctrl+O / Shift+Ctrl+O).
func (l *List) CycleMode(delta int) {
	i := 0
	for k, m := range Modes {
		if m == l.mode {
			i = k
		}
	}
	n := len(Modes)
	l.SetMode(Modes[((i+delta)%n+n)%n])
}

// Backspace drops the last search character.
func (l *List) Backspace() {
	if l.query == "" {
		return
	}
	r := []rune(l.query)
	l.query = string(r[:len(r)-1])
	l.folded = map[string]bool{}
	l.applyFilter()
}

// Type appends printable text to the search.
func (l *List) Type(s string) {
	for _, r := range s {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return
		}
	}
	if s == "" {
		return
	}
	l.query += s
	l.folded = map[string]bool{}
	l.applyFilter()
}

// ToggleLabelTimestamps shows or hides the label times.
func (l *List) ToggleLabelTimestamps() { l.showLabelTimes = !l.showLabelTimes }

func (l *List) selectedID() string {
	if n := l.Selected(); n != nil {
		return n.ID()
	}
	return ""
}

// isFoldable: a row with visible children that is a root or the first row
// of a branch (its visible parent has several visible children).
func (l *List) isFoldable(id string) bool {
	if len(l.visChildren[id]) == 0 {
		return false
	}
	parent, ok := l.visParent[id]
	if !ok || parent == "" {
		return true
	}
	return len(l.visChildren[parent]) > 1
}

// findBranchSegmentStart finds the next branch start: down follows the
// first visible child to the next branch point, up walks the visible
// parents to the start of the segment above.
func (l *List) findBranchSegmentStart(down bool) int {
	sel := l.selectedID()
	if sel == "" {
		return l.selected
	}
	index := make(map[string]int, len(l.filtered))
	for i, fn := range l.filtered {
		index[fn.node.ID()] = i
	}
	cur := sel
	if down {
		for {
			children := l.visChildren[cur]
			if len(children) == 0 {
				return index[cur]
			}
			if len(children) > 1 {
				return index[children[0]]
			}
			cur = children[0]
		}
	}
	for {
		parent := l.visParent[cur]
		if parent == "" {
			return index[cur]
		}
		if len(l.visChildren[parent]) > 1 {
			if start := index[cur]; start < l.selected {
				return start
			}
		}
		cur = parent
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
