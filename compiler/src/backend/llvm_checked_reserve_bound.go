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
	value, overflow, err := s.emitUSizeBinaryOverflowIntrinsic(intrinsicName, left, right, name)
	if err != nil {
		return nil, err
	}
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

func (s *functionState) emitUSizeBinaryOverflowIntrinsic(intrinsicName string, left, right C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	fn, err := s.overflowIntrinsic(intrinsicName, usizeLLVMType)
	if err != nil {
		return nil, nil, err
	}
	call := C.LLVMBuildCall2(s.builder, C.LLVMGlobalGetValueType(fn), fn, llvmValueSlicePtr([]C.LLVMValueRef{left, right}), 2, cStringFree(name+".checked"))
	value := C.LLVMBuildExtractValue(s.builder, call, 0, cStringFree(name+".value"))
	overflow := C.LLVMBuildExtractValue(s.builder, call, 1, cStringFree(name+".overflow"))
	return value, overflow, nil
}

func (s *functionState) emitCheckedUSizeAdd(left, right C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return s.emitCheckedUSizeBinaryIntrinsic("llvm.uadd.with.overflow", left, right, name)
}

func (s *functionState) emitCheckedUSizeMul(left, right C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return s.emitCheckedUSizeBinaryIntrinsic("llvm.umul.with.overflow", left, right, name)
}

func (s *functionState) emitSafeDoubledCapacity(currentCapacity, neededCapacity C.LLVMValueRef, usizeLLVMType C.LLVMTypeRef, name string) (C.LLVMValueRef, error) {
	doubled, overflow, err := s.emitUSizeBinaryOverflowIntrinsic("llvm.umul.with.overflow", currentCapacity, C.LLVMConstInt(usizeLLVMType, 2, 0), name+".capacity.double")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildSelect(s.builder, overflow, neededCapacity, doubled, cStringFree(name+".capacity.double.safe")), nil
}

// signedOverflowIntrinsicName maps `+`/`-`/`*` to the LLVM signed-with-overflow intrinsic. The
// returned ok=false for any other operator (those never overflow into a wrong-sign result here).
func signedOverflowIntrinsicName(op lexer.TokenKind) (string, bool) {
	switch op {
	case lexer.TOKEN_PLUS:
		return "llvm.sadd.with.overflow", true
	case lexer.TOKEN_MINUS:
		return "llvm.ssub.with.overflow", true
	case lexer.TOKEN_STAR:
		return "llvm.smul.with.overflow", true
	default:
		return "", false
	}
}

// emitCheckedSignedBinary emits a signed `+`/`-`/`*` that TRAPS on two's-complement overflow, using
// the matching `llvm.s{add,sub,mul}.with.overflow` intrinsic on the operands' integer type. It is the
// debug/contracts-enabled lowering of signed arithmetic: trapping on overflow makes the static
// verifier's "signed arithmetic does not overflow" assumption sound in exactly the builds where
// contracts are live. Release builds keep the plain wrapping op (callers gate on the opt level).
func (s *functionState) emitCheckedSignedBinary(op lexer.TokenKind, left, right C.LLVMValueRef, llvmType C.LLVMTypeRef, name string) (C.LLVMValueRef, error) {
	intrinsicName, ok := signedOverflowIntrinsicName(op)
	if !ok {
		return nil, fmt.Errorf("no signed overflow intrinsic for operator %v", op)
	}
	fn, err := s.overflowIntrinsic(intrinsicName, llvmType)
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
