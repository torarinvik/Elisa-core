package semantic

import (
	"elisacore/src/ast"
	"strconv"
)

func (a *Analyzer) analyzeResolvedCallExpr(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr) Type {
	return a.analyzeResolvedCallExprWithExpected(expr, ft, orderedArgs, nil)
}

func (a *Analyzer) analyzeResolvedCallExprWithExpected(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr, expectedReturn Type) Type {
	if a.declaredCallReturnRegions != nil {
		delete(a.declaredCallReturnRegions, expr)
	}
	if expr == nil || ft == nil {
		return invalidType
	}
	if decl, ok := a.resolveDirectCallFuncDecl(expr); ok && decl != nil && decl.IsGhost {
		a.ghostReadSeen = true
		if a.ghostReadAllowed == 0 {
			a.errorf(expr.Pos(), "ghost function %q is verification-only and cannot be called by runtime code: it is erased from codegen, so call it only from specs (requires/ensure/invariant/law/assert)", decl.Name)
		}
	}
	if ft.Static && a.staticContextDepth == 0 {
		a.errorf(expr.Pos(), "static function %q can only be called from a static context", ft.Name)
		return invalidType
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	if !ft.Variadic && len(orderedArgs) != explicitParamCount {
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d%s", ft.Name, explicitParamCount, len(orderedArgs), boundsLengthHint(ft))
	}
	if ft.Variadic && len(orderedArgs) < explicitParamCount {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, explicitParamCount, len(orderedArgs))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	regionBindings := map[string]string{}
	savedCallRegionBindings, savedCallRegionBindingConflicts := a.callRegionBindings, a.callRegionBindingConflicts
	a.callRegionBindings, a.callRegionBindingConflicts = regionBindings, nil
	defer func() {
		a.callRegionBindings, a.callRegionBindingConflicts = savedCallRegionBindings, savedCallRegionBindingConflicts
	}()
	permissionBindings := map[string][]ast.PermissionRef{}
	specializedParamTypes := map[int]Type{}
	regionParams := regionParamSet(ft.RegionParams)
	if expectedReturn != nil && ft.Return != nil && !IsInvalidType(expectedReturn) {
		a.collectTypeBindings(ft.Return, expectedReturn, bindings, map[string]Shape{}, map[string]string{}, map[string][]ast.PermissionRef{}, regionParams)
		a.collectTypeBindings(StripAggregateStateType(ft.Return), StripAggregateStateType(expectedReturn), bindings, map[string]Shape{}, map[string]string{}, map[string][]ast.PermissionRef{}, regionParams)
	}
	limit := explicitParamCount
	if len(orderedArgs) < limit {
		limit = len(orderedArgs)
	}
	for i := 0; i < len(orderedArgs); i++ {
		var argType Type
		if i < limit {
			expectedType := a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			// A nested generic call must infer an as-yet-unbound region from its
			// own arguments, not from the outer call's contextual result type. The
			// latter is only a constraint on the completed expression; pushing it
			// into the nested call's arguments can pin its region before inference
			// and reject a sound call (e.g. parsing into `arena`, then passing the
			// resulting region-indexed handle to another generic function).
			argumentExpected := expectedType
			if callHasUnresolvedRegionContext(orderedArgs[i], expectedType, regionParams, bindings, regionBindings) {
				argumentExpected = nil
			}
			orderedArgs[i], argType = a.analyzeCallLikeValueExpr(orderedArgs[i], argumentExpected)
			if actualFuncType, ok := argType.(*FuncType); ok {
				if !actualFuncType.ReturnProvenanceKnown {
					a.inferFuncReturnProvenanceForExpr(orderedArgs[i], actualFuncType)
				}
				if !actualFuncType.ReturnBorrowedOwnerRefsKnown {
					a.inferFuncReturnBorrowedOwnerRefsForExpr(orderedArgs[i], actualFuncType)
				}
			}
			// A locally-constructed struct argument whose container fields live in an ambient
			// region threads that region into the callee's struct-ref region param (the
			// locally-constructed analogue of struct-param region threading).
			orderedArgs[i], argType = a.attachStructLocalArgRegion(orderedArgs[i], argType, ft.Params[i], regionParams)
			// Keep the call-argument expression's cached type in sync with the type used for
			// inference. Code generation resolves the hidden Arena& argument from this cached
			// region; leaving it unstamped makes semantic analysis accept the call but lowers
			// the hidden argument as null for nested struct fields.
			a.exprTypes[orderedArgs[i]] = argType
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectArenaRegionBinding(ft.Params[i], orderedArgs[i], bindings, regionBindings, regionParams)
			expectedType = a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			if specializedType, ok := a.specializeFunctionValueType(expectedType, argType); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			if specializedType, ok := a.specializeCallbackCarryingTypeFromExpr(expectedType, orderedArgs[i]); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			// A worker that reads a packed store submitted to a pool captures the submitting
			// scope's store (analyzer_submit_store_capture.go): check it by its explicit signature.
			storeCapture := false
			if stripped, ok := a.acceptStoreCapturingSubmit(ft.Name, i, expectedType, argType); ok {
				expectedType = stripped
				specializedParamTypes[i] = stripped
				storeCapture = true
				a.checkStoreCapturingSubmitWorker(orderedArgs[i], argType.(*FuncType))
			}
			if !storeCapture && !AssignableTo(expectedType, argType) && !assignableThreadingRegionParam(expectedType, argType, regionParams) && !a.assignableArenaRegionParam(ft.Params[i], argType, orderedArgs[i], regionParams) {
				a.errorf(orderedArgs[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType, argType)
				if !writableRefAssignableIgnoringMutability(expectedType, argType) || !a.reportRebindableReadOnlyRefNote(orderedArgs[i].Pos(), orderedArgs[i]) {
					a.reportMutableRefArgumentNote(orderedArgs[i].Pos(), expectedType, argType)
				}
				a.reportShapeMismatchNotes(orderedArgs[i].Pos(), expectedType, argType)
			}
			if !a.tryConsumeSinkCallArg(expr.Func, ft, i, orderedArgs[i], expectedType) {
				a.consumeAffineValueExpr(orderedArgs[i], expectedType, "argument to call "+strconv.Quote(ft.Name))
			}
			// An `owned <store>` parameter takes ownership: the caller must move
			// an owned region into it (consuming it here; the callee discharges
			// it). Passing an owner without `move` would copy the allocator
			// handle into a second owner — reject it.
			if i < len(ft.OwnedParams) && ft.OwnedParams[i] {
				if srcOwner, ok := a.returnedRegionOwner(orderedArgs[i]); ok {
					a.transferRegionOwnerOut(srcOwner, "moved into owned parameter of "+strconv.Quote(ft.Name))
				} else if srcOwner, ok := a.regionOwnerIdent(orderedArgs[i]); ok {
					a.errorf(orderedArgs[i].Pos(), "owned region %q is move-only: use `move %s` to transfer it into the owned parameter of %q", srcOwner.Name, srcOwner.Name, ft.Name)
				}
			}
		} else {
			argType = a.analyzeExpr(orderedArgs[i])
		}
		// The spawned CLOSURE itself (fn = arg 0 of spawn1 / arg 1 of pool_submit1): closures
		// capture by value, so a captured reference or container header still aliases the
		// original — sending the closure to a thread is a data race. (CapturesThreadUnsafe was
		// computed at the lambda site via closureCaptureSharesMutableState.)
		if (ft.Name == "spawn1" && i == 0) || (ft.Name == "pool_submit1" && i == 1) {
			capturesUnsafe := false
			if ftArg, ok := argType.(*FuncType); ok && ftArg.CapturesThreadUnsafe {
				capturesUnsafe = true
			}
			// A closure bound to a local (`w = lambda …; spawn1(w, …)`) carries its capture
			// safety on the tracked function value, not the declared parameter type.
			if ident, ok := orderedArgs[i].(*ast.Ident); ok && a.currentScope != nil {
				if sym, ok := a.currentScope.Lookup(ident.Name); ok {
					if fv, ok := a.currentFunctionValues[sym]; ok && fv != nil && fv.CapturesThreadUnsafe {
						capturesUnsafe = true
					}
				}
			}
			if capturesUnsafe {
				a.errorf(orderedArgs[i].Pos(), "the closure passed to %q captures state that cannot be shared across threads (a mutable reference, or a container that aliases its buffer). Closures capture by value, so the captured handle still points at the original — a data race. Pass the data by value, move ownership into the thread, or guard it with a Mutex", ft.Name)
			}
		}
		if (ft.Name == "spawn1" && i == 1) || (ft.Name == "pool_submit1" && i == 2) {
			if a.enforceUnsafePermissions && threadTransferRequiresUnsafeThreadShare(argType, map[string]bool{}) {
				a.recordFunctionPermissionRefs(unsafeThreadShareRefs(orderedArgs[i].Pos()))
			}
			var joinPool *poolScopeState
			if ft.Name == "pool_submit1" {
				joinPool = a.joinedPoolScopeForArg(orderedArgs[0])
			}
			a.validateThreadTransferArg(ft.Name, orderedArgs[i], argType, joinPool)
			// Transferring an owned region into a worker thread: `move`-ing the
			// owner consumes it here (the thread now owns it; the worker's
			// `owned` parameter discharges it). Passing an owner WITHOUT move
			// would copy the allocator handle into the thread while the caller
			// still owns it — reject that and require an explicit move.
			if ownerSym, ok := a.returnedRegionOwner(orderedArgs[i]); ok {
				a.transferRegionOwnerOut(ownerSym, "moved into thread by "+strconv.Quote(ft.Name))
			} else if sym, ok := a.regionOwnerIdent(orderedArgs[i]); ok {
				a.errorf(orderedArgs[i].Pos(), "owned region %q passed to %q must be moved (`move %s`) to transfer ownership to the worker thread", sym.Name, ft.Name, sym.Name)
			}
		}
		if isAtomicRmwCallName(ft.Name) && i == 0 {
			a.validateAtomicRmwArg(ft.Name, orderedArgs[i], argType)
		}
		if i == 0 {
			a.warnOnLegacyRawAtomicCall(expr.Pos(), ft.Name, argType)
		}
	}
	// docs/91 S4 W5: a by-value struct grown in a local region, passed to a callee that stores it into
	// a longer-lived argument, dangles when the local region frees — caught at the call site.
	a.checkInterprocStoreEscape(expr, orderedArgs)
	a.resolveImplicitCallArgs(expr, ft, bindings, shapeBindings, regionBindings, permissionBindings)
	for _, name := range ft.RegionParams {
		if _, typeBound := bindings[name]; typeBound {
			continue
		}
		if _, ok := regionBindings[name]; !ok && a.lookupRegionParam(name) {
			regionBindings[name] = name
		}
		// Bind a region parameter from a same-named in-scope region owner — an explicit
		// `region owner(...):` scope (or `with arena ... as owner`) makes `owner` a visible
		// region the callee can allocate into. This is the no-argument binding path (the
		// region appears in neither an argument nor the return type), letting a builder like
		// `def make[@owner]() -> State` allocate into the caller's named region with no
		// threaded `Arena&` — the region-system replacement for arena-passing. Inference from
		// `@r`-annotated arguments/returns (resolveImplicitCallArgs) takes precedence.
		if _, ok := regionBindings[name]; !ok {
			if sym, _ := a.lookupRegionState(name); sym != nil {
				regionBindings[name] = name
			}
		}
		// docs/91 S4 Stage 3: an otherwise-unbound region param binds to the ambient `perm` scope. A
		// region-poly callee that grows a struct-field container then allocates into `perm` — the
		// region-inference equivalent of wrapping each growth in a per-hop `in perm:`, so the caller
		// writes ONE `in perm:` at the origin instead of scattering them through the call chain.
		// RESTRICTED TO perm (program-lifetime): perm outlives every storage, so the grown field can
		// never dangle however the struct is later stored — always sound. A SHORTER ambient region
		// (`in inner:`) is NOT bound here: the growth is interprocedural so the interior-taint escape
		// checks can't see it, and binding to a short region would be a use-after-free. Such a call
		// stays a conservative "cannot infer region parameter" error rather than a miscompile.
		if _, ok := regionBindings[name]; !ok {
			if active := a.activeContainerRegionName(); active == "perm" || a.isProgramRegion(active) {
				// A declared program-lived region (docs/128) is program-lifetime like perm.
				regionBindings[name] = active
			}
		}
		if _, ok := regionBindings[name]; !ok {
			if program, ok := a.programLivedRegionArgBinding(ft, orderedArgs, name); ok {
				regionBindings[name] = program
			}
		}
		if _, ok := regionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer region parameter %q for call to %q", name, ft.Name)
		}
	}
	a.checkRegionParamViewArgs(ft, orderedArgs, regionBindings)
	for _, param := range ft.GenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" {
			continue
		}
		bound, ok := bindings[param.Name]
		if !ok || bound == nil {
			a.errorf(expr.Pos(), "cannot infer interface-constrained type parameter %q for call to %q; use explicit specialization", param.Name, ft.Name)
			continue
		}
		iface := a.staticInterfaces[param.InterfaceBound]
		if iface == nil {
			var ok bool
			iface, _, ok = a.lookupVisibleStaticInterface(param.InterfaceBound)
			if !ok || iface == nil {
				a.errorf(expr.Pos(), "%s", UnknownInterfaceMessage(param.InterfaceBound))
				continue
			}
		}
		if !a.typeSatisfiesStaticInterface(bound, iface) {
			a.errorf(expr.Pos(), interfaceConformanceFactMessage(bound.String(), iface.Name, "call to "+quoteFactTarget(ft.Name)))
		}
	}
	for _, name := range ft.PermissionParams {
		if _, ok := permissionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer permission parameter %q for call to %q", name, ft.Name)
		}
	}
	for _, param := range ft.GenericParams {
		if param.Kind != ast.GenericParamErrorSet {
			continue
		}
		if _, ok := bindings[param.Name]; !ok {
			a.errorf(expr.Pos(), "cannot infer error-set parameter %q for call to %q; pass a fallible callback whose error set it can bind to", param.Name, ft.Name)
		}
	}
	appliedType, _ := a.substituteType(ft, bindings, shapeBindings, regionBindings, permissionBindings).(*FuncType)
	if appliedType == nil {
		appliedType = ft
	}
	if len(specializedParamTypes) != 0 {
		declaredReturnContract := appliedType.ReturnRegion != ""
		// A successful FuncType substitution already owns its parameter slice and
		// mutable metadata. Clone only the fallback declaration signature.
		if appliedType == ft {
			if clonedApplied, ok := a.substituteType(appliedType, nil, nil, nil, nil).(*FuncType); ok && clonedApplied != nil {
				appliedType = clonedApplied
			}
		}
		for i, specializedType := range specializedParamTypes {
			if i < len(appliedType.Params) {
				appliedType.Params[i] = specializedType
			}
		}
		if !declaredReturnContract {
			appliedType.ReturnProvenance = regionRefState{}
			appliedType.ReturnProvenanceKnown = false
		}
		appliedType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		appliedType.ReturnBorrowedOwnerRefsKnown = false
		appliedType.ReturnIsolation = ReturnIsolationSummary{}
		appliedType.ReturnIsolationKnown = false
	}
	if len(bindings) != 0 || len(shapeBindings) != 0 || len(regionBindings) != 0 || len(permissionBindings) != 0 || len(specializedParamTypes) != 0 {
		a.exprTypes[expr.Func] = appliedType
	}
	if resultPayload, ok := threadTransferResultPayloadType(ft.Name, appliedType.Return); ok {
		if a.enforceUnsafePermissions && threadTransferRequiresUnsafeThreadShare(resultPayload, map[string]bool{}) {
			a.recordFunctionPermissionRefs(unsafeThreadShareRefs(expr.Pos()))
		}
		a.validateThreadTransferResultType(ft.Name, expr.Pos(), resultPayload)
	}
	a.validateAtomicMemoryOrderArgs(ft.Name, orderedArgs, appliedType.Params, appliedType.PermissionRefs)
	a.warnOnLegacyRawAtomicFenceCall(expr.Pos(), ft)
	callAliasArgs := append([]ast.Expr(nil), orderedArgs...)
	callAliasArgs = append(callAliasArgs, expr.ResolvedImplicitArgs...)
	a.validateCallArgAliasAccess(expr, appliedType.Params, callAliasArgs)
	a.checkLinearMutableCallArgs(appliedType.Params, callAliasArgs)
	a.checkLmutThreadClaims(expr, callAliasArgs)
	a.recordCallArgDisjoint(expr, appliedType.Params, callAliasArgs)
	a.dischargeCallArgRefinements(expr, callAliasArgs)
	a.dischargeCallRequires(expr, callAliasArgs)
	a.checkHigherOrderParamContracts(expr, callAliasArgs)
	a.assumeExternEnsures(expr, callAliasArgs)
	a.assumeLemmaEnsures(expr, callAliasArgs)
	originalTrackedByRoot := map[*Symbol]Type{}
	// The explicit/implicit argument sequence is unchanged by the checks above.
	loweredArgs := callAliasArgs
	poststateLimit := len(loweredArgs)
	if len(appliedType.Params) < poststateLimit {
		poststateLimit = len(appliedType.Params)
	}
	for i := 0; i < poststateLimit; i++ {
		paramType := appliedType.Params[i]
		if appliedType == ft {
			paramType = a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			if specializedType, ok := specializedParamTypes[i]; ok {
				paramType = specializedType
			}
		}
		a.recordCallArgPoststates(expr, loweredArgs[i], i, paramType, funcPoststatesForParam(appliedType.Poststates, i), originalTrackedByRoot)
		// Mutable refinement flow (docs/85): passing a variable by ref (incl. the receiver of a
		// mutating method like `arr.pop()`) may break any predicate that held on it, so drop its
		// predicate facts at the call site — mirroring the named-state widening this same loop does
		// for ref args. Conservative on two axes (any ref, not just mutable; no `ensures`-preserve
		// credit yet); both only add runtime checks, never remove them, so the drop stays sound. A
		// callee that re-establishes the predicate via `ensures` is a later refinement.
		//
		// Frame-aware survival (docs/87): when the callee declares a bounding frame AND writes nothing
		// through this particular parameter (calleeFrameSuffixesForParam returns an empty non-nil slice),
		// the argument is provably untouched — so pred, range, writtenConst, and writtenField facts survive.
		// Mirrors the smtAssertFacts path in invalidateSMTAssertFactsFramed. Soundness: an unframed callee
		// or one that writes through this param falls back to the conservative whole-argument drop.
		if rt, ok := paramType.(*RefType); ok && rt != nil && !a.callPreservesArgRefinements(expr, appliedType, i) {
			frameSuffixes := calleeFrameSuffixesForParam(appliedType, i)
			frameWritesNothingThroughParam := frameSuffixes != nil && len(frameSuffixes) == 0
			if !frameWritesNothingThroughParam {
				a.invalidatePredFactsForTarget(loweredArgs[i])
				a.invalidateWrittenConst(rootIdentNameOrEmpty(loweredArgs[i]))
				// A mutable ref may SHORTEN the buffer (`pop`, a helper that truncates), which
				// can remove a NUL terminator, so drop that fact too. Appends go through the
				// builtin push/extend path and never reach here, so this does not lose the
				// common `push(0)` then cast idiom.
				a.forgetDarrayNulTerminated(loweredArgs[i])
				// A mutating ref to `self` (or any struct) lets the callee write ANY field, so every
				// written-field fact under that root is stale — drop them all (mirrors the writtenConst drop
				// above). An immutable borrow took the preserve credit and never reached this branch.
				a.invalidateWrittenFieldsForRoot(rootIdentNameOrEmpty(loweredArgs[i]))
				// docs/90 brick 90-11: a ref call may write the arg, so any range fact about it is stale too —
				// dropped here so a postcondition seed (below) is the only interval that survives the call.
				a.invalidateRangeFactsForTarget(loweredArgs[i])
			}
		}
		// Frame enforcement (docs/87 channel 2): passing a place by MUTABLE ref means the callee may
		// write it, so it counts as a write to that place. An immutable borrow cannot write — skip it.
		// Uses the resolved appliedType.Params mutability signal (reliable post-substitution).
		if i < len(appliedType.Params) {
			if rt, ok := appliedType.Params[i].(*RefType); ok && rt != nil && rt.Mutable {
				a.checkFrameMutableRefArg(loweredArgs[i], calleeFrameSuffixesForParam(appliedType, i))
				// docs/119 §6.2: inside a value block, passing an outer binding by mutable
				// ref (incl. a mutating method receiver) is a hidden write — E4.
				a.checkValueBlockMutatingCall(expr, loweredArgs[i])
				// docs/120 §10: passing a place to an `lmut` parameter mutates it in place —
				// the mutation must be a reassignment (`x <- f(…, x, …)`), not a bare call.
				if rt.Linear {
					a.checkLinearMutation(expr, loweredArgs[i])
				}
				// Iterator invalidation across a callee boundary: passing an actively-iterated
				// relocatable container by MUTABLE ref lets the callee push/clear/relocate its
				// buffer mid-iteration, which the function-local lock cannot see (the callee is
				// analyzed in its own scope). Conservatively reject any mutable-ref pass of an
				// iterated container (or a borrow alias of one) — the callee MIGHT relocate it.
				switch stripRefForBounds(rt.Elem).(type) {
				case *DArrayType:
					a.checkIteratorInvalidationForMutableRefArg(loweredArgs[i])
					a.checkIteratorInvalidationForEnclosingMutableRefArg(loweredArgs[i])
					a.invalidateStorageViewsForMutableRefArg(loweredArgs[i], appliedType.Name)
					a.recordStorageViewCalleeStores(expr, loweredArgs, i)
				case *StructType:
					a.checkIteratorInvalidationForEnclosingMutableRefArg(loweredArgs[i])
					if len(a.currentStorageViewDeps) != 0 {
						a.invalidateStorageViewsForMutableRefArgSparing(loweredArgs[i], appliedType.Name, a.storageViewCalleeWriteSpare(expr, loweredArgs[i]))
					}
				case *BuiltinType:
					// A callee given a dstr by mutable ref may reassign or grow it, moving
					// the buffer a live `as_sview()` of it points into.
					if isDStrType(stripRefForBounds(rt.Elem).(*BuiltinType)) {
						a.invalidateStorageViewsForMutableRefArg(loweredArgs[i], appliedType.Name)
					}
				}
			}
		}
		// docs/85 brick 2 (A): a callee postcondition `ensures <param i> is Law` lets the caller GAIN
		// the predicate fact on the argument bound to param i — applied AFTER the invalidation above so
		// it survives. Sound because the callee's returns are checked against the same law (B). Only a
		// plainly-rooted argument can carry the fact (the predicate is keyed by variable name).
		for _, re := range appliedType.RefinementEnsures {
			if re.ParamIndex != i {
				continue
			}
			if root, ok := rootIdentName(loweredArgs[i]); ok {
				// A parametric postcondition (`ensures p is Bounded[0,500]`) is keyed by its exact
				// constant args, the same identity tryProveRefinementByFactSet matches against.
				if key, keyOK := a.factKey(re.LawName, re.Args); keyOK {
					recordPredFact(a.currentScope, root, key)
				}
			}
			// docs/90 brick 90-11: ALSO seed the integer interval the law implies, so the flow/interval
			// prover can use the mutated argument's numeric bound (not only the factset identity) — e.g.
			// `clamp(&x)` with `ensures x is Bounded[0, 9]` lets `arr[x]` prove in-bounds. Gated to a
			// MUTABLE-REF param: only there does the postcondition constrain the caller's variable (for a
			// by-value/immutable-ref param it describes the callee's copy). Applied after the invalidation
			// above so it survives, and dropped again at the arg's next mutation.
			if i < len(appliedType.Params) {
				if rt, ok := appliedType.Params[i].(*RefType); ok && rt != nil && rt.Mutable {
					a.seedEnsuresParamRangeFacts(loweredArgs[i], re.LawName, re.Args)
				}
			}
		}
		// A mutable-ref parameter whose declared type is itself refined (`out: mutable Small&`)
		// re-establishes that refined type for the caller's out argument after the call. This mirrors
		// the explicit `ensures p is Law[...]` reseed above, but comes from the callee's parameter type:
		// the body can only write values that satisfy that declared refined slot.
		if i < len(appliedType.Params) {
			if rt, ok := appliedType.Params[i].(*RefType); ok && rt != nil && rt.Mutable {
				if fact, ok := appliedType.ParamRefinementRanges[i]; ok {
					a.seedMutableRefParamRefinementRangeFact(loweredArgs[i], fact)
				}
			}
		}
	}
	// Hand the deferred SMT-fact invalidation (analyzeCallExprWithExpected) the resolved callee frame
	// and position-aligned arguments, so facts about places this call provably does not write survive.
	a.cacheCallFrameContext(expr, appliedType, loweredArgs)
	a.rememberConditionalCallPoststates(expr, appliedType, originalTrackedByRoot)
	a.rejectBorrowedNurseryPoolShutdown(expr, ft, appliedType, loweredArgs, permissionBindings)
	switch ft.Name {
	case "pool_shutdown":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.recordAffineConsumption(key, "argument to call \"pool_shutdown\"")
			}
		}
	case "task_group_add":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.markLiveProtocolDescription(key, "task group with pending tasks")
			}
		}
	case "task_group_wait_all":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.clearLiveProtocolTracking(key)
			}
		}
	}
	a.recordFunctionPermissionRefs(functionPermissionRefs(appliedType))
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	if appliedType.ReturnRegion != "" {
		// appliedType already contains simultaneous caller-region substitution.
		// Looking this name up in the callee bindings again would turn a->b,
		// b->c into an incorrect a->c return owner.
		region := appliedType.ReturnRegion
		if !a.lookupRegionParam(region) {
			if owner, state := a.lookupRegionState(region); owner == nil || state.Destroyed {
				region = ""
			}
		}
		if region != "" {
			if a.declaredCallReturnRegions == nil {
				a.declaredCallReturnRegions = map[*ast.CallExpr]string{}
			}
			a.declaredCallReturnRegions[expr] = region
		}
	}
	a.bindFreshReturnShapes(appliedType, shapeBindings)
	resultType := a.substituteType(ft.Return, bindings, shapeBindings, regionBindings, permissionBindings)
	if callback, ok := resultType.(*FuncType); ok {
		callback = a.cloneFunctionValueType(callback)
		callback.FunctionReturnSourceNames = mergePermissionFamilies(callback.FunctionReturnSourceNames, []string{ft.Name})
		callback.FunctionReturnSourceNames = mergePermissionFamilies(callback.FunctionReturnSourceNames, ft.MutableGlobalSourceNames)
		resultType = callback
	}
	return resultType
}
