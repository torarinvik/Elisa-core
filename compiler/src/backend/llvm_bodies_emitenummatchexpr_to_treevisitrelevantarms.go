//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void llctxSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
	"strings"
)

func (s *functionState) emitEnumMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, semantic.Type, error) {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, expr.Value, expr.Store)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(expr.Value, enumType)
	if err != nil {
		return nil, nil, err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, s.g.packedModeForEnum(enumType), enumType, expr.Value, storeBinding, expr.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return nil, nil, err
		}
	}
	preloadedCommonValues, err := s.preloadPackedMatchCommonFieldValues(enumType, expr.Value, enumValue, decodedMatchValue, storeBinding, expr.Arms)
	if err != nil {
		return nil, nil, err
	}
	matchTagValue, err := s.extractEnumTagValue(enumValue, decodedMatchValue, enumType, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	valuePath, hasValuePath := s.packedEnumStoragePath(expr.Value)
	valueIdent, hasValueIdent := expr.Value.(*ast.Ident)
	exhaustive := matchIsExhaustive(enumType, expr.Arms)
	if packedEnumMatchCanUseTagSwitch(enumType, expr.Arms) {
		wildcardIndex := -1
		variantArmCount := 0
		for i, arm := range expr.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchVariantPattern:
				variantArmCount++
			case *ast.MatchWildcardPattern:
				wildcardIndex = i
			}
		}
		var wildcardBB C.LLVMBasicBlockRef
		defaultBB := failBB
		if wildcardIndex >= 0 {
			wildcardBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.wildcard"))
			defaultBB = wildcardBB
		}
		switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, defaultBB, C.unsigned(variantArmCount))
		for _, arm := range expr.Arms {
			pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				continue
			}
			variant, _ := enumType.Variant(pattern.Variant)
			tagConst, err := s.enumTagConstant(variant.Tag)
			if err != nil {
				return nil, nil, err
			}
			dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.dispatch"))
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
			C.LLVMAddCase(switchInst, tagConst, dispatchBB)

			C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
			patternFailureBB := failBB
			if wildcardBB != nil {
				patternFailureBB = wildcardBB
			}
			armDecodedValue, armPayloadValues, err := s.emitMatchedVariantPayloadPatternTest(pattern, enumValue, decodedMatchValue, enumType, variant, storeBinding, expr.Value, bodyBB, patternFailureBB)
			if err != nil {
				return nil, nil, err
			}

			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
			s.pushScope()
			if hasValueIdent && enumType.Packed && armDecodedValue != nil {
				s.bindPackedEnumStorage(valueIdent.Name, enumType, armDecodedValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
			if err != nil {
				s.popScope()
				return nil, nil, err
			}
			if reachable && !s.currentBlockTerminated() {
				armEnd := C.LLVMGetInsertBlock(s.builder)
				incomingValues = append(incomingValues, armValue)
				incomingBlocks = append(incomingBlocks, armEnd)
				C.LLVMBuildBr(s.builder, mergeBB)
			}
			s.popScope()
		}
		if wildcardIndex >= 0 {
			arm := expr.Arms[wildcardIndex]
			C.LLVMPositionBuilderAtEnd(s.builder, wildcardBB)
			s.pushScope()
			if hasValueIdent && enumType.Packed && decodedMatchValue != nil {
				s.bindPackedEnumStorage(valueIdent.Name, enumType, decodedMatchValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, packedPayloadValueCache{})
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
			if err != nil {
				s.popScope()
				return nil, nil, err
			}
			if reachable && !s.currentBlockTerminated() {
				armEnd := C.LLVMGetInsertBlock(s.builder)
				incomingValues = append(incomingValues, armValue)
				incomingBlocks = append(incomingBlocks, armEnd)
				C.LLVMBuildBr(s.builder, mergeBB)
			}
			s.popScope()
		}

		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if semantic.IsNeverType(resultType) || exhaustive {
			C.LLVMBuildUnreachable(s.builder)
		} else {
			llvmType, err := s.g.lowerType(resultType)
			if err != nil {
				return nil, nil, err
			}
			undefValue := C.LLVMGetUndef(llvmType)
			failEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, undefValue)
			incomingBlocks = append(incomingBlocks, failEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if len(incomingValues) == 0 {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
			return incomingValues[0], resultType, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
		return phi, resultType, nil
	}
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		armDecodedValue, armPayloadValues, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, expr.Value, matchTagValue, bodyBB, nextBB)
		if err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if hasValueIdent && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(valueIdent.Name, enumType, armDecodedValue)
		}
		if hasValuePath && enumType.Packed {
			s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
		}
		s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
		if hasValuePath && !preloadedCommonValues.empty() {
			s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitConstEnumMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, constEnumType *semantic.ConstEnumType) (C.LLVMValueRef, semantic.Type, error) {
	if constEnumType == nil {
		return nil, nil, fmt.Errorf("match requires a const enum value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, actualType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := constEnumMatchIsExhaustive(constEnumType, expr.Arms)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive || semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitErrorSetMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, errorSetType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorSetType == nil {
		return nil, nil, fmt.Errorf("match requires an error set value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, actualType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := errorSetMatchIsExhaustive(errorSetType, expr.Arms)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive || semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitTreeMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, treeType *semantic.TreeCategoryType) (C.LLVMValueRef, semantic.Type, error) {
	if treeType == nil {
		return nil, nil, fmt.Errorf("match requires a tree-category value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("tree match over %q does not take an in-store clause", treeType.Name)
	}
	actualType := s.exprType(expr.Value)
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := treeMatchIsExhaustive(treeType, expr.Arms)

	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, treeValue, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

type treeVisitRelevantArm struct {
	arm      ast.VisitArm
	variant  *semantic.EnumVariant
	wildcard bool
}
type treeVisitExactArm struct {
	arm      ast.VisitArm
	member   semantic.Type
	wildcard bool
}

func exactTreeVisitArm(memberName string, arms []ast.VisitArm) (ast.VisitArm, bool, bool) {
	for _, arm := range arms {
		if arm.Wildcard {
			return arm, true, true
		}
		if arm.TargetName == memberName {
			return arm, true, false
		}
	}
	return ast.VisitArm{}, false, false
}
func exactTreeVisitArms(memberName string, arms []ast.VisitArm) []ast.VisitArm {
	matched := make([]ast.VisitArm, 0)
	for _, arm := range arms {
		if arm.Wildcard || arm.TargetName == memberName {
			matched = append(matched, arm)
		}
	}
	return matched
}
func visitArmsHaveGuard(arms []ast.VisitArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			return true
		}
	}
	return false
}
func visitArmsCoverExactMember(memberName string, arms []ast.VisitArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if arm.Wildcard || arm.TargetName == memberName {
			return true
		}
	}
	return false
}
func (s *functionState) treeVisitRelevantArms(categoryType *semantic.TreeCategoryType, arms []ast.VisitArm) ([]treeVisitRelevantArm, bool, error) {
	relevant := make([]treeVisitRelevantArm, 0, len(arms))
	exhaustive := false
	covered := map[string]bool{}
	for _, arm := range arms {
		if arm.Wildcard {
			relevant = append(relevant, treeVisitRelevantArm{arm: arm, wildcard: true})
			if arm.Guard == nil {
				exhaustive = true
			}
			continue
		}
		idx := strings.LastIndex(arm.TargetName, ".")
		if idx <= 0 || idx+1 >= len(arm.TargetName) {
			continue
		}
		categoryName := arm.TargetName[:idx]
		variantName := arm.TargetName[idx+1:]
		if categoryName != categoryType.Name {
			continue
		}
		variant, ok := categoryType.Variant(variantName)
		if !ok {
			return nil, false, fmt.Errorf("tree category %s has no variant %s", categoryType.Name, variantName)
		}
		relevant = append(relevant, treeVisitRelevantArm{arm: arm, variant: variant})
		if arm.Guard == nil {
			covered[variant.Name] = true
		}
	}
	if !exhaustive && categoryType != nil {
		exhaustive = len(covered) == len(categoryType.Variants)
	}
	return relevant, exhaustive, nil
}
