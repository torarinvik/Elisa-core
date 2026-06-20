package semantic

import (
	"elisacore/src/ast"
)

// Ghost code & lemma functions.
//
// A `lemma` is a verification-only function: it is never emitted to code. It exists to give the
// prover a fact it cannot derive automatically — a step-by-step (possibly inductive) argument the
// programmer writes once and reuses. Soundness rests on two halves that mirror an ordinary
// interprocedural contract, but with NO runtime fallback (the lemma is erased):
//
//  1. AT THE DEFINITION (verifyLemmaEnsures): every `ensure` is PROVEN from the lemma's `requires`
//     plus the facts its body establishes (asserts, nested lemma calls, branch refinements). Because
//     a lemma carries no runtime check, an unproven ensure is a HARD ERROR unconditionally — there is
//     nothing to fall back to, so "checked at runtime" is not an option the way it is for `def`.
//
//  2. AT EACH CALL (assumeLemmaEnsures): the lemma's `requires` are discharged against the caller's
//     facts (the ordinary dischargeCallRequires path, shared with `def`), and then its `ensure`
//     clauses — with parameters substituted by the actual arguments — are ASSUMED as facts. This is
//     sound precisely because (1) proved the ensures follow from the requires, and the requires were
//     just shown to hold here. The substituted ensure is therefore a true fact at this program point.
//
// A lemma call is only meaningful as a statement (it returns nothing and only injects facts), so it
// is restricted to statement position and erased from codegen via Result.LemmaCalls.

// verifyLemmaEnsures proves every `ensure` of a lemma from its `requires` + body facts, at the end of
// the lemma body (where currentScope holds the accumulated body facts). Called from the function
// analyzer for IsLemma decls. An unprovable ensure is a hard error: the lemma is erased, so there is
// no runtime check to fall back to — it must hold statically or the program is rejected.
func (a *Analyzer) verifyLemmaEnsures(fn *ast.FuncDecl) {
	if a == nil || fn == nil || !fn.IsLemma {
		return
	}
	for _, clause := range fn.EnsureValues {
		if clause == nil {
			continue
		}
		// Cheap tier first: the bounded-linear prover discharges simple clauses without a solver (so
		// lemmas verify even under -nosmt).
		if a.proveRequiresClause(clause, nil) == requiresProven {
			a.recordProof(clause.Pos(), "lemma "+fn.Name, "ensure", ProofProvenLinear)
			continue
		}
		// trySMTProveRequires assumes the function's own `requires` as hypotheses and adds the current
		// scope's assert/flow facts, then proves the clause (only `unsat` of its negation concludes).
		proven, counterexample := a.trySMTProveRequires(clause, nil)
		if proven {
			a.recordProof(clause.Pos(), "lemma "+fn.Name, "ensure", ProofProvenSMT)
			continue
		}
		a.errorf(clause.Pos(), "lemma %q does not prove its `ensure`: a lemma is erased from code, so every postcondition must follow statically from its `requires` and body (add intermediate `assert`s, call a helper lemma, or strengthen `requires`)%s", fn.Name, a.counterexampleSuffix(counterexample))
	}
}

