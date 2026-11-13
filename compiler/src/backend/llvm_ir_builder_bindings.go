//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);

// These calls synchronously consume pointer arrays and copy their LLVM handles
// into C++-owned operands. They retain neither the caller's array nor its name
// buffer. The compiler installs no Go LLVM diagnostic/yield callbacks.
// Keep this list narrow: owning-buffer and callback-registration APIs do not
// have the same contract. Both hints are needed for Go's nonescaping wrapper.
#cgo noescape LLVMAddIncoming
#cgo nocallback LLVMAddIncoming
#cgo noescape LLVMBuildGEP2
#cgo nocallback LLVMBuildGEP2

// elisacoreValueIsTriviallyValidPtr reports whether an LLVM value used as a load
// address is provably a valid (non-null, non-near-null) pointer at compile time, so
// the null-deref guard can skip it: stack slots (alloca), module globals/functions,
// and constants (e.g. a constant GEP off a global) all have addresses far from the
// near-null zone. Pointers derived at runtime (GEP off a loaded base, inttoptr, call
// results) return 0 and get guarded.
static int elisacoreValueIsTriviallyValidPtr(LLVMValueRef v) {
	if (v == NULL) {
		return 1;
	}
	if (LLVMIsAAllocaInst(v) != NULL) {
		return 1;
	}
	if (LLVMIsAGlobalValue(v) != NULL) {
		return 1;
	}
	if (LLVMIsConstant(v)) {
		return 1;
	}
	return 0;
}

// Allow FP contraction (a*b+c -> fma) on a floating-point instruction, matching clang's default
// -ffp-contract=on. Only the AllowContract flag is set; no reassociation / no-nan / no-inf, so this
// does not change value semantics beyond the single-rounding fused multiply-add the C ABI permits.
static void elisacoreSetFPContract(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract);
	}
}

// Allow contraction AND reciprocal (a/b -> a * (1/b)) on a floating-point instruction, matching
// clang's -ffp-contract=fast -freciprocal-math. For a loop-invariant divisor this lets LICM hoist
// the single reciprocal out of the loop, turning a per-iteration fdiv (slow, poorly pipelined) into
// a multiply. This relaxes division rounding by up to ~1 ulp, the same relaxed-FP tier as contract.
static void elisacoreSetFPContractReciprocal(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract | LLVMFastMathAllowReciprocal);
	}
}

// Allow reassociation + contraction on an FP instruction, WITHOUT nnan/ninf/nsz. This is the
// "defined tree-order reduction" tier: it lets the vectorizer re-bracket a reduction into parallel
// partial sums (SIMD tree reduction) while leaving NaN/Inf/signed-zero behaviour at IEEE. Used on a
// comprehension fold's accumulator update — a fold's reduction order is defined as a tree, not strict
// left-to-right, so this reassociation is the defined semantics, not an unsafe opt-in (docs/79).
static void elisacoreSetFPReassoc(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowReassoc | LLVMFastMathAllowContract);
	}
}

// Set ALL fast-math flags (reassoc, nnan, ninf, nsz, arcp, contract) on an FP instruction, matching
// clang -ffast-math. Enables FP reassociation -> auto-vectorization of reduction/elementwise loops.
// Used only inside functions annotated @fast_math (opt-in; reorders FP, results may differ).
static void elisacoreSetFPFast(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAll);
	}
}
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
	llvmIntSGE = C.LLVMIntSGE
	llvmIntSGT = C.LLVMIntSGT
	llvmIntSLT = C.LLVMIntSLT
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

func llvmBuildXor(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildXor(builder, left, right, name)
}

func llvmBuildLShr(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildLShr(builder, left, right, name)
}

func llvmBuildTrunc(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildTrunc(builder, value, typ, name)
}

func llvmBuildBitCast(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildBitCast(builder, value, typ, name)
}

type llvmIRRealPredicate = C.LLVMRealPredicate

