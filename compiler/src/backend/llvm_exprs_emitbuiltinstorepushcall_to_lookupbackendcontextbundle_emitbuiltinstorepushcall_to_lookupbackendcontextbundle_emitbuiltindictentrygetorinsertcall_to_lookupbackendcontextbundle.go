//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinDictEntryGetOrInsertCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry get_or_insert expects 1 argument, got %d", len(expr.Args))
	}
	// Region-parameterized dict: prefer the dict's own region arena (if live in
	// s.regions) over the ambient scope, mirroring darray push / dict insert.
	owner, ok := s.regionArenaOwner(entryType.Dict.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("dict entry get_or_insert"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict.entry.get_or_insert.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	var entryPtr llvmIRValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict, true)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue llvmIRValueRef
	var valuePtrPtr llvmIRValueRef
	if entryPtr != nil {
		dictPtr := llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.get_or_insert.dict.ptr"))
		keyPtr := llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.get_or_insert.key.ptr"))
		valuePtrPtr = llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.get_or_insert.value.ptr"))
		dictValue = llvmBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = llvmBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = llvmBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.get_or_insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = llvmBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = llvmBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = llvmBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.get_or_insert.cached"))
	}
	nullValue := llvmConstNull(valueRefLLVMType)
	hasCached := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), cachedValue, nullValue, cStringFree("dict.entry.get_or_insert.has"))
	nonNullBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.nonnull"))
	insertBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.insert"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.merge"))
	llvmBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	llvmPositionBuilderAtEnd(s.builder, nonNullBB)
	llvmBuildBr(s.builder, mergeBB)
	nonNullEnd := llvmGetInsertBlock(s.builder)
	llvmPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertCallee, getOrInsertType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_dict_get_or_insert"), map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertLLVMType, err := s.g.lowerFunctionType(getOrInsertType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(getOrInsertLLVMType, getOrInsertCallee, []llvmIRValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.get_or_insert.result")
	if valuePtrPtr != nil {
		llvmBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	llvmBuildBr(s.builder, mergeBB)
	insertEnd := llvmGetInsertBlock(s.builder)
	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	phi := llvmBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.get_or_insert.phi"))
	llvmAddIncoming(phi, llvmValueSlicePtr([]llvmIRValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]llvmIRBlockRef{nonNullEnd, insertEnd}), 2)
	return phi, valueRefType, true, nil
}
func (s *functionState) emitBuiltinDictEntryFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(expr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	entryValue, err := s.emitBuiltinDictEntryValue(expr.Object, entryType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict, true)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	switch expr.Field {
	case "value":
		value := llvmBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.value"))
		return value, valueRefType, true, nil
	case "found":
		value := llvmBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.found.value"))
		nullValue := llvmConstNull(valueRefLLVMType)
		found := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), value, nullValue, cStringFree("dict.entry.found"))
		return found, s.g.namedType("bool"), true, nil
	default:
		return nil, nil, false, nil
	}
}

// backendStructLikeValueType reports whether t is a by-value aggregate (struct or
// generic struct instance) for which a reference argument should be auto-dereferenced
// when fed to a by-value parameter.
func backendStructLikeValueType(t semantic.Type) bool {
	switch semantic.StripAggregateStateType(t).(type) {
	case *semantic.StructType:
		return true
	case *semantic.GenericInstanceType:
		return true
	}
	return false
}

