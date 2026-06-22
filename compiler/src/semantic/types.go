package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type Type interface {
	String() string
	isType()
}

type PermissionSet struct {
	Name      string
	Members   []string
	MemberSet map[string]bool
	Includes  []string // families this one subsumes (qualified names, transitive at query time)
	Decl      *ast.PermissionDecl
	Builtin   bool
}

type Shape interface {
	String() string
	isShape()
}

type InvalidType struct{}

type NeverType struct{}

type NullType struct{}

type BuiltinType struct {
	Name string
}

type BitIntType struct {
	Signed bool
	Width  int
}

type IDType struct {
	Tag     Type
	Storage Type
}

type AddressSpaceType struct {
	Space   string
	Elem    Type
	Storage Type
}

type TypeParamType struct {
	Name string
}

type ConstParamType struct {
	Name      string
	ValueType Type
}

type ConstValueType struct {
	Value ConstValue
}

type StructStateCaseType struct {
	StructName string
	Case       string
}

type StructStateSetType struct {
	StructName string
	Cases      []string
}

type RefStorageValueType struct {
	Storage RefStorage
}

type RegionParamType struct {
	Name string
}

type RegionValueType struct {
	Name string
}

type ErrorSetType struct {
	Name     string
	Tags     []string
	Payloads map[string][]Type
	// Params are unresolved error-set generic parameters unioned into this set
	// (the `R` in `def f[errorset R](...) -> T error[R]`, docs/64 Phase 5b). A
	// set with only Params is the opaque placeholder; a mixed set such as
	// `error[R, Timeout]` carries concrete Tags alongside. Params are opaque
	// inside the function body and bound to concrete sets at each call site.
	Params []string
}

// HasParams reports whether the set still carries unresolved error-set
// generic parameters (it is not fully concrete).
func (t *ErrorSetType) HasParams() bool { return t != nil && len(t.Params) > 0 }

type ErrorUnionType struct {
	Value  Type
	Errors *ErrorSetType
}

type OptionalType struct {
	Value Type
}

type TupleField struct {
	Name string
	Type Type
}

type TupleType struct {
	Fields []TupleField
}

type BitGroupKind int

const (
	BitGroupBitset BitGroupKind = iota + 1
	BitGroupBitfield
)

type BitGroupMember struct {
	Name   string
	Type   Type
	Offset int
	Width  int
	Signed bool
	Decl   *ast.BitGroupMemberDecl
}

type BitGroupType struct {
	Name         string
	Kind         BitGroupKind
	Members      []BitGroupMember
	MemberMap    map[string]BitGroupMember
	BackingWidth int
	Decl         *ast.BitGroupDecl
}

type ConstEnumMember struct {
	Name  string
	Value int64
	Decl  *ast.ConstEnumMemberDecl
}

type ShapeParam struct {
	Name string
}

type NamedShape struct {
	Name string
}

type WildcardShape struct{}

type FreshShape struct {
	ID     int
	Label  string
	Origin string
}

type RefState int

const (
	RefStateNonNull RefState = iota
	RefStateNullable
	RefStateNull
)

type RefStorage int

const (
	RefStorageAny RefStorage = iota
	RefStorageHeap
	RefStorageStack
	RefStorageStatic
)

type RefType struct {
	Elem            Type
	Mutable         bool
	State           RefState
	Storage         RefStorage
	Region          string
	ExplicitStorage bool
}

type ArrayType struct {
	Elem         Type
	Size         string
	HasConstSize bool
	ConstSize    int64
	ConstParam   string
	SurfaceName  string
}

type DArrayType struct {
	Elem        Type
	Shape       Shape
	SurfaceName string
	// Region is the (string-named) region this darray was allocated in, inferred
	// from the enclosing `in <region>:` scope at creation. Phase 1 of
	// region-parameterized containers: carried on the type but NOT yet consulted
	// by SameType/AssignableTo/String or codegen (see REGION_CONTAINERS_DESIGN.md).
	// Empty == unknown / not-yet-inferred.
	Region string
}

// ViewType is a borrowed window into contiguous T memory: surface `view[T]`. It carries optional
// (static-or-dynamic) bounds in Begin/End — a parameter, not a separate type. (It was historically
// split into a static `view` and a dynamic `dview`; those were unified, and the `dview` spelling and
// the `DArrayViewType` name were removed.) SurfaceName is the spelling used in diagnostics.
type ViewType struct {
	Elem        Type
	Begin       string
	End         string
	SurfaceName string
	// Region is the allocation region a `view @r` points into. A view produced
	// by slicing a region-backed `darray[T] @r` (or another region-carrying view)
	// inherits r, so the escape checker proves the borrowed window cannot outlive
	// r — the same guarantee SViewType.Region gives string views. Inert in
	// SameType/AssignableTo, like DArrayType/DictType/DStrType/SViewType.Region.
	Region string
}

type StoreRowsViewType struct {
	Store *StructType
}

type StoreRowViewType struct {
	Store *StructType
}

