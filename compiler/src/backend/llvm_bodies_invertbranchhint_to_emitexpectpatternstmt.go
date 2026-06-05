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
	"sort"
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
	condValue, _, err := s.emitExpr(expr, s.g.result.NamedTypes["bool"])
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
		if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, elseBB, stmt.Hint); err != nil {
			return err
		}
	} else {
		if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, mergeBB, stmt.Hint); err != nil {
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
		if err := s.emitBlock(stmt.Body, true); err != nil {
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
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}
	s.buildCondBrWithHint(condValue, bodyBB, exitBB, stmt.Hint)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushLoopTargets(exitBB, condBB)
	if err := s.emitBlock(stmt.Body, true); err != nil {
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
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popLoopTargets()
		s.popScope()
		return err
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
		C.LLVMBuildBr(s.builder, condBB)
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
func (s *functionState) bindMatchedPackedVariantView(name string, pattern ast.MatchPattern, enumValue C.LLVMValueRef, decodedValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, payloadValues packedPayloadValueCache) {
	if enumType == nil || !enumType.Packed {
		return
	}
	if name == "" {
		return
	}
	variantPattern, ok := pattern.(*ast.MatchVariantPattern)
	if !ok {
		return
	}
	variant, ok := enumType.Variant(variantPattern.Variant)
	if !ok || variant == nil {
		return
	}
	viewType := s.g.cachedPackedVariantViewType(enumType, variant)
	if decodedValue != nil {
		storeValue := packedStoreBinding{}
		if store != nil {
			storeValue = *store
		}
		s.bindPackedVariantViewOwned(name, viewType, decodedValue, enumValue, storeValue, payloadValues)
		return
	}
	if store != nil {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, *store, payloadValues)
		return
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, packedStoreBinding{}, payloadValues)
	}
}
func resolveMatchableEnumType(actual semantic.Type) (*semantic.EnumType, bool) {
	switch tt := actual.(type) {
	case *semantic.EnumType:
		return tt, tt != nil
	case *semantic.PackedVariantViewType:
		if tt == nil || tt.Enum == nil {
			return nil, false
		}
		return tt.Enum, true
	default:
		return nil, false
	}
}
func resolveMatchableConstEnumType(actual semantic.Type) (*semantic.ConstEnumType, bool) {
	constEnumType, ok := semantic.StripAggregateStateType(actual).(*semantic.ConstEnumType)
	return constEnumType, ok && constEnumType != nil
}
func resolveMatchableStructTypeBackend(actual semantic.Type) bool {
	switch tt := semantic.StripAggregateStateType(actual).(type) {
	case *semantic.StructType:
		return tt != nil && tt.Decl != nil
	case *semantic.GenericInstanceType:
		base, _ := tt.Base.(*semantic.StructType)
		return base != nil && base.Decl != nil
	case *semantic.TreeBlockType:
		return tt != nil && tt.Decl != nil
	case *semantic.TreeStructType:
		return tt != nil && tt.Decl != nil
	default:
		return false
	}
}
func resolveMatchableTupleTypeBackend(actual semantic.Type) bool {
	tupleType, ok := semantic.StripAggregateStateType(actual).(*semantic.TupleType)
	return ok && tupleType != nil
}
func resolveMatchableSequenceTypeBackend(actual semantic.Type) bool {
	_, ok := semantic.SequenceMatchElementType(actual)
	return ok
}
func runtimeStringLiteralType() semantic.Type {
	return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}, State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}
}
func isStringMatchableType(actual semantic.Type) bool {
	_, _, _, _, ok := runtimeStringCompareInfo(actual, runtimeStringLiteralType())
	return ok
}
func resolveMatchableErrorSetTypeBackend(actual semantic.Type) (*semantic.ErrorSetType, bool) {
	errorSetType, ok := semantic.StripAggregateStateType(actual).(*semantic.ErrorSetType)
	return errorSetType, ok && errorSetType != nil
}
func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitEnumMatch(stmt, enumType)
	}
	constEnumType, ok := resolveMatchableConstEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitConstEnumMatch(stmt, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetTypeBackend(s.exprType(stmt.Value))
	if ok {
		return s.emitErrorSetMatch(stmt, errorSetType)
	}
	if optionalType, ok := s.exprType(stmt.Value).(*semantic.OptionalType); ok {
		return s.emitOptionalMatch(stmt, optionalType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryTypeBackend(s.exprType(stmt.Value))
	if ok {
		return s.emitTreeMatch(stmt, treeType)
	}
	if isStringMatchableType(s.exprType(stmt.Value)) {
		return s.emitStringMatch(stmt)
	}
	if resolveMatchableTupleTypeBackend(s.exprType(stmt.Value)) {
		return s.emitTupleMatch(stmt)
	}
	if resolveMatchableSequenceTypeBackend(s.exprType(stmt.Value)) {
		return s.emitSequenceMatch(stmt)
	}
	if resolveMatchableStructTypeBackend(s.exprType(stmt.Value)) {
		return s.emitStructMatch(stmt)
	}
	return fmt.Errorf("match requires an enum, const enum, error set, optional, tree-category, string, tuple, sequence, or struct value")
}
func (s *functionState) emitExpectPatternStmt(stmt *ast.ExpectPatternStmt) error {
	if stmt == nil {
		return nil
	}
	actualType := s.exprType(stmt.Value)
	actualValue, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	if len(stmt.Patterns) == 1 {
		bindings := map[string]semantic.Type{}
		if err := s.collectMatchPatternBindings(stmt.Patterns[0], actualType, bindings); err != nil {
			return err
		}
		names := make([]string, 0, len(bindings))
		for name := range bindings {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			alloca, err := s.createEntryAlloca(name+".expect", bindings[name])
			if err != nil {
				return err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: bindings[name], mutable: false})
		}
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.ok"))
	failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.fail"))
	if len(stmt.Patterns) == 0 {
		C.LLVMBuildBr(s.builder, successBB)
	} else {
		for i, pattern := range stmt.Patterns {
			nextFailure := failureBB
			if i != len(stmt.Patterns)-1 {
				nextFailure = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.next"))
			}
			if err := s.emitConditionPatternTestAndBind(pattern, actualValue, actualType, stmt.Value, successBB, nextFailure); err != nil {
				return err
			}
			if i != len(stmt.Patterns)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextFailure)
			}
		}
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
	if err := s.emitPanicWithBacktrace(stmt.Position, &ast.StringLit{Position: stmt.Position, Value: "expect pattern failed"}); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	return nil
}
