//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// packedStoreKey returns the cache key for an enum's region-backed store. A sealed hierarchy (docs/77)
// shares ONE store per root, so all members key by the root's name.
func packedStoreKey(enumType *semantic.EnumType) string {
	if enumType == nil {
		return ""
	}
	return enumType.Root().Name
}
func (s *functionState) lookupPackedStore(enumType *semantic.EnumType) (packedStoreBinding, bool) {
	if enumType == nil {
		return packedStoreBinding{}, false
	}
	if s.packedStores != nil {
		if binding, ok := s.packedStores[packedStoreKey(enumType)]; ok && binding.typ != nil {
			return binding, true
		}
	}
	// Implicit region-backed stores live in the function-wide cache (not scope-restored);
	// an entry is valid exactly while its owning region is the active tree-alloc owner.
	if s.regionPackedStores != nil {
		regionKey := s.treeAllocOwner.arenaRef
		if regionKey == nil {
			regionKey = s.treeAllocOwner.arenaRefPtr
		}
		if regionKey != nil {
			key := regionPackedStoreKey{region: regionKey, enum: packedStoreKey(enumType)}
			if binding, ok := s.regionPackedStores[key]; ok && binding.typ != nil {
				return binding, true
			}
		}
	}
	if binding, ok, err := s.programPackedStore(enumType); ok && err == nil {
		return binding, true
	}
	return packedStoreBinding{}, false
}
func (s *functionState) bindPackedStoreValue(t semantic.Type, value C.LLVMValueRef) {
	storeType, ok := t.(*semantic.PackedEnumStoreType)
	if !ok || storeType == nil || storeType.Enum == nil || value == nil {
		return
	}
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[packedStoreKey(storeType.Enum)] = packedStoreBinding{value: value, typ: storeType}
}
func (s *functionState) invalidatePackedReadCaches() {
	if s == nil {
		return
	}
	s.packedStoreValueKey1 = packedStoreExtractCacheKey{}
	s.packedStoreValue1 = nil
	s.packedStoreValueKey2 = packedStoreExtractCacheKey{}
	s.packedStoreValue2 = nil
	s.packedStoreValueKey3 = packedStoreExtractCacheKey{}
	s.packedStoreValue3 = nil
	clear(s.packedStoreValues)
	clear(s.packedVariantSparseTagReads)
	clear(s.packedVariantSparseWordReads)
	clear(s.packedDenseDArrayItemsReads)
	clear(s.packedDenseTagReads)
	clear(s.packedDenseWordReads)
	clear(s.packedDenseSideWordReads)
	clear(s.packedDirectFieldReads)
	clear(s.packedVariantPayloadReads)
}
func (s *functionState) lookupPackedStoreFieldValue(key packedStoreExtractCacheKey) (C.LLVMValueRef, bool) {
	if s == nil {
		return nil, false
	}
	if s.packedStoreValue1 != nil && s.packedStoreValueKey1 == key {
		return s.packedStoreValue1, true
	}
	if s.packedStoreValue2 != nil && s.packedStoreValueKey2 == key {
		return s.packedStoreValue2, true
	}
	if s.packedStoreValue3 != nil && s.packedStoreValueKey3 == key {
		return s.packedStoreValue3, true
	}
	value, ok := s.packedStoreValues[key]
	return value, ok && value != nil
}
func (s *functionState) cachePackedStoreFieldValue(key packedStoreExtractCacheKey, value C.LLVMValueRef) {
	if s == nil || value == nil {
		return
	}
	if s.packedStoreValue1 == nil || s.packedStoreValueKey1 == key {
		s.packedStoreValueKey1 = key
		s.packedStoreValue1 = value
		return
	}
	if s.packedStoreValue2 == nil || s.packedStoreValueKey2 == key {
		s.packedStoreValueKey2 = key
		s.packedStoreValue2 = value
		return
	}
	if s.packedStoreValue3 == nil || s.packedStoreValueKey3 == key {
		s.packedStoreValueKey3 = key
		s.packedStoreValue3 = value
		return
	}
	if s.packedStoreValues == nil {
		s.packedStoreValues = map[packedStoreExtractCacheKey]C.LLVMValueRef{}
	}
	s.packedStoreValues[key] = value
}
func (s *functionState) defineBinding(name string, binding valueBinding) {
	if s.scope == nil {
		s.scope = &codegenScope{}
	}
	s.invalidatePackedCommonFieldValues(name)
	s.invalidatePackedEnumStorage(name)
	s.invalidatePackedEnumStoreOrigin(name)
	s.invalidatePackedVariantView(name)
	if s.scope.bindingName == "" || s.scope.bindingName == name {
		s.scope.bindingName = name
		s.scope.binding = binding
		return
	}
	if s.scope.bindings == nil {
		s.scope.bindings = map[string]valueBinding{}
	}
	s.scope.bindings[name] = binding
}
func (s *functionState) lookupBinding(name string) (valueBinding, bool) {
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.bindingName == name {
			binding := scope.binding
			if binding.ptr != nil && binding.typ != nil {
				return binding, true
			}
		}
		if binding, ok := scope.bindings[name]; ok {
			return binding, true
		}
	}
	// docs/128: a program-lived region resolves to its program arena in every function.
	if binding, ok, err := s.programRegionArena(name); ok && err == nil {
		return binding, true
	}
	return valueBinding{}, false
}
func (s *functionState) specializeFunctionType(base *semantic.FuncType) *semantic.FuncType {
	if s == nil || base == nil {
		return nil
	}
	if len(s.typeMap) == 0 {
		return base
	}
	if s.specializedFuncTypes != nil {
		if specialized, ok := s.specializedFuncTypes[base]; ok && specialized != nil {
			return specialized
		}
	} else {
		s.specializedFuncTypes = make(map[*semantic.FuncType]*semantic.FuncType)
	}
	specialized := specializeFuncType(base, s.typeMap, s.g.result.StaticImpls)
	if specialized != nil {
		specialized.ExplicitParamCount = base.ExplicitParamCount
		specialized.ExplicitParamNames = append([]string(nil), base.ExplicitParamNames...)
		specialized.ExplicitParamDefaultExprs = append([]ast.Expr(nil), base.ExplicitParamDefaultExprs...)
		specialized.ExplicitParamHasDefault = append([]bool(nil), base.ExplicitParamHasDefault...)
		specialized.ImplicitParamNames = append([]string(nil), base.ImplicitParamNames...)
	}
	s.specializedFuncTypes[base] = specialized
	return specialized
}
func (s *functionState) packedEnumStoragePath(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := s.packedEnumStoragePath(n.Object)
		if !ok || base == "" {
			return "", false
		}
		return base + "." + n.Field, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return "", false
		}
		base, ok := s.packedEnumStoragePath(n.Object)
		if !ok || base == "" {
			return "", false
		}
		indexKey, ok := packedEnumStorageIndexKey(n.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *ast.ParenExpr:
		return s.packedEnumStoragePath(n.Inner)
	default:
		return "", false
	}
}
func packedEnumStorageIndexKey(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n == nil || n.Value == "" {
			return "", false
		}
		return n.Value, true
	case *ast.ParenExpr:
		return packedEnumStorageIndexKey(n.Inner)
	default:
		return "", false
	}
}
func (s *functionState) packedReadOriginKey(expr ast.Expr) (packedReadOriginKey, bool, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok && binding.ptr != nil {
			return packedReadOriginKey{root: binding.ptr}, true, nil
		}
		if s.g == nil || s.g.result == nil {
			return packedReadOriginKey{}, false, nil
		}
		if sym, ok := s.g.lookupGlobal(n.Name); ok {
			if sym.Kind == semantic.SymbolGlobal || sym.Kind == semantic.SymbolExternVar {
				global, err := s.g.ensureGlobalDeclared(n.Name, sym.Type, sym.Kind == semantic.SymbolExternVar)
				if err != nil {
					return packedReadOriginKey{}, false, err
				}
				return packedReadOriginKey{root: global}, true, nil
			}
		}
		return packedReadOriginKey{}, false, nil
	case *ast.FieldExpr:
		origin, ok, err := s.packedReadOriginKey(n.Object)
		if err != nil || !ok {
			return packedReadOriginKey{}, ok, err
		}
		return packedReadOriginKey{root: origin.root, path: origin.path + "." + n.Field}, true, nil
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return packedReadOriginKey{}, false, nil
		}
		origin, ok, err := s.packedReadOriginKey(n.Object)
		if err != nil || !ok {
			return packedReadOriginKey{}, ok, err
		}
		indexKey, ok := packedEnumStorageIndexKey(n.Index)
		if !ok {
			return packedReadOriginKey{}, false, nil
		}
		return packedReadOriginKey{root: origin.root, path: origin.path + "[" + indexKey + "]"}, true, nil
	case *ast.CastExpr:
		return s.packedReadOriginKey(n.Operand)
	case *ast.CanExpr:
		return s.packedReadOriginKey(n.Expr)
	case *ast.MoveExpr:
		return s.packedReadOriginKey(n.Operand)
	case *ast.ParenExpr:
		return s.packedReadOriginKey(n.Inner)
	default:
		return packedReadOriginKey{}, false, nil
	}
}
func (s *functionState) bindPackedEnumStorage(name string, enumType *semantic.EnumType, ptr C.LLVMValueRef) {
	if name == "" || enumType == nil || !enumType.Packed || ptr == nil {
		return
	}
	if s.scope == nil {
		s.scope = &codegenScope{}
	}
	if s.scope.packedEnumPtrs == nil {
		s.scope.packedEnumPtrs = map[string]packedEnumStorageBinding{}
	}
	s.scope.packedEnumPtrs[name] = packedEnumStorageBinding{ptr: ptr, block: C.LLVMGetInsertBlock(s.builder), typ: enumType}
}
func (s *functionState) bindPackedCommonFieldValues(name string, enumType *semantic.EnumType, values packedPayloadValueCache) {
	if name == "" || enumType == nil || !enumType.Packed || values.empty() {
		return
	}
	if s.scope == nil {
		s.scope = &codegenScope{}
	}
	binding := packedCommonFieldValueBinding{typ: enumType, values: clonePackedPayloadValues(values)}
	if s.scope.packedCommonValueName == "" || s.scope.packedCommonValueName == name {
		s.scope.packedCommonValueName = name
		s.scope.packedCommonValueBinding = binding
		return
	}
	if s.scope.packedCommonValues == nil {
		s.scope.packedCommonValues = map[string]packedCommonFieldValueBinding{}
	}
	s.scope.packedCommonValues[name] = binding
}
func (s *functionState) lookupPackedCommonFieldValue(name string, enumType *semantic.EnumType, fieldName string) (C.LLVMValueRef, bool) {
	if name == "" || enumType == nil || fieldName == "" {
		return nil, false
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedCommonValueName == name {
			binding := scope.packedCommonValueBinding
			if binding.typ == enumType {
				if value, ok := binding.values.lookup(fieldName); ok && value != nil {
					return value, true
				}
			}
		}
		binding, ok := scope.packedCommonValues[name]
		if !ok || binding.typ != enumType {
			continue
		}
		if value, ok := binding.values.lookup(fieldName); ok && value != nil {
			return value, true
		}
	}
	return nil, false
}
func clonePackedPayloadValues(values packedPayloadValueCache) packedPayloadValueCache {
	cloned := packedPayloadValueCache{
		name1:  values.name1,
		value1: values.value1,
		name2:  values.name2,
		value2: values.value2,
	}
	if len(values.values) == 0 {
		return cloned
	}
	cloned.values = make(map[string]C.LLVMValueRef, len(values.values))
	for key, value := range values.values {
		if value != nil {
			cloned.values[key] = value
		}
	}
	if len(cloned.values) == 0 {
		cloned.values = nil
	}
	return cloned
}
func (c packedPayloadValueCache) empty() bool {
	return c.name1 == "" && c.name2 == "" && len(c.values) == 0
}
func (c *packedPayloadValueCache) add(name string, value C.LLVMValueRef) {
	if c == nil || name == "" || value == nil {
		return
	}
	if c.name1 == "" || c.name1 == name {
		c.name1 = name
		c.value1 = value
		return
	}
	if c.name2 == "" || c.name2 == name {
		c.name2 = name
		c.value2 = value
		return
	}
	if c.values == nil {
		c.values = map[string]C.LLVMValueRef{
			c.name1: c.value1,
			c.name2: c.value2,
		}
	}
	c.values[name] = value
}
func (c packedPayloadValueCache) lookup(name string) (C.LLVMValueRef, bool) {
	if name == "" {
		return nil, false
	}
	if c.name1 == name && c.value1 != nil {
		return c.value1, true
	}
	if c.name2 == name && c.value2 != nil {
		return c.value2, true
	}
	value, ok := c.values[name]
	return value, ok && value != nil
}
func (s *functionState) bindPackedVariantView(name string, viewType *semantic.PackedVariantViewType, ptr C.LLVMValueRef, handle C.LLVMValueRef, store packedStoreBinding, payloadValues packedPayloadValueCache) {
	s.bindPackedVariantViewInternal(name, viewType, ptr, handle, store, payloadValues, true)
}
func (s *functionState) bindPackedVariantViewOwned(name string, viewType *semantic.PackedVariantViewType, ptr C.LLVMValueRef, handle C.LLVMValueRef, store packedStoreBinding, payloadValues packedPayloadValueCache) {
	s.bindPackedVariantViewInternal(name, viewType, ptr, handle, store, payloadValues, false)
}
func (s *functionState) bindPackedVariantViewInternal(name string, viewType *semantic.PackedVariantViewType, ptr C.LLVMValueRef, handle C.LLVMValueRef, store packedStoreBinding, payloadValues packedPayloadValueCache, clonePayloads bool) {
	if name == "" || viewType == nil || (ptr == nil && handle == nil) {
		return
	}
	if s.scope == nil {
		s.scope = &codegenScope{}
	}
	binding := packedVariantViewBinding{ptr: ptr, handle: handle, typ: viewType}
	if store.typ != nil && store.value != nil {
		binding.store = store
	}
	switch {
	case payloadValues.name1 == "" && payloadValues.name2 == "" && len(payloadValues.values) == 0:
		binding.payloadValues = packedPayloadValueCache{}
	case clonePayloads:
		binding.payloadValues = clonePackedPayloadValues(payloadValues)
	default:
		binding.payloadValues = payloadValues
	}
	if s.scope.packedViewName == "" || s.scope.packedViewName == name {
		s.scope.packedViewName = name
		s.scope.packedViewBinding = binding
		return
	}
	if s.scope.packedViewPtrs == nil {
		s.scope.packedViewPtrs = map[string]packedVariantViewBinding{}
	}
	s.scope.packedViewPtrs[name] = binding
}

