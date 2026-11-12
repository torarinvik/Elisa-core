package regionframesyntax

import "elisacore/src/ast"

func FieldPathLValue(expr ast.Expr, separator string) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		// A `?.` safe-navigation field is NOT a stable lvalue store base (the object may be null), so
		// reject it; only an unconditional `.field` projection of a stable path is admissible.
		if n == nil || n.Safe {
			return "", false
		}
		base, ok := FieldPathLValue(n.Object, separator)
		if !ok {
			return "", false
		}
		return base + separator + n.Field, true
	default:
		return "", false
	}
}
