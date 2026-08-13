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
	"strings"
	"unsafe"
)

type valueBinding struct {
	ptr     C.LLVMValueRef
	typ     semantic.Type
	mutable bool
}
type conditionBindingInfo struct {
	name string
	typ  semantic.Type
}
type codegenScope struct {
	parent                   *codegenScope
	bindingName              string
	binding                  valueBinding
	bindings                 map[string]valueBinding
	packedCommonValueName    string
	packedCommonValueBinding packedCommonFieldValueBinding
	packedCommonValues       map[string]packedCommonFieldValueBinding
	packedEnumPtrs           map[string]packedEnumStorageBinding
	packedEnumStoreName      string
	packedEnumStoreBinding   packedStoreBinding
	packedEnumStores         map[string]packedStoreBinding
	packedViewName           string
	packedViewBinding        packedVariantViewBinding
	packedViewPtrs           map[string]packedVariantViewBinding
}
type functionState struct {
	g                    *llvmGenerator
	decl                 *ast.FuncDecl
	fnValue              C.LLVMValueRef
	fnType               *semantic.FuncType
	builder              C.LLVMBuilderRef
	scope                *codegenScope
	diScope              C.LLVMMetadataRef
	traceNameGlobal      C.LLVMValueRef
	typeMap              map[string]semantic.Type
	// specializedExprTypes overlays the expression types recorded by the TEMPLATE
	// analysis with the ones a re-analysis produced for THIS instantiation. Empty for
	// a non-generic body. See semantic.Result.SpecializedExprTypes.
	specializedExprTypes map[ast.Expr]semantic.Type
	specializedFuncTypes map[*semantic.FuncType]*semantic.FuncType
	resultSlot           C.LLVMValueRef
	sretReturn           bool
	// scalarGrantDepth > 0 means the code currently being emitted sits inside a `can Scalar` grant
	// (a `can Scalar:` block, or the whole function via `can[Scalar]` on its signature). While
	// granted, loops are NOT tagged expected-to-vectorize, so the post-optimization autovec
	// verifier stays silent for them — the explicit "this is scalar on purpose" escape hatch.
	scalarGrantDepth int
	// aliasSafeElementPtrs holds GEP'd element addresses of scalar-element darrays (e.g. darray[f64]).
	// Loads/stores through these are tagged with the "elt" alias scope (noalias the "hdr" scope), and
	// the darray's data-pointer header load is tagged "hdr" (noalias "elt"). This lets LLVM prove the
	// f64 element stores don't clobber the header's data pointer, so LICM hoists the base-pointer load
	// out of hot loops. Gated to scalar element types: a scalar buffer can never contain a darray
	// header, so hdr != elt is always true (nested darray[darray[...]] is left untagged). All element
	// accesses share ONE "elt" scope, so they remain may-alias to each other (no spurious vectorization).
	aliasSafeElementPtrs map[C.LLVMValueRef]bool
	// disjointScopes carries the per-parameter alias.scope identities for proven-distinct
	// container-ref params (docs/84 Increment 3b). Nil unless -fnoalias is on AND the
	// analyzer's whole-program FuncDisjointParams proved a self-noalias group for this fn.
	disjointScopes *disjointParamScopeState
	// disjointElementPtrs maps a GEP'd element address of a proven-distinct param to its
	// scope, so the subsequent load/store in loadValue/storeValue is tagged with the
	// per-param scope (replacing the shared elt scope) for that element.
	disjointElementPtrs map[C.LLVMValueRef]*disjointParamScope
	// pendingDisjointScope is the scope of the index-site object currently being lowered;
	// set by emitIndexAddress and consumed once by emitRuntimePointerIndexedAddressWithType.
	pendingDisjointScope *disjointParamScope
	regions              []regionBinding
	// darrayStackTag routes a fresh inferred-region darray (by name) to its assigned parallel
	// arena (multi-stack regions, Phase B1b): name -> region arena tag "__auto_N#k". Populated at
	// region entry from Result.RegionStacks, cleared at region exit. Empty for ordinary code.
	darrayStackTag map[string]string
	// currentDArraySinkTag is the parallel-arena tag ("__auto_N#k") of the darray a seeded
	// container initializer is being stored into — set around a VarDecl/assign RHS emit when the
	// destination has a darrayStackTag, and CONSUMED ONCE (read-and-cleared) by the container
	// literal / comprehension emit so its initial backing lands in the SAME parallel arena its
	// later growth ops use. Without this a non-empty-seeded local's backing goes to the region
	// base arena while a grower reallocs in the parallel arena — a straddling realloc that trips
	// `assert a.end != null` (task_00a7fdf3). Empty for ordinary code / empty-`[]` seeds.
	currentDArraySinkTag string
	// earlyFreeByOffset frees an own-stack arena early (Phase B2): the byte offset of a top-level
	// statement -> the stack arena to free right after it (the object died and is not aliased).
	// Populated at region entry, fired once and removed when the statement is emitted.
	earlyFreeByOffset map[int]C.LLVMValueRef
	packedStores      map[string]packedStoreBinding
	// regionPackedStores caches the IMPLICIT region-backed packed store per (region, enum root)
	// (getOrCreateRegionPackedStore). Unlike packedStores it is NOT scope-cloned/restored:
	// the creation instructions are hoisted to the owning region's anchor (they dominate the
	// region's whole extent), and the region half of the key makes an entry valid exactly
	// while that region is the active owner — so a scope exit between two uses must not drop
	// the binding (that re-created an empty store and broke previously-issued handles), and a
	// nested region's store never evicts the outer region's.
	regionPackedStores map[regionPackedStoreKey]packedStoreBinding
	treeAllocOwner     treeAllocOwnerBinding
	// regionPolyOwner is the region threaded into a region-polymorphic function via the hidden
	// `__region_auto` Arena& param (docs/75). The function's synthesized `__auto_*` region adopts it
	// rather than creating a fresh, locally-freed arena, so `new[auto]` allocates into the caller's
	// region and the returned handle outlives the call. Zero value (nil arenaRef) for ordinary fns.
	regionPolyOwner treeAllocOwnerBinding
	// ambientGrownContainerRegion (the void-grower fix) is the `__rg_<param>` region of a caller-owned
	// container this function grows with region-allocated inserts; when set, the function's synthesized
	// `__auto_*` region adopts that container's arena so inserted region-poly values land in the
	// caller's region. Empty for ordinary functions.
	ambientGrownContainerRegion  string
	currentSequenceRewrite       *sequenceRewriteCodegenContext
	packedStoreValueKey1         packedStoreExtractCacheKey
	packedStoreValue1            C.LLVMValueRef
	packedStoreValueKey2         packedStoreExtractCacheKey
	packedStoreValue2            C.LLVMValueRef
	packedStoreValueKey3         packedStoreExtractCacheKey
	packedStoreValue3            C.LLVMValueRef
	packedStoreValues            map[packedStoreExtractCacheKey]C.LLVMValueRef
	packedVariantSparseTagReads  map[packedVariantSparseTagReadCacheKey]C.LLVMValueRef
	packedVariantSparseWordReads map[packedVariantSparseWordReadCacheKey]C.LLVMValueRef
	packedDenseDArrayItemsReads  map[packedDenseDArrayItemsReadCacheKey]C.LLVMValueRef
	packedDenseTagReads          map[packedDenseTagReadCacheKey]C.LLVMValueRef
	packedDenseWordReads         map[packedDenseWordReadCacheKey]C.LLVMValueRef
	packedDenseSideWordReads     map[packedDenseSideWordReadCacheKey]C.LLVMValueRef
	packedDirectFieldReads       map[packedDirectFieldReadCacheKey]C.LLVMValueRef
	packedVariantPayloadReads    map[packedVariantPayloadReadCacheKey][]C.LLVMValueRef
	// nonPackedEnumMatchTemps maps a match scrutinee SSA value to the single
	// addressable copy emitted before its dispatch. The copy dominates every arm.
	nonPackedEnumMatchTemps map[C.LLVMValueRef]C.LLVMValueRef
	// straightLineBlockParent maps a basic block created purely as a straight-line continuation of
	// another (e.g. the `wd.ok` arm of an index bounds-check, whose `wd.fail` arm always traps) to its
	// predecessor. Such a block is only reachable from that predecessor, so a value live in the
	// predecessor dominates it. The dense read caches canonicalize their per-block key through this
	// chain so repeated reads across trap-guard splits still share one cached read; real divergent
	// blocks (if/match/loop arms) never register here and keep distinct keys (plus the explicit
	// invalidatePackedReadCaches at mutations/binds), so cross-branch reuse stays blocked.
	straightLineBlockParent map[C.LLVMBasicBlockRef]C.LLVMBasicBlockRef
	scopedCleanups          []scopedCleanupBinding
	checkpoints             map[string]checkpointBinding
	poolScopes              []activePoolBinding
	breakTargets            []C.LLVMBasicBlockRef
	continueTargets         []C.LLVMBasicBlockRef
	loopCleanupFloors       []int
	cleanupDepth            int
	scopePool               []*codegenScope
	// reduceReassocScope counts active comprehension-fold accumulator-update scopes. While > 0, FP
	// ops emitted in the scope get reassociation+contraction (not full fast-math), so the reduction
	// re-brackets into a vectorizable tree — a fold's reduction order is defined as a tree, not strict
	// left-to-right (docs/79). Scoped to the one accumulator update, not program-wide.
	reduceReassocScope int
	// oldCaptures holds, for each `old(expr)` pseudo-call in this function's `ensure` clauses, the
	// value of expr captured at function entry (the SSA value dominates all returns). emitExpr reads
	// it when lowering the `old(...)` node during postcondition checks. nil when there are no olds.
	oldCaptures map[*ast.CallExpr]oldCapture
	// activeInvariants holds the in-body `invariant` conditions currently in scope, each with the set
	// of identifier names it reads, so a later assignment to one of those names re-asserts the
	// invariant (docs/90 brick 90-14). Truncated at block exit; debug-gated like all contracts.
	activeInvariants []activeInvariant
	// Large compiler-generated temporaries are heap-backed to keep debug/O0 stack
	// frames bounded. They are allocated in the entry block and freed on every
	// normal function exit after the return payload has been copied out.
	heapTemps []C.LLVMValueRef
}

