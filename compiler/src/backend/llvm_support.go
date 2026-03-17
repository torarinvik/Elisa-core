//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func (s *functionState) emitAddress(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			return binding.ptr, binding.typ, nil
		}
		if sym, ok := s.g.result.GlobalScope.Lookup(n.Name); ok {
			if sym.Kind == semantic.SymbolGlobal || sym.Kind == semantic.SymbolExternVar {
				global, err := s.g.ensureGlobalDeclared(n.Name, sym.Type, sym.Kind == semantic.SymbolExternVar)
				return global, sym.Type, err
			}
		}
		return nil, nil, fmt.Errorf("identifier %s is not addressable", n.Name)
	case *ast.FieldExpr:
		return s.emitFieldAddress(n)
	case *ast.IndexExpr:
		return s.emitIndexAddress(n)
	case *ast.ParenExpr:
		return s.emitAddress(n.Inner)
	default:
		return nil, nil, fmt.Errorf("expression %T is not addressable", expr)
	}
}

func (s *functionState) emitFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	objType := s.exprType(expr.Object)
	if objType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for field base when lowering .%s", expr.Field)
	}
	fieldType, index, containerType, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	var objPtr C.LLVMValueRef
	if pointerLike {
		objPtr, _, err = s.emitExpr(expr.Object, nil)
	} else {
		objPtr, _, err = s.emitAddress(expr.Object)
	}
	if err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, objPtr, C.unsigned(index), cStringFree(expr.Field))
	return fieldPtr, fieldType, nil
}

func (s *functionState) emitAddressOrTemp(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	if ptr, typ, err := s.emitAddress(expr); err == nil {
		return ptr, typ, nil
	}
	value, typ, err := s.emitExpr(expr, nil)
	if err != nil {
		return nil, nil, err
	}
	ptr, err := s.emitStackTempValue(value, typ, "addr.tmp")
	if err != nil {
		return nil, nil, err
	}
	return ptr, typ, nil
}

func (s *functionState) emitIndexAddress(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	objType := s.exprType(expr.Object)
	indexValue, _, err := s.emitExpr(expr.Index, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	switch t := objType.(type) {
	case *semantic.ArrayType:
		arrayPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		arrayLLVMType, err := s.g.lowerType(t)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{zero, indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
		return ptr, t.Elem, nil
	case *semantic.DArrayType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		return s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
	case *semantic.DArrayViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		return s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
	case *semantic.RefType:
		basePtr, _, err := s.emitExpr(expr.Object, nil)
		if err != nil {
			return nil, nil, err
		}
		if arrayElem, ok := t.Elem.(*semantic.ArrayType); ok {
			arrayLLVMType, err := s.g.lowerType(arrayElem)
			if err != nil {
				return nil, nil, err
			}
			indices := []C.LLVMValueRef{zero, indexValue}
			ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
			return ptr, arrayElem.Elem, nil
		}
		if elemType, ok := runtimeIndexedElemType(t.Elem); ok {
			return s.emitRuntimeIndexedAddress(basePtr, t.Elem, elemType, indexValue)
		}
		elemLLVMType, err := s.g.lowerType(t.Elem)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
		return ptr, t.Elem, nil
	default:
		return nil, nil, fmt.Errorf("indexing is not implemented for %s", objType.String())
	}
}

func (s *functionState) emitRuntimeIndexedAddress(containerPtr C.LLVMValueRef, containerType semantic.Type, elemType semantic.Type, indexValue C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	return s.emitRuntimePointerIndexedAddressWithType(containerPtr, containerLLVMType, elemType, indexValue)
}

func (s *functionState) emitRuntimePointerIndexedAddress(containerPtr C.LLVMValueRef, lowerContainer func() (C.LLVMTypeRef, error), elemType semantic.Type, indexValue C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	containerLLVMType, err := lowerContainer()
	if err != nil {
		return nil, nil, err
	}
	return s.emitRuntimePointerIndexedAddressWithType(containerPtr, containerLLVMType, elemType, indexValue)
}

func (s *functionState) emitRuntimePointerIndexedAddressWithType(containerPtr C.LLVMValueRef, containerLLVMType C.LLVMTypeRef, elemType semantic.Type, indexValue C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	dataFieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, containerPtr, 0, cStringFree("idx.data.ptr"))
	dataPtr, err := s.loadValue(dataFieldPtr, &semantic.RefType{Elem: elemType, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny}, "idx.data")
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(elemType)
	if err != nil {
		return nil, nil, err
	}
	indices := []C.LLVMValueRef{indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("idx.ptr"))
	return ptr, elemType, nil
}

func runtimeIndexedElemType(t semantic.Type) (semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.DArrayViewType:
		return tt.Elem, true
	default:
		return nil, false
	}
}

