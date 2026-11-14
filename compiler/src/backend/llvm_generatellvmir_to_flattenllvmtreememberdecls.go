//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"unsafe"
)

// GenerateLLVMIR lowers the analyzed program into an LLVM module using the LLVM C API
// and returns the textual IR produced by LLVM itself.
func GenerateLLVMIR(result *semantic.Result) (string, error) {
	return GenerateLLVMIRWithOpt(result, OptimizationLevel0)
}

// GenerateLLVMIRWithOpt lowers the analyzed program and optionally optimizes the
// module before returning textual LLVM IR.
func GenerateLLVMIRWithOpt(result *semantic.Result, optLevel OptimizationLevel) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, DefaultPackedLoweringProfile())
}
func GenerateLLVMIRWithOptAndPackedLoweringProfile(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, optLevel, profile, "")
}

func GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result, optLevel, profile, targetTriple, false)
}

// GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug is like the non-Debug
// variant but emits DWARF debug-info metadata into the IR when debugInfo is true, so
// the textual-IR -> llc native build path produces a symbolized (attachable) binary.
func GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, false)
}

// GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace additionally emits
// -ftrace instrumentation when traceInfo is true, so the native build records the
// execution trace.
func GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, error) {
	output, _, err := GenerateLLVMIRWithWarnings(result, optLevel, profile, targetTriple, debugInfo, traceInfo)
	return output, err
}

// GenerateLLVMIRWithWarnings is the full-arg lowering entry point that, in addition to the IR text,
// returns any performance-friction warnings collected during optimized codegen (the
// auto-vectorization verifier — docs/79 Part IV). The slice is empty at -O0 or when every eligible
// loop vectorized. Build/emit drivers surface these to the user; the thinner overloads that don't
// thread warnings out delegate here and discard them.
func GenerateLLVMIRWithWarnings(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, []string, error) {
	output, warnings, err := generateLLVMIRWithWarnings(result, optLevel, profile, targetTriple, debugInfo, traceInfo)
	recordBackendParityCase(result, optLevel, err)
	return output, warnings, err
}

func generateLLVMIRWithWarnings(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, []string, error) {
	if result != nil {
		// Keep canonical syntax membership and specialization products for this
		// lowering, as the partitioned object path does.
		result.BeginSharedCodegen()
		defer result.EndSharedCodegen()
	}
	g, err := compileLLVMModuleWithTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, traceInfo)
	if err != nil {
		return "", nil, err
	}
	defer g.dispose()
	// Give the module its datalayout and triple BEFORE printing. ensureTargetMachine is
	// what sets both (LLVMSetDataLayout/LLVMSetTarget), and the textual-IR path never
	// called it: optimizeModule returns early at -O0, and writeObjectFile is a different
	// entry point. The header therefore appeared only when some incidental path happened to
	// need the DataLayout — a program using pointer arithmetic got one, `def main() -> i64:
	// return 7` did not — so `-emit llvm` output was NONDETERMINISTIC in a way no exit code
	// could show.
	//
	// It is also wrong, not merely inconsistent: IR without a datalayout is interpreted with
	// LLVM's defaults, so a module emitted under `-target-triple` carried neither the
	// requested triple nor its layout, and any downstream opt/llc would size pointers and
	// align types for the wrong target.
	if err := g.ensureTargetMachine(); err != nil {
		return "", nil, err
	}
	if err := g.optimizeModule(optLevel); err != nil {
		return "", nil, err
	}
	output := g.printModule()
	if err := validateSegmentAgnosticLLVMIR(output); err != nil {
		return "", nil, err
	}
	return output, g.PerfWarnings(), nil
}
func compileLLVMModule(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (*llvmGenerator, error) {
	return compileLLVMModuleWithTarget(result, optLevel, profile, "")
}

func compileLLVMModuleWithTarget(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string) (*llvmGenerator, error) {
	return compileLLVMModuleWithTargetAndDebug(result, optLevel, profile, targetTriple, false)
}

func compileLLVMModuleWithTargetAndDebug(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool) (*llvmGenerator, error) {
	return compileLLVMModuleWithTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, false)
}

