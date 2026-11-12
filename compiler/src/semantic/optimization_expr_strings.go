package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/exprkey"
)

func optimizationCallArg(call *ast.CallExpr, index int) (ast.Expr, bool) {
	return exprkey.CallArg(call, index)
}
func optimizationFieldMatches(expr, object ast.Expr, field string) bool {
	return exprkey.FieldMatches(expr, object, field)
}
func isZeroOptimizationExpr(expr ast.Expr) bool      { return exprkey.IsZero(expr) }
func stripOptimizationParens(expr ast.Expr) ast.Expr { return exprkey.StripParens(expr) }
func optimizationExprString(expr ast.Expr) string    { return exprkey.String(expr) }
