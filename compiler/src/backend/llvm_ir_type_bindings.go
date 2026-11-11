//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
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

static void elisacoreAddAlwaysInlineAttr(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen) {
	unsigned Kind = LLVMGetEnumAttributeKindForName(Name, NameLen);
	if (Kind == 0) {
		return;
	}
	LLVMAttributeRef Attr = LLVMCreateEnumAttribute(Ctx, Kind, 0);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
}

static void elisacoreAddFunctionStringAttr(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen, const char* Value, size_t ValueLen) {
	LLVMAttributeRef Attr = LLVMCreateStringAttribute(Ctx, Name, NameLen, Value, ValueLen);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
}

static LLVMValueRef elisacoreGetInlineAsmLocal(LLVMTypeRef Ty, const char* AsmString, size_t AsmStringSize, const char* Constraints, size_t ConstraintsSize, LLVMBool HasSideEffects, LLVMBool IsAlignStack) {
	return LLVMGetInlineAsm(Ty, (char*)AsmString, AsmStringSize, (char*)Constraints, ConstraintsSize, HasSideEffects, IsAlignStack, LLVMInlineAsmDialectATT, 0);
}

static int elisacorePackedAddress(LLVMValueRef p, LLVMValueRef *path, unsigned depth) {
 if (!p) return 0;
 for (unsigned i = 0; i < depth; ++i) if (path[i] == p) return 0;
 if (depth == 64) return 1; // conservative for unusually deep address expressions
 path[depth++] = p;
 int instruction = LLVMIsAInstruction(p) != 0;
 int expression = LLVMIsAConstantExpr(p) != 0;
 if (!instruction && !expression) return 0;
 LLVMOpcode op = instruction ? LLVMGetInstructionOpcode(p) : LLVMGetConstOpcode(p);
 if (op == LLVMGetElementPtr) {
  LLVMTypeRef type = LLVMGetGEPSourceElementType(p);
  unsigned count = LLVMGetNumOperands(p);
  for (unsigned i = 2; i < count; ++i) {
   LLVMTypeKind kind = LLVMGetTypeKind(type);
   if (kind == LLVMStructTypeKind) {
    if (LLVMIsPackedStruct(type)) return 1;
    LLVMValueRef index = LLVMGetOperand(p, i);
    if (!LLVMIsAConstantInt(index)) return 1;
    unsigned field = (unsigned)LLVMConstIntGetZExtValue(index);
    if (field >= LLVMCountStructElementTypes(type)) return 1;
    type = LLVMStructGetTypeAtIndex(type, field);
   } else if (kind == LLVMArrayTypeKind || kind == LLVMVectorTypeKind || kind == LLVMScalableVectorTypeKind) {
    type = LLVMGetElementType(type);
   } else { break; }
  }
  if (LLVMGetTypeKind(type) == LLVMStructTypeKind && LLVMIsPackedStruct(type)) return 1;
  return elisacorePackedAddress(LLVMGetOperand(p, 0), path, depth);
 }
 if (op == LLVMBitCast || op == LLVMAddrSpaceCast)
  return elisacorePackedAddress(LLVMGetOperand(p, 0), path, depth);
 if (op == LLVMSelect)
  return elisacorePackedAddress(LLVMGetOperand(p, 1), path, depth) || elisacorePackedAddress(LLVMGetOperand(p, 2), path, depth);
 if (op == LLVMPHI) {
  for (unsigned i = 0; i < LLVMCountIncoming(p); ++i)
   if (elisacorePackedAddress(LLVMGetIncomingValue(p, i), path, depth)) return 1;
 }
 return 0;
}

static void elisacoreAlignPackedRecordAccesses(LLVMModuleRef module) {
 LLVMValueRef path[64];
 for (LLVMValueRef f = LLVMGetFirstFunction(module); f; f = LLVMGetNextFunction(f)) {
  for (LLVMBasicBlockRef b = LLVMGetFirstBasicBlock(f); b; b = LLVMGetNextBasicBlock(b)) {
   for (LLVMValueRef i = LLVMGetFirstInstruction(b); i; i = LLVMGetNextInstruction(i)) {
    LLVMOpcode op = LLVMGetInstructionOpcode(i);
    LLVMValueRef p = 0;
    if (op == LLVMLoad) p = LLVMGetOperand(i, 0);
    if (op == LLVMStore) p = LLVMGetOperand(i, 1);
    if (p && elisacorePackedAddress(p, path, 0)) LLVMSetAlignment(i, 1);
   }
  }
 }
}

