//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTreeTableSetCapacity(tablePtr C.LLVMValueRef, memberType semantic.Type, capacityValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return err
	}
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 1, cStringFree(name+".capacity.ptr"))
	C.LLVMBuildStore(s.builder, capacityValue, capacityPtr)
	return nil
}
func treeInitialRowCapacity(rowSizeBytes uint64) uint64 {
	switch {
	case rowSizeBytes == 0:
		return 0
	case rowSizeBytes <= 16:
		return 16
	case rowSizeBytes <= 64:
		return 8
	case rowSizeBytes <= 256:
		return 4
	default:
		return 2
	}
}

type treeExactAppendSlot struct {
	tablePtr    C.LLVMValueRef
	rowIndex    C.LLVMValueRef
	neededCount C.LLVMValueRef
}

type treeCategoryUnionAppendSlot struct {
	tablePtr    C.LLVMValueRef
	rowIndex    C.LLVMValueRef
	neededCount C.LLVMValueRef
}

type treeRootUnionAppendSlot struct {
	tablePtr    C.LLVMValueRef
	rowIndex    C.LLVMValueRef
	neededCount C.LLVMValueRef
}

func (s *functionState) emitTreeExactAppendSlot(arenaValue C.LLVMValueRef, stateValue C.LLVMValueRef, family *semantic.TreeType, memberType semantic.Type, name string) (treeExactAppendSlot, error) {
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, name)
	if err != nil {
		return treeExactAppendSlot{}, err
	}
	rowIndex, err := s.emitTreeTableCountValue(tablePtr, memberType, name)
	if err != nil {
		return treeExactAppendSlot{}, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return treeExactAppendSlot{}, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree(name+".needed"))
	if err := s.emitTreeEnsureTableCapacity(arenaValue, tablePtr, memberType, neededCount, name); err != nil {
		return treeExactAppendSlot{}, err
	}
	return treeExactAppendSlot{tablePtr: tablePtr, rowIndex: rowIndex, neededCount: neededCount}, nil
}

func (s *functionState) emitTreeCategoryUnionAppendSlot(arenaValue C.LLVMValueRef, stateValue C.LLVMValueRef, family *semantic.TreeType, category *semantic.TreeCategoryType, name string) (treeCategoryUnionAppendSlot, error) {
	tablePtr, err := s.emitTreeCategoryUnionTablePtr(stateValue, family, category, name)
	if err != nil {
		return treeCategoryUnionAppendSlot{}, err
	}
	rowIndex, err := s.emitTreeCategoryUnionTableCountValue(tablePtr, category, name)
	if err != nil {
		return treeCategoryUnionAppendSlot{}, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return treeCategoryUnionAppendSlot{}, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree(name+".needed"))
	if err := s.emitTreeEnsureCategoryUnionTableCapacity(arenaValue, tablePtr, category, neededCount, name); err != nil {
		return treeCategoryUnionAppendSlot{}, err
	}
	return treeCategoryUnionAppendSlot{tablePtr: tablePtr, rowIndex: rowIndex, neededCount: neededCount}, nil
}

func (s *functionState) emitTreeRootUnionAppendSlot(arenaValue C.LLVMValueRef, stateValue C.LLVMValueRef, family *semantic.TreeType, name string) (treeRootUnionAppendSlot, error) {
	tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, family, name)
	if err != nil {
		return treeRootUnionAppendSlot{}, err
	}
	rowIndex, err := s.emitTreeRootUnionTableCountValue(tablePtr, family, name)
	if err != nil {
		return treeRootUnionAppendSlot{}, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return treeRootUnionAppendSlot{}, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree(name+".needed"))
	if err := s.emitTreeEnsureRootUnionTableCapacity(arenaValue, tablePtr, family, neededCount, name); err != nil {
		return treeRootUnionAppendSlot{}, err
	}
	return treeRootUnionAppendSlot{tablePtr: tablePtr, rowIndex: rowIndex, neededCount: neededCount}, nil
}

func (s *functionState) emitTreeCategoryUnionTableSetCapacity(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, capacityValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetCapacity(tableType, tablePtr, capacityValue, name)
}

func (s *functionState) emitTreeCategoryUnionTableSetCount(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, countValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetCount(tableType, tablePtr, countValue, name)
}

func (s *functionState) emitTreeCategoryUnionTableCountValue(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTableCountValue(tableType, tablePtr, name)
}

func (s *functionState) emitTreeCategoryUnionTableCapacityValue(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTableCapacityValue(tableType, tablePtr, name)
}

func (s *functionState) emitTreeCategoryUnionKindsPointerValue(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTablePointerValue(tableType, tablePtr, 2, name+".kinds")
}

func (s *functionState) emitTreeCategoryUnionPayloadsPointerValue(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTablePointerValue(tableType, tablePtr, 3, name+".payloads")
}

func (s *functionState) emitTreeCategoryUnionTableSetKindsPointer(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, value C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetPointer(tableType, tablePtr, 2, value, name+".kinds")
}

func (s *functionState) emitTreeCategoryUnionTableSetPayloadsPointer(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, value C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeCategoryUnionTableType(category)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetPointer(tableType, tablePtr, 3, value, name+".payloads")
}

func (s *functionState) emitTreeRootUnionTableSetCapacity(tablePtr C.LLVMValueRef, family *semantic.TreeType, capacityValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetCapacity(tableType, tablePtr, capacityValue, name)
}

func (s *functionState) emitTreeRootUnionTableSetCount(tablePtr C.LLVMValueRef, family *semantic.TreeType, countValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetCount(tableType, tablePtr, countValue, name)
}

