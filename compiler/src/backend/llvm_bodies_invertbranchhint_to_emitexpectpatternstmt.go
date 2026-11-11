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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func invertBranchHint(hint ast.BranchHint) ast.BranchHint {
	switch hint {
	case ast.BranchHintLikely:
		return ast.BranchHintUnlikely
	case ast.BranchHintUnlikely:
		return ast.BranchHintLikely
	default:
		return hint
	}
}

// Value-form lowering retains short-circuit evaluation while giving LLVM a
// simpler boolean expression to optimize. Keep pattern tests, overloaded
// operators and expressions with effects on the binding-aware branch path.
func simpleConditionValue(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return simpleConditionValue(n.Inner)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && (n.Op == lexer.TOKEN_NOT || n.Op == lexer.TOKEN_MINUS) && simpleConditionValue(n.Operand)
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ,
			lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			return simpleConditionValue(n.Left) && simpleConditionValue(n.Right)
		}
	}
	return false
}

func (s *functionState) emitSimpleConditionBranch(expr ast.Expr, trueBB, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) error {
	if n, ok := expr.(*ast.BinaryExpr); ok && (n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR) && simpleConditionValue(expr) {
		cond, _, err := s.emitExpr(expr, s.g.namedType("bool"))
		if err != nil {
			return err
		}
		s.buildCondBrWithHint(cond, trueBB, falseBB, hint)
		return nil
	}
	return s.emitConditionBranchWithBindings(expr, trueBB, falseBB, hint)
}

