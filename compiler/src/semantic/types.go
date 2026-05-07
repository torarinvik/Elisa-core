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

type RefStorageParamType struct {
	Name string
}

type RefStorageValueType struct {
	Storage RefStorage
}

type RefStateParamType struct {
	Name string
}

type RefStateValueType struct {
	State RefState
}

type ErrorSetType struct {
	Name string
	Tags []string
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
	StateParam      string
	Storage         RefStorage
	StorageParam    string
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
}

type DictType struct {
	Key         Type
	Value       Type
	SurfaceName string
}

type DictEntryType struct {
	Dict    *DictType
	Mutable bool
}

type SViewType struct {
	Begin string
	End   string
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

type TreeLayout int

const (
	TreeLayoutPerVariantRows TreeLayout = iota
	TreeLayoutCategoryUnion
)

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
}

type StructDerivedState struct {
	Name      string
	Condition ast.Expr
}

type StructType struct {
	Name             string
	TypeParams       []string
	RefStorageParams []string
	RefStateParams   []string
	GenericParams    []ast.GenericParam
	NamedStateCases  []string
	DerivedStates    []StructDerivedState
	DerivedStateMap  map[string]*StructDerivedState
	Fields           map[string]Field
	Affine           bool
	ReprC            bool
	HasPackedGroups  bool
	Alignment        int
	HasAlignment     bool
	Decl             *ast.StructDecl
	StoreDecl        *ast.StoreDecl
	Store            bool
	StoreFieldOrder  []string
	Builtin          bool
}

type OpaqueType struct {
	Name string
}

type GenericInstanceType struct {
	Name string
	Base Type
	Args []Type
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

type FuncType struct {
	Name                         string
	TypeParams                   []string
	RefStorageParams             []string
	RefStateParams               []string
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
	InlineMode                   FuncInlineMode
	HasInlineMode                bool
	HasNoRecurse                 bool
	TemperatureMode              FuncTemperatureMode
	HasTemperatureMode           bool
	GuardEffects                 []FuncGuardEffect
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
}
