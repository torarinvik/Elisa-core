package regionframesyntax

import "elisacore/src/ast"

func RegionForwardedParamPath(arg ast.Expr) (name string, isField bool, ok bool) {
	for {
		switch e := UnwrapRegionValue(arg).(type) {
		case *ast.AddrOfExpr:
			if e == nil {
				return "", false, false
			}
			arg = e.Operand
		case *ast.CastExpr:
			if e == nil {
				return "", false, false
			}
			arg = e.Operand
		default:
			goto inspectPath
		}
	}

inspectPath:
	current := UnwrapRegionValue(arg)
	for current != nil {
		switch e := current.(type) {
		case *ast.FieldExpr:
			if e == nil {
				return "", false, false
			}
			isField = true
			current = UnwrapRegionValue(e.Object)
		case *ast.IndexExpr:
			if e == nil {
				return "", false, false
			}
			current = UnwrapRegionValue(e.Object)
		case *ast.Ident:
			if e == nil || e.Name == "" {
				return "", false, false
			}
			return e.Name, isField, true
		default:
			return "", false, false
		}
	}
	return "", false, false
}

func ForwardsParamIdent(arg ast.Expr, name string) bool {
	for {
		switch e := UnwrapRegionValue(arg).(type) {
		case *ast.Ident:
			return e != nil && e.Name == name
		case *ast.AddrOfExpr:
			if e == nil {
				return false
			}
			arg = e.Operand
		case *ast.CastExpr:
			if e == nil {
				return false
			}
			arg = e.Operand
		default:
			return false
		}
	}
}

// TypeExprCarriesRegionParam reports whether a parameter type-expr is annotated with a region that is
// one of the callee's region params (so passing a value there REQUIRES supplying that region). Peels
// the mutable/owned/tail/ref qualifiers and checks the region annotation on the reference, the
// container builtin, or a generic type.
func TypeExprCarriesRegionParam(t ast.TypeExpr, regionParams map[string]bool) bool {
	for t != nil {
		switch n := t.(type) {
		case *ast.MutableType:
			t = n.Elem
		case *ast.OwnedType:
			t = n.Elem
		case *ast.TailType:
			t = n.Elem
		case *ast.RefType:
			if n.Region != "" && regionParams[n.Region] {
				return true
			}
			t = n.Elem
		case *ast.BuiltinTypeExpr:
			return n.Region != "" && regionParams[n.Region]
		case *ast.GenericType:
			return n.Region != "" && regionParams[n.Region]
		default:
			return false
		}
	}
	return false
}