// 1 when fnType is `void(i32)` (want_int_param) or `void()`, non-variadic.
static int elisacoreLibcNoreturnSignature(LLVMTypeRef fnType, int want_int_param) {
	if (LLVMIsFunctionVarArg(fnType)) {
		return 0;
	}
	if (LLVMGetTypeKind(LLVMGetReturnType(fnType)) != LLVMVoidTypeKind) {
		return 0;
	}
	unsigned n = LLVMCountParamTypes(fnType);
	if (!want_int_param) {
		return n == 0;
	}
	if (n != 1) {
		return 0;
	}
	LLVMTypeRef param;
	LLVMGetParamTypes(fnType, &param);
	return LLVMGetTypeKind(param) == LLVMIntegerTypeKind && LLVMGetIntTypeWidth(param) == 32;
}

static void elisacoreAddNoreturnAttr(LLVMContextRef ctx, LLVMValueRef fn) {
	unsigned kind = LLVMGetEnumAttributeKindForName("noreturn", strlen("noreturn"));
	if (kind != 0) {
		LLVMAddAttributeAtIndex(fn, LLVMAttributeFunctionIndex, LLVMCreateEnumAttribute(ctx, kind, 0));
	}
}

static void elisacorePanicReportAddEnumAttr(LLVMContextRef ctx, LLVMValueRef fn, const char *name, uint64_t value) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, strlen(name));
	if (kind == 0) {
		return;
	}
	LLVMAddAttributeAtIndex(fn, LLVMAttributeFunctionIndex, LLVMCreateEnumAttribute(ctx, kind, value));
}

// Create a type attribute (e.g. "byval"/"sret") for the given type.
static LLVMAttributeRef elisacoreCreateTypeAttr(LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, nameLen);
	if (kind == 0) {
		return NULL;
	}
	return LLVMCreateTypeAttribute(ctx, kind, ty);
}

static void elisacoreAddFuncTypeAttr(LLVMValueRef fn, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	LLVMAttributeRef attr = elisacoreCreateTypeAttr(ctx, name, nameLen, ty);
	if (attr != NULL) {
		LLVMAddAttributeAtIndex(fn, index, attr);
	}
}

static void elisacoreAddCallTypeAttr(LLVMValueRef call, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	LLVMAttributeRef attr = elisacoreCreateTypeAttr(ctx, name, nameLen, ty);
	if (attr != NULL) {
		LLVMAddCallSiteAttribute(call, index, attr);
	}
}

// Add a value-less enum attribute (e.g. "noalias") to a function at the given
// attribute index.
static void elisacoreAddFuncEnumAttr(LLVMValueRef fn, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, nameLen);
	if (kind == 0) {
		return;
	}
	LLVMAttributeRef attr = LLVMCreateEnumAttribute(ctx, kind, 0);
	LLVMAddAttributeAtIndex(fn, index, attr);
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

const (
	llvmCCallConv          = C.LLVMCCallConv
	llvmColdCallConv       = C.LLVMColdCallConv
	llvmFastCallConv       = C.LLVMFastCallConv
	llvmX86StdcallCallConv = C.LLVMX86StdcallCallConv
)

func llvmContextCreate() llvmIRContextRef         { return C.LLVMContextCreate() }
func llvmContextDispose(context llvmIRContextRef) { C.LLVMContextDispose(context) }

func llvmModuleCreateWithNameInContext(name *llvmIRChar, context llvmIRContextRef) llvmIRModuleRef {
	return C.LLVMModuleCreateWithNameInContext(name, context)
}

func llvmDisposeModule(module llvmIRModuleRef) { C.LLVMDisposeModule(module) }
func llvmDisposeMessage(message *llvmIRChar)   { C.LLVMDisposeMessage(message) }

