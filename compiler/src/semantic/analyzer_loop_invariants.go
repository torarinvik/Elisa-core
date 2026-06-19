package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/smt"
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
	subst, captured := a.captureLoopBodyEffect(stmt.Body)

	allProven := true
	for _, inv := range invs {
		// Establishment: prove from the pre-loop facts on the current scope (intact: the body has not
		// been analyzed yet).
		established, viaSMT := a.proveLoopClause(inv.Cond, nil, a.currentScope)
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
		// Preservation: prove `cond ∧ (every invariant) ⊢ inv[body]`. This is discharged by a
		// dedicated SMT implication where the loop variables are FREE — constrained only by the loop
		// condition and the invariants, never by their (now stale) pre-loop values. That isolation is
		// what makes it sound: a child scope cannot mask the outer loop-variable facts (range-fact
		// lookup intersects the whole chain), so reusing the ambient fact set would let a false
		// invariant like `i < 5` "prove" preserved off the entry value `i == 0`.
		if !a.proveLoopPreservationSMT(stmt.Cond, invs, inv.Cond, subst) {
			a.recordProof(inv.Pos(), "loop invariant", "preserve", ProofRuntime)
			a.proofLint(inv.Pos(), "loop invariant is established on entry but could not be proven preserved by the loop body; it is only checked at runtime")
			allProven = false
			continue
		}
		a.recordProof(inv.Pos(), "loop invariant", "preserve", ProofProvenSMT)
	}

	// Sound exit facts require EVERY invariant proven inductive on a body we fully captured. A
	// captured body is straight-line (only assignments and invariant decls), so it has no `break`
	// that could leave the loop with the condition still true — `¬cond` therefore holds at exit.
	return invs, allProven && captured
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
}

func provenTier(viaSMT bool) ProofOutcome {
	if viaSMT {
		return ProofProvenSMT
	}
	return ProofProvenLinear
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
func (a *Analyzer) proveLoopPreservationSMT(cond ast.Expr, invs []*ast.ContractStmt, target ast.Expr, subst map[string]ast.Expr) bool {
	solver := a.openSMT()
	if solver == nil || target == nil {
		return false
	}
	tr := a.newSMTTranslator(nil)
	// The obligation: the invariant after the body's substitution. smtEnvForSubst maps each loop
	// variable to its post-body term (declaring the free variables of those terms).
	env, ok := a.smtEnvForSubst(tr, subst)
	if !ok {
		return false
	}
	obligation, ok := tr.boolTerm(target, env)
	if !ok {
		return false
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
		return false
	}
	for _, inv := range invs {
		if !addHyp(inv.Cond) {
			return false
		}
	}
	// Enclosing preconditions are safe to assume and may bound a param the invariant mentions. Gathered
	// before factPreamble so its declarations are included.
	reqHyps := a.smtRequiresHypotheses(tr)
	query := tr.factPreamble() + reqHyps + hypSB.String() + "(assert (not " + obligation + "))\n"
	a.smtStats.Attempts++
	res, _, _ := solver.CheckValues(query, tr.declaredSMTVars())
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		return true
	}
	a.smtStats.Declined++
	return false
}

// captureLoopBodyEffect models a straight-line loop body as a simultaneous substitution from each
// assigned loop variable to its new value. It returns ok=false for any body it cannot fully account
// for — a non-identifier assignment target, a variable written more than once, a side-effecting or
// non-arithmetic right-hand side, or any non-assignment statement. This conservatism is what makes
// the substitution sound: a captured body provably touches the loop variables only through these
// pure arithmetic assignments and contains no control flow.
func (a *Analyzer) captureLoopBodyEffect(body []ast.Stmt) (map[string]ast.Expr, bool) {
	subst := map[string]ast.Expr{}
	assigned := map[string]bool{}
	var order []string
	for _, s := range body {
		switch n := s.(type) {
		case *ast.ContractStmt:
			if n.Kind != ast.ContractInvariant {
				return nil, false
			}
		case *ast.AssignStmt:
			id, ok := n.Target.(*ast.Ident)
			if !ok || id == nil {
				return nil, false // non-ident target (e.g. `arr[i] <- …`)
			}
			if assigned[id.Name] {
				return nil, false // multiple writes: simultaneous substitution would be wrong
			}
			if !exprIsPureArith(n.Value) {
				return nil, false // calls / address-of / non-arithmetic: possible side effects
			}
			subst[id.Name] = n.Value
			assigned[id.Name] = true
			order = append(order, id.Name)
		default:
			return nil, false // any other statement form
		}
	}
	if len(order) == 0 {
		return nil, false
	}
	// Disjoint-RHS rule: each right-hand side may reference an assigned variable only when it IS the
	// assigned variable (`i <- i + 1`). Otherwise simultaneous substitution would diverge from the
	// body's sequential semantics (`i <- i + 1; j <- i`).
	for _, name := range order {
		refs := map[string]bool{}
		collectArithIdents(subst[name], refs)
		for r := range refs {
			if r != name && assigned[r] {
				return nil, false
			}
		}
	}
	return subst, true
}

// exprIsPureArith reports whether expr is a side-effect-free integer arithmetic expression over
// literals and variables — the only right-hand sides captureLoopBodyEffect admits. Calls,
// address-of, container reads, and casts are rejected (default false) so capture never silently
// admits a side effect.
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
	}
}