type DStrType struct {
	Shape       Shape
	SurfaceName string
	// Region is the allocation region a `cstr @r` points into (region-
	// parameterized containers). A cstr produced from `darray[u8] @r` via
	// `.cstr()` carries r so the escape checker proves it cannot outlive r.
	// Inert in SameType/AssignableTo (Phase 1), like DArrayType/DictType.Region.
	Region string
}

type DictType struct {
	Key         Type
	Value       Type
	SurfaceName string
	// Region — allocation region (see DArrayType.Region). Region-parameterized
	// containers Phase 1+: carried/inferred and escape-checked; the dict-op
	// codegen ABI (insert through a region param) is a follow-up.
	Region string
}

type SetType struct {
	Elem        Type
	SurfaceName string
	Region      string
}

type DictEntryType struct {
	Dict    *DictType
	Mutable bool
}

type SViewType struct {
	Begin string
	End   string
	// Region is the allocation region an `sview @r` points into. An sview
	// produced from `darray[u8] @r` via `.sview()` carries r so the escape
	// checker proves the bounded {data,len} view cannot outlive r. Inert in
	// SameType/AssignableTo (Phase 1), like DArrayType/DictType/DStrType.Region.
	Region string
}

type EnumVariant struct {
	Name             string
	Tag              uint32
	Payload          []Type
	PayloadNames     []string
	PayloadRelations []ast.EnumPayloadRelation
	TailIndex        int
	Decl             *ast.EnumVariantDecl
	packedViewType   *PackedVariantViewType
}

type PackedEnumStoreType struct {
	Name  string
	Enum  *EnumType
	State Type
}

type PackedVariantViewType struct {
	Enum    *EnumType
	Variant *EnumVariant
}

type EnumType struct {
	Name                    string
	Packed                  bool
	PackedProfile           string
	HasPackedProfile        bool
	PackedABIOverride       string
	HasPackedABIOverride    bool
	PackedPrefixOverride    string
	HasPackedPrefixOverride bool
	Common                  map[string]Field
	StoreType               *PackedEnumStoreType
	TagType                 *ConstEnumType
	Variants                []*EnumVariant
	VariantMap              map[string]*EnumVariant
	Decl                    *ast.EnumDecl
	// Layout (docs/76) records the `enum X layout soa|aos(...)` suffix. LayoutSet says whether one was
	// written; when unset the compiler's default applies (AoS-in-arena for a recursive enum). The
	// layout axis is independent of region (provenance) and freeze (usage) — docs/10.
	Layout       ast.StructLayoutMode
	LayoutSet    bool
	LayoutSparse bool
	IndexWidth   string // "u8"|"u16"|"u32"|"u64"; "" = default u32 (docs/76 opaque index handle)
	// RecursivePlain (docs/76 Phase 3) is set when a plain `enum` (no `packed` keyword) was promoted
	// to the region-backed machinery because a variant references the enum by value (a recursive AST
	// node). It selects the AoS storage mode by default and lets the migration diagnostics (Phase 4)
	// distinguish "the user wrote `packed enum`" from "the compiler promoted a recursive plain enum".
	RecursivePlain bool
	// Parent (docs/77) is the enum this one refines via `enum Child is Parent:`. nil means a root.
	// Child's cases are a subset of Parent's, so Child <: Parent (sealed nominal subtyping). Resolved
	// after all enum skeletons are collected (parents may be declared in any order / another file).
	Parent *EnumType
	// Children are the direct refinements declared with `is this` — the downward edges of the sealed
	// hierarchy (the inverse of Parent), in declaration order. Populated as parents are resolved.
	Children []*EnumType
	// LeafTagLo/LeafTagCount (docs/77 Phase 2 groundwork) describe this node's contiguous, dense
	// leaf-tag range within its hierarchy root's tag space: the leaves of this category (and all its
	// refinements) occupy tags [LeafTagLo, LeafTagLo+LeafTagCount). Ranges nest, so a category test
	// `is X` is a single unsigned range check and an upcast is a no-op. Assigned per root after all
	// variants are populated; only meaningful for hierarchical enums. NOTE: this dense range is
	// recomputed each compile and must NOT be used as a stable on-disk/serialization tag (docs/77 §5).
	LeafTagLo    uint32
	LeafTagCount uint32
}

// Root returns the topmost ancestor of a sealed hierarchy (docs/77) — the enum that owns the unified
// representation and tag space. For a plain enum it returns the enum itself.
func (e *EnumType) Root() *EnumType {
	for e != nil && e.Parent != nil {
		e = e.Parent
	}
	return e
}

type PackedFieldStorageMode string

const (
	PackedFieldStorageDefault   PackedFieldStorageMode = ""
	PackedFieldStorageInline    PackedFieldStorageMode = "inline"
	PackedFieldStorageSideTable PackedFieldStorageMode = "side_table"
)

type ConstEnumType struct {
	Name      string
	Storage   Type
	Members   []*ConstEnumMember
	MemberMap map[string]*ConstEnumMember
	Decl      *ast.ConstEnumDecl
}

