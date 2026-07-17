//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
)

// Optionals at the C ABI boundary.
//
// Elisa represents `T?` as {i1 tag, T payload}; C has no such type. Declaring an extern
// with an optional in its signature used to lower that struct straight into the native
// declaration — `-> LLVMValueRef?` became `declare {i1, ptr} @LLVMGetNamedFunction(...)`
// — so the tag was read from whichever register the C function happened to leave its
// pointer in. On arm64 that is the returned pointer's low bit, which is always 0 for an
// aligned pointer: every such call bound as null, with no diagnostic.
//
// When the payload has a null niche (semantic.NullNichePointerPayload) the C
// representation is a plain nullable pointer, which is what these functions declare and
// pass. The optional is destructured on the way in and rebuilt on the way out, so Elisa
// code on either side still sees an ordinary `T?` with its normal in-memory layout.
// Optionals without a niche never reach here — checkExternOptionalABI rejects them.

// externNicheOptionalPayload returns t's payload when t is an optional in the signature
// of an extern function and crosses the boundary as a plain nullable pointer.
func externNicheOptionalPayload(fn *semantic.FuncType, t semantic.Type) (semantic.Type, bool) {
	if fn == nil || !fn.IsNativeExtern {
		return nil, false
	}
	return semantic.NicheOptionalPayload(t)
}

// convertExternOptionalArgs rewrites each optional argument that crosses as a niche
// pointer, leaving every other argument as it was. Returns a new slice rather than
// editing the caller's, matching convertByvalArgs.
func (s *functionState) convertExternOptionalArgs(fn *semantic.FuncType, args []C.LLVMValueRef) []C.LLVMValueRef {
	var out []C.LLVMValueRef
	for i := 0; i < len(fn.Params) && i < len(args); i++ {
		if _, ok := externNicheOptionalPayload(fn, fn.Params[i]); !ok {
			continue
		}
		if out == nil {
			out = make([]C.LLVMValueRef, len(args))
			copy(out, args)
		}
		out[i] = s.emitExternOptionalToNiche(args[i])
	}
	if out == nil {
		return args
	}
	return out
}

// emitExternOptionalToNiche lowers an Elisa optional argument to the bare pointer the C
// callee expects: the payload when present, null when absent.
func (s *functionState) emitExternOptionalToNiche(value C.LLVMValueRef) C.LLVMValueRef {
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	present := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("extern.opt.arg.present"))
	payload := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("extern.opt.arg.payload"))
	// select, not a branch: both operands are already materialized values.
	return C.LLVMBuildSelect(s.builder, present, payload, C.LLVMConstNull(ptrType), cStringFree("extern.opt.arg.niche"))
}

// emitExternOptionalFromNiche rebuilds an Elisa optional from the bare pointer a C
// function returned: absent when null, present carrying the pointer otherwise.
func (s *functionState) emitExternOptionalFromNiche(ptr C.LLVMValueRef, optional semantic.Type) (C.LLVMValueRef, error) {
	optionalType, err := s.g.lowerType(optional)
	if err != nil {
		return nil, err
	}
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	present := C.LLVMBuildICmp(s.builder, C.LLVMIntNE, ptr, C.LLVMConstNull(ptrType), cStringFree("extern.opt.present"))
	result := C.LLVMGetUndef(optionalType)
	result = C.LLVMBuildInsertValue(s.builder, result, present, 0, cStringFree("extern.opt.tag"))
	result = C.LLVMBuildInsertValue(s.builder, result, ptr, 1, cStringFree("extern.opt.val"))
	return result, nil
}
