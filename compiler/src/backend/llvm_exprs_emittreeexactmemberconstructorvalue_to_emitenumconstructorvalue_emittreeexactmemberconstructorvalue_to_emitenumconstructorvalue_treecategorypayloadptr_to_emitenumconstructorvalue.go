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

func (s *functionState) treeCategoryPayloadPtr(nodePtr C.LLVMValueRef, categoryType *semantic.TreeCategoryType) (C.LLVMValueRef, error) {
	if !treeCategoryHasPayloadStorage(categoryType) {
		return nil, fmt.Errorf("tree category %s has no lowered payload storage", categoryType.Name)
	}
	storageType, err := s.g.ensureTreeCategoryBody(categoryType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, storageType, nodePtr, C.unsigned(treeCategoryPayloadFieldIndex(categoryType)), cStringFree("tree.payload.ptr")), nil
}
func (s *functionState) extractTreeCategoryTagValue(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType) (C.LLVMValueRef, error) {
	if categoryType == nil {
		return nil, fmt.Errorf("missing tree category metadata")
	}
	return s.emitTreeHandleTagValue(nodeValue, "tree.tag")
}
func (s *functionState) extractTreeVariantPayloadValues(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if categoryType == nil || categoryType.Family == nil {
		return nil, fmt.Errorf("missing tree category metadata")
	}
	memberType := categoryType.VariantViewType(variant)
	if categoryType.Layout == semantic.TreeLayoutCategoryUnion {
		access, err := s.emitTreeHandleAccess(nodeValue, "tree.payload")
		if err != nil {
			return nil, err
		}
		tablePtr, err := s.emitTreeCategoryUnionTablePtr(access.stateValue, categoryType.Family, categoryType, "tree.payload")
		if err != nil {
			return nil, err
		}
		values := make([]C.LLVMValueRef, 0, len(variant.Payload))
		for i := range variant.Payload {
			fieldName := variant.PayloadLabel(i)
			if fieldName == "" {
				fieldName = fmt.Sprintf("payload%d", i)
			}
			value, _, err := s.emitTreeCategoryUnionFieldValueAtIndex(tablePtr, categoryType, variant, fieldName, access.rowIndex, "tree.payload.field")
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	}
	access, err := s.emitTreeExactTableAccessFromHandle(nodeValue, categoryType.Family, memberType, "tree.payload")
	if err != nil {
		return nil, err
	}
	rowValue, _, err := s.emitTreeExactRowValueAtIndex(access.tablePtr, memberType, access.rowIndex, "tree.payload")
	if err != nil {
		return nil, err
	}
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i := range variant.Payload {
		fieldName := variant.PayloadLabel(i)
		if fieldName == "" {
			fieldName = fmt.Sprintf("payload%d", i)
		}
		value, _, err := s.emitTreeExactFieldValueFromRow(memberType, rowValue, fieldName, "tree.payload.field")
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
func (s *functionState) emitEnumConstructorValue(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s must be allocated with new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name)
	}
	orderedArgs, err := s.resolveEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	if enumIsTagOnly(enumType) {
		tagValue, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, nil, err
		}
		return tagValue, enumType, nil
	}
	enumPtr, err := s.emitStackTempZeroed(enumType, "enum.ctor")
	if err != nil {
		return nil, nil, err
	}
	enumLLVMType, err := s.g.lowerType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("enum.tag.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, tagPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				C.LLVMBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("enum.payload.ins"))
			}
			if !allZero {
				C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	value, err := s.loadValue(enumPtr, enumType, "enum.value")
	if err != nil {
		return nil, nil, err
	}
	return value, enumType, nil
}
