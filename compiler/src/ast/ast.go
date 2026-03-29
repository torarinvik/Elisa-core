package ast

import "llcontext/src/lexer"

type Node interface {
	Pos() lexer.Pos
	nodeTag()
}

type File struct {
	Filename string
	Decls    []Decl
}

type Decl interface {
	Node
	declTag()
}

type ConstDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
	Value    Expr
}

type ConstEnumDecl struct {
	Position lexer.Pos
	Name     string
	Storage  TypeExpr
	Members  []ConstEnumMemberDecl
}

type ConstEnumMemberDecl struct {
	Position lexer.Pos
	Name     string
	Value    Expr
}

type ErrorDecl struct {
	Position lexer.Pos
	Name     string
	Tags     []string
}

type PermissionDecl struct {
	Position lexer.Pos
	Name     string
	Members  []string
}

type NamespaceDecl struct {
	Position lexer.Pos
	Name     string
	Decls    []Decl
}

type UsingDecl struct {
	Position lexer.Pos
	Name     string
}

type EnumDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Packed      bool
	Common      []FieldDecl
	Variants    []EnumVariantDecl
}

type EnumVariantDecl struct {
	Position lexer.Pos
	Name     string
	Payload  []EnumPayloadDecl
}

type EnumPayloadDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
}

type GlobalDecl struct {
	Position lexer.Pos
	Mutable  bool
	Name     string
	Type     TypeExpr
	Value    Expr
}

type StructDecl struct {
	Position         lexer.Pos
	Annotations      []Annotation
	Name             string
	TypeParams       []string
	RefStorageParams []string
	RefStateParams   []string
	GenericParams    []GenericParam
	HasStateParam    bool
	StateParamCount  int
	Affine           bool
	ReprC            bool
	Fields           []FieldDecl
}

type GenericParamKind int

const (
	GenericParamType GenericParamKind = iota
	GenericParamRefStorage
	GenericParamRefState
	GenericParamRegion
	GenericParamPermission
)

type GenericParam struct {
	Position lexer.Pos
	Kind     GenericParamKind
	Name     string
}

type FieldDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Mutable     bool
	IsTail      bool
	Type        TypeExpr
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

type FuncDecl struct {
	Position         lexer.Pos
	Annotations      []Annotation
	Name             string
	TypeParams       []string
	RefStorageParams []string
	RefStateParams   []string
	RegionParams     []string
	PermissionParams []string
	GenericParams    []GenericParam
	Permissions      []PermissionRef
	Params           []ParamDecl
	ReturnType       TypeExpr
	Body             []Stmt
}

type ParamDecl struct {
	Position lexer.Pos
	Name     string
	Mutable  bool
	Type     TypeExpr
}

type ExternFuncDecl struct {
	Position     lexer.Pos
	Annotations  []Annotation
	Name         string
	RegionParams []string
	Permissions  []PermissionRef
	Params       []ParamDecl
	ReturnType   TypeExpr
	Variadic     bool
}

type ExternVarDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
}

