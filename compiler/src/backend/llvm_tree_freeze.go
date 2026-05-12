//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTreeStoreFreezeCall(arg ast.Expr, frozenStore *semantic.TreeStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if frozenStore == nil || frozenStore.Family == nil {
		return nil, nil, fmt.Errorf("missing frozen tree store metadata")
	}
	operand := arg
	if moved, ok := backendExplicitMoveOperand(arg); ok {
		operand = moved
	}
	sourceType := s.exprType(operand)
	if sourceType == nil {
		sourceType = semantic.TreeStoreWithState(frozenStore, s.g.result.NamedTypes["Local"])
	}
	storeValue, _, err := s.emitExpr(operand, sourceType)
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitTreeStoreFreezeLayout(storeValue, frozenStore, "tree.freeze"); err != nil {
		return nil, nil, err
	}
	return storeValue, frozenStore, nil
}

func (s *functionState) emitTreeStoreFreezeLayout(storeValue C.LLVMValueRef, storeType *semantic.TreeStoreType, name string) error {
	if storeType == nil || storeType.Family == nil {
		return fmt.Errorf("missing tree store freeze metadata")
	}
	family := storeType.Family
	if !treeFamilyLayoutPlan(family).isCategoryUnion() {
		return nil
	}
	arenaRef := s.emitTreeStoreArenaValueNamed(storeValue, name+".arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, name+".state")
	for _, category := range treeFamilyCategoryMembersInDeclOrder(family) {
		plan := treeCategoryLayoutPlan(category)
		if !plan.isSoA() && !plan.requestsIndexes() {
			continue
		}
		tagColumn, err := s.emitTreeFreezeCategoryTagColumn(arenaRef, stateValue, family, category, name+"."+sanitizeIdentifier(category.Name))
		if err != nil {
			return err
		}
		if plan.isSoA() {
			if err := s.emitTreeFreezeCategoryColumnPointers(arenaRef, stateValue, family, category, tagColumn, name+"."+sanitizeIdentifier(category.Name)); err != nil {
				return err
			}
		}
		if plan.requestsIndexes() {
			if err := s.emitTreeFreezeCategoryIndexPointers(stateValue, category, tagColumn, name+"."+sanitizeIdentifier(category.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *functionState) emitTreeFreezeCategoryTagColumn(arenaRef C.LLVMValueRef, stateValue C.LLVMValueRef, family *semantic.TreeType, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	tablePtr, err := s.emitTreeCategoryUnionTablePtr(stateValue, family, category, name)
	if err != nil {
		return nil, err
	}
	countValue, err := s.emitTreeCategoryUnionTableCountValue(tablePtr, category, name)
	if err != nil {
		return nil, err
	}
	sourceKinds, err := s.emitTreeCategoryUnionKindsPointerValue(tablePtr, category, name)
	if err != nil {
		return nil, err
	}
	u32Type, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	u32Size, err := s.g.abiSizeOfLLVMType(u32Type)
	if err != nil {
		return nil, err
	}
	bytes, err := s.emitTreeFreezeByteCount(countValue, u32Size, name+".tags")
	if err != nil {
		return nil, err
	}
	dest, err := s.emitTreeFreezeArenaAlloc(arenaRef, bytes, name+".tags")
	if err != nil {
		return nil, err
	}
	if err := s.emitTreeFreezeMemcpy(dest, sourceKinds, bytes, name+".tags"); err != nil {
		return nil, err
	}
	return dest, nil
}

func (s *functionState) emitTreeFreezeCategoryColumnPointers(arenaRef C.LLVMValueRef, stateValue C.LLVMValueRef, family *semantic.TreeType, category *semantic.TreeCategoryType, tagColumn C.LLVMValueRef, name string) error {
	columnsPtr, err := s.emitTreeCategoryFrozenColumnsPtr(stateValue, category, name+".columns")
	if err != nil {
		return err
	}
	columnsType, err := s.g.ensureTreeCategoryFrozenColumnsType(category)
	if err != nil {
		return err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, columnsType, columnsPtr, 0, cStringFree(name+".columns.tags.ptr"))
	C.LLVMBuildStore(s.builder, tagColumn, tagPtr)
	tablePtr, err := s.emitTreeCategoryUnionTablePtr(stateValue, family, category, name+".columns")
	if err != nil {
		return err
	}
	countValue, err := s.emitTreeCategoryUnionTableCountValue(tablePtr, category, name+".columns")
	if err != nil {
		return err
	}
	nullPtr := C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0))
	for i, column := range treeCategorySoAColumnNames(category) {
		value := nullPtr
		if field, ok := category.Common[column]; ok {
			columnValue, err := s.emitTreeFreezeCategoryCommonFieldColumn(arenaRef, tablePtr, countValue, category, column, field, name+"."+sanitizeIdentifier(column))
			if err != nil {
				return err
			}
			value = columnValue
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, columnsType, columnsPtr, C.unsigned(i+1), cStringFree(name+".columns.field.ptr"))
		C.LLVMBuildStore(s.builder, value, fieldPtr)
	}
	return nil
}

func (s *functionState) emitTreeFreezeCategoryCommonFieldColumn(arenaRef C.LLVMValueRef, tablePtr C.LLVMValueRef, countValue C.LLVMValueRef, category *semantic.TreeCategoryType, fieldName string, field semantic.Field, name string) (C.LLVMValueRef, error) {
	elemLLVMType, err := s.g.lowerType(field.Type)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.g.abiSizeOfLLVMType(elemLLVMType)
	if err != nil {
		return nil, err
	}
	bytes, err := s.emitTreeFreezeByteCount(countValue, elemSize, name)
	if err != nil {
		return nil, err
	}
	dest, err := s.emitTreeFreezeArenaAlloc(arenaRef, bytes, name)
	if err != nil {
		return nil, err
	}
	if err := s.emitTreeFreezePopulateCommonFieldColumn(dest, elemLLVMType, tablePtr, countValue, category, fieldName, name); err != nil {
		return nil, err
	}
	return dest, nil
}

func (s *functionState) emitTreeFreezePopulateCommonFieldColumn(dest C.LLVMValueRef, elemLLVMType C.LLVMTypeRef, tablePtr C.LLVMValueRef, countValue C.LLVMValueRef, category *semantic.TreeCategoryType, fieldName string, name string) error {
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	zero := C.LLVMConstInt(usizeType, 0, 0)
	one := C.LLVMConstInt(usizeType, 1, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
	nextBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
	endBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	C.LLVMBuildBr(s.builder, condBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeType, cStringFree(name+".index"))
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{zero}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{entryBlock}), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree(name+".has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, bodyBB, endBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	tagValue, err := s.emitTreeCategoryUnionKindValueAtIndex(tablePtr, category, indexValue, name)
	if err != nil {
		return err
	}
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, nextBB, C.unsigned(len(category.Variants)))
	for _, variant := range category.Variants {
		if variant == nil {
			continue
		}
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+sanitizeIdentifier(variant.Name)))
		C.LLVMAddCase(switchInst, C.LLVMConstInt(C.LLVMTypeOf(tagValue), C.ulonglong(variant.Tag), 0), caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		payloadValue, _, err := s.emitTreeCategoryUnionPayloadValueAtIndex(tablePtr, category, variant, indexValue, name+"."+sanitizeIdentifier(variant.Name))
		if err != nil {
			return err
		}
		memberType := category.VariantViewType(variant)
		fieldIndex, _, err := treeExactFieldIndex(memberType, fieldName)
		if err != nil {
			return err
		}
		fieldValue := C.LLVMBuildExtractValue(s.builder, payloadValue, C.unsigned(fieldIndex), cStringFree(name+".field"))
		destPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dest, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".dest.ptr"))
		C.LLVMBuildStore(s.builder, fieldValue, destPtr)
		C.LLVMBuildBr(s.builder, nextBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(name+".index.next"))
	nextBlock := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, condBB)
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{nextIndex}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{nextBlock}), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, endBB)
	return nil
}

