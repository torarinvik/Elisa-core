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
	"strings"
)

func (s *functionState) emitTupleExpr(expr *ast.TupleExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	tupleType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, nil, fmt.Errorf("tuple expression requires a tuple type, got %s", s.exprType(expr))
	}
	// Prefer a compatible EXPECTED tuple type: a match-expression arm's tuple is
	// emitted with the merged arm type, whose fields may adapt an element (a string
	// literal recorded as static u8& lowering into an sview field). Emitting each
	// element with the expected FIELD type triggers the same literal→view conversion
	// as a typed declaration, and the result struct matches the phi's type.
	if expectedTuple, ok := semantic.StripAggregateStateType(expected).(*semantic.TupleType); ok && expectedTuple != nil && len(expectedTuple.Fields) == len(tupleType.Fields) {
		tupleType = expectedTuple
	}
	llvmType, err := s.g.lowerType(tupleType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i, elem := range expr.Elems {
		if i >= len(tupleType.Fields) {
			break
		}
		elemValue, _, err := s.emitExpr(elem, tupleType.Fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, elemValue, C.unsigned(i), cStringFree("tuple.ins"))
	}
	return value, tupleType, nil
}
func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}
func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := s.lookupVisibleNamedType(ownerName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}
func qualifiedFieldOwnerAndLeaf(expr *ast.FieldExpr) (string, string, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], true
}
func qualifiedFieldParts(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return []string{n.Name}, true
	case *ast.FieldExpr:
		parts, ok := qualifiedFieldParts(n.Object)
		if !ok || n.Field == "" {
			return nil, false
		}
		return append(parts, n.Field), true
	case *ast.ParenExpr:
		return qualifiedFieldParts(n.Inner)
	default:
		return nil, false
	}
}
