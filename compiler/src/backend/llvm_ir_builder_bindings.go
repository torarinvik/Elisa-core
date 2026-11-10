//go:build cgo

package backend

// #include <llvm-c/Core.h>
import "C"

// Keep LLVM handle identity while letting emitters that only use these operations
// compile as ordinary Go. Cgo processes the shared binding rather than each user.
type llvmIRValueRef = C.LLVMValueRef
type llvmIRTypeRef = C.LLVMTypeRef
type llvmIRBlockRef = C.LLVMBasicBlockRef
type llvmIRBuilderRef = C.LLVMBuilderRef
type llvmIRContextRef = C.LLVMContextRef
type llvmIRMetadataRef = C.LLVMMetadataRef
type llvmIRChar = C.char
type llvmIRUnsigned = C.unsigned
type llvmIRUnsignedLongLong = C.ulonglong
type llvmIRBool = C.LLVMBool

func llvmBuildStore(builder llvmIRBuilderRef, value, pointer llvmIRValueRef) llvmIRValueRef {
	return C.LLVMBuildStore(builder, value, pointer)
}

func llvmBuildZExt(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildZExt(builder, value, typ, name)
}

func llvmConstInt(typ llvmIRTypeRef, value llvmIRUnsignedLongLong, signExtend llvmIRBool) llvmIRValueRef {
	return C.LLVMConstInt(typ, value, signExtend)
}

func llvmBuildExtractValue(builder llvmIRBuilderRef, aggregate llvmIRValueRef, index llvmIRUnsigned, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildExtractValue(builder, aggregate, index, name)
}

func llvmGetInsertBlock(builder llvmIRBuilderRef) llvmIRBlockRef {
	return C.LLVMGetInsertBlock(builder)
}

func llvmAppendBasicBlockInContext(context llvmIRContextRef, function llvmIRValueRef, name *llvmIRChar) llvmIRBlockRef {
	return C.LLVMAppendBasicBlockInContext(context, function, name)
}

func llvmBuildCondBr(builder llvmIRBuilderRef, condition llvmIRValueRef, yes, no llvmIRBlockRef) llvmIRValueRef {
	return C.LLVMBuildCondBr(builder, condition, yes, no)
}

func llvmPositionBuilderAtEnd(builder llvmIRBuilderRef, block llvmIRBlockRef) {
	C.LLVMPositionBuilderAtEnd(builder, block)
}

func llvmBuildBr(builder llvmIRBuilderRef, block llvmIRBlockRef) llvmIRValueRef {
	return C.LLVMBuildBr(builder, block)
}

func llvmBuildStructGEP2(builder llvmIRBuilderRef, typ llvmIRTypeRef, pointer llvmIRValueRef, index llvmIRUnsigned, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildStructGEP2(builder, typ, pointer, index, name)
}

func llvmGetUndef(typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMGetUndef(typ)
}

func llvmBuildLoad2(builder llvmIRBuilderRef, typ llvmIRTypeRef, pointer llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildLoad2(builder, typ, pointer, name)
}

func llvmBuildUnreachable(builder llvmIRBuilderRef) llvmIRValueRef {
	return C.LLVMBuildUnreachable(builder)
}

func llvmBuildInsertValue(builder llvmIRBuilderRef, aggregate, value llvmIRValueRef, index llvmIRUnsigned, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildInsertValue(builder, aggregate, value, index, name)
}

func llvmConstNull(typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMConstNull(typ)
}

func llvmBuildGEP2(builder llvmIRBuilderRef, typ llvmIRTypeRef, pointer llvmIRValueRef, indices *llvmIRValueRef, count llvmIRUnsigned, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildGEP2(builder, typ, pointer, indices, count, name)
}

func llvmBuildUDiv(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildUDiv(builder, left, right, name)
}

func llvmBuildRet(builder llvmIRBuilderRef, value llvmIRValueRef) llvmIRValueRef {
	return C.LLVMBuildRet(builder, value)
}

func llvmBuildRetVoid(builder llvmIRBuilderRef) llvmIRValueRef { return C.LLVMBuildRetVoid(builder) }

func llvmBuildPtrToInt(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildPtrToInt(builder, value, typ, name)
}
