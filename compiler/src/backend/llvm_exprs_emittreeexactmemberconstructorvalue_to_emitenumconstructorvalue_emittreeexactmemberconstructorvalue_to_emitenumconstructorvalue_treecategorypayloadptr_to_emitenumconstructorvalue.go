//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitEnumConstructorValue(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (llvmIRValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s must be allocated with new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name)
	}
	orderedArgs, err := s.resolveEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	if enumIsTagOnly(enumType) {
		tagValue, err := s.enumTagConstant(enumType, variant.Tag)
		if err != nil {
			return nil, nil, err
		}
		return tagValue, enumType, nil
	}
	enumPtr, err := s.emitStackTempZeroed(enumType, "enum.ctor")
	if err != nil {
		return nil, nil, err
	}
	enumLLVMType, err := s.g.lowerType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tagValue, err := s.enumTagConstant(enumType, variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := llvmBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("enum.tag.ptr"))
	llvmBuildStore(s.builder, tagValue, tagPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
			if err != nil {
				return nil, nil, err
			}
			// docs/76 free-null niche: the payload slot for an optional packed-enum child holds
			// the bare handle (sentinel = absent). The generic {bool, handle} carrier converts at
			// the record boundary — pack on write here, mirroring emitPackedEnumConstructorPayloadValue
			// (the match-read path already unpacks symmetrically for plain enums).
			if nicheEnum, ok := s.g.optionalEnumNicheField(variant.Payload[0]); ok {
				argValue, err = s.packOptionalEnumNicheValue(argValue, nicheEnum, "enum.payload")
				if err != nil {
					return nil, nil, err
				}
			}
			if !llvmValueIsZeroConstant(argValue) {
				llvmBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := llvmGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
				if err != nil {
					return nil, nil, err
				}
				// docs/76 free-null niche: pack the {bool, handle} carrier into the bare-handle
				// slot at the record boundary (see the single-payload branch above).
				if nicheEnum, ok := s.g.optionalEnumNicheField(payload); ok {
					argValue, err = s.packOptionalEnumNicheValue(argValue, nicheEnum, "enum.payload")
					if err != nil {
						return nil, nil, err
					}
				}
				// An insertvalue with a mismatched operand type only fails much later, at module
				// verification, with no source context. Catch it here and name the enum, variant,
				// field, and both types so the miscompile is diagnosable.
				if expectedField := llvmStructGetTypeAtIndex(payloadType, llvmIRUnsigned(i)); llvmTypeOf(argValue) != expectedField {
					fieldName := ""
					if i < len(variant.PayloadNames) {
						fieldName = variant.PayloadNames[i]
					}
					gotStr := llvmTypeString(llvmTypeOf(argValue))
					wantStr := llvmTypeString(expectedField)
					return nil, nil, fmt.Errorf("enum constructor %s.%s payload field %d (%s: %s): argument lowers to LLVM type %s but the payload slot expects %s", enumType.Name, variant.Name, i, fieldName, payload, gotStr, wantStr)
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = llvmBuildInsertValue(s.builder, aggregate, argValue, llvmIRUnsigned(i), cStringFree("enum.payload.ins"))
			}
			if !allZero {
				llvmBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	value, err := s.loadValue(enumPtr, enumType, "enum.value")
	if err != nil {
		return nil, nil, err
	}
	return value, enumType, nil
}
