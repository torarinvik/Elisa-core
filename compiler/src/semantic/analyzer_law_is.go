package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// tryAnalyzeLawIsExpr handles `subject is Law` as predicate application (docs/85 §2): `is` is
// UFCS first-arg binding, so `x is P` ≡ `P(x)`. When the (single) target resolves to a law, this
// builds the synthetic call `P(subject)`, analyzes it (which type-checks the subject against the
// law's first parameter and yields bool), and records it for codegen. Returns false when the
// target is not a bare law, leaving the existing `is` handling (variants, patterns, comparisons)
// untouched. The parametric `is P[args]` form is handled later with refinement types (Stage 1c).
func (a *Analyzer) tryAnalyzeLawIsExpr(expr *ast.BinaryExpr) bool {
	if a == nil || expr == nil || a.lawIsCalls == nil {
		return false
	}
	targets := flattenIsTargetExprs(expr.Right)
	if len(targets) != 1 {
		return false
	}
	// A built-in shape/measure law (docs/89) has no user `law` decl, so resolveLawIsTarget won't
	// recognize it; catch the wrong-class value `is NoBoundsChecks` / `is Vectorizes` on the bare
	// target name before falling through to type resolution (which would report a misleading "unknown
	// type").
	if name, ok := bareTargetName(targets[0]); ok && isBuiltinFunctionLevelLaw(name) {
		class := "shape law"
		if isBuiltinMeasureLaw(name) {
			class = "measure law"
		}
		a.errorf(expr.Pos(), "%q is a %s; apply it to a function with `fulfills %s`, not with `is` in a value position", name, class, name)
		return true
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return false
	}
	// A frame law (docs/88) is not a bool predicate — it cannot be applied with value `is`; it is
	// applied to a function with `fulfills`. Reject the wrong-class use with a clear diagnostic.
	if decl, _, found := a.lookupLaw(lawName); found && isFrameLaw(decl) {
		a.errorf(expr.Pos(), "%q is a frame law; apply it to a function with `fulfills ... is %s`, not with `is` in a value position", lawName, lawName)
		return true
	}
	// An effect law (docs/85 §4) is a function-level effect bound, not a value predicate — applied
	// with the subject-free `fulfills`, never value `is`.
	if decl, _, found := a.lookupLaw(lawName); found && isEffectLaw(decl) {
		a.errorf(expr.Pos(), "%q is an effect law; apply it to a function with `fulfills %s`, not with `is` in a value position", lawName, lawName)
		return true
	}
	// A composite law (docs/85 §6) is function-level — applied with the subject-free `fulfills`.
	if decl, _, found := a.lookupLaw(lawName); found && isCompositeLaw(decl) {
		a.errorf(expr.Pos(), "%q is a composite law; apply it to a function with `fulfills %s`, not with `is` in a value position", lawName, lawName)
		return true
	}
	call := &ast.CallExpr{
		Position: expr.Pos(),
		Func:     &ast.Ident{Position: expr.Right.Pos(), Name: lawName},
		Args:     append([]ast.Expr{expr.Left}, lawArgs...),
	}
	a.analyzeExpr(call)
	a.lawIsCalls[expr] = call
	return true
}

// resolveLawIsTarget recognizes an `is` target that names a law, returning the law name and its
// value arguments (nil for a bare law). Handles bare references (`Positive`) and the parametric form
// `Bounded[0, 500]` (a GenericType carrying value args). A non-law target, or a parametric target
// with a non-value (type) argument, returns false.
func (a *Analyzer) resolveLawIsTarget(target ast.Expr) (string, []ast.Expr, bool) {
	if name, ok := a.resolveBareLawIsTarget(target); ok {
		return name, nil, true
	}
	inner := target
	if p, ok := inner.(*ast.ParenExpr); ok && p != nil {
		inner = p.Inner
	}
	te, ok := inner.(*ast.TypeExprExpr)
	if !ok || te == nil {
		return "", nil, false
	}
	gen, ok := te.Type.(*ast.GenericType)
	if !ok || gen == nil || indexOfByte(gen.Name, '.') >= 0 {
		return "", nil, false
	}
	if _, _, isLaw := a.lookupLaw(gen.Name); !isLaw {
		return "", nil, false
	}
	// The target IS a law: reinterpret each bracket arg as a VALUE expression. A literal arg already
	// carries its value; an identifier or dotted path the parser shaped as a type node (`n`,
	// `xs.count`) is converted back to a value expr so a DEPENDENT bound (docs/85 §5.3) like
	// `Bounded[0, xs.count]` is usable as a `is`-narrowing target. Done only after the law check, so
	// genuine type tests over non-law generics are untouched.
	args := make([]ast.Expr, 0, len(gen.Args))
	for _, ta := range gen.Args {
		value, ok := typeArgAsValueExpr(ta)
		if !ok {
			return "", nil, false // an irreducible type argument ⇒ not a value-parametric law application
		}
		args = append(args, value)
	}
	return gen.Name, args, true
}

