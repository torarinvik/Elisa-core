package semantic

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanNameNodesBuiltOnDemand(t *testing.T) {
	idx := &storeScanDeclIndex{flat: []any{
		&ast.VarDeclStmt{Name: "dst", Value: &ast.Ident{Name: "src"}},
		&ast.AssignStmt{Target: &ast.Ident{Name: "dst"}, Value: &ast.Ident{Name: "other"}},
		&ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "dst"}, Field: "push"}, Args: []ast.Expr{&ast.Ident{Name: "src"}}},
		&ast.ReturnStmt{Value: &ast.Ident{Name: "ignored"}},
	}}
	if idx.nameNodes != nil {
		t.Fatal("reverse index was built eagerly")
	}
	for name, want := range map[string][]int{"dst": {0, 1, 2}, "src": {0, 2}, "other": {1}, "ignored": nil} {
		if got := idx.nodesForName(name); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestStoreScanNameNodesConcurrentReaders(t *testing.T) {
	idx := &storeScanDeclIndex{flat: []any{&ast.VarDeclStmt{Name: "x", Value: &ast.Ident{Name: "source"}}}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := idx.nodesForName("source"); !reflect.DeepEqual(got, []int{0}) {
				t.Errorf("offsets = %v", got)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkStoreScanIndexLazyNames(b *testing.B) {
	decl := &ast.FuncDecl{}
	for i := 0; i < 200; i++ {
		decl.Body = append(decl.Body, &ast.VarDeclStmt{Name: fmt.Sprintf("var%d", i), Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "source"}, Right: &ast.IntLit{Value: "1"}}})
	}
	for _, needNames := range []bool{false, true} {
		name := "without-filled-proof"
		if needNames {
			name = "with-filled-proof"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a := &Analyzer{}
				idx := a.storeScanIndex(decl)
				if needNames && len(idx.nodesForName("source")) != 200 {
					b.Fatal("lost names")
				}
			}
		})
	}
}
