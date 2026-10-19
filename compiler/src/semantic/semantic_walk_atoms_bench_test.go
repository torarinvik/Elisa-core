package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Keep setup outside timing. These benchmarks isolate post-typing walks;
// they do not measure parsing, semantic analysis or end-to-end edits.
func BenchmarkSemanticPostTypingWalks(b *testing.B) {
	for _, depth := range []int{16, 128} {
		for _, shape := range []string{"scalar", "shared-block"} {
			var expr ast.Expr = &ast.IntLit{Value: "1"}
			if shape == "shared-block" {
				loop := &ast.ExprBlock{
					Stmts: []ast.Stmt{&ast.VarDeclStmt{Name: "valid"}, &ast.ForStmt{}},
					Value: &ast.Ident{Name: "valid"},
				}
				expr = &ast.ExprBlock{
					Stmts: []ast.Stmt{&ast.ExprStmt{Expr: loop}, &ast.RegionStmt{Name: "__auto_atom", Lazy: true}},
					Value: expr,
				}
			}
			for i := 0; i < depth; i++ {
				if shape == "scalar" {
					expr = &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: expr, Right: &ast.IntLit{Value: "2"}}
				} else {
					expr = &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
				}
			}
			fn := &ast.FuncDecl{Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
			expected := 0
			if shape == "shared-block" {
				expected = 1
			}
			check := &Analyzer{}
			check.checkDiscardedLoopValues(fn)
			if len(check.diagnostics) != expected || len(collectSynthesizedRegions(fn.Body)) != expected {
				b.Fatal("fixture does not exercise expected diagnostics/regions")
			}
			b.Run(fmt.Sprintf("%s/depth%d", shape, depth), func(b *testing.B) {
				b.Run("discard", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						a := &Analyzer{}
						a.checkDiscardedLoopValues(fn)
						if len(a.diagnostics) != expected {
							b.Fatal("diagnostic count changed")
						}
					}
				})
				b.Run("regions", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						if len(collectSynthesizedRegions(fn.Body)) != expected {
							b.Fatal("region count changed")
						}
					}
				})
			})
		}
	}
}
