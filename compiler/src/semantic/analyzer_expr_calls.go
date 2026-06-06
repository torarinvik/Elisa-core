package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	return a.analyzeCallExprWithExpected(expr, nil)
}

func (a *Analyzer) analyzeCallExprWithExpected(expr *ast.CallExpr, expected Type) Type {
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
	if resultType, ok := a.analyzeConstReflectionCall(expr); ok {
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
	if storeType, ok := a.treeStoreConstructorCall(expr); ok {
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
	if helperType, ok := a.analyzeTreeTraversalHelperCall(expr); ok {
		a.recordBuiltinHelperFuncType(expr, callIdentName(expr), helperType)
		return helperType
	}
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
	if treeType, variant, ok := a.treeConstructorCall(expr); ok {
		if variant != nil {
			a.requireActiveTreeConstructorOwner(expr.Pos(), treeType, variant)
		}
		return a.analyzeTreeConstructorCallExpr(expr, treeType, variant)
	}
	if memberType, ok := a.treeExactMemberConstructorCall(expr); ok {
		if family, ok := TreeFamilyForMemberType(memberType); ok {
			a.requireActiveTreeFamilyConstructorOwner(expr.Pos(), family, memberType.String())
		}
		return a.analyzeTreeExactMemberConstructorCallExpr(expr, memberType)
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
	if a.enforceUnsafePermissions && isIndirectCallTarget(expr.Func) {
		a.recordFunctionPermissionRefs(unsafeIndirectCallRefs(expr.Pos()))
	}
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
	if !orderedOK {
		return invalidType
	}
	return a.analyzeResolvedCallExprWithExpected(expr, ft, orderedArgs, expected)
}

func (a *Analyzer) analyzeResolvedCallExpr(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr) Type {
	return a.analyzeResolvedCallExprWithExpected(expr, ft, orderedArgs, nil)
}

func (a *Analyzer) analyzeResolvedCallExprWithExpected(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr, expectedReturn Type) Type {
	if expr == nil || ft == nil {
		return invalidType
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
			if !AssignableTo(expectedType, argType) {
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
	}
	a.resolveImplicitCallArgs(expr, ft, bindings, shapeBindings, regionBindings, permissionBindings)
	for _, name := range ft.RegionParams {
		if _, ok := regionBindings[name]; !ok && a.lookupRegionParam(name) {
			regionBindings[name] = name
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
	callAliasArgs := append([]ast.Expr(nil), orderedArgs...)
	callAliasArgs = append(callAliasArgs, expr.ResolvedImplicitArgs...)
	a.validateCallArgAliasAccess(expr, appliedType.Params, callAliasArgs)
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
	}
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
		Position:      expr.Position,
		Func:          &ast.Ident{Position: fieldExpr.Position, Name: resolvedSym.Name},
		Args:          append([]ast.Expr{&ast.ZeroedLit{Position: fieldExpr.Object.Pos()}}, expr.Args...),
		ArgNames:      append([]string{""}, expr.ArgNames...),
		WithArgs:      append([]ast.WithArg(nil), expr.WithArgs...),
		WithBundles:   append([]ast.WithBundleUse(nil), expr.WithBundles...),
		WithItemOrder: append([]ast.WithItem(nil), expr.WithItemOrder...),
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
		Position:      expr.Position,
		Func:          expr.Func,
		Args:          append([]ast.Expr{placeholder}, expr.Args...),
		ArgNames:      append([]string{""}, expr.ArgNames...),
		WithArgs:      append([]ast.WithArg(nil), expr.WithArgs...),
		WithBundles:   append([]ast.WithBundleUse(nil), expr.WithBundles...),
		WithItemOrder: append([]ast.WithItem(nil), expr.WithItemOrder...),
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
