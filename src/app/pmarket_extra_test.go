package app

import "testing"

func TestValidPluginSpec(t *testing.T) {
	for _, s := range []string{"npm:pi-lens", "npm:@scope/pi-foo", "git:github.com/a/b"} {
		if !validPluginSpec(s) {
			t.Fatalf("spec %q should validate", s)
		}
	}
	for _, s := range []string{"", "pi-lens", "npm:", "npm:foo;bar", "npm:foo bar", "npm:foo&&bar", "npm:$(x)", "npm:foo|bar", "exec install", "../../x", "npm:FOO"} {
		if validPluginSpec(s) {
			t.Fatalf("spec %q should refuse", s)
		}
	}
	// invalid specs never reach exec
	m := New(nil, t.TempDir())
	if msg := m.ChangePluginCmd("install", "npm:foo;bar")(); msg.(PluginChangeMsg).Err == nil {
		t.Fatal("invalid spec should error without exec")
	}
	if msg := m.ChangePluginCmd("rm", "npm:pi-lens")(); msg.(PluginChangeMsg).Err == nil {
		t.Fatal("invalid action should error without exec")
	}
}
