package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

const (
	petFlashDelay = 4 * time.Second
	// petTickEvery is the fast cadence: busy/flashing/task-active turns, where
	// the same timer repaints the elapsed "7s" label every half second.
	petTickEvery = 500 * time.Millisecond
	// petTickIdle is the slow cadence for the only remaining job — animating
	// the idle pet (blink/sway) and letting the rotation come around. A
	// still pet does not need 60 repaints a minute.
	petTickIdle = time.Second
	// petRotateEvery is how long a pet keeps the sidebar before the next one
	// from the set takes its place (the pets take turns). Rotation is
	// anchored on pet.at, not on the tick count, so a slow terminal cannot
	// skip a pet.
	petRotateEvery = 20 * time.Second
)

type petState struct {
	status  petStatus
	since   time.Time // current status period began (busy only)
	tick    int
	gen     int  // transition generation; stale flash timers check it
	ticking bool // a petTickEvery loop is already scheduled
	inTurn  bool // between agent_start and agent_settled
	sawStop bool // a done(stop) closed a round (gates the Done flash)
	shown   string
	at      time.Time // when shown took the sidebar; drives the rotation clock
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

func petTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return petTickMsg{} })
}

// petInterval is the ONE place the cadence is decided: fast while the pet is
// busy/flashing or a task spins (the elapsed counter needs it), slow when the
// loop only animates the idle pet. armPetTick and the petTickMsg handler both
// read it, so a loop can never re-arm itself at a stale cadence.
func (m Model) petInterval() time.Duration {
	if m.pet.status.Busy() || m.pet.status.Flashing() || m.taskActive() {
		return petTickEvery
	}
	return petTickIdle
}

// petLooping reports whether the tick loop has a reason to stay alive. The
// pet section itself is a reason: a visible block animates and rotates, and
// the loop is what makes it do so while pi sits idle.
func (m Model) petLooping() bool {
	return m.pet.status.Busy() || m.pet.status.Flashing() ||
		m.taskActive() || m.SideVisible(SidePet)
}

