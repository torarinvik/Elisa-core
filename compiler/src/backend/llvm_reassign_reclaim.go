//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// emitReassignReclaim returns the old buffer of a reassigned darray local to its arena's free list
// (semantic.Result.ReassignReclaims proves nothing else references it). It runs after the new value
// has been computed and before it is stored, so the old header is still in the slot. A darray whose
// growth arena cannot be resolved here is simply not reclaimed.
func (s *functionState) emitReassignReclaim(n *ast.AssignStmt, slotPtr C.LLVMValueRef, targetType semantic.Type) error {
	darrayType, ok := semantic.StripAggregateStateType(targetType).(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil
	}
	owner, ok := s.darrayGrowthOwner(n.Value, n.Target, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "reclaim.owner.arena")
		if err != nil {
			return err
		}
		owner.arenaRef = arenaRef
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(slotPtr, darrayType)
	if err != nil {
		return err
	}
	capacityPtr, usizeType, err := s.emitBuiltinDArrayCapacityPtr(slotPtr, darrayType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	oldItems := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("reclaim.old.items"))
	oldCapacity := C.LLVMBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree("reclaim.old.capacity"))
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), oldItems, C.LLVMConstPointerNull(voidPtrType), cStringFree("reclaim.old.null"))
	freeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reclaim.free"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reclaim.cont"))
	C.LLVMBuildCondBr(s.builder, isNull, contBB, freeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, freeBB)
	oldBytes, err := s.emitCheckedElemByteCount(oldCapacity, elemSize, "reclaim.old")
	if err != nil {
		return err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	reclaimType := s.g.cachedRuntimeHelperType("arena_reclaim_bytes", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_reclaim_bytes", Params: []semantic.Type{arenaRefType, voidRefType, usizeType}, Return: s.g.result.NamedTypes["void"]}
	})
	callee, err := s.g.ensureFunctionDeclared("arena_reclaim_bytes", reclaimType)
	if err != nil {
		return err
	}
	llvmType, err := s.g.lowerFunctionType(reclaimType)
	if err != nil {
		return err
	}
	s.buildCall(llvmType, callee, []C.LLVMValueRef{owner.arenaRef, oldItems, oldBytes}, "")
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
