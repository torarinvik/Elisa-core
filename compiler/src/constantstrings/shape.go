package constantstrings

import (
	"elisacore/src/ast"
	"elisacore/src/exprkey"
	"elisacore/src/lexer"
	"strings"
)

// This fragment compares literal results; it does not model mutable string storage.
func Result(expr ast.Expr, depth int) bool {
	if depth > 32 {
		return false
	}
	switch n := exprkey.StripParens(expr).(type) {
	case *ast.StringLit:
		return !strings.ContainsRune(n.Value, 0)
	case *ast.TernaryExpr:
		// `"a" if c else "b"`: evaluated once at the return, so the call-free condition re-reads
		// the same values in the obligation.
		return n != nil && callFreeCondition(n.Cond, 0) && Result(n.Value, depth+1) && Result(n.Alt, depth+1)
	case *ast.MatchExpr:
		// A source match evaluates its subject once; do not duplicate call or
		// mutable projection evaluation while lowering the individual arm guards.
		switch exprkey.StripParens(n.Value).(type) {
		case *ast.Ident, *ast.IntLit:
		default:
			return false
		}
		if n.Store != nil || len(n.Arms) == 0 {
			return false
		}
		wildcard, ok := n.Arms[len(n.Arms)-1].Pattern.(*ast.MatchWildcardPattern)
		if !ok || wildcard == nil {
			return false
		}
		patternKind := 0
		for i, arm := range n.Arms {
			if arm.Pattern == nil || arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			if i < len(n.Arms)-1 {
				kind := 0
				switch pattern := arm.Pattern.(type) {
				case *ast.MatchLiteralPattern:
					if pattern.Pinned {
						return false
					}
					if _, ok := exprkey.StripParens(pattern.Value).(*ast.IntLit); !ok {
						return false
					}
					kind = 1
				case *ast.MatchStringLiteralPattern:
					if strings.ContainsRune(pattern.Value, 0) {
						return false
					}
					if _, ok := exprkey.StripParens(n.Value).(*ast.Ident); !ok {
						return false
					}
					kind = 2
				default:
					return false
				}
				if patternKind != 0 && patternKind != kind {
					return false
				}
				patternKind = kind
			}
			body, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !Result(body.Expr, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func callFreeCondition(expr ast.Expr, depth int) bool {
	if depth > 32 {
		return false
	}
	switch n := exprkey.StripParens(expr).(type) {
	case *ast.BoolLit, *ast.IntLit, *ast.StringLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return n != nil && callFreeCondition(n.Object, depth+1)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && (n.Op == lexer.TOKEN_NOT || n.Op == lexer.TOKEN_MINUS) && callFreeCondition(n.Operand, depth+1)
	case *ast.BinaryExpr:
		// An overloaded operator lowers to a user call, which may have effects.
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR:
			return callFreeCondition(n.Left, depth+1) && callFreeCondition(n.Right, depth+1)
		}
	}
	return false
}
