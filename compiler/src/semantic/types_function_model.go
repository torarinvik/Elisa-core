package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type FuncInlineMode string

const (
	FuncInlineModeDefault FuncInlineMode = ""
	FuncInlineModeAlways  FuncInlineMode = "always"
	FuncInlineModeNever   FuncInlineMode = "never"
)

type FuncTemperatureMode string

const (
	FuncTemperatureModeDefault FuncTemperatureMode = ""
	FuncTemperatureModeHot     FuncTemperatureMode = "hot"
	FuncTemperatureModeCold    FuncTemperatureMode = "cold"
)

type FuncGuardKind string

const (
	FuncGuardKindNonNull       FuncGuardKind = "nonnull"
	FuncGuardKindPackedVariant FuncGuardKind = "packed_variant"
)

type FuncGuardEffect struct {
	Kind        FuncGuardKind
	ParamIndex  int
	EnumName    string
	VariantName string
}

type FuncPoststateKind int

const (
	FuncPoststateKindNamedState FuncPoststateKind = iota
	FuncPoststateKindRefState
	FuncPoststateKindPreserve
)

type FuncPoststateConditionKind int

const (
	FuncPoststateConditionAlways FuncPoststateConditionKind = iota
	FuncPoststateConditionReturnBool
)

type FuncPoststateCondition struct {
	Kind       FuncPoststateConditionKind
	ReturnBool bool
}

type FuncPoststate struct {
	Position   lexer.Pos
	Condition  FuncPoststateCondition
	ParamIndex int
	Path       []borrowReturnAnnotationStep
	Kind       FuncPoststateKind
	StateCases []string
	RefState   RefState
	// Implicit marks a poststate the analyzer synthesized rather than one the user wrote. It is set on
	// the implicit `preserve` given to a `mutable T[S]&` parameter with a single declared state and no
	// explicit `ensures` (strict protocol balance): a borrowed stateful resource must be handed back in
	// the state it was lent in unless the signature says otherwise. Drives a tailored diagnostic.
	Implicit bool
}

// RefinementEnsure is a function postcondition of the form `ensures <param> is <BareLaw>`
// (docs/85, mutable refinement flow brick 2). It is a lightweight predicate-fact channel kept
// separate from FuncPoststate (which tracks named/ref states via the type system): at a call the
// caller GAINS the predicate fact on the argument bound to ParamIndex; at the callee's returns the
// postcondition is checked (debug runtime check + static attempt) so the caller's assumption is
// backed. Bare laws over a bare parameter only for now.
// FrameParamWrite is one place a callee may write through one of its reference parameters (docs/87
// 87-3): the parameter's index plus the field suffix beneath it. Index steps are dropped (field
// granularity), matching framePath. An empty Fields means the whole parameter place.
type FrameParamWrite struct {
	ParamIndex int
	Fields     []string
}

type RefinementEnsure struct {
	Position   lexer.Pos
	ParamIndex int
	LawName    string
	// Args are the constant bracket arguments of a parametric postcondition (`ensures x is
	// Bounded[0, 500]`); nil for a bare law. Validated const-evaluable at resolution time so both the
	// caller-gain fact key and the callee discharge use the exact same (law, args) identity.
	Args []ast.Expr
}

// CParamPart is one C parameter of a C-ABI extern: which Elisa explicit parameter it comes
// from and which part of it (the whole value, or the pointer / length of a split view).
type CParamPart struct {
	Param int
	Part  CParamPartKind
}

type CParamPartKind int

const (
	CParamWhole CParamPartKind = iota
	CParamViewPtr
	CParamViewLen
)

type FuncSegmentTransition int

const (
	FuncSegmentTransitionNone FuncSegmentTransition = iota
	FuncSegmentTransitionHost
	FuncSegmentTransitionGuest
)