func compileLLVMModuleWithTargetDebugTrace(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (*llvmGenerator, error) {
	return compileLLVMModuleConfigured(result, optLevel, profile, targetTriple, debugInfo, traceInfo, nil)
}

// compileLLVMModuleConfigured is compileLLVMModuleWithTargetDebugTrace with a
// hook that adjusts the generator before any IR is emitted.
func compileLLVMModuleConfigured(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, configure func(*llvmGenerator), shared ...*sharedCodegenLookups) (*llvmGenerator, error) {
	g, err := newLLVMGenerator(result, shared...)
	if err != nil {
		return nil, err
	}
	if configure != nil {
		configure(g)
	}
	if g.inputReads != nil {
		// newLLVMGenerator builds symbolsByNode before its configuration hook.
		// Re-observe the exact source domain once the unit recorder is attached.
		g.codegenReadView().GlobalSymbolsMap("generator-symbols-by-node-input")
	}
	g.emitDebugInfo = debugInfo
	g.emitTrace = traceInfo
	// ELISACORE_FORCE_BOUNDS_CHECK forces the debug index-bounds watchdog on even in
	// optimized builds (it is otherwise -O0 only). Set by the `-fbounds-check` CLI flag.
	// Lets release/cross builds (e.g. the CUSA07399 emulator) trap at the offending
	// indexing site instead of crashing later on a derived bad pointer.
	g.forceBoundsCheck = os.Getenv("ELISACORE_FORCE_BOUNDS_CHECK") != ""
	// Value-contract preconditions (`requires`) are emitted in debug builds (-O0); this env forces
	// them on at any opt level (parallel to ELISACORE_FORCE_BOUNDS_CHECK) for testing release paths.
	g.forceContracts = os.Getenv("ELISACORE_FORCE_CONTRACTS") != ""
	// ELISACORE_FAST_MATH opts the whole program into full fast-math FP (reassociation,
	// reciprocals, no-nan/no-inf) — clang's -ffast-math. Set by the `-ffast-math` CLI flag.
	// This applies full fast-math (incl. no-nan/no-inf/no-signed-zero) program-wide, so it stays an
	// explicit opt-in. Comprehension folds already reassociate into a vectorizable tree by default
	// (reassoc+contract only); -ffast-math and `@fast_math` are the broader, value-changing opt-ins.
	g.globalFastMath = os.Getenv("ELISACORE_FAST_MATH") != ""
	// ELISACORE_STRICT_FP is the opposite opt-in: emit FP arithmetic with NO fast-math flags at
	// all, so `a + b * c` stays two roundings rather than becoming an fma. Contraction is on by
	// default here (clang's -ffp-contract=on) and is value-changing wherever the add cancels --
	// `-0.35 + 0.7 * r` differs from `fma(0.7, r, -0.35)` by an ulp or two. That is invisible in
	// most code and fatal in the one place it matters: reproducing another implementation's FP
	// results bit-for-bit. Off by default, since strict FP costs the fma.
	g.strictFP = os.Getenv("ELISACORE_STRICT_FP") != ""
	// ELISACORE_NOALIAS_MUTABLE_REFS (set by the `-fnoalias` CLI flag) stamps LLVM `noalias`
	// on the eligible subset of `mutable T&` params (scalar/darray pointee). OFF by default:
	// a noalias miscompile is silent, so it stays an explicit opt-in. NOTE (empirically
	// measured): sound but currently low-value at O3 — darray header-pointer noalias does not
	// cover the SROA'd inner data buffer, and scalar refs are arg-promoted by value before it
	// matters. Real kernel-vectorization wins need buffer-level alias.scope metadata.
	g.noaliasMutableRefs = os.Getenv("ELISACORE_NOALIAS_MUTABLE_REFS") != ""
	g.profileHotFunctions = profileHotFunctionsFromEnvironment()
	g.optLevel = optLevel
	g.packedProfile = profile
	g.packedEnumABI = profile.packedModeForStore(nil)
	g.requestedTargetTriple = targetTriple
	// BEFORE emitModule: every struct layout and index computation below reads
	// g.wordBits, and it is seeded from the HOST's pointer size.
	if err := g.syncWordBitsWithTarget(); err != nil {
		g.dispose()
		return nil, err
	}
	if g.result != nil && g.partBody == nil {
		// Partition generators share the result; their caller records this once.
		g.result.PackedLowering = profile.metadata()
	}
	// Partition generators require stable external names for body ownership.
	// Their finishing step gives non-owner definitions weak ODR linkage.
	g.preferPrivateLinkage = optLevel != OptimizationLevel0 && g.partBody == nil
	if err := g.emitModule(); err != nil {
		g.dispose()
		return nil, err
	}
	g.alignPackedRecordAccesses()
	g.appendAppleX64SegmentReservations(targetTriple)
	// Ordinary partition builds verify after finalization and exact bitcode-cache
	// lookup. A hit can reuse only an object retained after that same finalized
	// bitcode passed verification on an earlier miss. Declines and all other
	// callers keep the eager verifier below.
	if !g.deferPartitionCacheVerification || g.partUnsafeHelpers {
		if err := g.verify(); err != nil {
			g.dispose()
			return nil, err
		}
	}
	return g, nil
}

type llvmGenerator struct {
	inputReads                      *irInputReadRecorder // optional diagnostic trace; never cache authority
	deferPartitionCacheVerification bool
	verifyObserver                  func() // optional test observation of verifier calls
	partitionImportWeight           int
	partitionImportDefaults         bool
	// partitionModuleDecls is a build-local worklist for partitioned IR. A
	// non-owner function body is absent because partition generation has already
	// assigned it to its owner; all shared declarations remain present.
	partitionModuleDecls    []ast.Decl
	hasPartitionModuleDecls bool
	hasPackedRecords        bool // emitted a StructLayoutPacked body
	// partBody, when set, limits this generator to one partition of the
	// program: a top-level external function's body is emitted only when
	// partBody accepts its symbol name (see llvm_partition_ir.go).
	partBody func(name string) bool
	// Source globals are defined by partition zero. Other partitions keep
	// declarations, while on-demand generated storage retains its own linkage.
	partitionGlobalDeclarationsOnly bool
	// partitionSpecializationQueue coordinates O0 body ownership across
	// independently generated partition modules. Supplemental owner modules
	// contain only explicitly queued specialization bodies.
	partitionSpecializationQueue  *partitionSpecializationQueue
	partitionSpecializationOnly   bool
	partitionSpecializationParts  int
	partSpecializationDefinitions map[string]bool
	// sharedResult is set when other generators read the same Result
	// concurrently; this generator then writes only its own overlays.
	sharedResult bool
	bodyDepth    int
	// exprOverlay and boundsOverlay hold this generator's records for the
	// expressions it synthesizes when the Result is shared (llvm_partition_ir.go).
	sharedLookups    *sharedCodegenLookups
	canonicalLookups *sharedCodegenLookups
	exprOverlay      map[ast.Expr]semantic.Type
	boundsOverlay    map[*ast.IndexExpr]bool
	// Last-segment indexes for the unique-suffix name fallbacks (see
	// namedTypeSuffixIndex); rebuilt when the indexed map's size changes.
	namedTypeSuffixes      map[string][]string
	namedTypeSuffixesSize  int
	globalSuffixes         map[string][]string
	globalSuffixesSize     int
	provenIndexSpans       map[lexer.Pos]bool
	provenIndexSpansSize   int
	provenIndexSpansResult *semantic.Result
	// panicReportFn is the module's outlined panic reporter (llvm_panic_report.go), created lazily.
	panicReportFn             llvmIRValueRef
	sameNameWrapperDecisions  map[*semantic.ExportedFunc]bool
	result                    *semantic.Result
	optLevel                  OptimizationLevel
	context                   llvmIRContextRef
	module                    llvmIRModuleRef
	targetMachine             llvmIRTargetMachineRef
	targetData                llvmIRTargetDataRef
	targetTriple              *llvmIRChar
	targetTripleOwnedByLLVM   bool
	requestedTargetTriple     string
	optimizedForCodegen       bool
	preferPrivateLinkage      bool
	packedProfile             PackedLoweringProfile
	packedEnumABI             packedEnumABIMode
	symbolsByNode             map[ast.Node]*semantic.Symbol
	easmLayoutNames           map[string]bool
	structTypes               map[string]llvmIRTypeRef
	structBodies              map[string]bool
	functionTypes             map[*semantic.FuncType]llvmIRTypeRef
	runtimeHelperTypes        map[string]*semantic.FuncType
	packedViewTypes           map[*semantic.EnumVariant]*semantic.PackedVariantViewType
	packedVariantPayloadTypes map[*semantic.EnumVariant]llvmIRTypeRef
	commonFieldLayouts        map[packedEnumCommonFieldLayoutCacheKey]*packedEnumCommonFieldLayout
	specializedFuncTypes      map[string]*semantic.FuncType
	genericSymbolKeys         map[string]string
	functions                 map[string]llvmIRValueRef
	globals                   map[string]llvmIRValueRef
	constEvalScopes           []map[string]semantic.ConstValue
	staticCallDepth           int
	noteTypeInProgress        map[typeMemoKey]bool
	noteTypeDone              map[typeMemoKey]bool
	cachedVoidRefType         *semantic.RefType
	cachedArenaRefType        *semantic.RefType
	syntheticCounter          int
	partSynthNS               string
	partLocalHelpers          map[string]bool
	partUnsafeHelpers         bool
	partSyntheticDepth        int
	partSubmitTypes           submitTypeProvenance
	wordBits                  int
	emitDebugInfo             bool
	di                        *debugInfo
	emitTrace                 bool
	trace                     *traceState
	forceBoundsCheck          bool
	forceContracts            bool
	globalFastMath            bool
	strictFP                  bool
	noaliasMutableRefs        bool
	profileHotFunctions       map[string]bool
	perfWarnings              []string
	transientCStringLifetime  bool
}
type typeMemoKey struct {
	id  semantic.TypeID
	typ semantic.Type
}

func noteTypeKeyFor(t semantic.Type) typeMemoKey {
	if id, ok := semantic.TryCanonicalTypeID(t); ok {
		return typeMemoKey{id: id}
	}
	return typeMemoKey{typ: t}
}

// typeMemoKey is noteTypeKeyFor, shared across the partitions of one result.
func (g *llvmGenerator) typeMemoKey(t semantic.Type) typeMemoKey {
	shared := g.sharedLookups
	if shared != nil {
		shared = shared.programLookups()
	}
	if shared == nil || reflect.TypeOf(t).Kind() != reflect.Pointer {
		return noteTypeKeyFor(t)
	}
	if key, ok := shared.typeKeys.Load(t); ok {
		return key.(typeMemoKey)
	}
	key := noteTypeKeyFor(t)
	shared.typeKeys.Store(t, key)
	return key
}

func newLLVMGenerator(result *semantic.Result, shared ...*sharedCodegenLookups) (*llvmGenerator, error) {
	if result == nil {
		return nil, fmt.Errorf("backend requires a semantic result with file and global scope")
	}
	activeFile := result.ActiveFile()
	if activeFile == nil || !newIRCodegenReadView(result, nil).HasGlobalScope() {
		return nil, fmt.Errorf("backend requires a semantic result with file and global scope")
	}
	ctx := llvmContextCreate()
	// LLVM's module identifier is not semantic source metadata.  Keeping the
	// caller's absolute path here made object code depend on whether the same
	// source was invoked as `src/file.elisa` or `/abs/.../src/file.elisa` on
	// Darwin/AArch64.  The generated IR differed only in its module header, but
	// LLVM's backend then took different machine-code paths and one variant
	// could miscompile.  Use the stable basename for the module identity; source
	// locations and compiler diagnostics still retain activeFile.Filename.
	moduleID := filepath.Base(activeFile.Filename)
	if moduleID == "." || moduleID == "" {
		moduleID = "module.elisa"
	}
	moduleName := cString(moduleID)
	defer llvmFree(unsafe.Pointer(moduleName))
	mod := llvmModuleCreateWithNameInContext(moduleName, ctx)
	g := &llvmGenerator{
		result:                    result,
		context:                   ctx,
		module:                    mod,
		packedProfile:             DefaultPackedLoweringProfile(),
		packedEnumABI:             packedEnumABIVariantSparse,
		symbolsByNode:             map[ast.Node]*semantic.Symbol{},
		structTypes:               map[string]llvmIRTypeRef{},
		structBodies:              map[string]bool{},
		functionTypes:             map[*semantic.FuncType]llvmIRTypeRef{},
		runtimeHelperTypes:        map[string]*semantic.FuncType{},
		packedViewTypes:           map[*semantic.EnumVariant]*semantic.PackedVariantViewType{},
		packedVariantPayloadTypes: map[*semantic.EnumVariant]llvmIRTypeRef{},
		commonFieldLayouts:        map[packedEnumCommonFieldLayoutCacheKey]*packedEnumCommonFieldLayout{},
		specializedFuncTypes:      map[string]*semantic.FuncType{},
		functions:                 map[string]llvmIRValueRef{},
		globals:                   map[string]llvmIRValueRef{},
		noteTypeInProgress:        map[typeMemoKey]bool{},
		noteTypeDone:              map[typeMemoKey]bool{},
		wordBits:                  int(unsafe.Sizeof(uintptr(0)) * 8),
	}
	beginModuleCStringLifetime()
	g.transientCStringLifetime = true
	if len(shared) != 0 && shared[0] != nil && shared[0].result == result {
		g.sharedLookups = shared[0]
		g.symbolsByNode = shared[0].symbolsByNode
	} else {
		g.symbolsByNode = codegenSymbolsByNode(result)
	}
	return g, nil
}
func (g *llvmGenerator) packedModeForEnum(enumType *semantic.EnumType) packedEnumABIMode {
	if g == nil {
		return packedEnumABIVariantSparse
	}
	return g.packedProfile.packedModeForPackedEnum(enumType)
}
func (g *llvmGenerator) packedLoweringForStore(storeType *semantic.PackedEnumStoreType) packedEnumABIMode {
	if g == nil {
		return packedEnumABIVariantSparse
	}
	return g.packedProfile.packedModeForStore(storeType)
}
func (g *llvmGenerator) cachedRuntimeHelperType(name string, build func() *semantic.FuncType) *semantic.FuncType {
	if g == nil {
		return build()
	}
	if cached := g.runtimeHelperTypes[name]; cached != nil {
		return cached
	}
	helperType := build()
	g.runtimeHelperTypes[name] = helperType
	return helperType
}
func (g *llvmGenerator) usesCanonicalPackedLowering() bool {
	if g == nil {
		return false
	}
	return !g.packedProfile.hasExplicitMode
}
func (g *llvmGenerator) nextSyntheticName(prefix string) string {
	name := fmt.Sprintf("%s%d", prefix, g.syntheticCounter)
	g.syntheticCounter++
	return name
}

// nextSyntheticHelperName scopes generated global helpers to their IR partition.
// A counter alone can identify different bodies in different LLVM modules.
func (g *llvmGenerator) nextSyntheticHelperName(prefix string) string {
	return g.partSynthNS + g.nextSyntheticName(prefix)
}
func (g *llvmGenerator) dispose() {
	if g.di != nil {
		g.di.dispose()
		g.di = nil
	}
	if g.targetData != nil {
		llvmDisposeTargetData(g.targetData)
	}
	if g.targetMachine != nil {
		llvmDisposeTargetMachine(g.targetMachine)
	}
	if g.targetTriple != nil {
		if g.targetTripleOwnedByLLVM {
			llvmDisposeMessage(g.targetTriple)
		} else {
			llvmFree(unsafe.Pointer(g.targetTriple))
		}
	}
	if g.module != nil {
		llvmDisposeModule(g.module)
	}
	if g.context != nil {
		llvmContextDispose(g.context)
	}
	if g.transientCStringLifetime {
		endModuleCStringLifetime()
		g.transientCStringLifetime = false
	}
}

// disposeInBackground releases the generator like dispose, then queues module
// and context teardown on the current object build's bounded cleanup scope. The
// scope is joined before WriteObject returns so retained requests cannot leave
// unbounded LLVM allocations behind. Each context is private to this generator.
func (g *llvmGenerator) disposeInBackground(scope *llvmDisposalScope) {
	module, context := g.module, g.context
	// LLVM may retain names until its context is destroyed. Transfer the pool
	// reference to the cleanup instead of releasing it before module teardown.
	retainCStringLifetime := g.transientCStringLifetime
	g.transientCStringLifetime = false
	g.module, g.context = nil, nil
	g.dispose()
	if module == nil && context == nil {
		if retainCStringLifetime {
			endModuleCStringLifetime()
		}
		return
	}
	scope.schedule(func() {
		if module != nil {
			llvmDisposeModule(module)
		}
		if context != nil {
			llvmContextDispose(context)
		}
		if retainCStringLifetime {
			endModuleCStringLifetime()
		}
	})
}
