package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

// assignableThreadingRegionParam reports whether a region-LESS reference argument satisfies a parameter
// whose reference type carries one of the callee's REGION PARAMS (docs/91 S4 Stage 3). There the region
// is THREADED — bound from the argument's region or the caller's ambient scope — not carried by the
// value, so a region-less `T&` arg is acceptable (a by-value stack struct whose growable fields the
// callee allocates into the threaded region). Soundness holds downstream: the by-value-struct
// interior-taint escape checks reject binding to a region shorter-lived than where the grown value is
// later stored. An arg that already carries a region is left to the normal AssignableTo path.
func assignableThreadingRegionParam(expected, actual Type, regionParams map[string]bool) bool {
	er, ok := expected.(*RefType)
	if !ok || er == nil || er.Region == "" || !regionParams[er.Region] {
		return false
	}
	ar, ok := actual.(*RefType)
	if !ok || ar == nil || ar.Region != "" {
		return false
	}
	stripped := cloneRefType(er)
	stripped.Region = ""
	return AssignableTo(stripped, ar)
}

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	return a.analyzeCallExprWithExpected(expr, nil)
}

func (a *Analyzer) maybeEmitDirectCallDeprecation(expr *ast.CallExpr) {
	if a == nil || expr == nil || expr.Func == nil {
		return
	}
	var sym *Symbol
	switch fn := expr.Func.(type) {
	case *ast.Ident:
		s, _, ok := a.lookupVisibleGlobal(fn.Name)
		if ok {
			sym = s
		}
	case *ast.SpecializeExpr:
		if ident, ok := fn.Operand.(*ast.Ident); ok {
			s, _, found := a.lookupVisibleGlobal(ident.Name)
			if found {
				sym = s
			}
		}
	}
	if sym != nil && sym.Deprecated != "" {
		a.deprecatedf(expr.Pos(), "%s", sym.Deprecated)
	}
}

