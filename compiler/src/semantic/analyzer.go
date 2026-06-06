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
	"rt_string_builder_finish":  {FreshReturnShapeParams: []string{"shape_out"}},
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
	file                    *ast.File
	diagnostics             []Diagnostic
	namedTypes              map[string]Type
	staticInterfaces        map[string]*StaticInterface
	staticImpls             map[string]*StaticImpl
	extensionMethodsByName  map[string][]*ExtensionMethod
	ufcsFunctionsByName     map[string][]*Symbol
	permissions             map[string]*PermissionSet
	effectAliases           map[string]*EffectAlias
	grantAliases            map[string][]ast.PermissionRef
	contextBundles          map[string]*ContextBundle
	paramPacks              map[string]*ParamPack
	globalScope             *Scope
	functionTypes           map[string]*FuncType
	externLinkNames         map[string]externLinkNameSignature
	constValues             map[string]ConstValue
	exprTypes               map[ast.Expr]Type
	treeAttributes          map[string]map[string]*TreeAttribute
	attributeFieldRefs      map[*ast.FieldExpr]*AttributeFieldRef
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
	checkedSliceExprs            map[*ast.SliceExpr]bool
	storageViewStaleUses         map[ast.Expr]storageViewDependencyState
	unsafeAliasExprs             map[ast.Expr]bool
	unsafeAliasStmts             map[ast.Stmt]bool
	progressSummaries            map[*ast.FuncDecl]*FunctionProgressSummary
	numericLiteralSuffixWarnings map[ast.Expr]bool
	treeConstructorCallees       map[ast.Expr]bool
	resolvedCastHooks            map[ast.Expr]*Symbol
	unsafeLifetimeWidenCasts     map[*ast.CastExpr]bool
	unsafeBufferReinterpretCasts map[*ast.CastExpr]bool
	sentinelFuncNameCache        map[string]bool
	loweredInitCalls             map[*ast.StructLitExpr]*ast.CallExpr
	postfixShorthandCalls        map[*ast.CastExpr]*ast.CallExpr
	regionStacks                 map[*ast.RegionStmt]RegionStackAssignment
	exprDenseNodeKeys            map[ast.Expr]DenseNodeKeyInfo
	exprNodeTables               map[ast.Expr]NodeTableInfo
	deferInfo                    map[*ast.DeferStmt]*DeferInfo
	foldInfo                     map[*ast.FoldExpr]*FoldInfo
	lambdaInfo                   map[*ast.LambdaExpr]*LambdaInfo
	symbolFacts                  map[*Symbol]OptimizationFacts
	funcDeclSymbols              map[*ast.FuncDecl]*Symbol
	declVisibility               map[ast.Decl]string
	privateTypeNames             map[string]bool
	castHooksByName              map[string]map[castHookSignature]*Symbol
	initHooksByName              map[string]map[initHookSignature]*Symbol
	typeParamScopes              []map[string]Type
	typeParamInterfaceScopes     []map[string]*StaticInterface
	interfaceAssocTypeScopes     []map[string]Type
	constParamScopes             []map[string]Type
	constEvalScopes              []map[string]ConstValue
	staticContextDepth           int
	staticCallDepth              int
	shapeParamScopes             []map[string]Shape
	regionParamScopes            []map[string]bool
	permissionParamScopes        []map[string]bool
	errorSetParamScopes          []map[string]bool
	freshShapeCounter            int
	returnFreshShapeStatus       map[string]freshReturnStatus
	annotatedFuncs               []*AnnotatedFunc
	exportedTypes                []*ExportedType
	exportedFuncs                []*ExportedFunc
	exportedGlobals              []*ExportedGlobal
	currentScope                 *Scope
	currentReturn                Type
	currentFuncDecl              *ast.FuncDecl
	currentFuncType              *FuncType
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
	regionLifetimeOrdinals        map[string]int
	regionLifetimeCounter         int
	currentRegionMarks            map[*Symbol]regionMarkState
	currentCheckpoints            map[*Symbol]checkpointState
	currentRegionRefs             map[*Symbol]regionRefState
	currentAffineValues           map[affineValueKey]affineValueState
	currentBorrowedOwnerRefs      map[*Symbol]borrowedOwnerRefState
	currentFunctionValues         map[*Symbol]*FuncType
	currentSpecializedValueTypes  map[*Symbol]Type
	currentValueBindings          map[*Symbol]ast.Expr
	currentStorageViewDeps        map[*Symbol]storageViewDependencyState
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
	currentAliasBindings          map[*Symbol]aliasAccessBinding
	currentPackedVariantViews     map[*Symbol]*PackedVariantViewType
	currentPackedStores           map[string]*PackedEnumStoreType
	currentPackedStoreResolutions map[*Symbol]packedStoreResolution
	currentTreeAllocOwner         treeAllocOwnerBinding
	currentFunctionUsedTreeStores map[string]*TreeStoreType
	currentRewriteDefault         *rewriteDefaultContext
	currentSequenceRewrite        *sequenceRewriteContext
	currentAllocExpr              ast.Expr
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
	suppressUninitReadCheck           int
	suppressGlobalReadCheck           int
	currentPoolScopes                 []poolScopeState
	currentIndexBounds                map[string]indexBoundFact
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	currentProgressSummary            *FunctionProgressSummary
	loopDepth                         int
	currentTrustedNonProgressDepth    int
	currentTrustedAssumeProgressDepth int
	currentReturnProvenance           regionRefState
	currentReturnBorrowedOwnerRefs    borrowedOwnerRefSummary
	currentConservativeCallWidenings  map[*Symbol][]conservativeCallWidening
	currentRegionFactTransforms       []FactTransform
	conditionalCallPoststateOriginals map[*ast.CallExpr]map[*Symbol]Type
	suppressDiagnostics               bool
	// Set while analyzing a query/comprehension predicate whose source const-folds (e.g.
	// `count field in fields(T) where field.name != "x"`). Such a predicate is evaluated at
	// compile time over interned literals, so a raw-u8&-`==`/`!=` there is not a runtime
	// address-compare footgun and must not be flagged by the C-string comparison lint.
	inCompileTimeQueryPredicate      bool
	enforceUnsafePermissions         bool
	enforceProgressSafety            bool
	enforcePerfLints                 bool
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
	sinkParamInferenceInProgress     map[*ast.FuncDecl]bool
	parallelForInfo                  map[*ast.ParallelForStmt]*ParallelForInfo
	functionAnalyses                 map[*ast.FuncDecl]*FunctionAnalysis
	loweredWithStmts                 map[*ast.WithStmt]bool
	currentNamespace                 string
	currentUsings                    []string
	importAliases                    map[string]string
	resolvedTypeNames                map[ast.TypeExpr]string
	resolvedValueNames               map[*ast.Ident]string
	currentImplicitScopes            []map[string]ast.Expr
	currentExplicitArgScopes         []map[string]ast.Expr
	currentLocalParamPackScopes      []map[string]*ParamPack
	implicitTempCounter              int
	semanticLimitDiagnostics         map[string]bool
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

