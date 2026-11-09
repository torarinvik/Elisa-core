package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// checkLoopInvariants verifies `invariant` clauses that lead a `while` body as INDUCTIVE loop
// invariants (docs/87 — loop-invariant preservation), rather than the previous behaviour of
// silently trusting them. For each invariant it discharges two Hoare obligations:
//
//	establishment:  pre-loop facts                ⊢ inv          (the zero-iteration case)
//	preservation:   inv ∧ cond ∧ pre-loop facts   ⊢ inv[body]    (one arbitrary iteration)
//
// where inv[body] is the invariant with each loop variable replaced by its straight-line value at
// the end of the body. When BOTH hold for every invariant — and the body is a straight-line form
// we can fully account for (so it cannot `break` out with the condition still true) — the
// after-loop fact `inv ∧ ¬cond` is sound and is seeded into the enclosing scope so later
// obligations can use it (this is what lets `while i < n: …; i <- i + 1` with `invariant i <= n`
// export `i == n` to the code after the loop).
//
// Discharge reuses the existing ladder: the affine clause prover for constant-bounded invariants,
// then the SMT tier (under -smt) for relational ones (`i <= count`). Anything outside the
// analyzable fragment declines, leaving the in-place runtime invariant check as the backstop — so
// this pass only ever ADDS static guarantees, it never removes the existing safety net.
//
// It is split across the loop's analysis: proveLoopInvariants runs BEFORE the body is analyzed (so
// establishment reads the pristine pre-loop facts — the body's own mutations, e.g. `i <- i + 1`,
// invalidate the entry fact `i == 0` upward through the scope chain), and seedLoopExitFacts runs
// after, exporting `inv ∧ ¬cond` only when every invariant was proven inductive.
func (a *Analyzer) proveLoopInvariants(stmt *ast.WhileStmt) (proven []*ast.ContractStmt, exitFactsSound bool) {
	if a == nil || stmt == nil || a.currentScope == nil {
		return nil, false
	}
	invs := leadingInvariants(stmt.Body)
	if len(invs) == 0 {
		return nil, false
	}
	// The invariants are proven before the body (which holds them) is analyzed, so their nodes carry
	// no types yet and the SMT tier declines on any field access or call. Type them quietly on the
	// pre-loop scope; the in-place analysis of the body re-types them and reports any diagnostics.
	a.typeLoopInvariantsQuietly(invs)
	// Capture the body's net effect on the loop variables as a simultaneous substitution. A body we
	// cannot fully account for (calls, non-arithmetic writes, address-taking, control flow) declines
	// capture — establishment still runs (it needs no body model), but preservation cannot, and no
	// exit fact is exported.
	subst, arrayStores, captured := a.captureLoopBodyEffect(stmt.Body)
	for _, v := range subst {
		a.typeArithNodesForSMT(v)
	}

	// Collect outer proven invariants that are DISJOINT from this inner loop's assigned variables —
	// the conservative check that makes it sound to carry an outer invariant into the inner proof.
	// If the inner body mutates any variable the outer invariant depends on, that outer invariant is
	// excluded (it may have been invalidated). Only identifiers in the substitution keys count as
	// "assigned" for this purpose; variables the inner loop reads but doesn't write are not excluded.
	outerSafeInvs := outerLoopInvariantsDisjointFrom(a.activeOuterLoopInvariants, subst)
	// Preservation may also assume live pre-loop facts about values the loop never writes.
	preserveHyps := append(outerSafeInvs[:len(outerSafeInvs):len(outerSafeInvs)], a.loopFrameFactHypotheses(stmt.Body, subst)...)

	// Establishment scope: a child of the live pre-loop scope additionally carrying POINT range facts
	// for any loop variable pinned to a compile-time constant at entry (`n: usize = 0` records the
	// assert fact `n == 0`, which we lift to the range `[0,0]`). This lets the affine prover establish a
	// constant-bounded invariant (`n <= MAX` from `n == 0`) with no solver. It is local to
	// establishment — the entry value is NEVER admitted into the preservation proof (the body runs on an
	// arbitrary iteration), preserving the inductive-step isolation.
	//
	// Outer proven invariants that are disjoint from this inner loop's mutations are also seeded as
	// assert facts here: they held throughout the outer loop's arbitrary iteration, so they hold at
	// the start of any inner-loop execution too. This lets the inner establishment proof use `j <= n`
	// (for example) when the outer loop already proved `j <= n` inductively.
	establishScope := NewScope(a.currentScope)
	a.seedLoopEntryRangeFacts(establishScope, subst)
	for _, outerInv := range outerSafeInvs {
		a.recordSMTAssertFactInScope(establishScope, outerInv.Cond)
	}

	// inductiveInvs collects the invariants for which BOTH establishment AND preservation were proven
	// statically. These are the ones that are valid as outer-loop hypotheses for nested inner loops
	// and that qualify for seeding exit facts (inv ∧ ¬cond). An invariant whose body capture is
	// unavailable (captured=false) or whose preservation falls back to runtime is NOT included.
	var inductiveInvs []*ast.ContractStmt
	allProven := true
	written := loopWrittenRoots(stmt.Body)
	guarded := map[*ast.ContractStmt]bool{}
	// Establishment: each invariant from the pre-loop facts (plus the entry-value point ranges). The
	// body has not been analyzed yet, so these facts are intact.
	var candidates []*ast.ContractStmt
	for _, inv := range invs {
		established, viaSMT := a.proveLoopClause(inv.Cond, nil, establishScope)
		if !established && stmt.Cond != nil {
			// An invariant is checked at body entry, where the guard holds: establish it under the guard.
			// Such a GUARDED invariant does not hold at loop exit (no exit facts).
			guardScope := NewScope(establishScope)
			a.applyConditionRefinements(guardScope, stmt.Cond, true)
			a.recordSMTAssertFactInScope(guardScope, smtFactExprForCondition(stmt.Cond, true))
			if established, viaSMT = a.proveLoopClause(inv.Cond, nil, guardScope); established {
				guarded[inv] = true
			}
		}
		if !established && !exprReadsAnyRoot(inv.Cond, written) {
			// A frame invariant (it reads nothing the body may write) keeps the entry facts it reads
			// at every loop head, so the in-place check decides it; no inductive lint.
			allProven = false
			continue
		}
		if !established {
			a.recordProof(inv.Pos(), "loop invariant", "establish", ProofRuntime)
			a.deferLoopInvariantLint(inv, "loop invariant could not be established on entry; it is only checked at runtime")
			allProven = false
			continue
		}
		a.recordProof(inv.Pos(), "loop invariant", "establish", provenTier(viaSMT))
		candidates = append(candidates, inv)
	}
	// A body with `if`/`break`/`return` is modeled path by path (the guard-`if` enumeration the
	// termination prover uses); preservation must hold on every non-exiting path.
	var paths []loopBodyPath
	pathsOK := false
	if !captured && len(candidates) != 0 {
		focus := map[string]bool{}
		for _, inv := range invs {
			collectExprIdentNames(inv.Cond, focus)
		}
		if p, ok := a.enumerateLoopBodyPaths(stmt.Body, focus); ok {
			for _, path := range p {
				a.typeLoopSubstCastsForSMT(path.subst)
			}
			paths, pathsOK = p, true
		}
	}
	// When the joint focus (every invariant's variables) is unmodelable, each invariant may still be
	// modeled on its own focus: preservation of `slot < cap` only needs the writes to slot and cap.
	// The sequential model (analyzer_loop_sequential_model.go) is a second, independent body model:
	// an invariant the enumerated paths cannot preserve is retried on it.
	var seqPaths []loopBodyPath
	seqOK := false
	if !captured && len(candidates) != 0 {
		specs := []ast.Expr{stmt.Cond}
		for _, inv := range invs {
			specs = append(specs, inv.Cond)
		}
		if a.seqLoopSpecsReadNoGlobals(specs...) {
			if p, ok := a.sequentialLoopBodyPaths(stmt.Body); ok {
				a.typeSequentialPathsQuietly(p)
				seqPaths, seqOK = p, true
			}
		}
	}
	if !captured && !pathsOK && seqOK {
		paths, pathsOK = seqPaths, true
	}
	// A `return` path leaves the function, not the loop: code after the loop is reached only when
	// the guard fails, unless the body can `break`.
	seqExits := loopBodyBreaks(stmt.Body)
	perInvPaths := map[*ast.ContractStmt][]loopBodyPath{}
	if !captured && !pathsOK {
		for _, inv := range candidates {
			focus := map[string]bool{}
			collectExprIdentNames(inv.Cond, focus)
			if p, ok := a.enumerateLoopBodyPaths(stmt.Body, focus); ok {
				for _, path := range p {
					a.typeLoopSubstCastsForSMT(path.subst)
				}
				perInvPaths[inv] = p
			}
		}
		if len(perInvPaths) == 0 {
			return nil, false
		}
		kept := candidates[:0:0]
		for _, inv := range candidates {
			if _, ok := perInvPaths[inv]; ok {
				kept = append(kept, inv)
			} else {
				allProven = false
			}
		}
		candidates = kept
	}
	// Preservation is proven JOINTLY: each surviving invariant must be preserved under the hypothesis
	// of the surviving SET. An invariant that fails is removed and the rest are re-proven without it,
	// to a fixpoint, so no invariant is "preserved" by leaning on one that is not.
	preserveTier := map[*ast.ContractStmt]ProofOutcome{}
	failedCE := map[*ast.ContractStmt]string{}
	for {
		var failed *ast.ContractStmt
		for _, inv := range candidates {
			if _, done := preserveTier[inv]; done {
				continue
			}
			if captured {
				// Affine fast path (no SMT): ONLY the truthy loop-guard's constant range facts, then the
				// substituted invariant difference bounded over those ranges.
				if a.proveLoopPreservationAffine(stmt.Cond, inv.Cond, subst) {
					preserveTier[inv] = ProofProvenLinear
					continue
				}
				// SMT: the loop variables are FREE, constrained only by the loop condition, the surviving
				// invariants and the disjoint outer invariants, never by their stale pre-loop values.
				// Only after both the ordinary and guarded obligations fail do we retry this same
				// preservation decision under the existing larger per-query budget.
				prove := func() (bool, bool, string) {
					preserved, ce := a.proveLoopPreservationSMT(stmt.Cond, candidates, inv.Cond, subst, arrayStores, outerSafeInvs)
					if preserved || stmt.Cond == nil {
						return preserved, false, ce
					}
					// Guarded preservation: the invariant need only hold at the NEXT body entry, where the
					// guard holds again: prove `cond' implies inv'`.
					guardCE := ""
					preserved, guardCE = a.proveLoopPreservationSMT(stmt.Cond, candidates, guardedInvariantGoal(stmt.Cond, inv.Cond), subst, arrayStores, outerSafeInvs)
					if preserved {
						return true, true, ""
					}
					if ce == "" {
						ce = guardCE
					}
					return false, false, ce
				}
				preserved, guardedUse, ce := a.proveLoopPreservationReported(prove)
				if !preserved {
					failed, failedCE[inv] = inv, ce
					break
				}
				if guardedUse {
					guarded[inv] = true
				}
				preserveTier[inv] = ProofProvenSMT
				continue
			}
			invPaths := paths
			if !pathsOK {
				invPaths = perInvPaths[inv]
			}
			prove := func() (bool, bool, string) {
				preserved, guardedUse, ce := a.proveInvariantOnPaths(stmt.Cond, candidates, inv, invPaths, outerSafeInvs)
				// Fallbacks, each sound and tried only after the standard query fails (extra
				// hypotheses can slow a query the standard set already decides):
				//  - live pre-loop facts about values the body never writes;
				//  - the invariant alone, since a heavy sibling hypothesis can leave the joint query
				//    undecided and an invariant preserved under itself is preserved under any superset.
				if !preserved && len(preserveHyps) > len(outerSafeInvs) {
					preserved, guardedUse, _ = a.proveInvariantOnPaths(stmt.Cond, candidates, inv, invPaths, preserveHyps)
				}
				if !preserved && len(candidates) > 1 {
					preserved, guardedUse, _ = a.proveInvariantOnPaths(stmt.Cond, []*ast.ContractStmt{inv}, inv, invPaths, preserveHyps)
				}
				if !preserved && seqOK && !sameLoopPathSet(invPaths, seqPaths) {
					var seqCE string
					preserved, guardedUse, seqCE = a.proveInvariantOnPaths(stmt.Cond, candidates, inv, seqPaths, outerSafeInvs)
					if !preserved && ce == "" {
						ce = seqCE
					}
				}
				return preserved, guardedUse, ce
			}
			preserved, guardedUse, ce := a.proveLoopPreservationReported(prove)
			if !preserved {
				failed, failedCE[inv] = inv, ce
				break
			}
			if guardedUse {
				guarded[inv] = true
			}
			preserveTier[inv] = ProofProvenSMT
		}
		if failed == nil {
			break
		}
		kept := candidates[:0:0]
		for _, inv := range candidates {
			if inv != failed {
				kept = append(kept, inv)
			}
		}
		candidates = kept
		allProven = false
		// Every proof so far may have used the removed invariant: re-prove the survivors without it.
		preserveTier = map[*ast.ContractStmt]ProofOutcome{}
		a.recordProof(failed.Pos(), "loop invariant", "preserve", ProofRuntime)
		a.deferLoopInvariantLint(failed, "loop invariant is established on entry but could not be proven preserved by the loop body; it is only checked at runtime"+a.counterexampleSuffix(failedCE[failed]))
	}
	for _, inv := range candidates {
		a.recordProof(inv.Pos(), "loop invariant", "preserve", preserveTier[inv])
		inductiveInvs = append(inductiveInvs, inv)
		if guarded[inv] {
			// Holds at every body entry, not necessarily when the guard fails: no exit facts.
			allProven = false
		}
	}

	// Sound exit facts require EVERY invariant proven inductive on a body we fully captured. A
	// captured body is straight-line (only assignments and invariant decls), so it has no `break`
	// that could leave the loop with the condition still true — `¬cond` therefore holds at exit.
	// inductiveInvs (not invs) is returned so the caller only pushes PROVEN invariants onto the
	// outer-loop stack — an invariant whose preservation fell back to runtime is NOT admitted as a
	// hypothesis in nested inner loops (it is not guaranteed to hold at every iteration boundary).
	// A body modeled path by path (enumerated or sequential) whose every invariant was proven
	// inductive on every non-exiting path, and which has no `break`, leaves the loop only through
	// the guard (a `return` leaves the function): `inv ∧ ¬cond` holds after the loop exactly as for
	// a captured straight-line body.
	exitSound := captured || ((pathsOK || len(perInvPaths) != 0) && !seqExits)
	return inductiveInvs, allProven && exitSound
}

