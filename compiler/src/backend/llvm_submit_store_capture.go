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

// Store-capturing submission (semantic/analyzer_submit_store_capture.go): `pool_submit1(pool,
// worker, arg)` where `worker` carries implicit packed-store parameters. pool_submit1 calls its
// function through `fn(A) -> R`, which has no slot for the stores, so the site is lowered as
//
//	pool_submit1[Capture, R](pool, __submit_capture_N_thunk, Capture{arg, store_0, ...})
//	def __submit_capture_N_thunk(cap: Capture) -> R: return worker(cap.arg) with stores cap.store_k
//
// The stores are the submitting scope's own (its implicit parameter, or its active region store):
// the same values a direct call `worker(arg)` here would pass.
func (s *functionState) emitStoreCapturingSubmitCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || callIdentName(expr) != "pool_submit1" || len(expr.Args) != 3 {
		return nil, nil, false, nil
	}
	workerType, ok := s.exprType(expr.Args[1]).(*semantic.FuncType)
	if !ok || workerType == nil {
		return nil, nil, false, nil
	}
	names := semantic.SubmitStoreCaptureNames(workerType)
	if len(names) == 0 {
		return nil, nil, false, nil
	}
	explicit := len(workerType.Params) - len(names)
	if explicit != 1 {
		return nil, nil, true, fmt.Errorf("store-capturing submit of %q expects a one-argument worker, got %d explicit parameters", workerType.Name, explicit)
	}
	argType := workerType.Params[0]
	retType := workerType.Return
	pos := expr.Position
	prefix := s.g.nextSyntheticName("__submit_capture_")

	fields := map[string]semantic.Field{"arg": {Name: "arg", Type: argType}}
	declFields := []ast.FieldDecl{{Position: lexer.Pos{}, Name: "arg"}}
	storeFields := make([]string, len(names))
	for k := range names {
		fieldName := fmt.Sprintf("store_%d", k)
		storeFields[k] = fieldName
		fields[fieldName] = semantic.Field{Name: fieldName, Type: workerType.Params[explicit+k]}
		declFields = append(declFields, ast.FieldDecl{Position: lexer.Pos{}, Name: fieldName})
	}
	capDecl := &ast.StructDecl{Position: lexer.Pos{}, Name: prefix + "_capture", Fields: declFields, ReprC: true}
	capType := &semantic.StructType{Name: capDecl.Name, Fields: fields, ReprC: true, Decl: capDecl}

	// The thunk: one parameter, the capture; its body is the direct call with the stores supplied.
	thunkName := prefix + "_thunk"
	thunkType := &semantic.FuncType{Name: thunkName, Params: []semantic.Type{capType}, Return: retType}
	thunkFn, err := s.g.addFunction(thunkName, thunkType)
	if err != nil {
		return nil, nil, true, err
	}
	s.g.functions[thunkName] = thunkFn
	s.g.setDefinedFunctionLinkage(thunkName, thunkFn, thunkType)
	capParam := prefix + "_cap"
	field := func(name string, t semantic.Type) ast.Expr {
		f := &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: capParam}, Field: name}
		s.g.result.ExprTypes[f] = t
		return f
	}
	implicitArgs := make([]ast.Expr, len(names))
	for k, name := range storeFields {
		implicitArgs[k] = field(name, fields[name].Type)
	}
	call := &ast.CallExpr{
		Position:                  pos,
		Func:                      expr.Args[1],
		Args:                      []ast.Expr{field("arg", argType)},
		ResolvedImplicitArgs:      implicitArgs,
		ResolvedImplicitArgsValid: true,
	}
	s.g.result.ExprTypes[call] = retType
	thunkDecl := &ast.FuncDecl{
		Position:   pos,
		Name:       thunkName,
		Params:     []ast.ParamDecl{{Position: pos, Name: capParam}},
		ReturnType: &ast.NamedType{Position: pos, Name: retType.String()},
		Body:       []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: call}},
	}
	if err := s.g.defineFunctionBody(thunkDecl, thunkType, thunkFn); err != nil {
		return nil, nil, true, err
	}

	// The submission itself, on the pool_submit1 instance for the capture type.
	submit, submitType, err := s.ensureRuntimeFunction("pool_submit1", map[string]semantic.Type{"A": capType, "R": retType})
	if err != nil {
		return nil, nil, true, err
	}
	submitLLVMType, err := s.g.lowerFunctionType(submitType)
	if err != nil {
		return nil, nil, true, err
	}
	poolValue, _, err := s.emitExpr(expr.Args[0], submitType.Params[0])
	if err != nil {
		return nil, nil, true, err
	}
	argValue, _, err := s.emitExpr(expr.Args[2], argType)
	if err != nil {
		return nil, nil, true, err
	}
	capLLVMType, err := s.g.lowerType(capType)
	if err != nil {
		return nil, nil, true, err
	}
	capValue := C.LLVMGetUndef(capLLVMType)
	capValue = C.LLVMBuildInsertValue(s.builder, capValue, argValue, 0, cStringFree("submit.capture.arg"))
	for k, name := range names {
		storeType := fields[storeFields[k]].Type
		// A scope that carries the store itself passes its own parameter; any other scope
		// resolves it like a consumer call does (the active binding the handles came from).
		ident := &ast.Ident{Position: pos, Name: name}
		if _, bound := s.lookupBinding(name); !bound {
			ident = &ast.Ident{Position: pos, Name: semantic.PackedStoreImplicitUsePrefix + strings.TrimPrefix(name, "__packed_store_")}
		}
		storeValue, _, err := s.emitExpr(ident, storeType)
		if err != nil {
			return nil, nil, true, err
		}
		capValue = C.LLVMBuildInsertValue(s.builder, capValue, storeValue, C.uint(k+1), cStringFree("submit.capture.store"))
	}
	task := s.buildCall(submitLLVMType, submit, []C.LLVMValueRef{poolValue, thunkFn, capValue}, "submit.capture")
	return task, submitType.Return, true, nil
}
