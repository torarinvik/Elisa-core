package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

type recursionEdge struct {
	Caller *ast.FuncDecl
	Callee *ast.FuncDecl
	Call   *ast.CallExpr
}

// Recursive-function termination via `decreases` (docs/86 brick 86-7).
//
// Dafny's termination story: a function annotated with a `decreases <measure>` clause proves it
// terminates by showing the measure strictly decreases — and stays bounded below — at every
// recursive call. A strictly-decreasing sequence of naturals is finite, so the recursion bottoms
// out. This brick implements that for DIRECT self-recursion, reusing the bounded-linear prover
// (substitutedAffine from brick 86-5): the measure at a recursive call is the measure expression
// with the callee's parameters substituted by the call's arguments, and the obligation is that
// `measure(params) - measure(args)` is a provably-positive constant.
//
// Opt-in and additive: termination is checked ONLY when a `decreases` clause is present, so existing
// recursive code without the clause is unaffected (status quo — no termination guarantee). When the
// clause IS present it is an explicit claim, so an unprovable measure is a hard error, like a
// `requires` under -strict.
//
// Scope: direct self-recursion. Mutual recursion (a calls b calls a) and while-loop variants are
// deferred follow-ups (documented in docs/86 §9). A `decreases` measure may be a lexicographic tuple
// (multiple clauses): the obligation is satisfied at the first component that strictly decreases with
// all earlier components provably unchanged.

// checkTermination verifies the `decreases` measure of a function decreases at every direct
// self-recursive call. Called with the function's parameters in scope (after analyzeRequiresClauses).
func (a *Analyzer) checkTermination(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil {
		return
	}
	// `decreases * "reason"` — explicit opt-out: suppresses the termination obligation entirely.
	// A missing reason string is always a compile error (trust must never be silent).
	// Soundness: DecreasesWild does NOT set verifiedTerminating, so defining-equation axiomatization
	// is NOT enabled — an inconsistency (via non-termination) cannot be introduced.
	if fn.DecreasesWild != "" {
		if fn.DecreasesWild == "*" {
			// Sentinel: `decreases *` was present but no reason string was supplied.
			a.errorf(fn.Pos(), "`decreases *` on %q requires a non-empty reason string: write `decreases * \"reason\"`", fn.Name)
		}
		// Reason present (or error already reported): suppress the termination proof
		// but do NOT mark verifiedTerminating — soundness-critical.
		return
	}
	if len(fn.Decreases) == 0 {
		return
	}
	measures := decreaseMeasureComponents(fn.Decreases)
	if len(measures) == 0 {
		return
	}
	if len(measures) == 1 && a.isStructuralDecreaseMeasure(fn, measures[0]) {
		if a.checkStructuralTermination(fn, measures[0]) {
			a.recordProof(measures[0].Pos(), "structural termination of "+fn.Name, "decreases", ProofProvenLinear)
		} else {
			a.recordProof(measures[0].Pos(), "structural termination of "+fn.Name, "decreases", ProofRefuted)
			a.errorf(measures[0].Pos(), "cannot prove structural `decreases` for %q: recursive calls must pass values bound from a match on the decreasing enum parameter", fn.Name)
		}
		return
	}
	// Type-check the measure components: each must be an integer (the prover reasons over integers).
	for _, m := range measures {
		if m == nil {
			continue
		}
		t := a.analyzeExpr(m)
		if t != nil && (!IsNumericType(t) || IsFloatType(t)) {
			a.errorf(m.Pos(), "decreases measure must be an integer, got %s", t)
		}
	}
	calls := a.collectSelfRecursiveCalls(fn)
	if len(calls) == 0 {
		edges := a.collectRecursiveSCCEdges(fn)
		if len(edges) > 0 {
			if a.mutualRecursionVerified(fn, edges) {
				a.recordProof(measures[0].Pos(), "mutual termination of "+fn.Name, "decreases", ProofProvenLinear)
			} else {
				a.recordProof(measures[0].Pos(), "mutual termination of "+fn.Name, "decreases", ProofRefuted)
				a.errorf(measures[0].Pos(), "cannot prove the `decreases` measure strictly decreases across the mutually-recursive cycle containing %q; the cycle may not terminate", fn.Name)
			}
			return
		}
		// A `decreases` on a non-self-recursive function is harmless but pointless — flag it so the
		// user knows the clause is doing nothing (and isn't masking a typo'd recursive call).
		a.warnf(measures[0].Pos(), "`decreases` on %q, which makes no direct recursive call; the termination clause is unused", fn.Name)
		return
	}
	for _, call := range calls {
		cert, ok := a.directNumericTerminationCertificate(fn, call)
		if ok {
			a.recordProof(cert.pos(), "termination of "+fn.Name+" ("+cert.label()+")", "decreases", cert.Outcome)
			continue
		}
		a.recordProof(cert.pos(), "termination of "+fn.Name+" ("+cert.label()+")", "decreases", ProofRefuted)
		witness := ""
		if cert.Witness != "" {
			witness = " — " + cert.Witness
		}
		a.errorf(call.Pos(), "cannot prove the `decreases` measure strictly decreases at this recursive call to %q; the function may not terminate%s", fn.Name, witness)
	}
	if a.definingEquationCache != nil {
		delete(a.definingEquationCache, fn)
	}
}