// proveLoopPreservationReported retries a complete preservation strategy only after its normal
// and sound fallback attempts still fail. The retry runs the same closure and hypotheses under
// withSMTEscalation; proof success still requires the same sound preservation path to accept the
// obligation. Callers use this only immediately before they would otherwise remove the invariant
// and report a runtime
// fallback, so successful proofs and unrelated queries keep their ordinary budget.
func (a *Analyzer) proveLoopPreservationReported(prove func() (bool, bool, string)) (bool, bool, string) {
	preserved, guarded, ce := prove()
	if preserved {
		return preserved, guarded, ce
	}
	retryCE := ""
	a.withSMTEscalation(func() bool {
		var retryGuarded bool
		preserved, retryGuarded, retryCE = prove()
		if preserved {
			guarded = retryGuarded
		}
		return preserved
	})
	if retryCE != "" {
		ce = retryCE
	}
	return preserved, guarded, ce
}

// proveInvariantOnPaths proves inv preserved on every non-exiting path; guardedUse reports that
// some path needed the guarded form (the invariant then holds only at body entry).
func (a *Analyzer) proveInvariantOnPaths(cond ast.Expr, candidates []*ast.ContractStmt, inv *ast.ContractStmt, paths []loopBodyPath, outer []*ast.ContractStmt) (bool, bool, string) {
	guardedUse := false
	read := map[string]bool{}
	collectExprIdentNames(inv.Cond, read)
	if cond != nil {
		collectExprIdentNames(cond, read)
	}
	for _, path := range paths {
		if path.exits {
			continue
		}
		// Only the post-state of variables the goal reads enters the obligation; an update the
		// translator cannot model for an unrelated variable must not decline it.
		subst := map[string]ast.Expr{}
		for name, v := range path.subst {
			if read[name] {
				subst[name] = v
			}
		}
		pathCond := buildPathCond(cond, path.conds)
		preserved, ce := a.proveLoopPreservationSMT(pathCond, candidates, inv.Cond, subst, nil, outer)
		if preserved {
			continue
		}
		if cond == nil {
			return false, false, ce
		}
		if ok, _ := a.proveLoopPreservationSMT(pathCond, candidates, guardedInvariantGoal(cond, inv.Cond), subst, nil, outer); !ok {
			return false, false, ce
		}
		guardedUse = true
	}
	return true, guardedUse, ""
}

