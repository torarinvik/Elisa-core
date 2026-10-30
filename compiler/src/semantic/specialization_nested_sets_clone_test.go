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

func TestSpecializationNameSetCacheCloneCopiesOuterIndex(t *testing.T) {
	fn, other := &ast.FuncDecl{Name: "fn"}, &ast.FuncDecl{Name: "other"}
	for _, name := range []string{"reassignedParamCache", "privateFreshDArrayCache"} {
		var src Analyzer
		row := map[string]bool{"param": true}
		switch name {
		case "reassignedParamCache":
			src.reassignedParamCache = map[*ast.FuncDecl]map[string]bool{fn: row}
		case "privateFreshDArrayCache":
			src.privateFreshDArrayCache = map[*ast.FuncDecl]map[string]bool{fn: row}
		}

		var dst Analyzer
		if !cloneSpecializationNestedSetField(&dst, &src, name) {
			t.Fatalf("%s was not cloned", name)
		}
		var cloned map[*ast.FuncDecl]map[string]bool
		switch name {
		case "reassignedParamCache":
			cloned = dst.reassignedParamCache
		case "privateFreshDArrayCache":
			cloned = dst.privateFreshDArrayCache
		}
		if !reflect.DeepEqual(cloned[fn], row) {
			t.Fatalf("%s lost its cached row", name)
		}
		cloned[other] = map[string]bool{}
		delete(cloned, fn)
		var original map[*ast.FuncDecl]map[string]bool
		switch name {
		case "reassignedParamCache":
			original = src.reassignedParamCache
		case "privateFreshDArrayCache":
			original = src.privateFreshDArrayCache
		}
		if len(original) != 1 || !reflect.DeepEqual(original[fn], row) {
			t.Fatalf("%s outer index aliases its source", name)
		}
	}

	for _, name := range []string{"reassignedParamCache", "privateFreshDArrayCache"} {
		var src, dst Analyzer
		if !cloneSpecializationNestedSetField(&dst, &src, name) {
			t.Fatalf("%s was not cloned", name)
		}
		if name == "reassignedParamCache" && dst.reassignedParamCache != nil {
			t.Fatal("nil reassigned-parameter cache changed")
		}
		if name == "privateFreshDArrayCache" && dst.privateFreshDArrayCache != nil {
			t.Fatal("nil private-fresh cache changed")
		}
	}
	for _, name := range []string{"reassignedParamCache", "privateFreshDArrayCache"} {
		var src, dst Analyzer
		if name == "reassignedParamCache" {
			src.reassignedParamCache = map[*ast.FuncDecl]map[string]bool{}
		} else {
			src.privateFreshDArrayCache = map[*ast.FuncDecl]map[string]bool{}
		}
		if !cloneSpecializationNestedSetField(&dst, &src, name) {
			t.Fatalf("%s was not cloned", name)
		}
		if name == "reassignedParamCache" && dst.reassignedParamCache == nil {
			t.Fatal("empty reassigned-parameter cache became nil")
		}
		if name == "privateFreshDArrayCache" && dst.privateFreshDArrayCache == nil {
			t.Fatal("empty private-fresh cache became nil")
		}
	}
}
