//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func matchHasWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			// A guarded arm may fail at runtime; it cannot count toward exhaustiveness.
			continue
		}
		// `_`, or an irrefutable struct pattern (`Item{v}:` — every field a binding or
		// `_`), which matches every value of its struct scrutinee. The analyzer's
		// fall-through rule uses the same predicate.
		if semantic.MatchPatternIsIrrefutable(arm.Pattern) {
			return true
		}
	}
	return false
}
func (s *functionState) emitStringMatch(stmt *ast.MatchStmt) error {
	actualType := s.exprType(stmt.Value)
	actualValue, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	for i, arm := range stmt.Arms {
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB llvmIRBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if err := s.emitStringMatchPatternTest(arm.Pattern, actualValue, stmt.Value, actualType, bodyBB, nextBB); err != nil {
			return err
		}

		llvmPositionBuilderAtEnd(s.builder, bodyBB)
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
			llvmBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			llvmPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	hasWildcard := matchHasWildcard(stmt.Arms)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard {
		llvmBuildUnreachable(s.builder)
	} else {
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && hasWildcard {
		llvmBuildUnreachable(s.builder)
	}
	return nil
}
