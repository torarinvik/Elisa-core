//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
unsigned elisacoreProveSqrtNonNaN(LLVMModuleRef);
unsigned elisacoreOptimizeSqrtNonNaN(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func proveSqrtNonNaN(g *llvmGenerator) int {
	return int(C.elisacoreProveSqrtNonNaN(g.module))
}

func optimizeSqrtNonNaN(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeSqrtNonNaN(g.module, g.targetMachine))
}
