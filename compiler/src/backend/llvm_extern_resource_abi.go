//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
)

// `extern resource` at the C ABI boundary (docs/127 §3.2).
//
// Inside Elisa a resource is the struct `Name{__handle: Name__native}`, which is what gives
// it moves, `__drop__` and use-after-release for free. C never sees the struct: at a native
// extern the resource crosses as the bare handle. By value (`f(r: Name)`, a consuming call
// such as the destructor's release) the handle is extracted; by reference (`f(r: Name&)`,
// a borrow) the handle is LOADED from the referenced struct; a returned `Name` is wrapped
// back into the struct, and a returned `Name?` uses the null niche (extern_optional_abi.go)
// and wraps the non-null handle.

func externResourceParam(fn *semantic.FuncType, t semantic.Type) (byRef bool, ok bool) {
	if fn == nil || !fn.IsNativeExtern {
		return false, false
	}
	if ref, isRef := t.(*semantic.RefType); isRef && ref != nil {
		return true, semantic.IsResourceStructType(ref.Elem)
	}
	return false, semantic.IsResourceStructType(t)
}

func externReturnsResource(fn *semantic.FuncType) bool {
	return fn != nil && fn.IsNativeExtern && semantic.IsResourceStructType(fn.Return)
}

// convertExternResourceArgs replaces each resource argument with its handle. Runs before
// the aggregate/byval and view conversions, one arg per param still.
func (s *functionState) convertExternResourceArgs(fn *semantic.FuncType, args []C.LLVMValueRef) []C.LLVMValueRef {
	var out []C.LLVMValueRef
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	for i := 0; i < len(fn.Params) && i < len(args); i++ {
		byRef, ok := externResourceParam(fn, fn.Params[i])
		if !ok {
			continue
		}
		if out == nil {
			out = make([]C.LLVMValueRef, len(args))
			copy(out, args)
		}
		if byRef {
			out[i] = C.LLVMBuildLoad2(s.builder, ptrType, args[i], cStringFree("extern.resource.handle"))
		} else {
			out[i] = C.LLVMBuildExtractValue(s.builder, args[i], 0, cStringFree("extern.resource.handle"))
		}
	}
	if out == nil {
		return args
	}
	return out
}

// emitResourceFromHandle wraps a native handle into the resource struct.
func (s *functionState) emitResourceFromHandle(handle C.LLVMValueRef, resource semantic.Type) (C.LLVMValueRef, error) {
	structType, err := s.g.lowerType(resource)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(structType)
	return C.LLVMBuildInsertValue(s.builder, value, handle, 0, cStringFree("extern.resource.wrap")), nil
}