// typeArgAsValueExpr reinterprets a generic bracket argument as a value expression for a law
// application (docs/85). A `GenericValueArgTypeExpr` already wraps a value. A bare or dotted type
// name the parser produced for an identifier/path argument (`n`, `xs.count`) is rebuilt as an
// identifier or field-access chain so dependent bounds can name runtime values. Anything else (a real
// type, a generic instantiation with args) is not a value and returns false.
func typeArgAsValueExpr(ta ast.TypeExpr) (ast.Expr, bool) {
	switch t := ta.(type) {
	case *ast.GenericValueArgTypeExpr:
		if t == nil || t.Value == nil {
			return nil, false
		}
		return t.Value, true
	case *ast.NamedType:
		if t == nil || t.Name == "" {
			return nil, false
		}
		return dottedNameAsValueExpr(t.Name, t.Position), true
	case *ast.GenericType:
		if t == nil || len(t.Args) != 0 || t.Name == "" {
			return nil, false
		}
		return dottedNameAsValueExpr(t.Name, t.Position), true
	default:
		return nil, false
	}
}

// dottedNameAsValueExpr turns a (possibly dotted) name into a value expression: `n` → Ident{n},
// `xs.count` → FieldExpr{Ident{xs}, count}.
func dottedNameAsValueExpr(name string, pos lexer.Pos) ast.Expr {
	parts := strings.Split(name, ".")
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for _, field := range parts[1:] {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}
	}
	return expr
}

// recordRefinementChecks records the discharge obligations for a refinement-typed var declaration
// (docs/85 Stage 1c-2): for each bare predicate `P` in the declared type, the check `P(name)` that
// must hold for the bound value. Codegen emits these as a debug boundary check (trap on
// violation), elided in release. Bare predicates only for now; parametric `P[args]` discharge
// (range/const-arg expansion) is a follow-up.
func (a *Analyzer) recordRefinementChecks(n *ast.VarDeclStmt) {
	if a == nil || n == nil || a.refinementChecks == nil || n.Value == nil {
		return
	}
	// paramRefinementTypeExpr (not a raw cast) so a `type` ALIAS local — `base: PageAddr = …` —
	// surfaces its underlying predicates instead of erasing them (the param-boundary fix, applied
	// to local declarations too; otherwise an alias-typed local would be a vacuous no-check).
	rt, ok := a.paramRefinementTypeExpr(n.Type)
	if !ok || rt == nil {
		return
	}
	var checks []*ast.CallExpr
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue // not a law — already reported by validateRefinementPreds
		}
		// Try to discharge statically (flow then constant entailment). Proven/refuted → done.
		if a.tryDischargeRefinementStatically(n.Value, "\""+n.Name+"\"", pred, lawDecl, n.Pos()) {
			continue
		}
		// Contract composition: the initializer carries a refinement scheme (call return, refined
		// field read, or immutable declared binding) whose predicates entail this local's. The scheme
		// adapter keeps the three sources on one consumption path while preserving the existing
		// per-source helper API.
		if a.exprRefinementSchemeEntails(n.Value, pred) {
			a.recordProof(n.Pos(), "\""+n.Name+"\"", pred.Name, ProofProvenContract)
			continue
		}
		// Not statically proven: fall back to a runtime check AND tell the user — a static guarantee
		// was not achieved here (docs/85: the fallback must be KNOWN). Warning by default; hard error
		// under -strict (prove-it-or-fail, the Dafny-like mode).
		a.recordProof(n.Pos(), "\""+n.Name+"\"", pred.Name, ProofRuntime)
		a.proofLint(n.Pos(), "refinement %q on %q could not be proven statically; it is checked at runtime (debug) — make the value provable, or accept the runtime check%s", pred.Name, n.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
		// A built-in modular predicate (Aligned/Divides/Fits/MaskedZero) has no callable symbol, so a
		// synthetic `Pred(value)` check would mis-analyze as an undefined identifier. Skip it — under
		// -strict the proofLint above is already a hard error.
		if isBuiltinModularLawName(pred.Name) {
			continue
		}
		call := &ast.CallExpr{
			Position: pred.Position,
			Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
			Args:     append([]ast.Expr{&ast.Ident{Position: n.Pos(), Name: n.Name}}, pred.Args...),
		}
		a.analyzeExpr(call)
		checks = append(checks, call)
	}
	if len(checks) != 0 {
		a.refinementChecks[n] = checks
	}
}

