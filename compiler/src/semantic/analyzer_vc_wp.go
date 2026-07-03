package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// Weakest-precondition transport over a straight-line scalar body (VC IR brick 3), extended to
// aug-assignment and single-level `if`/`else` merges.
//
// The existing `ensure` discharge proves a postcondition by substituting `result` with the returned
// expression and assuming the defining equalities of IMMUTABLE locals. A MUTABLE local that is
// reassigned has no such equality, so a body like `y <- y + 1; y <- y * 2; return y` leaves `y` a free
// variable and the postcondition cannot be proven. WP closes this: starting from the postcondition, it
// substitutes each assignment's variable with its right-hand side in REVERSE order, threading every
// value back to the function's parameters, then discharges the resulting goal over the inputs.
//
// Beyond plain assignments, the capture handles:
//   - aug-assignment `x += e`, modeled as `x <- x + e` (the desugared form WP already transports);
//   - a single-level conditional `if c: …then… [else: …else…]` whose branches are straight-line scalar
//     assignments, merged as wp = (c → wp(then, Q)) ∧ (¬c → wp(else, Q)). The merge is sound only when
//     the condition and every branch RHS are fully structural (no opaque leaf), so a later assignment's
//     substitution can still flow into the condition; anything else declines (WP only ever adds proofs).

// wpAssign is one captured scalar assignment `name := rhs`.
type wpAssign struct {
	name string
	rhs  ast.Expr
}

// wpStep is one step of a captured straight-line body: exactly one field is non-nil. `assign` is a
// scalar (re)assignment; `cond` is a single-level if/else merge over scalar assignments.
type wpStep struct {
	assign *wpAssign
	cond   *wpConditional
	call   *wpCallBinding
}

// wpCallBinding is `name := f(args)` where f's declared return type carries a CONSTANT refinement. WP
// does not thread the (non-arithmetic) call result; instead `name` stays a free variable and f's return
// refinement is asserted as a hypothesis — sound because the callee enforces its return contract on
// every exit, so the result already satisfies it.
type wpCallBinding struct {
	name    string
	lawDecl *ast.FuncDecl
	args    []ast.Expr
}

// wpConditional is an `if cond: then [else: els]` whose branches are straight-line scalar assignments.
type wpConditional struct {
	cond ast.Expr
	then []wpAssign
	els  []wpAssign
}

// augAssignBaseOp maps an aug-assignment operator (`+=`) to its base arithmetic operator (`+`), or
// ok=false for a compound op WP does not model.
func augAssignBaseOp(op lexer.TokenKind) (lexer.TokenKind, bool) {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return lexer.TOKEN_PLUS, true
	case lexer.TOKEN_MINUSEQ:
		return lexer.TOKEN_MINUS, true
	case lexer.TOKEN_STAREQ:
		return lexer.TOKEN_STAR, true
	case lexer.TOKEN_SLASHEQ:
		return lexer.TOKEN_SLASH, true
	case lexer.TOKEN_PERCENTEQ:
		return lexer.TOKEN_PERCENT, true
	}
	return 0, false
}

// captureScalarAssign turns a single statement into a scalar assignment `name := rhs`, expanding an
// aug-assignment `x OP= e` into `x := x OP e`. Returns ok=false for any other statement shape or a
// non-identifier target / impure RHS.
func (a *Analyzer) captureScalarAssign(stmt ast.Stmt) (wpAssign, bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value == nil || !exprIsPureArithOrRead(n.Value) {
			return wpAssign{}, false
		}
		return wpAssign{name: n.Name, rhs: n.Value}, true
	case *ast.AssignStmt:
		if n.Optional || !exprIsPureArithOrRead(n.Value) {
			return wpAssign{}, false
		}
		key, ok := a.wpPlaceKey(n.Target)
		if !ok {
			return wpAssign{}, false
		}
		return wpAssign{name: key, rhs: n.Value}, true
	case *ast.AugAssignStmt:
		if !exprIsPureArithOrRead(n.Value) {
			return wpAssign{}, false
		}
		key, ok := a.wpPlaceKey(n.Target)
		if !ok {
			return wpAssign{}, false
		}
		baseOp, ok := augAssignBaseOp(n.Op)
		if !ok {
			return wpAssign{}, false
		}
		// `place OP= e`  ≡  `place := place OP e`. The synthesized RHS reads its own target (an ident or a
		// scalar field place), exactly the case WP backward substitution exists to handle (it is NOT
		// recorded as a one-symbol equality fact).
		leftRead := cloneReadPlace(n.Target)
		rhs := &ast.BinaryExpr{
			Position: n.Pos(),
			Left:     leftRead,
			Op:       baseOp,
			Right:    n.Value,
		}
		// SOUNDNESS: stamp the synthetic nodes with the target's resolved type. Without it
		// a.exprTypes[rhs] is nil, so arithWrapBits/wrapMachineArith cannot recover the width and emit a
		// CLEAN `(+ y 100)` with no `(mod 2^W)` — proving false `>=` postconditions on a wrapping unsigned
		// type (`u8` y += 100 wraps 200->44 but the engine accepted `result >= x`). With the type present
		// the wrap model engages and the obligation correctly declines.
		if t := a.augAssignTargetType(n.Target, key); t != nil {
			a.exprTypes[rhs] = t
			a.exprTypes[leftRead] = t
		}
		return wpAssign{name: key, rhs: rhs}, true
	}
	return wpAssign{}, false
}

