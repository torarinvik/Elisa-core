package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/grammar"
	"elisacore/src/lexer"
)

type ConstValueKind int

const (
	ConstUnknown ConstValueKind = iota
	ConstInt
	ConstFloat
	ConstBool
	ConstString
	ConstTuple
	ConstList
	ConstRecord
	ConstOptional
)

const (
	semanticTraversalDepthLimit    = 256
	semanticSubstitutionDepthLimit = 512
	semanticCloneDepthLimit        = 512
)

type ConstValue struct {
	Kind   ConstValueKind
	Int    int64
	Float  float64
	Bool   bool
	String string
	Elems  []ConstValue
	Fields map[string]ConstValue
	Some   bool
	Value  *ConstValue
}

type ShapeTransformSpec struct {
	FreshReturnShapeParams []string
}

type externLinkNameSignature struct {
	SymbolName string
	Signature  string
	Pos        lexer.Pos
}

var shapeTransformTable = map[string]ShapeTransformSpec{
	"resize":                    {FreshReturnShapeParams: []string{"shape_out"}},
	"push":                      {FreshReturnShapeParams: []string{"shape_out"}},
	"append_many":               {FreshReturnShapeParams: []string{"shape_out"}},
	"truncate":                  {FreshReturnShapeParams: []string{"shape_out"}},
	"clear":                     {FreshReturnShapeParams: []string{"shape_out"}},
	"concat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"strcat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"arena_da_append":           {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_append_many":      {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_concat2":                {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_concat2_scratch":        {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_int_to_string":          {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_int_to_string_scratch":  {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_slice":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_from_view":      {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_from_view":        {FreshReturnShapeParams: []string{"shape_out"}},
}

type freshReturnStatus int

const (
	freshReturnUnknown freshReturnStatus = iota
	freshReturnAlways
	freshReturnNotFresh
)

type Analyzer struct {
	file        *ast.File
	diagnostics []Diagnostic
	namedTypes  map[string]Type
	// aliasRefinements keeps a type alias's REFINEMENT target expr (`type TileX = i32 is Bounded[..]`)
	// keyed by qualified name. namedTypes erases the refinement to the base, so this is the only
	// channel by which the tier-2 prover can recover a refinement-typed param's entry bound (docs/86).
	aliasRefinements map[string]*ast.RefinementTypeExpr
	// deferredAliasRefinements holds alias refinements whose predicate validation is postponed until
	// after law symbols are collected (aliases are resolved long before functions/laws exist).
	deferredAliasRefinements []deferredAliasRefinement
	// finalizingRefinements is true during the post-law validation pass: a refinement predicate whose
	// law is still missing is then a hard error. During EAGER resolution (e.g. a struct field type
	// resolved before laws are registered) a missing law instead DEFERS to that pass — laws can have
	// struct subjects and structs refined fields, so the two cannot be ordered, only re-checked later.
	finalizingRefinements bool
	staticInterfaces         map[string]*StaticInterface
	staticImpls              map[string]*StaticImpl
	// regionPolyFn is the function under examination by the region-polymorphism
	// classification pre-pass; it supplies the generic-param protocol bounds for
	// resolving `B.method(...)` callees. Nil outside the pre-pass.
	regionPolyFn            *ast.FuncDecl
	extensionMethodsByName  map[string][]*ExtensionMethod
	ufcsFunctionsByName     map[string][]*Symbol
	permissions             map[string]*PermissionSet
	capabilityAliases       map[string][]ast.PermissionRef
	globalScope             *Scope
	functionTypes           map[string]*FuncType
	externLinkNames         map[string]externLinkNameSignature
	constValues             map[string]ConstValue
	exprTypes               map[ast.Expr]Type
	permGrowthOps           map[ast.Expr]bool
	rewriteDefaults         map[*ast.Ident]bool
	optionalBindSourceTypes map[*ast.OptionalBindExpr]Type
	interfaceMethodRefs     map[*ast.FieldExpr]*InterfaceMethodRef
	safeCalls               map[*ast.CallExpr]*SafeCallInfo
	exprFacts               map[ast.Expr]OptimizationFacts
	indexBoundsProven       map[*ast.IndexExpr]bool
	// getWrappedIndexExprs records index expressions whose `else` recovery is
	// owned by an enclosing `get` (e.g. `get arr[i] else 0`). Such sites already
	// use the explicit `get` head, so they must NOT receive the bare-`else`
	// deprecation warning.
	getWrappedIndexExprs map[*ast.IndexExpr]bool
	// currentViewStaticLen records the statically-known element count of a
	// view/darray-view binding produced by a constant-bounded slice
	// (e.g. `s = arr[0:15]` -> 15). A constant index `s[k]` with 0 <= k < len is
	// then provably in bounds with zero runtime cost. Keyed by binding name, it
	// follows the same block/loop save-restore and reassignment-invalidation
	// discipline as currentIndexBounds.
	currentViewStaticLen map[string]int64
	// currentViewMutable records whether a slice-derived view binding permits
	// writes (its source is writable and not frozen). Writing through a view of
	// an immutable source is rejected. Keyed by binding name, scoped like
	// currentViewStaticLen.
	currentViewMutable map[string]bool
	// checkedSliceExprs records slice expressions whose bounds are checked at
	// runtime because they are the operand of `get` / `if let` (the safe
	// bounded-view forms). Codegen emits a bounds test at slice creation for
	// these, taking the recovery / else path when out of range.
	checkedSliceExprs map[*ast.SliceExpr]bool
	// callFrameContexts caches, per resolved call expression, the callee's applied FuncType and the
	// position-aligned argument expressions, so the deferred SMT-fact invalidation can consult the
	// callee's `changes`/`preserves` frame and let provably-untouched facts survive the call (docs/87
	// frame-aware fact survival). Populated in analyzeResolvedCallExprWithExpected, consumed (and
	// cleared) by invalidateSMTAssertFactsForCall.
	callFrameContexts            map[*ast.CallExpr]callFrameCtx
	storageViewStaleUses         map[ast.Expr]storageViewDependencyState
	unsafeAliasExprs             map[ast.Expr]bool
	unsafeAliasStmts             map[ast.Stmt]bool
	progressSummaries            map[*ast.FuncDecl]*FunctionProgressSummary
	numericLiteralSuffixWarnings map[ast.Expr]bool
	ambiguousDArrayBuilders      map[*ast.VarDeclStmt]bool
	treeConstructorCallees       map[ast.Expr]bool
	resolvedCastHooks            map[ast.Expr]*Symbol
	unsafeLifetimeWidenCasts     map[*ast.CastExpr]bool
	unsafeBufferReinterpretCasts map[*ast.CastExpr]bool
	sentinelFuncNameCache        map[string]bool
	loweredInitCalls             map[*ast.StructLitExpr]*ast.CallExpr
	postfixShorthandCalls        map[*ast.CastExpr]*ast.CallExpr
	regionStacks                 map[*ast.RegionStmt]RegionStackAssignment
	// deathTimeCohorts holds the docs/91 G0 inferred death cohorts per function, recorded when
	// ELISA_DUMP_DEATHTIME is set or RecordDeathTimeCohorts is requested (read-only observability;
	// surfaced on Result).
	deathTimeCohorts         map[string][]DeathTimeCohort
	deathEscapeStats         map[string]DeathTimeEscapeStats
	paramRetained            map[*ast.FuncDecl][]bool
	paramStoreTargets        map[*ast.FuncDecl][]map[int]bool
	recordDeathCohortsOpt    bool
	exprDenseNodeKeys        map[ast.Expr]DenseNodeKeyInfo
	exprNodeTables           map[ast.Expr]NodeTableInfo
	deferInfo                map[*ast.DeferStmt]*DeferInfo
	foldInfo                 map[*ast.FoldExpr]*FoldInfo
	lambdaInfo               map[*ast.LambdaExpr]*LambdaInfo
	symbolFacts              map[*Symbol]OptimizationFacts
	funcDeclSymbols          map[*ast.FuncDecl]*Symbol
	declVisibility           map[ast.Decl]string
	privateTypeNames         map[string]bool
	castHooksByName          map[string]map[castHookSignature]*Symbol
	initHooksByName          map[string]map[initHookSignature]*Symbol
	typeParamScopes          []map[string]Type
	typeParamInterfaceScopes []map[string]*StaticInterface
	interfaceAssocTypeScopes []map[string]Type
	constParamScopes         []map[string]Type
	constEvalScopes          []map[string]ConstValue
	staticContextDepth       int
	staticCallDepth          int
	shapeParamScopes         []map[string]Shape
	regionParamScopes        []map[string]bool
	permissionParamScopes    []map[string]bool
	errorSetParamScopes      []map[string]bool
	freshShapeCounter        int
	returnFreshShapeStatus   map[string]freshReturnStatus
	annotatedFuncs           []*AnnotatedFunc
	exportedTypes            []*ExportedType
	exportedFuncs            []*ExportedFunc
	exportedGlobals          []*ExportedGlobal
	currentScope             *Scope
	currentReturn            Type
	// lambdaErrorAccumulate is true while analyzing the body of a bare expr-lambda with no annotated
	// or contextual error-union return (docs/64 Phase 5b). In that mode `try`-without-else and `raise`
	// UNION their operand's error set into lambdaErrorAccum instead of checking it against
	// currentReturn (which is not yet an error union), so the lambda's error return is inferred from
	// what its body actually propagates. An empty accumulator after the body means the lambda is
	// infallible. Saved/restored around each lambda so nested lambdas don't cross-contaminate.
	lambdaErrorAccumulate bool
	lambdaErrorAccum      *ErrorSetType
	// constEvalExpectedType is the declared type of the const currently being evaluated (set around
	// evalConstExpr for a ConstDecl). It lets a suffixless literal exceeding i64 max be accepted when
	// the const's type is u64/usize/uintptr — the declared type standing in for the `u64` suffix. nil
	// elsewhere, where const-eval keeps its signed-default behavior.
	constEvalExpectedType Type
	// inEnsureContext is true while analyzing `ensure` postcondition expressions, enabling the
	// `old(expr)` pseudo-call (the value of expr at function entry).
	inEnsureContext bool
	currentFuncDecl *ast.FuncDecl
	currentFuncType *FuncType
	// currentChangesPaths / currentPreservesPaths and their Has flags hold the resolved frame
	// conditions (docs/87) of the function being analyzed, so the mutation sites can enforce that
	// every caller-visible write lands in the declared `changes` set and never touches a `preserves`
	// place.
	currentChangesPaths   []framePath
	currentPreservesPaths []framePath
	currentHasChanges     bool
	currentHasPreserves   bool
	// currentFuncSawPlainValueReturn records whether the current function has a
	// value-returning path that is NOT a `return move <region>`. Combined with
	// FuncType.ReturnsOwnedRegion it rejects functions that transfer an owned
	// region on some paths but not others (which would make the caller's
	// must-consume obligation unsound).
	currentFuncSawPlainValueReturn bool
	currentRegions                 map[*Symbol]regionState
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
	currentRegionMarks     map[*Symbol]regionMarkState
	currentCheckpoints     map[*Symbol]checkpointState
	currentRegionRefs      map[*Symbol]regionRefState
	// currentStructInteriorRegionTaint records, per region-less aggregate local,
	// the innermost (shortest-lived) region name that one of its interior
	// container/view fields was stored a value from. A struct's TYPE never carries
	// its fields' regions, so copying such a struct out to a longer-lived binding
	// would otherwise launder a dangling interior reference past the region's death
	// undetected. Consumed only by checkStructCopyInteriorRegionEscape; kept off the
	// shared region-ref provenance map to avoid perturbing promote/packed/invalidation.
	currentStructInteriorRegionTaint map[*Symbol]string
	currentAffineValues              map[affineValueKey]affineValueState
	currentBorrowedOwnerRefs         map[*Symbol]borrowedOwnerRefState
	currentFunctionValues            map[*Symbol]*FuncType
	currentSpecializedValueTypes     map[*Symbol]Type
	currentValueBindings             map[*Symbol]ast.Expr
	currentStorageViewDeps           map[*Symbol]storageViewDependencyState
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
	// suppressUninitReadCheck > 0 while analyzing an address-of operand or an
	// assignment target, where naming a `zeroed`-uninitialized local is not a
	// value read (it is being filled / had its address taken), so the
	// definite-assignment read check must not fire.
	suppressUninitReadCheck int
	suppressGlobalReadCheck int
	currentPoolScopes       []poolScopeState
	currentIndexBounds      map[string]indexBoundFact
	// currentBoundEqual is a flow-sensitive equivalence relation over upper-bound EXPRESSION STRINGS
	// (the same canonical form as indexBoundFact.Upper / indexableUpperBoundString): a symmetric
	// adjacency set recording that two length-ish expressions are provably equal — `n == xs.count`
	// from a guard, or `n = xs.count` from an immutable binding. It lets a loop index whose bound is
	// `n` discharge an `xs[i]` access whose bound is `xs.count` (docs/86 brick 86-6, the cross-variable
	// coupling §8 flagged). It rides EXACTLY the same clone/save/restore/invalidate sites as
	// currentIndexBounds, so it can never outlive a branch or survive a mutation that the index facts
	// themselves wouldn't (soundness).
	currentBoundEqual                 map[string]map[string]bool
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	// currentFunctionGuardedIndexes collects the positions of index accesses in the current function
	// that would emit a runtime bounds check (bounds-requiring type, not statically proven). It backs
	// the `NoBoundsChecks` shape law (docs/89): a function `fulfills NoBoundsChecks` is violated iff
	// this list is non-empty. Saved/restored per function like currentFunctionUsedPermissionRefs.
	currentFunctionGuardedIndexes []lexer.Pos
	// currentFunctionExpectsVectorize is set while analyzing a function that `fulfills Vectorizes` (a
	// measure law, docs/89 Stage 5). It opts the function's range loops into the existing autovec
	// verifier: each ForStmt is tagged AutovecExpected, so the post-optimization pass warns (verify-
	// only, never build-gating) if the loop did not vectorize. Saved/restored per function.
	currentFunctionExpectsVectorize   bool
	currentProgressSummary            *FunctionProgressSummary
	loopDepth                         int
	currentTrustedNonProgressDepth    int
	currentTrustedAssumeProgressDepth int
	currentTrustedStaleRefDepth       int
	// currentGrantedStaleRefDepth tracks an enclosing *tracked* `can Unsafe.StaleRef:`
	// grant (vs the untracked `trusted` suppression in currentTrustedStaleRefDepth).
	// A forwarded-ref store inside it is allowed AND surfaces Unsafe.StaleRef in the
	// function's effect signature (recorded by analyzeCanStmt), so the unsafe store is
	// auditable through the capability system rather than invisibly suppressed.
	currentGrantedStaleRefDepth       int
	currentReturnProvenance           regionRefState
	currentReturnBorrowedOwnerRefs    borrowedOwnerRefSummary
	currentConservativeCallWidenings  map[*Symbol][]conservativeCallWidening
	currentRegionFactTransforms       []FactTransform
	conditionalCallPoststateOriginals map[*ast.CallExpr]map[*Symbol]Type
	suppressDiagnostics               bool
	// warnOnceSeen dedupes warnings emitted from multi-visit paths (warnOncef).
	warnOnceSeen map[string]bool
	// deprecatedSeen dedupes deprecation diagnostics emitted for the same site+message
	// (a call may be analyzed more than once across passes).
	deprecatedSeen map[string]bool
	// Set while analyzing a query/comprehension predicate whose source const-folds (e.g.
	// `count field in fields(T) where field.name != "x"`). Such a predicate is evaluated at
	// compile time over interned literals, so a raw-u8&-`==`/`!=` there is not a runtime
	// address-compare footgun and must not be flagged by the C-string comparison lint.
	inCompileTimeQueryPredicate bool
	enforceUnsafePermissions    bool
	enforceProgressSafety       bool
	enforceStrictConcurrency    bool
	enforcePerfLints            bool
	enforceStrictProofs         bool
	requireExternContracts      bool
	// SMT discharge tier (docs/90). The solver is opened LAZILY on the first obligation that needs it
	// (so a compile with no hard obligations never spawns a process) and closed at the end of
	// analysis. smtUnavailable latches once Open fails, so we don't retry a missing solver per query.
	smtEnabled                       bool
	smtBinary                        string
	smtSolver                        smtSolverHandle
	smtUnavailable                   bool
	smtStats                         SMTStats
	// smtQueryCache memoizes solver verdicts keyed on the FULL query string (VC IR brick 5). z3 is
	// deterministic for a given query + timeout, so an identical query — same preamble, hypotheses, and
	// negated obligation — has the same verdict and counterexample; reusing it is sound and skips the
	// round-trip. Lazily created; lives for one analysis pass (a query embeds its function's facts, so a
	// cross-function key collision means a byte-identical proof).
	smtQueryCache map[string]smtQueryResult
	proofReport                      []ProofFact
	suppressOptimizationFacts        bool
	suppressLazyFuncSummaryInference bool
	returnProvenanceInProgress       map[*ast.FuncDecl]bool
	returnProvenanceLocalInProgress  map[*Symbol]bool
	returnBorrowedOwnerRefInProgress map[*ast.FuncDecl]bool
	// inReturnBorrowCapture is true only while computing a return statement's
	// borrowed-owner summary, enabling pass-through tracking of raw reference parameters
	// (so `return p` records "returns param i") without affecting any other analysis.
	inReturnBorrowCapture            bool
	returnBorrowedOwnerLocalProgress map[*Symbol]bool
	// functionValueResolveInProgress guards functionValueTypeForExpr against a self-referential /
	// cyclic value binding (e.g. `x = x`, where the symbol's value expr resolves back to itself):
	// re-entering a symbol already on the resolution stack returns "no function-value type" instead of
	// recursing until the goroutine stack overflows.
	functionValueResolveInProgress map[*Symbol]bool
	sinkParamInferenceInProgress   map[*ast.FuncDecl]bool
	parallelForInfo                  map[*ast.ParallelForStmt]*ParallelForInfo
	callArgDisjoint                  map[*ast.CallExpr]*CallArgDisjointInfo
	disjointCallSites                map[*ast.FuncDecl][]callDisjointObservation
	funcDisjointParams               map[*ast.FuncDecl]*FuncDisjointParamInfo
	lawIsCalls                       map[*ast.BinaryExpr]*ast.CallExpr
	lemmaCalls                       map[*ast.CallExpr]bool
	ghostDecls                       map[*ast.VarDeclStmt]bool
	ghostContracts                   map[ast.Expr]bool
	// ghostReadAllowed, when > 0, permits reading a `ghost` variable (the analyzer is inside a
	// contract clause or another ghost initializer). Outside these contexts a ghost read is a hard
	// error — that is the ghost-to-real flow barrier that keeps erasure sound.
	ghostReadAllowed int
	// ghostReadSeen records that a ghost variable was read since it was last reset — used to flag a
	// contract condition (invariant/assert) that mentions a ghost so codegen erases its check.
	ghostReadSeen bool
	lemmasInAnalysis                 map[*ast.FuncDecl]bool
	// lemmaTerminationCache memoizes whether a lemma's `decreases` measure is verified to strictly
	// decrease at every self-recursive call (read-only mirror of checkTermination, no diagnostics). It
	// gates the inductive hypothesis: only a provably-terminating lemma may assume its own ensure for a
	// strictly-smaller argument. A false entry only forgoes the IH; it never fabricates a proof.
	lemmaTerminationCache map[*ast.FuncDecl]bool
	// definingEquationCache / ...InProgress memoize whether a PURE, TOTAL function's defining equation
	// (`f(args) == body[params:=args]`) may be soundly asserted to the SMT tier. InProgress breaks
	// self/mutual-recursive purity checks. A false verdict only forgoes the equation; it is never unsound.
	definingEquationCache      map[*ast.FuncDecl]bool
	definingEquationInProgress map[*ast.FuncDecl]bool
	// lastSMTCounterexample holds the satisfying model (as a readable "x=5, n=0" string) from the most
	// recent FAILED refinement SMT proof, so the diagnostic that follows can show a concrete witness.
	// Cleared at the start of each refinement discharge to avoid surfacing a stale one.
	lastSMTCounterexample       string
	refinementChecks            map[*ast.VarDeclStmt][]*ast.CallExpr
	callArgRefinementChecks     map[*ast.CallExpr][]*ast.CallExpr
	returnRefinementChecks      map[*ast.ReturnStmt][]*ast.CallExpr
	hotDisjointKernelCandidates []hotDisjointKernelCandidate
	privateFreshDArrayCache     map[*ast.FuncDecl]map[string]bool
	reassignedParamCache        map[*ast.FuncDecl]map[string]bool
	functionAnalyses            map[*ast.FuncDecl]*FunctionAnalysis
	currentNamespace            string
	currentUsings               []string
	importAliases               map[string]string
	resolvedTypeNames           map[ast.TypeExpr]string
	resolvedValueNames          map[*ast.Ident]string
	currentImplicitScopes       []map[string]ast.Expr
	semanticLimitDiagnostics    map[string]bool
}

type castHookSignature struct {
	Source string
	Target string
}

type initHookSignature struct {
	Target string
	Params string
}

type regionState struct {
	Destroyed  bool
	Generation int
	Allocated  bool
	// Backing is the normalized backing strategy (chained / fixed / reserve_commit
	// / scratch; docs/68 §3). It governs adopt's backing-family check and the
	// pointer-stability rule.
	Backing string
	// DeclScope is the scope in which the region was declared. A binding whose
	// defining scope strictly encloses DeclScope outlives the region's block, so
	// storing region-allocated data into it dangles (see the region-less-target
	// case of checkNestedRegionStoreEscape).
	DeclScope *Scope
}

type regionMarkState struct {
	Region        *Symbol
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type checkpointKind int

const (
	checkpointKindDArray checkpointKind = iota
)

type checkpointState struct {
	Kind          checkpointKind
	Target        ast.Expr
	TargetType    Type
	Valid         bool
	InvalidatedBy string
}

type storageViewDependencyState struct {
	Source        string
	Valid         bool
	InvalidatedBy string
}

type regionDependencyState struct {
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type packedStoreDependencyState struct {
	Type *PackedEnumStoreType
}

type packedStoreResolution struct {
	Symbol *Symbol
	Type   *PackedEnumStoreType
}

type rewriteDefaultContext struct {
	Allowed    bool
	ExactType  Type
	ResultType Type
	Message    string
}

type sequenceRewriteContext struct {
	ElemType   Type
	OutputElem Type
}

type regionRefState struct {
	Deps                    map[*Symbol]regionDependencyState
	StoreDeps               map[*Symbol]packedStoreDependencyState
	DirectParamDep          int
	HasDirectParamDep       bool
	ParamDeps               IntBitSet
	Fields                  map[string]regionRefState
	PackedStoreSummary      PackedStoreProvenance
	PackedStoreSummaryKnown bool
	ParamOnlySummary        bool
}

type conservativeCallWidening struct {
	Path      []borrowReturnAnnotationStep
	Source    string
	SourcePos lexer.Pos
	Reason    string
	Before    string
	After     string
}

type borrowedOwnerRefState struct {
	HasDirect bool
	Direct    affineValueKey
	Fields    map[string]borrowedOwnerRefState
	// RawInteriorAffineAlias marks an entry that is NOT a borrow of a linear/owner
	// value, but a raw interior pointer copied out of an affine handle's field
	// (e.g. `b: heap T& = h.ptr` where `h` is an affine `Pooled[T]`). `Direct` then
	// names the affine handle whose consumption recycles the slot `b` points at, so
	// using `b` after that handle is consumed is a use-after-free. These entries are
	// merged with conservative UNION semantics (keep the taint if either branch has
	// it), unlike the intersection semantics used for genuine owner borrows.
	RawInteriorAffineAlias bool
}

type affineValueState struct {
	ConsumedBy              string
	LiveProtocolType        Type
	LiveProtocolDescription string
	// Uninitialized marks a local declared with `= zeroed` that has not yet been
	// assigned (definite-assignment tracking). Reading it before any assignment
	// to it (or a field of it) is a use of uninitialized memory. Folded into the
	// affine state so it inherits clone/merge/snapshot across control flow.
	Uninitialized bool
	// ScheduledForDefer marks a linear value whose must-consume obligation will
	// be discharged by a `defer function` body that consumes it. Such a value no
	// longer reports "must be consumed" at scope exit (the deferred body runs at
	// every function exit), may still be borrowed/read, but must NOT be consumed
	// again inline (that would double-consume the resource at runtime).
	ScheduledForDefer bool
}

type affineValueKey struct {
	Root *Symbol
	Path string
}

type poolScopeState struct {
	Name string
}

type AnalyzeOptions struct {
	EnforceUnsafePermissions bool
	EnforceProgressSafety    bool
	// EnforceStrictConcurrency promotes legacy raw-concurrency migration diagnostics
	// from deprecations to hard errors while the proof-carrying surface matures.
	EnforceStrictConcurrency bool
	// EnforcePerfLints promotes the performance-friction lints (pointer-graph, allocation
	// churn) from warnings to hard errors — the `-Wperf` graduated-strictness level for
	// shipped code (docs/70). Off by default so prototyping stays fluid.
	EnforcePerfLints bool
	// EnforceStrictProofs promotes the refinement-proof diagnostic from a warning to a hard error
	// (docs/85: the `-strict` safety channel). When a refinement obligation cannot be discharged
	// statically it falls back to a runtime check; by default that emits a WARNING so the user
	// always KNOWS a static guarantee was not achieved, and under -strict it is an error
	// (prove-it-or-fail, the Dafny-like mode). Off by default.
	EnforceStrictProofs bool
	// EnableSMT turns on the optional SMT discharge tier (docs/90): an obligation the bounded-linear
	// prover declines (a non-linear product, a richer boolean body) is handed to an SMT solver. Off
	// by default — it spawns a solver subprocess and is only worth it for code the linear tier can't
	// reach. SMTSolverBinary overrides the solver (default "z3"). The tier is sound regardless: an
	// `unsat` of the negated obligation proves it; sat/unknown/missing-solver decline to runtime.
	EnableSMT       bool
	SMTSolverBinary string
	TargetTriple    string
	TargetDebug     bool
	// RequireExternContracts enforces extern contract DISCIPLINE: every extern function must carry a
	// `requires`/`ensure` boundary contract OR an explicit `@trusted("reason")` annotation. An extern
	// that is neither is an error. Opt-in (the `-strict-externs` channel) and deliberately NOT implied
	// by -strict: -strict is on by default, and most real programs link uncontracted libc/host externs,
	// so blanket enforcement would break them. This makes "is this native boundary specified?" auditable.
	RequireExternContracts bool
	// RecordDeathTimeCohorts records the docs/91 G0 inferred death cohorts on Result.DeathTimeCohorts
	// (programmatic access, e.g. the tightness-validation harness) without needing the
	// ELISA_DUMP_DEATHTIME env dump. Read-only analysis; no codegen effect.
	RecordDeathTimeCohorts bool
}

func Analyze(file *ast.File) *Result {
	return AnalyzeWithOptions(file, AnalyzeOptions{})
}

func AnalyzeWithOptions(file *ast.File, options AnalyzeOptions) *Result {
	loweredFile := grammar.LowerFile(file)
	activeFile := loweredFile
	if activeFile == nil {
		activeFile = file
	}
	census := analyzeASTCensus(activeFile)
	exprCapacity := census.exprs
	exprFactsCapacity := census.exprs / 8
	if exprFactsCapacity < 32 {
		exprFactsCapacity = 32
	}
	resolvedCastHookCapacity := census.exprs / 32
	if resolvedCastHookCapacity < 8 {
		resolvedCastHookCapacity = 8
	}
	resolvedInitCallCapacity := census.exprs / 32
	if resolvedInitCallCapacity < 8 {
		resolvedInitCallCapacity = 8
	}
	denseNodeCapacity := census.exprs / 64
	if denseNodeCapacity < 8 {
		denseNodeCapacity = 8
	}
	funcDeclCapacity := census.funcDecls
	if funcDeclCapacity < 8 {
		funcDeclCapacity = 8
	}
	parallelForCapacity := census.parallelFors
	if parallelForCapacity < 4 {
		parallelForCapacity = 4
	}
	a := &Analyzer{
		file:                              file,
		namedTypes:                        map[string]Type{},
		staticInterfaces:                  map[string]*StaticInterface{},
		staticImpls:                       map[string]*StaticImpl{},
		extensionMethodsByName:            map[string][]*ExtensionMethod{},
		ufcsFunctionsByName:               map[string][]*Symbol{},
		permissions:                       map[string]*PermissionSet{},
		aliasRefinements:                  map[string]*ast.RefinementTypeExpr{},
		capabilityAliases:                 map[string][]ast.PermissionRef{},
		globalScope:                       NewScope(nil),
		functionTypes:                     map[string]*FuncType{},
		externLinkNames:                   map[string]externLinkNameSignature{},
		constValues:                       map[string]ConstValue{},
		exprTypes:                         make(map[ast.Expr]Type, exprCapacity),
		permGrowthOps:                     make(map[ast.Expr]bool),
		rewriteDefaults:                   make(map[*ast.Ident]bool, exprCapacity/128+4),
		optionalBindSourceTypes:           make(map[*ast.OptionalBindExpr]Type, exprCapacity/16+8),
		interfaceMethodRefs:               make(map[*ast.FieldExpr]*InterfaceMethodRef, exprCapacity/16+8),
		safeCalls:                         make(map[*ast.CallExpr]*SafeCallInfo, exprCapacity/32+8),
		exprFacts:                         make(map[ast.Expr]OptimizationFacts, exprFactsCapacity),
		indexBoundsProven:                 make(map[*ast.IndexExpr]bool, exprCapacity/64+8),
		getWrappedIndexExprs:              make(map[*ast.IndexExpr]bool, exprCapacity/64+8),
		currentViewStaticLen:              make(map[string]int64),
		currentViewMutable:                make(map[string]bool),
		checkedSliceExprs:                 make(map[*ast.SliceExpr]bool),
		storageViewStaleUses:              make(map[ast.Expr]storageViewDependencyState, exprCapacity/64+8),
		unsafeAliasExprs:                  make(map[ast.Expr]bool, exprCapacity/64+8),
		unsafeAliasStmts:                  make(map[ast.Stmt]bool, exprCapacity/64+8),
		progressSummaries:                 make(map[*ast.FuncDecl]*FunctionProgressSummary, funcDeclCapacity),
		numericLiteralSuffixWarnings:      make(map[ast.Expr]bool, exprCapacity/64+8),
		ambiguousDArrayBuilders:           make(map[*ast.VarDeclStmt]bool),
		treeConstructorCallees:            make(map[ast.Expr]bool, exprCapacity/16+8),
		resolvedCastHooks:                 make(map[ast.Expr]*Symbol, resolvedCastHookCapacity),
		unsafeLifetimeWidenCasts:          make(map[*ast.CastExpr]bool),
		unsafeBufferReinterpretCasts:      make(map[*ast.CastExpr]bool),
		loweredInitCalls:                  make(map[*ast.StructLitExpr]*ast.CallExpr, resolvedInitCallCapacity),
		postfixShorthandCalls:             make(map[*ast.CastExpr]*ast.CallExpr),
		regionStacks:                      make(map[*ast.RegionStmt]RegionStackAssignment),
		resolvedTypeNames:                 make(map[ast.TypeExpr]string),
		resolvedValueNames:                make(map[*ast.Ident]string),
		exprDenseNodeKeys:                 make(map[ast.Expr]DenseNodeKeyInfo, denseNodeCapacity),
		exprNodeTables:                    make(map[ast.Expr]NodeTableInfo, denseNodeCapacity),
		deferInfo:                         map[*ast.DeferStmt]*DeferInfo{},
		foldInfo:                          map[*ast.FoldExpr]*FoldInfo{},
		lambdaInfo:                        map[*ast.LambdaExpr]*LambdaInfo{},
		parallelForInfo:                   make(map[*ast.ParallelForStmt]*ParallelForInfo, parallelForCapacity),
		callArgDisjoint:                   map[*ast.CallExpr]*CallArgDisjointInfo{},
		disjointCallSites:                 map[*ast.FuncDecl][]callDisjointObservation{},
		lawIsCalls:                        map[*ast.BinaryExpr]*ast.CallExpr{},
		refinementChecks:                  map[*ast.VarDeclStmt][]*ast.CallExpr{},
		callArgRefinementChecks:           map[*ast.CallExpr][]*ast.CallExpr{},
		returnRefinementChecks:            map[*ast.ReturnStmt][]*ast.CallExpr{},
		symbolFacts:                       map[*Symbol]OptimizationFacts{},
		funcDeclSymbols:                   make(map[*ast.FuncDecl]*Symbol, funcDeclCapacity),
		declVisibility:                    activeFile.DeclVisibility,
		privateTypeNames:                  map[string]bool{},
		functionAnalyses:                  make(map[*ast.FuncDecl]*FunctionAnalysis, funcDeclCapacity),
		enforceUnsafePermissions:          options.EnforceUnsafePermissions,
		enforceProgressSafety:             options.EnforceProgressSafety,
		enforceStrictConcurrency:          options.EnforceStrictConcurrency,
		enforcePerfLints:                  options.EnforcePerfLints,
		enforceStrictProofs:               options.EnforceStrictProofs,
		requireExternContracts:            options.RequireExternContracts,
		smtEnabled:                        options.EnableSMT,
		smtBinary:                         options.SMTSolverBinary,
		recordDeathCohortsOpt:             options.RecordDeathTimeCohorts,
		castHooksByName:                   map[string]map[castHookSignature]*Symbol{},
		initHooksByName:                   map[string]map[initHookSignature]*Symbol{},
		returnProvenanceInProgress:        map[*ast.FuncDecl]bool{},
		returnProvenanceLocalInProgress:   map[*Symbol]bool{},
		returnBorrowedOwnerRefInProgress:  map[*ast.FuncDecl]bool{},
		returnBorrowedOwnerLocalProgress:  map[*Symbol]bool{},
		sinkParamInferenceInProgress:      map[*ast.FuncDecl]bool{},
		conditionalCallPoststateOriginals: make(map[*ast.CallExpr]map[*Symbol]Type, exprCapacity/16+8),
	}
	a.registerBuiltins()
	a.populateTargetConstValues(options.TargetTriple, options.TargetDebug)
	activeDecls := a.flattenScopedDecls(activeFile.Decls, "", nil)
	a.collectConstValues(activeDecls)
	a.collectPermissionDecls(activeDecls)
	a.collectNamedTypes(activeDecls)
	a.collectTypeAliases(activeDecls)
	a.collectCapabilityAliases(activeDecls)
	a.collectStaticInterfaces(activeDecls)
	a.populateConstEnumMembers(activeDecls)
	a.populateStructFields(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.assignHierarchyEnumTags(activeDecls)
	a.inheritHierarchyCommonFields(activeDecls)
	generatedDecls := make(map[ast.Decl]bool)
	expandedDecls := a.expandActiveAndGeneratedDecls(activeFile.Decls, generatedDecls)
	if len(generatedDecls) != 0 {
		activeFile = &ast.File{Filename: activeFile.Filename, Decls: expandedDecls, DeclVisibility: activeFile.DeclVisibility}
		if loweredFile != nil {
			loweredFile = activeFile
		}
		activeDecls = a.flattenScopedDecls(activeFile.Decls, "", nil)
		generatedScopedDecls := make([]scopedDecl, 0, len(generatedDecls))
		for _, scoped := range activeDecls {
			if generatedDecls[scoped.Decl] {
				generatedScopedDecls = append(generatedScopedDecls, scoped)
			}
		}
		a.collectConstValues(generatedScopedDecls)
		a.collectPermissionDecls(generatedScopedDecls)
		a.collectNamedTypes(generatedScopedDecls)
		a.collectTypeAliases(generatedScopedDecls)
		a.collectCapabilityAliases(generatedScopedDecls)
		a.collectStaticInterfaces(generatedScopedDecls)
		a.populateConstEnumMembers(generatedScopedDecls)
		a.populateStructFields(generatedScopedDecls)
		a.populateEnumVariants(generatedScopedDecls)
		a.assignHierarchyEnumTags(generatedScopedDecls)
		a.inheritHierarchyCommonFields(generatedScopedDecls)
	}
	a.validatePermissionSubsumption()
	a.synthesizeDerivedImplMembers(activeDecls)
	a.warnOnAvoidableStructPadding(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	// docs/75 S2: rewrite zero-annotation grown container ref params into the explicit
	// `[@r]`/`@r` form BEFORE FuncTypes are built, so callee-side region inference reuses
	// the proven S1 region-param threading end-to-end.
	a.inferRegionParamsForGrownContainerParams(activeDecls)
	a.warnOnByValueGrownContainerParams(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.validateAliasRefinements()
	a.collectStaticImpls(activeDecls)
	a.classifyRegionPolymorphicFunctions(activeDecls)
	// docs/91 S4 W5: interprocedural store-target summaries — computed BEFORE body analysis
	// (purely structural; no exprTypes dependency) so checkInterprocStoreEscape can consult them
	// at call sites while the interior-taint side-table is live.
	a.paramStoreTargets = a.computeParamStoreTargets(collectRegionPolyCandidateFuncs(activeDecls))
	// docs/97: fold leading `uses Name(args)` named-contract applications into each function's own
	// Requires/EnsureValues/Changes/Preserves BEFORE body analysis, so contract discharge + the frame
	// checker see the unfolded clauses and a `uses`d precondition is checked at every call site.
	a.expandUsesContracts(activeDecls)
	a.analyzeDecls(activeDecls)
	a.inferFunctionPermissionEffects(activeDecls)
	if options.EnforceProgressSafety {
		a.validateProgressBlocking(activeDecls)
		a.validateProgressRecursion(activeDecls)
	}
	a.validatePermissionUsage(activeDecls)
	a.analyzeExports(activeDecls)
	a.finalizeFuncDisjointParams(activeDecls)
	a.lintHotKernelsForDisjointness()
	// docs/91 G0: death-time cohort + interprocedural arg-retention analysis. Runs AFTER body
	// analysis so exprTypes is complete and the whole-program retention fixpoint can be computed.
	// Read-only observability (surfaced on Result; dumped under ELISA_DUMP_DEATHTIME); no codegen.
	if dumpDeathTime || a.recordDeathCohortsOpt {
		funcs := collectRegionPolyCandidateFuncs(activeDecls)
		a.paramRetained = a.computeParamRetention(funcs)
		for _, fn := range funcs {
			if dumpDeathTime {
				a.recordDeathTimeCohorts(fn)
			}
			a.recordDeathTimeData(fn)
		}
	}
	a.closeSMT()
	if dumpRegionStacks {
		a.dumpRegionLifetimeSummary()
	}
	return &Result{
		SMTProfile:              a.smtStats,
		File:                    file,
		LoweredFile:             loweredFile,
		GlobalScope:             a.globalScope,
		NamedTypes:              a.namedTypes,
		StaticInterfaces:        a.staticInterfaces,
		StaticImpls:             a.staticImpls,
		ConstValues:             a.constValues,
		ExprTypes:               a.exprTypes,
		PermGrowthOps:           a.permGrowthOps,
		RewriteDefaults:         a.rewriteDefaults,
		OptionalBindSourceTypes: a.optionalBindSourceTypes,
		InterfaceMethodRefs:     a.interfaceMethodRefs,
		SafeCalls:               a.safeCalls,
		ExprFacts:               a.exprFacts,
		CastHooks:               a.resolvedCastHooks,
		InitCalls:               a.loweredInitCalls,
		PostfixShorthandCalls:   a.postfixShorthandCalls,
		RegionStacks:            a.regionStacks,
		DeathTimeCohorts:        a.deathTimeCohorts,
		DeathTimeEscapeStats:    a.deathEscapeStats,
		ResolvedTypeNames:       a.resolvedTypeNames,
		ResolvedValueNames:      a.resolvedValueNames,
		DenseNodeKeys:           a.exprDenseNodeKeys,
		NodeTables:              a.exprNodeTables,
		ParallelFor:             a.parallelForInfo,
		CallArgDisjoint:         a.callArgDisjoint,
		FuncDisjointParams:      a.funcDisjointParams,
		LawIsCalls:              a.lawIsCalls,
		LemmaCalls:              a.lemmaCalls,
		GhostDecls:              a.ghostDecls,
		GhostContracts:          a.ghostContracts,
		RefinementChecks:        a.refinementChecks,
		CallArgRefinementChecks: a.callArgRefinementChecks,
		ReturnRefinementChecks:  a.returnRefinementChecks,
		IndexBoundsProven:       a.indexBoundsProven,
		ProofReport:             a.proofReport,
		Defer:                   a.deferInfo,
		Fold:                    a.foldInfo,
		Lambdas:                 a.lambdaInfo,
		FunctionAnalyses:        a.functionAnalyses,
		ProgressSummaries:       a.progressSummaries,
		AnnotatedFuncs:          a.annotatedFuncs,
		ExportedTypes:           a.exportedTypes,
		ExportedFuncs:           a.exportedFuncs,
		ExportedGlobals:         a.exportedGlobals,
		Diagnostics:             a.diagnostics,
	}
}
