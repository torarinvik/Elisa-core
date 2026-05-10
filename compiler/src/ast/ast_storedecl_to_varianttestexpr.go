package ast

import "elisacore/src/lexer"

type StoreDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Soa         bool
	Fields      []FieldDecl
}
type GenericParamKind int

const (
	GenericParamType GenericParamKind = iota
	GenericParamState
	GenericParamRefStorage
	GenericParamRefState
	GenericParamRegion
	GenericParamPermission
	GenericParamValue
)

type GenericParam struct {
	Position       lexer.Pos
	Kind           GenericParamKind
	Name           string
	InterfaceBound string
	StateCases     []string
	StateOwner     string
}
type DerivedStateDecl struct {
	Position  lexer.Pos
	StateName string
	Condition Expr
}
type FieldDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Mutable     bool
	IsTail      bool
	Type        TypeExpr
	BitGroup    *BitGroupDecl
}
type BitGroupKind int

const (
	BitGroupBitset BitGroupKind = iota + 1
	BitGroupBitfield
)

type BitGroupDecl struct {
	Position lexer.Pos
	Kind     BitGroupKind
	Members  []BitGroupMemberDecl
}
type BitGroupMemberDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
}
type Annotation struct {
	Position lexer.Pos
	Name     string
	Args     []string
}
type PermissionRef struct {
	Position lexer.Pos
	Name     string
	Member   string
}
type EnsuresConditionKind int

const (
	EnsuresConditionAlways EnsuresConditionKind = iota
	EnsuresConditionReturnBool
)

type EnsuresCondition struct {
	Position   lexer.Pos
	Kind       EnsuresConditionKind
	ReturnBool bool
}
type EnsuresKind int

const (
	EnsuresKindNamedState EnsuresKind = iota
	EnsuresKindRefState
	EnsuresKindPreserve
)

