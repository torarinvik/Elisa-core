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
	rt, ok := n.Type.(*ast.RefinementTypeExpr)
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
		// Not statically proven: fall back to a runtime check AND tell the user — a static guarantee
		// was not achieved here (docs/85: the fallback must be KNOWN). Warning by default; hard error
		// under -strict (prove-it-or-fail, the Dafny-like mode).
		a.recordProof(n.Pos(), "\""+n.Name+"\"", pred.Name, ProofRuntime)
		a.proofLint(n.Pos(), "refinement %q on %q could not be proven statically; it is checked at runtime (debug) — make the value provable, or accept the runtime check%s", pred.Name, n.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
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
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		return
	}
	var checks []*ast.CallExpr
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			break
		}
		rt, ok := param.Type.(*ast.RefinementTypeExpr)
		if !ok || rt == nil {
			continue
		}
		for _, pred := range rt.Preds {
			lawDecl, _, ok := a.lookupLaw(pred.Name)
			if !ok {
				continue
			}
			name := "argument " + itoaParam(i+1)
			// Cross-function boundary: a dependent fact's names belong to the callee's scope, so they
			// must not discharge a caller-site argument obligation (allowDependentFacts=false).
			if a.tryDischargeRefinementStaticallyOpt(args[i], name, pred, lawDecl, call.Pos(), false) {
				continue
			}
			a.recordProof(call.Pos(), name, pred.Name, ProofRuntime)
			a.proofLint(call.Pos(), "refinement %q on %s of %q could not be proven statically; pass a provable value or accept the runtime check%s", pred.Name, name, decl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
			// Fall back to a runtime debug-check at the call site — but only for a side-effect-free
			// argument, since the predicate re-evaluates it. An impure arg keeps the warning only (a
			// double evaluation would change behavior), so the runtime tier stays sound.
			if !a.isSideEffectFreeRefinementArg(args[i]) {
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
// inherits the guarantee. Entailment is recognized for an IDENTICAL predicate (same law, same constant
// args) and, for the interval law family, a TIGHTER bound (callee `Bounded[clo,chi]` ⊆ required
// `Bounded[rlo,rhi]`). Sound: the callee's return refinement is enforced on its every exit (statically
// or by a debug runtime check), so its result already satisfies `pred`.
func (a *Analyzer) returnCallRefinementEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	call, ok := stripOptimizationParens(value).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return false
	}
	rt, ok := decl.ReturnType.(*ast.RefinementTypeExpr)
	if !ok || rt == nil {
		return false
	}
	for _, cp := range rt.Preds {
		if a.refinementPredEntails(cp, pred) {
			return true
		}
	}
	return false
}

// refinementPredEntails reports whether predicate `have` guarantees `want` — the same law applied to
// identical constant arguments (the trivial, overwhelmingly common forward/wrap case). Returning a value
// already refined `Bounded[0,255]` satisfies a required `Bounded[0,255]`. Conservative: a non-constant
// or differing arg declines (no false entailment).
func (a *Analyzer) refinementPredEntails(have, want ast.RefinementPredExpr) bool {
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

func (a *Analyzer) dischargeReturnRefinements(n *ast.ReturnStmt) {
	if a == nil || n == nil || n.Value == nil || a.currentFuncDecl == nil {
		return
	}
	rt, ok := a.currentFuncDecl.ReturnType.(*ast.RefinementTypeExpr)
	if !ok || rt == nil {
		return
	}
	var checks []*ast.CallExpr
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue
		}
		if a.tryDischargeRefinementStatically(n.Value, "the returned value", pred, lawDecl, n.Pos()) {
			continue
		}
		// Composition: `return callee(...)` where the callee's RETURN TYPE already carries a refinement
		// that entails this one is satisfied by the callee's contract (its return value is checked/proven
		// against that refinement on every exit). This is the common forward/wrap pattern — without it a
		// function that returns another refinement-typed function's result paid a redundant runtime check.
		if a.returnCallRefinementEntails(n.Value, pred) {
			a.recordProof(n.Pos(), "the returned value", pred.Name, ProofProvenContract)
			continue
		}
		a.recordProof(n.Pos(), "the returned value", pred.Name, ProofRuntime)
		a.proofLint(n.Pos(), "refinement %q on the return of %q could not be proven statically; return a provable value or accept the runtime check%s", pred.Name, a.currentFuncDecl.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
		if !a.isSideEffectFreeRefinementArg(n.Value) {
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
	if a == nil || a.currentFuncDecl == nil || !a.enforceStrictProofs {
		return
	}
	for _, clause := range a.currentFuncDecl.EnsureValues {
		if clause == nil || exprReferencesResult(clause) {
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
			a.errorf(pos, "ensure postcondition of %q could not be proven statically at the function exit; make it provable, or drop -strict / use -permissive to accept the debug runtime check%s", a.currentFuncDecl.Name, a.counterexampleSuffix(counterexample))
		}
	}
}

// exprReferencesResult reports whether an expression reads the contract `result` binding.
func exprReferencesResult(expr ast.Expr) bool {
	return smtFactDeps(expr)["result"]
}

func (a *Analyzer) dischargeEnsureBooleans(n *ast.ReturnStmt) {
	if a == nil || n == nil || n.Value == nil || a.currentFuncDecl == nil {
		return
	}
	if len(a.currentFuncDecl.EnsureValues) == 0 || !a.enforceStrictProofs {
		return
	}
	subst := map[string]ast.Expr{"result": n.Value}
	for _, clause := range a.currentFuncDecl.EnsureValues {
		if clause == nil {
			continue
		}
		proven, counterexample := a.trySMTProveRequires(clause, subst)
		if proven {
			continue
		}
		if call, ok := a.proofCallExpr(n.Value); ok {
			if a.tryProveEnsureByReturnCallRange(clause, call) {
				continue
			}
			if a.trySMTProveEnsureFromReturnCall(clause, call) {
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
		a.errorf(n.Pos(), "ensure postcondition of %q could not be proven statically at this return; make it provable (e.g. give params refinement bounds), pass -nosmt off, or drop -strict to accept the debug runtime check%s", a.currentFuncDecl.Name, a.counterexampleSuffix(counterexample))
	}
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