func (s *functionState) loadValue(ptr C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, llvmType, ptr, cStringFree(name)), nil
}

func (s *functionState) coerceValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	if expected == nil || actual == nil || semantic.SameType(actual, expected) {
		return value, nil
	}
	if expectedErrSet, ok := expected.(*semantic.ErrorSetType); ok {
		if actualErrSet, ok := actual.(*semantic.ErrorSetType); ok {
			return s.remapErrorCode(value, actualErrSet, expectedErrSet)
		}
	}
	if expectedUnion, ok := expected.(*semantic.ErrorUnionType); ok {
		if actualUnion, ok := actual.(*semantic.ErrorUnionType); ok {
			codeValue, err := s.extractErrorUnionCode(value, actualUnion)
			if err != nil {
				return nil, err
			}
			codeValue, err = s.remapErrorCode(codeValue, actualUnion.Errors, expectedUnion.Errors)
			if err != nil {
				return nil, err
			}
			if isVoidType(expectedUnion.Value) {
				return codeValue, nil
			}
			payloadValue, err := s.extractErrorUnionPayload(value, actualUnion)
			if err != nil {
				return nil, err
			}
			payloadValue, err = s.coerceValue(payloadValue, actualUnion.Value, expectedUnion.Value)
			if err != nil {
				return nil, err
			}
			return s.buildErrorUnionValue(expectedUnion, codeValue, payloadValue)
		}
		if actualErrSet, ok := actual.(*semantic.ErrorSetType); ok && semantic.ErrorSetAssignable(expectedUnion.Errors, actualErrSet) {
			mappedCode, err := s.remapErrorCode(value, actualErrSet, expectedUnion.Errors)
			if err != nil {
				return nil, err
			}
			return s.buildErrorUnionFailure(expectedUnion, mappedCode)
		}
		payloadValue, err := s.coerceValue(value, actual, expectedUnion.Value)
		if err != nil {
			return nil, err
		}
		return s.buildErrorUnionSuccess(expectedUnion, payloadValue)
	}
	actualLLVM, err := s.g.lowerType(actual)
	if err != nil {
		return nil, err
	}
	expectedLLVM, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	if actualLLVM == expectedLLVM {
		return value, nil
	}
	if isPointerLikeType(actual) && isPointerLikeType(expected) {
		return value, nil
	}
	if semantic.IsNullType(actual) && isPointerLikeType(expected) {
		return C.LLVMConstNull(expectedLLVM), nil
	}
	if isNumericType(actual) && isNumericType(expected) {
		return s.coerceNumericValue(value, actual, expected)
	}
	if isPointerLikeType(actual) && isNumericType(expected) {
		return C.LLVMBuildPtrToInt(s.builder, value, expectedLLVM, cStringFree("ptrtoint")), nil
	}
	if isNumericType(actual) && isPointerLikeType(expected) {
		return C.LLVMBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("inttoptr")), nil
	}
	return value, nil
}

func (s *functionState) remapErrorCode(value C.LLVMValueRef, actual *semantic.ErrorSetType, expected *semantic.ErrorSetType) (C.LLVMValueRef, error) {
	if actual == nil || expected == nil {
		return nil, fmt.Errorf("missing error set for code remap")
	}
	if semantic.SameType(actual, expected) {
		return value, nil
	}
	if !semantic.ErrorSetAssignable(expected, actual) {
		return nil, fmt.Errorf("cannot remap %s into %s", actual.String(), expected.String())
	}
	errorCodeType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	mapped, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, err
	}
	for _, tag := range actual.Tags {
		actualCode, ok := actual.TagCode(tag)
		if !ok {
			continue
		}
		mappedTag, ok := semantic.MatchErrorTag(expected, tag)
		if !ok {
			return nil, fmt.Errorf("cannot remap missing tag %s into %s", tag, expected.String())
		}
		expectedCode, ok := expected.TagCode(mappedTag)
		if !ok {
			return nil, fmt.Errorf("cannot remap missing tag %s into %s", mappedTag, expected.String())
		}
		actualConst, err := s.errorCodeConstant(actualCode)
		if err != nil {
			return nil, err
		}
		expectedConst, err := s.errorCodeConstant(expectedCode)
		if err != nil {
			return nil, err
		}
		tagID := sanitizeIdentifier(tag)
		cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), value, actualConst, cStringFree("errmap_is_"+tagID))
		mask := C.LLVMBuildZExt(s.builder, cmp, errorCodeType, cStringFree("errmap_mask_"+tagID))
		negMask := C.LLVMBuildSub(s.builder, C.LLVMConstNull(errorCodeType), mask, cStringFree("errmap_negmask_"+tagID))
		diff := C.LLVMBuildXor(s.builder, mapped, expectedConst, cStringFree("errmap_diff_"+tagID))
		maskedDiff := C.LLVMBuildAnd(s.builder, diff, negMask, cStringFree("errmap_masked_"+tagID))
		mapped = C.LLVMBuildXor(s.builder, mapped, maskedDiff, cStringFree("errmap_"+tagID))
	}
	return mapped, nil
}