func sameLoopPathSet(x, y []loopBodyPath) bool {
	if len(x) != len(y) {
		return false
	}
	if len(x) == 0 {
		return true
	}
	return &x[0] == &y[0]
}

// seedLoopExitFacts records `inv ∧ ¬cond` as after-loop facts on the current scope. Called only with
// the invariants returned by proveLoopInvariants when exitFactsSound — never on its own.
func (a *Analyzer) seedLoopExitFacts(stmt *ast.WhileStmt, invs []*ast.ContractStmt) {
	if a == nil || stmt == nil || a.currentScope == nil {
		return
	}
	for _, inv := range invs {
		a.applyConditionRefinements(a.currentScope, inv.Cond, true)
		// The invariant was proven inductive, so it holds at loop exit (it holds after every iteration,
		// including zero) — record it directly. Do NOT route through canAssumeContractFact here: that
		// helper would try to RE-prove the fact from the (not-yet-seeded) post-loop facts and emit a
		// spurious "invariant could not be proven statically" lint when it can't.
		a.recordSMTAssertFact(inv.Cond)
	}
	a.applyConditionRefinements(a.currentScope, stmt.Cond, false)
	// Also seed the negated loop condition as a direct SMT assert fact. applyConditionRefinements only
	// records CONSTANT-bound range facts (e.g. `i >= 5`); a symbolic guard like `i < n` (n = parameter)
	// produces no range fact, so the post-loop bound `i >= n` is invisible to the SMT tier. Recording
	// `not(cond)` explicitly lets a quantified ensure that bridges the loop's proven invariant to the
	// return site discharge: the solver sees `inv ∧ (i >= n)` and can prove `forall k < n: arr[k]==0`
	// from `forall k < i: arr[k]==0` (docs/90 brick 90-17).
	//
	// Before seeding, drop the STALE range fact for the loop variable (e.g. `i == 0` from its
	// initialisation), which is restored by analyzeBlockWithConditionAffineClone after the body. A stale
	// `i == 0` together with the new `i >= n` is contradictory when n > 0: from a contradiction Z3
	// proves any goal, including false ones. invalidateRangeFacts(condVar) removes the stale entry-point
	// bound; the negated-cond assert fact is the ONLY post-loop bound seeded for the symbolic case.
	if condVar := loopCondMutableVar(stmt.Cond); condVar != "" {
		a.invalidateRangeFacts(condVar)
	}
	a.recordSMTAssertFact(&ast.UnaryExpr{Position: stmt.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: stmt.Cond})
}