type oldCapture struct {
	val C.LLVMValueRef
	typ semantic.Type
}

func (s *functionState) currentNamespace() string {
	if s == nil {
		return ""
	}
	name := ""
	if s.fnType != nil {
		name = strings.TrimSpace(s.fnType.Name)
	}
	if name == "" && s.g != nil && s.decl != nil && s.g.symbolsByNode != nil {
		if sym, ok := s.g.symbolsByNode[s.decl]; ok && sym != nil {
			name = strings.TrimSpace(sym.Name)
		}
	}
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func (s *functionState) visibleGlobalNames(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") || strings.Contains(name, "::") {
		return []string{strings.ReplaceAll(name, "::", ".")}
	}
	namespace := s.currentNamespace()
	if namespace == "" {
		return []string{name}
	}
	return []string{namespace + "." + name, name}
}

// lookupVisibleNamedType resolves a (possibly unqualified) type name against the
// analyzer's NamedTypes the same way lookupVisibleGlobalSymbol resolves values:
// current-namespace candidates first, then a unique-suffix fallback so a `using`
// (which the backend does not track) still resolves when unambiguous.
func (s *functionState) lookupVisibleNamedType(name string) (semantic.Type, string, bool) {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.NamedTypes == nil {
		return nil, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if t, ok := s.g.result.NamedTypes[candidate]; ok && t != nil {
			return t, candidate, true
		}
	}
	if !strings.Contains(name, ".") && !strings.Contains(name, "::") {
		suffix := "." + strings.TrimSpace(name)
		var resolvedName string
		var resolved semantic.Type
		for candidate, t := range s.g.result.NamedTypes {
			if !strings.HasSuffix(candidate, suffix) || t == nil {
				continue
			}
			if resolved != nil {
				return nil, "", false
			}
			resolvedName = candidate
			resolved = t
		}
		if resolved != nil {
			return resolved, resolvedName, true
		}
	}
	return nil, "", false
}

