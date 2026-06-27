package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

func (a *Analyzer) dischargeReturnRefinements(n *ast.ReturnStmt) {
	if a == nil || n == nil || n.Value == nil || a.currentFuncDecl == nil {
		return
	}
	rt, ok := a.paramRefinementTypeExpr(a.currentFuncDecl.ReturnType)
	if !ok || rt == nil {
		return
	}
	var checks []*ast.CallExpr
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue
		}
		// Tag all proof records in this obligation as return-refinement for elision telemetry.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.tryDischargeRefinementStatically(n.Value, "the returned value", pred, lawDecl, n.Pos()) {
			continue
		}
		// Composition: `return callee(...)` where the callee's RETURN TYPE already carries a refinement
		// that entails this one is satisfied by the callee's contract (its return value is checked/proven
		// against that refinement on every exit). This is the common forward/wrap pattern — without it a
		// function that returns another refinement-typed function's result paid a redundant runtime check.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.exprRefinementSchemeEntails(n.Value, pred) {
			a.recordProofCat(n.Pos(), "the returned value", pred.Name, ProofProvenContract, ProofCatReturnRefinement)
			continue
		}
		// Refutation gate: the linear prover may refute a return-refinement obligation that the
		// const-eval tier missed (e.g. an affine expression whose bounded range excludes the law).
		// Mirror the ensure/where/requires refutation gate: a PROVABLY false postcondition is a hard
		// error independent of -strict; merely-unprovable stays a lint.
		a.currentProofCategory = ProofCatReturnRefinement
		if a.returnRefinementPredRefuted(n.Value, lawDecl, pred) {
			a.recordProofCat(n.Pos(), "the returned value", pred.Name, ProofRefuted, ProofCatReturnRefinement)
			a.errorf(n.Pos(), "return refinement %q is provably violated: the returned value does not satisfy it", pred.Name)
			continue
		}
		a.currentProofCategory = ProofCatReturnRefinement
		a.recordProof(n.Pos(), "the returned value", pred.Name, ProofRuntime)
		a.proofLint(n.Pos(), "refinement %q on the return of %q could not be proven statically; return a provable value or accept the runtime check%s", pred.Name, a.currentFuncDecl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
		if isBuiltinModularLawName(pred.Name) || !a.isSideEffectFreeRefinementArg(n.Value) {
			continue
		}
		check := &ast.CallExpr{
			Position: pred.Position,
			Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
			Args:     append([]ast.Expr{n.Value}, pred.Args...),
		}
		a.analyzeExpr(check)
		checks = append(checks, check)
	}
	if len(checks) != 0 && a.returnRefinementChecks != nil {
		a.returnRefinementChecks[n] = checks
	}
}

// returnRefinementPredRefuted reports whether a return-position refinement predicate is PROVABLY
// FALSE for the given return value, using the linear prover. Returns true only when the prover
// verdict is requiresRefuted — the negation of the predicate is entailed by the value's bounds.
// Returns false for unknown or proven (the caller handles those). Sound: only a definite
// refutation verdict escalates; an unknown verdict stays a lint (the existing fallback path).
func (a *Analyzer) returnRefinementPredRefuted(value ast.Expr, lawDecl *ast.FuncDecl, pred ast.RefinementPredExpr) bool {
	if a == nil || lawDecl == nil || len(lawDecl.Params) == 0 {
		return false
	}
	body, ok := a.lawBodyExpr(lawDecl)
	if !ok || body == nil {
		return false
	}
	// Build subst: law subject param → return value; law value params → predicate args.
	subst := map[string]ast.Expr{lawDecl.Params[0].Name: value}
	for i, arg := range pred.Args {
		if i+1 >= len(lawDecl.Params) {
			return false
		}
		subst[lawDecl.Params[i+1].Name] = arg
	}
	return a.proveRequiresClause(body, subst) == requiresRefuted
}

