package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// exprIsPureArith reports whether expr is a side-effect-free integer arithmetic expression over
// literals and variables — the only right-hand sides captureLoopBodyEffect admits. Calls,
// address-of, and container reads are rejected (default false) so capture never silently admits a
// side effect. A value-form numeric WIDTH conversion of an already-pure operand (`i.usize()`,
// `n.u64()`, `x.i64()`) IS admitted: such a conversion is a total, side-effect-free integer function
// of its operand, so substituting it into the loop measure is sound. A `.cast[T]` REINTERPRET, a
// reference cast, a `?`-suffixed/optional conversion, a non-integer conversion (`.f64()`, `.bool()`),
// and a conversion of a non-pure operand are all still rejected — the SMT term translator independently
// models the surviving conversions value-exactly (and declines, forcing the whole proof to decline,
// for any conversion it cannot model faithfully), so admitting them here can never make the prover
// "prove" a false decrease.
func exprIsPureArith(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return exprIsPureArith(n.Inner)
	case *ast.IntLit:
		return true
	case *ast.Ident:
		return true
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && exprIsPureArith(n.Operand)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			// Bitwise operators are pure too; the term translator models them exactly or mints an
			// unconstrained value (which only declines a proof).
			return exprIsPureArith(n.Left) && exprIsPureArith(n.Right)
		}
		return false
	case *ast.CastExpr:
		return n != nil && isPureNumericValueCast(n) && exprIsPureArith(n.Operand)
	default:
		return false
	}
}

// isPureNumericValueCast reports whether a CastExpr is a value-form NUMERIC INTEGER conversion (the
// `x.u64()` / `x.usize()` postfix shorthand), as opposed to a `.cast[T]` reinterpret, a reference
// cast, an indirect-call cast, or a float/non-numeric conversion. Only the postfix-shorthand origin
// with a bare integer target name qualifies — that is precisely the value-preserving-or-modeled
// conversion family the SMT term translator handles (see analyzer_smt_discharge.go CastExpr term
// case). Everything else returns false (rejected), so capture never admits an unmodeled cast.
func isPureNumericValueCast(n *ast.CastExpr) bool {
	if n == nil || n.RefShorthand || n.Origin != ast.CastExprOriginPostfixShorthand {
		return false
	}
	named, ok := n.Target.(*ast.NamedType)
	if !ok || named == nil {
		return false // optional (`x.u64?()`), ref-suffixed, or compound targets are not plain value casts
	}
	return isIntegerConversionTargetName(named.Name)
}

// isIntegerConversionTargetName reports whether a postfix-shorthand cast target names a built-in
// INTEGER type (the only conversions the integer measure prover can reason about). Float, bool,
// char, string-view, and user-defined targets are excluded.
func isIntegerConversionTargetName(name string) bool {
	switch name {
	case "int",
		"i8", "i16", "i32", "i64", "isize",
		"s8", "s16", "s32", "s64",
		"u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	}
	return false
}

// exprIsPureArithOrRead is the relaxed purity predicate used ONLY for the index/value of an
// array-element store on the loop-TERMINATION capture path. In addition to everything
// exprIsPureArith admits, it allows ARRAY-ELEMENT READS (`arr[j+1]`) and FIELD READS (`p.x`), which
// are side-effect-free. This is sound for termination specifically because the loop measure is over
// SCALAR counters and an array store mutates no scalar — so the store (and whatever it reads) is
// irrelevant to the scalar-measure decrease proof; the read merely needs to be guaranteed
// effect-free. Method/overlay CALLS are NOT effect-free (could mutate a counter or diverge), so an
// IndexExpr carrying an analyzer-rewritten overlay/specialization/fallback/two-operand form, or any
// CallExpr, is still rejected.
func exprIsPureArithOrRead(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return exprIsPureArithOrRead(n.Inner)
	case *ast.IntLit:
		return true
	case *ast.Ident:
		return true
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && exprIsPureArithOrRead(n.Operand)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			return exprIsPureArithOrRead(n.Left) && exprIsPureArithOrRead(n.Right)
		}
		return false
	case *ast.CastExpr:
		return n != nil && isPureNumericValueCast(n) && exprIsPureArithOrRead(n.Operand)
	case *ast.IndexExpr:
		// A plain single-subscript array read. Reject any analyzer-rewritten / sugared form that lowers
		// to a CALL or a non-read (overlay accessor, value specialization, `else` fallback, two-operand
		// table accessor) — those are not guaranteed side-effect-free.
		if n == nil || n.Index2 != nil || n.Fallback != nil || n.AsSpecialize != nil || n.AsOverlayCall != nil {
			return false
		}
		return exprIsPureArithOrRead(n.Object) && exprIsPureArithOrRead(n.Index)
	case *ast.FieldExpr:
		// A field projection read. Safe (`?.`) navigation is still a pure read of a field; reject nothing
		// extra here, but the object must itself be pure (no call/address-of underneath).
		return n != nil && exprIsPureArithOrRead(n.Object)
	default:
		return false
	}
}

