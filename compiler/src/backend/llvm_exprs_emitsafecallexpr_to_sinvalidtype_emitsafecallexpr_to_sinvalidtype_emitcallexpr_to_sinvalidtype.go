//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && s.g != nil && s.g.result != nil {
		if info, ok := s.g.semanticReadView().LookupTaskGroupSubmits(expr); ok {
			return s.emitVerifiedTaskGroupSubmit(expr, info)
		}
	}
	return s.emitCallExprUnwrapped(expr)
}

// emitVerifiedTaskGroupSubmit lowers one submit proved by semantic analysis as
// part of a joined TaskGroup window. The same annotation relaxes the lifetime
// check and adds the returned handle here, so the two stages cannot disagree
// about whether the pool still owns the capture at function return.
func (s *functionState) emitVerifiedTaskGroupSubmit(expr *ast.CallExpr, info semantic.TaskGroupSubmitInfo) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || info.GroupName == "" {
		return nil, nil, fmt.Errorf("invalid verified TaskGroup submission")
	}
	group, ok := s.lookupBinding(info.GroupName)
	if !ok {
		return nil, nil, fmt.Errorf("missing TaskGroup binding %q during verified submit lowering", info.GroupName)
	}
	taskValue, taskType, err := s.emitCallExprUnwrapped(expr)
	if err != nil {
		return nil, nil, err
	}
	task, ok := taskType.(*semantic.GenericInstanceType)
	taskName := ""
	if ok && task != nil {
		taskName = task.Name
		if taskName == "" {
			if base, isStruct := task.Base.(*semantic.StructType); isStruct && base != nil {
				taskName = base.Name
			}
		}
	}
	if !ok || task == nil || taskName != "Task" || len(task.Args) != 2 {
		return nil, nil, fmt.Errorf("verified submit did not produce a Task[R, Pending] value")
	}
	if info.AlreadyAdded {
		return taskValue, taskType, nil
	}
	groupAdd, groupAddType, err := s.ensureRuntimeFunction("task_group_add", map[string]semantic.Type{"R": task.Args[0]})
	if err != nil {
		return nil, nil, err
	}
	groupAddLLVMType, err := s.g.lowerFunctionType(groupAddType)
	if err != nil {
		return nil, nil, err
	}
	s.buildCall(groupAddLLVMType, groupAdd, []C.LLVMValueRef{group.ptr, taskValue}, "")
	return nil, s.g.namedType("void"), nil
}

func (s *functionState) emitCallExprUnwrapped(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if err := s.emitCallArgRefinementChecks(expr); err != nil {
		return nil, nil, err
	}
	if value, actualType, handled, err := s.emitProtocolTransitionCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitStoreCapturingSubmitCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitVaArgCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitVaCopyCall(expr); handled {
		return value, actualType, err
	}
	if storeType, ok := s.packedStoreConstructorCall(expr); ok {
		return s.emitPackedStoreConstructorValue(expr, storeType)
	}
	if callIdentName(expr) == "freeze" && s.g.result.IsCompilerBuiltinHelperCall(expr, "freeze") {
		if len(expr.Args) != 1 {
			return nil, nil, fmt.Errorf("freeze expects 1 argument, got %d", len(expr.Args))
		}
		frozenType := s.exprType(expr)
		return s.emitExpr(expr.Args[0], frozenType)
	}
	if value, actualType, handled, err := s.emitDenseKeyHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitNodeTableFillHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitHashBuiltinCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCloneCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCopyCall(expr); handled {
		return value, actualType, err
	}
	if errorType, qualifiedTag, ok := s.errorConstructorInfo(expr); ok {
		return s.emitErrorConstructorValue(expr, errorType, qualifiedTag)
	}
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		if enumType != nil && enumType.Packed {
			store, ok := s.lookupPackedStore(enumType)
			if !ok {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s requires an active in %s.Store: scope or explicit new[%s.Store]", ast.ModulePathSpelling(enumType.Name), variant.Name, ast.ModulePathSpelling(enumType.Name), ast.ModulePathSpelling(enumType.Name))
			}
			return s.emitPackedEnumConstructorAlloc(expr, store.value, enumType, variant, expr.Args, expr.ArgNames)
		}
		return s.emitEnumConstructorValue(expr, enumType, variant, expr.Args, expr.ArgNames)
	}
	if value, actualType, handled, err := s.emitProofCarryingViewHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitAtomicRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewEqCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaFromViewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewFillCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinFStrCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayPushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayPopCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayExtendCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayResizeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArraySviewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayCstrCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStorePushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreRowsCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinSOAValidCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryGetOrInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictRegionMutationCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinSetRegionMutationCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedMemcpyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitTypeConstructorCastCall(expr); handled {
		return value, actualType, err
	}
	callee, funcType, direct, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	if len(funcType.ImplicitParamNames) != 0 && (!expr.ResolvedImplicitArgsValid || len(expr.ResolvedImplicitArgs) != len(funcType.ImplicitParamNames)) {
		if recovered, ok := s.recoverImplicitCallArgs(expr, funcType); ok {
			expr.ResolvedImplicitArgs = recovered
			expr.ResolvedImplicitArgsValid = true
		} else {
			return nil, nil, fmt.Errorf("call to %s is missing resolved implicit arguments (implicit params: %v)", funcType.Name, funcType.ImplicitParamNames)
		}
	}
	loweredArgs := backendLoweredCallArgsForFunc(expr, funcType)
	args := make([]C.LLVMValueRef, 0, len(loweredArgs))
	for i, arg := range loweredArgs {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, actual, err := s.emitCallArg(arg, expected, funcType, i)
		if err != nil {
			return nil, nil, err
		}
		if backendIsCVariadicTailArg(funcType, i) {
			value, _, err = s.promoteCVariadicArg(value, actual)
			if err != nil {
				return nil, nil, err
			}
		}
		args = append(args, value)
	}
	regionArenaArgs, err := s.resolveRegionArenaArgs(expr, funcType, loweredArgs, args)
	if err != nil {
		return nil, nil, err
	}
	return s.emitResolvedCall(callee, funcType, direct, args, regionArenaArgs)
}

