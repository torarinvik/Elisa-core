//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (s *functionState) emitStringViewStaticLiteralEqual(viewExpr ast.Expr, viewType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	// Through a REFERENCE (`found: mutable sview&`, e.g. `dict.get(...) is existing`),
	// emitExpr yields the POINTER rather than the StringView aggregate, and the
	// ExtractValue below then ran on a pointer and took LLVM down with a SIGSEGV inside
	// cgo -- a compiler crash, not a diagnostic. emitStringCompareOperandValue already
	// knows how to load a view through a ref; the general runtime string-compare path
	// uses it, and this static-literal fast path must use the same coercion. The non-ref
	// path is unchanged.
	viewValue, err := s.emitStringCompareOperandValue(viewExpr, viewType)
	if err != nil {
		return nil, err
	}
	return s.emitStringViewValueStaticLiteralEqual(viewValue, literalExpr, literalText)
}

// emitStringViewValueStaticLiteralEqual lowers the literal comparison from an
// already evaluated view value. Match scrutinees use this form so a call or
// other effectful expression is never emitted a second time for each arm.
func (s *functionState) emitStringViewValueStaticLiteralEqual(viewValue C.LLVMValueRef, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svlit.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svlit.len"))
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), viewLen, lenValue, cStringFree("svlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}
	// The source-level carrier may be malformed by unsafe code, a foreign
	// boundary, or corrupted storage. Even though ordinary sview constructors
	// produce a valid pointer for a positive length, this specialization must
	// preserve the runtime helper's null guard before it reaches byte loads or
	// memcmp. The pointer comparison itself is safe; the branch below keeps all
	// memory reads control-dependent on both the length and non-null checks.
	viewDataNonNull := C.LLVMBuildICmp(
		s.builder,
		C.LLVMIntPredicate(C.LLVMIntNE),
		viewData,
		C.LLVMConstNull(C.LLVMTypeOf(viewData)),
		cStringFree("svlit.data.nonnull"),
	)
	compareAllowed := C.LLVMBuildAnd(s.builder, lenEqual, viewDataNonNull, cStringFree("svlit.valid"))

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.merge"))
	C.LLVMBuildCondBr(s.builder, compareAllowed, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(viewData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(viewData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("svlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}
func (s *functionState) emitCStrStaticLiteralEqual(textExpr ast.Expr, textType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(textType) != runtimeStringCompareCStr {
		return nil, fmt.Errorf("cstr literal specialization requires cstr operand")
	}
	lenType := s.g.namedType("i64")
	var (
		textData C.LLVMValueRef
		textLen  C.LLVMValueRef
		err      error
	)
	if baseExpr, baseType, start, end, ok := s.constantCStrSliceCall(textExpr); ok {
		textData, textLen, err = s.emitConstantClampedStringSliceOperand(baseExpr, baseType, start, end, "cstrlit.slice")
		if err != nil {
			return nil, err
		}
	} else {
		textValue, _, err := s.emitExpr(textExpr, textType)
		if err != nil {
			return nil, err
		}
		textData = textValue
		textLen, err = s.emitRuntimeStringLengthValue(textValue, textType, lenType, "cstrlit.len")
		if err != nil {
			return nil, err
		}
	}
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), textLen, lenValue, cStringFree("cstrlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cstrlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cstrlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(textData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(textData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("cstrlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}
func (s *functionState) emitStringViewLiteralBytesEqual(viewData C.LLVMValueRef, literalText string) (C.LLVMValueRef, error) {
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	byteType := C.LLVMInt8TypeInContext(s.g.context)
	indexType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	if err := s.g.ensureTargetMachine(); err != nil {
		return nil, err
	}
	littleEndian := C.LLVMByteOrder(s.g.targetData) == C.LLVMLittleEndian
	result := C.LLVMConstInt(boolType, 1, 0)
	// The caller has checked the length (and a nonempty view's pointer).
	// Load only complete chunks within those bytes, with alignment 1: views
	// need not be word-aligned and the last byte may precede a guard page.
	for offset := 0; offset < len(literalText); {
		width := 8
		for width > len(literalText)-offset {
			width /= 2
		}
		var word uint64
		for j := 0; j < width; j++ {
			shift := j
			if !littleEndian {
				shift = width - 1 - j
			}
			word |= uint64(literalText[offset+j]) << (8 * shift)
		}
		indexValue := C.LLVMConstInt(indexType, C.ulonglong(offset), 0)
		indices := []C.LLVMValueRef{indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, byteType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("svlit.chunk.ptr"))
		chunkType := C.LLVMIntTypeInContext(s.g.context, C.unsigned(width*8))
		value := C.LLVMBuildLoad2(s.builder, chunkType, ptr, cStringFree("svlit.chunk"))
		C.LLVMSetAlignment(value, 1)
		literalValue := C.LLVMConstInt(chunkType, C.ulonglong(word), 0)
		equal := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), value, literalValue, cStringFree("svlit.chunk.eq"))
		result = C.LLVMBuildAnd(s.builder, result, equal, cStringFree("svlit.bytes.and"))
		offset += width
	}
	return result, nil
}
func (s *functionState) emitMemcmpEqual(left C.LLVMValueRef, right C.LLVMValueRef, length int) (C.LLVMValueRef, error) {
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	lengthValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(length), 0)
	return s.emitMemcmpEqualValue(left, right, lengthValue, "svlit.memcmp", false)
}
func (s *functionState) emitMemcmpEqualValue(left C.LLVMValueRef, right C.LLVMValueRef, lengthValue C.LLVMValueRef, callName string, noAliasArgs bool) (C.LLVMValueRef, error) {
	voidType := s.g.namedType("void")
	usizeType := s.g.namedType("usize")
	// libc's memcmp returns C int (i32); Elisa's `int` is 64-bit and conflicts
	// with the standard-library extern on ABI-correct Stage1 sources.
	intType := s.g.namedType("i32")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{
		Name:   "memcmp",
		Params: []semantic.Type{voidRefType, voidRefType, usizeType},
		Return: intType,
	}
	callee, err := s.g.ensureFunctionDeclared("memcmp", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{left, right, lengthValue}, callName)
	if noAliasArgs {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(intLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), call, zero, cStringFree(callName+".eq")), nil
}
func (s *functionState) staticCStringLiteral(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		return n.Value, true
	case *ast.ParenExpr:
		return s.staticCStringLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticCStringLiteral(n.Operand)
	case *ast.Ident:
		value, ok := s.g.constValue(n.Name)
		if !ok || value.Kind != semantic.ConstString {
			return "", false
		}
		return value.String, true
	default:
		return "", false
	}
}
func (s *functionState) emitLogicalExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	left, _, err := s.emitExpr(expr.Left, s.g.namedType("bool"))
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	rhsName := cString("logic.rhs")
	defer C.free(unsafe.Pointer(rhsName))
	rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, rhsName)
	mergeName := cString("logic.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	if expr.Op == lexer.TOKEN_AND {
		C.LLVMBuildCondBr(s.builder, left, rhsBB, mergeBB)
	} else {
		C.LLVMBuildCondBr(s.builder, left, mergeBB, rhsBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
	right, _, err := s.emitExpr(expr.Right, s.g.namedType("bool"))
	if err != nil {
		return nil, nil, err
	}
	rhsEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(rhsEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("logicphi"))
	fallback := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	if expr.Op == lexer.TOKEN_OR {
		fallback = C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	}
	values := []C.LLVMValueRef{fallback, right}
	blocks := []C.LLVMBasicBlockRef{parentBlock, rhsEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.namedType("bool"), nil
}
func (s *functionState) emitUnaryExpr(expr *ast.UnaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Operand == nil {
		pos := lexer.Pos{}
		op := lexer.TokenKind(0)
		if expr != nil {
			pos = expr.Pos()
			op = expr.Op
		}
		return nil, nil, fmt.Errorf("cannot emit unary expression %s with nil operand at %s", lexer.TokenName(op), pos)
	}
	// Overloaded unary operator (`-x` on a user type): emit the analyzer's desugared `T.__neg__(x)`.
	if expr.LoweredCall != nil {
		return s.emitExpr(expr.LoweredCall, s.exprType(expr))
	}
	operandType := s.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.exprType(expr)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		return C.LLVMBuildNot(s.builder, value, cStringFree("nottmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
		}
		return C.LLVMBuildNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
	case lexer.TOKEN_TILDE:
		return C.LLVMBuildNot(s.builder, value, cStringFree("invt")), resultType, nil
	case lexer.TOKEN_BANG:
		return value, resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported unary operator %s", lexer.TokenName(expr.Op))
	}
}
