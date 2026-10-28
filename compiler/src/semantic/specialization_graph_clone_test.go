package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationGraphClone(t *testing.T) {
	if !specializationGraphCacheSchema("storeFlowSCC") || !specializationGraphCacheSchema("callEdgeCache") || specializationGraphCacheSchema("unknown") {
		t.Fatal("unexpected schema validation")
	}
	a, b, c, d := &ast.FuncDecl{Name: "a"}, &ast.FuncDecl{Name: "b"}, &ast.FuncDecl{Name: "c"}, &ast.FuncDecl{Name: "d"}
	shared := make([]*ast.FuncDecl, 1, 4)
	shared[0] = d
	for _, src := range []map[*ast.FuncDecl][]*ast.FuncDecl{nil, {}, {a: shared, b: nil, c: {}, d: shared}} {
		got := cloneSpecializationFuncDeclSliceMap(src, true)
		fallback := cloneSpecializationFuncDeclSliceMap(src, false)
		want := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl][]*ast.FuncDecl)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fallback, want) {
			t.Fatal("clone differs from reflection")
		}
		if len(src) == 0 {
			continue
		}
		if got[b] != nil || got[c] == nil || cap(got[a]) != len(got[a]) || &got[a][0] != &src[a][0] || &got[a][0] != &got[d][0] {
			t.Fatal("slice nilness, capacity or backing changed")
		}
		row := append(got[a], a)
		if &row[0] == &src[a][0] {
			t.Fatal("append retained source backing")
		}
		delete(got, a)
		if _, ok := src[a]; !ok {
			t.Fatal("outer map was shared")
		}
	}
}
