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
	"fmt"
	"unsafe"
)

func (s *functionState) createEntryAlloca(name string, t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMGetEntryBasicBlock(s.fnValue)
	first := C.LLVMGetFirstInstruction(entry)
	if first != nil {
		C.LLVMPositionBuilderBefore(builder, first)
	} else {
		C.LLVMPositionBuilderAtEnd(builder, entry)
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	alloca := C.LLVMBuildAlloca(builder, llvmType, nameC)
	s.g.applyTypeAlignment(alloca, t)
	return alloca, nil
}

// createEntryAllocaZeroed allocas in the entry block AND stores a zero value there, so the slot is
// initialized exactly ONCE per function call. Used for loop-reset regions: the arena is zeroed
// before the loop, then reset (not re-created) each iteration so its blocks are reused.
func (s *functionState) createEntryAllocaZeroed(name string, t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	zero, err := s.zeroValue(t)
	if err != nil {
		return nil, err
	}
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMGetEntryBasicBlock(s.fnValue)
	first := C.LLVMGetFirstInstruction(entry)
	if first != nil {
		C.LLVMPositionBuilderBefore(builder, first)
	} else {
		C.LLVMPositionBuilderAtEnd(builder, entry)
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	alloca := C.LLVMBuildAlloca(builder, llvmType, nameC)
	s.g.applyTypeAlignment(alloca, t)
	C.LLVMBuildStore(builder, zero, alloca)
	return alloca, nil
}
func (s *functionState) currentBlockTerminated() bool {
	block := C.LLVMGetInsertBlock(s.builder)
	if block == nil {
		return true
	}
	return C.LLVMGetBasicBlockTerminator(block) != nil
}
func (s *functionState) pushScope() {
	var scope *codegenScope
	if n := len(s.scopePool); n != 0 {
		scope = s.scopePool[n-1]
		s.scopePool = s.scopePool[:n-1]
	} else {
		scope = &codegenScope{}
	}
	scope.parent = s.scope
	s.scope = scope
}
func (s *functionState) popScope() {
	if s.scope != nil {
		scope := s.scope
		s.scope = scope.parent
		scope.parent = nil
		scope.bindingName = ""
		scope.binding = valueBinding{}
		clear(scope.bindings)
		scope.packedCommonValueName = ""
		scope.packedCommonValueBinding = packedCommonFieldValueBinding{}
		clear(scope.packedCommonValues)
		clear(scope.packedEnumPtrs)
		scope.packedEnumStoreName = ""
		scope.packedEnumStoreBinding = packedStoreBinding{}
		clear(scope.packedEnumStores)
		scope.packedViewName = ""
		scope.packedViewBinding = packedVariantViewBinding{}
		clear(scope.packedViewPtrs)
		s.scopePool = append(s.scopePool, scope)
	}
}
func (s *functionState) currentActivePool() (activePoolBinding, bool) {
	if len(s.poolScopes) == 0 {
		return activePoolBinding{}, false
	}
	return s.poolScopes[len(s.poolScopes)-1], true
}
func (s *functionState) clonePackedStores() map[string]packedStoreBinding {
	if s.packedStores == nil {
		return nil
	}
	cloned := make(map[string]packedStoreBinding, len(s.packedStores))
	for name, binding := range s.packedStores {
		cloned[name] = binding
	}
	return cloned
}
func stripTreeAllocOwnerExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.Inner
			continue
		}
		return expr
	}
	return nil
}
func isTreeAllocPermExpr(expr ast.Expr) bool {
	ident, ok := stripTreeAllocOwnerExpr(expr).(*ast.Ident)
	return ok && ident != nil && ident.Name == "perm"
}
func (s *functionState) classifyTreeAllocOwnerExpr(expr ast.Expr) (treeAllocOwnerBinding, bool, error) {
	if expr == nil {
		return treeAllocOwnerBinding{}, false, nil
	}
	if isTreeAllocPermExpr(expr) {
		return treeAllocOwnerBinding{isPerm: true}, true, nil
	}
	if storeType, ok := s.exprType(expr).(*semantic.TreeStoreType); ok && storeType != nil {
		storeValue, _, err := s.emitExpr(expr, storeType)
		if err != nil {
			return treeAllocOwnerBinding{}, false, err
		}
		return treeAllocOwnerBinding{storeValue: storeValue, storeType: storeType}, true, nil
	}
	if refType, ok := s.exprType(expr).(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		if storeType, ok := refType.Elem.(*semantic.TreeStoreType); ok && storeType != nil {
			storePtr, _, err := s.emitExpr(expr, refType)
			if err != nil {
				return treeAllocOwnerBinding{}, false, err
			}
			storeValue, err := s.loadValue(storePtr, storeType, "tree.store.load")
			if err != nil {
				return treeAllocOwnerBinding{}, false, err
			}
			return treeAllocOwnerBinding{storeValue: storeValue, storeType: storeType}, true, nil
		}
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return treeAllocOwnerBinding{}, false, nil
	}
	ownerType := s.exprType(expr)
	stripped := stripTreeAllocOwnerExpr(expr)
	if ident, ok := stripped.(*ast.Ident); ok && semantic.SameType(ownerType, arenaType) {
		binding, ok := s.lookupBinding(ident.Name)
		if !ok {
			return treeAllocOwnerBinding{}, false, fmt.Errorf("unknown Arena owner %q during LLVM lowering", ident.Name)
		}
		return treeAllocOwnerBinding{arenaRef: binding.ptr}, true, nil
	}
	if refType, ok := ownerType.(*semantic.RefType); ok && refType != nil && semantic.SameType(refType.Elem, arenaType) {
		if ident, ok := stripped.(*ast.Ident); ok {
			if binding, ok := s.lookupBinding(ident.Name); ok && binding.ptr != nil {
				return treeAllocOwnerBinding{arenaRefPtr: binding.ptr}, true, nil
			}
		}
		ownerValue, _, err := s.emitExpr(expr, ownerType)
		if err != nil {
			return treeAllocOwnerBinding{}, false, err
		}
		return treeAllocOwnerBinding{arenaRef: ownerValue}, true, nil
	}
	return treeAllocOwnerBinding{}, false, nil
}
func (s *functionState) lookupTreeAllocOwner() (treeAllocOwnerBinding, bool) {
	if s.treeAllocOwner.isPerm || s.treeAllocOwner.arenaRef != nil || s.treeAllocOwner.arenaRefPtr != nil || s.treeAllocOwner.storePtr != nil || s.treeAllocOwner.storeValue != nil {
		return s.treeAllocOwner, true
	}
	return treeAllocOwnerBinding{}, false
}

