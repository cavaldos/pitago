package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/pet"
)

// Sidebar pet — state core lives in components/pet; this file keeps the
// Model wiring (transition timers, tick loop, sidebar render).
//
// Busy states are TIMED per cycle: the row shows elapsed seconds for the
// current status period ("Thinking... 7s"). Every transition restarts the
// count. done(stop) gates the Done flash, so Esc-abort decays quietly.

// Aliases so existing wiring (update.go, tests) keeps reading naturally.
type petStatus = pet.Status

const (
	petIdle     = pet.Idle
	petThinking = pet.Thinking
	petWriting  = pet.Writing
	petWorking  = pet.Working
	petSuccess  = pet.Success
	petError    = pet.Error
)

var petFaces = pet.Faces

const (
	petFlashDelay = 4 * time.Second
	petTickEvery  = 500 * time.Millisecond
)

type petState struct {
	status  petStatus
	since   time.Time // current status period began (busy only)
	tick    int
	gen     int  // transition generation; stale flash timers check it
	ticking bool // a 500ms loop is already scheduled
	inTurn  bool // between agent_start and agent_settled
	sawStop bool // a done(stop) closed a round (gates the Done flash)
}

// taskRuntime tracks the one task currently executing above the editor.
// Usage totals come from authoritative finalized assistant messages.
type taskRuntime struct {
	activeID     string
	startedAt    time.Time
	inputTokens  int
	outputTokens int
}

type petTickMsg struct{}
type petFlashMsg struct{ gen int }

func petTickCmd() tea.Cmd {
	return tea.Tick(petTickEvery, func(time.Time) tea.Msg { return petTickMsg{} })
}

// petSet transitions the pet. Same-status calls are no-ops so stream deltas
// never restart the elapsed timer.
func (m *Model) petSet(next petStatus) tea.Cmd {
	if m.pet.status == next {
		return nil
	}
	m.pet.status = next
	m.pet.gen++
	if next.Busy() {
		m.pet.since = time.Now()
	}
	var cmds []tea.Cmd
	if next.Flashing() {
		gen := m.pet.gen
		cmds = append(cmds, tea.Tick(petFlashDelay, func(time.Time) tea.Msg {
			return petFlashMsg{gen: gen}
		}))
	}
	if (next.Busy() || next.Flashing()) && !m.pet.ticking {
		m.pet.ticking = true
		cmds = append(cmds, petTickCmd())
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

// petAnchor runs on agent_start: a fresh run anchors as working, but
// re-issued starts between rounds must not override thinking/writing, and a
// queued follow-up must not wipe the Done flash (its stream events re-anchor
// below if the turn genuinely continues).
func (m *Model) petAnchor() tea.Cmd {
	m.pet.inTurn = true
	m.pet.sawStop = false
	if m.pet.status.Flashing() || m.pet.status.Busy() {
		return nil
	}
	return m.petSet(petWorking)
}

// petSettled runs on agent_settled: pi will not continue automatically.
// Without a preceding done(stop) (Esc-abort) it decays quietly to idle.
func (m *Model) petSettled() tea.Cmd {
	m.pet.inTurn = false
	if m.pet.status.Flashing() {
		return nil
	}
	if m.pet.sawStop {
		return m.petSet(petSuccess)
	}
	return m.petSet(petIdle)
}

func (m Model) petFace() string {
	return pet.Face(m.pet.status, m.pet.tick)
}

func (m Model) petLabel() string {
	return pet.Label(m.pet.status, m.pet.since)
}

// renderPet draws the PET sidebar section (face + timed status line).
// Fixed height: petRows content rows (title + face + separator) —
// recentAt's click mapping depends on this, keep them in sync.
const petRows = 3

func (m Model) renderPet(inner int) string {
	face := m.petFace()
	fstyle := statusBarStyle
	switch m.pet.status {
	case petThinking, petWriting, petWorking:
		fstyle = fstyle.Copy().Foreground(cText)
	case petSuccess:
		fstyle = okStyle
	case petError:
		fstyle = errStyle
	}
	row := fstyle.Render(face) + " " + statusBarStyle.Render(Short(m.petLabel(), inner-10))
	return sideTitleStyle.Render("PET") + "\n" + row + "\n" + sep() + "\n"
}