const llvmRealOEQ = C.LLVMRealOEQ

func llvmBuildFCmp(builder llvmIRBuilderRef, predicate llvmIRRealPredicate, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFCmp(builder, predicate, left, right, name)
}

func llvmBuildNeg(builder llvmIRBuilderRef, value llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildNeg(builder, value, name)
}

func llvmBuildGlobalStringPtr(builder llvmIRBuilderRef, value, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildGlobalStringPtr(builder, value, name)
}

func llvmConstNot(value llvmIRValueRef) llvmIRValueRef { return C.LLVMConstNot(value) }

func llvmSetAlignment(value llvmIRValueRef, bytes llvmIRUnsigned) { C.LLVMSetAlignment(value, bytes) }

func llvmGetNumSuccessors(term llvmIRValueRef) llvmIRUnsigned { return C.LLVMGetNumSuccessors(term) }

func llvmGetSuccessor(term llvmIRValueRef, index llvmIRUnsigned) llvmIRBlockRef {
	return C.LLVMGetSuccessor(term, index)
}

func llvmSetBranchWeights(branch llvmIRValueRef, context llvmIRContextRef, trueWeight, falseWeight llvmIRUnsigned) {
	C.elisa_coreSetBranchWeights(branch, context, trueWeight, falseWeight)
}

func llvmCreateBuilderInContext(context llvmIRContextRef) llvmIRBuilderRef {
	return C.LLVMCreateBuilderInContext(context)
}

func llvmDisposeBuilder(builder llvmIRBuilderRef) {
	C.LLVMDisposeBuilder(builder)
}

func llvmGetEntryBasicBlock(function llvmIRValueRef) llvmIRBlockRef {
	return C.LLVMGetEntryBasicBlock(function)
}

func llvmGetFirstInstruction(block llvmIRBlockRef) llvmIRValueRef {
	return C.LLVMGetFirstInstruction(block)
}

func llvmGetLastInstruction(block llvmIRBlockRef) llvmIRValueRef {
	return C.LLVMGetLastInstruction(block)
}

func llvmGetNextInstruction(instruction llvmIRValueRef) llvmIRValueRef {
	return C.LLVMGetNextInstruction(instruction)
}

func llvmGetInstructionParent(instruction llvmIRValueRef) llvmIRBlockRef {
	return C.LLVMGetInstructionParent(instruction)
}

func llvmPositionBuilderBefore(builder llvmIRBuilderRef, instruction llvmIRValueRef) {
	C.LLVMPositionBuilderBefore(builder, instruction)
}

func llvmBuildSExt(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSExt(builder, value, typ, name)
}

const (
	llvmRealOGE = C.LLVMRealOGE
	llvmRealOGT = C.LLVMRealOGT
	llvmRealOLE = C.LLVMRealOLE
	llvmRealOLT = C.LLVMRealOLT
	llvmRealUNE = C.LLVMRealUNE
)

func llvmBuildFNeg(builder llvmIRBuilderRef, value llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFNeg(builder, value, name)
}

func llvmBuildVAArg(builder llvmIRBuilderRef, list llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildVAArg(builder, list, typ, name)
}

func llvmBuildIsNull(builder llvmIRBuilderRef, value llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildIsNull(builder, value, name)
}

func llvmBuildURem(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildURem(builder, left, right, name)
}

func llvmGetCondition(branch llvmIRValueRef) llvmIRValueRef {
	return C.LLVMGetCondition(branch)
}

func llvmIsABranchInst(value llvmIRValueRef) llvmIRValueRef {
	return C.LLVMIsABranchInst(value)
}

func llvmIsAConstantInt(value llvmIRValueRef) llvmIRValueRef {
	return C.LLVMIsAConstantInt(value)
}

func llvmIsConditional(branch llvmIRValueRef) llvmIRBool {
	return C.LLVMIsConditional(branch)
}

func llvmBuildAShr(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildAShr(builder, left, right, name)
}

