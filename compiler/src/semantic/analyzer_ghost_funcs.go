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
	if a.functionPureEquationShape(fn) {
		return
	}
	// A defining equation needs an SMT-modelable signature and body (integer/bool/sview values,
	// no slices or calls to unmodeled functions). A ghost outside that fragment gets no defining
	// equation, but it must still be pure and total: its return tree may only call ghosts or
	// other pure-total functions.
	if a.ghostBodyPureTotal(fn, map[*ast.FuncDecl]bool{}) {
		return
	}
	a.errorf(fn.Pos(), "ghost function %q must be pure and total so its defining equation can be used by the prover", fn.Name)
}

// ghostBodyPureTotal is the type-independent purity/totality gate: the body is a pure return tree
// (no mutation, loops or effectful statements), the function declares no effects and no mutable
// parameter, and every call goes to a ghost or to a function that passes this same gate.
func (a *Analyzer) ghostBodyPureTotal(fn *ast.FuncDecl, inProgress map[*ast.FuncDecl]bool) bool {
	if fn == nil || fn.IsLemma || fn.DecreasesWild != "" || len(fn.Permissions) != 0 {
		return false
	}
	if inProgress[fn] {
		return true
	}
	for _, param := range fn.Params {
		if param.Mutable || a.paramIsMutable(param) {
			return false
		}
	}
	body, ok := pureReturnExpr(fn)
	work := pureReturnMaxWork
	if !ok || !pureReturnExpressionShapeWithinBudget(body, &work, 0) {
		return false
	}
	inProgress[fn] = true
	defer delete(inProgress, fn)
	pure := true
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: body}}, func(node any) {
		call, isCall := node.(*ast.CallExpr)
		if !pure || !isCall || call == nil || a.ghostCallIsValueConversion(call) {
			return
		}
		callee, ok := a.resolveDirectCallFuncDecl(call)
		switch {
		case !ok || callee == nil:
			pure = false
		case callee.IsGhost:
			// Validated on its own; recursion terminates through its `decreases` check.
		case !a.functionPureEquationShape(callee) && !a.ghostBodyPureTotal(callee, inProgress):
			pure = false
		}
	})
	return pure
}

// ghostCallIsValueConversion admits the builtin, total scalar conversions `x.u64()`.
func (a *Analyzer) ghostCallIsValueConversion(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || len(call.Args) != 0 {
		return false
	}
	switch field.Field {
	case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize":
	default:
		return false
	}
	t, known := a.exprTypes[field.Object]
	if !known || t == nil {
		return false
	}
	t = stripRefForBounds(t)
	return IsNumericType(t) && !IsFloatType(t)
}
