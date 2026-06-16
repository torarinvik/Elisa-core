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
	GenericParamRegion
	GenericParamPermission
	GenericParamErrorSet
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
	Position     lexer.Pos
	Annotations  []Annotation
	Name         string
	Mutable      bool
	IsTail       bool
	Type         TypeExpr
	DefaultValue Expr
	BitGroup     *BitGroupDecl
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
type FuncDecl struct {
	Position         lexer.Pos
	Annotations      []Annotation
	Static           bool
	Override         bool
	Name             string
	TypeParams       []string
	RegionParams     []string
	PermissionParams []string
	GenericParams    []GenericParam
	Permissions      []PermissionRef
	Ensures          []EnsuresClause
	// Requires holds value-contract preconditions (`requires <bool-expr>` clauses), checked at
	// function entry in debug builds only (elided in release), like the bounds-check watchdog.
	// Distinct from Ensures, which is the typestate/resource-state contract.
	Requires []Expr
	// EnsureValues holds value-contract postconditions (`ensure <bool-expr>`), checked at every
	// return in debug builds. They may reference `result` (the returned value) and `old(expr)`
	// (the value of expr at function entry). Distinct from Ensures (typestate).
	EnsureValues []Expr
	Params       []ParamDecl
	ReturnType   TypeExpr
	Body         []Stmt
}
type ParamDecl struct {
	Position     lexer.Pos
	Name         string
	Mutable      bool
	Type         TypeExpr
	DefaultValue Expr
}
type ExternFuncDecl struct {
	Position         lexer.Pos
	Annotations      []Annotation
	Override         bool
	Name             string
	TypeParams       []string
	PermissionParams []string
	GenericParams    []GenericParam
	RegionParams     []string
	Permissions      []PermissionRef
	Ensures          []EnsuresClause
	Params           []ParamDecl
	ReturnType       TypeExpr
	Variadic         bool
}
type ExternVarDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Type        TypeExpr
}
type ExternTypeDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
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
type StaticAssertDecl struct {
	Position lexer.Pos
	Cond     Expr
	Message  Expr
}
type StaticAssertItem struct {
	Position lexer.Pos
	Cond     Expr
	Message  Expr
}
type StaticAssertBlockDecl struct {
	Position   lexer.Pos
	Assertions []StaticAssertItem
}
type StaticGenerateDecl struct {
	Position lexer.Pos
	Body     []StaticGenerateStmt
}
type StaticGenerateStmt interface {
	Node
	staticGenerateStmtTag()
}
type StaticGenerateEmitDecl struct {
	Position lexer.Pos
	Tokens   []lexer.Token
}
type StaticGenerateForDecl struct {
	Position lexer.Pos
	Name     string
	Source   Expr
	Filter   Expr
	Body     []StaticGenerateStmt
}
type StaticGenerateIfDecl struct {
	Position lexer.Pos
	Cond     Expr
	Then     []StaticGenerateStmt
	Elifs    []StaticGenerateElifDecl
	Else     []StaticGenerateStmt
}
type StaticGenerateElifDecl struct {
	Position lexer.Pos
	Cond     Expr
	Body     []StaticGenerateStmt
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
	Position lexer.Pos
	Elem     TypeExpr
	State    RefState
	Storage  RefStorage
	Region   string
	Explicit bool
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
	// Region is the `@r` allocation-region suffix on a generic-type use site
	// (`Box[i64] @r`, docs/68 §5), mirroring BuiltinTypeExpr.Region for containers.
	Region string
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

// OwnedType marks a binding/parameter as OWNING the lifetime/resource
// obligation of an allocator/store value (e.g. `owned Arena`): it is affine
// (move-only) and must be consumed (destroy/leak/move/return). A plain
// (non-owned) store value/borrow keeps its existing semantics unchanged.
type OwnedType struct {
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
	// Region is an explicit `@r` region annotation on a container type
	// (region-parameterized containers, Phase 2). Empty == inferred from scope.
	Region string
}
type FuncTypeExpr struct {
	Position    lexer.Pos
	Params      []TypeExpr
	Return      TypeExpr
	Permissions []PermissionRef
	Variadic    bool
}
type ErrorTagExpr struct {
	Position lexer.Pos
	SetName  string
	Tag      string
	// Family marks an explicit `*Set` whole-family spread. A bare `Name`
	// (Family=false, Tag="") resolves as a whole family if Name is a declared
	// set, otherwise as a single variant searched across all sets.
	Family bool
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
	ArgItemOrder  []CallArgItem
	Safe          bool
	// SafeConcurrencySugar marks a call node that the parser synthesized from a
	// safe concurrency construct (e.g. `submit`/`pool` scope lowering to a
	// `pool_submit1` call). User code never wrote the raw primitive, so the
	// raw-concurrency-surface-removed diagnostic must not fire on it.
	SafeConcurrencySugar bool

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

// EnumColumnExpr is a first-class column scan over a `layout soa` enum store:
// `Expr of .field` yields the dense column of `field` across every node in the
// implicit store (docs/76 §5). Enum names the enum type; Field is the selected
// common/tag column.
type EnumColumnExpr struct {
	Position lexer.Pos
	Enum     string
	Field    string
}
type CallArgItem struct {
	Position lexer.Pos
	ArgIndex int
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
	// LegacyElseFallback marks a postfix `xs[i] else fallback` parsed directly from
	// source. `get xs[i] else ...` still reuses IndexExpr.Fallback internally, so
	// later stages use this marker to reject only the legacy implicit form.
	LegacyElseFallback bool
	// AsSpecialize is set by the analyzer when `fn[T]` over a generic function is reinterpreted
	// as a single-type-arg value specialization (the single-arg counterpart of the multi-arg
	// `fn[A, B]` SpecializeExpr the parser produces). When non-nil, codegen emits this instead
	// of an index. nil for ordinary indexing.
	AsSpecialize *SpecializeExpr
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
	Spreads  []bool
	Brace    bool
	Owner    Expr
	// Keys is non-nil for a brace DICT literal `{k1: v1, k2: v2}`: Keys[i] is the key for the
	// value Elems[i]. A brace literal with Keys == nil is a set-membership literal (`{a, b}`)
	// or, when empty, an empty dict initializer disambiguated by the expected type.
	Keys []Expr
}
type MembershipRangeExpr struct {
	Position lexer.Pos
	Start    Expr
	End      Expr
	Op       lexer.TokenKind
}
type ListComprehensionExpr struct {
	Position  lexer.Pos
	Value     Expr
	Name      string
	Source    Expr
	RangeEnd  Expr
	RangeStep Expr
	RangeOp   lexer.TokenKind
	Filter    Expr
	Owner     Expr
	// Key, when non-nil, marks a dict comprehension `{ Key: Value for ... }`; the
	// result is dict[KeyType, ValueType] instead of darray[ValueType].
	Key Expr
	// Set marks a set comprehension `{ Value for ... }`; result is set[ValueType].
	Set bool
	// Bindings are comma-head `name [:T] = e` per-element lets, in scope for Key,
	// Value, and Filter. Each is a *VarDeclStmt; recomputed every iteration.
	Bindings []Stmt
	// Parallel marks a `by par` list-map comprehension: a parallel map over the source's disjoint
	// bands. The analyzer lowers it (LoweredParallel) once the element type is known.
	Parallel bool
	// LoweredParallel is the analyzer-synthesized desugar of a `by par` map — a block that presizes
	// the output and fills it via the runtime `par_map` combinator. Set by the analyzer (which knows
	// the element type), emitted by the backend in place of the sequential comprehension. nil
	// otherwise. Mirrors BinaryExpr.LoweredCall.
	LoweredParallel Expr
}
type QueryExprKind int

const (
	QueryExprAny QueryExprKind = iota
	QueryExprAll
	QueryExprFirst
	QueryExprCount
	QueryExprEach
	// QueryExprSum / QueryExprProduct are the monoid reducers `sum x in xs [where p]` and
	// `product x in xs [where p]`: they fold the (optionally filtered) numeric elements with + (unit
	// 0) or * (unit 1). Unlike the other query kinds, `where` is optional (an unconditional sum is
	// common). Result type is the element type.
	QueryExprSum
	QueryExprProduct
	// QueryExprMin / QueryExprMax: `min x in xs [where p]` / `max x in xs [where p]`. They have no
	// identity element, so they return `T?` — null over an empty (or fully-filtered) sequence,
	// otherwise the smallest/largest element. `where` is optional, like the other reducers.
	QueryExprMin
	QueryExprMax
)

type QueryExpr struct {
	Position             lexer.Pos
	Kind                 QueryExprKind
	Name                 string
	Pattern              MoveBindPattern
	Source               Expr
	Filter               Expr
	PatternFilter        MatchPattern
	PatternFilterSubject string
	Projection           Expr
	Owner                Expr
}
type CastExprOrigin int

const (
	CastExprOriginGeneral CastExprOrigin = iota
	CastExprOriginToSyntax
	CastExprOriginAsSyntax
	CastExprOriginExplicitCast
	CastExprOriginPostfixShorthand
	// CastExprOriginIndirectCall marks the synthetic cast produced by the
	// postfix `value.call_as[func(...)->T](args)` form: the operand (a raw
	// pointer-like value) is reinterpreted as a function pointer with the given
	// signature so the surrounding CallExpr lowers to a native indirect call.
	// The cast itself does NOT require Unsafe.PointerCast; the call requires
	// Unsafe.IndirectCall instead (the single capability gating the operation).
	CastExprOriginIndirectCall
)

type CastExpr struct {
	Position lexer.Pos
	Operand  Expr
	Target   TypeExpr
	Origin   CastExprOrigin
	// RefShorthand marks a cast parsed from the removed `x.ref[T]` reference
	// shorthand. The analyzer rejects it with the targeted `&x` / `(&x).cast[T]`
	// replacement.
	RefShorthand bool
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
	Spreads  []Expr

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