// typeLoopSubstCastsForSMT quietly populates exprTypes for any value-form numeric CastExpr appearing
// in a loop-body substitution value, so the SMT cast translator (which keys off operand/result types)
// can model it. It analyzes only the cast subexpressions, under diagnostic suppression, on the current
// (pristine pre-loop) scope — the same scope the loop variables and measure already resolve in. It is
// purely a type-recording side effect: it never changes which substitutions exist or what they mean.
func (a *Analyzer) typeLoopSubstCastsForSMT(subst map[string]ast.Expr) {
	if a == nil || len(subst) == 0 {
		return
	}
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	for _, v := range subst {
		a.typeCastNodesInExpr(v)
	}
}

// typeCastNodesInExpr walks a pure-arith substitution value and analyzes each CastExpr it contains
// (recording exprTypes for the cast and, transitively, its operand). Non-cast arithmetic nodes need no
// per-node type for the SMT translator, so only casts are analyzed.
func (a *Analyzer) typeCastNodesInExpr(expr ast.Expr) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Inner)
		}
	case *ast.UnaryExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Operand)
		}
	case *ast.BinaryExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Left)
			a.typeCastNodesInExpr(n.Right)
		}
	case *ast.CastExpr:
		if n != nil {
			a.analyzeExpr(n) // records exprTypes for n and its operand on the current scope
		}
	}
}

// collectSelfRecursiveCalls returns every direct call `fn.Name(...)` syntactically inside fn's body.
// Direct-by-name only (mirrors resolveDirectCallFuncDecl's conservatism): an indirect or method call
// is not recognized as recursion, so it never gets a (false) termination obligation.
func (a *Analyzer) collectSelfRecursiveCalls(fn *ast.FuncDecl) []*ast.CallExpr {
	var calls []*ast.CallExpr
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		if call, ok := expr.(*ast.CallExpr); ok && call != nil {
			if ident, ok := call.Func.(*ast.Ident); ok && ident != nil && ident.Name == fn.Name {
				calls = append(calls, call)
			}
		}
		return false // visit every node; never short-circuit
	})
	return calls
}

// substForSelfCall maps each parameter name to the corresponding argument expression of a recursive
// call. A parameter without a positional argument is absent — a measure mentioning it then leaves the
// fragment and the decrease cannot be proven (sound: the call is reported as non-decreasing).
func (a *Analyzer) substForSelfCall(fn *ast.FuncDecl, call *ast.CallExpr) map[string]ast.Expr {
	subst := map[string]ast.Expr{}
	for i, param := range fn.Params {
		if i >= len(call.Args) || call.Args[i] == nil {
			continue
		}
		subst[param.Name] = call.Args[i]
	}
	return subst
}

