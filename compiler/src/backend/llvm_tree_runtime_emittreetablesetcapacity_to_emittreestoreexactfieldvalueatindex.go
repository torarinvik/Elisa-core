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
