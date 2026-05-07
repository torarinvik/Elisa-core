//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import "elisacore/src/semantic"

func (s *functionState) emitTreeSwitchMergedValue(resultType semantic.Type, incomingValues []C.LLVMValueRef, incomingBlocks []C.LLVMBasicBlockRef, phiName string) (C.LLVMValueRef, bool, error) {
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, true, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], false, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, false, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree(phiName))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, false, nil
}