type FuncType struct {
	Name         string
	TypeParams   []string
	RegionParams []string
	// InferredRegionParams are call-inferred region binders (for example grown-container
	// plumbing). They are part of the callable signature, but callers do not spell them in
	// explicit generic specialization arguments.
	InferredRegionParams   []string
	PermissionParams       []string
	GenericParams          []ast.GenericParam
	UsedPermissionParams   []string
	DeclaredPermissionRefs []ast.PermissionRef
	DeclaredPermissions    []string
	PermissionRefs         []ast.PermissionRef
	// MutableGlobalPermissionRefs tracks mandatory effects separately from immutable global reads.
	MutableGlobalPermissionRefs []ast.PermissionRef
	// Retain declaration identities when function-value joins precede the effect fixpoint.
	MutableGlobalSourceNames []string
	// Effects of a callback returned by this function, separate from construction effects.
	ReturnedFunctionPermissionRefs       []ast.PermissionRef
	ReturnedFunctionGlobalPermissionRefs []ast.PermissionRef
	// Declaration identities of factories that supplied this function value.
	FunctionReturnSourceNames []string
	Permissions               []string
	ShapeParams               []string
	FreshReturnShapeParams    []string
	Static                    bool
	// CapturesThreadUnsafe marks a closure that captures a value which is not safe to
	// share across threads (a non-static mutable ref, a darray/dict, etc.). Closures
	// capture by value, so capturing such a *reference* copies the pointer and shares its
	// referent — passing the closure to a thread is therefore a data race unless gated by
	// Unsafe.ThreadShare. The flag flows into threadTransferRequiresUnsafeThreadShare.
	CapturesThreadUnsafe bool
	InlineMode           FuncInlineMode
	HasInlineMode        bool
	// FastMath opts the function body into full fast-math FP semantics (reassoc + nnan + ninf + nsz +
	// arcp + contract), matching clang -ffast-math. Set by @fast_math. Enables FP reassociation and
	// thus auto-vectorization of reduction/elementwise loops. Off by default; opt-in only because it
	// reorders FP operations (results may differ by more than the contract/reciprocal tier).
	FastMath               bool
	HasNoRecurse           bool
	HasAsyncEntry          bool
	HasSegmentAgnostic     bool
	HasSegmentEstablishing bool
	HasReentrantSafe       bool
	SegmentTransition      FuncSegmentTransition
	TemperatureMode        FuncTemperatureMode
	HasTemperatureMode     bool
	CallConv               string
	IsNativeExtern         bool // a non-generic `extern`: the callee may be native C, so the signature is an ABI boundary (see extern_optional_abi.go)
	// CParamPlan, when non-nil, is the C parameter order of a C-ABI extern whose Elisa-facing
	// parameter list differs from the native prototype: `@bounds(buf, count)` turns `buf: T&`
	// into `buf: view[T]` and removes `count`, and the plan says where the view's pointer and
	// length go (docs/127 §3.3 legacy form). Nil means "explicit params in order, each split
	// view adjacent" (the native form). See semantic.CParamPart and backend/llvm_extern_view_abi.go.
	CParamPlan []CParamPart
	// ExternEnsureValues / ExternRequires are a native extern's boundary clauses, kept for the
	// backend: an extern `ensure` is CHECKED at every call site in checked builds (docs/127 D8),
	// not merely assumed, unless the extern is @trusted — a lying library then stops at the
	// boundary instead of turning safe code into undefined behaviour downstream.
	ExternEnsureValues []ast.Expr
	ExternRequires     []ast.Expr
	TrustedExtern      bool
	// BoundsLengthNames are the length parameters `@bounds` removed, for the call-site
	// message when a caller still passes one.
	BoundsLengthNames           []string
	IntrinsicName               string
	GuardEffects                []FuncGuardEffect
	BoundaryPointerParamIndices []int
	SpecSignature               *SpecSignature
	Poststates                  []FuncPoststate
	RefinementEnsures           []RefinementEnsure
	ParamRefinementRanges       map[int]numRange
	// FrameWrites is the callee's EFFECTIVE frame (docs/87 87-3): every caller-visible place it may
	// write, as a (param index, field suffix) pair — direct `changes` paths plus `fulfills`-expanded
	// frame-law paths. FrameBounded reports whether the callee's writes are bounded at all (it has a
	// `changes`/`fulfills` frame). A bounded callee lets a caller REFINE a mutable-ref argument from
	// the whole place to just the written subpaths (`f(&r.x)` where `f changes self.a` writes only
	// `r.x.a`); an unbounded callee keeps the conservative whole-place rule. Pure `preserves` does not
	// bound writes, so it does not set FrameBounded.
	FrameWrites               []FrameParamWrite
	FrameBounded              bool
	Params                    []Type
	ExplicitParamCount        int
	ExplicitParamNames        []string
	ExplicitParamDefaultExprs []ast.Expr
	ExplicitParamHasDefault   []bool
	ImplicitParamNames        []string
	Return                    Type
	// ReturnRegion is the function-level `-> T @r` contract. It records the
	// lifetime tie without changing the nominal return type (especially for
	// recursive packed enums whose constructors are packed variant views).
	ReturnRegion                 string
	Variadic                     bool
	SinkParams                   []bool
	SinkParamsKnown              bool
	ReturnProvenance             regionRefState
	ReturnProvenanceKnown        bool
	ReturnBorrowedOwnerRefs      borrowedOwnerRefSummary
	ReturnBorrowedOwnerRefsKnown bool
	ReturnIsolation              ReturnIsolationSummary
	ReturnIsolationKnown         bool
	// ReturnsOwnedRegion is set when every value-returning path hands back an
	// owned region (`return move <region>`). The caller's binding then inherits
	// the affine must-consume obligation — approach A: ownership transfer
	// inferred from `move`, no distinct owned-region type.
	ReturnsOwnedRegion bool
	// OwnedParams[i] is true when parameter i is declared `owned <store>` and
	// therefore takes ownership of a region: the caller must `move` an owned
	// region into it (consuming it) and the callee must consume it. Defaults
	// empty/false so a propagation gap is over-strict (the caller keeps the
	// must-consume obligation -> a compile error), never a silent double-free.
	OwnedParams []bool
	// RegionPolymorphic is set (docs/75) when a value-returning path hands back a
	// value whose region dependency is a *synthesized inferred* region (`__auto_*`)
	// local to this function — i.e. the function allocates with `new[auto]` and
	// returns the result. Such a function is implicitly parameterized over the
	// region its result lives in: the region is threaded as a hidden param and
	// bound at the call site to the caller's ambient region. Step 1 (detection)
	// only records the classification; threading is layered on top.
	RegionPolymorphic bool
}
