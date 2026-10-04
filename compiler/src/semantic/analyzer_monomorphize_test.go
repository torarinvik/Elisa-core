//go:build cgo

package semantic

import (
	"maps"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializedExprTypesRestoresFunctionAnnotationsOnly(t *testing.T) {
	result := analyzeTreeTestSource(t, "specialized_expr_types.elisa", `def id[T](x: T) -> T:
    return x

def other(x: i64) -> i64:
    return x + 1
`)
	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "id" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("generic function id was not found")
	}

	before := maps.Clone(result.ExprTypes)
	functionExprs := map[ast.Expr]struct{}{}
	collectSpecializedFunctionExprs(fn, functionExprs)
	overlay := result.SpecializedExprTypes(fn, []Type{&BuiltinType{Name: "i64"}})
	if len(overlay) == 0 {
		t.Fatal("expected concrete type annotations from specialized re-analysis")
	}
	for expr, typ := range before {
		if _, belongsToFunction := functionExprs[expr]; belongsToFunction {
			continue
		}
		if result.ExprTypes[expr] != typ {
			t.Fatalf("specialization changed expression type outside id: %T", expr)
		}
	}
	for expr, typ := range before {
		if _, belongsToFunction := functionExprs[expr]; belongsToFunction && result.ExprTypes[expr] != typ {
			t.Fatalf("specialization did not restore the template type for %T", expr)
		}
	}
}
