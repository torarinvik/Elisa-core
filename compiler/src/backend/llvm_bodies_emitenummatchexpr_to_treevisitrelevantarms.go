//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// resolveEnumArmVariant resolves a match arm's variant against the scrutinee enum or, for a sealed
// hierarchy (docs/77), against the refinement the arm names (`Mono.Black` while matching a `Color`).
// Semantic analysis has already validated the arm, so this trusts the qualified name.
func (s *functionState) resolveEnumArmVariant(enumType *semantic.EnumType, pattern *ast.MatchVariantPattern) (*semantic.EnumVariant, bool) {
	owner := enumType
	if pattern.EnumName != "" && pattern.EnumName != enumType.Name {
		if resolved, ok := s.g.result.NamedTypes[pattern.EnumName].(*semantic.EnumType); ok && resolved != nil {
			owner = resolved
		}
	}
	return owner.Variant(pattern.Variant)
}

func (s *functionState) emitEnumMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, semantic.Type, error) {
	resultSize, err := s.g.abiSizeOfType(resultType)
	if err != nil {
		return nil, nil, err
	}
	spillLargeResult := resultSize >= largeCompilerTempHeapThresholdBytes && !semantic.IsNeverType(resultType)
	var resultSlot C.LLVMValueRef
	if spillLargeResult {
		resultSlot, err = s.createTempStorage("match.expr.result", resultType)
		if err != nil {
			return nil, nil, err
		}
	}
	recordIncoming := func(value C.LLVMValueRef, block C.LLVMBasicBlockRef, values *[]C.LLVMValueRef, blocks *[]C.LLVMBasicBlockRef) error {
		if spillLargeResult {
			if storeErr := s.storeValue(resultSlot, value, resultType, "match.expr.result.store"); storeErr != nil {
				return storeErr
			}
		}
		*values = append(*values, value)
		*blocks = append(*blocks, block)
		return nil
	}
	finishResult := func(values []C.LLVMValueRef, blocks []C.LLVMBasicBlockRef) (C.LLVMValueRef, semantic.Type, error) {
		if spillLargeResult {
			value, loadErr := s.loadValue(resultSlot, resultType, "match.expr.result.load")
			return value, resultType, loadErr
		}
		if len(values) == 1 || semantic.IsNeverType(resultType) {
			return values[0], resultType, nil
		}
		llvmType, lowerErr := s.g.lowerType(resultType)
		if lowerErr != nil {
			return nil, nil, lowerErr
		}
		phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
		return phi, resultType, nil
	}
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
	if _, err := s.prepareNonPackedEnumMatchTemp(enumValue, enumType); err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	valuePath, hasValuePath := s.packedEnumStoragePath(expr.Value)
	valueIdent, hasValueIdent := expr.Value.(*ast.Ident)
	exhaustive := s.matchIsExhaustive(enumType, expr.Arms)
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
			variant, _ := s.resolveEnumArmVariant(enumType, pattern)
			tagConst, err := s.enumTagConstant(enumType, variant.Tag)
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
				if err := recordIncoming(armValue, armEnd, &incomingValues, &incomingBlocks); err != nil {
					s.popScope()
					return nil, nil, err
				}
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
				if err := recordIncoming(armValue, armEnd, &incomingValues, &incomingBlocks); err != nil {
					s.popScope()
					return nil, nil, err
				}
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
		return finishResult(incomingValues, incomingBlocks)
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
		if err := s.emitMatchArmGuard(arm.Guard, nextBB); err != nil {
			s.popScope()
			return nil, nil, err
		}
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			if err := recordIncoming(armValue, armEnd, &incomingValues, &incomingBlocks); err != nil {
				s.popScope()
				return nil, nil, err
			}
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
	return finishResult(incomingValues, incomingBlocks)
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
		if err := s.emitMatchArmGuard(arm.Guard, nextBB); err != nil {
			s.popScope()
			return nil, nil, err
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
		if err := s.emitMatchArmGuard(arm.Guard, nextBB); err != nil {
			s.popScope()
			return nil, nil, err
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
