//go:build cgo

package backend

// #include <stdlib.h>
// #include "llvm-c/Core.h"
import "C"

import (
	"elisacore/src/semantic"
)

// The optional-niche sentinel (docs/76 "free null", docs/82 polish): inside an AoS node record,
// an optional child `T?` (T a store-backed packed enum) is stored as the BARE HANDLE, with the
// handle's reserved null sentinel (the top value at the index width; address 0 for `handle: ptr`)
// meaning absent — the index-world pointer-niche optimization. The generic `{bool, handle}`
// carrier remains the ABI everywhere OUTSIDE the record; conversion happens exactly at the
// record boundary (constructor write packs, payload read unpacks). Gated to AoS-mode roots:
// their reads/writes all flow through the typed record chokepoints, so the two conversions
// below cover every path.

// optionalEnumNicheField reports the enum when a payload field type is an optional store-backed
// packed enum stored niche-style in AoS records.
func (g *llvmGenerator) optionalEnumNicheField(t semantic.Type) (*semantic.EnumType, bool) {
	optionalType, ok := t.(*semantic.OptionalType)
	if !ok || optionalType == nil {
		return nil, false
	}
	enumType, ok := semantic.StripAggregateStateType(optionalType.Value).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, false
	}
	if g.packedModeForEnum(enumType) != packedEnumABIAoS {
		return nil, false
	}
	return enumType, true
}

// lowerEnumPayloadFieldType lowers a variant payload field for the RECORD layout: niche fields
// take the handle's type (so the record carries one narrow slot, no presence flag), everything
// else lowers as usual. Field byte offsets derive from this same lowering, so sizing and access
// stay consistent by construction.
func (g *llvmGenerator) lowerEnumPayloadFieldType(t semantic.Type) (C.LLVMTypeRef, error) {
	if enumType, ok := g.optionalEnumNicheField(t); ok {
		return g.lowerPackedEnumType(enumType)
	}
	return g.lowerType(t)
}

// optionalEnumNicheSentinel returns the absent-marker constant in the handle's own width.
func (s *functionState) optionalEnumNicheSentinel(enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	handleType, err := s.g.lowerPackedEnumType(enumType)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(handleType, C.ulonglong(enumType.Root().NullSentinelValue()), 0), nil
}

// packOptionalEnumNicheValue converts the generic `{bool, handle}` carrier into the record's
// niche slot: present ? handle : sentinel.
func (s *functionState) packOptionalEnumNicheValue(carrierValue C.LLVMValueRef, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	present := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(name+".niche.present"))
	handleValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 1, cStringFree(name+".niche.handle"))
	sentinel, err := s.optionalEnumNicheSentinel(enumType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildSelect(s.builder, present, handleValue, sentinel, cStringFree(name+".niche.pack")), nil
}

// unpackOptionalEnumNicheValue converts the record's niche slot back into the generic carrier:
// {handle != sentinel, handle}.
func (s *functionState) unpackOptionalEnumNicheValue(nicheValue C.LLVMValueRef, optionalType *semantic.OptionalType, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	sentinel, err := s.optionalEnumNicheSentinel(enumType)
	if err != nil {
		return nil, err
	}
	present := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), nicheValue, sentinel, cStringFree(name+".niche.unpack.present"))
	carrierType, err := s.g.lowerType(optionalType)
	if err != nil {
		return nil, err
	}
	carrier := C.LLVMGetUndef(carrierType)
	carrier = C.LLVMBuildInsertValue(s.builder, carrier, present, 0, cStringFree(name+".niche.unpack.flag"))
	carrier = C.LLVMBuildInsertValue(s.builder, carrier, nicheValue, 1, cStringFree(name+".niche.unpack.value"))
	return carrier, nil
}
