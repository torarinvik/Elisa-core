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
	llvmIntegerTypeKind            = C.LLVMIntegerTypeKind
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

func llvmStructGetTypeAtIndex(typ llvmIRTypeRef, index llvmIRUnsigned) llvmIRTypeRef {
	return C.LLVMStructGetTypeAtIndex(typ, index)
}
func llvmTypeString(typ llvmIRTypeRef) string {
	message := C.LLVMPrintTypeToString(typ)
	if message == nil {
		return ""
	}
	defer C.LLVMDisposeMessage(message)
	return C.GoString(message)
}

func llvmInt8TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt8TypeInContext(context)
}

func llvmInt64TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt64TypeInContext(context)
}

func llvmGetIntTypeWidth(typ llvmIRTypeRef) llvmIRUnsigned {
	return C.LLVMGetIntTypeWidth(typ)
}

func llvmConstPointerNull(typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMConstPointerNull(typ)
}

func llvmGetTypeByName2(context llvmIRContextRef, name *llvmIRChar) llvmIRTypeRef {
	return C.LLVMGetTypeByName2(context, name)
}

type llvmIRDouble = C.double
type llvmIRUint64 = C.uint64_t

func llvmInt1TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt1TypeInContext(context)
}

func llvmIntTypeInContext(context llvmIRContextRef, width llvmIRUnsigned) llvmIRTypeRef {
	return C.LLVMIntTypeInContext(context, width)
}

func llvmVoidTypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMVoidTypeInContext(context)
}

func llvmArrayType2(element llvmIRTypeRef, count llvmIRUint64) llvmIRTypeRef {
	return C.LLVMArrayType2(element, count)
}

func llvmStructTypeInContext(context llvmIRContextRef, elements *llvmIRTypeRef, count llvmIRUnsigned, packed llvmIRBool) llvmIRTypeRef {
	return C.LLVMStructTypeInContext(context, elements, count, packed)
}

func llvmGlobalGetValueType(value llvmIRValueRef) llvmIRTypeRef {
	return C.LLVMGlobalGetValueType(value)
}

func llvmConstReal(typ llvmIRTypeRef, value llvmIRDouble) llvmIRValueRef {
	return C.LLVMConstReal(typ, value)
}
