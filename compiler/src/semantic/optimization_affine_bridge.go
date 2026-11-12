package semantic

import "elisacore/src/extentmath"

func (e optimizationAffineExpr) sub(other optimizationAffineExpr) optimizationAffineExpr {
	return optimizationAffineExpr(extentmath.Expr(e).Sub(extentmath.Expr(other)))
}
func optimizationAffineIsConst(e optimizationAffineExpr) bool {
	return extentmath.IsConst(extentmath.Expr(e))
}
func optimizationAffineExprEqual(a, b optimizationAffineExpr) bool {
	return extentmath.Equal(extentmath.Expr(a), extentmath.Expr(b))
}
func optimizationAffineCompare(a, b optimizationAffineExpr) (int, bool) {
	return extentmath.Compare(extentmath.Expr(a), extentmath.Expr(b))
}
func optimizationAffineCompareByPositiveSymbol(a, b optimizationAffineExpr, s string) (int, bool) {
	return extentmath.CompareByPositiveSymbol(extentmath.Expr(a), extentmath.Expr(b), s)
}
func optimizationAffineCompareBySinglePositiveTerm(a, b optimizationAffineExpr) (int, bool) {
	return extentmath.CompareBySinglePositiveTerm(extentmath.Expr(a), extentmath.Expr(b))
}
func optimizationAffineExprString(e optimizationAffineExpr) string {
	return extentmath.String(extentmath.Expr(e))
}
func parseOptimizationAffineExpr(s string) (optimizationAffineExpr, bool) {
	e, ok := extentmath.Parse(s)
	return optimizationAffineExpr(e), ok
}
func optimizationTrimExtentExpr(s string) string       { return extentmath.Trim(s) }
func optimizationSplitTopLevel(s, sep string) []string { return extentmath.SplitTopLevel(s, sep) }