// decreaseMeasureComponents normalizes both accepted spellings of a lexicographic measure:
// repeated clauses (`decreases a` / `decreases b`) and tuple syntax (`decreases (a, b)`).
func decreaseMeasureComponents(measures []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, 0, len(measures))
	for _, measure := range measures {
		appendDecreaseMeasureComponents(&out, measure)
	}
	return out
}

func appendDecreaseMeasureComponents(out *[]ast.Expr, measure ast.Expr) {
	switch m := measure.(type) {
	case *ast.ParenExpr:
		if m != nil {
			appendDecreaseMeasureComponents(out, m.Inner)
		}
	case *ast.TupleExpr:
		if m != nil {
			for _, elem := range m.Elems {
				appendDecreaseMeasureComponents(out, elem)
			}
		}
	default:
		if measure != nil {
			*out = append(*out, measure)
		}
	}
}

// proveMeasureDecreases proves the lexicographic measure tuple strictly decreases from entry
// (parameters symbolic) to a recursive call (parameters substituted by arguments). The tuple
// decreases iff some component k strictly decreases while every earlier component is provably
// unchanged. The deciding component must also be bounded below by 0 — otherwise an ever-decreasing
// measure would never bottom out.
func (a *Analyzer) proveMeasureDecreases(measures []ast.Expr, subst map[string]ast.Expr) bool {
	measures = decreaseMeasureComponents(measures)
	for k, measure := range measures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.measureDiffIsZero(measures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		if a.measureStrictlyDecreases(measure, subst) && a.measureBoundedBelow(measure) {
			return true
		}
	}
	return false
}

// measureDiff returns the affine form of `measure(params) - measure(args)` — the amount the measure
// drops from entry to the recursive call. The entry side treats parameters as symbolic variables
// (affineOf admits immutable integer params); the call side substitutes arguments (substitutedAffine).
func (a *Analyzer) measureDiff(measure ast.Expr, subst map[string]ast.Expr) (affineForm, bool) {
	entry, ok := a.affineOf(measure, a.currentScope)
	if !ok {
		return affineForm{}, false
	}
	call, ok := a.substitutedAffine(measure, subst)
	if !ok {
		return affineForm{}, false
	}
	return subtractAffine(entry, call), true
}

// measureStrictlyDecreases reports whether the measure provably drops by a positive amount: the
// difference `entry - call` has a provably-positive lower bound. A constant positive difference
// (the `decreases n` + `f(n-1)` case → +1) is the common, always-sound result.
func (a *Analyzer) measureStrictlyDecreases(measure ast.Expr, subst map[string]ast.Expr) bool {
	diff, ok := a.measureDiff(measure, subst)
	if !ok {
		return false
	}
	r := a.boundAffine(diff, a.currentScope)
	return r.loKnown && r.lo > 0
}

// measureDiffIsZero reports whether an (earlier, lexicographic) measure component is provably
// unchanged across the recursive call — the difference is exactly the constant 0.
func (a *Analyzer) measureDiffIsZero(measure ast.Expr, subst map[string]ast.Expr) bool {
	diff, ok := a.measureDiff(measure, subst)
	if !ok {
		return false
	}
	r := a.boundAffine(diff, a.currentScope)
	return r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0
}