// dischargeEnsuresRefinements discharges `ensures <param> is Law` postconditions (docs/85 brick 2,
// half B) at each return: the parameter must satisfy the law at function exit, so the caller's
// gained fact (half A) is backed. This is the STATIC half — prove (flow/factset/const), refute
// (compile error), or report+warn/-strict. The RUNTIME fallback is emitted by the backend
// (emitRefinementPostconditionChecks) uniformly on every exit path INCLUDING fall-through, so a
// void function that never writes an explicit `return` is still checked — which keeps the call-site
// gain sound. Static proof and runtime check are a sound pair (debug verifies what release assumes).
func (a *Analyzer) dischargeEnsuresRefinements(n *ast.ReturnStmt) {
	if a == nil || n == nil || a.currentFuncType == nil || a.currentFuncDecl == nil {
		return
	}
	// All proof records in this function are contract-ensures obligations (elision telemetry).
	defer func(prev ProofObligationCategory) { a.currentProofCategory = prev }(a.currentProofCategory)
	a.currentProofCategory = ProofCatContractEnsures
	for _, re := range a.currentFuncType.RefinementEnsures {
		if re.ParamIndex < 0 || re.ParamIndex >= len(a.currentFuncDecl.Params) {
			continue
		}
		paramName := a.currentFuncDecl.Params[re.ParamIndex].Name
		lawDecl, _, ok := a.lookupLaw(re.LawName)
		if !ok {
			continue
		}
		subject := &ast.Ident{Position: n.Pos(), Name: paramName}
		pred := ast.RefinementPredExpr{Position: re.Position, Name: re.LawName, Args: re.Args}
		valueName := "parameter \"" + paramName + "\""
		if a.tryDischargeRefinementStatically(subject, valueName, pred, lawDecl, n.Pos()) {
			continue
		}
		a.recordProof(n.Pos(), valueName, re.LawName, ProofRuntime)
		a.proofLint(n.Pos(), "postcondition %q on parameter %q of %q could not be proven statically; it is checked at runtime (debug) — make it provable or accept the runtime check%s", re.LawName, paramName, a.currentFuncDecl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
	}
}

// dischargeEnsureBooleans statically discharges the general-boolean `ensure <bool>`
// postconditions (FuncDecl.EnsureValues) at each return: substitute `result` with the
// returned expression and prove the clause via the discharge ladder (SMT, assuming the
// function's `requires` as hypotheses). The backend still emits the debug runtime check
// uniformly, so a proven clause is a sound pair (debug verifies what release assumes);
// the static half upgrades a contract from "checked" to "proven". Under `-strict` an
// ensure clause that cannot be proven at a return is a hard error (the Dafny-like mode);
// without `-strict` it stays a silent runtime check (no warning noise). `old(...)` and
// locals in the clause/return are free SMT vars (sound: fewer facts only declines).
// dischargeEnsureBooleansAtVoidExit discharges the boolean `ensure` clauses of a void / fall-through
// function at its synthetic exit (a value-less `return` or running off the end of the body), mirroring
// the explicit-return path (dischargeEnsureBooleans). Without it, a postcondition over a mutated ref
// param — `ensure p >= old(p)` on a body that does `p -= 1` — was never checked under -strict and
// silently relied on the debug runtime check, so a FALSE postcondition slipped through static checking.
//
// Clauses that reference `result` are skipped: `result` has no meaning at a void exit. The discharge is
// SOUND: `old(p)` lowers to a distinct entry symbol, so an undischargeable clause is reported, never
// falsely proven. Like the explicit-return path, a mutated-param `old()` postcondition the prover cannot
// relate to the exit value is reported under -strict (drop -strict / use -permissive for the runtime check).
func (a *Analyzer) dischargeEnsureBooleansAtVoidExit(pos lexer.Pos) {
	if a == nil || a.currentFuncDecl == nil {
		return
	}
	// All proof records in this function are contract-ensures obligations (elision telemetry).
	defer func(prev ProofObligationCategory) { a.currentProofCategory = prev }(a.currentProofCategory)
	a.currentProofCategory = ProofCatContractEnsures
	for i, clause := range a.currentFuncDecl.EnsureValues {
		if clause == nil || exprReferencesResult(clause) {
			continue
		}
		if proofAt(a.currentFuncDecl.EnsureProofs, i) != nil {
			continue
		}
		// Refutation gate (always on): a void-exit postcondition over params PROVABLY FALSE here is a
		// hard error, like the refuted-`requires`/`where` gate. No `result` subst exists at a void exit
		// (result-referencing clauses are already skipped above). Only a provably-false verdict escalates.
		if a.ensureClauseRefuted(clause, nil) {
			a.reportRefutedEnsure(pos, clause, nil)
			continue
		}
		// The remaining ladder and its unprovable→runtime-fallback handling stay -strict-gated.
		if !a.enforceStrictProofs {
			continue
		}
		clause = normalizeBoolLiteralEnsure(clause)
		// Written-field fast-path: `ensure self.f == E` where the body writes `self.f <- V` and the
		// recorded V proves the clause (V == 0 for `self.f == 0`; or both `self.a` and `self.b` trace
		// to the same value expr for `self.a == self.b`). The fact is dropped at every mutation/borrow/
		// mutating-callee of the root, so a hit means the last write still stands. The SMT lane fails
		// these because two loads of a reference field are distinct opaque terms (the SetupRegions gap).
		if a.ensureHoldsByWrittenField(clause) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "written field", ProofProvenSMT)
			continue
		}
		// WP transport relates the param's EXIT value to `old(p)` (its entry value), proving e.g.
		// `ensure p >= old(p)` when the body keeps/raises p. The trySMTProveRequires fallback models
		// `old(p)` as an unconstrained fresh symbol — sound but always declining an old() postcondition.
		if a.tryProveVoidEnsureByWP(clause) {
			a.recordProof(pos, "ensure "+a.currentFuncDecl.Name, "wp", ProofProvenSMT)
			continue
		}
		if proven, counterexample := a.trySMTProveRequires(clause, nil); !proven {
			diag := a.buildEnsureFailureDiagnostic(clause, nil, counterexample)
			a.errorf(pos, "%s", diag.FormatEnsure(a.currentFuncDecl.Name))
		}
	}
}

