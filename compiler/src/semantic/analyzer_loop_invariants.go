package semantic

import (
	"strings"

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
	// Capture the body's net effect on the loop variables as a simultaneous substitution. A body we
	// cannot fully account for (calls, non-arithmetic writes, address-taking, control flow) declines
	// capture — establishment still runs (it needs no body model), but preservation cannot, and no
	// exit fact is exported.
	subst, arrayStores, captured := a.captureLoopBodyEffect(stmt.Body)

	// Collect outer proven invariants that are DISJOINT from this inner loop's assigned variables —
	// the conservative check that makes it sound to carry an outer invariant into the inner proof.
	// If the inner body mutates any variable the outer invariant depends on, that outer invariant is
	// excluded (it may have been invalidated). Only identifiers in the substitution keys count as
	// "assigned" for this purpose; variables the inner loop reads but doesn't write are not excluded.
	outerSafeInvs := outerLoopInvariantsDisjointFrom(a.activeOuterLoopInvariants, subst)

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
	for _, inv := range invs {
		// Establishment: prove from the pre-loop facts (plus the entry-value point ranges). The body has
		// not been analyzed yet, so these facts are intact.
		established, viaSMT := a.proveLoopClause(inv.Cond, nil, establishScope)
		if !established {
			a.recordProof(inv.Pos(), "loop invariant", "establish", ProofRuntime)
			a.proofLint(inv.Pos(), "loop invariant could not be established on entry; it is only checked at runtime")
			allProven = false
			continue
		}
		a.recordProof(inv.Pos(), "loop invariant", "establish", provenTier(viaSMT))
		if !captured {
			allProven = false
			continue
		}
		// Preservation: prove `cond ∧ (every invariant) ⊢ inv[body]`.
		//
		// Affine fast path (no SMT): seed ONLY the truthy loop-guard's constant range facts about the
		// loop variables into a scratch scope, then bound the substituted invariant difference over
		// those ranges. This proves the common constant-bounded inductive step `n < MAX ⊢ n+1 <= MAX`
		// with no solver — the missing hypothesis was the truthy guard, which the body executes under.
		// Sound: the scratch scope carries ONLY guard-derived facts (no stale pre-loop entry values),
		// and the affine difference is bounded conservatively (open/decline on any unknown bound).
		if a.proveLoopPreservationAffine(stmt.Cond, inv.Cond, subst) {
			a.recordProof(inv.Pos(), "loop invariant", "preserve", ProofProvenLinear)
			inductiveInvs = append(inductiveInvs, inv)
			continue
		}
		// SMT fallback (off unless -smt): a dedicated implication where the loop variables are FREE —
		// constrained only by the loop condition and the invariants, never by their (now stale)
		// pre-loop values. That isolation is what makes it sound: a child scope cannot mask the outer
		// loop-variable facts (range-fact lookup intersects the whole chain), so reusing the ambient
		// fact set would let a false invariant like `i < 5` "prove" preserved off the entry value
		// `i == 0`.
		// The outer-loop safe invariants are also passed as additional SMT hypotheses — they are proven
		// inductive by the outer loop and hold throughout its execution, so they are valid assumptions
		// for any inner-loop preservation obligation that doesn't mutate the variables they depend on.
		if preserved, counterexample := a.proveLoopPreservationSMT(stmt.Cond, invs, inv.Cond, subst, arrayStores, outerSafeInvs); !preserved {
			a.recordProof(inv.Pos(), "loop invariant", "preserve", ProofRuntime)
			a.proofLint(inv.Pos(), "loop invariant is established on entry but could not be proven preserved by the loop body; it is only checked at runtime%s", a.counterexampleSuffix(counterexample))
			allProven = false
			continue
		}
		a.recordProof(inv.Pos(), "loop invariant", "preserve", ProofProvenSMT)
		inductiveInvs = append(inductiveInvs, inv)
	}

	// Sound exit facts require EVERY invariant proven inductive on a body we fully captured. A
	// captured body is straight-line (only assignments and invariant decls), so it has no `break`
	// that could leave the loop with the condition still true — `¬cond` therefore holds at exit.
	// inductiveInvs (not invs) is returned so the caller only pushes PROVEN invariants onto the
	// outer-loop stack — an invariant whose preservation fell back to runtime is NOT admitted as a
	// hypothesis in nested inner loops (it is not guaranteed to hold at every iteration boundary).
	return inductiveInvs, allProven && captured
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
		if !ok || cs.Kind != ast.ContractInvariant {
			break
		}
		if cs.Cond != nil {
			out = append(out, cs)
		}
	}
	return out
}