// checkLoopTermination verifies a `decreases` measure leading a `while` body strictly decreases on
// every iteration and is bounded below — so the loop runs finitely. Unlike function termination
// (immutable params, affine measure), a loop's variables are MUTABLE, so the measure is not affine;
// the obligations are discharged by the same SMT implication used for invariant preservation, with the
// loop variables FREE and constrained only by the loop condition + invariants:
//
//	DECREASE:        cond ∧ invariants ⊢ measure > measure[vars := post-body]
//	BOUNDED-BELOW:   cond ∧ invariants ⊢ measure >= 0
//
// post-body is the measure with each loop variable replaced by its net body effect (captureLoopBodyEffect,
// the same simultaneous substitution the preservation proof uses). A lexicographic tuple decreases iff
// some component strictly drops while every earlier component is provably unchanged across the body.
//
// Opt-in and additive (mirrors function `decreases`): checked ONLY when a `decreases` clause leads the
// loop body. An unprovable measure declines to the existing runtime/progress backstop: the proof report
// records ProofRuntime and strict-proof mode may escalate the advisory lint, but acceptance never rests
// on trusting an unproved termination claim. Called on the pristine pre-loop scope (before the body
// mutates the loop variables).
func (a *Analyzer) checkLoopTermination(stmt *ast.WhileStmt) {
	if a == nil || stmt == nil {
		return
	}
	decs := leadingDecreases(stmt.Body)
	if len(decs) == 0 {
		return
	}
	// A tracked `can Unsafe.AssumeProgress` / `can Unsafe.NonProgress` (or their `trusted`
	// firewall equivalents) in scope discharges the loop's termination obligation just like it
	// discharges the progress-safety obligation: the author asserts the measure makes progress
	// (or the non-termination is intentional) where this analyzer cannot prove it. The proof is
	// still type-checked below for an integer measure, but the unprovable-measure proofLint is
	// suppressed and the proof is recorded as ProofRuntime (auditable: the `can` surfaces
	// Unsafe.AssumeProgress/NonProgress in the function's effect signature — see analyzeCanStmt).
	terminationAssumed := a.currentGrantedAssumeProgressDepth > 0 || a.currentTrustedAssumeProgressDepth > 0 ||
		a.currentGrantedNonProgressDepth > 0 || a.currentTrustedNonProgressDepth > 0
	measures := make([]ast.Expr, 0, len(decs))
	for _, d := range decs {
		measures = append(measures, decreaseMeasureComponents([]ast.Expr{d.Cond})...)
	}
	for _, m := range measures {
		t := a.analyzeExpr(m)
		if t != nil && (!IsNumericType(t) || IsFloatType(t)) {
			a.errorf(m.Pos(), "loop `decreases` measure must be an integer, got %s", t)
		}
	}
	subst, captured := a.captureLoopBodyEffectForTermination(stmt.Body)
	if !captured {
		// The body is not straight-line (it contains `if`/`break`/`return`, etc.). Attempt GUARD-`if`
		// path modeling: enumerate the body's mutually-exclusive paths and require the measure to
		// strictly decrease (and stay >= 0) on EVERY non-exiting path. ALL paths must discharge — a
		// single non-decreasing non-exit path (including the implicit no-op `else`) rejects the loop.
		if paths, ok := a.enumerateLoopBodyPaths(stmt.Body); ok {
			for _, p := range paths {
				a.typeLoopSubstCastsForSMT(p.subst)
			}
			invs := leadingInvariants(stmt.Body)
			if a.proveLoopMeasureDecreasesAllPaths(stmt.Cond, invs, measures, paths) {
				a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofProvenSMT)
				return
			}
		}
		a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofRuntime)
		if !terminationAssumed {
			a.proofLint(decs[0].Pos(), "loop `decreases` measure could not be proven: the body has effects this analyzer cannot model (calls, non-arithmetic writes, or control flow); falling back to the runtime loop-progress check")
		}
		return
	}
	// The SMT term translator models a value-form numeric cast (`(i - 1).usize()`) by reading the
	// recorded types of the cast node and its operand (analyzer_smt_discharge.go CastExpr case). The
	// loop body has NOT been analyzed yet at this point (termination runs on the pristine pre-loop
	// scope, before the body), so those body-RHS cast nodes carry no exprTypes. Type the substitution
	// values now — quietly (suppressing diagnostics: the body's later real analysis re-emits any genuine
	// diagnostic) — so the cast translator can see through the conversion. A cast whose types still
	// cannot be resolved simply makes the SMT translation decline (sound: a declined term forgoes the
	// proof, never fabricates a decrease).
	a.typeLoopSubstCastsForSMT(subst)
	invs := leadingInvariants(stmt.Body)
	proven, smtCE := a.proveLoopMeasureDecreases(stmt.Cond, invs, measures, subst)
	if proven {
		a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofProvenSMT)
		return
	}
	a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofRuntime)
	if terminationAssumed {
		return
	}
	witness := a.loopLexicographicDecreaseDiagnostic(stmt.Cond, invs, measures, subst)
	if witness != "" {
		witness = " — " + witness
	}
	a.proofLint(decs[0].Pos(), "loop `decreases` measure could not be proven to strictly decrease and stay >= 0 on every iteration; falling back to the runtime loop-progress check%s%s", witness, a.counterexampleSuffix(smtCE))
}