// exprReferencesResult reports whether an expression reads the contract `result` binding.
func exprReferencesResult(expr ast.Expr) bool {
	return smtFactDeps(expr)["result"]
}

// normalizeBoolLiteralEnsure rewrites a postcondition phrased against a boolean literal to the bare
// predicate the discharge ladder already proves: `X == true` / `X != false` become `X`, and
// `X == false` / `X != true` become `not X` (either operand may be the literal). Without this, a
// no-overflow law written as `ensure result == true` over a `return base+size >= base` fails to
// discharge even under -smt, while the identical bare `ensure result` (and the integer phrasing
// `ensure result >= base`) prove — the SMT lane models `==` between a Bool term and a bool literal as
// an opaque equality rather than the predicate itself. Folding the literal away routes the wrapped form
// through the same path. SOUND: `X == true` is logically `X` and `X == false` is `not X`; the rewrite is
// a pure logical identity, asserting nothing extra, so an unprovable `X` still declines.
func normalizeBoolLiteralEnsure(clause ast.Expr) ast.Expr {
	bin, ok := stripOptimizationParens(clause).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return clause
	}
	if bin.Op != lexer.TOKEN_EQEQ && bin.Op != lexer.TOKEN_BANGEQ {
		return clause
	}
	var lit *ast.BoolLit
	var other ast.Expr
	if l, ok := stripOptimizationParens(bin.Left).(*ast.BoolLit); ok {
		lit, other = l, bin.Right
	} else if r, ok := stripOptimizationParens(bin.Right).(*ast.BoolLit); ok {
		lit, other = r, bin.Left
	} else {
		return clause
	}
	// `== true` and `!= false` keep the predicate; `== false` and `!= true` negate it.
	if (bin.Op == lexer.TOKEN_EQEQ) == lit.Value {
		return other
	}
	return &ast.UnaryExpr{Position: bin.Pos(), Op: lexer.TOKEN_NOT, Operand: other}
}

