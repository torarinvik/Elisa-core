package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"testing"
)

var sortedSymbolBenchSink []*Symbol

func BenchmarkSortedSymbolKeys(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := make(map[*Symbol]bool, n)
			for i := 0; i < n; i++ {
				m[&Symbol{Name: fmt.Sprint(i)}] = true
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sortedSymbolBenchSink = sortedSymbolKeys(m)
			}
		})
	}
}

var sortedDeclBenchSink []*ast.FuncDecl

func BenchmarkSortedFuncDeclKeys(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := make(map[*ast.FuncDecl]bool, n)
			for i := 0; i < n; i++ {
				m[&ast.FuncDecl{Name: fmt.Sprint(i)}] = true
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sortedDeclBenchSink = sortedFuncDeclKeys(m)
			}
		})
	}
}
func TestSortedKeysUsePositionAndNilOrder(t *testing.T) {
	early := &ast.FuncDecl{Name: "same", Position: lexer.Pos{File: "a", Offset: 1, Line: 2, Col: 4}}
	late := &ast.FuncDecl{Name: "same", Position: lexer.Pos{File: "a", Offset: 1, Line: 3, Col: 1}}
	ds := sortedFuncDeclKeys(map[*ast.FuncDecl]bool{nil: true, late: true, early: true})
	if len(ds) != 3 || ds[0] != nil || ds[1] != early || ds[2] != late {
		t.Fatalf("decl order %v", ds)
	}
	es := &Symbol{Name: "same", Node: early}
	ls := &Symbol{Name: "same", Node: late}
	syms := sortedSymbolKeys(map[*Symbol]bool{nil: true, ls: true, es: true})
	if len(syms) != 3 || syms[0] != nil || syms[1] != es || syms[2] != ls {
		t.Fatalf("symbol order %v", syms)
	}
}