func (s *functionState) emitTreeRootUnionTableCountValue(tablePtr C.LLVMValueRef, family *semantic.TreeType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTableCountValue(tableType, tablePtr, name)
}

func (s *functionState) emitTreeRootUnionTableCapacityValue(tablePtr C.LLVMValueRef, family *semantic.TreeType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTableCapacityValue(tableType, tablePtr, name)
}

func (s *functionState) emitTreeRootUnionKindsPointerValue(tablePtr C.LLVMValueRef, family *semantic.TreeType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTablePointerValue(tableType, tablePtr, 2, name+".kinds")
}

func (s *functionState) emitTreeRootUnionPayloadsPointerValue(tablePtr C.LLVMValueRef, family *semantic.TreeType, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return nil, err
	}
	return s.emitDenseTreeTablePointerValue(tableType, tablePtr, 3, name+".payloads")
}

func (s *functionState) emitTreeRootUnionTableSetKindsPointer(tablePtr C.LLVMValueRef, family *semantic.TreeType, value C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetPointer(tableType, tablePtr, 2, value, name+".kinds")
}

func (s *functionState) emitTreeRootUnionTableSetPayloadsPointer(tablePtr C.LLVMValueRef, family *semantic.TreeType, value C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeRootUnionTableType(family)
	if err != nil {
		return err
	}
	return s.emitDenseTreeTableSetPointer(tableType, tablePtr, 3, value, name+".payloads")
}

func (s *functionState) emitDenseTreeTableSetCapacity(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, capacityValue C.LLVMValueRef, name string) error {
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 1, cStringFree(name+".capacity.ptr"))
	C.LLVMBuildStore(s.builder, capacityValue, capacityPtr)
	return nil
}

func (s *functionState) emitDenseTreeTableSetCount(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, countValue C.LLVMValueRef, name string) error {
	countPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 0, cStringFree(name+".count.ptr"))
	C.LLVMBuildStore(s.builder, countValue, countPtr)
	return nil
}

func (s *functionState) emitDenseTreeTableCountValue(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	countPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 0, cStringFree(name+".count.ptr"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, usizeType, countPtr, cStringFree(name+".count")), nil
}

func (s *functionState) emitDenseTreeTableCapacityValue(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 1, cStringFree(name+".capacity.ptr"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, usizeType, capacityPtr, cStringFree(name+".capacity")), nil
}

func (s *functionState) emitDenseTreeTablePointerValue(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, fieldIndex uint, name string) (C.LLVMValueRef, error) {
	ptr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, C.unsigned(fieldIndex), cStringFree(name+".ptr"))
	return C.LLVMBuildLoad2(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), ptr, cStringFree(name)), nil
}

func (s *functionState) emitDenseTreeTableSetPointer(tableType C.LLVMTypeRef, tablePtr C.LLVMValueRef, fieldIndex uint, value C.LLVMValueRef, name string) error {
	ptr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, C.unsigned(fieldIndex), cStringFree(name+".ptr"))
	C.LLVMBuildStore(s.builder, value, ptr)
	return nil
}