type ExternTypeDecl struct {
	Position lexer.Pos
	Name     string
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

type AggregateStateTypeExpr struct {
	Position lexer.Pos
	Base     TypeExpr
	State    RefState
	States   []RefState
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

type BinaryExpr struct {
	Position lexer.Pos
	Op       lexer.TokenKind
	Left     Expr
	Right    Expr
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
	Position lexer.Pos
	Func     Expr
	Args     []Expr
	ArgNames []string

	ResolvedArgsValid  bool
	ResolvedArgs       []Expr
	ResolvedCommonArgs map[string]Expr
}

type FieldExpr struct {
	Position lexer.Pos
	Object   Expr
	Field    string
}

type IndexExpr struct {
	Position lexer.Pos
	Object   Expr
	Index    Expr
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

type CastExprOrigin int

const (
	CastExprOriginGeneral CastExprOrigin = iota
	CastExprOriginPostfixShorthand
)

type CastExpr struct {
	Position     lexer.Pos
	Operand      Expr
	Target       TypeExpr
	Origin       CastExprOrigin
	LegacySyntax bool
}

type SizeofExpr struct {
	Position lexer.Pos
	Type     TypeExpr
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
	Args     []Expr
}

type ParenExpr struct {
	Position lexer.Pos
	Inner    Expr
}

type RaiseExpr struct {
	Position lexer.Pos
	Error    Expr
}

type TryExpr struct {
	Position lexer.Pos
	Value    Expr
	Fallback Expr
}

type UnwrapElseExpr struct {
	Position lexer.Pos
	Value    Expr
	Fallback Expr
}

type AllocExpr struct {
	Position lexer.Pos
	Owner    Expr
	Value    Expr
}

type CanExpr struct {
	Position    lexer.Pos
	Expr        Expr
	Permissions []PermissionRef
}

type MatchExpr struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Arms     []MatchArm
}

type MatchPattern interface {
	Node
	matchPatternTag()
}

type MatchWildcardPattern struct {
	Position lexer.Pos
}

type MatchBindPattern struct {
	Position lexer.Pos
	Name     string
}

type MatchStringLiteralPattern struct {
	Position lexer.Pos
	Value    string
}

type MatchVariantPattern struct {
	Position     lexer.Pos
	EnumName     string
	Variant      string
	Args         []MatchPatternArg
	ResolvedArgs []*MatchPatternArg
}

type MatchPatternArg struct {
	Position lexer.Pos
	Name     string
	Pattern  MatchPattern
}

type MoveBindPattern interface {
	Node
	moveBindPatternTag()
}

type MoveBindNamePattern struct {
	Position lexer.Pos
	Name     string
}

type MoveBindStructPattern struct {
	Position lexer.Pos
	TypeName string
	Args     []MoveBindArg
}

type MoveBindVariantPattern struct {
	Position lexer.Pos
	EnumName string
	Variant  string
	Args     []MatchPatternArg
}

type ViewBindPattern struct {
	Position lexer.Pos
	EnumName string
	Variant  string
	Name     string
	Args     []MatchPatternArg
}

type MoveBindArg struct {
	Position lexer.Pos
	Name     string
}

type Stmt interface {
	Node
	stmtTag()
}

type AssignStmt struct {
	Position lexer.Pos
	Target   Expr
	Value    Expr
}

type AugAssignStmt struct {
	Position lexer.Pos
	Op       lexer.TokenKind
	Target   Expr
	Value    Expr
}

type AsRefAssignStmt struct {
	Position lexer.Pos
	Target   Expr
	AsKind   string
	Value    Expr
}

type VarDeclStmt struct {
	Position lexer.Pos
	Name     string
	Mutable  bool
	Type     TypeExpr
	Value    Expr
}

type MoveBindStmt struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Pattern  MoveBindPattern
}

type OpenStmt struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Pattern  *MoveBindVariantPattern
	Body     []Stmt
}

type ViewStmt struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Pattern  *ViewBindPattern
	Body     []Stmt
}

type ReturnStmt struct {
	Position lexer.Pos
	Value    Expr
}

type IfStmt struct {
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Then     []Stmt
	Elifs    []ElifClause
	Else     []Stmt
}

type WhileStmt struct {
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Body     []Stmt
}

type ForStmt struct {
	Position lexer.Pos
	Name     string
	Start    Expr
	End      Expr
	Step     Expr
	Op       lexer.TokenKind
	Body     []Stmt
}

type ParallelForStmt struct {
	Position  lexer.Pos
	Name      string
	IndexName string
	Source    Expr
	Body      []Stmt
}

type MatchStmt struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Arms     []MatchArm
}

type InStoreStmt struct {
	Position lexer.Pos
	Store    Expr
	Body     []Stmt
}

