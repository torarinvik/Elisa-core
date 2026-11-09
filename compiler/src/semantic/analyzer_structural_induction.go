package semantic

// Structural induction over recursive datatypes (docs/101 follow-up to bounded numeric unfolding).
//
// The numeric defining-equation machinery (analyzer_recursive_proof_certs.go +
// emitDefiningEquation) proves properties of integer-recursive pure functions by canonicalizing each
// recursive call to a shared SMT symbol and asserting `f(args) == body`, with the body's own recursive
// sub-call standing in as the inductive hypothesis. That machinery is INTEGER-ONLY: a bool-returning
// predicate over a recursive enum (`ghost def all_nonneg(t: Tree) -> bool`) returns bool and is defined
// by a `match`, neither of which the numeric path models — so `ensure all_nonneg(result)` always
// declined.
//
// This file adds the STRUCTURAL-INDUCTION analogue for such predicates:
//
//   (1) CANONICALIZATION. A call `p(arg)` to a bool predicate lowers to ONE opaque SMT Bool symbol
//       keyed by callee + the SYNTACTIC projection of its arguments. Two occurrences of `p(t)` — one in
//       a `requires` hypothesis, one in an `ensure` obligation — therefore share a symbol, so an
//       identity passthrough (`return t` with `requires p(t)` / `ensure p(result)`) discharges trivially.
//       This alone needs NO unfolding and is always sound (a deterministic predicate maps equal
//       syntactic inputs to equal truth values).
//
//   (2) STRUCTURAL DEFINING EQUATION. When the predicate is STRUCTURALLY WELL-FOUNDED — it recurses
//       only on match-bound children of a recursive-plain enum and carries a VERIFIED structural
//       `decreases` measure (reusing structuralTerminationCertificate) — we additionally assert the
//       match-unfolding `p(t) == arm-disjunction`, where each recursive sub-call `p(child)` is itself
//       canonicalized to its own opaque Bool symbol. That sub-symbol IS the inductive hypothesis: the
//       prover may assume the predicate on strictly-smaller substructures (justified by structural
//       decrease) while proving it for the whole. The unfolding is bounded by callEqMaxUnroll, so the
//       asserted axiom set is always finite and the prover never hangs.
//
// SOUNDNESS. Only `unsat` ever concludes a proof. The structural defining equation is emitted ONLY for
// a structurally well-founded predicate; a non-well-founded recursive predicate (recursion not on a
// strictly-smaller child) gets the opaque symbol from (1) but NO equation — exactly as a
// non-terminating numeric recursion is denied its (possibly inconsistent) equation. Bounded unrolling
// guarantees termination of the emission itself. We never assume an unproven inductive step: the IH is
// only the predicate applied to a child the termination certificate proved is structurally smaller.

import (
	"elisacore/src/ast"
	"strings"
)

// boolPredicateCallSym returns the canonical opaque SMT Bool symbol for a call to a bool-returning
// predicate, declaring it as a free Bool const, and (when the predicate is structurally well-founded)
// emitting its bounded structural defining equation. Returns ok=false when the expression is not a
// direct call to a resolvable bool predicate, or its arguments cannot be given a stable canonical key —
// in which case boolTerm falls back to its other cases (declining soundly).
func (tr *smtTranslator) boolPredicateCallSym(call *ast.CallExpr, env map[string]string) (string, bool) {
	if tr == nil || call == nil || tr.a == nil {
		return "", false
	}
	decl, ok := tr.a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return "", false
	}
	if !tr.a.boolPredicateEligible(decl) {
		return "", false
	}
	args := proofCallArgs(call)
	key, ok := tr.boolPredicateCanonKey(decl, args, env)
	if !ok {
		return "", false
	}
	if sym, seen := tr.callCanon[key]; seen {
		return sym, true
	}
	sym := tr.freshBoolPredAux()
	tr.callCanon[key] = sym
	tr.assertGhostCallCongruenceSort(decl, args, env, sym, "Bool")
	// (2) bounded structural defining equation — only for a well-founded recursive (or trivially total
	// non-recursive) predicate. A NO-OP otherwise, leaving `sym` purely opaque (still sound).
	tr.emitStructuralPredicateEquation(call, decl, sym, args, env, key)
	tr.emitBoolPredicateDefiningEquation(call, decl, sym, args, env, key)
	tr.assumeBoolPredicateEnsures(decl, args, env, sym)
	return sym, true
}

