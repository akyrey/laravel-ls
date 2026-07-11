package phputil_test

import (
	"reflect"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/phputil"
)

func TestResolveReachable(t *testing.T) {
	// A -> B -> Base, C -> Base, D -> Unknown, E has no parent.
	extends := map[phputil.FQN]phputil.FQN{
		"A": "B",
		"B": "Base",
		"C": "Base",
		"D": "Unknown",
	}
	extendsOf := func(fqn phputil.FQN) phputil.FQN { return extends[fqn] }

	fqns := []phputil.FQN{"A", "B", "C", "D", "E", "Base"}
	got := phputil.ResolveReachable(fqns, extendsOf, "Base")

	want := map[phputil.FQN]struct{}{
		"A":    {},
		"B":    {},
		"C":    {},
		"Base": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveReachable = %v, want %v", got, want)
	}
}

func TestResolveReachable_NoMatches(t *testing.T) {
	extendsOf := func(phputil.FQN) phputil.FQN { return "" }
	got := phputil.ResolveReachable([]phputil.FQN{"X", "Y"}, extendsOf, "Base")
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}
