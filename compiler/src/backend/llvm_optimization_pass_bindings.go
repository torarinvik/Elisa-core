//go:build cgo

package backend

/*
#include <string.h>
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

// Attaches !prof branch_weights {likely, unlikely} to a conditional branch.
static void elisacoreSetLikelyBranchWeights(LLVMContextRef ctx, LLVMValueRef br, unsigned likely, unsigned unlikely) {
	LLVMTypeRef i32 = LLVMInt32TypeInContext(ctx);
	LLVMMetadataRef ops[3];
	ops[0] = LLVMMDStringInContext2(ctx, "branch_weights", strlen("branch_weights"));
	ops[1] = LLVMValueAsMetadata(LLVMConstInt(i32, likely, 0));
	ops[2] = LLVMValueAsMetadata(LLVMConstInt(i32, unlikely, 0));
	LLVMMetadataRef node = LLVMMDNodeInContext2(ctx, ops, 3);
	unsigned kind = LLVMGetMDKindIDInContext(ctx, "prof", strlen("prof"));
	LLVMSetMetadata(br, kind, LLVMMetadataAsValue(ctx, node));
}

unsigned elisacoreValidatedIndexBounds(LLVMModuleRef);
unsigned elisacoreOptimizeIndexBounds(LLVMModuleRef, LLVMTargetMachineRef);
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

func llvmSetLikelyBranchWeights(context llvmIRContextRef, branch llvmIRValueRef, likely, unlikely llvmIRUnsigned) {
	C.elisacoreSetLikelyBranchWeights(context, branch, likely, unlikely)
}

func llvmOptimizeIndexBounds(module llvmIRModuleRef, target llvmIRTargetMachineRef) llvmIRUnsigned {
	return C.elisacoreOptimizeIndexBounds(module, target)
}

func llvmValidatedIndexBounds(module llvmIRModuleRef) llvmIRUnsigned {
	return C.elisacoreValidatedIndexBounds(module)
}
