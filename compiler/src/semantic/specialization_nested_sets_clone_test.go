package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationNestedSetClone(t *testing.T) {
	for name, valid := range specializationNestedSetSchemas() {
		if !valid {
			t.Fatalf("invalid schema %s", name)
		}
	}
	a, b, c, d := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	row := map[string]bool{"x": true}
	for _, src := range []map[*ast.FuncDecl]map[string]bool{nil, {}, {a: row, b: row, c: nil, d: {}}} {
		want := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]map[string]bool)
		got := cloneSpecializationNestedSet(src, true)
		fallback := cloneSpecializationNestedSet(src, false)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fallback, want) {
			t.Fatal("clone differs from reflection")
		}
		if len(src) == 0 {
			continue
		}
		if got[c] != nil || got[d] == nil {
			t.Fatal("nil/empty distinction changed")
		}
		got[a]["x"] = false
		if !src[a]["x"] || !got[b]["x"] {
			t.Fatal("row map aliases source or sibling")
		}
		delete(got, a)
		if _, ok := src[a]; !ok {
			t.Fatal("outer map aliases source")
		}
	}
	var dst, src Analyzer
	src.ambientFillFreshParams = map[*ast.FuncDecl]map[int]bool{a: {2: true}}
	if !cloneSpecializationNestedSetField(&dst, &src, "ambientFillFreshParams") {
		t.Fatal("missing field")
	}
	dst.ambientFillFreshParams[a][2] = false
	if !src.ambientFillFreshParams[a][2] {
		t.Fatal("integer row alias")
	}
	shared := map[*ast.FuncDecl]bool{c: true}
	src.disjointDependents = map[*ast.FuncDecl]map[*ast.FuncDecl]bool{a: shared, b: shared}
	if !cloneSpecializationNestedSetField(&dst, &src, "disjointDependents") {
		t.Fatal("missing pointer-key field")
	}
	if !dst.disjointDependents[a][c] {
		t.Fatal("pointer-key identity changed")
	}
	delete(dst.disjointDependents[a], c)
	if !src.disjointDependents[a][c] || !dst.disjointDependents[b][c] {
		t.Fatal("pointer-key row aliases source or sibling")
	}
	if cloneSpecializationNestedSetField(&dst, &src, "nonexistent") {
		t.Fatal("unexpected field")
	}
}
