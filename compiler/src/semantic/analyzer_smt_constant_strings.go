package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

type smtConstantStringBinding struct {
	expr ast.Expr
	env  map[string]string
}

// This fragment compares literal results; it does not model mutable string storage.
func constantStringResult(expr ast.Expr, depth int) bool {
	if depth > 32 {
		return false
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.StringLit:
		return !strings.ContainsRune(n.Value, 0)
	case *ast.MatchExpr:
		// A source match evaluates its subject once; do not duplicate call or
		// mutable projection evaluation while lowering the individual arm guards.
		switch stripOptimizationParens(n.Value).(type) {
		case *ast.Ident, *ast.IntLit:
		default:
			return false
		}
		if n.Store != nil || len(n.Arms) == 0 {
			return false
		}
		if _, ok := n.Arms[len(n.Arms)-1].Pattern.(*ast.MatchWildcardPattern); !ok {
			return false
		}
		for i, arm := range n.Arms {
			if arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			if i < len(n.Arms)-1 {
				pattern, ok := arm.Pattern.(*ast.MatchLiteralPattern)
				if !ok || pattern.Pinned {
					return false
				}
				if _, ok := stripOptimizationParens(pattern.Value).(*ast.IntLit); !ok {
					return false
				}
			}
			body, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !constantStringResult(body.Expr, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func (tr *smtTranslator) constantStringEquality(left, right ast.Expr, env map[string]string, depth int) (string, bool) {
	if depth > 32 {
		return "", false
	}
	left = stripOptimizationParens(left)
	right = stripOptimizationParens(right)
	// Keep the literal comparator independent of the substituted operand's scope.
	// Comparing two symbolic strings needs two environments and is outside this fragment.
	if _, ok := right.(*ast.StringLit); !ok {
		if _, ok := left.(*ast.StringLit); ok {
			return tr.constantStringEquality(right, left, env, depth+1)
		}
		return "", false
	}
	if id, ok := left.(*ast.Ident); ok {
		if bound, ok := tr.stringBindings[env["\x00string:"+id.Name]]; ok {
			return tr.constantStringEquality(bound.expr, right, bound.env, depth+1)
		}
	}
	if id, ok := right.(*ast.Ident); ok {
		if bound, ok := tr.stringBindings[env["\x00string:"+id.Name]]; ok {
			return tr.constantStringEquality(left, bound.expr, bound.env, depth+1)
		}
	}
	wanted, ok := right.(*ast.StringLit)
	if ok && strings.ContainsRune(wanted.Value, 0) {
		return "", false
	}
	if !ok {
		if _, ok := left.(*ast.StringLit); ok {
			return tr.constantStringEquality(right, left, env, depth+1)
		}
		return "", false
	}
	if literal, ok := left.(*ast.StringLit); ok {
		if strings.ContainsRune(literal.Value, 0) {
			return "", false
		}
		if literal.Value == wanted.Value {
			return "true", true
		}
		return "false", true
	}
	if atom, ok := tr.classifierStringEquality(left, right, env); ok {
		return atom, true
	}
	match, ok := left.(*ast.MatchExpr)
	if !ok || !constantStringResult(match, depth) {
		return "", false
	}
	last := match.Arms[len(match.Arms)-1].Body[0].(*ast.ExprStmt)
	result, ok := tr.constantStringEquality(last.Expr, wanted, env, depth+1)
	if !ok {
		return "", false
	}
	for i := len(match.Arms) - 2; i >= 0; i-- {
		arm := match.Arms[i]
		pattern := arm.Pattern.(*ast.MatchLiteralPattern)
		condition := &ast.BinaryExpr{Position: arm.Position, Op: lexer.TOKEN_EQEQ, Left: match.Value, Right: pattern.Value}
		guard, ok := tr.boolTerm(condition, env)
		if !ok {
			return "", false
		}
		body := arm.Body[0].(*ast.ExprStmt)
		branch, ok := tr.constantStringEquality(body.Expr, wanted, env, depth+1)
		if !ok {
			return "", false
		}
		result = "(ite " + guard + " " + branch + " " + result + ")"
	}
	return result, true
}