func (a *Analyzer) analyzeCallExprWithExpected(expr *ast.CallExpr, expected Type) Type {
	defer a.invalidateSMTAssertFactsForCall(expr)
	// A mutating BUILTIN collection method (`xs.clear()`, `d.put(...)`, `s.add(...)`) is modeled with a
	// VALUE receiver, not a `T&` ref, so it slips past the ref-arg fact invalidation below — leaving a
	// stale predicate/range/written-const fact (e.g. `NonEmpty` surviving `xs.clear()`). Drop the
	// receiver's facts here so the mutation is honored on every channel.
	defer a.invalidateFactsForMutatingBuiltinMethod(expr)
	// …and the same value-receiver write must be enforced against the function's `changes`/`preserves`
	// frame — `b.items.push(v)` writes b.items just as `b.items <- …` does.
	defer a.checkFrameForMutatingBuiltinMethod(expr)
	// A relocating dict insert invalidates any live interior reference returned by an earlier
	// arena_dict_get (the bucket array can move on resize). Run before dispatch so it applies on
	// every call path.
	a.invalidateStorageViewsForRelocatingDictCall(expr)
	if expr != nil && expr.Safe {
		return a.analyzeSafeCallExpr(expr)
	}
	if lambda, ok := unwrapLambdaExpr(expr.Func); ok && lambdaHasUntypedParams(lambda) {
		return a.analyzeDirectLambdaCallExpr(expr, lambda)
	}
	a.rewriteIndexedElementCall(expr)
	// `Foo.bar(...)` where Foo is a namespace (not a value) is a namespaced call
	// spelled with `.`; direct the user to `Foo::bar(...)` before any receiver
	// analysis reports a cryptic "undefined identifier Foo".
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok && fieldExpr != nil && !fieldExpr.Safe {
		if recvIdent, ok := fieldExpr.Object.(*ast.Ident); ok && recvIdent != nil && fieldExpr.Field != "" && !a.identNameResolvesAsValue(recvIdent.Name) {
			if _, found := a.globalScope.Lookup(joinQualifiedName(recvIdent.Name, fieldExpr.Field)); found {
				a.errorf(expr.Pos(), "%q is a namespace; write %s::%s(...) (`.` accesses value members, `::` accesses namespaces)", recvIdent.Name, recvIdent.Name, fieldExpr.Field)
				return invalidType
			}
		}
	}
	switch a.rewriteBuiltinDictMethodCall(expr) {
	case builtinDictMethodRewriteApplied:
		return a.analyzeCallExpr(expr)
	case builtinDictMethodRewriteInvalid:
		return invalidType
	}
	switch a.rewriteBuiltinSetMethodCall(expr) {
	case builtinDictMethodRewriteApplied:
		return a.analyzeCallExpr(expr)
	case builtinDictMethodRewriteInvalid:
		return invalidType
	}
	if resultType, ok := a.analyzeConstReflectionCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinFStrCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryInsertCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryGetOrInsertCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictRegionMutationCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinSetRegionMutationCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeHashBuiltinCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeCloneBuiltinCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeCopyBuiltinCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayPushCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayExtendCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayReserveCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayResizeCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarraySviewCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayCstrCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayClearCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayTruncateCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStorePushCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreReserveCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreClearCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreTruncateCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreRowsCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinSOAValidCall(expr); ok {
		return resultType
	}
	a.rewriteFreeCallReceiverOverload(expr)
	switch a.rewriteExtensionMethodCall(expr) {
	case extensionMethodCallRewriteInvalid:
		return invalidType
	}
	if storeType, ok := a.packedStoreConstructorCall(expr); ok {
		return storeType
	}
	if callIdentName(expr) == "freeze" {
		return a.analyzeFreezeCallExpr(expr)
	}
	if helperType, ok := a.analyzePackedNodeHelperCall(expr); ok {
		return helperType
	}
	if helperType, ok := a.analyzeProofCarryingViewHelperCall(expr); ok {
		a.recordBuiltinHelperFuncType(expr, callIdentName(expr), helperType)
		return helperType
	}
	a.rewriteQualifiedFunctionCallee(expr)
	if enumType, variant, ok := a.enumConstructorCall(expr); ok {
		if variant == nil {
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if enumType.Packed {
			alloc := &ast.AllocExpr{Position: expr.Pos(), Value: expr}
			// docs/76 Slice 0b: a bare constructor `Expr.Add(...)` of a region-backed (recursive-plain)
			// enum with no active explicit store is an implicit `new[auto]` into the inferred region.
			// The region-poly pre-pass now recognizes such constructors, so the building function is
			// region-polymorphic and a region is threaded (no no-region recursion). An explicit
			// `packed enum` (not RecursivePlain) keeps the explicit-store path.
			if enumType.RecursivePlain {
				if _, hasStore := a.lookupPackedStore(enumType); !hasStore {
					alloc.AutoRegion = true
					return a.analyzeAutoAllocExpr(alloc, expected)
				}
			}
			return a.analyzeScopedPackedAllocExpr(alloc)
		}
		orderedArgs, ok := a.resolveEnumConstructorArgs(expr, enumType, variant)
		if !ok {
			return enumType
		}
		if len(orderedArgs) != len(variant.Payload) {
			a.errorf(expr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
		}
		limit := len(orderedArgs)
		if len(variant.Payload) < limit {
			limit = len(variant.Payload)
		}
		for i := 0; i < len(orderedArgs); i++ {
			if i < limit {
				var actual Type
				orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], variant.Payload[i])
				if !AssignableTo(variant.Payload[i], actual) {
					label := variant.PayloadLabel(i)
					if label != "" {
						a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d (%s) to %q expects %s, got %s", i+1, label, enumType.Name+"."+variant.Name, variant.Payload[i], actual)
					} else {
						a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d to %q expects %s, got %s", i+1, enumType.Name+"."+variant.Name, variant.Payload[i], actual)
					}
				}
				a.consumeAffineValueExpr(orderedArgs[i], variant.Payload[i], a.enumConstructorMoveReason(enumType.Name, variant, i))
			} else {
				a.analyzeExpr(orderedArgs[i])
			}
		}
		return enumType
	}
	if errSet, qualifiedTag, payloadTypes, ok := a.errorConstructorCall(expr); ok {
		if len(expr.Args) != len(payloadTypes) {
			a.errorf(expr.Pos(), "error constructor %q expects %d arguments, got %d", ErrorTagDiagnosticName(qualifiedTag), len(payloadTypes), len(expr.Args))
		}
		limit := len(expr.Args)
		if len(payloadTypes) < limit {
			limit = len(payloadTypes)
		}
		for i := 0; i < len(expr.Args); i++ {
			if i < limit {
				actual := a.analyzeValueExpr(expr.Args[i], payloadTypes[i])
				if !AssignableTo(payloadTypes[i], actual) {
					a.errorf(expr.Args[i].Pos(), "error constructor argument %d to %q expects %s, got %s", i+1, ErrorTagDiagnosticName(qualifiedTag), payloadTypes[i], actual)
				}
			} else {
				a.analyzeExpr(expr.Args[i])
			}
		}
		return errSet
	}
	if targetType, ok := a.analyzeTypeConstructorCall(expr); ok {
		return targetType
	}
	fnType := a.analyzeExpr(expr.Func)
	ft, ok := fnType.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", fnType)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	a.maybeEmitDirectCallDeprecation(expr)
	if isIndirectCallTarget(expr.Func) {
		// call_as targets a raw (C) function pointer, so its aggregate ABI must
		// follow the platform C calling convention (sret returns / indirect args
		// for structs > 16 bytes), not Elisa's internal first-class lowering.
		if ft.CallConv == "" {
			ft.CallConv = "c"
		}
		if a.enforceUnsafePermissions {
			a.recordFunctionPermissionRefs(unsafeIndirectCallRefs(expr.Pos()))
		}
	}
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
	if !orderedOK {
		return invalidType
	}
	return a.analyzeResolvedCallExprWithExpected(expr, ft, orderedArgs, expected)
}

