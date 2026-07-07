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
)

func matchHasWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			// A guarded arm may fail at runtime; it cannot count toward exhaustiveness.
			continue
		}
		if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
			return true
		}
	}
	return false
}
func (s *functionState) emitStringMatch(stmt *ast.MatchStmt) error {
	actualType := s.exprType(stmt.Value)
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if err := s.emitStringMatchPatternTest(arm.Pattern, stmt.Value, actualType, bodyBB, nextBB); err != nil {
			return err
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

	hasWildcard := matchHasWildcard(stmt.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
