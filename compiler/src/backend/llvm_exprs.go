//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strconv"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func (s *functionState) emitExpr(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("cannot emit nil expression")
	}

	actualType := s.exprType(expr)
	if expected != nil && isZeroedExpr(expr) {
		value, err := s.zeroValue(expected)
		return value, expected, err
	}

	var (
		value C.LLVMValueRef
		err   error
	)

	switch n := expr.(type) {
	case *ast.Ident:
		value, actualType, err = s.emitIdent(n)
	case *ast.IntLit:
		value, actualType, err = s.emitIntLiteral(n)
	case *ast.StringLit:
		value, actualType, err = s.emitStringLiteral(n)
	case *ast.BoolLit:
		value, actualType, err = s.emitBoolLiteral(n)
	case *ast.NullLit:
		value, actualType, err = s.emitNullLiteral()
	case *ast.ZeroedLit:
		return nil, nil, fmt.Errorf("zeroed requires an expected destination type")
	case *ast.ListLitExpr:
		value, actualType, err = s.emitListLitExpr(n, expected)
	case *ast.BinaryExpr:
		value, actualType, err = s.emitBinaryExpr(n)
	case *ast.UnaryExpr:
		value, actualType, err = s.emitUnaryExpr(n)
	case *ast.CallExpr:
		value, actualType, err = s.emitCallExpr(n)
	case *ast.FieldExpr:
		if errorType, _, ok := s.errorTagInfo(n); ok {
			value, actualType, err = s.emitErrorTagExpr(n, errorType)
		} else if enumType, variant, ok := s.enumConstructorInfoFromField(n); ok && variant != nil && len(variant.Payload) == 0 {
			value, actualType, err = s.emitEnumConstructorValue(enumType, variant, nil)
		} else {
			value, actualType, err = s.emitFieldExpr(n)
		}
	case *ast.RaiseExpr:
		value, actualType, err = s.emitRaiseExpr(n)
	case *ast.TryExpr:
		value, actualType, err = s.emitTryExpr(n)
	case *ast.UnwrapElseExpr:
		value, actualType, err = s.emitUnwrapElseExpr(n)
	case *ast.RegionAllocExpr:
		value, actualType, err = s.emitRegionAllocExpr(n)
	case *ast.IndexExpr:
		value, actualType, err = s.emitIndexExpr(n)
	case *ast.SliceExpr:
		value, actualType, err = s.emitSliceExpr(n)
	case *ast.CastExpr:
		value, actualType, err = s.emitCastExpr(n)
	case *ast.SizeofExpr:
		value, actualType, err = s.emitSizeofExpr(n)
	case *ast.TernaryExpr:
		value, actualType, err = s.emitTernaryExpr(n)
	case *ast.AddrOfExpr:
		value, actualType, err = s.emitAddrOfExpr(n)
	case *ast.StructLitExpr:
		value, actualType, err = s.emitStructLitExpr(n)
	case *ast.ParenExpr:
		value, actualType, err = s.emitExpr(n.Inner, expected)
	default:
		return nil, nil, fmt.Errorf("unsupported expression %T", expr)
	}
	if err != nil {
		return nil, nil, err
	}
	if expected != nil {
		coerced, err := s.coerceValue(value, actualType, expected)
		if err != nil {
			return nil, nil, err
		}
		return coerced, expected, nil
	}
	return value, actualType, nil
}

func (s *functionState) buildCall(llvmFnType C.LLVMTypeRef, callee C.LLVMValueRef, args []C.LLVMValueRef, name string) C.LLVMValueRef {
	argCount := len(args)
	argPtr := llvmValueSlicePtr(args)
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.LLVMBuildCall2(s.builder, llvmFnType, callee, argPtr, C.unsigned(argCount), nameC)
}

