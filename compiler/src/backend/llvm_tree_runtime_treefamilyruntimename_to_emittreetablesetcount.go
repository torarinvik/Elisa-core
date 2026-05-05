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

func treeFamilyRuntimeName(treeType *semantic.TreeType) string {
	if treeType == nil || treeType.Name == "" {
		return "TreeFamily"
	}
	return sanitizeIdentifier(treeType.Name)
}
func treeHandleCarrierName(treeType *semantic.TreeType) string {
	return treeFamilyRuntimeName(treeType) + "__TreeHandle"
}
func treeStoreStateName(treeType *semantic.TreeType) string {
	return treeFamilyRuntimeName(treeType) + "__TreeStoreState"
}
func treeExactTableName(memberType semantic.Type) string {
	return sanitizeIdentifier(treeExactMemberSurfaceName(memberType)) + "__TreeTable"
}
func treeExactMemberSurfaceName(memberType semantic.Type) string {
	switch tt := semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeVariantViewType:
		if tt != nil && tt.Category != nil && tt.Variant != nil {
			return tt.Category.Name + "." + tt.Variant.Name
		}
	case *semantic.TreeBlockType:
		if tt != nil {
			return tt.Name
		}
	case *semantic.TreeStructType:
		if tt != nil {
			return tt.Name
		}
	}
	return "TreeExactMember"
}
func treeExactFieldDecls(memberType semantic.Type) []ast.FieldDecl {
	switch tt := semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return nil
		}
		decls := append([]ast.FieldDecl(nil), semantic.TreeCommonFieldDeclsForFamily(tt.Category.Family)...)
		for i := range tt.Variant.Payload {
			name := tt.Variant.PayloadLabel(i)
			if name == "" {
				name = fmt.Sprintf("payload%d", i)
			}
			decls = append(decls, ast.FieldDecl{Name: name})
		}
		return decls
	case *semantic.TreeBlockType:
		return semantic.TreeBlockFieldDeclsWithCommon(tt)
	case *semantic.TreeStructType:
		return semantic.TreeStructFieldDeclsWithCommon(tt)
	default:
		return nil
	}
}
func treeExactFieldInfo(memberType semantic.Type, fieldName string) (semantic.Field, bool) {
	switch tt := semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return semantic.Field{}, false
		}
		if field, ok := tt.Category.Common[fieldName]; ok {
			return field, true
		}
		if index, ok := tt.Variant.PayloadIndex(fieldName); ok && index < len(tt.Variant.Payload) {
			return semantic.Field{Type: tt.Variant.Payload[index]}, true
		}
		return semantic.Field{}, false
	default:
		return semantic.TreeExactFieldInfo(memberType, fieldName)
	}
}
func treeExactMemberFamily(memberType semantic.Type) *semantic.TreeType {
	switch tt := semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil
		}
		return tt.Category.Family
	case *semantic.TreeBlockType:
		if tt == nil {
			return nil
		}
		return tt.Family
	case *semantic.TreeStructType:
		if tt == nil {
			return nil
		}
		return tt.Family
	default:
		return nil
	}
}
func treeExactMemberTag(memberType semantic.Type) (uint32, bool) {
	switch tt := semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Variant == nil {
			return 0, false
		}
		return tt.Variant.Tag, true
	default:
		return semantic.TreeExactTag(memberType)
	}
}
func treeCategoryMembersInTagOrder(categoryType *semantic.TreeCategoryType) []semantic.Type {
	if categoryType == nil {
		return nil
	}
	out := make([]semantic.Type, 0, len(categoryType.Variants))
	for _, variant := range categoryType.Variants {
		out = append(out, categoryType.VariantViewType(variant))
	}
	return out
}
func treeNodeHandleFamily(t semantic.Type) (*semantic.TreeType, bool) {
	switch tt := semantic.StripAggregateStateType(t).(type) {
	case *semantic.TreeNodeType:
		return tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeCategoryType:
		return tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, false
		}
		return tt.Category.Family, tt.Category.Family != nil
	case *semantic.TreeBlockType:
		return tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeStructType:
		return tt.Family, tt != nil && tt.Family != nil
	default:
		return nil, false
	}
}
func (g *llvmGenerator) ensureTreeHandleCarrierType(treeType *semantic.TreeType) (C.LLVMTypeRef, error) {
	if treeType == nil {
		return nil, fmt.Errorf("missing tree family for handle lowering")
	}
	name := treeHandleCarrierName(treeType)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	keyType, err := g.lowerBuiltin("u64")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{C.LLVMPointerTypeInContext(g.context, 0), keyType}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) ensureTreeExactTableType(memberType semantic.Type) (C.LLVMTypeRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("missing tree exact member family")
	}
	name := treeExactTableName(memberType)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	usizeType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{usizeType, usizeType}
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing tree exact field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		if err := g.noteType(field.Type); err != nil {
			return nil, err
		}
		fields = append(fields, C.LLVMPointerTypeInContext(g.context, 0))
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) ensureTreeStoreStateType(treeType *semantic.TreeType) (C.LLVMTypeRef, error) {
	if treeType == nil {
		return nil, fmt.Errorf("missing tree family for store state lowering")
	}
	name := treeStoreStateName(treeType)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	members := semantic.TreeFamilyExactMembersInTagOrder(treeType)
	fields := make([]C.LLVMTypeRef, 0, len(members))
	for _, member := range members {
		tableType, err := g.ensureTreeExactTableType(member)
		if err != nil {
			return nil, err
		}
		fields = append(fields, tableType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func treePermStoreGlobalName(treeType *semantic.TreeType) string {
	return treeFamilyRuntimeName(treeType) + "__perm_tree_store"
}
func (g *llvmGenerator) ensureTreePermStoreGlobal(treeType *semantic.TreeType) (C.LLVMValueRef, error) {
	if treeType == nil || treeType.StoreType == nil {
		return nil, fmt.Errorf("missing tree family store metadata for perm store")
	}
	name := treePermStoreGlobalName(treeType)
	if value, ok := g.globals[name]; ok && value != nil {
		if C.LLVMIsDeclaration(value) != 0 {
			storeType, err := g.lowerTreeStoreType(treeType.StoreType)
			if err != nil {
				return nil, err
			}
			C.LLVMSetInitializer(value, C.LLVMConstNull(storeType))
			C.LLVMSetLinkage(value, C.LLVMPrivateLinkage)
		}
		return value, nil
	}
	value, err := g.addGlobal(name, treeType.StoreType, false)
	if err != nil {
		return nil, err
	}
	storeType, err := g.lowerTreeStoreType(treeType.StoreType)
	if err != nil {
		return nil, err
	}
	C.LLVMSetInitializer(value, C.LLVMConstNull(storeType))
	C.LLVMSetLinkage(value, C.LLVMPrivateLinkage)
	g.globals[name] = value
	return value, nil
}
func treeExactMemberByTag(treeType *semantic.TreeType, tag uint32) (semantic.Type, bool) {
	for _, member := range semantic.TreeFamilyExactMembersInTagOrder(treeType) {
		memberTag, ok := treeExactMemberTag(member)
		if ok && memberTag == tag {
			return member, true
		}
	}
	return nil, false
}
func treeCategoryMemberByTag(categoryType *semantic.TreeCategoryType, tag uint32) (semantic.Type, bool) {
	for _, member := range treeCategoryMembersInTagOrder(categoryType) {
		memberTag, ok := treeExactMemberTag(member)
		if ok && memberTag == tag {
			return member, true
		}
	}
	return nil, false
}
func (s *functionState) emitEntryStore(ptr C.LLVMValueRef, value C.LLVMValueRef) {
	if s == nil || s.g == nil || s.fnValue == nil || ptr == nil || value == nil {
		return
	}
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMGetEntryBasicBlock(s.fnValue)
	insertBefore := C.LLVMGetFirstInstruction(entry)
	for insertBefore != nil && C.LLVMGetInstructionOpcode(insertBefore) == C.LLVMAlloca {
		insertBefore = C.LLVMGetNextInstruction(insertBefore)
	}
	if insertBefore != nil {
		C.LLVMPositionBuilderBefore(builder, insertBefore)
	} else {
		C.LLVMPositionBuilderAtEnd(builder, entry)
	}
	C.LLVMBuildStore(builder, value, ptr)
}
func (s *functionState) buildTreeHandleValue(treeType *semantic.TreeType, stateValue C.LLVMValueRef, keyValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if treeType == nil {
		return nil, fmt.Errorf("missing tree family for handle value")
	}
	handleType, err := s.g.ensureTreeHandleCarrierType(treeType)
	if err != nil {
		return nil, err
	}
	handleValue := C.LLVMGetUndef(handleType)
	handleValue = C.LLVMBuildInsertValue(s.builder, handleValue, stateValue, 0, cStringFree(name+".state"))
	handleValue = C.LLVMBuildInsertValue(s.builder, handleValue, keyValue, 1, cStringFree(name+".key"))
	return handleValue, nil
}
func (s *functionState) emitTreeHandleStateValue(handleValue C.LLVMValueRef, name string) C.LLVMValueRef {
	return C.LLVMBuildExtractValue(s.builder, handleValue, 0, cStringFree(name+".state"))
}
func (s *functionState) emitTreeHandleKeyValue(handleValue C.LLVMValueRef, name string) C.LLVMValueRef {
	return C.LLVMBuildExtractValue(s.builder, handleValue, 1, cStringFree(name+".key"))
}
func (s *functionState) emitTreeTagValueFromKey(keyValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing tree handle lowering state")
	}
	u64Type, err := s.g.lowerBuiltin("u64")
	if err != nil {
		return nil, err
	}
	u32Type, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	shifted := C.LLVMBuildLShr(s.builder, keyValue, C.LLVMConstInt(u64Type, 32, 0), cStringFree(name+".tag.shift"))
	return C.LLVMBuildTrunc(s.builder, shifted, u32Type, cStringFree(name+".tag.trunc")), nil
}
func (s *functionState) emitTreeIndexValueFromKey(keyValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing tree handle lowering state")
	}
	u64Type, err := s.g.lowerBuiltin("u64")
	if err != nil {
		return nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	masked := C.LLVMBuildAnd(s.builder, keyValue, C.LLVMConstInt(u64Type, 0xffffffff, 0), cStringFree(name+".index.mask"))
	return C.LLVMBuildTrunc(s.builder, masked, usizeType, cStringFree(name+".index.trunc")), nil
}
func (s *functionState) emitTreeHandleTagValue(handleValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return s.emitTreeTagValueFromKey(s.emitTreeHandleKeyValue(handleValue, name), name)
}
func (s *functionState) emitTreeHandleIndexValue(handleValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return s.emitTreeIndexValueFromKey(s.emitTreeHandleKeyValue(handleValue, name), name)
}
func (s *functionState) buildTreeHandleKey(tag uint32, rowIndexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing tree handle lowering state")
	}
	u64Type, err := s.g.lowerBuiltin("u64")
	if err != nil {
		return nil, err
	}
	rowIndex64 := C.LLVMBuildZExt(s.builder, rowIndexValue, u64Type, cStringFree(name+".row.zext"))
	tagValue := C.LLVMConstInt(u64Type, C.ulonglong(tag), 0)
	tagShifted := C.LLVMBuildShl(s.builder, tagValue, C.LLVMConstInt(u64Type, 32, 0), cStringFree(name+".tag.shl"))
	return C.LLVMBuildOr(s.builder, tagShifted, rowIndex64, cStringFree(name+".key")), nil
}
func (s *functionState) emitTreeStoreFieldValueNamed(storeValue C.LLVMValueRef, index C.unsigned, name string) C.LLVMValueRef {
	return C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name))
}
func (s *functionState) emitTreeStoreArenaValueNamed(storeValue C.LLVMValueRef, name string) C.LLVMValueRef {
	return s.emitTreeStoreFieldValueNamed(storeValue, 0, name)
}
func (s *functionState) emitTreeStoreStateValueNamed(storeValue C.LLVMValueRef, name string) C.LLVMValueRef {
	return s.emitTreeStoreFieldValueNamed(storeValue, 1, name)
}
func (s *functionState) emitTreeStoreValueFromArenaRef(arenaRef C.LLVMValueRef, storeType *semantic.TreeStoreType) (C.LLVMValueRef, error) {
	if storeType == nil || storeType.Family == nil {
		return nil, fmt.Errorf("missing tree store metadata")
	}
	stateType, err := s.g.ensureTreeStoreStateType(storeType.Family)
	if err != nil {
		return nil, err
	}
	stateSizeBytes, err := s.g.abiSizeOfLLVMType(stateType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(stateSizeBytes), 0)
	statePtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, sizeValue}, "tree.store.state")
	C.LLVMBuildStore(s.builder, C.LLVMConstNull(stateType), statePtr)
	storeLLVMType, err := s.g.lowerTreeStoreType(storeType)
	if err != nil {
		return nil, err
	}
	storeValue := C.LLVMGetUndef(storeLLVMType)
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, arenaRef, 0, cStringFree("tree.store.arena"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, statePtr, 1, cStringFree("tree.store.state"))
	return storeValue, nil
}
func (s *functionState) emitPermArenaRef() (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing tree lowering state")
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return nil, fmt.Errorf("missing builtin Arena type")
	}
	return s.g.ensureGlobalDeclared("perm_arena", arenaType, false)
}
func (s *functionState) ensureTreeOwnerStoreValue(owner treeAllocOwnerBinding, family *semantic.TreeType) (C.LLVMValueRef, *semantic.TreeStoreType, error) {
	if family == nil || family.StoreType == nil {
		return nil, nil, fmt.Errorf("missing tree family store metadata")
	}
	if owner.storeValue != nil && owner.storeType != nil {
		return owner.storeValue, owner.storeType, nil
	}
	if owner.isPerm {
		globalStorePtr, err := s.g.ensureTreePermStoreGlobal(family)
		if err != nil {
			return nil, nil, err
		}
		storeValue, err := s.loadValue(globalStorePtr, family.StoreType, "tree.perm.store.load")
		if err != nil {
			return nil, nil, err
		}
		stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.perm.store.state")
		nullPtr := C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0))
		isReady := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), stateValue, nullPtr, cStringFree("tree.perm.store.ready"))
		initBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.perm.store.init"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.perm.store.cont"))
		C.LLVMBuildCondBr(s.builder, isReady, contBB, initBB)
		C.LLVMPositionBuilderAtEnd(s.builder, initBB)
		arenaRef, err := s.emitPermArenaRef()
		if err != nil {
			return nil, nil, err
		}
		initialized, err := s.emitTreeStoreValueFromArenaRef(arenaRef, family.StoreType)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, initialized, globalStorePtr)
		C.LLVMBuildBr(s.builder, contBB)
		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		resolved, err := s.loadValue(globalStorePtr, family.StoreType, "tree.perm.store.value")
		if err != nil {
			return nil, nil, err
		}
		return resolved, family.StoreType, nil
	}
	if owner.arenaRef == nil {
		return nil, nil, fmt.Errorf("missing Arena owner for tree store")
	}
	key := treeImplicitStoreCacheKey{family: family, isPerm: false, arena: owner.arenaRef}
	if s.treeImplicitStores == nil {
		s.treeImplicitStores = map[treeImplicitStoreCacheKey]treeImplicitStoreSlot{}
	}
	slot, ok := s.treeImplicitStores[key]
	if !ok || slot.ptr == nil {
		slotPtr, err := s.createEntryAlloca(treeFamilyRuntimeName(family)+".tree.store", family.StoreType)
		if err != nil {
			return nil, nil, err
		}
		zeroValue, err := s.zeroValue(family.StoreType)
		if err != nil {
			return nil, nil, err
		}
		s.emitEntryStore(slotPtr, zeroValue)
		slot = treeImplicitStoreSlot{ptr: slotPtr, storeType: family.StoreType}
		s.treeImplicitStores[key] = slot
	}
	storeValue, err := s.loadValue(slot.ptr, family.StoreType, "tree.implicit.store.load")
	if err != nil {
		return nil, nil, err
	}
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.implicit.store.state")
	nullPtr := C.LLVMConstPointerNull(C.LLVMPointerTypeInContext(s.g.context, 0))
	isReady := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), stateValue, nullPtr, cStringFree("tree.implicit.store.ready"))
	initBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.implicit.store.init"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.implicit.store.cont"))
	C.LLVMBuildCondBr(s.builder, isReady, contBB, initBB)
	C.LLVMPositionBuilderAtEnd(s.builder, initBB)
	initialized, err := s.emitTreeStoreValueFromArenaRef(owner.arenaRef, family.StoreType)
	if err != nil {
		return nil, nil, err
	}
	C.LLVMBuildStore(s.builder, initialized, slot.ptr)
	C.LLVMBuildBr(s.builder, contBB)
	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	resolved, err := s.loadValue(slot.ptr, family.StoreType, "tree.implicit.store.value")
	if err != nil {
		return nil, nil, err
	}
	return resolved, family.StoreType, nil
}
func (s *functionState) emitTreeStateTablePtr(stateValue C.LLVMValueRef, family *semantic.TreeType, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	if family == nil {
		return nil, fmt.Errorf("missing tree family for exact table access")
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, fmt.Errorf("missing exact tree member tag for %s", treeExactMemberSurfaceName(memberType))
	}
	stateType, err := s.g.ensureTreeStoreStateType(family)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, stateType, stateValue, C.unsigned(tag), cStringFree(name+".table")), nil
}
func (s *functionState) emitTreeTableCountValue(tablePtr C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 0, cStringFree(name+".count.ptr"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, usizeType, countPtr, cStringFree(name+".count")), nil
}
func (s *functionState) emitTreeTableCapacityValue(tablePtr C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return nil, err
	}
	capPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 1, cStringFree(name+".capacity.ptr"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, usizeType, capPtr, cStringFree(name+".capacity")), nil
}
func (s *functionState) emitTreeTableColumnPointerValue(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldIndex int, name string) (C.LLVMValueRef, error) {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return nil, err
	}
	columnPtrPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, C.unsigned(2+fieldIndex), cStringFree(name+".column.ptrptr"))
	opaquePtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	return C.LLVMBuildLoad2(s.builder, opaquePtrType, columnPtrPtr, cStringFree(name+".column.ptr")), nil
}
func (s *functionState) emitTreeTableSetColumnPointer(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldIndex int, columnPtr C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return err
	}
	columnPtrPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, C.unsigned(2+fieldIndex), cStringFree(name+".column.ptrptr"))
	C.LLVMBuildStore(s.builder, columnPtr, columnPtrPtr)
	return nil
}
func (s *functionState) emitTreeTableSetCount(tablePtr C.LLVMValueRef, memberType semantic.Type, countValue C.LLVMValueRef, name string) error {
	tableType, err := s.g.ensureTreeExactTableType(memberType)
	if err != nil {
		return err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, tableType, tablePtr, 0, cStringFree(name+".count.ptr"))
	C.LLVMBuildStore(s.builder, countValue, countPtr)
	return nil
}
