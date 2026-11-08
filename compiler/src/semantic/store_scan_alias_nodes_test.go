package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanAliasClosureNodes(t *testing.T) {
	decl, call, assign, ret := &ast.VarDeclStmt{Name: "x"}, &ast.CallExpr{}, &ast.AssignStmt{}, &ast.ReturnStmt{}
	binding := &ast.ExpectPatternStmt{Patterns: []ast.MatchPattern{&ast.MatchBindPattern{Name: "item"}}}
	idx := &storeScanDeclIndex{flat: []any{&ast.Ident{Name: "x"}, decl, &ast.IntLit{Value: "1"}, call, &ast.ForStmt{}, assign, ret, binding}}
	want := []any{decl, call, assign, binding}
	for i := 0; i < 2; i++ {
		if got := idx.bindingEffectNodes(); !reflect.DeepEqual(got, want) {
			t.Fatalf("nodes = %v, want %v", got, want)
		}
	}
}

func TestStoreScanParamAliasCacheModes(t *testing.T) {
	decl := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "source"}}, Body: []ast.Stmt{&ast.ReturnStmt{}}}
	a := &Analyzer{}
	idx := a.storeScanIndex(decl)
	for _, mode := range []int{storeScanModeWrite, storeScanModeShareFrom, storeScanModeDeep} {
		got, holds := a.storeScanParamAliases(decl, idx, 0, mode)
		var want map[string]bool
		var wantHolds storeScanHolds
		if mode == storeScanModeDeep {
			want, wantHolds = a.storeScanAliasClosureFrom(decl, idx, map[string]bool{}, map[string]bool{"source": true})
		} else {
			want, wantHolds = a.storeScanAliasClosure(decl, idx, map[string]bool{"source": true})
		}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(holds, wantHolds) {
			t.Fatalf("mode %d: cached result differs", mode)
		}
	}
	if len(idx.paramAliases) != 2 {
		t.Fatalf("expected ordinary and deep caches, got %d", len(idx.paramAliases))
	}
	a.invalidateStoreScanParamAliases()
	if len(idx.paramAliases) != 0 {
		t.Fatal("alias cache survived environment invalidation")
	}
}

func BenchmarkStoreScanParamAliases(b *testing.B) {
	decl := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "source"}}, Body: []ast.Stmt{&ast.ReturnStmt{}}}
	a := &Analyzer{}
	idx := a.storeScanIndex(decl)
	a.storeScanParamAliases(decl, idx, 0, storeScanModeWrite)
	for _, cached := range []bool{false, true} {
		name := "uncached"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if cached {
					a.storeScanParamAliases(decl, idx, 0, storeScanModeWrite)
				} else {
					a.storeScanAliasClosure(decl, idx, map[string]bool{"source": true})
				}
			}
		})
	}
}
