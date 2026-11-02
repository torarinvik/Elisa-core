//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
unsigned elisacoreSpecializeShortTrips(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func specializeShortTrips(g *llvmGenerator) int {
	return int(C.elisacoreSpecializeShortTrips(g.module, g.targetMachine))
}
