package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (a *Analyzer) dischargeEnsureBooleans(n *ast.ReturnStmt) {
	if a == nil || n == nil || n.Value == nil || a.currentFuncDecl == nil {
		return
	}
	if len(a.currentFuncDecl.EnsureValues) == 0 {
		return
	}
	// All proof records in this function are contract-ensures obligations (elision telemetry).
	defer func(prev ProofObligationCategory) { a.currentProofCategory = prev }(a.currentProofCategory)
	a.currentProofCategory = ProofCatContractEnsures
	// An implicit non-value-preserving integer conversion at the return (an i64 `view.len` returned
	// as usize) is made explicit for `result`; the tiers below that re-read the return expression or
	// the body's return statements themselves would see the unconverted value, so they are skipped.
	retValue := a.implicitIntConversion(n.Value, a.exprType(n.Value), a.currentReturn)
	converted := retValue != n.Value
	subst := map[string]ast.Expr{"result": retValue}
	for i, clause := range a.currentFuncDecl.EnsureValues {
		if clause == nil {
			continue
		}
		if proofAt(a.currentFuncDecl.EnsureProofs, i) != nil {
			continue
		}
		// Refutation gate (always on, independent of -strict): a postcondition PROVABLY FALSE for the
		// returned value is a hard error here, mirroring the refuted-`requires`/`where` gate. Only a
		// provably-false verdict escalates; merely-unprovable clauses fall through to the strict ladder
		// below (which lints / -strict-errors). Skip the rest of this clause once it is refuted.
		// See dischargeEnsureBooleansAtVoidExit: a shadowed parameter makes every tier unsound here.
		if a.mentionsShadowedParam(clause) {
			if a.enforceStrictProofs {
				diag := a.buildEnsureFailureDiagnostic(clause, subst, "")
				a.errorf(n.Pos(), "%s", diag.FormatEnsure(a.currentFuncDecl.Name))
			}
			continue
		}
		if a.ensureClauseRefuted(clause, subst) {
			a.reportRefutedEnsure(n.Pos(), clause, subst)
			continue
		}
		// The remaining proving ladder and its unprovable→runtime-fallback lint stay -strict-gated.
		if !a.enforceStrictProofs {
			continue
		}
		clause = normalizeBoolLiteralEnsure(clause)
		if a.ensureGoalReflexivelyValid(clause, subst) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "reflexivity", ProofProvenSMT)
			continue
		}
		// Reflexivity fast-path: `ensure result == E` where the body is `return E` and E is a pure
		// place read (ident / field projection) holds definitionally — result IS the value of E at this
		// same side-effect-free point. The SMT lane otherwise fails these because two loads of a
		// reference field (`self.base`) are modeled as distinct opaque terms (shadPS4 memory-06 finding).
		// NaN != NaN: a float result is never equal to itself by reflexivity.
		if !converted && !IsFloatType(stripRefForBounds(a.exprType(n.Value))) && ensureHoldsByReturnReflexivity(clause, n.Value) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "reflexivity", ProofProvenSMT)
			continue
		}
		// Written-field fast-path also applies at an explicit `return` in a void/struct-mutating fn
		// (`ensure self.f == E` over a `self.f <- V` the body performed). The clause does not reference
		// `result`, so the subst above is inert for it. Same soundness as the void-exit path.
		if !exprReferencesResult(clause) && a.ensureHoldsByWrittenField(clause) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "written field", ProofProvenSMT)
			continue
		}
		if !converted && a.tryProveEnsureByDisjunctiveResultRange(clause, n.Value) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "range disjunction", ProofProvenSMT)
			continue
		}
		if a.incrementalBodies != nil && a.incrementalBodies.hasEnsureProof(a.currentFuncDecl, i) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenSMT)
			continue
		}
		if a.ensureGoalIsLiveFact(clause, subst) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "live fact", ProofProvenSMT)
			continue
		}
		proven, counterexample := a.trySMTProveRequiresReported(clause, subst)
		if a.incrementalBodies != nil {
			a.incrementalBodies.recordEnsureProof(a.currentFuncDecl, i, proven)
		}
		if proven {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenSMT)
			continue
		}
		if call, ok := a.proofCallExpr(n.Value); ok && !converted {
			if a.tryProveEnsureByReturnCallRange(clause, call) {
				a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenSMT)
				continue
			}
			// docs/101: `return f(x)` where `f` is a contracted function-typed parameter — assume the
			// parameter contract's predicate on the call result (discharged at every call site).
			if a.tryProveEnsureByParamContract(clause, call) {
				a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenContract)
				continue
			}
			if a.trySMTProveEnsureFromReturnCall(clause, call) {
				a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenSMT)
				continue
			}
		}
		// Weakest-precondition transport: a straight-line scalar body with mutable reassignments has no
		// immutable-local equality for the prover, so thread the postcondition backward through the
		// assignments (VC IR brick 3) and discharge the result over the parameters.
		if !converted && a.tryProveEnsureByWP(clause, n) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "wp", ProofProvenSMT)
			continue
		}
		if !converted && a.trySMTProveEnsureFromPureReturnPaths(clause) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "return paths", ProofProvenSMT)
			continue
		}
		if a.tryProveEnsureNullnessCastDisjunct(clause, subst) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "nullness-cast disjunct", ProofProvenSMT)
			continue
		}
		diag := a.buildEnsureFailureDiagnostic(clause, subst, counterexample)
		a.errorf(n.Pos(), "%s", diag.FormatEnsure(a.currentFuncDecl.Name))
	}
}

