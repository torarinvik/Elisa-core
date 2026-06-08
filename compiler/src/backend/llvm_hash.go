//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// emitHashBuiltinCall lowers the compiler-emitted `ctx_hash_value(key)` builtin (used by the
// runtime-backed hash dict) per the key's CONCRETE type — which is resolved here because the
// dict generics are monomorphized before codegen, exactly like `==`. A cstr hashes its content
// (ctx_hash_cstr → FNV-1a); every fixed-size scalar key (integer/bool/char/const enum) is
// zero-extended to u64 and finalized (ctx_hash_u64 → splitmix64). Both return u64.
func (s *functionState) emitHashBuiltinCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || callIdentName(expr) != "ctx_hash_value" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("ctx_hash_value expects 1 argument, got %d", len(expr.Args))
	}
	u64Type := s.g.result.NamedTypes["u64"]
	argType := s.exprType(expr.Args[0])
	if argType == nil {
		return nil, nil, true, fmt.Errorf("ctx_hash_value requires a resolved argument type")
	}
	argValue, _, err := s.emitExpr(expr.Args[0], argType)
	if err != nil {
		return nil, nil, true, err
	}

	// cstr key: hash the string content.
	if _, isCstr := semantic.StripAggregateStateType(argType).(*semantic.DStrType); isCstr {
		callee, fnType, err := s.ensureRuntimeFunction("ctx_hash_cstr", nil)
		if err != nil {
			return nil, nil, true, err
		}
		llvmType, err := s.g.lowerFunctionType(fnType)
		if err != nil {
			return nil, nil, true, err
		}
		coerced := argValue
		if len(fnType.Params) == 1 {
			coerced, err = s.coerceValue(argValue, argType, fnType.Params[0])
			if err != nil {
				return nil, nil, true, err
			}
		}
		return s.buildCall(llvmType, callee, []C.LLVMValueRef{coerced}, "hash.cstr"), u64Type, true, nil
	}

	// Fixed-size scalar key (integer / bool / char / const enum): zero-extend the value to u64
	// and run the integer finalizer. Equal keys of a given concrete type always extend
	// identically, so the hash is well-defined.
	if C.LLVMGetTypeKind(C.LLVMTypeOf(argValue)) != C.LLVMIntegerTypeKind {
		return nil, nil, true, fmt.Errorf("ctx_hash_value: unsupported non-integer scalar key type %s", argType.String())
	}
	i64LLVM := C.LLVMInt64TypeInContext(s.g.context)
	extended := argValue
	if C.LLVMGetIntTypeWidth(C.LLVMTypeOf(argValue)) < 64 {
		extended = C.LLVMBuildZExt(s.builder, argValue, i64LLVM, cStringFree("hash.zext"))
	}
	callee, fnType, err := s.ensureRuntimeFunction("ctx_hash_u64", nil)
	if err != nil {
		return nil, nil, true, err
	}
	llvmType, err := s.g.lowerFunctionType(fnType)
	if err != nil {
		return nil, nil, true, err
	}
	return s.buildCall(llvmType, callee, []C.LLVMValueRef{extended}, "hash.u64"), u64Type, true, nil
}
