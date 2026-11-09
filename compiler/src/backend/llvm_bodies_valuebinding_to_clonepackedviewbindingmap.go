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
	"strings"
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
type comprehensionAliasContext struct {
	domainName       string
	sourceName       string
	destinationName  string
	sourceScope      string
	destinationScope string
}
type comprehensionAliasAccess struct {
	domainName   string
	aliasScope   string
	noAliasScope string
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
	// Consecutive-index check merging (llvm_index_check_merge.go): accesses whose bounds check a
	// dominating merged guard has discharged, and the nesting depth of merged emission.
	mergedIndexGuarded map[*ast.IndexExpr]bool
	indexMergeDepth    int
	g                  *llvmGenerator
	decl               *ast.FuncDecl
	fnValue            C.LLVMValueRef
	fnType             *semantic.FuncType
	builder            C.LLVMBuilderRef
	scope              *codegenScope
	diScope            C.LLVMMetadataRef
	traceNameGlobal    C.LLVMValueRef
	typeMap            map[string]semantic.Type
	// specializedExprTypes overlays the expression types recorded by the TEMPLATE
	// analysis with the ones a re-analysis produced for THIS instantiation. Empty for
	// a non-generic body. See semantic.Result.SpecializedExprTypes.
	specializedExprTypes map[ast.Expr]semantic.Type
	specializedFuncTypes map[*semantic.FuncType]*semantic.FuncType
	resultSlot           C.LLVMValueRef
	sretReturn           bool
	// A source-level `void main` retains its Elisa type but is emitted as the
	// process entry point with an i32 return. Every void return path must therefore
	// return an explicit zero rather than exposing an arbitrary register value.
	mainReturnsStatus bool
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
	// elementDerivedPtrs: addresses inside a darray/view element buffer whose plain-struct type is not
	// alias-safe as a whole; only their scalar leaf fields get the elt tag (propagateAliasSafeFieldPtr).
	elementDerivedPtrs map[C.LLVMValueRef]bool
	// A compiler-generated fresh-result comprehension can prove that its source and
	// destination darrays are distinct. Keep that fact scoped to the loop; self-extend
	// and user-written loops remain conservative.
	comprehensionAliasContext  *comprehensionAliasContext
	comprehensionElementScopes map[C.LLVMValueRef]comprehensionAliasAccess
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
	// earlyFreeByOffset frees own-stack arenas early (Phase B2): the byte offset of a top-level
	// statement -> the stack arenas to free right after it (their objects died and are not
	// aliased), in stack-id order. Several stacks can die after the same statement. Populated at
	// region entry, fired once and removed when the statement is emitted.
	earlyFreeByOffset map[int][]C.LLVMValueRef
	packedStores      map[string]packedStoreBinding
	// regionPackedStores caches the IMPLICIT region-backed packed store per (region, enum root)
	// (getOrCreateRegionPackedStore). Unlike packedStores it is NOT scope-cloned/restored:
	// the creation instructions are hoisted to the owning region's anchor (they dominate the
	// region's whole extent), and the region half of the key makes an entry valid exactly
	// while that region is the active owner — so a scope exit between two uses must not drop
	// the binding (that re-created an empty store and broke previously-issued handles), and a
	// nested region's store never evicts the outer region's.
	regionPackedStores map[regionPackedStoreKey]packedStoreBinding
	// scopedArenaOwnerAlias maps the `as OWNER` name of a scoped region to the region's own
	// name. `with arena scratch(n) as owner:` lowers to `in scratch:` plus a synthesized
	// `owner: Arena& = &scratch`, so `in owner:` and `in scratch:` denote ONE arena — but the
	// tree-alloc owner key for `owner` is the alloca holding the REFERENCE, while `scratch`
	// keys on the Arena alloca itself. Two keys for one arena meant a store registered under
	// one was invisible under the other. Resolving the alias back to the region name keeps a
	// single key per arena.
	scopedArenaOwnerAlias map[string]string
	treeAllocOwner        treeAllocOwnerBinding
	// regionPolyOwner is the region threaded into a region-polymorphic function via the hidden
	// `__region_auto` Arena& param (docs/75). The function's synthesized `__auto_*` region adopts it
	// rather than creating a fresh, locally-freed arena, so `new[auto]` allocates into the caller's
	// region and the returned handle outlives the call. Zero value (nil arenaRef) for ordinary fns.
	regionPolyOwner treeAllocOwnerBinding
	// scratchArena is an owned temporary carrier used while an adopted region-polymorphic function
	// evaluates a local initializer. Returned/caller-visible values stay in regionPolyOwner, while
	// a non-escaping local result (for example read_file's captured bytes) must not append a dead
	// allocation after the caller's reserve/fixed report buffer. It is registered as a function
	// cleanup, not as a region binding, because the adopted source region belongs to the caller.
	scratchArena C.LLVMValueRef
	// adoptedEscapeNames contains locals whose storage may be observed outside the current adopted
	// function (return values, copied call arguments, or assignment initializers). Such locals keep
	// the caller-owned arena; all other fresh locals may use scratchArena.
	adoptedEscapeNames map[string]bool
	adoptedEscapeBody  []ast.Stmt
	adoptedEscapeReady bool
	// callerStorageArenas maps a local result name to the caller-owned arena of the aggregate it is
	// copied into. This is distinct from regionPolyOwner: a void helper can return a storage-carrying
	// aggregate into a local auto region and immediately push it into a by-reference output parameter.
	// In that case the result must be built in the output parameter's arena, or the auto region can be
	// freed while the output still contains the nested backing.
	callerStorageArenas map[string]C.LLVMValueRef
	// activeInitializerName is set only while emitting a local initializer. It lets the implicit
	// region argument for a storage-carrying call follow callerStorageArenas precisely, without
	// routing unrelated temporary results to an output arena.
	activeInitializerName string
	// activeSynthesizedAutoRegion is true while lowering the body of a synthesized function-level
	// `__auto_*` region. Region-polymorphic calls made in that body must receive the current scoped
	// arena for their hidden region argument; otherwise a temporary result is allocated in the caller
	// arena even though the enclosing function has a private auto region.
	activeSynthesizedAutoRegion bool
	// activeInitializerCarriesRegionStorage is true only while emitting a local initializer whose
	// result can retain region-backed storage. It prevents a region-polymorphic call used solely for
	// a scalar side effect from being redirected to scratch just because its enclosing function has
	// an adopted or inferred auto region.
	activeInitializerCarriesRegionStorage bool
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
		if sym, ok := s.g.symbolsByNode[s.g.result.CanonicalCodegenFunction(s.decl)]; ok && sym != nil {
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
	var aliases map[string]string
	if s.g != nil && s.g.result != nil {
		aliases = s.g.result.ModuleAliases
	}
	return llvmVisibleGlobalNamesWithAliases(name, s.currentNamespace(), aliases)
}

func llvmVisibleGlobalNamesWithAliases(name, namespace string, aliases map[string]string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	// `::` is the surface module separator; symbols are registered joined with `.`.
	qualified := strings.ReplaceAll(name, "::", ".")
	candidates := make([]string, 0, 3)
	// Keep the analyzer's precedence: lexical namespace, canonical alias target,
	// then the explicitly written path.
	for lexical := namespace; lexical != ""; {
		candidates = append(candidates, lexical+"."+qualified)
		if dot := strings.LastIndex(lexical, "."); dot >= 0 {
			lexical = lexical[:dot]
		} else {
			lexical = ""
		}
	}
	if dot := strings.Index(qualified, "."); dot > 0 {
		if target, ok := semantic.ScopedModuleAliasTarget(aliases, namespace, qualified[:dot]); ok {
			candidates = append(candidates, target+qualified[dot:])
		}
	}
	return append(candidates, qualified)
}

// lookupVisibleNamedType resolves a (possibly unqualified) type name against the
// analyzer's NamedTypes the same way lookupVisibleGlobalSymbol resolves values:
// current-namespace candidates first, then a unique-suffix fallback so a `using`
// (which the backend does not track) still resolves when unambiguous.
func (s *functionState) lookupVisibleNamedType(name string) (semantic.Type, string, bool) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.hasNamedTypes() {
		return nil, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if t, ok := s.g.lookupNamedType(candidate); ok && t != nil {
			return t, candidate, true
		}
	}
	if !strings.Contains(name, ".") && !strings.Contains(name, "::") {
		if matches := s.g.namedTypeSuffixIndex()[strings.TrimSpace(name)]; len(matches) == 1 {
			return s.g.namedType(matches[0]), matches[0], true
		}
	}
	return nil, "", false
}

