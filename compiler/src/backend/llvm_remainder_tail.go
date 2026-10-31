//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
unsigned elisacorePeelRemainderTail(LLVMModuleRef);
*/
import "C"

func peelRemainderTail(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacorePeelRemainderTail(module))
}
