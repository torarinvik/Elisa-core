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

func (s *functionState) emitEnumMatch(stmt *ast.MatchStmt, enumType *semantic.EnumType) error {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
	if err != nil {
		return err
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, s.g.packedModeForEnum(enumType), enumType, stmt.Value, storeBinding, stmt.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return err
		}
	}
	preloadedCommonValues, err := s.preloadPackedMatchCommonFieldValues(enumType, stmt.Value, enumValue, decodedMatchValue, storeBinding, stmt.Arms)
	if err != nil {
		return err
	}
	matchTagValue, err := s.extractEnumTagValue(enumValue, decodedMatchValue, enumType, storeBinding)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	valuePath, hasValuePath := s.packedEnumStoragePath(stmt.Value)
	if packedEnumMatchCanUseTagSwitch(enumType, stmt.Arms) {
		wildcardIndex := -1
		variantArmCount := 0
		for i, arm := range stmt.Arms {
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
			wildcardBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.wildcard"))
			defaultBB = wildcardBB
		}
		switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, defaultBB, C.unsigned(variantArmCount))
		for i, arm := range stmt.Arms {
			pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				continue
			}
			variant, _ := s.resolveEnumArmVariant(enumType, pattern)
			tagConst, err := s.enumTagConstant(variant.Tag)
			if err != nil {
				return err
			}
			dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.dispatch"))
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
			C.LLVMAddCase(switchInst, tagConst, dispatchBB)

			C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
			patternFailureBB := failBB
			if wildcardBB != nil {
				patternFailureBB = wildcardBB
			}
			armDecodedValue, armPayloadValues, err := s.emitMatchedVariantPayloadPatternTest(pattern, enumValue, decodedMatchValue, enumType, variant, storeBinding, stmt.Value, bodyBB, patternFailureBB)
			if err != nil {
				return err
			}

			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
			s.pushScope()
			if hasValuePath && enumType.Packed && armDecodedValue != nil {
				s.bindPackedEnumStorage(valuePath, enumType, armDecodedValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
				s.popScope()
				return err
			}
			s.popScope()
			if !s.currentBlockTerminated() {
				allTerminated = false
				C.LLVMBuildBr(s.builder, mergeBB)
			}
			_ = i
		}
		if wildcardIndex >= 0 {
			arm := stmt.Arms[wildcardIndex]
			C.LLVMPositionBuilderAtEnd(s.builder, wildcardBB)
			s.pushScope()
			if hasValuePath && enumType.Packed && decodedMatchValue != nil {
				s.bindPackedEnumStorage(valuePath, enumType, decodedMatchValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, packedPayloadValueCache{})
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
				s.popScope()
				return err
			}
			s.popScope()
			if !s.currentBlockTerminated() {
				allTerminated = false
				C.LLVMBuildBr(s.builder, mergeBB)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if s.matchIsExhaustive(enumType, stmt.Arms) {
			C.LLVMBuildUnreachable(s.builder)
		} else {
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if allTerminated && s.matchIsExhaustive(enumType, stmt.Arms) {
			C.LLVMBuildUnreachable(s.builder)
		}
		return nil
	}

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		armDecodedValue, armPayloadValues, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, stmt.Value, matchTagValue, bodyBB, nextBB)
		if err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if hasValuePath && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(valuePath, enumType, armDecodedValue)
		}
		if hasValuePath && enumType.Packed {
			s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
		}
		s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
		if hasValuePath && !preloadedCommonValues.empty() {
			s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
		}
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if s.matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && s.matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
func (s *functionState) emitTreeMatch(stmt *ast.MatchStmt, treeType *semantic.TreeCategoryType) error {
	if treeType == nil {
		return fmt.Errorf("match requires a tree-category value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("tree match over %q does not take an in-store clause", treeType.Name)
	}
	actualType := s.exprType(stmt.Value)
	treeValue, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := treeMatchIsExhaustive(treeType, stmt.Arms)

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, treeValue, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
func (s *functionState) emitConstEnumMatch(stmt *ast.MatchStmt, constEnumType *semantic.ConstEnumType) error {
	if constEnumType == nil {
		return fmt.Errorf("match requires a const enum value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := constEnumMatchIsExhaustive(constEnumType, stmt.Arms)
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
func errorSetMatchIsExhaustive(errorSetType *semantic.ErrorSetType, arms []ast.MatchArm) bool {
	if errorSetType == nil {
		return false
	}
	covered := make(map[string]bool, len(errorSetType.Tags))
	hasWildcard := false
	for _, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			hasWildcard = true
		case *ast.MatchVariantPattern:
			if errorSetType.HasQualifiedTag(errorSetType.Name, pattern.Variant) {
				covered[semantic.QualifyErrorTag(errorSetType.Name, pattern.Variant)] = true
			}
		}
	}
	if hasWildcard {
		return true
	}
	for _, tag := range errorSetType.Tags {
		if !covered[tag] {
			return false
		}
	}
	return true
}
func (s *functionState) emitErrorSetMatch(stmt *ast.MatchStmt, errorSetType *semantic.ErrorSetType) error {
	if errorSetType == nil {
		return fmt.Errorf("match requires an error set value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := errorSetMatchIsExhaustive(errorSetType, stmt.Arms)
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
func (s *functionState) emitMatchExpr(expr *ast.MatchExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	enumType, ok := resolveMatchableEnumType(s.exprType(expr.Value))
	if ok {
		return s.emitEnumMatchExpr(expr, resultType, enumType)
	}
	constEnumType, ok := resolveMatchableConstEnumType(s.exprType(expr.Value))
	if ok {
		return s.emitConstEnumMatchExpr(expr, resultType, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetTypeBackend(s.exprType(expr.Value))
	if ok {
		return s.emitErrorSetMatchExpr(expr, resultType, errorSetType)
	}
	if optionalType, ok := s.exprType(expr.Value).(*semantic.OptionalType); ok {
		return s.emitOptionalMatchExpr(expr, resultType, optionalType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryTypeBackend(s.exprType(expr.Value))
	if ok {
		return s.emitTreeMatchExpr(expr, resultType, treeType)
	}
	if isStringMatchableType(s.exprType(expr.Value)) {
		return s.emitStringMatchExpr(expr, resultType)
	}
	if resolveMatchableTupleTypeBackend(s.exprType(expr.Value)) {
		return s.emitTupleMatchExpr(expr, resultType)
	}
	if resolveMatchableStructTypeBackend(s.exprType(expr.Value)) {
		return s.emitStructMatchExpr(expr, resultType)
	}
	return nil, nil, fmt.Errorf("match requires an enum, const enum, error set, optional, tree-category, string, tuple, or struct value")
}