type EnsuresPath struct {
	Position lexer.Pos
	Root     string
	Fields   []string
}
type EnsuresClause struct {
	Position   lexer.Pos
	Condition  EnsuresCondition
	Target     EnsuresPath
	Kind       EnsuresKind
	StateCases []string
	RefState   RefState
}
type ContextDecl struct {
	Position lexer.Pos
	Name     string
	Fields   []ParamDecl
}
type ParamsDecl struct {
	Position lexer.Pos
	Name     string
	Params   []ParamDecl
}
type WithArg struct {
	Position  lexer.Pos
	Name      string
	Value     Expr
	Shorthand bool
}
type ParamPackUse struct {
	Position lexer.Pos
	Name     string
	Args     []WithArg
	Bare     bool
}
type WithBundleUse struct {
	Position lexer.Pos
	Name     string
	Args     []WithArg
	Spread   bool
}
type ImplicitSigItem struct {
	Position lexer.Pos
	Bundle   string
	Param    ParamDecl
	IsBundle bool
}
type WithItem struct {
	Position lexer.Pos
	Arg      WithArg
	Bundle   WithBundleUse
	IsBundle bool
}
type ParamSigItem struct {
	Position lexer.Pos
	Param    ParamDecl
	Pack     ParamPackUse
	IsPack   bool
}
type FuncDecl struct {
	Position          lexer.Pos
	Annotations       []Annotation
	Override          bool
	Name              string
	TypeParams        []string
	RefStorageParams  []string
	RefStateParams    []string
	RegionParams      []string
	PermissionParams  []string
	GenericParams     []GenericParam
	EffectAliasPos    lexer.Pos
	EffectAlias       string
	Effects           []SignatureEffectItem
	Permissions       []PermissionRef
	Ensures           []EnsuresClause
	Params            []ParamDecl
	ParamPacks        []ParamPackUse
	ParamItemOrder    []ParamSigItem
	ImplicitParams    []ParamDecl
	ImplicitBundles   []string
	ImplicitItemOrder []ImplicitSigItem
	ReturnType        TypeExpr
	Body              []Stmt
}
type ParamDecl struct {
	Position     lexer.Pos
	Name         string
	Mutable      bool
	Type         TypeExpr
	DefaultValue Expr
}
type ExternFuncDecl struct {
	Position          lexer.Pos
	Annotations       []Annotation
	Override          bool
	Name              string
	TypeParams        []string
	RefStorageParams  []string
	RefStateParams    []string
	PermissionParams  []string
	GenericParams     []GenericParam
	RegionParams      []string
	EffectAliasPos    lexer.Pos
	EffectAlias       string
	Effects           []SignatureEffectItem
	Permissions       []PermissionRef
	Ensures           []EnsuresClause
	Params            []ParamDecl
	ParamPacks        []ParamPackUse
	ParamItemOrder    []ParamSigItem
	ImplicitParams    []ParamDecl
	ImplicitBundles   []string
	ImplicitItemOrder []ImplicitSigItem
	ReturnType        TypeExpr
	Variadic          bool
}
type ExternVarDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Type        TypeExpr
}
type ExternTypeDecl struct {
	Position lexer.Pos
	Name     string
}
type TypeAliasDecl struct {
	Position lexer.Pos
	Name     string
	Target   TypeExpr
}
type ExportTypeDecl struct {
	Position     lexer.Pos
	ExportedType TypeExpr
	Alias        string
}
type ExportFuncDecl struct {
	Position       lexer.Pos
	Name           string
	Params         []ParamDecl
	ReturnType     TypeExpr
	TargetName     string
	TargetTypeArgs []TypeExpr
}
type ExportGlobalDecl struct {
	Position   lexer.Pos
	TargetName string
	Alias      string
}
type StaticIfDecl struct {
	Position lexer.Pos
	Cond     Expr
	Then     []Decl
	Elifs    []StaticElifDecl
	Else     []Decl
}
type StaticElifDecl struct {
	Position lexer.Pos
	Cond     Expr
	Body     []Decl
}
type TypeExpr interface {
	Node
	typeExprTag()
}
type NamedType struct {
	Position lexer.Pos
	Name     string
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

type BranchHint int

const (
	BranchHintNone BranchHint = iota
	BranchHintLikely
	BranchHintUnlikely
)

type RefType struct {
	Position     lexer.Pos
	Elem         TypeExpr
	State        RefState
	Storage      RefStorage
	StateParam   string
	StorageParam string
	Region       string
	Explicit     bool
}
type RefStateLiteralTypeExpr struct {
	Position lexer.Pos
	State    RefState
}
type RefStorageLiteralTypeExpr struct {
	Position lexer.Pos
	Storage  RefStorage
}
type GenericType struct {
	Position lexer.Pos
	Name     string
	Args     []TypeExpr
}
type GenericValueArgTypeExpr struct {
	Position lexer.Pos
	Value    Expr
}
type AggregateStateTypeExpr struct {
	Position lexer.Pos
	Base     TypeExpr
	State    RefState
	States   []RefState
}
type StateSetTypeExpr struct {
	Position lexer.Pos
	Cases    []string
}
type MutableType struct {
	Position lexer.Pos
	Elem     TypeExpr
}
type TailType struct {
	Position lexer.Pos
	Elem     TypeExpr
}
type ArrayType struct {
	Position lexer.Pos
	Elem     TypeExpr
	Size     Expr
}
type BuiltinTypeExpr struct {
	Position  lexer.Pos
	Name      string
	TypeArgs  []TypeExpr
	ValueArgs []Expr
}
type FuncTypeExpr struct {
	Position          lexer.Pos
	Params            []TypeExpr
	ImplicitParams    []ParamDecl
	ImplicitBundles   []string
	ImplicitItemOrder []ImplicitSigItem
	Return            TypeExpr
	EffectAliasPos    lexer.Pos
	EffectAlias       string
	Effects           []SignatureEffectItem
	Permissions       []PermissionRef
	Variadic          bool
}
type ErrorTagExpr struct {
	Position lexer.Pos
	SetName  string
	Tag      string
}
type ErrorSetExpr struct {
	Position    lexer.Pos
	Tags        []ErrorTagExpr
	HasEllipsis bool
}
type ErrorUnionTypeExpr struct {
	Position lexer.Pos
	Value    TypeExpr
	Errors   TypeExpr
}
type OptionalTypeExpr struct {
	Position lexer.Pos
	Value    TypeExpr
}
type TupleTypeExpr struct {
	Position lexer.Pos
	Fields   []TupleTypeField
}
type TupleTypeField struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
}

func RefStateMarker(state RefState) string {
	switch state {
	case RefStateNullable:
		return "?"
	case RefStateNull:
		return "!"
	default:
		return "&"
	}
}

