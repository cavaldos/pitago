package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefs(t *testing.T) {
	got := Refs(`check @shot.png and @"my dir/pic.jpg" please`)
	if len(got) != 2 || got[0] != "shot.png" || got[1] != "my dir/pic.jpg" {
		t.Fatalf("refs = %q", got)
	}
	if got := Refs("mail me at a@b.com ok"); len(got) != 0 {
		t.Fatalf("email leaked: %q", got)
	}
	if got := Refs("@a.png @a.png"); len(got) != 1 {
		t.Fatalf("dedupe failed: %q", got)
	}
}

func TestIsImageName(t *testing.T) {
	for _, n := range []string{"a.png", "a.JPG", "a.jpeg", "a.gif", "a.webp", `"sp ace.png"`} {
		if !IsImageName(n) {
			t.Fatalf("%s should be image", n)
		}
	}
	for _, n := range []string{"a.bmp", "a.txt", "a", "a.svg"} {
		if IsImageName(n) {
			t.Fatalf("%s should not be image", n)
		}
	}
}

func TestMimeOf(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0}
	if MimeOf(png) != "image/png" {
		t.Fatal("png")
	}
	if MimeOf([]byte{0xff, 0xd8, 0xff, 0xe0}) != "image/jpeg" {
		t.Fatal("jpeg")
	}
	if MimeOf([]byte("GIF89a...")) != "image/gif" {
		t.Fatal("gif")
	}
	webp := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}
	if MimeOf(webp) != "image/webp" {
		t.Fatal("webp")
	}
	if MimeOf([]byte("hello")) != "" {
		t.Fatal("garbage should be empty")
	}
}

func TestUnescapeDequote(t *testing.T) {
	if got := Unescape(`Copied\ Screenshots/a.png`); got != "Copied Screenshots/a.png" {
		t.Fatalf("unescape = %q", got)
	}
	if got := Dequote(`"/a/b c.png"`); got != "/a/b c.png" {
		t.Fatalf("dequote = %q", got)
	}
}

func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, png, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanPaths(t *testing.T) {
	dir := t.TempDir()
	shot := writePNG(t, dir, "shot.png")
	// escaped absolute drop, like Finder/terminal bracketed paste
	dropped := strings.ReplaceAll(shot, " ", `\ `)
	if got := ScanPaths("look "+dropped+" ok", dir); len(got) != 1 || got[0] != shot {
		t.Fatalf("scan drop = %q", got)
	}
	// quoted drop with spaces
	sp := filepath.Join(dir, "my pic.png")
	writePNG(t, dir, "my pic.png")
	_ = sp
	if got := ScanPaths(`see "`+filepath.Join(dir, "my pic.png")+`"`, dir); len(got) != 1 {
		t.Fatalf("scan quoted = %q", got)
	}
	// prose and missing files never match; @refs belong to Refs
	if got := ScanPaths("see a.png and @shot.png", dir); len(got) != 0 {
		t.Fatalf("scan false positive = %q", got)
	}
	// non-images never match even when real
	note := filepath.Join(dir, "note.txt")
	_ = os.WriteFile(note, []byte("hi"), 0o644)
	if got := ScanPaths("read "+note, dir); len(got) != 0 {
		t.Fatalf("scan text file = %q", got)
	}
}

func TestStripRefs(t *testing.T) {
	dir := t.TempDir()
	shot := writePNG(t, dir, "shot.png")
	esc := strings.ReplaceAll(shot, " ", `\ `)
	got := StripRefs("look "+esc+" ok @shot.png", []string{shot})
	if strings.Contains(got, esc) || !strings.Contains(got, "@shot.png") {
		t.Fatalf("strip = %q", got)
	}
}

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	atts, notes, over := Extract(NewLoader(dir), "look @shot.png and @note.txt")
	if len(notes) != 0 || len(over) != 0 {
		t.Fatalf("unexpected notices: %q %q", notes, over)
	}
	if len(atts) != 1 || atts[0].Mime != "image/png" || atts[0].Name != "shot.png" {
		t.Fatalf("atts = %+v", atts)
	}
	// missing + oversize/unsupported fall back to notices, never fatal
	atts, notes, _ = Extract(NewLoader(dir), "@missing.png @note.txt")
	if len(atts) != 0 || len(notes) == 0 {
		t.Fatalf("want fallback notice, got %v %q", atts, notes)
	}
	if !strings.Contains(notes[0], "missing") {
		t.Fatalf("notice = %q", notes)
	}
}

func TestImageRefs(t *testing.T) {
	got := ImageRefs("a @x.png b @note.txt c @y.jpg")
	if len(got) != 2 || got[0] != "x.png" || got[1] != "y.jpg" {
		t.Fatalf("image refs = %q", got)
	}
}

// One Loader per message: refs fed from several sources share the dedupe
// set and the cap, and come back in call order.
func TestLoaderSharedDedupeAndCap(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	names := []string{"shot.png", "a.png", "b.png", "c.png", "d.png", "e.png", "f.png"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), png, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l := NewLoader(dir)
	// same file, three spellings → one attachment
	atts, notes, _ := l.Load([]string{"shot.png", "./shot.png", filepath.Join(dir, "shot.png")})
	if len(notes) != 0 || len(atts) != 1 {
		t.Fatalf("dedupe failed: %d atts %q", len(atts), notes)
	}
	more, notes, over := l.Load(names[1:])
	if len(notes) != 0 {
		t.Fatalf("notes = %q", notes)
	}
	if len(more) != MaxCount-1 {
		t.Fatalf("got %d, want %d", len(more), MaxCount-1)
	}
	if len(over) != 2 || over[0] != "e.png" || over[1] != "f.png" {
		t.Fatalf("overflow = %q", over)
	}
	// the cap is spent: a later source adds nothing
	after, _, over2 := l.Load([]string{"a.png"})
	if len(after) != 0 || len(over2) != 0 {
		t.Fatalf("after cap: %d atts %q", len(after), over2)
	}
}

// A fresh Loader per message: the cap is per message, not per session.
func TestLoaderCapIsPerMessage(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake")...)
	_ = os.WriteFile(filepath.Join(dir, "a.png"), png, 0o644)
	if _, _, over := NewLoader(dir).Load([]string{"a.png"}); len(over) != 0 {
		t.Fatalf("over = %q", over)
	}
	atts, _, over := NewLoader(dir).Load([]string{"a.png"})
	if len(atts) != 1 || len(over) != 0 {
		t.Fatalf("second message blocked by the first: %+v %q", atts, over)
	}
}