type CanStmt struct {
	Position    lexer.Pos
	Permissions []PermissionRef
	Body        []Stmt
}

type PoolStmt struct {
	Position lexer.Pos
	Name     string
	Workers  Expr
	Body     []Stmt
}

type LockStmt struct {
	Position  lexer.Pos
	Mutex     Expr
	GuardName string
	Body      []Stmt
}

type MatchArm struct {
	Position lexer.Pos
	Pattern  MatchPattern
	Body     []Stmt
}

type PassStmt struct {
	Position lexer.Pos
}

type PanicStmt struct {
	Position lexer.Pos
	Message  Expr
}

type ExprStmt struct {
	Position lexer.Pos
	Expr     Expr
}

type StaticIfStmt struct {
	Position lexer.Pos
	Cond     Expr
	Then     []Stmt
	Elifs    []StaticElifClause
	Else     []Stmt
}

type StaticErrorStmt struct {
	Position lexer.Pos
	Message  Expr
}

type DiscardStmt struct {
	Position lexer.Pos
	Value    Expr
}

type RegionStmt struct {
	Position lexer.Pos
	Name     string
	Capacity Expr
}

type DestroyStmt struct {
	Position lexer.Pos
	Name     string
}

type MarkStmt struct {
	Position   lexer.Pos
	RegionName string
	Name       string
}

type RestoreStmt struct {
	Position   lexer.Pos
	RegionName string
	MarkName   string
}

type ResetStmt struct {
	Position lexer.Pos
	Name     string
}

type ElifClause struct {
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Body     []Stmt
}

type StaticElifClause struct {
	Position lexer.Pos
	Cond     Expr
	Body     []Stmt
}

// ---------- Tag implementations ----------