func (s *functionState) emitIdent(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	if binding, ok := s.lookupBinding(expr.Name); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, expr.Name)
		return value, binding.typ, err
	}
	if value, ok := s.g.constValue(expr.Name); ok {
		llvmValue, llvmType, err := s.emitConstValue(value)
		return llvmValue, llvmType, err
	}
	if sym, ok := s.g.result.GlobalScope.Lookup(expr.Name); ok {
		switch sym.Kind {
		case semantic.SymbolFunc, semantic.SymbolExternFunc:
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("global function %s is missing function type", expr.Name)
			}
			value, err := s.g.ensureFunctionDeclared(expr.Name, fnType)
			return value, fnType, err
		case semantic.SymbolGlobal, semantic.SymbolExternVar:
			global, err := s.g.ensureGlobalDeclared(expr.Name, sym.Type, sym.Kind == semantic.SymbolExternVar)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(global, sym.Type, expr.Name)
			return value, sym.Type, err
		case semantic.SymbolConst:
			if value, ok := s.g.constValue(expr.Name); ok {
				llvmValue, llvmType, err := s.emitConstValue(value)
				return llvmValue, llvmType, err
			}
		}
	}
	return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering", expr.Name)
}

func (s *functionState) errorTagInfo(expr *ast.FieldExpr) (*semantic.ErrorSetType, string, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, "", false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, "", false
	}
	errSet, ok := base.(*semantic.ErrorSetType)
	if !ok || !errSet.HasQualifiedTag(ident.Name, expr.Field) {
		return nil, "", false
	}
	return errSet, semantic.QualifyErrorTag(ident.Name, expr.Field), true
}

func (s *functionState) emitErrorTagExpr(expr *ast.FieldExpr, errorType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorType == nil {
		return nil, nil, fmt.Errorf("missing error set for tag expression")
	}
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("missing error set qualifier for tag expression")
	}
	code, ok := errorType.TagCodeFor(ident.Name, expr.Field)
	if !ok {
		return nil, nil, fmt.Errorf("unknown error tag %s.%s", errorType.Name, expr.Field)
	}
	value, err := s.errorCodeConstant(code)
	if err != nil {
		return nil, nil, err
	}
	return value, errorType, nil
}

func (s *functionState) emitRaiseExpr(expr *ast.RaiseExpr) (C.LLVMValueRef, semantic.Type, error) {
	currentUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("raise requires an error-union return type")
	}
	var (
		errorValue C.LLVMValueRef
		errorType  semantic.Type
		err        error
	)
	if fieldExpr, ok := expr.Error.(*ast.FieldExpr); ok {
		if _, qualifiedTag, ok := s.errorTagInfo(fieldExpr); ok {
			mappedTag, matched := semantic.MatchErrorTag(currentUnion.Errors, qualifiedTag)
			if matched {
				code, ok := currentUnion.Errors.TagCode(mappedTag)
				if !ok {
					return nil, nil, fmt.Errorf("missing destination error tag %s", mappedTag)
				}
				errorValue, err = s.errorCodeConstant(code)
				errorType = currentUnion.Errors
			} else {
				errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
			}
		} else {
			errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
		}
	} else {
		errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitFunctionReturn(errorValue, errorType); err != nil {
		return nil, nil, err
	}
	return nil, s.exprType(expr), nil
}

