package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) typeStructurallyThreadShareable(t Type, seen map[string]bool) bool {
	if t == nil {
		return true
	}
	if a.containsAffineHandleValues(t, map[string]bool{}) {
		return false
	}
	if isBlessedThreadTransferCarrierType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *RefType:
		return tt.Storage == RefStorageStatic
	case *ArrayType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *DArrayType, *DictType:
		// A growable owned container is a header over a heap backing buffer it can mutate:
		// copying it across a thread boundary aliases that buffer, so it is NOT structurally
		// shareable (it would be a data race / lifetime hole). Transfer must go through a
		// frozen packed store, an owned-region move, or a Mutex/CondVar guard. This mirrors
		// closureCaptureSharesMutableState so the direct-arg and closure-capture paths agree.
		// (Views — ViewType/ViewType — recurse: their safety is governed by the
		// region- and packed-store-dependency checks in validateThreadTransferArg.)
		return false
	case *ViewType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if !a.typeStructurallyThreadShareable(payload, seen) {
					return false
				}
			}
		}
		return true
	case *StructType:
		for _, field := range tt.Fields {
			if !a.typeStructurallyThreadShareable(field.Type, seen) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := genericBindingsForStructInstance(base, tt.Args)
			regionBindings := regionBindingsForStructInstance(base, tt.Args)
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 || len(regionBindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, regionBindings, nil)
				}
				if !a.typeStructurallyThreadShareable(fieldType, seen) {
					return false
				}
			}
			return true
		}
		for _, arg := range tt.Args {
			if !a.typeStructurallyThreadShareable(arg, seen) {
				return false
			}
		}
		return a.typeStructurallyThreadShareable(tt.Base, seen)
	case *PackedEnumStoreType:
		return IsFrozenPackedEnumStoreType(tt)
	default:
		return true
	}
}

func isBlessedThreadTransferCarrierType(t Type) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.Builtin && (tt.Name == "Mutex" || tt.Name == "CondVar")
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.Builtin && (base.Name == "Mutex" || base.Name == "CondVar")
	default:
		return false
	}
}

func threadTransferRequiresUnsafeThreadShare(t Type, seen map[string]bool) bool {
	if t == nil || IsInvalidType(t) || isBlessedThreadTransferCarrierType(t) {
		return false
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *FuncType:
		// A closure carrying thread-unsafe captures shares them with the spawned thread.
		return tt.CapturesThreadUnsafe
	case *RefType:
		return tt.Storage != RefStorageStatic
	}
	return threadTransferRequiresUnsafeThreadShareSwitch(t, seen)
}

// closureCaptureSharesMutableState reports whether a value captured BY VALUE into a closure
// still aliases shared mutable state, so sending the closure to a thread is a data race.
// Unlike threadTransferRequiresUnsafeThreadShare (which assumes a MOVED argument and only
// looks through to a container's element), a captured container HEADER or reference copies a
// pointer the original still holds — so the container/ref itself is the sharing.
func (a *Analyzer) closureCaptureSharesMutableState(t Type, seen map[string]bool) bool {
	if t == nil || IsInvalidType(t) || isBlessedThreadTransferCarrierType(t) {
		return false
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *RefType:
		return tt.Storage != RefStorageStatic // a copied non-static ref still aliases its referent
	case *DArrayType, *DictType, *ViewType:
		return true // a copied header aliases the shared backing buffer
	case *ArrayType:
		return a.closureCaptureSharesMutableState(tt.Elem, seen) // inline array: shares iff element does
	case *StructType:
		for _, f := range tt.Fields {
			if a.closureCaptureSharesMutableState(f.Type, seen) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		// A generic struct instance shares iff any of its (substituted) FIELDS shares —
		// the type arguments alone miss a `Holder[T]{items: darray[T]}` whose darray
		// field aliases its backing. Mirror typeStructurallyThreadShareable so the
		// generic and non-generic struct paths agree.
		if base, ok := tt.Base.(*StructType); ok {
			bindings := genericBindingsForStructInstance(base, tt.Args)
			regionBindings := regionBindingsForStructInstance(base, tt.Args)
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 || len(regionBindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, regionBindings, nil)
				}
				if a.closureCaptureSharesMutableState(fieldType, seen) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.closureCaptureSharesMutableState(arg, seen) {
				return true
			}
		}
		return false
	default:
		return false // plain values (i64, bool, char, frozen stores, …) copy safely
	}
}

