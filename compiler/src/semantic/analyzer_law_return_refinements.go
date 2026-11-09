package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) dischargeReturnRefinements(n *ast.ReturnStmt) {
	if a == nil || n == nil || n.Value == nil || a.currentFuncDecl == nil {
		return
	}
	rt, ok := a.paramRefinementTypeExpr(a.currentFuncDecl.ReturnType)
	if !ok || rt == nil {
		return
	}
	var checks []*ast.CallExpr
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue
		}
		// Tag all proof records in this obligation as return-refinement for elision telemetry.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.tryDischargeRefinementStatically(n.Value, "the returned value", pred, lawDecl, n.Pos()) {
			continue
		}
		// Composition: `return callee(...)` where the callee's RETURN TYPE already carries a refinement
		// that entails this one is satisfied by the callee's contract (its return value is checked/proven
		// against that refinement on every exit). This is the common forward/wrap pattern — without it a
		// function that returns another refinement-typed function's result paid a redundant runtime check.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.exprRefinementSchemeEntails(n.Value, pred) {
			a.recordProofCat(n.Pos(), "the returned value", pred.Name, ProofProvenContract, ProofCatReturnRefinement)
			continue
		}
		// Refutation gate: the linear prover may refute a return-refinement obligation that the
		// const-eval tier missed (e.g. an affine expression whose bounded range excludes the law).
		// Mirror the ensure/where/requires refutation gate: a PROVABLY false postcondition is a hard
		// error independent of -strict; merely-unprovable stays a lint.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.returnRefinementPredRefuted(n.Value, lawDecl, pred) {
			a.recordProofCat(n.Pos(), "the returned value", pred.Name, ProofRefuted, ProofCatReturnRefinement)
			a.errorf(n.Pos(), "return refinement %q is provably violated: the returned value does not satisfy it", pred.Name)
			continue
		}
		a.currentProofCategory = ProofCatReturnRefinement
		a.recordProof(n.Pos(), "the returned value", pred.Name, ProofRuntime)
		a.proofLint(n.Pos(), "refinement %q on the return of %q could not be proven statically; return a provable value or accept the runtime check%s", pred.Name, a.currentFuncDecl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
		// Opt-in lint: when the return subject is a field or index access, check whether binding it
		// to a local first would allow the flow tier to prove the refinement statically via the
		// declared type range. Only fires when the local-bound form is provably dischargeable.
		if a.returnSubjectLocalBindingWouldProve(n.Value, lawDecl, pred) {
			a.proofLint(n.Pos(), "hint: binding %s to a local before returning would let the flow prover discharge %q statically (zero-cost runtime-check elision)", a.returnSubjectDescription(n.Value), pred.Name)
		}
		if isBuiltinModularLawName(pred.Name) || !a.isSideEffectFreeRefinementArg(n.Value) {
			continue
		}
		check := &ast.CallExpr{
			Position: pred.Position,
			Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
			Args:     append([]ast.Expr{n.Value}, pred.Args...),
		}
		a.analyzeExpr(check)
		checks = append(checks, check)
	}
	if len(checks) != 0 && a.returnRefinementChecks != nil {
		a.returnRefinementChecks[n] = checks
	}
}

// returnRefinementPredRefuted reports whether a return-position refinement predicate is PROVABLY
// FALSE for the given return value, using the linear prover. Returns true only when the prover
// verdict is requiresRefuted — the negation of the predicate is entailed by the value's bounds.
// Returns false for unknown or proven (the caller handles those). Sound: only a definite
// refutation verdict escalates; an unknown verdict stays a lint (the existing fallback path).
func (a *Analyzer) returnRefinementPredRefuted(value ast.Expr, lawDecl *ast.FuncDecl, pred ast.RefinementPredExpr) bool {
	if a == nil || lawDecl == nil || len(lawDecl.Params) == 0 {
		return false
	}
	body, ok := a.lawBodyExpr(lawDecl)
	if !ok || body == nil {
		return false
	}
	// Build subst: law subject param → return value; law value params → predicate args.
	subst := map[string]ast.Expr{lawDecl.Params[0].Name: value}
	for i, arg := range pred.Args {
		if i+1 >= len(lawDecl.Params) {
			return false
		}
		subst[lawDecl.Params[i+1].Name] = arg
	}
	return a.proveRequiresClause(body, subst) == requiresRefuted
}