func (n *ConstDecl) Pos() lexer.Pos     { return n.Position }
func (n *ConstEnumDecl) Pos() lexer.Pos { return n.Position }
func (n *ConstEnumMemberDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *ErrorDecl) Pos() lexer.Pos      { return n.Position }
func (n *PermissionDecl) Pos() lexer.Pos { return n.Position }
func (n *NamespaceDecl) Pos() lexer.Pos  { return n.Position }
func (n *UsingDecl) Pos() lexer.Pos      { return n.Position }
func (n *EnumDecl) Pos() lexer.Pos       { return n.Position }
func (n *GlobalDecl) Pos() lexer.Pos     { return n.Position }
func (n *StructDecl) Pos() lexer.Pos     { return n.Position }
func (n *FuncDecl) Pos() lexer.Pos       { return n.Position }
func (n *ExternFuncDecl) Pos() lexer.Pos { return n.Position }
func (n *ExternVarDecl) Pos() lexer.Pos  { return n.Position }
func (n *ExternTypeDecl) Pos() lexer.Pos { return n.Position }
func (n *ExportTypeDecl) Pos() lexer.Pos { return n.Position }
func (n *ExportFuncDecl) Pos() lexer.Pos { return n.Position }
func (n *ExportGlobalDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *StaticIfDecl) Pos() lexer.Pos { return n.Position }
func (n *NamedType) Pos() lexer.Pos    { return n.Position }
func (n *RefType) Pos() lexer.Pos      { return n.Position }
func (n *RefStateLiteralTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *RefStorageLiteralTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *GenericType) Pos() lexer.Pos { return n.Position }
func (n *AggregateStateTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *MutableType) Pos() lexer.Pos     { return n.Position }
func (n *TailType) Pos() lexer.Pos        { return n.Position }
func (n *ArrayType) Pos() lexer.Pos       { return n.Position }
func (n *BuiltinTypeExpr) Pos() lexer.Pos { return n.Position }
func (n *FuncTypeExpr) Pos() lexer.Pos    { return n.Position }
func (n *ErrorSetExpr) Pos() lexer.Pos    { return n.Position }
func (n *ErrorUnionTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *OptionalTypeExpr) Pos() lexer.Pos { return n.Position }
func (n *Ident) Pos() lexer.Pos            { return n.Position }
func (n *IntLit) Pos() lexer.Pos           { return n.Position }
func (n *FloatLit) Pos() lexer.Pos         { return n.Position }
func (n *StringLit) Pos() lexer.Pos        { return n.Position }
func (n *CharLit) Pos() lexer.Pos          { return n.Position }
func (n *BoolLit) Pos() lexer.Pos          { return n.Position }
func (n *NullLit) Pos() lexer.Pos          { return n.Position }
func (n *ZeroedLit) Pos() lexer.Pos        { return n.Position }
func (n *BinaryExpr) Pos() lexer.Pos       { return n.Position }
func (n *UnaryExpr) Pos() lexer.Pos        { return n.Position }
func (n *MoveExpr) Pos() lexer.Pos         { return n.Position }
func (n *CallExpr) Pos() lexer.Pos         { return n.Position }
func (n *FieldExpr) Pos() lexer.Pos        { return n.Position }
func (n *IndexExpr) Pos() lexer.Pos        { return n.Position }
func (n *SliceExpr) Pos() lexer.Pos        { return n.Position }
func (n *ListLitExpr) Pos() lexer.Pos      { return n.Position }
func (n *CastExpr) Pos() lexer.Pos         { return n.Position }
func (n *SizeofExpr) Pos() lexer.Pos       { return n.Position }
func (n *TernaryExpr) Pos() lexer.Pos      { return n.Position }
func (n *AddrOfExpr) Pos() lexer.Pos       { return n.Position }
func (n *SpecializeExpr) Pos() lexer.Pos   { return n.Position }
func (n *StructLitExpr) Pos() lexer.Pos    { return n.Position }
func (n *ParenExpr) Pos() lexer.Pos        { return n.Position }
func (n *RaiseExpr) Pos() lexer.Pos        { return n.Position }
func (n *TryExpr) Pos() lexer.Pos          { return n.Position }
func (n *UnwrapElseExpr) Pos() lexer.Pos   { return n.Position }
func (n *AllocExpr) Pos() lexer.Pos        { return n.Position }
func (n *CanExpr) Pos() lexer.Pos          { return n.Position }
func (n *MatchExpr) Pos() lexer.Pos        { return n.Position }
func (n *MatchWildcardPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MatchBindPattern) Pos() lexer.Pos { return n.Position }
func (n *MatchStringLiteralPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MatchVariantPattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindNamePattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindStructPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MoveBindVariantPattern) Pos() lexer.Pos { return n.Position }
func (n *ViewBindPattern) Pos() lexer.Pos        { return n.Position }
func (n *AssignStmt) Pos() lexer.Pos             { return n.Position }
func (n *AugAssignStmt) Pos() lexer.Pos          { return n.Position }
func (n *AsRefAssignStmt) Pos() lexer.Pos        { return n.Position }
func (n *VarDeclStmt) Pos() lexer.Pos            { return n.Position }
func (n *MoveBindStmt) Pos() lexer.Pos           { return n.Position }
func (n *OpenStmt) Pos() lexer.Pos               { return n.Position }
func (n *ViewStmt) Pos() lexer.Pos               { return n.Position }
func (n *ReturnStmt) Pos() lexer.Pos             { return n.Position }
func (n *IfStmt) Pos() lexer.Pos                 { return n.Position }
func (n *WhileStmt) Pos() lexer.Pos              { return n.Position }
func (n *ForStmt) Pos() lexer.Pos                { return n.Position }
func (n *ParallelForStmt) Pos() lexer.Pos        { return n.Position }
func (n *MatchStmt) Pos() lexer.Pos              { return n.Position }
func (n *InStoreStmt) Pos() lexer.Pos            { return n.Position }
func (n *CanStmt) Pos() lexer.Pos                { return n.Position }
func (n *PoolStmt) Pos() lexer.Pos               { return n.Position }
func (n *LockStmt) Pos() lexer.Pos               { return n.Position }
func (n *PassStmt) Pos() lexer.Pos               { return n.Position }
func (n *PanicStmt) Pos() lexer.Pos              { return n.Position }
func (n *ExprStmt) Pos() lexer.Pos               { return n.Position }
func (n *StaticIfStmt) Pos() lexer.Pos           { return n.Position }
func (n *StaticErrorStmt) Pos() lexer.Pos        { return n.Position }
func (n *DiscardStmt) Pos() lexer.Pos            { return n.Position }
func (n *RegionStmt) Pos() lexer.Pos             { return n.Position }
func (n *DestroyStmt) Pos() lexer.Pos            { return n.Position }
func (n *MarkStmt) Pos() lexer.Pos               { return n.Position }
func (n *RestoreStmt) Pos() lexer.Pos            { return n.Position }
func (n *ResetStmt) Pos() lexer.Pos              { return n.Position }

