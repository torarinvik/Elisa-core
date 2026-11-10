//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) buildDynArrayViewValue(arrayValue llvmIRValueRef, arrayType *semantic.DArrayType, viewType *semantic.ViewType, name string) (llvmIRValueRef, error) {
	if s == nil || arrayType == nil || viewType == nil {
		return nil, fmt.Errorf("missing dynamic array view conversion metadata")
	}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	dataValue := llvmBuildExtractValue(s.builder, arrayValue, 0, cStringFree(name+".data"))
	lenValue := llvmBuildExtractValue(s.builder, arrayValue, 1, cStringFree(name+".len"))
	viewValue := llvmGetUndef(viewLLVMType)
	viewValue = llvmBuildInsertValue(s.builder, viewValue, dataValue, 0, cStringFree(name+".view.data"))
	viewValue = llvmBuildInsertValue(s.builder, viewValue, lenValue, 1, cStringFree(name+".view.len"))
	return viewValue, nil
}
