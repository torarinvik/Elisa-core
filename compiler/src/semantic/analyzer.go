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
	file                              *ast.File
	diagnostics                       []Diagnostic
	namedTypes                        map[string]Type
	staticInterfaces                  map[string]*StaticInterface
	staticImpls                       map[string]*StaticImpl
	extensionMethodsByName            map[string][]*ExtensionMethod
	ufcsFunctionsByName               map[string][]*Symbol
	permissions                       map[string]*PermissionSet
	effectAliases                     map[string]*EffectAlias
	contextBundles                    map[string]*ContextBundle
	paramPacks                        map[string]*ParamPack
	globalScope                       *Scope
	functionTypes                     map[string]*FuncType
	constValues                       map[string]ConstValue
	exprTypes                         map[ast.Expr]Type
	treeAttributes                    map[string]map[string]*TreeAttribute
	attributeFieldRefs                map[*ast.FieldExpr]*AttributeFieldRef
	rewriteDefaults                   map[*ast.Ident]bool
	optionalBindSourceTypes           map[*ast.OptionalBindExpr]Type
	interfaceMethodRefs               map[*ast.FieldExpr]*InterfaceMethodRef
	safeCalls                         map[*ast.CallExpr]*SafeCallInfo
	exprFacts                         map[ast.Expr]OptimizationFacts
	indexBoundsProven                 map[*ast.IndexExpr]bool
	numericLiteralSuffixWarnings      map[ast.Expr]bool
	treeConstructorCallees            map[ast.Expr]bool
	resolvedCastHooks                 map[ast.Expr]*Symbol
	loweredInitCalls                  map[*ast.StructLitExpr]*ast.CallExpr
	exprDenseNodeKeys                 map[ast.Expr]DenseNodeKeyInfo
	exprNodeTables                    map[ast.Expr]NodeTableInfo
	deferInfo                         map[*ast.DeferStmt]*DeferInfo
	foldInfo                          map[*ast.FoldExpr]*FoldInfo
	lambdaInfo                        map[*ast.LambdaExpr]*LambdaInfo
	symbolFacts                       map[*Symbol]OptimizationFacts
	funcDeclSymbols                   map[*ast.FuncDecl]*Symbol
	castHooksByName                   map[string]map[castHookSignature]*Symbol
	initHooksByName                   map[string]map[initHookSignature]*Symbol
	typeParamScopes                   []map[string]Type
	typeParamInterfaceScopes          []map[string]*StaticInterface
	interfaceAssocTypeScopes          []map[string]Type
	refStorageParamScopes             []map[string]Type
	refStateParamScopes               []map[string]Type
	constParamScopes                  []map[string]Type
	constEvalScopes                   []map[string]ConstValue
	staticContextDepth                int
	staticCallDepth                   int
	shapeParamScopes                  []map[string]Shape
	regionParamScopes                 []map[string]bool
	permissionParamScopes             []map[string]bool
	freshShapeCounter                 int
	returnFreshShapeStatus            map[string]freshReturnStatus
	annotatedFuncs                    []*AnnotatedFunc
	exportedTypes                     []*ExportedType
	exportedFuncs                     []*ExportedFunc
	exportedGlobals                   []*ExportedGlobal
	currentScope                      *Scope
	currentReturn                     Type
	currentFuncDecl                   *ast.FuncDecl
	currentFuncType                   *FuncType
	currentRegions                    map[*Symbol]regionState
	currentRegionMarks                map[*Symbol]regionMarkState
	currentCheckpoints                map[*Symbol]checkpointState
	currentRegionRefs                 map[*Symbol]regionRefState
	currentAffineValues               map[affineValueKey]affineValueState
	currentBorrowedOwnerRefs          map[*Symbol]borrowedOwnerRefState
	currentFunctionValues             map[*Symbol]*FuncType
	currentSpecializedValueTypes      map[*Symbol]Type
	currentValueBindings              map[*Symbol]ast.Expr
	currentPackedVariantViews         map[*Symbol]*PackedVariantViewType
	currentPackedStores               map[string]*PackedEnumStoreType
	currentPackedStoreResolutions     map[*Symbol]packedStoreResolution
	currentTreeAllocOwner             treeAllocOwnerBinding
	currentFunctionUsedTreeStores     map[string]*TreeStoreType
	currentRewriteDefault             *rewriteDefaultContext
	currentSequenceRewrite            *sequenceRewriteContext
	currentAllocExpr                  ast.Expr
	currentPoolScopes                 []poolScopeState
	currentIndexBounds                map[string]indexBoundFact
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	currentReturnProvenance           regionRefState
	currentReturnBorrowedOwnerRefs    borrowedOwnerRefSummary
	currentConservativeCallWidenings  map[*Symbol][]conservativeCallWidening
	currentRegionFactTransforms       []FactTransform
	conditionalCallPoststateOriginals map[*ast.CallExpr]map[*Symbol]Type
	suppressDiagnostics               bool
	enforceUnsafePermissions          bool
	suppressOptimizationFacts         bool
	suppressLazyFuncSummaryInference  bool
	returnProvenanceInProgress        map[*ast.FuncDecl]bool
	returnProvenanceLocalInProgress   map[*Symbol]bool
	returnBorrowedOwnerRefInProgress  map[*ast.FuncDecl]bool
	returnBorrowedOwnerLocalProgress  map[*Symbol]bool
	sinkParamInferenceInProgress      map[*ast.FuncDecl]bool
	parallelForInfo                   map[*ast.ParallelForStmt]*ParallelForInfo
	functionAnalyses                  map[*ast.FuncDecl]*FunctionAnalysis
	loweredWithStmts                  map[*ast.WithStmt]bool
	currentNamespace                  string
	currentUsings                     []string
	currentImplicitScopes             []map[string]ast.Expr
	currentExplicitArgScopes          []map[string]ast.Expr
	currentLocalParamPackScopes       []map[string]*ParamPack
	implicitTempCounter               int
	semanticLimitDiagnostics          map[string]bool
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
}

