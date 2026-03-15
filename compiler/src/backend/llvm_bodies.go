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

type valueBinding struct {
	ptr C.LLVMValueRef
	typ semantic.Type
}

type codegenScope struct {
	parent   *codegenScope
	bindings map[string]valueBinding
}

type functionState struct {
	g       *llvmGenerator
	decl    *ast.FuncDecl
	fnValue C.LLVMValueRef
	fnType  *semantic.FuncType
	builder C.LLVMBuilderRef
	scope   *codegenScope
}

func (g *llvmGenerator) defineFunctionBody(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef) error {
	if decl == nil || fnType == nil || fnValue == nil {
		return fmt.Errorf("cannot define function body without declaration, type, and value")
	}
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)

	entryName := cString("entry")
	defer C.free(unsafe.Pointer(entryName))
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, entryName)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	state := &functionState{
		g:       g,
		decl:    decl,
		fnValue: fnValue,
		fnType:  fnType,
		builder: builder,
		scope:   &codegenScope{bindings: map[string]valueBinding{}},
	}

	for i, param := range decl.Params {
		if i >= len(fnType.Params) {
			break
		}
		alloca, err := state.createEntryAlloca(param.Name, fnType.Params[i])
		if err != nil {
			return err
		}
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(i))
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(param.Name, valueBinding{ptr: alloca, typ: fnType.Params[i]})
	}

	if err := state.emitBlock(decl.Body, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		if isVoidType(fnType.Return) {
			C.LLVMBuildRetVoid(builder)
		} else {
			return fmt.Errorf("function %s may fall through without returning a value", decl.Name)
		}
	}

	return nil
}

func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	if scoped {
		s.pushScope()
		defer s.popScope()
	}
	for _, stmt := range stmts {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitStmt(stmt ast.Stmt) error {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		declType, err := s.resolveTypeExpr(n.Type)
		if err != nil {
			return err
		}
		alloca, err := s.createEntryAlloca(n.Name, declType)
		if err != nil {
			return err
		}
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType})
		if n.Value != nil {
			value, _, err := s.emitExpr(n.Value, declType)
			if err != nil {
				return err
			}
			C.LLVMBuildStore(s.builder, value, alloca)
		}
		return nil
	case *ast.AssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		return nil
	case *ast.AugAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		current, err := s.loadValue(ptr, targetType, "aug.cur")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		result, err := s.emitAugmentedValue(n.Op, current, value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, result, ptr)
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			C.LLVMBuildRetVoid(s.builder)
			return nil
		}
		value, _, err := s.emitExpr(n.Value, s.fnType.Return)
		if err != nil {
			return err
		}
		C.LLVMBuildRet(s.builder, value)
		return nil
	case *ast.IfStmt:
		return s.emitIf(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.PassStmt:
		return nil
	case *ast.PanicStmt:
		if n.Message != nil {
			if _, _, err := s.emitExpr(n.Message, nil); err != nil {
				return err
			}
		}
		trapFn, err := s.ensureTrapFunction()
		if err != nil {
			return err
		}
		trapType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
		if err != nil {
			return err
		}
		callName := cString("")
		defer C.free(unsafe.Pointer(callName))
		C.LLVMBuildCall2(s.builder, trapType, trapFn, nil, 0, callName)
		C.LLVMBuildUnreachable(s.builder)
		return nil
	case *ast.ExprStmt:
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return fmt.Errorf("static if lowering inside function bodies is not implemented yet")
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}

