//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func (s *functionState) emitIndexAddress(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr != nil && expr.Fallback != nil {
		return nil, nil, fmt.Errorf("safe index fallback is not addressable")
	}
	if ptr, elemType, handled, err := s.emitNodeTableIndexAddress(expr); handled {
		return ptr, elemType, err
	}
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
	case *semantic.ViewType:
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
func (s *functionState) loweredEnumStorageType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil {
		return nil, fmt.Errorf("missing enum type")
	}
	if enumType.Packed {
		return s.g.ensurePackedEnumStorageType(enumType)
	}
	return s.g.lowerType(enumType)
}
func (s *functionState) coercePackedEnumHandleValue(value C.LLVMValueRef, actual semantic.Type, expected *semantic.EnumType) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || expected == nil || !expected.Packed {
		return nil, false, nil
	}
	switch s.g.packedModeForEnum(expected) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		if !isNumericCastType(actual) {
			return nil, false, nil
		}
		coerced, err := s.coerceNumericValue(value, actual, s.g.result.NamedTypes["u32"])
		return coerced, true, err
	default:
		return nil, false, nil
	}
}
func runtimeIndexedElemType(t semantic.Type) (semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.ViewType:
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
	if semantic.IsNeverType(actual) {
		if isVoidType(expected) {
			return nil, nil
		}
		llvmType, err := s.g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		return C.LLVMGetUndef(llvmType), nil
	}
	if expectedBuiltin, ok := expected.(*semantic.BuiltinType); ok && expectedBuiltin.Name == "bool" {
		actualLLVM, err := s.g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		zero := C.LLVMConstNull(actualLLVM)
		if isFloatType(actual) {
			return C.LLVMBuildFCmp(s.builder, C.LLVMRealPredicate(C.LLVMRealONE), value, zero, cStringFree("tobool")), nil
		}
		return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, zero, cStringFree("tobool")), nil
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
	if expectedOptional, ok := expected.(*semantic.OptionalType); ok {
		if actualOptional, ok := actual.(*semantic.OptionalType); ok {
			presentValue, err := s.extractOptionalPresent(value, actualOptional)
			if err != nil {
				return nil, err
			}
			payloadValue, err := s.extractOptionalPayload(value, actualOptional)
			if err != nil {
				return nil, err
			}
			payloadValue, err = s.coerceValue(payloadValue, actualOptional.Value, expectedOptional.Value)
			if err != nil {
				return nil, err
			}
			return s.buildOptionalValue(expectedOptional, presentValue, payloadValue)
		}
		if semantic.IsNullType(actual) {
			return s.buildOptionalNone(expectedOptional)
		}
		payloadValue, err := s.coerceValue(value, actual, expectedOptional.Value)
		if err != nil {
			return nil, err
		}
		return s.buildOptionalSome(expectedOptional, payloadValue)
	}
	if expectedEnum, ok := expected.(*semantic.EnumType); ok {
		if actualView, ok := actual.(*semantic.PackedVariantViewType); ok && actualView.Enum == expectedEnum {
			binding, err := s.unpackPackedVariantViewValue(value, actualView)
			if err != nil {
				return nil, err
			}
			return binding.handle, nil
		}
		if coerced, ok, err := s.coercePackedEnumHandleValue(value, actual, expectedEnum); ok || err != nil {
			return coerced, err
		}
	}
	if expectedView, ok := expected.(*semantic.PackedVariantViewType); ok {
		if actualEnum, ok := actual.(*semantic.EnumType); ok && actualEnum == expectedView.Enum && actualEnum.Packed {
			store, ok := s.lookupPackedStore(actualEnum)
			if !ok {
				return nil, fmt.Errorf("packedview %s requires store context for materialization", expectedView.String())
			}
			return s.buildPackedVariantViewValue(expectedView, value, &store)
		}
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
	if isNumericCastType(actual) && isNumericCastType(expected) {
		return s.coerceNumericValue(value, actual, expected)
	}
	if isPointerLikeType(actual) && isNumericCastType(expected) {
		return C.LLVMBuildPtrToInt(s.builder, value, expectedLLVM, cStringFree("ptrtoint")), nil
	}
	if isNumericCastType(actual) && isPointerLikeType(expected) {
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
func (s *functionState) buildOptionalSome(optionalType *semantic.OptionalType, payload C.LLVMValueRef) (C.LLVMValueRef, error) {
	presentType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, err
	}
	present := C.LLVMConstInt(presentType, 1, 0)
	return s.buildOptionalValue(optionalType, present, payload)
}
func (s *functionState) buildOptionalNone(optionalType *semantic.OptionalType) (C.LLVMValueRef, error) {
	if optionalType == nil {
		return nil, fmt.Errorf("missing optional type")
	}
	payload, err := s.zeroValue(optionalType.Value)
	if err != nil {
		return nil, err
	}
	presentType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, err
	}
	present := C.LLVMConstInt(presentType, 0, 0)
	return s.buildOptionalValue(optionalType, present, payload)
}
func (s *functionState) buildOptionalValue(optionalType *semantic.OptionalType, present C.LLVMValueRef, payload C.LLVMValueRef) (C.LLVMValueRef, error) {
	if optionalType == nil {
		return nil, fmt.Errorf("missing optional type")
	}
	llvmType, err := s.g.lowerType(optionalType)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	value = C.LLVMBuildInsertValue(s.builder, value, present, 0, cStringFree("optional.present"))
	value = C.LLVMBuildInsertValue(s.builder, value, payload, 1, cStringFree("optional.value"))
	return value, nil
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
func (s *functionState) extractOptionalPresent(value C.LLVMValueRef, optionalType *semantic.OptionalType) (C.LLVMValueRef, error) {
	if optionalType == nil {
		return nil, fmt.Errorf("missing optional type")
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("optional.present")), nil
}
func (s *functionState) extractOptionalPayload(value C.LLVMValueRef, optionalType *semantic.OptionalType) (C.LLVMValueRef, error) {
	if optionalType == nil || optionalType.Value == nil {
		return nil, fmt.Errorf("optional has no payload")
	}
	return C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("optional.payload")), nil
}
func (s *functionState) errorCodeConstant(code uint32) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(code), 0), nil
}
func (s *functionState) coerceNumericValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	actual = numericCastType(actual)
	expected = numericCastType(expected)
	actualBits := integerBitWidth(actual, s.g.wordBits)
	expectedBits := integerBitWidth(expected, s.g.wordBits)
	expectedLLVM, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	if isFloatType(actual) {
		if isFloatType(expected) {
			switch {
			case actualBits == expectedBits:
				return value, nil
			case actualBits < expectedBits:
				return C.LLVMBuildFPExt(s.builder, value, expectedLLVM, cStringFree("fpext")), nil
			default:
				return C.LLVMBuildFPTrunc(s.builder, value, expectedLLVM, cStringFree("fptrunc")), nil
			}
		}
		if isSignedIntegerType(expected) {
			return C.LLVMBuildFPToSI(s.builder, value, expectedLLVM, cStringFree("fptosi")), nil
		}
		return C.LLVMBuildFPToUI(s.builder, value, expectedLLVM, cStringFree("fptoui")), nil
	}
	if isFloatType(expected) {
		if isSignedIntegerType(actual) {
			return C.LLVMBuildSIToFP(s.builder, value, expectedLLVM, cStringFree("sitofp")), nil
		}
		return C.LLVMBuildUIToFP(s.builder, value, expectedLLVM, cStringFree("uitofp")), nil
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
		if isFloatType(t) {
			return C.LLVMBuildFAdd(s.builder, left, right, cStringFree("pluseq")), nil
		}
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("pluseq")), nil
	case lexer.TOKEN_MINUSEQ:
		if isFloatType(t) {
			return C.LLVMBuildFSub(s.builder, left, right, cStringFree("minuseq")), nil
		}
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("minuseq")), nil
	case lexer.TOKEN_STAREQ:
		if isFloatType(t) {
			return C.LLVMBuildFMul(s.builder, left, right, cStringFree("stareq")), nil
		}
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("stareq")), nil
	case lexer.TOKEN_SLASHEQ:
		if isFloatType(t) {
			return C.LLVMBuildFDiv(s.builder, left, right, cStringFree("slasheq")), nil
		}
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