// tryDischargeRefinementStatically attempts to discharge one refinement obligation `value is
// pred` at compile time, by flow entailment then constant entailment. Returns true when statically
// resolved: PROVEN (nothing emitted) or REFUTED (a compile error is emitted here). Returns false
// when the obligation is unknown — the caller decides the runtime/observability fallback. Shared by
// the var-decl boundary and call-argument boundaries.
func (a *Analyzer) tryDischargeRefinementStatically(value ast.Expr, valueName string, pred ast.RefinementPredExpr, lawDecl *ast.FuncDecl, pos lexer.Pos) bool {
	return a.tryDischargeRefinementStaticallyOpt(value, valueName, pred, lawDecl, pos, true)
}

// tryDischargeRefinementStaticallyOpt is tryDischargeRefinementStatically with control over whether
// DEPENDENT predicate facts (docs/85 §5.3) may discharge the obligation. Same-function obligations
// (var decl, return, ensures) allow them; a cross-function call-argument boundary does not, since a
// dependent key names variables in the callee's scope (see tryProveRefinementByFactSet).
func (a *Analyzer) tryDischargeRefinementStaticallyOpt(value ast.Expr, valueName string, pred ast.RefinementPredExpr, lawDecl *ast.FuncDecl, pos lexer.Pos, allowDependentFacts bool) bool {
	a.addProofReportOrigin(lawDecl)
	// Reset any counterexample left over from a previous obligation; the SMT tier below re-populates it
	// on a failed proof, so the diagnostics that follow only ever show a witness for THIS obligation.
	a.lastSMTCounterexample = ""
	// Cheap modular tier (docs/85 modular predicates): a syntactic alignment/mask proof that does not
	// need the solver — `x & C`, `k*N`, an already-aligned constant. Runs first so the common
	// page-alignment shapes discharge without z3. Sound: only proves, never refutes.
	if isBuiltinModularLawName(pred.Name) && a.tryDischargeModularCheaply(value, pred.Name, pred.Args) {
		return true
	}
	if a.tryProveRefinementByFlow(value, lawDecl, pred.Args) {
		a.recordProof(pos, valueName, pred.Name, ProofProvenFlow)
		return true
	}
	// Mutable refinement flow (docs/85): a live predicate fact gained from a narrowing and not
	// since invalidated by a mutation discharges the obligation with no runtime check.
	if a.tryProveRefinementByFactSet(value, pred.Name, pred.Args, allowDependentFacts) {
		a.recordProof(pos, valueName, pred.Name, ProofProvenFlow)
		return true
	}
	// Tier-2 (docs/86): a DERIVED affine subject (e.g. `tx*MAPHEIGHT + ty`) whose bounded range
	// entails the law. Runs after the bare-variable tiers declined, before the const-eval backstop.
	if a.tryProveRefinementByLinear(value, lawDecl, pred.Args, a.currentScope) {
		a.recordProof(pos, valueName, pred.Name, ProofProvenLinear)
		return true
	}
	// Relational tier (docs/85 dependent-arg): discharges `subject is law[args]` when one or more
	// bracket args are non-constant immutable identifiers whose relationship to the subject is
	// established by a `requires` clause of the enclosing function. E.g. `raw is InRange[0, cap]`
	// with `requires raw < cap` proves without SMT. Sound: both subject and bracket-arg identifiers
	// must be immutable so the requires fact is valid throughout the body.
	if a.tryProveRefinementByRelational(value, lawDecl, pred.Args) {
		a.recordProof(pos, valueName, pred.Name, ProofProvenLinear)
		return true
	}
	// SMT tier (docs/90): the last prove-step before the runtime fallback. Only reached when the
	// linear tier declined, so the subject is genuinely outside the affine fragment (e.g. a var*var
	// product). Off unless -smt; sound regardless (only `unsat` of the negation concludes).
	if a.trySMTProveRefinement(value, lawDecl, pred.Args) {
		a.recordProof(pos, valueName, pred.Name, ProofProvenSMT)
		return true
	}
	// Written-constant substitution: when the subject is a bare variable whose last write was a
	// compile-time constant of any kind (`p <- 0`, `ready <- true`, `d <- Door.Open` — even through a
	// non-aliased `mutable T&` pointee), use that constant expr as the const-eval subject. This proves
	// `ensures p is Positive` after `p <- 1` (refutes after `p <- 0`), and likewise for bool/enum
	// laws, where the bare identifier alone carries no const value.
	constSubject := value
	if ident, ok := value.(*ast.Ident); ok && ident != nil {
		if c, known := a.lookupWrittenConst(ident.Name); known {
			constSubject = c
		}
	}
	if ok, known := a.evalConstBoolExpr(&ast.CallExpr{
		Position: pred.Position,
		Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
		Args:     append([]ast.Expr{constSubject}, pred.Args...),
	}); known {
		if !ok {
			a.recordProof(pos, valueName, pred.Name, ProofRefuted)
			a.errorf(pos, "refinement %q is violated: %s does not satisfy it", pred.Name, valueName)
		} else {
			a.recordProof(pos, valueName, pred.Name, ProofProvenConst)
		}
		return true
	}
	// A quantified law body (docs/90 brick 90-4) is SPEC-ONLY: an unbounded `forall`/`exists` is not
	// executable, so there is no runtime fallback. If neither SMT nor const-eval proved it, report it
	// here (warning by default, error under -strict) and return resolved so the caller emits NO runtime
	// check (which would generate broken code for a quantifier).
	if a.lawBodyContainsQuantifier(lawDecl) {
		a.recordProof(pos, valueName, pred.Name, ProofRuntime)
		a.proofLint(pos, "quantified refinement %q on %s could not be proven statically (it has no runtime check); enable -smt or strengthen the facts%s", pred.Name, valueName, a.counterexampleSuffix(a.lastSMTCounterexample))
		return true
	}
	return false
}

