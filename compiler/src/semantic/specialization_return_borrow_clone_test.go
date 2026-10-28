package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationReturnBorrowClone(t *testing.T) {
	if !specializationReturnBorrowSchema() {
		t.Fatal("current schema rejected")
	}
	a, b, c := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	shared := map[int]bool{1: true, 2: false}
	for _, src := range []map[*ast.FuncDecl]returnBorrowFlow{nil, {}, {a: {Params: shared, Contents: shared, Local: true}, b: {Params: map[int]bool{}, Contents: nil}, c: {Params: shared, Contents: map[int]bool{}}}} {
		got := cloneSpecializationReturnBorrowSummaries(src)
		old := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]returnBorrowFlow)
		fallback := cloneSpecializationReturnBorrowSummariesGuarded(src, false)
		if !reflect.DeepEqual(got, old) || !reflect.DeepEqual(fallback, old) {
			t.Fatal("clone differs from reflective path")
		}
		if len(src) == 0 {
			continue
		}
		if got[b].Params == nil || got[b].Contents != nil || got[c].Contents == nil {
			t.Fatal("nil/empty distinction lost")
		}
		flow := got[a]
		flow.Params[7] = true
		if shared[7] || flow.Contents[7] || got[c].Params[7] {
			t.Fatal("map aliasing leaked across copies")
		}
		flow.Contents[8] = true
		if shared[8] || flow.Params[8] {
			t.Fatal("nested maps not independently cloned")
		}
		delete(got, a)
		if _, ok := src[a]; !ok {
			t.Fatal("outer map is shared")
		}
	}
}
