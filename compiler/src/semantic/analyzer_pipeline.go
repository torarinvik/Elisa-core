package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"time"
)

func Analyze(file *ast.File) *Result {
	return AnalyzeWithOptions(file, AnalyzeOptions{})
}

func AnalyzeWithOptions(file *ast.File, options AnalyzeOptions) *Result {
	return analyzeWithBodyInvocation(file, options, nil)
}

func analyzeWithBodyInvocation(file *ast.File, options AnalyzeOptions, bodies *incrementalBodyInvocation) *Result {
	return analyzeWithEffectInference(file, options, bodies, nil)
}

// The private solver seam permits complete-result differential tests against the
// reference inference scheduler without changing public analysis options.
func analyzeWithEffectInference(file *ast.File, options AnalyzeOptions, bodies *incrementalBodyInvocation, solver func(*Analyzer, []scopedDecl)) *Result {
	return analyzeWithLookupRecorder(file, options, bodies, solver, nil, nil)
}

// Per-invocation setup prevents ordinary concurrent analyses from observing or
// consuming a retained or pre-body analysis's private state.
type analysisSetup struct {
	retained *RetainedAnalysis
	preBody  *preBodyState
	build    *AnalysisBuild
}

var postTiming = os.Getenv("ELISA_POST_TIMING") != ""

// postTimed runs one post-body phase, reporting its time under ELISA_POST_TIMING.
func postTimed(name string, phase func()) {
	if !postTiming {
		phase()
		return
	}
	start := time.Now()
	phase()
	fmt.Fprintf(os.Stderr, "[post-timing] %s %v\n", name, time.Since(start).Round(time.Millisecond))
}

