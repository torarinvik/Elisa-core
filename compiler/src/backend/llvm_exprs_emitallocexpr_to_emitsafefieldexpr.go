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
//
// A CONSUMER call (`__packed_store_use_E`, semantic.PackedStoreImplicitUsePrefix) passes
// pre-existing handles of E into the callee, so it must resolve to the store those handles were
// built against: the active binding is reused even when it was created under a DIFFERENT region
// than the current one. Letting it fall through to getOrCreateRegionPackedStore would mint a fresh
// EMPTY store (and clobber the binding), making every handle read in the callee undefined — the
// compile-clean-but-segfaulting storeless cross-function match.
func (s *functionState) emitImplicitPackedStoreIdent(n *ast.Ident, actualType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	enumType := s.resolveImplicitPackedStoreEnum(n, actualType)
	if enumType == nil {
		return nil, nil, fmt.Errorf("cannot resolve enum for implicit packed store %q", n.Name)
	}
	if strings.HasPrefix(n.Name, semantic.PackedStoreImplicitUsePrefix) {
		if binding, ok := s.lookupPackedStore(enumType); ok {
			return binding.value, binding.typ, nil
		}
		// No binding yet: the handles must live in the active region's on-demand store (the
		// docs/74 entry-point pattern — a callee built them into this region's store, and
		// get-or-create below resolves to that same instance).
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
	enumName := strings.TrimPrefix(n.Name, semantic.PackedStoreImplicitUsePrefix)
	enumName = strings.TrimPrefix(enumName, "__packed_store_")
	if enumName == "" || s.g == nil || s.g.result == nil {
		return nil
	}
	if named, ok := s.g.namedType(enumName).(*semantic.EnumType); ok && named != nil && named.Packed {
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
	// A region parameter is not a lexical Arena binding. Its arena arrives as a
	// hidden function argument and is registered in the region environment, just
	// like the arenas used by region-qualified container operations. Resolve
	// `new[r]` through that environment when there is no local binding; otherwise
	// a valid allocation in a region-polymorphic helper fails only in LLVM.
	if !found {
		if arena := s.regionArenaPointer(ownerIdent.Name); arena != nil {
			binding = valueBinding{ptr: arena, typ: s.g.namedType("Arena")}
			found = true
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	return s.emitRegionStructAlloc(expr, binding)
}

// autoRegionBinding returns the arena binding for new[auto]: the innermost active inferred region's
// arena (the native stack arena set up by the enclosing synthesized/in-auto region — the same arena
// containers allocate into). A nil ptr means no active region; emitRegionStructAlloc reports it.
func (s *functionState) autoRegionBinding() valueBinding {
	return valueBinding{ptr: s.treeAllocOwner.arenaRef, typ: s.g.namedType("Arena")}
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
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.namedType("void")
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
//
// Creation is emitted at the owning region's store ANCHOR (right after its arena initialization),
// not at the current builder position: a first use inside a loop body would otherwise re-execute
// ctx_aos_store_new every iteration — a fresh, empty store per iteration — so a handle escaping one
// iteration decodes against a different store (the machine-from degenerate-darray SIGBUS). The
// hoisted store dominates the region's whole extent, and the binding is cached function-wide
// (regionPackedStores, not the scope-restored packedStores) keyed by the region identity.
func (s *functionState) getOrCreateRegionPackedStore(enumType *semantic.EnumType) (packedStoreBinding, error) {
	if binding, ok, err := s.programPackedStore(enumType); ok || err != nil {
		return binding, err // docs/128 §8: the declared program-lived store, whatever the region
	}
	// regionKey is the stable identity for the arena so that the "is this the same arena?" check below
	// uses a compile-time-constant SSA value (an alloca pointer) rather than a loaded pointer value that
	// is a fresh SSA each time. For direct Arena values (arenaRef), the alloca IS the stable key. For
	// Arena& references (arenaRefPtr), use the alloca that holds the reference pointer.
	regionKey := s.treeAllocOwner.arenaRef
	if regionKey == nil {
		regionKey = s.treeAllocOwner.arenaRefPtr
	}
	if binding, ok := s.lookupPackedStore(enumType); ok {
		// Reuse an explicit/threaded store (regionArena nil) unconditionally, or an implicit store
		// built on the CURRENT region arena. An implicit store from an outer region (different arena)
		// falls through to build a fresh one so nested-region trees stay region-local.
		if binding.regionArena == nil || binding.regionArena == regionKey {
			return binding, nil
		}
	}
	if regionKey == nil {
		return packedStoreBinding{}, fmt.Errorf("new[auto] packed allocation has no active inferred region arena (in %s)", s.fnType.Name)
	}
	savedBlock := C.LLVMGetInsertBlock(s.builder)
	hoisted := s.treeAllocOwner.storeAnchorBlock != nil
	if hoisted {
		s.positionAfterStoreAnchor(s.treeAllocOwner.storeAnchorBlock, s.treeAllocOwner.storeAnchorInstr)
	}
	restore := func() {
		if hoisted && savedBlock != nil {
			C.LLVMPositionBuilderAtEnd(s.builder, savedBlock)
		}
	}
	arenaPtr := s.treeAllocOwner.arenaRef
	if arenaPtr == nil {
		loaded, err := s.treeOwnerArenaRefValue(s.treeAllocOwner, "packed.store.owner.arena")
		if err != nil {
			restore()
			return packedStoreBinding{}, err
		}
		arenaPtr = loaded
	}
	// docs/77: a sealed hierarchy shares ONE store per root, whose record is the union over all
	// refinements' leaves. Build (and key) the store on the root.
	root := enumType.Root()
	if root.StoreType == nil {
		restore()
		return packedStoreBinding{}, fmt.Errorf("packed enum %s is missing store layout metadata", root.Name)
	}
	storeType := semantic.PackedEnumStoreWithState(root.StoreType, s.g.namedType("Local"))
	storeValue, err := s.emitPackedStoreValueFromArenaPtr(arenaPtr, storeType)
	restore()
	if err != nil {
		return packedStoreBinding{}, err
	}
	if s.regionPackedStores == nil {
		s.regionPackedStores = map[regionPackedStoreKey]packedStoreBinding{}
	}
	binding := packedStoreBinding{value: storeValue, typ: storeType, regionArena: regionKey}
	s.regionPackedStores[regionPackedStoreKey{region: regionKey, enum: packedStoreKey(enumType)}] = binding
	return binding, nil
}

// positionAfterStoreAnchor positions the builder immediately after the captured anchor
// instruction (or at the block start when the anchor block was empty at capture time).
func (s *functionState) positionAfterStoreAnchor(block C.LLVMBasicBlockRef, instr C.LLVMValueRef) {
	if instr != nil {
		if next := C.LLVMGetNextInstruction(instr); next != nil {
			C.LLVMPositionBuilderBefore(s.builder, next)
			return
		}
		C.LLVMPositionBuilderAtEnd(s.builder, C.LLVMGetInstructionParent(instr))
		return
	}
	if first := C.LLVMGetFirstInstruction(block); first != nil {
		C.LLVMPositionBuilderBefore(s.builder, first)
		return
	}
	C.LLVMPositionBuilderAtEnd(s.builder, block)
}

// captureStoreAnchor snapshots the builder's current position as a (block, last-instruction)
// pair for treeAllocOwnerBinding.storeAnchor*. Called right after an owner's arena is
// initialized, so implicit packed stores hoisted there see a fully-initialized arena.
func (s *functionState) captureStoreAnchor() (C.LLVMBasicBlockRef, C.LLVMValueRef) {
	block := C.LLVMGetInsertBlock(s.builder)
	if block == nil {
		return nil, nil
	}
	return block, C.LLVMGetLastInstruction(block)
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