func llvmBuildFAdd(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFAdd(builder, left, right, name)
}

func llvmBuildFDiv(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFDiv(builder, left, right, name)
}

func llvmBuildFMul(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFMul(builder, left, right, name)
}

func llvmBuildFSub(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFSub(builder, left, right, name)
}

func llvmBuildSDiv(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSDiv(builder, left, right, name)
}

func llvmBuildSRem(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSRem(builder, left, right, name)
}

func llvmGetOperand(value llvmIRValueRef, index llvmIRUnsigned) llvmIRValueRef {
	return C.LLVMGetOperand(value, index)
}

func llvmIsALoadInst(value llvmIRValueRef) llvmIRValueRef {
	return C.LLVMIsALoadInst(value)
}

func llvmSetIsInBounds(value llvmIRValueRef, inBounds llvmIRBool) {
	C.LLVMSetIsInBounds(value, inBounds)
}

func llvmValueIsTriviallyValidPtr(value llvmIRValueRef) C.int {
	return C.elisacoreValueIsTriviallyValidPtr(value)
}

func llvmBuildNUWMul(builder llvmIRBuilderRef, left, right llvmIRValueRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildNUWMul(builder, left, right, name)
}

func llvmSetFPContract(value llvmIRValueRef) {
	C.elisacoreSetFPContract(value)
}

func llvmSetFPContractReciprocal(value llvmIRValueRef) {
	C.elisacoreSetFPContractReciprocal(value)
}

func llvmSetFPFast(value llvmIRValueRef) {
	C.elisacoreSetFPFast(value)
}

func llvmSetFPReassoc(value llvmIRValueRef) {
	C.elisacoreSetFPReassoc(value)
}

func llvmBuildFPExt(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFPExt(builder, value, typ, name)
}

func llvmBuildFPToSI(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFPToSI(builder, value, typ, name)
}

func llvmBuildFPToUI(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFPToUI(builder, value, typ, name)
}

func llvmBuildFPTrunc(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildFPTrunc(builder, value, typ, name)
}

func llvmBuildSIToFP(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildSIToFP(builder, value, typ, name)
}

func llvmBuildUIToFP(builder llvmIRBuilderRef, value llvmIRValueRef, typ llvmIRTypeRef, name *llvmIRChar) llvmIRValueRef {
	return C.LLVMBuildUIToFP(builder, value, typ, name)
}

// The auto-arena cleanup analysis inspects uses without requiring its own cgo preamble.
type llvmIRUseRef = C.LLVMUseRef

func llvmGetFirstUse(value llvmIRValueRef) llvmIRUseRef { return C.LLVMGetFirstUse(value) }
func llvmGetNextUse(use llvmIRUseRef) llvmIRUseRef      { return C.LLVMGetNextUse(use) }
func llvmGetUser(use llvmIRUseRef) llvmIRValueRef       { return C.LLVMGetUser(use) }
func llvmIsStore(value llvmIRValueRef) bool             { return C.LLVMIsAStoreInst(value) != nil }
func llvmIsCall(value llvmIRValueRef) bool              { return C.LLVMIsACallInst(value) != nil }
func llvmIsVolatile(value llvmIRValueRef) bool          { return C.LLVMGetVolatile(value) != 0 }
func llvmIsNonAtomic(value llvmIRValueRef) bool {
	return C.LLVMGetOrdering(value) == C.LLVMAtomicOrderingNotAtomic
}
func llvmIsNull(value llvmIRValueRef) bool                { return C.LLVMIsNull(value) != 0 }
func llvmCalledValue(value llvmIRValueRef) llvmIRValueRef { return C.LLVMGetCalledValue(value) }
func llvmNumCallArgs(value llvmIRValueRef) llvmIRUnsigned { return C.LLVMGetNumArgOperands(value) }
func llvmEraseInstruction(value llvmIRValueRef)           { C.LLVMInstructionEraseFromParent(value) }