// dischargeEnsuresRefinements discharges `ensures <param> is Law` postconditions (docs/85 brick 2,
// half B) at each return: the parameter must satisfy the law at function exit, so the caller's
// gained fact (half A) is backed. This is the STATIC half — prove (flow/factset/const), refute
// (compile error), or report+warn/-strict. The RUNTIME fallback is emitted by the backend
// (emitRefinementPostconditionChecks) uniformly on every exit path INCLUDING fall-through, so a
// void function that never writes an explicit `return` is still checked — which keeps the call-site
// gain sound. Static proof and runtime check are a sound pair (debug verifies what release assumes).
func (a *Analyzer) dischargeEnsuresRefinements(n *ast.ReturnStmt) {
	if a == nil || n == nil || a.currentFuncType == nil || a.currentFuncDecl == nil {
		return
	}
	// All proof records in this function are contract-ensures obligations (elision telemetry).
	defer func(prev ProofObligationCategory) { a.currentProofCategory = prev }(a.currentProofCategory)
	a.currentProofCategory = ProofCatContractEnsures
	for _, re := range a.currentFuncType.RefinementEnsures {
		if re.ParamIndex < 0 || re.ParamIndex >= len(a.currentFuncDecl.Params) {
			continue
		}
		paramName := a.currentFuncDecl.Params[re.ParamIndex].Name
		lawDecl, _, ok := a.lookupLaw(re.LawName)
		if !ok {
			continue
		}
		subject := &ast.Ident{Position: n.Pos(), Name: paramName}
		pred := ast.RefinementPredExpr{Position: re.Position, Name: re.LawName, Args: re.Args}
		valueName := "parameter \"" + paramName + "\""
		if a.tryDischargeRefinementStatically(subject, valueName, pred, lawDecl, n.Pos()) {
			continue
		}
		a.recordProof(n.Pos(), valueName, re.LawName, ProofRuntime)
		a.proofLint(n.Pos(), "postcondition %q on parameter %q of %q could not be proven statically; it is checked at runtime (debug) — make it provable or accept the runtime check%s", re.LawName, paramName, a.currentFuncDecl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
	}
}

// dischargeEnsureBooleans statically discharges the general-boolean `ensure <bool>`
// postconditions (FuncDecl.EnsureValues) at each return: substitute `result` with the
// returned expression and prove the clause via the discharge ladder (SMT, assuming the
// function's `requires` as hypotheses). The backend still emits the debug runtime check
// uniformly, so a proven clause is a sound pair (debug verifies what release assumes);
// the static half upgrades a contract from "checked" to "proven". Under `-strict` an
// ensure clause that cannot be proven at a return is a hard error (the Dafny-like mode);
// without `-strict` it stays a silent runtime check (no warning noise). `old(...)` and
// locals in the clause/return are free SMT vars (sound: fewer facts only declines).
// dischargeEnsureBooleansAtVoidExit discharges the boolean `ensure` clauses of a void / fall-through
// function at its synthetic exit (a value-less `return` or running off the end of the body), mirroring
// the explicit-return path (dischargeEnsureBooleans). Without it, a postcondition over a mutated ref
// param — `ensure p >= old(p)` on a body that does `p -= 1` — was never checked under -strict and
// silently relied on the debug runtime check, so a FALSE postcondition slipped through static checking.
//
// At a non-void synthetic exit, result-dependent clauses belong to explicit return checks. Actual
// void functions check every clause: a local binder named `result` must not suppress an obligation.
// The discharge is SOUND: `old(p)` lowers to a distinct entry symbol, so an undischargeable clause is reported, never
// falsely proven. Like the explicit-return path, a mutated-param `old()` postcondition the prover cannot
// relate to the exit value is reported under -strict (drop -strict / use -permissive for the runtime check).
func (a *Analyzer) dischargeEnsureBooleansAtVoidExit(pos lexer.Pos) {
	if a == nil || a.currentFuncDecl == nil {
		return
	}
	// All proof records in this function are contract-ensures obligations (elision telemetry).
	defer func(prev ProofObligationCategory) { a.currentProofCategory = prev }(a.currentProofCategory)
	a.currentProofCategory = ProofCatContractEnsures
	for i, clause := range a.currentFuncDecl.EnsureValues {
		if clause == nil {
			continue
		}
		// This hook also runs at a value function's synthetic exit. Only there
		// is a result-dependent clause handled exclusively by explicit returns.
		// A true void function must never skip an obligation based on a name
		// scan: an arm/block-local binder may legitimately be named result.
		if exprReferencesResult(clause) && !SameType(a.currentReturn, a.namedTypes["void"]) {
			continue
		}
		if proofAt(a.currentFuncDecl.EnsureProofs, i) != nil {
			continue
		}
		// Refutation gate (always on): a void-exit postcondition over params PROVABLY FALSE here is a
		// hard error, like the refuted-`requires`/`where` gate. No `result` subst exists at a void exit
		// (local bindings named `result` retain their ordinary lexical meaning). Only a provably-false verdict escalates.
		// A clause naming a parameter that a body local shadows here would be resolved against the
		// local by every tier below (SMT symbols are keyed by spelling): neither prove nor refute it.
		if a.mentionsShadowedParam(clause) {
			if a.enforceStrictProofs {
				diag := a.buildEnsureFailureDiagnostic(clause, nil, "")
				a.errorf(pos, "%s", diag.FormatEnsure(a.currentFuncDecl.Name))
			}
			continue
		}
		if a.ensureClauseRefuted(clause, nil) {
			a.reportRefutedEnsure(pos, clause, nil)
			continue
		}
		// The remaining ladder and its unprovable→runtime-fallback handling stay -strict-gated.
		if !a.enforceStrictProofs {
			continue
		}
		clause = normalizeBoolLiteralEnsure(clause)
		if a.ensureGoalReflexivelyValid(clause, nil) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "reflexivity", ProofProvenSMT)
			continue
		}
		// Written-field fast-path: `ensure self.f == E` where the body writes `self.f <- V` and the
		// recorded V proves the clause (V == 0 for `self.f == 0`; or both `self.a` and `self.b` trace
		// to the same value expr for `self.a == self.b`). The fact is dropped at every mutation/borrow/
		// mutating-callee of the root, so a hit means the last write still stands. The SMT lane fails
		// these because two loads of a reference field are distinct opaque terms (the SetupRegions gap).
		if a.ensureHoldsByWrittenField(clause) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "written field", ProofProvenSMT)
			continue
		}
		// A live assert fact spelled exactly like the clause was proven at an earlier point and not
		// retired by any later write to its roots, so it holds at this exit.
		if a.assertFactSpellingLive(clause) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "assert fact", ProofProvenSMT)
			continue
		}
		// WP transport relates the param's EXIT value to `old(p)` (its entry value), proving e.g.
		// `ensure p >= old(p)` when the body keeps/raises p. The trySMTProveRequires fallback models
		// `old(p)` as an unconstrained fresh symbol — sound but always declining an old() postcondition.
		if a.tryProveVoidEnsureByWP(clause) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "wp", ProofProvenSMT)
			continue
		}
		if proven, counterexample := a.trySMTProveRequiresReported(clause, nil); !proven {
			diag := a.buildEnsureFailureDiagnostic(clause, nil, counterexample)
			a.errorf(pos, "%s", diag.FormatEnsure(a.currentFuncDecl.Name))
		}
	}
}