func (s *functionState) emitIf(stmt *ast.IfStmt) error {
	stmt = normalizeIf(stmt)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
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
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)
	} else {
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
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
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) emitExpr(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("cannot emit nil expression")
	}

	actualType := s.g.exprType(expr)
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
			global, err := s.g.ensureGlobalDeclared(expr.Name, sym.Type)
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
	t := s.g.exprType(expr)
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
	return value, s.g.exprType(expr), nil
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
	leftType := s.g.exprType(expr.Left)
	rightType := s.g.exprType(expr.Right)
	resultType := s.g.exprType(expr)
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
	operandType := s.g.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.exprType(expr)
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
	funcType, ok := s.g.exprType(expr.Func).(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	callee, err := s.emitCallee(expr.Func, funcType)
	if err != nil {
		return nil, nil, err
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

func (s *functionState) emitCallee(expr ast.Expr, funcType *semantic.FuncType) (C.LLVMValueRef, error) {
	if ident, ok := expr.(*ast.Ident); ok {
		if sym, ok := s.g.result.GlobalScope.Lookup(ident.Name); ok {
			if sym.Kind == semantic.SymbolFunc || sym.Kind == semantic.SymbolExternFunc {
				return s.g.ensureFunctionDeclared(ident.Name, funcType)
			}
		}
	}
	callee, _, err := s.emitExpr(expr, nil)
	return callee, err
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
	ptr, elemType, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
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
	resultType := s.g.exprType(expr)
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
	structType := s.g.exprType(expr)
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

func (s *functionState) emitAddress(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			return binding.ptr, binding.typ, nil
		}
		if sym, ok := s.g.result.GlobalScope.Lookup(n.Name); ok {
			if sym.Kind == semantic.SymbolGlobal || sym.Kind == semantic.SymbolExternVar {
				global, err := s.g.ensureGlobalDeclared(n.Name, sym.Type)
				return global, sym.Type, err
			}
		}
		return nil, nil, fmt.Errorf("identifier %s is not addressable", n.Name)
	case *ast.FieldExpr:
		return s.emitFieldAddress(n)
	case *ast.IndexExpr:
		return s.emitIndexAddress(n)
	case *ast.ParenExpr:
		return s.emitAddress(n.Inner)
	default:
		return nil, nil, fmt.Errorf("expression %T is not addressable", expr)
	}
}

func (s *functionState) emitFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	objType := s.g.exprType(expr.Object)
	fieldType, index, containerType, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	var objPtr C.LLVMValueRef
	if pointerLike {
		objPtr, _, err = s.emitExpr(expr.Object, nil)
	} else {
		objPtr, _, err = s.emitAddress(expr.Object)
	}
	if err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, objPtr, C.unsigned(index), cStringFree(expr.Field))
	return fieldPtr, fieldType, nil
}

func (s *functionState) emitIndexAddress(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	objType := s.g.exprType(expr.Object)
	indexValue, _, err := s.emitExpr(expr.Index, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	switch t := objType.(type) {
	case *semantic.ArrayType:
		arrayPtr, _, err := s.emitAddress(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		arrayLLVMType, err := s.g.lowerType(t)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{zero, indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
		return ptr, t.Elem, nil
	case *semantic.RefType:
		basePtr, _, err := s.emitExpr(expr.Object, nil)
		if err != nil {
			return nil, nil, err
		}
		if arrayElem, ok := t.Elem.(*semantic.ArrayType); ok {
			arrayLLVMType, err := s.g.lowerType(arrayElem)
			if err != nil {
				return nil, nil, err
			}
			indices := []C.LLVMValueRef{zero, indexValue}
			ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
			return ptr, arrayElem.Elem, nil
		}
		elemLLVMType, err := s.g.lowerType(t.Elem)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
		return ptr, t.Elem, nil
	default:
		return nil, nil, fmt.Errorf("indexing is not implemented for %s", objType.String())
	}
}

func (s *functionState) loadValue(ptr C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, llvmType, ptr, cStringFree(name)), nil
}

func (s *functionState) coerceValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	if expected == nil || actual == nil || semantic.SameType(actual, expected) {
		return value, nil
	}
	actualLLVM, err := s.g.lowerType(actual)
	if err != nil {
		return nil, err
	}
	expectedLLVM, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	if actualLLVM == expectedLLVM {
		return value, nil
	}
	if isPointerLikeType(actual) && isPointerLikeType(expected) {
		return value, nil
	}
	if semantic.IsNullType(actual) && isPointerLikeType(expected) {
		return C.LLVMConstNull(expectedLLVM), nil
	}
	if isNumericType(actual) && isNumericType(expected) {
		return s.coerceNumericValue(value, actual, expected)
	}
	if isPointerLikeType(actual) && isNumericType(expected) {
		return C.LLVMBuildPtrToInt(s.builder, value, expectedLLVM, cStringFree("ptrtoint")), nil
	}
	if isNumericType(actual) && isPointerLikeType(expected) {
		return C.LLVMBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("inttoptr")), nil
	}
	return value, nil
}

func (s *functionState) coerceNumericValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	actualBits := integerBitWidth(actual, s.g.wordBits)
	expectedBits := integerBitWidth(expected, s.g.wordBits)
	expectedLLVM, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	switch {
	case actualBits == expectedBits:
		return value, nil
	case actualBits < expectedBits:
		if isSignedIntegerType(actual) {
			return C.LLVMBuildSExt(s.builder, value, expectedLLVM, cStringFree("sext")), nil
		}
		return C.LLVMBuildZExt(s.builder, value, expectedLLVM, cStringFree("zext")), nil
	default:
		return C.LLVMBuildTrunc(s.builder, value, expectedLLVM, cStringFree("trunc")), nil
	}
}

