//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

unsigned elisacoreSpecializeShortTrips(LLVMModuleRef, LLVMTargetMachineRef);
int elisacoreBoundedDivisionUnrollSupported(void);
unsigned elisacoreOptimizeBoundedDivisionLoops(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreBoundedLoopUnswitch(LLVMModuleRef);
unsigned elisacoreGroupRemainderReductions(LLVMModuleRef ModuleRef);
unsigned elisacorePeelRemainderTail(LLVMModuleRef);
*/
import "C"

func specializeShortTrips(g *llvmGenerator) int {
	return int(C.elisacoreSpecializeShortTrips(g.module, g.targetMachine))
}

func unrollBoundedDivisionLoops(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeBoundedDivisionLoops(g.module, g.targetMachine))
}

func boundedDivisionUnrollSupported() bool {
	return C.elisacoreBoundedDivisionUnrollSupported() != 0
}

func boundedLoopUnswitch(g *llvmGenerator) int {
	return int(C.elisacoreBoundedLoopUnswitch(g.module))
}

func groupRemainderReductions(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreGroupRemainderReductions(module))
}

func peelRemainderTail(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacorePeelRemainderTail(module))
}
