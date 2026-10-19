package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Exercise an assertion tree in a populated compiler scope, as seen in the
// Stage1 check profile. Scope construction is excluded from the measurement.
func BenchmarkLoopAssertConditionScope(b *testing.B) {
	scope := NewScope(nil)
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("binding_%d", i)
		scope.Define(&Symbol{Name: name, Type: &BuiltinType{Name: "bool"}})
	}
	numeric := &BuiltinType{Name: "i64"}
	scope.Define(&Symbol{Name: "number", Type: numeric})
	a := &Analyzer{currentScope: scope, namedTypes: map[string]Type{"int": numeric, "bool": &BuiltinType{Name: "bool"}}}
	var expr ast.Expr = &ast.BoolLit{Value: true}
	for i := 0; i < 32; i++ {
		expr = &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: expr, Right: &ast.Ident{Name: "binding_0"}}
	}
	numericExpr := ast.Expr(&ast.Ident{Name: "number"})
	for i := 0; i < 16; i++ {
		numericExpr = &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: numericExpr, Right: &ast.IntLit{}}
	}
	comparison := &ast.BinaryExpr{Op: lexer.TOKEN_GTEQ, Left: numericExpr, Right: &ast.IntLit{}}
	for name, condition := range map[string]ast.Expr{"logical": expr, "arithmetic-comparison": comparison} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !a.loopAssertConditionIsSideEffectFree(condition) {
					b.Fatal("pure condition rejected")
				}
			}
		})
	}
}

func TestLoopAssertGatePreservesInnerShadow(t *testing.T) {
	numeric := &BuiltinType{Name: "i64"}
	outer := NewScope(nil)
	outer.Define(&Symbol{Name: "number", Type: numeric})
	inner := NewScope(outer)
	a := &Analyzer{currentScope: inner, namedTypes: map[string]Type{"int": numeric}}
	comparison := &ast.BinaryExpr{Op: lexer.TOKEN_GTEQ, Left: &ast.Ident{Name: "number"}, Right: &ast.IntLit{}}
	condition := &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: &ast.BoolLit{Value: true}, Right: comparison}
	if !a.loopAssertConditionIsSideEffectFree(condition) {
		t.Fatal("numeric outer binding rejected")
	}
	inner.Define(&Symbol{Name: "number"})
	if a.loopAssertConditionIsSideEffectFree(condition) {
		t.Fatal("unresolved inner binding borrowed the outer type")
	}
}