// assumeLemmaEnsures, at a statement-position call to a lemma, injects the lemma's (already-proven)
// `ensure` clauses as facts at the call site, with the lemma's parameters substituted by the actual
// arguments. The lemma's `requires` are discharged separately by the ordinary dischargeCallRequires
// path. Marks the call for codegen erasure. Returns whether the call targeted a lemma.
func (a *Analyzer) assumeLemmaEnsures(call *ast.CallExpr, args []ast.Expr) bool {
	if a == nil || call == nil {
		return false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil || !decl.IsLemma {
		return false
	}
	if a.lemmaCalls == nil {
		a.lemmaCalls = map[*ast.CallExpr]bool{}
	}
	a.lemmaCalls[call] = true

	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}

	// Circular-reasoning guard vs. SOUND INDUCTION. A self-recursive call to the lemma currently under
	// analysis (`a.lemmasInAnalysis[decl]`) must NOT, in general, assume the lemma's own conclusion —
	// that is assuming what we are trying to prove, and would let a lemma "prove" anything. The ONE
	// sound exception is the inductive hypothesis: if the lemma has a VERIFIED `decreases` measure and
	// the argument at THIS call is provably strictly smaller than the lemma's own parameters under that
	// measure, then by well-founded induction the ensure already holds for the smaller argument, so we
	// may assume it. Soundness rests entirely on the termination gate: a strictly-decreasing,
	// bounded-below measure makes the recursion well-founded, and well-founded induction lets us assume
	// the IH for every smaller input. Without a verified decreases we inject NOTHING (status quo guard).
	if a.lemmasInAnalysis[decl] {
		if a.inductiveHypothesisAvailable(decl, call) {
			a.injectLemmaEnsureFacts(decl, call, subst)
		}
		return true
	}

	// SOUNDNESS: the lemma's ensure may be assumed here ONLY if the lemma's `requires` provably hold
	// at this call site. A lemma is erased, so an unmet `requires` is never checked at runtime either —
	// assuming the ensure without it would be unsound. Each precondition must be statically proven; an
	// unproven (or refuted) one is a hard error, and we inject NOTHING.
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		if a.proveRequiresClause(req, subst) == requiresProven {
			a.recordProof(call.Pos(), "precondition of lemma "+decl.Name, "requires", ProofProvenLinear)
			continue
		}
		if proven, _ := a.trySMTProveRequires(req, subst); proven {
			a.recordProof(call.Pos(), "precondition of lemma "+decl.Name, "requires", ProofProvenSMT)
			continue
		}
		a.errorf(call.Pos(), "precondition of lemma %q is not satisfied at this call: a lemma is erased from code, so its `requires` must be statically proven here (it has no runtime check to fall back to)", decl.Name)
		return true
	}

	a.injectLemmaEnsureFacts(decl, call, subst)
	return true
}

// injectLemmaEnsureFacts records each of the lemma's `ensure` clauses, with the lemma's parameters
// substituted by the call's actual arguments, as a flow fact at the call site. Used both by an
// ordinary (already-verified) lemma call and by a SOUND inductive-hypothesis self-call (see
// inductiveHypothesisAvailable). The substitution is complete-or-decline per clause (see
// substituteLemmaEnsure): a clause that cannot be faithfully rewritten into the caller's terms is
// dropped, which only forgoes a fact and never records one whose meaning shifts.
func (a *Analyzer) injectLemmaEnsureFacts(decl *ast.FuncDecl, call *ast.CallExpr, subst map[string]ast.Expr) {
	for _, clause := range decl.EnsureValues {
		if clause == nil {
			continue
		}
		rewritten, ok := substituteLemmaEnsure(clause, subst)
		if !ok {
			continue
		}
		a.recordSMTAssertFact(rewritten)
	}
}

// inductiveHypothesisAvailable reports whether a self-recursive call to the lemma currently under
// analysis may soundly assume the lemma's `ensure` (the inductive hypothesis). Two airtight gates:
//
//  1. TERMINATION: the lemma must carry a `decreases` measure that is verified to strictly decrease
//     (and stay bounded below) at EVERY direct self-recursive call. This makes the recursion
//     well-founded — there is no infinite descent — which is exactly the side condition that licenses
//     well-founded induction. Without it, the "lemma" could be vacuously self-justifying nonsense
//     (`circular(x); ensure x != x`), so we refuse to inject anything.
//
//  2. THIS CALL DECREASES: the argument at THIS particular call site must itself be provably strictly
//     smaller than the lemma's parameters under the measure. (Gate 1 already proves every self-call
//     decreases, so this is implied; we re-check defensively so the IH is tied to the concrete call.)
//
// Both checks reuse the exact termination machinery (proveMeasureDecreases / substForSelfCall) run
// against the live body scope, so the IH can never be stronger than what termination established.
func (a *Analyzer) inductiveHypothesisAvailable(decl *ast.FuncDecl, call *ast.CallExpr) bool {
	if a == nil || decl == nil || call == nil || len(decl.Decreases) == 0 {
		return false
	}
	if decl != a.currentFuncDecl {
		return false // only the lemma actively under analysis induces over itself
	}
	if !a.lemmaTerminationVerified(decl) {
		return false
	}
	subst := a.substForSelfCall(decl, call)
	return a.proveMeasureDecreases(decl.Decreases, subst)
}

