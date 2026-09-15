package parser

import (
	"elisacore/src/ast"
)

// normalizeFunctionTailReturns gives block-bodied value functions the same tail
// semantics as an explicit return.  A final expression in the function body is
// a return value; a final if/match inherits that meaning in each branch/arm.
// Loops and other nested blocks deliberately do not inherit it: an expression
// inside a loop is not proof that the function returns after the loop.
//
// This is a parser normalization rather than a backend fallback.  Keeping the
// AST on the explicit ReturnStmt path makes typing, postconditions, ownership,
// and cleanup identical for both spellings.
func normalizeFunctionTailReturns(body []ast.Stmt, returnType ast.TypeExpr) []ast.Stmt {
	if functionReturnTypeIsVoid(returnType) || len(body) == 0 {
		return body
	}
	normalized := append([]ast.Stmt(nil), body...)
	normalized[len(normalized)-1] = normalizeFunctionTailStatement(normalized[len(normalized)-1])
	return normalized
}

func functionReturnTypeIsVoid(returnType ast.TypeExpr) bool {
	if returnType == nil {
		return true
	}
	named, ok := returnType.(*ast.NamedType)
	return ok && named.Name == "void"
}

func normalizeFunctionTailReturnsInBlock(body []ast.Stmt) []ast.Stmt {
	if len(body) == 0 {
		return body
	}
	normalized := append([]ast.Stmt(nil), body...)
	normalized[len(normalized)-1] = normalizeFunctionTailStatement(normalized[len(normalized)-1])
	return normalized
}

func normalizeFunctionTailStatement(statement ast.Stmt) ast.Stmt {
	switch node := statement.(type) {
	case *ast.ExprStmt:
		if node == nil || node.Expr == nil {
			return statement
		}
		if isNonReturningFunctionTailCall(node.Expr) {
			return statement
		}
		return &ast.ReturnStmt{Position: node.Position, Value: node.Expr}
	case *ast.IfStmt:
		if node == nil {
			return statement
		}
		copy := *node
		copy.Then = normalizeFunctionTailReturnsInBlock(node.Then)
		copy.Elifs = append([]ast.ElifClause(nil), node.Elifs...)
		for index := range copy.Elifs {
			copy.Elifs[index].Body = normalizeFunctionTailReturnsInBlock(copy.Elifs[index].Body)
		}
		copy.Else = normalizeFunctionTailReturnsInBlock(node.Else)
		return &copy
	case *ast.MatchStmt:
		if node == nil {
			return statement
		}
		copy := *node
		copy.Arms = append([]ast.MatchArm(nil), node.Arms...)
		for index := range copy.Arms {
			copy.Arms[index].Body = normalizeFunctionTailReturnsInBlock(copy.Arms[index].Body)
		}
		return &copy
	default:
		return statement
	}
}

func isNonReturningFunctionTailCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	callee, ok := call.Func.(*ast.Ident)
	return ok && (callee.Name == "raise" || callee.Name == "panic" || callee.Name == "error")
}
