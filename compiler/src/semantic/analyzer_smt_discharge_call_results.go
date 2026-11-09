package semantic

import (
	"maps"
	"math/big"
	"slices"
	"strings"

	"elisacore/src/ast"
)

func (tr *smtTranslator) callResultTermEnv(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	// SURFACE dict lookup `d.get(k)` (resolved to `arena_dict_get`) maps to the dict value array, so a
	// goal like `d.get(k) > 0` under a `d.contains(k)` guard reaches the dict quantifier model.
	if sel, ok := tr.dictMethodCall(call, "vals", argEnv); ok {
		return sel, true
	}
	if !tr.opaqueCallQuantifierIndependent(call, argEnv) {
		return tr.scalarCallUF(call, argEnv, "Int")
	}
	resultType := tr.a.exprType(call)
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if resultType == nil && direct && decl != nil {
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				resultType = fnType.Return
			}
		}
	}
	if resultType == nil && !direct {
		// Abstract protocol-method call inside a contract (docs P1): its node was never analyzed in
		// this caller context (the predicate lives on the protocol decl), so recover the return type
		// from the bound protocol's method signature.
		resultType = tr.abstractMethodReturnType(call)
	}
	if !isSMTExactAssignmentType(resultType) || IsBoolType(resultType) {
		return "", false
	}
	// CANONICALIZATION: a function is deterministic, so two calls with syntactically-equal arguments
	// denote the same value and must share one SMT symbol. Without this, `g(n-1)` in a hypothesis and
	// `g(n-1)` in the obligation would be unrelated fresh symbols and nothing about a recursive function
	// could be transported between them. The key is the callee identity plus the canonical SMT terms of
	// the arguments (so `g(n - 1)` and `g(n-1)` match, and `g(x)` vs `g(y)` do not). If any argument is
	// outside the integer fragment we cannot form a stable key, so we fall back to a fresh aux (sound:
	// just less sharing). Only DIRECT calls to a known decl are canonicalized.
	args := proofCallArgs(call)
	var canonKey string
	if direct && decl != nil {
		if key, ok := tr.callCanonKeyEnv(decl, args, tr.canonKeyEnv(decl, argEnv)); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	// ABSTRACT protocol-method canonicalization (docs P1 behavioral subtyping): a call to a protocol
	// method through a bounded type param (`self.count()` in a contract, `c.count()` in a caller fact)
	// has no resolvable decl, but is still DETERMINISTIC — the same method on the same receiver denotes
	// one value. Key it by (method name, receiver SMT term, arg SMT terms) so the protocol-side
	// obligation and the caller-side hypothesis share a symbol and facts transport. This treats the
	// abstract method as an uninterpreted function, which is sound: we assert nothing about its result.
	if !direct || decl == nil {
		if key, ok := tr.abstractMethodCanonKey(call, argEnv); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	ret := tr.freshAux(smtTypeNonNegative(resultType))
	tr.linkScalarCallUF(call, argEnv, ret, "Int")
	// A call returns a value of its machine integer type, so it lies in that type's range.
	if signed, bits, ok := smtIntWidthSign(resultType); ok && bits > 0 && bits <= 64 {
		lo, hi := "0", new(big.Int).Lsh(big.NewInt(1), uint(bits)).String()
		if signed {
			half := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
			lo, hi = "(- "+half.String()+")", half.String()
		}
		tr.auxDecls = append(tr.auxDecls, "(assert (and (>= "+ret+" "+lo+") (< "+ret+" "+hi+")))\n")
	}
	if canonKey != "" {
		tr.callCanon[canonKey] = ret
	}
	if direct && decl != nil {
		tr.assertGhostCallCongruence(decl, args, argEnv, ret)
	}
	if !direct || decl == nil {
		return ret, true
	}
	expandsContract := len(decl.Requires) != 0 || len(decl.EnsureValues) != 0
	// Keep calls congruent even after contract expansion reaches its resource bound.
	// The canonical result was cached above, so repeated calls with the same arguments
	// still share one opaque value while their deeper clauses are omitted soundly.
	if expandsContract && (tr.callClauseDepth >= smtCallClauseMaxDepth || tr.callClauseExpansions >= smtCallClauseMaxExpansions) {
		return ret, true
	}
	// A nested call of a callee whose clauses are being expanded gets no further clause expansion,
	// but may still receive its (bounded, guarded) defining equation: that is how a terminating
	// recursive spec unfolds more than one level. Its requires are translated for the guard only.
	equationOnly := false
	if expandsContract && tr.callClauseInFlight[decl] {
		if !tr.callEquationEligible(decl) || tr.a.isRecursiveProofCall(decl) {
			return ret, true
		}
		equationOnly = true
	}
	if expandsContract && !equationOnly {
		tr.callClauseExpansions++
		tr.callClauseDepth++
		tr.callClauseInFlight[decl] = true
		defer func() {
			tr.callClauseDepth--
			delete(tr.callClauseInFlight, decl)
		}()
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	priorBinding := tr.bindingCallee
	tr.bindingCallee = decl
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	tr.bindingCallee = priorBinding
	opaqueArgs := false
	if !ok {
		// An argument outside the fragment becomes an unconstrained value of its parameter's type.
		// The callee's (requires ⇒ ensures) holds for every argument value, so asserting it for a
		// free stand-in only weakens what is known; the defining equation and recursive IH are skipped.
		env, ok = tr.a.smtEnvForCallSubstOpaque(tr, decl, subst, argEnv)
		if !ok {
			return ret, true
		}
		opaqueArgs = true
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return ret, true
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	// (1) Assume the callee's already-verified `ensure` clauses at the call (existing behavior): a
	// contract-sound postcondition holds for the actual arguments.
	if equationOnly {
		if !opaqueArgs {
			tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, false, env)
		}
		return ret, true
	}
	recursiveProofCall := tr.a.isRecursiveProofCall(decl) && !opaqueArgs
	var recursiveCert recursiveProofCertificate
	if recursiveProofCall && tr.a.recursiveCertInProgress[call] {
		// Re-entered while proving this very call's certificate or requires (the goal mentions a local defined by
		// the call). Decline the recursive ensure assumption here: assuming less is always sound.
		recursiveProofCall = false
	}
	if recursiveProofCall {
		callPath := tr.a.contractCallPath(call)
		if tr.a.recursiveCertInProgress == nil {
			tr.a.recursiveCertInProgress = map[*ast.CallExpr]bool{}
		}
		tr.a.recursiveCertInProgress[call] = true
		var ok bool
		recursiveCert, ok = tr.a.recursiveCallCertificateUnderPath(tr.a.currentFuncDecl, decl, call, callPath)
		if !ok {
			recursiveProofCall = false
		}
		if recursiveProofCall && !tr.a.recursiveCallRequiresProven(decl, call) {
			tr.a.recordProof(call.Pos(), "recursive proof declined for "+decl.Name, "callee requires", ProofRuntime)
			tr.a.proofLint(call.Pos(), "recursive proof declined: callee requires for %q could not be proven at this recursive call", decl.Name)
			recursiveProofCall = false
		}
		delete(tr.a.recursiveCertInProgress, call)
	}
	ensureEnv := make(map[string]string, len(env)+1)
	for name, term := range env {
		ensureEnv[name] = term
	}
	ensureEnv["result"] = ret
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if tr.a.isRecursiveProofCall(decl) && !recursiveProofCall {
			continue
		}
		if h, ok := tr.calleeBoolTerm(decl, ensure, ensureEnv); ok {
			if guard == "true" {
				tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
			} else {
				tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
			}
			if recursiveProofCall {
				tr.a.recordProof(ensure.Pos(), "recursive IH of "+decl.Name+" ("+recursiveCert.label()+")", "ensure", ProofProvenContract)
			}
		}
	}
	// (2) DEFINING EQUATION for a PURE, TOTAL function: assert `f(args) == body[params:=args]`, so the
	// prover can reason THROUGH the function (and, for a terminating recursive function, perform
	// induction — the body's own recursive sub-call canonicalizes to a symbol the IH/equation can
	// constrain). SOUNDNESS GATES (tr.callEquationEligible): the function must be pure (a single
	// integer-returning `return <pure-expr>` over integer params, no mutation/loops/effects) AND, if it
	// is recursive, have a VERIFIED `decreases` measure. A non-terminating function's equation is
	// inconsistent (it would let `f(n) == f(n) + 1` style nonsense be assumed); the termination gate is
	// exactly what forbids that. The instantiation is bounded by callEqMaxUnroll so the axiom set stays
	// finite.
	if !opaqueArgs {
		tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, recursiveProofCall, env)
	}
	return ret, true
}

