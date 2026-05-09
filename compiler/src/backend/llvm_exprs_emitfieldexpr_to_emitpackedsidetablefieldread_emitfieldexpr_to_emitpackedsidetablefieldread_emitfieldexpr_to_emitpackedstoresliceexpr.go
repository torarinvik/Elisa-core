//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, fieldType, handled, err := s.emitStaticInterfaceMethodExpr(expr); handled {
		return value, fieldType, err
	}
	if treeType, variant, ok := s.treeConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor %s.%s", treeType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
				return nil, nil, fmt.Errorf("tree constructor %s.%s requires explicit common fields; use call syntax with named arguments", treeType.Name, variant.Name)
			}
			return s.emitTreeConstructorValue(nil, treeType, variant, nil, nil, nil)
		}
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
	if value, fieldType, handled, err := s.emitTreeAttributeFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitTreeFieldExpr(expr); handled {
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
	value := C.LLVMBuildExtractValue(s.builder, objValue, C.unsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}
func (s *functionState) emitPackedStoreCountExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	return value, s.g.result.NamedTypes["usize"], true, nil
}
func (s *functionState) emitPackedStoreCountValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).storeCount(name)
}
func (s *functionState) emitPackedStoreTagsExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	resultType, ok := s.exprType(expr).(*semantic.DArrayViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("packed store tags requires dview result type")
	}
	totalValue, err := ops.storeCount("packed.store.tags.count")
	if err != nil {
		return nil, nil, true, err
	}
	zeroType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	zeroValue := C.LLVMConstInt(zeroType, 0, 0)
	value, actualType, err := ops.storeTagsView(zeroValue, totalValue, resultType, "packed.store.tags")
	return value, actualType, true, err
}
func (s *functionState) emitPackedVariantViewFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
		if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
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
			if hasName {
				s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
			}
		}
		containerType, err := s.loweredEnumStorageType(binding.typ.Enum)
		if err != nil {
			return nil, nil, true, err
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, binding.ptr, C.unsigned(layout.RowFieldIndex), cStringFree("view.common.field"))
		value, err := s.loadValue(fieldPtr, fieldType, expr.Field)
		return value, fieldType, true, err
	}
	if cachedValue, ok := binding.payloadValues.lookup(expr.Field); ok && cachedValue != nil {
		return cachedValue, field.Type, true, nil
	}
	if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
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
		if hasName {
			s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
		}
	}
	payloadValues, err := s.loadEnumVariantPayload(binding.ptr, binding.handle, binding.typ.Enum, binding.typ.Variant, nil, origin)
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
func (s *functionState) emitPackedCommonFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	ops, ok := s.packedStoreOpsFromBinding(&store)
	if !ok || !ops.canDirectWordRead() {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil && s.g.result.ExprHasOnlyFrozenPackedStoreDeps(expr.Object) {
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
func (s *functionState) emitPackedStoreValueAtDenseKey(ops *packedStoreOps, keyIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if ops == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil, nil, fmt.Errorf("dense-key packed store read requires store metadata")
	}
	switch s.g.packedModeForEnum(ops.storeType.Enum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		coerced, err := s.coerceValue(keyIndex, s.g.result.NamedTypes["u32"], ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(ops.storeType.Enum))
	}
}
func (s *functionState) emitPackedStoreIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	indexValue, _, err := s.emitExpr(expr.Index, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	value, actualType, err := ops.storeValueAt(indexValue, "packed.store.index")
	return value, actualType, true, err
}
func (s *functionState) emitPackedStoreSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	startValue, _, err := s.emitExpr(expr.Start, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	resultType := s.exprType(expr)
	value, actualType, err := ops.storeSlice(startValue, endValue, resultType, "packed.store.view")
	return value, actualType, true, err
}
