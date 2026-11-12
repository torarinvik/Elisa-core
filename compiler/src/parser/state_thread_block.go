package parser

import (
	"elisacore/src/ast"
	"elisacore/src/parserdesugar"
)

func desugarStateThreadBlock(target, value ast.Expr) (ast.Stmt, bool) {
	return parserdesugar.StateThreadBlock(target, value)
}