// proveLoopClause proves a boolean invariant clause (a conjunction of arithmetic comparisons) under
// the facts of `scope`, with `subst` replacing each named loop variable by its post-body value. It
// returns whether the clause was proven and whether the proof needed the SMT tier. The affine fast
// path handles constant-bounded clauses; the SMT tier (off unless -smt) handles relational ones.
func (a *Analyzer) proveLoopClause(clause ast.Expr, subst map[string]ast.Expr, scope *Scope) (bool, bool) {
	if clause == nil {
		return false, false
	}
	// Both tiers read facts (range facts, assert facts, flow hypotheses) from a.currentScope, so make
	// `scope` current for the duration of the proof. For establishment `scope` IS the current scope;
	// for preservation it is the scratch scope (a child of it) carrying the cond/invariant hypotheses.
	saved := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = saved }()
	if a.proveLoopClauseAffine(clause, subst, scope) {
		return true, false
	}
	// SMT translates the clause against the scope's assert/flow facts (and the enclosing function's
	// requires), substituting loop variables to their post-body terms. Only `unsat` of the negation
	// concludes — sound, and a decline simply leaves the runtime check.
	proven, _ := a.trySMTProveRequires(clause, subst)
	return proven, proven
}

// proveLoopPreservationAffine discharges the inductive step `cond ⊢ inv[body]` with the affine prover
// alone (no solver). It seeds a scratch child scope with the constant range facts implied by the
// TRUTHY loop guard about the loop variables — the body runs only when the guard holds — then bounds
// the substituted invariant over those ranges. This is the missing-hypothesis fix: without the guard,
// `n` is unbounded and `n + 1 <= MAX` cannot be concluded; with `n < MAX` seeded it follows.
//
// Soundness: the scratch scope carries ONLY guard-derived facts (a child of the live scope chain, so
// it adds facts but cannot mask outer ones); it never seeds the stale pre-loop entry value (`n == 0`),
// so a false invariant cannot be "proven" off it. boundAffine is conservative — any unknown bound
// declines — so this path only ADDS proofs, never admits an unsound one. The invariants themselves are
// NOT assumed here (unlike the SMT tier): the affine fragment cannot use a relational invariant as a
// hypothesis anyway, and the constant-bounded cases this path targets need only the guard.
func (a *Analyzer) proveLoopPreservationAffine(cond, inv ast.Expr, subst map[string]ast.Expr) bool {
	if cond == nil || inv == nil || a.currentScope == nil {
		return false
	}
	saved := a.currentScope
	scratch := NewScope(saved)
	// SOUNDNESS: close the world at the scratch scope so the range-fact lookup STOPS here and never
	// reaches the pre-loop entry value of a loop variable (e.g. the decl-seeded `n == 0`). The body
	// runs on an ARBITRARY iteration, so the only admissible facts about a loop variable are the ones
	// the truthy guard re-establishes — using the entry value would let a false invariant like
	// `n < MAX` "prove" preserved off `n == 0`. (Constants/written-consts resolve through the symbol
	// table and live-fact channels, which closedWorld does not gate, so `MAX` is still available.)
	scratch.closedWorld = true
	a.currentScope = scratch
	defer func() { a.currentScope = saved }()
	a.seedLoopGuardRangeFacts(scratch, cond)
	if scratch.rangeFacts == nil {
		return false // the guard gave us no usable constant bound on a loop variable
	}
	return a.proveLoopClauseAffine(inv, subst, scratch)
}

