//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// emitConstStringLenExpr lowers `.len` on a compile-time string — the only `.len` a `u8&` receiver
// has (the analyzer's constStringLenField rejects every receiver that does not const-evaluate to a
// string) — to its byte length. A `u8&` carries no length, so nothing is computed at runtime.
func (s *functionState) emitConstStringLenExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "len" {
		return nil, nil, false, nil
	}
	ref, ok := s.exprType(expr.Object).(*semantic.RefType)
	if !ok || ref == nil {
		return nil, nil, false, nil
	}
	if elem, ok := ref.Elem.(*semantic.BuiltinType); !ok || elem == nil || elem.Name != "u8" {
		return nil, nil, false, nil
	}
	value, ok := s.evalConstExpr(expr)
	if !ok || value.Kind != semantic.ConstInt {
		return nil, nil, false, nil
	}
	lenType := &semantic.BuiltinType{Name: "i64"}
	llvmValue, err := s.g.constValueAsLLVM(value, lenType)
	return llvmValue, lenType, true, err
}

// nameIsRuntimeBinding reports whether a bare name resolves to a binding in the function's scope
// chain rather than to a compile-time value; a const generic argument in typeMap is compile-time.
// The constant evaluator must not read a const through such a binding (semantic.nameIsRuntimeBinding).
func (s *functionState) nameIsRuntimeBinding(name string) bool {
	if bound, ok := s.typeMap[name]; ok {
		if _, isConst := bound.(*semantic.ConstValueType); isConst {
			return false
		}
	}
	_, ok := s.lookupBinding(name)
	return ok
}

func (s *functionState) emitFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, error) {
	if value, fieldType, handled, err := s.emitStaticInterfaceMethodExpr(expr); handled {
		return value, fieldType, err
	}
	if enumType, variant, ok := s.enumConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor %s.%s", enumType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			return s.emitEnumConstructorValue(nil, enumType, variant, nil, nil)
		}
	}
	if fieldType, ok := cstrSyntheticFieldType(s.exprType(expr.Object), expr.Field); ok {
		return s.emitRuntimeStringLenExpr(expr.Object, fieldType)
	}
	if value, fieldType, handled, err := s.emitConstStringLenExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitBitGroupMemberExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedStoreCountExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedStoreTagsExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedVariantViewFieldExpr(expr); handled {
		return value, fieldType, err
	}

	if value, fieldType, handled, err := s.emitPackedCommonFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitBuiltinDictEntryFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitBuiltinStoreRowsFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitBuiltinSOACountFieldExpr(expr); handled {
		return value, fieldType, err
	}
	ptr, fieldType, addressErr := s.emitReadableFieldAddress(expr)
	if addressErr == nil {
		value, loadErr := s.loadValue(ptr, fieldType, expr.Field)
		return value, fieldType, loadErr
	}
	objValue, objType, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, err
	}
	fieldType, index, _, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	if pointerLike {
		return nil, nil, fmt.Errorf("field %s requires an addressable object (base %T: %v)", expr.Field, expr.Object, addressErr)
	}
	value := llvmBuildExtractValue(s.builder, objValue, llvmIRUnsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}
