package pet

// Sidebar ASCII pets — the drawn creatures, not the status state machine
// (that stays in pet.go). 25 entries: 18 drawn animals + 7 pixel sprites,
// each a name + two frames: the resting drawing and one movement beat (a
// blink, or a sway for the eyeless ones), so the sidebar pet is alive instead
// of a static sticker.
//
// The pixel sprites ("px-*" names) are 1-bit sprites drawn with half blocks:
// one cell is a pixel wide and two pixels tall, so a frame still holds
// ArtRows lines but twice the vertical resolution. Same table, same frames —
// nothing else in the package knows the difference.
//
// Every frame is normalized to ArtRows lines with blank lines padded on TOP,
// so switching pets never changes the sidebar block height: the bottom line
// of a drawing (its "ground") stays on the same row for all of them.
//
// Art lines are interpreted string literals on purpose: several drawings
// contain backticks (ground lines), so raw string literals cannot be used.
// Backslashes in the drawings are doubled.

import "strings"

// Pet is one named ASCII pet: Name doubles as the sidebar label, Frames is
// the drawing — frame 0 at rest, frame 1 the movement beat.
type Pet struct {
	Name   string
	Frames [][]string
}

// ArtRows is the fixed drawing height every frame is padded to.
const ArtRows = 4

// DefaultName is the pet used when nothing is chosen yet (or the saved name
// is unknown) — the most legible face of the set.
const DefaultName = "cat"

// Animation cadence, in beats of the sidebar tick loop. A two-frame pet
// rests on frame 0 and shows frame 1 for a single beat near the end of each
// cycle, so it blinks instead of strobing. A working pet runs the short
// cycle, which doubles the blink rate at the same beat length.
const (
	animCycle     = 8
	animCycleBusy = 4
	animBlinkFrom = 3 // the blink sits this many beats before the loop wraps
)