// ensureHoldsByReturnReflexivity reports whether the postcondition `clause` is `result == E` (in
// either operand order) where `E` is structurally identical to the returned expression `retVal` and
// is a pure place read. Such a clause holds by definition: at the return point `result` is exactly the
// value of `retVal`, and re-reading the same side-effect-free places yields the same value. Sound
// because exprsSyntacticallyEqual admits only ident/field/paren projections — never calls or arithmetic.
func ensureHoldsByReturnReflexivity(clause ast.Expr, retVal ast.Expr) bool {
	if retVal == nil {
		return false
	}
	bin, ok := clause.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_EQEQ {
		return false
	}
	leftIsResult := isResultIdent(bin.Left)
	rightIsResult := isResultIdent(bin.Right)
	if leftIsResult == rightIsResult {
		return false // need exactly one side to be `result`
	}
	other := bin.Right
	if rightIsResult {
		other = bin.Left
	}
	return exprsSyntacticallyEqual(other, retVal)
}

// ensureHoldsByWrittenField discharges a void-exit postcondition `LHS == RHS` by resolving each
// field-place operand (`self.f`) to the last value the body wrote to it (recorded in writtenField,
// dropped at every mutation/borrow/mutating-callee of the root). It proves two shapes:
//
//	ensure self.f == 0                       — self.f resolves to 0, the other side is the constant 0
//	ensure self.a == self.b                  — both resolve to the SAME value expr
//
// A side with no field fact resolves to itself, so `self.f == 0` (RHS already constant) and a
// field-vs-field equality both work. Discharge is then either constant-equality or syntactic equality
// of the resolved value exprs — never arithmetic or calls, so a stale or unrelated write can't falsely
// prove. Returns false unless at least one operand was actually resolved from a written-field fact
// (otherwise this adds nothing the SMT lane didn't already try).
func (a *Analyzer) ensureHoldsByWrittenField(clause ast.Expr) bool {
	bin, ok := clause.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_EQEQ {
		return false
	}
	left, leftResolved := a.resolveWrittenFieldOperand(bin.Left)
	right, rightResolved := a.resolveWrittenFieldOperand(bin.Right)
	if !leftResolved && !rightResolved {
		return false
	}
	if left == nil || right == nil {
		return false
	}
	if lc, ok := a.evalConstExpr(left); ok {
		if rc, ok := a.evalConstExpr(right); ok {
			return constScalarsEqual(lc, rc)
		}
		return false
	}
	// Non-constant values (e.g. both fields written `<- self.total_flexible_size`) prove only by
	// syntactic equality of the pure place reads — exprsSyntacticallyEqual admits only ident/field
	// projections, never calls or arithmetic, so this stays sound.
	return exprsSyntacticallyEqual(left, right)
}

// constScalarsEqual reports whether two constant values are equal in a SCALAR kind (int/bool/float/
// string). Aggregate kinds (tuple/list/record/optional) decline — equality of those would need a
// deep, kind-aware comparison this fast-path does not attempt, so it falls to the runtime check.
func constScalarsEqual(x, y ConstValue) bool {
	if x.Kind != y.Kind {
		return false
	}
	switch x.Kind {
	case ConstInt:
		return x.Int == y.Int
	case ConstBool:
		return x.Bool == y.Bool
	case ConstFloat:
		return x.Float == y.Float
	case ConstString:
		return x.String == y.String
	default:
		return false
	}
}

// resolveWrittenFieldOperand returns the last-written value of a field-place operand (`self.f` →
// its recorded RHS) and ok=true when a live written-field fact was used; otherwise it returns the
// operand unchanged with ok=false (a literal or unresolved place is its own value).
func (a *Analyzer) resolveWrittenFieldOperand(expr ast.Expr) (ast.Expr, bool) {
	field, ok := unwrapParen(expr).(*ast.FieldExpr)
	if !ok || field == nil {
		return expr, false
	}
	key := smtProjectionName(field)
	if !isPureProjectionKey(key) {
		return expr, false
	}
	if v, ok := a.lookupWrittenField(key); ok {
		return v, true
	}
	return expr, false
}

func isResultIdent(e ast.Expr) bool {
	if p, ok := e.(*ast.ParenExpr); ok {
		return isResultIdent(p.Inner)
	}
	id, ok := e.(*ast.Ident)
	return ok && id != nil && id.Name == "result"
}

