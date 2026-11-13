package semantic

import (
	"elisacore/src/flowshape"
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
			shape := flowshape.ReturnShape(exit.value)
			shapes[shape] = true
			if flowshape.ShapeIsIntLiteral(shape) {
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
