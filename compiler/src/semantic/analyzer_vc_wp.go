package semantic

import "elisacore/src/ast"

// Weakest-precondition transport over a straight-line scalar body (VC IR brick 3).
//
// The existing `ensure` discharge proves a postcondition by substituting `result` with the returned
// expression and assuming the defining equalities of IMMUTABLE locals. A MUTABLE local that is
// reassigned has no such equality, so a body like `y <- y + 1; y <- y * 2; return y` leaves `y` a free
// variable and the postcondition cannot be proven. WP closes this: starting from the postcondition, it
// substitutes each assignment's variable with its right-hand side in REVERSE order, threading every
// value back to the function's parameters, then discharges the resulting goal over the inputs.

// wpAssign is one captured straight-line scalar assignment `name := rhs`.
type wpAssign struct {
	name string
	rhs  ast.Expr
}

// captureStraightLineScalarBody returns the ordered scalar assignments leading up to (and excluding) the
// return, or ok=false if the body holds anything WP cannot account for: control flow, calls, a
// non-arithmetic right-hand side, or a non-identifier assignment target. Only `var name = e` decls and
// `name <- e` assignments with a pure-arithmetic `e` are captured; the return must be the final
// statement (so the captured prefix is the whole computation).
func (a *Analyzer) captureStraightLineScalarBody(ret *ast.ReturnStmt) ([]wpAssign, bool) {
	if a.currentFuncDecl == nil || ret == nil {
		return nil, false
	}
	var out []wpAssign
	for _, s := range a.currentFuncDecl.Body {
		if stmt, ok := s.(*ast.ReturnStmt); ok && stmt == ret {
			return out, true
		}
		switch n := s.(type) {
		case *ast.ContractStmt:
			// Leading contracts are lifted to the decl, but tolerate a stray one in the prefix.
		case *ast.VarDeclStmt:
			if n.Value == nil || !exprIsPureArith(n.Value) {
				return nil, false
			}
			out = append(out, wpAssign{name: n.Name, rhs: n.Value})
		case *ast.AssignStmt:
			id, ok := n.Target.(*ast.Ident)
			if n.Optional || !ok || id == nil || !exprIsPureArith(n.Value) {
				return nil, false
			}
			out = append(out, wpAssign{name: id.Name, rhs: n.Value})
		default:
			return nil, false
		}
	}
	return nil, false
}

// tryProveEnsureByWP discharges an `ensure` clause over a straight-line scalar body by weakest
// precondition. It substitutes `result` with the returned expression (AST level, since `result` is not
// a runtime binding here), lowers to the VC IR, then substitutes each assignment's variable with its
// right-hand side in reverse — folding as it goes — and discharges the resulting goal over the
// parameters against the standard hypotheses (the function's `requires`). Returns true only on a real
// proof; any sub-step outside the structural fragment declines (sound: WP only ever forgoes a proof).
func (a *Analyzer) tryProveEnsureByWP(clause ast.Expr, ret *ast.ReturnStmt) bool {
	if clause == nil || ret == nil || ret.Value == nil {
		return false
	}
	assigns, ok := a.captureStraightLineScalarBody(ret)
	if !ok {
		return false
	}
	substituted, ok := substituteLemmaEnsure(clause, map[string]ast.Expr{"result": ret.Value})
	if !ok {
		return false
	}
	tr := a.newSMTTranslator(nil)
	goal, ok := tr.lowerVCFormula(substituted, nil)
	if !ok || !vcFormulaFullyStructural(goal) {
		return false
	}
	for i := len(assigns) - 1; i >= 0; i-- {
		rhsTerm, ok := tr.lowerVCTerm(assigns[i].rhs, nil)
		if !ok || !vcTermFullyStructural(rhsTerm) {
			return false
		}
		goal = substVCFormula(goal, assigns[i].name, rhsTerm)
	}
	if isVCTrue(goal) {
		return true
	}
	if isVCFalse(goal) {
		return false
	}
	proven, _ := a.smtCheckVC(tr, emitVCFormula(goal), "")
	return proven
}
