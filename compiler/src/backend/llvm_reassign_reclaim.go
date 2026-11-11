//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// emitReassignReclaim returns the old buffer of a reassigned darray local to its arena's free list
// (semantic.Result.ReassignReclaims proves nothing else references it). It runs after the new value
// has been computed and before it is stored, so the old header is still in the slot. A darray whose
// growth arena cannot be resolved here is simply not reclaimed.
func (s *functionState) emitReassignReclaim(n *ast.AssignStmt, slotPtr llvmIRValueRef, targetType semantic.Type) error {
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
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	oldItems := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("reclaim.old.items"))
	oldCapacity := llvmBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree("reclaim.old.capacity"))
	isNull := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), oldItems, llvmConstPointerNull(voidPtrType), cStringFree("reclaim.old.null"))
	freeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reclaim.free"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reclaim.cont"))
	llvmBuildCondBr(s.builder, isNull, contBB, freeBB)

	llvmPositionBuilderAtEnd(s.builder, freeBB)
	oldBytes, err := s.emitCheckedElemByteCount(oldCapacity, elemSize, "reclaim.old")
	if err != nil {
		return err
	}
	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	reclaimType := s.g.cachedRuntimeHelperType("arena_reclaim_bytes", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_reclaim_bytes", Params: []semantic.Type{arenaRefType, voidRefType, usizeType}, Return: s.g.namedType("void")}
	})
	callee, err := s.g.ensureFunctionDeclared("arena_reclaim_bytes", reclaimType)
	if err != nil {
		return err
	}
	llvmType, err := s.g.lowerFunctionType(reclaimType)
	if err != nil {
		return err
	}
	s.buildCall(llvmType, callee, []llvmIRValueRef{owner.arenaRef, oldItems, oldBytes}, "")
	llvmBuildBr(s.builder, contBB)

	llvmPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