// lawBodyContainsQuantifier reports whether a law's `= <bool-expr>` body contains a `forall`/`exists`
// — making it spec-only (provable by SMT, never runtime-checkable).
func (a *Analyzer) lawBodyContainsQuantifier(decl *ast.FuncDecl) bool {
	body, ok := a.lawBodyExpr(decl)
	if !ok {
		return false
	}
	return a.walkStaticExpr(body, func(e ast.Expr) bool {
		_, isQ := e.(*ast.QuantifierExpr)
		return isQ
	})
}

// dischargeCallArgRefinements discharges the refinement obligations on a direct call's arguments
// against the callee's refinement-typed parameters (docs/85: the function-contract boundary). The
// callee's param refinements survive on its decl's parameter type exprs (the resolved FuncType is
// erased), so a direct by-name call can read them and prove/refute/warn each argument with the same
// discharge tiers as a var declaration. Runtime enforcement of an unproven arg at the call site is
// a follow-up; under -strict an unproven arg is already a hard error.
func (a *Analyzer) dischargeCallArgRefinements(call *ast.CallExpr, args []ast.Expr) {
	// The canonical channel is authoritative even for a direct declaration.
	// Avoid allocating a full scheme when it has no argument obligations, while
	// retaining the declaration dependency used by incremental proof reports.
	if ft, ok := a.callFuncType(call); ok && ft.SpecSignature != nil && len(ft.SpecSignature.ParamPredicates) == 0 {
		if _, found := a.resolveDirectCallFuncDecl(call); !found {
			a.resolveDirectCallExternFuncDecl(call)
		}
		return
	}
	scheme, ok := a.callRefinementScheme(call)
	if !ok || len(scheme.ParamRefinements) == 0 {
		return
	}
	var checks []*ast.CallExpr
	for _, paramRefinement := range scheme.ParamRefinements {
		i := paramRefinement.ParamIndex
		if i >= len(args) || args[i] == nil {
			continue
		}
		for _, pred := range paramRefinement.Preds {
			lawDecl, _, ok := a.lookupLaw(pred.Name)
			if !ok {
				continue
			}
			name := "argument " + itoaParam(i+1)
			// Tag all proof records in this obligation as call-arg-refinement for elision telemetry.
			a.currentProofCategory = ProofCatCallArgRefinement
			// Cross-function boundary: a dependent fact's names belong to the callee's scope, so they
			// must not discharge a caller-site argument obligation (allowDependentFacts=false).
			if a.tryDischargeRefinementStaticallyOpt(args[i], name, pred, lawDecl, call.Pos(), false) {
				continue
			}
			// Contract composition: the argument carries a refinement scheme whose predicates entail
			// this parameter's. This is what makes `map_fixed(page_align_down(raw), ..)` prove when
			// `page_align_down -> u64 is Aligned[..]`.
			a.currentProofCategory = ProofCatCallArgRefinement
			if a.exprRefinementSchemeEntails(args[i], pred) {
				a.recordProofCat(call.Pos(), name, pred.Name, ProofProvenContract, ProofCatCallArgRefinement)
				continue
			}
			a.recordProofCat(call.Pos(), name, pred.Name, ProofRuntime, ProofCatCallArgRefinement)
			a.proofLint(call.Pos(), "refinement %q on %s of %q could not be proven statically; pass a provable value or accept the runtime check%s", pred.Name, name, scheme.DeclName, a.counterexampleSuffix(a.lastSMTCounterexample))
			// Fall back to a runtime debug-check at the call site — but only for a side-effect-free
			// argument, since the predicate re-evaluates it. An impure arg keeps the warning only (a
			// double evaluation would change behavior), so the runtime tier stays sound.
			if isBuiltinModularLawName(pred.Name) || !a.isSideEffectFreeRefinementArg(args[i]) {
				continue
			}
			check := &ast.CallExpr{
				Position: pred.Position,
				Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
				Args:     append([]ast.Expr{args[i]}, pred.Args...),
			}
			a.analyzeExpr(check)
			checks = append(checks, check)
		}
	}
	if len(checks) != 0 && a.callArgRefinementChecks != nil {
		a.callArgRefinementChecks[call] = checks
	}
}

