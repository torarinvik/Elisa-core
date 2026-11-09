//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"sort"
)

func (s *functionState) packedEnumVariantPayloadFieldByteOffset(enumType *semantic.EnumType, variant *semantic.EnumVariant, payloadIndex int) (uint64, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || payloadIndex < 0 || payloadIndex >= len(variant.Payload) {
		return 0, false, nil
	}
	payloadFieldIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return 0, false, err
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return 0, false, err
	}
	baseOffsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, payloadFieldIndex)
	if err != nil {
		return 0, false, err
	}
	if len(variant.Payload) > 1 {
		payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
		if err != nil {
			return 0, false, err
		}
		fieldOffsetBytes, err := s.g.abiOffsetOfLLVMElement(payloadType, payloadIndex)
		if err != nil {
			return 0, false, err
		}
		return baseOffsetBytes + fieldOffsetBytes, true, nil
	}
	return baseOffsetBytes, true, nil
}
func (s *functionState) readPackedEnumTagWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s tag read requires store context", enumType.Name)
	}
	return ops.storeTagAt(handleValue, enumType, "packed.tag.store")
}
func packedMatchNeedsEagerDecode(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if matchPatternNeedsPayloadDecode(arm.Pattern) {
			return true
		}
	}
	return false
}
func packedMatchHasWidePayloadAccess(arms []ast.MatchArm, minArgs int) bool {
	if minArgs <= 0 {
		return false
	}
	for _, arm := range arms {
		pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
		if !ok {
			continue
		}
		if len(pattern.Args) >= minArgs {
			return true
		}
	}
	return false
}
func (s *functionState) preloadPackedMatchCommonFieldValues(enumType *semantic.EnumType, matchValue ast.Expr, enumValue C.LLVMValueRef, decodedMatchValue C.LLVMValueRef, store *packedStoreBinding, arms []ast.MatchArm) (packedPayloadValueCache, error) {
	var values packedPayloadValueCache
	if s == nil || enumType == nil || !enumType.Packed || len(enumType.Common) == 0 || !packedEnumMatchCanUseTagSwitch(enumType, arms) {
		return values, nil
	}
	ident, ok := matchValue.(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" || !matchArmsReadMatchedValueField(ident.Name, arms) {
		return values, nil
	}
	origin := packedReadOriginKey{}
	if resolvedOrigin, ok, err := s.packedReadOriginKey(matchValue); err != nil {
		return values, err
	} else if ok {
		origin = resolvedOrigin
	}
	var ops *packedStoreOps
	if store != nil {
		if resolvedOps, ok := s.packedStoreOpsFromBinding(store); ok {
			ops = resolvedOps
		}
	}
	fieldNames := make([]string, 0, len(enumType.Common))
	for fieldName := range enumType.Common {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)
	for _, fieldName := range fieldNames {
		layout, err := s.g.packedEnumCommonFieldLayout(enumType, fieldName)
		if err != nil {
			return values, err
		}
		fieldType := layout.Field.Type
		cacheName := "packed.match.common.preload." + fieldName
		var fieldValue C.LLVMValueRef
		switch {
		case !layout.StoredInline:
			if store == nil {
				continue
			}
			fieldValue, err = s.emitPackedSideTableFieldRead(enumValue, enumType, store, fieldType, layout.SideWordOffset, layout.WordCount, origin, cacheName)
			if err != nil {
				return values, err
			}
		case decodedMatchValue != nil:
			containerType, err := s.loweredEnumStorageType(enumType)
			if err != nil {
				return values, err
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, decodedMatchValue, C.unsigned(layout.RowFieldIndex), cStringFree(cacheName+".ptr"))
			fieldValue, err = s.loadValue(fieldPtr, fieldType, cacheName)
			if err != nil {
				return values, err
			}
		case ops != nil && ops.canDirectWordRead():
			fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(enumType, layout.RowFieldIndex)
			if err != nil {
				return values, err
			}
			if !ok {
				continue
			}
			fieldValue, err = s.emitPackedDirectFieldReadAtOrigin(ops, enumValue, enumType, fieldType, fieldOffsetBytes, origin, cacheName)
			if err != nil {
				return values, err
			}
		default:
			continue
		}
		if fieldValue != nil {
			values.add(fieldName, fieldValue)
		}
	}
	return values, nil
}
func packedMatchShouldEagerDecode(view semantic.CodegenReadView, abi packedEnumABIMode, enumType *semantic.EnumType, matchValue ast.Expr, store *packedStoreBinding, arms []ast.MatchArm) bool {
	needsPayloadDecode := packedMatchNeedsEagerDecode(arms)
	ident, ok := matchValue.(*ast.Ident)
	readsMatchedValueField := ok && matchArmsReadMatchedValueField(ident.Name, arms)
	if !needsPayloadDecode && !readsMatchedValueField {
		return false
	}
	if store != nil && store.typ != nil && semantic.IsFrozenPackedEnumStoreType(store.typ) && packedModeUsesDirectWordReads(abi) {
		return false
	}
	hasFrozenPackedStoreDeps := false
	if view.ExprHasOnlyFrozenPackedStoreDeps(matchValue) {
		hasFrozenPackedStoreDeps = true
	}
	if !hasFrozenPackedStoreDeps && store != nil && store.typ != nil && semantic.IsFrozenPackedEnumStoreType(store.typ) {
		hasFrozenPackedStoreDeps = true
	}
	if hasFrozenPackedStoreDeps {
		if packedModeUsesDenseIndexHandle(abi) {
			return false
		}
		return true
	}
	if !needsPayloadDecode {
		return false
	}
	if !ok {
		return false
	}
	return readsMatchedValueField
}
func (s *functionState) enumPayloadPtr(enumPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if enumIsTagOnly(enumType) {
		return nil, fmt.Errorf("enum %s has no lowered payload storage", enumType.Name)
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	payloadIndex := 1
	if enumType != nil && enumType.Packed {
		payloadIndex, err = s.g.packedEnumPayloadFieldIndex(enumType)
		if err != nil {
			return nil, err
		}
	}
	return C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, C.unsigned(payloadIndex), cStringFree("enum.payload.ptr")), nil
}
func (s *functionState) resolvePackedViewStoreBinding(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil || enumType == nil {
		return nil, false, nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedViewStoreBinding(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedViewStoreBinding(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedViewStoreBinding(n.Operand, enumType)
	case *ast.CanExpr:
		return s.resolvePackedViewStoreBinding(n.Expr, enumType)
	case *ast.Ident:
		if binding, ok := s.lookupPackedVariantView(n.Name); ok && binding.typ != nil && binding.typ.Enum == enumType && binding.store.typ != nil && binding.store.value != nil {
			storeCopy := binding.store
			return &storeCopy, true, nil
		}
		valueBinding, ok := s.lookupBinding(n.Name)
		if !ok {
			return nil, false, nil
		}
		viewType, ok := valueBinding.typ.(*semantic.PackedVariantViewType)
		if !ok || viewType == nil || viewType.Enum != enumType {
			return nil, false, nil
		}
		viewValue, err := s.loadValue(valueBinding.ptr, valueBinding.typ, n.Name)
		if err != nil {
			return nil, false, err
		}
		viewBinding, err := s.unpackPackedVariantViewValue(viewValue, viewType)
		if err != nil {
			return nil, false, err
		}
		if viewBinding.store.typ == nil || viewBinding.store.value == nil {
			return nil, false, nil
		}
		storeCopy := viewBinding.store
		return &storeCopy, true, nil
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedStoreBindingExpr(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil {
		return nil, false, nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedStoreBindingExpr(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedStoreBindingExpr(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedStoreBindingExpr(n.Operand, enumType)
	case *ast.FieldExpr:
		actualType := s.exprType(expr)
		storeType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok || storeType == nil || storeType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && storeType.Enum != enumType {
			return nil, false, nil
		}
		storeValue, actualType, err := s.emitExpr(expr, nil)
		if err != nil {
			return nil, false, err
		}
		resolvedType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok || resolvedType == nil || resolvedType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && resolvedType.Enum != enumType {
			return nil, false, nil
		}
		resolved := &packedStoreBinding{value: storeValue, typ: resolvedType}
		return resolved, true, nil
	case *ast.Ident:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return nil, false, nil
		}
		storeType, ok := binding.typ.(*semantic.PackedEnumStoreType)
		if !ok || storeType == nil || storeType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && storeType.Enum != enumType {
			return nil, false, nil
		}
		storeValue, err := s.loadValue(binding.ptr, binding.typ, n.Name)
		if err != nil {
			return nil, false, err
		}
		resolved := &packedStoreBinding{value: storeValue, typ: storeType}
		return resolved, true, nil
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedNodeStoreBinding(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil || enumType == nil || !enumType.Packed {
		return nil, false, nil
	}
	if path, ok := s.packedEnumStoragePath(expr); ok {
		if binding, ok := s.lookupPackedEnumStoreOrigin(path, enumType); ok {
			storeCopy := binding
			return &storeCopy, true, nil
		}
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedNodeStoreBinding(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedNodeStoreBinding(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedNodeStoreBinding(n.Operand, enumType)
	case *ast.CanExpr:
		return s.resolvePackedNodeStoreBinding(n.Expr, enumType)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, false, nil
		}
		return s.resolvePackedStoreBindingExpr(n.Object, enumType)
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedMatchStoreBinding(enumType *semantic.EnumType, valueExpr ast.Expr, storeExpr ast.Expr) (*packedStoreBinding, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil
	}
	if storeExpr != nil {
		storeValue, actualType, err := s.emitExpr(storeExpr, nil)
		if err != nil {
			return nil, err
		}
		storeType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok {
			return nil, fmt.Errorf("packed match over %s requires a packed store, got %s", enumType.Name, actualType.String())
		}
		binding := &packedStoreBinding{value: storeValue, typ: storeType}
		return binding, nil
	}
	if inferred, ok, err := s.resolvePackedViewStoreBinding(valueExpr, enumType); err != nil {
		return nil, err
	} else if ok {
		return inferred, nil
	}
	if inferred, ok, err := s.resolvePackedNodeStoreBinding(valueExpr, enumType); err != nil {
		return nil, err
	} else if ok {
		return inferred, nil
	}
	binding, ok := s.lookupPackedStore(enumType)
	if !ok {
		if synthetic, err := s.syntheticPtrHandleStoreBinding(enumType); synthetic != nil || err != nil {
			return synthetic, err
		}
		// If we are inside an active region arena (set by `in owner:` / `in alloc:`), lazily
		// create the packed store backed by that arena. This covers the common pattern of reading
		// (matching, expecting) packed-enum handles that were allocated in the same arena by a
		// callee, where the caller never explicitly allocated any nodes itself and therefore never
		// registered the store via getOrCreateRegionPackedStore.
		return nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
	}
	return &binding, nil
}

// syntheticPtrHandleStoreBinding returns a read-only placeholder store binding for a `handle: ptr`
// enum (docs/82). A ptr handle is the record address, so every read (tag, payload, range test)
// resolves through a bare inttoptr and never touches the store state — functions that only consume
// such handles carry no threaded store at all, and match codegen still needs a binding value only
// to satisfy the packedStoreOps plumbing. The value is undef and MUST never be read; any path that
// did would be a representation bug (it would trap loudly in -fbounds-check runs, not silently).
func (s *functionState) syntheticPtrHandleStoreBinding(enumType *semantic.EnumType) (*packedStoreBinding, error) {
	if enumType == nil || !enumType.HandleIsPointer() || enumType.StoreType == nil {
		return nil, nil
	}
	loweredStore, err := s.g.lowerType(enumType.StoreType)
	if err != nil {
		return nil, err
	}
	return &packedStoreBinding{value: C.LLVMGetUndef(loweredStore), typ: enumType.StoreType}, nil
}
