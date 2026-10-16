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
	case *ast.TernaryExpr:
		// `"a" if c else "b"`: evaluated once at the return, so the call-free condition re-reads
		// the same values in the obligation.
		return n != nil && exprIsCallFreeCondition(n.Cond, 0) && constantStringResult(n.Value, depth+1) && constantStringResult(n.Alt, depth+1)
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
					if _, ok := stripOptimizationParens(pattern.Value).(*ast.IntLit); !ok {
						return false
					}
					kind = 1
				case *ast.MatchStringLiteralPattern:
					if strings.ContainsRune(pattern.Value, 0) {
						return false
					}
					if _, ok := stripOptimizationParens(n.Value).(*ast.Ident); !ok {
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
	if field, ok := left.(*ast.FieldExpr); ok {
		if value, found := tr.constantStringProjection(field); found {
			return tr.constantStringEquality(value, right, env, depth+1)
		}
	}
	if field, ok := right.(*ast.FieldExpr); ok {
		if value, found := tr.constantStringProjection(field); found {
			return tr.constantStringEquality(left, value, env, depth+1)
		}
	}
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
	if ternary, ok := left.(*ast.TernaryExpr); ok && constantStringResult(ternary, depth) {
		guard, ok := tr.boolTerm(ternary.Cond, env)
		if !ok {
			return "", false
		}
		then, ok := tr.constantStringEquality(ternary.Value, wanted, env, depth+1)
		if !ok {
			return "", false
		}
		otherwise, ok := tr.constantStringEquality(ternary.Alt, wanted, env, depth+1)
		if !ok {
			return "", false
		}
		return "(ite " + guard + " " + then + " " + otherwise + ")", true
	}
	match, ok := left.(*ast.MatchExpr)
	if !ok || !constantStringResult(match, depth) {
		return "", false
	}
	stringPattern := false
	for _, arm := range match.Arms[:len(match.Arms)-1] {
		if _, ok := arm.Pattern.(*ast.MatchStringLiteralPattern); ok {
			stringPattern = true
			break
		}
	}
	if stringPattern {
		if _, ok := tr.stringIDTerm(match.Value, env); !ok {
			return "", false
		}
	}
	last := match.Arms[len(match.Arms)-1].Body[0].(*ast.ExprStmt)
	result, ok := tr.constantStringEquality(last.Expr, wanted, env, depth+1)
	if !ok {
		return "", false
	}
	for i := len(match.Arms) - 2; i >= 0; i-- {
		arm := match.Arms[i]
		var selector ast.Expr
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchLiteralPattern:
			selector = pattern.Value
		case *ast.MatchStringLiteralPattern:
			selector = &ast.StringLit{Value: pattern.Value}
		default:
			return "", false
		}
		condition := &ast.BinaryExpr{Position: arm.Position, Op: lexer.TOKEN_EQEQ, Left: match.Value, Right: selector}
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

// constantStringProjection resolves only a literal string captured in a constructor
// expression already selected for this return proof. Nonliteral fields may have been
// captured before a later tuple/struct field mutates their source, so they stay opaque.
// This lets string-valued tuple/struct results use literal equality without rereading
// mutable storage or assuming a local's current value was its construction-time value.
func (tr *smtTranslator) constantStringProjection(field *ast.FieldExpr) (ast.Expr, bool) {
	if tr == nil || tr.a == nil || field == nil {
		return nil, false
	}
	var value ast.Expr
	var found bool
	if tr.resultFields != nil && tr.calleeBodyDepth == 0 {
		if id, ok := stripOptimizationParens(field.Object).(*ast.Ident); ok && id.Name == "result" {
			value, found = tr.resultFields[field.Field]
		}
	}
	if !found {
		if tuple, ok := stripOptimizationParens(field.Object).(*ast.TupleExpr); ok {
			value, found = tr.a.tupleLiteralFieldValue(tuple, field.Field)
		}
	}
	if !found {
		if _, nested := stripOptimizationParens(field.Object).(*ast.FieldExpr); nested {
			value, found = tr.a.nestedLiteralFieldValue(field, 0)
		}
	}
	if !found {
		return nil, false
	}
	_, literal := stripOptimizationParens(value).(*ast.StringLit)
	return value, literal
}

// exprIsCallFreeCondition reports whether a condition is built only from literals, identifiers,
// field reads and operators, so evaluating it again (in an obligation) cannot run a call or a
// side effect and reads the same values as the evaluation it stands for.
func exprIsCallFreeCondition(expr ast.Expr, depth int) bool {
	if depth > 32 {
		return false
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.BoolLit, *ast.IntLit, *ast.StringLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return n != nil && exprIsCallFreeCondition(n.Object, depth+1)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && (n.Op == lexer.TOKEN_NOT || n.Op == lexer.TOKEN_MINUS) && exprIsCallFreeCondition(n.Operand, depth+1)
	case *ast.BinaryExpr:
		// An overloaded operator lowers to a user call, which may have effects.
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR:
			return exprIsCallFreeCondition(n.Left, depth+1) && exprIsCallFreeCondition(n.Right, depth+1)
		}
	}
	return false
}
