package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/constantstrings"
	"elisacore/src/lexer"
	"strings"
)

type smtConstantStringBinding struct {
	expr ast.Expr
	env  map[string]string
}

func constantStringResult(expr ast.Expr, depth int) bool {
	return constantstrings.Result(expr, depth)
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