// collectArithIdents gathers the identifier names referenced by a pure-arithmetic expression. It
// assumes exprIsPureArith(expr) already held, so the grammar it walks is complete.
func collectArithIdents(expr ast.Expr, out map[string]bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		collectArithIdents(n.Inner, out)
	case *ast.Ident:
		out[n.Name] = true
	case *ast.UnaryExpr:
		collectArithIdents(n.Operand, out)
	case *ast.BinaryExpr:
		collectArithIdents(n.Left, out)
		collectArithIdents(n.Right, out)
	case *ast.CastExpr:
		collectArithIdents(n.Operand, out)
	case *ast.IndexExpr:
		// Reached only on the termination capture path (exprIsPureArithOrRead): the array base and the
		// index both contribute referenced names.
		collectArithIdents(n.Object, out)
		collectArithIdents(n.Index, out)
	case *ast.FieldExpr:
		collectArithIdents(n.Object, out)
	}
}

// outerLoopInvariantsDisjointFrom filters outer proven loop invariants to those whose dependency
// variables are disjoint from the inner loop's assigned variables (the keys of subst). This is the
// conservative soundness gate: if the inner loop body writes ANY variable that appears in an outer
// invariant, that invariant is excluded — it may have been invalidated by the write. Only invariants
// whose every referenced name is untouched by the inner body are admitted as additional hypotheses.
//
// Empty subst (inner body not captured) returns nil — no outer facts are admitted for an inner loop
// whose body cannot be fully modeled, since we have no proof the inner body doesn't mutate the
// outer invariant's variables.
func outerLoopInvariantsDisjointFrom(outers []*ast.ContractStmt, innerSubst map[string]ast.Expr) []*ast.ContractStmt {
	if len(outers) == 0 || len(innerSubst) == 0 {
		// len(innerSubst)==0 means capture failed (no body effect recorded); decline all outer facts
		// since we cannot verify disjointness without knowing what the inner body writes.
		return nil
	}
	var out []*ast.ContractStmt
	for _, inv := range outers {
		if inv == nil || inv.Cond == nil {
			continue
		}
		deps := smtFactDeps(inv.Cond)
		disjoint := true
		for name := range deps {
			if _, mutated := innerSubst[name]; mutated {
				disjoint = false
				break
			}
		}
		if disjoint {
			out = append(out, inv)
		}
	}
	return out
}

// loopTerminationCallAdmissible admits, on the loop-TERMINATION capture path only, a value that
// calls functions, when no call can write a measure variable:
//
//   - every measure root (focus) is a local or parameter whose type is not a reference, so it has
//     no alias and a callee can reach it only through an argument; and
//   - every call is a direct call of a named function, no argument passes a measure root as a
//     place to a mutable/linear reference parameter, and every argument satisfies this predicate.
//
// The value itself is never modeled (its target is ignored or a body local), so this only claims
// "the measure variables are untouched by this statement" — never anything about its result.
func (a *Analyzer) loopTerminationCallAdmissible(expr ast.Expr, focus map[string]bool) bool {
	if a == nil || len(focus) == 0 || !a.loopTerminationFocusUnaliasable(focus) {
		return false
	}
	return a.loopTerminationValueAdmissible(expr, focus)
}

func (a *Analyzer) loopTerminationFocusUnaliasable(focus map[string]bool) bool {
	if a.currentScope == nil {
		return false
	}
	for name := range focus {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil {
			return false
		}
		if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
			return false
		}
		if sym.Type == nil {
			return false
		}
		if _, isRef := StripAggregateStateType(sym.Type).(*RefType); isRef {
			return false
		}
	}
	return true
}