// loopLexicographicDecreaseDiagnostic mirrors proveLoopMeasureDecreases to name the deciding
// lexicographic component (the first whose strictly-earlier components are all provably unchanged) that
// the prover could not show strictly decreases across one iteration, rendering its measure→body-effect
// transition as the witness. Advisory only — it never re-decides the proof.
func (a *Analyzer) loopLexicographicDecreaseDiagnostic(cond ast.Expr, invs []*ast.ContractStmt, measures []ast.Expr, subst map[string]ast.Expr) string {
	if len(measures) == 0 {
		return ""
	}
	for k, measure := range measures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.proveLoopMeasureUnchanged(cond, invs, measures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		isLast := k == len(measures)-1
		if !isLast && a.proveLoopMeasureUnchanged(cond, invs, measure, subst) {
			continue
		}
		if proven, _ := a.proveLoopMeasureComponentDecreases(cond, invs, measure, subst); proven {
			continue
		}
		comp := unparse.FormatExpr(measure)
		call := comp
		if post, ok := substituteLemmaEnsure(measure, subst); ok {
			call = unparse.FormatExpr(post)
		}
		if len(measures) == 1 {
			return "measure `" + comp + "` not proven to strictly decrease across the loop body: " + comp + " -> " + call
		}
		return "lexicographic decreases: component `" + comp + "` not proven to strictly decrease across the loop body: " + comp + " -> " + call
	}
	return ""
}

// proveLoopMeasureDecreases proves the lexicographic measure tuple strictly decreases across one loop
// iteration under `cond ∧ invariants`, with the loop variables free and the body effect substituted.
// On success it returns (true, ""). On failure it returns (false, ce) where ce is z3's counterexample
// string for the first component that could not be proven (empty when no SMT counterexample is available).
func (a *Analyzer) proveLoopMeasureDecreases(cond ast.Expr, invs []*ast.ContractStmt, measures []ast.Expr, subst map[string]ast.Expr) (bool, string) {
	for k, measure := range measures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.proveLoopMeasureUnchanged(cond, invs, measures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		if proven, ce := a.proveLoopMeasureComponentDecreases(cond, invs, measure, subst); proven {
			return true, ""
		} else {
			return false, ce
		}
	}
	return false, ""
}

// proveLoopMeasureComponentDecreases proves `cond ∧ invs ⊢ measure > measure[body]` AND
// `cond ∧ invs ⊢ measure >= 0` — one component strictly decreasing and bounded below.
// Returns (true, "") on success. On failure returns (false, ce) where ce is z3's counterexample
// string for the failing obligation (empty when SMT was not reached or produced no model).
func (a *Analyzer) proveLoopMeasureComponentDecreases(cond ast.Expr, invs []*ast.ContractStmt, measure ast.Expr, subst map[string]ast.Expr) (bool, string) {
	post, ok := substituteLemmaEnsure(measure, subst)
	if !ok {
		return false, ""
	}
	decrease := &ast.BinaryExpr{Position: measure.Pos(), Op: lexer.TOKEN_GT, Left: measure, Right: post}
	bounded := &ast.BinaryExpr{Position: measure.Pos(), Op: lexer.TOKEN_GTEQ, Left: measure, Right: &ast.IntLit{Position: measure.Pos(), Value: "0"}}
	empty := map[string]ast.Expr{}
	if dec, ce := a.proveLoopPreservationSMT(cond, invs, decrease, empty, nil, nil); !dec {
		return false, ce
	}
	bnd, ce := a.proveLoopPreservationSMT(cond, invs, bounded, empty, nil, nil)
	return bnd, ce
}

