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
	"strings"
)

func appendIsTargetExprsBackend(out []ast.Expr, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return out
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return appendIsTargetExprsBackend(out, n.Inner)
	case *ast.IsAliasExpr:
		return appendIsTargetExprsBackend(out, n.Target)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			out = appendIsTargetExprsBackend(out, target)
		}
		return out
	default:
		return append(out, expr)
	}
}
func flattenIsTargetExprsBackend(expr ast.Expr) []ast.Expr {
	return appendIsTargetExprsBackend(nil, expr)
}
func (s *functionState) emitComparableIsTargetTest(leftExpr ast.Expr, targetExpr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(targetExpr)
	resultType := s.g.result.NamedTypes["bool"]
	synthetic := &ast.BinaryExpr{Position: leftExpr.Pos(), Op: lexer.TOKEN_EQEQ, Left: leftExpr, Right: targetExpr}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(leftType, rightType); ok {
		return s.emitRuntimeStringCompareExpr(synthetic, helperName, firstType, secondType, swap)
	}
	if value, actualType, handled, err := s.emitOptionalCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	left, _, err := s.emitExpr(leftExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(targetExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok {
		return s.emitEnumCompareExpr(lexer.TOKEN_EQEQ, enumType, left, right, resultType)
	}
	if isFloatType(operandType) {
		return C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, left, right, cStringFree("isvalue.eq")), resultType, nil
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), left, right, cStringFree("isvalue.eq")), resultType, nil
}
func (s *functionState) emitEnumIsTest(leftExpr ast.Expr, enumType *semantic.EnumType, variant *semantic.EnumVariant, pattern *ast.MatchVariantPattern) (C.LLVMValueRef, semantic.Type, error) {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, leftExpr, nil)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(leftExpr, enumType)
	if err != nil {
		return nil, nil, err
	}
	if pattern != nil && len(pattern.Args) != 0 {
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.ok"))
		failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.cont"))
		if _, _, err := s.emitMatchPatternTest(pattern, enumValue, nil, enumType, storeBinding, leftExpr, nil, successBB, failureBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		C.LLVMBuildBr(s.builder, contBB)
		successEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
		failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
		C.LLVMBuildBr(s.builder, contBB)
		failureEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.variant.result"))
		values := []C.LLVMValueRef{successValue, failureValue}
		blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
		return phi, s.g.result.NamedTypes["bool"], nil
	}
	tagValue, err := s.extractEnumTagValue(enumValue, nil, enumType, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	tagConst, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("istag"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

// emitEnumCategoryIsTest lowers a bare-category `is` test (`e is Statement`, docs/77 §2) to the
// docs/81 range primitive against the category's dense leaf-tag range. A widening test (the
// scrutinee's static type already descends from the category) is constant true; an empty category
// is constant false. The scrutinee is still evaluated for effects in the constant cases.
func (s *functionState) emitEnumCategoryIsTest(leftExpr ast.Expr, scrutinee *semantic.EnumType, category *semantic.EnumType) (C.LLVMValueRef, semantic.Type, error) {
	boolType := s.g.result.NamedTypes["bool"]
	storeBinding, err := s.resolvePackedMatchStoreBinding(scrutinee, leftExpr, nil)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(leftExpr, scrutinee)
	if err != nil {
		return nil, nil, err
	}
	if semantic.EnumDescendsFrom(scrutinee, category) {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), boolType, nil
	}
	if category.LeafTagCount == 0 {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0), boolType, nil
	}
	tagValue, err := s.extractEnumTagValue(enumValue, nil, scrutinee, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	return s.emitTagRangeTest(tagValue, category.LeafTagLo, category.LeafTagCount)
}

// emitTagRangeTest emits the docs/81 category-membership primitive: matches ⇔ tag - lo <u count.
// count==1 folds to plain equality (a leaf is a range of size 1), lo==0 skips the subtract.
func (s *functionState) emitTagRangeTest(tagValue C.LLVMValueRef, lo uint32, count uint32) (C.LLVMValueRef, semantic.Type, error) {
	boolType := s.g.result.NamedTypes["bool"]
	loConst, err := s.enumTagConstant(lo)
	if err != nil {
		return nil, nil, err
	}
	if count == 1 {
		return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, loConst, cStringFree("istag")), boolType, nil
	}
	relative := tagValue
	if lo != 0 {
		relative = C.LLVMBuildSub(s.builder, tagValue, loConst, cStringFree("istag.rel"))
	}
	countConst, err := s.enumTagConstant(count)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), relative, countConst, cStringFree("istag.range")), boolType, nil
}

// enumCategoryArm resolves a bind-arm name to a sub-category of a hierarchy scrutinee (docs/77 §2),
// mirroring the analyzer's resolveEnumCategoryArm so the backend dispatches the same arms the
// analyzer accepted. A plain (non-category) bind name returns false and stays a catch-all bind.
func (s *functionState) enumCategoryArm(enumType *semantic.EnumType, name string) (*semantic.EnumType, bool) {
	if enumType == nil || name == "" || (enumType.Parent == nil && len(enumType.Children) == 0) {
		return nil, false
	}
	category, ok := s.g.result.NamedTypes[name].(*semantic.EnumType)
	if !ok || category == nil || !semantic.EnumDescendsFrom(category, enumType) {
		return nil, false
	}
	return category, true
}

