package semantic

import "elisacore/src/ast"

// recordUpdatePreservesBacking accepts only replacements known not to introduce
// a different backing region. Nested updates must be rooted at the very field
// being replaced; an unrelated aggregate can carry shorter-lived containers.
func (a *Analyzer) recordUpdatePreservesBacking(update *ast.RecordUpdateExpr, depth int) bool {
	if update == nil || depth >= 64 || len(update.Args) != len(update.ArgNames) {
		return false
	}
	if !recordUpdateSamePath(update.Base, update.Base, 0) {
		return false
	}
	for index, arg := range update.Args {
		if arg == nil {
			return false
		}
		typ := a.exprType(arg)
		if builtin, ok := typ.(*BuiltinType); ok && builtin != nil && isScalarBuiltinName(builtin.Name) && recordUpdateStableScalar(arg, 0) {
			continue
		}
		if _, ok := typ.(*BitIntType); ok && recordUpdateStableScalar(arg, 0) {
			continue
		}
		nested, ok := recordUpdateUnwrapParen(arg).(*ast.RecordUpdateExpr)
		if !ok || nested == nil || update.ArgNames[index] == "" {
			return false
		}
		field, ok := recordUpdateUnwrapParen(nested.Base).(*ast.FieldExpr)
		if !ok || field == nil || field.Safe || field.Field != update.ArgNames[index] || !recordUpdateSamePath(field.Object, update.Base, 0) {
			return false
		}
		if !a.recordUpdatePreservesBacking(nested, depth+1) {
			return false
		}
	}
	return true
}

// Only side-effect-free identifier and field paths can establish that the
// nested update copied the same base. Do not compare call expressions by text
// or source position: repeated calls can return different owners.
func recordUpdateSamePath(left, right ast.Expr, depth int) bool {
	if left == nil || right == nil || depth >= 64 {
		return false
	}
	left = recordUpdateUnwrapParen(left)
	right = recordUpdateUnwrapParen(right)
	switch l := left.(type) {
	case *ast.Ident:
		r, ok := right.(*ast.Ident)
		return ok && l != nil && r != nil && l.Name != "" && l.Name == r.Name
	case *ast.FieldExpr:
		r, ok := right.(*ast.FieldExpr)
		return ok && l != nil && r != nil && !l.Safe && !r.Safe && l.Field != "" && l.Field == r.Field && recordUpdateSamePath(l.Object, r.Object, depth+1)
	default:
		return false
	}
}

// A move or effect-wrapped expression is not interchangeable with evaluating a
// plain path. Unwrap grouping only when proving record-update identity.
func recordUpdateUnwrapParen(value ast.Expr) ast.Expr {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok || paren == nil {
			return value
		}
		value = paren.Inner
	}
}

// A scalar-producing call can still mutate the base while the update is
// evaluated. Only expressions with no calls, moves, or overloaded operators
// establish that the copied backing stayed unchanged.
func recordUpdateStableScalar(value ast.Expr, depth int) bool {
	if value == nil || depth >= 64 {
		return false
	}
	value = recordUpdateUnwrapParen(value)
	switch expr := value.(type) {
	case *ast.Ident:
		return expr != nil
	case *ast.FieldExpr:
		return expr != nil && recordUpdateSamePath(expr, expr, 0)
	case *ast.IntLit:
		return expr != nil
	case *ast.FloatLit:
		return expr != nil
	case *ast.CharLit:
		return expr != nil
	case *ast.BoolLit:
		return expr != nil
	case *ast.UnaryExpr:
		return expr != nil && expr.LoweredCall == nil && recordUpdateStableScalar(expr.Operand, depth+1)
	case *ast.BinaryExpr:
		return expr != nil && expr.LoweredCall == nil && recordUpdateStableScalar(expr.Left, depth+1) && recordUpdateStableScalar(expr.Right, depth+1)
	default:
		return false
	}
}
