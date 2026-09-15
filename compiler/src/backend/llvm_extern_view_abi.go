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
// A `@bounds` plan makes the view's parts explicit, so the plan owns the split then.
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

// externCParts is the C parameter order of fn: FuncType.CParamPlan when `@bounds` set one,
// else the explicit params in order with each split view as adjacent (ptr, len). Nil when
// nothing about fn differs from the plain one-LLVM-param-per-semantic-param lowering.
func externCParts(fn *semantic.FuncType) []semantic.CParamPart {
	if fn == nil {
		return nil
	}
	if len(fn.CParamPlan) > 0 {
		return fn.CParamPlan
	}
	var parts []semantic.CParamPart
	split := false
	for i, p := range fn.Params {
		if _, ok := externSplitView(fn, p); ok {
			split = true
			parts = append(parts, semantic.CParamPart{Param: i, Part: semantic.CParamViewPtr}, semantic.CParamPart{Param: i, Part: semantic.CParamViewLen})
			continue
		}
		parts = append(parts, semantic.CParamPart{Param: i, Part: semantic.CParamWhole})
	}
	if !split {
		return nil
	}
	return parts
}

// funcTypeSplitsViews reports whether fn's C parameter order differs from its semantic one.
func funcTypeSplitsViews(fn *semantic.FuncType) bool {
	return externCParts(fn) != nil
}

// externViewLLVMParamPos maps a semantic explicit-parameter index to its LLVM parameter
// index (relative to the explicit-parameter base): the position of that parameter's whole
// value, or of its pointer for a split view.
func externViewLLVMParamPos(fn *semantic.FuncType, semanticIndex int) int {
	parts := externCParts(fn)
	if parts == nil {
		return semanticIndex
	}
	for pos, part := range parts {
		if part.Param == semanticIndex && part.Part != semantic.CParamViewLen {
			return pos
		}
	}
	return semanticIndex
}

// convertExternViewArgs reorders and splits the explicit arguments of a C-ABI extern call
// into its C parameter order, and remaps the byval index map to the new positions. Returns
// the caller's slices unchanged when the order is the identity.
func (s *functionState) convertExternViewArgs(fn *semantic.FuncType, args []C.LLVMValueRef, byval map[int]C.LLVMTypeRef) ([]C.LLVMValueRef, map[int]C.LLVMTypeRef) {
	parts := externCParts(fn)
	if parts == nil {
		return args, byval
	}
	out := make([]C.LLVMValueRef, 0, len(parts)+len(args))
	var remapped map[int]C.LLVMTypeRef
	for _, part := range parts {
		if part.Param >= len(args) {
			continue
		}
		arg := args[part.Param]
		switch part.Part {
		case semantic.CParamViewPtr:
			out = append(out, C.LLVMBuildExtractValue(s.builder, arg, 0, cStringFree("extern.view.ptr")))
		case semantic.CParamViewLen:
			out = append(out, C.LLVMBuildExtractValue(s.builder, arg, 1, cStringFree("extern.view.len")))
		default:
			if ty, ok := byval[part.Param]; ok {
				if remapped == nil {
					remapped = map[int]C.LLVMTypeRef{}
				}
				remapped[len(out)] = ty
			}
			out = append(out, arg)
		}
	}
	// Variadic tail (arguments beyond the explicit params) follows in order.
	for i := len(fn.Params); i < len(args); i++ {
		out = append(out, args[i])
	}
	return out, remapped
}
