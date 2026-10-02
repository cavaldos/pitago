package app

import (
	"strings"
	"time"

	"pitago/src/components/format"
)

// Shared formatting lives in components/format (single source of truth).
// The Short/ShortID/OrDefault/FmtNum names are re-exported here because
// src/builtin calls them as app.Short etc.
var (
	Short     = format.Short
	ShortID   = format.ShortID
	OrDefault = format.OrDefault
	FmtNum    = format.FmtNum
	Fit       = format.Fit
)

func (m Model) firstUser() string {
	for _, b := range m.blocks {
		if b.Kind == "user" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

// maxToolResultChars caps stored tool output like pi (51200 bytes): enough
// for a whole file so expand can show it, small enough to stay in memory.
const maxToolResultChars = 51200

// expandHint is the inline key hint pi shows after a collapsed tool
// preview. Ctrl+O is the yank picker here, so expand lives on Ctrl+G.
const expandHint = "ctrl+g to expand"

const collapseHint = "ctrl+g to collapse"

func fmtDur(d time.Duration) string { return format.FmtDur(d) }

// fmtTurnDur is fmtDur under a second: fmtDur rounds to whole seconds, so a
// prompt answered in 400ms would read as "0s". Above a second the two agree.
func fmtTurnDur(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return fmtDur(d)
}
func twoCol(l, r string, inner int) string { return format.TwoCol(l, r, inner) }
func ctxBar(pct float64, w int) string     { return format.CtxBar(pct, w) }
func stripANSI(s string) string            { return format.StripANSI(s) }
func fmtComma(n int) string                { return format.FmtComma(n) }
func prettyArgs(tool, raw string) string   { return format.PrettyArgs(tool, raw) }
func oneLineStr(s string) string           { return strings.ReplaceAll(s, "\n", " ⏎ ") }
func boolPtr(b bool) *bool                 { return &b }
