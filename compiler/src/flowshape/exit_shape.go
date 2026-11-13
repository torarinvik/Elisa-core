package flowshape

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// ReturnShape maps a returned expression to a stable shape key. Two returns share a shape iff
// they build the same kind of result: the same constructor name, the same integer/char literal
// value (so `-1` and `0` are distinct sentinels), a bool literal, or a plain binding (returning
// different variables is not the mess R4 targets, so all idents collapse to one shape).
func ReturnShape(value ast.Expr) string {
	switch e := value.(type) {
	case nil:
		return "void-return"
	case *ast.CallExpr:
		if name := flowCallHeadName(e); name != "" {
			return "ctor:" + name
		}
		return "call"
	case *ast.IntLit:
		return "int:" + e.Value
	case *ast.CharLit:
		return "char:" + e.Value
	case *ast.BoolLit:
		if e.Value {
			return "bool:true"
		}
		return "bool:false"
	case *ast.UnaryExpr:
		// A signed literal (`-1`) is a distinct sentinel from its positive form.
		if e.Op == lexer.TOKEN_MINUS {
			if inner, ok := e.Operand.(*ast.IntLit); ok {
				return "int:-" + inner.Value
			}
		}
		return "unary"
	case *ast.Ident:
		return "binding"
	case *ast.NullLit:
		return "null"
	}
	return "other"
}

func ShapeIsIntLiteral(shape string) bool {
	return len(shape) >= 4 && shape[:4] == "int:"
}

// flowCallHeadName returns the constructor/function name a call targets, for `Token(…)` /
// `Geo::Point(…)` / `x.wrap()` result shaping. Empty when the callee is not a simple name.
func flowCallHeadName(call *ast.CallExpr) string {
	switch fn := call.Func.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.FieldExpr:
		return fn.Field
	}
	return ""
}