// wpPlaceKey returns the WP substitution key for an assignment target WP can thread: a bare identifier
// (its name) or a scalar struct-field place `p.pos` reached through a reference root (its syntactic
// projection name, matching the vcVar name lowerVCTerm mints for the field READ). A field place is
// admitted only when the enclosing function has AT MOST ONE reference-typed parameter — otherwise two
// distinct syntactic paths (`a.pos`, `b.pos`) could alias the same location, and a WP-threaded write to
// one would silently miss the aliased read of the other, unsoundly leaving its exit value equal to
// `old()`. Declines on any other target shape.
func (a *Analyzer) wpPlaceKey(target ast.Expr) (string, bool) {
	switch t := stripOptimizationParens(target).(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.FieldExpr:
		if !fieldPlaceRootedAtIdent(t) || !a.currentFuncSingleRefRoot() {
			return "", false
		}
		return smtProjectionName(t), true
	}
	return "", false
}

// fieldPlaceRootedAtIdent reports whether a field-projection chain bottoms out at a plain identifier
// (`p.pos`, `p.a.b`) rather than a call/index/other computed base — the only shape whose projection name
// is a stable place key.
func fieldPlaceRootedAtIdent(fe *ast.FieldExpr) bool {
	obj := stripOptimizationParens(fe.Object)
	for {
		switch o := obj.(type) {
		case *ast.Ident:
			return true
		case *ast.FieldExpr:
			obj = stripOptimizationParens(o.Object)
		default:
			return false
		}
	}
}

// currentFuncSingleRefRoot reports whether the enclosing function has at most one reference-typed
// parameter, so field places on distinct syntactic roots cannot alias (see wpPlaceKey).
func (a *Analyzer) currentFuncSingleRefRoot() bool {
	if a.currentFuncDecl == nil {
		return false
	}
	refCount := 0
	for _, p := range a.currentFuncDecl.Params {
		// `mutable T&` wraps the RefType in a MutableType, so a bare `p.Type.(*ast.RefType)` would MISS a
		// mutable reference and undercount — letting two `mutable T&` params (which CAN alias two field
		// paths) slip past this aliasing gate. typeExprIsReference unwraps the modifier.
		if typeExprIsReference(p.Type) {
			refCount++
		}
	}
	return refCount <= 1
}

// cloneReadPlace builds a fresh READ expression for an aug-assignment target (ident or field place), so
// the synthesized `place OP e` RHS re-reads the target without sharing the lvalue node.
func cloneReadPlace(target ast.Expr) ast.Expr {
	switch t := stripOptimizationParens(target).(type) {
	case *ast.Ident:
		return &ast.Ident{Position: t.Position, Name: t.Name}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: t.Position, Object: t.Object, Field: t.Field, Safe: t.Safe}
	}
	return target
}

// augAssignTargetType resolves the type of an aug-assignment target. The target is an lvalue, so its
// per-node exprTypes entry is often unset; the symbol's declared type (via scope lookup) is the
// authoritative width for the synthetic `x OP e` node's wrap modeling, with exprTypes as a fallback.
func (a *Analyzer) augAssignTargetType(target ast.Expr, name string) Type {
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(name); ok && sym != nil && sym.Type != nil {
			return sym.Type
		}
	}
	if t := a.exprTypes[target]; t != nil {
		return t
	}
	// A field-place target (`p.pos`) has no scope symbol under its projection key; recover the field's
	// declared width from the object's struct type so the synthetic `place OP e` node wraps correctly.
	if fe, ok := stripOptimizationParens(target).(*ast.FieldExpr); ok {
		if rt, ok := a.fieldReadResolvedType(fe); ok {
			return rt
		}
	}
	return nil
}