func (s *functionState) buildErrorUnionSuccess(unionType *semantic.ErrorUnionType, payload C.LLVMValueRef) (C.LLVMValueRef, error) {
	zeroCode, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, err
	}
	return s.buildErrorUnionValue(unionType, zeroCode, payload)
}

func (s *functionState) buildErrorUnionFailure(unionType *semantic.ErrorUnionType, errorCode C.LLVMValueRef) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		return errorCode, nil
	}
	payload, err := s.zeroValue(unionType.Value)
	if err != nil {
		return nil, err
	}
	return s.buildErrorUnionValue(unionType, errorCode, payload)
}

func (s *functionState) buildErrorUnionValue(unionType *semantic.ErrorUnionType, errorCode C.LLVMValueRef, payload C.LLVMValueRef) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		return errorCode, nil
	}
	llvmType, err := s.g.lowerType(unionType)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	value = C.LLVMBuildInsertValue(s.builder, value, errorCode, 0, cStringFree("errunion.err"))
	value = C.LLVMBuildInsertValue(s.builder, value, payload, 1, cStringFree("errunion.value"))
	return value, nil
}

func (s *functionState) extractErrorUnionCode(value C.LLVMValueRef, unionType *semantic.ErrorUnionType) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("errunion.code")), nil
}

func (s *functionState) extractErrorUnionPayload(value C.LLVMValueRef, unionType *semantic.ErrorUnionType) (C.LLVMValueRef, error) {
	if unionType == nil || isVoidType(unionType.Value) {
		return nil, fmt.Errorf("error union has no payload")
	}
	return C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("errunion.payload")), nil
}

func (s *functionState) errorCodeConstant(code uint32) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(code), 0), nil
}

func (s *functionState) coerceNumericValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	actualBits := integerBitWidth(actual, s.g.wordBits)
	expectedBits := integerBitWidth(expected, s.g.wordBits)
	expectedLLVM, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	switch {
	case actualBits == expectedBits:
		return value, nil
	case actualBits < expectedBits:
		if isSignedIntegerType(actual) {
			return C.LLVMBuildSExt(s.builder, value, expectedLLVM, cStringFree("sext")), nil
		}
		return C.LLVMBuildZExt(s.builder, value, expectedLLVM, cStringFree("zext")), nil
	default:
		return C.LLVMBuildTrunc(s.builder, value, expectedLLVM, cStringFree("trunc")), nil
	}
}

func (s *functionState) binaryOperandType(op lexer.TokenKind, left semantic.Type, right semantic.Type) semantic.Type {
	switch op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		return s.g.result.NamedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if isNumericType(left) && isNumericType(right) {
			return semantic.CommonNumericType(left, right)
		}
		if semantic.IsNullType(left) {
			return right
		}
		if semantic.IsNullType(right) {
			return left
		}
		return left
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ,
		lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
		lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if isNumericType(left) && isNumericType(right) {
			return semantic.CommonNumericType(left, right)
		}
	}
	return left
}

