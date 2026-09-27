package pet

import "testing"

func TestPetTableIsNormalized(t *testing.T) {
	names := map[string]bool{}
	for _, p := range Pets() {
		if p.Name == "" {
			t.Fatal("pet without a name")
		}
		if names[p.Name] {
			t.Fatalf("duplicate pet name %q", p.Name)
		}
		names[p.Name] = true
		if len(p.Frames) < 2 {
			t.Fatalf("%s needs a rest frame and a movement frame", p.Name)
		}
		for f, frame := range p.Frames {
			if len(frame) != ArtRows {
				t.Fatalf("%s frame %d has %d rows, want %d", p.Name, f, len(frame), ArtRows)
			}
			for i, ln := range frame {
				if Width([]string{ln}) != len([]rune(ln)) {
					t.Fatalf("%s frame %d row %d has trailing blanks", p.Name, f, i)
				}
			}
		}
		// The ground row (last line) is where the status label lands, so a
		// drawing padded on top must keep a real bottom line.
		if frame := p.Frames[0]; frame[ArtRows-1] == "" {
			t.Fatalf("%s lost its ground row", p.Name)
		}
	}
}

func TestFrameForBlinksOnce(t *testing.T) {
	p, ok := Get("cat")
	if !ok {
		t.Fatal("cat missing from the set")
	}
	rest, move := p.Frames[0], p.Frames[1]
	// One full idle cycle: exactly one movement beat, never two in a row.
	moves := 0
	for tick := 0; tick < animCycle; tick++ {
		if sameFrame(p.FrameFor(tick, false), move) {
			moves++
			if sameFrame(p.FrameFor(tick+1, false), move) {
				t.Fatal("movement frame must be a single beat, not a hold")
			}
		} else if !sameFrame(p.FrameFor(tick, false), rest) {
			t.Fatal("a two-frame pet must only ever show frame 0 or 1")
		}
	}
	if moves != 1 {
		t.Fatalf("idle cycle blinks %d times, want 1", moves)
	}
	// Busy halves the cycle, so twice the beats over the same span.
	moves = 0
	for tick := 0; tick < animCycle; tick++ {
		if sameFrame(p.FrameFor(tick, true), move) {
			moves++
		}
	}
	if moves != 2 {
		t.Fatalf("busy cycle blinks %d times, want 2", moves)
	}
}

// A pet without a movement frame must still render (no index panic).
func TestFrameForSingleFrame(t *testing.T) {
	p := Pet{Name: "x", Frames: [][]string{pets[3].Frames[0]}}
	if got := p.FrameFor(99, true); len(got) != ArtRows {
		t.Fatalf("single-frame pet frame = %d rows, want %d", len(got), ArtRows)
	}
	if got := (Pet{Name: "empty"}).FrameFor(1, false); got != nil {
		t.Fatal("a pet without art must render nothing, not panic")
	}
}

func sameFrame(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveAndRotate(t *testing.T) {
	if got := Resolve(" DRAGON "); got.Name != "dragon" {
		t.Fatalf("case/space-insensitive resolve: %q", got.Name)
	}
	if got := Resolve("nope").Name; got != DefaultName {
		t.Fatalf("unknown name must fall back to %q, got %q", DefaultName, got)
	}
	if p, ok := Get(""); ok || p.Name != "" {
		t.Fatal("empty name must not resolve")
	}

	// Rotation walks the whole set once per lap and wraps, from any start.
	names := Names()
	for start := range names {
		seen := map[string]bool{}
		name := names[start]
		for i := 0; i < len(names); i++ {
			if seen[name] {
				t.Fatalf("rotation from %q repeated %q after %d steps", names[start], name, i)
			}
			seen[name] = true
			name = Rotate(name, 1)
		}
		if name != names[start] {
			t.Fatalf("rotation from %q ended on %q, want a full lap back", names[start], name)
		}
		if !seen[names[start]] {
			t.Fatalf("rotation from %q skipped its own start", names[start])
		}
	}
	// Negative and oversized steps wrap the other way / around the lap.
	if got := Rotate("cat", -1); got != Rotate("cat", len(names)-1) {
		t.Fatalf("backward step = %q, want %q", got, Rotate("cat", len(names)-1))
	}
	// An unknown name must not stall the rotation.
	if got := Rotate("nope", 1); got != Rotate(DefaultName, 1) {
		t.Fatalf("rotation from an unknown name = %q, want %q", got, Rotate(DefaultName, 1))
	}
}

func TestNamesMatchesPets(t *testing.T) {
	ps, names := Pets(), Names()
	if len(ps) != len(names) {
		t.Fatalf("Pets()=%d Names()=%d", len(ps), len(names))
	}
	for i := range ps {
		if ps[i].Name != names[i] {
			t.Fatalf("row %d: %q vs %q", i, ps[i].Name, names[i])
		}
	}
}