func (s *functionState) lookupVisibleGlobalSymbol(name string) (*semantic.Symbol, string, bool) {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil {
		return nil, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if sym, ok := s.g.result.GlobalScope.Lookup(candidate); ok && sym != nil {
			return sym, candidate, true
		}
	}
	if !strings.Contains(name, ".") && !strings.Contains(name, "::") {
		suffix := "." + strings.TrimSpace(name)
		var resolvedName string
		var resolvedSym *semantic.Symbol
		for candidate, sym := range s.g.result.GlobalScope.Symbols {
			if !strings.HasSuffix(candidate, suffix) || sym == nil {
				continue
			}
			if resolvedSym != nil {
				return nil, "", false
			}
			resolvedName = candidate
			resolvedSym = sym
		}
		if resolvedSym != nil {
			return resolvedSym, resolvedName, true
		}
	}
	return nil, "", false
}

func (s *functionState) visibleConstValue(name string) (semantic.ConstValue, string, bool) {
	if s == nil || s.g == nil {
		return semantic.ConstValue{}, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if value, ok := s.g.constValue(candidate); ok {
			return value, candidate, true
		}
	}
	return semantic.ConstValue{}, "", false
}

type scopedCleanupKind int

const (
	scopedCleanupLockGuard scopedCleanupKind = iota
	scopedCleanupThreadPool
	scopedCleanupDeferBody
	scopedCleanupValue
	// scopedCleanupRegion frees a scoped region's arena at block exit (every exit path), so a
	// `region`/`in auto:` scope reclaims at scope exit instead of leaking until function return.
	scopedCleanupRegion
	// scopedCleanupRegionReset resets (not frees) a loop-entered lazy region at block exit, so its
	// blocks are reused next iteration (no mmap/munmap churn); function-return arena_free releases.
	scopedCleanupRegionReset
)

type scopedCleanupBinding struct {
	kind      scopedCleanupKind
	name      string
	ptr       C.LLVMValueRef
	typ       semantic.Type
	owner     *codegenScope
	deferBody *deferredBodyBinding
}
type deferredBodyBinding struct {
	stmt         *ast.DeferStmt
	captureScope *codegenScope
}
type regionBinding struct {
	name  string
	ptr   C.LLVMValueRef
	typ   semantic.Type
	owned bool
}
type checkpointBindingKind int

const (
	checkpointBindingRegion checkpointBindingKind = iota
	checkpointBindingDArray
)

type checkpointBinding struct {
	kind       checkpointBindingKind
	name       string
	targetPtr  C.LLVMValueRef
	targetType semantic.Type
	markPtr    C.LLVMValueRef
	markType   semantic.Type
}
type packedStoreBinding struct {
	value C.LLVMValueRef
	typ   *semantic.PackedEnumStoreType
	// regionArena marks an IMPLICIT region-backed store (docs/74) with the arena it was built on.
	// nil for an explicit or threaded store (those are reused unconditionally). An implicit store is
	// reused only within its own region; entering a nested region (a different arena) builds a fresh
	// one, so per-region trees are reclaimed with their region instead of leaking into an outer one.
	regionArena C.LLVMValueRef
}
type treeAllocOwnerBinding struct {
	arenaRef    C.LLVMValueRef
	arenaRefPtr C.LLVMValueRef
	// storeAnchorBlock/storeAnchorInstr mark the position right after this owner's arena
	// became initialized. Implicit region-backed packed stores (getOrCreateRegionPackedStore)
	// are emitted THERE, not at first use: a first use inside a loop body would otherwise
	// re-execute ctx_aos_store_new every iteration (a fresh empty store per iteration), so a
	// handle escaping one iteration decodes against a different store — the machine-from
	// degenerate-darray SIGBUS. One hoisted store per (region, enum) dominates every use.
	storeAnchorBlock C.LLVMBasicBlockRef
	storeAnchorInstr C.LLVMValueRef
}