// dictMutationHelperName picks the runtime helper the dict-entry codegen calls
// for an insert/get_or_insert. The real std `arena_dict_put` /
// `arena_dict_get_or_insert` return `error[RuntimeError]` (an sret/error-tag
// ABI the hand-rolled call site cannot consume), so the std also ships thin
// non-error `_checked` wrappers (try->panic-via-else). Prefer the wrapper when
// present; fall back to the base name for minimal runtimes/test stubs that
// already define a non-error helper of that name.
func (s *functionState) dictMutationHelperName(base string) string {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil {
		return base
	}
	if _, ok := s.g.result.GlobalScope.Lookup(base + "_checked"); ok {
		return base + "_checked"
	}
	return base
}

// regionArenaOwner resolves the arena for a named region from the live region
// environment (s.regions), for region-parameterized container ops. Region-
// containers Phase 2 codegen: a container grows in its OWN region's arena
// rather than the ambient scope. Today the two coincide (a container's region
// == its ambient scope), so this is behavior-preserving; it becomes load-
// bearing once pushes happen through a borrow whose region differs.
func (s *functionState) regionArenaOwner(region string) (treeAllocOwnerBinding, bool) {
	if s == nil || region == "" {
		return treeAllocOwnerBinding{}, false
	}
	for i := len(s.regions) - 1; i >= 0; i-- {
		if s.regions[i].name == region && s.regions[i].ptr != nil {
			return treeAllocOwnerBinding{arenaRef: s.regions[i].ptr}, true
		}
	}
	return treeAllocOwnerBinding{}, false
}

// darrayGrowthOwner resolves the arena a darray growth op (push/reserve/resize/...) allocates into.
// Multi-stack regions (Phase B1b): a fresh inferred-region darray routed to its own parallel
// arena allocates there (kept consistent across all its growth ops, so a realloc never straddles
// arenas). Falls back to the existing region/ambient resolution for everything else. All parallel
// arenas share one region lifetime, so routing is layout-only and cannot affect when memory frees.
func (s *functionState) darrayGrowthOwner(receiver ast.Expr, darrayType *semantic.DArrayType) (treeAllocOwnerBinding, bool) {
	if id, ok := receiver.(*ast.Ident); ok && s.darrayStackTag != nil {
		if tag, ok := s.darrayStackTag[id.Name]; ok {
			if owner, ok := s.regionArenaOwner(tag); ok {
				return owner, true
			}
		}
	}
	if darrayType != nil {
		if owner, ok := s.regionArenaOwner(darrayType.Region); ok {
			return owner, true
		}
	}
	return s.lookupTreeAllocOwner()
}

