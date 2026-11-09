//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

*/
import "C"

import (
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

// errorSetPayloadBaseIndex returns the LLVM struct field index of the first payload
// field of `tag` in a payloaded error set's {code, payloads...} layout (field 0 is the
// code; per-variant payloads follow in Tags order). Mirrors buildErrorSetValue.
func errorSetPayloadBaseIndex(set *semantic.ErrorSetType, tag string) int {
	base := 1
	for _, candidate := range set.Tags {
		if candidate == tag {
			break
		}
		base += len(set.PayloadForTag(candidate))
	}
	return base
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
	// The scalar tag code lives at field 0 of a payloaded set's struct, and IS the
	// value for a payloadless set.
	sourceCode := value
	if actual.HasPayloads() {
		sourceCode = C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("errmap.srccode"))
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
		cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), sourceCode, actualConst, cStringFree("errmap_is_"+tagID))
		mask := C.LLVMBuildZExt(s.builder, cmp, errorCodeType, cStringFree("errmap_mask_"+tagID))
		negMask := C.LLVMBuildSub(s.builder, C.LLVMConstNull(errorCodeType), mask, cStringFree("errmap_negmask_"+tagID))
		diff := C.LLVMBuildXor(s.builder, mapped, expectedConst, cStringFree("errmap_diff_"+tagID))
		maskedDiff := C.LLVMBuildAnd(s.builder, diff, negMask, cStringFree("errmap_masked_"+tagID))
		mapped = C.LLVMBuildXor(s.builder, mapped, maskedDiff, cStringFree("errmap_"+tagID))
	}
	if !expected.HasPayloads() {
		return mapped, nil
	}
	// The destination is a {code, payloads...} struct. Insert the remapped code, then
	// relocate each source payload field to its position in the destination layout. The
	// copy is unconditional: only the active tag's payload is meaningful (selected by the
	// code), so the inactive fields carry undef on both sides — harmless.
	destType, err := s.g.lowerType(expected)
	if err != nil {
		return nil, err
	}
	destVal := C.LLVMGetUndef(destType)
	destVal = C.LLVMBuildInsertValue(s.builder, destVal, mapped, 0, cStringFree("errmap.code"))
	if actual.HasPayloads() {
		srcFieldIdx := 1
		for _, tag := range actual.Tags {
			mappedTag, ok := semantic.MatchErrorTag(expected, tag)
			if !ok {
				return nil, fmt.Errorf("cannot remap missing tag %s into %s", tag, expected.String())
			}
			destBase := errorSetPayloadBaseIndex(expected, mappedTag)
			for k := range actual.PayloadForTag(tag) {
				fieldVal := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(srcFieldIdx), cStringFree("errmap.srcpayload"))
				destVal = C.LLVMBuildInsertValue(s.builder, destVal, fieldVal, C.unsigned(destBase+k), cStringFree("errmap.dstpayload"))
				srcFieldIdx++
			}
		}
	}
	return destVal, nil
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

// wrapVoidErrorUnionCode produces the representation of a `void error[set]` value
// from a bare error code. A payloadless set is just the code; a payloaded set is the
// {code, payloads...} struct, so the code is inserted at field 0 (payload fields are
// undef — never read on a void union, whose only datum is which tag fired). If the
// value is already the struct (e.g. a `raise` built it), it is returned unchanged.
func (s *functionState) wrapVoidErrorUnionCode(errorSet *semantic.ErrorSetType, errorCode C.LLVMValueRef) (C.LLVMValueRef, error) {
	if errorSet == nil || !errorSet.HasPayloads() {
		return errorCode, nil
	}
	errorType, err := s.g.lowerType(errorSet)
	if err != nil {
		return nil, err
	}
	if C.LLVMTypeOf(errorCode) == errorType {
		return errorCode, nil
	}
	setValue := C.LLVMGetUndef(errorType)
	setValue = C.LLVMBuildInsertValue(s.builder, setValue, errorCode, 0, cStringFree("errset.code"))
	return setValue, nil
}

