#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight) {
	LLVMMetadataRef operands[3];
	operands[0] = LLVMMDStringInContext2(ctx, "branch_weights", 14);
	operands[1] = LLVMValueAsMetadata(LLVMConstInt(LLVMInt32TypeInContext(ctx), trueWeight, 0));
	operands[2] = LLVMValueAsMetadata(LLVMConstInt(LLVMInt32TypeInContext(ctx), falseWeight, 0));
	LLVMMetadataRef metadata = LLVMMDNodeInContext2(ctx, operands, 3);
	LLVMValueRef metadataValue = LLVMMetadataAsValue(ctx, metadata);
	unsigned kindID = LLVMGetMDKindIDInContext(ctx, "prof", 4);
	LLVMSetMetadata(branch, kindID, metadataValue);
}
