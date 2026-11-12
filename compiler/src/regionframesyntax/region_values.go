package regionframesyntax

import (
	"elisacore/src/ast"
	"strings"
)

func SimpleTypeNameSegment(n string) string {
	if i := strings.LastIndex(n, "::"); i >= 0 {
		n = n[i+2:]
	}
	if i := strings.LastIndex(n, "."); i >= 0 {
		n = n[i+1:]
	}
	return n
}

func UnwrapRegionValue(value ast.Expr) ast.Expr {
	for {
		switch wrapped := value.(type) {
		case *ast.ParenExpr:
			if wrapped == nil {
				return value
			}
			value = wrapped.Inner
		case *ast.CanExpr:
			if wrapped == nil {
				return value
			}
			value = wrapped.Expr
		case *ast.MoveExpr:
			if wrapped == nil {
				return value
			}
			// Moving transfers ownership without changing the backing arena.
			// A moved local return must adopt exactly as its value would.
			value = wrapped.Operand
		default:
			return value
		}
	}
}