func (s *functionState) lookupVisibleGlobalSymbol(name string) (*semantic.Symbol, string, bool) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.hasGlobalScope() {
		return nil, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if sym, ok := s.g.lookupGlobal(candidate); ok && sym != nil {
			return sym, candidate, true
		}
	}
	if !strings.Contains(name, ".") && !strings.Contains(name, "::") {
		if matches := s.g.globalSuffixIndex()[strings.TrimSpace(name)]; len(matches) == 1 {
			return s.g.globalSymbolsMap("unique-suffix-result")[matches[0]], matches[0], true
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
	// A nursery joins its workers before cleanup of any value they may have captured.
	scopedCleanupTaskGroupWait
	scopedCleanupDeferBody
	scopedCleanupValue
	// scopedCleanupRegion frees a scoped region's arena at block exit (every exit path), so a
	// `region`/`in auto:` scope reclaims at scope exit instead of leaking until function return.
	scopedCleanupRegion
	// scopedCleanupRegionReset resets (not frees) a loop-entered lazy region at block exit, so its
	// blocks are reused next iteration (no mmap/munmap churn); function-return arena_free releases.
	scopedCleanupRegionReset
	// scopedCleanupDrop runs a drop type's `__drop__` destructor at block exit (docs/126 D1),
	// guarded on the value's live flag so a moved-away value is not dropped twice.
	scopedCleanupDrop
)

type scopedCleanupBinding struct {
	kind      scopedCleanupKind
	name      string
	ptr       C.LLVMValueRef
	typ       semantic.Type
	owner     *codegenScope
	deferBody *deferredBodyBinding
	// flagPtr is the i1 "still owns its value" slot for a scopedCleanupDrop binding:
	// set at the declaration, cleared by any move, tested before the destructor runs.
	flagPtr C.LLVMValueRef
	// dropHook is the mangled `__drop__` symbol to call for a scopedCleanupDrop binding.
	dropHook string
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
