package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationParamStoreTargetsClone(t *testing.T) {
	if !specializationParamStoreTargetsSchema() {
		t.Fatal("current schema unexpectedly rejected")
	}
	a, b, c := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	backing := make([]map[int]bool, 1, 3)
	backing[0] = map[int]bool{1: true}
	for _, src := range []map[*ast.FuncDecl][]map[int]bool{nil, {}, {a: backing, b: nil, c: {}}} {
		got := cloneSpecializationParamStoreTargets(src)
		fallback := cloneSpecializationParamStoreTargetsGuarded(src, false)
		want := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl][]map[int]bool)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fallback, want) {
			t.Fatal("typed or fallback clone differs from reflection")
		}
		if src == nil || len(src) == 0 {
			continue
		}
		if got[b] != nil || got[c] == nil || cap(got[a]) != len(got[a]) || &got[a][0] != &src[a][0] {
			t.Fatal("slice nilness, capacity, or backing changed")
		}
		got[a][0][2] = true
		if !src[a][0][2] {
			t.Fatal("inner map sharing changed")
		}
		row := append(got[a], map[int]bool{3: true})
		if &row[0] == &src[a][0] {
			t.Fatal("append did not detach from source")
		}
		delete(got, a)
		if _, ok := src[a]; !ok {
			t.Fatal("outer map was not detached")
		}
	}
}

var paramStoreTargetsCloneSink map[*ast.FuncDecl][]map[int]bool

func BenchmarkSpecializationParamStoreTargetsClone(b *testing.B) {
	src := make(map[*ast.FuncDecl][]map[int]bool, 1000)
	for i := 0; i < 1000; i++ {
		src[&ast.FuncDecl{Name: string(rune(i))}] = []map[int]bool{{0: true}, nil}
	}
	b.Run("reflect", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			paramStoreTargetsCloneSink = deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl][]map[int]bool)
		}
	})
	b.Run("typed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			paramStoreTargetsCloneSink = cloneSpecializationParamStoreTargets(src)
		}
	})
}