func (s *functionState) emitTreeFreezeCategoryIndexPointers(stateValue C.LLVMValueRef, category *semantic.TreeCategoryType, tagColumn C.LLVMValueRef, name string) error {
	indexesPtr, err := s.emitTreeCategoryFrozenIndexesPtr(stateValue, category, name+".indexes")
	if err != nil {
		return err
	}
	indexesType, err := s.g.ensureTreeCategoryFrozenIndexesType(category)
	if err != nil {
		return err
	}
	nullPtr := C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0))
	plan := treeCategoryLayoutPlan(category)
	for i, index := range plan.indexes {
		value := nullPtr
		if index.Kind {
			value = tagColumn
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, indexesType, indexesPtr, C.unsigned(i), cStringFree(name+".indexes.field.ptr"))
		C.LLVMBuildStore(s.builder, value, fieldPtr)
	}
	return nil
}

func (s *functionState) emitTreeFreezeByteCount(countValue C.LLVMValueRef, elementSizeBytes uint64, name string) (C.LLVMValueRef, error) {
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	sizeValue := C.LLVMConstInt(usizeType, C.ulonglong(elementSizeBytes), 0)
	return C.LLVMBuildMul(s.builder, countValue, sizeValue, cStringFree(name+".bytes")), nil
}

func (s *functionState) emitTreeFreezeArenaAlloc(arenaRef C.LLVMValueRef, byteCount C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, byteCount}, name+".alloc"), nil
}