func (s *functionState) emitAugmentedValue(op lexer.TokenKind, left C.LLVMValueRef, right C.LLVMValueRef, t semantic.Type) (C.LLVMValueRef, error) {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("pluseq")), nil
	case lexer.TOKEN_MINUSEQ:
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("minuseq")), nil
	case lexer.TOKEN_STAREQ:
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("stareq")), nil
	case lexer.TOKEN_SLASHEQ:
		if isSignedIntegerType(t) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("slasheq")), nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("slasheq")), nil
	case lexer.TOKEN_PERCENTEQ:
		if isSignedIntegerType(t) {
			return C.LLVMBuildSRem(s.builder, left, right, cStringFree("percenteq")), nil
		}
		return C.LLVMBuildURem(s.builder, left, right, cStringFree("percenteq")), nil
	case lexer.TOKEN_CARETEQ:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("careteq")), nil
	case lexer.TOKEN_PIPEEQ:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("pipeeq")), nil
	case lexer.TOKEN_AMPEQ:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("ampeq")), nil
	case lexer.TOKEN_LSHIFTEQ:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("lshifteq")), nil
	case lexer.TOKEN_RSHIFTEQ:
		if isSignedIntegerType(t) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("rshifteq")), nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("rshifteq")), nil
	default:
		return nil, fmt.Errorf("unsupported augmented assignment operator %s", lexer.TokenName(op))
	}
}

func (s *functionState) createEntryAlloca(name string, t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMGetEntryBasicBlock(s.fnValue)
	first := C.LLVMGetFirstInstruction(entry)
	if first != nil {
		C.LLVMPositionBuilderBefore(builder, first)
	} else {
		C.LLVMPositionBuilderAtEnd(builder, entry)
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.LLVMBuildAlloca(builder, llvmType, nameC), nil
}

func (s *functionState) currentBlockTerminated() bool {
	block := C.LLVMGetInsertBlock(s.builder)
	if block == nil {
		return true
	}
	return C.LLVMGetBasicBlockTerminator(block) != nil
}

func (s *functionState) pushScope() {
	s.scope = &codegenScope{parent: s.scope, bindings: map[string]valueBinding{}}
}

func (s *functionState) popScope() {
	if s.scope != nil {
		s.scope = s.scope.parent
	}
}

func (s *functionState) defineBinding(name string, binding valueBinding) {
	if s.scope == nil {
		s.scope = &codegenScope{bindings: map[string]valueBinding{}}
	}
	s.scope.bindings[name] = binding
}

func (s *functionState) lookupBinding(name string) (valueBinding, bool) {
	for scope := s.scope; scope != nil; scope = scope.parent {
		if binding, ok := scope.bindings[name]; ok {
			return binding, true
		}
	}
	return valueBinding{}, false
}

func (s *functionState) emitConstValue(value semantic.ConstValue) (C.LLVMValueRef, semantic.Type, error) {
	switch value.Kind {
	case semantic.ConstInt:
		llvmType, err := s.g.lowerBuiltin("int")
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value.Int), 1), s.g.result.NamedTypes["int"], nil
	case semantic.ConstBool:
		llvmType, err := s.g.lowerBuiltin("bool")
		if err != nil {
			return nil, nil, err
		}
		var raw C.ulonglong
		if value.Bool {
			raw = 1
		}
		return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
	case semantic.ConstString:
		name := cString("cstr")
		defer C.free(unsafe.Pointer(name))
		text := cString(value.String)
		defer C.free(unsafe.Pointer(text))
		return C.LLVMBuildGlobalStringPtr(s.builder, text, name), &semantic.RefType{Elem: s.g.result.NamedTypes["u8"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported const kind %d", value.Kind)
	}
}

func (s *functionState) zeroValue(t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstNull(llvmType), nil
}

func (s *functionState) ensureTrapFunction() (C.LLVMValueRef, error) {
	if value, ok := s.g.functions["llvm.trap"]; ok {
		return value, nil
	}
	voidType := s.g.result.NamedTypes["void"]
	trapType := &semantic.FuncType{Name: "llvm.trap", Return: voidType}
	return s.g.ensureFunctionDeclared("llvm.trap", trapType)
}

func (s *functionState) resolveTypeExpr(expr ast.TypeExpr) (semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.NamedType:
		if bound, ok := s.typeMap[n.Name]; ok {
			return bound, nil
		}
		if t, ok := s.g.result.NamedTypes[n.Name]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("unknown type %q", n.Name)
	case *ast.ErrorSetExpr:
		return s.resolveErrorSetExpr(n)
	case *ast.ErrorUnionTypeExpr:
		valueType, err := s.resolveTypeExpr(n.Value)
		if err != nil {
			return nil, err
		}
		errorType, err := s.resolveTypeExpr(n.Errors)
		if err != nil {
			return nil, err
		}
		errSet, ok := errorType.(*semantic.ErrorSetType)
		if !ok {
			return nil, fmt.Errorf("error union expects an error set on the right-hand side")
		}
		return &semantic.ErrorUnionType{Value: valueType, Errors: errSet}, nil
	case *ast.RefType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefState(n.State), Storage: semantic.RefStorage(n.Storage), ExplicitStorage: n.Explicit}, nil
	case *ast.ArrayType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		size, err := s.evalConstIntExpr(n.Size)
		if err != nil {
			return nil, err
		}
		return &semantic.ArrayType{Elem: elem, Size: fmt.Sprintf("%d", size), HasConstSize: true, ConstSize: size}, nil
	case *ast.BuiltinTypeExpr:
		return s.resolveBuiltinSurfaceTypeExpr(n)
	case *ast.MutableType:
		return s.resolveTypeExpr(n.Elem)
	case *ast.TailType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}, nil
	case *ast.GenericType:
		if t, ok, err := s.resolveDynamicShapeType(n); ok || err != nil {
			return t, err
		}
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, fmt.Errorf("unknown type %q", n.Name)
		}
		args := make([]semantic.Type, 0, len(n.Args))
		for _, arg := range n.Args {
			resolved, err := s.resolveTypeExpr(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, resolved)
		}
		return &semantic.GenericInstanceType{Name: n.Name, Base: base, Args: args}, nil
	default:
		return nil, fmt.Errorf("unsupported type expression %T", expr)
	}
}

