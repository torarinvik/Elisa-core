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
			value, actualType, err = s.emitEnumConstructorValue(enumType, variant, nil, nil)
		} else {
			value, actualType, err = s.emitFieldExpr(n)
		}
	case *ast.RaiseExpr:
		value, actualType, err = s.emitRaiseExpr(n)
	case *ast.TryExpr:
		value, actualType, err = s.emitTryExpr(n)
	case *ast.UnwrapElseExpr:
		value, actualType, err = s.emitUnwrapElseExpr(n)
	case *ast.AllocExpr:
		value, actualType, err = s.emitAllocExpr(n)
	case *ast.MatchExpr:
		value, actualType, err = s.emitMatchExpr(n)
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
	if enumType, ok := operandType.(*semantic.EnumType); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitEnumCompareExpr(expr.Op, enumType, left, right, resultType)
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

func (s *functionState) emitEnumCompareExpr(op lexer.TokenKind, enumType *semantic.EnumType, left C.LLVMValueRef, right C.LLVMValueRef, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil {
		return nil, nil, fmt.Errorf("missing enum type for comparison")
	}
	if enumType.Packed {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.packed")), resultType, nil
	}
	if enumIsTagOnly(enumType) {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.tagonly")), resultType, nil
	}
	leftTag := C.LLVMBuildExtractValue(s.builder, left, 0, cStringFree("enumcmp.left.tag"))
	rightTag := C.LLVMBuildExtractValue(s.builder, right, 0, cStringFree("enumcmp.right.tag"))
	equal := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftTag, rightTag, cStringFree("enumcmp.tag.eq"))

	payloadSlots, err := s.enumPayloadWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	if payloadSlots > 0 {
		leftPayload := C.LLVMBuildExtractValue(s.builder, left, 1, cStringFree("enumcmp.left.payload"))
		rightPayload := C.LLVMBuildExtractValue(s.builder, right, 1, cStringFree("enumcmp.right.payload"))
		for i := uint64(0); i < payloadSlots; i++ {
			nameSuffix := fmt.Sprintf(".%d", i)
			leftWord := C.LLVMBuildExtractValue(s.builder, leftPayload, C.unsigned(i), cStringFree("enumcmp.left.word"+nameSuffix))
			rightWord := C.LLVMBuildExtractValue(s.builder, rightPayload, C.unsigned(i), cStringFree("enumcmp.right.word"+nameSuffix))
			wordEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftWord, rightWord, cStringFree("enumcmp.word.eq"+nameSuffix))
			equal = C.LLVMBuildAnd(s.builder, equal, wordEqual, cStringFree("enumcmp.and"+nameSuffix))
		}
	}
	if op == lexer.TOKEN_BANGEQ {
		return C.LLVMBuildNot(s.builder, equal, cStringFree("enumcmp.ne")), resultType, nil
	}
	return equal, resultType, nil
}

func (s *functionState) encodePackedEnumHandle(rowPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.encodePackedEnumHandleWithStore(rowPtr, enumType, nil)
}

func (s *functionState) encodePackedEnumHandleWithStore(rowPtr C.LLVMValueRef, enumType *semantic.EnumType, storeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	switch s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		return rowPtr, nil
	case packedEnumABIWordHandle:
		storeType := enumType.StoreType
		if storeType == nil {
			return nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
		}
		arenaValue, err := s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.encode.store.arena")
		if err != nil {
			return nil, err
		}
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_encode", Params: []semantic.Type{arenaRefType, voidRefType}, Return: s.g.result.NamedTypes["uintptr"]}
		callee, err := s.g.ensureFunctionDeclared("ctx_packed_store_encode", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		encoded := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, rowPtr}, "packed.handle.encode")
		return s.coerceValue(encoded, s.g.result.NamedTypes["uintptr"], enumType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedEnumABI)
	}
}

func (s *functionState) decodePackedEnumHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.decodePackedEnumHandleWithStore(handleValue, enumType, nil)
}

func (s *functionState) decodePackedEnumHandleWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	switch s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		return handleValue, nil
	case packedEnumABIWordHandle:
		if store == nil || store.typ == nil {
			return nil, fmt.Errorf("packed enum %s word-handle decode requires store context", enumType.Name)
		}
		arenaValue, err := s.emitPackedStoreArenaValueNamed(store.value, store.typ, "packed.decode.store.arena")
		if err != nil {
			return nil, err
		}
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_decode", Params: []semantic.Type{arenaRefType, s.g.result.NamedTypes["uintptr"]}, Return: voidRefType}
		callee, err := s.g.ensureFunctionDeclared("ctx_packed_store_decode", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := s.coerceValue(handleValue, enumType, s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle}, "packed.handle.decode"), nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedEnumABI)
	}
}

