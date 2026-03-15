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

type GlobalDecl struct {
	Position lexer.Pos
	Mutable  bool
	Name     string
	Type     TypeExpr
	Value    Expr
}

type StructDecl struct {
	Position   lexer.Pos
	Name       string
	TypeParams []string
	ReprC      bool
	Fields     []FieldDecl
}

type FieldDecl struct {
	Position lexer.Pos
	Name     string
	Mutable  bool
	IsTail   bool
	Type     TypeExpr
}

type FuncDecl struct {
	Position   lexer.Pos
	Name       string
	TypeParams []string
	Params     []ParamDecl
	ReturnType TypeExpr
	Body       []Stmt
}

type ParamDecl struct {
	Position lexer.Pos
	Name     string
	Mutable  bool
	Type     TypeExpr
}

type ExternFuncDecl struct {
	Position   lexer.Pos
	Name       string
	Params     []ParamDecl
	ReturnType TypeExpr
	Variadic   bool
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

type RefType struct {
	Position lexer.Pos
	Elem     TypeExpr
	State    RefState
}

type GenericType struct {
	Position lexer.Pos
	Name     string
	Args     []TypeExpr
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

type StringLit struct {
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

type CallExpr struct {
	Position lexer.Pos
	Func     Expr
	Args     []Expr
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

type CastExpr struct {
	Position lexer.Pos
	Operand  Expr
	Target   TypeExpr
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

type StructLitExpr struct {
	Position lexer.Pos
	Name     string
	Args     []Expr
}

type ParenExpr struct {
	Position lexer.Pos
	Inner    Expr
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

type ReturnStmt struct {
	Position lexer.Pos
	Value    Expr
}

type IfStmt struct {
	Position lexer.Pos
	Cond     Expr
	Then     []Stmt
	Elifs    []ElifClause
	Else     []Stmt
}

type WhileStmt struct {
	Position lexer.Pos
	Cond     Expr
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

type ElifClause struct {
	Position lexer.Pos
	Cond     Expr
	Body     []Stmt
}

type StaticElifClause struct {
	Position lexer.Pos
	Cond     Expr
	Body     []Stmt
}

// ---------- Tag implementations ----------

func (n *ConstDecl) Pos() lexer.Pos       { return n.Position }
func (n *GlobalDecl) Pos() lexer.Pos      { return n.Position }
func (n *StructDecl) Pos() lexer.Pos      { return n.Position }
func (n *FuncDecl) Pos() lexer.Pos        { return n.Position }
func (n *ExternFuncDecl) Pos() lexer.Pos  { return n.Position }
func (n *ExternVarDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExternTypeDecl) Pos() lexer.Pos  { return n.Position }
func (n *StaticIfDecl) Pos() lexer.Pos    { return n.Position }
func (n *NamedType) Pos() lexer.Pos       { return n.Position }
func (n *RefType) Pos() lexer.Pos         { return n.Position }
func (n *GenericType) Pos() lexer.Pos     { return n.Position }
func (n *MutableType) Pos() lexer.Pos     { return n.Position }
func (n *TailType) Pos() lexer.Pos        { return n.Position }
func (n *ArrayType) Pos() lexer.Pos       { return n.Position }
func (n *Ident) Pos() lexer.Pos           { return n.Position }
func (n *IntLit) Pos() lexer.Pos          { return n.Position }
func (n *StringLit) Pos() lexer.Pos       { return n.Position }
func (n *BoolLit) Pos() lexer.Pos         { return n.Position }
func (n *NullLit) Pos() lexer.Pos         { return n.Position }
func (n *ZeroedLit) Pos() lexer.Pos       { return n.Position }
func (n *BinaryExpr) Pos() lexer.Pos      { return n.Position }
func (n *UnaryExpr) Pos() lexer.Pos       { return n.Position }
func (n *CallExpr) Pos() lexer.Pos        { return n.Position }
func (n *FieldExpr) Pos() lexer.Pos       { return n.Position }
func (n *IndexExpr) Pos() lexer.Pos       { return n.Position }
func (n *CastExpr) Pos() lexer.Pos        { return n.Position }
func (n *SizeofExpr) Pos() lexer.Pos      { return n.Position }
func (n *TernaryExpr) Pos() lexer.Pos     { return n.Position }
func (n *AddrOfExpr) Pos() lexer.Pos      { return n.Position }
func (n *StructLitExpr) Pos() lexer.Pos   { return n.Position }
func (n *ParenExpr) Pos() lexer.Pos       { return n.Position }
func (n *AssignStmt) Pos() lexer.Pos      { return n.Position }
func (n *AugAssignStmt) Pos() lexer.Pos   { return n.Position }
func (n *AsRefAssignStmt) Pos() lexer.Pos { return n.Position }
func (n *VarDeclStmt) Pos() lexer.Pos     { return n.Position }
func (n *ReturnStmt) Pos() lexer.Pos      { return n.Position }
func (n *IfStmt) Pos() lexer.Pos          { return n.Position }
func (n *WhileStmt) Pos() lexer.Pos       { return n.Position }
func (n *PassStmt) Pos() lexer.Pos        { return n.Position }
func (n *PanicStmt) Pos() lexer.Pos       { return n.Position }
func (n *ExprStmt) Pos() lexer.Pos        { return n.Position }
func (n *StaticIfStmt) Pos() lexer.Pos    { return n.Position }
func (n *StaticErrorStmt) Pos() lexer.Pos { return n.Position }
func (n *DiscardStmt) Pos() lexer.Pos     { return n.Position }

func (*ConstDecl) nodeTag()       {}
func (*GlobalDecl) nodeTag()      {}
func (*StructDecl) nodeTag()      {}
func (*FuncDecl) nodeTag()        {}
func (*ExternFuncDecl) nodeTag()  {}
func (*ExternVarDecl) nodeTag()   {}
func (*ExternTypeDecl) nodeTag()  {}
func (*StaticIfDecl) nodeTag()    {}
func (*NamedType) nodeTag()       {}
func (*RefType) nodeTag()         {}
func (*GenericType) nodeTag()     {}
func (*MutableType) nodeTag()     {}
func (*TailType) nodeTag()        {}
func (*ArrayType) nodeTag()       {}
func (*Ident) nodeTag()           {}
func (*IntLit) nodeTag()          {}
func (*StringLit) nodeTag()       {}
func (*BoolLit) nodeTag()         {}
func (*NullLit) nodeTag()         {}
func (*ZeroedLit) nodeTag()       {}
func (*BinaryExpr) nodeTag()      {}
func (*UnaryExpr) nodeTag()       {}
func (*CallExpr) nodeTag()        {}
func (*FieldExpr) nodeTag()       {}
func (*IndexExpr) nodeTag()       {}
func (*CastExpr) nodeTag()        {}
func (*SizeofExpr) nodeTag()      {}
func (*TernaryExpr) nodeTag()     {}
func (*AddrOfExpr) nodeTag()      {}
func (*StructLitExpr) nodeTag()   {}
func (*ParenExpr) nodeTag()       {}
func (*AssignStmt) nodeTag()      {}
func (*AugAssignStmt) nodeTag()   {}
func (*AsRefAssignStmt) nodeTag() {}
func (*VarDeclStmt) nodeTag()     {}
func (*ReturnStmt) nodeTag()      {}
func (*IfStmt) nodeTag()          {}
func (*WhileStmt) nodeTag()       {}
func (*PassStmt) nodeTag()        {}
func (*PanicStmt) nodeTag()       {}
func (*ExprStmt) nodeTag()        {}
func (*StaticIfStmt) nodeTag()    {}
func (*StaticErrorStmt) nodeTag() {}
func (*DiscardStmt) nodeTag()     {}

func (*ConstDecl) declTag()      {}
func (*GlobalDecl) declTag()     {}
func (*StructDecl) declTag()     {}
func (*FuncDecl) declTag()       {}
func (*ExternFuncDecl) declTag() {}
func (*ExternVarDecl) declTag()  {}
func (*ExternTypeDecl) declTag() {}
func (*StaticIfDecl) declTag()   {}

func (*NamedType) typeExprTag()   {}
func (*RefType) typeExprTag()     {}
func (*GenericType) typeExprTag() {}
func (*MutableType) typeExprTag() {}
func (*TailType) typeExprTag()    {}
func (*ArrayType) typeExprTag()   {}

func (*Ident) exprTag()         {}
func (*IntLit) exprTag()        {}
func (*StringLit) exprTag()     {}
func (*BoolLit) exprTag()       {}
func (*NullLit) exprTag()       {}
func (*ZeroedLit) exprTag()     {}
func (*BinaryExpr) exprTag()    {}
func (*UnaryExpr) exprTag()     {}
func (*CallExpr) exprTag()      {}
func (*FieldExpr) exprTag()     {}
func (*IndexExpr) exprTag()     {}
func (*CastExpr) exprTag()      {}
func (*SizeofExpr) exprTag()    {}
func (*TernaryExpr) exprTag()   {}
func (*AddrOfExpr) exprTag()    {}
func (*StructLitExpr) exprTag() {}
func (*ParenExpr) exprTag()     {}

func (*AssignStmt) stmtTag()      {}
func (*AugAssignStmt) stmtTag()   {}
func (*AsRefAssignStmt) stmtTag() {}
func (*VarDeclStmt) stmtTag()     {}
func (*ReturnStmt) stmtTag()      {}
func (*IfStmt) stmtTag()          {}
func (*WhileStmt) stmtTag()       {}
func (*PassStmt) stmtTag()        {}
func (*PanicStmt) stmtTag()       {}
func (*ExprStmt) stmtTag()        {}
func (*StaticIfStmt) stmtTag()    {}
func (*StaticErrorStmt) stmtTag() {}
func (*DiscardStmt) stmtTag()     {}