func (a *Analyzer) tryProveEnsureByDisjunctiveResultRange(clause ast.Expr, ret ast.Expr) bool {
	subject, constants, ok := resultEqualityDisjunction(a, clause)
	if !ok || !isResultIdent(subject) || len(constants) == 0 {
		return false
	}
	min, max := constants[0], constants[0]
	seen := map[int64]bool{}
	for _, c := range constants {
		seen[c] = true
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if int64(len(seen)) != max-min+1 {
		return false
	}
	if typ := a.exprTypes[ret]; typ != nil {
		if _, _, ok := BitIntInfo(typ); !ok {
			return false
		}
	}
	if id, ok := stripOptimizationParens(ret).(*ast.Ident); ok && id != nil {
		if r, found := a.lookupRangeFact(id.Name); found && r.loKnown && r.hiKnown && r.lo >= min && r.hi <= max {
			return true
		}
		if c, found := a.writtenConstInt(id.Name); found && c >= min && c <= max {
			return true
		}
	}
	if c, ok := a.constIntValue(ret); ok {
		return c >= min && c <= max
	}
	return false
}

func resultEqualityDisjunction(a *Analyzer, expr ast.Expr) (ast.Expr, []int64, bool) {
	expr = stripOptimizationParens(expr)
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin != nil && bin.Op == lexer.TOKEN_OR {
		leftSubject, leftConsts, ok := resultEqualityDisjunction(a, bin.Left)
		if !ok {
			return nil, nil, false
		}
		rightSubject, rightConsts, ok := resultEqualityDisjunction(a, bin.Right)
		if !ok || !exprsSyntacticallyEqual(leftSubject, rightSubject) {
			return nil, nil, false
		}
		return leftSubject, append(leftConsts, rightConsts...), true
	}
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_EQEQ {
		return nil, nil, false
	}
	if isResultIdent(bin.Left) {
		if c, ok := a.constIntValue(bin.Right); ok {
			return bin.Left, []int64{c}, true
		}
	}
	if isResultIdent(bin.Right) {
		if c, ok := a.constIntValue(bin.Left); ok {
			return bin.Right, []int64{c}, true
		}
	}
	return nil, nil, false
}

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
	subst := map[string]ast.Expr{"result": n.Value}
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
		if a.ensureClauseRefuted(clause, subst) {
			a.reportRefutedEnsure(n.Pos(), clause, subst)
			continue
		}
		// The remaining proving ladder and its unprovable→runtime-fallback lint stay -strict-gated.
		if !a.enforceStrictProofs {
			continue
		}
		clause = normalizeBoolLiteralEnsure(clause)
		// Reflexivity fast-path: `ensure result == E` where the body is `return E` and E is a pure
		// place read (ident / field projection) holds definitionally — result IS the value of E at this
		// same side-effect-free point. The SMT lane otherwise fails these because two loads of a
		// reference field (`self.base`) are modeled as distinct opaque terms (shadPS4 memory-06 finding).
		if ensureHoldsByReturnReflexivity(clause, n.Value) {
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
		if a.tryProveEnsureByDisjunctiveResultRange(clause, n.Value) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "range disjunction", ProofProvenSMT)
			continue
		}
		proven, counterexample := a.trySMTProveRequires(clause, subst)
		if proven {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "ensure", ProofProvenSMT)
			continue
		}
		if call, ok := a.proofCallExpr(n.Value); ok {
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
		if a.tryProveEnsureByWP(clause, n) {
			a.recordProof(n.Pos(), "ensure "+a.currentFuncDecl.Name, "wp", ProofProvenSMT)
			continue
		}
		if a.trySMTProveEnsureFromPureReturnPaths(clause) {
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
	return a.proveRequiresClause(clause, subst) == requiresRefuted
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
	if a.openSMT() == nil {
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

// smtResultNullnessHypothesis emits `(= isnull_result true)` when the substitution binds `result` to a
// literal null return value, mirroring exactly the predicate key the null-compare lowering produces for
// `result == null` (`isnull_` + smtProjectionName(result) == "isnull_result"). Returns "" otherwise —
// in particular it asserts NOTHING for a non-null return, so an unsound `ensure result == null` over a
// non-null return path is still rejected. Kept as a self-contained helper for clean merges.
func (a *Analyzer) smtResultNullnessHypothesis(tr *smtTranslator, subst map[string]ast.Expr) string {
	if a == nil || tr == nil || subst == nil {
		return ""
	}
	ret, ok := subst["result"]
	if !ok || ret == nil {
		return ""
	}
	if _, isNull := stripOptimizationParens(ret).(*ast.NullLit); !isNull {
		return ""
	}
	pred := "isnull_result"
	tr.boolDecls[pred] = true
	return "(assert (= " + pred + " true))\n"
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
			if !a.exprIsPointerLike(sym.Type) || !a.exprIsPointerLike(a.exprTypes[cast.Operand]) {
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
	if a == nil || a.currentFuncDecl == nil || clause == nil {
		return false
	}
	body, ok := pureReturnExpr(a.currentFuncDecl)
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

// validateRefinementPreds checks that every predicate in a refinement type `Base is P, …` names a
// law and that the law's subject (first parameter) accepts the base type. Representation is the
// base type (erased); this only validates the predicates so a malformed refinement is a clear
// error rather than a silent no-op. Discharge of the predicate at a binding boundary is separate.
func (a *Analyzer) validateRefinementPreds(n *ast.RefinementTypeExpr, base Type) {
	if a == nil || n == nil {
		return
	}
	for _, pred := range n.Preds {
		decl, ft, ok := a.lookupLaw(pred.Name)
		if !ok {
			// During eager resolution the law may simply not be registered yet (a struct field type is
			// resolved before law decls). Defer the whole refinement's validation to the post-law pass
			// rather than falsely reporting "not a law"; only the final pass errors.
			if !a.finalizingRefinements {
				a.deferredAliasRefinements = append(a.deferredAliasRefinements, deferredAliasRefinement{
					rt:        n,
					base:      base,
					namespace: a.currentNamespace,
					usings:    append([]string(nil), a.currentUsings...),
				})
				return
			}
			a.errorf(pred.Position, "refinement predicate %q is not a law", pred.Name)
			continue
		}
		if ft == nil || len(ft.Params) == 0 {
			continue // signature not built yet, or a subjectless law (reported at its declaration)
		}
		// A generic law (subject is a type parameter) accepts any base via inference. A concrete
		// subject must accept the base type.
		if len(decl.TypeParams) == 0 && base != nil {
			subject := ft.Params[0]
			if !AssignableTo(base, subject) && !AssignableTo(subject, base) {
				a.errorf(pred.Position, "refinement %q expects a subject of type %s, but the refined type is %s", pred.Name, typeString(subject), typeString(base))
			}
		}
	}
}

// lookupLaw resolves a name to a law declaration (and its function type if already built),
// following alias chains. It consults the current scope then the global scope, since refinement
// types are resolved during signature building when the active scope may not yet chain to globals.
// The FuncType may be nil if the law's signature is not built yet; callers that need the subject
// type must tolerate that.
func (a *Analyzer) lookupLaw(name string) (*ast.FuncDecl, *FuncType, bool) {
	scopes := []*Scope{a.currentScope, a.globalScope}
	for _, scope := range scopes {
		if scope == nil {
			continue
		}
		sym, ok := scope.Lookup(name)
		if !ok {
			continue
		}
		for sym != nil {
			if decl, isDecl := sym.Node.(*ast.FuncDecl); isDecl && decl != nil && decl.IsLaw {
				ft, _ := sym.Type.(*FuncType)
				return decl, ft, true
			}
			sym = sym.AliasOf
		}
	}
	// Built-in modular-arithmetic predicates (Aligned/Divides/Fits/MaskedZero) have no user `law`
	// decl: synthesize one on demand so every law consumer (discharge, flow facts, validation) treats
	// them uniformly. The FuncType is left nil — refinement discharge does not need it, and the
	// builtin's subject accepts any integer width.
	if decl := a.lookupBuiltinModularLaw(name); decl != nil {
		return decl, nil, true
	}
	return nil, nil, false
}

// resolveBareLawIsTarget returns the law name when `target` is a bare reference (an identifier or
// plain named type, no bracket/value args) to a `law` declaration. Parametric targets
// (`Bounded[0..500]`) and non-laws return false.
func (a *Analyzer) resolveBareLawIsTarget(target ast.Expr) (string, bool) {
	name, ok := bareTargetName(target)
	if !ok || name == "" {
		return "", false
	}
	if a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return "", false
	}
	for sym != nil {
		if decl, ok := sym.Node.(*ast.FuncDecl); ok && decl != nil && decl.IsLaw {
			return name, true
		}
		sym = sym.AliasOf
	}
	return "", false
}

// gatherLawIsSMTFact records the LAW BODY (instantiated for the subject expression) as an assumable
// SMT flow fact for an `if EXPR is Law:` branch condition (docs/85: refinement flow-narrowing). Inside
// the THEN branch the predicate holds, so the body — e.g. `self >= 0` for `law NonNeg(self:i64) = self
// >= 0`, with `self` replaced by EXPR — becomes a hypothesis available to discharge a downstream
// obligation such as `assert EXPR >= 0`. In the ELSE branch the NEGATION `not(body)` is recorded ONLY
// when the law is decidable/total (a quantifier-free, clonable boolean body): a partial/quantified law
// has no usable runtime negation, so the else records nothing (always sound — fewer facts only declines
// a proof, never fabricates one). This complements gatherLawIsPredFact (which tracks the `EXPR is Law`
// fact itself for a later `is`-obligation) by feeding the comparison/arithmetic prover.
//
// SOUNDNESS: the substituted body is the law's own definition evaluated at EXPR, recorded only in the
// branch where the condition's truth value matches. smtFactDeps over the substituted body names EXPR's
// roots, so the fact is invalidated automatically when EXPR is mutated (the standard fact-drop path).
func (a *Analyzer) gatherLawIsSMTFact(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if a == nil || scope == nil || n == nil || n.Left == nil {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	lawDecl, _, ok := a.lookupLaw(lawName)
	if !ok || lawDecl == nil || len(lawDecl.Params) == 0 {
		return
	}
	body, ok := a.lawBodyExpr(lawDecl)
	if !ok || body == nil {
		return
	}
	// A quantified body is spec-only (no executable/negatable boolean) — never record it as a flow
	// hypothesis here; the dedicated refinement-discharge tiers handle quantified laws.
	if a.lawBodyContainsQuantifier(lawDecl) {
		return
	}
	// Build the subject/param substitution: `self` -> EXPR, each law value-param -> the bracket arg.
	subst := map[string]ast.Expr{lawDecl.Params[0].Name: n.Left}
	for i, arg := range lawArgs {
		if i+1 >= len(lawDecl.Params) || arg == nil {
			return // arity mismatch or a missing arg ⇒ cannot instantiate soundly
		}
		subst[lawDecl.Params[i+1].Name] = arg
	}
	instantiated := ast.CloneExprSubst(body, subst)
	if instantiated == nil {
		return // body or a substituted arg fell outside the clonable fragment ⇒ decline (sound)
	}
	if truthy {
		a.recordSMTAssertFactInScope(scope, instantiated)
		return
	}
	// ELSE branch: the law is decidable (quantifier-free, clonable), so its negation is a sound fact.
	a.recordSMTAssertFactInScope(scope, &ast.UnaryExpr{Position: n.Pos(), Op: lexer.TOKEN_NOT, Operand: instantiated})
}

// bareTargetName extracts a plain name from an `is` target that is a bare identifier or named
// type (unwrapping parens). Anything compound (generic args, dotted, value args) returns false.
func bareTargetName(target ast.Expr) (string, bool) {
	switch n := target.(type) {
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return bareTargetName(n.Inner)
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.TypeExprExpr:
		if n == nil {
			return "", false
		}
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || indexOfByte(named.Name, '.') >= 0 {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}
