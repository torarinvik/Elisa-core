package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
	// Establishment (no substitution) reads the live pre-loop facts, so the requires ladder the
	// in-place check uses applies too: index-bound facts (`slot = h & mask` with `mask = cap - 1`
	// bounds slot below cap) and the other non-SMT tiers.
	if subst == nil && a.proveRequiresClause(clause, nil) == requiresProven {
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
