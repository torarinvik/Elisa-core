package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/lexer"
	"fmt"
)

type Diagnostic struct {
	Pos      lexer.Pos
	Severity DiagnosticSeverity
	Message  string
}

type DiagnosticSeverity string

const (
	DiagnosticSeverityError      DiagnosticSeverity = "error"
	DiagnosticSeverityWarning    DiagnosticSeverity = "warning"
	DiagnosticSeverityDeprecated DiagnosticSeverity = "deprecated"
)

func (d Diagnostic) String() string {
	if d.Severity == "" || d.Severity == DiagnosticSeverityError {
		return fmt.Sprintf("%s: %s", d.Pos, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Pos, d.Severity, d.Message)
}

type Result struct {
	// PermissionEffectsConverged is true only when the final effect work queue
	// drained. False includes an exhausted budget or a custom solver which did
	// not establish completion. Partial rows are unsuitable for retained summaries.
	PermissionEffectsConverged bool
	// SMTProfile reports the optional SMT discharge tier's cost (docs/90): query count, verdict
	// breakdown, and wall time. Zero-valued when the tier is off. Surfaced by `--explain` so "is SMT
	// cheap or demanding?" is answered with data.
	SMTProfile SMTStats
	// EnforcePerfLints echoes the -Wperf flag into the analysis result so the BACKEND (which only
	// learns vectorization outcomes post-LLVM-optimization) and its callers can promote the autovec
	// verifier's warnings to hard compile errors without a separate flag-threading path.
	EnforcePerfLints bool
	File             *ast.File
	LoweredFile      *ast.File
	GlobalScope      *Scope
	// ModuleAliases maps module-scoped alias names to canonical declaring modules
	// into lowering; the backend must not reconstruct them from source leaf names.
	ModuleAliases           map[string]string
	NamedTypes              map[string]Type
	StaticInterfaces        map[string]*StaticInterface
	StaticImpls             map[string]*StaticImpl
	ConstValues             map[string]ConstValue
	ExprTypes               map[ast.Expr]Type
	PermGrowthOps           map[ast.Expr]bool
	RewriteDefaults         map[*ast.Ident]bool
	OptionalBindSourceTypes map[*ast.OptionalBindExpr]Type
	InterfaceMethodRefs     map[*ast.FieldExpr]*InterfaceMethodRef
	SafeCalls               map[*ast.CallExpr]*SafeCallInfo
	ExprFacts               map[ast.Expr]OptimizationFacts
	CastHooks               map[ast.Expr]*Symbol
	InitCalls               map[*ast.StructLitExpr]*ast.CallExpr
	PostfixShorthandCalls   map[*ast.CastExpr]*ast.CallExpr
	RegionStacks            map[*ast.RegionStmt]RegionStackAssignment
	// DeathTimeCohorts / DeathTimeEscapeStats: docs/91 G0 inferred death cohorts and per-function
	// escape breakdown, populated when ELISA_DUMP_DEATHTIME is set or RecordDeathTimeCohorts is
	// requested (read-only observability of the global death-time model).
	DeathTimeCohorts     map[string][]DeathTimeCohort
	DeathTimeEscapeStats map[string]DeathTimeEscapeStats
	ResolvedTypeNames    map[ast.TypeExpr]string
	// ContextualTypeBindings preserves scoped receiver/associated-type resolution
	// for code generation. These names must never become global nominal types.
	ContextualTypeBindings map[ast.TypeExpr]Type
	ResolvedValueNames     map[*ast.Ident]string
	DenseNodeKeys          map[ast.Expr]DenseNodeKeyInfo
	NodeTables             map[ast.Expr]NodeTableInfo
	PackedLowering         PackedLoweringMetadata
	ParallelFor            map[*ast.ParallelForStmt]*ParallelForInfo
	// TaskGroupSubmits contains only safe-concurrency `submit` calls that the
	// analyzer proved are registered in a local TaskGroup and followed by its
	// matching wait on every normal path. The backend uses the group binding to
	// register each handle; lifetime analysis uses the same call identity.
	TaskGroupSubmits map[*ast.CallExpr]TaskGroupSubmitInfo
	CallArgDisjoint  map[*ast.CallExpr]*CallArgDisjointInfo
	// ReassignReclaims are `x <- f(..)` assignments whose target's previous darray buffer is
	// provably unreferenced (analyzer_reassign_reclaim.go); the backend returns it to its arena.
	ReassignReclaims   map[*ast.AssignStmt]bool
	FuncDisjointParams map[*ast.FuncDecl]*FuncDisjointParamInfo
	// LawIsCalls desugars a `subject is Law` predicate application (docs/85 §2: `is` = UFCS
	// first-arg binding) into the synthetic call `Law(subject)`. Analysis type-checks the call and
	// records it here; codegen emits the call for the `is` expression. Bare-law (no bracket args)
	// single-target form for now.
	LawIsCalls map[*ast.BinaryExpr]*ast.CallExpr
	// LemmaCalls marks statement-position calls that target a `lemma` (ghost code). They are
	// verification-only — the analyzer has already discharged the lemma's requires and injected its
	// (separately-proven) ensures as facts — so codegen must emit NOTHING for them.
	LemmaCalls map[*ast.CallExpr]bool
	// GhostDecls marks `ghost x: T = expr` verification-only local declarations. Like a lemma call,
	// a ghost var exists solely for the prover (it may appear in requires/ensure/invariant/assert);
	// codegen must emit NOTHING for it. The analyzer has already proven that no real value depends on
	// a ghost var, so erasing it cannot change runtime behavior.
	GhostDecls map[*ast.VarDeclStmt]bool
	// GhostContracts marks in-body contract conditions (`invariant`/`assert`) that READ a ghost
	// variable. They are verification-only — already discharged statically — so codegen must NOT emit
	// a runtime check for them (the ghost they read is erased and has no runtime storage).
	GhostContracts map[ast.Expr]bool
	// RefinementChecks holds the discharge obligations for a refinement-typed var decl (docs/85
	// Stage 1c-2): the predicate calls `P(x)` that must hold for the bound value. Codegen emits
	// them as a debug boundary check (trap on violation), elided in release — "debug verifies what
	// release assumes" — until the static prover (1d) elides the ones it can prove.
	RefinementChecks map[*ast.VarDeclStmt][]*ast.CallExpr
	// CallArgRefinementChecks holds the discharge obligations for an unproven, side-effect-free
	// argument passed to a refinement-typed parameter (docs/85: the function-contract boundary). Like
	// RefinementChecks but at the call site: codegen emits each predicate call as a debug boundary
	// check (trap on violation) before the real call, elided in release. Only side-effect-free args
	// (idents/literals) get a check, so re-evaluating the arg in the predicate is safe.
	CallArgRefinementChecks map[*ast.CallExpr][]*ast.CallExpr
	// ReturnRefinementChecks holds the discharge obligations for a return statement whose function has
	// a refinement-typed return (docs/85: the return half of the function-contract boundary). Codegen
	// emits each predicate call as a debug boundary check (trap on violation) before the return,
	// elided in release. Only side-effect-free return values get a check.
	ReturnRefinementChecks map[*ast.ReturnStmt][]*ast.CallExpr
	// IndexBoundsProven marks each `arr[i]` whose index the analyzer proved in-bounds (const index in
	// a fixed/static-length container, or a variable index with a proven upper bound + non-negativity).
	// The backend consults it to SKIP the debug bounds-check guard for that site — watchdog subsumption
	// (docs/85 §9.6, docs/86 86-3): a proven index disables the debug check, so a proven access is never
	// double-instrumented. Sound by construction: a missing/false entry always keeps the guard.
	IndexBoundsProven map[*ast.IndexExpr]bool
	// ProofReport records every refinement-discharge decision (docs/85: the "always known to the
	// user" observability layer). The CLI prints it under --explain so the user can audit exactly
	// what is statically guaranteed vs runtime-checked. Populated regardless of flags (it is small —
	// one entry per refinement obligation).
	ProofReport []ProofFact
	// ElisionSummary is the per-category proof-elision telemetry (docs/85 §elision-summary):
	// counts of statically-elided vs runtime-fallback checks, broken down by the four obligation
	// categories. Populated alongside ProofReport; printed under --explain as a one-liner summary
	// so the dogfooding payoff is immediately scannable ("N bounds checks elided").
	ElisionSummary ProofElisionSummary
	// RequiresReport is the docs c3 -requires-report aggregation: one entry per (requires-bearing
	// function, clause) with the provable/unprovable call-site split. Empty unless -requires-report
	// is on. The CLI prints it.
	RequiresReport    []RequiresReportEntry
	Defer             map[*ast.DeferStmt]*DeferInfo
	Fold              map[*ast.FoldExpr]*FoldInfo
	Lambdas           map[*ast.LambdaExpr]*LambdaInfo
	FunctionAnalyses  map[*ast.FuncDecl]*FunctionAnalysis
	ProgressSummaries map[*ast.FuncDecl]*FunctionProgressSummary
	AnnotatedFuncs    []*AnnotatedFunc
	// LawFuzzObligations are protocol laws (docs/85 P3) that could NOT be statically discharged for
	// a concrete impl (a non-affine `le` body the SMT tier declined, or SMT off). Each is auto-lowered
	// by the test runner into a per-impl `@property` randomized harness so the law is fuzz-checked in
	// debug/CI rather than silently assumed. A law PROVEN at the impl site produces no entry (zero cost).
	LawFuzzObligations []*LawFuzzObligation
	ExportedTypes      []*ExportedType
	ExportedFuncs      []*ExportedFunc
	ExportedGlobals    []*ExportedGlobal
	EASMModules        []*easm.Module
	Diagnostics        []Diagnostic
	// analyzer is retained so a generic body can be RE-ANALYZED per instantiation
	// (SpecializedExprTypes). Unexported: nothing outside this package may drive it.
	analyzer *Analyzer
	// shared is set while several code generators read this result at once
	// (BeginSharedCodegen); nil otherwise.
	shared                   *sharedCodegen
	specializationRoot       *Result
	specializationCanonical  map[*ast.FuncDecl]*ast.FuncDecl
	specializationCurrent    *ast.FuncDecl
	specializationNodes      map[ast.Node]ast.Node
	specializationOwned      map[ast.Node]bool
	specializationProofMask  map[*ast.IndexExpr]bool
	specializationSpanProofs map[*ast.IndexExpr]bool
	specializationQueries    *specializationQueries
}

// TaskGroupSubmitInfo is a positive proof attached to one discarded safe submit
// expression. GroupName identifies the same-scope TaskGroup local whose matching
// wait post-dominates the submission window.
type TaskGroupSubmitInfo struct {
	GroupName    string
	AlreadyAdded bool
}

func (r *Result) ActiveFile() *ast.File {
	if r == nil {
		return nil
	}
	if r.LoweredFile != nil {
		return r.LoweredFile
	}
	return r.File
}

// ActiveStaticStmtBranch returns the target-selected branch of a statement-level
// `static if`.  The backend and the semantic passes must agree about which branch
// is live; consumers that inspect the analyzed AST (for example the unsafe audit)
// must not walk every platform branch as if it were executable.
func (r *Result) ActiveStaticStmtBranch(stmt *ast.StaticIfStmt) []ast.Stmt {
	if r == nil || stmt == nil {
		return nil
	}
	if queries := r.specializationQueries; queries != nil {
		node, owned := r.specializationLookupNode(stmt)
		if owned {
			private, ok := node.(*ast.StaticIfStmt)
			if !ok {
				return nil
			}
			return queries.staticBranches[private]
		}
		// Canonical foreign queries lock their own root. Do not recursively
		// acquire that same shared-codegen mutex through this private Result.
		if queries.root != nil {
			return queries.root.ActiveStaticStmtBranch(stmt)
		}
		return nil
	}
	if r.analyzer == nil {
		return nil
	}
	defer r.lockAnalyzer()()
	return r.analyzer.activeStmtBranch(stmt)
}

type SafeCallInfo struct {
	ResolvedFuncName string
	ResolvedFuncType *FuncType
	TransformFunc    ast.Expr
	TransformArgs    []ast.Expr
	ReceiverArgIndex int
	ReceiverArgType  Type
	TailArgs         []ast.Expr
	ImplicitArgs     []ast.Expr
}

type ParallelForInfo struct {
	SourceType Type
	ItemType   Type
	Captures   []string
	// BandMode is set when the source is a mutable Slice[T]: the loop variable binds to a
	// disjoint Slice[T] BAND (one per worker) rather than to each element, and the body runs
	// once per band. BandElement is the band's element type T (for slice_band[T] lowering).
	BandMode    bool
	BandElement Type
	// BandSourceDisjoint is set (band mode only) when the writable band source and every
	// Slice/ReadView capture provably refer to DISJOINT buffers: each resolves to a distinct
	// fresh LOCAL allocation (not a parameter, whose buffer is opaque and could alias at the
	// caller). It is the soundness precondition for tagging band writes `noalias` the captured
	// view reads (the buffer-level restrict the backend emits under -fnoalias). False whenever
	// disjointness cannot be proven locally — conservatively giving up the optimization, never
	// miscompiling. DisjointViewCaptures lists the capture names proven disjoint from the band.
	BandSourceDisjoint   bool
	DisjointViewCaptures []string
}

// CallArgDisjointInfo records, for one call site, which container-reference arguments
// (`mutable darray[T]&` / `darray[T]&`) are provably backed by DISJOINT buffers. It is the
// general per-call lift of the band-disjointness fact (ParallelForInfo.BandSourceDisjoint):
// the `proven_distinct` predicate of docs/84 §3.1. Soundness is positive-evidence only — a
// pair is recorded distinct solely when both arguments resolve to distinct FRESH-LOCAL
// allocations (two distinct in-function buffers never share storage). Distinct parameters,
// self-aliasing (`f(&a,&a)`), and header-copy aliasing (`b=a; f(&a,&b)`) are NOT distinct and
// are omitted — conservatively giving up the optimization, never miscompiling. Increment 3
// (the backend) consumes this to stamp buffer-level alias.scope/noalias.
type CallArgDisjointInfo struct {
	// ContainerParams lists the param-aligned argument indices that are container references.
	ContainerParams []int
	// DistinctPairs holds the unordered index pairs {i,j} (i<j, both in ContainerParams) whose
	// argument buffers are provably disjoint at this call site.
	DistinctPairs map[[2]int]bool
	// SelfNoalias[i] is true when container arg i is proven distinct from EVERY other container
	// arg at this call. NOTE for Increment 3: a sound `noalias` stamp on the callee body also
	// requires that body to touch no other aliasing memory (globals / captured buffers); the
	// backend must AND this call-site bit with that body fact before emitting noalias.
	SelfNoalias map[int]bool
}

func (info *CallArgDisjointInfo) PairDistinct(i, j int) bool {
	if info == nil || info.DistinctPairs == nil {
		return false
	}
	if i > j {
		i, j = j, i
	}
	return info.DistinctPairs[[2]int{i, j}]
}

// FuncDisjointParamInfo lifts the per-call `proven_distinct` predicate to a whole-program,
// per-callee fact: which container-reference parameter pairs of a function are distinct at
// EVERY call site (docs/84 §3.2, Increment 3a). The backend stamps buffer-level
// alias.scope/noalias on element accesses inside the callee body — a stamp that is sound only
// if no caller ever passes aliasing buffers for the pair. The intersection across all call
// sites is what makes that whole-program guarantee.
//
// SOUNDNESS — completeness of call-site capture. Stamping the callee body requires observing
// ALL of its calls. This is guaranteed only for a function that (a) has a program-unique name
// (so each call's by-name resolution is unambiguous) and (b) is never address-taken (so every
// call is a direct by-name call the analyzer sees — a function value could be invoked from an
// unobserved site). A function failing either gate is excluded outright (no entry), forgoing
// the optimization. Methods, overloaded names, and generic/specialized kernels currently fall
// under these exclusions; whole-array monomorphic free-function kernels (AXPY, Jacobi sweeps,
// serial Stable-Fluids fields — the docs/84 §3.5 census majority) are the served case.
type FuncDisjointParamInfo struct {
	// DistinctPairs holds the unordered param index pairs {i,j} (i<j) that are proven distinct
	// at every observed call site — the intersection of the per-call DistinctPairs.
	DistinctPairs map[[2]int]bool
	// SelfNoalias[i] is true when param i is proven distinct from every other container param at
	// every observed call site (the per-call SelfNoalias intersection). It is the precondition
	// for the loop-access-analysis memcheck elision, still subject to the backend's
	// no-other-aliasing-memory body check before a noalias stamp.
	SelfNoalias map[int]bool
}

func (info *FuncDisjointParamInfo) PairDistinct(i, j int) bool {
	if info == nil || info.DistinctPairs == nil {
		return false
	}
	if i > j {
		i, j = j, i
	}
	return info.DistinctPairs[[2]int{i, j}]
}

type DeferInfo struct {
	Mode     ast.DeferMode
	Captures []string
}

type FoldInfo struct {
	Captures []string
}

type LambdaInfo struct {
	Captures []string
}

type DenseNodeKeyInfo struct {
	Enum      *EnumType
	StoreRoot *Symbol
	StorePath string
}

type NodeTableInfo struct {
	Enum      *EnumType
	Elem      Type
	StoreRoot *Symbol
	StorePath string
	CountExpr string
}

type PackedLoweringMetadata struct {
	Contract                          string
	CanonicalPackedLowering           string
	OnePackedEnumOneHandleInvariant   bool
	PublicationReadonlyGateStoreState string
}

type AnnotatedFunc struct {
	Name        string
	Annotations []ast.Annotation
	Signature   *FuncType
	Decl        *ast.FuncDecl
}

type ExportedType struct {
	PublicName string
	Type       Type
	Decl       *ast.ExportTypeDecl
}

type ExportedFunc struct {
	PublicName string
	// LinkName is the optional linker spelling from @link_name on the export
	// declaration. PublicName remains the source/API name; the backend uses
	// LinkName for the emitted symbol when a target ABI needs punctuation such
	// as a WIT component export name.
	LinkName          string
	Signature         *FuncType
	TargetName        string
	TargetBase        *FuncType
	TargetSpecialized *FuncType
	TargetGenericDecl *ast.FuncDecl
	TargetBindings    map[string]Type
	Decl              *ast.ExportFuncDecl
}

type ExportedGlobal struct {
	PublicName string
	Type       Type
	TargetName string
	TargetKind SymbolKind
	Mutable    bool
	Decl       *ast.ExportGlobalDecl
}

func (r *Result) Errors() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		if d.Severity != "" && d.Severity != DiagnosticSeverityError {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) FunctionAnalysis(decl *ast.FuncDecl) (*FunctionAnalysis, bool) {
	if r == nil || decl == nil || r.FunctionAnalyses == nil {
		return nil, false
	}
	if root := r.specializationRoot; root != nil {
		// Canonical declaration queries for this instantiation resolve to its
		// concrete analysis. Other canonical program queries keep their root
		// authority, even if a private quiet/read view exists for that callee.
		if current := r.specializationCurrent; current != nil && r.specializationCanonical[current] == decl {
			decl = current
		} else if _, private := r.specializationCanonical[decl]; !private {
			return root.FunctionAnalysis(decl)
		}
	}
	analysis, ok := r.LookupFunctionAnalyses(decl)
	return analysis, ok
}

func (r *Result) FunctionAnalysisByName(name string) (*FunctionAnalysis, bool) {
	if r == nil || name == "" || r.GlobalScope == nil {
		return nil, false
	}
	sym, ok := r.GlobalScope.Lookup(name)
	if !ok {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		return nil, false
	}
	return r.FunctionAnalysis(decl)
}

func (r *Result) Warnings() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		if d.Severity != DiagnosticSeverityWarning {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) Deprecations() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		if d.Severity != DiagnosticSeverityDeprecated {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) Notices() []string {
	out := r.Warnings()
	out = append(out, r.Deprecations()...)
	return out
}