// petShown is the pet actually drawn right now — the wandering one. It
// starts from the persisted anchor (the /pet choice) and is empty until the
// first tick or SetPet, so a hand-built Model still draws Resolve(PetName).
func (m Model) petShown() pet.Pet {
	if m.pet.shown == "" {
		return pet.Resolve(m.PetName)
	}
	return pet.Resolve(m.pet.shown)
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
		cmds = append(cmds, petTickCmd(m.petInterval()))
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

// armPetTick latches the pet loop. Every path that starts the loop goes
// through here, so the latch has exactly one owner: a path that arms it must
// also return the command, or the pet freezes for the rest of the session.
// The cadence comes from petInterval, so this works for a busy turn and for a
// merely animated idle block alike.
func (m *Model) armPetTick() tea.Cmd {
	m.pet.ticking = true
	return petTickCmd(m.petInterval())
}

// ensurePetTick starts the loop if nothing else has. Toggle paths use it so
// hiding the pet section stops the loop and showing it again restarts the
// animation without a turn having to come along to prime the latch.
func (m *Model) ensurePetTick() tea.Cmd {
	if m.pet.ticking || !m.petLooping() {
		return nil
	}
	return m.armPetTick()
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

// petFrame is the drawing currently shown in the sidebar: the wandering
// pet's frame for this tick. Resolve() absorbs saved-name drift — an unknown
// or missing pref falls back to the default pet, so the block always has art.
func (m Model) petFrame() []string {
	p := m.petShown()
	return p.FrameFor(m.pet.tick, m.pet.status.Busy())
}

func (m Model) petLabel() string {
	return pet.Label(m.pet.status, m.pet.since)
}

// petRows is the sidebar block height in content rows, per look. recentAt's
// click mapping is derived from it (see sideRowsBeforeRecent), so this method
// is the single source of truth: ascii = ArtRows art + separator, classic =
// the compact face+label row + separator. Nothing else may assume a height.
func (m Model) petRows() int {
	if m.petClassic() {
		return 2
	}
	return pet.ArtRows + 1
}

// petClassic reports whether the kaomoji look is active. Only the explicit
// "classic" value counts: Configure normalises a missing or hand-edited
// petStyle to ascii, and a zero Model renders ascii too.
func (m Model) petClassic() bool { return m.PetStyle == pet.StyleClassic }

// petArtStyle is the per-status color switch for the drawing (busy tints,
// Done!/Error flash). Shared by both looks so the status colors survive the
// classic face too.
func (m Model) petArtStyle() lipgloss.Style {
	switch m.pet.status {
	case petThinking, petWriting, petWorking:
		return statusBarStyle.Copy().Foreground(cText)
	case petSuccess:
		return okStyle
	case petError:
		return errStyle
	}
	return statusBarStyle
}

// renderPet draws the sidebar pet section in the look the user picked.
//
// ascii: the drawing with its name on the first INKED row and the timed
// status label on the ground row — ArtRows art + separator. Both labels sit
// in a column just past the drawing's widest line, measured from the raw
// (unstyled) frame and padded with plain spaces BEFORE any styling: ANSI
// escapes must never enter the width math.
//
// classic: the original compact row — animated face and status label on ONE
// line, then the separator. No "PET" title, no name: the look is the face.
//
// Height is m.petRows(); the recentAt click mapping follows it.
func (m Model) renderPet(inner int) string {
	astyle := m.petArtStyle()
	if m.petClassic() {
		labelW := inner - lipgloss.Width(pet.ClassicFace(m.pet.status, m.pet.tick)) - 1
		if labelW < 0 {
			labelW = 0
		}
		row := astyle.Render(pet.ClassicFace(m.pet.status, m.pet.tick)) + " " +
			statusBarStyle.Render(Short(m.petLabel(), labelW))
		return row + "\n" + sep() + "\n"
	}
	p := m.petShown()
	frame := m.petFrame()
	// Label budget: whatever is left of the sidebar width past the drawing
	// and the two-space gutter. Clamped at 0 so a tiny sidebar (or a wide
	// pet) cannot hand Short a negative width.
	labelW := inner - pet.Width(frame) - 2
	if labelW < 0 {
		labelW = 0
	}
	name := sideTitleStyle.Render(Short(p.Name, labelW))
	label := statusBarStyle.Render(Short(m.petLabel(), labelW))
	rows := make([]string, 0, m.petRows())
	for i, ln := range frame {
		// Pad on the RAW line, then style the whole row, so the colored name
		// and label never shift the art or each other between frames.
		ln = ln + strings.Repeat(" ", max(0, pet.Width(frame)-len([]rune(ln))))
		switch i {
		case petNameRow(frame):
			ln += "  " + name
		case len(frame) - 1:
			ln += "  " + label
		}
		rows = append(rows, astyle.Render(ln))
	}
	rows = append(rows, sep())
	return strings.Join(rows, "\n") + "\n"
}

// petNameRow picks the row the name shares with the art: the first row that
// actually has ink. Frames are padded on TOP to a fixed height, so a pet
// drawn shorter than ArtRows (cat) would otherwise get its name stranded on
// a blank line — a title row all over again, the very row this layout just
// saved. The label keeps the ground row; if a drawing were a single row the
// label wins it and the name falls back to the first one.
func petNameRow(frame []string) int {
	for i, ln := range frame {
		if strings.TrimSpace(ln) != "" {
			if i == len(frame)-1 {
				return 0
			}
			return i
		}
	}
	return 0
}

// SetPet applies one /pet entry — an animal name, or the "ascii"/"classic"
// look — and persists both keys. The style decides what the sidebar draws;
// the animal is the anchor, and a look switch KEEPS it, so flipping to
// classic and back does not lose the pet. Returns false for an unknown entry
// without touching the Model or prefs — /pet reports that as a bad argument.
func (m *Model) SetPet(entry string) bool {
	// Resolve the entry to (style, anchor): a look keeps the current anchor,
	// an animal switches to ascii and re-anchors on it.
	style, name := m.PetStyle, ""
	applied := ""
	switch {
	case pet.IsClassic(entry):
		style, applied = pet.StyleClassic, pet.StyleClassic
	case pet.IsStyle(entry): // "ascii"
		style, applied = pet.StyleASCII, pet.StyleASCII
	default:
		p, ok := pet.Get(entry)
		if !ok {
			return false
		}
		style, name, applied = pet.StyleASCII, p.Name, p.Name
	}
	m.PetStyle = style
	if name != "" {
		m.PetName = name
		// Show the pick immediately, not at the next rotation: without this
		// /pet would look like it did nothing for up to petRotateEvery.
		m.pet.shown = name
		m.pet.at = time.Now()
	}
	prefs := LoadPrefs(m.prefsPath)
	prefs.PetStyle = style
	prefs.Pet = m.PetName
	_ = SavePrefs(m.prefsPath, prefs)
	m.AddBlock(Block{Kind: "notice", Text: "pet → " + applied})
	m.Refresh()
	return true
}

// OpenPet shows the pet picker (/pet with no args). Enter confirms through
// confirmPet in src/builtin. Current is the entry that reflects the state
// actually on screen, so the ● marker lands on the right row: the classic
// look in classic style, otherwise the pinned animal.
func (m *Model) OpenPet() tea.Cmd {
	current := m.PetName
	if m.petClassic() {
		current = pet.StyleClassic
	}
	return func() tea.Msg {
		return PickerMsg{Kind: "pet", Options: pet.Entries(), Current: current}
	}
}
