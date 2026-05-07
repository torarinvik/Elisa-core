//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTreeRewriteDefaultValue(ctx *treeRewriteDefaultContext, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if ctx == nil || ctx.memberType == nil {
		return nil, nil, fmt.Errorf("default is only available while lowering a rewrite arm")
	}
	if resultType == nil {
		resultType = semantic.TreeRewriteResultTypeForValue(ctx.memberType)
	}
	memberType := semantic.StripAggregateStateType(ctx.memberType)
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("rewrite default requires an exact tree member")
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("rewrite default is missing an exact tree tag for %s", memberType.String())
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok {
		return nil, nil, fmt.Errorf("default requires an active in <owner>: scope")
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(owner, family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.default.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.default.store.state")
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	oneValue := C.LLVMConstInt(usizeLLVMType, 1, 0)
	if viewType, ok := semantic.StripAggregateStateType(memberType).(*semantic.TreeVariantViewType); ok && viewType != nil && viewType.Category != nil && viewType.Category.Layout == semantic.TreeLayoutCategoryUnion {
		sourceAccess, err := s.emitTreeHandleAccess(ctx.nodeValue, "tree.default.src")
		if err != nil {
			return nil, nil, err
		}
		sourceTablePtr, err := s.emitTreeCategoryUnionTablePtr(sourceAccess.stateValue, family, viewType.Category, "tree.default.src")
		if err != nil {
			return nil, nil, err
		}
		slot, err := s.emitTreeCategoryUnionAppendSlot(arenaValue, stateValue, family, viewType.Category, "tree.default")
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeCategoryUnionKindAtIndex(slot.tablePtr, viewType.Category, slot.rowIndex, tag, "tree.default"); err != nil {
			return nil, nil, err
		}
		offsetValue := zeroValue
		fieldDecls := treeExactFieldDecls(memberType)
		patchNames := make([]string, 0, len(fieldDecls))
		patchValues := make([]C.LLVMValueRef, 0, len(fieldDecls))
		for _, fieldDecl := range fieldDecls {
			field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
			}
			sourceFieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(ctx.nodeValue, family, memberType, fieldDecl.Name, "tree.default.src")
			if err != nil {
				return nil, nil, err
			}
			fieldValue := sourceFieldValue
			relation := semantic.TreeFieldStructuralRelation(family, field.Type)
			switch relation {
			case ast.EnumPayloadRelationChild:
				bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
				if !ok {
					return nil, nil, fmt.Errorf("rewrite default could not determine child result type for %s.%s", memberType.String(), fieldDecl.Name)
				}
				childResultType := bindingType
				if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
					childResultType = optionalBinding.Value
				}
				if optionalFieldType, ok := field.Type.(*semantic.OptionalType); ok {
					presentValue, err := s.extractOptionalPresent(sourceFieldValue, optionalFieldType)
					if err != nil {
						return nil, nil, err
					}
					childCount := C.LLVMBuildSelect(s.builder, presentValue, oneValue, zeroValue, cStringFree("tree.default.child.count"))
					payloadLLVMType, err := s.g.lowerType(optionalFieldType.Value)
					if err != nil {
						return nil, nil, err
					}
					presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.some"))
					absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.none"))
					contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.cont"))
					C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

					C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
					presentPayload, err := s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
					if err != nil {
						return nil, nil, err
					}
					presentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, absentBB)
					absentPayload, err := s.zeroValue(optionalFieldType.Value)
					if err != nil {
						return nil, nil, err
					}
					absentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, contBB)
					payloadPhi := C.LLVMBuildPhi(s.builder, payloadLLVMType, cStringFree("tree.default.child.payload"))
					C.LLVMAddIncoming(payloadPhi, llvmValueSlicePtr([]C.LLVMValueRef{presentPayload, absentPayload}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{presentEnd, absentEnd}), 2)
					fieldValue, err = s.buildOptionalValue(optionalFieldType, presentValue, payloadPhi)
					if err != nil {
						return nil, nil, err
					}
					offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, childCount, cStringFree("tree.default.child.offset.next"))
				} else {
					fieldValue, err = s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
					if err != nil {
						return nil, nil, err
					}
					offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, oneValue, cStringFree("tree.default.child.offset.next"))
				}
			case ast.EnumPayloadRelationChildren:
				bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
				if !ok {
					return nil, nil, fmt.Errorf("rewrite default could not determine children result type for %s.%s", memberType.String(), fieldDecl.Name)
				}
				childElemType := field.Type
				if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
					if viewType, ok := optionalBinding.Value.(*semantic.DArrayViewType); ok {
						childElemType = viewType.Elem
					}
				} else if viewType, ok := bindingType.(*semantic.DArrayViewType); ok {
					childElemType = viewType.Elem
				}
				countValue, err := s.emitTreeStructuralSequenceCount(sourceFieldValue, field.Type, "tree.default.children.count")
				if err != nil {
					return nil, nil, err
				}
				subViewValue, subViewType, err := s.emitTreeFoldChildResultsSubview(ctx.childViewValue, childElemType, offsetValue, countValue, "tree.default.children")
				if err != nil {
					return nil, nil, err
				}
				fieldValue, err = s.coerceTreeRewriteSequenceFieldValue(subViewValue, subViewType, field.Type, owner, "tree.default.children")
				if err != nil {
					return nil, nil, err
				}
				offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, countValue, cStringFree("tree.default.children.offset.next"))
			}
			if fieldValue != sourceFieldValue {
				patchNames = append(patchNames, fieldDecl.Name)
				patchValues = append(patchValues, fieldValue)
			}
		}
		if err := s.emitTreeCopyCategoryUnionPayloadAndPatch(sourceTablePtr, slot.tablePtr, viewType.Category, viewType.Variant, sourceAccess.rowIndex, slot.rowIndex, patchNames, patchValues, "tree.default"); err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeCategoryUnionTableSetCount(slot.tablePtr, viewType.Category, slot.neededCount, "tree.default"); err != nil {
			return nil, nil, err
		}
		keyValue, err := s.buildTreeHandleKey(tag, slot.rowIndex, "tree.default")
		if err != nil {
			return nil, nil, err
		}
		handleValue, err := s.buildTreeHandleValue(family, stateValue, keyValue, "tree.default")
		if err != nil {
			return nil, nil, err
		}
		return handleValue, resultType, nil
	}
	sourceAccess, err := s.emitTreeExactTableAccessFromHandle(ctx.nodeValue, family, memberType, "tree.default.src")
	if err != nil {
		return nil, nil, err
	}
	slot, err := s.emitTreeExactAppendSlot(arenaValue, stateValue, family, memberType, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	offsetValue := zeroValue
	fieldDecls := treeExactFieldDecls(memberType)
	if len(fieldDecls) > 0 {
		rowValue, _, err := s.emitTreeExactRowValueAtIndex(sourceAccess.tablePtr, memberType, sourceAccess.rowIndex, "tree.default.src")
		if err != nil {
			return nil, nil, err
		}
		for _, fieldDecl := range fieldDecls {
			field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
			}
			sourceFieldValue, _, err := s.emitTreeExactFieldValueFromRow(memberType, rowValue, fieldDecl.Name, "tree.default.src")
			if err != nil {
				return nil, nil, err
			}
			fieldValue := sourceFieldValue
			relation := semantic.TreeFieldStructuralRelation(family, field.Type)
			switch relation {
			case ast.EnumPayloadRelationChild:
				bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
				if !ok {
					return nil, nil, fmt.Errorf("rewrite default could not determine child result type for %s.%s", memberType.String(), fieldDecl.Name)
				}
				childResultType := bindingType
				if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
					childResultType = optionalBinding.Value
				}
				if optionalFieldType, ok := field.Type.(*semantic.OptionalType); ok {
					presentValue, err := s.extractOptionalPresent(sourceFieldValue, optionalFieldType)
					if err != nil {
						return nil, nil, err
					}
					childCount := C.LLVMBuildSelect(s.builder, presentValue, oneValue, zeroValue, cStringFree("tree.default.child.count"))
					payloadLLVMType, err := s.g.lowerType(optionalFieldType.Value)
					if err != nil {
						return nil, nil, err
					}
					presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.some"))
					absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.none"))
					contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.cont"))
					C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

					C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
					presentPayload, err := s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
					if err != nil {
						return nil, nil, err
					}
					presentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, absentBB)
					absentPayload, err := s.zeroValue(optionalFieldType.Value)
					if err != nil {
						return nil, nil, err
					}
					absentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, contBB)
					payloadPhi := C.LLVMBuildPhi(s.builder, payloadLLVMType, cStringFree("tree.default.child.payload"))
					C.LLVMAddIncoming(payloadPhi, llvmValueSlicePtr([]C.LLVMValueRef{presentPayload, absentPayload}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{presentEnd, absentEnd}), 2)
					fieldValue, err = s.buildOptionalValue(optionalFieldType, presentValue, payloadPhi)
					if err != nil {
						return nil, nil, err
					}
					offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, childCount, cStringFree("tree.default.child.offset.next"))
				} else {
					fieldValue, err = s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
					if err != nil {
						return nil, nil, err
					}
					offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, oneValue, cStringFree("tree.default.child.offset.next"))
				}
			case ast.EnumPayloadRelationChildren:
				bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
				if !ok {
					return nil, nil, fmt.Errorf("rewrite default could not determine children result type for %s.%s", memberType.String(), fieldDecl.Name)
				}
				childElemType := field.Type
				if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
					if viewType, ok := optionalBinding.Value.(*semantic.DArrayViewType); ok {
						childElemType = viewType.Elem
					}
				} else if viewType, ok := bindingType.(*semantic.DArrayViewType); ok {
					childElemType = viewType.Elem
				}
				countValue, err := s.emitTreeStructuralSequenceCount(sourceFieldValue, field.Type, "tree.default.children.count")
				if err != nil {
					return nil, nil, err
				}
				subViewValue, subViewType, err := s.emitTreeFoldChildResultsSubview(ctx.childViewValue, childElemType, offsetValue, countValue, "tree.default.children")
				if err != nil {
					return nil, nil, err
				}
				fieldValue, err = s.coerceTreeRewriteSequenceFieldValue(subViewValue, subViewType, field.Type, owner, "tree.default.children")
				if err != nil {
					return nil, nil, err
				}
				offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, countValue, cStringFree("tree.default.children.offset.next"))
			}
			if fieldValue != sourceFieldValue {
				rowValue, err = s.emitTreePatchExactRowFieldValue(memberType, rowValue, fieldDecl.Name, fieldValue, "tree.default")
				if err != nil {
					return nil, nil, err
				}
			}
		}
		if err := s.emitTreeStoreExactRowValue(slot.tablePtr, memberType, slot.rowIndex, rowValue, "tree.default"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(slot.tablePtr, memberType, slot.neededCount, "tree.default"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, slot.rowIndex, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(family, stateValue, keyValue, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, resultType, nil
}
func (s *functionState) emitTreeRewriteDefaultExpr(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("invalid rewrite default expression")
	}
	return s.emitTreeRewriteDefaultValue(s.treeRewriteDefault, s.exprType(expr))
}
func (s *functionState) coerceTreeRewriteSequenceFieldValue(viewValue C.LLVMValueRef, viewType semantic.Type, targetType semantic.Type, owner treeAllocOwnerBinding, name string) (C.LLVMValueRef, error) {
	if optionalType, ok := targetType.(*semantic.OptionalType); ok {
		payloadValue, err := s.coerceTreeRewriteSequenceFieldValue(viewValue, viewType, optionalType.Value, owner, name+".payload")
		if err != nil {
			return nil, err
		}
		presentValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		return s.buildOptionalValue(optionalType, presentValue, payloadValue)
	}
	view, ok := viewType.(*semantic.DArrayViewType)
	if !ok || view == nil {
		return nil, fmt.Errorf("rewrite default expected dview child results, got %s", viewType.String())
	}
	switch tt := targetType.(type) {
	case *semantic.DArrayViewType:
		return viewValue, nil
	case *semantic.DArrayType:
		return s.materializeTreeOwnerDArrayFromView(viewValue, view, tt, owner, name)
	default:
		return nil, fmt.Errorf("rewrite default does not know how to rebuild sequence field %s from %s", targetType.String(), viewType.String())
	}
}
func (s *functionState) emitTreeOwnerAllocBytes(owner treeAllocOwnerBinding, byteCount C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	if !owner.isPerm {
		if owner.arenaRef == nil && owner.storeValue != nil && owner.storeType != nil {
			arenaRef, err := s.emitTreeStoreArenaValue(owner.storeValue, owner.storeType)
			if err != nil {
				return nil, err
			}
			owner.arenaRef = arenaRef
		}
		if owner.arenaRef == nil {
			return nil, fmt.Errorf("missing Arena owner for tree rewrite default materialization")
		}
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidType := s.g.result.NamedTypes["void"]
		voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
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
		return s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{owner.arenaRef, byteCount}, name+".alloc"), nil
	}
	i64Type := s.g.result.NamedTypes["i64"]
	sizeValue, err := s.coerceValue(byteCount, usizeType, i64Type)
	if err != nil {
		return nil, err
	}
	heapVoidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("alloc_perm", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{sizeValue}, name+".alloc"), nil
}
