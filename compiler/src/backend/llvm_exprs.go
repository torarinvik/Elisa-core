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
	case *ast.BinaryExpr:
		value, actualType, err = s.emitBinaryExpr(n)
	case *ast.UnaryExpr:
		value, actualType, err = s.emitUnaryExpr(n)
	case *ast.CallExpr:
		value, actualType, err = s.emitCallExpr(n)
	case *ast.FieldExpr:
		value, actualType, err = s.emitFieldExpr(n)
	case *ast.IndexExpr:
		value, actualType, err = s.emitIndexExpr(n)
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
	leftType := s.exprType(expr.Left)
	rightType := s.exprType(expr.Right)
	resultType := s.exprType(expr)
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

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
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
	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	call := C.LLVMBuildCall2(s.builder, llvmFnType, callee, llvmValueSlicePtr(args), C.unsigned(len(args)), cStringFree(callName))
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
	if ptr, fieldType, err := s.emitFieldAddress(expr); err == nil {
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
		return nil, nil, fmt.Errorf("field %s requires an addressable object", expr.Field)
	}
	value := C.LLVMBuildExtractValue(s.builder, objValue, C.unsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}

func (s *functionState) emitIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if stringType, ok := dstrIndexedOperandType(s.exprType(expr.Object)); ok {
		return s.emitRuntimeStringIndexExpr(expr, stringType)
	}
	ptr, elemType, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
}

func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, stringType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, stringType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.result.NamedTypes["i64"]
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_stage1rt_string_index",
		Params: []semantic.Type{stringType, indexType},
		Return: indexType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_stage1rt_string_index", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue, indexValue}
	call := C.LLVMBuildCall2(s.builder, llvmFnType, callee, llvmValueSlicePtr(args), C.unsigned(len(args)), cStringFree("stridx"))
	return call, indexType, nil
}

func dstrIndexedOperandType(t semantic.Type) (semantic.Type, bool) {
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
	value := C.LLVMGetUndef(llvmType)
	st, ok := structType.(*semantic.StructType)
	if !ok {
		return nil, nil, fmt.Errorf("struct literal %s did not resolve to a concrete struct type", expr.Name)
	}
	for i, arg := range expr.Args {
		if i >= len(st.Decl.Fields) {
			break
		}
		fieldDecl := st.Decl.Fields[i]
		field := st.Fields[fieldDecl.Name]
		fieldValue, _, err := s.emitExpr(arg, field.Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	return value, structType, nil
}
