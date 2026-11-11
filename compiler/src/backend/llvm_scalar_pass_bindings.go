//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

unsigned elisacoreFastCheckedMul(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreProveSqrtNonNaN(LLVMModuleRef);
unsigned elisacoreOptimizeSqrtNonNaN(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreBiasValidationReturns(LLVMModuleRef);
*/
import "C"

func fastCheckedMul(g *llvmGenerator) int {
	return int(C.elisacoreFastCheckedMul(g.module, g.targetMachine))
}

func proveSqrtNonNaN(g *llvmGenerator) int {
	return int(C.elisacoreProveSqrtNonNaN(g.module))
}

func optimizeSqrtNonNaN(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeSqrtNonNaN(g.module, g.targetMachine))
}

func biasValidationReturns(g *llvmGenerator) int {
	return int(C.elisacoreBiasValidationReturns(g.module))
}