func analyzeWithLookupRecorder(file *ast.File, options AnalyzeOptions, bodies *incrementalBodyInvocation, solver func(*Analyzer, []scopedDecl), recorder *lookupDependencyRecorder, setup *analysisSetup) *Result {
	loweredFile := file
	activeFile := loweredFile
	// Fold recognized total char classifiers to static lookup tables (docs/125 §9.6 / C3)
	// before census/analysis, so the synthesized const table and rewritten body flow through
	// the normal analysis + codegen paths. A no-op unless a foldable classifier is present.
	foldCharClassifierTables(activeFile)
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
		optimizationIdentityNext:          &atomic.Uint64{},
		lookupTrace:                       recorder,
		trustedRuntimeSourceFiles:         trustedRuntimeSourceFileSet(options.TrustedRuntimeSourceFiles),
		file:                              file,
		incrementalBodies:                 bodies,
		namedTypes:                        map[string]Type{},
		staticInterfaces:                  map[string]*StaticInterface{},
		staticImpls:                       map[string]*StaticImpl{},
		effectHandlers:                    map[string]*EffectHandler{},
		extensionMethodsByName:            map[string][]*ExtensionMethod{},
		ufcsFunctionsByName:               map[string][]*Symbol{},
		permissions:                       map[string]*PermissionSet{},
		aliasRefinements:                  map[string]*ast.RefinementTypeExpr{},
		parametricAliasRefinements:        map[string]parametricAliasEntry{},
		capabilityAliases:                 map[string][]ast.PermissionRef{},
		globalScope:                       NewScope(nil),
		functionTypes:                     map[string]*FuncType{},
		externLinkNames:                   map[string]externLinkNameSignature{},
		constValues:                       map[string]ConstValue{},
		exprTypes:                         make(map[ast.Expr]Type, exprCapacity),
		compilerBuiltinHelperCalls:        make(map[*ast.CallExpr]string),
		permGrowthOps:                     make(map[ast.Expr]bool),
		rewriteDefaults:                   make(map[*ast.Ident]bool, exprCapacity/128+4),
		optionalBindSourceTypes:           make(map[*ast.OptionalBindExpr]Type, exprCapacity/128+8),
		interfaceMethodRefs:               make(map[*ast.FieldExpr]*InterfaceMethodRef, exprCapacity/128+8),
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
		ambientGrownCalleeParamIndex:      make(map[string]int),
		regionParamCalleeParamIndex:       make(map[string]map[int]bool),
		treeConstructorCallees:            make(map[ast.Expr]bool),
		resolvedCastHooks:                 make(map[ast.Expr]*Symbol, resolvedCastHookCapacity),
		unsafeLifetimeWidenCasts:          make(map[*ast.CastExpr]bool),
		unsafeBufferReinterpretCasts:      make(map[*ast.CastExpr]bool),
		nulTerminatedBuffers:              make(map[string]bool),
		pendingIterFillLints:              make(map[*ast.IterForStmt]string),
		iterFillLintDecided:               make(map[*ast.IterForStmt]bool),
		iterFillSuppressedAt:              make(map[string]bool),
		loweredInitCalls:                  make(map[*ast.StructLitExpr]*ast.CallExpr, resolvedInitCallCapacity),
		postfixShorthandCalls:             make(map[*ast.CastExpr]*ast.CallExpr),
		regionStacks:                      make(map[*ast.RegionStmt]RegionStackAssignment),
		resolvedTypeNames:                 make(map[ast.TypeExpr]string),
		contextualTypeBindings:            make(map[ast.TypeExpr]Type),
		resolvedValueNames:                make(map[*ast.Ident]string),
		exprDenseNodeKeys:                 make(map[ast.Expr]DenseNodeKeyInfo, denseNodeCapacity),
		exprNodeTables:                    make(map[ast.Expr]NodeTableInfo, denseNodeCapacity),
		deferInfo:                         map[*ast.DeferStmt]*DeferInfo{},
		foldInfo:                          map[*ast.FoldExpr]*FoldInfo{},
		lambdaInfo:                        map[*ast.LambdaExpr]*LambdaInfo{},
		parallelForInfo:                   make(map[*ast.ParallelForStmt]*ParallelForInfo, parallelForCapacity),
		taskGroupSubmits:                  map[*ast.CallExpr]TaskGroupSubmitInfo{},
		callArgDisjoint:                   map[*ast.CallExpr]*CallArgDisjointInfo{},
		callArgDisjointByFunc:             map[*ast.FuncDecl]map[*ast.CallExpr]bool{},
		disjointCallSites:                 map[*ast.FuncDecl][]callDisjointObservation{},
		disjointCallSitesByCaller:         map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation{},
		disjointDependents:                map[*ast.FuncDecl]map[*ast.FuncDecl]bool{},
		disjointDirtyCallees:              map[*ast.FuncDecl]bool{},
		disjointEligibility:               map[*ast.FuncDecl]bool{},
		funcDisjointParams:                map[*ast.FuncDecl]*FuncDisjointParamInfo{},
		lawIsCalls:                        map[*ast.BinaryExpr]*ast.CallExpr{},
		builtinModularLaws:                map[string]*ast.FuncDecl{},
		refinementChecks:                  map[*ast.VarDeclStmt][]*ast.CallExpr{},
		callArgRefinementChecks:           map[*ast.CallExpr][]*ast.CallExpr{},
		returnRefinementChecks:            map[*ast.ReturnStmt][]*ast.CallExpr{},
		symbolFacts:                       map[*Symbol]OptimizationFacts{},
		funcDeclSymbols:                   make(map[*ast.FuncDecl]*Symbol, funcDeclCapacity),
		declVisibility:                    activeFile.DeclVisibility,
		privateTypeNames:                  map[string]bool{},
		privateModules:                    map[string]bool{},
		functionAnalyses:                  make(map[*ast.FuncDecl]*FunctionAnalysis, funcDeclCapacity),
		enforceUnsafePermissions:          options.EnforceUnsafePermissions,
		enforceGlobalPermissions:          options.EnforceGlobalPermissions,
		enforceProgressSafety:             options.EnforceProgressSafety,
		enforceStrictConcurrency:          options.EnforceStrictConcurrency,
		enforcePerfLints:                  options.EnforcePerfLints,
		flowLintMode:                      options.FlowLintMode,
		enforceStrictProofs:               options.EnforceStrictProofs,
		emitProofHoleHints:                options.EmitProofHoleHints,
		warnDiscardedValues:               options.WarnDiscardedValues,
		requiresReport:                    options.RequiresReport,
		requireExternContracts:            options.RequireExternContracts,
		smtEnabled:                        options.EnableSMT,
		smtBinary:                         options.SMTSolverBinary,
		recordDeathCohortsOpt:             options.RecordDeathTimeCohorts,
		overlayLayouts:                    overlayLayoutRegistry(options.OverlayLayouts),
		castHooksByName:                   map[string]map[castHookSignature]*Symbol{},
		initHooksByName:                   map[string]map[initHookSignature]*Symbol{},
		returnProvenanceInProgress:        map[*ast.FuncDecl]bool{},
		ambientFillAnalyzed:               map[*ast.FuncDecl]bool{},
		ambientFillFresh:                  map[*ast.FuncDecl]bool{},
		funcDeclUsings:                    map[*ast.FuncDecl][]string{},
		returnProvenanceLocalInProgress:   map[*Symbol]bool{},
		returnBorrowedOwnerRefInProgress:  map[*ast.FuncDecl]bool{},
		returnBorrowedOwnerLocalProgress:  map[*Symbol]bool{},
		functionValueSummaryInProgress:    map[functionValueSummaryKey]bool{},
		sinkParamInferenceInProgress:      map[*ast.FuncDecl]bool{},
		conditionalCallPoststateOriginals: make(map[*ast.CallExpr]map[*Symbol]Type, exprCapacity/128+8),
	}
	if setup != nil {
		a.retained, a.preBody = setup.retained, setup.preBody
	}
	if bodies != nil {
		a.lexicalBinders = &lexicalBinderOwnership{}
		a.lexicalReads = bodies.lexical
		a.capabilityQueries = &capabilityQueryInvocation{session: bodies.session, local: map[Type]capabilityLocalResult{}}
		if len(bodies.keys) == 0 {
			a.incrementalBodies = nil
		}
	}
	defer func() {
		if bodies != nil && a.lexicalBinders != nil {
			bodies.session.stats.BinderFramesOwned += a.lexicalBinders.owned
			bodies.session.stats.BinderFramesOwnerless += a.lexicalBinders.ownerless
		}
		a.lexicalReads = nil
		a.lexicalBinders = nil
		a.incrementalBodies = nil
		a.capabilityQueries = nil
		a.lookupTrace = nil
	}()
	// docs/120 §10: the uniform linear-mutation rule is default-on now that the whole
	// self-hosted frontend is converted to the reassignment form. `ELISA_LINEAR_MUTATION=0`
	// is the explicit off-switch (escape hatch during any residual migration).
	a.enforceLinearMutation = os.Getenv("ELISA_LINEAR_MUTATION") != "0"
	declPhaseStart := time.Now()
	a.registerBuiltins()
	a.populateTargetConstValues(options.TargetTriple, options.TargetDebug)
	activeDecls := a.flattenScopedDecls(activeFile.Decls, "", nil)
	a.collectProgramRegions(activeDecls)
	a.collectConstValues(activeDecls)
	a.validateModuleExtensions(activeFile.Decls)
	a.collectPermissionDecls(activeDecls)
	a.collectOverlayLayouts(activeDecls)
	a.collectNamedTypes(activeDecls)
	a.collectTypeAliases(activeDecls)
	a.collectRefineAliases(activeDecls)
	a.collectCapabilityAliases(activeDecls)
	a.collectStaticInterfaces(activeDecls)
	a.collectEffectPermissions(activeDecls)
	a.collectEffectHandlers(activeDecls)
	a.populateConstEnumMembers(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.checkDeclaredStoreRegions(activeDecls)
	// Struct-derived-state predicates may compare fields with qualified enum
	// variants (for example, `self.phase == Lifecycle.Cold`). Enum variants
	// therefore have to be populated before struct validation analyzes those
	// predicates. Both passes operate on already-collected type skeletons, so
	// enum payloads can still resolve struct types declared later in the file.
	a.populateStructFields(activeDecls)
	a.checkStructByValueCycles(activeDecls)
	a.assignHierarchyEnumTags(activeDecls)
	a.inheritHierarchyCommonFields(activeDecls)
	generatedDecls := make(map[ast.Decl]bool)
	expandedDecls := a.expandActiveAndGeneratedDecls(activeFile.Decls, generatedDecls)
	if len(generatedDecls) != 0 {
		activeFile = &ast.File{Filename: activeFile.Filename, Decls: expandedDecls, DeclVisibility: activeFile.DeclVisibility}
		loweredFile = activeFile
		activeDecls = a.flattenScopedDecls(activeFile.Decls, "", nil)
		generatedScopedDecls := make([]scopedDecl, 0, len(generatedDecls))
		for _, scoped := range activeDecls {
			if generatedDecls[scoped.Decl] {
				generatedScopedDecls = append(generatedScopedDecls, scoped)
			}
		}
		a.collectConstValues(generatedScopedDecls)
		a.collectPermissionDecls(generatedScopedDecls)
		a.collectOverlayLayouts(generatedScopedDecls)
		a.collectNamedTypes(generatedScopedDecls)
		a.collectTypeAliases(generatedScopedDecls)
		a.collectRefineAliases(generatedScopedDecls)
		a.collectCapabilityAliases(generatedScopedDecls)
		a.collectStaticInterfaces(generatedScopedDecls)
		a.collectEffectPermissions(generatedScopedDecls)
		a.collectEffectHandlers(generatedScopedDecls)
		a.populateConstEnumMembers(generatedScopedDecls)
		a.populateStructFields(generatedScopedDecls)
		a.populateEnumVariants(generatedScopedDecls)
		a.assignHierarchyEnumTags(generatedScopedDecls)
		a.inheritHierarchyCommonFields(generatedScopedDecls)
	}
	// Expand named refinement aliases in binder positions (parameter / return types) in place, so all
	// downstream where machinery sees a normal anonymous WhereRefinementTypeExpr. Runs after generated
	// decls are merged into activeDecls so newly-synthesized functions are covered too.
	a.rewriteBinderRefineAliases(activeDecls)
	a.validatePermissionSubsumption()
	// Protocol depth: fold base-protocol members into derived protocols (protocol inheritance),
	// then synthesize inherited default-method bodies into impls that omit them. Both run before
	// value-symbol collection so the synthesized impl methods flow through the normal impl path.
	a.foldInterfaceBases(activeDecls)
	a.synthesizeDerivedImplMembers(activeDecls)
	a.synthesizeDefaultImplMembers(activeDecls)
	a.warnOnAvoidableStructPadding(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	a.checkAppendOnlyStores(activeDecls)
	a.induceAppendOnlyAffinity(activeDecls)
	// docs/75 S2: rewrite zero-annotation grown container ref params into the explicit
	// `[@r]`/`@r` form BEFORE FuncTypes are built, so callee-side region inference reuses
	// the proven S1 region-param threading end-to-end.
	a.lowerProtocolParameters(activeDecls)
	a.inferRegionParamsForGrownContainerParams(activeDecls)
	a.warnOnByValueGrownContainerParams(activeDecls)
	a.collectValueSymbols(activeDecls)
	// docs/126 D1: register `__drop__` destructors and INDUCE AFFINITY on their types.
	// Must run after value symbols (each hook needs its resolved FuncType) and before
	// any body analysis, so the induced move-only discipline governs every use.
	a.collectDropHooks(activeDecls)
	a.checkResourceDrops()
	a.rejectDropTypedGlobals(activeDecls)
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
	// docs/101: lift higher-order `ensures(f, pred)` clauses off function-typed parameters into
	// structured ParamContracts (removing them from Requires), so the call-site entailment check and
	// the callee-body assumption can compose value contracts through first-class functions.
	a.expandHigherOrderContracts(activeDecls)
	if os.Getenv("ELISA_DECL_TIMING") != "" {
		fmt.Fprintf(os.Stderr, "[decl-timing] decl phase %v\n", time.Since(declPhaseStart))
		if os.Getenv("ELISA_DECL_TIMING") == "exit" {
			pprof.StopCPUProfile()
			if path := os.Getenv("ELISACORE_MEMPROFILE"); path != "" {
				if f, err := os.Create(path); err == nil {
					runtime.GC()
					_ = pprof.Lookup("allocs").WriteTo(f, 0)
					f.Close()
				}
			}
			os.Exit(3)
		}
	}
	if probe := a.preBody; probe != nil {
		// A retained update's scratch run: only the declaration phase is wanted.
		probe.capture(a, activeDecls)
		return &Result{File: file}
	}
	if a.retained != nil {
		a.retained.pre = capturePreBody(a, activeDecls)
	}
	a.returnBorrowLateEnabled = true
	a.typeShapesFrozen = true
	if a.incrementalBodies != nil {
		bodies.prepareAnalyzer(a)
		bodies.prefetchLocalInitializerQueries()
	}
	if a.retained != nil {
		a.retained.perms.recordDeclared(a, activeDecls)
	}
	if setup != nil && setup.build != nil && a.retained == setup.build.retained {
		build := setup.build
		build.file, build.loweredFile = file, loweredFile
		a.retained.capture(a, activeDecls, nil)
		a.closeSMT()
		return &Result{File: file}
	}
	a.analyzeDecls(activeDecls)
	return a.finishProgramAnalysis(file, loweredFile, activeDecls, options, solver)
}

// finishProgramAnalysis is shared by ordinary analysis and an initial analysis
// resumed from a disk checkpoint. No partially analyzed Result is published.
func (a *Analyzer) finishProgramAnalysis(file, loweredFile *ast.File, activeDecls []scopedDecl, options AnalyzeOptions, solver func(*Analyzer, []scopedDecl)) *Result {
	a.runPostPhases(activeDecls, options, solver)
	return a.finishProgramResult(file, loweredFile, activeDecls, options, solver)
}

func (a *Analyzer) finishProgramResult(file, loweredFile *ast.File, activeDecls []scopedDecl, options AnalyzeOptions, solver func(*Analyzer, []scopedDecl)) *Result {
	if a.optimizationSymbolIdentityOverlay != nil {
		err := a.rejectOptimizationSymbolIdentityOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.storeScanBinderOverlay != nil {
		err := a.rejectStoreScanBinderOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.storeScanParamFactsOverlay != nil {
		err := a.rejectStoreScanParamFactsOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.noStorageOverlay != nil {
		err := a.rejectNoStorageOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.iterBinderElementOverlay != nil {
		err := a.rejectIterBinderElementOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.exprFactsOverlay != nil {
		err := a.rejectExprFactsOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	if a.exprTypeOverlay != nil {
		// Specialization publication has its own owned sparse Result. Never
		// silently materialize or publish a partial whole-program overlay.
		err := a.rejectExprTypeOverlayOperation("canonical program publication")
		return &Result{File: file, LoweredFile: loweredFile, Diagnostics: []Diagnostic{{Severity: DiagnosticSeverityError, Message: err.Error()}}}
	}
	a.publishLiveProofReport()
	if a.retained != nil {
		a.retained.capture(a, activeDecls, func(decls []scopedDecl, appended map[*ast.FuncDecl]*FuncType) {
			a.runPostPhases(decls, options, solver, appended)
		})
	}
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
	reassignReclaims := a.computeReassignReclaims(collectRegionPolyCandidateFuncs(activeDecls))
	a.closeSMT()
	diagnostics := publishedResultDiagnostics(a.diagnostics)
	if dumpRegionStacks {
		a.dumpRegionLifetimeSummary()
	}
	analysisResult := &Result{
		analyzer:                a,
		SMTProfile:              a.smtStats,
		EnforcePerfLints:        a.enforcePerfLints,
		File:                    file,
		LoweredFile:             loweredFile,
		GlobalScope:             a.globalScope,
		NamedTypes:              a.namedTypes,
		ModuleAliases:           a.moduleAliases,
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
		ContextualTypeBindings:  a.contextualTypeBindings,
		ResolvedValueNames:      a.resolvedValueNames,
		DenseNodeKeys:           a.exprDenseNodeKeys,
		NodeTables:              a.exprNodeTables,
		ParallelFor:             a.parallelForInfo,
		TaskGroupSubmits:        a.taskGroupSubmits,
		CallArgDisjoint:         a.callArgDisjoint,
		ReassignReclaims:        reassignReclaims,
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
		ElisionSummary:          a.currentElisionSummary(),
		RequiresReport:          a.requiresReportEntries(),
		Defer:                   a.deferInfo,
		Fold:                    a.foldInfo,
		Lambdas:                 a.lambdaInfo,
		FunctionAnalyses:        a.functionAnalyses,
		ProgressSummaries:       a.progressSummaries,
		AnnotatedFuncs:          a.annotatedFuncs,
		LawFuzzObligations:      a.lawFuzzObligations,
		ExportedTypes:           a.exportedTypes,
		ExportedFuncs:           a.exportedFuncs,
		ExportedGlobals:         a.exportedGlobals,
		Diagnostics:             diagnostics,

		PermissionEffectsConverged: a.permissionEffectsOutcome.Converged,
	}
	recordInternalParityCase(file, options, analysisResult)
	return analysisResult
}

// publishedResultDiagnostics gives the public result a stable empty-slice
// representation across cold and retained analysis paths.
func publishedResultDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	if len(diagnostics) == 0 {
		return nil
	}
	return diagnostics
}

// runPostPhases runs exactly the same ordered checks as resumable initial
// analysis, without stopping between phases.
func (a *Analyzer) runPostPhases(decls []scopedDecl, options AnalyzeOptions, solver func(*Analyzer, []scopedDecl), appended ...map[*ast.FuncDecl]*FuncType) {
	for phase := range analysisPostPhaseNames {
		a.runPostPhase(phase, decls, options, solver, appended...)
	}
}