// ensureClauseRefuted reports whether the postcondition `clause` (under the given substitution of
// `result` → the returned value, or nil at a void exit) is PROVABLY FALSE on this reachable exit path.
// This is the postcondition mirror of the precondition/`where` refutation gate (`requiresRefuted` in
// analyzer_requires_discharge.go / analyzer_where_refinements.go): a `requires`/`where` whose negation
// is entailed already hard-errors, and so must an `ensure` whose negation is entailed at the return site.
//
// It is deliberately ASYMMETRIC with the proving ladder: only a verdict of *refuted* returns true. An
// obligation that is merely UNPROVEN (requiresUnknown) returns false and is left to the existing
// unprovable→runtime-fallback lint. This keeps the escalation SOUND: a hard error fires only when no
// reachable return value could satisfy the clause.
//
// The refutation oracle is the linear clause prover (`proveRequiresClause`), whose `requiresRefuted`
// verdict means the clause's negation holds over the WHOLE bounded range — the exact primitive
// `requires`/`where` use. The `result` substitution is threaded through unchanged; proveRequiresClause
// substitutes internally (its `result` ident binds to the returned value's affine form via affineOf),
// just as the precondition path threads the callee-param→caller-arg map. affineOf DECLINES on mutable
// locals, so a postcondition over a reassigned local (`return y` after branch merges) yields
// requiresUnknown, not a refutation — only constants / immutable affine values can be refuted.
//
// We deliberately do NOT consult an SMT negation here. trySMTProveRequires would prove `not clause`
// against the analyzer's STANDING fact set, which can be INCOMPLETE at a return (e.g. a stale `y == 0`
// fact from before an if/else merge that the strict ladder's WP transport corrects). Concluding
// refutation from those facts would be unsound — the merge path can in fact satisfy the clause. The
// linear affine prover avoids this because it relies on exact bounds and abstains on mutated locals.
func (a *Analyzer) ensureClauseRefuted(clause ast.Expr, subst map[string]ast.Expr) bool {
	if a == nil || clause == nil {
		return false
	}
	clause = normalizeBoolLiteralEnsure(clause)
	if a.proveRequiresClause(clause, subst) != requiresRefuted {
		return false
	}
	// A refutation is only an ERROR on a reachable exit. The interval tier sees the returned value's
	// bounds but not the path condition, so on a dead branch (`x: i64 = 5; return 1 if x == 5;
	// return 2`) it "refutes" a clause no execution can violate and rejects a correct program. When the
	// path's own facts are contradictory (the solver proves `false` from them), the exit is
	// unreachable and nothing is refuted. This can only withdraw a hard error, never prove a clause:
	// the strict ladder below still decides the obligation exactly as for any unrefuted clause.
	if dead, _ := a.trySMTProveRequires(&ast.BoolLit{Position: clause.Pos(), Value: false}, nil); dead {
		return false
	}
	return true
}

