package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

func BenchmarkParamRetentionIndependent(b *testing.B) {
	const n = 256
	funcs := make([]*ast.FuncDecl, n)
	a := &Analyzer{funcDeclSymbols: make(map[*ast.FuncDecl]*Symbol, n)}
	for i := range funcs {
		fn := &ast.FuncDecl{Name: fmt.Sprint(i), Params: []ast.ParamDecl{{Name: "value"}}}
		funcs[i] = fn
		a.funcDeclSymbols[fn] = &Symbol{Type: &FuncType{Params: []Type{&BuiltinType{Name: "i64"}}}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(a.computeParamRetention(funcs)) != n {
			b.Fatal("lost functions")
		}
	}
}
