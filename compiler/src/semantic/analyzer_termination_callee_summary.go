//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Interprocedural termination via callee `ensure` summaries (docs/118, bricks 118-2/118-4).
//
// The affine `decreases` proof (proveMeasureDecreases) discharges by substituting the recursive call's
// ARGUMENTS into the measure. A recursive-descent parser re-passes the SAME `mutable Parser&` every call,
// so argument substitution reports the measure unchanged (Wall 1): the decrease lives in a SIDE EFFECT —
// a consumer `advance(p)` that advanced `p.pos` — performed by a callee on the path to the recursion.
//
// This certificate composes that callee's `ensure` postcondition into the decrease proof. The measure is
// the honest `p.tokens.count - p.pos`; the PROOF is what gets stronger. Fully erased in release (the
// consumer's `ensure` is a contract), so the proof carries zero runtime cost.
//
// Deliberately pattern-restricted and fail-closed (docs/118 §5 — an unsound composition is worse than no
// proof). Engaged only when ALL of these hold:
//   - a SINGLE measure component mentioning scalar field place(s) `p.field` of a `mutable T&` parameter;
//   - the recursive call re-passes each such place's root in its own parameter position UNCHANGED
//     (a genuine argument change is the affine path's job, not this one);
//   - EXACTLY ONE consumer call on the must-execute straight-line prefix mutates a measured place, via a
//     callee carrying a boolean `ensure` relating the place to `old(place)`; every other prefix statement
//     provably leaves the measured places untouched (no direct write, no other mutating call, no nested
//     control flow that could write them);
//   - positive enclosing `if` guards on the path to the consumer are asserted as hypotheses, so a
//     GUARDED strict increase (`old(pos) < stop => pos > old(pos)` — the EOF-saturating advance) still
//     discharges when the caller establishes the guard.
//
// Discharge: `measure[places := old(places)] > measure` — the entry measure strictly exceeds the measure
// at the recursive call — under the instantiated consumer `ensure` clauses, the caller's `requires`, and
// the collected guards. Because the consumer is the ONLY measured-place mutation on the prefix, the place
// value at the recursive call IS its post-consumer value and `old(place)` IS the entry (pre-consumer)
// value, so the consumer's ensure exactly bridges the two.
func (a *Analyzer) proveDecreaseViaCalleeSummary(fn *ast.FuncDecl, call *ast.CallExpr, measure ast.Expr) bool {
	if a == nil || fn == nil || call == nil || measure == nil {
		return false
	}
	// The whole model relies on there being a single reference root, so two syntactic field paths cannot
	// alias (same gate the WP field-place transport uses).
	if !a.currentFuncOnlyRefRootIs(fn) {
		return false
	}
	roots := a.measuredMutRefRoots(fn, measure)
	if len(roots) == 0 {
		return false
	}
	// The recursive call must re-pass every measured root in its own parameter position unchanged.
	if !a.callRepassesRootsUnchanged(fn, call, roots) {
		return false
	}
	// The measure must be bounded below (else an ever-decreasing measure never bottoms out).
	if !a.measureBoundedBelow(measure) {
		return false
	}
	consumer, guards, ok := a.singlePrefixConsumer(fn, call, roots)
	if !ok || consumer == nil {
		return false
	}
	// The consumer must carry a `changes` frame (an UPPER BOUND on what it mutates, docs/87). Places NOT
	// covered by it are guaranteed unchanged and stay a SINGLE symbol across entry/exit (so they cancel in
	// the measure); covered places get distinct entry/current symbols related by the consumer's `ensure`.
	// Without a `changes` frame we cannot assume ANY place stable — decline (sound).
	changed := a.consumerChangedPaths(consumer, roots)
	if len(changed) == 0 {
		return false
	}
	// At least one MEASURED place must be a changed place, else the consumer moves nothing in the measure.
	if !a.measureHasChangedPlace(measure, roots, changed) {
		return false
	}
	hyps, ok := a.calleeSummaryHypExprs(consumer, roots)
	if !ok || len(hyps) == 0 {
		return false
	}

	// Two-symbol model. A CHANGED measured place gets an entry twin (root renamed `__entry_<root>`, its
	// field type stamped so the SMT translator can size it); an UNCHANGED place keeps its single current
	// symbol. `old(x)` in a consumer ensure denotes the entry (pre-consumer) value → its argument is
	// rewritten to the entry side; a bare place is the current (post-consumer) value.
	//   goal:  measure[changed := entry-twin]  >  measure            (entry measure exceeds call measure)
	//   hyps:  each consumer ensure with old(x) -> entry-twin(x), bare place current;
	//          the fall-through guards and the consumer's own requires, over entry values.
	entryMeasure := a.rewriteChangedPlacesToEntry(measure, roots, changed)
	goalExpr := &ast.BinaryExpr{Position: measure.Pos(), Op: lexer.TOKEN_GT, Left: entryMeasure, Right: ast.CloneExpr(measure)}

	tr := a.newSMTTranslator(nil)
	goal, ok := tr.lowerVCFormula(goalExpr, nil)
	if !ok || isVCFalse(goal) {
		return false
	}
	if isVCTrue(goal) {
		return true
	}

	var hypSMT string
	assert := func(e ast.Expr) {
		if e == nil {
			return
		}
		if s, ok := tr.boolTerm(e, nil); ok {
			hypSMT += "(assert " + s + ")\n"
		}
	}
	for _, h := range hyps {
		assert(a.rewriteEnsureOldToEntry(h, roots, changed))
	}
	// Fall-through guards and the consumer's own `requires` are ENTRY-time facts (they hold before the
	// consumer runs), so rewrite their changed places to the entry side.
	for _, g := range guards {
		assert(a.rewriteChangedPlacesToEntry(g, roots, changed))
	}
	for _, req := range a.calleeRequiresRebased(consumer) {
		assert(a.rewriteChangedPlacesToEntry(req, roots, changed))
	}
	proven, _ := a.smtDischargeFormula(tr, goal, hypSMT)
	return proven
}