func (s *functionState) buildErrorUnionFailure(unionType *semantic.ErrorUnionType, errorCode C.LLVMValueRef) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		return s.wrapVoidErrorUnionCode(unionType.Errors, errorCode)
	}
	payloadPtr, err := s.errorUnionZeroPayloadGlobal(unionType.Value)
	if err != nil {
		return nil, err
	}
	return s.buildErrorUnionValueFromPointer(unionType, errorCode, payloadPtr)
}

// Failed error unions never expose their success payload.  Point them at one immutable
// module-level zero object instead of allocating a full payload-sized stack temporary at
// every propagation site.  The split return ABI may still copy this zero to the caller's
// already-zeroed out slot, which is harmless and keeps the representation total.
func (s *functionState) errorUnionZeroPayloadGlobal(payloadType semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(payloadType)
	if err != nil {
		return nil, err
	}
	name := "__elisa_errzero_" + llvmTypeSymbolName(errorUnionLayoutValue(payloadType))
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	global := C.LLVMGetNamedGlobal(s.g.module, nameC)
	if global != nil {
		return global, nil
	}
	global = C.LLVMAddGlobal(s.g.module, llvmType, nameC)
	C.LLVMSetInitializer(global, C.LLVMConstNull(llvmType))
	C.LLVMSetGlobalConstant(global, 1)
	C.LLVMSetLinkage(global, C.LLVMPrivateLinkage)
	return global, nil
}

func (s *functionState) buildErrorSetValue(errorSet *semantic.ErrorSetType, tag string, args []C.LLVMValueRef) (C.LLVMValueRef, error) {
	if errorSet == nil {
		return nil, fmt.Errorf("missing error set type")
	}
	code, ok := errorSet.TagCode(tag)
	if !ok {
		return nil, fmt.Errorf("missing error tag %s", tag)
	}
	codeValue, err := s.errorCodeConstant(code)
	if err != nil {
		return nil, err
	}
	if !errorSet.HasPayloads() {
		return codeValue, nil
	}
	llvmType, err := s.g.lowerType(errorSet)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	value = C.LLVMBuildInsertValue(s.builder, value, codeValue, 0, cStringFree("errset.code"))
	fieldIndex := 1
	for _, candidate := range errorSet.Tags {
		payload := errorSet.PayloadForTag(candidate)
		for payloadIndex := range payload {
			if candidate == tag && payloadIndex < len(args) {
				value = C.LLVMBuildInsertValue(s.builder, value, args[payloadIndex], C.unsigned(fieldIndex), cStringFree("errset.payload"))
			}
			fieldIndex++
		}
	}
	return value, nil
}

func (s *functionState) extractErrorSetCode(value C.LLVMValueRef, errorSet *semantic.ErrorSetType) (C.LLVMValueRef, error) {
	if errorSet == nil {
		return nil, fmt.Errorf("missing error set type")
	}
	if !errorSet.HasPayloads() {
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("errset.code")), nil
}

func (s *functionState) extractErrorSetPayloadValues(value C.LLVMValueRef, errorSet *semantic.ErrorSetType, tag string) ([]C.LLVMValueRef, error) {
	if errorSet == nil {
		return nil, fmt.Errorf("missing error set type")
	}
	payloadTypes := errorSet.PayloadForTag(tag)
	values := make([]C.LLVMValueRef, 0, len(payloadTypes))
	fieldIndex := 1
	for _, candidate := range errorSet.Tags {
		payload := errorSet.PayloadForTag(candidate)
		for range payload {
			if candidate == tag {
				values = append(values, C.LLVMBuildExtractValue(s.builder, value, C.unsigned(fieldIndex), cStringFree("errset.payload")))
				if len(values) == len(payloadTypes) {
					return values, nil
				}
			}
			fieldIndex++
		}
	}
	if len(values) != len(payloadTypes) {
		return nil, fmt.Errorf("missing payload fields for error tag %s", tag)
	}
	return values, nil
}