// pets is the drawing table, in the order /pet lists them.
var pets = []Pet{
	{"duck", [][]string{
		{"  ___", "<(· )__", "(  ·->", " `-´"},
		{"  ___", "<(- )__", "(  ·->", " `-´"},
	}},
	{"goose", [][]string{
		{" (:>", "  ||", " _(__)_", " ~~~~"},
		{" (->", "  ||", " _(__)_", " ~~~~"},
	}},
	{"blob", [][]string{
		{" .----.", "( :  : )", "(      )", " `----'"},
		{" .----.", "(-  -)", "(      )", " `----'"},
	}},
	{"cat", [][]string{
		{" /\\^/\\", " ( o.o )", " > ^ <"},
		{" /\\^/\\", " ( -.- )", " > ^ <"},
	}},
	{"dragon", [][]string{
		{" /\\^   /\\", "<  .  . >", "(  ~~  )", " `-vvvv-'"},
		{" /\\^   /\\", "<  -  - >", "(  ~~  )", " `-vvvv-'"},
	}},
	{"octopus", [][]string{
		{" .----.", "( :  : )", "(_____)", "/\\/\\/\\/\\"},
		{" .----.", "( -  - )", "(_____)", "/\\/\\/\\/\\"},
	}},
	{"owl", [][]string{
		{" /\\   /\\", "((·.)(·.))", "(  ><  )", " `----'"},
		{" /\\   /\\", "((-)(-))", "(  ><  )", " `----'"},
	}},
	{"penguin", [][]string{
		{" .----.", " (o::.)", " /(  )\\", "  `----'"},
		{" .----.", " (o--.)", " /(  )\\", "  `----'"},
	}},
	{"turtle", [][]string{
		{" _-'---`-_", "( :  : )", "/[_______]\\", " ``  ``"},
		{" _-'---`-_", "( -  - )", "/[_______]\\", " ``  ``"},
	}},
	{"snail", [][]string{
		{" .----.", " \\ (@)", " \\`--.-'", " ~~~~~~~"},
		{" .----.", " \\ (-)", " \\`--.-'", " ~~~~~~~"},
	}},
	{"ghost", [][]string{
		{" .----.", " / : : \\", " |     |", " `~^.~^~`"},
		{" .----.", " / : : \\", " |     |", " `~^~^~`"},
	}},
	{"axolotl", [][]string{
		{" }~{_______}~{", "}~( : .. : )~{", "(  .---.  )", "(_/     \\_)"},
		{" }~{_______}~{", "}~( - .. - )~{", "(  .---.  )", "(_/     \\_)"},
	}},
	{"capybara", [][]string{
		{" n_______n", "(  ·  ·  )", "(   oo   )", " `-------'"},
		{" n_______n", "(  -  -  )", "(   -   )", " `-------'"},
	}},
	{"cactus", [][]string{
		{" n       n", " |  ___  |", " | |·  ·| |", " |_|____|_|"},
		{"  n       n", "  |  ___  |", "  | |·  ·| |", "  |_|____|_|"},
	}},
	{"robot", [][]string{
		{" .[|||].", " [ ·  · ]", " [====]", " `-----'"},
		{" .[|||].", " [ -  - ]", " [====]", " `-----'"},
	}},
	{"rabbit", [][]string{
		{" (\\___/)", " ( ·  · )", "=(  ..  )=", " (\")__(\")"},
		{" (\\___/)", " ( -  - )", "=(  --  )=", " (\")__(\")"},
	}},
	{"mushroom", [][]string{
		{" .-0-00-0-.", "(__________)", " |·    ·|", " |_____|"},
		{" .-0-0-00-.", "(__________)", " |·    ·|", " |_____|"},
	}},
	{"chonk", [][]string{
		{" /\\   /\\", "( ·   · )", "(  ..  )", " `------'"},
		{" /\\   /\\", "( -   - )", "(  --  )", " `------'"},
	}},
	{"px-cat", [][]string{
		{"  ▄█    █▄", "  █▀█▀▀█▀█", "  ▀█▀▀▀▀█▀", "  █▀▀██▀▀█"},
		{"  ▄█    █▄", "  ████████", "  ▀█▀▀▀▀█▀", "  █▀▀██▀▀█"},
	}},
	{"px-frog", [][]string{
		{"  ▄▄    ▄▄", " █▀▄    ▄▀█", " █▀██████▀█", " ▀▀▀▀▀▀▀▀▀▀"},
		{"  ▄▄    ▄▄", " █▀      ▀█", " █▀██████▀█", " ▀▀▀▀▀▀▀▀▀▀"},
	}},
	{"px-bee", [][]string{
		{" ▄▄      ▄▄", "█  █ ██ █  █", "█  █▀██▀█  █", "▀▄▄▀ ▀▀ ▀▄▄▀"},
		{" ▄  ▄  ▄  ▄", "█  █ ██ █  █", "█  █▀██▀█  █", "▀▄ ▀▄▀▀▄▀ ▄▀"},
	}},
	// px-crab: GitHub's crab, traced off the mascot sprite (a lookalike, not
	// the logo itself). px-capybara: the charm.sh capybara, minus its
	// sparkles and status line.
	{"px-crab", [][]string{
		{"  ██        ██", "   █▄▄▄▄▄▄▄▄█", "  ███▄▄██▄▄███", " ▄█▀  ▀██▀  ▀█▄"},
		{"  ██        ██", "   █▄▄▄▄▄▄▄▄█", "  ████████████", " ▄█▀  ▀██▀  ▀█▄"},
	}},
	{"px-capybara", [][]string{
		{"     ▄███▄", " ▄████████████▄", " █▄████████████", "  ██  ██  ██"},
		{"     ▄███▄", " ▄████████████▄", " ██████████████", "  ██  ██  ██"},
	}},
	{"px-slime", [][]string{
		{"  ▄██████▄  ", " ███▄▄▄▄███ ", " ██████████ ", " ▀███▀▀███▀ "},
		{"  ▄██████▄  ", " █▄██▄▄██▄█ ", " ██████████ ", " ▀███▀▀███▀ "},
	}},
	{"px-ghost", [][]string{
		{" ▄████████▄ ", " ██▄████▄██ ", " ██████████ ", " ██▀██▀██▀█▄"},
		{" ▄████████▄ ", " ██████████ ", " ██████████ ", " ██▀██▀██▀█▄"},
	}},
}

