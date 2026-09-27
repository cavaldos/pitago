// Package palette implements the / command-palette matching, ranked
// outside-in: a query matches every command whose name contains it
// (case-insensitive), prefix matches first, then by match position, then
// alphabetically — so "/re" suggests /recent, /reload, /resume before
// /tree. Name matches win outright; source/description only match when no
// name does (fallback tier). Empty query matches everything in catalog
// order, so "/" shows the full list and the popup window scrolls.
// Pure, no TUI state.
package palette

import (
	"sort"
	"strings"
)

// Win is the visible row window of the command popup (the full match list
// scrolls; the popup never grows past this). Var (not const) so the
// /settings "Autocomplete max" row can tune it like stock pi.
var Win = 10

// Match returns the indices of names matching query, ranked outside-in:
// earlier match position first, then alphabetically (case-insensitive).
// Empty query matches everything in catalog order.
func Match(query string, names []string) []int {
	q := strings.ToLower(query)
	if q == "" {
		out := make([]int, len(names))
		for i := range names {
			out[i] = i
		}
		return out
	}
	type scored struct {
		idx   int
		pos   int
		lower string
	}
	var s []scored
	for i, n := range names {
		nl := strings.ToLower(n)
		pos := strings.Index(nl, q)
		if pos < 0 {
			continue
		}
		s = append(s, scored{i, pos, nl})
	}
	sort.Slice(s, func(a, b int) bool {
		if s[a].pos != s[b].pos {
			return s[a].pos < s[b].pos
		}
		if s[a].lower != s[b].lower {
			return s[a].lower < s[b].lower
		}
		return s[a].idx < s[b].idx
	})
	out := make([]int, 0, len(s))
	for _, e := range s {
		out = append(out, e.idx)
	}
	return out
}

// MatchWithFallback ranks name matches first (see Match); only when no
// name matches does it fall back to the per-row fallback haystack
// (source + extension tag + description). Fallback rows sort by match
// position, then command name alphabetically. Empty query matches
// everything in catalog order.
func MatchWithFallback(query string, names, fallbacks []string) []int {
	q := strings.ToLower(query)
	if q == "" {
		out := make([]int, len(names))
		for i := range names {
			out[i] = i
		}
		return out
	}
	if got := Match(query, names); len(got) > 0 {
		return got
	}
	type scored struct {
		idx   int
		pos   int
		lower string
	}
	var s []scored
	for i, n := range names {
		var fb string
		if i < len(fallbacks) {
			fb = fallbacks[i]
		}
		fl := strings.ToLower(fb)
		pos := strings.Index(fl, q)
		if pos < 0 {
			continue
		}
		s = append(s, scored{i, pos, strings.ToLower(n)})
	}
	sort.Slice(s, func(a, b int) bool {
		if s[a].pos != s[b].pos {
			return s[a].pos < s[b].pos
		}
		if s[a].lower != s[b].lower {
			return s[a].lower < s[b].lower
		}
		return s[a].idx < s[b].idx
	})
	out := make([]int, 0, len(s))
	for _, e := range s {
		out = append(out, e.idx)
	}
	return out
}
