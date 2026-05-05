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
func (s *functionState) emitTreeEnsureTableCapacity(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, memberType semantic.Type, neededCount C.LLVMValueRef, name string) error {
	currentCapacity, err := s.emitTreeTableCapacityValue(tablePtr, memberType, name)
	if err != nil {
		return err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	needsGrow := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), currentCapacity, neededCount, cStringFree(name+".grow"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.bb"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow.cont"))
	C.LLVMBuildCondBr(s.builder, needsGrow, growBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeType, 0, 0)
	initCap := C.LLVMConstInt(usizeType, 8, 0)
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
	for i, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		elemSizeBytes, err := s.sizeOfType(field.Type)
		if err != nil {
			return err
		}
		elemSizeValue := C.LLVMConstInt(usizeType, C.ulonglong(elemSizeBytes), 0)
		oldBytes := C.LLVMBuildMul(s.builder, currentCapacity, elemSizeValue, cStringFree(name+".old.bytes"))
		newBytes := C.LLVMBuildMul(s.builder, newCapacity, elemSizeValue, cStringFree(name+".new.bytes"))
		columnPtr, err := s.emitTreeTableColumnPointerValue(tablePtr, memberType, i, name)
		if err != nil {
			return err
		}
		isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), columnPtr, C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0)), cStringFree(name+".column.null"))
		fieldAllocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".column.alloc"))
		fieldReallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".column.realloc"))
		fieldContBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".column.cont"))
		C.LLVMBuildCondBr(s.builder, isNull, fieldAllocBB, fieldReallocBB)

		C.LLVMPositionBuilderAtEnd(s.builder, fieldAllocBB)
		allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newBytes}, name+".column.alloc")
		allocEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, fieldContBB)

		C.LLVMPositionBuilderAtEnd(s.builder, fieldReallocBB)
		reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, columnPtr, oldBytes, newBytes}, name+".column.realloc")
		reallocEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, fieldContBB)

		C.LLVMPositionBuilderAtEnd(s.builder, fieldContBB)
		columnValue := C.LLVMBuildPhi(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), cStringFree(name+".column.phi"))
		C.LLVMAddIncoming(columnValue, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
		if err := s.emitTreeTableSetColumnPointer(tablePtr, memberType, i, columnValue, name); err != nil {
			return err
		}
	}
	if err := s.emitTreeTableSetCapacity(tablePtr, memberType, newCapacity, name); err != nil {
		return err
	}
	C.LLVMBuildBr(s.builder, contBB)
	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
func (s *functionState) emitTreeExactFieldValueAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	fieldDecls := treeExactFieldDecls(memberType)
	fieldIndex := -1
	for i, fieldDecl := range fieldDecls {
		if fieldDecl.Name == fieldName {
			fieldIndex = i
			break
		}
	}
	if fieldIndex < 0 {
		return nil, nil, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	field, ok := treeExactFieldInfo(memberType, fieldName)
	if !ok {
		return nil, nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	columnPtr, err := s.emitTreeTableColumnPointerValue(tablePtr, memberType, fieldIndex, name)
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(field.Type)
	if err != nil {
		return nil, nil, err
	}
	elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, columnPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".elem.ptr"))
	value := C.LLVMBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree(name+".elem"))
	return value, field.Type, nil
}
func (s *functionState) emitTreeStoreExactFieldValueAtIndex(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldName string, rowIndex C.LLVMValueRef, fieldValue C.LLVMValueRef, name string) error {
	fieldDecls := treeExactFieldDecls(memberType)
	fieldIndex := -1
	var fieldType semantic.Type
	for i, fieldDecl := range fieldDecls {
		if fieldDecl.Name == fieldName {
			fieldIndex = i
			field, ok := treeExactFieldInfo(memberType, fieldName)
			if !ok {
				return fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldName)
			}
			fieldType = field.Type
			break
		}
	}
	if fieldIndex < 0 || fieldType == nil {
		return fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	columnPtr, err := s.emitTreeTableColumnPointerValue(tablePtr, memberType, fieldIndex, name)
	if err != nil {
		return err
	}
	elemLLVMType, err := s.g.lowerType(fieldType)
	if err != nil {
		return err
	}
	elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, columnPtr, llvmValueSlicePtr([]C.LLVMValueRef{rowIndex}), 1, cStringFree(name+".elem.ptr"))
	C.LLVMBuildStore(s.builder, fieldValue, elemPtr)
	return nil
}