// smtEnvForCallSubstOpaque binds each callee parameter to its argument's term, or, for an integer
// parameter whose argument does not translate, to a fresh unconstrained integer (non-negative for an
// unsigned type). Any other untranslatable argument declines.
func (a *Analyzer) smtEnvForCallSubstOpaque(tr *smtTranslator, decl *ast.FuncDecl, subst map[string]ast.Expr, argEnv map[string]string) (map[string]string, bool) {
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || tr.proofParamCount(fnType) != len(decl.Params) {
		return nil, false
	}
	paramType := map[string]Type{}
	for i, p := range decl.Params {
		paramType[p.Name] = fnType.Params[i]
	}
	env := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(subst)) {
		scratch := map[string]string{}
		if a.bindSMTEnvArg(tr, scratch, name, subst[name], argEnv, false) {
			for k, v := range scratch {
				env[k] = v
			}
			continue
		}
		t := paramType[name]
		if t == nil || !IsNumericType(t) || IsFloatType(t) || IsBoolType(t) {
			return nil, false
		}
		if _, isRef := t.(*RefType); isRef {
			return nil, false
		}
		signed, _, ok := smtIntWidthSign(t)
		if !ok {
			return nil, false
		}
		env[name] = tr.freshAux(!signed)
	}
	return env, true
}