// reportRefutedEnsure emits the hard error for a postcondition statically proven false on a reachable
// exit path, reusing the rich ensure diagnostic and recording a ProofRefuted fact (mirroring the
// requires/where refutation reporting). Unlike the unprovable lint, this fires regardless of -strict.
func (a *Analyzer) reportRefutedEnsure(pos lexer.Pos, clause ast.Expr, subst map[string]ast.Expr) {
	if a == nil || a.currentFuncDecl == nil {
		return
	}
	a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "ensure", ProofRefuted)
	diag := a.buildEnsureFailureDiagnostic(clause, subst, "")
	a.errorf(pos, "postcondition of %q is provably violated: %s", a.currentFuncDecl.Name, diag.FormatEnsure(a.currentFuncDecl.Name))
}

// tryProveEnsureNullnessCastDisjunct discharges a disjunctive postcondition `(P != null) or (result == …)`
// where the non-null operand `P` is a function parameter whose non-null-ness on the active return path is
// only KNOWN through a nullness-preserving cast: a local `e = P.cast[…]` is null-guarded (`if e == null:
// return …`), so the surviving path carries the flow fact `e != null` — but the disjunct is phrased over
// `P`. A pointer cast maps null↔null and non-null↔non-null bijectively, so the two pointers share their
// null-ness exactly; this helper surfaces that equivalence as the extra hypothesis `(= isnull_e isnull_P)`
// for every immutable local in scope that is defined as a pointer cast of another pointer path, then
// re-runs the standard discharge (which already assumes the path's `e != null` flow fact). SOUND: only an
// equality between the two predicates the prover already keys is added; no extra non-null-ness is asserted,
// so a return that genuinely permits `P == null` (and a falsy `result`) still fails (its flow lacks the
// `e != null` fact). Kept as a self-contained helper + one fast-path line so a later merge stays trivial.
func (a *Analyzer) tryProveEnsureNullnessCastDisjunct(clause ast.Expr, subst map[string]ast.Expr) bool {
	if a == nil || clause == nil {
		return false
	}
	bin, ok := stripOptimizationParens(clause).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_OR {
		return false
	}
	if !a.smtEnabled || a.smtUnavailable {
		return false
	}
	tr := a.newSMTTranslator(nil)
	env, ok := a.smtEnvForSubst(tr, subst)
	if !ok {
		return false
	}
	extra := a.smtNullnessCastLinkHypotheses(tr)
	// C2: also admit a pointer-nullness `result == null` disjunct. The null-compare lowering keys
	// `result == null` to the opaque Bool `isnull_result`; on a `return null` path the return value
	// IS the null literal, so the disjunct holds definitionally. Surface that as the extra hypothesis
	// `(= isnull_result true)`, gated on the returned expression being a literal null (subst maps
	// `result` to it). SOUND: the fact is asserted ONLY when the return value is *definitely* null, so a
	// path that returns a non-null pointer leaves `isnull_result` free and a bare `ensure result == null`
	// still fails. The cast-link disjunct (`ev != null`) is unaffected on the non-null return path.
	extra += a.smtResultNullnessHypothesis(tr, subst)
	if extra == "" {
		return false
	}
	proven, _, lowered := a.smtCheckGoal(tr, clause, env, extra)
	return lowered && proven
}

