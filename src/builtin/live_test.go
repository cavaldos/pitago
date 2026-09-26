package builtin

import (
	"strings"
	"testing"

	"pitago/src/app"
)

// TestLiveBuiltinListsSessions checks /live is ours and that it is the
// attach/detach toggle over the pi processes in this directory — not a
// broadcast of our own session. The empty directory is the observable case:
// it reports, installs the bridge, and returns no command.
func TestLiveBuiltinListsSessions(t *testing.T) {
	var b app.Builtin
	found := false
	for _, c := range All() {
		if c.Name == "live" {
			b, found = c, true
		}
	}
	if !found {
		t.Fatal("/live is not registered")
	}
	if b.Origin != OriginPitago || b.Usage != "/live" {
		t.Fatalf("/live metadata: origin=%q usage=%q", b.Origin, b.Usage)
	}
	if strings.Contains(strings.ToLower(b.Desc), "broadcast") {
		t.Fatalf("/live still advertises publishing our own session: %q", b.Desc)
	}

	dir := t.TempDir()
	t.Setenv("HOME", dir) // keep the bridge install out of the real home
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PITAGO_LIVE_DESCRIPTORS", dir+"/desc")

	m := app.New(nil, dir)
	if cmd := b.Run(&m, ""); cmd != nil {
		t.Fatal("/live with no running pi started a transport")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("/live opened a picker with nothing to pick: %+v", m.Dialogs)
	}

	// The confirm map must know the picker kind, or Enter would silently do
	// nothing on the dialog /live opens.
	if _, ok := Confirmers()["live"]; !ok {
		t.Fatal("the /live picker has no Enter action")
	}
}