// abstractMethodReturnType recovers the return type of an abstract protocol-method call (one with no
// resolvable concrete decl) from the bound protocol's method signature. It handles both the method
// form (`recv.m(...)`, receiver typed as a bound type param) and the static type-path form
// (`T.m(...)`). Returns nil when the protocol/method cannot be identified.
func (tr *smtTranslator) abstractMethodReturnType(call *ast.CallExpr) Type {
	if tr == nil || tr.a == nil || call == nil {
		return nil
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field == "" {
		return nil
	}
	var iface *StaticInterface
	if obj, ok := fieldExpr.Object.(*ast.Ident); ok {
		// Static type-path form: object is a bound type param `T` with interface bound.
		if bound, isTypeParam := tr.a.lookupTypeParamInterface(obj.Name); isTypeParam {
			iface = bound
		}
	}
	if iface == nil {
		// Method form: the receiver expression's resolved type is a bound type param.
		if recvType := tr.a.exprType(fieldExpr.Object); recvType != nil {
			if tp, ok := stripRefForBounds(recvType).(*TypeParamType); ok {
				if bound, isTypeParam := tr.a.lookupTypeParamInterface(tp.Name); isTypeParam {
					iface = bound
				}
			}
		}
	}
	if iface == nil {
		// The receiver is an unresolved contract identifier (`self` on a protocol-decl predicate that
		// was never analyzed in this caller context). Fall back to the in-scope bound type-param
		// interfaces: find the unique one declaring a method of this name.
		for i := len(tr.a.typeParamInterfaceScopes) - 1; i >= 0 && iface == nil; i-- {
			scope := tr.a.typeParamInterfaceScopes[i]
			for _, boundName := range slices.Sorted(maps.Keys(scope)) { // map: sort so the chosen bound is stable
				bound := scope[boundName]
				if bound == nil {
					continue
				}
				if _, ok := bound.Methods[fieldExpr.Field]; ok {
					iface = bound
					break
				}
			}
		}
	}
	if iface == nil {
		return nil
	}
	method, ok := iface.Methods[fieldExpr.Field]
	if !ok || method == nil || method.Signature == nil {
		return nil
	}
	return method.Signature.Return
}

// abstractMethodCanonKey builds a stable canonicalization key for a deterministic protocol-method
// call that does not resolve to a concrete decl. It normalizes BOTH surface forms to the same key:
//
//   - method form  `recv.m(a, b)`   → participants = [recv, a, b]
//   - static form  `T.m(recv, a, b)` (the bound-type-param rewrite) → participants = [recv, a, b]
//
// The key is `method-name | term(p0) | term(p1) | …`, so `self.count()` (with `self`↦c) and the
// caller's `c.count()` produce the same key and share one SMT symbol. Returns false when the call is
// not a recognizable method/static-method form or any participant is outside the integer fragment.
func (tr *smtTranslator) abstractMethodCanonKey(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || call == nil {
		return "", false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field == "" {
		return "", false
	}
	args := proofCallArgs(call)
	var participants []ast.Expr
	// Static type-path form `T.m(...)`: the object is a bare type-param / type name, and the receiver
	// is the first argument. Detect it by the object being an Ident bound to a type-param interface.
	if obj, ok := fieldExpr.Object.(*ast.Ident); ok && tr.a != nil {
		if _, isTypeParam := tr.a.lookupTypeParamInterface(obj.Name); isTypeParam {
			participants = args
		}
	}
	if participants == nil {
		// Method form `recv.m(args...)`: receiver is the object, followed by the explicit args.
		participants = append(participants, fieldExpr.Object)
		participants = append(participants, args...)
	}
	var b strings.Builder
	b.WriteString("absmethod|")
	b.WriteString(fieldExpr.Field)
	b.WriteByte('|')
	for index, participant := range participants {
		t, ok := tr.callCanonArgKeyEnv(participant, argEnv)
		if !ok && index == 0 {
			// Protocol clauses may be translated before their receiver nodes have
			// expression types. Use the substituted projection or the caller's
			// scoped type-parameter binding to match the typed, lowered call.
			if receiver, bare := stripOptimizationParens(participant).(*ast.Ident); bare && receiver != nil {
				if bound, substituted := argEnv[receiver.Name]; substituted {
					t, ok = bound, strings.HasPrefix(bound, dtTokenPrefix)
				} else if _, bounded := tr.a.protocolMethodReceiverInterface(receiver, fieldExpr.Field); bounded {
					t, ok = dtToken(smtProjectionName(receiver)), true
				}
			}
		}
		if !ok {
			return "", false
		}
		b.WriteString(t)
		b.WriteByte('|')
	}
	return b.String(), true
}

func (tr *smtTranslator) callResultBoolTerm(call *ast.CallExpr) (string, bool) {
	return tr.callResultBoolTermEnv(call, nil)
}

func (tr *smtTranslator) callResultBoolTermEnv(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	// SURFACE dict membership `d.contains(k)` (resolved to `arena_dict_contains`) maps to the dict key
	// array, so it can serve as the in-domain GUARD for a value-lookup goal.
	if sel, ok := tr.dictMethodCall(call, "keys", argEnv); ok {
		return sel, true
	}
	if !tr.opaqueCallQuantifierIndependent(call, argEnv) {
		return tr.scalarCallUF(call, argEnv, "Bool")
	}
	if concrete, ok := tr.concreteStaticBoolMethodCall(call); ok {
		return tr.callResultBoolTermEnv(concrete, argEnv)
	}
	// A PROTOCOL-METHOD bool call (`x.le(y)` where le is an interface method, docs/85 P3) is modeled as
	// an UNINTERPRETED predicate: a stable Bool symbol keyed by (Interface.method, argument idents). The
	// same `x.le(y)` therefore denotes one symbol everywhere — so an injected protocol-law hypothesis
	// (`(=> (and le_xy le_yz) le_xz)`) shares symbols with the goal and transports the fact. Modeling a
	// total method as an arbitrary predicate is sound: it constrains nothing beyond what the laws assert.
	if sym, ok := tr.interfaceMethodBoolSym(call, argEnv); ok {
		return sym, true
	}
	resultType := tr.a.exprType(call)
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if resultType == nil && direct && decl != nil {
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				resultType = fnType.Return
			}
		}
	}
	if !IsBoolType(resultType) {
		return "", false
	}
	args := proofCallArgs(call)
	var canonKey string
	if direct && decl != nil {
		if key, ok := tr.callCanonKeyEnv(decl, args, tr.canonKeyEnv(decl, argEnv)); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	ret := tr.freshBoolAux()
	tr.linkScalarCallUF(call, argEnv, ret, "Bool")
	if canonKey != "" {
		tr.callCanon[canonKey] = ret
	}
	if !direct || decl == nil {
		return ret, true
	}
	expandsContract := len(decl.Requires) != 0 || len(decl.EnsureValues) != 0
	// Preserve deterministic congruence at the bound: calls already have a stable
	// canonical symbol, but no additional callee clauses are expanded.
	if expandsContract && (tr.callClauseDepth >= smtCallClauseMaxDepth || tr.callClauseExpansions >= smtCallClauseMaxExpansions) {
		return ret, true
	}
	equationOnly := false
	if expandsContract && tr.callClauseInFlight[decl] {
		if !tr.callEquationEligible(decl) || tr.a.isRecursiveProofCall(decl) {
			return ret, true
		}
		equationOnly = true
	}
	if expandsContract && !equationOnly {
		tr.callClauseExpansions++
		tr.callClauseDepth++
		tr.callClauseInFlight[decl] = true
		defer func() {
			tr.callClauseDepth--
			delete(tr.callClauseInFlight, decl)
		}()
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	priorBinding := tr.bindingCallee
	tr.bindingCallee = decl
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	tr.bindingCallee = priorBinding
	if !ok {
		return ret, true
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return ret, true
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	if !equationOnly {
		tr.assumeBoolCallEnsures(decl, env, guard, ret)
	}
	tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, false, env)
	return ret, true
}

// assumeBoolCallEnsures asserts a bool callee's ensures for one call occurrence, with `result` bound
// to the call's Bool symbol and the formals to the argument terms, under the callee's `requires`
// guard — the bool counterpart of the ensure assumption in callResultTermEnv. A recursive callee is
// skipped: its ensures at a recursive call are an induction hypothesis that needs a certificate.
func (tr *smtTranslator) assumeBoolCallEnsures(decl *ast.FuncDecl, env map[string]string, guard, ret string) {
	if decl == nil || len(decl.EnsureValues) == 0 || tr.a.isRecursiveProofCall(decl) || decl == tr.a.currentFuncDecl {
		return
	}
	ensureEnv := make(map[string]string, len(env)+1)
	for name, term := range env {
		ensureEnv[name] = term
	}
	ensureEnv["result"] = ret
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, ensure, ensureEnv)
		if !ok {
			continue
		}
		if guard == "true" {
			tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
		} else {
			tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
		}
	}
}

// callCanonKey builds the canonicalization key for a direct call: the callee's identity plus the
// canonical SMT term of each argument. Returns ok=false if any argument is outside the integer term
// fragment (so no stable key exists and the caller mints a fresh aux instead).
func (tr *smtTranslator) callCanonKey(decl *ast.FuncDecl, args []ast.Expr) (string, bool) {
	return tr.callCanonKeyEnv(decl, args, nil)
}

// canonKeyEnv keeps source self-call keys in the enclosing proof frame. During
// callee-body expansion, preserve actual substitutions: f(n-1)'s recursive call
// is f(n-2), never the same symbol. Per-declaration depth bounds unfolding.
func (tr *smtTranslator) canonKeyEnv(decl *ast.FuncDecl, argEnv map[string]string) map[string]string {
	return argEnv
}