func init() {
	for i := range pets {
		for f, frame := range pets[i].Frames {
			pets[i].Frames[f] = normalize(frame)
		}
	}
}

// normalize trims trailing blanks and pads on TOP so every frame is
// ArtRows tall and bottom-aligned.
func normalize(lines []string) []string {
	out := make([]string, ArtRows)
	keep := lines
	if len(keep) > ArtRows {
		keep = keep[len(keep)-ArtRows:] // never grow: clip the top
	}
	off := ArtRows - len(keep)
	for i, ln := range keep {
		out[off+i] = strings.TrimRight(ln, " \t")
	}
	return out
}

// Width is the widest line of the frame (0 for a pet without art) — the
// sidebar pads the name and status label into a column past the drawing, so
// both stay put while the frames swap.
func Width(lines []string) int {
	w := 0
	for _, ln := range lines {
		if n := len([]rune(ln)); n > w {
			w = n
		}
	}
	return w
}

// FrameFor returns the drawing to show at tick. A two-frame pet shows frame
// 1 for a single beat per cycle (the blink) and rests on frame 0 the rest of
// the time; busy halves the cycle so a working pet moves more often.
func (p Pet) FrameFor(tick int, busy bool) []string {
	if len(p.Frames) == 0 {
		return nil
	}
	if len(p.Frames) == 1 {
		return p.Frames[0]
	}
	cycle := animCycle
	if busy {
		cycle = animCycleBusy
	}
	beat := ((tick % cycle) + cycle) % cycle
	if beat == cycle-animBlinkFrom {
		return p.Frames[1]
	}
	return p.Frames[0]
}

// Pets returns the whole set (copies — the table stays private).
func Pets() []Pet {
	out := make([]Pet, len(pets))
	copy(out, pets)
	return out
}

// Names lists the pets in table order, for the /pet picker and for rotation.
func Names() []string {
	out := make([]string, 0, len(pets))
	for _, p := range pets {
		out = append(out, p.Name)
	}
	return out
}

// Entries lists everything /pet can pick: every animal, then the two looks
// (ascii / classic). The look entries are not animals — they switch the
// sidebar style — so the picker must render them differently from the rest.
func Entries() []string {
	out := Names()
	return append(out, StyleASCII, StyleClassic)
}

// Demo is the preview a picker shows for an entry: an animal's rest frame
// (never an animated one — a grid/list of demos must not shimmer), or a
// sampler of the classic faces. "ascii" has no demo of its own: the caller
// resolves it to the animal currently pinned, so it returns nil.
func Demo(entry string) []string {
	if IsClassic(entry) {
		return []string{
			ClassicFace(Idle, 0),
			ClassicFace(Working, 0),
			ClassicFace(Success, 0),
		}
	}
	if IsStyle(entry) {
		return nil
	}
	p, ok := Get(entry)
	if !ok || len(p.Frames) == 0 {
		return nil
	}
	return p.Frames[0]
}

// Get looks a pet up by name, case- and space-insensitive. ok is false for
// an empty or unknown name — callers that need a fallback use Resolve.
func Get(name string) (Pet, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Pet{}, false
	}
	for _, p := range pets {
		if p.Name == n {
			return p, true
		}
	}
	return Pet{}, false
}

// Resolve is Get with the default pet as fallback (saved-name drift, an
// empty pref, a hand-edited prefs.json).
func Resolve(name string) Pet {
	if p, ok := Get(name); ok {
		return p
	}
	for _, p := range pets {
		if p.Name == DefaultName {
			return p
		}
	}
	return pets[0]
}

// Rotate returns the pet step places after name in table order, wrapping —
// the sidebar pet's turn among the set. An unknown name starts at the
// default pet, so a hand-edited pref can never stall the rotation.
func Rotate(name string, step int) string {
	names := Names()
	start, found := 0, false
	for i, n := range names {
		if n == name {
			start, found = i, true
			break
		}
	}
	if !found {
		for i, n := range names {
			if n == DefaultName {
				start = i
				break
			}
		}
	}
	step = ((step % len(names)) + len(names)) % len(names)
	return names[(start+step)%len(names)]
}