func (s *functionState) resolveErrorSetExpr(expr *ast.ErrorSetExpr) (semantic.Type, error) {
	if expr == nil || len(expr.Tags) == 0 {
		return nil, fmt.Errorf("error[...] requires at least one qualified error tag")
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		return nil, fmt.Errorf("error[Set.*, ...] is no longer supported; use error[Set, ...] or error[Set] instead")
	}
	if containsWildcardErrorTag(expr.Tags) {
		return nil, fmt.Errorf("error[Set.*] is no longer supported; use error[Set] instead")
	}
	if len(expr.Tags) == 1 && expr.Tags[0].Tag == "" {
		_, errSet, err := s.lookupDeclaredErrorSet(expr.Tags[0])
		if err != nil {
			return nil, err
		}
		return errSet, nil
	}
	if expr.HasEllipsis {
		return s.resolveExpandedErrorFamilies(expr)
	}
	familySets := map[string]*semantic.ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range expr.Tags {
		_, errSet, err := s.lookupDeclaredErrorSet(tag)
		if err != nil {
			return nil, err
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := semantic.SplitErrorTagName(qualifiedTag)
					return nil, fmt.Errorf("duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: semantic.ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := semantic.QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			return nil, fmt.Errorf("error set %q has no tag %q", tag.SetName, tag.Tag)
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			return nil, fmt.Errorf("duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return semantic.CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags), nil
}

func (s *functionState) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *semantic.ErrorSetType, error) {
	t, ok := s.g.result.NamedTypes[tag.SetName]
	if !ok {
		return "", nil, fmt.Errorf("unknown error set %q", tag.SetName)
	}
	errSet, ok := t.(*semantic.ErrorSetType)
	if !ok {
		return "", nil, fmt.Errorf("%q is not an error set", tag.SetName)
	}
	return tag.SetName, errSet, nil
}

func containsWildcardErrorTag(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "*" {
			return true
		}
	}
	return false
}

func containsBareFamily(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "" {
			return true
		}
	}
	return false
}

func (s *functionState) resolveExpandedErrorFamilies(expr *ast.ErrorSetExpr) (semantic.Type, error) {
	familySets := map[string]*semantic.ErrorSetType{}
	fullFamilies := map[string]bool{}
	for _, tag := range expr.Tags {
		_, errSet, err := s.lookupDeclaredErrorSet(tag)
		if err != nil {
			return nil, err
		}
		if tag.Tag != "" && !errSet.HasQualifiedTag(tag.SetName, tag.Tag) {
			return nil, fmt.Errorf("error set %q has no tag %q", tag.SetName, tag.Tag)
		}
		familySets[tag.SetName] = errSet
		fullFamilies[tag.SetName] = true
	}
	return semantic.CanonicalizeErrorSetSelections(familySets, fullFamilies, nil), nil
}

func onlyBareFamilies(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag != "" {
			return false
		}
	}
	return len(tags) > 0
}

