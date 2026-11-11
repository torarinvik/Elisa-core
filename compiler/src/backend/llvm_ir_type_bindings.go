//go:build cgo

package backend

/*
#include <llvm-c/Core.h>

static int elisacoreConstIntGetZExtValue(LLVMValueRef Value, unsigned long long* Out) {
	if (LLVMIsAConstantInt(Value) == NULL) {
		return 0;
	}
	*Out = LLVMConstIntGetZExtValue(Value);
	return 1;
}

// elisacoreConstIndexInBounds reports whether both the index and the count are
// compile-time constant integers and index < count. When true the index bounds
// guard is statically satisfied (the comparison would fold to `i1 true`), so emitting
// a runtime branch is pure overhead and — by splitting the basic block — defeats
// per-block read CSE at the indexed site. Returns 0 if either operand is non-constant.
static int elisacoreConstIndexInBounds(LLVMValueRef index, LLVMValueRef count) {
	if (index == NULL || count == NULL) {
		return 0;
	}
	if (LLVMIsAConstantInt(index) == NULL || LLVMIsAConstantInt(count) == NULL) {
		return 0;
	}
	return LLVMConstIntGetZExtValue(index) < LLVMConstIntGetZExtValue(count) ? 1 : 0;
}
*/
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

type llvmIRSize = C.size_t
type llvmIRUnnamedAddr = C.LLVMUnnamedAddr

const (
	llvmExternalLinkage    = C.LLVMExternalLinkage
	llvmLinkOnceAnyLinkage = C.LLVMLinkOnceAnyLinkage
	llvmGlobalUnnamedAddr  = C.LLVMGlobalUnnamedAddr
)

func llvmAddGlobal(module llvmIRModuleRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMAddGlobal(module, typ, name)
}

func llvmAddFunction(module llvmIRModuleRef, name *llvmIRChar, typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMAddFunction(module, name, typ)
}

func llvmAddAlias2(module llvmIRModuleRef, typ llvmIRTypeRef, addressSpace llvmIRUnsigned, aliasee llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMAddAlias2(module, typ, addressSpace, aliasee, name)
}

func llvmGetParam(function llvmIRValueRef, index llvmIRUnsigned) llvmIRValueRef {
	return C.LLVMGetParam(function, index)
}

func llvmFunctionType(result llvmIRTypeRef, params *llvmIRTypeRef, count llvmIRUnsigned, variadic llvmIRBool) llvmIRTypeRef {
	return C.LLVMFunctionType(result, params, count, variadic)
}

func llvmConstAllOnes(typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMConstAllOnes(typ)
}

func llvmConstSub(left, right llvmIRValueRef) llvmIRValueRef {
	return C.LLVMConstSub(left, right)
}

func llvmConstArray2(element llvmIRTypeRef, values *llvmIRValueRef, count llvmIRUint64) llvmIRValueRef {
	return C.LLVMConstArray2(element, values, count)
}

func llvmConstNamedStruct(typ llvmIRTypeRef, values *llvmIRValueRef, count llvmIRUnsigned) llvmIRValueRef {
	return C.LLVMConstNamedStruct(typ, values, count)
}

func llvmConstInBoundsGEP2(typ llvmIRTypeRef, base llvmIRValueRef, indices *llvmIRValueRef, count llvmIRUnsigned) llvmIRValueRef {
	return C.LLVMConstInBoundsGEP2(typ, base, indices, count)
}

func llvmSetUnnamedAddress(global llvmIRValueRef, kind llvmIRUnnamedAddr) {
	C.LLVMSetUnnamedAddress(global, kind)
}

func llvmLookupIntrinsicID(name *llvmIRChar, length llvmIRSize) llvmIRUnsigned {
	return C.LLVMLookupIntrinsicID(name, length)
}

func llvmGetIntrinsicDeclaration(module llvmIRModuleRef, id llvmIRUnsigned, types *llvmIRTypeRef, count llvmIRSize) llvmIRValueRef {
	return C.LLVMGetIntrinsicDeclaration(module, id, types, count)
}

func llvmPrintTypeToString(typ llvmIRTypeRef) *llvmIRChar {
	return C.LLVMPrintTypeToString(typ)
}

const llvmWeakAnyLinkage = C.LLVMWeakAnyLinkage

func llvmGetLinkage(global llvmIRValueRef) llvmIRLinkage {
	return C.LLVMGetLinkage(global)
}

func llvmGetValueName(value llvmIRValueRef) *llvmIRChar {
	return C.LLVMGetValueName(value)
}

func llvmConstIntGetZExtValue(value llvmIRValueRef) llvmIRUnsignedLongLong {
	return C.LLVMConstIntGetZExtValue(value)
}

func llvmConstStringInContext(context llvmIRContextRef, data *llvmIRChar, length llvmIRUnsigned, noNull llvmIRBool) llvmIRValueRef {
	return C.LLVMConstStringInContext(context, data, length, noNull)
}

func llvmConstIntToPtr(value llvmIRValueRef, typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMConstIntToPtr(value, typ)
}

func llvmConstPtrToInt(value llvmIRValueRef, typ llvmIRTypeRef) llvmIRValueRef {
	return C.LLVMConstPtrToInt(value, typ)
}

const llvmStructTypeKind = C.LLVMStructTypeKind

func llvmCountStructElementTypes(typ llvmIRTypeRef) llvmIRUnsigned {
	return C.LLVMCountStructElementTypes(typ)
}

func llvmIsPackedStruct(typ llvmIRTypeRef) llvmIRBool {
	return C.LLVMIsPackedStruct(typ)
}

func llvmTryConstIntGetZExtValue(value llvmIRValueRef, result *llvmIRUnsignedLongLong) C.int {
	return C.elisacoreConstIntGetZExtValue(value, result)
}

func llvmConstIndexInBounds(index, count llvmIRValueRef) C.int {
	return C.elisacoreConstIndexInBounds(index, count)
}
