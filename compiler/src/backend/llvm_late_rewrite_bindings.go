//go:build cgo

package backend

// Bindings for late table, zeroed-load and split-half rewrites, and the
// cleanup that propagates facts exposed by late loop rewrites.

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/IRReader.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
unsigned elisacoreCanonicalizeSplitHalfUnsignedAdds(LLVMModuleRef Module);
unsigned elisacoreForwardZeroedLoads(LLVMModuleRef Module);
unsigned elisacoreInferConstantTableRanges(LLVMModuleRef Module);
int elisacoreTableUnrollSupported(void);
int elisacoreUnrollConstantTableLoops(LLVMModuleRef Module, LLVMTargetMachineRef Target);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func tableUnrollSupported() bool { return C.elisacoreTableUnrollSupported() != 0 }

func unrollConstantTableLoops(module C.LLVMModuleRef, target C.LLVMTargetMachineRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreUnrollConstantTableLoops(module, target))
}

func inferConstantTableRanges(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreInferConstantTableRanges(module))
}

func forwardZeroedLoads(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreForwardZeroedLoads(module))
}

func canonicalizeSplitHalfUnsignedAdds(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreCanonicalizeSplitHalfUnsignedAdds(module))
}

func parseLLVMIRForSplitHalfTest(ir string) (*llvmGenerator, error) {
	context := C.LLVMContextCreate()
	if context == nil {
		return nil, fmt.Errorf("failed to create LLVM context for split-half test IR")
	}
	text := C.CString(ir)
	name := C.CString("split-half-add-test")
	buffer := C.LLVMCreateMemoryBufferWithMemoryRangeCopy(text, C.size_t(len(ir)), name)
	C.free(unsafe.Pointer(text))
	C.free(unsafe.Pointer(name))
	if buffer == nil {
		C.LLVMContextDispose(context)
		return nil, fmt.Errorf("failed to create LLVM buffer for split-half test IR")
	}
	var module C.LLVMModuleRef
	var message *C.char
	// Use the buffer-consuming API for compatibility with LLVM 21, which is
	// still supported by this project. LLVMParseIRInContext consumes buffer on
	// both success and parse failure.
	status := C.LLVMParseIRInContext(context, buffer, &module, &message)
	if status != 0 || module == nil {
		detail := "unknown LLVM IR parse error"
		if message != nil {
			detail = C.GoString(message)
			C.LLVMDisposeMessage(message)
		}
		if module != nil {
			C.LLVMDisposeModule(module)
		}
		C.LLVMContextDispose(context)
		return nil, fmt.Errorf("parse split-half test IR: %s", detail)
	}
	if message != nil {
		C.LLVMDisposeMessage(message)
	}
	return &llvmGenerator{context: context, module: module}, nil
}

// Late loop rewrites expose new branch facts after LLVM's default pipeline.
// Keep policy here so adjusting cleanup does not rebuild the native pass library.
func simplifyLateLoopFacts(g *llvmGenerator) error {
	options := C.LLVMCreatePassBuilderOptions()
	defer C.LLVMDisposePassBuilderOptions(options)
	pipeline := cString("function(sccp,instcombine<no-verify-fixpoint>,simplifycfg)")
	defer C.free(unsafe.Pointer(pipeline))
	if failure := C.LLVMRunPasses(g.module, pipeline, g.targetMachine, options); failure != nil {
		message := C.LLVMGetErrorMessage(failure)
		return fmt.Errorf("late loop fact cleanup failed: %s", disposeLLVMErrorMessage(message, "unknown LLVM pass pipeline error"))
	}
	return nil
}