// seedLoopEntryRangeFacts seeds point range facts for loop variables pinned to a compile-time
// constant on entry, read from the live `name == const` SMT assert facts (which `n: usize = 0`
// records). Only the variables the body actually mutates (the keys of subst) are seeded — those are
// the loop counters whose entry value matters for establishment. Used ONLY for the establishment
// proof; the preservation proof deliberately excludes the entry value (the inductive-step isolation).
func (a *Analyzer) seedLoopEntryRangeFacts(scope *Scope, subst map[string]ast.Expr) {
	if scope == nil || len(subst) == 0 {
		return
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			be, ok := fact.Expr.(*ast.BinaryExpr)
			if !ok || be.Op != lexer.TOKEN_EQEQ {
				continue
			}
			name, c, ok := a.eqConstFactForLoopVar(be, subst)
			if !ok {
				continue
			}
			if scope.rangeFacts == nil {
				scope.rangeFacts = map[string]numRange{}
			}
			if _, seen := scope.rangeFacts[name]; seen {
				continue // a nearer (already-seeded) fact wins; never widen
			}
			scope.rangeFacts[name] = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
		}
		if sc.closedWorld {
			break
		}
	}
}

// eqConstFactForLoopVar interprets `loopvar == const` (either operand order) where loopvar is one of
// the body-mutated loop variables, returning the variable name and the constant value.
func (a *Analyzer) eqConstFactForLoopVar(be *ast.BinaryExpr, subst map[string]ast.Expr) (string, int64, bool) {
	if id, ok := be.Left.(*ast.Ident); ok && id != nil {
		if _, isLoopVar := subst[id.Name]; isLoopVar {
			if c, ok := a.constIntValue(be.Right); ok {
				return id.Name, c, true
			}
		}
	}
	if id, ok := be.Right.(*ast.Ident); ok && id != nil {
		if _, isLoopVar := subst[id.Name]; isLoopVar {
			if c, ok := a.constIntValue(be.Left); ok {
				return id.Name, c, true
			}
		}
	}
	return "", 0, false
}

// seedLoopGuardRangeFacts records the constant integer range facts implied by a truthy loop guard
// (a conjunction of `loopvar OP const` / `const OP loopvar` comparisons) onto scope. Unlike
// gatherNumericRangeRefinement it admits MUTABLE loop variables (via loopIntIdentName): within one
// inductive iteration the entry value is fixed, so the guard's bound on it is a valid hypothesis.
func (a *Analyzer) seedLoopGuardRangeFacts(scope *Scope, cond ast.Expr) {
	switch n := stripOptimizationParens(cond).(type) {
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.seedLoopGuardRangeFacts(scope, n.Left)
			a.seedLoopGuardRangeFacts(scope, n.Right)
			return
		}
		a.seedLoopGuardComparisonFact(scope, n)
	}
}

