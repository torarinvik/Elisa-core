//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) packedEnumFieldHandleValue(expr ast.Expr, objectType semantic.Type, enumType *semantic.EnumType) (llvmIRValueRef, error) {
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
func (s *functionState) emitRawMemcpy(dstPtr llvmIRValueRef, srcPtr llvmIRValueRef, byteCount uint64, name string) error {
	if byteCount == 0 {
		return nil
	}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.namedType("usize")
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
	byteCountValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(byteCount), 0)
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []llvmIRValueRef{dstPtr, srcPtr, byteCountValue}, name)
	return nil
}
func (s *functionState) emitByteOffsetPtr(basePtr llvmIRValueRef, byteOffset uint64, name string) (llvmIRValueRef, error) {
	i8Type := s.g.namedType("u8")
	i8LLVMType, err := s.g.lowerType(i8Type)
	if err != nil {
		return nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	indices := []llvmIRValueRef{llvmConstInt(usizeType, llvmIRUnsignedLongLong(byteOffset), 0)}
	return llvmBuildGEP2(s.builder, i8LLVMType, basePtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree(name)), nil
}
func (s *functionState) emitPackedSideTableFieldRead(handleValue llvmIRValueRef, enumType *semantic.EnumType, store *packedStoreBinding, fieldType semantic.Type, sideWordOffset uint64, wordCount uint64, origin packedReadOriginKey, name string) (llvmIRValueRef, error) {
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
func (s *functionState) emitPackedSideTableFieldReadWithOps(ops *packedStoreOps, handleValue llvmIRValueRef, enumType *semantic.EnumType, fieldType semantic.Type, sideWordOffset uint64, wordCount uint64, origin packedReadOriginKey, name string) (llvmIRValueRef, error) {
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
	llvmBuildStore(s.builder, llvmConstNull(fieldLLVMType), fieldPtr)
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	for i := uint64(0); i < wordCount; i++ {
		wordOffsetValue, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		wordValue, err := ops.loadSideWordAtOrigin(handleValue, llvmConstInt(wordOffsetValue, llvmIRUnsignedLongLong(sideWordOffset+i), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		wordPtr, err := s.createEntryAlloca(name+".word.tmp", s.g.namedType("uintptr"))
		if err != nil {
			return nil, err
		}
		llvmBuildStore(s.builder, wordValue, wordPtr)
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
