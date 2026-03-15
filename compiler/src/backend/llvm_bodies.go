//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"llcontext/src/ast"
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
	typeMap map[string]semantic.Type
}

func (g *llvmGenerator) defineFunctionBody(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef) error {
	return g.defineFunctionBodyWithBindings(decl, fnType, fnValue, nil)
}

func (g *llvmGenerator) defineFunctionBodyWithBindings(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef, typeBindings map[string]semantic.Type) error {
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
		typeMap: typeBindings,
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
		return s.emitStaticIf(n)
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

func (s *functionState) emitStaticIf(stmt *ast.StaticIfStmt) error {
	branch, err := s.activeStmtBranch(stmt)
	if err != nil {
		return err
	}
	for _, inner := range branch {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(inner); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) activeStmtBranch(stmt *ast.StaticIfStmt) ([]ast.Stmt, error) {
	selected, ok := s.evalConstBoolExpr(stmt.Cond)
	if !ok {
		return nil, fmt.Errorf("static if condition must be a compile-time bool")
	}
	if selected {
		return stmt.Then, nil
	}
	for _, elif := range stmt.Elifs {
		selected, ok := s.evalConstBoolExpr(elif.Cond)
		if !ok {
			return nil, fmt.Errorf("static elif condition must be a compile-time bool")
		}
		if selected {
			return elif.Body, nil
		}
	}
	return stmt.Else, nil
}