func (s *functionState) emitConditionBranchWithBindings(expr ast.Expr, trueBB C.LLVMBasicBlockRef, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) error {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitConditionBranchWithBindings(n.Inner, trueBB, falseBB, hint)
	case *ast.OptionalBindExpr:
		return s.emitOptionalBindCondition(n, trueBB, falseBB, hint)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			return s.emitConditionBranchWithBindings(n.Operand, falseBB, trueBB, invertBranchHint(hint))
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.and.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, rhsBB, falseBB, hint); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
			return s.emitConditionBranchWithBindings(n.Right, trueBB, falseBB, ast.BranchHintNone)
		case lexer.TOKEN_OR:
			rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.or.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, trueBB, rhsBB, hint); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
			return s.emitConditionBranchWithBindings(n.Right, trueBB, falseBB, ast.BranchHintNone)
		}
	}
	if leftExpr, pattern, ok := s.directConditionPattern(expr); ok {
		leftType := s.exprType(leftExpr)
		leftValue, _, err := s.emitExpr(leftExpr, leftType)
		if err != nil {
			return err
		}
		return s.emitConditionPatternTestAndBind(pattern, leftValue, leftType, leftExpr, trueBB, falseBB)
	}
	condValue, _, err := s.emitExpr(expr, s.g.namedType("bool"))
	if err != nil {
		return err
	}
	s.buildCondBrWithHint(condValue, trueBB, falseBB, hint)
	return nil
}
func (s *functionState) emitIf(stmt *ast.IfStmt) error {
	stmt = normalizeIf(stmt)
	if condScope, ok, err := s.createConditionBindingScope(stmt.Cond); err != nil {
		return err
	} else if ok {
		parentScope := s.scope
		s.scope = condScope
		thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.then"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.end"))
		var elseBB C.LLVMBasicBlockRef
		if len(stmt.Else) > 0 {
			elseBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.else"))
			if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, elseBB, stmt.Hint); err != nil {
				s.scope = parentScope
				return err
			}
		} else {
			if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, mergeBB, stmt.Hint); err != nil {
				s.scope = parentScope
				return err
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
		s.scope = condScope
		if err := s.bindConditionVariantViews(stmt.Cond); err != nil {
			s.scope = parentScope
			return err
		}
		if err := s.emitBlock(stmt.Then, true); err != nil {
			s.scope = parentScope
			return err
		}
		thenTerminated := s.currentBlockTerminated()
		if !thenTerminated {
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		elseTerminated := false
		if len(stmt.Else) > 0 {
			C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
			s.scope = parentScope
			if err := s.emitBlock(stmt.Else, true); err != nil {
				s.scope = parentScope
				return err
			}
			elseTerminated = s.currentBlockTerminated()
			if !elseTerminated {
				C.LLVMBuildBr(s.builder, mergeBB)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		s.scope = parentScope
		if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
			C.LLVMBuildUnreachable(s.builder)
		}
		return nil
	}
	thenName := cString("if.then")
	defer C.free(unsafe.Pointer(thenName))
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, thenName)

	mergeName := cString("if.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	var elseBB C.LLVMBasicBlockRef
	if len(stmt.Else) > 0 {
		elseName := cString("if.else")
		defer C.free(unsafe.Pointer(elseName))
		elseBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, elseName)
		if err := s.emitSimpleConditionBranch(stmt.Cond, thenBB, elseBB, stmt.Hint); err != nil {
			return err
		}
	} else {
		if err := s.emitSimpleConditionBranch(stmt.Cond, thenBB, mergeBB, stmt.Hint); err != nil {
			return err
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	if err := s.bindConditionVariantViews(stmt.Cond); err != nil {
		return err
	}
	if err := s.emitBlock(stmt.Then, true); err != nil {
		return err
	}
	thenTerminated := s.currentBlockTerminated()
	if !thenTerminated {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	elseTerminated := false
	if len(stmt.Else) > 0 {
		C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
		if err := s.emitBlock(stmt.Else, true); err != nil {
			return err
		}
		elseTerminated = s.currentBlockTerminated()
		if !elseTerminated {
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
func (s *functionState) emitWhile(stmt *ast.WhileStmt) error {
	if condScope, ok, err := s.createConditionBindingScope(stmt.Cond); err != nil {
		return err
	} else if ok {
		parentScope := s.scope
		condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.cond"))
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.body"))
		exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.end"))

		C.LLVMBuildBr(s.builder, condBB)
		C.LLVMPositionBuilderAtEnd(s.builder, condBB)
		s.scope = condScope
		if err := s.emitConditionBranchWithBindings(stmt.Cond, bodyBB, exitBB, stmt.Hint); err != nil {
			s.scope = parentScope
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.scope = condScope
		s.pushLoopTargets(exitBB, condBB)
		if err := s.emitLoopBody(stmt.Body); err != nil {
			s.popLoopTargets()
			s.scope = parentScope
			return err
		}
		s.popLoopTargets()
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, condBB)
		}

		C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
		s.scope = parentScope
		return nil
	}
	condName := cString("while.cond")
	defer C.free(unsafe.Pointer(condName))
	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, condName)

	bodyName := cString("while.body")
	defer C.free(unsafe.Pointer(bodyName))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, bodyName)

	exitName := cString("while.end")
	defer C.free(unsafe.Pointer(exitName))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, exitName)

	C.LLVMBuildBr(s.builder, condBB)
	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.namedType("bool"))
	if err != nil {
		return err
	}
	s.buildCondBrWithHint(condValue, bodyBB, exitBB, stmt.Hint)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushLoopTargets(exitBB, condBB)
	if err := s.emitLoopBody(stmt.Body); err != nil {
		s.popLoopTargets()
		return err
	}
	s.popLoopTargets()
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}
func (s *functionState) emitForStmt(stmt *ast.ForStmt) error {
	for _, preReserve := range ast.EmittedPreReserveStmts(stmt) {
		if err := s.emitStmt(preReserve); err != nil {
			return err
		}
	}
	loopType := s.forLoopValueType(stmt)
	if loopType == nil {
		return fmt.Errorf("missing semantic type for for-loop")
	}
	startValue, _, err := s.emitExpr(stmt.Start, loopType)
	if err != nil {
		return err
	}
	endValue, _, err := s.emitExpr(stmt.End, loopType)
	if err != nil {
		return err
	}
	stepValue, err := s.emitForLoopStepMagnitude(stmt, loopType)
	if err != nil {
		return err
	}
	loopLLVMType, err := s.g.lowerType(loopType)
	if err != nil {
		return err
	}
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	zeroValue := C.LLVMConstInt(loopLLVMType, 0, 0)

	var ascendingValue C.LLVMValueRef
	switch stmt.Op {
	case lexer.TOKEN_RANGE:
		pred := C.LLVMIntPredicate(C.LLVMIntULE)
		if isSignedIntegerType(loopType) {
			pred = C.LLVMIntPredicate(C.LLVMIntSLE)
		}
		ascendingValue = C.LLVMBuildICmp(s.builder, pred, startValue, endValue, cStringFree("for.asc"))
	case lexer.TOKEN_RANGE_LT:
		ascendingValue = C.LLVMConstInt(boolType, 1, 0)
	case lexer.TOKEN_RANGE_GT:
		ascendingValue = C.LLVMConstInt(boolType, 0, 0)
	default:
		return fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(stmt.Op))
	}
	hasStep := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), stepValue, zeroValue, cStringFree("for.step.nonzero"))

	currentAlloca, err := s.createEntryAlloca(stmt.Name+".for.cur", loopType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, startValue, currentAlloca)
	loopVarAlloca, err := s.createEntryAlloca(stmt.Name, loopType)
	if err != nil {
		return err
	}

	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.body"))
	stepBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.step"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.end"))

	C.LLVMBuildCondBr(s.builder, hasStep, condBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	currentValue, err := s.loadValue(currentAlloca, loopType, stmt.Name+".for.cur")
	if err != nil {
		return err
	}
	ascendingCond, err := s.emitForLoopContinueCmp(stmt.Op, loopType, currentValue, endValue, true)
	if err != nil {
		return err
	}
	descendingCond, err := s.emitForLoopContinueCmp(stmt.Op, loopType, currentValue, endValue, false)
	if err != nil {
		return err
	}
	condValue := C.LLVMBuildSelect(s.builder, ascendingValue, ascendingCond, descendingCond, cStringFree("for.cond.select"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: loopVarAlloca, typ: loopType})
	C.LLVMBuildStore(s.builder, currentValue, loopVarAlloca)
	s.pushLoopTargets(exitBB, stepBB)
	savedComprehensionAliasContext := s.comprehensionAliasContext
	s.comprehensionAliasContext = s.comprehensionAliasContextForLoop(stmt)
	bodyErr := s.emitLoopBody(stmt.Body)
	s.comprehensionAliasContext = savedComprehensionAliasContext
	if bodyErr != nil {
		s.popLoopTargets()
		s.popScope()
		return bodyErr
	}
	s.popLoopTargets()
	s.popScope()
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, stepBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextAscending := C.LLVMBuildAdd(s.builder, currentValue, stepValue, cStringFree("for.next.asc"))
		nextDescending := C.LLVMBuildSub(s.builder, currentValue, stepValue, cStringFree("for.next.desc"))
		nextValue := C.LLVMBuildSelect(s.builder, ascendingValue, nextAscending, nextDescending, cStringFree("for.next"))
		C.LLVMBuildStore(s.builder, nextValue, currentAlloca)
		backedge := C.LLVMBuildBr(s.builder, condBB)
		if stmt.AutovecExpected {
			s.tagAutovecExpectedLoop(backedge, stmt.Position, stmt.AutovecReason)
		} else if stmt.Step == nil && s.userLoopVectorEligible(stmt.Body) {
			// A user range loop whose body is the clean element-wise store shape is expected to
			// vectorize too (docs/70); scalar fallback warns / errors under -Wperf unless the loop
			// sits inside a `can Scalar` grant (checked inside tagAutovecExpectedLoop).
			s.tagAutovecExpectedLoop(backedge, stmt.Position, "loop")
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}
func (s *functionState) forLoopValueType(stmt *ast.ForStmt) semantic.Type {
	if stmt == nil {
		return nil
	}
	loopType := semantic.CommonNumericType(s.exprType(stmt.Start), s.exprType(stmt.End))
	if stmt.Step != nil {
		loopType = semantic.CommonNumericType(loopType, s.exprType(stmt.Step))
	}
	return loopType
}
func (s *functionState) emitForLoopStepMagnitude(stmt *ast.ForStmt, loopType semantic.Type) (C.LLVMValueRef, error) {
	loopLLVMType, err := s.g.lowerType(loopType)
	if err != nil {
		return nil, err
	}
	if stmt.Step == nil {
		return C.LLVMConstInt(loopLLVMType, 1, 0), nil
	}
	rawStep, _, err := s.emitExpr(stmt.Step, loopType)
	if err != nil {
		return nil, err
	}
	if !isSignedIntegerType(loopType) {
		return rawStep, nil
	}
	zeroValue := C.LLVMConstInt(loopLLVMType, 0, 0)
	isNegative := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLT), rawStep, zeroValue, cStringFree("for.step.neg"))
	negated := C.LLVMBuildNeg(s.builder, rawStep, cStringFree("for.step.abs.neg"))
	return C.LLVMBuildSelect(s.builder, isNegative, negated, rawStep, cStringFree("for.step.abs")), nil
}
func (s *functionState) emitForLoopContinueCmp(op lexer.TokenKind, loopType semantic.Type, currentValue, endValue C.LLVMValueRef, ascending bool) (C.LLVMValueRef, error) {
	var pred C.LLVMIntPredicate
	signed := isSignedIntegerType(loopType)
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			if signed {
				pred = C.LLVMIntPredicate(C.LLVMIntSLE)
			} else {
				pred = C.LLVMIntPredicate(C.LLVMIntULE)
			}
		} else {
			if signed {
				pred = C.LLVMIntPredicate(C.LLVMIntSGE)
			} else {
				pred = C.LLVMIntPredicate(C.LLVMIntUGE)
			}
		}
	case lexer.TOKEN_RANGE_LT:
		if signed {
			pred = C.LLVMIntPredicate(C.LLVMIntSLT)
		} else {
			pred = C.LLVMIntPredicate(C.LLVMIntULT)
		}
	case lexer.TOKEN_RANGE_GT:
		if signed {
			pred = C.LLVMIntPredicate(C.LLVMIntSGT)
		} else {
			pred = C.LLVMIntPredicate(C.LLVMIntUGT)
		}
	default:
		return nil, fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(op))
	}
	return C.LLVMBuildICmp(s.builder, pred, currentValue, endValue, cStringFree("for.cmp")), nil
}