// proveLoopMeasureUnchanged proves an earlier lexicographic component is invariant across the body:
// `cond ∧ invs ⊢ measure == measure[body]`.
func (a *Analyzer) proveLoopMeasureUnchanged(cond ast.Expr, invs []*ast.ContractStmt, measure ast.Expr, subst map[string]ast.Expr) bool {
	post, ok := substituteLemmaEnsure(measure, subst)
	if !ok {
		return false
	}
	eq := &ast.BinaryExpr{Position: measure.Pos(), Op: lexer.TOKEN_EQEQ, Left: measure, Right: post}
	same, _ := a.proveLoopPreservationSMT(cond, invs, eq, map[string]ast.Expr{}, nil, nil)
	return same
}

// leadingDecreases returns the `decreases` contract statements that lead a loop body (mirrors
// leadingInvariants). Only leading clauses are the loop's termination measure.
func leadingDecreases(body []ast.Stmt) []*ast.ContractStmt {
	var out []*ast.ContractStmt
	for _, s := range body {
		cs, ok := s.(*ast.ContractStmt)
		if !ok {
			break
		}
		if cs.Kind == ast.ContractInvariant {
			continue // invariants may interleave with the leading clause group
		}
		if cs.Kind != ast.ContractDecreases {
			break
		}
		if cs.Cond != nil {
			out = append(out, cs)
		}
	}
	return out
}

// PURE / TOTAL function defining-equation eligibility (feat/recursive-axiom-b).
//
// To let the SMT tier reason THROUGH a `def` (and perform induction over a recursive one), we may
// assert its defining equation `f(args) == body[params:=args]`. This is sound ONLY when the function
// is a true mathematical function: PURE (its result depends only on its arguments, with no effects)
// and TOTAL (it terminates on all inputs the equation is instantiated for). The eligibility predicate
// below is the single soundness gate; emitDefiningEquation consults it before emitting anything.

// functionDefiningEquationEligible reports (and caches) whether a function's defining equation may be
// soundly assumed by the SMT tier. Conservative by construction: anything it is unsure about is
// ineligible, which only forgoes completeness, never soundness.
func (a *Analyzer) functionDefiningEquationEligible(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil {
		return false
	}
	if a.definingEquationCache == nil {
		a.definingEquationCache = map[*ast.FuncDecl]bool{}
	}
	if v, ok := a.definingEquationCache[decl]; ok {
		return v
	}
	// Compute under a guard so a self-recursive purity check (body references the function) does not
	// recurse forever; assume eligible-so-far while deciding, which is safe because the final verdict is
	// the conjunction of all gates and a false gate overrides.
	if a.definingEquationInProgress == nil {
		a.definingEquationInProgress = map[*ast.FuncDecl]bool{}
	}
	if a.definingEquationInProgress[decl] {
		return true
	}
	a.definingEquationInProgress[decl] = true
	defer delete(a.definingEquationInProgress, decl)
	ok := a.computeDefiningEquationEligible(decl)
	a.definingEquationCache[decl] = ok
	return ok
}

