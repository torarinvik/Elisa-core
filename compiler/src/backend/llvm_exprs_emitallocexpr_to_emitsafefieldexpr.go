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
	"strings"
)

// isUnboundImplicitPackedStoreIdent reports whether an identifier is a threaded `__packed_store_E`
// argument with NO local binding — i.e. a caller (the root, e.g. `build`) that does not itself carry
// the store and must create the region-backed store on demand. In make/eval the name IS bound (their
// own implicit param), so the normal ident path handles it.
func (s *functionState) isUnboundImplicitPackedStoreIdent(n *ast.Ident) bool {
	if n == nil || !strings.HasPrefix(n.Name, "__packed_store_") {
		return false
	}
	_, bound := s.lookupBinding(n.Name)
	return !bound
}

// emitImplicitPackedStoreIdent resolves an unbound `__packed_store_E` argument by getting-or-creating
// the region-backed store for E (docs/74), so the root call threads a real store value into the
// callee and reuses it for subsequent calls in the same region.
func (s *functionState) emitImplicitPackedStoreIdent(n *ast.Ident, actualType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	enumType := s.resolveImplicitPackedStoreEnum(n, actualType)
	if enumType == nil {
		return nil, nil, fmt.Errorf("cannot resolve enum for implicit packed store %q", n.Name)
	}
	store, err := s.getOrCreateRegionPackedStore(enumType)
	if err != nil {
		return nil, nil, err
	}
	return store.value, store.typ, nil
}

func (s *functionState) resolveImplicitPackedStoreEnum(n *ast.Ident, actualType semantic.Type) *semantic.EnumType {
	if storeType, ok := semantic.StripAggregateStateType(actualType).(*semantic.PackedEnumStoreType); ok && storeType != nil && storeType.Enum != nil {
		return storeType.Enum
	}
	enumName := strings.TrimPrefix(n.Name, "__packed_store_")
	if enumName == "" || s.g == nil || s.g.result == nil {
		return nil
	}
	if named, ok := s.g.result.NamedTypes[enumName].(*semantic.EnumType); ok && named != nil && named.Packed {
		return named
	}
	return nil
}

func (s *functionState) emitAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.AutoRegion {
		if s.isAutoRegionPackedConstructor(expr.Value) {
			return s.emitAutoRegionPackedAllocExpr(expr)
		}
		return s.emitRegionStructAlloc(expr, s.autoRegionBinding())
	}
	if expr.Owner == nil {
		return s.emitScopedPackedAllocExpr(expr)
	}
	if _, ok := s.exprType(expr.Owner).(*semantic.PackedEnumStoreType); ok {
		return s.emitPackedAllocExpr(expr)
	}
	ownerIdent, ok := expr.Owner.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("only region-backed new[...] is lowered so far")
	}
	binding, found := s.lookupBinding(ownerIdent.Name)
	if !found {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	return s.emitRegionStructAlloc(expr, binding)
}

// autoRegionBinding returns the arena binding for new[auto]: the innermost active inferred region's
// arena (the native stack arena set up by the enclosing synthesized/in-auto region — the same arena
// containers allocate into). A nil ptr means no active region; emitRegionStructAlloc reports it.
func (s *functionState) autoRegionBinding() valueBinding {
	return valueBinding{ptr: s.treeAllocOwner.arenaRef, typ: s.g.result.NamedTypes["Arena"]}
}

// emitRegionStructAlloc lowers a plain-struct allocation into a region's arena (arena_alloc(size)
// then store the constructed value). Shared by new[region] and new[auto].
func (s *functionState) emitRegionStructAlloc(expr *ast.AllocExpr, binding valueBinding) (C.LLVMValueRef, semantic.Type, error) {
	if binding.ptr == nil {
		return nil, nil, fmt.Errorf("new[auto] has no active inferred region arena during LLVM lowering")
	}
	if binding.typ == nil {
		return nil, nil, fmt.Errorf("missing Arena type for region allocation")
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value")
	}
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	sizeBytes, err := s.sizeOfType(valueType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{binding.ptr, sizeValue}, "region.alloc")
	C.LLVMBuildStore(s.builder, value, allocPtr)
	return allocPtr, s.exprType(expr), nil
}