// dischargeReturnRefinements discharges the refinement obligations on a returned value against the
// enclosing function's refinement-typed return (docs/85: the return half of the function-contract
// boundary). Symmetric to dischargeCallArgRefinements: prove/refute statically, else warn (and, for
// a side-effect-free value, record a debug runtime check); under -strict an unproven return is a
// hard error.
// returnCallRefinementEntails reports whether `value` is a direct call whose declared return type
// carries a refinement predicate that ENTAILS `pred` — so the function returning that call's result
// inherits the guarantee. Sound: the callee's return refinement is enforced on its every exit
// (statically or by a debug runtime check), so its result already satisfies `pred`.
func (a *Analyzer) returnCallRefinementEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	s, ok := a.callReturnRefinementScheme(value)
	return ok && a.valueRefinementSchemeEntails(s, pred)
}

// returnFieldRefinementEntails reports whether `value` is a struct FIELD read whose declared field
// refinement entails `pred`. Since field refinements are enforced at construction, the field's value
// already satisfies its refinement, so reading it (`return d.sdst` where sdst is `is InRange[0,127]`)
// inherits that guarantee — refinement types erase to their base on read, so this recovers the bound.
func (a *Analyzer) returnFieldRefinementEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	s, ok := a.fieldReadRefinementScheme(value)
	return ok && a.valueRefinementSchemeEntails(s, pred)
}