// emitBoolPredicateDefiningEquation gives a bool predicate that is a pure, TOTAL function (verified
// numeric `decreases`, see functionDefiningEquationEligible) its bounded, requires-guarded defining
// equation — the same equation an integer-valued function receives in callResultTermEnv.
func (tr *smtTranslator) emitBoolPredicateDefiningEquation(call *ast.CallExpr, decl *ast.FuncDecl, sym string, args []ast.Expr, argEnv map[string]string, key string) {
	if !tr.callEquationEligible(decl) || tr.a.isRecursiveProofCall(decl) {
		return
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			return
		}
		subst[param.Name] = args[i]
	}
	prior := tr.bindingCallee
	tr.bindingCallee = decl
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	tr.bindingCallee = prior
	if !ok {
		return
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	tr.emitDefiningEquationWithEnv(call, decl, sym, subst, key, guard, false, env)
}

// assumeBoolPredicateEnsures assumes a canonicalized bool predicate's ensures for this call (see
// assumeBoolCallEnsures), within the shared call-clause expansion budget.
func (tr *smtTranslator) assumeBoolPredicateEnsures(decl *ast.FuncDecl, args []ast.Expr, argEnv map[string]string, sym string) {
	if len(decl.EnsureValues) == 0 || tr.callClauseInFlight[decl] {
		return
	}
	if tr.callClauseDepth >= smtCallClauseMaxDepth || tr.callClauseExpansions >= smtCallClauseMaxExpansions {
		return
	}
	tr.callClauseExpansions++
	tr.callClauseDepth++
	tr.callClauseInFlight[decl] = true
	defer func() {
		tr.callClauseDepth--
		delete(tr.callClauseInFlight, decl)
	}()
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	if !ok {
		return
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	tr.assumeBoolCallEnsures(decl, env, guard, sym)
}

// boolPredicateCanonKey builds the canonicalization key for a bool-predicate call: the callee identity
// plus a SYNTACTIC projection of each argument (resolved through the current `env` binding so a
// match-bound child `l` keys on the projection it stands for, not the bare name). Datatype arguments
// have no integer SMT term, so — unlike callCanonKey — we key on the projection NAME, which is exactly
// what makes two textual occurrences of `p(t)` collapse to one symbol while keeping `p(l)` and `p(r)`
// distinct.
func (tr *smtTranslator) boolPredicateCanonKey(decl *ast.FuncDecl, args []ast.Expr, env map[string]string) (string, bool) {
	var b strings.Builder
	b.WriteString("boolpred|")
	if sym, ok := tr.a.symbolForFuncDecl(decl); ok && sym != nil {
		b.WriteString(decl.Name)
		_ = sym
	} else {
		b.WriteString(decl.Name)
	}
	b.WriteByte('|')
	for _, arg := range args {
		if arg == nil {
			return "", false
		}
		// Prefer an integer/bool SMT term when the argument lives in that fragment (so `p(n-1)` keys
		// canonically); otherwise fall back to a syntactic projection token (datatype args).
		if proj, ok := tr.boolPredicateArgToken(arg, env); ok {
			b.WriteString(proj)
			b.WriteByte('|')
			continue
		}
		return "", false
	}
	return b.String(), true
}

// boolPredicateArgToken yields a stable token for a predicate argument. For an argument bound in `env`
// (a match-child substitution) it uses the bound token directly; for a plain projection (ident / field
// chain) the syntactic projection name; for integer-fragment expressions the canonical SMT term.
func (tr *smtTranslator) boolPredicateArgToken(arg ast.Expr, env map[string]string) (string, bool) {
	if id, ok := stripOptimizationParens(arg).(*ast.Ident); ok {
		if env != nil {
			if bound, ok := env[id.Name]; ok && bound != "" {
				// A datatype-projection env binding (`result` -> the substituted value's projection) is
				// decoded to that projection, so `p(result)` with `result := t` keys identically to a bare
				// `p(t)`. Any other binding is an SMT term / child token used verbatim.
				if strings.HasPrefix(bound, dtTokenPrefix) {
					return "proj(" + strings.TrimPrefix(bound, dtTokenPrefix) + ")", true
				}
				return "tok(" + bound + ")", true
			}
		}
		return "proj(" + smtProjectionName(id) + ")", true
	}
	if fe, ok := stripOptimizationParens(arg).(*ast.FieldExpr); ok {
		return "proj(" + smtProjectionName(fe) + ")", true
	}
	if t, ok := tr.termEnv(arg, env); ok {
		return "int(" + t + ")", true
	}
	return "proj(" + smtProjectionName(arg) + ")", true
}

// emitStructuralPredicateEquation asserts `sym == <match-unfolding>` for a structurally well-founded
// bool predicate, with recursive sub-calls canonicalized (the IH). Bounded by callEqMaxUnroll. It is a
// sound NO-OP whenever the predicate is recursive but NOT verified structurally decreasing, the body is
// not a single pure `match`/return tree we can lower, or any arm fails to lower to a Bool term.
func (tr *smtTranslator) emitStructuralPredicateEquation(call *ast.CallExpr, decl *ast.FuncDecl, sym string, args []ast.Expr, env map[string]string, key string) {
	tr.a.addProofReportOrigin(decl)
	if tr == nil || decl == nil {
		return
	}
	// TOTALITY GATE: a recursive predicate must be proven structurally well-founded. A non-recursive
	// predicate is trivially total. Decline (opaque only) otherwise.
	if tr.a.predicateIsRecursive(decl) && !tr.a.predicateStructurallyWellFounded(decl) {
		return
	}
	if tr.callEqInFlight[key] >= tr.callEqMaxUnroll {
		return // bounded unroll reached: leave the symbol opaque (still sound)
	}
	// Map the predicate's parameters to the actual-argument tokens so a recursive sub-call on a child
	// keys on that child's projection (the IH symbol), and integer payload comparisons see the actuals.
	penv := map[string]string{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		if tok, ok := tr.boolPredicateArgToken(args[i], env); ok {
			penv[param.Name+"$tok"] = tok
		}
		if IsBoolType(tr.a.exprType(args[i])) {
			if bt, ok := tr.boolTerm(args[i], env); ok {
				penv[param.Name] = bt
			}
		} else if tr.a.smtEquationStructType(tr.a.exprType(args[i])) {
			// A struct argument binds field-wise (`l.a`), so the body's `l.a` reads the actual
			// argument's field rather than a free symbol named after the formal.
			tr.a.bindSMTStructEnvArg(tr, penv, param.Name, args[i], env, false)
		} else if t, ok := tr.termEnv(args[i], env); ok {
			penv[param.Name] = t
		}
	}
	tr.callEqInFlight[key]++
	defer func() { tr.callEqInFlight[key]-- }()
	bodyTerm, ok := tr.lowerPredicateBody(decl, penv)
	if !ok {
		return
	}
	tr.auxDecls = append(tr.auxDecls, "(assert (= "+sym+" "+bodyTerm+"))\n")
	if tr.a.predicateIsRecursive(decl) {
		if cert, ok := tr.a.recursiveEquationCertificate(decl); ok {
			tr.a.recordProof(call.Pos(), "structural induction over "+decl.Name+" ("+cert.label()+")", "definition", ProofProvenContract)
		} else {
			tr.a.recordProof(call.Pos(), "structural induction over "+decl.Name+" (verified structural decrease)", "definition", ProofProvenContract)
		}
	} else {
		tr.a.recordProof(call.Pos(), "predicate equation of "+decl.Name, "definition", ProofProvenContract)
	}
}

// lowerPredicateBody lowers a bool predicate's body to a Bool SMT term under the per-call environment
// `penv` (param tokens in `<name>$tok`, integer/bool param substitutions in `<name>`). Supports a body
// that is a single `match <param>:` over a recursive enum, or any pure-return tree boolTerm already
// handles. Returns ok=false for shapes we do not model.
func (tr *smtTranslator) lowerPredicateBody(decl *ast.FuncDecl, penv map[string]string) (string, bool) {
	priorDecl := tr.calleeBodyDecl
	tr.calleeBodyDecl = decl
	tr.calleeBodyDepth++
	defer func() { tr.calleeBodyDepth-- }()
	defer func() { tr.calleeBodyDecl = priorDecl }()
	// Pure-return tree (if/elif/else of bool returns, or a single bool return) — reuse boolTerm.
	if body, ok := tr.a.typedPureReturnExpr(decl); ok && body != nil {
		return tr.boolTerm(body, tr.predicateEnvIntBool(penv))
	}
	// Single top-level `match <param>:` returning bool from each arm.
	ms, scrutName, ok := singleMatchReturnBody(tr.a.readDecl(decl))
	if !ok {
		return "", false
	}
	return tr.lowerPredicateMatch(decl, ms, scrutName, penv)
}

// predicateEnvIntBool projects penv down to the int/bool substitution entries boolTerm understands
// (dropping the `$tok` projection entries, which are only for canonicalization keys).
func (tr *smtTranslator) predicateEnvIntBool(penv map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range penv {
		if strings.HasSuffix(k, "$tok") {
			continue
		}
		out[k] = v
	}
	return out
}