// exprReferencesResult reports whether an expression reads the contract `result` binding.
func exprReferencesResult(expr ast.Expr) bool {
	return smtFactDeps(expr)["result"]
}

// normalizeBoolLiteralEnsure rewrites a postcondition phrased against a boolean literal to the bare
// predicate the discharge ladder already proves: `X == true` / `X != false` become `X`, and
// `X == false` / `X != true` become `not X` (either operand may be the literal). Without this, a
// no-overflow law written as `ensure result == true` over a `return base+size >= base` fails to
// discharge even under -smt, while the identical bare `ensure result` (and the integer phrasing
// `ensure result >= base`) prove — the SMT lane models `==` between a Bool term and a bool literal as
// an opaque equality rather than the predicate itself. Folding the literal away routes the wrapped form
// through the same path. SOUND: `X == true` is logically `X` and `X == false` is `not X`; the rewrite is
// a pure logical identity, asserting nothing extra, so an unprovable `X` still declines.
func normalizeBoolLiteralEnsure(clause ast.Expr) ast.Expr {
	bin, ok := stripOptimizationParens(clause).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.LoweredCall != nil {
		return clause
	}
	if bin.Op != lexer.TOKEN_EQEQ && bin.Op != lexer.TOKEN_BANGEQ {
		return clause
	}
	var lit *ast.BoolLit
	var other ast.Expr
	if l, ok := stripOptimizationParens(bin.Left).(*ast.BoolLit); ok {
		lit, other = l, bin.Right
	} else if r, ok := stripOptimizationParens(bin.Right).(*ast.BoolLit); ok {
		lit, other = r, bin.Left
	} else {
		return clause
	}
	// `== true` and `!= false` keep the predicate; `== false` and `!= true` negate it.
	if (bin.Op == lexer.TOKEN_EQEQ) == lit.Value {
		return other
	}
	return &ast.UnaryExpr{Position: bin.Pos(), Op: lexer.TOKEN_NOT, Operand: other}
}