type treeAllocOwnerKind int

const (
	treeAllocOwnerNone treeAllocOwnerKind = iota
	treeAllocOwnerPerm
	treeAllocOwnerRegion
	treeAllocOwnerArena
	treeAllocOwnerStore
)

type treeAllocOwnerBinding struct {
	Kind        treeAllocOwnerKind
	RegionName  string
	StoreFamily *TreeType
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
	// EnforcePerfLints promotes the performance-friction lints (pointer-graph, allocation
	// churn) from warnings to hard errors — the `-Wperf` graduated-strictness level for
	// shipped code (docs/70). Off by default so prototyping stays fluid.
	EnforcePerfLints bool
	TargetTriple     string
	TargetDebug      bool
}

func Analyze(file *ast.File) *Result {
	return AnalyzeWithOptions(file, AnalyzeOptions{})
}

func AnalyzeWithOptions(file *ast.File, options AnalyzeOptions) *Result {
	normalizeCascadeStmts(file)
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
		effectAliases:                     map[string]*EffectAlias{},
		grantAliases:                      map[string][]ast.PermissionRef{},
		contextBundles:                    map[string]*ContextBundle{},
		paramPacks:                        map[string]*ParamPack{},
		globalScope:                       NewScope(nil),
		functionTypes:                     map[string]*FuncType{},
		externLinkNames:                   map[string]externLinkNameSignature{},
		constValues:                       map[string]ConstValue{},
		exprTypes:                         make(map[ast.Expr]Type, exprCapacity),
		treeAttributes:                    map[string]map[string]*TreeAttribute{},
		attributeFieldRefs:                make(map[*ast.FieldExpr]*AttributeFieldRef, exprCapacity/32+8),
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
		symbolFacts:                       map[*Symbol]OptimizationFacts{},
		funcDeclSymbols:                   make(map[*ast.FuncDecl]*Symbol, funcDeclCapacity),
		declVisibility:                    activeFile.DeclVisibility,
		privateTypeNames:                  map[string]bool{},
		functionAnalyses:                  make(map[*ast.FuncDecl]*FunctionAnalysis, funcDeclCapacity),
		enforceUnsafePermissions:          options.EnforceUnsafePermissions,
		enforceProgressSafety:             options.EnforceProgressSafety,
		enforcePerfLints:                  options.EnforcePerfLints,
		loweredWithStmts:                  map[*ast.WithStmt]bool{},
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
	a.collectEffectAliases(activeDecls)
	a.collectGrantAliases(activeDecls)
	a.collectContextBundles(activeDecls)
	a.collectParamPacks(activeDecls)
	a.collectStaticInterfaces(activeDecls)
	a.populateConstEnumMembers(activeDecls)
	a.populateStructFields(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.assignHierarchyEnumTags(activeDecls)
	a.inheritHierarchyCommonFields(activeDecls)
	a.populateTreeMembers(activeDecls)
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
		a.collectEffectAliases(generatedScopedDecls)
		a.collectGrantAliases(generatedScopedDecls)
		a.collectContextBundles(generatedScopedDecls)
		a.collectParamPacks(generatedScopedDecls)
		a.collectStaticInterfaces(generatedScopedDecls)
		a.populateConstEnumMembers(generatedScopedDecls)
		a.populateStructFields(generatedScopedDecls)
		a.populateEnumVariants(generatedScopedDecls)
		a.assignHierarchyEnumTags(generatedScopedDecls)
		a.inheritHierarchyCommonFields(generatedScopedDecls)
		a.populateTreeMembers(generatedScopedDecls)
	}
	a.validatePermissionSubsumption()
	a.collectTreeAttributes(activeDecls)
	a.synthesizeDerivedImplMembers(activeDecls)
	a.warnOnAvoidableStructPadding(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.collectStaticImpls(activeDecls)
	a.classifyRegionPolymorphicFunctions(activeDecls)
	a.analyzeDecls(activeDecls)
	a.inferFunctionPermissionEffects(activeDecls)
	if options.EnforceProgressSafety {
		a.validateProgressBlocking(activeDecls)
		a.validateProgressRecursion(activeDecls)
	}
	a.validatePermissionUsage(activeDecls)
	a.analyzeExports(activeDecls)
	if dumpRegionStacks {
		a.dumpRegionLifetimeSummary()
	}
	return &Result{
		File:                    file,
		LoweredFile:             loweredFile,
		GlobalScope:             a.globalScope,
		NamedTypes:              a.namedTypes,
		TreeAttributes:          a.treeAttributes,
		StaticInterfaces:        a.staticInterfaces,
		StaticImpls:             a.staticImpls,
		ContextBundles:          a.contextBundles,
		ParamPacks:              a.paramPacks,
		ConstValues:             a.constValues,
		ExprTypes:               a.exprTypes,
		AttributeFieldRefs:      a.attributeFieldRefs,
		RewriteDefaults:         a.rewriteDefaults,
		OptionalBindSourceTypes: a.optionalBindSourceTypes,
		InterfaceMethodRefs:     a.interfaceMethodRefs,
		SafeCalls:               a.safeCalls,
		ExprFacts:               a.exprFacts,
		CastHooks:               a.resolvedCastHooks,
		InitCalls:               a.loweredInitCalls,
		PostfixShorthandCalls:   a.postfixShorthandCalls,
		RegionStacks:            a.regionStacks,
		ResolvedTypeNames:       a.resolvedTypeNames,
		ResolvedValueNames:      a.resolvedValueNames,
		DenseNodeKeys:           a.exprDenseNodeKeys,
		NodeTables:              a.exprNodeTables,
		ParallelFor:             a.parallelForInfo,
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
