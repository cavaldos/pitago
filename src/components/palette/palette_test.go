package palette

import (
	"reflect"
	"testing"
)

// "/re" must suggest /recent, /reload, /resume (match at pos 0,
// alphabetical) before /tree (match buried at pos 2).
func TestMatchRanksOutsideIn(t *testing.T) {
	names := []string{"tree", "resume", "reload", "recent", "model"}
	got := Match("re", names)
	want := []int{3, 2, 1, 0} // recent, reload, resume, tree
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Match(re) = %v, want %v", got, want)
	}
}

// Case-insensitive, prefix beats inner match.
func TestMatchPrefixBeatsInner(t *testing.T) {
	names := []string{"tree", "recent"}
	if got := Match("RE", names); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("Match(RE) = %v, want [1 0]", got)
	}
}

// Empty query keeps catalog order ("/" shows the full list).
func TestMatchEmptyKeepsCatalogOrder(t *testing.T) {
	names := []string{"model", "recent", "yank"}
	if got := Match("", names); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("Match() = %v, want [0 1 2]", got)
	}
}

// Name matches win outright: /tree matches by NAME, so the row whose
// DESCRIPTION merely mentions "tree" must not crowd the list.
func TestFallbackOnlyWhenNoNameMatches(t *testing.T) {
	names := []string{"tree", "model"}
	fallbacks := []string{"builtin session tree", "builtin tree model manager"}
	got := MatchWithFallback("tree", names, fallbacks)
	if !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("name tier must win, got %v, want [0]", got)
	}
}

// No name matches → fall back to source/description, still ranked
// outside-in by match position then command name.
func TestFallbackTierRanking(t *testing.T) {
	names := []string{"zebra", "alpha"}
	fallbacks := []string{"builtin has tree inside", "builtin tree helper"}
	got := MatchWithFallback("tree", names, fallbacks)
	// both match at pos 8 in the fallback; "alpha" < "zebra"
	if !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("fallback tier = %v, want [1 0]", got)
	}
}

// Empty query on the fallback path keeps catalog order too.
func TestFallbackEmptyKeepsCatalogOrder(t *testing.T) {
	names := []string{"model", "recent"}
	fallbacks := []string{"b", "c"}
	if got := MatchWithFallback("", names, fallbacks); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("MatchWithFallback() = %v, want [0 1]", got)
	}
}

// Origin/extension narrowing still works through the fallback tier:
// "/pitago" matches no NAME, but the pitago row's source does.
func TestFallbackMatchesOrigin(t *testing.T) {
	names := []string{"model", "recent"}
	fallbacks := []string{"builtin  Select model", "pitago  Switch recent model"}
	got := MatchWithFallback("pitago", names, fallbacks)
	if !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("origin fallback = %v, want [1]", got)
	}
}
