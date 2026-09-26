package update

import (
	"testing"
	"time"
)

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.0.1", "v0.0.2", true},
		{"v0.0.2", "v0.0.2", false},
		{"v0.1.0", "v0.0.9", false},
		{"v0.0.10", "v0.0.9", false},
		{"dev", "v0.0.1", true},      // source build → offer install
		{"abc1234", "v0.0.1", true},  // commit hash → offer install
		{"", "v0.0.1", true},         // empty → offer install
		{"v0.0.2", "garbage", false}, // bad latest → never nag
		{"v0.0.2", "", false},
	}
	for _, c := range cases {
		if got := NeedsUpdate(c.cur, c.latest); got != c.want {
			t.Errorf("NeedsUpdate(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	if !IsRelease("v0.0.3") || !IsRelease("1.2.3") {
		t.Error("semver tags must be releases")
	}
	for _, v := range []string{"dev", "", "abc1234", "v1.2", "v1.2.3.4"} {
		if IsRelease(v) {
			t.Errorf("%q must not be a release", v)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("darwin", "arm64"); got != "pitago-darwin-arm64" {
		t.Errorf("got %q", got)
	}
	if got := AssetName("linux", "amd64"); got != "pitago-linux-amd64" {
		t.Errorf("got %q", got)
	}
	if got := AssetName("windows", "amd64"); got != "pitago-windows-amd64.exe" {
		t.Errorf("got %q", got)
	}
}

func TestTagFromLocation(t *testing.T) {
	tag, err := tagFromLocation("https://github.com/cavaldos/pitago/releases/tag/v0.0.5")
	if err != nil || tag != "v0.0.5" {
		t.Errorf("got %q, %v", tag, err)
	}
	for _, bad := range []string{"", "https://github.com/cavaldos/pitago/releases", "https://x/tag/notaversion"} {
		if _, err := tagFromLocation(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestFresh(t *testing.T) {
	if Fresh(Cache{}, time.Hour) {
		t.Error("zero cache must be stale")
	}
	if !Fresh(Cache{LastCheck: time.Now(), LatestTag: "v0.0.1"}, CheckTTL) {
		t.Error("fresh cache must be fresh")
	}
	stale := Cache{LastCheck: time.Now().Add(-25 * time.Hour), LatestTag: "v0.0.1"}
	if Fresh(stale, CheckTTL) {
		t.Error("25h-old cache must be stale")
	}
}