// isAutoRegionPackedConstructor reports whether a `new[auto]` value is a packed enum constructor
// (vs a plain struct). Packed constructors take the region-backed implicit-store path (docs/74);
// structs take the arena path.
func (s *functionState) isAutoRegionPackedConstructor(value ast.Expr) bool {
	switch n := value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		return ok && enumType != nil && variant != nil && enumType.Packed
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		return ok && enumType != nil && variant != nil && enumType.Packed
	}
	return false
}

// emitAutoRegionPackedAllocExpr lowers `new[auto] Expr.V(...)` (docs/74): get-or-create the implicit
// region-backed store for the enum (its columns backed by the active inferred region's arena, its
// lifetime = the region), then push the node row into it. The first new[auto] for an enum in a
// region creates the store; the rest reuse it, so all nodes share one store and their u32 handles
// stay mutually valid.
func (s *functionState) emitAutoRegionPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new[auto] expects a packed enum constructor")
		}
		store, err := s.getOrCreateRegionPackedStore(enumType)
		if err != nil {
			return nil, nil, err
		}
		return s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new[auto] expects a packed enum constructor")
		}
		store, err := s.getOrCreateRegionPackedStore(enumType)
		if err != nil {
			return nil, nil, err
		}
		return s.emitPackedEnumConstructorAlloc(n, store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new[auto] expects a packed enum constructor")
	}
}

// getOrCreateRegionPackedStore returns the implicit packed store for enumType active in the current
// region, creating it (backed by the active inferred region's arena) on first use and registering it
// as the active store so subsequent new[auto] allocations and storeless `match` resolve to it.
func (s *functionState) getOrCreateRegionPackedStore(enumType *semantic.EnumType) (packedStoreBinding, error) {
	arenaPtr := s.treeAllocOwner.arenaRef
	if binding, ok := s.lookupPackedStore(enumType); ok {
		// Reuse an explicit/threaded store (regionArena nil) unconditionally, or an implicit store
		// built on the CURRENT region arena. An implicit store from an outer region (different arena)
		// falls through to build a fresh one so nested-region trees stay region-local.
		if binding.regionArena == nil || binding.regionArena == arenaPtr {
			return binding, nil
		}
	}
	if arenaPtr == nil {
		return packedStoreBinding{}, fmt.Errorf("new[auto] packed allocation has no active inferred region arena")
	}
	// docs/77: a sealed hierarchy shares ONE store per root, whose record is the union over all
	// refinements' leaves. Build (and key) the store on the root.
	root := enumType.Root()
	if root.StoreType == nil {
		return packedStoreBinding{}, fmt.Errorf("packed enum %s is missing store layout metadata", root.Name)
	}
	storeType := semantic.PackedEnumStoreWithState(root.StoreType, s.g.result.NamedTypes["Local"])
	storeValue, err := s.emitPackedStoreValueFromArenaPtr(arenaPtr, storeType)
	if err != nil {
		return packedStoreBinding{}, err
	}
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	binding := packedStoreBinding{value: storeValue, typ: storeType, regionArena: arenaPtr}
	s.packedStores[packedStoreKey(enumType)] = binding
	return binding, nil
}

