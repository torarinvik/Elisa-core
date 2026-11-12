package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/reservebounds"
)

// Preserve the registered checkpoint type identity.
type reserveBoundLinear struct {
	terms    map[string]int
	constant int
}

func semanticCountingLoopBoundExpr(loop *ast.ForStmt) (ast.Expr, bool) {
	return reservebounds.CountingLoopBoundExpr(loop)
}
func semanticCloneReserveBoundExpr(e ast.Expr) ast.Expr {
	return reservebounds.CloneReserveBoundExpr(e)
}
func semanticIntReserveExpr(value int, pos lexer.Pos) ast.Expr {
	return reservebounds.IntReserveExpr(value, pos)
}
func semanticReserveExprIsIntOne(e ast.Expr) bool { return reservebounds.ReserveExprIsIntOne(e) }
func semanticAddReserveExpr(a, b ast.Expr, pos lexer.Pos) ast.Expr {
	return reservebounds.AddReserveExpr(a, b, pos)
}
func semanticMultiplyReserveExpr(a, b ast.Expr, pos lexer.Pos) ast.Expr {
	return reservebounds.MultiplyReserveExpr(a, b, pos)
}
func semanticReserveBoundAtLeast(actual, expected ast.Expr) bool {
	return reservebounds.ReserveBoundAtLeast(actual, expected)
}
