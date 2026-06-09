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
	"strconv"
)

func (s *functionState) emitStringMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	actualType := s.exprType(expr.Value)
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if err := s.emitStringMatchPatternTest(arm.Pattern, expr.Value, actualType, bodyBB, nextBB); err != nil {
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

	hasWildcard := matchHasWildcard(expr.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard || semantic.IsNeverType(resultType) {
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
func (s *functionState) emitStructMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("struct match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
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
func (s *functionState) emitTupleMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("tuple match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
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
func (s *functionState) emitSequenceMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("sequence match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
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
func (s *functionState) emitStructMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("struct match does not take an in-store clause")
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
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

	hasWildcard := matchHasWildcard(expr.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard || semantic.IsNeverType(resultType) {
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
func (s *functionState) emitTupleMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("tuple match does not take an in-store clause")
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
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

	hasWildcard := matchHasWildcard(expr.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard || semantic.IsNeverType(resultType) {
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
func (s *functionState) emitMatchExprArmBody(body []ast.Stmt, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	if len(body) == 0 {
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	scope := s.scope
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			if err := s.emitStmt(stmt); err != nil {
				s.discardScopeCleanups(scope)
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				s.discardScopeCleanups(scope)
				return nil, false, nil
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			value, _, err := s.emitExpr(exprStmt.Expr, resultType)
			if err != nil {
				s.discardScopeCleanups(scope)
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				s.discardScopeCleanups(scope)
				return nil, false, nil
			}
			if err := s.emitScopeCleanups(scope); err != nil {
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				return nil, false, nil
			}
			return value, true, nil
		}
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil, false, nil
		}
		if err := s.emitScopeCleanups(scope); err != nil {
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	return nil, false, fmt.Errorf("match expression arm must end with an expression")
}
func (s *functionState) predicateMatchPatternFuncType(name string) *semantic.FuncType {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil || name == "" {
		return nil
	}
	sym, ok := s.g.result.GlobalScope.Lookup(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil || backendExplicitParamCount(fnType, nil) != 1 || len(fnType.Params) == 0 || !semantic.IsBoolType(fnType.Return) {
		return nil
	}
	return s.specializeFunctionType(fnType)
}
func (s *functionState) emitPredicateMatchPatternTest(name string, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (bool, error) {
	fnType := s.predicateMatchPatternFuncType(name)
	if fnType == nil {
		return false, nil
	}
	arg := actualValue
	if !semantic.SameType(actualType, fnType.Params[0]) {
		var err error
		arg, err = s.coerceValue(actualValue, actualType, fnType.Params[0])
		if err != nil {
			return true, err
		}
	}
	callee, err := s.g.ensureFunctionDeclared(name, fnType)
	if err != nil {
		return true, err
	}
	llvmFnType, err := s.g.lowerFunctionType(fnType)
	if err != nil {
		return true, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arg}, "match.predicate")
	C.LLVMBuildCondBr(s.builder, call, successBB, failureBB)
	return true, nil
}
func (s *functionState) emitSequenceCountValue(expr ast.Expr, actualType semantic.Type, name string) (C.LLVMValueRef, error) {
	actualType = semantic.StripAggregateStateType(actualType)
	switch t := actualType.(type) {
	case *semantic.ArrayType:
		return s.safeIndexArrayCountValue(t)
	case *semantic.DArrayType:
		ptr, _, err := s.emitAddressOrTemp(expr)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(ptr, t, name)
	case *semantic.ViewType:
		ptr, _, err := s.emitAddressOrTemp(expr)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(ptr, t, name)
	case *semantic.RefType:
		if t.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("list pattern requires a non-null sequence reference")
		}
		switch elem := semantic.StripAggregateStateType(t.Elem).(type) {
		case *semantic.ArrayType:
			return s.safeIndexArrayCountValue(elem)
		case *semantic.DArrayType, *semantic.ViewType:
			ptr, _, err := s.emitExpr(expr, actualType)
			if err != nil {
				return nil, err
			}
			return s.emitContainerCountValue(ptr, elem, name)
		default:
			return nil, fmt.Errorf("list pattern length is not implemented for %s", actualType.String())
		}
	default:
		return nil, fmt.Errorf("list pattern length is not implemented for %s", actualType.String())
	}
}
func countMatchPatternExpectedLen(pattern ast.MatchPattern) (uint64, bool, error) {
	structPattern, ok := pattern.(*ast.MatchStructPattern)
	if !ok || structPattern == nil || structPattern.TypeName != "count" || structPattern.Brace {
		return 0, false, nil
	}
	if len(structPattern.Args) != 1 || structPattern.Args[0].Name != "" {
		return 0, true, fmt.Errorf("count pattern expects one positional integer literal")
	}
	literal, ok := structPattern.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok {
		return 0, true, fmt.Errorf("count pattern expects an integer literal")
	}
	intLit, ok := literal.Value.(*ast.IntLit)
	if !ok {
		return 0, true, fmt.Errorf("count pattern expects an integer literal")
	}
	value, err := strconv.ParseUint(intLit.Value, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("count pattern expects a non-negative integer literal")
	}
	return value, true, nil
}
