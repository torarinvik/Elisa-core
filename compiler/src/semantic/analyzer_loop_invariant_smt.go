package semantic

import (
	"elisacore/src/ast"
	"strings"
)

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
func (a *Analyzer) proveLoopPreservationSMT(cond ast.Expr, invs []*ast.ContractStmt, target ast.Expr, subst map[string]ast.Expr, arrayStores []loopArrayStore, outerInvs []*ast.ContractStmt) (bool, string) {
	if !a.smtEnabled || a.smtUnavailable || target == nil {
		return false, ""
	}
	savedRangeFacts := cloneScopeRangeFacts(a.currentScope)
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		sc.rangeFacts = nil
	}
	defer restoreScopeRangeFacts(savedRangeFacts)
	tr := a.newSMTTranslator(nil)
	// The obligation: the invariant after the body's substitution. smtEnvForSubst maps each loop
	// variable to its post-body term (declaring the free variables of those terms).
	env, ok := a.smtEnvForSubst(tr, subst)
	if !ok {
		return false, ""
	}
	for name := range subst {
		// The assignment stores the value into the variable's integer type, so the post-body value is
		// the value REINTERPRETED at that width: `h <- h + 1` on a u64 is `(h + 1) mod 2^64`. The body
		// is captured before it is analyzed, so the right-hand side carries no type of its own and
		// would otherwise translate as unbounded arithmetic, disagreeing with every typed occurrence of
		// the same expression (a callee unfolding `suf(n, i + 1, h + 1)`).
		if sym, ok := a.currentScope.Lookup(name); ok && sym != nil && !IsBoolType(sym.Type) {
			if signed, bits, ok := smtIntWidthSign(sym.Type); ok {
				if term, bound := env[name]; bound && term != "" {
					env[name] = smtSameWidthReinterpret(term, signed, bits)
				}
			}
		}
	}
	// Array-element stores extend the environment with the post-body array value `(store arr idx val)`.
	// The index/value are translated with the FREE (pre-body) environment — consistent with the
	// hypotheses below and with capture's guarantee that they reference only pre-body values — so the
	// invariant's `arr[k]` becomes `(select (store arr idx val) k)` after substitution, and z3's array
	// theory proves the inductive step (the just-written cell plus the IH on the rest).
	//
	// MULTI-STORE: when the same array has multiple element stores (captured in body order), we compose
	// them as nested `(store BASE idx val)` with the LATER store outermost. The base for the first store
	// is the free pre-body array constant; the base for each subsequent store is the already-composed
	// term. This faithfully models `arr[i] <- a; arr[i+1] <- b` as `(store (store arr i a) (i+1) b)`,
	// which z3's array theory reads element-wise correctly for the inductive step.
	for _, st := range arrayStores {
		if st.record {
			// A record store leaves the array value unchanged in env: its element reads go through
			// the overlay, active only while the obligation (post-body state) is translated.
			tr.admitLoopFrameContainer(st.arrayName)
			if tr.recordOverlays == nil {
				tr.recordOverlays = map[string]loopArrayStore{}
			}
			tr.recordOverlays[st.arrayName] = st
			continue
		}
		// Base: if a prior store to this array already built a composed term, use it; otherwise start
		// from the free pre-body array constant.
		var baseTerm string
		if prior, exists := env[st.arrayName]; exists {
			baseTerm = prior
		} else {
			var ok bool
			baseTerm, ok = tr.arrayTermEnv(st.array, nil)
			if !ok {
				return false, ""
			}
		}
		idxTerm, ok := tr.termEnv(st.index, nil)
		if !ok {
			return false, ""
		}
		valTerm, ok := tr.termEnv(st.value, nil)
		if !ok {
			return false, ""
		}
		env[st.arrayName] = "(store " + baseTerm + " " + idxTerm + " " + valTerm + ")"
	}
	// Lower the obligation into the central VC IR (shared folder + smart constructors) so a
	// preservation/termination goal that constant-folds to `true` is discharged with NO solver call and
	// structural comparisons/arithmetic are normalized. lowerVCFormula has the SAME symbol-declaration
	// side effects as boolTerm and emits opaque atoms verbatim, so the SMT query is byte-identical for
	// any goal the folder does not touch (behavior-preserving). When the target is outside the IR
	// fragment (quantifiers, array selects, …) lowering declines and we fall back to the opaque boolTerm
	// translation — the identical pre-IR path.
	tr.recordOverlayActive = len(tr.recordOverlays) != 0
	goal, lowered := tr.lowerVCFormula(target, env)
	var obligation string
	if lowered {
		obligation = emitVCFormula(goal)
	} else {
		var ok bool
		obligation, ok = tr.boolTerm(target, env)
		if !ok {
			tr.recordOverlayActive = false
			return false, ""
		}
	}
	tr.recordOverlayActive = false
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
	if cond != nil && !addHyp(cond) {
		// The guard and path conditions are hypotheses: asserting only the conjuncts the translator
		// models drops hypotheses and never adds one.
		for _, conjunct := range smtPathConjuncts(cond) {
			addHyp(conjunct)
		}
	}
	for _, inv := range invs {
		if !addHyp(inv.Cond) {
			return false, ""
		}
	}
	// Outer-loop proven invariants that are disjoint from this loop's mutations are additional sound
	// hypotheses: they were proven inductive by the outer loop, so they hold at the top of every outer
	// iteration, which includes the top of every inner iteration. A translation failure for an outer
	// invariant is non-fatal — skip that fact and continue (decline is always sound here).
	for _, outerInv := range outerInvs {
		addHyp(outerInv.Cond) // ignore bool return — optional hypothesis, decline is safe
	}
	// Hypotheses are ONLY the enclosing preconditions plus the chosen cond/invariant clauses (hypSB) —
	// deliberately NOT the ambient assert/flow facts the standard set carries, so the loop variables stay
	// free constants (the inductive hypothesis). Discharge through the shared check primitive; on a SAT
	// result the counterexample is a loop state satisfying cond + the invariants but VIOLATING the
	// invariant after one body step — a concrete witness to non-preservation.
	reqHyps := a.smtRequiresHypotheses(tr)
	// An immutable local's defining equation (`mask: usize = cap - 1`) holds wherever it is in scope,
	// at every iteration, so it is a sound hypothesis even though the ambient flow facts are not.
	locals := a.smtImmutableLocalHypotheses(tr)
	context := smtPreambleContext(tr.factPreamble()) + reqHyps + locals + hypSB.String() + obligation
	context = smtBoolMacroContext(tr, context)
	boolLocals := a.smtImmutableBoolLocalHypotheses(tr, context)
	hyps := reqHyps + locals + boolLocals + hypSB.String()
	// When the obligation lowered into the IR, discharge through the IR-aware helper so a goal that the
	// smart constructors folded to `true` concludes with no solver call; the loop's OWN hypothesis set is
	// passed through unchanged (loop variables free — the inductive hypothesis). Otherwise the opaque
	// obligation goes straight to the shared check primitive (the pre-IR path).
	if lowered {
		return a.smtDischargeFormulaWithHyps(tr, goal, hyps)
	}
	return a.smtCheckQuery(tr, hyps, obligation)
}

