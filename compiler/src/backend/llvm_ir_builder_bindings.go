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

// Keep LLVM handle identity while letting emitters that only use these operations
// compile as ordinary Go. Cgo processes the shared binding rather than each user.
type llvmIRValueRef = C.LLVMValueRef

// llvmValueRef is the value handle shared by the backend emitters.
type llvmValueRef = llvmIRValueRef
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

func llvmBuildAdd(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildAdd(builder, left, right, name)
}
func llvmBuildMul(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildMul(builder, left, right, name)
}
func llvmBuildOr(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildOr(builder, left, right, name)
}
func llvmBuildShl(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildShl(builder, left, right, name)
}
func llvmBuildPhi(builder llvmIRBuilderRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildPhi(builder, typ, name)
}
func llvmAddIncoming(phi llvmIRValueRef, values *llvmIRValueRef, blocks *llvmIRBlockRef, count llvmIRUnsigned) {
	C.LLVMAddIncoming(phi, values, blocks, count)
}
func llvmBuildSwitch(builder llvmIRBuilderRef, value llvmIRValueRef, fallback llvmIRBlockRef, count llvmIRUnsigned) llvmIRValueRef {
	return C.LLVMBuildSwitch(builder, value, fallback, count)
}
func llvmAddCase(branch, value llvmIRValueRef, block llvmIRBlockRef) {
	C.LLVMAddCase(branch, value, block)
}

func llvmBuildCall2(builder llvmIRBuilderRef, typ llvmIRTypeRef, callee llvmIRValueRef, arguments *llvmIRValueRef, count llvmIRUnsigned, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildCall2(builder, typ, callee, arguments, count, name)
}
func llvmSetInstructionCallConv(call llvmIRValueRef, convention llvmIRUnsigned) {
	C.LLVMSetInstructionCallConv(call, convention)
}

type llvmIRIntPredicate = C.LLVMIntPredicate

const (
	llvmIntEQ  = C.LLVMIntEQ
	llvmIntNE  = C.LLVMIntNE
	llvmIntULT = C.LLVMIntULT
	llvmIntULE = C.LLVMIntULE
	llvmIntUGT = C.LLVMIntUGT
	llvmIntUGE = C.LLVMIntUGE
	llvmIntSLE = C.LLVMIntSLE
)

func llvmBuildICmp(builder llvmIRBuilderRef, predicate llvmIRIntPredicate, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildICmp(builder, predicate, left, right, name)
}

func llvmBuildSelect(builder llvmIRBuilderRef, condition, yes, no llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSelect(builder, condition, yes, no, name)
}

func llvmBuildAlloca(builder llvmIRBuilderRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildAlloca(builder, typ, name)
}

func llvmBuildAnd(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildAnd(builder, left, right, name)
}

func llvmBuildSub(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSub(builder, left, right, name)
}

func llvmBuildNot(builder llvmIRBuilderRef, value llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildNot(builder, value, name)
}

func llvmBuildIntToPtr(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildIntToPtr(builder, value, typ, name)
}

func llvmBuildIntCast2(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, signed llvmIRBool, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildIntCast2(builder, value, typ, signed, name)
}

func llvmGetBasicBlockTerminator(block llvmIRBlockRef) llvmIRValueRef {
	return C.LLVMGetBasicBlockTerminator(block)
}