func (s *functionState) emitTreeEnsureTableCapacity(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, memberType semantic.Type, neededCount C.LLVMValueRef, name string) error {
	currentCapacity, err := s.emitTreeTableCapacityValue(tablePtr, memberType, name)
	if err != nil {
		return err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	rowType, err := s.g.ensureTreeExactRowType(memberType)
	if err != nil {
		return err
	}
	rowSizeBytes, err := s.g.abiSizeOfLLVMType(rowType)
	if err != nil {
		return err
	}
	needsGrow := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), currentCapacity, neededCount, cStringFree(name+".grow"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.bb"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.cont"))
	C.LLVMBuildCondBr(s.builder, needsGrow, growBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeType, 0, 0)
	initCap := C.LLVMConstInt(usizeType, C.ulonglong(treeInitialRowCapacity(rowSizeBytes)), 0)
	doubled := C.LLVMBuildMul(s.builder, currentCapacity, C.LLVMConstInt(usizeType, 2, 0), cStringFree(name+".capacity.double"))
	nonZeroCap := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubled,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), nonZeroCap, neededCount, cStringFree(name+".capacity.lt")),
		neededCount,
		nonZeroCap,
		cStringFree(name+".capacity.new"),
	)
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	reallocType := s.g.cachedRuntimeHelperType("arena_realloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_realloc", Params: []semantic.Type{arenaRefType, voidRefType, s.g.result.NamedTypes["usize"], s.g.result.NamedTypes["usize"]}, Return: voidRefType}
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
	if rowSizeBytes == 0 {
		if err := s.emitTreeTableSetCapacity(tablePtr, memberType, newCapacity, name); err != nil {
			return err
		}
		C.LLVMBuildBr(s.builder, contBB)
		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		return nil
	}
	rowSizeValue := C.LLVMConstInt(usizeType, C.ulonglong(rowSizeBytes), 0)
	oldBytes := C.LLVMBuildMul(s.builder, currentCapacity, rowSizeValue, cStringFree(name+".old.bytes"))
	newBytes := C.LLVMBuildMul(s.builder, newCapacity, rowSizeValue, cStringFree(name+".new.bytes"))
	rowsPtr, err := s.emitTreeTableRowsPointerValue(tablePtr, memberType, name)
	if err != nil {
		return err
	}
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), rowsPtr, C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0)), cStringFree(name+".rows.null"))
	rowsAllocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".rows.alloc"))
	rowsReallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".rows.realloc"))
	rowsContBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".rows.cont"))
	C.LLVMBuildCondBr(s.builder, isNull, rowsAllocBB, rowsReallocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rowsAllocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newBytes}, name+".rows.alloc")
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, rowsContBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rowsReallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, rowsPtr, oldBytes, newBytes}, name+".rows.realloc")
	reallocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, rowsContBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rowsContBB)
	rowsValue := C.LLVMBuildPhi(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), cStringFree(name+".rows.phi"))
	C.LLVMAddIncoming(rowsValue, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
	if err := s.emitTreeTableSetRowsPointer(tablePtr, memberType, rowsValue, name); err != nil {
		return err
	}
	s.invalidateTreeExactRowCaches()
	if err := s.emitTreeTableSetCapacity(tablePtr, memberType, newCapacity, name); err != nil {
		return err
	}
	C.LLVMBuildBr(s.builder, contBB)
	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitTreeEnsureCategoryUnionTableCapacity(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, neededCount C.LLVMValueRef, name string) error {
	currentCapacity, err := s.emitTreeCategoryUnionTableCapacityValue(tablePtr, category, name)
	if err != nil {
		return err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return err
	}
	payloadType, err := s.g.ensureTreeCategoryUnionPayloadType(category)
	if err != nil {
		return err
	}
	kindSizeBytes, err := s.g.abiSizeOfLLVMType(kindType)
	if err != nil {
		return err
	}
	payloadSizeBytes, err := s.g.abiSizeOfLLVMType(payloadType)
	if err != nil {
		return err
	}
	needsGrow := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), currentCapacity, neededCount, cStringFree(name+".grow"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.bb"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.cont"))
	C.LLVMBuildCondBr(s.builder, needsGrow, growBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeType, 0, 0)
	initCap := C.LLVMConstInt(usizeType, C.ulonglong(treeInitialRowCapacity(kindSizeBytes+payloadSizeBytes)), 0)
	doubled := C.LLVMBuildMul(s.builder, currentCapacity, C.LLVMConstInt(usizeType, 2, 0), cStringFree(name+".capacity.double"))
	nonZeroCap := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubled,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), nonZeroCap, neededCount, cStringFree(name+".capacity.lt")),
		neededCount,
		nonZeroCap,
		cStringFree(name+".capacity.new"),
	)
	if err := s.emitTreeGrowCategoryUnionPointer(arenaRef, tablePtr, category, currentCapacity, newCapacity, kindSizeBytes, true, name+".kinds"); err != nil {
		return err
	}
	if payloadSizeBytes > 0 {
		if err := s.emitTreeGrowCategoryUnionPointer(arenaRef, tablePtr, category, currentCapacity, newCapacity, payloadSizeBytes, false, name+".payloads"); err != nil {
			return err
		}
	}
	if err := s.emitTreeCategoryUnionTableSetCapacity(tablePtr, category, newCapacity, name); err != nil {
		return err
	}
	C.LLVMBuildBr(s.builder, contBB)
	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitTreeEnsureRootUnionTableCapacity(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, family *semantic.TreeType, neededCount C.LLVMValueRef, name string) error {
	currentCapacity, err := s.emitTreeRootUnionTableCapacityValue(tablePtr, family, name)
	if err != nil {
		return err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return err
	}
	payloadType, err := s.g.ensureTreeRootUnionPayloadType(family)
	if err != nil {
		return err
	}
	kindSizeBytes, err := s.g.abiSizeOfLLVMType(kindType)
	if err != nil {
		return err
	}
	payloadSizeBytes, err := s.g.abiSizeOfLLVMType(payloadType)
	if err != nil {
		return err
	}
	needsGrow := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), currentCapacity, neededCount, cStringFree(name+".grow"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.bb"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.cont"))
	C.LLVMBuildCondBr(s.builder, needsGrow, growBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeType, 0, 0)
	initCap := C.LLVMConstInt(usizeType, C.ulonglong(treeInitialRowCapacity(kindSizeBytes+payloadSizeBytes)), 0)
	doubled := C.LLVMBuildMul(s.builder, currentCapacity, C.LLVMConstInt(usizeType, 2, 0), cStringFree(name+".capacity.double"))
	nonZeroCap := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubled,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), nonZeroCap, neededCount, cStringFree(name+".capacity.lt")),
		neededCount,
		nonZeroCap,
		cStringFree(name+".capacity.new"),
	)
	if err := s.emitTreeGrowRootUnionPointer(arenaRef, tablePtr, family, currentCapacity, newCapacity, kindSizeBytes, true, name+".kinds"); err != nil {
		return err
	}
	if err := s.emitTreeGrowRootUnionPointer(arenaRef, tablePtr, family, currentCapacity, newCapacity, payloadSizeBytes, false, name+".payloads"); err != nil {
		return err
	}
	if err := s.emitTreeRootUnionTableSetCapacity(tablePtr, family, newCapacity, name); err != nil {
		return err
	}
	C.LLVMBuildBr(s.builder, contBB)
	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitTreeGrowCategoryUnionPointer(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, currentCapacity C.LLVMValueRef, newCapacity C.LLVMValueRef, rowSizeBytes uint64, kinds bool, name string) error {
	var loadPointer func() (C.LLVMValueRef, error)
	var storePointer func(C.LLVMValueRef) error
	if kinds {
		loadPointer = func() (C.LLVMValueRef, error) {
			return s.emitTreeCategoryUnionKindsPointerValue(tablePtr, category, name)
		}
		storePointer = func(newPtr C.LLVMValueRef) error {
			return s.emitTreeCategoryUnionTableSetKindsPointer(tablePtr, category, newPtr, name)
		}
	} else {
		loadPointer = func() (C.LLVMValueRef, error) {
			return s.emitTreeCategoryUnionPayloadsPointerValue(tablePtr, category, name)
		}
		storePointer = func(newPtr C.LLVMValueRef) error {
			return s.emitTreeCategoryUnionTableSetPayloadsPointer(tablePtr, category, newPtr, name)
		}
	}
	return s.emitTreeGrowDenseUnionPointer(arenaRef, currentCapacity, newCapacity, rowSizeBytes, name, loadPointer, storePointer)
}