// currentFuncOnlyRefRootIs reports whether fn has at most one reference-typed parameter (the aliasing
// gate: distinct syntactic field paths then cannot denote the same location).
func (a *Analyzer) currentFuncOnlyRefRootIs(fn *ast.FuncDecl) bool {
	refCount := 0
	for _, p := range fn.Params {
		if typeExprIsReference(p.Type) {
			refCount++
		}
	}
	return refCount <= 1
}

// typeExprIsReference reports whether a parameter type expression is a reference (`T&` / `mutable T&`).
// The `mutable` modifier wraps the reference in a MutableType, so unwrap it before testing.
func typeExprIsReference(t ast.TypeExpr) bool {
	for {
		switch n := t.(type) {
		case *ast.MutableType:
			t = n.Elem
		case *ast.RefType:
			return true
		default:
			return false
		}
	}
}

// measuredMutRefRoots returns the set of parameter names p such that the measure mentions a scalar field
// place `p.field` and p is a reference-typed parameter of fn.
func (a *Analyzer) measuredMutRefRoots(fn *ast.FuncDecl, measure ast.Expr) map[string]bool {
	refParams := map[string]bool{}
	for _, p := range fn.Params {
		if typeExprIsReference(p.Type) {
			refParams[p.Name] = true
		}
	}
	roots := map[string]bool{}
	a.walkStaticExpr(measure, func(e ast.Expr) bool {
		if fe, ok := e.(*ast.FieldExpr); ok && fe != nil && !fe.Safe {
			if id, ok := stripOptimizationParens(fe.Object).(*ast.Ident); ok && refParams[id.Name] {
				roots[id.Name] = true
			}
		}
		return false
	})
	return roots
}