type Expr interface {
	Node
	exprTag()
}
type Ident struct {
	Position lexer.Pos
	Name     string
}
type IntLit struct {
	Position lexer.Pos
	Value    string
	Suffix   string
	IsHex    bool
}
type FloatLit struct {
	Position lexer.Pos
	Value    string
	Suffix   string
}
type StringLit struct {
	Position lexer.Pos
	Value    string
}
type CharLit struct {
	Position lexer.Pos
	Value    string
}
type BoolLit struct {
	Position lexer.Pos
	Value    bool
}
type NullLit struct {
	Position lexer.Pos
}
type ZeroedLit struct {
	Position lexer.Pos
}
type ExprBlock struct {
	Position lexer.Pos
	Stmts    []Stmt
	Value    Expr
}
type BinaryExpr struct {
	Position    lexer.Pos
	Op          lexer.TokenKind
	Left        Expr
	Right       Expr
	LoweredCall *CallExpr
}
type UnaryExpr struct {
	Position lexer.Pos
	Op       lexer.TokenKind
	Operand  Expr
}
type MoveExpr struct {
	Position lexer.Pos
	Operand  Expr
}
type CallExpr struct {
	Position      lexer.Pos
	Func          Expr
	SafeReceiver  Expr
	HasArgForward bool
	ArgForwardPos lexer.Pos
	Args          []Expr
	ArgNames      []string
	ArgShorthand  []bool
	ParamPacks    []ParamPackUse
	ArgItemOrder  []CallArgItem
	Safe          bool
	WithArgs      []WithArg
	WithBundles   []WithBundleUse
	WithItemOrder []WithItem

	ResolvedArgsValid         bool
	ResolvedArgs              []Expr
	ResolvedCommonArgs        map[string]Expr
	ResolvedImplicitArgsValid bool
	ResolvedImplicitArgs      []Expr
}
type FieldExpr struct {
	Position lexer.Pos
	Object   Expr
	Field    string
	Safe     bool
}
type CallArgItem struct {
	Position lexer.Pos
	ArgIndex int
	Pack     ParamPackUse
	IsPack   bool
}
type ShorthandMemberExpr struct {
	Position lexer.Pos
	Parts    []string
}
type IndexExpr struct {
	Position lexer.Pos
	Object   Expr
	Index    Expr
	Fallback Expr
}
type SliceExpr struct {
	Position lexer.Pos
	Object   Expr
	Start    Expr
	End      Expr
}
type ListLitExpr struct {
	Position lexer.Pos
	Elems    []Expr
}
type ListComprehensionExpr struct {
	Position lexer.Pos
	Value    Expr
	Name     string
	Source   Expr
	Filter   Expr
}
type QueryExprKind int

const (
	QueryExprAny QueryExprKind = iota
	QueryExprAll
	QueryExprFirst
	QueryExprCount
)

type QueryExpr struct {
	Position lexer.Pos
	Kind     QueryExprKind
	Name     string
	Source   Expr
	Filter   Expr
}
type CastExprOrigin int

const (
	CastExprOriginGeneral CastExprOrigin = iota
	CastExprOriginToSyntax
	CastExprOriginAsSyntax
	CastExprOriginExplicitCast
	CastExprOriginPostfixShorthand
)

type CastExpr struct {
	Position lexer.Pos
	Operand  Expr
	Target   TypeExpr
	Origin   CastExprOrigin
}
type CascadeExpr struct {
	Position lexer.Pos
	Target   Expr
	Value    Expr
}
type LambdaExpr struct {
	Position            lexer.Pos
	Keyword             string
	UsesShorthandParams bool
	Params              []ParamDecl
	ReturnType          TypeExpr
	Body                []Stmt
	BodyExpr            Expr
}
type SizeofExpr struct {
	Position lexer.Pos
	Type     TypeExpr
}
type AlignofExpr struct {
	Position lexer.Pos
	Type     TypeExpr
}
type OffsetofExpr struct {
	Position lexer.Pos
	Type     TypeExpr
	Field    string
}
type TernaryExpr struct {
	Position lexer.Pos
	Value    Expr
	Cond     Expr
	Alt      Expr
}
type AddrOfExpr struct {
	Position lexer.Pos
	Operand  Expr
}
type SpecializeExpr struct {
	Position lexer.Pos
	Operand  Expr
	TypeArgs []TypeExpr
}
type StructLitExpr struct {
	Position lexer.Pos
	Name     string
	TypeArgs []TypeExpr
	Args     []Expr
	ArgNames []string
	Brace    bool

	ResolvedArgsValid bool
	ResolvedArgs      []Expr
}
type RecordUpdateExpr struct {
	Position lexer.Pos
	Base     Expr
	Args     []Expr
	ArgNames []string

	ResolvedArgsValid bool
	ResolvedArgs      []Expr
}
type TupleExpr struct {
	Position lexer.Pos
	Elems    []Expr
}
type VariantTestExpr struct {
	Position lexer.Pos
	Pattern  *MatchVariantPattern
}