func (s *functionState) enumPayloadWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	maxSlots := uint64(0)
	for _, variant := range enumType.Variants {
		slots, err := s.g.enumVariantPayloadSlots(variant)
		if err != nil {
			return 0, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	return maxSlots, nil
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
	if helperName == "ctx_stage1rt_string_view_eq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitStringViewStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("strcmp.lit.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
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

func (s *functionState) emitSpecializedRuntimeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if value, actualType, handled, err := s.emitSpecializedStringViewLiteralCall(expr); handled {
		return value, actualType, true, err
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "ctx_stage0_string_view_eq" && ident.Name != "ctx_stage1rt_string_view_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, false, nil
	}
	firstType := s.exprType(expr.Args[0])
	if classifyRuntimeStringCompareKind(firstType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[0], firstType, expr.Args[1], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) emitSpecializedStringViewLiteralCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	viewArgIndex, literalArgIndex, returnsInt, ok := s.specializedStringViewLiteralCallShape(ident.Name)
	if !ok {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[literalArgIndex])
	if !ok {
		return nil, nil, false, nil
	}
	viewType := s.exprType(expr.Args[viewArgIndex])
	if classifyRuntimeStringCompareKind(viewType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[viewArgIndex], viewType, expr.Args[literalArgIndex], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	if !returnsInt {
		return cmp, s.exprType(expr), true, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) specializedStringViewLiteralCallShape(funcName string) (int, int, bool, bool) {
	if funcName == "ctx_stage0_string_view_eq" || funcName == "ctx_stage1rt_string_view_eq" {
		return 0, 1, true, true
	}
	sym, ok := s.g.result.GlobalScope.Lookup(funcName)
	if !ok {
		return 0, 0, false, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || len(decl.Params) != 2 || len(decl.Body) != 1 {
		return 0, 0, false, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return 0, 0, false, false
	}
	callExpr, ok := s.boolStringViewLiteralWrapperCall(ret.Value, decl.Params[0].Name, decl.Params[1].Name)
	if !ok || callExpr == nil {
		return 0, 0, false, false
	}
	callee, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		return 0, 0, false, false
	}
	if callee.Name != "ctx_stage0_string_view_eq" && callee.Name != "ctx_stage1rt_string_view_eq" {
		return 0, 0, false, false
	}
	return 0, 1, false, true
}

func (s *functionState) boolStringViewLiteralWrapperCall(expr ast.Expr, viewParam string, literalParam string) (*ast.CallExpr, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	callExpr, intLit, ok := unwrapCallComparedToZero(binary.Left, binary.Right)
	if !ok {
		callExpr, intLit, ok = unwrapCallComparedToZero(binary.Right, binary.Left)
	}
	if !ok || intLit == nil || intLit.Value != "0" {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	if !matchesStringViewLiteralWrapperArgs(callExpr, viewParam, literalParam) {
		return nil, false
	}
	return callExpr, true
}

func unwrapCallComparedToZero(left ast.Expr, right ast.Expr) (*ast.CallExpr, *ast.IntLit, bool) {
	callExpr, ok := left.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	intLit, ok := right.(*ast.IntLit)
	if !ok {
		return nil, nil, false
	}
	return callExpr, intLit, true
}

func matchesStringViewLiteralWrapperArgs(callExpr *ast.CallExpr, viewParam string, literalParam string) bool {
	if callExpr == nil || len(callExpr.Args) != 2 {
		return false
	}
	viewIdent, ok := callExpr.Args[0].(*ast.Ident)
	if !ok || viewIdent.Name != viewParam {
		return false
	}
	return exprIsParamOrCastOfParam(callExpr.Args[1], literalParam)
}

func exprIsParamOrCastOfParam(expr ast.Expr, paramName string) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name == paramName
	case *ast.ParenExpr:
		return exprIsParamOrCastOfParam(n.Inner, paramName)
	case *ast.CastExpr:
		return exprIsParamOrCastOfParam(n.Operand, paramName)
	default:
		return false
	}
}

func (s *functionState) emitStringViewStaticLiteralEqual(viewExpr ast.Expr, viewType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svlit.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svlit.len"))
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), viewLen, lenValue, cStringFree("svlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(viewData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(viewData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("svlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitStringViewLiteralBytesEqual(viewData C.LLVMValueRef, literalText string) (C.LLVMValueRef, error) {
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	byteType := C.LLVMInt8TypeInContext(s.g.context)
	indexType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	result := C.LLVMConstInt(boolType, 1, 0)
	for i, b := range []byte(literalText) {
		indexValue := C.LLVMConstInt(indexType, C.ulonglong(i), 0)
		indices := []C.LLVMValueRef{indexValue}
		bytePtr := C.LLVMBuildGEP2(s.builder, byteType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("svlit.byte.ptr"))
		byteValue := C.LLVMBuildLoad2(s.builder, byteType, bytePtr, cStringFree("svlit.byte"))
		literalValue := C.LLVMConstInt(byteType, C.ulonglong(b), 0)
		byteEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteValue, literalValue, cStringFree("svlit.byte.eq"))
		result = C.LLVMBuildAnd(s.builder, result, byteEqual, cStringFree("svlit.bytes.and"))
	}
	return result, nil
}

func (s *functionState) emitMemcmpEqual(left C.LLVMValueRef, right C.LLVMValueRef, length int) (C.LLVMValueRef, error) {
	voidType := s.g.result.NamedTypes["void"]
	usizeType := s.g.result.NamedTypes["usize"]
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{
		Name:   "memcmp",
		Params: []semantic.Type{voidRefType, voidRefType, usizeType},
		Return: intType,
	}
	callee, err := s.g.ensureFunctionDeclared("memcmp", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	lengthValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(length), 0)
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{left, right, lengthValue}, "svlit.memcmp")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(intLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), call, zero, cStringFree("svlit.memcmp.eq")), nil
}

