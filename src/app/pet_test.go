package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pitago/src/components/pet"
	"pitago/src/pirpc"
)

func TestPetSetNoop(t *testing.T) {
	var m Model
	m.pet.inTurn = true
	if cmd := m.petSet(petThinking); cmd == nil {
		t.Fatal("first transition must schedule tick cmd")
	}
	gen := m.pet.gen
	if cmd := m.petSet(petThinking); cmd != nil {
		t.Fatal("same-status repeat must be no-op (timer must not restart)")
	}
	if m.pet.gen != gen {
		t.Fatal("gen must not advance on no-op")
	}
	if m.pet.since.IsZero() || time.Since(m.pet.since) > time.Second {
		t.Fatal("busy transition must stamp since=now")
	}
}

func TestPetAnchorSettled(t *testing.T) {
	var m Model
	if cmd := m.petAnchor(); cmd == nil {
		t.Fatal("anchor from idle must go working")
	}
	if m.pet.status != petWorking || !m.pet.inTurn {
		t.Fatalf("anchor: %+v", m.pet)
	}
	// re-issued starts between rounds must not override thinking
	m.petSet(petThinking)
	if cmd := m.petAnchor(); cmd != nil {
		t.Fatal("anchor must not override thinking")
	}
	if m.pet.status != petThinking {
		t.Fatalf("anchor overrode thinking: %v", m.pet.status)
	}
	// settled without done(stop) — e.g. Esc-abort — decays quietly
	m.pet.sawStop = false
	if cmd := m.petSettled(); cmd != nil {
		t.Fatal("abort settle must go idle without cmds")
	}
	if m.pet.status != petIdle || m.pet.inTurn {
		t.Fatalf("abort settle: %+v", m.pet)
	}
	// settled after done(stop) flashes Done
	m.petAnchor()
	m.pet.sawStop = true
	if cmd := m.petSettled(); cmd == nil {
		t.Fatal("stop settle must schedule flash timer")
	}
	if m.pet.status != petSuccess {
		t.Fatalf("stop settle: %v", m.pet.status)
	}
	// flash must not be cut by a trailing settle
	if cmd := m.petSettled(); cmd != nil {
		t.Fatal("settle during flash must be ignored")
	}
}

func TestPetFlashGen(t *testing.T) {
	var m Model
	m.pet.inTurn = true
	m.petSet(petSuccess)
	gen := m.pet.gen
	updated, _ := m.Update(petFlashMsg{gen: gen - 1})
	m = updated.(Model)
	if m.pet.status != petSuccess {
		t.Fatal("stale flash timer must not cut a new status")
	}
	updated, _ = m.Update(petFlashMsg{gen: gen})
	m = updated.(Model)
	if m.pet.status != petIdle {
		t.Fatalf("current-gen flash must decay: %v", m.pet.status)
	}
}

func TestPetDeltas(t *testing.T) {
	var m Model
	m.tools = make(map[string]int)
	m.curAsst, m.curThink = -1, -1
	m.pet.inTurn = true
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"thinking_delta","delta":"hmm"}}`))
	if m.pet.status != petThinking {
		t.Fatalf("thinking_delta: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`))
	if m.pet.status != petWriting {
		t.Fatalf("text_delta: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"done","reason":"toolUse"}}`))
	if m.pet.status != petWriting {
		t.Fatalf("done(toolUse) must not change status: %v", m.pet.status)
	}
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"done","reason":"stop"}}`))
	if m.pet.status != petSuccess || !m.pet.sawStop {
		t.Fatalf("done(stop): %+v", m.pet)
	}
	// deltas outside a turn never pollute the status
	var m2 Model
	m2.tools = make(map[string]int)
	m2.curAsst, m2.curThink = -1, -1
	m2.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`))
	if m2.petLabel() != "Ready" || m2.petShown().Name == "" {
		t.Fatalf("out-of-turn delta must read as idle: %q %q", m2.petLabel(), m2.petShown().Name)
	}
}

