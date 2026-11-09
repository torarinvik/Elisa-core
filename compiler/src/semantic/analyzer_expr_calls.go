package semantic

import (
	"elisacore/src/ast"
	"strings"
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

// assignableArenaRegionParam accepts the short-lived reference used to pass an
// Arena carrier into a generic `Arena& @r` formal. The call binds `r` to the
// Arena's backing region separately; requiring that identity on the temporary
// address itself would confuse the lifetime of the Arena value with the lifetime
// of the storage managed by that Arena.
func (a *Analyzer) assignableArenaRegionParam(pattern, actual Type, expr ast.Expr, regionParams map[string]bool) bool {
	formal, ok := StripAggregateStateType(pattern).(*RefType)
	if !ok || formal == nil || formal.Region == "" || !regionParams[formal.Region] || !IsArenaValueOrRefType(formal.Elem) {
		return false
	}
	actualRef, ok := StripAggregateStateType(actual).(*RefType)
	if !ok || actualRef == nil || actualRef.Region != "" || a.arenaRegionArgumentName(expr) == "" {
		return false
	}
	formalWithoutRegion := cloneRefType(formal)
	formalWithoutRegion.Region = ""
	return AssignableTo(formalWithoutRegion, actualRef)
}

func callHasUnresolvedRegionContext(expr ast.Expr, expected Type, regionParams map[string]bool, bindings map[string]Type, regionBindings map[string]string) bool {
	expr = unwrapParenForRegionPoly(expr)
	if _, ok := expr.(*ast.CallExpr); !ok {
		return false
	}
	regionIsUnresolved := func(name string) bool {
		if name == "" || !regionParams[name] {
			return false
		}
		if _, bound := regionBindings[name]; bound {
			return false
		}
		if binding, bound := bindings[name]; bound {
			region, isRegion := binding.(*RegionParamType)
			return isRegion && region != nil && region.Name == name
		}
		return true
	}
	seen := map[Type]bool{}
	var contains func(Type) bool
	contains = func(typ Type) bool {
		if typ == nil || seen[typ] {
			return false
		}
		seen[typ] = true
		containsRegion := func(name string) bool { return regionIsUnresolved(name) }
		containsChild := func(child Type) bool { return contains(child) }
		switch value := typ.(type) {
		case *RegionParamType:
			return value != nil && containsRegion(value.Name)
		case *RefType:
			return value != nil && (containsRegion(value.Region) || containsChild(value.Elem))
		case *DArrayType:
			return value != nil && (containsRegion(value.Region) || containsChild(value.Elem))
		case *ViewType:
			return value != nil && (containsRegion(value.Region) || containsChild(value.Elem))
		case *CStrType:
			return value != nil && containsRegion(value.Region)
		case *SViewType:
			return value != nil && containsRegion(value.Region)
		case *DictType:
			return value != nil && (containsRegion(value.Region) || containsChild(value.Key) || containsChild(value.Value))
		case *SetType:
			return value != nil && (containsRegion(value.Region) || containsChild(value.Elem))
		case *GenericInstanceType:
			if value == nil || containsRegion(value.Region) {
				return value != nil
			}
			for _, arg := range value.Args {
				if containsChild(arg) {
					return true
				}
			}
		case *OptionalType:
			return value != nil && containsChild(value.Value)
		case *ErrorUnionType:
			return value != nil && containsChild(value.Value)
		case *ArrayType:
			return value != nil && containsChild(value.Elem)
		case *TupleType:
			if value != nil {
				for _, field := range value.Fields {
					if containsChild(field.Type) {
						return true
					}
				}
			}
		case *AggregateStateType:
			return value != nil && containsChild(value.Base)
		case *StructType:
			if value != nil {
				for _, field := range value.Fields {
					if containsChild(field.Type) {
						return true
					}
				}
			}
		case *IDType:
			return value != nil && (containsChild(value.Tag) || containsChild(value.Storage))
		case *AddressSpaceType:
			return value != nil && (containsChild(value.Elem) || containsChild(value.Storage))
		case *FuncType:
			if value != nil {
				for _, param := range value.Params {
					if containsChild(param) {
						return true
					}
				}
				return containsChild(value.Return)
			}
		}
		return false
	}
	return contains(expected)
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
	if result, handled := a.analyzeProtocolTransitionCall(expr); handled {
		return result
	}
	// docs/127 D9: `Name.from_c(raw)` names the synthesized validator of an `extern enum`.
	// Rewrite before dispatch — Elisa has no static methods, so the callee would otherwise
	// resolve as const-enum member access.
	a.rewriteForeignEnumValidatorCallee(expr)
	defer a.invalidateSMTAssertFactsForCall(expr)
	if ident, isIdent := expr.Func.(*ast.Ident); isIdent {
		if _, _, isOneShot := a.oneShotClosureSymbol(ident); isOneShot {
			savedCallee := a.oneShotCallee
			a.oneShotCallee = ident
			defer func() { a.oneShotCallee = savedCallee }()
			defer a.consumeOneShotClosureCall(expr)
		}
	}
	// A mutating BUILTIN collection method (`xs.clear()`, `d.put(...)`, `s.add(...)`) is modeled with a
	// VALUE receiver, not a `T&` ref, so it slips past the ref-arg fact invalidation below — leaving a
	// stale predicate/range/written-const fact (e.g. `NonEmpty` surviving `xs.clear()`). Drop the
	// receiver's facts here so the mutation is honored on every channel.
	defer a.invalidateFactsForMutatingBuiltinMethod(expr)
	// …and the same value-receiver write must be enforced against the function's `changes`/`preserves`
	// frame — `b.items.push(v)` writes b.items just as `b.items <- …` does.
	defer a.checkFrameForMutatingBuiltinMethod(expr)
	// docs/119 §6.2: the same value-receiver mutation is a hidden write when it targets an
	// outer binding inside a value block — `xs.push(v)` on an uncaptured outer `xs` is E4.
	defer a.checkValueBlockMutatingBuiltinMethod(expr)
	// A call may store a borrow of this frame through a `mutable T&` argument that outlives it.
	defer a.queueCallArgumentStoreChecks(expr)
	// …and a borrow of a function-local REGION's storage (a local darray's bytes) the same way.
	defer a.checkCallArgumentRegionStoreEscape(expr)
	// …and the same for a non-container holder (`set(h, b.as_sview())`, set storing into h.s).
	defer a.checkCallArgumentHolderStoreEscape(expr)
	// A tracked local container a callee fills takes on the provenance of its other arguments.
	defer a.recordCallFilledElementStates(expr)
	// …and a void grower that may allocate its grown container's elements in the adopted arena.
	defer a.noteAmbientCallFill(expr)
	// A relocating dict insert invalidates any live interior reference returned by an earlier
	// arena_dict_get (the bucket array can move on resize). Run before dispatch so it applies on
	// every call path.
	a.invalidateStorageViewsForRelocatingDictCall(expr)
	// The method form (`m.put(k, v)`, `m.get_or_insert(k, v)`) is only rewritten to the
	// arena_dict_* helper during dispatch, so the pre-dispatch check above sees no helper name;
	// re-check once the rewrite has happened.
	if expr != nil && callBaseName(expr) == "" {
		defer a.invalidateStorageViewsForRelocatingDictCall(expr)
	}
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
				a.errorf(expr.Pos(), "%q is a namespace; write %s::%s(...) (`.` accesses value members, `::` accesses namespaces)", ast.ModulePathSpelling(recvIdent.Name), ast.ModulePathSpelling(recvIdent.Name), fieldExpr.Field)
				return invalidType
			}
		}
	}
	// `S.push(v)` where S is a PROTOCOL-BOUND TYPE PARAMETER is protocol dispatch, not a
	// builtin collection method. Everything below keys on the field name alone, so a protocol
	// method sharing a builtin's name (`clear`, `push`, `reserve`, `rows`, `truncate`, `valid`,
	// dict/set `insert`) had its call claimed here, and the receiver was then evaluated as a
	// value -- reporting `undefined identifier "S"` for a name that is plainly in scope.
	if !a.callIsProtocolBoundMethod(expr) {
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
		if resultType, ok := a.analyzeBuiltinDarrayPopCall(expr); ok {
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
	}
	a.rewriteFreeCallReceiverOverload(expr)
	switch a.rewriteExtensionMethodCall(expr) {
	case extensionMethodCallRewriteInvalid:
		return invalidType
	}
	if storeType, ok := a.packedStoreConstructorCall(expr); ok {
		return storeType
	}
	if callIdentName(expr) == "freeze" && a.callNameIsUnshadowed("freeze") {
		return a.analyzeFreezeCallExpr(expr)
	}
	if helperType, ok := a.analyzePackedNodeHelperCall(expr); ok {
		return helperType
	}
	if helperType, ok := a.analyzeProofCarryingViewHelperCall(expr); ok {
		a.recordBuiltinHelperFuncType(expr, callIdentName(expr), helperType)
		return helperType
	}
	a.rewriteEnclosingNamespaceCallee(expr)
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
			// Explicit source Packed enums keep the explicit-store path.
			if enumType.StoreBackedPlain {
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
				a.checkProgramRegionSlotStore(orderedArgs[i], variant.Payload[i], actual)
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
		if !IsInvalidType(fnType) {
			a.errorf(expr.Pos(), "cannot call non-function value of type %s", fnType)
		}
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

// rewriteEnclosingNamespaceCallee renames a BARE callee to the enclosing module's own
// member, when that module has one: inside `module M:`, `add(...)` means M's `add`.
//
// Identifier resolution reaches the global scope through the ordinary scope chain, so a
// top-level or stdlib definition of the name was found there and returned before the
// enclosing module was consulted. stage0 therefore bound a module member's call to its
// own sibling to the stdlib generic:
//
//	module Box:
//	    def add(s: mutable Sack&, v: i64): ...
//	    def fill(s: mutable Sack&):
//	        add(s, 7)      # argument 1 to "add" expects mutable Flags[T]&
//
// while the self-hosted stage1 resolved it correctly. Two compilers disagreeing about a
// module member calling its own sibling by its own name.
//
// It renames rather than just resolving the type. Preferring the member in ident
// analysis alone type-checked the call against M's signature while codegen still emitted
// the stdlib symbol -- the repro compiled and returned 0 instead of 7. Renaming keeps the
// type and the emitted symbol the same function.
//
// Locals and parameters still shadow (enclosingNamespaceMember declines when one exists),
// so a `fn`-typed local named `add` is still what `add(...)` calls.
func (a *Analyzer) rewriteEnclosingNamespaceCallee(expr *ast.CallExpr) {
	if a == nil || expr == nil || expr.Func == nil {
		return
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	sym, qualified, ok := a.enclosingNamespaceMember(ident.Name)
	if !ok || sym == nil || qualified == ident.Name {
		return
	}
	if _, isFunc := sym.Type.(*FuncType); !isFunc {
		return
	}
	expr.Func = &ast.Ident{Position: ident.Position, Name: qualified}
}

func (a *Analyzer) rewriteQualifiedFunctionCallee(expr *ast.CallExpr) {
	if a == nil || expr == nil || expr.Func == nil {
		return
	}
	name, ok := qualifiedTypePathFromExpr(expr.Func)
	if !ok || name == "" {
		return
	}
	aliased := false
	if head, _, qualifiedPath := strings.Cut(name, "."); qualifiedPath {
		namespace := a.currentNamespace
		if namespace == "" && a.currentFuncType != nil {
			namespace = privateOwnerNamespace(a.currentFuncType.Name)
		}
		_, aliased = a.lookupModuleAliasTarget(namespace, head)
	}
	if _, alreadyIdent := expr.Func.(*ast.Ident); alreadyIdent && !aliased {
		return
	}
	sym, qualified, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return
	}
	if _, ok := sym.Type.(*FuncType); !ok {
		return
	}
	if aliased {
		name = qualified
	}
	expr.Func = &ast.Ident{Position: expr.Func.Pos(), Name: name}
}