// loopArrayStore is a captured array-element assignment `array[index] <- value` in a loop body. The
// preservation prover models it as the SMT array-store `(store array index value)`, which is what lets
// a QUANTIFIED invariant over the array's contents (`forall k: 0<=k<i implies arr[k] == 0`) be proven
// inductive across the mutation that fills the array.
type loopArrayStore struct {
	arrayName string
	array     ast.Expr
	index     ast.Expr
	value     ast.Expr
	// record marks a whole-record store into an array of records. Its value is not a scalar term:
	// field reads of the stored element read the value's fields at the stored index and the
	// pre-body element's fields elsewhere (recordElementFieldTerm's overlay).
	record bool
}

// captureLoopBodyEffect models a straight-line loop body as a simultaneous substitution: each assigned
// scalar loop variable maps to its new value, and each array-element store `arr[i] <- v` is recorded
// as a loopArrayStore. It returns ok=false for any body it cannot fully account for — a side-effecting
// or non-arithmetic right-hand side, a doubly-written target, or any non-assignment statement. This
// conservatism is what makes the substitution sound: a captured body provably touches state only
// through these pure assignments and contains no control flow.
func (a *Analyzer) captureLoopBodyEffect(body []ast.Stmt) (map[string]ast.Expr, []loopArrayStore, bool) {
	return a.captureLoopBodyEffectMode(body, false, nil)
}

