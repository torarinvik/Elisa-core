//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
unsigned elisacoreFoldLittleEndianBytePacks(LLVMModuleRef ModuleRef);
*/
import "C"

func foldLittleEndianBytePacks(module C.LLVMModuleRef) int {
	if module == nil {
		return 0
	}
	return int(C.elisacoreFoldLittleEndianBytePacks(module))
}