func (s *functionState) binaryOperandType(op lexer.TokenKind, left semantic.Type, right semantic.Type) semantic.Type {
	switch op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		return s.g.result.NamedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if isNumericType(left) && isNumericType(right) {
			return semantic.CommonNumericType(left, right)
		}
		if semantic.IsNullType(left) {
			return right
		}
		if semantic.IsNullType(right) {
			return left
		}
		return left
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ,
		lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
		lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if isNumericType(left) && isNumericType(right) {
			return semantic.CommonNumericType(left, right)
		}
	}
	return left
}

func (s *functionState) emitAugmentedValue(op lexer.TokenKind, left C.LLVMValueRef, right C.LLVMValueRef, t semantic.Type) (C.LLVMValueRef, error) {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("pluseq")), nil
	case lexer.TOKEN_MINUSEQ:
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("minuseq")), nil
	case lexer.TOKEN_STAREQ:
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("stareq")), nil
	case lexer.TOKEN_SLASHEQ:
		if isSignedIntegerType(t) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("slasheq")), nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("slasheq")), nil
	case lexer.TOKEN_CARETEQ:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("careteq")), nil
	case lexer.TOKEN_PIPEEQ:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("pipeeq")), nil
	case lexer.TOKEN_AMPEQ:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("ampeq")), nil
	case lexer.TOKEN_LSHIFTEQ:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("lshifteq")), nil
	case lexer.TOKEN_RSHIFTEQ:
		if isSignedIntegerType(t) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("rshifteq")), nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("rshifteq")), nil
	default:
		return nil, fmt.Errorf("unsupported augmented assignment operator %s", lexer.TokenName(op))
	}
}

func (s *functionState) createEntryAlloca(name string, t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMGetEntryBasicBlock(s.fnValue)
	first := C.LLVMGetFirstInstruction(entry)
	if first != nil {
		C.LLVMPositionBuilderBefore(builder, first)
	} else {
		C.LLVMPositionBuilderAtEnd(builder, entry)
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.LLVMBuildAlloca(builder, llvmType, nameC), nil
}

func (s *functionState) currentBlockTerminated() bool {
	block := C.LLVMGetInsertBlock(s.builder)
	if block == nil {
		return true
	}
	return C.LLVMGetBasicBlockTerminator(block) != nil
}

func (s *functionState) pushScope() {
	s.scope = &codegenScope{parent: s.scope, bindings: map[string]valueBinding{}}
}

func (s *functionState) popScope() {
	if s.scope != nil {
		s.scope = s.scope.parent
	}
}

func (s *functionState) defineBinding(name string, binding valueBinding) {
	if s.scope == nil {
		s.scope = &codegenScope{bindings: map[string]valueBinding{}}
	}
	s.scope.bindings[name] = binding
}

func (s *functionState) lookupBinding(name string) (valueBinding, bool) {
	for scope := s.scope; scope != nil; scope = scope.parent {
		if binding, ok := scope.bindings[name]; ok {
			return binding, true
		}
	}
	return valueBinding{}, false
}

func (s *functionState) emitConstValue(value semantic.ConstValue) (C.LLVMValueRef, semantic.Type, error) {
	switch value.Kind {
	case semantic.ConstInt:
		llvmType, err := s.g.lowerBuiltin("int")
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value.Int), 1), s.g.result.NamedTypes["int"], nil
	case semantic.ConstBool:
		llvmType, err := s.g.lowerBuiltin("bool")
		if err != nil {
			return nil, nil, err
		}
		var raw C.ulonglong
		if value.Bool {
			raw = 1
		}
		return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
	case semantic.ConstString:
		name := cString("cstr")
		defer C.free(unsafe.Pointer(name))
		text := cString(value.String)
		defer C.free(unsafe.Pointer(text))
		return C.LLVMBuildGlobalStringPtr(s.builder, text, name), &semantic.RefType{Elem: s.g.result.NamedTypes["u8"], State: semantic.RefStateNonNull}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported const kind %d", value.Kind)
	}
}

func (s *functionState) zeroValue(t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstNull(llvmType), nil
}

func (s *functionState) ensureTrapFunction() (C.LLVMValueRef, error) {
	if value, ok := s.g.functions["llvm.trap"]; ok {
		return value, nil
	}
	voidType := s.g.result.NamedTypes["void"]
	trapType := &semantic.FuncType{Name: "llvm.trap", Return: voidType}
	return s.g.ensureFunctionDeclared("llvm.trap", trapType)
}

