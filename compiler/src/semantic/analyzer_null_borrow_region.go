package semantic

import (
	"elisacore/src/ast"
	"strings"
)

// nullBorrowRegionArgBinding binds a region parameter that only optional-borrow arguments
// carry, when every one of those arguments is `null`: `enqueue(x, null)` against
// `def enqueue(x: usize, edges: mutable darray[usize]&? @r)`. A null borrow has no container
// to grow, so the callee never allocates into the region; `perm` (program lifetime) is a sound
// binding that outlives everything. A return type mentioning the region keeps the call
// unbound: the result could then borrow from storage the caller must name.
func (a *Analyzer) nullBorrowRegionArgBinding(ft *FuncType, args []ast.Expr, name string) (string, bool) {
	if a == nil || ft == nil || strings.Contains(typeString(ft.Return), "@"+name) {
		return "", false
	}
	carriers := 0
	for i, parameterType := range ft.Params {
		if !typeCarriesRegionName(parameterType, name) {
			continue
		}
		if i >= len(args) {
			return "", false
		}
		if _, isNull := stripParenExpr(args[i]).(*ast.NullLit); !isNull {
			return "", false
		}
		carriers++
	}
	return "perm", carriers > 0
}

// typeCarriesRegionName reports whether NAME is the region of T, of a reference T wraps, or of
// the container behind it, looking through optional and reference wrappers. The printed type
// omits a region stamped on the container inside an optional borrow.
func typeCarriesRegionName(t Type, name string) bool {
	for t != nil {
		if strings.Contains(typeString(t), "@"+name) || containerRegion(t) == name {
			return true
		}
		switch wrapped := t.(type) {
		case *OptionalType:
			t = wrapped.Value
		case *RefType:
			if wrapped.Region == name {
				return true
			}
			t = wrapped.Elem
		default:
			return false
		}
	}
	return false
}
