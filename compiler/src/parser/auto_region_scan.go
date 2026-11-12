package parser

import (
	"elisacore/src/ast"
	"elisacore/src/parserregionscan"
)

type autoRegionScan struct{ scanner parserregionscan.Scan }

func (s *autoRegionScan) exprTailNeedsAutoRegion(expr ast.Expr, nested bool) bool {
	return s.scanner.ExprTailNeedsAutoRegion(expr, nested)
}
func functionBodyNeedsAutoRegion(stmts []ast.Stmt) bool {
	return parserregionscan.FunctionBodyNeedsAutoRegion(stmts)
}
func exprBlocksNeedAutoRegion(stmts []ast.Stmt) bool {
	return parserregionscan.ExprBlocksNeedAutoRegion(stmts)
}
func isAutoAllocValue(expr ast.Expr) bool { return parserregionscan.IsAutoAllocValue(expr) }
func bodyForwardsConstructedStructLocal(stmts []ast.Stmt) bool {
	return parserregionscan.BodyForwardsConstructedStructLocal(stmts)
}