func (s *functionState) staticCStringLiteral(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		return n.Value, true
	case *ast.ParenExpr:
		return s.staticCStringLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticCStringLiteral(n.Operand)
	case *ast.Ident:
		value, ok := s.g.constValue(n.Name)
		if !ok || value.Kind != semantic.ConstString {
			return "", false
		}
		return value.String, true
	default:
		return "", false
	}
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

func (s *functionState) emitAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Owner == nil {
		return s.emitScopedPackedAllocExpr(expr)
	}
	if _, ok := s.exprType(expr.Owner).(*semantic.PackedEnumStoreType); ok {
		return s.emitPackedAllocExpr(expr)
	}
	ownerIdent, ok := expr.Owner.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("only region-backed new[...] is lowered so far")
	}
	binding, ok := s.lookupBinding(ownerIdent.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value in %q", ownerIdent.Name)
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

func (s *functionState) emitScopedPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
	}
}

func (s *functionState) emitPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	storeValue, _, err := s.emitExpr(expr.Owner, nil)
	if err != nil {
		return nil, nil, err
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		enumType, variant, ok := s.enumConstructorInfoFromField(fieldExpr)
		if ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			return s.emitPackedEnumConstructorAlloc(storeValue, enumType, variant, nil, nil)
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("packed enum allocation expects a constructor call")
	}
	enumType, variant, ok := s.enumConstructorInfo(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum allocation expects a packed enum constructor call")
	}
	return s.emitPackedEnumConstructorAlloc(storeValue, enumType, variant, callExpr.Args, callExpr.ArgNames)
}

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if storeType, ok := s.packedStoreConstructorCall(expr); ok {
		return s.emitPackedStoreConstructorValue(expr, storeType)
	}
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		return s.emitEnumConstructorValue(enumType, variant, expr.Args, expr.ArgNames)
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeCall(expr); handled {
		return value, actualType, err
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

func (s *functionState) emitPackedStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("packed store constructor expects exactly one arena argument")
	}
	value, err := s.emitPackedStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitPackedStoreValue(arenaExpr ast.Expr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	switch s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		arenaPtr, _, err := s.emitAddressOrTemp(arenaExpr)
		if err != nil {
			return nil, err
		}
		storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
		if err != nil {
			return nil, err
		}
		storeValue := C.LLVMGetUndef(storeLLVMType)
		storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, arenaPtr, 0, cStringFree("packed.store.arena"))
		return storeValue, nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedEnumABI)
	}
}

func (s *functionState) emitPackedStoreArenaValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.store.arena.value")
}

func (s *functionState) emitPackedStoreArenaValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	switch s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		_, err := s.g.lowerPackedEnumStoreType(storeType)
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildExtractValue(s.builder, storeValue, 0, cStringFree(name)), nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedEnumABI)
	}
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
			return s.emitEnumConstructorValue(enumType, variant, nil, nil)
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

