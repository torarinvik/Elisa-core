//go:build cgo

package backend

// #include <llvm-c/Core.h>
import "C"

type llvmIRModuleRef = C.LLVMModuleRef
type llvmIRLinkage = C.LLVMLinkage
type llvmIRTypeKind = C.LLVMTypeKind

const (
	llvmInternalLinkage            = C.LLVMInternalLinkage
	llvmAvailableExternallyLinkage = C.LLVMAvailableExternallyLinkage
	llvmPrivateLinkage             = C.LLVMPrivateLinkage
	llvmPointerTypeKind            = C.LLVMPointerTypeKind
)

func llvmPointerTypeInContext(context llvmIRContextRef, addressSpace llvmIRUnsigned) llvmIRTypeRef {
	return C.LLVMPointerTypeInContext(context, addressSpace)
}

func llvmSetGlobalConstant(global llvmIRValueRef, constant llvmIRBool) {
	C.LLVMSetGlobalConstant(global, constant)
}

func llvmSetInitializer(global, value llvmIRValueRef)             { C.LLVMSetInitializer(global, value) }
func llvmSetLinkage(global llvmIRValueRef, linkage llvmIRLinkage) { C.LLVMSetLinkage(global, linkage) }
func llvmCountBasicBlocks(function llvmIRValueRef) llvmIRUnsigned {
	return C.LLVMCountBasicBlocks(function)
}
func llvmGetNamedFunction(module llvmIRModuleRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMGetNamedFunction(module, name)
}

func llvmStructSetBody(typ llvmIRTypeRef, elements *llvmIRTypeRef, count llvmIRUnsigned, packed llvmIRBool) {
	C.LLVMStructSetBody(typ, elements, count, packed)
}

func llvmInt32TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt32TypeInContext(context)
}
func llvmGetTypeKind(typ llvmIRTypeRef) llvmIRTypeKind { return C.LLVMGetTypeKind(typ) }
func llvmTypeOf(value llvmIRValueRef) llvmIRTypeRef    { return C.LLVMTypeOf(value) }
