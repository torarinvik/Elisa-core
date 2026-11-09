//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func (s *functionState) emitPointerCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, nil, false, nil
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)
	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	pred := C.LLVMIntPredicate(C.LLVMIntEQ)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntNE)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("ptrcmptmp"))
	return cmp, resultType, true, nil
}
func (s *functionState) emitOptionalCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	var (
		optionalExpr ast.Expr
		optionalType *semantic.OptionalType
	)
	switch leftOptional := leftType.(type) {
	case *semantic.OptionalType:
		if semantic.IsNullType(rightType) {
			optionalExpr = expr.Left
			optionalType = leftOptional
		}
	}
	if optionalType == nil {
		if leftOptional, ok := leftType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Right.(*ast.NullLit); isNull {
				optionalExpr = expr.Left
				optionalType = leftOptional
			}
		}
	}
	if optionalType == nil {
		if _, isNull := expr.Right.(*ast.NullLit); isNull {
			if leftOptional, ok := s.optionalCompareStoredType(expr.Left); ok {
				optionalExpr = expr.Left
				optionalType = leftOptional
			}
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
			optionalExpr = expr.Right
			optionalType = rightOptional
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Left.(*ast.NullLit); isNull {
				optionalExpr = expr.Right
				optionalType = rightOptional
			}
		}
	}
	if optionalType == nil {
		if _, isNull := expr.Left.(*ast.NullLit); isNull {
			if rightOptional, ok := s.optionalCompareStoredType(expr.Right); ok {
				optionalExpr = expr.Right
				optionalType = rightOptional
			}
		}
	}
	if optionalType == nil {
		return nil, nil, false, nil
	}
	optionalValue, err := s.emitOptionalCompareOperandValue(optionalExpr, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_EQEQ {
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("optionalisnull")), resultType, true, nil
	}
	return presentValue, resultType, true, nil
}
func (s *functionState) optionalCompareStoredType(expr ast.Expr) (*semantic.OptionalType, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.optionalCompareStoredType(n.Inner)
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			if optionalType, ok := binding.typ.(*semantic.OptionalType); ok {
				return optionalType, true
			}
		}
	case *ast.FieldExpr:
		if ptr, fieldType, err := s.emitFieldAddress(n); err == nil && ptr != nil {
			if optionalType, ok := fieldType.(*semantic.OptionalType); ok {
				return optionalType, true
			}
		}
	}
	return nil, false
}
func (s *functionState) emitOptionalCompareOperandValue(expr ast.Expr, optionalType *semantic.OptionalType) (C.LLVMValueRef, error) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitOptionalCompareOperandValue(n.Inner, optionalType)
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			if storedOptional, ok := binding.typ.(*semantic.OptionalType); ok && semantic.SameType(storedOptional, optionalType) {
				return s.loadValue(binding.ptr, storedOptional, n.Name)
			}
		}
	case *ast.FieldExpr:
		ptr, fieldType, err := s.emitFieldAddress(n)
		if err == nil {
			if storedOptional, ok := fieldType.(*semantic.OptionalType); ok && semantic.SameType(storedOptional, optionalType) {
				return s.loadValue(ptr, storedOptional, n.Field)
			}
		}
	}
	optionalValue, _, err := s.emitExpr(expr, optionalType)
	return optionalValue, err
}
func (s *functionState) emitPointerArithmeticExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if _, ok := resultType.(*semantic.RefType); !ok {
		return nil, nil, false, nil
	}
	leftRef, leftIsRef := leftType.(*semantic.RefType)
	rightRef, rightIsRef := rightType.(*semantic.RefType)
	leftIsNumeric := isNumericType(leftType)
	rightIsNumeric := isNumericType(rightType)

	var (
		baseExpr  ast.Expr
		baseType  *semantic.RefType
		indexExpr ast.Expr
	)

	switch {
	case leftIsRef && rightIsNumeric && (expr.Op == lexer.TOKEN_PLUS || expr.Op == lexer.TOKEN_MINUS):
		baseExpr, baseType, indexExpr = expr.Left, leftRef, expr.Right
	case expr.Op == lexer.TOKEN_PLUS && leftIsNumeric && rightIsRef:
		baseExpr, baseType, indexExpr = expr.Right, rightRef, expr.Left
	default:
		return nil, nil, false, nil
	}

	baseValue, _, err := s.emitExpr(baseExpr, baseType)
	if err != nil {
		return nil, nil, true, err
	}
	indexValue, _, err := s.emitExpr(indexExpr, nil)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_MINUS {
		indexValue = C.LLVMBuildNeg(s.builder, indexValue, cStringFree("ptridx.neg"))
	}
	elemLLVMType, err := s.g.lowerType(baseType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	indices := []C.LLVMValueRef{indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, baseValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("ptrarith"))
	return ptr, resultType, true, nil
}

// emitStringCompareOperandValue materializes a string-compare operand as a VALUE of
// valueType. When the operand expression is a reference to a view (`sview&`, e.g. a
// `dict.get` result bound via `is`), emitExpr on it yields the pointer, not the aggregate —
// the value-context coercion that loads scalars through a ref does not load a StringView. So
// detect the ref, emit it as the pointer, and load the value through it. valueType comes from
// stringCompareValueOperandType (already peeled), so it matches the helper's declared param.