func (s *functionState) packedStoreConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.PackedEnumStoreType, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" || enumType.StoreType == nil {
		return nil, false
	}
	return enumType.StoreType, true
}

func (s *functionState) packedStoreConstructorCall(expr *ast.CallExpr) (*semantic.PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.packedStoreConstructorInfoFromField(fieldExpr)
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

func (s *functionState) emitEnumConstructorValue(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s must be allocated with new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name)
	}
	orderedArgs, err := s.resolveEnumConstructorArgs(enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	if enumIsTagOnly(enumType) {
		tagValue, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, nil, err
		}
		return tagValue, enumType, nil
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
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
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
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
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

func (s *functionState) emitPackedEnumConstructorAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	orderedArgs, commonArgs, err := s.resolvePackedEnumConstructorArgs(enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr, enumValue, err := s.emitPackedEnumStorageAlloc(storeValue, enumType)
	if err != nil {
		return nil, nil, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstNull(rowType), allocPtr)
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, rowType, allocPtr, 0, cStringFree("packed.enum.tag.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, tagPtr)
	for i, commonDecl := range enumType.Decl.Common {
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		field, ok := enumType.Common[commonDecl.Name]
		if !ok {
			return nil, nil, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, commonDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(arg, field.Type)
		if err != nil {
			return nil, nil, err
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, rowType, allocPtr, C.unsigned(1+i), cStringFree("packed.enum.common.ptr"))
		C.LLVMBuildStore(s.builder, fieldValue, fieldPtr)
	}
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(allocPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
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
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
				if err != nil {
					return nil, nil, err
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("packed.enum.payload.ins"))
			}
			C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
		}
	}
	return enumValue, enumType, nil
}

func (s *functionState) emitPackedEnumStorageAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	storageType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	switch s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		storeType := enumType.StoreType
		if storeType == nil {
			return nil, nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
		}
		arenaValue, err := s.emitPackedStoreArenaValue(storeValue, storeType)
		if err != nil {
			return nil, nil, err
		}
		sizeBytes, err := s.g.abiSizeOfLLVMType(storageType)
		if err != nil {
			return nil, nil, err
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, err
		}
		sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
		callee, err := s.g.ensureFunctionDeclared("arena_alloc", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		allocPtr := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, sizeValue}, "packed.alloc")
		return allocPtr, allocPtr, nil
	case packedEnumABIWordHandle:
		storeType := enumType.StoreType
		if storeType == nil {
			return nil, nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
		}
		arenaValue, err := s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.alloc.store.arena")
		if err != nil {
			return nil, nil, err
		}
		sizeBytes, err := s.g.abiSizeOfLLVMType(storageType)
		if err != nil {
			return nil, nil, err
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, err
		}
		sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		uintptrType := s.g.result.NamedTypes["uintptr"]
		allocHelperType := &semantic.FuncType{Name: "ctx_packed_store_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: uintptrType}
		allocCallee, err := s.g.ensureFunctionDeclared("ctx_packed_store_alloc", allocHelperType)
		if err != nil {
			return nil, nil, err
		}
		allocLLVMFnType, err := s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, err
		}
		handleValue := s.buildCall(allocLLVMFnType, allocCallee, []C.LLVMValueRef{arenaValue, sizeValue}, "packed.handle.alloc")
		allocPtr, err := s.decodePackedEnumHandleWithStore(handleValue, enumType, &packedStoreBinding{typ: storeType, value: storeValue})
		if err != nil {
			return nil, nil, err
		}
		coercedHandle, err := s.coerceValue(handleValue, uintptrType, enumType)
		if err != nil {
			return nil, nil, err
		}
		return allocPtr, coercedHandle, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedEnumABI)
	}
}

func (s *functionState) resolveEnumConstructorArgs(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, error) {
	if variant == nil {
		return nil, fmt.Errorf("missing enum constructor metadata")
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		return args, nil
	}
	if namedCount != len(args) {
		return nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("enum constructor %s.%s does not declare named payload fields", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		index, ok := variant.PayloadIndex(name)
		if !ok {
			return nil, fmt.Errorf("enum constructor %s.%s has no payload field %q", enumType.Name, variant.Name, name)
		}
		if seen[index] {
			return nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	return ordered, nil
}

func (s *functionState) resolvePackedEnumConstructorArgs(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := enumType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s common field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s has no payload or common field %q", enumType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	return ordered, commonArgs, nil
}