func (s *functionState) buildErrorUnionValue(unionType *semantic.ErrorUnionType, errorCode C.LLVMValueRef, payload C.LLVMValueRef) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		// A void error union is lowered to its error-set representation: a bare code
		// when the set is payloadless, but the {code, payloads...} struct when it has
		// payloads. Wrap a bare code into that struct so the value matches the type.
		return s.wrapVoidErrorUnionCode(unionType.Errors, errorCode)
	}
	payloadSlot, err := s.createTempStorage("errunion.payload", unionType.Value)
	if err != nil {
		return nil, err
	}
	if err := s.storeValue(payloadSlot, payload, unionType.Value, "errunion.payload"); err != nil {
		return nil, err
	}
	return s.buildErrorUnionValueFromPointer(unionType, errorCode, payloadSlot)
}

func (s *functionState) buildErrorUnionValueFromPointer(unionType *semantic.ErrorUnionType, errorCode C.LLVMValueRef, payloadPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
	if unionType == nil || isVoidType(unionType.Value) || payloadPtr == nil {
		return nil, fmt.Errorf("missing value-carrying error union pointer")
	}
	llvmType, err := s.g.lowerType(unionType)
	if err != nil {
		return nil, err
	}
	errorType, err := s.g.lowerType(unionType.Errors)
	if err != nil {
		return nil, err
	}
	if C.LLVMTypeOf(errorCode) != errorType {
		if !unionType.Errors.HasPayloads() {
			return nil, fmt.Errorf("error union code type mismatch")
		}
		errorSetValue := C.LLVMGetUndef(errorType)
		errorSetValue = C.LLVMBuildInsertValue(s.builder, errorSetValue, errorCode, 0, cStringFree("errset.code"))
		errorCode = errorSetValue
	}
	value := C.LLVMGetUndef(llvmType)
	value = C.LLVMBuildInsertValue(s.builder, value, errorCode, 0, cStringFree("errunion.err"))
	value = C.LLVMBuildInsertValue(s.builder, value, payloadPtr, 1, cStringFree("errunion.value.ptr"))
	return value, nil
}
func (s *functionState) extractErrorUnionCode(value C.LLVMValueRef, unionType *semantic.ErrorUnionType) (C.LLVMValueRef, error) {
	if unionType == nil {
		return nil, fmt.Errorf("missing error union type")
	}
	if isVoidType(unionType.Value) {
		// The void union IS the error-set value (a bare code, or the {code, payloads...}
		// struct); callers extract the scalar code from it via extractErrorSetCode.
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("errunion.code")), nil
}
func (s *functionState) extractErrorUnionPayload(value C.LLVMValueRef, unionType *semantic.ErrorUnionType) (C.LLVMValueRef, error) {
	if unionType == nil || isVoidType(unionType.Value) {
		return nil, fmt.Errorf("error union has no payload")
	}
	payloadPtr := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("errunion.payload.ptr"))
	return s.loadValue(payloadPtr, unionType.Value, "errunion.payload")
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
		return s.g.namedType("bool")
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
		left = backendValueContextOperandType(left)
		right = backendValueContextOperandType(right)
		switch op {
		case lexer.TOKEN_PERCENT, lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_AMPERSAND,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			left = numericCastType(left)
			right = numericCastType(right)
		}
		if isNumericType(left) && isNumericType(right) {
			return semantic.CommonNumericType(left, right)
		}
	}
	return left
}

func backendValueContextOperandType(t semantic.Type) semantic.Type {
	if ref, ok := t.(*semantic.RefType); ok && ref != nil {
		if isNumericType(ref.Elem) || semantic.IsBoolType(ref.Elem) {
			return ref.Elem
		}
	}
	return t
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