func (s *functionState) emitScopedPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(n, store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
	}
}
func (s *functionState) emitPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	storeValue, _, err := s.emitExpr(expr.Owner, nil)
	if err != nil {
		return nil, nil, err
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		enumType, variant, ok := s.enumConstructorInfoFromField(fieldExpr)
		if ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			return s.emitPackedEnumConstructorAlloc(nil, storeValue, enumType, variant, nil, nil)
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("packed enum allocation expects a constructor call")
	}
	enumType, variant, ok := s.enumConstructorInfo(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum allocation expects a packed enum constructor call")
	}
	return s.emitPackedEnumConstructorAlloc(callExpr, storeValue, enumType, variant, callExpr.Args, callExpr.ArgNames)
}
func (s *functionState) nodeTableFillTypeArgs(expr *ast.CallExpr) (*semantic.EnumType, semantic.Type, error) {
	if expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, fmt.Errorf("node_table_fill expects explicit specialization")
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, fmt.Errorf("node_table_fill expects exactly 2 type arguments")
	}
	enumArg, err := s.resolveTypeExpr(specialize.TypeArgs[0])
	if err != nil {
		return nil, nil, err
	}
	enumType, ok := semantic.StripAggregateStateType(enumArg).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("node_table_fill expects a packed enum type argument")
	}
	elemType, err := s.resolveTypeExpr(specialize.TypeArgs[1])
	if err != nil {
		return nil, nil, err
	}
	return enumType, elemType, nil
}
func (s *functionState) emitNodeKeyIndexValue(expr ast.Expr) (C.LLVMValueRef, *semantic.EnumType, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyType := s.exprType(expr)
	enumType, ok := semantic.NodeKeyEnumType(keyType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	value, actualType, err := s.emitExpr(expr, keyType)
	if err != nil {
		return nil, nil, true, err
	}
	if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		loaded, loadErr := s.loadValue(value, refType.Elem, "nodekey.load")
		if loadErr != nil {
			return nil, nil, true, loadErr
		}
		value = loaded
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("nodekey.index")), enumType, true, nil
}
func (s *functionState) emitDenseKeyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callIdentName(expr) != "dense_key" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("dense_key expects 2 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("dense_key requires frozen packed-store metadata")
	}
	var handleValue C.LLVMValueRef
	actualNodeType := s.exprType(expr.Args[0])
	sourceEnum, ok := denseKeySourceEnumType(actualNodeType)
	if !ok || sourceEnum == nil {
		return nil, nil, true, fmt.Errorf("dense_key expects a packed enum value or packedview")
	}
	if viewType, ok := actualNodeType.(*semantic.PackedVariantViewType); ok && viewType != nil {
		viewValue, _, err := s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		handleValue = C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("nodekey.view.handle"))
	} else {
		var actualType semantic.Type
		handleValue, actualType, err = s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
			handleValue, err = s.loadValue(handleValue, refType.Elem, "nodekey.handle")
			if err != nil {
				return nil, nil, true, err
			}
		}
	}
	var indexValue C.LLVMValueRef
	switch s.g.packedModeForEnum(sourceEnum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		indexValue, err = s.coerceValue(handleValue, sourceEnum, s.g.result.NamedTypes["u32"])
		if err != nil {
			return nil, nil, true, err
		}
	default:
		return nil, nil, true, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(sourceEnum))
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, indexValue, 0, cStringFree("nodekey.index.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitNodeTableFillHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("node_table_fill expects 3 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, elemType, err := s.nodeTableFillTypeArgs(expr)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("node_table_fill requires frozen packed-store metadata")
	}
	countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "node.table.count")
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.merge"))
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree("node.table.count.zero"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	arenaPtr, _, err := s.emitAddressOrTemp(expr.Args[0])
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount, err := s.emitCheckedElemByteCount(countValue, elemSize, "node.table")
	if err != nil {
		return nil, nil, true, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaPtr, byteCount}, "node.table.alloc.ptr")
	viewType := &semantic.ViewType{Elem: elemType, SurfaceName: "view"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, allocPtr, 0, cStringFree("node.table.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree("node.table.view.len"))
	initValue, actualInitType, err := s.emitExpr(expr.Args[2], elemType)
	if err != nil {
		return nil, nil, true, err
	}
	initValue, err = s.coerceValue(initValue, actualInitType, elemType)
	if err != nil {
		return nil, nil, true, err
	}
	fillType := s.g.cachedRuntimeHelperType("arena_da_fill", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_da_fill", Params: []semantic.Type{viewType, elemType}, Return: s.g.result.NamedTypes["void"]}
	})
	fillCallee, err := s.g.ensureFunctionDeclared("arena_da_fill", fillType)
	if err != nil {
		return nil, nil, true, err
	}
	fillLLVMType, err := s.g.lowerFunctionType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(fillLLVMType, fillCallee, []C.LLVMValueRef{viewValue, initValue}, "")
	materialized := C.LLVMGetUndef(resultLLVMType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewValue, 0, cStringFree("node.table.values.insert"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	zeroResult := C.LLVMConstNull(resultLLVMType)
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("node.table.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitResolvedCall(callee C.LLVMValueRef, funcType *semantic.FuncType, direct bool, args []C.LLVMValueRef, regionArenaArgs []C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	layout, err := s.g.computeFuncAbiLayout(funcType)
	if err != nil {
		return nil, nil, err
	}
	// Convert any memory-class aggregate arguments into byval pointers
	// (memcpy-filled stack temporaries) so they match the lowered pointer
	// parameter type. byvalTypes maps explicit-arg index -> aggregate type.
	args, byvalTypes, err := s.convertByvalArgs(funcType, args)
	if err != nil {
		return nil, nil, err
	}
	// Region-parameterized containers: append the hidden Arena& args (one per
	// region param) after the explicit/implicit args, matching the param order
	// in lowerFunctionType. Empty for non-region-param functions.
	args = append(args, regionArenaArgs...)
	base := layout.paramBase()
	// applyByvalCallAttrs tags the byval pointer arguments on a built call.
	applyByvalCallAttrs := func(call C.LLVMValueRef) {
		for i, ty := range byvalTypes {
			s.addCallByvalAttr(call, base+i, ty)
		}
	}

	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildTypedCall(llvmFnType, callee, callArgs, "calltmp", funcType)
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "calltmp")
			if err != nil {
				return nil, nil, err
			}
		}
		applyByvalCallAttrs(call)
		payload, err := s.loadValue(resultSlot, retUnion.Value, "call.payload")
		if err != nil {
			return nil, nil, err
		}
		unionValue, err := s.buildErrorUnionValue(retUnion, call, payload)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}

	if layout.sret {
		// Large aggregate return: allocate a result slot, prepend it as the
		// hidden sret out-pointer, call (returns void), then load the result.
		resultSlot, err := s.createEntryAlloca("call.sret", funcType.Return)
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildTypedCall(llvmFnType, callee, callArgs, "", funcType)
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "")
			if err != nil {
				return nil, nil, err
			}
		}
		s.addCallSretAttr(call, layout.sretType)
		applyByvalCallAttrs(call)
		result, err := s.loadValue(resultSlot, funcType.Return, "call.sret.val")
		if err != nil {
			return nil, nil, err
		}
		return result, funcType.Return, nil
	}

	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	var call C.LLVMValueRef
	if direct {
		call = s.buildTypedCall(llvmFnType, callee, args, callName, funcType)
	} else {
		call, err = s.emitFunctionValueCall(callee, funcType, args, "calltmp")
		if err != nil {
			return nil, nil, err
		}
	}
	applyByvalCallAttrs(call)
	return call, funcType.Return, nil
}
func (s *functionState) emitSafeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, fmt.Errorf("optional field access requires a receiver")
	}
	resultType := s.exprType(expr)
	optionalType, ok := resultType.(*semantic.OptionalType)
	if !ok || optionalType == nil || optionalType.Value == nil {
		return nil, nil, fmt.Errorf("optional field access requires an optional result type")
	}
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		someValue  C.LLVMValueRef
		noneValue  C.LLVMValueRef
		presentEnd C.LLVMBasicBlockRef
		noneEnd    C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	payloadValue, payloadType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, expr.Field, "safe.field")
	if err != nil {
		return nil, nil, err
	}
	payloadValue, err = s.coerceValue(payloadValue, payloadType, optionalType.Value)
	if err != nil {
		return nil, nil, err
	}
	someValue, err = s.buildOptionalSome(optionalType, payloadValue)
	if err != nil {
		return nil, nil, err
	}
	presentEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	noneValue, err = s.buildOptionalNone(optionalType)
	if err != nil {
		return nil, nil, err
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("valuephi"))
	values := []C.LLVMValueRef{someValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}