// smtResultNullnessHypothesis emits an `isnull_result` assertion when the substitution binds
// `result` to a value whose null-ness is statically determined:
//   - `return null`   → `(= isnull_result true)`  (the literal is definitionally null)
//   - `return <T&>`   → `(= isnull_result false)` (a non-optional ref is structurally non-null)
//
// The `isnull_` predicate key mirrors exactly what nullComparePointer+boolTerm produce for
// `result == null` / `result != null`, so the asserted fact is immediately consumed by the solver
// when the obligation contains either comparison.
// SOUND:
//   - null branch: asserted only when ret is a NullLit — the compiler already type-checks that
//     the nil literal is assignable to the return type, so the assertion is true by construction.
//   - non-null branch: asserted only when the return expression's type is a `RefType` (T&, the
//     non-optional reference). In Elisa's type system T& cannot hold null (only T&?/T? can), so
//     the assertion is a structural guarantee, not a flow inference. OptionalType returns are left
//     unconstrained; they do NOT trigger this path, so `ensure result != null` over an optional
//     return still fails unless a flow guard proves non-nullness via smtAssertHypotheses.
func (a *Analyzer) smtResultNullnessHypothesis(tr *smtTranslator, subst map[string]ast.Expr) string {
	if a == nil || tr == nil || subst == nil {
		return ""
	}
	ret, ok := subst["result"]
	if !ok || ret == nil {
		return ""
	}
	pred := "isnull_result"
	// Case 1: literal null return → isnull_result is true.
	if _, isNull := stripOptimizationParens(ret).(*ast.NullLit); isNull {
		tr.boolDecls[pred] = true
		return "(assert (= " + pred + " true))\n"
	}
	// Case 2: the return expression has type T& (non-optional reference) → structurally non-null.
	retType := a.exprType(ret)
	if retType == nil {
		return ""
	}
	// A non-nullable reference (T&, RefStateNonNull) is structurally non-null.
	// A nullable reference (T&?, RefStateNullable) or an OptionalType (T?) may be null — skip.
	if rt, isRef := IsRefType(retType); isRef && rt.State == RefStateNonNull {
		tr.boolDecls[pred] = true
		return "(assert (= " + pred + " false))\n"
	}
	return ""
}

// smtNullnessCastLinkHypotheses emits `(= isnull_<local> isnull_<source>)` for each immutable local in
// scope whose defining expression is a pointer cast `<source>.cast[…]` of a projectable pointer path. The
// predicate keys mirror exactly what the BinaryExpr null-compare lowering produces (`isnull_` +
// smtProjectionName), so the asserted equivalence connects a `e == null` guard fact to a `P != null`
// obligation. Returns "" when no such link exists (the helper then declines, costing nothing).
func (a *Analyzer) smtNullnessCastLinkHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	seen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for name, sym := range sc.Symbols {
			if seen[name] || sym == nil || sym.Mutable || sym.Kind != SymbolLocal {
				continue
			}
			vd, ok := sym.Node.(*ast.VarDeclStmt)
			if !ok || vd == nil || vd.Value == nil {
				continue
			}
			cast, ok := stripOptimizationParens(vd.Value).(*ast.CastExpr)
			if !ok || cast == nil || cast.Operand == nil {
				continue
			}
			if !a.exprIsPointerLike(sym.Type) || !a.exprIsPointerLike(a.exprType(cast.Operand)) {
				continue
			}
			srcName := smtProjectionName(stripOptimizationParens(cast.Operand))
			if srcName == "" {
				continue
			}
			seen[name] = true
			localPred := "isnull_" + name
			srcPred := "isnull_" + srcName
			tr.boolDecls[localPred] = true
			tr.boolDecls[srcPred] = true
			b.WriteString("(assert (= " + localPred + " " + srcPred + "))\n")
		}
	}
	return b.String()
}

// exprIsPointerLike reports whether a type is a nullable/reference pointer (an optional or a ref), the
// only shape whose null-ness a cast preserves and the only shape the null-compare lowering keys.
func (a *Analyzer) exprIsPointerLike(t Type) bool {
	if t == nil {
		return false
	}
	if _, ok := IsOptionalType(t); ok {
		return true
	}
	if _, ok := IsRefType(t); ok {
		return true
	}
	return false
}

func (a *Analyzer) trySMTProveEnsureFromPureReturnPaths(clause ast.Expr) bool {
	if a == nil || a.currentFuncDecl == nil || clause == nil || a.shadowingActive() {
		return false
	}
	body, ok := a.typedPureReturnExpr(a.currentFuncDecl)
	if !ok || body == nil {
		return false
	}
	proven, _ := a.trySMTProveRequires(clause, map[string]ast.Expr{"result": body})
	return proven
}

// isSideEffectFreeRefinementArg reports whether a call argument can be safely re-evaluated by a
// runtime refinement predicate check (no observable side effect, no double cost worth worrying
// about). Conservatively: bare identifiers, literals, and parenthesizations of those.
func (a *Analyzer) isSideEffectFreeRefinementArg(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return n != nil && a.isSideEffectFreeRefinementArg(n.Inner)
	case *ast.Ident:
		return true
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	default:
		return false
	}
}