func (s *functionState) emitTreeGrowRootUnionPointer(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, family *semantic.TreeType, currentCapacity C.LLVMValueRef, newCapacity C.LLVMValueRef, rowSizeBytes uint64, kinds bool, name string) error {
	var loadPointer func() (C.LLVMValueRef, error)
	var storePointer func(C.LLVMValueRef) error
	if kinds {
		loadPointer = func() (C.LLVMValueRef, error) {
			return s.emitTreeRootUnionKindsPointerValue(tablePtr, family, name)
		}
		storePointer = func(newPtr C.LLVMValueRef) error {
			return s.emitTreeRootUnionTableSetKindsPointer(tablePtr, family, newPtr, name)
		}
	} else {
		loadPointer = func() (C.LLVMValueRef, error) {
			return s.emitTreeRootUnionPayloadsPointerValue(tablePtr, family, name)
		}
		storePointer = func(newPtr C.LLVMValueRef) error {
			return s.emitTreeRootUnionTableSetPayloadsPointer(tablePtr, family, newPtr, name)
		}
	}
	return s.emitTreeGrowDenseUnionPointer(arenaRef, currentCapacity, newCapacity, rowSizeBytes, name, loadPointer, storePointer)
}

func (s *functionState) emitTreeGrowDenseUnionPointer(arenaRef C.LLVMValueRef, currentCapacity C.LLVMValueRef, newCapacity C.LLVMValueRef, rowSizeBytes uint64, name string, loadPointer func() (C.LLVMValueRef, error), storePointer func(C.LLVMValueRef) error) error {
	if rowSizeBytes == 0 {
		return nil
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	reallocType := s.g.cachedRuntimeHelperType("arena_realloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_realloc", Params: []semantic.Type{arenaRefType, voidRefType, s.g.result.NamedTypes["usize"], s.g.result.NamedTypes["usize"]}, Return: voidRefType}
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
	rowSizeValue := C.LLVMConstInt(usizeType, C.ulonglong(rowSizeBytes), 0)
	oldBytes := C.LLVMBuildMul(s.builder, currentCapacity, rowSizeValue, cStringFree(name+".old.bytes"))
	newBytes := C.LLVMBuildMul(s.builder, newCapacity, rowSizeValue, cStringFree(name+".new.bytes"))
	oldPtr, err := loadPointer()
	if err != nil {
		return err
	}
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), oldPtr, C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0)), cStringFree(name+".null"))
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	reallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".realloc"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cont"))
	C.LLVMBuildCondBr(s.builder, isNull, allocBB, reallocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newBytes}, name+".alloc.call")
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, reallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, oldPtr, oldBytes, newBytes}, name+".realloc.call")
	reallocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	newPtr := C.LLVMBuildPhi(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), cStringFree(name+".phi"))
	C.LLVMAddIncoming(newPtr, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
	if err := storePointer(newPtr); err != nil {
		return err
	}
	s.invalidateDenseTreeValueCaches()
	return nil
}
func (s *functionState) currentBlock() C.LLVMBasicBlockRef {
	if s == nil || s.builder == nil {
		return nil
	}
	return C.LLVMGetInsertBlock(s.builder)
}
func (s *functionState) invalidateTreeExactRowCaches() {
	if s != nil {
		s.treeExactRowPointers = nil
		s.treeExactRowValues = nil
	}
}
func (s *functionState) invalidateDenseTreeValueCaches() {
	if s != nil {
		s.treeDenseKindValues = nil
		s.treeDensePayloadValues = nil
	}
}
func makeDenseTreeValueCacheKey(s *functionState, kind string, tablePtr C.LLVMValueRef, rowIndex C.LLVMValueRef) treeDenseValueCacheKey {
	return treeDenseValueCacheKey{
		block: s.currentBlock(),
		kind:  kind,
		table: tablePtr,
		row:   rowIndex,
	}
}
func (s *functionState) cachedTreeExactRowValue(memberType semantic.Type, tablePtr C.LLVMValueRef, rowIndex C.LLVMValueRef) C.LLVMValueRef {
	if s == nil || s.treeExactRowValues == nil {
		return nil
	}
	return s.treeExactRowValues[makeTreeExactRowCacheKey(s, memberType, tablePtr, rowIndex)]
}
func makeTreeExactRowCacheKey(s *functionState, memberType semantic.Type, tablePtr C.LLVMValueRef, rowIndex C.LLVMValueRef) treeExactRowCacheKey {
	return treeExactRowCacheKey{
		block:      s.currentBlock(),
		memberName: treeExactMemberSurfaceName(memberType),
		table:      tablePtr,
		row:        rowIndex,
	}
}
func treeExactFieldIndex(memberType semantic.Type, fieldName string) (int, semantic.Field, error) {
	for i, fieldDecl := range treeExactFieldDecls(memberType) {
		if fieldDecl.Name != fieldName {
			continue
		}
		field, ok := treeExactFieldInfo(memberType, fieldName)
		if !ok {
			return -1, semantic.Field{}, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldName)
		}
		return i, field, nil
	}
	return -1, semantic.Field{}, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
}
func (s *functionState) emitTreeExactRowPointerAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMTypeRef, error) {
	rowType, err := s.g.ensureTreeExactRowType(memberType)
	if err != nil {
		return nil, nil, err
	}
	cacheKey := makeTreeExactRowCacheKey(s, memberType, tablePtr, rowIndex)
	if cached, ok := s.treeExactRowPointers[cacheKey]; ok && cached != nil {
		return cached, rowType, nil
	}
	rowsPtr, err := s.emitTreeTableRowsPointerValue(tablePtr, memberType, name)
	if err != nil {
		return nil, nil, err
	}
	rowPtr := C.LLVMBuildGEP2(s.builder, rowType, rowsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".row.ptr"))
	if s.treeExactRowPointers == nil {
		s.treeExactRowPointers = map[treeExactRowCacheKey]C.LLVMValueRef{}
	}
	s.treeExactRowPointers[cacheKey] = rowPtr
	return rowPtr, rowType, nil
}
func (s *functionState) emitTreeExactFieldValueAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	fieldIndex, field, err := treeExactFieldIndex(memberType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	if rowValue := s.cachedTreeExactRowValue(memberType, tablePtr, rowIndex); rowValue != nil {
		value, _, err := s.emitTreeExactFieldValueFromRow(memberType, rowValue, fieldName, name)
		if err != nil {
			return nil, nil, err
		}
		return value, field.Type, nil
	}
	elemLLVMType, err := s.g.lowerType(field.Type)
	if err != nil {
		return nil, nil, err
	}
	rowPtr, rowType, err := s.emitTreeExactRowPointerAtIndex(tablePtr, memberType, rowIndex, name)
	if err != nil {
		return nil, nil, err
	}
	elemPtr := C.LLVMBuildStructGEP2(s.builder, rowType, rowPtr, C.unsigned(fieldIndex), cStringFree(name+".elem.ptr"))
	value := C.LLVMBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree(name+".elem"))
	return value, field.Type, nil
}
func (s *functionState) emitTreeExactRowValueAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMTypeRef, error) {
	rowPtr, rowType, err := s.emitTreeExactRowPointerAtIndex(tablePtr, memberType, rowIndex, name)
	if err != nil {
		return nil, nil, err
	}
	cacheKey := makeTreeExactRowCacheKey(s, memberType, tablePtr, rowIndex)
	if cached, ok := s.treeExactRowValues[cacheKey]; ok && cached != nil {
		return cached, rowType, nil
	}
	rowValue := C.LLVMBuildLoad2(s.builder, rowType, rowPtr, cStringFree(name+".row"))
	if s.treeExactRowValues == nil {
		s.treeExactRowValues = map[treeExactRowCacheKey]C.LLVMValueRef{}
	}
	s.treeExactRowValues[cacheKey] = rowValue
	return rowValue, rowType, nil
}
func (s *functionState) emitTreeExactFieldValueFromRow(memberType semantic.Type, rowValue C.LLVMValueRef, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	fieldIndex, field, err := treeExactFieldIndex(memberType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMBuildExtractValue(s.builder, rowValue, C.unsigned(fieldIndex), cStringFree(name+".elem"))
	return value, field.Type, nil
}
func (s *functionState) emitTreeCategoryUnionFieldValueAtIndex(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, variant *semantic.EnumVariant, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if category == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing category-union tree field metadata")
	}
	memberType := category.VariantViewType(variant)
	fieldIndex, field, err := treeExactFieldIndex(memberType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	payloadType, err := s.g.lowerTreeCategoryUnionVariantPayloadType(category, variant)
	if err != nil {
		return nil, nil, err
	}
	if C.LLVMGetTypeKind(payloadType) == C.LLVMVoidTypeKind {
		return nil, nil, fmt.Errorf("%s has no lowered payload field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	payloadValue, _, err := s.emitTreeCategoryUnionPayloadValueAtIndex(tablePtr, category, variant, rowIndex, name)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMBuildExtractValue(s.builder, payloadValue, C.unsigned(fieldIndex), cStringFree(name+".payload.elem"))
	return value, field.Type, nil
}
func (s *functionState) emitTreeCategoryUnionPayloadValueAtIndex(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, variant *semantic.EnumVariant, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMTypeRef, error) {
	if category == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing category-union tree payload metadata")
	}
	payloadType, err := s.g.lowerTreeCategoryUnionVariantPayloadType(category, variant)
	if err != nil {
		return nil, nil, err
	}
	if C.LLVMGetTypeKind(payloadType) == C.LLVMVoidTypeKind {
		return C.LLVMGetUndef(payloadType), payloadType, nil
	}
	cacheKey := makeDenseTreeValueCacheKey(s, "category-payload:"+category.Name+"."+variant.Name, tablePtr, rowIndex)
	if s.treeDensePayloadValues != nil {
		if cached, ok := s.treeDensePayloadValues[cacheKey]; ok && cached != nil {
			return cached, payloadType, nil
		}
	}
	payloadsPtr, err := s.emitTreeCategoryUnionPayloadsPointerValue(tablePtr, category, name)
	if err != nil {
		return nil, nil, err
	}
	payloadRowType, err := s.g.ensureTreeCategoryUnionPayloadType(category)
	if err != nil {
		return nil, nil, err
	}
	payloadRowPtr := C.LLVMBuildGEP2(s.builder, payloadRowType, payloadsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".payload.row.ptr"))
	payloadValue := C.LLVMBuildLoad2(s.builder, payloadType, payloadRowPtr, cStringFree(name+".payload.value"))
	if s.treeDensePayloadValues == nil {
		s.treeDensePayloadValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDensePayloadValues[cacheKey] = payloadValue
	return payloadValue, payloadType, nil
}
func (s *functionState) emitTreeCategoryUnionSurfaceFieldValue(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, variant *semantic.EnumVariant, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	memberType := category.VariantViewType(variant)
	field, ok := semantic.TreeVariantSurfaceFieldInfo(memberType, fieldName)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	value, rawType, err := s.emitTreeCategoryUnionFieldValueAtIndex(tablePtr, category, variant, fieldName, rowIndex, name)
	if err != nil {
		return nil, nil, err
	}
	return s.treeFieldSurfaceValue(value, rawType, field.Type, name)
}
func (s *functionState) emitTreeMemberFieldValueAtHandle(nodeValue C.LLVMValueRef, family *semantic.TreeType, memberType semantic.Type, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	if viewType, ok := semantic.StripAggregateStateType(memberType).(*semantic.TreeVariantViewType); ok && viewType != nil && viewType.Category != nil && treeCategoryLayoutPlan(viewType.Category).isCategoryUnion() {
		access, err := s.emitTreeCategoryUnionTableAccessFromHandle(nodeValue, family, viewType.Category, name)
		if err != nil {
			return nil, nil, err
		}
		return s.emitTreeCategoryUnionFieldValueAtIndex(access.tablePtr, viewType.Category, viewType.Variant, fieldName, access.rowIndex, name)
	}
	switch semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		if family != nil && treeFamilyLayoutPlan(family).isCategoryUnion() {
			stateValue, err := s.emitTreeCategoryUnionContextStateValue(family, name)
			if err != nil {
				return nil, nil, err
			}
			tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, family, name)
			if err != nil {
				return nil, nil, err
			}
			rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, name+".index")
			if err != nil {
				return nil, nil, err
			}
			return s.emitTreeRootUnionExactFieldValueAtIndex(tablePtr, family, memberType, fieldName, rowIndex, name)
		}
	}
	access, err := s.emitTreeExactTableAccessFromHandle(nodeValue, family, memberType, name)
	if err != nil {
		return nil, nil, err
	}
	return s.emitTreeExactFieldValueAtIndex(access.tablePtr, memberType, fieldName, access.rowIndex, name)
}
func (s *functionState) emitTreeMemberSurfaceFieldValueAtHandle(nodeValue C.LLVMValueRef, family *semantic.TreeType, memberType semantic.Type, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	if viewType, ok := semantic.StripAggregateStateType(memberType).(*semantic.TreeVariantViewType); ok && viewType != nil && viewType.Category != nil && treeCategoryLayoutPlan(viewType.Category).isCategoryUnion() {
		access, err := s.emitTreeCategoryUnionTableAccessFromHandle(nodeValue, family, viewType.Category, name)
		if err != nil {
			return nil, nil, err
		}
		return s.emitTreeCategoryUnionSurfaceFieldValue(access.tablePtr, viewType.Category, viewType.Variant, fieldName, access.rowIndex, name)
	}
	switch semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		if family != nil && treeFamilyLayoutPlan(family).isCategoryUnion() {
			value, rawType, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, fieldName, name)
			if err != nil {
				return nil, nil, err
			}
			field, ok := semantic.TreeExactSurfaceFieldInfo(memberType, fieldName)
			if !ok {
				return nil, nil, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
			}
			return s.treeFieldSurfaceValue(value, rawType, field.Type, name)
		}
	}
	access, err := s.emitTreeExactTableAccessFromHandle(nodeValue, family, memberType, name)
	if err != nil {
		return nil, nil, err
	}
	return s.emitTreeExactSurfaceFieldValue(access.tablePtr, memberType, fieldName, access.rowIndex, name)
}
func (s *functionState) emitTreePatchExactRowFieldValue(memberType semantic.Type, rowValue C.LLVMValueRef, fieldName string, fieldValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	fieldIndex, _, err := treeExactFieldIndex(memberType, fieldName)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(fieldIndex), cStringFree(name+".row.field")), nil
}
func (s *functionState) emitTreeStoreExactRowValueAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, rowIndex C.LLVMValueRef, fieldValues []C.LLVMValueRef, name string) error {
	fieldDecls := treeExactFieldDecls(memberType)
	if len(fieldValues) != len(fieldDecls) {
		return fmt.Errorf("tree row store for %s expects %d fields, got %d", treeExactMemberSurfaceName(memberType), len(fieldDecls), len(fieldValues))
	}
	if len(fieldValues) == 0 {
		return nil
	}
	rowType, err := s.g.ensureTreeExactRowType(memberType)
	if err != nil {
		return err
	}
	rowValue := C.LLVMGetUndef(rowType)
	for i, fieldValue := range fieldValues {
		rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(i), cStringFree(name+".row.field"))
	}
	return s.emitTreeStoreExactRowValue(tablePtr, memberType, rowIndex, rowValue, name)
}
func (s *functionState) emitTreeCopyRowAndPatch(tablePtr C.LLVMValueRef, memberType semantic.Type, rowIndex C.LLVMValueRef, rowValue C.LLVMValueRef, fieldNames []string, fieldValues []C.LLVMValueRef, name string) error {
	patched, err := s.emitTreePatchExactRowFields(memberType, rowValue, fieldNames, fieldValues, name)
	if err != nil {
		return err
	}
	return s.emitTreeStoreExactRowValue(tablePtr, memberType, rowIndex, patched, name)
}
func (s *functionState) emitTreeCopyCategoryUnionPayloadAndPatch(sourceTablePtr C.LLVMValueRef, destTablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, variant *semantic.EnumVariant, sourceRowIndex C.LLVMValueRef, destRowIndex C.LLVMValueRef, fieldNames []string, fieldValues []C.LLVMValueRef, name string) error {
	if len(fieldNames) != len(fieldValues) {
		return fmt.Errorf("category-union tree row patch for %s.%s expects %d field names and %d values", category.Name, variant.Name, len(fieldNames), len(fieldValues))
	}
	payloadValue, payloadType, err := s.emitTreeCategoryUnionPayloadValueAtIndex(sourceTablePtr, category, variant, sourceRowIndex, name+".src")
	if err != nil {
		return err
	}
	if C.LLVMGetTypeKind(payloadType) == C.LLVMVoidTypeKind {
		return nil
	}
	memberType := category.VariantViewType(variant)
	patched := payloadValue
	for i, fieldName := range fieldNames {
		fieldIndex, _, err := treeExactFieldIndex(memberType, fieldName)
		if err != nil {
			return err
		}
		patched = C.LLVMBuildInsertValue(s.builder, patched, fieldValues[i], C.unsigned(fieldIndex), cStringFree(name+".payload.field"))
	}
	return s.emitTreeCategoryUnionPayloadAtIndex(destTablePtr, category, variant, destRowIndex, payloadType, patched, name)
}
func (s *functionState) emitTreePatchExactRowFields(memberType semantic.Type, rowValue C.LLVMValueRef, fieldNames []string, fieldValues []C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if len(fieldNames) != len(fieldValues) {
		return nil, fmt.Errorf("tree row patch for %s expects %d field names and %d values", treeExactMemberSurfaceName(memberType), len(fieldNames), len(fieldValues))
	}
	patched := rowValue
	for i, fieldName := range fieldNames {
		var err error
		patched, err = s.emitTreePatchExactRowFieldValue(memberType, patched, fieldName, fieldValues[i], name)
		if err != nil {
			return nil, err
		}
	}
	return patched, nil
}
func (s *functionState) emitTreeStoreExactRowValue(tablePtr C.LLVMValueRef, memberType semantic.Type, rowIndex C.LLVMValueRef, rowValue C.LLVMValueRef, name string) error {
	if len(treeExactFieldDecls(memberType)) == 0 {
		return nil
	}
	rowPtr, _, err := s.emitTreeExactRowPointerAtIndex(tablePtr, memberType, rowIndex, name)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, rowValue, rowPtr)
	if s.treeExactRowValues == nil {
		s.treeExactRowValues = map[treeExactRowCacheKey]C.LLVMValueRef{}
	}
	s.treeExactRowValues[makeTreeExactRowCacheKey(s, memberType, tablePtr, rowIndex)] = rowValue
	return nil
}

