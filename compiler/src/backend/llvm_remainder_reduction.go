//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
unsigned elisacoreGroupRemainderReductions(LLVMModuleRef ModuleRef);
*/
import "C"

func groupRemainderReductions(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreGroupRemainderReductions(module))
}
