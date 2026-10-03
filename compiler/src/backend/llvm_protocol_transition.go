//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// Retyping transfers the existing payload, never writes a runtime state tag.
// Semantic analysis has already checked graph authority and consumed the owner.
func (s *functionState) emitProtocolTransitionCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if !s.g.result.IsCompilerBuiltinHelperCall(expr, "transition") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("invalid checked protocol transition arity")
	}
	target := s.exprType(expr)
	targetLLVM, err := s.g.lowerType(target)
	if err != nil {
		return nil, nil, true, err
	}
	value, _, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	sourceLLVM := C.LLVMTypeOf(value)
	if sourceLLVM == targetLLVM {
		return value, target, true, nil
	}
	if C.LLVMGetTypeKind(sourceLLVM) != C.LLVMStructTypeKind || C.LLVMGetTypeKind(targetLLVM) != C.LLVMStructTypeKind {
		return nil, nil, true, fmt.Errorf("protocol transition requires direct aggregate lowering")
	}
	count := C.LLVMCountStructElementTypes(sourceLLVM)
	if count != C.LLVMCountStructElementTypes(targetLLVM) {
		return nil, nil, true, fmt.Errorf("protocol transition changes payload layout")
	}
	for i := C.unsigned(0); i < count; i++ {
		if C.LLVMStructGetTypeAtIndex(sourceLLVM, i) != C.LLVMStructGetTypeAtIndex(targetLLVM, i) {
			return nil, nil, true, fmt.Errorf("protocol transition changes payload field %d", i)
		}
	}
	result := C.LLVMGetUndef(targetLLVM)
	for i := C.unsigned(0); i < count; i++ {
		field := C.LLVMBuildExtractValue(s.builder, value, i, cStringFree("transition.payload"))
		result = C.LLVMBuildInsertValue(s.builder, result, field, i, cStringFree("transition.result"))
	}
	return result, target, true, nil
}