func backendIsCVariadicTailArg(fnType *semantic.FuncType, index int) bool {
	return fnType != nil && fnType.Variadic && index >= len(fnType.Params)
}

func (s *functionState) promoteCVariadicArg(value C.LLVMValueRef, actual semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if value == nil || actual == nil || s == nil || s.g == nil || s.g.result == nil {
		return value, actual, nil
	}
	if semantic.IsBoolType(actual) {
		i32Type := s.g.namedType("i32")
		i32LLVM, err := s.g.lowerType(i32Type)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildZExt(s.builder, value, i32LLVM, cStringFree("vararg.bool")), i32Type, nil
	}
	if builtin, ok := numericCastType(actual).(*semantic.BuiltinType); ok && builtin != nil && builtin.Name == "f32" {
		f64Type := s.g.namedType("f64")
		promoted, err := s.coerceValue(value, actual, f64Type)
		return promoted, f64Type, err
	}
	if _, width, ok := semantic.BitIntInfo(numericCastType(actual)); ok && width < 32 {
		i32Type := s.g.namedType("i32")
		promoted, err := s.coerceValue(value, actual, i32Type)
		return promoted, i32Type, err
	}
	return value, actual, nil
}

func backendLoweredCallArgsForFunc(expr *ast.CallExpr, funcType *semantic.FuncType) []ast.Expr {
	if expr == nil {
		return nil
	}
	explicitArgs := expr.Args
	if expr.ResolvedArgsValid && expr.ResolvedCommonArgs == nil {
		explicitArgs = expr.ResolvedArgs
	}
	if funcType == nil || len(funcType.ImplicitParamNames) == 0 || !expr.ResolvedImplicitArgsValid || len(expr.ResolvedImplicitArgs) == 0 {
		return explicitArgs
	}
	implicitCount := len(funcType.ImplicitParamNames)
	if implicitCount > len(expr.ResolvedImplicitArgs) {
		implicitCount = len(expr.ResolvedImplicitArgs)
	}
	out := make([]ast.Expr, 0, len(explicitArgs)+implicitCount)
	out = append(out, explicitArgs...)
	out = append(out, expr.ResolvedImplicitArgs[:implicitCount]...)
	return out
}

func (s *functionState) emitCastHookArgs(exprLabel string, operand ast.Expr, fnType *semantic.FuncType) ([]C.LLVMValueRef, error) {
	if fnType == nil || len(fnType.Params) == 0 {
		return nil, fmt.Errorf("invalid semantic cast hook for %s", exprLabel)
	}
	arg, _, err := s.emitExpr(operand, fnType.Params[0])
	if err != nil {
		return nil, err
	}
	args := []C.LLVMValueRef{arg}
	if len(fnType.Params) > 1 {
		// A cast hook can carry trailing IMPLICIT params (threaded region / packed store —
		// e.g. its return type holds region-backed enum handles). Resolve them like any other
		// call site (recoverImplicitCallArgs) and append; only EXPLICIT extras are unsupported.
		implicitCount := len(fnType.ImplicitParamNames)
		if len(fnType.Params)-implicitCount != 1 {
			return nil, fmt.Errorf("unsupported semantic cast hook with extra parameters for %s", exprLabel)
		}
		hookCall := &ast.CallExpr{Func: &ast.Ident{Name: fnType.Name}, Args: []ast.Expr{operand}}
		implicitExprs, ok := s.recoverImplicitCallArgs(hookCall, fnType)
		if !ok || len(implicitExprs) != implicitCount {
			return nil, fmt.Errorf("unsupported semantic cast hook with extra parameters for %s", exprLabel)
		}
		for i, implicitExpr := range implicitExprs {
			value, _, err := s.emitExpr(implicitExpr, fnType.Params[len(fnType.Params)-implicitCount+i])
			if err != nil {
				return nil, err
			}
			args = append(args, value)
		}
	}
	return args, nil
}
