//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
unsigned elisacoreBiasValidationReturns(LLVMModuleRef);
*/
import "C"

func biasValidationReturns(g *llvmGenerator) int {
	return int(C.elisacoreBiasValidationReturns(g.module))
}
