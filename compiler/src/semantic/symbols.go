package semantic

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/lexer"
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
	File                    *ast.File
	LoweredFile             *ast.File
	GlobalScope             *Scope
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
	ResolvedTypeNames       map[ast.TypeExpr]string
	ResolvedValueNames      map[*ast.Ident]string
	DenseNodeKeys           map[ast.Expr]DenseNodeKeyInfo
	NodeTables              map[ast.Expr]NodeTableInfo
	PackedLowering          PackedLoweringMetadata
	ParallelFor             map[*ast.ParallelForStmt]*ParallelForInfo
	CallArgDisjoint         map[*ast.CallExpr]*CallArgDisjointInfo
	FuncDisjointParams      map[*ast.FuncDecl]*FuncDisjointParamInfo
	// LawIsCalls desugars a `subject is Law` predicate application (docs/85 §2: `is` = UFCS
	// first-arg binding) into the synthetic call `Law(subject)`. Analysis type-checks the call and
	// records it here; codegen emits the call for the `is` expression. Bare-law (no bracket args)
	// single-target form for now.
	LawIsCalls map[*ast.BinaryExpr]*ast.CallExpr
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
	// ProofReport records every refinement-discharge decision (docs/85: the "always known to the
	// user" observability layer). The CLI prints it under --explain so the user can audit exactly
	// what is statically guaranteed vs runtime-checked. Populated regardless of flags (it is small —
	// one entry per refinement obligation).
	ProofReport []ProofFact
	Defer                   map[*ast.DeferStmt]*DeferInfo
	Fold                    map[*ast.FoldExpr]*FoldInfo
	Lambdas                 map[*ast.LambdaExpr]*LambdaInfo
	FunctionAnalyses        map[*ast.FuncDecl]*FunctionAnalysis
	ProgressSummaries       map[*ast.FuncDecl]*FunctionProgressSummary
	AnnotatedFuncs          []*AnnotatedFunc
	ExportedTypes           []*ExportedType
	ExportedFuncs           []*ExportedFunc
	ExportedGlobals         []*ExportedGlobal
	EASMModules             []*easm.Module
	Diagnostics             []Diagnostic
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
	PublicName        string
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
	analysis, ok := r.FunctionAnalyses[decl]
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

type SymbolKind string

const (
	SymbolConst      SymbolKind = "const"
	SymbolGlobal     SymbolKind = "global"
	SymbolFunc       SymbolKind = "func"
	SymbolExternFunc SymbolKind = "extern-func"
	SymbolExternVar  SymbolKind = "extern-var"
	SymbolStruct     SymbolKind = "struct"
	SymbolExternType SymbolKind = "extern-type"
	SymbolParam      SymbolKind = "param"
	SymbolLocal      SymbolKind = "local"
	SymbolRegion     SymbolKind = "region"
	SymbolRegionMark SymbolKind = "region-mark"
	SymbolCheckpoint SymbolKind = "checkpoint"
)

type Symbol struct {
	Name       string
	Kind       SymbolKind
	Type       Type
	Node       ast.Node
	LinkName   string
	AliasOf    *Symbol
	ParamIndex int
	Mutable    bool
	Private    bool
	// Deprecated, when non-empty, is the `@deprecated("...")` message; calling this
	// function emits a deprecation diagnostic at the use site.
	Deprecated string
}

func symbolAliasRoot(sym *Symbol) *Symbol {
	for sym != nil && sym.AliasOf != nil {
		sym = sym.AliasOf
	}
	return sym
}

type Scope struct {
	Parent                  *Scope
	Symbols                 map[string]*Symbol
	Refinements             map[string]Type
	ConditionalBindingHints map[string]string
	// rangeFacts holds known integer bounds for IMMUTABLE variables within this scope, gathered
	// from a branch condition (`if a > 5` → a ∈ [6,∞)). Used by the refinement flow prover (docs/85
	// 1d-2) to discharge value refinements statically. Immutable-only, so no invalidation is needed.
	rangeFacts map[string]numRange
	// predFacts holds bare law-predicates known to hold on a variable within this scope (var name →
	// set of law names), gained from a flow narrowing (`if x is P:`). Unlike rangeFacts these MAY
	// cover mutable variables, because they are invalidated at every mutation site (docs/85: mutable
	// refinement flow — see analyzer_refinement_predfacts.go).
	predFacts map[string]map[string]bool
	// writtenConst records the last value written to a variable when that value is a compile-time
	// integer constant (`x <- 0`, `n = 5`) — including writes through a non-aliased `mutable T&`
	// param's pointee. Used to prove/refute a refinement obligation on the variable (e.g. an
	// `ensures p is Positive` postcondition after `p <- 1`). Invalidated at every mutation site
	// alongside predFacts, so it can never go stale; const-only RHS keeps re-evaluation stable.
	writtenConst map[string]int64
}

func NewScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Symbols: map[string]*Symbol{}, Refinements: map[string]Type{}, ConditionalBindingHints: map[string]string{}}
}

func (s *Scope) Define(sym *Symbol) (*Symbol, bool) {
	if existing, ok := s.Symbols[sym.Name]; ok {
		return existing, false
	}
	s.Symbols[sym.Name] = sym
	return sym, true
}

func (s *Scope) Lookup(name string) (*Symbol, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if sym, ok := cur.Symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

func (s *Scope) LookupRefinement(key string) (Type, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if t, ok := cur.Refinements[key]; ok {
			return t, true
		}
	}
	return nil, false
}

func (s *Scope) LookupConditionalBindingHint(name string) (string, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if hint, ok := cur.ConditionalBindingHints[name]; ok {
			return hint, true
		}
	}
	return "", false
}
