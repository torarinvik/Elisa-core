//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
unsigned elisacoreFastCheckedMul(LLVMModuleRef, LLVMTargetMachineRef);
*/
import "C"

func fastCheckedMul(g *llvmGenerator) int {
	return int(C.elisacoreFastCheckedMul(g.module, g.targetMachine))
}