// regionPackedStoreKey identifies an implicit region-backed packed store: the owning region's
// stable arena identity (an alloca/param pointer) plus the enum root's name.
type regionPackedStoreKey struct {
	region C.LLVMValueRef
	enum   string
}
type packedStoreExtractCacheKey struct {
	block C.LLVMBasicBlockRef
	store C.LLVMValueRef
	index C.unsigned
}
type packedVariantSparseTagReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	handle    C.LLVMValueRef
}
type packedVariantSparseWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	handle    C.LLVMValueRef
	offset    C.LLVMValueRef
}
type packedReadOriginKey struct {
	root C.LLVMValueRef
	path string
}
type packedDenseTagReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	handle    C.LLVMValueRef
}
type packedDenseDArrayItemsReadCacheKey struct {
	block            C.LLVMBasicBlockRef
	storeType        *semantic.PackedEnumStoreType
	state            C.LLVMValueRef
	fieldOffsetBytes uint64
}
type packedDenseWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	handle    C.LLVMValueRef
	offset    C.LLVMValueRef
}
type packedDenseSideWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	index     C.LLVMValueRef
	offset    C.LLVMValueRef
}
type packedDirectFieldReadCacheKey struct {
	block    C.LLVMBasicBlockRef
	store    C.LLVMValueRef
	enumType *semantic.EnumType
	origin   packedReadOriginKey
	handle   C.LLVMValueRef
	offset   uint64
	size     uint64
	typeKey  string
}
type packedVariantPayloadReadCacheKey struct {
	block    C.LLVMBasicBlockRef
	store    C.LLVMValueRef
	enumType *semantic.EnumType
	variant  *semantic.EnumVariant
	origin   packedReadOriginKey
	handle   C.LLVMValueRef
}
type packedEnumStorageBinding struct {
	ptr C.LLVMValueRef
	// block is where ptr (the decoded record address) was computed; reuse is only sound
	// from the same basic block (a sibling branch is not dominated by it).
	block C.LLVMBasicBlockRef
	typ   *semantic.EnumType
}
type packedCommonFieldValueBinding struct {
	typ    *semantic.EnumType
	values packedPayloadValueCache
}
type packedVariantViewBinding struct {
	ptr C.LLVMValueRef
	// ptrBlock is the basic block where ptr (the decoded record address) was computed. A
	// memoized decode is only safe to reuse from the SAME block — a sibling branch is not
	// dominated by it (LLVM "instruction does not dominate all uses"). Other blocks re-decode.
	ptrBlock C.LLVMBasicBlockRef
	handle   C.LLVMValueRef
	// handleBlock is the basic block where a MEMOIZED handle load was emitted (emitIdent's
	// load-and-rebind path). Like ptrBlock, the cached SSA value is only safe to reuse from the
	// same block — a sibling branch is not dominated by it. nil means the handle is valid
	// wherever the binding is visible (e.g. a pattern-dispatch extract that dominates the arm).
	handleBlock   C.LLVMBasicBlockRef
	store         packedStoreBinding
	typ           *semantic.PackedVariantViewType
	payloadValues packedPayloadValueCache
}
type packedPayloadValueCache struct {
	name1  string
	value1 C.LLVMValueRef
	name2  string
	value2 C.LLVMValueRef
	values map[string]C.LLVMValueRef
}
type activePoolBinding struct {
	name    string
	ptr     C.LLVMValueRef
	typ     semantic.Type
	workers C.LLVMValueRef
}

const (
	branchWeightLikely   = 2000
	branchWeightUnlikely = 1
)

