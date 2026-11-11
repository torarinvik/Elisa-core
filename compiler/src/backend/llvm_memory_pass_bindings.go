//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

unsigned elisacorePackPrivateBoolArrays(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreFoldLittleEndianBytePacks(LLVMModuleRef ModuleRef);
*/
import "C"

func packPrivateBoolArrays(g *llvmGenerator) int {
	return int(C.elisacorePackPrivateBoolArrays(g.module, g.targetMachine))
}

func foldLittleEndianBytePacks(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreFoldLittleEndianBytePacks(module))
}
