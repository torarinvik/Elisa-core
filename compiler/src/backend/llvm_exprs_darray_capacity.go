//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinDArrayReserveCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray reserve expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.darrayGrowthOwner(expr, fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		fnName := "<unknown>"
		if s.decl != nil {
			fnName = s.decl.Name
		}
		return nil, nil, true, fmt.Errorf("%s (in function %q, expr at %v)", semantic.NoGrowthRegionMessage("darray reserve"), fnName, expr.Pos())
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.reserve.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, err := s.emitReserveBoundExpr(expr.Args[0], "darray.reserve.bound")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.reserve"); err != nil {
		return nil, nil, true, err
	}
	return darrayPtr, resultType, true, nil
}

// emitBuiltinDArrayResizeCall lowers `da.resize(n)`: ensure capacity >= n (one growth, like
// reserve) then seal count := n (like clear, but storing n instead of 0). The result is a
// fixed-length array filled by index — no per-element push.
func (s *functionState) emitBuiltinDArrayResizeCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "resize" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray resize expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.darrayGrowthOwner(expr, fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("darray resize"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.resize.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, err := s.emitReserveBoundExpr(expr.Args[0], "darray.resize.bound")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.resize"); err != nil {
		return nil, nil, true, err
	}
	countPtr, _, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	// The bump allocator does not zero, so growing count past the old length would
	// otherwise expose recycled (non-zero) heap bytes as live elements. For element
	// types whose uninitialized bytes are unsafe to observe as a valid value
	// (references, handles, nested dynamic containers, enum payloads) this is a
	// no-Unsafe-needed use of uninitialized memory — a wild pointer/handle (deep
	// audit #9). Zero-fill the newly-live tail so any read-before-write sees a
	// null/empty value (trap-on-deref) rather than stale garbage. Plain POD element
	// types keep the fast path (no fill); their fill-by-index loop initializes them.
	if darrayElemNeedsZeroInit(darrayType.Elem, map[semantic.Type]bool{}) {
		if err := s.emitDArrayResizeZeroFillTail(darrayPtr, darrayType, countPtr, neededValue); err != nil {
			return nil, nil, true, err
		}
	}
	llvmBuildStore(s.builder, neededValue, countPtr)
	return darrayPtr, resultType, true, nil
}

// darrayElemNeedsZeroInit reports whether an uninitialized value of t is unsafe to
// observe — i.e. it contains a reference, handle, or dynamic-container header whose
// stale bytes would be interpreted as a valid pointer. Pure numeric/bool aggregates
// return false (their uninitialized bytes are a benign garbage scalar, and resize is
// always paired with an index-fill that writes them).
func darrayElemNeedsZeroInit(t semantic.Type, seen map[semantic.Type]bool) bool {
	if t == nil {
		return false
	}
	switch tt := t.(type) {
	case *semantic.RefType, *semantic.PackedEnumStoreType,
		*semantic.DArrayType, *semantic.DictType, *semantic.SetType,
		*semantic.ViewType, *semantic.SViewType, *semantic.CStrType,
		*semantic.PackedVariantViewType:
		return true
	case *semantic.OptionalType:
		return darrayElemNeedsZeroInit(tt.Value, seen)
	case *semantic.ArrayType:
		return darrayElemNeedsZeroInit(tt.Elem, seen)
	case *semantic.StructType:
		if seen[t] {
			return false
		}
		seen[t] = true
		for _, field := range tt.Fields {
			if darrayElemNeedsZeroInit(field.Type, seen) {
				return true
			}
		}
		return false
	case *semantic.EnumType:
		if seen[t] {
			return false
		}
		seen[t] = true
		if tt.Packed {
			return true
		}
		for _, field := range tt.Common {
			if darrayElemNeedsZeroInit(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if darrayElemNeedsZeroInit(payload, seen) {
					return true
				}
			}
		}
		return false
	case *semantic.GenericInstanceType:
		if seen[t] {
			return false
		}
		seen[t] = true
		if base, ok := tt.Base.(*semantic.StructType); ok {
			for _, field := range base.Fields {
				if darrayElemNeedsZeroInit(field.Type, seen) {
					return true
				}
			}
			return false
		}
		// Unresolved generics may hide a pointer — zero-fill conservatively.
		return true
	case *semantic.TypeParamType:
		return true
	}
	return false
}