func (a *Analyzer) loopTerminationValueAdmissible(expr ast.Expr, focus map[string]bool) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit, *ast.Ident:
		return true
	case *ast.ParenExpr:
		return n != nil && a.loopTerminationValueAdmissible(n.Inner, focus)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && a.loopTerminationValueAdmissible(n.Object, focus)
	case *ast.IndexExpr:
		return n != nil &&
			n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback &&
			n.AsSpecialize == nil && n.AsOverlayCall == nil &&
			a.loopTerminationValueAdmissible(n.Object, focus) && a.loopTerminationValueAdmissible(n.Index, focus)
	case *ast.SliceExpr:
		return n != nil &&
			a.loopTerminationValueAdmissible(n.Object, focus) &&
			(n.Start == nil || a.loopTerminationValueAdmissible(n.Start, focus)) &&
			(n.End == nil || a.loopTerminationValueAdmissible(n.End, focus))
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && a.loopTerminationValueAdmissible(n.Operand, focus)
	case *ast.BinaryExpr:
		return n != nil && a.loopTerminationValueAdmissible(n.Left, focus) && a.loopTerminationValueAdmissible(n.Right, focus)
	case *ast.CastExpr:
		return n != nil && a.loopTerminationValueAdmissible(n.Operand, focus)
	case *ast.TernaryExpr:
		return n != nil &&
			a.loopTerminationValueAdmissible(n.Cond, focus) &&
			a.loopTerminationValueAdmissible(n.Value, focus) &&
			a.loopTerminationValueAdmissible(n.Alt, focus)
	case *ast.CallExpr:
		if n == nil || n.Func == nil {
			return false
		}
		if _, ok := n.Func.(*ast.Ident); !ok {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil {
			return false
		}
		sym, ok := a.symbolForFuncDecl(decl)
		if !ok || sym == nil {
			return false
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok || fnType == nil || len(fnType.Params) != len(n.Args) {
			return false
		}
		for i, arg := range n.Args {
			if !a.loopTerminationValueAdmissible(arg, focus) {
				return false
			}
			// A measure root passed as a PLACE to a mutable/linear reference parameter could be
			// written by the callee. Reading it by value (or through an immutable reference, or
			// inside a computed rvalue such as `text[i]`) cannot change it.
			if root, isPlace := loopTerminationPlaceRoot(arg); isPlace && focus[root] {
				if ref, isRef := StripAggregateStateType(fnType.Params[i]).(*RefType); isRef && (ref.Mutable || ref.Linear) {
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

// loopTerminationPlaceRoot returns the root identifier of a place expression (`x`, `x.f`, `x[i]`).
func loopTerminationPlaceRoot(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return loopTerminationPlaceRoot(n.Inner)
	case *ast.FieldExpr:
		if n == nil {
			return "", false
		}
		return loopTerminationPlaceRoot(n.Object)
	case *ast.IndexExpr:
		if n == nil {
			return "", false
		}
		return loopTerminationPlaceRoot(n.Object)
	default:
		return "", false
	}
}

// guardedInvariantGoal is `not cond or inv`: the invariant holds whenever the body is entered again.
func guardedInvariantGoal(cond, inv ast.Expr) ast.Expr {
	return &ast.BinaryExpr{
		Position: inv.Pos(),
		Op:       lexer.TOKEN_OR,
		Left:     &ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: ast.CloneExpr(cond)},
		Right:    inv,
	}
}

func (a *Analyzer) typeLoopInvariantsQuietly(invs []*ast.ContractStmt) {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	savedSuppress := a.suppressDiagnostics
	a.suppressDiagnostics = true
	a.ghostReadAllowed++
	defer func() {
		a.suppressDiagnostics = savedSuppress
		a.ghostReadAllowed--
		// Proof records belong to the real (in-place) analysis, not this typing pass.
	}()
	for _, inv := range invs {
		if inv != nil && inv.Cond != nil && a.exprType(inv.Cond) == nil {
			a.analyzeCondExpr(inv.Cond)
		}
	}
}

// loopFrameFactHypotheses returns the live asserted facts that hold at every iteration of a loop:
// each names only locals and parameters the body never writes (no mutable references, no loop
// variables) and calls only functions whose results depend on their arguments alone. They are
// wrapped as hypotheses for the preservation step, which otherwise sees no ambient facts.
func (a *Analyzer) loopFrameFactHypotheses(body []ast.Stmt, subst map[string]ast.Expr) []*ast.ContractStmt {
	if a == nil || a.currentScope == nil {
		return nil
	}
	written := loopWrittenRoots(body)
	for name := range subst {
		written[name] = true
	}
	var out []*ast.ContractStmt
	seen := map[ast.Expr]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil || seen[fact.Expr] || len(fact.Binders) != 0 {
				continue
			}
			seen[fact.Expr] = true
			if a.loopFrameFact(fact.Expr, sc, written) {
				out = append(out, &ast.ContractStmt{Position: fact.Expr.Pos(), Kind: ast.ContractInvariant, Cond: fact.Expr})
			}
		}
		if sc.closedWorld {
			break
		}
	}
	return out
}

func (a *Analyzer) loopFrameFact(expr ast.Expr, recorded *Scope, written map[string]bool) bool {
	if !a.callsAreStateIndependent(expr) {
		return false
	}
	ok := true
	walkPruned(expr, func(node any) bool {
		if !ok {
			return false
		}
		id, isIdent := node.(*ast.Ident)
		if !isIdent || id == nil {
			return true
		}
		sym, found := a.currentScope.Lookup(id.Name)
		there, foundThere := recorded.Lookup(id.Name)
		if !found || !foundThere || sym != there || sym == nil {
			ok = false
			return false
		}
		switch sym.Kind {
		case SymbolLocal, SymbolParam:
			if written[id.Name] {
				ok = false
			}
			if ref, isRef := sym.Type.(*RefType); isRef && (ref.Mutable || ref.Linear) {
				ok = false
			}
		case SymbolFunc:
		default:
			ok = false
		}
		return ok
	})
	return ok
}
