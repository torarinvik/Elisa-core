package semantic

import (
	"elisacore/src/ast"
	"maps"
	"testing"
)

func TestStoreFlowTypedNamesPreserveConflictingBindings(t *testing.T) {
	integer := &ast.BuiltinTypeExpr{Name: "i64"}
	other := &ast.BuiltinTypeExpr{Name: "sview"}
	for mask := 0; mask < 256; mask++ {
		idx := &storeFlowBinderIndex{}
		for i := 0; i < 8; i++ {
			typ := ast.TypeExpr(integer)
			if mask&(1<<i) != 0 {
				typ = other
			}
			idx.typed = append(idx.typed, storeFlowTypedBinder{name: string(rune('a' + i%3)), t: typ})
		}
		idx.rangeNames = []string{"a", "range"}
		idx.rangeOther = []string{"a", "range", "extra"}
		idx.other = []string{"c", "untyped"}
		for _, pred := range []func(ast.TypeExpr) bool{storeFlowIntegerTypeExpr, storeFlowViewTypeExpr, func(ast.TypeExpr) bool { return true }} {
			want, excluded := idx.sets(pred)
			for name := range excluded {
				delete(want, name)
			}
			got := idx.typedNames(pred)
			if !maps.Equal(got, want) {
				t.Fatalf("mask %d: got %v, want %v", mask, got, want)
			}
			// Each result remains caller-owned; changing it cannot poison another query.
			clear(got)
			if !maps.Equal(idx.typedNames(pred), want) {
				t.Fatal("classification result aliases a cache")
			}
		}
	}
}
