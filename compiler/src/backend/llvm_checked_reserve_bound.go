//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (s *functionState) emitReserveBoundExpr(expr ast.Expr, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	if reserveBoundNeedsCheckedArithmetic(expr) {
		return s.emitCheckedUSizeReserveBoundExpr(expr, name)
	}
	value, _, err := s.emitExpr(expr, usizeType)
	return value, err
}

func reserveBoundNeedsCheckedArithmetic(expr ast.Expr) bool {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return reserveBoundNeedsCheckedArithmetic(paren.Inner)
	}
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil {
		return false
	}
	switch binary.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_STAR:
		return true
	}
	return false
}

func (s *functionState) emitCheckedUSizeReserveBoundExpr(expr ast.Expr, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.emitCheckedUSizeReserveBoundExpr(paren.Inner, name)
	}
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil {
		value, _, err := s.emitExpr(expr, usizeType)
		return value, err
	}
	switch binary.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_STAR:
		left, err := s.emitCheckedUSizeReserveBoundExpr(binary.Left, name+".left")
		if err != nil {
			return nil, err
		}
		right, err := s.emitCheckedUSizeReserveBoundExpr(binary.Right, name+".right")
		if err != nil {
			return nil, err
		}
		if binary.Op == lexer.TOKEN_PLUS {
			return s.emitCheckedUSizeBinaryIntrinsic("llvm.uadd.with.overflow", left, right, name+".add")
		}
		return s.emitCheckedUSizeBinaryIntrinsic("llvm.umul.with.overflow", left, right, name+".mul")
	default:
		value, _, err := s.emitExpr(expr, usizeType)
		return value, err
	}
}

func (s *functionState) emitCheckedUSizeBinaryIntrinsic(intrinsicName string, left, right C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	fn, err := s.overflowIntrinsic(intrinsicName, usizeLLVMType)
	if err != nil {
		return nil, err
	}
	call := C.LLVMBuildCall2(s.builder, C.LLVMGlobalGetValueType(fn), fn, llvmValueSlicePtr([]C.LLVMValueRef{left, right}), 2, cStringFree(name+".checked"))
	value := C.LLVMBuildExtractValue(s.builder, call, 0, cStringFree(name+".value"))
	overflow := C.LLVMBuildExtractValue(s.builder, call, 1, cStringFree(name+".overflow"))
	trapBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".overflow.trap"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".ok"))
	C.LLVMBuildCondBr(s.builder, overflow, trapBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, trapBB)
	if err := s.emitTrapUnreachable(name + ".overflow"); err != nil {
		return nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return value, nil
}

func (s *functionState) overflowIntrinsic(name string, llvmType C.LLVMTypeRef) (C.LLVMValueRef, error) {
	nameC := cStringFree(name)
	intrinsicID := C.LLVMLookupIntrinsicID(nameC, C.size_t(len(name)))
	if intrinsicID == 0 {
		return nil, fmt.Errorf("unknown LLVM intrinsic %s", name)
	}
	return C.LLVMGetIntrinsicDeclaration(s.g.module, intrinsicID, llvmTypeSlicePtr([]C.LLVMTypeRef{llvmType}), 1), nil
}
