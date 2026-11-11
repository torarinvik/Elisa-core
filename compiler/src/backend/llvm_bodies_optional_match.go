//go:build cgo

package backend

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

// docs/122 §4: an unguarded `_` discharges coverage everywhere, absence included.
func optionalMatchHasUnguardedWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok && arm.Guard == nil {
			return true
		}
	}
	return false
}

func optionalMatchExhaustive(arms []ast.MatchArm, payloadCovered bool) bool {
	return optionalMatchHasUnguardedWildcard(arms) || (optionalMatchHasNullArm(arms) && payloadCovered)
}

// A payload pattern is total when lowering emitted a direct branch to the
// arm. This distinguishes a plain binder from a predicate or narrowed enum
// category, even though they can share the same syntax node.
func optionalPayloadTestAlwaysMatches(testBB, bodyBB llvmIRBlockRef) bool {
	term := llvmGetBasicBlockTerminator(testBB)
	return term != nil && llvmGetNumSuccessors(term) == 1 && llvmGetSuccessor(term, 0) == bodyBB
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
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.end"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.fail"))
	allTerminated := true
	payloadCovered := false
	for i, arm := range stmt.Arms {
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.arm"))
		var nextBB llvmIRBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.next"))
		}
		if _, wildcard := arm.Pattern.(*ast.MatchWildcardPattern); wildcard {
			// `_` matches absence too; only its guard may send control onward.
			llvmBuildBr(s.builder, bodyBB)
		} else if backendMatchPatternIsNull(arm.Pattern) {
			absentValue := llvmBuildNot(s.builder, presentValue, cStringFree("match.optional.absent"))
			llvmBuildCondBr(s.builder, absentValue, bodyBB, nextBB)
		} else {
			payloadTestBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.payload"))
			llvmBuildCondBr(s.builder, presentValue, payloadTestBB, nextBB)
			llvmPositionBuilderAtEnd(s.builder, payloadTestBB)
			if _, _, err := s.emitMatchPatternTest(arm.Pattern, payloadValue, nil, optionalType.Value, nil, nil, nil, bodyBB, nextBB); err != nil {
				return err
			}
			if arm.Guard == nil && optionalPayloadTestAlwaysMatches(payloadTestBB, bodyBB) {
				payloadCovered = true
			}
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

	exhaustive := optionalMatchExhaustive(stmt.Arms, payloadCovered)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive {
		llvmBuildUnreachable(s.builder)
	} else {
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		llvmBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitOptionalMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, optionalType *semantic.OptionalType) (llvmIRValueRef, semantic.Type, error) {
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
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.end"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.fail"))
	incomingValues := make([]llvmIRValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]llvmIRBlockRef, 0, len(expr.Arms)+1)
	payloadCovered := false
	for i, arm := range expr.Arms {
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.arm"))
		var nextBB llvmIRBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.next"))
		}
		if _, wildcard := arm.Pattern.(*ast.MatchWildcardPattern); wildcard {
			// `_` matches absence too; only its guard may send control onward.
			llvmBuildBr(s.builder, bodyBB)
		} else if backendMatchPatternIsNull(arm.Pattern) {
			absentValue := llvmBuildNot(s.builder, presentValue, cStringFree("match.optional.expr.absent"))
			llvmBuildCondBr(s.builder, absentValue, bodyBB, nextBB)
		} else {
			payloadTestBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.optional.expr.payload"))
			llvmBuildCondBr(s.builder, presentValue, payloadTestBB, nextBB)
			llvmPositionBuilderAtEnd(s.builder, payloadTestBB)
			if _, _, err := s.emitMatchPatternTest(arm.Pattern, payloadValue, nil, optionalType.Value, nil, nil, nil, bodyBB, nextBB); err != nil {
				return nil, nil, err
			}
			if arm.Guard == nil && optionalPayloadTestAlwaysMatches(payloadTestBB, bodyBB) {
				payloadCovered = true
			}
		}

		llvmPositionBuilderAtEnd(s.builder, bodyBB)
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
			armEnd := llvmGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			llvmBuildBr(s.builder, mergeBB)
		}
		s.popScope()
		if nextBB != mergeBB {
			llvmPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	exhaustive := optionalMatchExhaustive(expr.Arms, payloadCovered)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive || semantic.IsNeverType(resultType) {
		llvmBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := llvmGetUndef(llvmType)
		failEnd := llvmGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		llvmBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := llvmBuildPhi(s.builder, llvmType, cStringFree("match.optional.expr.result"))
	llvmAddIncoming(phi, &incomingValues[0], &incomingBlocks[0], llvmIRUnsigned(len(incomingValues)))
	return phi, resultType, nil
}
