package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Per-keystroke cost, measured the way the user feels it: one typed
// character through Update and the View Bubble Tea paints after it. The
// shell-mode arms exist because shell output lands in the transcript as
// bash blocks, and the keystroke path hashes every block (blockKey), so
// what a block STORES is what typing costs.
//
// Run with:
//	go test ./src/app/ -run XXX -bench 'BenchmarkTypeRune' -benchtime 2000x

// shellPerfModel is the same shape as scrollModel (real transcript, real
// viewports), so a keystroke pays the full repaint it pays in the app.
// shellOn selects the mode under test.
func shellPerfModel(t testing.TB, shellOn bool) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 120, 30
	m.baseVpH = 23
	m.hideSide = true
	m.vp = viewport.New(116, 23)
	m.sideVp = viewport.New(sideInnerW, 10)
	m.blocks = make([]Block, 400)
	for i := range m.blocks {
		m.blocks[i] = Block{Kind: "assistant", Text: "answer\ndetail line one\ndetail line two"}
	}
	if shellOn {
		m.shellOn = true
		m.ta.Placeholder = shellPlaceholder
	}
	m.Refresh()
	return m
}

// BenchmarkTypeRune: the same keystroke in pi mode and in shell mode,
// against the same transcript. Mode must not change the cost.
func BenchmarkTypeRune(b *testing.B) {
	for _, shellOn := range []bool{false, true} {
		name := "pi"
		if shellOn {
			name = "shell"
		}
		b.Run(name, func(b *testing.B) {
			m := shellPerfModel(b, shellOn)
			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tm, _ := m.Update(key)
				m = tm.(Model)
				_ = m.View()
			}
		})
	}
}

// BenchmarkTypeRuneOutputSize is the one that matters for shell mode: a
// shell session fills the transcript with command output, and every
// keystroke re-hashes all of it (blockKey). blockBytes is what ONE bash
// block holds; the "raw" arm is the pre-fix behaviour (the whole command
// output stored) and the "capped" arm is what ships (capShellOutput).
func BenchmarkTypeRuneOutputSize(b *testing.B) {
	line := "some shell output line with a few words in it\n"
	for _, blockBytes := range []int{1 << 10, 40 << 10, 400 << 10, 2 << 20} {
		full := "$ ls -la\n" + strings.Repeat(line, blockBytes/len(line))
		for _, arm := range []struct {
			name string
			text string
		}{{"raw", full}, {"capped", capShellOutput(full)}} {
			bl := Block{Kind: "bash", Text: arm.text}
			b.Run(fmt.Sprintf("block=%dKB_x5/%s", blockBytes>>10, arm.name), func(b *testing.B) {
				m := shellPerfModel(b, true)
				m.blocks = make([]Block, 5)
				for i := range m.blocks {
					m.blocks[i] = bl
				}
				m.renderCache, m.renderCacheKey = nil, nil
				m.Refresh()
				key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					tm, _ := m.Update(key)
					m = tm.(Model)
					_ = m.View()
				}
			})
		}
	}
}

// BenchmarkBlockKey isolates the hash the render cache is keyed on: the
// per-block cost of a keystroke is this, once per block.
func BenchmarkBlockKey(b *testing.B) {
	line := "some shell output line with a few words in it\n"
	for _, blockBytes := range []int{shellOutCap, 400 << 10, 2 << 20} {
		bl := Block{Kind: "bash", Text: "$ ls -la\n" + strings.Repeat(line, blockBytes/len(line))}
		b.Run(fmt.Sprintf("text=%dKB", blockBytes>>10), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = blockKey(bl, 116, false, false, "", false)
			}
		})
	}
}
