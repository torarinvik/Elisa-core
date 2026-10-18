package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

func BenchmarkParamStoreTargetsControls(b *testing.B) {
	for _, shape := range []string{"independent-readonly", "independent-store", "callee-first", "caller-first"} {
		for _, mode := range []string{"worklist", "fixedpoint"} {
			b.Run(shape+"/"+mode, func(b *testing.B) {
				const n = 256
				a := &Analyzer{globalScope: NewScope(nil), funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
				funcs := make([]*ast.FuncDecl, n)
				for i := range funcs {
					fn := &ast.FuncDecl{Name: fmt.Sprintf("fn%d", i), Params: []ast.ParamDecl{{Name: "value"}}}
					switch shape {
					case "independent-readonly":
						fn.Body = []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "value"}}}
					case "independent-store":
						fn.Body = []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}}}
					default:
						if i == n-1 {
							fn.Body = []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}}}
						} else {
							fn.Body = []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("fn%d", i+1)}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}}}
						}
					}
					funcs[i] = fn
					sym := &Symbol{Name: fn.Name, Node: fn, Type: &FuncType{Params: []Type{&SViewType{}}}}
					a.funcDeclSymbols[fn] = sym
					a.globalScope.Define(sym)
				}
				first := funcs[0]
				if shape == "callee-first" {
					for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
						funcs[i], funcs[j] = funcs[j], funcs[i]
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var out map[*ast.FuncDecl][]map[int]bool
					if mode == "worklist" {
						out = a.computeParamStoreTargets(funcs)
					} else {
						out = a.computeParamStoreTargetsFixedPoint(funcs)
					}
					if got := out[first][0][storeTargetGlobal]; got != (shape != "independent-readonly") {
						b.Fatal("store parity")
					}
				}
			})
		}
	}
}