func (a *Analyzer) computeDefiningEquationEligible(decl *ast.FuncDecl) bool {
	// A lemma is ghost code with no value-returning body; its IH is handled separately. Never treat it
	// as a value-producing pure function here. A ghost def, unlike a lemma, is intentionally a
	// value-producing spec function, so it stays eligible when it passes the same purity/totality gates.
	if decl.IsLemma {
		return false
	}
	// PURE shape: SMT-exact params/return and a single `return <pure-expr>` body. A pure return
	// expression has no statements that could mutate state, loop, or perform effects.
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if !a.smtEquationResultType(fnType.Return) {
		return false
	}
	for _, pt := range fnType.Params {
		if !a.smtEquationParamType(pt) {
			return false
		}
	}
	body, ok := pureReturnExpr(decl)
	if !ok {
		return false
	}
	if !a.exprIsPureForEquation(body) {
		return false
	}
	// TOTAL: a function that makes ANY direct self-recursive call must have a VERIFIED `decreases`
	// measure — otherwise its "equation" may be inconsistent (non-termination ⇒ no fixed point). A
	// non-recursive pure function is trivially total.
	selfCalls := a.collectSelfRecursiveCalls(decl)
	if len(selfCalls) > 0 {
		return a.measureVerifiedForCalls(decl, selfCalls)
	}
	edges := a.collectRecursiveSCCEdges(decl)
	if len(edges) == 0 {
		return true
	}
	return a.mutualRecursionVerified(decl, edges)
}

// measureVerifiedForCalls proves (read-only, no diagnostics) that decl's `decreases` measure strictly
// decreases at every supplied self-recursive call. Shared by lemma-IH and function-equation gating.
func (a *Analyzer) measureVerifiedForCalls(decl *ast.FuncDecl, calls []*ast.CallExpr) bool {
	// A `decreases *` wildcard suppresses the termination proof obligation — but it does NOT make
	// the function verifiedTerminating. Axiom/defining-equation eligibility requires a PROVEN
	// termination measure; a mere opt-out cannot grant it (soundness-critical).
	if decl.DecreasesWild != "" {
		return false
	}
	if len(decl.Decreases) == 0 {
		return false
	}
	for _, m := range decl.Decreases {
		if m == nil {
			return false
		}
		if t := a.exprTypes[m]; t != nil && (!IsNumericType(t) || IsFloatType(t)) {
			return false
		}
	}
	// The measure prover reads the function's parameters from a.currentScope. It is only sound to use
	// the live scope when we are analyzing decl itself; for a foreign callee whose params are not in
	// scope, decline (conservative — forgoes the equation, never fabricates it).
	if decl != a.currentFuncDecl {
		return false
	}
	for _, c := range calls {
		subst := a.substForSelfCall(decl, c)
		if !a.proveMeasureDecreases(decl.Decreases, subst) && !a.directNumericSyntacticDecrease(decl, c) {
			return false
		}
	}
	return true
}

func (a *Analyzer) functionPureEquationShape(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.IsLemma || decl.DecreasesWild != "" {
		return false
	}
	if a.definingEquationInProgress == nil {
		a.definingEquationInProgress = map[*ast.FuncDecl]bool{}
	}
	if a.definingEquationInProgress[decl] {
		return true
	}
	a.definingEquationInProgress[decl] = true
	defer delete(a.definingEquationInProgress, decl)
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if !a.smtEquationResultType(fnType.Return) {
		return false
	}
	for _, pt := range fnType.Params {
		if !a.smtEquationParamType(pt) {
			return false
		}
	}
	body, ok := pureReturnExpr(decl)
	return ok && a.exprIsPureShapeForEquation(body)
}

func (a *Analyzer) exprIsPureShapeForEquation(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Object)
	case *ast.ParenExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Inner)
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && a.exprIsPureShapeForEquation(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Left) && a.exprIsPureShapeForEquation(n.Right)
	case *ast.CastExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Operand)
	case *ast.TernaryExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Cond) && a.exprIsPureShapeForEquation(n.Value) && a.exprIsPureShapeForEquation(n.Alt)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil {
			if ident, idOK := n.Func.(*ast.Ident); idOK && ident != nil && a.definingEquationInProgress != nil {
				for inProgress := range a.definingEquationInProgress {
					if inProgress != nil && inProgress.Name == ident.Name {
						for _, arg := range n.Args {
							if !a.exprIsPureShapeForEquation(arg) {
								return false
							}
						}
						return true
					}
				}
			}
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureShapeForEquation(arg) {
				return false
			}
		}
		return a.functionPureEquationShape(decl)
	default:
		return false
	}
}

