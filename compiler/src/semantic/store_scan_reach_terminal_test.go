package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanTerminalReachability(t *testing.T) {
	for _, writes := range []bool{false, true} {
		key := storeScanParamKey{decl: &ast.FuncDecl{Name: "f"}}
		a := &Analyzer{}
		a.setStoreScanParamFacts(key, storeScanParamFacts{writes: writes})
		if got := a.storeScanParamReaches(key); got != writes {
			t.Fatalf("terminal reachability = %v, want %v", got, writes)
		}
		if cached, ok := a.lookupStoreScanParamWriteCache(key); !ok || cached != writes {
			t.Fatal("terminal result was not cached")
		}
	}
}

func BenchmarkStoreScanTerminalReachability(b *testing.B) {
	key := storeScanParamKey{decl: &ast.FuncDecl{Name: "f"}}
	a := &Analyzer{}
	a.setStoreScanParamFacts(key, storeScanParamFacts{})
	a.setStoreScanParamWriteCache(key, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delete(a.storeScanParamWriteCache, key)
		if a.storeScanParamReaches(key) {
			b.Fatal("unexpected writer")
		}
	}
}

func TestStoreScanUncachedTerminalReachability(t *testing.T) {
	for _, tc := range []struct {
		name string
		decl *ast.FuncDecl
		want bool
	}{
		{"missing body", &ast.FuncDecl{Name: "external"}, true},
		{"empty writer", &ast.FuncDecl{Name: "plain", Params: []ast.ParamDecl{{Name: "p"}}, Body: []ast.Stmt{&ast.ReturnStmt{}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{}
			key := storeScanParamKey{decl: tc.decl}
			if got := a.storeScanParamReaches(key); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if cached, ok := a.lookupStoreScanParamWriteCache(key); !ok || cached != tc.want {
				t.Fatalf("bad cached result: %v %v", cached, ok)
			}
		})
	}
}

func TestStoreScanLiteralValuesDoNotShare(t *testing.T) {
	for _, expr := range []ast.Expr{&ast.IntLit{}, &ast.FloatLit{}, &ast.BoolLit{}, &ast.CharLit{}, &ast.StringLit{}} {
		a := &Analyzer{}
		if a.storeScanMayShare(&ast.FuncDecl{}, expr) {
			t.Fatalf("literal %T unexpectedly shares storage", expr)
		}
	}
}

func BenchmarkStoreScanShortReachability(b *testing.B) {
	a := &Analyzer{}
	keys := make([]storeScanParamKey, 8)
	for i := range keys {
		keys[i] = storeScanParamKey{decl: &ast.FuncDecl{Name: "f"}, index: i}
	}
	for i := 0; i < len(keys)-1; i++ {
		a.setStoreScanParamFacts(keys[i], storeScanParamFacts{reach: []storeScanParamKey{keys[i+1]}})
	}
	a.setStoreScanParamFacts(keys[len(keys)-1], storeScanParamFacts{writes: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(a.storeScanParamWriteCache)
		if !a.storeScanParamReaches(keys[0]) {
			b.Fatal("missing writer")
		}
	}
}
