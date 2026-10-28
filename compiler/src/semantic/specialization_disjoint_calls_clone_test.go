package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationDisjointCallsClone(t *testing.T) {
	if !specializationDisjointCallsSchema() {
		t.Fatal("schema rejected")
	}
	a, b, c, d := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	rows := make([]callDisjointObservation, 1, 4)
	shared := map[*ast.FuncDecl][]callDisjointObservation{a: rows, b: nil, c: {}}
	for _, src := range []map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation{nil, {}, {a: shared, b: shared, c: nil, d: {}}} {
		got := cloneSpecializationDisjointCallSites(src)
		fallback := cloneSpecializationDisjointCallSitesGuarded(src, false)
		want := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fallback, want) {
			t.Fatal("clone differs from reflective behavior")
		}
		if len(src) == 0 {
			continue
		}
		if got[c] != nil || got[d] == nil || got[a][b] != nil || got[a][c] == nil || cap(got[a][a]) != len(rows) || &got[a][a][0] != &rows[0] {
			t.Fatal("nil, capacity, or backing changed")
		}
		delete(got[a], a)
		if _, ok := got[b][a]; !ok {
			t.Fatal("aliased inner maps were not independently copied")
		}
		if _, ok := src[a][a]; !ok {
			t.Fatal("source inner map was mutated")
		}
		appended := append(got[b][a], callDisjointObservation{})
		if &appended[0] == &rows[0] {
			t.Fatal("append retained shared backing")
		}
		delete(got, a)
		if _, ok := src[a]; !ok {
			t.Fatal("source outer map was mutated")
		}
	}
}