func threadTransferRequiresUnsafeThreadShareSwitch(t Type, seen map[string]bool) bool {
	switch tt := t.(type) {
	case *ArrayType:
		return threadTransferRequiresUnsafeThreadShare(tt.Elem, seen)
	case *DArrayType:
		return threadTransferRequiresUnsafeThreadShare(tt.Elem, seen)
	case *ViewType:
		return threadTransferRequiresUnsafeThreadShare(tt.Elem, seen)
	case *DictType:
		return threadTransferRequiresUnsafeThreadShare(tt.Key, seen) || threadTransferRequiresUnsafeThreadShare(tt.Value, seen)
	case *EnumType:
		if tt.Packed {
			return false
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if threadTransferRequiresUnsafeThreadShare(payload, seen) {
					return true
				}
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if threadTransferRequiresUnsafeThreadShare(field.Type, seen) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		for _, arg := range tt.Args {
			if threadTransferRequiresUnsafeThreadShare(arg, seen) {
				return true
			}
		}
		return threadTransferRequiresUnsafeThreadShare(tt.Base, seen)
	default:
		return false
	}
}

func threadTransferResultPayloadType(callName string, returnType Type) (Type, bool) {
	instance, ok := returnType.(*GenericInstanceType)
	if !ok || len(instance.Args) == 0 {
		return nil, false
	}
	switch callName {
	case "spawn1":
		if instance.Name != "Thread" {
			return nil, false
		}
	case "pool_submit1":
		if instance.Name != "Task" {
			return nil, false
		}
	default:
		return nil, false
	}
	return instance.Args[0], true
}

func atomicRmwPayloadType(argType Type) (Type, bool) {
	refType, ok := argType.(*RefType)
	if !ok {
		return nil, false
	}
	instance, ok := refType.Elem.(*GenericInstanceType)
	if !ok || (instance.Name != "atomic" && instance.Name != "AtomicSlot") || len(instance.Args) != 1 {
		return nil, false
	}
	return instance.Args[0], true
}

func atomicSlotPayloadType(argType Type) (Type, bool) {
	return atomicRmwPayloadType(argType)
}

func isAtomicSlotCallName(name string) bool {
	switch name {
	case "load", "store", "exchange", "compare_exchange", "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		return true
	default:
		return false
	}
}

func isAtomicRmwCallName(name string) bool {
	switch name {
	case "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		return true
	default:
		return false
	}
}

func (a *Analyzer) warnOnLegacyRawAtomicCall(pos lexer.Pos, callName string, argType Type) {
	if callName == "" || !isAtomicSlotCallName(callName) || isRuntimeStdPermissionInternal(pos.File) {
		return
	}
	if _, ok := atomicSlotPayloadType(argType); !ok {
		return
	}
	a.emitLegacyRawAtomicDiagnostic(pos, "`%s` is legacy raw atomic surface; use `AtomicCell[T]` helpers or a named protocol type that owns the memory-ordering invariant", callName)
}

func (a *Analyzer) warnOnLegacyRawAtomicFenceCall(pos lexer.Pos, fnType *FuncType) {
	if fnType == nil || fnType.Name != "fence" || isRuntimeStdPermissionInternal(pos.File) {
		return
	}
	if !permissionRefsContainMember(fnType.PermissionRefs, "Atomics", "Fence") {
		return
	}
	a.emitLegacyRawAtomicDiagnostic(pos, "`fence` is legacy raw atomic surface; keep fences inside `AtomicCell[T]` helpers or a named protocol type that owns the memory-ordering invariant")
}

func (a *Analyzer) emitLegacyRawAtomicDiagnostic(pos lexer.Pos, format string, args ...interface{}) {
	// Raw atomic ops are no longer part of the public surface; user code goes
	// through AtomicCell[T] helpers. Stdlib internals stay exempt via the
	// isRuntimeStdPermissionInternal guard at the call sites above.
	a.errorf(pos, "raw concurrency surface removed: "+format, args...)
}

func (a *Analyzer) validateThreadTransferArg(callName string, arg ast.Expr, argType Type, joinPool *poolScopeState) {
	// An owned region moved into the worker is an exclusive ownership transfer,
	// not sharing — the caller relinquishes it, so it need not be structurally
	// shareable. The transfer/consume is recorded by the caller (see the
	// spawn1/pool_submit1 site in analyzer_expr_calls.go).
	if _, ok := a.returnedRegionOwner(arg); ok {
		return
	}
	if !a.typeStructurallyThreadShareable(argType, map[string]bool{}) {
		a.errorf(arg.Pos(), "argument to %q is not structurally shareable across threads: %s", callName, argType)
		return
	}
	if joinPool != nil && joinPool.JoinedViews != nil && a.currentScope != nil {
		named := false
		if a.currentStorageViewDeps != nil {
			collectClauseIdents(arg, func(ident *ast.Ident) {
				if sym, ok := a.currentScope.Lookup(ident.Name); ok {
					if dep, ok := a.currentStorageViewDeps[sym]; ok && dep.Valid {
						*joinPool.JoinedViews = append(*joinPool.JoinedViews, poolJoinedView{Expr: ident, Name: ident.Name})
						named = true
					}
				}
			})
		}
		// An unnamed view argument is just as live until the join as a named one.
		if !named {
			if dep, ok := a.storageViewDependencyForExpr(arg); ok && dep.Valid && len(dep.Sources) > 0 {
				name := optimizationExprString(arg)
				if name == "" {
					name = "submitted view"
				}
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: argType, Node: arg}
				if a.currentStorageViewDeps == nil {
					a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
				}
				a.currentStorageViewDeps[sym] = dep
				*joinPool.JoinedViews = append(*joinPool.JoinedViews, poolJoinedView{Expr: arg, Name: name, Sym: sym})
			}
		}
	}
	state, ok := a.regionRefStateForExpr(arg)
	if !ok {
		return
	}
	var escaping *Symbol
	var joined []poolJoinedRegionDependency
	for _, region := range sortedSymbolKeys(state.Deps) { // map: sort for a stable report order
		dep := state.Deps[region]
		if !dep.Valid || region == nil {
			continue
		}
		if isCompilerGeneratedAutoRegionSymbol(region) && (joinPool != nil || callName == "parallel for" || returnTypeIsScalarValue(argType)) {
			// The function's own auto region outlives a scope that joins its workers, and a scalar
			// value carries no borrow the worker could dereference through the type system.
			continue
		}
		if joinPool != nil {
			if generation, outer := joinPool.OuterRegions[region]; outer && generation == dep.Generation {
				joined = append(joined, poolJoinedRegionDependency{Region: region, Generation: dep.Generation, Pos: arg.Pos(), CallName: callName})
				continue
			}
		}
		if escaping == nil || region.Name < escaping.Name {
			escaping = region
		}
	}
	if escaping != nil {
		a.errorf(arg.Pos(), threadLocalRegionDependencyMessage(callName, escaping.Name))
		return
	}
	if joinPool != nil && joinPool.Joined != nil {
		*joinPool.Joined = append(*joinPool.Joined, joined...)
	}
	if store, dep, ok := firstNonShareablePackedStoreDependency(state); ok {
		label := "<packed store>"
		if store != nil {
			label = store.Name
		}
		if dep.Type != nil {
			label = dep.Type.String()
		}
		a.errorf(arg.Pos(), threadUnpublishedStoreDependencyMessage(callName, label))
	}
}