// containerRegionName peels ref wrappers and returns the allocation region of
// the underlying region-carrying container (darray or dict), or "" if t is not
// such a container. Used to match a region param against a call argument's
// region for hidden-arena passing.
func containerRegionName(t semantic.Type) string {
	for {
		switch tt := t.(type) {
		case *semantic.DArrayType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *semantic.DictType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *semantic.RefType:
			if tt == nil {
				return ""
			}
			t = tt.Elem
		default:
			return ""
		}
	}
}

// resolveRegionArenaArgs builds the hidden Arena& arguments for a call to a
// region-parameterized function: for each region param, find a container param
// annotated with that region, read the matching argument's region, and pass
// that region's arena from the caller's region environment. Order matches the
// hidden params appended by lowerFunctionType.
func (s *functionState) resolveRegionArenaArgs(expr *ast.CallExpr, fn *semantic.FuncType) []C.LLVMValueRef {
	if s == nil || expr == nil || fn == nil || len(fn.RegionParams) == 0 {
		return nil
	}
	out := make([]C.LLVMValueRef, 0, len(fn.RegionParams))
	for _, regionParam := range fn.RegionParams {
		var arena C.LLVMValueRef
		for i, p := range fn.Params {
			if containerRegionName(p) != regionParam || i >= len(expr.Args) {
				continue
			}
			if argRegion := containerRegionName(s.exprType(expr.Args[i])); argRegion != "" {
				if owner, ok := s.regionArenaOwner(argRegion); ok {
					arena = owner.arenaRef
				}
			}
			break
		}
		out = append(out, arena)
	}
	return out
}
func (s *functionState) lookupTreeAllocOwnerForFamily(family *semantic.TreeType) (treeAllocOwnerBinding, bool) {
	if s != nil && s.treeAllocOwner.isPerm {
		return s.treeAllocOwner, true
	}
	if owner, ok := s.lookupTreeAllocOwner(); ok && owner.storeType != nil && (owner.storeValue != nil || owner.storePtr != nil) {
		if owner.storeType.Family == family || (owner.storeType.Family != nil && family != nil && owner.storeType.Family.Name == family.Name) {
			return owner, true
		}
	}
	if owner, ok := s.lookupImplicitTreeStoreOwnerForFamily(family); ok {
		if owner.arenaRef == nil && owner.arenaRefPtr == nil {
			if active, activeOK := s.lookupTreeAllocOwner(); activeOK && active.storeValue == nil && active.storePtr == nil {
				owner.arenaRef = active.arenaRef
				owner.arenaRefPtr = active.arenaRefPtr
			}
		}
		return owner, true
	}
	if owner, ok := s.lookupTreeAllocOwner(); ok {
		if (owner.storeValue == nil && owner.storePtr == nil) || owner.storeType == nil || owner.storeType.Family == family {
			return owner, true
		}
		if owner.arenaRef != nil || owner.arenaRefPtr != nil {
			return treeAllocOwnerBinding{arenaRef: owner.arenaRef, arenaRefPtr: owner.arenaRefPtr}, true
		}
		if owner.storeValue != nil && owner.storeType != nil {
			if arenaRef, err := s.emitTreeStoreArenaValue(owner.storeValue, owner.storeType); err == nil && arenaRef != nil {
				return treeAllocOwnerBinding{arenaRef: arenaRef}, true
			}
		}
	}
	return treeAllocOwnerBinding{}, false
}
func (s *functionState) lookupImplicitTreeStoreOwnerForFamily(family *semantic.TreeType) (treeAllocOwnerBinding, bool) {
	if s == nil || family == nil {
		return treeAllocOwnerBinding{}, false
	}
	if s.implicitTreeStoreOwners != nil {
		if owner, ok := s.implicitTreeStoreOwners[family.Name]; ok && owner.storeType != nil && (owner.storeValue != nil || owner.storePtr != nil) {
			return owner, true
		}
	}
	return s.lookupImplicitTreeStoreBindingForFamily(family)
}
func (s *functionState) lookupImplicitTreeStoreBindingForFamily(family *semantic.TreeType) (treeAllocOwnerBinding, bool) {
	if s == nil || family == nil || family.StoreType == nil {
		return treeAllocOwnerBinding{}, false
	}
	binding, ok := s.lookupBinding(semantic.TreeStoreImplicitParamName(family))
	if !ok || binding.ptr == nil {
		return treeAllocOwnerBinding{}, false
	}
	if storeType, ok := binding.typ.(*semantic.TreeStoreType); ok && storeType != nil && storeType.Family == family {
		return treeAllocOwnerBinding{storePtr: binding.ptr, storeType: storeType}, true
	}
	if storeType, ok := binding.typ.(*semantic.TreeStoreType); ok && storeType != nil && storeType.Family != nil && storeType.Family.Name == family.Name {
		return treeAllocOwnerBinding{storePtr: binding.ptr, storeType: storeType}, true
	}
	return treeAllocOwnerBinding{}, false
}
func (s *functionState) bindImplicitTreeStoreValue(t semantic.Type, value C.LLVMValueRef) {
	storeType, ok := t.(*semantic.TreeStoreType)
	if !ok || storeType == nil || storeType.Family == nil || value == nil {
		return
	}
	if s.implicitTreeStoreOwners == nil {
		s.implicitTreeStoreOwners = map[string]treeAllocOwnerBinding{}
	}
	s.implicitTreeStoreOwners[storeType.Family.Name] = treeAllocOwnerBinding{storeValue: value, storeType: storeType}
}
func (s *functionState) bindImplicitTreeOwnerParam(name string, t semantic.Type, ptr C.LLVMValueRef, value C.LLVMValueRef) {
	if s == nil || s.treeAllocOwner.isPerm || s.treeAllocOwner.arenaRef != nil || s.treeAllocOwner.arenaRefPtr != nil || s.treeAllocOwner.storeValue != nil {
		return
	}
	isRegionParam := name == semantic.RegionPolymorphicImplicitParamName
	if name != "owner" && name != "alloc" && !isRegionParam {
		return
	}
	if storeType, ok := t.(*semantic.TreeStoreType); ok && storeType != nil && storeType.Family != nil && value != nil {
		s.treeAllocOwner = treeAllocOwnerBinding{storeValue: value, storeType: storeType}
		return
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil || ptr == nil {
		return
	}
	if semantic.SameType(t, arenaType) {
		s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: ptr}
		if isRegionParam {
			// docs/75: remember the threaded region so the function's synthesized `__auto_*`
			// region adopts it (instead of creating a fresh, locally-freed arena).
			s.regionPolyOwner = s.treeAllocOwner
		}
		return
	}
	if refType, ok := t.(*semantic.RefType); ok && refType != nil && semantic.SameType(refType.Elem, arenaType) && value != nil {
		s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: value, arenaRefPtr: ptr}
		if isRegionParam {
			s.regionPolyOwner = s.treeAllocOwner
		}
	}
}
func (s *functionState) lookupPackedStore(enumType *semantic.EnumType) (packedStoreBinding, bool) {
	if s.packedStores == nil || enumType == nil {
		return packedStoreBinding{}, false
	}
	binding, ok := s.packedStores[enumType.Name]
	if !ok || binding.typ == nil {
		return packedStoreBinding{}, false
	}
	return binding, true
}
func (s *functionState) bindPackedStoreValue(t semantic.Type, value C.LLVMValueRef) {
	storeType, ok := t.(*semantic.PackedEnumStoreType)
	if !ok || storeType == nil || storeType.Enum == nil || value == nil {
		return
	}
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: value, typ: storeType}
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
		semantic.AppendSpecializedBoundaryTreeStoreParams(specialized)
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
		if sym, ok := s.g.result.GlobalScope.Lookup(n.Name); ok {
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
	s.scope.packedEnumPtrs[name] = packedEnumStorageBinding{ptr: ptr, typ: enumType}
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
func (s *functionState) lookupPackedVariantView(name string) (packedVariantViewBinding, bool) {
	if name == "" {
		return packedVariantViewBinding{}, false
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedViewName == name {
			binding := scope.packedViewBinding
			if binding.typ != nil && (binding.ptr != nil || binding.handle != nil) {
				return binding, true
			}
		}
		binding, ok := scope.packedViewPtrs[name]
		if ok && binding.typ != nil && (binding.ptr != nil || binding.handle != nil) {
			return binding, true
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