type Field struct {
	Name          string
	Type          Type
	Mutable       bool
	IsTail        bool
	// Ghost marks a verification-only model field (`ghost name: T`). It is readable only in a
	// contract/ghost context and is erased from codegen (stripped from the struct's concrete
	// field set, so it has zero layout/size/offset impact on the real fields).
	Ghost         bool
	PackedStorage PackedFieldStorageMode
}

type StructDerivedState struct {
	Name      string
	Condition ast.Expr
}

type StructType struct {
	Name               string
	Namespace          string
	Usings             []string
	TypeParams         []string
	RegionParams       []string
	RegionOwner        string
	GenericParams      []ast.GenericParam
	NamedStateCases    []string
	TerminalStateCases []string
	DerivedStates      []StructDerivedState
	DerivedStateMap    map[string]*StructDerivedState
	Fields             map[string]Field
	Affine             bool
	// Droppable: `affine` (use-at-most-once, may be dropped) vs `linear`
	// (use-exactly-once, must-consume). Only meaningful when Affine is true;
	// defaults false so a propagation gap is over-strict (linear), never unsound.
	Droppable       bool
	ReprC           bool
	Layout          ast.StructLayoutMode
	PackedLayout    bool
	HasPackedGroups bool
	Alignment       int
	HasAlignment    bool
	CBindHeader     string
	CBindName       string
	CBindPrefix     bool
	Decl            *ast.StructDecl
	StoreDecl       *ast.StoreDecl
	Store           bool
	StoreFieldOrder []string
	Builtin         bool
	// GhostFieldOrder lists the verification-only model fields (`ghost name: T`) in declaration
	// order. They live in Fields (so contract analysis resolves them) but are stripped from
	// Decl.Fields, so codegen never lays them out — guaranteeing zero impact on the concrete
	// layout/size/offsets of the real fields.
	GhostFieldOrder []string
	// Invariants is the FULL struct-invariant list (including ghost-model-referencing ones), kept
	// for static discharge as method-entry assumptions. Decl.Invariants is stripped of ghost-reading
	// invariants so the backend never emits a runtime check over an erased field.
	Invariants []ast.Expr
}

type OpaqueType struct {
	Name    string
	CHeader string
	CType   string
}

type GenericInstanceType struct {
	Name string
	Base Type
	Args []Type
	// Region is the `@r` allocation region of a generic-type use site (`Box[i64] @r`,
	// docs/68 §5). Inert in SameType/AssignableTo (Phase 1), like DArrayType.Region —
	// it only feeds region provenance (destroy-invalidation, promote) via the flow analysis.
	Region string
}

type AggregateStateType struct {
	Base   Type
	State  RefState
	States []RefState
}

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

type FuncSegmentTransition int

const (
	FuncSegmentTransitionNone FuncSegmentTransition = iota
	FuncSegmentTransitionHost
	FuncSegmentTransitionGuest
)

type FuncType struct {
	Name                   string
	TypeParams             []string
	RegionParams           []string
	PermissionParams       []string
	GenericParams          []ast.GenericParam
	UsedPermissionParams   []string
	DeclaredPermissionRefs []ast.PermissionRef
	DeclaredPermissions    []string
	PermissionRefs         []ast.PermissionRef
	Permissions            []string
	ShapeParams            []string
	FreshReturnShapeParams []string
	Static                 bool
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
	FastMath                     bool
	HasNoRecurse                 bool
	HasAsyncEntry                bool
	HasSegmentAgnostic           bool
	HasSegmentEstablishing       bool
	HasReentrantSafe             bool
	SegmentTransition            FuncSegmentTransition
	TemperatureMode              FuncTemperatureMode
	HasTemperatureMode           bool
	CallConv                     string
	IntrinsicName                string
	GuardEffects                 []FuncGuardEffect
	BoundaryPointerParamIndices  []int
	Poststates                   []FuncPoststate
	RefinementEnsures            []RefinementEnsure
	// FrameWrites is the callee's EFFECTIVE frame (docs/87 87-3): every caller-visible place it may
	// write, as a (param index, field suffix) pair — direct `changes` paths plus `fulfills`-expanded
	// frame-law paths. FrameBounded reports whether the callee's writes are bounded at all (it has a
	// `changes`/`fulfills` frame). A bounded callee lets a caller REFINE a mutable-ref argument from
	// the whole place to just the written subpaths (`f(&r.x)` where `f changes self.a` writes only
	// `r.x.a`); an unbounded callee keeps the conservative whole-place rule. Pure `preserves` does not
	// bound writes, so it does not set FrameBounded.
	FrameWrites                  []FrameParamWrite
	FrameBounded                 bool
	Params                       []Type
	ExplicitParamCount           int
	ExplicitParamNames           []string
	ExplicitParamDefaultExprs    []ast.Expr
	ExplicitParamHasDefault      []bool
	ImplicitParamNames           []string
	Return                       Type
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