func (g *llvmGenerator) defineFunctionBody(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef) error {
	return g.defineFunctionBodyWithBindings(decl, fnType, fnValue, nil)
}
func backendExplicitParamCount(fnType *semantic.FuncType, decl *ast.FuncDecl) int {
	if fnType == nil {
		if decl == nil {
			return 0
		}
		return len(decl.Params)
	}
	if fnType.ExplicitParamCount != 0 || len(fnType.ImplicitParamNames) != 0 {
		return fnType.ExplicitParamCount
	}
	if decl != nil {
		return len(decl.Params)
	}
	return len(fnType.Params)
}
func (g *llvmGenerator) defineFunctionBodyWithBindings(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef, typeBindings map[string]semantic.Type) error {
	if decl == nil || fnType == nil || fnValue == nil {
		return fmt.Errorf("cannot define function body without declaration, type, and value")
	}
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)

	entryName := cString("entry")
	defer C.free(unsafe.Pointer(entryName))
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, entryName)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	state := &functionState{
		g:       g,
		decl:    decl,
		fnValue: fnValue,
		fnType:  fnType,
		builder: builder,
		typeMap: typeBindings,
	}
	// MONOMORPHIZATION: re-analyze this body with the type parameters bound to their
	// concrete arguments, so type-directed typing rules are decided on the INSTANTIATED
	// type rather than on the opaque template. Without this a specialized `T& + n` keeps
	// the pointer-arithmetic typing chosen when T was unknown, and means something
	// different from the identical code spelled `i64&`.
	if len(typeBindings) != 0 && g.result != nil {
		state.specializedExprTypes = g.result.SpecializedExprTypes(decl, orderedGenericTypeArgs(fnType, decl, typeBindings))
	}
	// A signature-level `can[Scalar]` grants the whole body; `can Scalar:` blocks nest on top
	// (see the CanStmt case in emitStmt).
	if decl != nil && permissionRefsGrantScalar(decl.Permissions) {
		state.scalarGrantDepth = 1
	}

	if g.di != nil {
		g.di.attachFunction(state, decl, fnValue)
	}
	if g.trace != nil {
		state.traceNameGlobal = g.trace.nameGlobalFor(decl.Name)
	}
	// docs/84 Increment 3b: compute the per-parameter disjoint alias scopes (no-op unless
	// -fnoalias and a proven self-noalias group). Done before param binding so the explicit
	// loop below can record each scope's shadowing-proof alloca identity.
	state.initDisjointParamScopes()

	abiLayout, layoutErr := g.computeFuncAbiLayout(fnType)
	if layoutErr != nil {
		return layoutErr
	}
	abiCABI := funcTypeIsCABI(fnType)
	paramOffset := abiLayout.paramBase()
	if abiLayout.errorUnionOut {
		state.resultSlot = C.LLVMGetParam(fnValue, 0)
	}
	if abiLayout.sret {
		state.resultSlot = C.LLVMGetParam(fnValue, C.unsigned(abiLayout.sretParamPos()))
		state.sretReturn = true
	}

	explicitCount := backendExplicitParamCount(fnType, decl)
	bindParam := func(name string, mutable bool, typeIndex int, llvmIndex int) error {
		if typeIndex < 0 || typeIndex >= len(fnType.Params) {
			return nil
		}
		paramType := fnType.Params[typeIndex]
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(llvmIndex+paramOffset))
		if g.aggregateIsMemoryClassABI(paramType, abiCABI) {
			// memory-class: the parameter is already a pointer to the aggregate
			// (a byval private copy, or on arm64 C-ABI a plain indirect pointer to
			// the caller's copy), so use it directly as the binding's address.
			state.defineBinding(name, valueBinding{ptr: paramValue, typ: paramType, mutable: mutable})
			return nil
		}
		alloca, err := state.createEntryAlloca(name, paramType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(name, valueBinding{ptr: alloca, typ: paramType, mutable: mutable})
		state.bindPackedStoreValue(paramType, paramValue)
		state.bindImplicitTreeOwnerParam(name, paramType, alloca, paramValue)
		return nil
	}

	for i, param := range decl.Params {
		if err := bindParam(param.Name, param.Mutable, i, i); err != nil {
			return err
		}
		// docs/84 Increment 3b: record this param's binding pointer as its scope identity.
		// The pointer (not the name) is the shadowing-proof key checked at each index site.
		if state.disjointScopes != nil {
			if binding, ok := state.lookupBinding(param.Name); ok {
				state.recordDisjointParamAlloca(i, binding.ptr)
			}
		}
		if g.di != nil {
			if binding, ok := state.lookupBinding(param.Name); ok {
				g.di.declareVariable(state, param.Name, binding.ptr, binding.typ, decl.Pos().Line, i+1)
			}
		}
	}
	for i, name := range fnType.ImplicitParamNames {
		if err := bindParam(name, false, explicitCount+i, explicitCount+i); err != nil {
			return err
		}
	}

	// Region-parameterized containers: the hidden Arena& params (appended after
	// all explicit/implicit params by lowerFunctionType) become this function's
	// region environment, so container ops on `@r` resolve their arena via
	// regionArenaOwner(r). Inert for functions with no region params.
	if len(fnType.RegionParams) != 0 {
		arenaType := g.result.NamedTypes["Arena"]
		base := paramOffset + len(fnType.Params)
		for j, regionName := range fnType.RegionParams {
			arenaParam := C.LLVMGetParam(fnValue, C.unsigned(base+j))
			state.regions = append(state.regions, regionBinding{name: regionName, ptr: arenaParam, typ: arenaType, owned: false})
		}
	}

	// Void-grower ambient region (docs/75 cross-fn container growth): when this function grows a
	// caller-owned container by INSERTING region-allocated values (`out.push(make_node())`), bind its
	// ambient allocation region to the container's (caller-provided) arena. A region-poly callee
	// producing the inserted value then allocates into the caller's container region — adopted with it
	// — instead of a per-call synthesized `__auto_*` region freed on return (the void-grower UAF).
	if decl.AmbientGrownContainerRegion != "" {
		state.ambientGrownContainerRegion = decl.AmbientGrownContainerRegion
		if owner, ok := state.regionArenaOwner(decl.AmbientGrownContainerRegion); ok && state.regionPolyOwner.arenaRef == nil {
			state.regionPolyOwner = owner
			if state.treeAllocOwner.arenaRef == nil && state.treeAllocOwner.arenaRefPtr == nil {
				state.treeAllocOwner = owner
			}
		}
	}

	if err := state.emitOldCaptures(decl); err != nil {
		return err
	}
	if err := state.emitPreconditionChecks(decl); err != nil {
		return err
	}

	if err := state.emitBlock(decl.Body, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		// Fall-through exit (no explicit `return`): check value-contract `ensure <bool>` postconditions
		// (docs/90) and `ensures … is Law` postconditions (docs/85 brick 2) here too, so a void function
		// that omits a return still enforces its contract / backs the caller's gained fact.
		if err := state.emitPostconditionChecks(nil, nil); err != nil {
			return err
		}
		if err := state.emitRefinementPostconditionChecks(); err != nil {
			return err
		}
		if err := state.emitActiveScopedCleanup(); err != nil {
			return err
		}
		if state.currentBlockTerminated() {
			return nil
		}
		if err := state.emitRegionCleanup(); err != nil {
			return err
		}
		if isVoidType(fnType.Return) {
			if err := state.emitHeapTempCleanup(); err != nil {
				return err
			}
			C.LLVMBuildRetVoid(builder)
		} else if retUnion, ok := fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
			zeroCode, err := state.errorCodeConstant(0)
			if err != nil {
				return err
			}
			successValue, err := state.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
			if err != nil {
				return err
			}
			if err := state.emitHeapTempCleanup(); err != nil {
				return err
			}
			C.LLVMBuildRet(builder, successValue)
		} else {
			return fmt.Errorf("function %s may fall through without returning a value", decl.Name)
		}
	}

	return nil
}
func (s *functionState) emitFunctionReturn(value C.LLVMValueRef, actual semantic.Type) error {
	if err := s.emitPostconditionChecks(value, actual); err != nil {
		return err
	}
	if err := s.emitRefinementPostconditionChecks(); err != nil {
		return err
	}
	if err := s.emitActiveScopedCleanup(); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	if err := s.emitRegionCleanup(); err != nil {
		return err
	}
	if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok {
		coerced, err := s.coerceValue(value, actual, retUnion)
		if err != nil {
			return err
		}
		if isVoidType(retUnion.Value) {
			if err := s.emitHeapTempCleanup(); err != nil {
				return err
			}
			C.LLVMBuildRet(s.builder, coerced)
			return nil
		}
		if s.resultSlot == nil {
			return fmt.Errorf("function %s is missing a hidden return slot for %s", s.decl.Name, retUnion.String())
		}
		errorCode, err := s.extractErrorUnionCode(coerced, retUnion)
		if err != nil {
			return err
		}
		payload, err := s.extractErrorUnionPayload(coerced, retUnion)
		if err != nil {
			return err
		}
		// A non-void error union uses a split ABI (scalar error return plus payload
		// out-pointer).  Large payloads must use the same memcpy-aware store path as
		// ordinary sret returns; a raw LLVM aggregate store makes llc -O0 expand a
		// multi-hundred-KB value into megabytes of elementwise machine code.
		if err := s.storeValue(s.resultSlot, payload, retUnion.Value, "errunion.ret"); err != nil {
			return err
		}
		if err := s.emitHeapTempCleanup(); err != nil {
			return err
		}
		C.LLVMBuildRet(s.builder, errorCode)
		return nil
	}
	if isVoidType(s.fnType.Return) {
		// `return <void-expr>` (e.g. `return void_fn()`): the value expression was
		// already emitted for its side effects by the caller. A void function must
		// terminate with RetVoid — building a value `ret` here yields an invalid
		// `ret void <badref>` that the module verifier rejects.
		if err := s.emitHeapTempCleanup(); err != nil {
			return err
		}
		C.LLVMBuildRetVoid(s.builder)
		return nil
	}
	coerced, err := s.coerceValue(value, actual, s.fnType.Return)
	if err != nil {
		return err
	}
	if s.sretReturn {
		// Large aggregate return: write the value through the sret out-pointer
		// (memcpy-lowered when `coerced` is a load) and return void.
		if s.resultSlot == nil {
			return fmt.Errorf("function is missing the sret return slot")
		}
		if err := s.storeValue(s.resultSlot, coerced, s.fnType.Return, "sret.ret"); err != nil {
			return err
		}
		if err := s.emitHeapTempCleanup(); err != nil {
			return err
		}
		C.LLVMBuildRetVoid(s.builder)
		return nil
	}
	if err := s.emitHeapTempCleanup(); err != nil {
		return err
	}
	C.LLVMBuildRet(s.builder, coerced)
	return nil
}
func (s *functionState) emitRegionCleanup() error {
	for i := len(s.regions) - 1; i >= 0; i-- {
		if !s.regions[i].owned {
			continue
		}
		if err := s.emitArenaFree(s.regions[i].ptr, s.regions[i].typ); err != nil {
			return err
		}
	}
	return nil
}

