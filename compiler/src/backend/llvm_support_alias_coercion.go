//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// isAliasSafeScalarElemType reports element types whose buffer memory can never hold a darray/view
// header: numeric scalars, bool, const enums, and (recursively) plain structs and non-packed enums
// built only from those. Such element memory and header memory are disjoint by construction, so
// element accesses ("elt") and header loads ("hdr") may be tagged as not aliasing each other.
func isAliasSafeScalarElemType(t semantic.Type) bool {
	return aliasSafeElemType(t, 0)
}

func aliasSafeElemType(t semantic.Type, depth int) bool {
	if t == nil || depth > 8 {
		return false
	}
	if isNumericType(t) || semantic.IsBoolType(t) {
		return true
	}
	switch tt := t.(type) {
	case *semantic.ConstEnumType:
		return true
	case *semantic.StructType:
		if tt == nil || len(tt.TypeParams) > 0 || len(tt.RegionParams) > 0 || tt.Fields == nil || isRuntimeContainerCarrierStruct(tt) {
			return false
		}
		for _, field := range tt.Fields {
			if !aliasSafeElemType(field.Type, depth+1) {
				return false
			}
		}
		return true
	case *semantic.EnumType:
		if tt == nil || tt.Packed || tt.StoreBackedPlain || tt.StoreType != nil {
			return false
		}
		for _, field := range tt.Common {
			if !aliasSafeElemType(field.Type, depth+1) {
				return false
			}
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				return false
			}
			for _, payload := range variant.Payload {
				if !aliasSafeElemType(payload, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// markElementDerivedPtr records an address inside a darray/view element buffer whose type is a
// plain struct that is not alias-safe as a whole (it contains a header somewhere).
func (s *functionState) markElementDerivedPtr(ptr llvmIRValueRef) {
	if s == nil || ptr == nil {
		return
	}
	if s.elementDerivedPtrs == nil {
		s.elementDerivedPtrs = map[llvmIRValueRef]bool{}
	}
	s.elementDerivedPtrs[ptr] = true
}

// propagateAliasSafeFieldPtr marks a field address derived from an element address. A field of
// an alias-safe element is alias-safe. A field of an element-derived plain struct is alias-safe
// when its own type is (struct fields never overlap, so a scalar leaf is never header memory even
// when a sibling field is a darray), and a nested plain struct field stays element-derived.
// Enum payloads are excluded: variants overlap, so a scalar slot of one variant may be a header
// slot of another.
func (s *functionState) propagateAliasSafeFieldPtr(objPtr, fieldPtr llvmIRValueRef, containerType, fieldType semantic.Type, pointerLike bool) {
	if s == nil || pointerLike || objPtr == nil || fieldPtr == nil {
		return
	}
	if s.aliasSafeElementPtrs != nil && s.aliasSafeElementPtrs[objPtr] {
		s.aliasSafeElementPtrs[fieldPtr] = true
		return
	}
	if s.elementDerivedPtrs == nil || !s.elementDerivedPtrs[objPtr] {
		return
	}
	containerStruct, ok := containerType.(*semantic.StructType)
	if !ok || containerStruct == nil || isRuntimeContainerCarrierStruct(containerStruct) {
		return
	}
	if isAliasSafeScalarElemType(fieldType) {
		s.markAliasSafeElementPtr(fieldPtr)
		return
	}
	if fieldStruct, ok := fieldType.(*semantic.StructType); ok && !isRuntimeContainerCarrierStruct(fieldStruct) {
		s.markElementDerivedPtr(fieldPtr)
	}
}

// isRuntimeContainerCarrierStruct reports the std structs that share memory layout with
// darray/view headers (the runtime reads headers through them), so their scalar fields ARE
// header memory and must never be elt-tagged.
func isRuntimeContainerCarrierStruct(t *semantic.StructType) bool {
	switch t.Name {
	case "DynArray", "DynArrayView", "DynDict":
		return true
	}
	return false
}

const (
	darrayAliasDomain   = "elisa.darray.aa"
	darrayAliasScopeHdr = "hdr"
	darrayAliasScopeElt = "elt"
)

// tagDarrayHeaderLoad marks a darray header (data-pointer) load as not aliasing scalar element memory.
func (s *functionState) tagDarrayHeaderLoad(load llvmIRValueRef) {
	if s == nil || load == nil {
		return
	}
	if llvmIsALoadInst(load) == nil {
		return
	}
	s.attachAliasScopeMetadataWithNames(load, darrayAliasDomain, darrayAliasScopeHdr, []string{darrayAliasScopeElt})
}

// markAliasSafeElementPtr records a scalar-darray element address so subsequent loads/stores through
// it get tagged with the "elt" scope (noalias "hdr") in loadValue/storeValue.
func (s *functionState) markAliasSafeElementPtr(ptr llvmIRValueRef) {
	if s == nil || ptr == nil {
		return
	}
	if s.aliasSafeElementPtrs == nil {
		s.aliasSafeElementPtrs = map[llvmIRValueRef]bool{}
	}
	s.aliasSafeElementPtrs[ptr] = true
}

// tagDarrayElementAccess marks a scalar-darray element load/store as not aliasing header memory.
func (s *functionState) tagDarrayElementAccess(inst llvmIRValueRef, ptr llvmIRValueRef) {
	if s == nil || inst == nil || ptr == nil {
		return
	}
	if s.comprehensionElementScopes != nil {
		if access, ok := s.comprehensionElementScopes[ptr]; ok {
			// The generated map loop has a fresh result allocation. Its source and
			// destination element accesses are therefore disjoint, but only inside
			// this loop-specific metadata domain.
			if s.aliasSafeElementPtrs != nil && s.aliasSafeElementPtrs[ptr] {
				s.attachDarrayAndComprehensionScope(inst, access.domainName, access.aliasScope, access.noAliasScope)
			} else {
				s.attachAliasScopeMetadataWithNames(inst, access.domainName, access.aliasScope, []string{access.noAliasScope})
			}
			return
		}
	}
	aliasSafe := s.aliasSafeElementPtrs != nil && s.aliasSafeElementPtrs[ptr]
	var disjointScope *disjointParamScope
	if s.disjointElementPtrs != nil {
		disjointScope = s.disjointElementPtrs[ptr]
	}
	if aliasSafe && disjointScope != nil {
		s.tagDarrayAndDisjointParamElementAccess(inst, disjointScope)
		return
	}
	if aliasSafe {
		s.attachAliasScopeMetadataWithNames(inst, darrayAliasDomain, darrayAliasScopeElt, []string{darrayAliasScopeHdr})
	}
	if disjointScope != nil {
		s.tagDisjointParamElementAccess(inst, disjointScope)
	}
}

func (s *functionState) markComprehensionElementPtr(object ast.Expr, ptr llvmIRValueRef) {
	if s == nil || ptr == nil || s.comprehensionAliasContext == nil {
		return
	}
	ident, ok := object.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	context := s.comprehensionAliasContext
	access := comprehensionAliasAccess{}
	switch ident.Name {
	case context.sourceName:
		access = comprehensionAliasAccess{domainName: context.domainName, aliasScope: context.sourceScope, noAliasScope: context.destinationScope}
	case context.destinationName:
		access = comprehensionAliasAccess{domainName: context.domainName, aliasScope: context.destinationScope, noAliasScope: context.sourceScope}
	default:
		return
	}
	if s.comprehensionElementScopes == nil {
		s.comprehensionElementScopes = map[llvmIRValueRef]comprehensionAliasAccess{}
	}
	s.comprehensionElementScopes[ptr] = access
}
func (s *functionState) loweredEnumStorageType(enumType *semantic.EnumType) (llvmIRTypeRef, error) {
	if enumType == nil {
		return nil, fmt.Errorf("missing enum type")
	}
	if enumType.Packed {
		return s.g.ensurePackedEnumStorageType(enumType)
	}
	return s.g.lowerType(enumType)
}
func (s *functionState) coercePackedEnumHandleValue(value llvmIRValueRef, actual semantic.Type, expected *semantic.EnumType) (llvmIRValueRef, bool, error) {
	if s == nil || s.g == nil || expected == nil || !expected.Packed {
		return nil, false, nil
	}
	switch s.g.packedModeForEnum(expected) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		if !isNumericCastType(actual) {
			return nil, false, nil
		}
		coerced, err := s.coerceNumericValue(value, actual, s.g.namedType("u32"))
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
	default:
		return nil, false
	}
}
func (s *functionState) loadValue(ptr llvmIRValueRef, t semantic.Type, name string) (llvmIRValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	if err := s.emitDebugPointerDerefGuard(ptr); err != nil {
		return nil, err
	}
	load := llvmBuildLoad2(s.builder, llvmType, ptr, cStringFree(name))
	s.tagDarrayElementAccess(load, ptr)
	return load, nil
}

// derefGuardNearNullLimit mirrors DEBUG_REFEREE_NEAR_NULL_LIMIT in debug_referee.elisa:
// any host address below this is null or near-null and never a valid dereference target
// (real stack/heap/global/guest-mapped addresses all live far above it).
const derefGuardNearNullLimit = 0x10000

// emitDebugPointerDerefGuard traps just before a load when the address is null or
// near-null. It is the dereference analogue of the index-bounds watchdog, but is gated
// on -fbounds-check (forceBoundsCheck) ONLY -- not plain -O0 -- because it instruments
// every runtime-derived load. It catches null-base field/element dereferences (e.g. a
// struct field read off a null pointer faulting at 0x8/0x68) at the deref site, with
// the offending address recorded into the -ftrace ring, instead of letting them surface
// later as a SIGSEGV on a derived bad address. Compile-time-valid addresses (allocas,
// globals, constants) are skipped.
func (s *functionState) emitDebugPointerDerefGuard(ptr llvmIRValueRef) error {
	if s == nil || s.g == nil || !s.g.forceBoundsCheck {
		return nil
	}
	if s.builder == nil || s.fnValue == nil || ptr == nil {
		return nil
	}
	if llvmValueIsTriviallyValidPtr(ptr) != 0 {
		return nil
	}
	i64 := llvmInt64TypeInContext(s.g.context)
	ptrInt := llvmBuildPtrToInt(s.builder, ptr, i64, cStringFree("pg.addr"))
	limit := llvmConstInt(i64, llvmIRUnsignedLongLong(derefGuardNearNullLimit), 0)
	valid := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntUGE), ptrInt, limit, cStringFree("pg.valid"))
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pg.ok"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pg.fail"))
	llvmBuildCondBr(s.builder, valid, okBB, failBB)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if s.g.emitTrace && s.g.trace != nil {
		s.g.trace.recordValue(s, 0, "deref.nullptr", ptrInt, s.g.namedType("usize"))
	}
	if err := s.emitTrapUnreachable("pg.trap"); err != nil {
		return err
	}
	llvmPositionBuilderAtEnd(s.builder, okBB)
	return nil
}

