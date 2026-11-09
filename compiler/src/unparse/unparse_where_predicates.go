package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func formatWhereViewExpr(expr *ast.CallExpr) (string, bool) {
	if expr == nil || len(expr.Args) != 2 || len(expr.ArgNames) != 0 || expr.HasArgForward {
		return "", false
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "where" {
		return "", false
	}
	lambda, ok := expr.Args[1].(*ast.LambdaExpr)
	if !ok || lambda == nil || !lambda.UsesShorthandParams || len(lambda.Params) != 1 || lambda.BodyExpr == nil || len(lambda.Body) != 0 {
		return "", false
	}
	paramName := lambda.Params[0].Name
	if pattern, filter, ok := wherePatternPredicate(lambda.BodyExpr, paramName); ok {
		text := formatExpr(expr.Args[0]) + " where " + formatMatchPattern(pattern)
		if filter != nil {
			text += ": " + formatExpr(filter)
		}
		return text, true
	}
	if pattern, filter, ok := whereTupleSubjectPatternPredicate(lambda.BodyExpr, paramName); ok {
		text := formatExpr(expr.Args[0]) + " where index, value is " + formatMatchPattern(pattern)
		if filter != nil {
			text += ": " + formatWhereTuplePredicateExpr(filter, paramName)
		}
		return text, true
	}
	if paramName == "__where_item" {
		return formatExpr(expr.Args[0]) + " where index, value: " + formatWhereTuplePredicateExpr(lambda.BodyExpr, paramName), true
	}
	return formatExpr(expr.Args[0]) + " where " + paramName + ": " + formatExpr(lambda.BodyExpr), true
}

func wherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	if pattern, ok := directWherePatternPredicate(expr, paramName); ok {
		return pattern, nil, true
	}
	if pattern, filter, ok := matchWherePatternPredicate(expr, paramName); ok {
		return pattern, filter, true
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil && binary.Op == lexer.TOKEN_AND {
		if pattern, ok := directWherePatternPredicate(binary.Left, paramName); ok {
			return pattern, binary.Right, true
		}
	}
	return nil, nil, false
}

func directWherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil || binary.Op != lexer.TOKEN_IS {
		return nil, false
	}
	ident, ok := binary.Left.(*ast.Ident)
	if !ok || ident == nil || ident.Name != paramName {
		return nil, false
	}
	switch target := binary.Right.(type) {
	case *ast.VariantTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	case *ast.StructTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	}
	return nil, false
}

func matchWherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	matchExpr, ok := expr.(*ast.MatchExpr)
	if !ok || matchExpr == nil || len(matchExpr.Arms) != 2 || matchExpr.Store != nil {
		return nil, nil, false
	}
	ident, ok := matchExpr.Value.(*ast.Ident)
	if !ok || ident == nil || ident.Name != paramName {
		return nil, nil, false
	}
	if _, ok := matchExpr.Arms[1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return nil, nil, false
	}
	if len(matchExpr.Arms[0].Body) != 1 || len(matchExpr.Arms[1].Body) != 1 {
		return nil, nil, false
	}
	filterStmt, ok := matchExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok || filterStmt == nil {
		return nil, nil, false
	}
	fallbackStmt, ok := matchExpr.Arms[1].Body[0].(*ast.ExprStmt)
	if !ok || fallbackStmt == nil {
		return nil, nil, false
	}
	fallback, ok := fallbackStmt.Expr.(*ast.BoolLit)
	if !ok || fallback == nil || fallback.Value {
		return nil, nil, false
	}
	switch matchExpr.Arms[0].Pattern.(type) {
	case *ast.MatchVariantPattern, *ast.MatchStructPattern:
		return matchExpr.Arms[0].Pattern, filterStmt.Expr, true
	default:
		return nil, nil, false
	}
}

func whereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	if pattern, ok := directWhereTupleSubjectPatternPredicate(expr, paramName); ok {
		return pattern, nil, true
	}
	if pattern, filter, ok := matchWhereTupleSubjectPatternPredicate(expr, paramName); ok {
		return pattern, filter, true
	}
	return nil, nil, false
}

func directWhereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil || binary.Op != lexer.TOKEN_IS {
		return nil, false
	}
	if !isWhereTupleValueField(binary.Left, paramName) {
		return nil, false
	}
	switch target := binary.Right.(type) {
	case *ast.VariantTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	case *ast.StructTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	}
	return nil, false
}

func matchWhereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	matchExpr, ok := expr.(*ast.MatchExpr)
	if !ok || matchExpr == nil || len(matchExpr.Arms) != 2 || matchExpr.Store != nil {
		return nil, nil, false
	}
	if !isWhereTupleValueField(matchExpr.Value, paramName) {
		return nil, nil, false
	}
	if _, ok := matchExpr.Arms[1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return nil, nil, false
	}
	if len(matchExpr.Arms[0].Body) != 1 || len(matchExpr.Arms[1].Body) != 1 {
		return nil, nil, false
	}
	filterStmt, ok := matchExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok || filterStmt == nil {
		return nil, nil, false
	}
	fallbackStmt, ok := matchExpr.Arms[1].Body[0].(*ast.ExprStmt)
	if !ok || fallbackStmt == nil {
		return nil, nil, false
	}
	fallback, ok := fallbackStmt.Expr.(*ast.BoolLit)
	if !ok || fallback == nil || fallback.Value {
		return nil, nil, false
	}
	switch matchExpr.Arms[0].Pattern.(type) {
	case *ast.MatchVariantPattern, *ast.MatchStructPattern:
		return matchExpr.Arms[0].Pattern, filterStmt.Expr, true
	default:
		return nil, nil, false
	}
}

func isWhereTupleValueField(expr ast.Expr, paramName string) bool {
	field, ok := expr.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "value" {
		return false
	}
	ident, ok := field.Object.(*ast.Ident)
	return ok && ident != nil && ident.Name == paramName
}

func formatWhereTuplePredicateExpr(expr ast.Expr, tupleName string) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		if ident, ok := n.Object.(*ast.Ident); ok && ident != nil && ident.Name == tupleName {
			switch n.Field {
			case "index":
				return "index"
			case "value":
				return "value"
			}
		}
		return formatWhereTuplePredicateExpr(n.Object, tupleName) + "." + n.Field
	case *ast.BinaryExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Left, tupleName) + " " + lexer.TokenName(n.Op) + " " + formatWhereTuplePredicateExpr(n.Right, tupleName) + ")"
	case *ast.UnaryExpr:
		op := lexer.TokenName(n.Op)
		if op == "not" {
			return "(not " + formatWhereTuplePredicateExpr(n.Operand, tupleName) + ")"
		}
		return "(" + op + formatWhereTuplePredicateExpr(n.Operand, tupleName) + ")"
	case *ast.CallExpr:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, formatWhereTuplePredicateExpr(arg, tupleName))
		}
		return formatWhereTuplePredicateExpr(n.Func, tupleName) + "(" + strings.Join(parts, ", ") + ")"
	case *ast.ParenExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Inner, tupleName) + ")"
	case *ast.IndexExpr:
		text := formatWhereTuplePredicateExpr(n.Object, tupleName) + "[" + formatWhereTuplePredicateExpr(n.Index, tupleName) + "]"
		if n.Fallback != nil {
			text += " else " + formatWhereTuplePredicateExpr(n.Fallback, tupleName)
		}
		return text
	case *ast.TernaryExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Value, tupleName) + " if " + formatWhereTuplePredicateExpr(n.Cond, tupleName) + " else " + formatWhereTuplePredicateExpr(n.Alt, tupleName) + ")"
	default:
		return formatExpr(expr)
	}
}

func isRefCastTarget(t ast.TypeExpr) bool {
	switch n := t.(type) {
	case *ast.RefType:
		return true
	case *ast.MutableType:
		return isRefCastTarget(n.Elem)
	default:
		return false
	}
}
func formatStructLiteralTypeName(name string, typeArgs []ast.TypeExpr) string {
	// A struct literal's type name is a module path plus a struct: the parser only
	// accepts `::` in it (`Pack.Item{...}` does not parse), so every dot in the
	// flattened key came from `::`.
	spelled := ast.ModulePathSpelling(name)
	if len(typeArgs) == 0 {
		return spelled
	}
	parts := make([]string, 0, len(typeArgs))
	for _, arg := range typeArgs {
		parts = append(parts, formatTypeExpr(arg))
	}
	return spelled + "[" + strings.Join(parts, ", ") + "]"
}
func formatNamedExprField(name string, value ast.Expr, separator string) string {
	if name == "" {
		return formatExpr(value)
	}
	if ident, ok := value.(*ast.Ident); ok && ident != nil && ident.Name == name {
		return name
	}
	return name + separator + formatExpr(value)
}
func formatStructLiteralField(name string, value ast.Expr, brace bool) string {
	if name == "" {
		return formatExpr(value)
	}
	// Brace shorthand `Foo{x}` (no colon) is kept; the paren colon-pun `Foo(x:)`
	// has been removed, so paren fields always render explicitly as `name: value`.
	if ident, ok := value.(*ast.Ident); ok && ident != nil && ident.Name == name && brace {
		return name
	}
	return name + ": " + formatExpr(value)
}
func formatMatchPatternField(arg ast.MatchPatternArg, brace bool) string {
	if arg.Name == "" {
		return ""
	}
	if brace {
		if bind, ok := arg.Pattern.(*ast.MatchBindPattern); ok && bind != nil && bind.Name == arg.Name {
			return arg.Name
		}
	}
	return arg.Name + ": " + formatMatchPattern(arg.Pattern)
}
