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

// emitDictLiteralExpr lowers a `{k1: v1, k2: v2, ...}` dict literal: a zeroed (empty) dict is
// materialized on the stack and each pair is inserted via the panic-on-OOM put helper, sourcing
// the arena from the active region scope (the auto-region inference supplies one for a bare
// literal). The populated dict value is returned. Mirrors the synthetic `dict.put` lowering.
func (s *functionState) emitDictLiteralExpr(expr *ast.ListLitExpr, dictType *semantic.DictType) (C.LLVMValueRef, semantic.Type, error) {
	dictPtr, err := s.createEntryAllocaZeroed("dictlit", dictType)
	if err != nil {
		return nil, nil, err
	}
	owner, ok := s.regionArenaOwner(dictType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, fmt.Errorf("dict literal requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dictlit.owner.arena")
		if err != nil {
			return nil, nil, err
		}
		owner.arenaRef = arenaRef
	}
	putCallee, putType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_dict_put"), map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, err
	}
	putLLVM, err := s.g.lowerFunctionType(putType)
	if err != nil {
		return nil, nil, err
	}
	for i := range expr.Keys {
		keyValue, _, err := s.emitExpr(expr.Keys[i], dictType.Key)
		if err != nil {
			return nil, nil, err
		}
		valueValue, _, err := s.emitExpr(expr.Elems[i], dictType.Value)
		if err != nil {
			return nil, nil, err
		}
		s.buildCall(putLLVM, putCallee, []C.LLVMValueRef{owner.arenaRef, dictPtr, keyValue, valueValue}, "dictlit.put")
	}
	dictLLVM, err := s.g.lowerType(dictType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildLoad2(s.builder, dictLLVM, dictPtr, cStringFree("dictlit.value")), dictType, nil
}

func (s *functionState) emitSetLiteralExpr(expr *ast.ListLitExpr, setType *semantic.SetType) (C.LLVMValueRef, semantic.Type, error) {
	setPtr, err := s.createEntryAllocaZeroed("setlit", setType)
	if err != nil {
		return nil, nil, err
	}
	owner, ok := s.regionArenaOwner(setType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, fmt.Errorf("set literal requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "setlit.owner.arena")
		if err != nil {
			return nil, nil, err
		}
		owner.arenaRef = arenaRef
	}
	addCallee, addType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_set_add"), map[string]semantic.Type{"T": setType.Elem})
	if err != nil {
		return nil, nil, err
	}
	addLLVM, err := s.g.lowerFunctionType(addType)
	if err != nil {
		return nil, nil, err
	}
	for _, elem := range expr.Elems {
		elemValue, _, err := s.emitExpr(elem, setType.Elem)
		if err != nil {
			return nil, nil, err
		}
		s.buildCall(addLLVM, addCallee, []C.LLVMValueRef{owner.arenaRef, setPtr, elemValue}, "setlit.add")
	}
	setLLVM, err := s.g.lowerType(setType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildLoad2(s.builder, setLLVM, setPtr, cStringFree("setlit.value")), setType, nil
}