// lemmaTerminationVerified eagerly proves (and caches) that the lemma's `decreases` measure strictly
// decreases at every direct self-recursive call, mirroring checkTermination but WITHOUT emitting
// diagnostics — it is a precondition query for the inductive hypothesis, run mid-body. The cache keeps
// it from re-proving per self-call. checkTermination still runs at end-of-body and is the authority
// that REPORTS an unprovable measure; this read-only mirror only ever gates the IH (a false result
// just forgoes the IH, never fabricates a proof).
func (a *Analyzer) lemmaTerminationVerified(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || len(decl.Decreases) == 0 {
		return false
	}
	if a.lemmaTerminationCache == nil {
		a.lemmaTerminationCache = map[*ast.FuncDecl]bool{}
	}
	if v, ok := a.lemmaTerminationCache[decl]; ok {
		return v
	}
	calls := a.collectSelfRecursiveCalls(decl)
	if len(calls) == 0 {
		// No self-call ⇒ nothing to induct over; the IH path is irrelevant.
		a.lemmaTerminationCache[decl] = false
		return false
	}
	ok := a.measureVerifiedForCalls(decl, calls)
	a.lemmaTerminationCache[decl] = ok
	return ok
}

// substituteLemmaEnsure clones a lemma `ensure` expression, replacing each parameter identifier with
// the caller's argument expression. It is COMPLETE-OR-DECLINE: every parameter identifier must map to
// an argument, and any node it does not know how to rewrite makes it return ok=false (so the caller
// drops the fact). A bare identifier that is not a parameter is kept as-is — it refers to a global or
// constant that means the same thing in the caller's scope. This conservatism is what keeps an
// assumed fact sound: a partially-substituted clause referencing the lemma's own parameter name would
// be a free, unconstrained variable in the caller and could license false conclusions.
func substituteLemmaEnsure(expr ast.Expr, subst map[string]ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			return arg, true
		}
		// Not a parameter: a global/const reference — same meaning in the caller. Keep it.
		return n, true
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return n, true
	case *ast.ParenExpr:
		inner, ok := substituteLemmaEnsure(n.Inner, subst)
		if !ok {
			return nil, false
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}, true
	case *ast.UnaryExpr:
		operand, ok := substituteLemmaEnsure(n.Operand, subst)
		if !ok {
			return nil, false
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}, true
	case *ast.BinaryExpr:
		left, ok := substituteLemmaEnsure(n.Left, subst)
		if !ok {
			return nil, false
		}
		right, ok := substituteLemmaEnsure(n.Right, subst)
		if !ok {
			return nil, false
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}, true
	case *ast.FieldExpr:
		object, ok := substituteLemmaEnsure(n.Object, subst)
		if !ok {
			return nil, false
		}
		return &ast.FieldExpr{Position: n.Position, Object: object, Field: n.Field}, true
	case *ast.IndexExpr:
		object, ok := substituteLemmaEnsure(n.Object, subst)
		if !ok {
			return nil, false
		}
		index, ok := substituteLemmaEnsure(n.Index, subst)
		if !ok {
			return nil, false
		}
		return &ast.IndexExpr{Position: n.Position, Object: object, Index: index}, true
	case *ast.CallExpr:
		fn, ok := substituteLemmaEnsure(n.Func, subst)
		if !ok {
			return nil, false
		}
		newArgs := make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			ra, ok := substituteLemmaEnsure(arg, subst)
			if !ok {
				return nil, false
			}
			newArgs[i] = ra
		}
		return &ast.CallExpr{Position: n.Position, Func: fn, Args: newArgs}, true
	default:
		return nil, false
	}
}

// lemmaCallInStatementPosition reports whether a bare expression statement is a lemma invocation, used
// to allow lemma calls only as statements (they return nothing and exist solely to inject facts).
func (a *Analyzer) callTargetsLemma(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	return ok && decl != nil && decl.IsLemma
}
