package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
	"sync/atomic"
)

type Analyzer struct {
	lookupTraceOwner         string
	lookupTrace              *lookupDependencyRecorder
	permissionEffectsOutcome permissionInferenceOutcome
	permissionInference      *permissionInferenceObserver
	incrementalBodies        *incrementalBodyInvocation
	capabilityQueries        *capabilityQueryInvocation
	// smtStaleLocalDefs marks immutable locals whose initializer read a binding that has since been
	// written (analyzer_smt_shadowing.go); their defining equation no longer holds.
	smtStaleLocalDefs map[*Symbol]bool
	// smtLocalDefDeps caches the syntactic dependency names of immutable local initializers.
	// These AST-derived sets are reused by mutation-driven SMT fact invalidation.
	smtLocalDefDeps                      map[*Symbol][]string
	smtBoolLocalInitCache                map[*Symbol]smtBoolLocalInitCacheEntry
	sviewFrameWriteFreeCache             map[*ast.FuncDecl]bool
	sviewFrameGhostReadsCache            map[*ast.FuncDecl]bool
	programRegions                       map[string]bool
	programRegionNameUse                 int
	programLivedTypes                    map[Type]bool
	provenanceAddrOfDepth                int
	callRegionBindings                   map[string]string
	callRegionBindingConflicts           [][3]string
	trustedRuntimeSourceFiles            map[string]bool
	headerFunctionNames                  headerFunctionNameIndex
	headerStabilityCompleted             map[*ast.FuncDecl]bool
	shortCircuitAnalysisDepth            int
	shortCircuitStability                map[shortCircuitStabilityKey]bool
	deferredImplEffectChecks             []implEffectCheck
	deferredBorrowedNurseryShutdownCalls []borrowedNurseryShutdownCall
	file                                 *ast.File
	diagnostics                          []Diagnostic
	namedTypes                           map[string]Type
	// refineAliases registers named refinement aliases (`refine Name(..) = Base where Pred`,
	// docs/85 "Level 2") by qualified name. They are pure desugaring sugar: a binder-position use
	// expands to the equivalent anonymous WhereRefinementTypeExpr (see
	// analyzer_named_refinement_alias.go). Representation-erased — never in namedTypes.
	refineAliases map[string]*ast.RefineDecl
	// overlayLayouts registers the docs/107 typed guest-memory overlay layouts by name. A
	// `GuestVAddr[L]` carrier whose L is a key here gets `base.field[mem]` field-access desugared
	// to MemoryManager_ReadU<N>/WriteU<N> against L's declared field offsets/widths. Empty unless
	// overlay layouts are supplied via AnalyzeOptions.OverlayLayouts or declared in-source with a
	// `layout` declaration (collectOverlayLayouts).
	overlayLayouts map[string]*easm.Layout
	// overlaySourceLayouts records which overlay layout names came from an in-source `layout`
	// declaration (docs/107 increment 5), so duplicate source declarations are reported while an
	// option-supplied layout of the same name is overridden silently by the program's own.
	overlaySourceLayouts map[string]bool
	// overlaySizeGuards is the stack of size lower-bound facts (docs/107 increment 4) proven by
	// dominating guards `if base.size[mem] >= N:` that are in scope at the current program point. A
	// `base.field[mem]` access whose field carries `requires size >= N` is discharged when some
	// active guard on the same carrier proves `size >= N`. Pushed for the truthy branch of a
	// recognized size guard and truncated when that branch's analysis completes.
	overlaySizeGuards []overlaySizeGuardFact
	// aliasRefinements keeps a type alias's REFINEMENT target expr (`type TileX = i32 is Bounded[..]`)
	// keyed by qualified name. namedTypes erases the refinement to the base, so this is the only
	// channel by which the tier-2 prover can recover a refinement-typed param's entry bound (docs/86).
	aliasRefinements map[string]*ast.RefinementTypeExpr
	// parametricAliasRefinements keeps PARAMETRIC refinement aliases (`type SlotIndex[cap] = u32 is InRange[0, cap]`)
	// keyed by qualified name. A use site `x: SlotIndex[128]` (a GenericType) is instantiated by
	// substituting 128→cap in the template's predicate Args to yield a concrete RefinementTypeExpr.
	parametricAliasRefinements map[string]parametricAliasEntry
	// deferredAliasRefinements holds alias refinements whose predicate validation is postponed until
	// after law symbols are collected (aliases are resolved long before functions/laws exist).
	deferredAliasRefinements []deferredAliasRefinement
	// finalizingRefinements is true during the post-law validation pass: a refinement predicate whose
	// law is still missing is then a hard error. During EAGER resolution (e.g. a struct field type
	// resolved before laws are registered) a missing law instead DEFERS to that pass — laws can have
	// struct subjects and structs refined fields, so the two cannot be ordered, only re-checked later.
	finalizingRefinements bool
	staticInterfaces      map[string]*StaticInterface
	staticImpls           map[string]*StaticImpl
	incrementalOperators  primitiveOperatorSafety
	effectHandlers        map[string]*EffectHandler
	// regionPolyFn is the function under examination by the region-polymorphism
	// classification pre-pass; it supplies the generic-param protocol bounds for
	// resolving `B.method(...)` callees. Nil outside the pre-pass.
	regionPolyFn *ast.FuncDecl
	// packedStoreBuilders maps a function to the packed hierarchy roots it builds nodes of,
	// directly or through a callee (computeTransitiveStoreNeeds); store-capturing submission
	// rejects a worker that builds into the store it shares.
	packedStoreBuilders map[*FuncType]map[string]string
	// regionPolyCandidateFnTypes maps a candidate function's SIMPLE name to its
	// FuncType(s), built once per classification pre-pass. It resolves a constructor
	// call (`Parser(args)`) to its `def Parser(...)->Parser` FuncType even when a
	// same-named struct shadows it in the global value scope — lookupVisibleGlobal
	// returns the struct, hiding the region-poly constructor. Region-poly-ness is read
	// live from the FuncType during the fixpoint. Nil outside the pre-pass.
	regionPolyCandidateFnTypes map[string][]*FuncType
	// regionPolyReturnsCallerArena records the narrower fact that a function's
	// returned value is actually allocated in the threaded caller arena. A
	// function may be region-polymorphic solely because it writes a packed
	// handle through an out parameter; that does not make an unrelated return
	// value caller-arena-owned.
	regionPolyReturnsCallerArena map[*FuncType]bool
	extensionMethodsByName       map[string][]*ExtensionMethod
	ufcsFunctionsByName          map[string][]*Symbol
	// returnBorrowAliasFrames/Journal undo a return-borrow branch's writes to its alias
	// environment (analyzer_return_borrow_alias_journal.go).
	returnBorrowAliasFrames  []returnBorrowAliasFrame
	returnBorrowAliasJournal []returnBorrowAliasJournalEntry
	// ufcsFunctionsVersion counts registrations into ufcsFunctionsByName; ufcsByLastSegment is
	// that map re-keyed by each name's last `.`/`:` segment, valid while its version matches.
	ufcsFunctionsVersion     int
	ufcsByLastSegment        map[string][]*Symbol
	ufcsByLastSegmentVersion int
	storageViewReturnOrigins map[*ast.FuncDecl]*storageViewReturnOriginSummary
	// recursiveCallCertificatesInFlight prevents an SMT proof of a recursive-call
	// certificate from recursively asking the same declaration edge to prove itself.
	// Re-entry declines the nested certificate, leaving that call opaque in the inner goal.
	recursiveCallCertificatesInFlight map[recursiveCallCertificateKey]bool
	permissions                       map[string]*PermissionSet
	capabilityAliases                 map[string][]ast.PermissionRef
	globalScope                       *Scope
	functionTypes                     map[string]*FuncType
	externLinkNames                   map[string]externLinkNameSignature
	constValues                       map[string]ConstValue
	exprTypes                         map[ast.Expr]Type
	exprTypeOverlay                   *analyzerExprTypeOverlay
	compilerBuiltinHelperCalls        map[*ast.CallExpr]string
	permGrowthOps                     map[ast.Expr]bool
	rewriteDefaults                   map[*ast.Ident]bool
	optionalBindSourceTypes           map[*ast.OptionalBindExpr]Type
	interfaceMethodRefs               map[*ast.FieldExpr]*InterfaceMethodRef
	safeCalls                         map[*ast.CallExpr]*SafeCallInfo
	exprFacts                         map[ast.Expr]OptimizationFacts
	exprFactsOverlay                  *analyzerExprFactsOverlay
	// Keep identity owners alive until all invocation-local facts are consumed;
	// a pointer formatted as a string alone does not prevent address reuse by GC.
	optimizationSymbolIDs             map[*Symbol]string
	optimizationSymbolIdentityOverlay *analyzerOptimizationSymbolIdentityOverlay
	optimizationExprIDs               map[ast.Expr]string
	optimizationIdentityNext          *atomic.Uint64
	indexBoundsProven                 map[*ast.IndexExpr]bool
	// Successful proofs retain their owning function even after constant folding
	// detaches the original node. Updates can retract one function's proofs only.
	indexBoundsProofOwners       map[*ast.IndexExpr]ast.Node
	indexBoundsProofHeaders      map[*ast.IndexExpr]bool
	indexBoundsProofOrigins      map[*ast.IndexExpr]ast.Node
	currentIndexProofHeaderOwner ast.Node
	currentIndexProofHeader      bool
	currentIndexProofContext     bool
	currentIndexProofOrigin      ast.Node
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
	storageViewCalleeWriteCache  map[storageViewCalleeWriteKey]storageViewCalleeWrites
	currentProofCitation         *scopedProofCitation
	currentClosedWorldTheory     string
	storageViewStaleUses         map[ast.Expr]storageViewDependencyState
	unsafeAliasExprs             map[ast.Expr]bool
	unsafeAliasStmts             map[ast.Stmt]bool
	progressSummaries            map[*ast.FuncDecl]*FunctionProgressSummary
	numericLiteralSuffixWarnings map[ast.Expr]bool
	ambiguousDArrayBuilders      map[*ast.VarDeclStmt]bool
	treeConstructorCallees       map[ast.Expr]bool
	resolvedCastHooks            map[ast.Expr]*Symbol
	// Exact lexical identities recorded by the ordinary pass for the separate
	// bounded derived-state loop transfer. Never resolve these by spelling later.
	derivedLoopBindings          map[*ast.Ident]*Symbol
	derivedLoopLocals            map[*ast.VarDeclStmt]*Symbol
	derivedLoopAliasWrites       map[*ast.AssignStmt]bool
	unsafeLifetimeWidenCasts     map[*ast.CastExpr]bool
	unsafeBufferReinterpretCasts map[*ast.CastExpr]bool
	// nulTerminatedBuffers names darray locals a `push(0)` has NUL-terminated, so a
	// `(&buf[0]).cast[cstr]` on them is a bounded C-string, not an unbounded scan.
	nulTerminatedBuffers map[string]bool
	// pendingIterFillLints defers "cannot infer a safe reserve bound" until the end of the
	// function, where the statement list is walkable and an explicit user-written
	// `target.reserve(...)` immediately before the loop can be honoured.
	pendingIterFillLints map[*ast.IterForStmt]string
	// iterFillLintDecided remembers loops the post-pass already ruled on. A function body can
	// be analyzed more than once, which would otherwise re-record a loop AFTER its walk
	// consumed it and resurrect a lint the programmer's reserve had already silenced.
	iterFillLintDecided map[*ast.IterForStmt]bool
	// iterFillSuppressedAt keys the same decision by SOURCE POSITION. A file can be analyzed
	// through more than one AST, and the safety-net flush would otherwise re-emit for the
	// second copy a lint the first copy's walk had already silenced.
	iterFillSuppressedAt  map[string]bool
	sentinelFuncNameCache map[string]bool
	loweredInitCalls      map[*ast.StructLitExpr]*ast.CallExpr
	postfixShorthandCalls map[*ast.CastExpr]*ast.CallExpr
	regionStacks          map[*ast.RegionStmt]RegionStackAssignment
	// stackBlockUpperBound: a darray.resize call or a `0..<n` list comprehension -> constant upper
	// bound flow facts prove for its count argument / range end at that site
	// (analyzer_region_stack_block.go). Read by region-stack assignment.
	stackBlockUpperBound map[ast.Expr]int64
	// ambientGrownCalleeParamIndex maps a void-grower function name to the index (among its
	// explicit params) of the container param whose region becomes the callee's ambient
	// allocation region (AmbientGrownContainerRegion). Recorded by the region-param pre-pass;
	// consumed by assignRegionStacks to demote a forwarded container's default reserve_commit
	// stack to chained — the callee interleaves adopted payload allocations into the container's
	// arena, so tail-only growth would panic at the first non-tail realloc.
	ambientGrownCalleeParamIndex map[string]int
	// storeTargetRegion is the container region of the place a checked store writes into,
	// set only for the duration of checkStoredRegionContainerEscapeInto.
	storeTargetRegion string
	// storeScanCache memoizes calleeStoreModeInto per (callee, source param, target param);
	// storeScanCycleHit marks a search that consulted an in-progress frame (not cacheable).
	storeScanCache    map[storeScanKey]storeMode
	storeScanCycleHit bool
	// storeLivedCache memoizes final calleeStoresProgramLivedElements proofs while
	// currentFuncDecl is unchanged; see region_store_lived_cache.go.
	storeLivedCache   map[storeScanKey]bool
	storeFlowKindsOff bool // tests: read every store-flow edge kind as Header
	// The store-scan DFS stack and provisional results (region_store_scan_frames.go).
	storeScanFrames          []storeScanFrame
	storeScanFrameAt         map[storeScanKey]int
	storeScanFrameSeq        uint64
	storeScanProvisionalMemo map[storeScanKey]storeScanProvisional
	// destinationCall / destinationRegion: while analyzing the value of `place <- f(..)` whose place
	// lives in a caller-owned region param R, the region-polymorphic call f receives R (not the
	// ambient region) as its hidden allocation region. destinationAllocated records the calls that
	// did, so the escape check knows their result lives in R.
	storeScanViewCache       map[*ast.FuncDecl]map[string]bool
	mutableLocalsCache       map[*ast.FuncDecl]map[string]bool
	storeScanScalarCache     map[*ast.FuncDecl]map[string]bool
	storeScanBoundCache      map[*ast.FuncDecl]map[string]bool
	storeScanReadOnlyCache   map[*ast.FuncDecl]map[string]bool
	storeScanWritableCache   map[storeScanKey]bool
	storeScanStructIndex     map[string]*StructType
	storeScanAllocCache      map[storeScanKey]bool
	storeScanAllocCycleHit   bool
	storeScanFilledCache     map[storeScanKey]map[string]bool
	storeScanIndexCache      map[*ast.FuncDecl]*storeScanDeclIndex
	storeScanBinderTypes     map[*ast.FuncDecl]map[string]storeScanBinderVerdict // storeScanPatternBinderType memo
	storeScanBinderOverlay   *analyzerStoreScanBinderOverlay
	storeScanLoopDepth       int // storeScanSubjectDeclType's loop-binder recursion depth
	storeScanParamWriteCache map[storeScanParamKey]bool
	storeScanLastHolders     map[string]bool // holders of the last storeScanTaint (read at once)

	noStorageOverlay *analyzerNoStorageOverlay

	storeScanNoStorageCache    map[Type]bool
	storeScanViewOnlyCache     map[Type]bool
	storeScanSharesCache       map[Type]bool
	storeScanTaintCache        map[storeScanTaintKey]storeScanTaintResult
	storeScanParamFactsCache   map[storeScanParamKey]storeScanParamFacts
	storeScanParamFactsOverlay *analyzerStoreScanParamFactsOverlay
	storeScanCallWriteCache    map[*ast.CallExpr][]storeScanCallWrite
	destinationCall            *ast.CallExpr
	destinationRegion          string
	destinationAllocated       map[*ast.CallExpr]string
	// regionParamCalleeParamIndex maps each function name to explicit parameter positions whose
	// types carry one of that function's region parameters. Forwarding a fresh region container to
	// any such position shares its allocation region with the callee, which may allocate between
	// growth operations on the caller's container. Region-stack assignment keeps that backing chained.
	regionParamCalleeParamIndex map[string]map[int]bool
	// deathTimeCohorts holds the docs/91 G0 inferred death cohorts per function, recorded when
	// ELISA_DUMP_DEATHTIME is set or RecordDeathTimeCohorts is requested (read-only observability;
	// surfaced on Result).
	deathTimeCohorts      map[string][]DeathTimeCohort
	deathEscapeStats      map[string]DeathTimeEscapeStats
	paramRetained         map[*ast.FuncDecl][]bool
	paramStoreTargets     map[*ast.FuncDecl][]map[int]bool
	handlerCaptureCounter uint64
	recordDeathCohortsOpt bool
	exprDenseNodeKeys     map[ast.Expr]DenseNodeKeyInfo
	exprNodeTables        map[ast.Expr]NodeTableInfo
	deferInfo             map[*ast.DeferStmt]*DeferInfo
	foldInfo              map[*ast.FoldExpr]*FoldInfo
	lambdaInfo            map[*ast.LambdaExpr]*LambdaInfo
	// One-shot closures (analyzer_one_shot_closure.go).
	oneShotLambdas        map[*ast.LambdaExpr]string
	oneShotClosureSyms    map[*Symbol]string
	pendingClosureBinding *ast.LambdaExpr
	oneShotCallee         *ast.Ident
	symbolFacts           map[*Symbol]OptimizationFacts
	funcDeclSymbols       map[*ast.FuncDecl]*Symbol
	declVisibility        map[ast.Decl]string
	reportedDiagnostics   map[diagnosticIdentity]bool
	privateTypeNames      map[string]bool
	privateModules        map[string]bool
	castHooksByName       map[string]map[castHookSignature]*Symbol
	initHooksByName       map[string]map[initHookSignature]*Symbol
	// dropHooks maps a qualified struct type name to its `__drop__` destructor
	// (docs/126 D1). Populated by collectDropHooks BEFORE body analysis, because
	// declaring a destructor INDUCES affinity on the type and every downstream
	// move/copy/must-consume check must see that.
	dropHooks map[string]*dropHook
	// implicitDropSites records the compiler-inserted destructor calls the flow analysis
	// proved necessary, keyed by the VarDeclStmt of the dying value — a drop always runs
	// in the declaring statement's own effect-grant scope, so that key is also exactly
	// the right one for the effect check and for the backend's cleanup registration.
	implicitDropSites map[ast.Node][]DropSite
	// currentDropLocals is the declaration-ordered list of drop-typed locals in the
	// function being analyzed. Reset per function; walked backwards at each exit edge
	// so drops run in reverse declaration order (docs/126 §2).
	currentDropLocals          []dropLocalBinding
	lexicalReads               *lexicalReadInvocation
	lexicalBinders             *lexicalBinderOwnership
	typeParamScopes            []map[string]Type
	typeParamInterfaceScopes   []map[string]*StaticInterface
	interfaceAssocTypeScopes   []map[string]Type
	constParamScopes           []map[string]Type
	constEvalScopes            []map[string]ConstValue
	constEvalDepth             int
	constInitDepth             int
	staticContextDepth         int
	staticCallDepth            int
	shapeParamScopes           []map[string]Shape
	regionParamScopes          []map[string]bool
	regionUnrelatedParamScopes []map[string]bool
	permissionParamScopes      []map[string]bool
	errorSetParamScopes        []map[string]bool
	freshShapeCounter          int
	freshShapeScope            string
	freshShapeLambdaCounter    int
	returnFreshShapeStatus     map[string]freshReturnStatus
	annotatedFuncs             []*AnnotatedFunc
	lawFuzzObligations         []*LawFuzzObligation
	exportedTypes              []*ExportedType
	exportedFuncs              []*ExportedFunc
	exportedGlobals            []*ExportedGlobal
	currentScope               *Scope
	currentReturn              Type
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
	// smtLogGoalLine is the source line of the goal being discharged (rlimit-log attribution only).
	smtLogGoalLine int
	// globalStorageAliasLocals marks by-value container locals that share a global's storage
	// (see analyzer_global_storage_return.go).
	globalStorageAliasLocals map[*Symbol]string
	currentFuncType          *FuncType
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
	analyzerFunctionRegionState
	// suppressUninitReadCheck > 0 while analyzing an address-of operand or an
	// assignment target, where naming a `zeroed`-uninitialized local is not a
	// value read (it is being filled / had its address taken), so the
	// definite-assignment read check must not fire.
	suppressUninitReadCheck int
	// zeroedPlaceholderExpected is non-nil only while analyzing a local's direct
	// `= zeroed` initializer. It permits a scalar invalid slot to be staged in
	// that local because definite-assignment tracking prevents reads before it is
	// filled. It does not permit invalid zeroed values in returns, call arguments,
	// nested fields, or aggregates whose field initialization is not path-sensitive.
	zeroedPlaceholderExpected Type
	suppressGlobalReadCheck   int
	currentPoolScopes         []poolScopeState
	analyzerControlFlowState
	suppressDiagnostics bool
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
	enforceGlobalPermissions    bool
	enforceProgressSafety       bool
	enforceStrictConcurrency    bool
	enforcePerfLints            bool
	flowLintMode                FlowLintMode
	enforceStrictProofs         bool
	emitProofHoleHints          bool
	warnDiscardedValues         bool
	// valueBlockAllowed is a stack of the innermost value block's allowed-mutation names
	// (block-local bindings + licensed captures), used by the mutating-CALL half of E4
	// (docs/119 §6.2). Empty ⇒ not inside a value block.
	valueBlockAllowed []map[string]bool
	// valueBlockLoopDepths is a stack of the loopDepth at each enclosing value block's entry
	// (docs/119 E5). A `break`/`continue` whose loopDepth equals the innermost entry depth
	// has no loop of its own inside the block, so it would jump out of the block.
	valueBlockLoopDepths []int
	// reassignTargets holds the root names on the left of the reassignment statement
	// currently being analyzed (`p <- …` / `t, p <- …`). docs/120 §10 enforcement exempts
	// a call that mutates an lmut place named here — the mutation IS reassigned, which is
	// exactly the invariant. Set around the value analysis of an AssignStmt / TupleBindStmt.
	reassignTargets map[string]bool
	// loopCaptureAllowed is the stack of statement-loop capture manifests
	// (`while parser.accept(k) |parser|:` — docs/120 §9): a mutating call on a
	// captured binding anywhere in the loop (condition or body) is licensed, the
	// capture being the loop's visible mutation manifest. One frame per nested
	// captured loop.
	loopCaptureAllowed []map[string]bool
	// enforceLinearMutation gates the docs/120 §10 uniform rule (a bare lmut-mutating call
	// is an error). Off until the frontend is fully converted to the reassignment form.
	enforceLinearMutation  bool
	requireExternContracts bool
	// requiresReport gates the -requires-report aggregation (docs c3): per requires-bearing function,
	// how many direct call sites statically discharge the precondition vs fall back to a runtime check.
	// OFF by default; when off, requiresReportData/Order stay nil and nothing else changes.
	requiresReport      bool
	requiresReportData  map[requiresReportKey]*RequiresReportEntry
	requiresReportOrder []requiresReportKey
	// inClosedWorldProof is set while discharging a `by scoped:` proof block (docs/99). Under it, the
	// ambient hypothesis sources that are NOT scope-walled — the enclosing function's `requires` and the
	// defining equalities of immutable locals — are suppressed, so the closed world holds ONLY the facts
	// cited inside the block (which ride the scope-walled assert/flow/range fact channels). This is what
	// makes a scoped proof's verdict depend solely on its citations.
	inClosedWorldProof bool
	analyzerProofSolverState
	storeScanFillWhy   string               // trace only: last reason storeScanLocalIsFilled rejected a local
	viewProvWhy        string               // trace only: last view-provenance decline reason
	parallel           *parallelWorkerState // set only on a parallel body-analysis worker
	trackedShareChecks []trackedShareRecord // ELISA_TRACKED_SHARE_CHECK; see tracked_share.go
	smtTextFree        []*smtTextBuilder    // recycled SMT text buffers; see smt_text_scratch.go
	// zeroedStructLocals tracks locals declared `= zeroed` whose type is a struct and whose root has
	// not been wholesale-replaced since declaration. Cleared for a root in invalidateWrittenFieldsForRoot
	// when called for a non-field write (a root reassignment, borrow, or mutating-callee escape).
	// Used by zeroedStructLocalFieldMap to supply zero as the baseline value for unwritten struct fields
	// in `ensure result.f == v` postcondition proofs where `result` is a zeroed-then-mutated local.
	zeroedStructLocals               map[string]bool
	suppressOptimizationFacts        bool
	suppressLazyFuncSummaryInference bool
	returnProvenanceInProgress       map[*ast.FuncDecl]bool
	// funcDeclUsings records each function declaration's file/module `using` imports so
	// speculative re-analyses (return-provenance / borrowed-owner-ref inference) resolve
	// unqualified names exactly as the body's real analysis does.
	funcDeclUsings map[*ast.FuncDecl][]string
	analyzerReturnBorrowState
	// functionValueResolveInProgress guards functionValueTypeForExpr against a self-referential /
	// cyclic value binding (e.g. `x = x`, where the symbol's value expr resolves back to itself):
	// re-entering a symbol already on the resolution stack returns "no function-value type" instead of
	// recursing until the goroutine stack overflows.
	functionValueResolveInProgress map[*Symbol]bool
	// functionValueSummaryInProgress prevents return-summary inference from recursively asking for
	// the same expression/type summary while following a cyclic function-value binding.
	functionValueSummaryInProgress map[functionValueSummaryKey]bool
	// projectedFieldResolveInProgress: the same guard for resolveProjectedFieldValueExprAtPath.
	projectedFieldResolveInProgress map[*Symbol]bool
	sinkParamInferenceInProgress    map[*ast.FuncDecl]bool
	parallelForInfo                 map[*ast.ParallelForStmt]*ParallelForInfo
	// taskGroupSubmits records the bare `submit[pool] worker(arg)` call sites in a
	// structurally verified TaskGroup window. Both codegen and the local-borrow
	// checker consume this same proof; a submit is never treated as joined unless
	// codegen also registers its returned handle in the matching group.
	taskGroupSubmits      map[*ast.CallExpr]TaskGroupSubmitInfo
	callArgDisjoint       map[*ast.CallExpr]*CallArgDisjointInfo
	callArgDisjointByFunc map[*ast.FuncDecl]map[*ast.CallExpr]bool
	disjointCallSites     map[*ast.FuncDecl][]callDisjointObservation
	// disjointCallSitesByCaller replaces only observations owned by a changed body.
	disjointCallSitesByCaller map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation
	disjointDependents        map[*ast.FuncDecl]map[*ast.FuncDecl]bool
	disjointDirtyCallees      map[*ast.FuncDecl]bool
	disjointEligibility       map[*ast.FuncDecl]bool
	disjointIndexesReady      bool
	funcDisjointParams        map[*ast.FuncDecl]*FuncDisjointParamInfo
	lawIsCalls                map[*ast.BinaryExpr]*ast.CallExpr
	builtinModularLaws        map[string]*ast.FuncDecl
	lemmaCalls                map[*ast.CallExpr]bool
	ghostDecls                map[*ast.VarDeclStmt]bool
	ghostContracts            map[ast.Expr]bool
	// ghostReadAllowed, when > 0, permits reading a `ghost` variable (the analyzer is inside a
	// contract clause or another ghost initializer). Outside these contexts a ghost read is a hard
	// error — that is the ghost-to-real flow barrier that keeps erasure sound.
	ghostReadAllowed int
	// ghostReadSeen records that a ghost variable was read since it was last reset — used to flag a
	// contract condition (invariant/assert) that mentions a ghost so codegen erases its check.
	ghostReadSeen    bool
	lemmasInAnalysis map[*ast.FuncDecl]bool
	// lemmaTerminationCache memoizes whether a lemma's `decreases` measure is verified to strictly
	// decrease at every self-recursive call (read-only mirror of checkTermination, no diagnostics). It
	// gates the inductive hypothesis: only a provably-terminating lemma may assume its own ensure for a
	// strictly-smaller argument. A false entry only forgoes the IH; it never fabricates a proof.
	lemmaTerminationCache map[*ast.FuncDecl]bool
	// definingEquationCache / ...InProgress memoize whether a PURE, TOTAL function's defining equation
	// (`f(args) == body[params:=args]`) may be soundly asserted to the SMT tier. InProgress breaks
	// self/mutual-recursive purity checks. A false verdict only forgoes the equation; it is never unsound.
	definingEquationCache      map[*ast.FuncDecl]bool
	viewEquationCache          map[*ast.FuncDecl]bool
	terminationVerified        map[*ast.FuncDecl]bool
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
	pendingDisjointCalls        []pendingDisjointCall
	reassignedParamCache        map[*ast.FuncDecl]map[string]bool
	functionAnalyses            map[*ast.FuncDecl]*FunctionAnalysis
	currentNamespace            string
	currentUsings               []string
	importAliases               map[string]string
	moduleAliases               map[string]string
	moduleAliasPaths            map[string]moduleAliasPath
	// visibleAliasGen counts alias registrations (invalidates visibleCandidates).
	visibleAliasGen   int
	visibleCandidates visibleCandidateCache
	// declaredModules is every `module` path (dot-flattened key) seen by
	// validateModuleExtensions; rejectDotModulePath consults it.
	declaredModules map[string]bool
	// reportedDotModulePaths dedupes rejectDotModulePathExpr across re-analysis.
	reportedDotModulePaths map[*ast.FieldExpr]bool
	reportedDotModuleTypes map[string]bool
	resolvedTypeNames      map[ast.TypeExpr]string
	// Transient concrete branch choices captured only during private codegen analysis.
	specializationStaticBranches  map[*ast.StaticIfStmt][]ast.Stmt
	contextualTypeBindings        map[ast.TypeExpr]Type
	mutableGlobalBorrowRefs       map[*ast.VarDeclStmt][]ast.PermissionRef
	mutableGlobalBorrowAssignRefs map[*ast.AssignStmt][]ast.PermissionRef
	resolvedGlobalStorage         map[*ast.Ident]*Symbol
	resolvedValueNames            map[*ast.Ident]string
	currentImplicitScopes         []map[string]ast.Expr
	semanticLimitDiagnostics      map[string]bool
	// semanticLimitHits counts reportSemanticDepthLimit calls, so a memo can tell whether a
	// traversal was cut short (and must not be cached).
	semanticLimitHits int
	analyzerTypeMemoState
}