func TestPetError(t *testing.T) {
	var m Model
	m.tools = make(map[string]int)
	m.curAsst, m.curThink = -1, -1
	m.pet.inTurn = true
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"error"}}`))
	if m.pet.status != petError || m.pet.inTurn {
		t.Fatalf("stream error: %+v", m.pet)
	}
	var m2 Model
	m2.applyMessageEnd([]byte(`{"message":{"role":"assistant","stopReason":"error","errorMessage":"boom"}}`))
	if m2.pet.status != petError {
		t.Fatalf("message_end error: %v", m2.pet.status)
	}
}

func TestPetLabel(t *testing.T) {
	var m Model
	if got := m.petLabel(); got != "Ready" {
		t.Fatalf("idle label: %q", got)
	}
	m.pet.status = petWorking
	m.pet.since = time.Now().Add(-7 * time.Second)
	if got := m.petLabel(); got != "Working... 7s" {
		t.Fatalf("timed label: %q", got)
	}
	// Every pet in the art table must render: non-empty frame, and a block
	// exactly petRows tall (the recentAt click-mapping budget).
	for _, p := range pet.Pets() {
		m.pet.status = petIdle
		m.pet.since = time.Time{}
		m.PetName = p.Name
		art := m.petShown()
		if strings.TrimSpace(art.Name) == "" {
			t.Fatalf("pet %q resolved empty", p.Name)
		}
		// Shorter drawings are padded on TOP, so the ground (last) line is
		// the only row every pet must have.
		frame := art.FrameFor(0, false)
		if strings.TrimSpace(frame[len(frame)-1]) == "" {
			t.Fatalf("pet %q: blank ground line", p.Name)
		}
		block := m.renderPet(40)
		rows := strings.Split(strings.TrimRight(block, "\n"), "\n")
		if len(rows) != m.petRows() {
			t.Fatalf("pet %q rendered %d rows, want petRows()=%d", p.Name, len(rows), m.petRows())
		}
		if !strings.Contains(block, "Ready") {
			t.Fatalf("pet %q: status label missing from the block", p.Name)
		}
		// Name shares a row with the art's first INKED line, label the ground
		// row — that is what keeps the block down to petRows rows. A blank
		// row must never carry the name: that would be the title row back.
		nameRow := petNameRow(frame)
		if strings.TrimSpace(frame[nameRow]) == "" {
			t.Fatalf("pet %q: name row %d is blank", p.Name, nameRow)
		}
		if nameRow == pet.ArtRows-1 {
			t.Fatalf("pet %q: name and label would share the ground row", p.Name)
		}
		if !strings.Contains(stripANSI(rows[nameRow]), p.Name) {
			t.Fatalf("pet %q: name not on art row %d: %q", p.Name, nameRow, rows[nameRow])
		}
		for i, row := range rows[:pet.ArtRows] {
			if i == nameRow {
				continue
			}
			if strings.Contains(stripANSI(row), p.Name) {
				t.Fatalf("pet %q: name leaked onto row %d: %q", p.Name, i, row)
			}
		}
		if !strings.Contains(stripANSI(rows[pet.ArtRows-1]), "Ready") {
			t.Fatalf("pet %q: label not on the ground row: %q", p.Name, rows[pet.ArtRows-1])
		}
	}
}

// stripANSI removes escape sequences so a rendered row can be compared as
// plain text (the labels are styled, the art is tinted).

// The art table must be uniform: petRows is derived from ArtRows, so a pet
// with a different line count would silently break the click mapping.
func TestPetArtTableUniform(t *testing.T) {
	all := pet.Pets()
	if len(all) == 0 {
		t.Fatal("art table is empty")
	}
	for _, p := range all {
		if len(p.Frames) == 0 {
			t.Fatalf("pet %q has no frames", p.Name)
		}
		for i, f := range p.Frames {
			if len(f) != pet.ArtRows {
				t.Fatalf("pet %q frame %d has %d lines, want ArtRows=%d", p.Name, i, len(f), pet.ArtRows)
			}
		}
		if strings.TrimSpace(p.Name) == "" {
			t.Fatal("pet with empty name")
		}
	}
	// Names are the /pet option list: unique, and default resolvable.
	seen := map[string]bool{}
	for _, n := range pet.Names() {
		if seen[n] {
			t.Fatalf("duplicate pet name %q", n)
		}
		seen[n] = true
	}
	if got := pet.Resolve("nope").Name; got != pet.DefaultName {
		t.Fatalf("unknown name must fall back to the default, got %q", got)
	}
}

// SetPet is the single entry point for both kinds of pick: an animal
// (ascii + that anchor), or a look (style only, anchor kept). Unknown
// entries are strict no-ops.
func TestSetPet(t *testing.T) {
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	if !m.SetPet(" Dragon ") {
		t.Fatal("SetPet must accept a case/space-different name")
	}
	if m.PetName != "dragon" {
		t.Fatalf("PetName = %q", m.PetName)
	}
	// The pick must show at once — not up to petRotateEvery later.
	if m.pet.shown != "dragon" {
		t.Fatalf("SetPet must show the pick immediately, shown = %q", m.pet.shown)
	}
	if time.Since(m.pet.at) > time.Second {
		t.Fatalf("SetPet must stamp the rotation clock, at = %v", m.pet.at)
	}
	prefs := LoadPrefs(m.prefsPath)
	if prefs.Pet != "dragon" {
		t.Fatalf("persisted pet = %q", prefs.Pet)
	}
	if prefs.PetStyle != pet.StyleASCII {
		t.Fatalf("an animal pick must persist the ascii look, got %q", prefs.PetStyle)
	}
	// A look keeps the anchor: classic now, back to the same animal after.
	if !m.SetPet("classic") {
		t.Fatal("\"classic\" must be a valid entry")
	}
	if m.PetStyle != pet.StyleClassic || m.PetName != "dragon" {
		t.Fatalf("classic must keep the anchor: style=%q pet=%q", m.PetStyle, m.PetName)
	}
	if got := LoadPrefs(m.prefsPath).PetStyle; got != pet.StyleClassic {
		t.Fatalf("persisted petStyle = %q", got)
	}
	if !m.SetPet("ascii") {
		t.Fatal("\"ascii\" must be a valid entry")
	}
	if m.PetStyle != pet.StyleASCII || m.PetName != "dragon" {
		t.Fatalf("ascii must keep the anchor: style=%q pet=%q", m.PetStyle, m.PetName)
	}
	// An animal pick while classic is active switches back to ascii.
	m.SetPet("classic")
	if !m.SetPet("owl") {
		t.Fatal("owl must be a valid entry")
	}
	if m.PetStyle != pet.StyleASCII || m.PetName != "owl" {
		t.Fatalf("an animal must re-anchor in ascii: style=%q pet=%q", m.PetStyle, m.PetName)
	}
	// An empty/saved-unknown pref resolves to the default drawing, so the
	// sidebar block can never come up blank.
	if got := pet.Resolve("").Name; got != pet.DefaultName {
		t.Fatalf("default pet = %q", got)
	}
	before := LoadPrefs(m.prefsPath)
	if m.SetPet("nosuchpet") {
		t.Fatal("unknown pet must be refused")
	}
	if m.PetName != "owl" {
		t.Fatalf("refused SetPet must not touch the Model: %q", m.PetName)
	}
	after := LoadPrefs(m.prefsPath)
	if after.Pet != before.Pet || after.PetStyle != before.PetStyle {
		t.Fatalf("refused SetPet must not touch prefs: %+v", after)
	}
}

// A settle swallowed behind an open dialog must not stick the pet: the
// clock keeps ticking behind dialogs, and closing one re-checks get_state.
func TestPetTickPassesDialog(t *testing.T) {
	m := New(nil, t.TempDir())
	m.pet.status = petWorking
	m.pet.since = time.Now()
	m.Dialogs = []*Dialog{{Kind: "palette"}}
	um, _ := m.Update(petTickMsg{})
	m2 := um.(Model)
	if m2.pet.tick != 1 {
		t.Fatalf("tick behind dialog should advance, got %d", m2.pet.tick)
	}
	if len(m2.Dialogs) != 1 {
		t.Fatal("tick must not disturb the dialog")
	}
}

// The loop must outlive the turn: a visible pet block keeps animating and
// rotating with pi idle, and hiding the section drops the last reason to tick
// so the timer stops instead of burning repaints forever.
func TestPetTickLoopsWhileVisible(t *testing.T) {
	m := New(nil, t.TempDir())
	if cmd := m.ensurePetTick(); cmd == nil {
		t.Fatal("a visible pet section must arm the loop")
	}
	if !m.pet.ticking {
		t.Fatal("armPetTick must latch pet.ticking")
	}
	um, cmd := m.Update(petTickMsg{})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("idle tick with the pet section visible must re-arm")
	}
	if m.pet.tick != 1 || !m.pet.ticking {
		t.Fatalf("idle tick must advance the animation: %+v", m.pet)
	}
	if got := m.petInterval(); got != petTickIdle {
		t.Fatalf("idle cadence = %v, want %v", got, petTickIdle)
	}
	// Hiding the section is the last reason to loop: the tick stops and the
	// latch clears, so a later re-show can re-arm cleanly.
	m.Side = map[string]bool{SidePet: false}
	um, cmd = m.Update(petTickMsg{})
	m = um.(Model)
	if cmd != nil {
		t.Fatal("hidden pet section must stop the loop")
	}
	if m.pet.ticking {
		t.Fatal("stopped loop must clear pet.ticking")
	}
	if cmd := m.ensurePetTick(); cmd != nil {
		t.Fatal("hidden pet section must not re-arm")
	}
	// Re-showing the section restarts the animation.
	m.Side = map[string]bool{SidePet: true}
	if cmd := m.ensurePetTick(); cmd == nil {
		t.Fatal("re-showing the pet section must re-arm the loop")
	}
}

// Rotation hands the sidebar to the next pet in the set, one place per
// petRotateEvery — and never twice in the same tick.
func TestPetRotateOnTick(t *testing.T) {
	m := New(nil, t.TempDir())
	m.pet.shown = "duck"
	m.pet.at = time.Now()
	um, _ := m.Update(petTickMsg{})
	m = um.(Model)
	if m.pet.shown != "duck" {
		t.Fatalf("rotation must wait for its turn, shown = %q", m.pet.shown)
	}
	m.pet.at = time.Now().Add(-petRotateEvery)
	um, _ = m.Update(petTickMsg{})
	m = um.(Model)
	if want := pet.Rotate("duck", 1); m.pet.shown != want {
		t.Fatalf("shown = %q, want %q", m.pet.shown, want)
	}
	if time.Since(m.pet.at) > time.Second {
		t.Fatalf("rotation must restamp the clock, at = %v", m.pet.at)
	}
	// A second tick in the same instant must not skip another pet.
	um, _ = m.Update(petTickMsg{})
	m = um.(Model)
	if m.pet.shown != pet.Rotate("duck", 1) {
		t.Fatalf("rotation must not run twice in one tick: %q", m.pet.shown)
	}
	// A zero Model (as tests build it) must not divide by a zero duration.
	var z Model
	z.Side = map[string]bool{SidePet: true}
	uz, _ := z.Update(petTickMsg{})
	z = uz.(Model)
	if z.pet.shown == "" {
		t.Fatal("a zero Model must lazily seed the shown pet")
	}
}

// Rotation is ascii-only: in the classic look the tick still runs (the faces
// animate) but the animals do not wander.
func TestPetRotationAsciiOnly(t *testing.T) {
	m := New(nil, t.TempDir())
	m.pet.shown = "duck"
	m.pet.at = time.Now().Add(-petRotateEvery)
	um, _ := m.Update(petTickMsg{})
	m = um.(Model)
	if want := pet.Rotate("duck", 1); m.pet.shown != want {
		t.Fatalf("ascii must rotate: shown = %q, want %q", m.pet.shown, want)
	}
	m2 := New(nil, t.TempDir())
	m2.PetStyle = pet.StyleClassic
	m2.pet.shown = "duck"
	m2.pet.at = time.Now().Add(-petRotateEvery)
	um, _ = m2.Update(petTickMsg{})
	m2 = um.(Model)
	if m2.pet.shown != "duck" {
		t.Fatalf("classic must not rotate, shown = %q", m2.pet.shown)
	}
	if m2.pet.tick == 0 {
		t.Fatal("classic tick must still run (the faces animate)")
	}
}

// The rendered block height must stay in lockstep with m.petRows() — the
// recentAt click mapping is derived from it, in BOTH looks.
func TestPetBlockHeightMatchesPetRows(t *testing.T) {
	m := New(nil, t.TempDir())
	m.PetStyle = pet.StyleASCII
	if got := m.petRows(); got != pet.ArtRows+1 {
		t.Fatalf("ascii petRows = %d, want %d", got, pet.ArtRows+1)
	}
	for i, name := range pet.Names() {
		m.pet.shown = name
		m.pet.tick = i
		m.pet.status = []petStatus{petIdle, petThinking, petSuccess, petError}[i%4]
		rows := strings.Split(strings.TrimRight(m.renderPet(40), "\n"), "\n")
		if len(rows) != m.petRows() {
			t.Fatalf("pet %q rendered %d rows, want petRows()=%d", name, len(rows), m.petRows())
		}
	}
	// Classic: the compact two-row block, for every status.
	m.PetStyle = pet.StyleClassic
	if got := m.petRows(); got != 2 {
		t.Fatalf("classic petRows = %d, want 2", got)
	}
	for _, st := range []petStatus{petIdle, petThinking, petWorking, petSuccess, petError} {
		m.pet.status = st
		block := m.renderPet(40)
		rows := strings.Split(strings.TrimRight(block, "\n"), "\n")
		if len(rows) != 2 {
			t.Fatalf("classic %v rendered %d rows, want 2: %q", st, len(rows), block)
		}
	}
}

// The classic look is the original compact row: the animated face and the
// status label on ONE line, no "PET" title, no name — and the face must
// change with the tick while the per-status color path still applies.
func TestPetClassicBlock(t *testing.T) {
	m := New(nil, t.TempDir())
	m.PetStyle = pet.StyleClassic
	m.pet.status = petWorking
	m.pet.since = time.Now().Add(-7 * time.Second)
	first := m.renderPet(40)
	lines := strings.Split(strings.TrimRight(first, "\n"), "\n")
	face := pet.ClassicFace(petWorking, 0)
	if !strings.Contains(stripANSI(lines[0]), face+" "+m.petLabel()) {
		t.Fatalf("classic row = %q, want face %q + label %q", lines[0], face, m.petLabel())
	}
	if strings.Contains(stripANSI(first), "PET") {
		t.Fatalf("classic block must not print a PET title: %q", first)
	}
	// The face animates: another tick shows another frame.
	m.pet.tick = 1
	if got := m.renderPet(40); stripANSI(got) == stripANSI(first) {
		t.Fatal("classic face must change with the tick")
	}
	// Per-status colors survive the classic look.
	m.pet.tick = 0
	m.pet.status = petError
	if fg := m.petArtStyle().GetForeground(); fg != errStyle.GetForeground() {
		t.Fatalf("error face color = %v, want %v", fg, errStyle.GetForeground())
	}
	m.pet.status = petSuccess
	if fg := m.petArtStyle().GetForeground(); fg != okStyle.GetForeground() {
		t.Fatalf("success face color = %v, want %v", fg, okStyle.GetForeground())
	}
	m.pet.status = petThinking
	if fg := m.petArtStyle().GetForeground(); fg != cText {
		t.Fatalf("busy face color = %v, want %v", fg, cText)
	}
}

func TestStateRefreshSettlesMissedTurn(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = true
	m.Status = "pi is running…"
	m.pet.status = petWorking
	m.pet.inTurn = true
	um, _ := m.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x.jsonl"}})
	m2 := um.(Model)
	if m2.thinking || m2.Status != "ready" {
		t.Fatalf("idle pi should settle a stuck turn, thinking=%v status=%q", m2.thinking, m2.Status)
	}
	if m2.pet.status != petIdle || m2.pet.inTurn {
		t.Fatalf("pet should decay to idle: %+v", m2.pet)
	}
	// A running turn must not be disturbed.
	m3 := New(nil, t.TempDir())
	m3.thinking = true
	m3.pet.status = petWorking
	um, _ = m3.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x.jsonl", IsStreaming: true}})
	m4 := um.(Model)
	if !m4.thinking || m4.pet.status != petWorking {
		t.Fatal("streaming turn must stay working")
	}
}

func TestReconcileTurnCmdQuietWhenIdle(t *testing.T) {
	var m Model
	if m.ReconcileTurnCmd() != nil {
		t.Fatal("idle must not fetch")
	}
	m.thinking = true
	if m.ReconcileTurnCmd() != nil {
		t.Fatal("disconnected must not fetch")
	}
}