// argDeclaredRefinementEntails reports whether `value` is an identifier whose DECLARED type (an
// immutable local var-decl or a parameter) carries a refinement predicate that entails `pred`. A
// refinement-typed value satisfies its predicate at every read — for a parameter the caller proved
// it at the call site, for an immutable local the declaration proved it — so passing it where the
// same predicate is required discharges without a runtime re-check: `base: u64 is Aligned[4096]`
// then `map_fixed(base)`. Gated to IMMUTABLE bindings: a mutable variable could be reassigned to a
// non-conforming value after declaration, so its declared refinement is not a standing guarantee.
func (a *Analyzer) argDeclaredRefinementEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	s, ok := a.declaredBindingRefinementScheme(value)
	return ok && a.valueRefinementSchemeEntails(s, pred)
}

// refinementPredicatesEntail reports whether predicate `have` guarantees `want`. This is the central
// value-contract entailment hook used by local declarations, call arguments, return forwarding, field
// forwarding, and declared-binding forwarding. It deliberately proves only small, sound fragments:
// exact predicate applications, and interval inclusion for laws whose bodies already lower to the
// existing `self OP const` constraint model. Future SMT-backed entailment belongs behind this helper.
func (a *Analyzer) refinementPredicatesEntail(have, want ast.RefinementPredExpr) bool {
	if a.refinementPredicatesExactlyMatch(have, want) {
		return true
	}
	return a.refinementPredicateIntervalEntails(have, want)
}

func (a *Analyzer) refinementPredicatesExactlyMatch(have, want ast.RefinementPredExpr) bool {
	if have.Name != want.Name || len(have.Args) != len(want.Args) {
		return false
	}
	for i := range have.Args {
		hv, hok := a.constIntValue(have.Args[i])
		wv, wok := a.constIntValue(want.Args[i])
		if !hok || !wok || hv != wv {
			return false
		}
	}
	return true
}

func (a *Analyzer) refinementPredicateIntervalEntails(have, want ast.RefinementPredExpr) bool {
	haveDecl, _, ok := a.lookupLaw(have.Name)
	if !ok {
		return false
	}
	wantDecl, _, ok := a.lookupLaw(want.Name)
	if !ok {
		return false
	}
	haveConstraints, ok := a.refinementPredicateConstraints(haveDecl, have.Args)
	if !ok || len(haveConstraints) == 0 {
		return false
	}
	wantConstraints, ok := a.refinementPredicateConstraints(wantDecl, want.Args)
	if !ok || len(wantConstraints) == 0 {
		return false
	}
	r, ok := rangeFromLawConstraints(haveConstraints)
	if !ok {
		return false
	}
	for _, k := range wantConstraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}

func (a *Analyzer) refinementPredicateConstraints(decl *ast.FuncDecl, args []ast.Expr) ([]lawConstraint, bool) {
	if decl == nil || len(args) != len(decl.Params)-1 {
		return nil, false
	}
	paramConsts := map[string]int64{}
	for i, arg := range args {
		c, ok := a.constIntValue(arg)
		if !ok {
			return nil, false
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	return a.lawConstraints(decl, paramConsts)
}

func rangeFromLawConstraints(constraints []lawConstraint) (numRange, bool) {
	var r numRange
	for _, k := range constraints {
		switch k.op {
		case lexer.TOKEN_GTEQ:
			if !r.loKnown || k.c > r.lo {
				r.loKnown = true
				r.lo = k.c
			}
		case lexer.TOKEN_GT:
			if k.c == int64(^uint64(0)>>1) {
				return numRange{}, false
			}
			lo := k.c + 1
			if !r.loKnown || lo > r.lo {
				r.loKnown = true
				r.lo = lo
			}
		case lexer.TOKEN_LTEQ:
			if !r.hiKnown || k.c < r.hi {
				r.hiKnown = true
				r.hi = k.c
			}
		case lexer.TOKEN_LT:
			if k.c == -int64(^uint64(0)>>1)-1 {
				return numRange{}, false
			}
			hi := k.c - 1
			if !r.hiKnown || hi < r.hi {
				r.hiKnown = true
				r.hi = hi
			}
		case lexer.TOKEN_EQEQ:
			if r.loKnown && r.lo > k.c {
				return numRange{}, false
			}
			if r.hiKnown && r.hi < k.c {
				return numRange{}, false
			}
			r.loKnown = true
			r.lo = k.c
			r.hiKnown = true
			r.hi = k.c
		default:
			return numRange{}, false
		}
	}
	if r.loKnown && r.hiKnown && r.lo > r.hi {
		return numRange{}, false
	}
	return r, true
}