func (s *functionState) emitTryExpr(expr *ast.TryExpr) (C.LLVMValueRef, semantic.Type, error) {
	unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("try requires a lowered error-union operand")
	}
	resultType := s.exprType(expr)
	fallibleValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	errorCode, err := s.extractErrorUnionCode(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	zeroCode, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, nil, err
	}
	successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("try.ok"))

	if expr.Fallback == nil {
		okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.ok"))
		errBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.err"))
		C.LLVMBuildCondBr(s.builder, successCond, okBB, errBB)

		C.LLVMPositionBuilderAtEnd(s.builder, errBB)
		if _, ok := s.fnType.Return.(*semantic.ErrorUnionType); !ok {
			return nil, nil, fmt.Errorf("try propagation requires an error-union function return")
		}
		if err := s.emitFunctionReturn(errorCode, unionType.Errors); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, okBB)
		if isVoidType(resultType) {
			return nil, resultType, nil
		}
		payload, err := s.extractErrorUnionPayload(fallibleValue, unionType)
		if err != nil {
			return nil, nil, err
		}
		return payload, resultType, nil
	}

	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.value"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.merge"))
	C.LLVMBuildCondBr(s.builder, successCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	var okValue C.LLVMValueRef
	if !isVoidType(resultType) {
		okValue, err = s.extractErrorUnionPayload(fallibleValue, unionType)
		if err != nil {
			return nil, nil, err
		}
	}
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
			incomingBlocks = append(incomingBlocks, okEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
			incomingBlocks = append(incomingBlocks, fallbackEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("tryphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitUnwrapElseExpr(expr *ast.UnwrapElseExpr) (C.LLVMValueRef, semantic.Type, error) {
	valueType, ok := s.exprType(expr.Value).(*semantic.RefType)
	if !ok {
		return nil, nil, fmt.Errorf("else recovery requires a reference operand")
	}
	resultType := s.exprType(expr)
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	llvmRefType, err := s.g.lowerType(valueType)
	if err != nil {
		return nil, nil, err
	}
	nullValue := C.LLVMConstNull(llvmRefType)
	nonNullCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("unwrap.nonnull"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.ok"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.merge"))
	C.LLVMBuildCondBr(s.builder, nonNullCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, fallbackValue)
		incomingBlocks = append(incomingBlocks, fallbackEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("unwrapphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitIntLiteral(expr *ast.IntLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["int"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseUint(expr.Value, 0, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse integer literal %q: %w", expr.Value, err)
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(parsed), 0), t, nil
}

func (s *functionState) emitStringLiteral(expr *ast.StringLit) (C.LLVMValueRef, semantic.Type, error) {
	name := cString("str")
	defer C.free(unsafe.Pointer(name))
	text := cString(expr.Value)
	defer C.free(unsafe.Pointer(text))
	value := C.LLVMBuildGlobalStringPtr(s.builder, text, name)
	return value, s.exprType(expr), nil
}

func (s *functionState) emitListLitExpr(expr *ast.ListLitExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	arrayType, err := s.listLiteralTargetType(expr, expected)
	if err != nil {
		return nil, nil, err
	}
	if arrayType.HasConstSize && arrayType.ConstSize != int64(len(expr.Elems)) {
		return nil, nil, fmt.Errorf("array literal resolved to %s but has %d elements", arrayType.String(), len(expr.Elems))
	}
	llvmType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, err
	}
	current := C.LLVMGetUndef(llvmType)
	for i, elem := range expr.Elems {
		elemValue, _, err := s.emitExpr(elem, arrayType.Elem)
		if err != nil {
			return nil, nil, err
		}
		current = C.LLVMBuildInsertValue(s.builder, current, elemValue, C.unsigned(i), cStringFree("arraylit.elem"))
	}
	return current, arrayType, nil
}

func (s *functionState) listLiteralTargetType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.ArrayType, error) {
	if expectedArray, ok := expected.(*semantic.ArrayType); ok {
		return expectedArray, nil
	}
	actualArray, ok := s.exprType(expr).(*semantic.ArrayType)
	if !ok {
		return nil, fmt.Errorf("array literal did not resolve to a fixed array type")
	}
	return actualArray, nil
}

func (s *functionState) emitStackTempZeroed(t semantic.Type, name string) (C.LLVMValueRef, error) {
	zero, err := s.zeroValue(t)
	if err != nil {
		return nil, err
	}
	return s.emitStackTempValue(zero, t, name)
}

func (s *functionState) emitStackTempValue(value C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	alloca, err := s.createEntryAlloca(name, t)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, value, alloca)
	return alloca, nil
}

func (s *functionState) emitBoolLiteral(expr *ast.BoolLit) (C.LLVMValueRef, semantic.Type, error) {
	llvmType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, nil, err
	}
	var raw C.ulonglong
	if expr.Value {
		raw = 1
	}
	return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitNullLiteral() (C.LLVMValueRef, semantic.Type, error) {
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	return C.LLVMConstNull(ptrType), &semantic.NullType{}, nil
}

func (s *functionState) emitBinaryExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Op == lexer.TOKEN_AND || expr.Op == lexer.TOKEN_OR {
		return s.emitLogicalExpr(expr)
	}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(s.exprType(expr.Left), s.exprType(expr.Right)); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitRuntimeStringCompareExpr(expr, helperName, firstType, secondType, swap)
	}
	leftType := s.exprType(expr.Left)
	rightType := s.exprType(expr.Right)
	resultType := s.exprType(expr)
	if value, actualType, handled, err := s.emitPointerCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerArithmeticExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)

	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, err
	}

	switch expr.Op {
	case lexer.TOKEN_PLUS:
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
	case lexer.TOKEN_STAR:
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
	case lexer.TOKEN_SLASH:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
	case lexer.TOKEN_PERCENT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSRem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
		}
		return C.LLVMBuildURem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
	case lexer.TOKEN_PIPE:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("ortmp")), resultType, nil
	case lexer.TOKEN_CARET:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("xortmp")), resultType, nil
	case lexer.TOKEN_AMPERSAND:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("andtmp")), resultType, nil
	case lexer.TOKEN_LSHIFT:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("shltmp")), resultType, nil
	case lexer.TOKEN_RSHIFT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		pred, err := llvmIntPredicate(expr.Op, operandType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported binary operator %s", lexer.TokenName(expr.Op))
	}
}

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