// callRepassesRootsUnchanged reports whether the recursive call passes each measured root in its own
// parameter position as the syntactically identical identifier (or omits it) — so the place at the call
// is exactly the place after the prefix, with no argument-level change.
func (a *Analyzer) callRepassesRootsUnchanged(fn *ast.FuncDecl, call *ast.CallExpr, roots map[string]bool) bool {
	for i, param := range fn.Params {
		if !roots[param.Name] {
			continue
		}
		if i >= len(call.Args) || call.Args[i] == nil {
			continue // omitted: the measure component mentioning it leaves the fragment; sound
		}
		id, ok := stripOptimizationParens(call.Args[i]).(*ast.Ident)
		if !ok || id.Name != param.Name {
			return false
		}
	}
	return true
}

// singlePrefixConsumer walks the top-level body of fn up to the statement containing the recursive call,
// requiring EXACTLY ONE consumer call that mutates a measured root (via a reference argument) and that no
// other prefix statement touches a measured place. Returns the consumer call and the enclosing positive
// guards (there are none at top level; nested guarded shapes are a future brick). Declines on any
// interfering write, nested control flow over a measured place, or zero/multiple consumers.
func (a *Analyzer) singlePrefixConsumer(fn *ast.FuncDecl, call *ast.CallExpr, roots map[string]bool) (*ast.CallExpr, []ast.Expr, bool) {
	var consumer *ast.CallExpr
	var guards []ast.Expr
	found := false
	reachedCall := false
	for _, stmt := range fn.Body {
		if a.stmtContainsCall(stmt, call) {
			reachedCall = true
			break
		}
		// A pure early-exit guard `if COND: return/break` (call-free COND, exit-only body) does not mutate
		// anything; on the fall-through path that reaches the consumer and the recursion, `not COND` holds.
		// Collect it as a guard hypothesis (it can license a conditional consumer `ensure`) and treat the
		// statement as non-interfering.
		if cond, ok := pureEarlyExitGuard(stmt); ok {
			guards = append(guards, &ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
			continue
		}
		// A statement that is exactly `g(args...)` (expression statement or `x = g(...)` / `x: T = g(...)`)
		// whose callee takes a measured root by reference is the candidate consumer.
		if c, ok := stmtIsMutatingConsumerCall(stmt, roots); ok {
			if found {
				return nil, nil, false // more than one measured-place mutation on the prefix
			}
			consumer = c
			found = true
			continue
		}
		// Any other prefix statement must NOT even MENTION a measured root — conservatively ruling out any
		// hidden write or mutating call the summary would miss (an over-decline on a harmless read is
		// sound: it only forgoes a proof).
		if a.stmtMentionsRoot(stmt, roots) {
			return nil, nil, false
		}
	}
	if !reachedCall || !found {
		return nil, nil, false
	}
	return consumer, guards, true
}

// pureEarlyExitGuard recognizes a statement `if COND: return` / `if COND: break` / `if COND: continue`
// with no elif/else and a call-free condition — a guard that only diverts control, never mutating state.
// Returns COND so the caller can assert `not COND` on the fall-through path.
func pureEarlyExitGuard(stmt ast.Stmt) (ast.Expr, bool) {
	ifStmt, ok := stmt.(*ast.IfStmt)
	if !ok || ifStmt == nil || len(ifStmt.Elifs) != 0 || len(ifStmt.Else) != 0 || len(ifStmt.Then) != 1 {
		return nil, false
	}
	switch ifStmt.Then[0].(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
	default:
		return nil, false
	}
	if exprContainsCall(ifStmt.Cond) {
		return nil, false
	}
	return ifStmt.Cond, true
}

// exprContainsCall reports whether an expression subtree contains a call (which could have side effects,
// disqualifying it as a pure guard condition).
func exprContainsCall(expr ast.Expr) bool {
	found := false
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.CallExpr:
			found = true
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.CastExpr:
			walk(n.Operand)
		}
	}
	walk(expr)
	return found
}