// captureLoopBodyEffectForTermination is the relaxed capture used by the loop-termination prover. It
// permits array-element STORES whose index/value contain side-effect-free reads (array-element and
// field reads), because such a store mutates no scalar and is therefore irrelevant to the
// scalar-measure decrease proof. It returns NO loopArrayStore records (termination never models
// array contents — it proves only the scalar measure), so this relaxation cannot reach, and cannot
// weaken, the invariant-preservation array-store SMT model, which keeps using the strict
// captureLoopBodyEffect.
func (a *Analyzer) captureLoopBodyEffectForTermination(body []ast.Stmt) (map[string]ast.Expr, bool) {
	subst, _, ok := a.captureLoopBodyEffectMode(body, true, nil)
	return subst, ok
}

func (a *Analyzer) captureLoopBodyEffectForTerminationFocused(body []ast.Stmt, focus map[string]bool) (map[string]ast.Expr, bool) {
	subst, _, ok := a.captureLoopBodyEffectMode(body, true, focus)
	return subst, ok
}

func (a *Analyzer) captureLoopBodyEffectMode(body []ast.Stmt, forTermination bool, focus map[string]bool) (map[string]ast.Expr, []loopArrayStore, bool) {
	// On the termination path the array-store index/value may also contain side-effect-free reads;
	// on the invariant path they must stay strictly pure-arith so the `(store arr idx val)` SMT model
	// reads only modeled terms.
	storeOperandPure := exprIsPureArith
	if forTermination {
		storeOperandPure = exprIsPureArithOrRead
	}
	subst := map[string]ast.Expr{}
	assigned := map[string]bool{}
	ignoredAssigned := map[string]bool{}
	var order []string
	var stores []loopArrayStore
	storedArrays := map[string]bool{}
	// Immutable body locals (strict path): their definitions read only pre-body state, so a record
	// store's value reads them through their definitions. Nothing else may read them.
	locals := map[string]ast.Expr{}
	readsLocal := func(exprs ...ast.Expr) bool {
		for _, e := range exprs {
			refs := map[string]bool{}
			collectExprIdentNames(e, refs)
			for r := range refs {
				if _, hit := locals[r]; hit {
					return true
				}
			}
		}
		return false
	}
	for _, s := range inlinePermissionBlocks(body) {
		switch n := s.(type) {
		case *ast.ExprStmt:
			// A pure builtin assertion has no effect on the loop state. Its condition is deliberately
			// not added as a proof hypothesis here: optimized builds may erase the runtime assertion,
			// so loop preservation must hold even when that condition is false.
			if !a.loopBodyAssertIsPure(n) {
				return nil, nil, false
			}
		case *ast.ContractStmt:
			// Leading `invariant` / `decreases` clauses are specifications, not body effects — skip them.
			if n.Kind != ast.ContractInvariant && n.Kind != ast.ContractDecreases {
				return nil, nil, false
			}
		case *ast.AssignStmt:
			switch target := n.Target.(type) {
			case *ast.Ident:
				if target == nil {
					return nil, nil, false
				}
				if forTermination && len(focus) != 0 && !focus[target.Name] {
					if !exprIsSideEffectFreeForTermination(n.Value) && !a.loopTerminationCallAdmissible(n.Value, focus) {
						return nil, nil, false
					}
					ignoredAssigned[target.Name] = true
					continue
				}
				if assigned[target.Name] || storedArrays[target.Name] {
					return nil, nil, false // multiple writes: simultaneous substitution would be wrong
				}
				if !exprIsPureArith(n.Value) || readsLocal(n.Value) {
					return nil, nil, false // calls / address-of / non-arithmetic: possible side effects
				}
				if _, isLocal := locals[target.Name]; isLocal {
					return nil, nil, false
				}
				subst[target.Name] = n.Value
				assigned[target.Name] = true
				order = append(order, target.Name)
			case *ast.IndexExpr:
				// Array-element store `arr[idx] <- val`, modeled as `(store arr idx val)`. Sound only when
				// arr is a bare array variable; idx and val are pure arithmetic; and idx/val reference only
				// PRE-body values (no variable assigned EARLIER in this body — else the store would observe
				// a post-assignment value, breaking the simultaneous-substitution model). The array must
				// never be whole-reassigned in the same body.
				//
				// MULTI-STORE: two or more stores to the SAME array are supported on the strict invariant
				// path. The SMT model composes them as nested `(store (store arr idx1 val1) idx2 val2)` in
				// body order (later store outermost). Sound because exprIsPureArith forbids array reads, so
				// each store's idx/val can only reference PRE-body scalar values; the ordering hazard
				// (store2 observing store1's result) cannot occur. The usual `assigned[r]` check blocks
				// idx/val from reading a scalar assigned earlier in the same body.
				//
				// On the TERMINATION path only, the array base may also be a stable struct-FIELD path
				// rooted at a single receiver identifier through `.field` hops (`self.xs`, `self.a.b`).
				// Such an element store changes the field array's CONTENTS but no scalar counter and not
				// its `.count`, so it is irrelevant to a scalar / field-count measure decrease and is
				// discarded exactly like the bare-array case. The "stored-once / not-also-reassigned"
				// tracking is keyed by the NORMALIZED field-path string so a store plus a whole-field
				// reassignment is still rejected. The strict invariant path keeps requiring a bare-Ident
				// base (no field key produced there).
				arrKey, arrOK := loopStoreTargetBaseKey(target, forTermination)
				if !arrOK {
					return nil, nil, false
				}
				// Reject whole-reassignment of an array that has been element-stored (and vice versa via
				// the `storedArrays[arrKey]` check in the scalar-Ident branch above). Multiple element
				// stores to the same array are allowed on the strict invariant path (composed as nested
				// stores in SMT), but still rejected on the termination path (field arrays; double stores
				// don't affect scalar measures and the termination prover already declines on them).
				if assigned[arrKey] || (forTermination && storedArrays[arrKey]) {
					return nil, nil, false
				}
				if readsLocal(target.Index) {
					return nil, nil, false
				}
				if !forTermination && !storeOperandPure(n.Value) {
					value, ok := a.loopRecordStoreValue(target, n.Value, locals, assigned, storedArrays)
					if !ok {
						return nil, nil, false
					}
					stores = append(stores, loopArrayStore{arrayName: arrKey, array: target.Object, index: target.Index, value: value, record: true})
					storedArrays[arrKey] = true
					continue
				}
				if !storeOperandPure(target.Index) || !storeOperandPure(n.Value) || readsLocal(n.Value) {
					return nil, nil, false
				}
				refs := map[string]bool{}
				collectArithIdents(target.Index, refs)
				collectArithIdents(n.Value, refs)
				for r := range refs {
					if assigned[r] {
						return nil, nil, false
					}
				}
				if a.loopRecordStored(stores, arrKey) {
					return nil, nil, false
				}
				// The termination prover proves only the scalar measure and discards stores, so recording
				// them is unnecessary there; on the invariant path each store is appended in body order so
				// the SMT tier can fold them into a left-nested (store ...) chain, outer = last write.
				if !forTermination {
					stores = append(stores, loopArrayStore{arrayName: arrKey, array: target.Object, index: target.Index, value: n.Value})
				}
				storedArrays[arrKey] = true
			default:
				return nil, nil, false
			}
		case *ast.VarDeclStmt:
			if !forTermination {
				if !a.loopRecordLocalAdmissible(n, locals, assigned, storedArrays) {
					return nil, nil, false
				}
				locals[n.Name] = n.Value
				continue
			}
			// TERMINATION path only: a body-local declaration (`d: i64 = digit(text[i])`) cannot
			// change a measure variable unless it shadows one or its initializer could write one.
			// Its name joins ignoredAssigned so a measure update that READS it is still declined
			// (the disjoint-RHS rule below) — the local's value is not modeled.
			if !forTermination || len(focus) == 0 || n == nil || n.Name == "" || focus[n.Name] {
				return nil, nil, false
			}
			if n.Value != nil && !exprIsSideEffectFreeForTermination(n.Value) && !a.loopTerminationCallAdmissible(n.Value, focus) {
				return nil, nil, false
			}
			ignoredAssigned[n.Name] = true
		default:
			return nil, nil, false // any other statement form
		}
	}
	if len(order) == 0 && len(stores) == 0 && len(storedArrays) == 0 {
		return nil, nil, false
	}
	// Disjoint-RHS rule: each scalar right-hand side may reference an assigned variable only when it IS
	// the assigned variable (`i <- i + 1`). Otherwise simultaneous substitution would diverge from the
	// body's sequential semantics (`i <- i + 1; j <- i`).
	for _, name := range order {
		refs := map[string]bool{}
		collectArithIdents(subst[name], refs)
		for r := range refs {
			if ignoredAssigned[r] {
				return nil, nil, false
			}
			if r != name && assigned[r] {
				return nil, nil, false
			}
		}
	}
	return subst, stores, true
}
