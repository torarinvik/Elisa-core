package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// analyzerFunctionRegionState holds function-local region, alias, and container provenance.
type analyzerFunctionRegionState struct {
	currentRegions         map[*Symbol]regionState
	currentRegionsBorrowed bool
	// regionLifetimeOrdinals maps a local region name to a monotonic ordinal
	// assigned at its `region NAME(...)` declaration. Because regions nest
	// lexically (LIFO destruction), a LOWER ordinal means a longer-lived region:
	// an outer region is declared before — and freed after — an inner one. This
	// is the region outlives-lattice for local regions; region params are
	// caller-owned and outlive every local (ordinal 0), and 'heap/borrowed arenas
	// are treated as outermost. Used to reject storing an inner-@r value into an
	// outer-region slot (a dangling pointer once the inner region is freed).
	regionLifetimeOrdinals map[string]int
	regionLifetimeCounter  int
	// regionLifetimeAncestors maps a local region name to the names of the
	// regions that were lexically OPEN (live enclosing region blocks) at the
	// point it was declared — i.e. its true nesting ancestors. The outlives-
	// lattice consults this so only genuinely nested regions are comparable;
	// disjoint sibling regions, absent from each other's ancestor set, are
	// incomparable (neither outlives the other). regionLiveStack is the stack of
	// currently-open region names used to populate it; it is saved/restored
	// across a scoped `region NAME(...):` block exactly like currentRegions.
	regionLifetimeAncestors map[string][]string
	regionLiveStack         []string
	currentRegionMarks      map[*Symbol]regionMarkState
	currentCheckpoints      map[*Symbol]checkpointState
	currentRegionRefs       map[*Symbol]regionRefState
	// A scoped block borrows its caller map until the first binding write.
	currentRegionRefsBorrowed bool
	// currentStructInteriorRegionTaint records, per region-less aggregate local,
	// the innermost (shortest-lived) region name that one of its interior
	// container/view fields was stored a value from. A struct's TYPE never carries
	// its fields' regions, so copying such a struct out to a longer-lived binding
	// would otherwise launder a dangling interior reference past the region's death
	// undetected. Consumed only by checkStructCopyInteriorRegionEscape; kept off the
	// shared region-ref provenance map to avoid perturbing promote/packed/invalidation.
	currentStructInteriorRegionTaint map[*Symbol]string
	// currentStructLocalAllocRegion records, per locally-constructed aggregate local
	// whose container/view fields were allocated under an ambient region, the name of
	// that region. Unlike currentStructInteriorRegionTaint (which tracks the shortest
	// region a value was STORED FROM, for copy-escape), this records the region the
	// struct's own fresh fields LIVE IN at construction. It lets a call site bind a
	// callee's struct-ref region param (`def f(b: mutable Bag& @r)`) when the argument
	// is such a local — the locally-constructed analogue of threading a struct param's
	// region. Consumed by attachStructLocalArgRegion at call sites.
	currentStructLocalAllocRegion map[*Symbol]string
	// Concrete declared return-region bindings belong to each call, not the shared callee type.
	declaredCallReturnRegions map[*ast.CallExpr]string
	// currentElementReads / currentElementStates track the element provenance of eligible
	// function-local containers (see region_local_container_elements.go).
	currentElementReads  map[*ast.IndexExpr]bool
	currentElementStates map[*Symbol]regionRefState
	// currentElementExtendSources are the statement `ys.extend(xs)` calls whose local source xs
	// is not grown again in any loop enclosing the call, so xs's tracked element state at the
	// call is the exact provenance the copied elements carry.
	currentElementExtendSources map[ast.Expr]bool
	// storeFlowBinderIndexes caches each function's binding sites for the store-flow summaries.
	storeFlowBinderIndexes map[*ast.FuncDecl]*storeFlowBinderIndex
	// storeFlowStructLookup is scoped to one synchronous summary computation.
	storeFlowStructLookup         map[string]Type
	storeFlowStructLookupComplete bool
	// Parallel body workers use a frozen named-type table, so their complete
	// unqualified struct index can be reused across summary computations.
	storeFlowFrozenStructLookup map[string]Type
	// currentElementBinderLoops are the by-value `for x in xs` loops over an eligible local
	// container whose binder may take the container's tracked element provenance;
	// iterBinderElementStates holds that provenance per binder symbol.
	currentElementBinderLoops map[*ast.IterForStmt]bool
	// currentElementLoop holds the in-loop reads of containers the same loop also pushes into,
	// with the assumptions those reads made (region_local_container_loop_reads.go).
	currentElementLoop *elementLoopInfo
	// ambientFillAnalyzed / ambientFillFresh summarize each function for callers that pass it a
	// local container to fill: analyzed once its body was checked with diagnostics on, fresh when
	// that body may put data allocated in a caller's arena into a parameter container (a void
	// grower's adoption sanction fired or it called a region-polymorphic function, or a parameter
	// was forwarded to a callee not known to fill it only from its arguments).
	ambientFillAnalyzed map[*ast.FuncDecl]bool
	ambientFillFresh    map[*ast.FuncDecl]bool
	// ambientFillFreshParams narrows freshness to the parameters (by index) that a function
	// forwarded to a callee not known to fill them only from its arguments: only those
	// parameter containers may receive caller-arena data through that call.
	ambientFillFreshParams map[*ast.FuncDecl]map[int]bool
	// fillMayAdoptSet is the lazily built syntactic over-approximation of ambientFillFresh
	// (see fillMayAdopt).
	fillMayAdoptSet map[*ast.FuncDecl]bool
	// funcAnalysisActive holds the functions whose body analysis is on the stack;
	// provisionalSummaryFn is the function a quiet on-demand analysis is publishing summaries for.
	funcAnalysisActive   map[*ast.FuncDecl]bool
	provisionalSummaryFn *ast.FuncDecl
	// summary convergence state (region_summary_convergence.go)
	convergeDepth       int
	storeFlowSummaries  map[*ast.FuncDecl]*storeFlowSummary
	storeFlowInProgress map[*ast.FuncDecl]*storeFlowSummary
	storeFlowActive     map[*ast.FuncDecl]bool
	// retainedProofsScoped: a retained update re-proves task-group submits only in bodies whose
	// summaries it already dropped, so a fresh proof there must not drop the rest.
	retainedProofsScoped bool
	storeFlowCurrentDecl *ast.FuncDecl // declaration context for type and binder resolution
	// storeFlowSCC caches each function's call-graph component (region_store_flow_summary.go).
	storeFlowSCC                map[*ast.FuncDecl][]*ast.FuncDecl
	storeFlowBinds              bool
	storeFlowParams             map[string]bool
	storeFlowDarrays            map[string]bool
	storeFlowViews              map[string]bool
	storeFlowLocals             map[string]bool
	storeFlowNamedTypes         map[string]string
	storeFlowEnv                map[string]string
	storeFlowStmtEnv            map[uintptr]map[string]string
	declaredFuncNames           map[string]bool
	convMembers                 map[*ast.FuncDecl]bool
	cyclicFuncSet               map[*ast.FuncDecl]bool
	callEdgeCache               map[*ast.FuncDecl][]*ast.FuncDecl
	callDeclsByName             map[string][]*ast.FuncDecl
	storageViewWriteDeclsByName map[string][]*ast.FuncDecl
	// returnElementStmtStates / returnElementStmtUnknown record, per return statement, the element
	// provenance of the returned container (region_return_element_summary.go);
	// returnElementSummaries holds each function's published summary.
	returnElementStmtStates  map[*ast.ReturnStmt]regionRefState
	returnElementStmtUnknown map[*ast.ReturnStmt]bool
	returnElementSummaries   map[*ast.FuncDecl]regionRefState
	iterBinderElementStates  map[*Symbol]regionRefState

	iterBinderElementOverlay *analyzerIterBinderElementOverlay

	// comprehensionElementStates holds the provenance of each list comprehension's element
	// expression, read in the binder scope, for a local container it initializes.
	comprehensionElementStates   map[*ast.ListComprehensionExpr]regionRefState
	currentAffineValues          map[affineValueKey]affineValueState
	currentBorrowedOwnerRefs     map[*Symbol]borrowedOwnerRefState
	currentFunctionValues        map[*Symbol]*FuncType
	currentSpecializedValueTypes map[*Symbol]Type
	currentValueBindings         map[*Symbol]ast.Expr
	currentStorageViewDeps       map[*Symbol]storageViewDependencyState
	patternStorageFallback       ast.Expr
	// pendingStorageViewErrors holds invalidated-view uses deferred until the per-function region
	// stack assignment is known (Phase C1b): a use whose source darray got a reserve_commit stack
	// is stable and the error is dropped; otherwise it is emitted. Scoped per function.
	pendingStorageViewErrors []pendingStorageViewError
	// currentIteratedSources keys (by optimizationExprString) the relocatable containers
	// currently being iterated by an enclosing `for` loop, to the loop position. A
	// relocating mutation (push/extend/reserve/clear/truncate) of such a container would
	// move its buffer out from under the live iteration — the iterator-invalidation gap —
	// so it is rejected at invalidateStorageViewsForSource, the universal mutation chokepoint.
	currentIteratedSources map[string]lexer.Pos
	currentAliasAccesses   map[string]aliasAccessState
	currentAliasBindings   map[*Symbol]aliasAccessBinding
	// callAlignedAliasArgs records, per call, the argument expressions aligned 1:1 to the
	// callee's parameters (receiver + reordered named args + implicits) — the same alignment
	// validateCallArgAliasAccess uses. It lets a ref bound from a reference-returning call map
	// the callee's return-isolation AliasParamIndices back to the borrowed argument, for method
	// and free-function calls alike.
	callAlignedAliasArgs map[*ast.CallExpr][]ast.Expr
	// currentAliasCarriers tracks struct/value locals that carry a reference into a parameter,
	// bound from a reference-returning call (`r = wrap(v)` where wrap's result aliases v). A later
	// reference-typed field access `r.p` is then known to alias v, closing the laundering gap for
	// mutable wrappers (which, unlike immutable ones, get no value binding to resolve through). It
	// follows the same scope save/restore lifecycle as currentAliasBindings. Re-bound on every
	// whole-local assignment; reference-field assignments use currentAliasCarrierFieldOverrides to
	// replace only the assigned field's roots without dropping sibling carried fields.
	// Keyed by local NAME (not *Symbol): a declaration and a later assignment to the same local
	// can resolve to distinct Symbol instances, but the name is stable, and block-scope cloning of
	// this map already isolates shadowed names.
	currentAliasCarriers              map[string][]string
	currentAliasCarrierFieldOverrides map[string]map[string][]string
	currentPackedVariantViews         map[*Symbol]*PackedVariantViewType
	currentPackedStores               map[string]*PackedEnumStoreType
	currentPackedStoreResolutions     map[*Symbol]packedStoreResolution
	currentRewriteDefault             *rewriteDefaultContext
	currentSequenceRewrite            *sequenceRewriteContext
	currentAllocExpr                  ast.Expr
	// localArenaEscapeLocals tracks local collection variables whose backing
	// buffer was grown while a function-local Arena value was the active
	// allocation owner. Such a buffer is freed when the local arena goes out of
	// scope, so returning the collection (or a pointer into it) or storing it
	// into a longer-lived location is a use-after-free. Keyed by the unique
	// local *Symbol, so no per-function reset is required (symbols never alias
	// across functions); the value is the offending arena's name for diagnostics.
	localArenaEscapeLocals map[*Symbol]string
}
