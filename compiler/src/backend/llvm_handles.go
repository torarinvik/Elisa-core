//go:build cgo

package backend

// #include <llvm-c/Types.h>
import "C"

// llvmValueRef lets code that passes LLVM values between emitters share the
// binding without running cgo on files that make no direct C calls.
type llvmValueRef = C.LLVMValueRef
