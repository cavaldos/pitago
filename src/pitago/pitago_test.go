package pitago

import "testing"

func TestScopeOf(t *testing.T) {
	for in, want := range map[string]string{"": "all", "tools": "tools", "messages": "messages", "foo": "all"} {
		if s, _ := ScopeOf(in); s != want {
			t.Errorf("ScopeOf(%q) = %q, want %q", in, s, want)
		}
	}
	if _, f := ScopeOf("foo"); f != "foo" {
		t.Errorf("unknown arg must become filter, got %q", f)
	}
}

func TestParseMouseArg(t *testing.T) {
	if ParseMouseArg("") != nil {
		t.Error("empty must toggle (nil)")
	}
	if b := ParseMouseArg("off"); b == nil || *b {
		t.Error("off must parse false")
	}
	if b := ParseMouseArg("on"); b == nil || !*b {
		t.Error("on must parse true")
	}
}
