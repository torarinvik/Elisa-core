//go:build cgo

package backend

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

func (s *functionState) emitSimpleConditionBranch(expr ast.Expr, trueBB, falseBB llvmIRBlockRef, hint ast.BranchHint) error {
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

func (s *functionState) emitConditionBranchWithBindings(expr ast.Expr, trueBB llvmIRBlockRef, falseBB llvmIRBlockRef, hint ast.BranchHint) error {
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
			rhsBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.and.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, rhsBB, falseBB, hint); err != nil {
				return err
			}
			llvmPositionBuilderAtEnd(s.builder, rhsBB)
			return s.emitConditionBranchWithBindings(n.Right, trueBB, falseBB, ast.BranchHintNone)
		case lexer.TOKEN_OR:
			rhsBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.or.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, trueBB, rhsBB, hint); err != nil {
				return err
			}
			llvmPositionBuilderAtEnd(s.builder, rhsBB)
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
		thenBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.then"))
		mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.end"))
		var elseBB llvmIRBlockRef
		if len(stmt.Else) > 0 {
			elseBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.else"))
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

		llvmPositionBuilderAtEnd(s.builder, thenBB)
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
			llvmBuildBr(s.builder, mergeBB)
		}

		elseTerminated := false
		if len(stmt.Else) > 0 {
			llvmPositionBuilderAtEnd(s.builder, elseBB)
			s.scope = parentScope
			if err := s.emitBlock(stmt.Else, true); err != nil {
				s.scope = parentScope
				return err
			}
			elseTerminated = s.currentBlockTerminated()
			if !elseTerminated {
				llvmBuildBr(s.builder, mergeBB)
			}
		}

		llvmPositionBuilderAtEnd(s.builder, mergeBB)
		s.scope = parentScope
		if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
			llvmBuildUnreachable(s.builder)
		}
		return nil
	}
	thenName := cString("if.then")
	defer llvmFree(unsafe.Pointer(thenName))
	thenBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, thenName)

	mergeName := cString("if.end")
	defer llvmFree(unsafe.Pointer(mergeName))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	var elseBB llvmIRBlockRef
	if len(stmt.Else) > 0 {
		elseName := cString("if.else")
		defer llvmFree(unsafe.Pointer(elseName))
		elseBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, elseName)
		if err := s.emitSimpleConditionBranch(stmt.Cond, thenBB, elseBB, stmt.Hint); err != nil {
			return err
		}
	} else {
		if err := s.emitSimpleConditionBranch(stmt.Cond, thenBB, mergeBB, stmt.Hint); err != nil {
			return err
		}
	}

	llvmPositionBuilderAtEnd(s.builder, thenBB)
	if err := s.bindConditionVariantViews(stmt.Cond); err != nil {
		return err
	}
	if err := s.emitBlock(stmt.Then, true); err != nil {
		return err
	}
	thenTerminated := s.currentBlockTerminated()
	if !thenTerminated {
		llvmBuildBr(s.builder, mergeBB)
	}

	elseTerminated := false
	if len(stmt.Else) > 0 {
		llvmPositionBuilderAtEnd(s.builder, elseBB)
		if err := s.emitBlock(stmt.Else, true); err != nil {
			return err
		}
		elseTerminated = s.currentBlockTerminated()
		if !elseTerminated {
			llvmBuildBr(s.builder, mergeBB)
		}
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
		llvmBuildUnreachable(s.builder)
	}
	return nil
}
func (s *functionState) emitWhile(stmt *ast.WhileStmt) error {
	if condScope, ok, err := s.createConditionBindingScope(stmt.Cond); err != nil {
		return err
	} else if ok {
		parentScope := s.scope
		condBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.cond"))
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.body"))
		exitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.end"))

		llvmBuildBr(s.builder, condBB)
		llvmPositionBuilderAtEnd(s.builder, condBB)
		s.scope = condScope
		if err := s.emitConditionBranchWithBindings(stmt.Cond, bodyBB, exitBB, stmt.Hint); err != nil {
			s.scope = parentScope
			return err
		}

		llvmPositionBuilderAtEnd(s.builder, bodyBB)
		s.scope = condScope
		s.pushLoopTargets(exitBB, condBB)
		if err := s.emitLoopBody(stmt.Body); err != nil {
			s.popLoopTargets()
			s.scope = parentScope
			return err
		}
		s.popLoopTargets()
		if !s.currentBlockTerminated() {
			llvmBuildBr(s.builder, condBB)
		}

		llvmPositionBuilderAtEnd(s.builder, exitBB)
		s.scope = parentScope
		return nil
	}
	condName := cString("while.cond")
	defer llvmFree(unsafe.Pointer(condName))
	condBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, condName)

	bodyName := cString("while.body")
	defer llvmFree(unsafe.Pointer(bodyName))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, bodyName)

	exitName := cString("while.end")
	defer llvmFree(unsafe.Pointer(exitName))
	exitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, exitName)

	llvmBuildBr(s.builder, condBB)
	llvmPositionBuilderAtEnd(s.builder, condBB)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.namedType("bool"))
	if err != nil {
		return err
	}
	s.buildCondBrWithHint(condValue, bodyBB, exitBB, stmt.Hint)

	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushLoopTargets(exitBB, condBB)
	if err := s.emitLoopBody(stmt.Body); err != nil {
		s.popLoopTargets()
		return err
	}
	s.popLoopTargets()
	if !s.currentBlockTerminated() {
		llvmBuildBr(s.builder, condBB)
	}

	llvmPositionBuilderAtEnd(s.builder, exitBB)
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
	boolType := llvmInt1TypeInContext(s.g.context)
	zeroValue := llvmConstInt(loopLLVMType, 0, 0)

	var ascendingValue llvmIRValueRef
	switch stmt.Op {
	case lexer.TOKEN_RANGE:
		pred := llvmIRIntPredicate(llvmIntULE)
		if isSignedIntegerType(loopType) {
			pred = llvmIRIntPredicate(llvmIntSLE)
		}
		ascendingValue = llvmBuildICmp(s.builder, pred, startValue, endValue, cStringFree("for.asc"))
	case lexer.TOKEN_RANGE_LT:
		ascendingValue = llvmConstInt(boolType, 1, 0)
	case lexer.TOKEN_RANGE_GT:
		ascendingValue = llvmConstInt(boolType, 0, 0)
	default:
		return fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(stmt.Op))
	}
	hasStep := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), stepValue, zeroValue, cStringFree("for.step.nonzero"))

	currentAlloca, err := s.createEntryAlloca(stmt.Name+".for.cur", loopType)
	if err != nil {
		return err
	}
	llvmBuildStore(s.builder, startValue, currentAlloca)
	loopVarAlloca, err := s.createEntryAlloca(stmt.Name, loopType)
	if err != nil {
		return err
	}

	condBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.cond"))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.body"))
	stepBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.step"))
	exitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.end"))

	llvmBuildCondBr(s.builder, hasStep, condBB, exitBB)

	llvmPositionBuilderAtEnd(s.builder, condBB)
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
	condValue := llvmBuildSelect(s.builder, ascendingValue, ascendingCond, descendingCond, cStringFree("for.cond.select"))
	llvmBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: loopVarAlloca, typ: loopType})
	llvmBuildStore(s.builder, currentValue, loopVarAlloca)
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
		llvmBuildBr(s.builder, stepBB)
	}

	llvmPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextAscending := llvmBuildAdd(s.builder, currentValue, stepValue, cStringFree("for.next.asc"))
		nextDescending := llvmBuildSub(s.builder, currentValue, stepValue, cStringFree("for.next.desc"))
		nextValue := llvmBuildSelect(s.builder, ascendingValue, nextAscending, nextDescending, cStringFree("for.next"))
		llvmBuildStore(s.builder, nextValue, currentAlloca)
		backedge := llvmBuildBr(s.builder, condBB)
		if stmt.AutovecExpected {
			s.tagAutovecExpectedLoop(backedge, stmt.Position, stmt.AutovecReason)
		} else if stmt.Step == nil && s.userLoopVectorEligible(stmt.Body) {
			// A user range loop whose body is the clean element-wise store shape is expected to
			// vectorize too (docs/70); scalar fallback warns / errors under -Wperf unless the loop
			// sits inside a `can Scalar` grant (checked inside tagAutovecExpectedLoop).
			s.tagAutovecExpectedLoop(backedge, stmt.Position, "loop")
		}
	}

	llvmPositionBuilderAtEnd(s.builder, exitBB)
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
func (s *functionState) emitForLoopStepMagnitude(stmt *ast.ForStmt, loopType semantic.Type) (llvmIRValueRef, error) {
	loopLLVMType, err := s.g.lowerType(loopType)
	if err != nil {
		return nil, err
	}
	if stmt.Step == nil {
		return llvmConstInt(loopLLVMType, 1, 0), nil
	}
	rawStep, _, err := s.emitExpr(stmt.Step, loopType)
	if err != nil {
		return nil, err
	}
	if !isSignedIntegerType(loopType) {
		return rawStep, nil
	}
	zeroValue := llvmConstInt(loopLLVMType, 0, 0)
	isNegative := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntSLT), rawStep, zeroValue, cStringFree("for.step.neg"))
	negated := llvmBuildNeg(s.builder, rawStep, cStringFree("for.step.abs.neg"))
	return llvmBuildSelect(s.builder, isNegative, negated, rawStep, cStringFree("for.step.abs")), nil
}
func (s *functionState) emitForLoopContinueCmp(op lexer.TokenKind, loopType semantic.Type, currentValue, endValue llvmIRValueRef, ascending bool) (llvmIRValueRef, error) {
	var pred llvmIRIntPredicate
	signed := isSignedIntegerType(loopType)
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			if signed {
				pred = llvmIRIntPredicate(llvmIntSLE)
			} else {
				pred = llvmIRIntPredicate(llvmIntULE)
			}
		} else {
			if signed {
				pred = llvmIRIntPredicate(llvmIntSGE)
			} else {
				pred = llvmIRIntPredicate(llvmIntUGE)
			}
		}
	case lexer.TOKEN_RANGE_LT:
		if signed {
			pred = llvmIRIntPredicate(llvmIntSLT)
		} else {
			pred = llvmIRIntPredicate(llvmIntULT)
		}
	case lexer.TOKEN_RANGE_GT:
		if signed {
			pred = llvmIRIntPredicate(llvmIntSGT)
		} else {
			pred = llvmIRIntPredicate(llvmIntUGT)
		}
	default:
		return nil, fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(op))
	}
	return llvmBuildICmp(s.builder, pred, currentValue, endValue, cStringFree("for.cmp")), nil
}