// ensureHoldsByReturnReflexivity reports whether the postcondition `clause` is `result == E` (in
// either operand order) where `E` is structurally identical to the returned expression `retVal` and
// is a pure expression (see pureLeafEqual). Such a clause holds by definition: at the return point `result` is exactly the
// value of `retVal`, and re-reading the same side-effect-free places yields the same value. Sound
// because the ensure side passed the contract purity check, so equal pure expressions denote one value.
func ensureHoldsByReturnReflexivity(clause ast.Expr, retVal ast.Expr) bool {
	if retVal == nil {
		return false
	}
	bin, ok := clause.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.LoweredCall != nil || bin.Op != lexer.TOKEN_EQEQ {
		return false
	}
	leftIsResult := isResultIdent(bin.Left)
	rightIsResult := isResultIdent(bin.Right)
	if leftIsResult == rightIsResult {
		return false // need exactly one side to be `result`
	}
	other := bin.Right
	if rightIsResult {
		other = bin.Left
	}
	return pureLeafEqual(other, retVal)
}

// ensureHoldsByWrittenField discharges a void-exit postcondition `LHS == RHS` by resolving each
// field-place operand (`self.f`) to the last value the body wrote to it (recorded in writtenField,
// dropped at every mutation/borrow/mutating-callee of the root). It proves two shapes:
//
//	ensure self.f == 0                       — self.f resolves to 0, the other side is the constant 0
//	ensure self.a == self.b                  — both resolve to the SAME value expr
//
// A side with no field fact resolves to itself, so `self.f == 0` (RHS already constant) and a
// field-vs-field equality both work. Discharge is then either constant-equality or syntactic equality
// of the resolved value exprs — never arithmetic or calls, so a stale or unrelated write can't falsely
// prove. Returns false unless at least one operand was actually resolved from a written-field fact
// (otherwise this adds nothing the SMT lane didn't already try).
func (a *Analyzer) ensureHoldsByWrittenField(clause ast.Expr) bool {
	bin, ok := clause.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.LoweredCall != nil || bin.Op != lexer.TOKEN_EQEQ {
		return false
	}
	left, leftResolved := a.resolveWrittenFieldOperand(bin.Left)
	right, rightResolved := a.resolveWrittenFieldOperand(bin.Right)
	if !leftResolved && !rightResolved {
		return false
	}
	if left == nil || right == nil {
		return false
	}
	if lc, ok := a.evalConstExpr(left); ok {
		if rc, ok := a.evalConstExpr(right); ok {
			return constScalarsEqual(lc, rc)
		}
		return false
	}
	// Non-constant values (e.g. both fields written `<- self.total_flexible_size`) prove only by
	// syntactic equality of the pure place reads — exprsSyntacticallyEqual admits only ident/field
	// projections, never calls or arithmetic, so this stays sound.
	return exprsSyntacticallyEqual(left, right)
}

