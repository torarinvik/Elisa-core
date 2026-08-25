//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

// emitCheckedElemByteCount returns countValue * elemSize as a usize byte count,
// trapping at runtime if the multiplication would overflow usize. This closes a
// no-Unsafe-needed OOB path (deep audit #3/#4): a runtime element count times a
// fixed element size can wrap to a small byte count, causing arena_alloc to
// under-allocate while the view/darray header keeps the un-wrapped length — so
// every "in-bounds" index then reads/writes past the real allocation.
//
// elemSize is a compile-time constant, so the overflow test is a single compare
// against the constant SIZE_MAX/elemSize (no umul intrinsic needed). For
// elemSize <= 1 the product cannot exceed countValue, so the check is skipped.
func (s *functionState) emitCheckedElemByteCount(countValue C.LLVMValueRef, elemSize uint64, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	if elemSize <= 1 {
		return C.LLVMBuildMul(s.builder, countValue, elemSizeValue, cStringFree(name+".bytes")), nil
	}
	// maxCount = SIZE_MAX / elemSize. count > maxCount  <=>  count*elemSize overflows.
	//
	// SIZE_MAX is the TARGET's, not the host's: on a 32-bit target (wasm32) a
	// 64-bit constant silently truncates when materialised into an i32, which
	// turns the guard into a comparison against a meaningless bound.
	sizeMax := ^uint64(0)
	if bits := s.g.wordBits; bits > 0 && bits < 64 {
		sizeMax = (uint64(1) << uint(bits)) - 1
	}
	maxCount := sizeMax / elemSize
	maxCountValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(maxCount), 0)
	overflow := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGT), countValue, maxCountValue, cStringFree(name+".size.overflow"))

	trapBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".size.overflow.trap"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".size.ok"))
	C.LLVMBuildCondBr(s.builder, overflow, trapBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, trapBB)
	if err := s.emitTrapUnreachable(name + ".size.overflow"); err != nil {
		return nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return C.LLVMBuildMul(s.builder, countValue, elemSizeValue, cStringFree(name+".bytes")), nil
}