// seedLoopGuardComparisonFact records the range fact for a single `loopvar OP const` (or flipped)
// comparison drawn from the truthy loop guard.
func (a *Analyzer) seedLoopGuardComparisonFact(scope *Scope, n *ast.BinaryExpr) {
	op := n.Op
	name, ok := loopIntIdentName(a, scope, n.Left)
	var c int64
	var cok bool
	if ok {
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = loopIntIdentName(a, scope, n.Right); ok {
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	}
	if !ok || !cok {
		return
	}
	var fact numRange
	switch op {
	case lexer.TOKEN_GT: // v > c  ⇒ v >= c+1
		fact = numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ: // v >= c
		fact = numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT: // v < c  ⇒ v <= c-1
		fact = numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ: // v <= c
		fact = numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ: // v == c
		fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	default:
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

func (a *Analyzer) proveLoopClauseAffine(clause ast.Expr, subst map[string]ast.Expr, scope *Scope) bool {
	switch n := stripOptimizationParens(clause).(type) {
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.proveLoopClauseAffine(n.Left, subst, scope) && a.proveLoopClauseAffine(n.Right, subst, scope)
		}
		return a.proveLoopComparisonAffine(n, subst, scope)
	default:
		return false
	}
}

// proveLoopComparisonAffine proves a single `L OP R` invariant comparison by bounding the affine
// difference `L - R` over the loop variables' known constant ranges. Relational comparisons between
// two unbounded symbolic variables fall outside this fragment and decline (handled by SMT).
func (a *Analyzer) proveLoopComparisonAffine(n *ast.BinaryExpr, subst map[string]ast.Expr, scope *Scope) bool {
	switch n.Op {
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_EQEQ:
	default:
		return false
	}
	left, ok := a.loopClauseAffine(n.Left, subst, scope)
	if !ok {
		return false
	}
	right, ok := a.loopClauseAffine(n.Right, subst, scope)
	if !ok {
		return false
	}
	r := a.boundAffine(subtractAffine(left, right), scope)
	switch n.Op {
	case lexer.TOKEN_GT:
		return r.loKnown && r.lo > 0
	case lexer.TOKEN_GTEQ:
		return r.loKnown && r.lo >= 0
	case lexer.TOKEN_LT:
		return r.hiKnown && r.hi < 0
	case lexer.TOKEN_LTEQ:
		return r.hiKnown && r.hi <= 0
	case lexer.TOKEN_EQEQ:
		return r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0
	}
	return false
}

// loopClauseAffine builds the affine form of an invariant-side expression, substituting each loop
// variable by its post-body value (evaluated in OLD-variable terms — substitution is simultaneous,
// so a substituted value is not itself re-substituted). Unlike affineOf it admits MUTABLE integer
// loop variables as bounded terms: within one Hoare iteration the entry value is fixed, and
// boundAffine resolves it from the scope's range facts (declining when no bound is known).
func (a *Analyzer) loopClauseAffine(expr ast.Expr, subst map[string]ast.Expr, scope *Scope) (affineForm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.loopClauseAffine(n.Inner, subst, scope)
	case *ast.IntLit:
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			return a.loopClauseAffine(arg, nil, scope) // post-body value, in OLD-variable terms
		}
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		if name, ok := loopIntIdentName(a, scope, n); ok {
			return affineForm{c: 0, terms: map[string]int64{name: 1}}, true
		}
		return affineForm{}, false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return affineForm{}, false
		}
		inner, ok := a.loopClauseAffine(n.Operand, subst, scope)
		if !ok {
			return affineForm{}, false
		}
		return negateAffine(inner), true
	case *ast.BinaryExpr:
		l, ok := a.loopClauseAffine(n.Left, subst, scope)
		if !ok {
			return affineForm{}, false
		}
		r, ok := a.loopClauseAffine(n.Right, subst, scope)
		if !ok {
			return affineForm{}, false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return addAffine(l, r), true
		case lexer.TOKEN_MINUS:
			return subtractAffine(l, r), true
		case lexer.TOKEN_STAR:
			if len(l.terms) == 0 {
				return scaleAffineForm(r, l.c), true
			}
			if len(r.terms) == 0 {
				return scaleAffineForm(l, r.c), true
			}
			return affineForm{}, false // variable*variable: non-linear, decline (sound)
		default:
			return affineForm{}, false
		}
	default:
		return affineForm{}, false
	}
}

// loopIntIdentName is immutableIntIdentName without the immutability gate: a loop counter is mutable
// by construction, but within a single inductive step its entry value is fixed, so it is a valid
// bounded term. Soundness is preserved because the only facts seeded about it come from the loop
// condition and the (independently proven) invariants.
func loopIntIdentName(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil {
		return "", false
	}
	if !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return "", false
	}
	return ident.Name, true
}