func (s *functionState) resolveTypeExpr(expr ast.TypeExpr) (semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.NamedType:
		if t, ok := s.g.result.NamedTypes[n.Name]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("unknown type %q", n.Name)
	case *ast.RefType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefState(n.State)}, nil
	case *ast.ArrayType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		size, err := s.evalConstIntExpr(n.Size)
		if err != nil {
			return nil, err
		}
		return &semantic.ArrayType{Elem: elem, Size: fmt.Sprintf("%d", size), HasConstSize: true, ConstSize: size}, nil
	case *ast.MutableType:
		return s.resolveTypeExpr(n.Elem)
	case *ast.TailType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefStateNonNull}, nil
	case *ast.GenericType:
		if t, ok, err := s.resolveDynamicShapeType(n); ok || err != nil {
			return t, err
		}
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, fmt.Errorf("unknown type %q", n.Name)
		}
		args := make([]semantic.Type, 0, len(n.Args))
		for _, arg := range n.Args {
			resolved, err := s.resolveTypeExpr(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, resolved)
		}
		return &semantic.GenericInstanceType{Name: n.Name, Base: base, Args: args}, nil
	default:
		return nil, fmt.Errorf("unsupported type expression %T", expr)
	}
}

func (s *functionState) resolveDynamicShapeType(expr *ast.GenericType) (semantic.Type, bool, error) {
	switch expr.Name {
	case "DArray":
		if len(expr.Args) != 2 {
			return nil, true, fmt.Errorf("DArray expects 2 arguments, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayType{Elem: elem, Shape: shapeFromTypeExpr(expr.Args[1])}, true, nil
	case "DArrayView":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("DArrayView expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayViewType{Elem: elem}, true, nil
	case "DList":
		if len(expr.Args) != 2 {
			return nil, true, fmt.Errorf("DList expects 2 arguments, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DListType{Elem: elem, Shape: shapeFromTypeExpr(expr.Args[1])}, true, nil
	case "DListView":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("DListView expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DListViewType{Elem: elem}, true, nil
	case "DStr":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("DStr expects 1 argument, got %d", len(expr.Args))
		}
		return &semantic.DStrType{Shape: shapeFromTypeExpr(expr.Args[0])}, true, nil
	default:
		return nil, false, nil
	}
}

func (g *llvmGenerator) fieldInfo(objType semantic.Type, fieldName string) (semantic.Type, int, semantic.Type, bool, error) {
	pointerLike := false
	if ref, ok := objType.(*semantic.RefType); ok {
		pointerLike = true
		objType = ref.Elem
	}
	if _, ok := objType.(*semantic.DListType); ok {
		pointerLike = true
	}
	if runtimeBacked := g.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *semantic.StructType:
		index, field, err := fieldInfoFromStruct(t, fieldName)
		return field.Type, index, t, pointerLike, err
	case *semantic.GenericInstanceType:
		base, ok := t.Base.(*semantic.StructType)
		if !ok {
			return nil, 0, nil, false, fmt.Errorf("field access requires a struct-backed type")
		}
		index, field, err := fieldInfoFromStruct(base, fieldName)
		if err != nil {
			return nil, 0, nil, false, err
		}
		subst := make(map[string]semantic.Type, len(base.TypeParams))
		for i, param := range base.TypeParams {
			if i < len(t.Args) {
				subst[param] = t.Args[i]
			}
		}
		return substituteType(field.Type, subst), index, t, pointerLike, nil
	default:
		return nil, 0, nil, false, fmt.Errorf("field access requires a struct type, got %s", objType.String())
	}
}

func fieldInfoFromStruct(st *semantic.StructType, fieldName string) (int, semantic.Field, error) {
	if st == nil || st.Decl == nil {
		return 0, semantic.Field{}, fmt.Errorf("struct metadata is unavailable")
	}
	for i, fieldDecl := range st.Decl.Fields {
		if fieldDecl.Name == fieldName {
			field, ok := st.Fields[fieldName]
			if !ok {
				return 0, semantic.Field{}, fmt.Errorf("missing field %s.%s", st.Name, fieldName)
			}
			return i, field, nil
		}
	}
	return 0, semantic.Field{}, fmt.Errorf("struct %s has no field %s", st.Name, fieldName)
}

func (g *llvmGenerator) runtimeBackedStructType(t semantic.Type) semantic.Type {
	if _, ok := t.(*semantic.DListType); ok {
		if base, ok := g.result.NamedTypes["CtxList"]; ok {
			return base
		}
		return nil
	}
	if _, ok := t.(*semantic.DListViewType); ok {
		if base, ok := g.result.NamedTypes["CtxListView"]; ok {
			return base
		}
		return nil
	}
	if _, ok := t.(*semantic.DArrayViewType); ok {
		if base, ok := g.result.NamedTypes["DynArrayView"]; ok {
			return base
		}
		return nil
	}
	if darray, ok := t.(*semantic.DArrayType); ok {
		base, ok := g.result.NamedTypes["DynArray"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynArray", Base: base, Args: []semantic.Type{darray.Elem}}
	}
	return nil
}

func (s *functionState) sizeOfType(t semantic.Type) (uint64, error) {
	switch tt := t.(type) {
	case *semantic.BuiltinType:
		switch tt.Name {
		case "bool", "i8", "u8":
			return 1, nil
		case "i16", "u16":
			return 2, nil
		case "i32", "u32":
			return 4, nil
		case "void":
			return 0, nil
		case "i64", "u64", "int", "isize", "usize", "uintptr":
			return uint64(s.g.wordBits / 8), nil
		default:
			return 0, fmt.Errorf("unsupported builtin type %q", tt.Name)
		}
	case *semantic.RefType, *semantic.NullType, *semantic.DStrType, *semantic.DListType, *semantic.FuncType:
		return uint64(s.g.wordBits / 8), nil
	case *semantic.ArrayType:
		elemSize, err := s.sizeOfType(tt.Elem)
		if err != nil {
			return 0, err
		}
		if !tt.HasConstSize {
			return 0, fmt.Errorf("array %s is missing a compile-time size", tt.String())
		}
		return elemSize * uint64(tt.ConstSize), nil
	case *semantic.StructType:
		if tt.Decl == nil {
			return 0, fmt.Errorf("struct %s is missing declaration metadata", tt.Name)
		}
		var total uint64
		for _, fieldDecl := range tt.Decl.Fields {
			field := tt.Fields[fieldDecl.Name]
			sz, err := s.sizeOfType(field.Type)
			if err != nil {
				return 0, err
			}
			total += sz
		}
		return total, nil
	case *semantic.GenericInstanceType:
		llvmType, err := s.g.lowerType(tt)
		if err != nil {
			return 0, err
		}
		fieldCount := int(C.LLVMCountStructElementTypes(llvmType))
		if fieldCount == 0 {
			return 0, nil
		}
		fields := make([]C.LLVMTypeRef, fieldCount)
		C.LLVMGetStructElementTypes(llvmType, llvmTypeSlicePtr(fields))
		var total uint64
		for _, fieldType := range fields {
			total += llvmTypeSize(fieldType, s.g.wordBits)
		}
		return total, nil
	case *semantic.DArrayType:
		return uint64((s.g.wordBits / 8) * 3), nil
	case *semantic.DArrayViewType, *semantic.DListViewType:
		return uint64((s.g.wordBits / 8) * 2), nil
	default:
		return 0, fmt.Errorf("sizeof is not implemented for %T", t)
	}
}

func shapeFromTypeExpr(expr ast.TypeExpr) semantic.Shape {
	if named, ok := expr.(*ast.NamedType); ok {
		return &semantic.NamedShape{Name: named.Name}
	}
	return &semantic.NamedShape{Name: "?"}
}

func normalizeIf(stmt *ast.IfStmt) *ast.IfStmt {
	if stmt == nil || len(stmt.Elifs) == 0 {
		return stmt
	}
	elseBody := stmt.Else
	for i := len(stmt.Elifs) - 1; i >= 0; i-- {
		elif := stmt.Elifs[i]
		elseBody = []ast.Stmt{&ast.IfStmt{Position: elif.Position, Cond: elif.Cond, Then: elif.Body, Else: elseBody}}
	}
	return &ast.IfStmt{Position: stmt.Position, Cond: stmt.Cond, Then: stmt.Then, Else: elseBody}
}

func (s *functionState) evalConstIntExpr(expr ast.Expr) (int64, error) {
	switch n := expr.(type) {
	case *ast.IntLit:
		return strconv.ParseInt(n.Value, 0, 64)
	case *ast.Ident:
		if value, ok := s.g.constValue(n.Name); ok && value.Kind == semantic.ConstInt {
			return value.Int, nil
		}
		return 0, fmt.Errorf("identifier %q is not a compile-time integer constant", n.Name)
	case *ast.ParenExpr:
		return s.evalConstIntExpr(n.Inner)
	case *ast.UnaryExpr:
		value, err := s.evalConstIntExpr(n.Operand)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case lexer.TOKEN_MINUS:
			return -value, nil
		case lexer.TOKEN_TILDE:
			return ^value, nil
		default:
			return 0, fmt.Errorf("unsupported compile-time unary operator %s", lexer.TokenName(n.Op))
		}
	case *ast.BinaryExpr:
		left, err := s.evalConstIntExpr(n.Left)
		if err != nil {
			return 0, err
		}
		right, err := s.evalConstIntExpr(n.Right)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return left + right, nil
		case lexer.TOKEN_MINUS:
			return left - right, nil
		case lexer.TOKEN_STAR:
			return left * right, nil
		case lexer.TOKEN_SLASH:
			if right == 0 {
				return 0, fmt.Errorf("division by zero in compile-time integer expression")
			}
			return left / right, nil
		case lexer.TOKEN_LSHIFT:
			return left << right, nil
		case lexer.TOKEN_RSHIFT:
			return left >> right, nil
		case lexer.TOKEN_AMPERSAND:
			return left & right, nil
		case lexer.TOKEN_PIPE:
			return left | right, nil
		case lexer.TOKEN_CARET:
			return left ^ right, nil
		default:
			return 0, fmt.Errorf("unsupported compile-time binary operator %s", lexer.TokenName(n.Op))
		}
	default:
		return 0, fmt.Errorf("unsupported compile-time integer expression %T", expr)
	}
}

func isZeroedExpr(expr ast.Expr) bool {
	_, ok := expr.(*ast.ZeroedLit)
	return ok
}

func isVoidType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	return ok && b.Name == "void"
}

func isNumericType(t semantic.Type) bool {
	return semantic.IsNumericType(t)
}

func isPointerLikeType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.RefType, *semantic.NullType, *semantic.DListType, *semantic.DStrType, *semantic.FuncType:
		return true
	default:
		return false
	}
}

