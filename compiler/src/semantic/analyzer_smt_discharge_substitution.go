package semantic

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"elisacore/src/ast"
)

func (a *Analyzer) trySMTProveEnsureFromReturnCall(clause ast.Expr, call *ast.CallExpr) bool {
	if !a.smtEnabled || a.smtUnavailable || clause == nil || call == nil {
		return false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil || len(decl.EnsureValues) == 0 {
		return false
	}
	tr := a.newSMTTranslator(nil)
	retSym := "@return_call_result"
	retTerm := smtVar(retSym)
	// The result symbol must carry the callee's result SORT. A bool-returning callee's ensures use
	// `result` as a Bool (`x < 0 implies not result`); declaring it Int made every such query
	// ill-sorted, so bool callee contracts never reached a call site.
	if IsBoolType(a.exprType(call)) {
		retTerm = smtBoolVar(retSym)
		tr.boolDecls[retTerm] = true
	} else if isDatatypeValuedType(a.exprType(call)) {
		// An aggregate result has no scalar term: bind it to a projection token so `result.f` in the
		// caller's obligation and in the callee's ensures both re-root to `@return_call_result.f`.
		retTerm = dtToken(retSym)
	} else if _, view := stripRefForBounds(a.exprType(call)).(*SViewType); !view {
		tr.decls[retSym] = true
	}
	callerEnv := map[string]string{"result": retTerm}
	// A view result is its bytes, start and length: fresh parts shared by the caller's
	// obligation and the callee's ensures.
	var viewData, viewLo, viewLen string
	if _, view := stripRefForBounds(a.exprType(call)).(*SViewType); view {
		viewData, viewLo, viewLen = "|@return_call_view$data|", "|@return_call_view$lo|", "|@return_call_view$len|"
		tr.auxDecls = append(tr.auxDecls,
			"(declare-const "+viewData+" (Array Int Int))\n",
			"(declare-const "+viewLo+" Int)\n",
			"(declare-const "+viewLen+" Int)\n",
			"(assert (>= "+viewLen+" 0))\n")
		callerEnv = map[string]string{}
		bindSMTIndexedSViewArgTerms(callerEnv, "result", viewData, viewLo, viewLen)
		callerEnv["result.len"] = viewLen
	}
	obligation, ok := tr.boolTerm(clause, callerEnv)
	if !ok {
		return false
	}
	subst := map[string]ast.Expr{}
	args := proofCallArgs(call)
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	// Call-argument binding can use callee-specific frame and formal metadata. In
	// particular, an indexed `darray[sview]` argument is only projected as a
	// stable byte window when the direct callee is known to be write-free.
	tr.bindingCallee = decl
	calleeEnv, ok := a.smtEnvForCallSubst(tr, subst)
	if !ok {
		return false
	}
	if viewData != "" {
		bindSMTIndexedSViewArgTerms(calleeEnv, "result", viewData, viewLo, viewLen)
		calleeEnv["result.len"] = viewLen
	} else {
		calleeEnv["result"] = retTerm
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.boolTerm(req, calleeEnv)
		if !ok {
			return false
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	var calleeHyps strings.Builder
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if h, ok := tr.boolTerm(ensure, calleeEnv); ok {
			if guard == "true" {
				calleeHyps.WriteString("(assert " + h + ")\n")
			} else {
				calleeHyps.WriteString("(assert (=> " + guard + " " + h + "))\n")
			}
		}
	}
	if calleeHyps.Len() == 0 {
		return false
	}
	// The called function's poststates (`calleeHyps`) are the site-specific extra hypotheses on top of
	// the standard set. Discharge through the central chokepoint (the counterexample is unused here).
	proven, _ := a.smtCheckVC(tr, obligation, calleeHyps.String())
	return proven
}

func (a *Analyzer) smtEnvForSubst(tr *smtTranslator, subst map[string]ast.Expr) (map[string]string, bool) {
	env := map[string]string{}
	// Sorted: binding order names fresh symbols (svarg$N) and decides which binding declines
	// first, so map order made the emitted query set differ run to run.
	for _, name := range slices.Sorted(maps.Keys(subst)) {
		argExpr := subst[name]
		if !a.bindSMTEnvArg(tr, env, name, argExpr, nil, true) {
			return nil, false
		}
	}
	return env, true
}

// isDatatypeValuedType reports whether a type is an enum/struct aggregate value (no scalar SMT term).
// Used to bind datatype substitutions to a projection token rather than a fabricated integer var.
func isDatatypeValuedType(t Type) bool {
	if t == nil {
		return false
	}
	switch stripRefForBounds(t).(type) {
	case *EnumType, *StructType:
		return true
	default:
		return false
	}
}

func (a *Analyzer) smtEnvForCallSubst(tr *smtTranslator, subst map[string]ast.Expr) (map[string]string, bool) {
	return a.smtEnvForCallSubstEnv(tr, subst, nil)
}

func (a *Analyzer) smtEnvForCallSubstEnv(tr *smtTranslator, subst map[string]ast.Expr, argEnv map[string]string) (map[string]string, bool) {
	env := map[string]string{}
	// Sorted: binding order names fresh symbols (svarg$N) and decides which binding declines
	// first, so map order made the emitted query set differ run to run.
	for _, name := range slices.Sorted(maps.Keys(subst)) {
		argExpr := subst[name]
		if !a.bindSMTEnvArg(tr, env, name, argExpr, argEnv, false) {
			return nil, false
		}
	}
	return env, true
}

func (a *Analyzer) bindSMTEnvArg(tr *smtTranslator, env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string, requireBinding bool) bool {
	if constantStringResult(argExpr, 0) {
		if tr.stringBindings == nil {
			tr.stringBindings = map[string]smtConstantStringBinding{}
		}
		token := fmt.Sprintf("string_binding_%d", len(tr.stringBindings))
		capturedEnv := map[string]string{}
		for key, value := range argEnv {
			capturedEnv[key] = value
		}
		tr.stringBindings[token] = smtConstantStringBinding{expr: argExpr, env: capturedEnv}
		env["\x00string:"+name] = token
		// Constant-string comparison and view-header reads use separate
		// bindings. Preserve both when a literal is passed as an sview.
		if _, literal := stripOptimizationParens(argExpr).(*ast.StringLit); literal {
			if _, view := stripRefForBounds(a.exprType(argExpr)).(*SViewType); view {
				return tr.bindSMTSViewLengthArg(env, name, argExpr, argEnv)
			}
		}
		return true
	}
	// A literal-null substitution (e.g. `result` on a `return null` path) has no scalar SMT term. It is
	// consumed structurally by the null-compare lowering (which keys an `isnull_<name>` predicate), so
	// leave the env entry unbound rather than failing the whole env build.
	if _, ok := stripOptimizationParens(argExpr).(*ast.NullLit); ok {
		return true
	}
	if lit, ok := argExpr.(*ast.BoolLit); ok {
		if lit.Value {
			env[name] = "true"
		} else {
			env[name] = "false"
		}
		return true
	}
	if IsBoolType(a.exprType(argExpr)) {
		bterm, ok := tr.boolTerm(argExpr, argEnv)
		if !ok {
			return false
		}
		env[name] = bterm
		return true
	}
	if t := a.containerArgType(argExpr); t != nil && !tr.isArrayLike(t) {
		if tr.bindContainerArg(env, name, argExpr, argEnv) {
			return true
		}
	}
	if tr.isArrayLike(a.exprType(argExpr)) {
		arr, ok := tr.arrayTermEnv(argExpr, argEnv)
		if !ok {
			return false
		}
		env[name] = arr
		return true
	}
	argType := a.exprType(argExpr)
	if argType == nil {
		// Loop-invariant preservation runs before the loop body has gone
		// through ordinary expression analysis. Recover a plain darray
		// element's type from its container so an indexed `darray[sview]`
		// argument can still reach indexedSViewCallArray in that earlier
		// proof phase. The projection itself remains gated by stable-container
		// and read-only-frame checks below.
		if idx, isIndex := stripOptimizationParens(argExpr).(*ast.IndexExpr); isIndex && idx != nil &&
			idx.Index2 == nil && idx.Fallback == nil && !idx.LegacyElseFallback &&
			idx.AsSpecialize == nil && idx.AsOverlayCall == nil && idx.AsBuiltinDictGet == nil {
			argType = tr.containerElementType(idx.Object)
		}
	}
	if argType == nil {
		if id, isIdent := stripOptimizationParens(argExpr).(*ast.Ident); isIdent && id != nil && a.currentScope != nil {
			if sym, found := a.currentScope.Lookup(id.Name); found && sym != nil {
				argType = sym.Type
			}
		}
	}
	if argType == nil {
		// A merged return value (`a if c else b`) carries no type of its own.
		argType = a.ternaryBranchType(argExpr)
	}
	if _, ok := stripRefForBounds(argType).(*SViewType); ok {
		// Carry the argument's string identity to the callee formal by explicit binding (never by
		// name), so the callee's literal-equality contracts speak about the caller's string.
		id, idOK := tr.stringIDTerm(argExpr, argEnv)
		viewData, viewOffset, viewLength, indexedView := tr.indexedSViewCallTerms(argExpr, argEnv)
		if !indexedView {
			if viewID, bare := stripOptimizationParens(argExpr).(*ast.Ident); bare && viewID != nil {
				viewData, viewOffset, viewLength, indexedView = smtIndexedSViewArgTermsFromEnv(viewID.Name, argEnv)
			}
		}
		if !indexedView {
			switch stripOptimizationParens(argExpr).(type) {
			case *ast.CallExpr, *ast.TernaryExpr:
				// A view produced by a pure call or a conditional is bound by its parts, so the
				// formal reads the same bytes the expression denotes.
				viewData, viewOffset, viewLength, indexedView = tr.sviewContentParts(argExpr, argEnv)
			}
		}
		if indexedView {
			bindSMTIndexedSViewArgTerms(env, name, viewData, viewOffset, viewLength)
		}
		if arr, length, ok := tr.indexedSViewCallArray(argExpr, argEnv); ok {
			env[name] = arr
			env[name+".len"] = length
			if idOK {
				bindSMTStringID(env, name, id)
			}
			return true
		}
		if indexedView {
			// The content array cannot be globally normalized when its index
			// mentions a quantifier binder. Preserve the header and call identity
			// as scoped terms for scalarCallUF instead.
			env[name+".len"] = viewLength
			if idOK {
				bindSMTStringID(env, name, id)
			}
			return true
		}
		// An immutable sview formal can be bound as an SMT byte array only when the enclosing
		// expression context passes immutableSviewArrayParameter's read-only-function gate.
		if arr, ok := tr.arrayTermEnv(argExpr, argEnv); ok {
			env[name] = arr
			if idOK {
				bindSMTStringID(env, name, id)
			}
			return true
		}
		// Contents unavailable: bind the length only (see bindSMTSViewLengthArg).
		if !tr.bindSMTSViewLengthArg(env, name, argExpr, argEnv) {
			return false
		}
		if idOK {
			bindSMTStringID(env, name, id)
		}
		return true
	}
	if _, view := stripRefForBounds(a.exprType(argExpr)).(*ViewType); view {
		if length, ok := tr.fixedArraySliceLengthTerm(argExpr, argEnv); ok {
			tr.bindSMTLengthOnlyTerm(env, name, length)
			return true
		}
	}
	if a.smtEquationStructType(a.exprType(argExpr)) {
		return a.bindSMTStructEnvArg(tr, env, name, argExpr, argEnv, requireBinding)
	}
	if token, ok := tr.stableRecordRootToken(argExpr, argEnv); ok {
		env[name] = token
		return true
	}
	// A datatype value (enum, or a struct not modeled field-wise above) has no scalar SMT term; bind a
	// recoverable projection token so a bool predicate over it canonicalizes to the SAME opaque symbol
	// as the un-substituted occurrence (structural induction). Tokens only key opaque predicate symbols;
	// they never enter a numeric SMT term (the type checker forbids datatype operands in arithmetic).
	if isDatatypeValuedType(a.exprType(argExpr)) {
		if _, _, _, ok := tr.enumConstructor(argExpr); ok {
			// Keep a substituted variant constructor recognizable: its tag is a constant and its payload
			// is its arguments. The token never enters a numeric term.
			token := dtTokenPrefix + "enumctor#" + strconv.Itoa(len(tr.enumBindings))
			tr.enumBindings[token] = smtEnumBinding{expr: argExpr, env: argEnv}
			env[name] = token
			return true
		}
		env[name] = dtToken(smtProjectionName(argExpr))
		return true
	}
	// A pointer-like value (nullable optional / reference, e.g. a `void&?` return on a `result == null`
	// disjunct) has no scalar SMT term either. It too is consumed structurally by the null-compare
	// lowering (keyed by the substituted name, not by an env term), so leave it unbound rather than
	// failing the env build. SOUND: an unbound entry contributes no fact; only the null-compare path
	// (which folds against the literal/flow facts) can conclude anything about it.
	if a.exprIsPointerLike(a.exprType(argExpr)) {
		return true
	}
	// A bound type-param-typed value (the receiver of a protocol-method call in a behavioral-subtyping
	// contract, e.g. `self`↦`c` where `c: C`) has no scalar SMT term, but it must still bind to a stable
	// projection token so an abstract method call `c.count()` keyed by its receiver canonicalizes the
	// same on both the obligation and the caller-fact side (docs P1). SOUND: the token only ever keys an
	// uninterpreted method-result symbol; it never enters a numeric term.
	if _, ok := stripRefForBounds(a.exprType(argExpr)).(*TypeParamType); ok {
		env[name] = dtToken(smtProjectionName(argExpr))
		return true
	}
	// Only early-decline when the type is KNOWN to be non-exact. A substitution expr captured before its
	// body is type-analyzed (e.g. a loop body's `i+1`) has a nil exprTypes entry; falling through to
	// termEnv resolves it via scope, which is what the loop-invariant preserve proof needs.
	if isAnyArrayLike(a.exprType(argExpr)) && !tr.isArrayLike(a.exprType(argExpr)) {
		// A container whose elements the array theory does not model: bind its length only.
		if tr.bindSMTLengthOnlyArg(env, name, argExpr, argEnv) {
			return true
		}
		return !requireBinding
	}
	// A tuple-valued call (`return parse(x)` substituted for `result`) binds to its call-projection
	// token, so `result.f` reads the same symbols as the call's own `parse(x).f` and its verified
	// ensures (analyzer_smt_call_projection.go).
	if call, isCall := stripOptimizationParens(argExpr).(*ast.CallExpr); isCall && call != nil {
		if _, tuple := stripRefForBounds(a.exprType(argExpr)).(*TupleType); tuple {
			if token, ok := tr.callProjectionToken(call, argEnv); ok {
				env[name] = dtToken(token)
				return true
			}
		}
	}
	if t := a.exprType(argExpr); t != nil && !isSMTExactAssignmentType(t) {
		return !requireBinding
	}
	term, ok := tr.termEnv(argExpr, argEnv)
	if !ok {
		return false
	}
	env[name] = term
	return true
}

func (a *Analyzer) bindSMTStructEnvArg(tr *smtTranslator, env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string, requireBinding bool) bool {
	st, ok := stripRefForBounds(a.exprType(argExpr)).(*StructType)
	if !ok || st == nil {
		return !requireBinding
	}
	leaves, ok := tr.scalarStructArgLeaves(argExpr, st, argEnv)
	if !ok {
		return false
	}
	for _, leaf := range leaves {
		env[name+smtFieldSeparator+leaf.path] = leaf.term
	}
	return true
}

// dtTokenPrefix marks an env binding as a DATATYPE-projection token (not an SMT term). Only
// boolPredicateArgToken reads these; arithmetic/comparison lowering never sees a datatype-typed operand
// (the type checker forbids it), so the marker can never leak into a numeric SMT term.
const dtTokenPrefix = "__dt__"

func dtToken(projection string) string { return dtTokenPrefix + projection }

// smtRequiresHypotheses translates the enclosing function's `requires` clauses into SMT assertions
// (non-negated — they are assumed), using the given translator so free variables and arrays share the
// obligation's symbols. A clause outside the fragment is silently skipped (sound: fewer assumptions is
// conservative). Returns the concatenated `(assert …)` lines.
func (a *Analyzer) smtRequiresHypotheses(tr *smtTranslator) string {
	if a.currentFuncDecl == nil {
		return ""
	}
	if a.inClosedWorldProof {
		return "" // docs/99: the enclosing `requires` is an ambient fact, walled out of a scoped proof.
	}
	var b strings.Builder
	for _, req := range a.currentFuncDecl.Requires {
		if req == nil {
			continue
		}
		// SOUNDNESS: a `requires` constrains a parameter's ENTRY value. For an immutable variable that
		// stays true everywhere, so it is asserted directly here. But a clause over a MUTABLE root (a
		// mutable param/local, or a field reachable through a `mutable T&` param) is only valid until that
		// root is mutated — re-asserting it unconditionally after `m <- 0` would prove a false postcondition
		// about the new value. Such clauses are seeded as flow assert-facts at function entry
		// (seedRequiresAsAssertFacts) and ride the standard mutation-invalidation instead, so they are
		// skipped here to avoid the stale unconditional copy.
		if a.requiresReferencesMutableRoot(req) {
			continue
		}
		// A parameter shadowed at the proof point no longer owns its SMT symbol.
		if a.mentionsShadowedParam(req) {
			continue
		}
		tr.requiresHypMode = true
		h, ok := tr.boolTerm(req, nil)
		tr.requiresHypMode = false
		if ok {
			b.WriteString("(assert " + h + ")\n")
		}
	}
	return b.String()
}

// requiresReferencesMutableRoot reports whether a `requires`/contract clause reads any variable whose
// symbol is MUTABLE (a mutable param/local, or — for a field path like `b.v` — a root bound to a
// `mutable T&` param). Such a clause's entry-value guarantee can be invalidated by a body mutation, so
// it is tracked through the invalidation-aware assert-fact channel rather than asserted unconditionally.
func (a *Analyzer) requiresReferencesMutableRoot(clause ast.Expr) bool {
	if a == nil || a.currentScope == nil || clause == nil {
		return false
	}
	for root := range smtFactDeps(clause) {
		if sym, ok := a.currentScope.Lookup(root); ok && symbolRootMutable(sym) {
			return true
		}
	}
	return false
}

// symbolRootMutable reports whether a symbol's value (or the place it points at) can be changed by a
// body mutation. A by-value mutable local/param qualifies (sym.Mutable). So does a MUTABLE-REFERENCE
// parameter (`s: mutable S&`): paramIsMutable leaves sym.Mutable FALSE there (the `mutable` only allows
// rebinding the ref), but the POINTEE is writable — `s.x <- …` / `bump(&s.x)` change what a `requires`
// over that place reads — so its entry-value guarantee is mutable and must ride the invalidation channel.
func symbolRootMutable(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Mutable {
		return true
	}
	if rt, ok := sym.Type.(*RefType); ok && rt != nil && rt.Mutable {
		return true
	}
	return false
}

// seedRequiresAsAssertFacts records every `requires` clause over a MUTABLE root as a flow assert-fact in
// the current (function body) scope, so it is assumed while the root holds its entry value and DROPPED
// by the standard mutation invalidation the moment the root is written — the exact lifetime a precondition
// has. Immutable-root clauses are left to smtRequiresHypotheses (always valid, no invalidation needed).
func (a *Analyzer) seedRequiresAsAssertFacts(fn *ast.FuncDecl) {
	if a == nil || fn == nil || a.currentScope == nil {
		return
	}
	for _, req := range fn.Requires {
		if req != nil && a.requiresReferencesMutableRoot(req) {
			a.recordSMTAssertFact(req)
		}
	}
	// Seed index-bound facts from `requires` clauses so that a precondition like
	// `requires i < xs.count` elides the runtime check at `xs[i]` when both xs and i
	// are immutable params. Mutable-root clauses are also seeded here; the existing
	// mutation-invalidation machinery (invalidateIndexBoundsForContainer / ForAssigned­Target)
	// will drop the fact the moment xs or i is mutated, preserving soundness.
	for _, req := range fn.Requires {
		if req != nil {
			a.applyIndexBoundsFactsForCondition(req, true)
		}
	}
	a.seedSelfStructInvariantsAsAssertFacts(fn)
	a.checkRequiresVacuity(fn)
}

// checkRequiresVacuity flags a function whose `requires` preconditions are PROVABLY contradictory
// (their conjunction is unsatisfiable). Such a function's body assumes its own `requires` as
// hypotheses, so contradictory preconditions let it vacuously "prove" ANY `ensure` postcondition. This
// is sound today only because every call site must itself discharge the precondition (which it never
// can), but it is a latent leak for exported / entry / uncalled functions where no call site exists to
// catch the impossibility — and it is almost always a real authoring bug regardless.
//
// SOUNDNESS OF THE CHECK ITSELF (mirrors the discharge ladder's "only unsat concludes" discipline):
// we ask the solver whether the CONJUNCTION of the `requires` clauses is satisfiable, and flag ONLY
// when it answers `unsat`. `sat` (a witnessing model exists), `unknown` (timeout/incompleteness), a
// clause outside the SMT fragment, or no solver at all all leave the function unflagged — so a
// tight-but-satisfiable precondition is never a false positive. A clause we cannot translate is simply
// dropped from the conjunction (a weaker assertion can only become MORE satisfiable, never less), so a
// partial translation can lose a true contradiction but can never fabricate one.
func (a *Analyzer) checkRequiresVacuity(fn *ast.FuncDecl) {
	if a == nil || !a.smtEnabled || a.smtUnavailable || fn == nil || len(fn.Requires) == 0 {
		return
	}
	tr := a.newSMTTranslator(nil)
	var b strings.Builder
	asserted := 0
	for _, req := range fn.Requires {
		if req == nil {
			continue
		}
		tr.requiresHypMode = true
		h, ok := tr.boolTerm(req, nil)
		tr.requiresHypMode = false
		if !ok {
			continue // outside the SMT fragment — drop (keeps the conjunction weaker, never stronger).
		}
		b.WriteString("(assert " + h + ")\n")
		asserted++
	}
	if asserted < 1 {
		return // nothing translatable to contradict.
	}
	query := tr.factPreamble() + b.String()
	proven, _ := a.smtCheckRawQueryWithLogLabel(query, "requires-vacuity", fn.Name)
	if !proven {
		return // sat / unknown / error → not proven contradictory → do NOT flag.
	}
	a.proofLint(fn.Position, "the `requires` preconditions of %q are contradictory (their conjunction is unsatisfiable); the function can never be called and its body can vacuously prove any `ensure`", fn.Name)
}