func singleExplicitErrorFamily(tags []ast.ErrorTagExpr) (string, bool) {
	family := ""
	for _, tag := range tags {
		if tag.Tag == "" {
			return "", false
		}
		if family == "" {
			family = tag.SetName
			continue
		}
		if tag.SetName != family {
			return "", false
		}
	}
	return family, family != ""
}

func (s *functionState) resolveBuiltinSurfaceTypeExpr(expr *ast.BuiltinTypeExpr) (semantic.Type, error) {
	switch expr.Name {
	case "array":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("array expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		resolved, err := s.resolveTypeExpr(&ast.ArrayType{Position: expr.Position, Elem: expr.TypeArgs[0], Size: expr.ValueArgs[0]})
		if err != nil {
			return nil, err
		}
		if arrayType, ok := resolved.(*semantic.ArrayType); ok {
			arrayType.SurfaceName = "array"
		}
		return resolved, nil
	case "darray":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("darray expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return &semantic.DArrayType{Elem: elem, Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "darray"}, nil
	case "dict":
		if len(expr.TypeArgs) != 2 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("dict expects 2 type arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		keyType, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		valueType, err := s.resolveTypeExpr(expr.TypeArgs[1])
		if err != nil {
			return nil, err
		}
		return resolveBackendDictType(keyType, valueType, "dict")
	case "str", "string":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("str expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		resolved, err := s.resolveTypeExpr(&ast.ArrayType{
			Position: expr.Position,
			Elem:     &ast.NamedType{Position: expr.Position, Name: "u8"},
			Size:     expr.ValueArgs[0],
		})
		if err != nil {
			return nil, err
		}
		if arrayType, ok := resolved.(*semantic.ArrayType); ok {
			arrayType.SurfaceName = "str"
		}
		return resolved, nil
	case "dstr", "dstring":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("dstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.DStrType{Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "dstr"}, nil
	case "view":
		if len(expr.TypeArgs) != 1 {
			return nil, fmt.Errorf("view expects 1 type argument, got %d", len(expr.TypeArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		viewType := &semantic.DArrayViewType{Elem: elem, SurfaceName: "view"}
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = exprSummary(expr.ValueArgs[0])
			viewType.End = exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return viewType, nil
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			return nil, fmt.Errorf("sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.SViewType{Begin: exprSummary(expr.ValueArgs[0]), End: exprSummary(expr.ValueArgs[1])}, nil
	default:
		return nil, fmt.Errorf("unknown built-in type %q", expr.Name)
	}
}

func resolveBackendDictType(keyType semantic.Type, valueType semantic.Type, surfaceName string) (semantic.Type, error) {
	if !isRuntimeDictKeyType(keyType) {
		return nil, fmt.Errorf("dict currently only supports dstr keys in the first runtime-backed slice, got %s", keyType.String())
	}
	return &semantic.DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName}, nil
}

func (s *functionState) exprType(expr ast.Expr) semantic.Type {
	t := s.g.exprType(expr)
	if t == nil {
		switch n := expr.(type) {
		case *ast.Ident:
			if binding, ok := s.lookupBinding(n.Name); ok {
				t = binding.typ
			} else if sym, ok := s.g.result.GlobalScope.Lookup(n.Name); ok {
				t = sym.Type
			}
		case *ast.FieldExpr:
			if fieldType, ok := dstrSyntheticFieldType(s.exprType(n.Object), n.Field); ok {
				t = fieldType
				break
			}
			objType := s.exprType(n.Object)
			if objType != nil {
				fieldType, _, _, _, err := s.g.fieldInfo(objType, n.Field)
				if err == nil {
					t = fieldType
				}
			}
		case *ast.CastExpr:
			resolved, err := s.resolveTypeExpr(n.Target)
			if err == nil {
				t = resolved
			}
		case *ast.SizeofExpr:
			t = s.g.result.NamedTypes["usize"]
		case *ast.AddrOfExpr:
			innerType := s.exprType(n.Operand)
			if innerType != nil {
				t = &semantic.RefType{Elem: innerType, State: semantic.RefStateNonNull}
			}
		case *ast.ParenExpr:
			return s.exprType(n.Inner)
		}
	}
	if t == nil || len(s.typeMap) == 0 {
		return t
	}
	return substituteType(t, s.typeMap)
}

func (s *functionState) resolveDynamicShapeType(expr *ast.GenericType) (semantic.Type, bool, error) {
	switch expr.Name {
	case "Dict":
		if len(expr.Args) != 2 {
			return nil, true, fmt.Errorf("Dict expects 2 arguments, got %d", len(expr.Args))
		}
		keyType, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		valueType, err := s.resolveTypeExpr(expr.Args[1])
		if err != nil {
			return nil, true, err
		}
		resolved, err := resolveBackendDictType(keyType, valueType, "Dict")
		return resolved, true, err
	case "view":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("view expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayViewType{Elem: elem, SurfaceName: "view"}, true, nil
	case "DArray":
		if len(expr.Args) != 2 {
			return nil, true, fmt.Errorf("DArray expects 2 arguments, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayType{Elem: elem, Shape: shapeFromTypeExpr(expr.Args[1])}, true, nil
	case "DArrayView":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("DArrayView expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayViewType{Elem: elem}, true, nil
	case "DList":
		return nil, true, fmt.Errorf("DList has been removed from the language; use DArray/darray instead")
	case "DListView":
		return nil, true, fmt.Errorf("DListView has been removed from the language; use DArrayView/view instead")
	case "DStr":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("DStr expects 1 argument, got %d", len(expr.Args))
		}
		return &semantic.DStrType{Shape: shapeFromTypeExpr(expr.Args[0])}, true, nil
	default:
		return nil, false, nil
	}
}

func (g *llvmGenerator) fieldInfo(objType semantic.Type, fieldName string) (semantic.Type, int, semantic.Type, bool, error) {
	if objType == nil {
		return nil, 0, nil, false, fmt.Errorf("field access requires a typed base expression for .%s", fieldName)
	}
	pointerLike := false
	if ref, ok := objType.(*semantic.RefType); ok {
		pointerLike = true
		objType = ref.Elem
	}
	if runtimeBacked := g.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *semantic.StructType:
		index, field, err := fieldInfoFromStruct(t, fieldName)
		return field.Type, index, t, pointerLike, err
	case *semantic.GenericInstanceType:
		base, ok := t.Base.(*semantic.StructType)
		if !ok {
			return nil, 0, nil, false, fmt.Errorf("field access requires a struct-backed type")
		}
		index, field, err := fieldInfoFromStruct(base, fieldName)
		if err != nil {
			return nil, 0, nil, false, err
		}
		subst := make(map[string]semantic.Type, len(base.TypeParams))
		for i, param := range base.TypeParams {
			if i < len(t.Args) {
				subst[param] = t.Args[i]
			}
		}
		return substituteType(field.Type, subst), index, t, pointerLike, nil
	default:
		return nil, 0, nil, false, fmt.Errorf("field access requires a struct type, got %s", objType.String())
	}
}

func fieldInfoFromStruct(st *semantic.StructType, fieldName string) (int, semantic.Field, error) {
	if st == nil || st.Decl == nil {
		return 0, semantic.Field{}, fmt.Errorf("struct metadata is unavailable")
	}
	for i, fieldDecl := range st.Decl.Fields {
		if fieldDecl.Name == fieldName {
			field, ok := st.Fields[fieldName]
			if !ok {
				return 0, semantic.Field{}, fmt.Errorf("missing field %s.%s", st.Name, fieldName)
			}
			return i, field, nil
		}
	}
	return 0, semantic.Field{}, fmt.Errorf("struct %s has no field %s", st.Name, fieldName)
}

func (g *llvmGenerator) runtimeBackedStructType(t semantic.Type) semantic.Type {
	if _, ok := t.(*semantic.SViewType); ok {
		if base, ok := g.result.NamedTypes["StringView"]; ok {
			return base
		}
		return nil
	}
	if dict, ok := t.(*semantic.DictType); ok {
		base, ok := g.result.NamedTypes["DynDict"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{dict.Value}}
	}
	if darray, ok := t.(*semantic.DArrayType); ok {
		base, ok := g.result.NamedTypes["DynArray"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynArray", Base: base, Args: []semantic.Type{darray.Elem}}
	}
	return nil
}

func isRuntimeDictKeyType(t semantic.Type) bool {
	_, ok := t.(*semantic.DStrType)
	return ok
}

func exprSummary(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Value
	case *ast.Ident:
		return n.Name
	default:
		return "?"
	}
}