func isSignedIntegerType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if _, ok := t.(*semantic.NullType); ok {
			return false
		}
		return false
	}
	switch b.Name {
	case "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}

func integerBitWidth(t semantic.Type, wordBits int) int {
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if isPointerLikeType(t) {
			return wordBits
		}
		return wordBits
	}
	switch b.Name {
	case "bool":
		return 1
	case "i8", "u8":
		return 8
	case "i16", "u16":
		return 16
	case "i32", "u32":
		return 32
	case "i64", "u64":
		return 64
	case "int", "isize", "usize", "uintptr":
		return wordBits
	default:
		return wordBits
	}
}

func llvmIntPredicate(op lexer.TokenKind, operandType semantic.Type) (C.LLVMIntPredicate, error) {
	signed := isSignedIntegerType(operandType)
	switch op {
	case lexer.TOKEN_EQEQ:
		return C.LLVMIntEQ, nil
	case lexer.TOKEN_BANGEQ:
		return C.LLVMIntNE, nil
	case lexer.TOKEN_LT:
		if signed {
			return C.LLVMIntSLT, nil
		}
		return C.LLVMIntULT, nil
	case lexer.TOKEN_GT:
		if signed {
			return C.LLVMIntSGT, nil
		}
		return C.LLVMIntUGT, nil
	case lexer.TOKEN_LTEQ:
		if signed {
			return C.LLVMIntSLE, nil
		}
		return C.LLVMIntULE, nil
	case lexer.TOKEN_GTEQ:
		if signed {
			return C.LLVMIntSGE, nil
		}
		return C.LLVMIntUGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}

func llvmTypeSize(t C.LLVMTypeRef, wordBits int) uint64 {
	switch C.LLVMGetTypeKind(t) {
	case C.LLVMIntegerTypeKind:
		bits := uint64(C.LLVMGetIntTypeWidth(t))
		if bits == 1 {
			return 1
		}
		return bits / 8
	case C.LLVMPointerTypeKind:
		return uint64(wordBits / 8)
	case C.LLVMArrayTypeKind:
		return uint64(C.LLVMGetArrayLength2(t)) * llvmTypeSize(C.LLVMGetElementType(t), wordBits)
	default:
		return uint64(wordBits / 8)
	}
}

func cStringFree(s string) *C.char {
	if s == "" {
		return nil
	}
	ptr := C.CString(s)
	return ptr
}
