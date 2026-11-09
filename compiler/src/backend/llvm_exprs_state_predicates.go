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
	"fmt"
	"strings"
)

func (s *functionState) emitNamedStateIsTest(leftExpr ast.Expr, base *semantic.StructType, cases []string) (C.LLVMValueRef, semantic.Type, error) {
	if base == nil {
		return nil, nil, fmt.Errorf("missing named-state struct metadata")
	}
	if len(cases) == 0 {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0), s.g.namedType("bool"), nil
	}
	if len(cases) == len(base.NamedStateCases) {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), s.g.namedType("bool"), nil
	}
	selfType := s.exprType(leftExpr)
	selfValue, _, err := s.emitExpr(leftExpr, selfType)
	if err != nil {
		return nil, nil, err
	}
	var combined C.LLVMValueRef
	for i, stateName := range cases {
		pred, err := s.emitDerivedStatePredicate(base, stateName, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = pred
			continue
		}
		combined = C.LLVMBuildOr(s.builder, combined, pred, cStringFree("isstate.or"))
	}
	return combined, s.g.namedType("bool"), nil
}
func (s *functionState) emitDerivedStatePredicate(base *semantic.StructType, stateName string, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, error) {
	if base == nil || base.DerivedStateMap == nil {
		return nil, fmt.Errorf("struct %q is missing derived state metadata", base.Name)
	}
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return nil, fmt.Errorf("struct %q has no derived state rule for %q", base.Name, stateName)
	}
	value, resultType, err := s.emitDerivedStateExpr(derived.Condition, selfValue, selfType)
	if err != nil {
		return nil, err
	}
	if !semantic.IsBoolType(resultType) {
		return nil, fmt.Errorf("derived state %s.%s does not evaluate to bool", base.Name, stateName)
	}
	return value, nil
}
func (s *functionState) emitDerivedStateExpr(expr ast.Expr, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		value, fieldType, err := s.emitDerivedStateSelfPath(selfValue, selfType, path)
		return value, fieldType, err
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitDerivedStateExpr(n.Inner, selfValue, selfType)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return s.emitExpr(expr, nil)
	case *ast.FieldExpr:
		// Qualified zero-payload enum variants are immutable constants. The
		// semantic pass admits them as pure derived-state operands; use the
		// ordinary field emitter so packed and non-packed enum representations
		// retain their normal construction rules.
		return s.emitExpr(n, nil)
	case *ast.UnaryExpr:
		operand, operandType, err := s.emitDerivedStateExpr(n.Operand, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.not")), s.g.namedType("bool"), nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFNeg(s.builder, operand, cStringFree("isstate.fneg")), operandType, nil
			}
			return C.LLVMBuildNeg(s.builder, operand, cStringFree("isstate.neg")), operandType, nil
		case lexer.TOKEN_TILDE:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.bitnot")), operandType, nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state unary operator %s", lexer.TokenName(n.Op))
		}
	case *ast.BinaryExpr:
		leftValue, leftType, err := s.emitDerivedStateExpr(n.Left, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		rightValue, rightType, err := s.emitDerivedStateExpr(n.Right, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			if n.Op == lexer.TOKEN_AND {
				return C.LLVMBuildAnd(s.builder, leftValue, rightValue, cStringFree("isstate.and")), s.g.namedType("bool"), nil
			}
			return C.LLVMBuildOr(s.builder, leftValue, rightValue, cStringFree("isstate.or")), s.g.namedType("bool"), nil
		}
		if (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) && (isPointerLikeType(leftType) || semantic.IsNullType(leftType)) && (isPointerLikeType(rightType) || semantic.IsNullType(rightType)) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.ptrcmp")), s.g.namedType("bool"), nil
		}
		if semantic.IsBoolType(leftType) && semantic.IsBoolType(rightType) && (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.boolcmp")), s.g.namedType("bool"), nil
		}
		operandType := s.binaryOperandType(n.Op, leftType, rightType)
		coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
		if err != nil {
			return nil, nil, err
		}
		coercedRight, err := s.coerceValue(rightValue, rightType, operandType)
		if err != nil {
			return nil, nil, err
		}
		resultType := s.exprType(n)
		if resultType == nil {
			resultType = operandType
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
			}
			return C.LLVMBuildAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
			}
			return C.LLVMBuildSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
		case lexer.TOKEN_STAR:
			if isFloatType(operandType) {
				return C.LLVMBuildFMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
			}
			return C.LLVMBuildMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
		case lexer.TOKEN_SLASH:
			if isFloatType(operandType) {
				return C.LLVMBuildFDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			return C.LLVMBuildUDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
		case lexer.TOKEN_PERCENT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSRem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
			}
			return C.LLVMBuildURem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
		case lexer.TOKEN_PIPE:
			return C.LLVMBuildOr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitor")), resultType, nil
		case lexer.TOKEN_CARET:
			return C.LLVMBuildXor(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitxor")), resultType, nil
		case lexer.TOKEN_AMPERSAND:
			return C.LLVMBuildAnd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitand")), resultType, nil
		case lexer.TOKEN_LSHIFT:
			return C.LLVMBuildShl(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shl")), resultType, nil
		case lexer.TOKEN_RSHIFT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildAShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
			}
			return C.LLVMBuildLShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			if isFloatType(operandType) {
				pred, err := llvmFloatPredicate(n.Op)
				if err != nil {
					return nil, nil, err
				}
				return C.LLVMBuildFCmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.fcmp")), s.g.namedType("bool"), nil
			}
			pred, err := llvmIntPredicate(n.Op, operandType)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildICmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.icmp")), s.g.namedType("bool"), nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state binary operator %s", lexer.TokenName(n.Op))
		}
	default:
		return nil, nil, fmt.Errorf("unsupported derived-state expression %T", expr)
	}
}
func (s *functionState) emitDerivedStateSelfPath(selfValue C.LLVMValueRef, selfType semantic.Type, path []string) (C.LLVMValueRef, semantic.Type, error) {
	// A named-state test is commonly applied to a borrowed value (`value is
	// App[Running]`). The semantic type retains that reference transport while
	// the derived predicate describes fields of the referenced struct. Load the
	// aggregate once before walking its field path; otherwise `fieldInfo` sees a
	// pointer-like type and rejects an otherwise ordinary `self.phase` access.
	if refType, ok := selfType.(*semantic.RefType); ok {
		if refType == nil || refType.Elem == nil {
			return nil, nil, fmt.Errorf("derived-state self reference has no element type")
		}
		var err error
		selfValue, err = s.loadValue(selfValue, refType.Elem, "isstate.self")
		if err != nil {
			return nil, nil, err
		}
		selfType = refType.Elem
	}
	currentValue := selfValue
	currentType := selfType
	if len(path) == 0 {
		return currentValue, currentType, nil
	}
	for _, field := range path {
		fieldType, index, _, pointerLike, err := s.g.fieldInfo(currentType, field)
		if err != nil {
			return nil, nil, err
		}
		if pointerLike {
			return nil, nil, fmt.Errorf("derived-state field access over pointer-like self path is not supported")
		}
		currentValue = C.LLVMBuildExtractValue(s.builder, currentValue, C.unsigned(index), cStringFree("isstate.field."+field))
		currentType = fieldType
	}
	return currentValue, currentType, nil
}
func (s *functionState) enumIsTarget(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		return s.enumConstructorInfoFromField(fieldExpr)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	enumName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := s.lookupVisibleNamedType(enumName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || enumType == nil {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, false
	}
	return enumType, variant, true
}
