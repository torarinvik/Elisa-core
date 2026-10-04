//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
	"fmt"
)

func namedStatePayloadFamily(typ semantic.Type) *semantic.StructType {
	switch value := semantic.StripAggregateStateType(typ).(type) {
	case *semantic.StructType:
		if len(value.NamedStateCases) != 0 {
			return value
		}
	case *semantic.GenericInstanceType:
		if base, ok := value.Base.(*semantic.StructType); ok && len(base.NamedStateCases) != 0 {
			return base
		}
	}
	return nil
}

// Only checked semantic state changes reach this helper. No runtime tag or
// reinterpret cast is introduced: each field must keep its exact LLVM type.
func (s *functionState) emitNamedStatePayloadRetype(value C.LLVMValueRef, source, target semantic.Type, label string) (C.LLVMValueRef, semantic.Type, error) {
	targetLLVM, err := s.g.lowerType(target)
	if err != nil {
		return nil, nil, err
	}
	sourceLLVM := C.LLVMTypeOf(value)
	if sourceLLVM == targetLLVM {
		return value, target, nil
	}
	sourceFamily, targetFamily := namedStatePayloadFamily(source), namedStatePayloadFamily(target)
	if sourceFamily == nil || targetFamily == nil || sourceFamily.Name != targetFamily.Name {
		return nil, nil, fmt.Errorf("%s changes the named-state family", label)
	}
	if C.LLVMGetTypeKind(sourceLLVM) != C.LLVMStructTypeKind || C.LLVMGetTypeKind(targetLLVM) != C.LLVMStructTypeKind {
		return nil, nil, fmt.Errorf("%s requires direct aggregate lowering", label)
	}
	count := C.LLVMCountStructElementTypes(sourceLLVM)
	if count != C.LLVMCountStructElementTypes(targetLLVM) || C.LLVMIsPackedStruct(sourceLLVM) != C.LLVMIsPackedStruct(targetLLVM) {
		return nil, nil, fmt.Errorf("%s changes payload layout", label)
	}
	for i := C.unsigned(0); i < count; i++ {
		if C.LLVMStructGetTypeAtIndex(sourceLLVM, i) != C.LLVMStructGetTypeAtIndex(targetLLVM, i) {
			return nil, nil, fmt.Errorf("%s changes payload field %d", label, i)
		}
	}
	result := C.LLVMGetUndef(targetLLVM)
	for i := C.unsigned(0); i < count; i++ {
		field := C.LLVMBuildExtractValue(s.builder, value, i, cStringFree(label+".payload"))
		result = C.LLVMBuildInsertValue(s.builder, result, field, i, cStringFree(label+".result"))
	}
	return result, target, nil
}
