//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) packedEnumFieldHandleValue(expr ast.Expr, objectType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if refType, ok := objectType.(*semantic.RefType); ok {
		if refEnum, ok := refType.Elem.(*semantic.EnumType); ok && refEnum == enumType {
			refValue, _, err := s.emitExpr(expr, objectType)
			if err != nil {
				return nil, err
			}
			return s.loadValue(refValue, enumType, "packed.common.handle")
		}
	}
	handleValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, err
	}
	return handleValue, nil
}
func (s *functionState) emitRawMemcpy(dstPtr C.LLVMValueRef, srcPtr C.LLVMValueRef, byteCount uint64, name string) error {
	if byteCount == 0 {
		return nil
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	byteCountValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(byteCount), 0)
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstPtr, srcPtr, byteCountValue}, name)
	return nil
}
func (s *functionState) emitByteOffsetPtr(basePtr C.LLVMValueRef, byteOffset uint64, name string) (C.LLVMValueRef, error) {
	i8Type := s.g.result.NamedTypes["u8"]
	i8LLVMType, err := s.g.lowerType(i8Type)
	if err != nil {
		return nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{C.LLVMConstInt(usizeType, C.ulonglong(byteOffset), 0)}
	return C.LLVMBuildGEP2(s.builder, i8LLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name)), nil
}
func (s *functionState) emitPackedSideTableFieldRead(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, fieldType semantic.Type, sideWordOffset uint64, wordCount uint64, origin packedReadOriginKey, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("packed side-table field read requires packed enum metadata")
	}
	if !packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) {
		return nil, fmt.Errorf("packed enum %s side-tabled common fields require an index-based packed ABI", enumType.Name)
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s side-tabled common-field read requires store context", enumType.Name)
	}
	return s.emitPackedSideTableFieldReadWithOps(ops, handleValue, enumType, fieldType, sideWordOffset, wordCount, origin, name)
}

// emitPackedSideTableFieldReadWithOps is the ops-based core of the side-table read: it assembles the
// field value from its parallel side column(s) given an already-resolved store ops. Callers that hold
// a *packedStoreOps directly (the column scan) use it without a packedStoreBinding round-trip.
func (s *functionState) emitPackedSideTableFieldReadWithOps(ops *packedStoreOps, handleValue C.LLVMValueRef, enumType *semantic.EnumType, fieldType semantic.Type, sideWordOffset uint64, wordCount uint64, origin packedReadOriginKey, name string) (C.LLVMValueRef, error) {
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return nil, err
	}
	if fieldSizeBytes == 0 || wordCount == 0 {
		return s.zeroValue(fieldType)
	}
	fieldPtr, err := s.createEntryAlloca(name+".tmp", fieldType)
	if err != nil {
		return nil, err
	}
	fieldLLVMType, err := s.g.lowerType(fieldType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstNull(fieldLLVMType), fieldPtr)
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	for i := uint64(0); i < wordCount; i++ {
		wordOffsetValue, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		wordValue, err := ops.loadSideWordAtOrigin(handleValue, C.LLVMConstInt(wordOffsetValue, C.ulonglong(sideWordOffset+i), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		wordPtr, err := s.createEntryAlloca(name+".word.tmp", s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		C.LLVMBuildStore(s.builder, wordValue, wordPtr)
		dstPtr, err := s.emitByteOffsetPtr(fieldPtr, i*wordBytes, name+".dst")
		if err != nil {
			return nil, err
		}
		remainingBytes := fieldSizeBytes - i*wordBytes
		copyBytes := wordBytes
		if remainingBytes < copyBytes {
			copyBytes = remainingBytes
		}
		if err := s.emitRawMemcpy(dstPtr, wordPtr, copyBytes, name+".copy"); err != nil {
			return nil, err
		}
	}
	return s.loadValue(fieldPtr, fieldType, name+".value")
}
