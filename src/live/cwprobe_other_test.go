//go:build !linux

package live

import "testing"

// The verified shape of `lsof -a -d cwd -Fn -p <pid>`.
func TestParseLsofCWDsReadsVerifiedTriple(t *testing.T) {
	raw := "p98390\nfcwd\nn/private/tmp/cwdprobe\n"
	got := parseLsofCWDs(raw)
	if got[98390] != "/private/tmp/cwdprobe" {
		t.Fatalf("parse = %#v", got)
	}
}

func TestParseLsofCWDsBatchedPids(t *testing.T) {
	raw := "p111\nfcwd\nn/a\np222\nfcwd\nn/b\np333\n"
	got := parseLsofCWDs(raw)
	if got[111] != "/a" || got[222] != "/b" {
		t.Fatalf("batched parse = %#v", got)
	}
	// A pid lsof could not inspect has no triple and must be absent, not
	// guessed: a wrong cwd would offer /live a session from another project.
	if _, ok := got[333]; ok {
		t.Fatalf("pid with no fcwd triple must be absent: %#v", got)
	}
}

// lsof reports many descriptors per process; only a path directly following
// the fcwd marker is the working directory. Taking any n<path> line would
// pick up an open file instead.
func TestParseLsofCWDsIgnoresNonCwdDescriptors(t *testing.T) {
	raw := "p5\nf12\nn/private/tmp/somefile\nfcwd\nn/private/tmp/realcwd\n"
	if got := parseLsofCWDs(raw); got[5] != "/private/tmp/realcwd" {
		t.Fatalf("a non-cwd descriptor was mistaken for the cwd: %#v", got)
	}
}

func TestParseLsofCWDsEmptyAndJunk(t *testing.T) {
	if got := parseLsofCWDs(""); len(got) != 0 {
		t.Fatalf("empty input = %#v", got)
	}
	// Junk must not panic and must not invent a mapping.
	if got := parseLsofCWDs("garbage\np\nfcwd\nn\nn/x\n"); len(got) != 0 {
		t.Fatalf("junk input produced %#v", got)
	}
}