func (s *functionState) emitTreeCategoryUnionKindAtIndex(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, rowIndex C.LLVMValueRef, tag uint32, name string) error {
	kindsPtr, err := s.emitTreeCategoryUnionKindsPointerValue(tablePtr, category, name)
	if err != nil {
		return err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return err
	}
	kindPtr := C.LLVMBuildGEP2(s.builder, kindType, kindsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".kind.ptr"))
	kindValue := C.LLVMConstInt(kindType, C.ulonglong(tag), 0)
	C.LLVMBuildStore(s.builder, kindValue, kindPtr)
	if s.treeDenseKindValues == nil {
		s.treeDenseKindValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDenseKindValues[makeDenseTreeValueCacheKey(s, "category-kind:"+category.Name, tablePtr, rowIndex)] = kindValue
	return nil
}

func (s *functionState) emitTreeCategoryUnionKindValueAtIndex(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	cacheKey := makeDenseTreeValueCacheKey(s, "category-kind:"+category.Name, tablePtr, rowIndex)
	if s.treeDenseKindValues != nil {
		if cached, ok := s.treeDenseKindValues[cacheKey]; ok && cached != nil {
			return cached, nil
		}
	}
	kindsPtr, err := s.emitTreeCategoryUnionKindsPointerValue(tablePtr, category, name)
	if err != nil {
		return nil, err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	kindPtr := C.LLVMBuildGEP2(s.builder, kindType, kindsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".kind.ptr"))
	kindValue := C.LLVMBuildLoad2(s.builder, kindType, kindPtr, cStringFree(name+".kind"))
	if s.treeDenseKindValues == nil {
		s.treeDenseKindValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDenseKindValues[cacheKey] = kindValue
	return kindValue, nil
}

func (s *functionState) emitTreeRootUnionKindAtIndex(tablePtr C.LLVMValueRef, family *semantic.TreeType, rowIndex C.LLVMValueRef, tagValue C.LLVMValueRef, name string) error {
	kindsPtr, err := s.emitTreeRootUnionKindsPointerValue(tablePtr, family, name)
	if err != nil {
		return err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return err
	}
	kindPtr := C.LLVMBuildGEP2(s.builder, kindType, kindsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".kind.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, kindPtr)
	if s.treeDenseKindValues == nil {
		s.treeDenseKindValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDenseKindValues[makeDenseTreeValueCacheKey(s, "root-kind:"+family.Name, tablePtr, rowIndex)] = tagValue
	return nil
}

func (s *functionState) emitTreeRootUnionKindValueAtIndex(tablePtr C.LLVMValueRef, family *semantic.TreeType, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	cacheKey := makeDenseTreeValueCacheKey(s, "root-kind:"+family.Name, tablePtr, rowIndex)
	if s.treeDenseKindValues != nil {
		if cached, ok := s.treeDenseKindValues[cacheKey]; ok && cached != nil {
			return cached, nil
		}
	}
	kindsPtr, err := s.emitTreeRootUnionKindsPointerValue(tablePtr, family, name)
	if err != nil {
		return nil, err
	}
	kindType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	kindPtr := C.LLVMBuildGEP2(s.builder, kindType, kindsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".kind.ptr"))
	kindValue := C.LLVMBuildLoad2(s.builder, kindType, kindPtr, cStringFree(name+".kind"))
	if s.treeDenseKindValues == nil {
		s.treeDenseKindValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDenseKindValues[cacheKey] = kindValue
	return kindValue, nil
}

func (s *functionState) emitTreeCategoryUnionPayloadAtIndex(tablePtr C.LLVMValueRef, category *semantic.TreeCategoryType, variant *semantic.EnumVariant, rowIndex C.LLVMValueRef, payloadType C.LLVMTypeRef, payloadValue C.LLVMValueRef, name string) error {
	payloadsPtr, err := s.emitTreeCategoryUnionPayloadsPointerValue(tablePtr, category, name)
	if err != nil {
		return err
	}
	payloadRowType, err := s.g.ensureTreeCategoryUnionPayloadType(category)
	if err != nil {
		return err
	}
	payloadRowPtr := C.LLVMBuildGEP2(s.builder, payloadRowType, payloadsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".payload.ptr"))
	payloadValuePtr := C.LLVMBuildAlloca(s.builder, payloadType, cStringFree(name+".payload.tmp"))
	C.LLVMBuildStore(s.builder, payloadValue, payloadValuePtr)
	sizeBytes, err := s.g.abiSizeOfLLVMType(payloadType)
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{payloadRowPtr, payloadValuePtr, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)}, name+".payload.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	if s.treeDensePayloadValues == nil {
		s.treeDensePayloadValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDensePayloadValues[makeDenseTreeValueCacheKey(s, "category-payload:"+category.Name+"."+variant.Name, tablePtr, rowIndex)] = payloadValue
	_ = variant
	return nil
}

func (s *functionState) emitTreeRootUnionPayloadAtIndex(tablePtr C.LLVMValueRef, family *semantic.TreeType, rowIndex C.LLVMValueRef, payloadType C.LLVMTypeRef, payloadValue C.LLVMValueRef, name string) error {
	payloadsPtr, err := s.emitTreeRootUnionPayloadsPointerValue(tablePtr, family, name)
	if err != nil {
		return err
	}
	payloadRowType, err := s.g.ensureTreeRootUnionPayloadType(family)
	if err != nil {
		return err
	}
	payloadRowPtr := C.LLVMBuildGEP2(s.builder, payloadRowType, payloadsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".payload.ptr"))
	payloadValuePtr := C.LLVMBuildAlloca(s.builder, payloadType, cStringFree(name+".payload.tmp"))
	C.LLVMBuildStore(s.builder, payloadValue, payloadValuePtr)
	sizeBytes, err := s.g.abiSizeOfLLVMType(payloadType)
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{payloadRowPtr, payloadValuePtr, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)}, name+".payload.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	if s.treeDensePayloadValues == nil {
		s.treeDensePayloadValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDensePayloadValues[makeDenseTreeValueCacheKey(s, "root-payload:"+family.Name, tablePtr, rowIndex)] = payloadValue
	return nil
}