func provenTier(viaSMT bool) ProofOutcome {
	if viaSMT {
		return ProofProvenSMT
	}
	return ProofProvenLinear
}

// loopCondMutableVar extracts the name of a mutable loop variable from a simple loop condition of
// the form `ident OP expr` (e.g. `i < n`, `i < 10`, `i <= max`). Returns "" for any other form.
// Used by seedLoopExitFacts to identify which variable's stale entry-point range fact to drop before
// seeding `not(cond)` as an SMT assert: a restored `i==0` range contradicts `i>=n` when n>0.
func loopCondMutableVar(cond ast.Expr) string {
	if cond == nil {
		return ""
	}
	// Strip a top-level `and`: `i < n and name[i] != 0` — take only the outermost left-hand comparison.
	expr := cond
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		expr = bin.Left
	}
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return ""
	}
	switch bin.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
	default:
		return ""
	}
	id, ok := bin.Left.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// leadingInvariants returns the `invariant` contract statements that lead the loop body (up to the
// first non-invariant statement). Only leading invariants are treated inductively; an invariant
// placed mid-body would assert a different program point and is left as an in-place runtime check.
func leadingInvariants(body []ast.Stmt) []*ast.ContractStmt {
	var out []*ast.ContractStmt
	for _, s := range body {
		cs, ok := s.(*ast.ContractStmt)
		if ok && cs != nil && cs.Kind == ast.ContractDecreases {
			// A `decreases` measure may lead or interleave with the invariants; it is not a body effect.
			continue
		}
		if !ok || cs.Kind != ast.ContractInvariant {
			break
		}
		if cs.Cond != nil {
			out = append(out, cs)
		}
	}
	return out
}