func (s *functionState) emitTreeFreezeMemcpy(dest C.LLVMValueRef, source C.LLVMValueRef, byteCount C.LLVMValueRef, name string) error {
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	call := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dest, source, byteCount}, name+".memcpy")
	s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
	return nil
}

func (s *functionState) emitTreeTagsHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("tree_tags expects 2 arguments, got %d", len(expr.Args))
	}
	storeType, ok := s.exprType(expr.Args[0]).(*semantic.TreeStoreType)
	if !ok || storeType == nil || storeType.Family == nil {
		return nil, nil, true, fmt.Errorf("tree_tags expects a frozen tree store")
	}
	if !semantic.IsFrozenTreeStoreType(storeType) {
		return nil, nil, true, fmt.Errorf("tree_tags expects a frozen tree store, got %s", storeType)
	}
	categoryName, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, true, fmt.Errorf("tree_tags category argument must be a compile-time string")
	}
	category, ok := treeCategoryByName(storeType.Family, categoryName)
	if !ok {
		return nil, nil, true, fmt.Errorf("tree family %s has no category %q", storeType.Family.Name, categoryName)
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("tree_tags result type is missing dview metadata")
	}
	storeValue, _, err := s.emitExpr(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, true, err
	}
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.tags.state")
	tablePtr, err := s.emitTreeCategoryUnionTablePtr(stateValue, storeType.Family, category, "tree.tags")
	if err != nil {
		return nil, nil, true, err
	}
	countValue, err := s.emitTreeCategoryUnionTableCountValue(tablePtr, category, "tree.tags")
	if err != nil {
		return nil, nil, true, err
	}
	dataPtr, err := s.emitTreeFrozenTagColumnPointer(stateValue, category, "tree.tags")
	if err != nil {
		return nil, nil, true, err
	}
	return s.buildTreeFrozenColumnDView(dataPtr, countValue, resultType, "tree.tags")
}

func (s *functionState) emitTreeColumnHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("tree_column expects 3 arguments, got %d", len(expr.Args))
	}
	storeType, ok := s.exprType(expr.Args[0]).(*semantic.TreeStoreType)
	if !ok || storeType == nil || storeType.Family == nil {
		return nil, nil, true, fmt.Errorf("tree_column expects a frozen tree store")
	}
	if !semantic.IsFrozenTreeStoreType(storeType) {
		return nil, nil, true, fmt.Errorf("tree_column expects a frozen tree store, got %s", storeType)
	}
	categoryName, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, true, fmt.Errorf("tree_column category argument must be a compile-time string")
	}
	fieldName, ok := s.staticCStringLiteral(expr.Args[2])
	if !ok {
		return nil, nil, true, fmt.Errorf("tree_column field argument must be a compile-time string")
	}
	category, ok := treeCategoryByName(storeType.Family, categoryName)
	if !ok {
		return nil, nil, true, fmt.Errorf("tree family %s has no category %q", storeType.Family.Name, categoryName)
	}
	if _, ok := category.Common[fieldName]; !ok {
		return nil, nil, true, fmt.Errorf("tree_column currently supports common fields only; category %s has no common field %q", category.Name, fieldName)
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("tree_column result type is missing dview metadata")
	}
	storeValue, _, err := s.emitExpr(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, true, err
	}
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.column.state")
	tablePtr, err := s.emitTreeCategoryUnionTablePtr(stateValue, storeType.Family, category, "tree.column")
	if err != nil {
		return nil, nil, true, err
	}
	countValue, err := s.emitTreeCategoryUnionTableCountValue(tablePtr, category, "tree.column")
	if err != nil {
		return nil, nil, true, err
	}
	dataPtr, err := s.emitTreeFrozenFieldColumnPointer(stateValue, category, fieldName, "tree.column")
	if err != nil {
		return nil, nil, true, err
	}
	return s.buildTreeFrozenColumnDView(dataPtr, countValue, resultType, "tree.column")
}