func (s *functionState) emitTreeRootUnionPayloadValueAtIndex(tablePtr C.LLVMValueRef, family *semantic.TreeType, rowIndex C.LLVMValueRef, payloadType C.LLVMTypeRef, name string) (C.LLVMValueRef, C.LLVMTypeRef, error) {
	cacheKey := makeDenseTreeValueCacheKey(s, "root-payload:"+family.Name, tablePtr, rowIndex)
	if s.treeDensePayloadValues != nil {
		if cached, ok := s.treeDensePayloadValues[cacheKey]; ok && cached != nil {
			return cached, payloadType, nil
		}
	}
	payloadsPtr, err := s.emitTreeRootUnionPayloadsPointerValue(tablePtr, family, name)
	if err != nil {
		return nil, nil, err
	}
	payloadRowType, err := s.g.ensureTreeRootUnionPayloadType(family)
	if err != nil {
		return nil, nil, err
	}
	payloadRowPtr := C.LLVMBuildGEP2(s.builder, payloadRowType, payloadsPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".payload.ptr"))
	payloadValue := C.LLVMBuildLoad2(s.builder, payloadType, payloadRowPtr, cStringFree(name+".payload"))
	if s.treeDensePayloadValues == nil {
		s.treeDensePayloadValues = map[treeDenseValueCacheKey]C.LLVMValueRef{}
	}
	s.treeDensePayloadValues[cacheKey] = payloadValue
	return payloadValue, payloadType, nil
}

func (s *functionState) emitTreeRootUnionExactFieldValueAtIndex(tablePtr C.LLVMValueRef, family *semantic.TreeType, memberType semantic.Type, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	fieldIndex, field, err := treeExactFieldIndex(memberType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	payloadType, err := s.g.lowerTreeRootUnionExactPayloadType(memberType)
	if err != nil {
		return nil, nil, err
	}
	if C.LLVMGetTypeKind(payloadType) == C.LLVMVoidTypeKind {
		return nil, nil, fmt.Errorf("%s has no lowered root payload field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	payloadValue, _, err := s.emitTreeRootUnionPayloadValueAtIndex(tablePtr, family, rowIndex, payloadType, name)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMBuildExtractValue(s.builder, payloadValue, C.unsigned(fieldIndex), cStringFree(name+".elem"))
	return value, field.Type, nil
}