func (a *Analyzer) validateThreadTransferResultType(callName string, pos lexer.Pos, resultType Type) {
	if !a.typeStructurallyThreadShareable(resultType, map[string]bool{}) {
		a.errorf(pos, "result of %q is not structurally shareable across threads: %s", callName, resultType)
	}
}

func (a *Analyzer) validateAtomicRmwArg(callName string, arg ast.Expr, argType Type) {
	payloadType, ok := atomicRmwPayloadType(argType)
	if !ok {
		return
	}
	if !IsNumericType(payloadType) {
		a.errorf(arg.Pos(), "argument to %q requires atomic_numeric(T), got atomic[%s]", callName, payloadType)
	}
}

func (a *Analyzer) validateAtomicMemoryOrderArgs(callName string, args []ast.Expr, params []Type, permissions []ast.PermissionRef) {
	isMemoryOrderParameter := func(index int) bool {
		if index < 0 || index >= len(params) {
			return false
		}
		enumType, ok := params[index].(*EnumType)
		return ok && enumType.Name == "MemoryOrder"
	}
	if callName == "fence" {
		if !permissionRefsContainMember(permissions, "Atomics", "Fence") || !isMemoryOrderParameter(0) {
			return
		}
	} else if isAtomicSlotCallName(callName) {
		if len(params) == 0 {
			return
		}
		if _, ok := atomicSlotPayloadType(params[0]); !ok {
			return
		}
	}
	switch callName {
	case "load":
		if len(args) >= 2 && isMemoryOrderParameter(1) {
			a.validateAtomicMemoryOrder(args[1], callName, atomicOrderContextLoad)
		}
	case "store":
		if len(args) >= 3 && isMemoryOrderParameter(2) {
			a.validateAtomicMemoryOrder(args[2], callName, atomicOrderContextStore)
		}
	case "compare_exchange":
		if len(args) >= 5 && isMemoryOrderParameter(3) && isMemoryOrderParameter(4) {
			a.validateAtomicMemoryOrder(args[3], callName, atomicOrderContextReadModifyWrite)
			a.validateAtomicMemoryOrder(args[4], callName, atomicOrderContextFailure)
			a.validateCompareExchangeFailureOrdering(args[3], args[4])
		}
	case "exchange", "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		if len(args) >= 3 && isMemoryOrderParameter(2) {
			a.validateAtomicMemoryOrder(args[2], callName, atomicOrderContextReadModifyWrite)
		}
	case "fence":
		if len(args) >= 1 && isMemoryOrderParameter(0) {
			a.validateAtomicMemoryOrder(args[0], callName, atomicOrderContextFence)
		}
	}
}