func (s *functionState) emitPointerArithmeticExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
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

func (s *functionState) emitRuntimeStringCompareExpr(expr *ast.BinaryExpr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, semantic.Type, error) {
	firstExpr := expr.Left
	secondExpr := expr.Right
	if swap {
		firstExpr, secondExpr = secondExpr, firstExpr
	}
	firstValue, _, err := s.emitExpr(firstExpr, firstType)
	if err != nil {
		return nil, nil, err
	}
	secondValue, _, err := s.emitExpr(secondExpr, secondType)
	if err != nil {
		return nil, nil, err
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{firstValue, secondValue}
	call := s.buildCall(llvmFnType, callee, args, "streqtmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("strcmp"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitLogicalExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	left, _, err := s.emitExpr(expr.Left, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	rhsName := cString("logic.rhs")
	defer C.free(unsafe.Pointer(rhsName))
	rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, rhsName)
	mergeName := cString("logic.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	if expr.Op == lexer.TOKEN_AND {
		C.LLVMBuildCondBr(s.builder, left, rhsBB, mergeBB)
	} else {
		C.LLVMBuildCondBr(s.builder, left, mergeBB, rhsBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
	right, _, err := s.emitExpr(expr.Right, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	rhsEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(rhsEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("logicphi"))
	fallback := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	if expr.Op == lexer.TOKEN_OR {
		fallback = C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	}
	values := []C.LLVMValueRef{fallback, right}
	blocks := []C.LLVMBasicBlockRef{parentBlock, rhsEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitUnaryExpr(expr *ast.UnaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	operandType := s.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.exprType(expr)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		return C.LLVMBuildNot(s.builder, value, cStringFree("nottmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		return C.LLVMBuildNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
	case lexer.TOKEN_TILDE:
		return C.LLVMBuildNot(s.builder, value, cStringFree("invt")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported unary operator %s", lexer.TokenName(expr.Op))
	}
}

func (s *functionState) emitRegionAllocExpr(expr *ast.RegionAllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	binding, ok := s.lookupBinding(expr.Region)
	if !ok {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", expr.Region)
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value in %q", expr.Region)
	}
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	sizeBytes, err := s.sizeOfType(valueType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	callee, err := s.g.ensureFunctionDeclared("arena_alloc", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{binding.ptr, sizeValue}, "region.alloc")
	C.LLVMBuildStore(s.builder, value, allocPtr)
	return allocPtr, s.exprType(expr), nil
}

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		return s.emitEnumConstructorValue(enumType, variant, expr.Args)
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		call := s.buildCall(llvmFnType, callee, callArgs, "calltmp")
		payload, err := s.loadValue(resultSlot, retUnion.Value, "call.payload")
		if err != nil {
			return nil, nil, err
		}
		unionValue, err := s.buildErrorUnionValue(retUnion, call, payload)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}
	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, funcType.Return, nil
}

func (s *functionState) resolveCallTarget(expr *ast.CallExpr) (C.LLVMValueRef, *semantic.FuncType, error) {
	if ident, ok := expr.Func.(*ast.Ident); ok {
		if sym, ok := s.g.result.GlobalScope.Lookup(ident.Name); ok {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("call target %s does not resolve to a function type", ident.Name)
			}
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(decl.TypeParams) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes)
				value, specialized, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			value, err := s.g.ensureFunctionDeclared(ident.Name, specializeFuncType(fnType, s.typeMap))
			return value, specializeFuncType(fnType, s.typeMap), err
		}
	}
	calleeType, ok := s.exprType(expr.Func).(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	callee, _, err := s.emitExpr(expr.Func, nil)
	return callee, calleeType, err
}

func (s *functionState) emitFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if enumType, variant, ok := s.enumConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor %s.%s", enumType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			return s.emitEnumConstructorValue(enumType, variant, nil)
		}
	}
	if fieldType, ok := dstrSyntheticFieldType(s.exprType(expr.Object), expr.Field); ok {
		return s.emitRuntimeStringLenExpr(expr.Object, fieldType)
	}
	ptr, fieldType, addressErr := s.emitFieldAddress(expr)
	if addressErr == nil {
		value, loadErr := s.loadValue(ptr, fieldType, expr.Field)
		return value, fieldType, loadErr
	}
	objValue, objType, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, err
	}
	fieldType, index, _, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	if pointerLike {
		return nil, nil, fmt.Errorf("field %s requires an addressable object (base %T: %v)", expr.Field, expr.Object, addressErr)
	}
	value := C.LLVMBuildExtractValue(s.builder, objValue, C.unsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}

func (s *functionState) emitSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, resultType, handled, err := s.emitFixedArraySliceExpr(expr); handled {
		return value, resultType, err
	}
	info, ok := runtimeSliceOperandInfo(s.exprType(expr.Object), s.exprType(expr))
	if !ok {
		return nil, nil, fmt.Errorf("slice is not implemented for %s", s.exprType(expr.Object).String())
	}
	var (
		objectValue C.LLVMValueRef
		err         error
	)
	if info.useAddress {
		objectValue, _, err = s.emitAddressOrTemp(expr.Object)
	} else {
		objectValue, _, err = s.emitExpr(expr.Object, info.operandType)
	}
	if err != nil {
		return nil, nil, err
	}
	startValue, _, err := s.emitExpr(expr.Start, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(expr.End, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   info.helperName,
		Params: []semantic.Type{info.operandType, info.indexType, info.indexType},
		Return: info.resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(info.helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{objectValue, startValue, endValue}
	callName := "slicetmp"
	if isVoidType(info.resultType) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, info.resultType, nil
}

func (s *functionState) emitFixedArraySliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	arrayType, arrayPtr, handled, err := s.fixedArraySliceBase(expr.Object)
	if err != nil || !handled {
		return nil, nil, handled, err
	}
	resultType := s.exprType(expr)
	usizeSemanticType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	startValue, _, err := s.emitExpr(expr.Start, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	arrayLen := C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0)
	startClamped := s.emitUnsignedMin(startValue, arrayLen, usizeLLVMType, "arrayslice.start.clamped")
	endClamped := s.emitUnsignedMin(endValue, arrayLen, usizeLLVMType, "arrayslice.end.clamped")
	boundedStart := s.emitUnsignedMin(startClamped, endClamped, usizeLLVMType, "arrayslice.start.bounded")
	sliceLen := C.LLVMBuildSub(s.builder, endClamped, boundedStart, cStringFree("arrayslice.len"))

	llvmArrayType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	indices := []C.LLVMValueRef{zeroIndex, boundedStart}
	dataPtr := C.LLVMBuildGEP2(s.builder, llvmArrayType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("arrayslice.data"))

	viewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	if _, ok := resultType.(*semantic.SViewType); ok {
		viewValue := C.LLVMGetUndef(viewLLVMType)
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("strslice.view.data"))
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("strslice.view.len"))
		return viewValue, resultType, true, nil
	}
	elemSize, err := s.sizeOfType(arrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("arrayslice.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("arrayslice.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("arrayslice.view.elem_size"))
	return viewValue, resultType, true, nil
}

func (s *functionState) fixedArraySliceBase(object ast.Expr) (*semantic.ArrayType, C.LLVMValueRef, bool, error) {
	objectType := s.exprType(object)
	if arrayType, ok := objectType.(*semantic.ArrayType); ok {
		arrayPtr, _, err := s.emitAddressOrTemp(object)
		if err != nil {
			return nil, nil, true, err
		}
		return arrayType, arrayPtr, true, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, false, nil
	}
	arrayType, ok := refType.Elem.(*semantic.ArrayType)
	if !ok {
		return nil, nil, false, nil
	}
	arrayPtr, _, err := s.emitExpr(object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	return arrayType, arrayPtr, true, nil
}

func (s *functionState) emitUnsignedMin(left C.LLVMValueRef, right C.LLVMValueRef, llvmType C.LLVMTypeRef, name string) C.LLVMValueRef {
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULE), left, right, cStringFree(name+".cmp"))
	mask := C.LLVMBuildZExt(s.builder, cmp, llvmType, cStringFree(name+".mask"))
	negMask := C.LLVMBuildSub(s.builder, C.LLVMConstNull(llvmType), mask, cStringFree(name+".negmask"))
	diff := C.LLVMBuildXor(s.builder, left, right, cStringFree(name+".diff"))
	maskedDiff := C.LLVMBuildAnd(s.builder, diff, negMask, cStringFree(name+".masked"))
	return C.LLVMBuildXor(s.builder, right, maskedDiff, cStringFree(name))
}

func (s *functionState) emitRuntimeStringLenExpr(object ast.Expr, fieldType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringType, ok := dstrFieldOperandType(s.exprType(object))
	if !ok {
		return nil, nil, fmt.Errorf("string len requires dstr operand")
	}
	stringValue, _, err := s.emitExpr(object, stringType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_stage1rt_strlen",
		Params: []semantic.Type{stringType},
		Return: fieldType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_stage1rt_strlen", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue}
	call := s.buildCall(llvmFnType, callee, args, "strlen")
	return call, fieldType, nil
}

func (s *functionState) emitIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if _, ok := semanticStringArrayType(s.exprType(expr.Object)); ok {
		return s.emitStaticStringIndexExpr(expr)
	}
	if helperName, operandType, ok := runtimeStringIndexedOperand(s.exprType(expr.Object)); ok {
		return s.emitRuntimeStringIndexExpr(expr, helperName, operandType)
	}
	ptr, elemType, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
}

func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, helperName string, operandType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, operandType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.result.NamedTypes["i64"]
	resultType := s.g.result.NamedTypes["char"]
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue, indexValue}
	call := s.buildCall(llvmFnType, callee, args, "stridx")
	return call, resultType, nil
}

func semanticStringArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		if arrayType.SurfaceName == "str" || arrayType.SurfaceName == "string" {
			return arrayType, true
		}
		return nil, false
	}
	ref, ok := t.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	arrayType, ok := ref.Elem.(*semantic.ArrayType)
	if !ok || (arrayType.SurfaceName != "str" && arrayType.SurfaceName != "string") {
		return nil, false
	}
	return arrayType, true
}

func (s *functionState) emitStaticStringIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, _, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	byteType := s.g.result.NamedTypes["u8"]
	loaded, err := s.loadValue(ptr, byteType, "str.byte")
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree("stridx.zext")), resultType, nil
}

func runtimeStringIndexedOperand(t semantic.Type) (string, semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return "ctx_stage1rt_string_index", t, true
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return "ctx_stage1rt_string_view_index", t, true
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_stage1rt_string_view_index", t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return "", nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return "", nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return "ctx_stage1rt_string_index", ref.Elem, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return "ctx_stage1rt_string_view_index", ref.Elem, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_stage1rt_string_view_index", ref.Elem, true
	}
	return "", nil, false
}

func dstrSyntheticFieldType(t semantic.Type, fieldName string) (semantic.Type, bool) {
	if fieldName != "len" {
		return nil, false
	}
	if _, ok := dstrFieldOperandType(t); !ok {
		return nil, false
	}
	return &semantic.BuiltinType{Name: "i64"}, true
}