func (s *functionState) emitCallArg(arg ast.Expr, expected semantic.Type, fnType *semantic.FuncType, index int) (llvmIRValueRef, semantic.Type, error) {
	// A region-polymorphic callee's hidden __region_auto argument normally comes
	// from the caller's visible region binding. While a synthesized auto region
	// emits a non-escaping region-carrying local initializer, however, the current
	// tree owner is the function-local/private scratch arena and the hidden argument
	// must follow that owner. If it is left on the caller arena, the callee's result
	// is allocated immediately after caller-owned reserve/fixed storage and a later
	// growth can reject the non-tail realloc. Scalar initializers are excluded: a
	// region-polymorphic call used for a side effect must retain the caller binding.
	if value, ok, err := s.regionPolyImplicitArgForActiveInitializer(expected, fnType, index); ok {
		return value, expected, err
	}
	if s != nil && fnType != nil && fnType.SinkParamsKnown && index >= 0 && index < len(fnType.SinkParams) && fnType.SinkParams[index] {
		if operand, moved := backendExplicitMoveOperand(arg); moved {
			return s.emitMovedValue(operand, expected)
		}
		return s.emitMovedValue(arg, expected)
	}
	if expectedRef, ok := expected.(*semantic.RefType); ok && expectedRef != nil {
		actual := s.exprType(arg)
		if actual != nil && !semantic.AssignableTo(expected, actual) && semantic.AssignableTo(expectedRef.Elem, actual) {
			ptr, valueType, err := s.emitValueAddress(arg)
			if err == nil {
				return ptr, &semantic.RefType{
					Elem:            valueType,
					Mutable:         expectedRef.Mutable,
					State:           semantic.RefStateNonNull,
					Storage:         expectedRef.Storage,
					ExplicitStorage: expectedRef.ExplicitStorage,
				}, nil
			}
		}
	} else if expected != nil && backendStructLikeValueType(expected) {
		// Symmetric autoderef (mirror of the autoref branch above): a by-VALUE struct
		// parameter fed a REFERENCE argument whose pointee is that struct — load the
		// pointee. Reaches the generic-dispatch case where a `mutable R&` receiver is
		// passed to a protocol method whose `self` is by value (`self: Self`); the
		// numeric/bool ref→value path stays in coerceValue (with its uintptr nuance).
		actual := s.exprType(arg)
		if actualRef, ok := actual.(*semantic.RefType); ok && actualRef != nil &&
			!semantic.AssignableTo(expected, actual) && semantic.AssignableTo(expected, actualRef.Elem) {
			ptr, _, err := s.emitExpr(arg, actual)
			if err != nil {
				return nil, nil, err
			}
			loaded, err := s.loadValue(ptr, actualRef.Elem, "ref.deref")
			if err != nil {
				return nil, nil, err
			}
			return loaded, actualRef.Elem, nil
		}
	}
	return s.emitExpr(arg, expected)
}

func (s *functionState) regionPolyImplicitArgForActiveInitializer(expected semantic.Type, fnType *semantic.FuncType, index int) (llvmIRValueRef, bool, error) {
	if s == nil || fnType == nil || index < 0 {
		return nil, false, nil
	}
	explicitCount := backendExplicitParamCount(fnType, s.decl)
	implicitIndex := index - explicitCount
	if implicitIndex < 0 || implicitIndex >= len(fnType.ImplicitParamNames) || fnType.ImplicitParamNames[implicitIndex] != semantic.RegionPolymorphicImplicitParamName {
		return nil, false, nil
	}
	// A region-carrying local initializer inside a synthesized auto region uses that region's
	// current owner. In an adopted region this is either the caller owner or the private scratch
	// owner selected for the current initializer; in an ordinary inferred function it is the
	// function-local auto arena. Calls used only for scalar side effects deliberately fall through
	// to their analyzer-resolved caller binding.
	if s.activeSynthesizedAutoRegion && s.activeInitializerCarriesRegionStorage && (s.treeAllocOwner.arenaRef != nil || s.treeAllocOwner.arenaRefPtr != nil) {
		if s.callerStorageArenas != nil {
			if arena, ok := s.callerStorageArenas[s.activeInitializerName]; ok && arena != nil {
				return arena, true, nil
			}
		}
		value, err := s.treeOwnerArenaRefValue(s.treeAllocOwner, "auto.region")
		if err != nil {
			return nil, true, err
		}
		if value == nil {
			return nil, true, fmt.Errorf("active auto-region owner has no Arena value")
		}
		return value, true, nil
	}
	return nil, false, nil
}