// proveLoopPreservationSMT discharges the inductive step `cond ∧ inv₀ ∧ … ∧ invₙ ⊢ target[body]`
// with the solver, where `target[body]` is `target` with each loop variable substituted by its
// post-body value. Unlike trySMTProveRequires it does NOT fold in the ambient scope's assert/flow
// facts — only the enclosing function's preconditions (which constrain params, never the reassigned
// loop variables) plus the explicitly chosen hypotheses. The loop variables are therefore free SMT
// constants bound only by the condition and the invariants, which is exactly the inductive
// hypothesis. Returns true only on `unsat` of the negation — sound; off (declines) unless -smt.
//
// The invariants may be assumed as hypotheses because establishment (checked separately) covers the
// base case, so at the top of an arbitrary iteration every invariant holds.
func (a *Analyzer) proveLoopPreservationSMT(cond ast.Expr, invs []*ast.ContractStmt, target ast.Expr, subst map[string]ast.Expr, arrayStores []loopArrayStore, outerInvs []*ast.ContractStmt) (bool, string) {
	solver := a.openSMT()
	if solver == nil || target == nil {
		return false, ""
	}
	savedRangeFacts := cloneScopeRangeFacts(a.currentScope)
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		sc.rangeFacts = nil
	}
	defer restoreScopeRangeFacts(savedRangeFacts)
	tr := a.newSMTTranslator(nil)
	// The obligation: the invariant after the body's substitution. smtEnvForSubst maps each loop
	// variable to its post-body term (declaring the free variables of those terms).
	env, ok := a.smtEnvForSubst(tr, subst)
	if !ok {
		return false, ""
	}
	// Array-element stores extend the environment with the post-body array value `(store arr idx val)`.
	// The index/value are translated with the FREE (pre-body) environment — consistent with the
	// hypotheses below and with capture's guarantee that they reference only pre-body values — so the
	// invariant's `arr[k]` becomes `(select (store arr idx val) k)` after substitution, and z3's array
	// theory proves the inductive step (the just-written cell plus the IH on the rest).
	//
	// MULTI-STORE: when the same array has multiple element stores (captured in body order), we compose
	// them as nested `(store BASE idx val)` with the LATER store outermost. The base for the first store
	// is the free pre-body array constant; the base for each subsequent store is the already-composed
	// term. This faithfully models `arr[i] <- a; arr[i+1] <- b` as `(store (store arr i a) (i+1) b)`,
	// which z3's array theory reads element-wise correctly for the inductive step.
	for _, st := range arrayStores {
		// Base: if a prior store to this array already built a composed term, use it; otherwise start
		// from the free pre-body array constant.
		var baseTerm string
		if prior, exists := env[st.arrayName]; exists {
			baseTerm = prior
		} else {
			var ok bool
			baseTerm, ok = tr.arrayTermEnv(st.array, nil)
			if !ok {
				return false, ""
			}
		}
		idxTerm, ok := tr.termEnv(st.index, nil)
		if !ok {
			return false, ""
		}
		valTerm, ok := tr.termEnv(st.value, nil)
		if !ok {
			return false, ""
		}
		env[st.arrayName] = "(store " + baseTerm + " " + idxTerm + " " + valTerm + ")"
	}
	// Lower the obligation into the central VC IR (shared folder + smart constructors) so a
	// preservation/termination goal that constant-folds to `true` is discharged with NO solver call and
	// structural comparisons/arithmetic are normalized. lowerVCFormula has the SAME symbol-declaration
	// side effects as boolTerm and emits opaque atoms verbatim, so the SMT query is byte-identical for
	// any goal the folder does not touch (behavior-preserving). When the target is outside the IR
	// fragment (quantifiers, array selects, …) lowering declines and we fall back to the opaque boolTerm
	// translation — the identical pre-IR path.
	goal, lowered := tr.lowerVCFormula(target, env)
	var obligation string
	if lowered {
		obligation = emitVCFormula(goal)
	} else {
		var ok bool
		obligation, ok = tr.boolTerm(target, env)
		if !ok {
			return false, ""
		}
	}
	// Hypotheses are translated with an identity environment, so a loop variable `i` in a hypothesis
	// and the `i` inside a substituted term (`i + 1`) resolve to the SAME free SMT constant.
	identEnv := map[string]string{}
	var hypSB strings.Builder
	addHyp := func(h ast.Expr) bool {
		if h == nil {
			return false
		}
		term, ok := tr.boolTerm(h, identEnv)
		if !ok {
			return false
		}
		hypSB.WriteString("(assert " + term + ")\n")
		return true
	}
	if !addHyp(cond) {
		return false, ""
	}
	for _, inv := range invs {
		if !addHyp(inv.Cond) {
			return false, ""
		}
	}
	// Outer-loop proven invariants that are disjoint from this loop's mutations are additional sound
	// hypotheses: they were proven inductive by the outer loop, so they hold at the top of every outer
	// iteration, which includes the top of every inner iteration. A translation failure for an outer
	// invariant is non-fatal — skip that fact and continue (decline is always sound here).
	for _, outerInv := range outerInvs {
		addHyp(outerInv.Cond) // ignore bool return — optional hypothesis, decline is safe
	}
	// Hypotheses are ONLY the enclosing preconditions plus the chosen cond/invariant clauses (hypSB) —
	// deliberately NOT the ambient assert/flow facts the standard set carries, so the loop variables stay
	// free constants (the inductive hypothesis). Discharge through the shared check primitive; on a SAT
	// result the counterexample is a loop state satisfying cond + the invariants but VIOLATING the
	// invariant after one body step — a concrete witness to non-preservation.
	reqHyps := a.smtRequiresHypotheses(tr)
	hyps := reqHyps + hypSB.String()
	// When the obligation lowered into the IR, discharge through the IR-aware helper so a goal that the
	// smart constructors folded to `true` concludes with no solver call; the loop's OWN hypothesis set is
	// passed through unchanged (loop variables free — the inductive hypothesis). Otherwise the opaque
	// obligation goes straight to the shared check primitive (the pre-IR path).
	if lowered {
		return a.smtDischargeFormulaWithHyps(tr, goal, hyps)
	}
	return a.smtCheckQuery(tr, hyps, obligation)
}

