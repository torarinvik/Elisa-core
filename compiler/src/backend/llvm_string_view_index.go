//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import "elisacore/src/semantic"

// emitInlineStringViewIndex matches string_view_index on 64-bit targets. Keep
// the byte load behind the bounds branch so an empty view or an index at the
// end never touches the backing pointer. LLVM can remove the check in loops
// whose index is already bounded by the view length.
func (s *functionState) emitInlineStringViewIndex(view, index C.LLVMValueRef, resultType semantic.Type) (C.LLVMValueRef, error) {
	resultLLVM, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, err
	}
	ctx := s.g.context
	length := C.LLVMBuildExtractValue(s.builder, view, 1, cStringFree("sview.index.len"))
	validLength := C.LLVMBuildICmp(s.builder, C.LLVMIntSGE, length, C.LLVMConstNull(C.LLVMTypeOf(length)), cStringFree("sview.index.valid.len"))
	// With a nonnegative length, an unsigned comparison also rejects negative indexes.
	validIndex := C.LLVMBuildICmp(s.builder, C.LLVMIntULT, index, length, cStringFree("sview.index.in.range"))
	valid := C.LLVMBuildAnd(s.builder, validLength, validIndex, cStringFree("sview.index.valid"))
	origin := C.LLVMGetInsertBlock(s.builder)
	read := C.LLVMAppendBasicBlockInContext(ctx, s.fnValue, cStringFree("sview.index.read"))
	join := C.LLVMAppendBasicBlockInContext(ctx, s.fnValue, cStringFree("sview.index.join"))
	C.LLVMBuildCondBr(s.builder, valid, read, join)
	C.LLVMPositionBuilderAtEnd(s.builder, read)
	data := C.LLVMBuildExtractValue(s.builder, view, 0, cStringFree("sview.index.data"))
	byteType := C.LLVMInt8TypeInContext(ctx)
	indices := []C.LLVMValueRef{index}
	address := C.LLVMBuildGEP2(s.builder, byteType, data, llvmValueSlicePtr(indices), 1, cStringFree("sview.index.ptr"))
	byteValue := C.LLVMBuildLoad2(s.builder, byteType, address, cStringFree("sview.index.byte"))
	value := C.LLVMBuildZExt(s.builder, byteValue, resultLLVM, cStringFree("sview.index.wide"))
	C.LLVMBuildBr(s.builder, join)
	C.LLVMPositionBuilderAtEnd(s.builder, join)
	result := C.LLVMBuildPhi(s.builder, resultLLVM, cStringFree("sview.index"))
	values := []C.LLVMValueRef{C.LLVMConstNull(resultLLVM), value}
	blocks := []C.LLVMBasicBlockRef{origin, read}
	C.LLVMAddIncoming(result, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), 2)
	return result, nil
}
