//go:build cgo

package backend

/*
#include <llvm-c/Core.h>

// A GEP into a packed record carries no natural-alignment guarantee for its
// fields. Opaque pointers lose that fact at LLVMBuildLoad2/LLVMBuildStore.
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
*/
import "C"

// Apply once before optimization, while the lowered GEP type path is intact.
// LLVM can subsequently strengthen alignment if it proves a particular access
// aligned. This covers direct packed-record addresses, including nested fields;
// it does not change the language's contracts for escaped scalar references.
func (g *llvmGenerator) alignPackedRecordAccesses() {
	if g.hasPackedRecords {
		C.elisacoreAlignPackedRecordAccesses(g.module)
	}
}