func (a *Analyzer) rewriteQualifiedFunctionCallee(expr *ast.CallExpr) {
	if a == nil || expr == nil || expr.Func == nil {
		return
	}
	if _, alreadyIdent := expr.Func.(*ast.Ident); alreadyIdent {
		return
	}
	name, ok := qualifiedTypePathFromExpr(expr.Func)
	if !ok || name == "" {
		return
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return
	}
	if _, ok := sym.Type.(*FuncType); !ok {
		return
	}
	expr.Func = &ast.Ident{Position: expr.Func.Pos(), Name: name}
}

func (a *Analyzer) analyzeResolvedCallExpr(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr) Type {
	return a.analyzeResolvedCallExprWithExpected(expr, ft, orderedArgs, nil)
}

func (a *Analyzer) analyzeResolvedCallExprWithExpected(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr, expectedReturn Type) Type {
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
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(orderedArgs))
	}
	if ft.Variadic && len(orderedArgs) < explicitParamCount {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, explicitParamCount, len(orderedArgs))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	regionBindings := map[string]string{}
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
			orderedArgs[i], argType = a.analyzeCallLikeValueExpr(orderedArgs[i], expectedType)
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
			argType = a.attachStructLocalArgRegion(orderedArgs[i], argType, ft.Params[i], regionParams)
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			expectedType = a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			if specializedType, ok := a.specializeFunctionValueType(expectedType, argType); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			if specializedType, ok := a.specializeCallbackCarryingTypeFromExpr(expectedType, orderedArgs[i]); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			if !AssignableTo(expectedType, argType) && !assignableThreadingRegionParam(expectedType, argType, regionParams) {
				a.errorf(orderedArgs[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType, argType)
				a.reportMutableRefArgumentNote(orderedArgs[i].Pos(), expectedType, argType)
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
			a.validateThreadTransferArg(ft.Name, orderedArgs[i], argType)
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
			if a.activeContainerRegionName() == "perm" {
				regionBindings[name] = "perm"
			}
		}
		if _, ok := regionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer region parameter %q for call to %q", name, ft.Name)
		}
	}
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
		if clonedApplied, ok := a.substituteType(appliedType, nil, nil, nil, nil).(*FuncType); ok && clonedApplied != nil {
			appliedType = clonedApplied
		}
		for i, specializedType := range specializedParamTypes {
			if i < len(appliedType.Params) {
				appliedType.Params[i] = specializedType
			}
		}
		appliedType.ReturnProvenance = regionRefState{}
		appliedType.ReturnProvenanceKnown = false
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
	a.validateAtomicMemoryOrderArgs(ft.Name, orderedArgs)
	a.warnOnLegacyRawAtomicFenceCall(expr.Pos(), ft)
	callAliasArgs := append([]ast.Expr(nil), orderedArgs...)
	callAliasArgs = append(callAliasArgs, expr.ResolvedImplicitArgs...)
	a.validateCallArgAliasAccess(expr, appliedType.Params, callAliasArgs)
	a.recordCallArgDisjoint(expr, appliedType.Params, callAliasArgs)
	a.dischargeCallArgRefinements(expr, callAliasArgs)
	a.dischargeCallRequires(expr, callAliasArgs)
	a.checkHigherOrderParamContracts(expr, callAliasArgs)
	a.assumeExternEnsures(expr, callAliasArgs)
	a.assumeLemmaEnsures(expr, callAliasArgs)
	originalTrackedByRoot := map[*Symbol]Type{}
	loweredArgs := append([]ast.Expr(nil), orderedArgs...)
	loweredArgs = append(loweredArgs, expr.ResolvedImplicitArgs...)
	poststateLimit := len(loweredArgs)
	if len(appliedType.Params) < poststateLimit {
		poststateLimit = len(appliedType.Params)
	}
	for i := 0; i < poststateLimit; i++ {
		paramType := a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
		if specializedType, ok := specializedParamTypes[i]; ok {
			paramType = specializedType
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
				// Iterator invalidation across a callee boundary: passing an actively-iterated
				// relocatable container by MUTABLE ref lets the callee push/clear/relocate its
				// buffer mid-iteration, which the function-local lock cannot see (the callee is
				// analyzed in its own scope). Conservatively reject any mutable-ref pass of an
				// iterated container (or a borrow alias of one) — the callee MIGHT relocate it.
				if _, isDArray := stripRefForBounds(rt.Elem).(*DArrayType); isDArray {
					a.checkIteratorInvalidationForMutableRefArg(loweredArgs[i])
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
	a.bindFreshReturnShapes(appliedType, shapeBindings)
	return a.substituteType(appliedType.Return, bindings, shapeBindings, regionBindings, permissionBindings)
}

func (a *Analyzer) safeChainReceiverType(receiverType Type) (Type, bool) {
	if receiverType == nil {
		return nil, false
	}
	if opt, ok := receiverType.(*OptionalType); ok && opt != nil && opt.Value != nil {
		return opt.Value, true
	}
	if ref, ok := receiverType.(*RefType); ok && ref != nil && ref.State != RefStateNonNull {
		return cloneRefTypeWithState(ref, RefStateNonNull), true
	}
	return nil, false
}

func optionalizeSafeChainResult(result Type) Type {
	if result == nil || IsInvalidType(result) || IsNeverType(result) {
		return result
	}
	return &OptionalType{Value: result}
}

func (a *Analyzer) analyzeSafeFieldExpr(expr *ast.FieldExpr) Type {
	if a == nil || expr == nil || expr.Object == nil || expr.Field == "" {
		return invalidType
	}
	receiverType := a.analyzeExpr(expr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		return invalidType
	}
	if field, ok := cstrSyntheticField(baseReceiverType, expr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		return optionalizeSafeChainResult(field.Type)
	}
	field, ok := a.lookupField(baseReceiverType, expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
	return optionalizeSafeChainResult(field.Type)
}

func (a *Analyzer) analyzeSafeCallExpr(expr *ast.CallExpr) Type {
	if a == nil || expr == nil {
		return invalidType
	}
	if expr.SafeReceiver != nil {
		return a.analyzeSafeTransformCallExpr(expr)
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		a.errorf(expr.Pos(), "optional call requires member-call syntax")
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if field, ok := a.lookupFieldNoError(baseReceiverType, fieldExpr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(fieldExpr, field.Type)
		ft, ok := field.Type.(*FuncType)
		if !ok {
			a.errorf(expr.Pos(), "cannot call non-function value of type %s", field.Type)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
		if !orderedOK {
			return invalidType
		}
		resultType := a.analyzeResolvedCallExpr(expr, ft, orderedArgs)
		if ft.Return == nil || isVoidType(resultType) {
			return a.namedTypes["void"]
		}
		return optionalizeSafeChainResult(resultType)
	}
	method, methodOK, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, baseReceiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	var resolvedSym *Symbol
	if methodOK && method != nil {
		resolvedSym = method.Symbol
	} else {
		resolvedSym, methodOK, err = a.lookupVisibleUFCSFunctionWithArity(fieldExpr.Field, baseReceiverType, 1+len(expr.Args))
		if err != nil {
			a.errorf(expr.Pos(), "%s", err.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if !methodOK || resolvedSym == nil {
			a.errorf(expr.Pos(), "optional call receiver %s has no member or UFCS function %q", receiverType, fieldExpr.Field)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
	}
	ft, ok := resolvedSym.Type.(*FuncType)
	if !ok || ft == nil {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", resolvedSym.Type)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	synthetic := &ast.CallExpr{
		Position: expr.Position,
		Func:     &ast.Ident{Position: fieldExpr.Position, Name: resolvedSym.Name},
		Args:     append([]ast.Expr{&ast.ZeroedLit{Position: fieldExpr.Object.Pos()}}, expr.Args...),
		ArgNames: append([]string{""}, expr.ArgNames...),
	}
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(synthetic, ft)
	if !orderedOK {
		return invalidType
	}
	resultType := a.analyzeResolvedCallExpr(synthetic, ft, orderedArgs)
	resolvedFuncType := ft
	if analyzedType, ok := a.exprTypes[synthetic.Func].(*FuncType); ok && analyzedType != nil {
		resolvedFuncType = analyzedType
	}
	receiverArgType := baseReceiverType
	if resolvedFuncType != nil && len(resolvedFuncType.Params) != 0 {
		receiverArgType = ufcsPreparedReceiverType(baseReceiverType, resolvedFuncType.Params[0])
	}
	a.safeCalls[expr] = &SafeCallInfo{
		ResolvedFuncName: resolvedSym.Name,
		ResolvedFuncType: resolvedFuncType,
		ReceiverArgType:  receiverArgType,
		TailArgs:         append([]ast.Expr(nil), orderedArgs[1:]...),
		ImplicitArgs:     append([]ast.Expr(nil), synthetic.ResolvedImplicitArgs...),
	}
	if ft.Return == nil || isVoidType(resultType) {
		return a.namedTypes["void"]
	}
	return optionalizeSafeChainResult(resultType)
}

func (a *Analyzer) analyzeSafeTransformCallExpr(expr *ast.CallExpr) Type {
	receiverType := a.analyzeExpr(expr.SafeReceiver)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	placeholder := &ast.ZeroedLit{Position: expr.SafeReceiver.Pos()}
	synthetic := &ast.CallExpr{
		Position: expr.Position,
		Func:     expr.Func,
		Args:     append([]ast.Expr{placeholder}, expr.Args...),
		ArgNames: append([]string{""}, expr.ArgNames...),
	}
	resultType := a.analyzeCallExpr(synthetic)
	resolvedFuncType, _ := a.exprTypes[synthetic.Func].(*FuncType)
	orderedArgs := synthetic.ResolvedArgs
	if !synthetic.ResolvedArgsValid {
		orderedArgs = synthetic.Args
	}
	receiverArgIndex := 0
	for i, arg := range orderedArgs {
		if arg == placeholder {
			receiverArgIndex = i
			break
		}
	}
	receiverArgType := baseReceiverType
	if analyzedReceiverArgType := a.exprTypes[placeholder]; analyzedReceiverArgType != nil {
		receiverArgType = analyzedReceiverArgType
	} else if resolvedFuncType != nil && receiverArgIndex < len(resolvedFuncType.Params) {
		receiverArgType = ufcsPreparedReceiverType(baseReceiverType, resolvedFuncType.Params[receiverArgIndex])
	}
	a.safeCalls[expr] = &SafeCallInfo{
		TransformFunc:    synthetic.Func,
		TransformArgs:    append([]ast.Expr(nil), orderedArgs...),
		ReceiverArgIndex: receiverArgIndex,
		ResolvedFuncType: resolvedFuncType,
		ReceiverArgType:  receiverArgType,
		TailArgs:         append([]ast.Expr(nil), synthetic.Args[1:]...),
		ImplicitArgs:     append([]ast.Expr(nil), synthetic.ResolvedImplicitArgs...),
	}
	if isVoidType(resultType) {
		return a.namedTypes["void"]
	}
	return optionalizeSafeChainResult(resultType)
}
