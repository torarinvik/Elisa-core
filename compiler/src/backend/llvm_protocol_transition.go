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
	value, source, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	result, actual, err := s.emitNamedStatePayloadRetype(value, source, target, "transition")
	return result, actual, true, err
}