// markPackedVariantViewHandleBlock stamps the just-bound view's handle with the current basic
// block, marking it as a MEMOIZED load that is only safe to reuse from this block (see
// packedVariantViewBinding.handleBlock).
func (s *functionState) markPackedVariantViewHandleBlock(name string) {
	if name == "" || s.scope == nil {
		return
	}
	block := C.LLVMGetInsertBlock(s.builder)
	if s.scope.packedViewName == name {
		s.scope.packedViewBinding.handleBlock = block
		return
	}
	if binding, ok := s.scope.packedViewPtrs[name]; ok {
		binding.handleBlock = block
		s.scope.packedViewPtrs[name] = binding
	}
}
func (s *functionState) lookupPackedVariantView(name string) (packedVariantViewBinding, bool) {
	if name == "" {
		return packedVariantViewBinding{}, false
	}
	currentBlock := C.LLVMGetInsertBlock(s.builder)
	// A memoized handle load from another block does not dominate this one (see handleBlock);
	// drop it from the returned copy. The binding stays usable only if a decoded ptr remains.
	usable := func(binding packedVariantViewBinding) (packedVariantViewBinding, bool) {
		if binding.typ == nil {
			return binding, false
		}
		if binding.handle != nil && binding.handleBlock != nil && binding.handleBlock != currentBlock {
			binding.handle = nil
			binding.handleBlock = nil
		}
		return binding, binding.ptr != nil || binding.handle != nil
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedViewName == name {
			if binding, ok := usable(scope.packedViewBinding); ok {
				return binding, true
			}
		}
		if binding, ok := scope.packedViewPtrs[name]; ok {
			if binding, ok := usable(binding); ok {
				return binding, true
			}
		}
	}
	return packedVariantViewBinding{}, false
}
func (s *functionState) invalidatePackedVariantView(name string) {
	if name == "" {
		return
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedViewName == name {
			scope.packedViewName = ""
			scope.packedViewBinding = packedVariantViewBinding{}
		}
		delete(scope.packedViewPtrs, name)
	}
}
