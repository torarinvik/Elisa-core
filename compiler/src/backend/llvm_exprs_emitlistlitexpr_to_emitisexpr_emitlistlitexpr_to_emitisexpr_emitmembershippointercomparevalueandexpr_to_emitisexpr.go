//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitMembershipPointerCompareValueAndExpr(leftValue llvmIRValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (llvmIRValueRef, bool, error) {
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, false, nil
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, operandType)
	if err != nil {
		return nil, true, err
	}
	cmp := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), coercedLeft, rightValue, cStringFree("membership.ptr.eq"))
	return cmp, resultType != nil, nil
}
func (s *functionState) emitMembershipOptionalCompareValueAndExpr(leftValue llvmIRValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (llvmIRValueRef, bool, error) {
	if leftOptional, ok := leftType.(*semantic.OptionalType); ok && semantic.IsNullType(rightType) {
		presentValue, err := s.extractOptionalPresent(leftValue, leftOptional)
		if err != nil {
			return nil, true, err
		}
		return llvmBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
		rightValue, _, err := s.emitExpr(rightExpr, rightOptional)
		if err != nil {
			return nil, true, err
		}
		presentValue, err := s.extractOptionalPresent(rightValue, rightOptional)
		if err != nil {
			return nil, true, err
		}
		return llvmBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	return nil, false, nil
}
func (s *functionState) emitRuntimeStringCompareValues(op lexer.TokenKind, leftValue llvmIRValueRef, leftType semantic.Type, rightExpr ast.Expr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (llvmIRValueRef, error) {
	var (
		firstValue  llvmIRValueRef
		secondValue llvmIRValueRef
		err         error
	)
	if swap {
		firstValue, _, err = s.emitExpr(rightExpr, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, err = s.coerceValue(leftValue, leftType, secondType)
		if err != nil {
			return nil, err
		}
	} else {
		firstValue, err = s.coerceValue(leftValue, leftType, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, _, err = s.emitExpr(rightExpr, secondType)
		if err != nil {
			return nil, err
		}
	}
	helperReturn := s.g.namedType("int")
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []llvmIRValueRef{firstValue, secondValue}, "membership.strcmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, err
	}
	zero := llvmConstInt(helperLLVMType, 0, 0)
	pred := llvmIRIntPredicate(llvmIntNE)
	if op == lexer.TOKEN_BANGEQ {
		pred = llvmIRIntPredicate(llvmIntEQ)
	}
	return llvmBuildICmp(s.builder, pred, call, zero, cStringFree("membership.strcmp.eq")), nil
}
func (s *functionState) emitIsExpr(expr *ast.BinaryExpr) (llvmIRValueRef, semantic.Type, error) {
	// docs/85 §2: `subject is Law` was desugared by analysis into the call `Law(subject)`; emit it.
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnLawIsCallsMap() {
		if call := s.g.semanticReadView().ValueLawIsCalls(expr); call != nil {
			return s.emitExpr(call, nil)
		}
	}
	targets := flattenIsTargetExprsBackend(expr.Right)
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("is expression is missing a target")
	}
	var combined llvmIRValueRef
	for i, target := range targets {
		var (
			value llvmIRValueRef
			err   error
		)
		if pattern, ok := s.structIsTargetPattern(target); ok {
			value, _, err = s.emitStructIsTest(expr.Left, pattern)
		} else if enumType, variant, pattern, ok := s.enumIsTargetPattern(target); ok {
			value, _, err = s.emitEnumIsTest(expr.Left, enumType, variant, pattern)
		} else if scrutinee, category, ok := s.enumCategoryIsTarget(expr.Left, target); ok {
			value, _, err = s.emitEnumCategoryIsTest(expr.Left, scrutinee, category)
		} else if base, cases, ok := s.namedStateIsTarget(target); ok {
			value, _, err = s.emitNamedStateIsTest(expr.Left, base, cases)
		} else {
			value, _, err = s.emitComparableIsTargetTest(expr.Left, target)
		}
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = value
			continue
		}
		combined = llvmBuildOr(s.builder, combined, value, cStringFree("istest.or"))
	}
	return combined, s.g.namedType("bool"), nil
}
