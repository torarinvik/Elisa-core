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

type ContextBundleField struct {
	Name    string
	Type    Type
	Mutable bool
	Decl    ast.ParamDecl
}

type ContextBundle struct {
	Name   string
	Fields []ContextBundleField
	Decl   *ast.ContextDecl
}

type ParamPackField struct {
	Name    string
	Type    Type
	Mutable bool
	Decl    ast.ParamDecl
}

type ParamPack struct {
	Name      string
	Fields    []ParamPackField
	Decl      *ast.ParamsDecl
	Namespace string
	Usings    []string
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
	// Param marks this as a polymorphic error-set parameter placeholder (e.g. the
	// `R` in `def f[errorset R](...) -> T error[R]`). A Param set is opaque inside
	// the function body and is bound to a concrete error set at each call site.
	Param bool
}

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

type ViewType struct {
	Elem  Type
	Begin string
	End   string
}

type DArrayViewType struct {
	Elem        Type
	Begin       string
	End         string
	SurfaceName string
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

type TreeStoreType struct {
	Name   string
	Family *TreeType
	State  Type
}

type FrozenTreeRowsViewType struct {
	Store    *TreeStoreType
	Category *TreeCategoryType
}

type TreeLayout int

const (
	TreeLayoutPerVariantRows TreeLayout = iota
	TreeLayoutCategoryUnion
	TreeLayoutAOS
	TreeLayoutSOA
)

type TreeFieldTemperature int

const (
	TreeFieldTemperatureDefault TreeFieldTemperature = iota
	TreeFieldTemperatureHot
	TreeFieldTemperatureCold
)

type TreeIndexSpec struct {
	Name string
	Kind bool
}

type PackedVariantViewType struct {
	Enum    *EnumType
	Variant *EnumVariant
}

type TreeVariantViewType struct {
	Category *TreeCategoryType
	Variant  *EnumVariant
}

type TreeNodeType struct {
	Name     string
	Family   *TreeType
	KindType *ConstEnumType
}

type TreeType struct {
	Name           string
	Layout         TreeLayout
	LayoutExplicit bool
	Indexes        []TreeIndexSpec
	Common         map[string]Field
	MemberTypes    map[string]Type
	NodeType       *TreeNodeType
	StoreType      *TreeStoreType
	Decl           *ast.TreeDecl
}

type TreeCategoryType struct {
	Name           string
	Family         *TreeType
	Parent         *TreeCategoryType
	Role           string
	Layout         TreeLayout
	LayoutExplicit bool
	Indexes        []TreeIndexSpec
	KindType       *ConstEnumType
	Common         map[string]Field
	Variants       []*EnumVariant
	VariantMap     map[string]*EnumVariant
	Decl           *ast.TreeCategoryDecl
}

type TreeBlockType struct {
	Name     string
	Family   *TreeType
	ExactTag uint32
	Fields   map[string]Field
	Decl     *ast.TreeBlockDecl
}

type TreeStructType struct {
	Name     string
	Family   *TreeType
	ExactTag uint32
	Fields   map[string]Field
	Decl     *ast.TreeStructDecl
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
	PackedStorage PackedFieldStorageMode
	TreeTemp      TreeFieldTemperature
}

type StructDerivedState struct {
	Name      string
	Condition ast.Expr
}

type StructType struct {
	Name            string
	Namespace       string
	Usings          []string
	TypeParams      []string
	RegionParams    []string
	RegionOwner     string
	GenericParams   []ast.GenericParam
	NamedStateCases []string
	DerivedStates   []StructDerivedState
	DerivedStateMap map[string]*StructDerivedState
	Fields          map[string]Field
	Affine          bool
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
}

type FuncSegmentTransition int

const (
	FuncSegmentTransitionNone FuncSegmentTransition = iota
	FuncSegmentTransitionHost
	FuncSegmentTransitionGuest
)

type FuncType struct {
	Name                         string
	TypeParams                   []string
	RegionParams                 []string
	PermissionParams             []string
	GenericParams                []ast.GenericParam
	UsedPermissionParams         []string
	DeclaredPermissionRefs       []ast.PermissionRef
	DeclaredPermissions          []string
	PermissionRefs               []ast.PermissionRef
	Permissions                  []string
	ShapeParams                  []string
	FreshReturnShapeParams       []string
	Static                       bool
	// CapturesThreadUnsafe marks a closure that captures a value which is not safe to
	// share across threads (a non-static mutable ref, a darray/dict, etc.). Closures
	// capture by value, so capturing such a *reference* copies the pointer and shares its
	// referent — passing the closure to a thread is therefore a data race unless gated by
	// Unsafe.ThreadShare. The flag flows into threadTransferRequiresUnsafeThreadShare.
	CapturesThreadUnsafe         bool
	InlineMode                   FuncInlineMode
	HasInlineMode                bool
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