// loopArrayStore is a captured array-element assignment `array[index] <- value` in a loop body. The
// preservation prover models it as the SMT array-store `(store array index value)`, which is what lets
// a QUANTIFIED invariant over the array's contents (`forall k: 0<=k<i implies arr[k] == 0`) be proven
// inductive across the mutation that fills the array.
type loopArrayStore struct {
	arrayName string
	array     ast.Expr
	index     ast.Expr
	value     ast.Expr
}

// captureLoopBodyEffect models a straight-line loop body as a simultaneous substitution: each assigned
// scalar loop variable maps to its new value, and each array-element store `arr[i] <- v` is recorded
// as a loopArrayStore. It returns ok=false for any body it cannot fully account for — a side-effecting
// or non-arithmetic right-hand side, a doubly-written target, or any non-assignment statement. This
// conservatism is what makes the substitution sound: a captured body provably touches state only
// through these pure assignments and contains no control flow.
func (a *Analyzer) captureLoopBodyEffect(body []ast.Stmt) (map[string]ast.Expr, []loopArrayStore, bool) {
	return a.captureLoopBodyEffectMode(body, false)
}

// captureLoopBodyEffectForTermination is the relaxed capture used by the loop-termination prover. It
// permits array-element STORES whose index/value contain side-effect-free reads (array-element and
// field reads), because such a store mutates no scalar and is therefore irrelevant to the
// scalar-measure decrease proof. It returns NO loopArrayStore records (termination never models
// array contents — it proves only the scalar measure), so this relaxation cannot reach, and cannot
// weaken, the invariant-preservation array-store SMT model, which keeps using the strict
// captureLoopBodyEffect.
func (a *Analyzer) captureLoopBodyEffectForTermination(body []ast.Stmt) (map[string]ast.Expr, bool) {
	subst, _, ok := a.captureLoopBodyEffectMode(body, true)
	return subst, ok
}