// emitDArrayResizeZeroFillTail zeroes the byte range [oldCount, neededCount) of a
// darray's backing store. The capacity is already >= neededCount (caller ran
// emitBuiltinDArrayEnsureCapacity). A memset of a zero-length tail (shrink/no-op) is
// harmless, so the only guard needed is a saturating subtraction.
func (s *functionState) emitDArrayResizeZeroFillTail(darrayPtr llvmIRValueRef, darrayType *semantic.DArrayType, countPtr llvmIRValueRef, neededValue llvmIRValueRef) error {
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	oldCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.resize.oldcount"))
	grows := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntUGT), neededValue, oldCount, cStringFree("darray.resize.grows"))
	rawDelta := llvmBuildSub(s.builder, neededValue, oldCount, cStringFree("darray.resize.rawdelta"))
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	deltaCount := llvmBuildSelect(s.builder, grows, rawDelta, zero, cStringFree("darray.resize.delta"))

	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return err
	}
	offsetBytes, err := s.emitCheckedElemByteCount(oldCount, elemSize, "darray.resize.offset")
	if err != nil {
		return err
	}
	tailBytes, err := s.emitCheckedElemByteCount(deltaCount, elemSize, "darray.resize.tail")
	if err != nil {
		return err
	}

	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.resize.items"))
	i8Type := llvmInt8TypeInContext(s.g.context)
	tailPtr := llvmBuildGEP2(s.builder, i8Type, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{offsetBytes}), 1, cStringFree("darray.resize.tailptr"))

	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	// Match libc's `memset(void*, int, size_t)` ABI: the fill byte is i32, not
	// Elisa's 64-bit `int`.
	memsetValueType := s.g.namedType("i32")
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, usizeType}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return err
	}
	memsetLLVMType, err := s.g.lowerFunctionType(memsetType)
	if err != nil {
		return err
	}
	fillZero := llvmConstInt(llvmInt32TypeInContext(s.g.context), 0, 0)
	fillValue, err := s.coerceValue(fillZero, s.g.namedType("i32"), memsetValueType)
	if err != nil {
		return err
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []llvmIRValueRef{tailPtr, fillValue, tailBytes}, "darray.resize.zerofill")
	return nil
}
func (s *functionState) emitBuiltinDArrayClearCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("darray clear expects 0 arguments, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	llvmBuildStore(s.builder, llvmConstInt(usizeLLVMType, 0, 0), countPtr)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayTruncateCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray truncate expects 1 argument, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.truncate.count"))
	limitValue, _, err := s.emitExpr(expr.Args[0], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	shouldStore := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), limitValue, currentCount, cStringFree("darray.truncate.lt"))
	entryBlock := llvmGetInsertBlock(s.builder)
	storeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.store"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.merge"))
	llvmBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, storeBB)
	llvmBuildStore(s.builder, limitValue, countPtr)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	_ = entryBlock
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (llvmIRValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	resultType := &semantic.RefType{Elem: s.exprType(receiver), Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	return ptr, resultType, nil
}
func (s *functionState) emitBuiltinDArrayCountPtr(darrayPtr llvmIRValueRef, darrayType *semantic.DArrayType) (llvmIRValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	countPtr := llvmBuildStructGEP2(s.builder, containerType, darrayPtr, 1, cStringFree("darray.count.ptr"))
	return countPtr, s.g.namedType("usize"), nil
}
func (s *functionState) emitBuiltinDArrayItemsPtr(darrayPtr llvmIRValueRef, darrayType *semantic.DArrayType) (llvmIRValueRef, error) {
	if darrayType == nil {
		return nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, err
	}
	return llvmBuildStructGEP2(s.builder, containerType, darrayPtr, 0, cStringFree("darray.items.ptr")), nil
}
func (s *functionState) emitBuiltinDArrayCapacityPtr(darrayPtr llvmIRValueRef, darrayType *semantic.DArrayType) (llvmIRValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	capacityPtr := llvmBuildStructGEP2(s.builder, containerType, darrayPtr, 2, cStringFree("darray.capacity.ptr"))
	return capacityPtr, s.g.namedType("usize"), nil
}
func (s *functionState) emitBuiltinStoreReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (llvmIRValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType.Elem, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	return ptr, s.exprType(receiver), nil
}
func (s *functionState) emitBuiltinStoreFieldDArrayPtr(storePtr llvmIRValueRef, storeType semantic.Type, fieldName string) (llvmIRValueRef, *semantic.DArrayType, error) {
	fieldType, index, _, _, err := s.g.fieldInfo(storeType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	darrayType, ok := fieldType.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, fmt.Errorf("store field %q is not a darray column", fieldName)
	}
	containerType, err := s.g.lowerType(storeType)
	if err != nil {
		return nil, nil, err
	}
	fieldPtr := llvmBuildStructGEP2(s.builder, containerType, storePtr, llvmIRUnsigned(index), cStringFree("store."+fieldName+".ptr"))
	return fieldPtr, darrayType, nil
}
func (s *functionState) emitBuiltinDArrayEnsureCapacity(darrayPtr llvmIRValueRef, darrayType *semantic.DArrayType, arenaRef llvmIRValueRef, neededValue llvmIRValueRef, name string) error {
	if darrayType == nil {
		return fmt.Errorf("missing darray type")
	}
	capacityPtr, usizeType, err := s.emitBuiltinDArrayCapacityPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	currentCapacity := llvmBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree(name+".capacity"))
	hasCapacity := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntUGE), currentCapacity, neededValue, cStringFree(name+".capacity.ok"))
	growBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cont"))
	llvmBuildCondBr(s.builder, hasCapacity, contBB, growBB)

	llvmPositionBuilderAtEnd(s.builder, growBB)
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	// Keep the compiler's builtin push lowering in lockstep with
	// elisacore_std.collections' ARENA_DA_INIT_CAP. A stale 256 here defeats
	// the runtime's small-seed policy and multiplies peak memory during the
	// recursive AST construction that motivated that policy.
	initCap := llvmConstInt(usizeLLVMType, 8, 0)
	doubledOrNeeded, err := s.emitSafeDoubledCapacity(currentCapacity, neededValue, usizeLLVMType, name)
	if err != nil {
		return err
	}
	baseCapacity := llvmBuildSelect(
		s.builder,
		llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubledOrNeeded,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := llvmBuildSelect(
		s.builder,
		llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), baseCapacity, neededValue, cStringFree(name+".capacity.lt")),
		neededValue,
		baseCapacity,
		cStringFree(name+".capacity.new"),
	)
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return err
	}
	oldSize, err := s.emitCheckedElemByteCount(currentCapacity, elemSizeBytes, name+".old")
	if err != nil {
		return err
	}
	newSize, err := s.emitCheckedElemByteCount(newCapacity, elemSizeBytes, name+".new")
	if err != nil {
		return err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	currentItems := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	isNull := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), currentItems, llvmConstPointerNull(voidPtrType), cStringFree(name+".items.null"))

	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	reallocType := s.g.cachedRuntimeHelperType("arena_realloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_realloc", Params: []semantic.Type{arenaRefType, voidRefType, usizeType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return err
	}
	reallocCallee, err := s.g.ensureFunctionDeclared("arena_realloc", reallocType)
	if err != nil {
		return err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return err
	}
	reallocLLVMType, err := s.g.lowerFunctionType(reallocType)
	if err != nil {
		return err
	}
	allocBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	reallocBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".realloc"))
	storeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".store"))
	llvmBuildCondBr(s.builder, isNull, allocBB, reallocBB)

	llvmPositionBuilderAtEnd(s.builder, allocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []llvmIRValueRef{arenaRef, newSize}, name+".alloc")
	allocEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, storeBB)

	llvmPositionBuilderAtEnd(s.builder, reallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []llvmIRValueRef{arenaRef, currentItems, oldSize, newSize}, name+".realloc")
	reallocEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, storeBB)

	llvmPositionBuilderAtEnd(s.builder, storeBB)
	newItems := llvmBuildPhi(s.builder, voidPtrType, cStringFree(name+".items.new"))
	llvmAddIncoming(newItems, llvmValueSlicePtr([]llvmIRValueRef{allocated, reallocated}), llvmBlockSlicePtr([]llvmIRBlockRef{allocEnd, reallocEnd}), 2)
	llvmBuildStore(s.builder, newItems, itemsPtr)
	llvmBuildStore(s.builder, newCapacity, capacityPtr)
	llvmBuildBr(s.builder, contBB)

	llvmPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
func builtinStoreResultRefType(storeType *semantic.StructType) *semantic.RefType {
	return &semantic.RefType{Elem: storeType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}
func builtinStorePushResultType(storeType *semantic.StructType, u32Type semantic.Type) semantic.Type {
	if storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		return &semantic.IDType{Tag: storeType, Storage: u32Type}
	}
	return builtinStoreResultRefType(storeType)
}
