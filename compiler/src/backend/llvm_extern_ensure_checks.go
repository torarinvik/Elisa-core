//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
)

// docs/127 D8 — an extern's `ensure` is CHECKED, not assumed.
//
// The analyzer lets the caller assume an extern's `ensure` downstream once the `requires`
// is discharged (extern_boundary_contracts_test.go). For a scalar fact that is the right
// default; for a fact a memory obligation rides on (`ensure result <= count` on a read)
// a lying library turns safe code into undefined behaviour with no stop. So in checked
// builds every native extern call re-evaluates each `ensure` clause at the call site,
// with the parameters bound to the argument values and `result` to the returned value,
// and panics on violation — the same gate and label shape as an Elisa function's own
// postcondition. `@trusted("reason")` is what buys the pure assumption.
func (s *functionState) emitExternEnsureChecks(fn *semantic.FuncType, args []C.LLVMValueRef, result C.LLVMValueRef, resultType semantic.Type) error {
	if fn == nil || !fn.IsNativeExtern || fn.TrustedExtern || len(fn.ExternEnsureValues) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	if _, isUnion := fn.Return.(*semantic.ErrorUnionType); isUnion {
		return nil
	}
	s.pushScope()
	defer s.popScope()
	for i, name := range fn.ExplicitParamNames {
		if i >= len(args) || i >= len(fn.Params) || name == "" || args[i] == nil {
			continue
		}
		alloca, err := s.createEntryAlloca(name, fn.Params[i])
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, args[i], alloca)
		s.defineBinding(name, valueBinding{ptr: alloca, typ: fn.Params[i], mutable: false})
	}
	if result != nil && resultType != nil && !isVoidType(resultType) {
		alloca, err := s.createEntryAlloca("result", resultType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, result, alloca)
		s.defineBinding("result", valueBinding{ptr: alloca, typ: resultType, mutable: false})
	}
	for _, cond := range fn.ExternEnsureValues {
		if cond == nil {
			continue
		}
		if err := s.emitContractCheck(cond, "extern postcondition failed"); err != nil {
			return err
		}
	}
	return nil
}
