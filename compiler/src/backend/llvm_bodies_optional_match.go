//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func backendMatchPatternIsNull(pattern ast.MatchPattern) bool {
	literal, ok := pattern.(*ast.MatchLiteralPattern)
	if !ok || literal == nil {
		return false
	}
	_, ok = literal.Value.(*ast.NullLit)
	return ok
}

func optionalMatchHasPayloadWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			// A guarded arm may fail at runtime; it cannot count toward exhaustiveness.
			continue
		}
		if backendMatchPatternIsNull(arm.Pattern) {
			continue
		}
		if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
			return true
		}
	}
	return false
}

func optionalMatchHasNullArm(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if backendMatchPatternIsNull(arm.Pattern) {
			return true
		}
	}
	return false
}

func optionalMatchExhaustive(arms []ast.MatchArm) bool {
	return optionalMatchHasNullArm(arms) && optionalMatchHasPayloadWildcard(arms)
}

func (s *functionState) emitOptionalMatch(stmt *ast.MatchStmt, optionalType *semantic.OptionalType) error {
	if stmt.Store != nil {
		return fmt.Errorf("optional match does not take an in-store clause")
	}
	optionalValue, _, err := s.emitExpr(stmt.Value, optionalType)
	if err != nil {
		return err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return err
	}
	payloadValue, err := s.extractOptionalPayload(optionalValue, optionalType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.fail"))
	allTerminated := true
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.next"))
		}
		if backendMatchPatternIsNull(arm.Pattern) {
			absentValue := C.LLVMBuildNot(s.builder, presentValue, cStringFree("match.optional.absent"))
			C.LLVMBuildCondBr(s.builder, absentValue, bodyBB, nextBB)
		} else {
			payloadTestBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.payload"))
			C.LLVMBuildCondBr(s.builder, presentValue, payloadTestBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, payloadTestBB)
			if _, _, err := s.emitMatchPatternTest(arm.Pattern, payloadValue, nil, optionalType.Value, nil, nil, nil, bodyBB, nextBB); err != nil {
				return err
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitMatchArmGuard(arm.Guard, nextBB); err != nil {
			s.popScope()
			return err
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

	exhaustive := optionalMatchExhaustive(stmt.Arms)
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

func (s *functionState) emitOptionalMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, optionalType *semantic.OptionalType) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("optional match does not take an in-store clause")
	}
	optionalValue, _, err := s.emitExpr(expr.Value, optionalType)
	if err != nil {
		return nil, nil, err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return nil, nil, err
	}
	payloadValue, err := s.extractOptionalPayload(optionalValue, optionalType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.next"))
		}
		if backendMatchPatternIsNull(arm.Pattern) {
			absentValue := C.LLVMBuildNot(s.builder, presentValue, cStringFree("match.optional.expr.absent"))
			C.LLVMBuildCondBr(s.builder, absentValue, bodyBB, nextBB)
		} else {
			payloadTestBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.payload"))
			C.LLVMBuildCondBr(s.builder, presentValue, payloadTestBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, payloadTestBB)
			if _, _, err := s.emitMatchPatternTest(arm.Pattern, payloadValue, nil, optionalType.Value, nil, nil, nil, bodyBB, nextBB); err != nil {
				return nil, nil, err
			}
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

	exhaustive := optionalMatchExhaustive(expr.Arms)
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
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.optional.expr.result"))
	C.LLVMAddIncoming(phi, &incomingValues[0], &incomingBlocks[0], C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
