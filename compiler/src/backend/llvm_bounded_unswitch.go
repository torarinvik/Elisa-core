//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
unsigned elisacoreBoundedLoopUnswitch(LLVMModuleRef);
*/
import "C"

func boundedLoopUnswitch(g *llvmGenerator) int {
	return int(C.elisacoreBoundedLoopUnswitch(g.module))
}
