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
	"os"
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
	partitionExperiment := s.g.partSynthNS != "" && os.Getenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT") != "0" && !s.g.emitDebugInfo && !s.g.emitTrace && s.g.optLevel == OptimizationLevel0
	if partitionExperiment {
		sym, found := s.g.lookupGlobal("pool_submit1")
		if !found || sym == nil {
			partitionExperiment = false
		} else {
			_, declOK := sym.Node.(*ast.FuncDecl)
			fn, typeOK := sym.Type.(*semantic.FuncType)
			if !declOK || !typeOK || !partitionSubmitSupportedSignature(fn) {
				partitionExperiment = false
			}
		}
	}
	prefix := ""
	if partitionExperiment {
		prefix = s.g.nextSyntheticHelperName("__submit_capture_")
	} else {
		s.g.partUnsafeHelpers = true
		prefix = s.g.nextSyntheticName("__submit_capture_")
	}

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
	if partitionExperiment {
		if _, exists := s.g.lookupNamedType(capDecl.Name); exists {
			return nil, nil, true, errPartitionDeclined
		}
		if _, exists := s.g.structTypes[capDecl.Name]; exists {
			return nil, nil, true, errPartitionDeclined
		}
		if C.LLVMGetTypeByName2(s.g.context, cStringFree(capDecl.Name)) != nil {
			return nil, nil, true, errPartitionDeclined
		}
	}
	capType := &semantic.StructType{Name: capDecl.Name, Fields: fields, ReprC: true, Decl: capDecl}
	if partitionExperiment {
		if !s.g.partSubmitTypes.register(capType) {
			return nil, nil, true, errPartitionDeclined
		}
		s.g.partSyntheticDepth++
		defer func() { s.g.partSyntheticDepth-- }()
	}

	// The thunk: one parameter, the capture; its body is the direct call with the stores supplied.
	thunkName := prefix + "_thunk"
	thunkType := &semantic.FuncType{Name: thunkName, Params: []semantic.Type{capType}, Return: retType}
	thunkFn, err := s.g.addFunction(thunkName, thunkType)
	if err != nil {
		return nil, nil, true, err
	}
	s.g.functions[thunkName] = thunkFn
	s.g.setDefinedFunctionLinkage(thunkName, thunkFn, thunkType)
	if partitionExperiment {
		C.LLVMSetLinkage(thunkFn, C.LLVMPrivateLinkage)
		if s.g.partLocalHelpers == nil {
			s.g.partLocalHelpers = make(map[string]bool)
		}
		s.g.partLocalHelpers[llvmValueName(thunkFn)] = true
	}
	capParam := prefix + "_cap"
	field := func(name string, t semantic.Type) ast.Expr {
		f := &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: capParam}, Field: name}
		{
			key, value := f, t
			s.g.setResultExprType(key, value)
		}
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
	{
		key, value := call, retType
		s.g.setResultExprType(key, value)
	}
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
		// Preserve the worker's exact store identity on this synthetic expression.
		// Its generated name is sanitized and cannot recover a qualified enum name.
		{
			key, value := ident, storeType
			s.g.setResultExprType(key, value)
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

// partitionSubmitSupportedSignature restricts the experimental path to the
// runtime shape whose generated capture is guaranteed to appear in the
// specialization key and in the argument and callback types. An unfamiliar
// runtime declaration must retain whole-module code generation.
func partitionSubmitSupportedSignature(fn *semantic.FuncType) bool {
	if fn == nil {
		return false
	}
	params := functionSpecializationParams(fn)
	if !partitionSubmitPlainCallABI(fn, 3) || len(params) != 2 || params[0].Kind != ast.GenericParamType || params[0].Name != "A" || params[1].Kind != ast.GenericParamType || params[1].Name != "R" || len(fn.TypeParams) != 2 || fn.TypeParams[0] != "A" || fn.TypeParams[1] != "R" || len(fn.PermissionParams) != 1 || fn.PermissionParams[0] != "P" || len(fn.Params) != 3 {
		return false
	}
	if len(fn.GenericParams) != 0 && (len(fn.GenericParams) != 2 || fn.GenericParams[0].Kind != ast.GenericParamType || fn.GenericParams[0].Name != "A" || fn.GenericParams[1].Kind != ast.GenericParamType || fn.GenericParams[1].Name != "R") {
		return false
	}
	isParam := func(t semantic.Type, name string) bool {
		p, ok := t.(*semantic.TypeParamType)
		return ok && p != nil && p.Name == name
	}
	isPoolRef := func(t semantic.Type) bool {
		ref, ok := t.(*semantic.RefType)
		if !ok || ref == nil || !ref.Mutable || ref.State != semantic.RefStateNonNull || ref.Storage != semantic.RefStorageAny || ref.Region != "" || ref.ExplicitStorage || ref.Linear {
			return false
		}
		pool, ok := ref.Elem.(*semantic.StructType)
		return ok && pool != nil && pool.Builtin && pool.Name == "ThreadPool" && len(pool.TypeParams) == 0 && len(pool.RegionParams) == 0
	}
	isTaskResult := func(t semantic.Type) bool {
		task, ok := t.(*semantic.GenericInstanceType)
		if !ok || task == nil || (task.Name != "" && task.Name != "Task") || task.Region != "" || len(task.Args) != 2 {
			return false
		}
		base, ok := task.Base.(*semantic.StructType)
		return ok && base != nil && base.Builtin && base.Name == "Task" && isParam(task.Args[0], "R") && semantic.SameType(task.Args[1], &semantic.BuiltinType{Name: "Pending"})
	}
	callback, ok := fn.Params[1].(*semantic.FuncType)
	return isPoolRef(fn.Params[0]) && ok && partitionSubmitPlainCallABI(callback, 1) && len(callback.TypeParams) == 0 && len(callback.GenericParams) == 0 && len(callback.RegionParams) == 0 && len(callback.Params) == 1 && isParam(callback.Params[0], "A") && isParam(callback.Return, "R") && isParam(fn.Params[2], "A") && isTaskResult(fn.Return)
}

// The lowering above passes three arguments directly and creates a plain Elisa
// thunk. Unfamiliar ABI metadata can change the effective function signature or
// calling convention, so it must use the existing whole-module fallback.
func partitionSubmitPlainCallABI(fn *semantic.FuncType, explicit int) bool {
	if fn == nil || fn.Variadic || fn.CallConv != "" || fn.IsNativeExtern || fn.HasAsyncEntry ||
		fn.HasSegmentAgnostic || fn.HasSegmentEstablishing || fn.HasReentrantSafe || fn.SegmentTransition != semantic.FuncSegmentTransitionNone ||
		fn.IntrinsicName != "" || fn.CParamPlan != nil || len(fn.ImplicitParamNames) != 0 ||
		fn.RegionPolymorphic || len(fn.InferredRegionParams) != 0 || fn.ReturnsOwnedRegion ||
		(fn.ExplicitParamCount != 0 && fn.ExplicitParamCount != explicit) {
		return false
	}
	for _, owned := range fn.OwnedParams {
		if owned {
			return false
		}
	}
	return true
}