// pushLoopTargets records the break/continue targets for a loop along with the current scoped
// cleanup floor — the number of cleanups registered by ENCLOSING scopes. A break/continue must
// only run cleanups registered INSIDE the loop body (indices >= floor); firing an enclosing
// scope's cleanup (e.g. a function-body region that wraps the loop) on a continue that stays
// within the loop would free a still-live region — a use-after-free.
func (s *functionState) pushLoopTargets(breakBB, continueBB C.LLVMBasicBlockRef) {
	s.breakTargets = append(s.breakTargets, breakBB)
	s.continueTargets = append(s.continueTargets, continueBB)
	s.loopCleanupFloors = append(s.loopCleanupFloors, len(s.scopedCleanups))
}

func (s *functionState) popLoopTargets() {
	s.breakTargets = s.breakTargets[:len(s.breakTargets)-1]
	s.continueTargets = s.continueTargets[:len(s.continueTargets)-1]
	s.loopCleanupFloors = s.loopCleanupFloors[:len(s.loopCleanupFloors)-1]
}

// emitActiveScopedCleanup runs ALL active scoped cleanups. This is the function-EXIT semantics:
// a return / function return / lambda return leaves every enclosing scope, so every registered
// cleanup (defers, mutex unlocks, pool shutdowns, region frees) must fire — including those of
// scopes that enclose a loop the return sits inside. Break/continue must NOT use this (they stay
// within the loop's enclosing scopes); they use emitLoopExitCleanup, which stops at the loop floor.
func (s *functionState) emitActiveScopedCleanup() error {
	return s.emitScopedCleanupsFrom(0)
}

// emitLoopExitCleanup runs only the cleanups registered INSIDE the innermost loop (indices >= its
// floor). break/continue transfer control to a point still inside every enclosing scope, so firing
// an enclosing scope's cleanup (e.g. a region that wraps the loop) would free a still-live owner —
// a use-after-free. When not inside a loop the floor is 0 (identical to emitActiveScopedCleanup).
func (s *functionState) emitLoopExitCleanup() error {
	floor := 0
	if len(s.loopCleanupFloors) > 0 {
		floor = s.loopCleanupFloors[len(s.loopCleanupFloors)-1]
	}
	return s.emitScopedCleanupsFrom(floor)
}

