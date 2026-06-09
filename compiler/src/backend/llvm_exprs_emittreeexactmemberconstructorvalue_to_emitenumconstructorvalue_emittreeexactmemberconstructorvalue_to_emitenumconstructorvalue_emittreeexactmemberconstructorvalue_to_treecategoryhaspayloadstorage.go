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

func (s *functionState) emitTreeExactMemberConstructorValue(callExpr *ast.CallExpr, memberType semantic.Type, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if memberType == nil {
		return nil, nil, fmt.Errorf("missing exact tree member constructor metadata")
	}
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("missing tree family metadata for %s", memberType.String())
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("missing exact tree member tag for %s", memberType.String())
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
		if (resolvedOwner.arenaRef != nil || resolvedOwner.arenaRefPtr != nil) && resolvedOwner.storeValue == nil && resolvedOwner.storePtr == nil && treeExactMemberLayoutPlan(memberType).isCategoryUnion() {
			arenaRef := resolvedOwner.arenaRef
			arenaRefPtr := resolvedOwner.arenaRefPtr
			if implicitOwner, ok := s.lookupImplicitTreeStoreOwnerForFamily(family); ok {
				resolvedOwner = implicitOwner
				resolvedOwner.arenaRef = arenaRef
				resolvedOwner.arenaRefPtr = arenaRefPtr
			}
		}
	} else {
		activeOwner, ok := s.lookupTreeAllocOwnerForFamily(family)
		if !ok {
			return nil, nil, fmt.Errorf("tree constructor %s requires an active in <owner>: scope or explicit new[owner]", memberType.String())
		}
		resolvedOwner = activeOwner
	}
	fieldDecls := treeExactFieldDecls(memberType)
	var orderedArgs []ast.Expr
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(fieldDecls) {
		orderedArgs = callExpr.ResolvedArgs
	} else if callExpr != nil {
		orderedArgs = callExpr.Args
	}
	if len(orderedArgs) != len(fieldDecls) {
		return nil, nil, fmt.Errorf("tree constructor %s expects %d fields, got %d", memberType.String(), len(fieldDecls), len(orderedArgs))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.exact.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.exact.store.state")
	fieldValues := make([]C.LLVMValueRef, 0, len(fieldDecls))
	for i, fieldDecl := range fieldDecls {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(orderedArgs[i], field.Type)
		if err != nil {
			return nil, nil, err
		}
		fieldValues = append(fieldValues, fieldValue)
	}
	if treeFamilyLayoutPlan(family).isCategoryUnion() {
		slot, err := s.emitTreeRootUnionAppendSlot(arenaValue, stateValue, family, "tree.exact")
		if err != nil {
			return nil, nil, err
		}
		tagType, err := s.g.lowerBuiltin("u32")
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeRootUnionKindAtIndex(slot.tablePtr, family, slot.rowIndex, C.LLVMConstInt(tagType, C.ulonglong(tag), 0), "tree.exact"); err != nil {
			return nil, nil, err
		}
		payloadType, err := s.g.lowerTreeRootUnionExactPayloadType(memberType)
		if err != nil {
			return nil, nil, err
		}
		if C.LLVMGetTypeKind(payloadType) != C.LLVMVoidTypeKind {
			payloadValue := C.LLVMGetUndef(payloadType)
			for i, fieldValue := range fieldValues {
				payloadValue = C.LLVMBuildInsertValue(s.builder, payloadValue, fieldValue, C.unsigned(i), cStringFree("tree.exact.payload.field"))
			}
			if err := s.emitTreeRootUnionPayloadAtIndex(slot.tablePtr, family, slot.rowIndex, payloadType, payloadValue, "tree.exact"); err != nil {
				return nil, nil, err
			}
		}
		if err := s.emitTreeRootUnionTableSetCount(slot.tablePtr, family, slot.neededCount, "tree.exact"); err != nil {
			return nil, nil, err
		}
		handleValue, err := s.buildTreeHandleValue(family, stateValue, slot.rowIndex, "tree.exact")
		if err != nil {
			return nil, nil, err
		}
		return handleValue, memberType, nil
	}
	slot, err := s.emitTreeExactAppendSlot(arenaValue, stateValue, family, memberType, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitTreeStoreExactRowValueAtIndex(slot.tablePtr, memberType, slot.rowIndex, fieldValues, "tree.exact"); err != nil {
		return nil, nil, err
	}
	if err := s.emitTreeTableSetCount(slot.tablePtr, memberType, slot.neededCount, "tree.exact"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, slot.rowIndex, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(family, stateValue, keyValue, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, memberType, nil
}
func (s *functionState) resolveTreeConstructorArgs(callExpr *ast.CallExpr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if treeType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing tree constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) {
		return callExpr.ResolvedArgs, callExpr.ResolvedCommonArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("tree constructor %s.%s cannot mix positional and named arguments", treeType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("tree constructor %s.%s payload field %q is specified more than once", treeType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := treeType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("tree constructor %s.%s common field %q is specified more than once", treeType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("tree constructor %s.%s has no payload or common field %q", treeType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("tree constructor %s.%s is missing argument %d", treeType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("tree constructor %s.%s is missing payload field %q", treeType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = commonArgs
	}
	return ordered, commonArgs, nil
}
func (s *functionState) emitTreeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	objType := s.exprType(expr.Object)
	handleValue, baseType, err := s.emitTreeHandleValue(expr.Object, objType)
	if err != nil || handleValue == nil || baseType == nil {
		return nil, nil, false, err
	}
	switch tt := baseType.(type) {
	case *semantic.TreeVariantViewType:
		if field, ok := semantic.TreeVariantSurfaceFieldInfo(tt, expr.Field); ok {
			if expr.Field == "kind" {
				if kindType, ok := semantic.TreeKindType(tt); ok && kindType != nil && semantic.SameType(field.Type, kindType) {
					llvmType, err := s.g.lowerType(kindType)
					if err != nil {
						return nil, nil, true, err
					}
					value := C.LLVMConstInt(llvmType, C.ulonglong(tt.Variant.Tag), 0)
					return value, kindType, true, nil
				}
			}
			if tt.Category != nil && treeCategoryLayoutPlan(tt.Category).isCategoryUnion() {
				access, err := s.emitTreeCategoryUnionTableAccessFromHandle(handleValue, tt.Category.Family, tt.Category, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				surfaceValue, surfaceType, err := s.emitTreeCategoryUnionSurfaceFieldValue(access.tablePtr, tt.Category, tt.Variant, expr.Field, access.rowIndex, "tree.field")
				return surfaceValue, surfaceType, true, err
			}
			if tt.Category != nil && tt.Category.Family != nil && treeFamilyLayoutPlan(tt.Category.Family).isCategoryUnion() {
				stateValue, err := s.emitTreeCategoryUnionContextStateValue(tt.Category.Family, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, tt.Category.Family, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				surfaceValue, surfaceType, err := s.emitTreeRootUnionExactFieldValueAtIndex(tablePtr, tt.Category.Family, tt, expr.Field, rowIndex, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				value, valueType, err := s.treeFieldSurfaceValue(surfaceValue, surfaceType, field.Type, "tree.field")
				return value, valueType, true, err
			}
			access, err := s.emitTreeExactTableAccessFromHandle(handleValue, tt.Category.Family, tt, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			surfaceValue, surfaceType, err := s.emitTreeExactSurfaceFieldValue(access.tablePtr, tt, expr.Field, access.rowIndex, "tree.field")
			return surfaceValue, surfaceType, true, err
		}
		return nil, nil, true, fmt.Errorf("%s has no field %s", tt.String(), expr.Field)
	case *semantic.TreeNodeType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", tt.String())
			}
			if tt.Family != nil && treeFamilyLayoutPlan(tt.Family).isCategoryUnion() {
				stateValue, err := s.emitTreeCategoryUnionContextStateValue(tt.Family, "tree.field.kind")
				if err != nil {
					return nil, nil, true, err
				}
				tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, tt.Family, "tree.field.kind")
				if err != nil {
					return nil, nil, true, err
				}
				rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field.kind")
				if err != nil {
					return nil, nil, true, err
				}
				value, err := s.emitTreeRootUnionKindValueAtIndex(tablePtr, tt.Family, rowIndex, "tree.field.kind")
				if err != nil {
					return nil, nil, true, err
				}
				return value, kindType, true, nil
			}
			value, err := s.emitTreeHandleTagValue(handleValue, "tree.field.kind")
			if err != nil {
				return nil, nil, true, err
			}
			return value, kindType, true, nil
		}
		return nil, nil, true, fmt.Errorf("%s has no field %s", tt.String(), expr.Field)
	case *semantic.TreeCategoryType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", tt.String())
			}
			value, err := s.extractTreeCategoryTagValue(handleValue, tt)
			if err != nil {
				return nil, nil, true, err
			}
			return value, kindType, true, nil
		}
		if field, ok := semantic.TreeCategorySurfaceFieldInfo(tt, expr.Field); ok {
			tagValue, err := s.extractTreeCategoryTagValue(handleValue, tt)
			if err != nil {
				return nil, nil, true, err
			}
			var categoryUnionAccess treeCategoryUnionTableAccess
			if treeCategoryLayoutPlan(tt).isCategoryUnion() {
				access, err := s.emitTreeCategoryUnionTableAccessFromHandle(handleValue, tt.Family, tt, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				categoryUnionAccess = access
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(tt.Variants)))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, variant := range tt.Variants {
				memberType := tt.VariantViewType(variant)
				if _, ok := semantic.TreeVariantSurfaceFieldInfo(memberType, expr.Field); !ok {
					continue
				}
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.case"))
				tagConst, err := s.enumTagConstant(variant.Tag)
				if err != nil {
					return nil, nil, true, err
				}
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				var surfaceValue C.LLVMValueRef
				if treeCategoryLayoutPlan(tt).isCategoryUnion() {
					surfaceValue, _, err = s.emitTreeCategoryUnionSurfaceFieldValue(categoryUnionAccess.tablePtr, tt, variant, expr.Field, categoryUnionAccess.rowIndex, "tree.field")
					if err != nil {
						return nil, nil, true, err
					}
				} else {
					access, err := s.emitTreeExactTableAccessFromHandle(handleValue, tt.Family, memberType, "tree.field")
					if err != nil {
						return nil, nil, true, err
					}
					surfaceValue, _, err = s.emitTreeExactSurfaceFieldValue(access.tablePtr, memberType, expr.Field, access.rowIndex, "tree.field")
					if err != nil {
						return nil, nil, true, err
					}
				}
				incomingValues = append(incomingValues, surfaceValue)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if len(incomingValues) == 0 {
				return nil, nil, true, fmt.Errorf("%s has no field %s", tt.String(), expr.Field)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, nil, true, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			llvmFieldType, err := s.g.lowerType(field.Type)
			if err != nil {
				return nil, nil, true, err
			}
			phi := C.LLVMBuildPhi(s.builder, llvmFieldType, cStringFree("tree.field.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, field.Type, true, nil
		}
		return nil, nil, false, nil
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", baseType.String())
			}
			llvmType, err := s.g.lowerType(kindType)
			if err != nil {
				return nil, nil, true, err
			}
			tag, ok := semantic.TreeExactTag(tt)
			if !ok {
				return nil, nil, true, fmt.Errorf("%s has no exact tree tag", baseType.String())
			}
			value := C.LLVMConstInt(llvmType, C.ulonglong(tag), 0)
			return value, kindType, true, nil
		}
		field, ok := semantic.TreeExactSurfaceFieldInfo(tt, expr.Field)
		if !ok {
			return nil, nil, true, fmt.Errorf("%s has no field %s", baseType.String(), expr.Field)
		}
		if family := treeExactMemberFamily(tt); family != nil && treeFamilyLayoutPlan(family).isCategoryUnion() {
			stateValue, err := s.emitTreeCategoryUnionContextStateValue(family, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, family, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			fieldValue, fieldType, err := s.emitTreeRootUnionExactFieldValueAtIndex(tablePtr, family, tt, expr.Field, rowIndex, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			surfaceValue, surfaceType, err := s.treeFieldSurfaceValue(fieldValue, fieldType, field.Type, "tree.field")
			return surfaceValue, surfaceType, true, err
		}
		access, err := s.emitTreeExactTableAccessFromHandle(handleValue, treeExactMemberFamily(tt), tt, "tree.field")
		if err != nil {
			return nil, nil, true, err
		}
		surfaceValue, surfaceType, err := s.emitTreeExactSurfaceFieldValue(access.tablePtr, tt, expr.Field, access.rowIndex, "tree.field")
		return surfaceValue, surfaceType, true, err
	default:
		return nil, nil, false, nil
	}
}
func (s *functionState) buildDynArrayViewValue(arrayValue C.LLVMValueRef, arrayType *semantic.DArrayType, viewType *semantic.ViewType, name string) (C.LLVMValueRef, error) {
	if s == nil || arrayType == nil || viewType == nil {
		return nil, fmt.Errorf("missing dynamic array view conversion metadata")
	}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	dataValue := C.LLVMBuildExtractValue(s.builder, arrayValue, 0, cStringFree(name+".data"))
	lenValue := C.LLVMBuildExtractValue(s.builder, arrayValue, 1, cStringFree(name+".len"))
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataValue, 0, cStringFree(name+".view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, lenValue, 1, cStringFree(name+".view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree(name+".view.elem_size"))
	return viewValue, nil
}
func (s *functionState) treeFieldSurfaceValue(value C.LLVMValueRef, rawType semantic.Type, surfaceType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	if rawType == nil {
		return nil, nil, fmt.Errorf("missing raw tree field type")
	}
	if surfaceType == nil || semantic.SameType(rawType, surfaceType) {
		return value, rawType, nil
	}
	if rawOptional, ok := rawType.(*semantic.OptionalType); ok {
		surfaceOptional, ok := surfaceType.(*semantic.OptionalType)
		if !ok || surfaceOptional == nil || rawOptional == nil || rawOptional.Value == nil || surfaceOptional.Value == nil {
			return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
		}
		presentValue, err := s.extractOptionalPresent(value, rawOptional)
		if err != nil {
			return nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(value, rawOptional)
		if err != nil {
			return nil, nil, err
		}
		surfacePayload, _, err := s.treeFieldSurfaceValue(payloadValue, rawOptional.Value, surfaceOptional.Value, name+".optional")
		if err != nil {
			return nil, nil, err
		}
		optionalValue, err := s.buildOptionalValue(surfaceOptional, presentValue, surfacePayload)
		if err != nil {
			return nil, nil, err
		}
		return optionalValue, surfaceOptional, nil
	}
	rawArray, ok := rawType.(*semantic.DArrayType)
	if !ok || rawArray == nil {
		return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
	}
	viewType, ok := surfaceType.(*semantic.ViewType)
	if !ok || viewType == nil || !semantic.SameType(rawArray.Elem, viewType.Elem) {
		return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
	}
	viewValue, err := s.buildDynArrayViewValue(value, rawArray, viewType, name+".surface")
	if err != nil {
		return nil, nil, err
	}
	return viewValue, viewType, nil
}
func (s *functionState) emitTreeExactSurfaceFieldValue(tablePtr C.LLVMValueRef, memberType semantic.Type, fieldName string, rowIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	field, ok := semantic.TreeExactSurfaceFieldInfo(memberType, fieldName)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	value, rawType, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldName, rowIndex, name)
	if err != nil {
		return nil, nil, err
	}
	return s.treeFieldSurfaceValue(value, rawType, field.Type, name)
}
func (s *functionState) emitTreeExactSurfaceFieldValueFromRow(memberType semantic.Type, rowValue C.LLVMValueRef, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	field, ok := semantic.TreeExactSurfaceFieldInfo(memberType, fieldName)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no field %s", treeExactMemberSurfaceName(memberType), fieldName)
	}
	value, rawType, err := s.emitTreeExactFieldValueFromRow(memberType, rowValue, fieldName, name)
	if err != nil {
		return nil, nil, err
	}
	return s.treeFieldSurfaceValue(value, rawType, field.Type, name)
}
func (s *functionState) emitTreeHandleValue(expr ast.Expr, objType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	baseType := semantic.StripAggregateStateType(objType)
	if refType, ok := baseType.(*semantic.RefType); ok {
		if _, ok := treeNodeHandleFamily(refType.Elem); !ok {
			return nil, nil, fmt.Errorf("tree field access requires a tree node base")
		}
		valuePtr, _, err := s.emitExpr(expr, objType)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.loadValue(valuePtr, refType.Elem, "tree.handle.load")
		if err != nil {
			return nil, nil, err
		}
		return value, refType.Elem, nil
	}
	if _, ok := treeNodeHandleFamily(baseType); !ok {
		return nil, nil, fmt.Errorf("tree field access requires a tree node base")
	}
	value, _, err := s.emitExpr(expr, objType)
	if err != nil {
		return nil, nil, err
	}
	return value, baseType, nil
}
func (s *functionState) emitTreeCommonFieldAddress(objExpr ast.Expr, objType semantic.Type, fieldName string) (C.LLVMValueRef, semantic.Type, error) {
	return nil, nil, fmt.Errorf("tree field address .%s is not supported for handle-lowered tree values", fieldName)
}
func treeCategoryCommonFieldInfo(categoryType *semantic.TreeCategoryType, fieldName string) (semantic.Field, int, error) {
	for i, fieldDecl := range treeCommonFieldDecls(categoryType) {
		if fieldDecl.Name != fieldName {
			continue
		}
		field, ok := categoryType.Common[fieldName]
		if !ok {
			return semantic.Field{}, 0, fmt.Errorf("missing tree common field %s.%s", categoryType.Name, fieldName)
		}
		return field, 1 + i, nil
	}
	return semantic.Field{}, 0, fmt.Errorf("tree category %s has no common field %s", categoryType.Name, fieldName)
}
func treeCategoryPayloadFieldIndex(categoryType *semantic.TreeCategoryType) int {
	return 1 + len(treeCommonFieldDecls(categoryType))
}
func treeCategoryHasPayloadStorage(categoryType *semantic.TreeCategoryType) bool {
	if categoryType == nil {
		return false
	}
	for _, variant := range categoryType.Variants {
		if len(variant.Payload) != 0 {
			return true
		}
	}
	return false
}