func (*ConstDecl) nodeTag()                 {}
func (*ConstEnumDecl) nodeTag()             {}
func (*ConstEnumMemberDecl) nodeTag()       {}
func (*ErrorDecl) nodeTag()                 {}
func (*PermissionDecl) nodeTag()            {}
func (*NamespaceDecl) nodeTag()             {}
func (*UsingDecl) nodeTag()                 {}
func (*EnumDecl) nodeTag()                  {}
func (*GlobalDecl) nodeTag()                {}
func (*StructDecl) nodeTag()                {}
func (*FuncDecl) nodeTag()                  {}
func (*ExternFuncDecl) nodeTag()            {}
func (*ExternVarDecl) nodeTag()             {}
func (*ExternTypeDecl) nodeTag()            {}
func (*ExportTypeDecl) nodeTag()            {}
func (*ExportFuncDecl) nodeTag()            {}
func (*ExportGlobalDecl) nodeTag()          {}
func (*StaticIfDecl) nodeTag()              {}
func (*NamedType) nodeTag()                 {}
func (*RefType) nodeTag()                   {}
func (*RefStateLiteralTypeExpr) nodeTag()   {}
func (*RefStorageLiteralTypeExpr) nodeTag() {}
func (*GenericType) nodeTag()               {}
func (*AggregateStateTypeExpr) nodeTag()    {}
func (*MutableType) nodeTag()               {}
func (*TailType) nodeTag()                  {}
func (*ArrayType) nodeTag()                 {}
func (*BuiltinTypeExpr) nodeTag()           {}
func (*FuncTypeExpr) nodeTag()              {}
func (*ErrorSetExpr) nodeTag()              {}
func (*ErrorUnionTypeExpr) nodeTag()        {}
func (*OptionalTypeExpr) nodeTag()          {}
func (*Ident) nodeTag()                     {}
func (*IntLit) nodeTag()                    {}
func (*FloatLit) nodeTag()                  {}
func (*StringLit) nodeTag()                 {}
func (*CharLit) nodeTag()                   {}
func (*BoolLit) nodeTag()                   {}
func (*NullLit) nodeTag()                   {}
func (*ZeroedLit) nodeTag()                 {}
func (*BinaryExpr) nodeTag()                {}
func (*UnaryExpr) nodeTag()                 {}
func (*MoveExpr) nodeTag()                  {}
func (*CallExpr) nodeTag()                  {}
func (*FieldExpr) nodeTag()                 {}
func (*IndexExpr) nodeTag()                 {}
func (*SliceExpr) nodeTag()                 {}
func (*ListLitExpr) nodeTag()               {}
func (*CastExpr) nodeTag()                  {}
func (*SizeofExpr) nodeTag()                {}
func (*TernaryExpr) nodeTag()               {}
func (*AddrOfExpr) nodeTag()                {}
func (*SpecializeExpr) nodeTag()            {}
func (*StructLitExpr) nodeTag()             {}
func (*ParenExpr) nodeTag()                 {}
func (*RaiseExpr) nodeTag()                 {}
func (*TryExpr) nodeTag()                   {}
func (*UnwrapElseExpr) nodeTag()            {}
func (*AllocExpr) nodeTag()                 {}
func (*CanExpr) nodeTag()                   {}
func (*MatchExpr) nodeTag()                 {}
func (*MatchWildcardPattern) nodeTag()      {}
func (*MatchBindPattern) nodeTag()          {}
func (*MatchStringLiteralPattern) nodeTag() {}
func (*MatchVariantPattern) nodeTag()       {}
func (*MoveBindNamePattern) nodeTag()       {}
func (*MoveBindStructPattern) nodeTag()     {}
func (*MoveBindVariantPattern) nodeTag()    {}
func (*ViewBindPattern) nodeTag()           {}
func (*AssignStmt) nodeTag()                {}
func (*AugAssignStmt) nodeTag()             {}
func (*AsRefAssignStmt) nodeTag()           {}
func (*VarDeclStmt) nodeTag()               {}
func (*MoveBindStmt) nodeTag()              {}
func (*OpenStmt) nodeTag()                  {}
func (*ViewStmt) nodeTag()                  {}
func (*ReturnStmt) nodeTag()                {}
func (*IfStmt) nodeTag()                    {}
func (*WhileStmt) nodeTag()                 {}
func (*ForStmt) nodeTag()                   {}
func (*ParallelForStmt) nodeTag()           {}
func (*MatchStmt) nodeTag()                 {}
func (*InStoreStmt) nodeTag()               {}
func (*CanStmt) nodeTag()                   {}
func (*PoolStmt) nodeTag()                  {}
func (*LockStmt) nodeTag()                  {}
func (*PassStmt) nodeTag()                  {}
func (*PanicStmt) nodeTag()                 {}
func (*ExprStmt) nodeTag()                  {}
func (*StaticIfStmt) nodeTag()              {}
func (*StaticErrorStmt) nodeTag()           {}
func (*DiscardStmt) nodeTag()               {}
func (*RegionStmt) nodeTag()                {}
func (*DestroyStmt) nodeTag()               {}
func (*MarkStmt) nodeTag()                  {}
func (*RestoreStmt) nodeTag()               {}
func (*ResetStmt) nodeTag()                 {}