func llvmSetFunctionCallConv(function llvmIRValueRef, convention llvmIRUnsigned) {
	C.LLVMSetFunctionCallConv(function, convention)
}

func llvmInt16TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt16TypeInContext(context)
}

func llvmDoubleTypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMDoubleTypeInContext(context)
}

func llvmFloatTypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMFloatTypeInContext(context)
}

func llvmStructCreateNamed(context llvmIRContextRef, name *llvmIRChar) llvmIRTypeRef {
	return C.LLVMStructCreateNamed(context, name)
}

func llvmAlignPackedRecordAccesses(module llvmIRModuleRef) {
	C.elisacoreAlignPackedRecordAccesses(module)
}

func llvmAppendModuleInlineAsm(module llvmIRModuleRef, text *llvmIRChar, length llvmIRSize) {
	C.LLVMAppendModuleInlineAsm(module, text, length)
}

func llvmGetInlineAsmLocal(typ llvmIRTypeRef, text *llvmIRChar, textLen llvmIRSize, constraints *llvmIRChar, constraintsLen llvmIRSize, sideEffects, alignStack llvmIRBool) llvmIRValueRef {
	return C.elisacoreGetInlineAsmLocal(typ, text, textLen, constraints, constraintsLen, sideEffects, alignStack)
}

func llvmAddAlwaysInlineAttr(context llvmIRContextRef, function llvmIRValueRef, name *llvmIRChar, nameLen llvmIRSize) {
	C.elisacoreAddAlwaysInlineAttr(context, function, name, nameLen)
}

func llvmAddFunctionStringAttr(context llvmIRContextRef, function llvmIRValueRef, name *llvmIRChar, nameLen llvmIRSize, value *llvmIRChar, valueLen llvmIRSize) {
	C.elisacoreAddFunctionStringAttr(context, function, name, nameLen, value, valueLen)
}

func llvmIntrinsicIsOverloaded(id llvmIRUnsigned) llvmIRBool {
	return C.LLVMIntrinsicIsOverloaded(id)
}

func llvmInt128TypeInContext(context llvmIRContextRef) llvmIRTypeRef {
	return C.LLVMInt128TypeInContext(context)
}

func llvmIntrinsicGetType(context llvmIRContextRef, id llvmIRUnsigned, types *llvmIRTypeRef, count llvmIRSize) llvmIRTypeRef {
	return C.LLVMIntrinsicGetType(context, id, types, count)
}

func llvmAddNoreturnAttr(context llvmIRContextRef, function llvmIRValueRef) {
	C.elisacoreAddNoreturnAttr(context, function)
}

func llvmLibcNoreturnSignature(typ llvmIRTypeRef, wantIntParam llvmIRInt) llvmIRInt {
	return C.elisacoreLibcNoreturnSignature(typ, wantIntParam)
}

func llvmPanicReportAddEnumAttr(context llvmIRContextRef, function llvmIRValueRef, name *llvmIRChar, value llvmIRUint64) {
	C.elisacorePanicReportAddEnumAttr(context, function, name, value)
}

func llvmAddFuncTypeAttr(value llvmIRValueRef, index llvmIRUnsigned, context llvmIRContextRef, name *llvmIRChar, nameLen llvmIRSize, typ llvmIRTypeRef) {
	C.elisacoreAddFuncTypeAttr(value, index, context, name, nameLen, typ)
}

func llvmAddCallTypeAttr(value llvmIRValueRef, index llvmIRUnsigned, context llvmIRContextRef, name *llvmIRChar, nameLen llvmIRSize, typ llvmIRTypeRef) {
	C.elisacoreAddCallTypeAttr(value, index, context, name, nameLen, typ)
}

func llvmAddFuncEnumAttr(value llvmIRValueRef, index llvmIRUnsigned, context llvmIRContextRef, name *llvmIRChar, nameLen llvmIRSize) {
	C.elisacoreAddFuncEnumAttr(value, index, context, name, nameLen)
}

type llvmIRInt = C.int

const llvmVoidTypeKind = C.LLVMVoidTypeKind

func llvmGetNamedGlobal(module llvmIRModuleRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMGetNamedGlobal(module, name)
}
