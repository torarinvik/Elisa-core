//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
unsigned elisacorePackPrivateBoolArrays(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func packPrivateBoolArrays(g *llvmGenerator) int {
	return int(C.elisacorePackPrivateBoolArrays(g.module, g.targetMachine))
}
