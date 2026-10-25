//go:build cgo

package backend

/*
#include <llvm-c/Core.h>

// These calls synchronously consume pointer arrays and copy their LLVM handles
// into C++-owned operands. They retain neither the caller's array nor its name
// buffer. The compiler installs no Go LLVM diagnostic/yield callbacks.
// Keep this list narrow: owning-buffer and callback-registration APIs do not
// have the same contract. Both hints are needed for Go's nonescaping wrapper.
#cgo noescape LLVMAddIncoming
#cgo nocallback LLVMAddIncoming
#cgo noescape LLVMBuildGEP2
#cgo nocallback LLVMBuildGEP2
*/
import "C"