// stmtIsMutatingConsumerCall recognizes a top-level consumer call statement `g(args...)` (bare expression
// or the RHS of a var-decl / assignment) that passes a measured root by reference.
func stmtIsMutatingConsumerCall(stmt ast.Stmt, roots map[string]bool) (*ast.CallExpr, bool) {
	var value ast.Expr
	switch n := stmt.(type) {
	case *ast.ExprStmt:
		value = n.Expr
	case *ast.VarDeclStmt:
		value = n.Value
	case *ast.AssignStmt:
		value = n.Value
	default:
		return nil, false
	}
	call, ok := stripOptimizationParens(value).(*ast.CallExpr)
	if !ok || call == nil {
		return nil, false
	}
	if !callPassesRootByRef(call, roots) {
		return nil, false
	}
	return call, true
}

// callPassesRootByRef reports whether any argument of the call is a measured root passed as itself or via
// address-of (`p` or `&p`) — the reference argument through which a consumer mutates the place.
func callPassesRootByRef(call *ast.CallExpr, roots map[string]bool) bool {
	for _, arg := range call.Args {
		if arg == nil {
			continue
		}
		switch e := stripOptimizationParens(arg).(type) {
		case *ast.Ident:
			if roots[e.Name] {
				return true
			}
		case *ast.UnaryExpr:
			if e.Op == lexer.TOKEN_AMPERSAND {
				if id, ok := stripOptimizationParens(e.Operand).(*ast.Ident); ok && roots[id.Name] {
					return true
				}
			}
		}
	}
	return false
}

// stmtMentionsRoot conservatively reports whether a statement references a measured root anywhere — as a
// bare identifier or the object of a field access. Used to rule out any prefix statement (other than the
// single consumer) that could read or write a measured place.
func (a *Analyzer) stmtMentionsRoot(stmt ast.Stmt, roots map[string]bool) bool {
	mentions := false
	a.walkStaticStmt(stmt, func(e ast.Expr) bool {
		if id, ok := e.(*ast.Ident); ok && roots[id.Name] {
			mentions = true
			return true
		}
		return false
	})
	return mentions
}

// calleeSummaryHypExprs resolves the consumer's callee and returns its boolean `ensure` postconditions
// rebased onto the CALLER's argument names — mapping each callee parameter to the caller's argument, so a
// callee `ensure q.pos > old(q.pos)` for a call `advance(p)` becomes `p.pos > old(p.pos)`. Only clauses
// mentioning a measured place are kept (the rest cannot inform the measure).
func (a *Analyzer) calleeSummaryHypExprs(consumer *ast.CallExpr, roots map[string]bool) ([]ast.Expr, bool) {
	decl, ok := a.resolveDirectCallFuncDecl(consumer)
	if !ok || decl == nil || len(decl.EnsureValues) == 0 {
		return nil, false
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(consumer.Args) || consumer.Args[i] == nil {
			continue
		}
		arg := stripOptimizationParens(consumer.Args[i])
		// Rebase `q` -> the caller's reference argument root. `&p` and `p` both rebase to `p` (the place
		// path `q.field` becomes `p.field`).
		if u, isAddr := arg.(*ast.UnaryExpr); isAddr && u.Op == lexer.TOKEN_AMPERSAND {
			arg = stripOptimizationParens(u.Operand)
		}
		subst[param.Name] = arg
	}
	var out []ast.Expr
	for _, e := range decl.EnsureValues {
		if e == nil {
			continue
		}
		rebased, ok := substituteLemmaEnsure(e, subst)
		if !ok {
			continue
		}
		if a.exprMentionsMeasuredPlace(rebased, roots) {
			out = append(out, rebased)
		}
	}
	return out, len(out) > 0
}

// exprMentionsMeasuredPlace reports whether an expression references a measured place `root.field`
// (including inside an `old(...)`).
func (a *Analyzer) exprMentionsMeasuredPlace(expr ast.Expr, roots map[string]bool) bool {
	mentions := false
	a.walkStaticExpr(expr, func(e ast.Expr) bool {
		if fe, ok := e.(*ast.FieldExpr); ok && fe != nil {
			if id, ok := stripOptimizationParens(fe.Object).(*ast.Ident); ok && roots[id.Name] {
				mentions = true
				return true
			}
		}
		return false
	})
	return mentions
}
