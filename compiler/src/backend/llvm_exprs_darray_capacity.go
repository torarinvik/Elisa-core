//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinDArrayReserveCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
func (s *functionState) emitBuiltinDArrayResizeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
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
func (s *functionState) emitDArrayResizeZeroFillTail(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType, countPtr C.LLVMValueRef, neededValue C.LLVMValueRef) error {
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	oldCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.resize.oldcount"))
	grows := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGT), neededValue, oldCount, cStringFree("darray.resize.grows"))
	rawDelta := C.LLVMBuildSub(s.builder, neededValue, oldCount, cStringFree("darray.resize.rawdelta"))
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	deltaCount := C.LLVMBuildSelect(s.builder, grows, rawDelta, zero, cStringFree("darray.resize.delta"))

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
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.resize.items"))
	i8Type := C.LLVMInt8TypeInContext(s.g.context)
	tailPtr := C.LLVMBuildGEP2(s.builder, i8Type, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{offsetBytes}), 1, cStringFree("darray.resize.tailptr"))

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
	fillZero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	fillValue, err := s.coerceValue(fillZero, s.g.namedType("i32"), memsetValueType)
	if err != nil {
		return err
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []C.LLVMValueRef{tailPtr, fillValue, tailBytes}, "darray.resize.zerofill")
	return nil
}
func (s *functionState) emitBuiltinDArrayClearCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLLVMType, 0, 0), countPtr)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayTruncateCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.truncate.count"))
	limitValue, _, err := s.emitExpr(expr.Args[0], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	shouldStore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), limitValue, currentCount, cStringFree("darray.truncate.lt"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.store"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.merge"))
	C.LLVMBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	C.LLVMBuildStore(s.builder, limitValue, countPtr)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	_ = entryBlock
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
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
func (s *functionState) emitBuiltinDArrayCountPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 1, cStringFree("darray.count.ptr"))
	return countPtr, s.g.namedType("usize"), nil
}
func (s *functionState) emitBuiltinDArrayItemsPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, error) {
	if darrayType == nil {
		return nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 0, cStringFree("darray.items.ptr")), nil
}
func (s *functionState) emitBuiltinDArrayCapacityPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 2, cStringFree("darray.capacity.ptr"))
	return capacityPtr, s.g.namedType("usize"), nil
}
func (s *functionState) emitBuiltinStoreReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
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
func (s *functionState) emitBuiltinStoreFieldDArrayPtr(storePtr C.LLVMValueRef, storeType semantic.Type, fieldName string) (C.LLVMValueRef, *semantic.DArrayType, error) {
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
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, storePtr, C.unsigned(index), cStringFree("store."+fieldName+".ptr"))
	return fieldPtr, darrayType, nil
}
func (s *functionState) emitBuiltinDArrayEnsureCapacity(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType, arenaRef C.LLVMValueRef, neededValue C.LLVMValueRef, name string) error {
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
	currentCapacity := C.LLVMBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree(name+".capacity"))
	hasCapacity := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGE), currentCapacity, neededValue, cStringFree(name+".capacity.ok"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cont"))
	C.LLVMBuildCondBr(s.builder, hasCapacity, contBB, growBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	// Keep the compiler's builtin push lowering in lockstep with
	// elisacore_std.collections' ARENA_DA_INIT_CAP. A stale 256 here defeats
	// the runtime's small-seed policy and multiplies peak memory during the
	// recursive AST construction that motivated that policy.
	initCap := C.LLVMConstInt(usizeLLVMType, 8, 0)
	doubledOrNeeded, err := s.emitSafeDoubledCapacity(currentCapacity, neededValue, usizeLLVMType, name)
	if err != nil {
		return err
	}
	baseCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubledOrNeeded,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), baseCapacity, neededValue, cStringFree(name+".capacity.lt")),
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
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	currentItems := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentItems, C.LLVMConstPointerNull(voidPtrType), cStringFree(name+".items.null"))

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
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	reallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".realloc"))
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".store"))
	C.LLVMBuildCondBr(s.builder, isNull, allocBB, reallocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newSize}, name+".alloc")
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, reallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, currentItems, oldSize, newSize}, name+".realloc")
	reallocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	newItems := C.LLVMBuildPhi(s.builder, voidPtrType, cStringFree(name+".items.new"))
	C.LLVMAddIncoming(newItems, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
	C.LLVMBuildStore(s.builder, newItems, itemsPtr)
	C.LLVMBuildStore(s.builder, newCapacity, capacityPtr)
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
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
