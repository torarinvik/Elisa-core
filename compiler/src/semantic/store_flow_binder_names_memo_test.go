package semantic

import (
	"maps"
	"testing"

	"elisacore/src/ast"
)

// binderNames is memoized on the immutable index: every call returns the same set as the
// unmemoized union of both partitions.
func TestStoreFlowBinderNamesMemoMatchesFreshUnion(t *testing.T) {
	idx := &storeFlowBinderIndex{
		typed:      []storeFlowTypedBinder{{name: "a", t: &ast.NamedType{Name: "i64"}}, {name: "b", t: nil}},
		rangeNames: []string{"i"},
		rangeOther: []string{"s"},
		other:      []string{"p", "a"},
		untyped:    map[string]bool{},
	}
	scalar, other := idx.sets(func(ast.TypeExpr) bool { return true })
	want := map[string]bool{}
	for n := range scalar {
		want[n] = true
	}
	for n := range other {
		want[n] = true
	}
	for i := 0; i < 3; i++ {
		if got := idx.binderNames(); !maps.Equal(got, want) {
			t.Fatalf("call %d: binderNames = %v, want %v", i, got, want)
		}
	}
}
