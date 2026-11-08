package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

func BenchmarkFillMayAdoptLargeBody(b *testing.B) {
	body := make([]ast.Stmt, 10000)
	for i := range body {
		body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	root := reflect.ValueOf(body)
	visit := func(any) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillMayAdoptWalk(root, visit)
	}
}

func BenchmarkFillMayAdoptLocalDeclarations(b *testing.B) {
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
	for i := 0; i < 100; i++ {
		fn := &ast.FuncDecl{Name: fmt.Sprintf("function%d", i)}
		for j := 0; j < 30; j++ {
			fn.Body = append(fn.Body, &ast.VarDeclStmt{Name: fmt.Sprintf("local%d", j), Value: &ast.Ident{Name: "input"}})
		}
		a.funcDeclSymbols[fn] = &Symbol{Name: fn.Name}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(a.computeFillMayAdopt()) != len(a.funcDeclSymbols) {
			b.Fatal("missing functions")
		}
	}
}