// constScalarsEqual reports whether two constant values are equal in a SCALAR kind (int/bool/float/
// string). Aggregate kinds (tuple/list/record/optional) decline — equality of those would need a
// deep, kind-aware comparison this fast-path does not attempt, so it falls to the runtime check.
func constScalarsEqual(x, y ConstValue) bool {
	if x.Kind != y.Kind {
		return false
	}
	switch x.Kind {
	case ConstInt:
		return x.Int == y.Int
	case ConstBool:
		return x.Bool == y.Bool
	case ConstFloat:
		return x.Float == y.Float
	case ConstString:
		return x.String == y.String
	default:
		return false
	}
}

// resolveWrittenFieldOperand returns the last-written value of a field-place operand (`self.f` →
// its recorded RHS) and ok=true when a live written-field fact was used; otherwise it returns the
// operand unchanged with ok=false (a literal or unresolved place is its own value).
func (a *Analyzer) resolveWrittenFieldOperand(expr ast.Expr) (ast.Expr, bool) {
	field, ok := unwrapParen(expr).(*ast.FieldExpr)
	if !ok || field == nil {
		return expr, false
	}
	key := smtProjectionName(field)
	if !isPureProjectionKey(key) {
		return expr, false
	}
	if v, ok := a.lookupWrittenField(key); ok {
		return v, true
	}
	return expr, false
}

func isResultIdent(e ast.Expr) bool {
	if p, ok := e.(*ast.ParenExpr); ok {
		return isResultIdent(p.Inner)
	}
	id, ok := e.(*ast.Ident)
	return ok && id != nil && id.Name == "result"
}

func (a *Analyzer) tryProveEnsureByDisjunctiveResultRange(clause ast.Expr, ret ast.Expr) bool {
	subject, constants, ok := resultEqualityDisjunction(a, clause)
	if !ok || !isResultIdent(subject) || len(constants) == 0 {
		return false
	}
	min, max := constants[0], constants[0]
	seen := map[int64]bool{}
	for _, c := range constants {
		seen[c] = true
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if int64(len(seen)) != max-min+1 {
		return false
	}
	if typ := a.exprType(ret); typ != nil {
		if _, _, ok := BitIntInfo(typ); !ok {
			return false
		}
	}
	if id, ok := stripOptimizationParens(ret).(*ast.Ident); ok && id != nil {
		if r, found := a.lookupRangeFact(id.Name); found && r.loKnown && r.hiKnown && r.lo >= min && r.hi <= max {
			return true
		}
		if c, found := a.writtenConstInt(id.Name); found && c >= min && c <= max {
			return true
		}
	}
	if c, ok := a.constIntValue(ret); ok {
		return c >= min && c <= max
	}
	return false
}

func resultEqualityDisjunction(a *Analyzer, expr ast.Expr) (ast.Expr, []int64, bool) {
	expr = stripOptimizationParens(expr)
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin != nil && bin.Op == lexer.TOKEN_OR {
		leftSubject, leftConsts, ok := resultEqualityDisjunction(a, bin.Left)
		if !ok {
			return nil, nil, false
		}
		rightSubject, rightConsts, ok := resultEqualityDisjunction(a, bin.Right)
		if !ok || !exprsSyntacticallyEqual(leftSubject, rightSubject) {
			return nil, nil, false
		}
		return leftSubject, append(leftConsts, rightConsts...), true
	}
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.LoweredCall != nil || bin.Op != lexer.TOKEN_EQEQ {
		return nil, nil, false
	}
	if isResultIdent(bin.Left) {
		if c, ok := a.constIntValue(bin.Right); ok {
			return bin.Left, []int64{c}, true
		}
	}
	if isResultIdent(bin.Right) {
		if c, ok := a.constIntValue(bin.Left); ok {
			return bin.Right, []int64{c}, true
		}
	}
	return nil, nil, false
}