// exprIsPureForEquation reports whether an expression is a PURE integer expression we may put on the
// RHS of a defining equation: literals, parameter/immutable identifiers, arithmetic, comparisons,
// parens, value-preserving casts, and calls to OTHER functions that are THEMSELVES equation-eligible
// (so purity is transitive and a mutating/effectful helper poisons the whole body). Anything else —
// an address-of, a field/index into mutable state, an unknown call — makes the body impure ⇒ decline.
func (a *Analyzer) exprIsPureForEquation(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.Ident:
		return true
	case *ast.FieldExpr:
		return n != nil && a.exprIsPureForEquation(n.Object)
	case *ast.ParenExpr:
		return n != nil && a.exprIsPureForEquation(n.Inner)
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && a.exprIsPureForEquation(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && a.exprIsPureForEquation(n.Left) && a.exprIsPureForEquation(n.Right)
	case *ast.CastExpr:
		return n != nil && a.exprIsPureForEquation(n.Operand)
	case *ast.TernaryExpr:
		return n != nil && a.exprIsPureForEquation(n.Cond) && a.exprIsPureForEquation(n.Value) && a.exprIsPureForEquation(n.Alt)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil {
			if ident, idOK := n.Func.(*ast.Ident); idOK && ident != nil && a.definingEquationInProgress != nil {
				for inProgress := range a.definingEquationInProgress {
					if inProgress != nil && inProgress.Name == ident.Name {
						for _, arg := range n.Args {
							if !a.exprIsPureForEquation(arg) {
								return false
							}
						}
						return true
					}
				}
			}
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureForEquation(arg) {
				return false
			}
		}
		// Transitive purity: a callee must itself be a pure equation-eligible function. The in-progress
		// guard in functionDefiningEquationEligible breaks self/mutual recursion (returns true for the
		// node currently being decided), so this terminates.
		return a.functionDefiningEquationEligible(decl)
	default:
		return false
	}
}

func (a *Analyzer) smtEquationResultType(t Type) bool {
	t = stripRefForBounds(t)
	return t != nil && ((IsNumericType(t) && !IsFloatType(t)) || IsBoolType(t))
}

func (a *Analyzer) smtEquationParamType(t Type) bool {
	t = stripRefForBounds(t)
	if t == nil {
		return false
	}
	if (IsNumericType(t) && !IsFloatType(t)) || IsBoolType(t) || a.smtEquationStructType(t) {
		return true
	}
	return false
}

func (a *Analyzer) smtEquationStructType(t Type) bool {
	st, ok := stripRefForBounds(t).(*StructType)
	if !ok || st == nil || len(st.Fields) == 0 {
		return false
	}
	for _, field := range st.Fields {
		ft := stripRefForBounds(field.Type)
		if !((IsNumericType(ft) && !IsFloatType(ft)) || IsBoolType(ft)) {
			return false
		}
	}
	return true
}

// measureBoundedBelow reports whether the measure cannot fall below 0. An unsigned-typed measure is
// non-negative by construction (the common, zero-friction case: `decreases n` with `n: usize`).
// Otherwise the measure's affine form must bound to a non-negative lower value under the current
// facts. Without a provable floor, a strictly-decreasing measure could run forever, so we decline.
func (a *Analyzer) measureBoundedBelow(measure ast.Expr) bool {
	if t := a.exprTypes[measure]; t != nil && indexTypeGuaranteedNonNegative(t) {
		return true
	}
	form, ok := a.affineOf(measure, a.currentScope)
	if !ok {
		return false
	}
	r := a.boundAffine(form, a.currentScope)
	return r.loKnown && r.lo >= 0
}
