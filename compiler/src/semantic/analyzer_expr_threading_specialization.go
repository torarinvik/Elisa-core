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
		// (Views — ViewType/DArrayViewType — recurse: their safety is governed by the
		// region- and packed-store-dependency checks in validateThreadTransferArg.)
		return false
	case *ViewType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *DArrayViewType:
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
				if len(bindings) != 0 {
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
	case *DArrayType, *DictType, *ViewType, *DArrayViewType:
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
				if len(bindings) != 0 {
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
	case *DArrayViewType:
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
	if !ok || instance.Name != "atomic" || len(instance.Args) != 1 {
		return nil, false
	}
	return instance.Args[0], true
}

func isAtomicRmwCallName(name string) bool {
	switch name {
	case "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		return true
	default:
		return false
	}
}

func (a *Analyzer) validateThreadTransferArg(callName string, arg ast.Expr, argType Type) {
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
	state, ok := a.regionRefStateForExpr(arg)
	if !ok {
		return
	}
	if region, _, ok := firstLiveRegionDependency(state); ok && region != nil {
		a.errorf(arg.Pos(), threadLocalRegionDependencyMessage(callName, region.Name))
		return
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

func (a *Analyzer) validateAtomicMemoryOrderArgs(callName string, args []ast.Expr) {
	switch callName {
	case "load":
		if len(args) >= 2 {
			a.validateAtomicMemoryOrder(args[1], callName, atomicOrderContextLoad)
		}
	case "store":
		if len(args) >= 3 {
			a.validateAtomicMemoryOrder(args[2], callName, atomicOrderContextStore)
		}
	case "compare_exchange":
		if len(args) >= 5 {
			a.validateAtomicMemoryOrder(args[3], callName, atomicOrderContextReadModifyWrite)
			a.validateAtomicMemoryOrder(args[4], callName, atomicOrderContextFailure)
			a.validateCompareExchangeFailureOrdering(args[3], args[4])
		}
	case "exchange", "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		if len(args) >= 3 {
			a.validateAtomicMemoryOrder(args[2], callName, atomicOrderContextReadModifyWrite)
		}
	case "fence":
		if len(args) >= 1 {
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

func (a *Analyzer) specializeFunctionValueType(expected Type, actual Type) (Type, bool) {
	expectedFunc, ok := expected.(*FuncType)
	if !ok {
		return expected, false
	}
	actualFunc, ok := actual.(*FuncType)
	if !ok {
		return expected, false
	}
	specialized, _ := a.substituteType(expectedFunc, nil, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return expected, false
	}
	changed := false
	if len(specialized.ExplicitParamNames) == 0 && len(actualFunc.ExplicitParamNames) != 0 {
		specialized.ExplicitParamNames = append([]string(nil), actualFunc.ExplicitParamNames...)
		changed = true
	}
	if !funcTypeHasAnyExplicitDefault(specialized) && funcTypeHasAnyExplicitDefault(actualFunc) {
		specialized.ExplicitParamDefaultExprs = append([]ast.Expr(nil), actualFunc.ExplicitParamDefaultExprs...)
		specialized.ExplicitParamHasDefault = append([]bool(nil), actualFunc.ExplicitParamHasDefault...)
		changed = true
	}
	if len(specialized.ImplicitParamNames) == 0 && len(actualFunc.ImplicitParamNames) != 0 {
		specialized.ImplicitParamNames = append([]string(nil), actualFunc.ImplicitParamNames...)
		changed = true
	}
	if specialized.Name == "func" && actualFunc.Name != "" && actualFunc.Name != specialized.Name {
		specialized.Name = actualFunc.Name
		changed = true
	}
	limit := len(specialized.Params)
	if len(actualFunc.Params) < limit {
		limit = len(actualFunc.Params)
	}
	for i := 0; i < limit; i++ {
		if paramType, ok := a.specializeFunctionValueType(specialized.Params[i], actualFunc.Params[i]); ok {
			specialized.Params[i] = paramType
			changed = true
		}
	}
	if returnType, ok := a.specializeFunctionValueType(specialized.Return, actualFunc.Return); ok {
		specialized.Return = returnType
		changed = true
	}
	if hasRegionProvenance(actualFunc.ReturnProvenance) {
		specialized.ReturnProvenance = cloneRegionRefState(actualFunc.ReturnProvenance)
		specialized.ReturnProvenanceKnown = true
		changed = true
	}
	if hasBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs) {
		specialized.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs)
		specialized.ReturnBorrowedOwnerRefsKnown = true
		changed = true
	}
	return specialized, changed
}

func cloneStructTypeWithFields(base *StructType, fields map[string]Field) *StructType {
	if base == nil {
		return nil
	}
	cloned := *base
	cloned.Fields = fields
	return &cloned
}

func cloneStructFields(fields map[string]Field) map[string]Field {
	if len(fields) == 0 {
		return map[string]Field{}
	}
	cloned := make(map[string]Field, len(fields))
	for name, field := range fields {
		cloned[name] = field
	}
	return cloned
}

func (a *Analyzer) lookupResolvedFieldType(actual Type, name string) (Type, bool) {
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}

func (a *Analyzer) specializeCallbackCarryingType(expected Type, actual Type) (Type, bool) {
	if expected == nil || actual == nil {
		return expected, false
	}
	if specialized, ok := a.specializeFunctionValueType(expected, actual); ok {
		return specialized, true
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingType(tt.Base, StripAggregateStateType(actual))
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingType(field.Type, actualFieldType)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		regionBindings := regionBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			expectedFieldType := field.Type
			if len(bindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, regionBindings, nil)
			}
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingType(expectedFieldType, actualFieldType)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}

func (a *Analyzer) specializeCallbackCarryingTypeFromExpr(expected Type, actualExpr ast.Expr) (Type, bool) {
	if expected == nil || actualExpr == nil {
		return expected, false
	}
	if actualType := a.exprTypes[actualExpr]; actualType != nil {
		if specialized, ok := a.specializeCallbackCarryingType(expected, actualType); ok {
			return specialized, true
		}
	}
	if specialized, ok := a.functionValueTypeForExpr(actualExpr); ok {
		if next, changed := a.specializeFunctionValueType(expected, specialized); changed {
			return next, true
		}
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingTypeFromExpr(tt.Base, actualExpr)
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(field.Type, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		regionBindings := regionBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			expectedFieldType := field.Type
			if len(bindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, regionBindings, nil)
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(expectedFieldType, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}