func (s *functionState) emitScopedCleanupsFrom(floor int) error {
	if s.cleanupDepth != 0 {
		return nil
	}
	s.cleanupDepth++
	defer func() {
		s.cleanupDepth--
	}()
	for i := len(s.scopedCleanups) - 1; i >= floor; i-- {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitScopedCleanup(s.scopedCleanups[i]); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitScopedCleanup(binding scopedCleanupBinding) error {
	if binding.kind == scopedCleanupDeferBody {
		return s.emitDeferredBody(binding.deferBody)
	}
	if binding.kind == scopedCleanupRegion {
		// arena_free is idempotent (nulls begin/end) and an adopted/destroyed region is already
		// zeroed, so freeing here is safe even though the function-return cleanup also frees it.
		return s.emitArenaFree(binding.ptr, binding.typ)
	}
	if binding.kind == scopedCleanupRegionReset {
		// Keep the blocks for next iteration; the function-return arena_free releases them.
		return s.emitArenaReset(binding.ptr, binding.typ)
	}
	ops := semantic.CreateTypeBoundOps(binding.typ)
	if len(ops) == 0 {
		switch binding.kind {
		case scopedCleanupLockGuard:
			return s.emitConditionalMutexUnlock(binding)
		case scopedCleanupThreadPool:
			return s.emitConditionalPoolShutdown(binding)
		default:
			return fmt.Errorf("unsupported scoped cleanup kind %d", binding.kind)
		}
	}
	for _, op := range ops {
		if len(op.Path) != 0 || op.IsFillSeq() {
			return fmt.Errorf("unsupported synthesized scoped cleanup path for %q", binding.name)
		}
		switch op.Kind {
		case semantic.TypeBoundCleanupMutexUnlock:
			if err := s.emitConditionalMutexUnlock(binding); err != nil {
				return err
			}
		case semantic.TypeBoundCleanupThreadPoolShutdown:
			if err := s.emitConditionalPoolShutdown(binding); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported synthesized scoped cleanup kind %q", op.Kind)
		}
	}
	return nil
}
func defineBindingInCodegenScope(scope *codegenScope, name string, binding valueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.bindingName == "" || scope.bindingName == name {
		scope.bindingName = name
		scope.binding = binding
		return
	}
	if scope.bindings == nil {
		scope.bindings = map[string]valueBinding{}
	}
	scope.bindings[name] = binding
}
func bindPackedCommonFieldValueInCodegenScope(scope *codegenScope, name string, binding packedCommonFieldValueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedCommonValueName == "" || scope.packedCommonValueName == name {
		scope.packedCommonValueName = name
		scope.packedCommonValueBinding = binding
		return
	}
	if scope.packedCommonValues == nil {
		scope.packedCommonValues = map[string]packedCommonFieldValueBinding{}
	}
	scope.packedCommonValues[name] = binding
}
func bindPackedEnumStorageInCodegenScope(scope *codegenScope, name string, binding packedEnumStorageBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumPtrs == nil {
		scope.packedEnumPtrs = map[string]packedEnumStorageBinding{}
	}
	scope.packedEnumPtrs[name] = binding
}
func bindPackedEnumStoreInCodegenScope(scope *codegenScope, name string, binding packedStoreBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumStoreName == "" || scope.packedEnumStoreName == name {
		scope.packedEnumStoreName = name
		scope.packedEnumStoreBinding = binding
		return
	}
	if scope.packedEnumStores == nil {
		scope.packedEnumStores = map[string]packedStoreBinding{}
	}
	scope.packedEnumStores[name] = binding
}
func bindPackedViewInCodegenScope(scope *codegenScope, name string, binding packedVariantViewBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedViewName == "" || scope.packedViewName == name {
		scope.packedViewName = name
		scope.packedViewBinding = binding
		return
	}
	if scope.packedViewPtrs == nil {
		scope.packedViewPtrs = map[string]packedVariantViewBinding{}
	}
	scope.packedViewPtrs[name] = binding
}
func cloneValueBindingMap(src map[string]valueBinding) map[string]valueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]valueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedCommonBindingMap(src map[string]packedCommonFieldValueBinding) map[string]packedCommonFieldValueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedCommonFieldValueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedEnumStorageBindingMap(src map[string]packedEnumStorageBinding) map[string]packedEnumStorageBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedEnumStorageBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedStoreBindingMap(src map[string]packedStoreBinding) map[string]packedStoreBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedStoreBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedViewBindingMap(src map[string]packedVariantViewBinding) map[string]packedVariantViewBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedVariantViewBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}

// emitOldCaptures evaluates every `old(expr)` appearing in the function's `ensure` clauses at entry
// and records the value, so postcondition checks at each return read the entry-time value. Debug
// builds only. The captured SSA values are computed in the entry region and dominate all returns.
func (s *functionState) emitOldCaptures(decl *ast.FuncDecl) error {
	if decl == nil || len(decl.EnsureValues) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	var olds []*ast.CallExpr
	for _, e := range decl.EnsureValues {
		collectOldCalls(e, &olds)
	}
	if len(olds) == 0 {
		return nil
	}
	s.oldCaptures = make(map[*ast.CallExpr]oldCapture, len(olds))
	for _, oc := range olds {
		if len(oc.Args) != 1 {
			continue
		}
		// Capture the entry-time *value*, not a reference. `old(p)` where p is a `T&` param must
		// snapshot the pointee NOW (at entry); storing the pointer would re-read the same address at
		// the return check and always equal the current value — and it would also type-mismatch the
		// auto-deref'd operand. Coerce to the pointee type so emitExpr loads through the ref here.
		expected := s.exprType(oc.Args[0])
		if rt, ok := expected.(*semantic.RefType); ok && rt != nil {
			expected = rt.Elem
		}
		val, typ, err := s.emitExpr(oc.Args[0], expected)
		if err != nil {
			return err
		}
		s.oldCaptures[oc] = oldCapture{val: val, typ: typ}
	}
	return nil
}

