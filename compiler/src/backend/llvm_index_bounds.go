//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
unsigned elisacoreValidatedIndexBounds(LLVMModuleRef);
unsigned elisacoreOptimizeIndexBounds(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func proveIndexBounds(g *llvmGenerator) int {
	return int(C.elisacoreValidatedIndexBounds(g.module))
}
func optimizeIndexBounds(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeIndexBounds(g.module, g.targetMachine))
}
