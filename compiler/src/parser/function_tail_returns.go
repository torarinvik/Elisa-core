package parser

import (
	"elisacore/src/ast"
	"elisacore/src/parserdesugar"
)

func normalizeFunctionTailReturns(body []ast.Stmt, returnType ast.TypeExpr) []ast.Stmt {
	return parserdesugar.NormalizeFunctionTailReturns(body, returnType)
}