func (*ConstDecl) declTag()        {}
func (*ConstEnumDecl) declTag()    {}
func (*ErrorDecl) declTag()        {}
func (*PermissionDecl) declTag()   {}
func (*NamespaceDecl) declTag()    {}
func (*UsingDecl) declTag()        {}
func (*EnumDecl) declTag()         {}
func (*GlobalDecl) declTag()       {}
func (*StructDecl) declTag()       {}
func (*FuncDecl) declTag()         {}
func (*ExternFuncDecl) declTag()   {}
func (*ExternVarDecl) declTag()    {}
func (*ExternTypeDecl) declTag()   {}
func (*ExportTypeDecl) declTag()   {}
func (*ExportFuncDecl) declTag()   {}
func (*ExportGlobalDecl) declTag() {}
func (*StaticIfDecl) declTag()     {}

func (*NamedType) typeExprTag()                 {}
func (*RefType) typeExprTag()                   {}
func (*RefStateLiteralTypeExpr) typeExprTag()   {}
func (*RefStorageLiteralTypeExpr) typeExprTag() {}
func (*GenericType) typeExprTag()               {}
func (*AggregateStateTypeExpr) typeExprTag()    {}
func (*MutableType) typeExprTag()               {}
func (*TailType) typeExprTag()                  {}
func (*ArrayType) typeExprTag()                 {}
func (*BuiltinTypeExpr) typeExprTag()           {}
func (*FuncTypeExpr) typeExprTag()              {}
func (*ErrorSetExpr) typeExprTag()              {}
func (*ErrorUnionTypeExpr) typeExprTag()        {}
func (*OptionalTypeExpr) typeExprTag()          {}