type atomicOrderContext int

const (
	atomicOrderContextLoad atomicOrderContext = iota
	atomicOrderContextStore
	atomicOrderContextReadModifyWrite
	atomicOrderContextFailure
	atomicOrderContextFence
)

const (
	atomicMemoryOrderRelaxed = 0
	atomicMemoryOrderAcquire = 1
	atomicMemoryOrderRelease = 2
	atomicMemoryOrderAcqRel  = 3
	atomicMemoryOrderSeqCst  = 4
)

func (a *Analyzer) validateAtomicMemoryOrder(arg ast.Expr, callName string, context atomicOrderContext) {
	order, ok := a.atomicMemoryOrderValue(arg)
	if !ok {
		a.errorf(arg.Pos(), "atomic %s memory order must be a compile-time MemoryOrder constant", callName)
		return
	}
	switch context {
	case atomicOrderContextLoad:
		if order == atomicMemoryOrderRelease || order == atomicMemoryOrderAcqRel {
			a.errorf(arg.Pos(), "atomic load cannot use release ordering")
		}
	case atomicOrderContextStore:
		if order == atomicMemoryOrderAcquire || order == atomicMemoryOrderAcqRel {
			a.errorf(arg.Pos(), "atomic store cannot use acquire ordering")
		}
	case atomicOrderContextFailure:
		if order == atomicMemoryOrderRelease || order == atomicMemoryOrderAcqRel {
			a.errorf(arg.Pos(), "compare_exchange failure ordering cannot use release semantics")
		}
	case atomicOrderContextFence:
		if order == atomicMemoryOrderRelaxed {
			a.errorf(arg.Pos(), "atomic fence cannot use relaxed ordering")
		}
	case atomicOrderContextReadModifyWrite:
		if order < atomicMemoryOrderRelaxed || order > atomicMemoryOrderSeqCst {
			a.errorf(arg.Pos(), "atomic %s uses unknown memory ordering %d", callName, order)
		}
	}
}

func (a *Analyzer) validateCompareExchangeFailureOrdering(successArg ast.Expr, failureArg ast.Expr) {
	success, successOK := a.atomicMemoryOrderValue(successArg)
	failure, failureOK := a.atomicMemoryOrderValue(failureArg)
	if !successOK || !failureOK {
		return
	}
	if compareExchangeFailureOrderAllowed(success, failure) {
		return
	}
	a.errorf(failureArg.Pos(), "compare_exchange failure ordering cannot be stronger than success ordering")
}

func compareExchangeFailureOrderAllowed(success int64, failure int64) bool {
	switch success {
	case atomicMemoryOrderRelaxed:
		return failure == atomicMemoryOrderRelaxed
	case atomicMemoryOrderAcquire:
		return failure == atomicMemoryOrderRelaxed || failure == atomicMemoryOrderAcquire
	case atomicMemoryOrderRelease:
		return failure == atomicMemoryOrderRelaxed
	case atomicMemoryOrderAcqRel:
		return failure == atomicMemoryOrderRelaxed || failure == atomicMemoryOrderAcquire
	case atomicMemoryOrderSeqCst:
		return failure == atomicMemoryOrderRelaxed || failure == atomicMemoryOrderAcquire || failure == atomicMemoryOrderSeqCst
	default:
		return false
	}
}

func (a *Analyzer) atomicMemoryOrderValue(arg ast.Expr) (int64, bool) {
	if value, ok := a.evalConstExpr(arg); ok && value.Kind == ConstInt {
		return value.Int, true
	}
	if field, ok := arg.(*ast.FieldExpr); ok {
		if ident, ok := field.Object.(*ast.Ident); ok && ident.Name == "MemoryOrder" {
			switch field.Field {
			case "Relaxed":
				return atomicMemoryOrderRelaxed, true
			case "Acquire":
				return atomicMemoryOrderAcquire, true
			case "Release":
				return atomicMemoryOrderRelease, true
			case "AcqRel":
				return atomicMemoryOrderAcqRel, true
			case "SeqCst":
				return atomicMemoryOrderSeqCst, true
			}
		}
	}
	return 0, false
}