// enumCategoryIsTarget recognizes a bare enum-category `is` target (docs/77): the target names an
// enum TYPE (no `.Variant`) and the scrutinee is a hierarchical enum related to it. Mirrors the
// analyzer's resolveEnumCategoryIsTarget gating so flat-enum `is` lowering is unchanged.
func (s *functionState) enumCategoryIsTarget(leftExpr ast.Expr, target ast.Expr) (*semantic.EnumType, *semantic.EnumType, bool) {
	scrutinee, ok := semantic.StripAggregateStateType(s.exprType(leftExpr)).(*semantic.EnumType)
	if !ok || scrutinee == nil || (scrutinee.Parent == nil && len(scrutinee.Children) == 0) {
		return nil, nil, false
	}
	name := ""
	switch e := target.(type) {
	case *ast.ParenExpr:
		if e == nil {
			return nil, nil, false
		}
		return s.enumCategoryIsTarget(leftExpr, e.Inner)
	case *ast.TypeExprExpr:
		if e == nil {
			return nil, nil, false
		}
		named, ok := e.Type.(*ast.NamedType)
		if !ok || named == nil || strings.Contains(named.Name, ".") {
			return nil, nil, false // dotted ⇒ a variant target, handled elsewhere
		}
		name = named.Name
	case *ast.Ident:
		if e == nil {
			return nil, nil, false
		}
		name = e.Name
	default:
		return nil, nil, false
	}
	category, ok := s.g.result.NamedTypes[name].(*semantic.EnumType)
	if !ok || category == nil {
		return nil, nil, false
	}
	if !semantic.EnumDescendsFrom(category, scrutinee) && !semantic.EnumDescendsFrom(scrutinee, category) {
		return nil, nil, false
	}
	return scrutinee, category, true
}
func (s *functionState) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.structIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return s.structIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.StructTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	return nil, false
}
func (s *functionState) emitStructIsTest(leftExpr ast.Expr, pattern *ast.MatchStructPattern) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	value, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, err
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.ok"))
	failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.fail"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.cont"))
	if _, _, err := s.emitMatchPatternTest(pattern, value, nil, leftType, nil, leftExpr, nil, successBB, failureBB); err != nil {
		return nil, nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	C.LLVMBuildBr(s.builder, contBB)
	successEnd := C.LLVMGetInsertBlock(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
	failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	C.LLVMBuildBr(s.builder, contBB)
	failureEnd := C.LLVMGetInsertBlock(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.struct.result"))
	values := []C.LLVMValueRef{successValue, failureValue}
	blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) enumIsTargetPattern(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, *ast.MatchVariantPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.enumIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return s.enumIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		base, ok := s.g.result.NamedTypes[testExpr.Pattern.EnumName]
		if !ok {
			return nil, nil, nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || enumType == nil {
			return nil, nil, nil, false
		}
		variant, ok := enumType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return nil, nil, nil, false
		}
		return enumType, variant, testExpr.Pattern, true
	}
	enumType, variant, ok := s.enumIsTarget(expr)
	if !ok || enumType == nil || variant == nil {
		return nil, nil, nil, false
	}
	pattern := &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: enumType.Name, Variant: variant.Name}
	return enumType, variant, pattern, true
}
func (s *functionState) emitNamedStateIsTest(leftExpr ast.Expr, base *semantic.StructType, cases []string) (C.LLVMValueRef, semantic.Type, error) {
	if base == nil {
		return nil, nil, fmt.Errorf("missing named-state struct metadata")
	}
	if len(cases) == 0 {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0), s.g.result.NamedTypes["bool"], nil
	}
	if len(cases) == len(base.NamedStateCases) {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), s.g.result.NamedTypes["bool"], nil
	}
	selfType := s.exprType(leftExpr)
	selfValue, _, err := s.emitExpr(leftExpr, selfType)
	if err != nil {
		return nil, nil, err
	}
	var combined C.LLVMValueRef
	for i, stateName := range cases {
		pred, err := s.emitDerivedStatePredicate(base, stateName, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = pred
			continue
		}
		combined = C.LLVMBuildOr(s.builder, combined, pred, cStringFree("isstate.or"))
	}
	return combined, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) emitDerivedStatePredicate(base *semantic.StructType, stateName string, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, error) {
	if base == nil || base.DerivedStateMap == nil {
		return nil, fmt.Errorf("struct %q is missing derived state metadata", base.Name)
	}
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return nil, fmt.Errorf("struct %q has no derived state rule for %q", base.Name, stateName)
	}
	value, resultType, err := s.emitDerivedStateExpr(derived.Condition, selfValue, selfType)
	if err != nil {
		return nil, err
	}
	if !semantic.IsBoolType(resultType) {
		return nil, fmt.Errorf("derived state %s.%s does not evaluate to bool", base.Name, stateName)
	}
	return value, nil
}
func (s *functionState) emitDerivedStateExpr(expr ast.Expr, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		value, fieldType, err := s.emitDerivedStateSelfPath(selfValue, selfType, path)
		return value, fieldType, err
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitDerivedStateExpr(n.Inner, selfValue, selfType)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return s.emitExpr(expr, nil)
	case *ast.UnaryExpr:
		operand, operandType, err := s.emitDerivedStateExpr(n.Operand, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.not")), s.g.result.NamedTypes["bool"], nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFNeg(s.builder, operand, cStringFree("isstate.fneg")), operandType, nil
			}
			return C.LLVMBuildNeg(s.builder, operand, cStringFree("isstate.neg")), operandType, nil
		case lexer.TOKEN_TILDE:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.bitnot")), operandType, nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state unary operator %s", lexer.TokenName(n.Op))
		}
	case *ast.BinaryExpr:
		leftValue, leftType, err := s.emitDerivedStateExpr(n.Left, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		rightValue, rightType, err := s.emitDerivedStateExpr(n.Right, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			if n.Op == lexer.TOKEN_AND {
				return C.LLVMBuildAnd(s.builder, leftValue, rightValue, cStringFree("isstate.and")), s.g.result.NamedTypes["bool"], nil
			}
			return C.LLVMBuildOr(s.builder, leftValue, rightValue, cStringFree("isstate.or")), s.g.result.NamedTypes["bool"], nil
		}
		if (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) && (isPointerLikeType(leftType) || semantic.IsNullType(leftType)) && (isPointerLikeType(rightType) || semantic.IsNullType(rightType)) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.ptrcmp")), s.g.result.NamedTypes["bool"], nil
		}
		if semantic.IsBoolType(leftType) && semantic.IsBoolType(rightType) && (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.boolcmp")), s.g.result.NamedTypes["bool"], nil
		}
		operandType := s.binaryOperandType(n.Op, leftType, rightType)
		coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
		if err != nil {
			return nil, nil, err
		}
		coercedRight, err := s.coerceValue(rightValue, rightType, operandType)
		if err != nil {
			return nil, nil, err
		}
		resultType := s.exprType(n)
		if resultType == nil {
			resultType = operandType
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
			}
			return C.LLVMBuildAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
			}
			return C.LLVMBuildSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
		case lexer.TOKEN_STAR:
			if isFloatType(operandType) {
				return C.LLVMBuildFMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
			}
			return C.LLVMBuildMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
		case lexer.TOKEN_SLASH:
			if isFloatType(operandType) {
				return C.LLVMBuildFDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			return C.LLVMBuildUDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
		case lexer.TOKEN_PERCENT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSRem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
			}
			return C.LLVMBuildURem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
		case lexer.TOKEN_PIPE:
			return C.LLVMBuildOr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitor")), resultType, nil
		case lexer.TOKEN_CARET:
			return C.LLVMBuildXor(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitxor")), resultType, nil
		case lexer.TOKEN_AMPERSAND:
			return C.LLVMBuildAnd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitand")), resultType, nil
		case lexer.TOKEN_LSHIFT:
			return C.LLVMBuildShl(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shl")), resultType, nil
		case lexer.TOKEN_RSHIFT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildAShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
			}
			return C.LLVMBuildLShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			if isFloatType(operandType) {
				pred, err := llvmFloatPredicate(n.Op)
				if err != nil {
					return nil, nil, err
				}
				return C.LLVMBuildFCmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.fcmp")), s.g.result.NamedTypes["bool"], nil
			}
			pred, err := llvmIntPredicate(n.Op, operandType)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildICmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.icmp")), s.g.result.NamedTypes["bool"], nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state binary operator %s", lexer.TokenName(n.Op))
		}
	default:
		return nil, nil, fmt.Errorf("unsupported derived-state expression %T", expr)
	}
}
func (s *functionState) emitDerivedStateSelfPath(selfValue C.LLVMValueRef, selfType semantic.Type, path []string) (C.LLVMValueRef, semantic.Type, error) {
	currentValue := selfValue
	currentType := selfType
	if len(path) == 0 {
		return currentValue, currentType, nil
	}
	for _, field := range path {
		fieldType, index, _, pointerLike, err := s.g.fieldInfo(currentType, field)
		if err != nil {
			return nil, nil, err
		}
		if pointerLike {
			return nil, nil, fmt.Errorf("derived-state field access over pointer-like self path is not supported")
		}
		currentValue = C.LLVMBuildExtractValue(s.builder, currentValue, C.unsigned(index), cStringFree("isstate.field."+field))
		currentType = fieldType
	}
	return currentValue, currentType, nil
}
func (s *functionState) enumIsTarget(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		return s.enumConstructorInfoFromField(fieldExpr)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	enumName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, ok := s.g.result.NamedTypes[enumName]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || enumType == nil {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, false
	}
	return enumType, variant, true
}