func dstrFieldOperandType(t semantic.Type) (semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return ref.Elem, true
	}
	return nil, false
}

type runtimeSliceInfo struct {
	helperName  string
	operandType semantic.Type
	resultType  semantic.Type
	indexType   semantic.Type
	useAddress  bool
}

func runtimeSliceOperandInfo(objectType semantic.Type, resultType semantic.Type) (runtimeSliceInfo, bool) {
	i64Type := &semantic.BuiltinType{Name: "i64"}
	usizeType := &semantic.BuiltinType{Name: "usize"}
	if view, ok := objectType.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: &semantic.RefType{Elem: objectType, State: semantic.RefStateNonNull},
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
			useAddress:  true,
		}, true
	}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := objectType.(*semantic.DArrayViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if _, ok := objectType.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := objectType.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := objectType.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	ref, ok := objectType.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return runtimeSliceInfo{}, false
	}
	if view, ok := ref.Elem.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := ref.Elem.(*semantic.ViewType); ok {
		return runtimeSliceInfo{helperName: "arena_da_view_slice", operandType: ref.Elem, resultType: &semantic.ViewType{Elem: view.Elem}, indexType: usizeType}, true
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_stage1rt_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	return runtimeSliceInfo{}, false
}

func runtimeStringCompareInfo(leftType semantic.Type, rightType semantic.Type) (string, semantic.Type, semantic.Type, bool, bool) {
	leftKind := classifyRuntimeStringCompareKind(leftType)
	rightKind := classifyRuntimeStringCompareKind(rightType)
	if leftKind == runtimeStringCompareNone || rightKind == runtimeStringCompareNone {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareRaw && rightKind == runtimeStringCompareRaw {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareView && rightKind == runtimeStringCompareView {
		return "ctx_stage1rt_string_views_eq", leftType, rightType, false, true
	}
	if leftKind == runtimeStringCompareView {
		return "ctx_stage1rt_string_view_eq", leftType, rightType, false, true
	}
	if rightKind == runtimeStringCompareView {
		return "ctx_stage1rt_string_view_eq", rightType, leftType, true, true
	}
	return "ctx_stage1rt_streq", leftType, rightType, false, true
}

type runtimeStringCompareKind int

const (
	runtimeStringCompareNone runtimeStringCompareKind = iota
	runtimeStringCompareDStr
	runtimeStringCompareView
	runtimeStringCompareRaw
)

func classifyRuntimeStringCompareKind(t semantic.Type) runtimeStringCompareKind {
	if t == nil {
		return runtimeStringCompareNone
	}
	if _, ok := t.(*semantic.DStrType); ok {
		return runtimeStringCompareDStr
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeStringCompareView
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return runtimeStringCompareNone
	}
	if builtin, ok := ref.Elem.(*semantic.BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringCompareRaw
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	return runtimeStringCompareNone
}

func (s *functionState) emitCastExpr(expr *ast.CastExpr) (C.LLVMValueRef, semantic.Type, error) {
	targetType, err := s.resolveTypeExpr(expr.Target)
	if err != nil {
		return nil, nil, err
	}
	value, actualType, err := s.emitExpr(expr.Operand, nil)
	if err != nil {
		return nil, nil, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, err
	}
	return coerced, targetType, nil
}

func (s *functionState) emitSizeofExpr(expr *ast.SizeofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	size, err := s.sizeOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(size), 0), s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitTernaryExpr(expr *ast.TernaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	condValue, _, err := s.emitExpr(expr.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.then"))
	elseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.else"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.end"))
	C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	leftValue, _, err := s.emitExpr(expr.Value, resultType)
	if err != nil {
		return nil, nil, err
	}
	thenEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(thenEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
	rightValue, _, err := s.emitExpr(expr.Alt, resultType)
	if err != nil {
		return nil, nil, err
	}
	elseEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(elseEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("termp"))
	values := []C.LLVMValueRef{leftValue, rightValue}
	blocks := []C.LLVMBasicBlockRef{thenEnd, elseEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	_ = parentBlock
	return phi, resultType, nil
}

func (s *functionState) emitAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, operandType, err := s.emitAddress(expr.Operand)
	if err != nil {
		return nil, nil, err
	}
	return ptr, &semantic.RefType{Elem: operandType, State: semantic.RefStateNonNull}, nil
}

func (s *functionState) emitStructLitExpr(expr *ast.StructLitExpr) (C.LLVMValueRef, semantic.Type, error) {
	structType := s.exprType(expr)
	llvmType, err := s.g.lowerType(structType)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(structType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i, arg := range expr.Args {
		if i >= len(fields) {
			break
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	return value, structType, nil
}

func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}

func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}

func (s *functionState) emitEnumConstructorValue(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if len(args) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	enumPtr, err := s.emitStackTempZeroed(enumType, "enum.ctor")
	if err != nil {
		return nil, nil, err
	}
	enumLLVMType, err := s.g.lowerType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("enum.tag.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, tagPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(args[0], variant.Payload[0])
			if err != nil {
				return nil, nil, err
			}
			C.LLVMBuildStore(s.builder, argValue, payloadPtr)
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			for i, payload := range variant.Payload {
				argValue, _, err := s.emitExpr(args[i], payload)
				if err != nil {
					return nil, nil, err
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("enum.payload.ins"))
			}
			C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
		}
	}
	value, err := s.loadValue(enumPtr, enumType, "enum.value")
	if err != nil {
		return nil, nil, err
	}
	return value, enumType, nil
}
