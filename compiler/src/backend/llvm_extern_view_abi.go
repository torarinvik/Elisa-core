//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
)

// Bounded views at the C ABI boundary (docs/127 §3.3).
//
// Elisa passes `view[T]` as its {ptr, len} aggregate (%DynArrayView). C has no such type:
// a C function that takes a buffer takes a POINTER and a LENGTH as two parameters. So a
// `view[T]` / `mutable view[T]` parameter of a C-ABI native extern lowers to two C
// parameters in place, pointer then `size_t` length, and the call site splits the view
// value into them. The declaration `extern scale(samples: mutable view[f32], gain: f32)`
// therefore binds `void scale(float *samples, size_t count, float gain)`.
//
// Gated on BOTH predicates: IsNativeExtern (a non-generic extern) AND CallConv == "c"
// (`@c_abi(c)` / `@callconv(c)`). An extern without an explicit C calling convention may be
// an Elisa function in another unit, which must keep the aggregate ABI.

// externSplitView reports whether parameter type t of function fn crosses as (ptr, len).
func externSplitView(fn *semantic.FuncType, t semantic.Type) (*semantic.ViewType, bool) {
	if fn == nil || !fn.IsNativeExtern || !funcTypeIsCABI(fn) {
		return nil, false
	}
	view, ok := t.(*semantic.ViewType)
	if !ok || view == nil {
		return nil, false
	}
	return view, true
}

// funcTypeSplitsViews reports whether any parameter of fn is split, so callers that index
// LLVM parameters by semantic position can take the shifted layout into account.
func funcTypeSplitsViews(fn *semantic.FuncType) bool {
	if fn == nil {
		return false
	}
	for _, p := range fn.Params {
		if _, ok := externSplitView(fn, p); ok {
			return true
		}
	}
	return false
}

// externViewLLVMParamPos maps a semantic explicit-parameter index to its LLVM parameter
// index (relative to the explicit-parameter base), accounting for every split view before it.
func externViewLLVMParamPos(fn *semantic.FuncType, semanticIndex int) int {
	pos := semanticIndex
	for i := 0; i < semanticIndex && i < len(fn.Params); i++ {
		if _, ok := externSplitView(fn, fn.Params[i]); ok {
			pos++
		}
	}
	return pos
}

// convertExternViewArgs splits each view argument of a C-ABI extern call into its pointer
// and length, and remaps the byval index map to the expanded positions. Returns the caller's
// slices unchanged when nothing splits.
func (s *functionState) convertExternViewArgs(fn *semantic.FuncType, args []C.LLVMValueRef, byval map[int]C.LLVMTypeRef) ([]C.LLVMValueRef, map[int]C.LLVMTypeRef) {
	if !funcTypeSplitsViews(fn) {
		return args, byval
	}
	out := make([]C.LLVMValueRef, 0, len(args)+len(fn.Params))
	var remapped map[int]C.LLVMTypeRef
	for i, arg := range args {
		if ty, ok := byval[i]; ok {
			if remapped == nil {
				remapped = map[int]C.LLVMTypeRef{}
			}
			remapped[len(out)] = ty
		}
		if i < len(fn.Params) {
			if _, ok := externSplitView(fn, fn.Params[i]); ok {
				ptr := C.LLVMBuildExtractValue(s.builder, arg, 0, cStringFree("extern.view.ptr"))
				length := C.LLVMBuildExtractValue(s.builder, arg, 1, cStringFree("extern.view.len"))
				out = append(out, ptr, length)
				continue
			}
		}
		out = append(out, arg)
	}
	return out, remapped
}
