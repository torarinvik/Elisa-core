//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

// elisacoreValueIsTriviallyValidPtr reports whether an LLVM value used as a load
// address is provably a valid (non-null, non-near-null) pointer at compile time, so
// the null-deref guard can skip it: stack slots (alloca), module globals/functions,
// and constants (e.g. a constant GEP off a global) all have addresses far from the
// near-null zone. Pointers derived at runtime (GEP off a loaded base, inttoptr, call
// results) return 0 and get guarded.
static int elisacoreValueIsTriviallyValidPtr(LLVMValueRef v) {
	if (v == NULL) {
		return 1;
	}
	if (LLVMIsAAllocaInst(v) != NULL) {
		return 1;
	}
	if (LLVMIsAGlobalValue(v) != NULL) {
		return 1;
	}
	if (LLVMIsConstant(v)) {
		return 1;
	}
	return 0;
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

// emitIndexAddress computes the address of an indexed element. userFacing marks
// a genuine user `arr[i]` access (vs compiler-internal carrier reads): only such
// accesses receive the debug-mode bounds watchdog, so debug verification stays
// at the user-operation boundary and never instruments internal machinery.
func (s *functionState) emitIndexAddress(expr *ast.IndexExpr, userFacing bool) (C.LLVMValueRef, semantic.Type, error) {
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
		if userFacing {
			if err := s.emitDebugIndexBoundsGuard(containerPtr, t, indexValue); err != nil {
				return nil, nil, err
			}
		}
		return s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
	case *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		if userFacing {
			if err := s.emitDebugIndexBoundsGuard(containerPtr, t, indexValue); err != nil {
				return nil, nil, err
			}
		}
		return s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
	case *semantic.RefType:
		basePtr, _, err := s.emitExpr(expr.Object, nil)
		if err != nil {
			return nil, nil, err
		}
		// Indexing through a reference dereferences basePtr; guard it so a null/near-null
		// ref traps at the index site (covers `nullRef[i]` reads and writes).
		if err := s.emitDebugPointerDerefGuard(basePtr); err != nil {
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
			if userFacing {
				if err := s.emitDebugIndexBoundsGuard(basePtr, t.Elem, indexValue); err != nil {
					return nil, nil, err
				}
			}
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

// emitDebugIndexBoundsGuard is the debug-mode watchdog: in debug builds
// (OptimizationLevel0) every dynamic container index is bounds-checked at
// runtime and traps on violation — so statically-proven and `trusted`-unchecked
// accesses, which emit no check in release, are still verified while testing.
// In release builds this is a no-op (zero overhead): "debug verifies what
// release assumes." Only containers carrying a runtime count are guarded.
func (s *functionState) emitDebugIndexBoundsGuard(containerPtr C.LLVMValueRef, containerType semantic.Type, indexValue C.LLVMValueRef) error {
	if s == nil || s.g == nil {
		return nil
	}
	// Active in debug (-O0) builds, or in any build when forced via -fbounds-check
	// (ELISACORE_FORCE_BOUNDS_CHECK). The forced mode lets optimized/cross builds trap
	// at the offending indexing site rather than crashing later on a derived bad pointer.
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceBoundsCheck {
		return nil
	}
	switch containerType.(type) {
	case *semantic.DArrayType, *semantic.ViewType:
	default:
		return nil
	}
	countValue, err := s.emitContainerCountValue(containerPtr, containerType, "wd.count")
	if err != nil {
		return err
	}
	inBounds := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("wd.in_bounds"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("wd.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("wd.fail"))
	C.LLVMBuildCondBr(s.builder, inBounds, okBB, failBB)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	// When -ftrace is also on, record the offending index and the container count into
	// the trace ring just before trapping, so the fault dump shows the exact values
	// (e.g. `oob.index = 4294967295`, `oob.count = 1712`) at the right site.
	if s.g.emitTrace && s.g.trace != nil {
		usize := s.g.result.NamedTypes["usize"]
		line := 0
		s.g.trace.recordValue(s, line, "oob.index", indexValue, usize)
		s.g.trace.recordValue(s, line, "oob.count", countValue, usize)
	}
	if err := s.emitTrapUnreachable("wd.trap"); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
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
	if isNumericType(elemType) {
		// Scalar element buffer can never contain a darray header, so the data-pointer load (header
		// memory) provably never aliases the element stores (buffer memory). Tag both so LLVM hoists
		// the base-pointer load out of hot loops. See aliasSafeElementPtrs.
		s.tagDarrayHeaderLoad(dataPtr)
		s.markAliasSafeElementPtr(ptr)
	}
	return ptr, elemType, nil
}

const (
	darrayAliasDomain   = "elisa.darray.aa"
	darrayAliasScopeHdr = "hdr"
	darrayAliasScopeElt = "elt"
)

// tagDarrayHeaderLoad marks a darray header (data-pointer) load as not aliasing scalar element memory.
func (s *functionState) tagDarrayHeaderLoad(load C.LLVMValueRef) {
	if s == nil || load == nil {
		return
	}
	if C.LLVMIsALoadInst(load) == nil {
		return
	}
	s.attachAliasScopeMetadataWithNames(load, darrayAliasDomain, darrayAliasScopeHdr, []string{darrayAliasScopeElt})
}

// markAliasSafeElementPtr records a scalar-darray element address so subsequent loads/stores through
// it get tagged with the "elt" scope (noalias "hdr") in loadValue/storeValue.
func (s *functionState) markAliasSafeElementPtr(ptr C.LLVMValueRef) {
	if s == nil || ptr == nil {
		return
	}
	if s.aliasSafeElementPtrs == nil {
		s.aliasSafeElementPtrs = map[C.LLVMValueRef]bool{}
	}
	s.aliasSafeElementPtrs[ptr] = true
}

// tagDarrayElementAccess marks a scalar-darray element load/store as not aliasing header memory.
func (s *functionState) tagDarrayElementAccess(inst C.LLVMValueRef, ptr C.LLVMValueRef) {
	if s == nil || inst == nil || ptr == nil || s.aliasSafeElementPtrs == nil {
		return
	}
	if !s.aliasSafeElementPtrs[ptr] {
		return
	}
	s.attachAliasScopeMetadataWithNames(inst, darrayAliasDomain, darrayAliasScopeElt, []string{darrayAliasScopeHdr})
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
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
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
	default:
		return nil, false
	}
}
func (s *functionState) loadValue(ptr C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	if err := s.emitDebugPointerDerefGuard(ptr); err != nil {
		return nil, err
	}
	load := C.LLVMBuildLoad2(s.builder, llvmType, ptr, cStringFree(name))
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
func (s *functionState) emitDebugPointerDerefGuard(ptr C.LLVMValueRef) error {
	if s == nil || s.g == nil || !s.g.forceBoundsCheck {
		return nil
	}
	if s.builder == nil || s.fnValue == nil || ptr == nil {
		return nil
	}
	if C.elisacoreValueIsTriviallyValidPtr(ptr) != 0 {
		return nil
	}
	i64 := C.LLVMInt64TypeInContext(s.g.context)
	ptrInt := C.LLVMBuildPtrToInt(s.builder, ptr, i64, cStringFree("pg.addr"))
	limit := C.LLVMConstInt(i64, C.ulonglong(derefGuardNearNullLimit), 0)
	valid := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGE), ptrInt, limit, cStringFree("pg.valid"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pg.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pg.fail"))
	C.LLVMBuildCondBr(s.builder, valid, okBB, failBB)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if s.g.emitTrace && s.g.trace != nil {
		s.g.trace.recordValue(s, 0, "deref.nullptr", ptrInt, s.g.result.NamedTypes["usize"])
	}
	if err := s.emitTrapUnreachable("pg.trap"); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}
func (s *functionState) coerceValue(value C.LLVMValueRef, actual semantic.Type, expected semantic.Type) (C.LLVMValueRef, error) {
	if expected == nil || actual == nil || semantic.SameType(actual, expected) {
		return value, nil
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
	if actualAddr, ok := actual.(*semantic.AddressSpaceType); ok {
		if semantic.SameType(actualAddr.Storage, expected) {
			return value, nil
		}
		if isPointerLikeType(expected) {
			expectedLLVM, err := s.g.lowerType(expected)
			if err != nil {
				return nil, err
			}
			return C.LLVMBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("addrspace.inttoptr")), nil
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
	if expectedNode, ok := semantic.StripAggregateStateType(expected).(*semantic.TreeNodeType); ok && expectedNode != nil && expectedNode.Family != nil && treeFamilyLayoutPlan(expectedNode.Family).isCategoryUnion() {
		switch actualTree := semantic.StripAggregateStateType(actual).(type) {
		case *semantic.TreeVariantViewType:
			if actualTree != nil && actualTree.Category != nil && actualTree.Category.Family == expectedNode.Family {
				tagType, err := s.g.lowerBuiltin("u32")
				if err != nil {
					return nil, err
				}
				tagValue := C.LLVMConstInt(tagType, C.ulonglong(actualTree.Variant.Tag), 0)
				return s.emitTreeCategoryUnionRootHandle(value, actualTree.Category, tagValue, "tree.root.coerce")
			}
		case *semantic.TreeCategoryType:
			if actualTree != nil && actualTree.Family == expectedNode.Family {
				tagValue, err := s.extractTreeCategoryTagValue(value, actualTree)
				if err != nil {
					return nil, err
				}
				return s.emitTreeCategoryUnionRootHandle(value, actualTree, tagValue, "tree.root.coerce")
			}
		case *semantic.TreeBlockType:
			if actualTree != nil && actualTree.Family == expectedNode.Family {
				return value, nil
			}
		case *semantic.TreeStructType:
			if actualTree != nil && actualTree.Family == expectedNode.Family {
				return value, nil
			}
		}
	}
	if actualNode, ok := semantic.StripAggregateStateType(actual).(*semantic.TreeNodeType); ok && actualNode != nil && actualNode.Family != nil && treeFamilyLayoutPlan(actualNode.Family).isCategoryUnion() {
		switch expectedTree := semantic.StripAggregateStateType(expected).(type) {
		case *semantic.TreeCategoryType:
			if expectedTree != nil && expectedTree.Family == actualNode.Family {
				return s.emitTreeRootUnionCategoryHandleForTarget(value, actualNode.Family, expectedTree, "tree.root.project")
			}
		case *semantic.TreeBlockType:
			if expectedTree != nil && expectedTree.Family == actualNode.Family {
				return s.emitTreeRootUnionExactHandleForTarget(value, actualNode.Family, expectedTree, "tree.root.project")
			}
		case *semantic.TreeStructType:
			if expectedTree != nil && expectedTree.Family == actualNode.Family {
				return s.emitTreeRootUnionExactHandleForTarget(value, actualNode.Family, expectedTree, "tree.root.project")
			}
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
		// LLVM requires integer-typed input for inttoptr; float->ptr must lower
		// through an explicit float->uintptr conversion first.
		if isFloatType(actual) {
			uintptrType := s.g.result.NamedTypes["uintptr"]
			if uintptrType == nil {
				return nil, fmt.Errorf("missing builtin uintptr type for float-to-pointer cast")
			}
			coerced, err := s.coerceNumericValue(value, actual, uintptrType)
			if err != nil {
				return nil, err
			}
			return C.LLVMBuildIntToPtr(s.builder, coerced, expectedLLVM, cStringFree("inttoptr")), nil
		}
		return C.LLVMBuildIntToPtr(s.builder, value, expectedLLVM, cStringFree("inttoptr")), nil
	}
	return value, nil
}

func (s *functionState) emitTreeRootUnionCategoryHandleForTarget(rootValue C.LLVMValueRef, family *semantic.TreeType, category *semantic.TreeCategoryType, name string) (C.LLVMValueRef, error) {
	if family == nil || category == nil || category.Family != family {
		return nil, fmt.Errorf("tree root projection has mismatched category metadata")
	}
	stateValue, err := s.emitTreeCategoryUnionContextStateValue(family, name)
	if err != nil {
		return nil, err
	}
	tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, family, name)
	if err != nil {
		return nil, err
	}
	rowIndex, err := s.emitTreeHandleIndexValue(rootValue, name+".index")
	if err != nil {
		return nil, err
	}
	kindValue, err := s.emitTreeRootUnionKindValueAtIndex(tablePtr, family, rowIndex, name)
	if err != nil {
		return nil, err
	}
	u32Type, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, kindValue, failBB, C.unsigned(len(category.Variants)))
	incomingValues := make([]C.LLVMValueRef, 0, len(category.Variants))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(category.Variants))
	for _, variant := range category.Variants {
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".case"))
		C.LLVMAddCase(switchInst, C.LLVMConstInt(C.LLVMTypeOf(kindValue), C.ulonglong(variant.Tag), 0), caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		payloadValue, _, err := s.emitTreeRootUnionPayloadForHandle(rootValue, family, name)
		if err != nil {
			return nil, err
		}
		handleValue := C.LLVMBuildExtractValue(s.builder, payloadValue, 1, cStringFree(name+".handle"))
		incomingValues = append(incomingValues, handleValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)
	}
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, u32Type, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func (s *functionState) emitTreeRootUnionExactHandleForTarget(rootValue C.LLVMValueRef, family *semantic.TreeType, expected semantic.Type, name string) (C.LLVMValueRef, error) {
	if family == nil || expected == nil {
		return nil, fmt.Errorf("tree root projection has missing exact metadata")
	}
	tag, ok := treeExactMemberTag(expected)
	if !ok {
		return nil, fmt.Errorf("tree root projection target %s has no exact tag", treeExactMemberSurfaceName(expected))
	}
	stateValue, err := s.emitTreeCategoryUnionContextStateValue(family, name)
	if err != nil {
		return nil, err
	}
	tablePtr, err := s.emitTreeRootUnionTablePtr(stateValue, family, name)
	if err != nil {
		return nil, err
	}
	rowIndex, err := s.emitTreeHandleIndexValue(rootValue, name+".index")
	if err != nil {
		return nil, err
	}
	kindValue, err := s.emitTreeRootUnionKindValueAtIndex(tablePtr, family, rowIndex, name)
	if err != nil {
		return nil, err
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), kindValue, C.LLVMConstInt(C.LLVMTypeOf(kindValue), C.ulonglong(tag), 0), cStringFree(name+".matches"))
	C.LLVMBuildCondBr(s.builder, condValue, successBB, failBB)
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	return rootValue, nil
}

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
	payload, err := s.zeroValue(unionType.Value)
	if err != nil {
		return nil, err
	}
	return s.buildErrorUnionValue(unionType, errorCode, payload)
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
	value = C.LLVMBuildInsertValue(s.builder, value, payload, 1, cStringFree("errunion.value"))
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
		left = backendValueContextOperandType(left)
		right = backendValueContextOperandType(right)
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
