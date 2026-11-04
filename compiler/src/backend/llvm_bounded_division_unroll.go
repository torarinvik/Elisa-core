//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
int elisacoreBoundedDivisionUnrollSupported(void);
unsigned elisacoreOptimizeBoundedDivisionLoops(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func unrollBoundedDivisionLoops(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeBoundedDivisionLoops(g.module, g.targetMachine))
}

func boundedDivisionUnrollSupported() bool {
	return C.elisacoreBoundedDivisionUnrollSupported() != 0
}
