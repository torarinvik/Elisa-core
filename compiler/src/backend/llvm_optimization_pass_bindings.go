//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

unsigned elisacoreSpecializeShortTrips(LLVMModuleRef, LLVMTargetMachineRef);
int elisacoreBoundedDivisionUnrollSupported(void);
unsigned elisacoreOptimizeBoundedDivisionLoops(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreUnrollLaggedRecurrences(LLVMModuleRef);
unsigned elisacoreBoundedLoopUnswitch(LLVMModuleRef);
unsigned elisacoreGroupRemainderReductions(LLVMModuleRef ModuleRef);
unsigned elisacorePeelRemainderTail(LLVMModuleRef);
unsigned elisacorePackPrivateBoolArrays(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreFoldLittleEndianBytePacks(LLVMModuleRef ModuleRef);
unsigned elisacoreFastCheckedMul(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreProveSqrtNonNaN(LLVMModuleRef);
unsigned elisacoreOptimizeSqrtNonNaN(LLVMModuleRef, LLVMTargetMachineRef);
unsigned elisacoreBiasValidationReturns(LLVMModuleRef);
*/
import "C"

func specializeShortTrips(g *llvmGenerator) int {
	return int(C.elisacoreSpecializeShortTrips(g.module, g.targetMachine))
}

func unrollBoundedDivisionLoops(g *llvmGenerator) int {
	return int(C.elisacoreOptimizeBoundedDivisionLoops(g.module, g.targetMachine))
}

func unrollLaggedRecurrences(g *llvmGenerator) int {
	return int(C.elisacoreUnrollLaggedRecurrences(g.module))
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

func packPrivateBoolArrays(g *llvmGenerator) int {
	return int(C.elisacorePackPrivateBoolArrays(g.module, g.targetMachine))
}

func foldLittleEndianBytePacks(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreFoldLittleEndianBytePacks(module))
}

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
