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

func (s *functionState) emitMembershipPointerCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, false, nil
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, operandType)
	if err != nil {
		return nil, true, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedLeft, rightValue, cStringFree("membership.ptr.eq"))
	return cmp, resultType != nil, nil
}
func (s *functionState) emitMembershipOptionalCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	if leftOptional, ok := leftType.(*semantic.OptionalType); ok && semantic.IsNullType(rightType) {
		presentValue, err := s.extractOptionalPresent(leftValue, leftOptional)
		if err != nil {
			return nil, true, err
		}
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
		rightValue, _, err := s.emitExpr(rightExpr, rightOptional)
		if err != nil {
			return nil, true, err
		}
		presentValue, err := s.extractOptionalPresent(rightValue, rightOptional)
		if err != nil {
			return nil, true, err
		}
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	return nil, false, nil
}
func (s *functionState) emitRuntimeStringCompareValues(op lexer.TokenKind, leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, error) {
	var (
		firstValue  C.LLVMValueRef
		secondValue C.LLVMValueRef
		err         error
	)
	if swap {
		firstValue, _, err = s.emitExpr(rightExpr, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, err = s.coerceValue(leftValue, leftType, secondType)
		if err != nil {
			return nil, err
		}
	} else {
		firstValue, err = s.coerceValue(leftValue, leftType, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, _, err = s.emitExpr(rightExpr, secondType)
		if err != nil {
			return nil, err
		}
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{firstValue, secondValue}, "membership.strcmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	return C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("membership.strcmp.eq")), nil
}
func (s *functionState) emitIsExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	// docs/85 §2: `subject is Law` was desugared by analysis into the call `Law(subject)`; emit it.
	if s.g != nil && s.g.result != nil && s.g.result.LawIsCalls != nil {
		if call := s.g.result.LawIsCalls[expr]; call != nil {
			return s.emitExpr(call, nil)
		}
	}
	targets := flattenIsTargetExprsBackend(expr.Right)
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("is expression is missing a target")
	}
	var combined C.LLVMValueRef
	for i, target := range targets {
		var (
			value C.LLVMValueRef
			err   error
		)
		if pattern, ok := s.structIsTargetPattern(target); ok {
			value, _, err = s.emitStructIsTest(expr.Left, pattern)
		} else if enumType, variant, pattern, ok := s.enumIsTargetPattern(target); ok {
			value, _, err = s.emitEnumIsTest(expr.Left, enumType, variant, pattern)
		} else if scrutinee, category, ok := s.enumCategoryIsTarget(expr.Left, target); ok {
			value, _, err = s.emitEnumCategoryIsTest(expr.Left, scrutinee, category)
		} else if base, cases, ok := s.namedStateIsTarget(target); ok {
			value, _, err = s.emitNamedStateIsTest(expr.Left, base, cases)
		} else {
			value, _, err = s.emitComparableIsTargetTest(expr.Left, target)
		}
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = value
			continue
		}
		combined = C.LLVMBuildOr(s.builder, combined, value, cStringFree("istest.or"))
	}
	return combined, s.g.result.NamedTypes["bool"], nil
}