// captureScalarAssigns captures a flat block of scalar assignments (a conditional branch). Returns
// ok=false if the block holds anything other than scalar (incl. aug) assignments — no nested control
// flow, calls, or returns.
func (a *Analyzer) captureScalarAssigns(stmts []ast.Stmt) ([]wpAssign, bool) {
	out := make([]wpAssign, 0, len(stmts))
	for _, s := range stmts {
		asg, ok := a.captureScalarAssign(s)
		if !ok {
			return nil, false
		}
		out = append(out, asg)
	}
	return out, true
}

// captureRefinedCallBinding recognizes `name := f(args)` whose callee declares a return refinement with
// CONSTANT arguments (e.g. `-> i64 is Bounded[0,255]`). Returns the binding so WP can assume the result
// satisfies that refinement. Declines on a non-call RHS, an unresolved callee, no return refinement, or
// non-constant refinement args (a dependent bound is not assumed here).
func (a *Analyzer) captureRefinedCallBinding(stmt ast.Stmt) (*wpCallBinding, bool) {
	vd, ok := stmt.(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Value == nil {
		return nil, false
	}
	call, ok := stripOptimizationParens(vd.Value).(*ast.CallExpr)
	if !ok || call == nil {
		return nil, false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return nil, false
	}
	rt, ok := decl.ReturnType.(*ast.RefinementTypeExpr)
	if !ok || rt == nil || len(rt.Preds) == 0 {
		return nil, false
	}
	pred := rt.Preds[0]
	lawDecl, _, ok := a.lookupLaw(pred.Name)
	if !ok || lawDecl == nil {
		return nil, false
	}
	for _, arg := range pred.Args {
		if _, ok := a.constIntValue(arg); !ok {
			return nil, false
		}
	}
	return &wpCallBinding{name: vd.Name, lawDecl: lawDecl, args: pred.Args}, true
}

// wpCallHyps asserts, for each refined-call binding, the callee's return refinement instantiated on the
// bound local: the law body with `self` -> the local and the law's bracket params -> the constant args.
func (a *Analyzer) wpCallHyps(tr *smtTranslator, steps []wpStep) string {
	var b strings.Builder
	for _, step := range steps {
		if step.call == nil || step.call.lawDecl == nil {
			continue
		}
		body, ok := a.lawBodyExpr(step.call.lawDecl)
		if !ok {
			continue
		}
		params := step.call.lawDecl.Params
		if len(params) == 0 {
			continue
		}
		subst := map[string]ast.Expr{params[0].Name: &ast.Ident{Position: params[0].Position, Name: step.call.name}}
		for i, arg := range step.call.args {
			if i+1 < len(params) {
				subst[params[i+1].Name] = arg
			}
		}
		bound, ok := substituteLemmaEnsure(body, subst)
		if !ok {
			continue
		}
		tr.decls[step.call.name] = true
		if h, ok := tr.boolTerm(bound, nil); ok {
			b.WriteString("(assert " + h + ")\n")
		}
	}
	return b.String()
}

// captureWPStepsToEnd captures the whole body as WP steps for a VOID / fall-through function (no return
// value). It threads PARAM mutations (`p -= 1`, `p += k`) so a postcondition over the param's EXIT value
// can be related to its entry value via `old(p)`. Declines on any statement WP cannot account for.
func (a *Analyzer) captureWPStepsToEnd() ([]wpStep, bool) {
	if a.currentFuncDecl == nil {
		return nil, false
	}
	var out []wpStep
	for _, s := range a.currentFuncDecl.Body {
		switch n := s.(type) {
		case *ast.ContractStmt:
			// Leading contracts are lifted to the decl.
		case *ast.ReturnStmt:
			if n.Value != nil {
				return nil, false // a value return belongs to the explicit-return WP path
			}
		case *ast.IfStmt:
			if len(n.Elifs) != 0 {
				return nil, false
			}
			thenA, ok := a.captureScalarAssigns(n.Then)
			if !ok {
				return nil, false
			}
			elseA, ok := a.captureScalarAssigns(n.Else)
			if !ok {
				return nil, false
			}
			out = append(out, wpStep{cond: &wpConditional{cond: n.Cond, then: thenA, els: elseA}})
		default:
			if cb, ok := a.captureRefinedCallBinding(s); ok {
				out = append(out, wpStep{call: cb})
				break
			}
			asg, ok := a.captureScalarAssign(s)
			if !ok {
				return nil, false
			}
			out = append(out, wpStep{assign: &asg})
		}
	}
	return out, true
}

// tryProveVoidEnsureByWP discharges a boolean `ensure` clause of a void / fall-through function (one that
// references params and `old(...)`, not `result`) by weakest precondition. It lowers the clause — a bare
// param `p` becomes the substitutable EXIT value, `old(p)` the fixed ENTRY value (vcOldEntry) — then
// threads each param mutation backward, so `ensure p >= old(p)` PROVES when the body keeps or raises p
// and correctly DECLINES when it lowers p. Sound: `old(p)` is anchored to the entry symbol and never
// rewritten; any sub-step outside the structural fragment declines (WP only ever adds proofs).
func (a *Analyzer) tryProveVoidEnsureByWP(clause ast.Expr) bool {
	if clause == nil {
		return false
	}
	steps, ok := a.captureWPStepsToEnd()
	if !ok {
		return false
	}
	tr := a.newSMTTranslator(nil)
	tr.oldAsEntry = true
	goal, ok := tr.lowerVCFormula(clause, nil)
	if !ok || !vcFormulaFullyStructural(goal) {
		return false
	}
	goal, ok = a.wpTransport(tr, steps, goal)
	if !ok {
		return false
	}
	if isVCTrue(goal) {
		return true
	}
	if isVCFalse(goal) {
		return false
	}
	proven, _ := a.smtDischargeFormula(tr, goal, a.wpEntryRequiresHyps(tr)+a.wpCallHyps(tr, steps))
	return proven
}

// wpEntryRequiresHyps emits the enclosing function's `requires` clauses as SMT hypotheses for a WP
// discharge. A WP goal reasons over ENTRY values (the bare param threaded to its exit, `old(p)` and the
// param symbol both anchored to entry), so every `requires` — which constrains entry values — holds and
// is asserted here. This is what the standard hypothesis set omits for a MUTABLE-root requires (cluster
// B drops it once the param is mutated, sound for the CURRENT value but discarding the entry bound the
// WP goal legitimately needs, e.g. `requires p < 1000; p += 5; ensure p >= old(p)`).
func (a *Analyzer) wpEntryRequiresHyps(tr *smtTranslator) string {
	if a.currentFuncDecl == nil {
		return ""
	}
	var b strings.Builder
	for _, req := range a.currentFuncDecl.Requires {
		if req == nil {
			continue
		}
		if h, ok := tr.boolTerm(req, nil); ok {
			b.WriteString("(assert " + h + ")\n")
		}
	}
	return b.String()
}

// captureWPSteps returns the ordered steps leading up to (and excluding) the return, or ok=false if the
// body holds anything WP cannot account for: a call, a non-scalar statement, a loop, or an `if` with
// elif clauses / non-straight-line branches. The return must be the final statement (so the captured
// prefix is the whole computation).
func (a *Analyzer) captureWPSteps(ret *ast.ReturnStmt) ([]wpStep, bool) {
	if a.currentFuncDecl == nil || ret == nil {
		return nil, false
	}
	var out []wpStep
	for _, s := range a.currentFuncDecl.Body {
		if stmt, ok := s.(*ast.ReturnStmt); ok && stmt == ret {
			return out, true
		}
		switch n := s.(type) {
		case *ast.ContractStmt:
			// Leading contracts are lifted to the decl, but tolerate a stray one in the prefix.
		case *ast.IfStmt:
			// Only a single-level if/else (no elif) whose branches are straight-line scalar assignments.
			if len(n.Elifs) != 0 {
				return nil, false
			}
			thenA, ok := a.captureScalarAssigns(n.Then)
			if !ok {
				return nil, false
			}
			elseA, ok := a.captureScalarAssigns(n.Else)
			if !ok {
				return nil, false
			}
			out = append(out, wpStep{cond: &wpConditional{cond: n.Cond, then: thenA, els: elseA}})
		default:
			if cb, ok := a.captureRefinedCallBinding(s); ok {
				out = append(out, wpStep{call: cb})
				break
			}
			asg, ok := a.captureScalarAssign(s)
			if !ok {
				return nil, false
			}
			_ = n
			out = append(out, wpStep{assign: &asg})
		}
	}
	return nil, false
}

// applyAssignsBackward substitutes a branch's scalar assignments into `goal` in reverse order, folding
// via the smart constructors. Returns ok=false if any RHS is not fully structural (an opaque term
// could hide the assigned variable, making substitution unsound).
func (a *Analyzer) applyAssignsBackward(tr *smtTranslator, assigns []wpAssign, goal vcFormula) (vcFormula, bool) {
	for i := len(assigns) - 1; i >= 0; i-- {
		rhsTerm, ok := tr.lowerVCTerm(assigns[i].rhs, nil)
		if !ok || !vcTermFullyStructural(rhsTerm) {
			return nil, false
		}
		goal = substVCFormula(goal, assigns[i].name, rhsTerm)
	}
	return goal, true
}

// wpTransport computes the weakest precondition of `goal` over the captured steps, processed in
// reverse. An assignment substitutes its RHS; a conditional merges the two branch preconditions under
// the (fully structural) condition: (c → wp(then)) ∧ (¬c → wp(else)). Declines (ok=false) on any
// non-structural sub-part, keeping WP sound.
func (a *Analyzer) wpTransport(tr *smtTranslator, steps []wpStep, goal vcFormula) (vcFormula, bool) {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		switch {
		case step.call != nil:
			// The call result stays a free variable; its refinement is asserted by wpCallHyps.
		case step.assign != nil:
			rhsTerm, ok := tr.lowerVCTerm(step.assign.rhs, nil)
			if !ok || !vcTermFullyStructural(rhsTerm) {
				return nil, false
			}
			goal = substVCFormula(goal, step.assign.name, rhsTerm)
		case step.cond != nil:
			condF, ok := tr.lowerVCFormula(step.cond.cond, nil)
			if !ok || !vcFormulaFullyStructural(condF) {
				return nil, false
			}
			thenG, ok := a.applyAssignsBackward(tr, step.cond.then, goal)
			if !ok {
				return nil, false
			}
			elseG, ok := a.applyAssignsBackward(tr, step.cond.els, goal)
			if !ok {
				return nil, false
			}
			// (c → thenG) ∧ (¬c → elseG)  ≡  (¬c ∨ thenG) ∧ (c ∨ elseG).
			goal = vcMkAnd(vcMkOr(vcMkNot(condF), thenG), vcMkOr(condF, elseG))
		default:
			return nil, false
		}
	}
	return goal, true
}