type affineValueState struct {
	ConsumedBy              string
	LiveProtocolType        Type
	LiveProtocolDescription string
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
		contextBundles:                    map[string]*ContextBundle{},
		paramPacks:                        map[string]*ParamPack{},
		globalScope:                       NewScope(nil),
		functionTypes:                     map[string]*FuncType{},
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
		numericLiteralSuffixWarnings:      make(map[ast.Expr]bool, exprCapacity/64+8),
		treeConstructorCallees:            make(map[ast.Expr]bool, exprCapacity/16+8),
		resolvedCastHooks:                 make(map[ast.Expr]*Symbol, resolvedCastHookCapacity),
		loweredInitCalls:                  make(map[*ast.StructLitExpr]*ast.CallExpr, resolvedInitCallCapacity),
		exprDenseNodeKeys:                 make(map[ast.Expr]DenseNodeKeyInfo, denseNodeCapacity),
		exprNodeTables:                    make(map[ast.Expr]NodeTableInfo, denseNodeCapacity),
		deferInfo:                         map[*ast.DeferStmt]*DeferInfo{},
		foldInfo:                          map[*ast.FoldExpr]*FoldInfo{},
		lambdaInfo:                        map[*ast.LambdaExpr]*LambdaInfo{},
		parallelForInfo:                   make(map[*ast.ParallelForStmt]*ParallelForInfo, parallelForCapacity),
		symbolFacts:                       map[*Symbol]OptimizationFacts{},
		funcDeclSymbols:                   make(map[*ast.FuncDecl]*Symbol, funcDeclCapacity),
		functionAnalyses:                  make(map[*ast.FuncDecl]*FunctionAnalysis, funcDeclCapacity),
		enforceUnsafePermissions:          options.EnforceUnsafePermissions,
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
	activeDecls := a.flattenScopedDecls(activeFile.Decls, "", nil)
	a.collectConstValues(activeDecls)
	a.collectPermissionDecls(activeDecls)
	a.collectNamedTypes(activeDecls)
	a.collectTypeAliases(activeDecls)
	a.collectEffectAliases(activeDecls)
	a.collectContextBundles(activeDecls)
	a.collectParamPacks(activeDecls)
	a.collectStaticInterfaces(activeDecls)
	a.populateConstEnumMembers(activeDecls)
	a.populateStructFields(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.populateTreeMembers(activeDecls)
	generatedDecls := make(map[ast.Decl]bool)
	expandedDecls := a.expandActiveAndGeneratedDecls(activeFile.Decls, generatedDecls)
	if len(generatedDecls) != 0 {
		activeFile = &ast.File{Filename: activeFile.Filename, Decls: expandedDecls}
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
		a.collectContextBundles(generatedScopedDecls)
		a.collectParamPacks(generatedScopedDecls)
		a.collectStaticInterfaces(generatedScopedDecls)
		a.populateConstEnumMembers(generatedScopedDecls)
		a.populateStructFields(generatedScopedDecls)
		a.populateEnumVariants(generatedScopedDecls)
		a.populateTreeMembers(generatedScopedDecls)
	}
	a.collectTreeAttributes(activeDecls)
	a.synthesizeDerivedImplMembers(activeDecls)
	a.warnOnAvoidableStructPadding(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.collectStaticImpls(activeDecls)
	a.analyzeDecls(activeDecls)
	a.inferFunctionPermissionEffects(activeDecls)
	a.validatePermissionUsage(activeDecls)
	a.analyzeExports(activeDecls)
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
		DenseNodeKeys:           a.exprDenseNodeKeys,
		NodeTables:              a.exprNodeTables,
		ParallelFor:             a.parallelForInfo,
		Defer:                   a.deferInfo,
		Fold:                    a.foldInfo,
		Lambdas:                 a.lambdaInfo,
		FunctionAnalyses:        a.functionAnalyses,
		AnnotatedFuncs:          a.annotatedFuncs,
		ExportedTypes:           a.exportedTypes,
		ExportedFuncs:           a.exportedFuncs,
		ExportedGlobals:         a.exportedGlobals,
		Diagnostics:             a.diagnostics,
	}
}