func (s *functionState) emitPackedStoreCountExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "count" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	value, err := ops.storeCount("packed.store.count")
	if err != nil {
		return nil, nil, true, err
	}
	return value, s.g.namedType("usize"), true, nil
}
func (s *functionState) emitPackedStoreCountValue(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType, name string) (llvmIRValueRef, error) {
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).storeCount(name)
}
func (s *functionState) emitPackedStoreTagsExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "tags" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok || ops.storeType == nil || ops.storeType.Enum == nil || !semantic.IsFrozenPackedEnumStoreType(ops.storeType) {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.ViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("packed store tags requires view result type")
	}
	totalValue, err := ops.storeCount("packed.store.tags.count")
	if err != nil {
		return nil, nil, true, err
	}
	zeroType, err := s.g.lowerType(s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	zeroValue := llvmConstInt(zeroType, 0, 0)
	value, actualType, err := ops.storeTagsView(zeroValue, totalValue, resultType, "packed.store.tags")
	return value, actualType, true, err
}
func (s *functionState) emitPackedVariantViewFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	name, hasName := packedVariantViewName(expr.Object)
	binding, ok := s.lookupPackedVariantView(name)
	if !ok {
		viewType, ok := s.exprType(expr.Object).(*semantic.PackedVariantViewType)
		if !ok || viewType == nil {
			return nil, nil, false, nil
		}
		objectValue, _, err := s.emitExpr(expr.Object, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		binding, err = s.unpackPackedVariantViewValue(objectValue, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		if hasName {
			s.bindPackedVariantView(name, viewType, binding.ptr, binding.handle, binding.store, binding.payloadValues)
		}
	}
	if binding.typ == nil || (binding.ptr == nil && binding.handle == nil) {
		return nil, nil, false, nil
	}
	origin := packedReadOriginKey{}
	if resolvedOrigin, ok, err := s.packedReadOriginKey(expr.Object); err != nil {
		return nil, nil, true, err
	} else if ok {
		origin = resolvedOrigin
	}
	field, ok := binding.typ.Field(expr.Field)
	if !ok {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	if _, isCommonField := binding.typ.Enum.Common[expr.Field]; isCommonField {
		if hasName {
			if cachedValue, ok := s.lookupPackedCommonFieldValue(name, binding.typ.Enum, expr.Field); ok {
				return cachedValue, field.Type, true, nil
			}
		}
		layout, err := s.g.packedEnumCommonFieldLayout(binding.typ.Enum, expr.Field)
		if err != nil {
			return nil, nil, true, err
		}
		fieldType := layout.Field.Type
		if !layout.StoredInline {
			if binding.handle == nil || binding.store.typ == nil {
				return nil, nil, true, fmt.Errorf("packed enum common field %s.%s is stored in a side table and requires store context", binding.typ.Enum.Name, expr.Field)
			}
			value, err := s.emitPackedSideTableFieldRead(binding.handle, binding.typ.Enum, &binding.store, fieldType, layout.SideWordOffset, layout.WordCount, origin, "packed.view.common.side")
			return value, fieldType, true, err
		}
		if (binding.ptr == nil || (binding.ptrBlock != nil && binding.ptrBlock != llvmGetInsertBlock(s.builder))) && binding.handle != nil && binding.store.typ != nil {
			binding.ptr = nil // stale cross-block decode: recompute in the current block
			ops, ok := s.packedStoreOpsFromBinding(&binding.store)
			if ok && ops.canDirectWordRead() {
				fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(binding.typ.Enum, layout.RowFieldIndex)
				if err != nil {
					return nil, nil, true, err
				}
				if ok {
					coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, binding.handle, binding.typ.Enum, fieldType, fieldOffsetBytes, origin, "packed.view.common")
					if err != nil {
						return nil, nil, true, err
					}
					return coerced, fieldType, true, nil
				}
			}
			decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
			if err != nil {
				return nil, nil, true, err
			}
			binding.ptr = decodedPtr
			binding.ptrBlock = llvmGetInsertBlock(s.builder)
			if hasName {
				s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
			}
		}
		containerType, err := s.loweredEnumStorageType(binding.typ.Enum)
		if err != nil {
			return nil, nil, true, err
		}
		fieldPtr := llvmBuildStructGEP2(s.builder, containerType, binding.ptr, llvmIRUnsigned(layout.RowFieldIndex), cStringFree("view.common.field"))
		value, err := s.loadValue(fieldPtr, fieldType, expr.Field)
		return value, fieldType, true, err
	}
	if cachedValue, ok := binding.payloadValues.lookup(expr.Field); ok && cachedValue != nil {
		return cachedValue, field.Type, true, nil
	}
	if (binding.ptr == nil || (binding.ptrBlock != nil && binding.ptrBlock != llvmGetInsertBlock(s.builder))) && binding.handle != nil && binding.store.typ != nil {
		binding.ptr = nil // stale cross-block decode: recompute in the current block
		ops, ok := s.packedStoreOpsFromBinding(&binding.store)
		if ok && ops.canDirectWordRead() {
			payloadValues, ok, err := s.readPackedEnumVariantPayloadWithStore(binding.handle, binding.typ.Enum, binding.typ.Variant, &binding.store, origin)
			if err != nil {
				return nil, nil, true, err
			}
			if ok {
				index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
				if !ok || index < 0 || index >= len(payloadValues) {
					return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
				}
				return payloadValues[index], field.Type, true, nil
			}
		}
		decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
		if err != nil {
			return nil, nil, true, err
		}
		binding.ptr = decodedPtr
		binding.ptrBlock = llvmGetInsertBlock(s.builder)
		if hasName {
			s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
		}
	}
	payloadSourcePtr := binding.handle
	decodedPayloadPtr := binding.ptr
	if !binding.typ.Enum.Packed {
		payloadSourcePtr = binding.ptr
		decodedPayloadPtr = nil
	}
	payloadValues, err := s.loadEnumVariantPayload(decodedPayloadPtr, payloadSourcePtr, binding.typ.Enum, binding.typ.Variant, nil, origin)
	if err != nil {
		return nil, nil, true, err
	}
	index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
	if !ok || index < 0 || index >= len(payloadValues) {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	return payloadValues[index], field.Type, true, nil
}
func packedVariantViewName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		return base + "." + n.Field, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return "", false
		}
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		indexKey, ok := packedEnumStorageIndexKey(n.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *ast.CastExpr:
		return packedVariantViewName(n.Operand)
	case *ast.CanExpr:
		return packedVariantViewName(n.Expr)
	case *ast.ParenExpr:
		return packedVariantViewName(n.Inner)
	default:
		return "", false
	}
}
func (s *functionState) emitPackedCommonFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, false, nil
	}
	containerType := objectType
	if refType, ok := objectType.(*semantic.RefType); ok {
		containerType = refType.Elem
	}
	containerType = semantic.StripAggregateStateType(containerType)
	enumType, ok := containerType.(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, false, nil
	}
	if _, ok := enumType.Common[expr.Field]; !ok {
		return nil, nil, false, nil
	}
	layout, err := s.g.packedEnumCommonFieldLayout(enumType, expr.Field)
	if err != nil {
		return nil, nil, true, err
	}
	fieldType := layout.Field.Type
	if key, ok := s.packedEnumStoragePath(expr.Object); ok {
		if cachedValue, ok := s.lookupPackedCommonFieldValue(key, enumType, expr.Field); ok {
			return cachedValue, fieldType, true, nil
		}
	}
	if layout.StoredInline {
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			if _, ok := s.lookupPackedEnumStorage(key, enumType); ok {
				return nil, nil, false, nil
			}
		}
	}
	store, ok := s.lookupPackedStore(enumType)
	if !ok {
		if layout.StoredInline {
			return nil, nil, false, nil
		}
		return nil, nil, true, fmt.Errorf("packed enum common field %s.%s is stored in a side table and requires store context", enumType.Name, expr.Field)
	}
	if !layout.StoredInline {
		handleValue, err := s.packedEnumFieldHandleValue(expr.Object, objectType, enumType)
		if err != nil {
			return nil, nil, true, err
		}
		origin, ok, err := s.packedReadOriginKey(expr.Object)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			origin = packedReadOriginKey{}
		}
		value, err := s.emitPackedSideTableFieldRead(handleValue, enumType, &store, fieldType, layout.SideWordOffset, layout.WordCount, origin, "packed.common.side")
		if err != nil {
			return nil, nil, true, err
		}
		return value, fieldType, true, nil
	}
	// A MUTABLE inline common must be read from the ROW, which is the only place a
	// write goes. The word reader serves any word below the store's prefix_words out of
	// the parallel COLUMN arrays that ctx_packed_store_record_prefix_words filled at
	// construction, and nothing refreshes those on `w.field <- v` -- so a post-construction
	// write was invisible whenever the read happened to take this path (a read in the same
	// block reuses the decoded row pointer and answered correctly, which is what made this
	// look intermittent). Falling through here decodes the handle and GEPs the row.
	//
	// Immutable commons keep the fast path: with no writes, row and column cannot disagree.
	if layout.Field.Mutable {
		return nil, nil, false, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(&store)
	if !ok || !ops.canDirectWordRead() {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().ExprHasOnlyFrozenPackedStoreDeps(expr.Object) {
		if !packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) {
			return nil, nil, false, nil
		}
	}
	fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(enumType, layout.RowFieldIndex)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	handleValue, err := s.packedEnumFieldHandleValue(expr.Object, objectType, enumType)
	if err != nil {
		return nil, nil, true, err
	}
	origin, ok, err := s.packedReadOriginKey(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		origin = packedReadOriginKey{}
	}
	coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, fieldType, fieldOffsetBytes, origin, "packed.common.store")
	if err != nil {
		return nil, nil, true, err
	}
	return coerced, fieldType, true, nil
}
func (s *functionState) emitPackedStoreValueAtDenseKey(ops *packedStoreOps, keyIndex llvmIRValueRef, name string) (llvmIRValueRef, semantic.Type, error) {
	if ops == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil, nil, fmt.Errorf("dense-key packed store read requires store metadata")
	}
	switch s.g.packedModeForEnum(ops.storeType.Enum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		coerced, err := s.coerceValue(keyIndex, s.g.namedType("u32"), ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(ops.storeType.Enum))
	}
}
func (s *functionState) emitPackedStoreIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	if keyIndex, _, handled, err := s.emitNodeKeyIndexValue(expr.Index); handled {
		if err != nil {
			return nil, nil, true, err
		}
		value, actualType, err := s.emitPackedStoreValueAtDenseKey(ops, keyIndex, "packed.store.key")
		return value, actualType, true, err
	}
	indexValue, _, err := s.emitExpr(expr.Index, s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	value, actualType, err := ops.storeValueAt(indexValue, "packed.store.index")
	return value, actualType, true, err
}
func (s *functionState) emitPackedStoreSliceExpr(expr *ast.SliceExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	startValue, _, err := s.emitExpr(expr.Start, s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	resultType := s.exprType(expr)
	value, actualType, err := ops.storeSlice(startValue, endValue, resultType, "packed.store.view")
	return value, actualType, true, err
}
