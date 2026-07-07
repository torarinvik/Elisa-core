package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// R4 — exit budget (docs/121 §3-R4). A loop that leaves through more than two structurally
// different results — return a `Token(…)` here, a bare `-1` there, a `0` somewhere else — is an
// unwritten sum type: the caller has to reconstruct which case happened from the shape of what
// came back. The fix is to make the cases explicit (`T or ErrorSet` + `raise`), so every exit
// either builds the same value or is a typed error.
//
// `raise` never counts — error unions already force the caller to handle it, so an error exit is
// not an untyped sentinel. Only the shapes of `return`/`break` values are counted; all-same-shape
// (every exit builds `Token(FString, …)`) passes at any count. `break` carries no value in this
// AST, so it is its own single shape.
func (a *Analyzer) checkFlowExitBudget(info *loopFlowInfo) {
	if info == nil {
		return
	}
	shapes := map[string]bool{}
	intLiteralShapes := map[string]bool{}
	for _, exit := range info.exits {
		switch exit.kind {
		case flowExitRaise:
			continue // exempt — a typed error, not a sentinel
		case flowExitBreak:
			shapes["break"] = true
		case flowExitReturn:
			shape := flowReturnShape(exit.value)
			shapes[shape] = true
			if flowShapeIsIntLiteral(shape) {
				intLiteralShapes[shape] = true
			}
		}
	}
	if len(shapes) <= 2 {
		return // two or fewer distinct exit shapes reads fine
	}
	a.flowLint(info.pos, "%s", flowExitBudgetMessage(len(intLiteralShapes) >= 2))
}

func flowExitBudgetMessage(sentinelHeavy bool) string {
	base := "flow warning [-Wflow]: this loop exits with more than two structurally different " +
		"results — an untyped sum type the caller must decode from the value's shape. Make the " +
		"cases explicit so every exit builds one result or raises a typed error:\n" +
		"    def scan(…) -> Token or ScanError:\n" +
		"        …\n" +
		"        raise ScanError.Unterminated   # instead of a sentinel return\n"
	if sentinelHeavy {
		base += "  Two of the exits are distinct integer sentinels (`-1` vs `0`) — return " +
			"`T or ErrorSet` and `raise` for the error case rather than magic numbers.\n"
	}
	base += "  To keep the multiple shapes, wrap the loop in `can ComplexFlow:`."
	return base
}

// flowReturnShape maps a returned expression to a stable shape key. Two returns share a shape iff
// they build the same kind of result: the same constructor name, the same integer/char literal
// value (so `-1` and `0` are distinct sentinels), a bool literal, or a plain binding (returning
// different variables is not the mess R4 targets, so all idents collapse to one shape).
func flowReturnShape(value ast.Expr) string {
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

func flowShapeIsIntLiteral(shape string) bool {
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