func (a *Analyzer) captureLoopBodyEffectMode(body []ast.Stmt, forTermination bool) (map[string]ast.Expr, []loopArrayStore, bool) {
	// On the termination path the array-store index/value may also contain side-effect-free reads;
	// on the invariant path they must stay strictly pure-arith so the `(store arr idx val)` SMT model
	// reads only modeled terms.
	storeOperandPure := exprIsPureArith
	if forTermination {
		storeOperandPure = exprIsPureArithOrRead
	}
	subst := map[string]ast.Expr{}
	assigned := map[string]bool{}
	var order []string
	var stores []loopArrayStore
	storedArrays := map[string]bool{}
	for _, s := range body {
		switch n := s.(type) {
		case *ast.ContractStmt:
			// Leading `invariant` / `decreases` clauses are specifications, not body effects — skip them.
			if n.Kind != ast.ContractInvariant && n.Kind != ast.ContractDecreases {
				return nil, nil, false
			}
		case *ast.AssignStmt:
			switch target := n.Target.(type) {
			case *ast.Ident:
				if target == nil {
					return nil, nil, false
				}
				if assigned[target.Name] || storedArrays[target.Name] {
					return nil, nil, false // multiple writes: simultaneous substitution would be wrong
				}
				if !exprIsPureArith(n.Value) {
					return nil, nil, false // calls / address-of / non-arithmetic: possible side effects
				}
				subst[target.Name] = n.Value
				assigned[target.Name] = true
				order = append(order, target.Name)
			case *ast.IndexExpr:
				// Array-element store `arr[idx] <- val`, modeled as `(store arr idx val)`. Sound only when
				// arr is a bare array variable; idx and val are pure arithmetic; and idx/val reference only
				// PRE-body values (no variable assigned EARLIER in this body — else the store would observe
				// a post-assignment value, breaking the simultaneous-substitution model). The array must
				// never be whole-reassigned in the same body.
				//
				// MULTI-STORE: two or more stores to the SAME array are supported on the strict invariant
				// path. The SMT model composes them as nested `(store (store arr idx1 val1) idx2 val2)` in
				// body order (later store outermost). Sound because exprIsPureArith forbids array reads, so
				// each store's idx/val can only reference PRE-body scalar values; the ordering hazard
				// (store2 observing store1's result) cannot occur. The usual `assigned[r]` check blocks
				// idx/val from reading a scalar assigned earlier in the same body.
				//
				// On the TERMINATION path only, the array base may also be a stable struct-FIELD path
				// rooted at a single receiver identifier through `.field` hops (`self.xs`, `self.a.b`).
				// Such an element store changes the field array's CONTENTS but no scalar counter and not
				// its `.count`, so it is irrelevant to a scalar / field-count measure decrease and is
				// discarded exactly like the bare-array case. The "stored-once / not-also-reassigned"
				// tracking is keyed by the NORMALIZED field-path string so a store plus a whole-field
				// reassignment is still rejected. The strict invariant path keeps requiring a bare-Ident
				// base (no field key produced there).
				arrKey, arrOK := loopStoreTargetBaseKey(target, forTermination)
				if !arrOK {
					return nil, nil, false
				}
				// Reject whole-reassignment of an array that has been element-stored (and vice versa via
				// the `storedArrays[arrKey]` check in the scalar-Ident branch above). Multiple element
				// stores to the same array are allowed on the strict invariant path (composed as nested
				// stores in SMT), but still rejected on the termination path (field arrays; double stores
				// don't affect scalar measures and the termination prover already declines on them).
				if assigned[arrKey] || (forTermination && storedArrays[arrKey]) {
					return nil, nil, false
				}
				if !storeOperandPure(target.Index) || !storeOperandPure(n.Value) {
					return nil, nil, false
				}
				refs := map[string]bool{}
				collectArithIdents(target.Index, refs)
				collectArithIdents(n.Value, refs)
				for r := range refs {
					if assigned[r] {
						return nil, nil, false
					}
				}
				// The termination prover proves only the scalar measure and discards stores, so recording
				// them is unnecessary there; on the invariant path each store is appended in body order so
				// the SMT tier can fold them into a left-nested (store ...) chain, outer = last write.
				if !forTermination {
					stores = append(stores, loopArrayStore{arrayName: arrKey, array: target.Object, index: target.Index, value: n.Value})
				}
				storedArrays[arrKey] = true
			default:
				return nil, nil, false
			}
		default:
			return nil, nil, false // any other statement form
		}
	}
	if len(order) == 0 && len(stores) == 0 && len(storedArrays) == 0 {
		return nil, nil, false
	}
	// Disjoint-RHS rule: each scalar right-hand side may reference an assigned variable only when it IS
	// the assigned variable (`i <- i + 1`). Otherwise simultaneous substitution would diverge from the
	// body's sequential semantics (`i <- i + 1; j <- i`).
	for _, name := range order {
		refs := map[string]bool{}
		collectArithIdents(subst[name], refs)
		for r := range refs {
			if r != name && assigned[r] {
				return nil, nil, false
			}
		}
	}
	return subst, stores, true
}