func treeCategoryByName(family *semantic.TreeType, name string) (*semantic.TreeCategoryType, bool) {
	if family == nil {
		return nil, false
	}
	member, ok := family.Member(name)
	category, _ := member.(*semantic.TreeCategoryType)
	return category, ok && category != nil
}

func (s *functionState) emitTreeFrozenTagColumnPointer(stateValue C.LLVMValueRef, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	if category == nil {
		return nil, fmt.Errorf("missing tree tag column category")
	}
	plan := treeCategoryLayoutPlan(category)
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	if plan.isSoA() {
		columnsPtr, err := s.emitTreeCategoryFrozenColumnsPtr(stateValue, category, name+".columns")
		if err != nil {
			return nil, err
		}
		columnsType, err := s.g.ensureTreeCategoryFrozenColumnsType(category)
		if err != nil {
			return nil, err
		}
		tagPtr := C.LLVMBuildStructGEP2(s.builder, columnsType, columnsPtr, 0, cStringFree(name+".columns.tags.ptr"))
		return C.LLVMBuildLoad2(s.builder, ptrType, tagPtr, cStringFree(name+".columns.tags")), nil
	}
	if plan.requestsIndexes() {
		for i, index := range plan.indexes {
			if !index.Kind {
				continue
			}
			indexesPtr, err := s.emitTreeCategoryFrozenIndexesPtr(stateValue, category, name+".indexes")
			if err != nil {
				return nil, err
			}
			indexesType, err := s.g.ensureTreeCategoryFrozenIndexesType(category)
			if err != nil {
				return nil, err
			}
			tagPtr := C.LLVMBuildStructGEP2(s.builder, indexesType, indexesPtr, C.unsigned(i), cStringFree(name+".indexes.tags.ptr"))
			return C.LLVMBuildLoad2(s.builder, ptrType, tagPtr, cStringFree(name+".indexes.tags")), nil
		}
	}
	return nil, fmt.Errorf("tree_tags requires @layout(soa) or @index(kind) on category %s", category.Name)
}

func (s *functionState) emitTreeFrozenFieldColumnPointer(stateValue C.LLVMValueRef, category *semantic.TreeCategoryType, fieldName string, name string) (C.LLVMValueRef, error) {
	if category == nil {
		return nil, fmt.Errorf("missing tree field column category")
	}
	columnsPtr, err := s.emitTreeCategoryFrozenColumnsPtr(stateValue, category, name+".columns")
	if err != nil {
		return nil, err
	}
	columnsType, err := s.g.ensureTreeCategoryFrozenColumnsType(category)
	if err != nil {
		return nil, err
	}
	columnIndex := treeCategorySoAColumnIndex(category, fieldName)
	if columnIndex < 0 {
		return nil, fmt.Errorf("tree category %s has no frozen column %q", category.Name, fieldName)
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, columnsType, columnsPtr, C.unsigned(columnIndex+1), cStringFree(name+".columns.field.ptr"))
	return C.LLVMBuildLoad2(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), fieldPtr, cStringFree(name+".columns.field")), nil
}

func treeCategorySoAColumnIndex(category *semantic.TreeCategoryType, fieldName string) int {
	for i, name := range treeCategorySoAColumnNames(category) {
		if name == fieldName {
			return i
		}
	}
	return -1
}

func (s *functionState) buildTreeFrozenColumnDView(dataPtr C.LLVMValueRef, countValue C.LLVMValueRef, viewType *semantic.DArrayViewType, name string) (C.LLVMValueRef, semantic.Type, bool, error) {
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree(name+".view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree(name+".view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0), 2, cStringFree(name+".view.elem_size"))
	return viewValue, viewType, true, nil
}
