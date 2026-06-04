package ast

import "elisacore/src/lexer"

type StructTestExpr struct {
	Position lexer.Pos
	Pattern  *MatchStructPattern
}
type IsPatternExpr struct {
	Position lexer.Pos
	Targets  []Expr
	Brackets bool
}
type IsAliasExpr struct {
	Position lexer.Pos
	Target   Expr
	Alias    string
}
type TypeExprExpr struct {
	Position lexer.Pos
	Type     TypeExpr
}
type ParenExpr struct {
	Position lexer.Pos
	Inner    Expr
}
type RaiseExpr struct {
	Position lexer.Pos
	Error    Expr
}
type RecoveryKind int

const (
	RecoveryValue RecoveryKind = iota
	RecoveryReturn
	RecoveryRaise
	RecoveryVoid
	RecoveryBlock
)

type RecoveryClause struct {
	Position lexer.Pos
	Kind     RecoveryKind
	Value    Expr
	Binding  string
	Body     []Stmt
}
type TryExpr struct {
	Position                 lexer.Pos
	Value                    Expr
	Fallback                 Expr
	Recovery                 *RecoveryClause
	UsesDefaultShorthandForm bool
}

// GetExpr is the optional analog of TryExpr: `get expr` unwraps an Optional /
// nullable reference, propagating absence (early-return None) when there is no
// `else`. With a trailing `else` it carries a Fallback/Recovery exactly like
// TryExpr, lowering to the same recovery machinery.
type GetExpr struct {
	Position lexer.Pos
	Value    Expr
	Fallback Expr
	Recovery *RecoveryClause
}
type CatchArm struct {
	Position     lexer.Pos
	Name         string
	ErrorBinding bool
	Payload      []string // binder names for a matched variant's payload, e.g. `E.Bad(x, y):`
	Body         []Stmt
}
type CatchExpr struct {
	Position lexer.Pos
	Value    Expr
	Success  CatchArm
	Arms     []CatchArm
}
type UnwrapElseExpr struct {
	Position lexer.Pos
	Value    Expr
	Fallback Expr
	Recovery *RecoveryClause
}
type OptionalBindExpr struct {
	Position lexer.Pos
	Name     string
	Value    Expr
}
type AllocExpr struct {
	Position  lexer.Pos
	Owner     Expr
	Value     Expr
	NodeSugar bool
	NodeSpan  Expr
}
type CanExpr struct {
	Position                    lexer.Pos
	Expr                        Expr
	Permissions                 []PermissionRef
	SuppressPermissionInference bool
}
type MatchExpr struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Arms     []MatchArm
}
type VisitExpr struct {
	Position lexer.Pos
	Value    Expr
	Root     TypeExpr
	Arms     []VisitArm
}
type FoldExpr struct {
	Position       lexer.Pos
	Keyword        string
	Value          Expr
	Root           TypeExpr
	ResultType     TypeExpr
	RewriteDefault bool
	Arms           []VisitArm
}
type EmitExpr struct {
	Position lexer.Pos
	Value    Expr
	Nothing  bool
	All      bool
}
type VisitArmChildBinding struct {
	Position  lexer.Pos
	FieldName string
	BindName  string
}
type VisitArm struct {
	Position         lexer.Pos
	TargetName       string
	BindName         string
	ChildResultsName string
	ChildBindings    []VisitArmChildBinding
	Guard            Expr
	Wildcard         bool
	Body             []Stmt
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
type MatchLiteralPattern struct {
	Position lexer.Pos
	Value    Expr
	Pinned   bool
}
type MatchTuplePattern struct {
	Position lexer.Pos
	Elems    []MatchPattern
}
type MatchListPattern struct {
	Position lexer.Pos
	Elems    []MatchPattern
}
type MatchOrPattern struct {
	Position lexer.Pos
	Options  []MatchPattern
}
type MatchRestPattern struct {
	Position lexer.Pos
}
type MatchStructPattern struct {
	Position     lexer.Pos
	TypeName     string
	Args         []MatchPatternArg
	Brace        bool
	ResolvedArgs []*MatchPatternArg
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
	Brace    bool
}
type MoveBindTuplePattern struct {
	Position lexer.Pos
	Args     []MoveBindArg
}
type MoveBindVariantPattern struct {
	Position lexer.Pos
	EnumName string
	Variant  string
	Args     []MatchPatternArg
}
type MoveBindArg struct {
	Position lexer.Pos
	Field    string
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
	Optional bool
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
	Owner    Expr
}
type LocalParamsStmt struct {
	Position              lexer.Pos
	Name                  string
	Params                []ParamDecl
	DeprecatedSyntax      string
	DeprecatedReplacement string
}
type LetDestructureStmt struct {
	Position lexer.Pos
	Pattern  *MoveBindStructPattern
	Value    Expr
}
type TupleBindName struct {
	Position lexer.Pos
	Name     string
}
type TupleBindStmt struct {
	Position lexer.Pos
	Names    []TupleBindName
	Declare  bool
	Value    Expr
}
type MoveBindStmt struct {
	Position lexer.Pos
	Value    Expr
	Store    Expr
	Pattern  MoveBindPattern
}
type DeferMode int

const (
	DeferModeBlock DeferMode = iota
	DeferModeFunction
)

type DeferStmt struct {
	Position lexer.Pos
	Mode     DeferMode
	Body     []Stmt
}
type ReturnStmt struct {
	Position lexer.Pos
	Value    Expr
}
type BreakStmt struct {
	Position lexer.Pos
}
type ContinueStmt struct {
	Position lexer.Pos
}
type IfStmt struct {
	Position              lexer.Pos
	Hint                  BranchHint
	Cond                  Expr
	Then                  []Stmt
	Elifs                 []ElifClause
	Else                  []Stmt
	DeprecatedSyntax      string
	DeprecatedReplacement string
}
type WhileStmt struct {
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Body     []Stmt
}
type ForStmt struct {
	Position lexer.Pos
	Reverse  bool
	Name     string
	Start    Expr
	End      Expr
	Step     Expr
	Op       lexer.TokenKind
	Body     []Stmt
}
type IterBindMode int

const (
	IterBindValue IterBindMode = iota
	IterBindRef
	IterBindMutableRef
)

type IterForStmt struct {
	Position             lexer.Pos
	Reverse              bool
	Pattern              MoveBindPattern
	Mode                 IterBindMode
	Source               Expr
	PatternFilter        MatchPattern
	PatternFilterSubject string
	WhereFilter          Expr
	Filter               Expr
	Body                 []Stmt
}
type ParallelForStmt struct {
	Position  lexer.Pos
	Name      string
	IndexName string
	Source    Expr
	Body      []Stmt
}
type MatchStmt struct {
	Position                       lexer.Pos
	Value                          Expr
	Store                          Expr
	Arms                           []MatchArm
	DeprecatedIfStorePatternBinder bool
}
type ExpectPatternStmt struct {
	Position lexer.Pos
	Value    Expr
	Patterns []MatchPattern
}
type InStoreStmt struct {
	Position lexer.Pos
	Store    Expr
	Body     []Stmt
}
type CanStmt struct {
	Position                    lexer.Pos
	Permissions                 []PermissionRef
	Body                        []Stmt
	SuppressPermissionInference bool
	As                          string // checked `as <Family>` re-attribution target (Phase 4); "" if absent
}
type WithStmt struct {
	Position      lexer.Pos
	Args          []WithArg
	Bundles       []WithBundleUse
	WithItemOrder []WithItem
	Body          []Stmt
}
type ArgsScopeItem struct {
	Position lexer.Pos
	Arg      WithArg
	Pack     ParamPackUse
	IsPack   bool
}
type ArgsScopeStmt struct {
	Position   lexer.Pos
	Args       []WithArg
	ParamPacks []ParamPackUse
	ItemOrder  []ArgsScopeItem
	Body       []Stmt
}
type CascadeStmt struct {
	Position lexer.Pos
	Target   Expr
	Body     []Stmt
}
type ScopeStmt struct {
	Position lexer.Pos
	Guard    Expr
	Body     []Stmt
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
type SignalStmt struct {
	Position    lexer.Pos
	Permissions []PermissionRef
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
type StaticAssertStmt struct {
	Position lexer.Pos
	Cond     Expr
	Message  Expr
}
type StaticAssertBlockStmt struct {
	Position   lexer.Pos
	Assertions []StaticAssertItem
}
type StaticBlockStmt struct {
	Position lexer.Pos
	Body     []Stmt
}
type DiscardStmt struct {
	Position lexer.Pos
	Value    Expr
}
type RegionStmt struct {
	Position  lexer.Pos
	Name      string
	Capacity  Expr
	OwnerName string
	// Allocator selects the region's backing allocator via `region NAME(cap) using <name>:`.
	// Empty means the compile-time default backend; "malloc" backs blocks with libc malloc.
	Allocator string
	// Lazy marks a compiler-synthesized region (e.g. `in auto:`) whose first block is
	// created on demand by arena_alloc rather than eagerly at the declaration. A region
	// that never allocates costs only a zero-initialized Arena slot and an empty free.
	Lazy bool
	Body []Stmt
}
type DestroyStmt struct {
	Position lexer.Pos
	Name     string
}

// PromoteStmt is `promote <value> into <region>`: a copy-promote that relocates a
// value's own backing storage into a longer-lived region and re-types its
// provenance to that region. See docs/67.
type PromoteStmt struct {
	Position lexer.Pos
	Value    Expr
	Region   string
}

func (n *PromoteStmt) Pos() lexer.Pos { return n.Position }
func (*PromoteStmt) nodeTag()         {}
func (*PromoteStmt) stmtTag()         {}

// AdoptStmt is `adopt <child-region> into <parent-region>`: splices an entire
// child region into a longer-lived parent, consuming the child. See docs/67.
type AdoptStmt struct {
	Position lexer.Pos
	Child    string
	Parent   string
}

func (n *AdoptStmt) Pos() lexer.Pos { return n.Position }
func (*AdoptStmt) nodeTag()         {}
func (*AdoptStmt) stmtTag()         {}

type LeakStmt struct {
	Position lexer.Pos
	Name     string
}
type MarkStmt struct {
	Position   lexer.Pos
	RegionName string
	Name       string
}
type CheckpointStmt struct {
	Position lexer.Pos
	Name     string
	Target   Expr
	Body     []Stmt
}
type GroupedCheckpointStmt struct {
	Position lexer.Pos
	Targets  []Expr
	Body     []Stmt
}
type RestoreStmt struct {
	Position   lexer.Pos
	RegionName string
	MarkName   string
}
type RestoreCheckpointStmt struct {
	Position lexer.Pos
	Name     string
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

func (n *ConstDecl) Pos() lexer.Pos    { return n.Position }
func (n *TokenSetDecl) Pos() lexer.Pos { return n.Position }
func (n *CharsetDecl) Pos() lexer.Pos  { return n.Position }
func (n *KeywordMapDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *KeywordMapEntry) Pos() lexer.Pos {
	return n.Position
}
func (n *ConstEnumDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *ConstEnumMemberDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *ErrorDecl) Pos() lexer.Pos      { return n.Position }
func (n *EffectsDecl) Pos() lexer.Pos    { return n.Position }
func (n *EffectDecl) Pos() lexer.Pos     { return n.Position }
func (n *PermissionDecl) Pos() lexer.Pos { return n.Position }
func (n *GrantAliasDecl) Pos() lexer.Pos { return n.Position }
func (n *ContextDecl) Pos() lexer.Pos    { return n.Position }
func (n *ParamsDecl) Pos() lexer.Pos     { return n.Position }
func (n *NamespaceDecl) Pos() lexer.Pos  { return n.Position }
func (n *UsingDecl) Pos() lexer.Pos      { return n.Position }
func (n *ImportDecl) Pos() lexer.Pos     { return n.Position }
func (n *EnumDecl) Pos() lexer.Pos       { return n.Position }
func (n *TreeDecl) Pos() lexer.Pos       { return n.Position }
func (n *GrammarDecl) Pos() lexer.Pos    { return n.Position }
func (n *GrammarEnvDecl) Pos() lexer.Pos { return n.Position }
func (n *LexerDecl) Pos() lexer.Pos      { return n.Position }
func (n *GrammarProductionDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPassTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarTokenTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarTokenKindTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarCallTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarChoiceTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarOptionalTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarWhenTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarMatchTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarRecoverTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarRequiredTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarDelimitedTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSeqTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarLookaheadTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarExprTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSingletonTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarEmptyTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarConcatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarGuardTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarAttemptTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarCutTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarListTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarRepeatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarFlatRepeatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarWhileTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSeparatedTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSuffixTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPostfixTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPrecedenceTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarInfixTableTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarTokenSetRefTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarFirstTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarApplyTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarBindTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarAssignTerm) Pos() lexer.Pos {
	return n.Position
}