// loopStoreTargetBaseKey returns the normalized base key of an array-element store target
// `BASE[idx]`, plus whether the base is admissible for the current capture mode.
//
//   - The STRICT (invariant) path requires BASE to be a bare array variable — exactly the
//     prior behaviour; a field path is rejected so the `(store arr idx val)` SMT model only ever
//     keys on a plain Ident array symbol.
//   - The TERMINATION path additionally admits a stable struct-FIELD path lvalue rooted at a single
//     `*ast.Ident` receiver through `*ast.FieldExpr` hops only — `self.xs`, `self.a.b`. No calls,
//     no index/subscript, no deref, no safe-navigation in the path; anything else declines.
//
// The returned key is a canonical string for the base path (`self`, `self__field__xs`), so the
// caller's stored-once / not-also-reassigned tracking treats two syntactically-equal paths as the
// same array (conservative aliasing: identical paths alias; distinct paths we cannot prove disjoint
// are kept apart only by syntactic string, and a store is permitted at most ONCE per key — so the
// simultaneous-substitution invariant cannot be broken even if two distinct strings happened to
// alias, because each store is independently discarded and changes no scalar).
//
// The IndexExpr itself must be an ordinary single-subscript array index: any analyzer-rewritten or
// sugared form (two-operand overlay accessor, `else` fallback, value specialization, overlay call)
// is rejected — those do not lower to a plain element store.
func loopStoreTargetBaseKey(target *ast.IndexExpr, forTermination bool) (string, bool) {
	if target == nil {
		return "", false
	}
	if target.Index2 != nil || target.Fallback != nil || target.LegacyElseFallback ||
		target.AsSpecialize != nil || target.AsOverlayCall != nil {
		return "", false
	}
	if arrIdent, ok := target.Object.(*ast.Ident); ok && arrIdent != nil {
		return arrIdent.Name, true
	}
	// Struct-field array paths (`s.data[i] <- v`) are admitted on BOTH the invariant path and the
	// termination path. The normalized path key (e.g. `s__field__data`) matches the projection name
	// that smtProjectionName / arrayTermEnv uses, so proveLoopPreservationSMT injects
	//   env["s__field__data"] = "(store v_s__field__data i v)"
	// and arrayTermEnv's FieldExpr env-lookup picks it up when translating `s.data[k]` in the
	// quantified invariant. Soundness constraints remain identical: stored-once per path,
	// never whole-reassigned, pure-arith index/value.
	key, ok := normalizeFieldPathLValue(target.Object)
	return key, ok
}

// normalizeFieldPathLValue returns a canonical key string for a stable field-path lvalue rooted at a
// single `*ast.Ident` receiver through `*ast.FieldExpr` hops ONLY, and whether `expr` is exactly such
// a path. It walks `obj.a.b…` down to the root identifier; ANYTHING ELSE in the chain — a call, an
// index, an address-of, a cast, a parenthesized non-field, or a safe (`?.`) navigation that could be
// null — declines (returns ok=false). The key is the dotted path joined by a separator that cannot
// appear in an identifier, so two equal paths produce one key and unequal paths produce distinct keys.
func normalizeFieldPathLValue(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		// A `?.` safe-navigation field is NOT a stable lvalue store base (the object may be null), so
		// reject it; only an unconditional `.field` projection of a stable path is admissible.
		if n == nil || n.Safe {
			return "", false
		}
		base, ok := normalizeFieldPathLValue(n.Object)
		if !ok {
			return "", false
		}
		return base + "__field__" + n.Field, true
	default:
		return "", false
	}
}

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
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
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
