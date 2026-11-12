// Package conjunctionsyntax provides syntax-only conjunction traversal.
package conjunctionsyntax

import (
	"elisacore/src/ast"
	"elisacore/src/exprkey"
	"elisacore/src/lexer"
)

// Flatten preserves the original leaves, including parentheses, in source order.
func Flatten(expr ast.Expr) []ast.Expr {
	var leaves []ast.Expr
	pending := []ast.Expr{expr}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if bin, ok := exprkey.StripParens(current).(*ast.BinaryExpr); ok && bin != nil && bin.Op == lexer.TOKEN_AND {
			pending = append(pending, bin.Right, bin.Left)
		} else {
			leaves = append(leaves, current)
		}
	}
	return leaves
}