// tryProveEnsureByWP discharges an `ensure` clause over a straight-line scalar body by weakest
// precondition. It substitutes `result` with the returned expression (AST level, since `result` is not
// a runtime binding here), lowers to the VC IR, then transports the goal backward through the captured
// steps — folding as it goes — and discharges the resulting goal over the parameters against the
// standard hypotheses (the function's `requires`). Returns true only on a real proof; any sub-step
// outside the structural fragment declines (sound: WP only ever forgoes a proof).
func (a *Analyzer) tryProveEnsureByWP(clause ast.Expr, ret *ast.ReturnStmt) bool {
	if clause == nil || ret == nil || ret.Value == nil {
		return false
	}
	steps, ok := a.captureWPSteps(ret)
	if !ok {
		return false
	}
	substituted, ok := substituteLemmaEnsure(clause, map[string]ast.Expr{"result": ret.Value})
	if !ok {
		return false
	}
	tr := a.newSMTTranslator(nil)
	tr.oldAsEntry = true
	goal, ok := tr.lowerVCFormula(substituted, nil)
	if !ok || !vcFormulaFullyStructural(goal) {
		return false
	}
	goal, ok = a.wpTransport(tr, steps, goal)
	if !ok {
		return false
	}
	if isVCTrue(goal) {
		return true
	}
	if isVCFalse(goal) {
		return false
	}
	// Discharge through the brick-4 splitter: a WP-transported conjunctive postcondition splits into
	// independent conjuncts over the shared `requires` hypotheses.
	proven, _ := a.smtDischargeFormula(tr, goal, a.wpEntryRequiresHyps(tr)+a.wpCallHyps(tr, steps))
	return proven
}
