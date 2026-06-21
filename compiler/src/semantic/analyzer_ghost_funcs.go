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
	if _, ok := pureReturnExpr(fn); !ok {
		a.errorf(fn.Pos(), "ghost function %q body must be a pure total return tree (`return` or exhaustive `if`/`else` returns), with no mutation, loops, or effectful statements", fn.Name)
		return
	}
	if !a.functionDefiningEquationEligible(fn) {
		a.errorf(fn.Pos(), "ghost function %q must be pure and total so its defining equation can be used by the prover", fn.Name)
	}
}
