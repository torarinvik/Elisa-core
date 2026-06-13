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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) namedStateIsTarget(expr ast.Expr) (*semantic.StructType, []string, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.namedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	switch n := typedExpr.Type.(type) {
	case *ast.NamedType:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		return structType, append([]string(nil), structType.NamedStateCases...), true
	case *ast.GenericType:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		params := structGenericParams(structType)
		if len(n.Args) != len(params) {
			return nil, nil, false
		}
		for i, param := range params {
			if param.Kind != ast.GenericParamState {
				continue
			}
			cases, ok := namedStateCasesFromTypeExpr(n.Args[i])
			if !ok {
				return nil, nil, false
			}
			return structType, cases, true
		}
		return nil, nil, false
	default:
		return nil, nil, false
	}
}
func namedStateCasesFromTypeExpr(expr ast.TypeExpr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.NamedType:
		return []string{n.Name}, true
	case *ast.StateSetTypeExpr:
		return append([]string(nil), n.Cases...), true
	default:
		return nil, false
	}
}
func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	case *ast.ParenExpr:
		return derivedStateSelfFieldPath(n.Inner)
	default:
		return nil, false
	}
}
func (s *functionState) emitEnumCompareExpr(op lexer.TokenKind, enumType *semantic.EnumType, left C.LLVMValueRef, right C.LLVMValueRef, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil {
		return nil, nil, fmt.Errorf("missing enum type for comparison")
	}
	if enumType.Packed {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.packed")), resultType, nil
	}
	if enumIsTagOnly(enumType) {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.tagonly")), resultType, nil
	}
	leftTag := C.LLVMBuildExtractValue(s.builder, left, 0, cStringFree("enumcmp.left.tag"))
	rightTag := C.LLVMBuildExtractValue(s.builder, right, 0, cStringFree("enumcmp.right.tag"))
	equal := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftTag, rightTag, cStringFree("enumcmp.tag.eq"))

	payloadSlots, err := s.enumPayloadWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	if payloadSlots > 0 {
		leftPayload := C.LLVMBuildExtractValue(s.builder, left, 1, cStringFree("enumcmp.left.payload"))
		rightPayload := C.LLVMBuildExtractValue(s.builder, right, 1, cStringFree("enumcmp.right.payload"))
		for i := uint64(0); i < payloadSlots; i++ {
			nameSuffix := fmt.Sprintf(".%d", i)
			leftWord := C.LLVMBuildExtractValue(s.builder, leftPayload, C.unsigned(i), cStringFree("enumcmp.left.word"+nameSuffix))
			rightWord := C.LLVMBuildExtractValue(s.builder, rightPayload, C.unsigned(i), cStringFree("enumcmp.right.word"+nameSuffix))
			wordEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftWord, rightWord, cStringFree("enumcmp.word.eq"+nameSuffix))
			equal = C.LLVMBuildAnd(s.builder, equal, wordEqual, cStringFree("enumcmp.and"+nameSuffix))
		}
	}
	if op == lexer.TOKEN_BANGEQ {
		return C.LLVMBuildNot(s.builder, equal, cStringFree("enumcmp.ne")), resultType, nil
	}
	return equal, resultType, nil
}
func (s *functionState) encodePackedEnumHandle(rowPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.encodePackedEnumHandleWithStore(rowPtr, enumType, nil)
}
func (s *functionState) encodePackedEnumHandleWithStore(rowPtr C.LLVMValueRef, enumType *semantic.EnumType, storeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	if storeValue == nil {
		return nil, fmt.Errorf("packed enum %s encode requires store context", enumType.Name)
	}
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).encodeHandle(rowPtr, enumType, "packed.encode.store")
}
func (s *functionState) decodePackedEnumHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.decodePackedEnumHandleWithStore(handleValue, enumType, nil)
}
func (s *functionState) decodePackedEnumHandleWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s decode requires store context", enumType.Name)
	}
	return ops.decodeHandle(handleValue, enumType, "packed.decode.store")
}
func (s *functionState) enumPayloadWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	maxSlots := uint64(0)
	for _, variant := range enumType.Variants {
		slots, err := s.g.enumVariantPayloadSlots(enumType, variant)
		if err != nil {
			return 0, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	return maxSlots, nil
}
