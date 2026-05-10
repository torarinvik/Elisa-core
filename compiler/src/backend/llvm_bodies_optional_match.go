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