func (*Ident) exprTag()      {}
func (*IntLit) exprTag()     {}
func (*FloatLit) exprTag()   {}
func (*StringLit) exprTag()  {}
func (*CharLit) exprTag()    {}
func (*BoolLit) exprTag()    {}
func (*NullLit) exprTag()    {}
func (*ZeroedLit) exprTag()  {}
func (*BinaryExpr) exprTag() {}
func (*UnaryExpr) exprTag()  {}
func (*MoveExpr) exprTag()   {}
func (*CallExpr) exprTag()   {}
func (*FieldExpr) exprTag()  {}
func (*IndexExpr) exprTag()  {}
func (*SliceExpr) exprTag()  {}

func (*MatchWildcardPattern) matchPatternTag()      {}
func (*MatchBindPattern) matchPatternTag()          {}
func (*MatchStringLiteralPattern) matchPatternTag() {}
func (*MatchVariantPattern) matchPatternTag()       {}
func (*MoveBindNamePattern) moveBindPatternTag()    {}
func (*MoveBindStructPattern) moveBindPatternTag()  {}
func (*MoveBindVariantPattern) moveBindPatternTag() {}
func (*ListLitExpr) exprTag()                       {}
func (*CastExpr) exprTag()                          {}
func (*SizeofExpr) exprTag()                        {}
func (*TernaryExpr) exprTag()                       {}
func (*AddrOfExpr) exprTag()                        {}
func (*SpecializeExpr) exprTag()                    {}
func (*StructLitExpr) exprTag()                     {}
func (*ParenExpr) exprTag()                         {}
func (*RaiseExpr) exprTag()                         {}
func (*MatchExpr) exprTag()                         {}
func (*MatchStmt) stmtTag()                         {}
func (*TryExpr) exprTag()                           {}
func (*UnwrapElseExpr) exprTag()                    {}
func (*AllocExpr) exprTag()                         {}
func (*CanExpr) exprTag()                           {}

func (*AssignStmt) stmtTag()      {}
func (*AugAssignStmt) stmtTag()   {}
func (*AsRefAssignStmt) stmtTag() {}
func (*VarDeclStmt) stmtTag()     {}
func (*MoveBindStmt) stmtTag()    {}
func (*OpenStmt) stmtTag()        {}
func (*ViewStmt) stmtTag()        {}
func (*ReturnStmt) stmtTag()      {}
func (*IfStmt) stmtTag()          {}
func (*WhileStmt) stmtTag()       {}
func (*ForStmt) stmtTag()         {}
func (*ParallelForStmt) stmtTag() {}
func (*InStoreStmt) stmtTag()     {}
func (*CanStmt) stmtTag()         {}
func (*PoolStmt) stmtTag()        {}
func (*LockStmt) stmtTag()        {}
func (*PassStmt) stmtTag()        {}
func (*PanicStmt) stmtTag()       {}
func (*ExprStmt) stmtTag()        {}
func (*StaticIfStmt) stmtTag()    {}
func (*StaticErrorStmt) stmtTag() {}
func (*DiscardStmt) stmtTag()     {}
func (*RegionStmt) stmtTag()      {}
func (*DestroyStmt) stmtTag()     {}
func (*MarkStmt) stmtTag()        {}
func (*RestoreStmt) stmtTag()     {}
func (*ResetStmt) stmtTag()       {}

func (n *CallExpr) ArgName(index int) string {
	if n == nil || index < 0 || index >= len(n.ArgNames) {
		return ""
	}
	return n.ArgNames[index]
}

func (n *CallExpr) NamedArgCount() int {
	if n == nil {
		return 0
	}
	count := 0
	for _, name := range n.ArgNames {
		if name != "" {
			count++
		}
	}
	return count
}