// resizeUnsignedInt resizes an integer value to the target integer type (zext when widening,
// trunc when narrowing, no-op at equal width) — the docs/82 handle-boundary cast.
func (s *functionState) resizeUnsignedInt(value llvmIRValueRef, target llvmIRTypeRef, name string) llvmIRValueRef {
	fromBits := llvmGetIntTypeWidth(llvmTypeOf(value))
	toBits := llvmGetIntTypeWidth(target)
	switch {
	case fromBits < toBits:
		return llvmBuildZExt(s.builder, value, target, cStringFree(name))
	case fromBits > toBits:
		return llvmBuildTrunc(s.builder, value, target, cStringFree(name))
	default:
		return value
	}
}

func (s *functionState) coerceValue(value llvmIRValueRef, actual semantic.Type, expected semantic.Type) (llvmIRValueRef, error) {
	if expected == nil || actual == nil || semantic.SameType(actual, expected) {
		return value, nil
	}
	// docs/82: a packed enum's opaque handle may be narrower/wider than the u32 the store runtime
	// traffics in (`layout(handle: uN)`). Coerce integer ↔ handle at the boundary with an
	// unsigned int resize; same-width is a no-op.
	if value != nil && llvmGetTypeKind(llvmTypeOf(value)) == llvmIntegerTypeKind {
		if expectedEnum, ok := semantic.StripAggregateStateType(expected).(*semantic.EnumType); ok && expectedEnum != nil && expectedEnum.Packed && isNumericType(actual) {
			handleType, err := s.g.lowerType(expectedEnum)
			if err != nil {
				return nil, err
			}
			return s.resizeUnsignedInt(value, handleType, "handle.resize"), nil
		}
		if actualEnum, ok := semantic.StripAggregateStateType(actual).(*semantic.EnumType); ok && actualEnum != nil && actualEnum.Packed && isNumericType(expected) && !isFloatType(expected) {
			expectedLLVM, err := s.g.lowerType(expected)
			if err != nil {
				return nil, err
			}
			if llvmGetTypeKind(expectedLLVM) == llvmIntegerTypeKind {
				return s.resizeUnsignedInt(value, expectedLLVM, "handle.resize"), nil
			}
		}
	}
	if actualRef, ok := actual.(*semantic.RefType); ok && actualRef != nil {
		if _, expectedIsRef := expected.(*semantic.RefType); !expectedIsRef && (isNumericType(actualRef.Elem) || semantic.IsBoolType(actualRef.Elem)) {
			// A reference to a numeric/bool auto-dereferences to its pointee value when
			// coerced to a numeric/bool target -- including across numeric kinds, e.g. a
			// `usize&` parameter used as `u64` (off.u64()). The sole exception is uintptr:
			// `someRef.uintptr()` (esp. x.ref[T&].uintptr()) is the established address-of
			// idiom, so coercing a reference to uintptr keeps its pointer bits (ptrtoint).
			derefToValue := semantic.SameType(actualRef.Elem, expected) ||
				((isNumericType(expected) || semantic.IsBoolType(expected)) && !isUintptrType(expected))
			if derefToValue {
				loaded, err := s.loadValue(value, actualRef.Elem, "ref.value")
				if err != nil {
					return nil, err
				}
				return s.coerceValue(loaded, actualRef.Elem, expected)
			}
		}
	}
	if semantic.IsNeverType(actual) {
		if isVoidType(expected) {
			return nil, nil
		}
		llvmType, err := s.g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		return llvmGetUndef(llvmType), nil
	}
	if expectedBuiltin, ok := expected.(*semantic.BuiltinType); ok && expectedBuiltin.Name == "bool" {
		actualLLVM, err := s.g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		zero := llvmConstNull(actualLLVM)
		if isFloatType(actual) {
			// C truthiness treats NaN as nonzero. Use unordered-not-equal so
			// `if (nan)` remains true instead of using ordered-not-equal.
			return llvmBuildFCmp(s.builder, llvmIRRealPredicate(llvmRealUNE), value, zero, cStringFree("tobool")), nil
		}
		return llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), value, zero, cStringFree("tobool")), nil
	}
	if expectedErrSet, ok := expected.(*semantic.ErrorSetType); ok {
		if actualErrSet, ok := actual.(*semantic.ErrorSetType); ok {
			return s.remapErrorCode(value, actualErrSet, expectedErrSet)
		}
	}
	if actualAddr, ok := actual.(*semantic.AddressSpaceType); ok {
		if semantic.SameType(actualAddr.Storage, expected) {
			return value, nil
		}
		if isPointerLikeType(expected) {
			expectedLLVM, err := s.g.lowerType(expected)
			if err != nil {
				return nil, err
			}
			return llvmBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("addrspace.inttoptr")), nil
		}
	}
	if expectedAddr, ok := expected.(*semantic.AddressSpaceType); ok {
		if semantic.SameType(actual, expectedAddr.Storage) {
			return value, nil
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
			if !expectedEnum.Packed && binding.ptr != nil {
				return s.loadValue(binding.ptr, expectedEnum, "enum.view.value")
			}
			return binding.handle, nil
		}
		if coerced, ok, err := s.coercePackedEnumHandleValue(value, actual, expectedEnum); ok || err != nil {
			return coerced, err
		}
	}
	if expectedView, ok := expected.(*semantic.PackedVariantViewType); ok {
		if actualEnum, ok := actual.(*semantic.EnumType); ok && actualEnum == expectedView.Enum && actualEnum.Packed {
			var store *packedStoreBinding
			if actualEnum.StoreType != nil {
				resolvedStore, ok := s.lookupPackedStore(actualEnum)
				if ok {
					store = &resolvedStore
				}
			}
			return s.buildPackedVariantViewValue(expectedView, value, store)
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
		return llvmConstNull(expectedLLVM), nil
	}
	if isNumericCastType(actual) && isNumericCastType(expected) {
		return s.coerceNumericValue(value, actual, expected)
	}
	if isPointerLikeType(actual) && isNumericCastType(expected) {
		return llvmBuildPtrToInt(s.builder, value, expectedLLVM, cStringFree("ptrtoint")), nil
	}
	if isNumericCastType(actual) && isPointerLikeType(expected) {
		// LLVM requires integer-typed input for inttoptr; float->ptr must lower
		// through an explicit float->uintptr conversion first.
		if isFloatType(actual) {
			uintptrType := s.g.namedType("uintptr")
			if uintptrType == nil {
				return nil, fmt.Errorf("missing builtin uintptr type for float-to-pointer cast")
			}
			coerced, err := s.coerceNumericValue(value, actual, uintptrType)
			if err != nil {
				return nil, err
			}
			return llvmBuildIntToPtr(s.builder, coerced, expectedLLVM, cStringFree("inttoptr")), nil
		}
		return llvmBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("inttoptr")), nil
	}
	return value, nil
}
