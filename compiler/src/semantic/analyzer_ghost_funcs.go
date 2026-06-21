package semantic

import "elisacore/src/ast"

func (a *Analyzer) validateGhostFuncDecl(fn *ast.FuncDecl) {
	if a == nil || fn == nil || !fn.IsGhost {
		return
	}
	if fn.Static {
		a.errorf(fn.Pos(), "`ghost def` cannot be `static`")
	}
	if fn.ReturnType == nil {
		a.errorf(fn.Pos(), "ghost function %q must declare a return type", fn.Name)
	}
	if len(fn.Permissions) != 0 {
		a.errorf(fn.Pos(), "ghost function %q cannot declare runtime effects with `can[...]`", fn.Name)
	}
	if len(fn.Changes) != 0 || len(fn.Preserves) != 0 || len(fn.Fulfills) != 0 {
		a.errorf(fn.Pos(), "ghost function %q cannot declare frame effects (`changes`, `preserves`, or `fulfills`)", fn.Name)
	}
	// A bool predicate defined by structural recursion over a recursive datatype (`match t: ...`) is a
	// valid ghost spec function even though it is not an integer pure-return tree: the prover reasons
	// about it via the structural-induction schema (analyzer_structural_induction.go), not the numeric
	// defining equation. Accept it here so its body validates.
	if a.boolPredicateEligible(fn) {
		return
	}
	if _, ok := pureReturnExpr(fn); !ok {
		a.errorf(fn.Pos(), "ghost function %q body must be a pure total return tree (`return` or exhaustive `if`/`else` returns), with no mutation, loops, or effectful statements", fn.Name)
		return
	}
	if !a.functionPureEquationShape(fn) {
		a.errorf(fn.Pos(), "ghost function %q must be pure and total so its defining equation can be used by the prover", fn.Name)
	}
}