// collectOldCalls appends every `old(...)` pseudo-call reachable in expr to out (depth-first over the
// common expression shapes used in contracts).
func collectOldCalls(expr ast.Expr, out *[]*ast.CallExpr) {
	switch n := expr.(type) {
	case nil:
		return
	case *ast.CallExpr:
		if ast.IsOldCall(n) {
			*out = append(*out, n)
			return // don't descend into the captured expression
		}
		collectOldCalls(n.Func, out)
		for _, a := range n.Args {
			collectOldCalls(a, out)
		}
	case *ast.BinaryExpr:
		collectOldCalls(n.Left, out)
		collectOldCalls(n.Right, out)
	case *ast.UnaryExpr:
		collectOldCalls(n.Operand, out)
	case *ast.ParenExpr:
		collectOldCalls(n.Inner, out)
	case *ast.FieldExpr:
		collectOldCalls(n.Object, out)
	case *ast.IndexExpr:
		collectOldCalls(n.Object, out)
		collectOldCalls(n.Index, out)
	}
}

// emitPreconditionChecks emits a function's `requires` value-contracts at entry, in debug builds
// only (-O0, or forced via ELISACORE_FORCE_CONTRACTS). Zero cost in release: "debug verifies what
// release assumes." Params are already bound, so the conditions resolve normally.
func (s *functionState) emitPreconditionChecks(decl *ast.FuncDecl) error {
	if decl == nil || len(decl.Requires) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	for i, cond := range decl.Requires {
		if cond == nil {
			continue
		}
		if proofAt(decl.RequiresProofs, i) != nil {
			continue
		}
		if err := s.emitContractCheck(cond, "precondition failed"); err != nil {
			return err
		}
	}
	return nil
}

// emitPostconditionChecks emits a function's `ensure` value-contracts just before a return, in
// debug builds only. `result` is bound to the returned value so the conditions can reference it,
// and `old(...)` reads the entry-time snapshot captured by emitOldCaptures.
func (s *functionState) emitPostconditionChecks(value C.LLVMValueRef, actual semantic.Type) error {
	if s.decl == nil || len(s.decl.EnsureValues) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	// Bind `result` to the returned value (skip for void / error-union returns) so the ensure
	// conditions resolve it. The binding lives only for these checks; the block terminates on return.
	if s.fnType != nil && !isVoidType(s.fnType.Return) {
		if _, isUnion := s.fnType.Return.(*semantic.ErrorUnionType); !isUnion {
			alloca, err := s.createEntryAlloca("result", s.fnType.Return)
			if err != nil {
				return err
			}
			C.LLVMBuildStore(s.builder, value, alloca)
			s.defineBinding("result", valueBinding{ptr: alloca, typ: s.fnType.Return, mutable: false})
		}
	}
	for i, cond := range s.decl.EnsureValues {
		if cond == nil {
			continue
		}
		if proofAt(s.decl.EnsureProofs, i) != nil {
			continue
		}
		if err := s.emitContractCheck(cond, "postcondition failed"); err != nil {
			return err
		}
	}
	return nil
}

func proofAt(proofs []*ast.ProofBlockStmt, i int) *ast.ProofBlockStmt {
	if i < 0 || i >= len(proofs) {
		return nil
	}
	return proofs[i]
}

// emitRefinementPostconditionChecks emits the runtime half of `ensures <param> is Law` (docs/85
// brick 2 B): for each refinement postcondition, a debug-gated check `Law(param)` that panics if
// false. Called at every exit — explicit returns AND implicit fall-through — so a void function
// that omits `return` is still checked, keeping the caller's gained predicate fact sound. Elided in
// release ("debug verifies what release assumes"). The static prove/refute half runs in the
// analyzer (dischargeEnsuresRefinements); a statically-proven case still gets this (harmless,
// debug-only) check.
func (s *functionState) emitRefinementPostconditionChecks() error {
	if s.decl == nil || len(s.decl.Ensures) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	for _, clause := range s.decl.Ensures {
		if clause.Kind != ast.EnsuresKindRefinement || clause.RefinementLaw == "" || clause.Target.Root == "" {
			continue
		}
		// Subject first, then the parametric postcondition's constant args (`is Bounded[0, 500]`).
		args := []ast.Expr{&ast.Ident{Position: clause.Position, Name: clause.Target.Root}}
		args = append(args, clause.RefinementArgs...)
		call := &ast.CallExpr{
			Position: clause.Position,
			Func:     &ast.Ident{Position: clause.Position, Name: clause.RefinementLaw},
			Args:     args,
		}
		if err := s.emitContractCheck(call, "postcondition failed"); err != nil {
			return err
		}
	}
	return nil
}

// emitContractCheck lowers a single boolean contract: if the condition is false, panic with a
// backtrace at the contract's source location; otherwise fall through.
func (s *functionState) emitContractCheck(cond ast.Expr, label string) error {
	condVal, _, err := s.emitExpr(cond, nil)
	if err != nil {
		return err
	}
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("contract.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("contract.fail"))
	C.LLVMBuildCondBr(s.builder, condVal, okBB, failBB)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitPanicWithBacktrace(cond.Pos(), &ast.StringLit{Position: cond.Pos(), Value: label}); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}
