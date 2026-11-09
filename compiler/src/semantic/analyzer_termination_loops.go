package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

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
// checkLoopTermination proves a leading `decreases` measure. Only the invariants proven INDUCTIVE
// (provenInvs) may serve as hypotheses: an unproven invariant holds only as a debug runtime check.
func (a *Analyzer) checkLoopTermination(stmt *ast.WhileStmt, provenInvs []*ast.ContractStmt) {
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
		a.currentGrantedNonProgressDepth > 0 || a.currentTrustedNonProgressDepth > 0 ||
		stmt.RuntimeProgressBackstop
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
	focus := terminationMeasureIdentSet(measures)
	subst, captured := a.captureLoopBodyEffectForTerminationFocused(stmt.Body, focus)
	if !captured {
		// The body is not straight-line (it contains `if`/`break`/`return`, etc.). Attempt GUARD-`if`
		// path modeling: enumerate the body's mutually-exclusive paths and require the measure to
		// strictly decrease (and stay >= 0) on EVERY non-exiting path. ALL paths must discharge — a
		// single non-decreasing non-exit path (including the implicit no-op `else`) rejects the loop.
		if paths, ok := a.enumerateLoopBodyPaths(stmt.Body, focus); ok {
			for _, p := range paths {
				a.typeLoopSubstCastsForSMT(p.subst)
			}
			invs := provenInvs
			if a.proveLoopMeasureDecreasesAllPaths(stmt.Cond, invs, measures, paths) {
				a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofProvenSMT)
				return
			}
		}
		if a.seqLoopSpecsReadNoGlobals(append([]ast.Expr{stmt.Cond}, measures...)...) && a.seqLoopSpecsReadNoGlobals(contractConds(provenInvs)...) {
			if paths, ok := a.sequentialLoopBodyPaths(stmt.Body); ok {
				a.typeSequentialPathsQuietly(paths)
				if a.proveLoopMeasureDecreasesAllPaths(stmt.Cond, provenInvs, measures, paths) {
					a.recordProof(decs[0].Pos(), "termination of loop", "decreases", ProofProvenSMT)
					return
				}
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
	invs := provenInvs
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

func terminationMeasureIdentSet(measures []ast.Expr) map[string]bool {
	focus := map[string]bool{}
	for _, measure := range measures {
		collectArithIdents(measure, focus)
	}
	return focus
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
	ok := a.computeDefiningEquationEligible(decl, false)
	a.definingEquationCache[decl] = ok
	return ok
}

// viewEquationEligible is functionDefiningEquationEligible for a function returning a view: the
// same purity, parameter and totality gates, with an sview result read through its byte parts.
func (a *Analyzer) viewEquationEligible(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil {
		return false
	}
	if v, ok := a.viewEquationCache[decl]; ok {
		return v
	}
	if a.viewEquationCache == nil {
		a.viewEquationCache = map[*ast.FuncDecl]bool{}
	}
	if a.definingEquationInProgress == nil {
		a.definingEquationInProgress = map[*ast.FuncDecl]bool{}
	}
	if a.definingEquationInProgress[decl] {
		return true
	}
	a.definingEquationInProgress[decl] = true
	defer delete(a.definingEquationInProgress, decl)
	ok := a.computeDefiningEquationEligible(decl, true)
	a.viewEquationCache[decl] = ok
	return ok
}

func (a *Analyzer) computeDefiningEquationEligible(decl *ast.FuncDecl, viewResult bool) bool {
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
	if viewResult {
		if _, view := fnType.Return.(*SViewType); !view {
			return false
		}
	} else if !a.smtEquationResultType(fnType.Return) {
		return false
	}
	// Trailing implicit parameters (an injected packed store) are ambient state, not equation inputs;
	// emitDefiningEquationWithEnv admits a call without them only while the frame writes no memory.
	for _, pt := range fnType.Params[:min(funcTypeExplicitParamCount(fnType), len(fnType.Params))] {
		if !a.smtEquationParamType(pt) {
			return false
		}
	}
	body, ok := a.typedPureReturnExpr(decl)
	if !ok {
		return false
	}
	if !a.exprIsPureForEquation(body, decl) {
		return false
	}
	// TOTAL: a function that makes ANY direct self-recursive call must have a VERIFIED `decreases`
	// measure — otherwise its "equation" may be inconsistent (non-termination ⇒ no fixed point). A
	// non-recursive pure function is trivially total.
	selfCalls := a.collectSelfRecursiveCallSites(decl)
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
func (a *Analyzer) measureVerifiedForCalls(decl *ast.FuncDecl, calls []recursiveCallSite) bool {
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
		if t := a.exprType(m); t != nil && (!IsNumericType(t) || IsFloatType(t)) {
			return false
		}
	}
	// The measure prover reads the function's parameters from a.currentScope. It is only sound to use
	// the live scope when we are analyzing decl itself; for a foreign callee whose params are not in
	// scope, decline (conservative — forgoes the equation, never fabricates it).
	if decl != a.currentFuncDecl {
		// A foreign callee's measure was proven when decl itself was analyzed (checkTermination).
		return a.terminationVerified[decl]
	}
	for _, c := range calls {
		subst := a.substForSelfCall(decl, c.Call)
		if _, ok := a.proveMeasureDecreasesOutcomeUnderPath(decl.Decreases, subst, c.Path); !ok && !a.directNumericSyntacticDecrease(decl, c.Call) {
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
	body, ok := a.typedPureReturnExpr(decl)
	return ok && a.exprIsPureShapeForEquation(body, decl)
}

func (a *Analyzer) exprIsPureShapeForEquation(expr ast.Expr, owner *ast.FuncDecl) bool {
	switch n := expr.(type) {
	case *ast.MatchExpr:
		return pureScalarMatchEquation(n)
	case *ast.IntLit, *ast.BoolLit:
		return true
	case *ast.StructLitExpr:
		if n == nil || n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureShapeForEquation(arg, owner) {
				return false
			}
		}
		return true
	case *ast.Ident:
		return a.equationIdentifierIsStable(n, owner)
	case *ast.FieldExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Object, owner)
	case *ast.IndexExpr:
		return (a.pureEquationSViewIndex(n, owner) || a.pureEquationContainerIndex(n, owner)) && a.exprIsPureShapeForEquation(n.Index, owner)
	case *ast.ParenExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Inner, owner)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op != lexer.TOKEN_AMPERSAND && a.exprIsPureShapeForEquation(n.Operand, owner)
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && a.exprIsPureShapeForEquation(n.Left, owner) && a.exprIsPureShapeForEquation(n.Right, owner)
	case *ast.CastExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Operand, owner)
	case *ast.TernaryExpr:
		return n != nil && a.exprIsPureShapeForEquation(n.Cond, owner) && a.exprIsPureShapeForEquation(n.Value, owner) && a.exprIsPureShapeForEquation(n.Alt, owner)
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
							if !a.exprIsPureShapeForEquation(arg, owner) {
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
			if !a.exprIsPureShapeForEquation(arg, owner) {
				return false
			}
		}
		return a.functionPureEquationShape(decl)
	default:
		return false
	}
}

// equationIdentifierIsStable limits equation variables to immutable parameters and compile-time
// constants. In particular, mutable module state must never become a free variable in a recursive
// equation: its value can change between evaluations.
func (a *Analyzer) equationIdentifierIsStable(id *ast.Ident, owner *ast.FuncDecl) bool {
	if id == nil || a == nil {
		return false
	}
	if _, ok := a.constIntValue(id); ok {
		return true
	}
	if _, ok := a.resolvedPrimitiveConstant(id); ok {
		return true
	}
	if owner == nil {
		return false
	}
	for _, param := range owner.Params {
		if param.Name == id.Name {
			return !param.Mutable && !a.paramIsMutable(param)
		}
	}
	return false
}
