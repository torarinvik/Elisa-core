package ast

import "elisacore/src/lexer"

type GrammarDecl struct {
	Position               lexer.Pos
	Extend                 bool
	Name                   string
	TypeParams             []string
	RegionParams           []string
	PermissionParams       []string
	GenericParams          []GenericParam
	EnvType                TypeExpr
	OverType               TypeExpr
	UsingType              TypeExpr
	Uses                   []TypeExpr
	ErrorType              TypeExpr
	CursorExpr             Expr
	AllocExpr              Expr
	TokenKindType          TypeExpr
	TokenEnumName          string
	TokenEnumStorage       TypeExpr
	EOFExpr                Expr
	TokenKindField         string
	CurrentFunc            string
	AdvanceFunc            string
	ExpectFunc             string
	ExpectKindFunc         string
	RecordErrorFunc        string
	TokenLookupFunc        string
	TokenLookupCompareFunc string
	TokenAliases           []GrammarTokenAliasDecl
	Channels               []GrammarChannelDecl
	TokenSets              []GrammarTokenSetDecl
	GrammarAliases         []GrammarAliasDecl
	GrammarFns             []GrammarFnDecl
	RecoveryPolicies       []GrammarRecoveryDecl
	InfixTables            []GrammarInfixTableDecl
	Productions            []GrammarProductionDecl
}
type GrammarEnvDecl struct {
	Position         lexer.Pos
	Name             string
	OverType         TypeExpr
	UsingType        TypeExpr
	ErrorType        TypeExpr
	CursorExpr       Expr
	AllocExpr        Expr
	TokenKindType    TypeExpr
	TokenGrammarName string
	EOFExpr          Expr
	TokenKindField   string
	CurrentFunc      string
	AdvanceFunc      string
	ExpectFunc       string
	ExpectKindFunc   string
	RecordErrorFunc  string
}
type LexerDecl struct {
	Position           lexer.Pos
	Name               string
	TokenKindType      TypeExpr
	ModeEnumName       string
	GrammarName        string
	KeywordCompareFunc string
	Modes              []LexerModeDecl
	CharClasses        []LexerCharClassDecl
	Keywords           *LexerKeywordDecl
	Literals           *LexerLiteralDecl
}
type LexerModeDecl struct {
	Position lexer.Pos
	Name     string
}
type LexerCharClassDecl struct {
	Position lexer.Pos
	Name     string
	Terms    []LexerCharClassTerm
}
type LexerCharClassTerm struct {
	Position lexer.Pos
	Name     string
	Start    string
	End      string
	Range    bool
	Ref      bool
}
type LexerKeywordDecl struct {
	Position lexer.Pos
	Fallback string
	Entries  []LexerKeywordEntry
}
type LexerKeywordEntry struct {
	Position lexer.Pos
	Text     string
	Kind     string
}
type LexerLiteralDecl struct {
	Position lexer.Pos
	Fallback string
	Longest  bool
	Entries  []LexerLiteralEntry
}
type LexerLiteralEntry struct {
	Position lexer.Pos
	Text     string
	Kind     string
}
type GrammarTokenAliasDecl struct {
	Position   lexer.Pos
	Kind       string
	Literal    string
	HasLiteral bool
}
type GrammarChannelDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
	Default  Expr
}
type GrammarTokenSetDecl struct {
	Position    lexer.Pos
	Name        string
	TokenFamily bool
	Terms       []GrammarTerm
	// Excluded holds `- item` difference operands: token kinds (or whole
	// tokensets, resolved recursively) removed from the union of Terms.
	Excluded []GrammarTerm
}
type GrammarAliasDecl struct {
	Position lexer.Pos
	Name     string
	Params   []GrammarFnParam
	Term     GrammarTerm
}
type GrammarFnDecl struct {
	Position      lexer.Pos
	Name          string
	TypeCtor      bool
	Shorthand     bool
	TypeParams    []string
	GenericParams []GenericParam
	Params        []GrammarFnParam
	Return        GrammarFnType
	Terms         []GrammarTerm
}
type GrammarFnParam struct {
	Position    lexer.Pos
	Name        string
	Type        GrammarFnType
	Default     GrammarTerm
	DefaultExpr Expr
}
type GrammarFnType struct {
	Position lexer.Pos
	Kind     string
	Result   TypeExpr
}
type GrammarRecoveryDecl struct {
	Position lexer.Pos
	Name     string
	Message  Expr
	Until    []GrammarTerm
	Fallback Expr
}
type GrammarInfixTableDecl struct {
	Position lexer.Pos
	Name     string
	Result   string
	Levels   []GrammarPrecedenceLevel
	// Dynamic marks a runtime-fixity table (`dynamic infix table T(state.lookup) -> V:`);
	// nil for static tables.
	Dynamic *GrammarDynamicInfixSpec
}

// GrammarDynamicInfixSpec describes a dynamic infix table: precedence climbing
// with binding power fetched from a host fixity callback instead of static levels.
type GrammarDynamicInfixSpec struct {
	// FixityRef is the dotted host callback: token -> (active: bool,
	// precedence: i64, right_assoc: bool, info: T-opaque).
	FixityRef  string
	ReturnType TypeExpr
	Atom       GrammarTerm
	// CombineParams names (left, op_token, right, info) for the Combine expr.
	CombineParams []string
	Combine       Expr
}

// GrammarDynamicClimbTerm is the synthesized body of a dynamic infix table's
// climb production; it never appears in user-written grammars.
type GrammarDynamicClimbTerm struct {
	Position    lexer.Pos
	TableName   string
	MinPrecName string
	Spec        *GrammarDynamicInfixSpec
}
type GrammarProductionDecl struct {
	Position      lexer.Pos
	Public        bool
	Append        bool
	Name          string
	HasParamList  bool
	Params        []ParamDecl
	ReturnType    TypeExpr
	RecoverPolicy string
	RecoverMsg    Expr
	RecoverUntil  []GrammarTerm
	RecoverValue  Expr
	Channels      []GrammarChannelDecl
	Terms         []GrammarTerm
	// ExternalHost records the dotted host-function reference of an external
	// rule (`grammar name -> T = state.parse_fn`). The decl is already desugared
	// to a single return term at parse time; this field only preserves the
	// declared seam for formatting.
	ExternalHost string
}
type GrammarTerm interface {
	Node
	grammarTermTag()
}
type GrammarPassTerm struct {
	Position lexer.Pos
}
type GrammarTokenTerm struct {
	Position lexer.Pos
	Value    string
}
type GrammarTokenKindTerm struct {
	Position lexer.Pos
	Kind     string
}
type GrammarCallTerm struct {
	Position lexer.Pos
	Name     string
	Explicit bool
	Args     []Expr
}
type GrammarChoiceTerm struct {
	Position lexer.Pos
	Options  []GrammarTerm
}
type GrammarOptionalTerm struct {
	Position lexer.Pos
	Term     GrammarTerm
}
type GrammarWhenTerm struct {
	Position      lexer.Pos
	Cond          Expr
	TokenKindGate string
	Then          GrammarTerm
	Else          GrammarTerm
}
type GrammarMatchArm struct {
	Position lexer.Pos
	Patterns []MatchPattern
	Term     GrammarTerm
}
type GrammarMatchTerm struct {
	Position lexer.Pos
	Value    Expr
	Arms     []GrammarMatchArm
}
type GrammarRecoverTerm struct {
	Position      lexer.Pos
	Term          GrammarTerm
	RecoverPolicy string
	RecoverMsg    Expr
	RecoverUntil  []GrammarTerm
	RecoverValue  Expr
}
type GrammarRequiredTerm struct {
	Position lexer.Pos
	Term     GrammarTerm
	Message  Expr
}
type GrammarDelimitedTerm struct {
	Position lexer.Pos
	Open     GrammarTerm
	Body     GrammarTerm
	Close    GrammarTerm
	Message  Expr
}
type GrammarSeqTerm struct {
	Position lexer.Pos
	Terms    []GrammarTerm
}
type GrammarLookaheadTerm struct {
	Position lexer.Pos
	Term     GrammarTerm
}
type GrammarExprTerm struct {
	Position lexer.Pos
	Type     TypeExpr
	Expr     Expr
}
type GrammarSingletonTerm struct {
	Position lexer.Pos
	Type     TypeExpr
	Value    Expr
}
type GrammarEmptyTerm struct {
	Position lexer.Pos
	Type     TypeExpr
}
type GrammarConcatTerm struct {
	Position lexer.Pos
	Terms    []GrammarTerm
}
type GrammarGuardTerm struct {
	Position lexer.Pos
	Cond     Expr
}
type GrammarAttemptTerm struct {
	Position lexer.Pos
	Expr     Expr
}
type GrammarCutTerm struct {
	Position lexer.Pos
}
type GrammarListTerm struct {
	Position  lexer.Pos
	Elem      GrammarTerm
	Separator GrammarTerm
	Until     []GrammarTerm
}
type GrammarRepeatTerm struct {
	Position lexer.Pos
	Elem     GrammarTerm
	Until    []GrammarTerm
	// MinOne marks one-or-more repetition (`term+`): the repetition fails as an
	// uncommitted attempt when zero items match, instead of yielding an empty list.
	MinOne bool
}
type GrammarFlatRepeatTerm struct {
	Position lexer.Pos
	Elem     GrammarTerm
	Until    []GrammarTerm
}
type GrammarSeparatedTerm struct {
	Position  lexer.Pos
	Elem      GrammarTerm
	Separator GrammarTerm
	Until     []GrammarTerm
}
type GrammarSuffixTerm struct {
	Position lexer.Pos
	LeftName string
	Seed     GrammarTerm
	Arms     []GrammarPostfixArm
}
type GrammarPostfixArm struct {
	Position lexer.Pos
	OpName   string
	Op       GrammarTerm
	Block    bool
	Bindings []*GrammarBindTerm
	Value    Expr
}
type GrammarPrecedenceArm struct {
	Position lexer.Pos
	OpName   string
	Op       GrammarTerm
	Block    bool
	Bindings []*GrammarBindTerm
	Value    Expr
}
type GrammarPrecedenceLevel struct {
	Position lexer.Pos
	Assoc    string
	Name     string
	LeftName string
	Seed     GrammarTerm
	Arms     []GrammarPrecedenceArm
}
type GrammarPrecedenceTerm struct {
	Position lexer.Pos
	Assoc    string
	Result   string
	Levels   []GrammarPrecedenceLevel
	LeftName string
	Seed     GrammarTerm
	Arms     []GrammarPrecedenceArm
}

const (
	GrammarAssociativityLeft     = "left"
	GrammarAssociativityRight    = "right"
	GrammarAssociativityNonAssoc = "nonassoc"
)

type GrammarInfixTableTerm struct {
	Position  lexer.Pos
	TableName string
}
type GrammarTokenSetRefTerm struct {
	Position lexer.Pos
	Name     string
}
type GrammarFirstTerm struct {
	Position lexer.Pos
	Name     string
}
type GrammarApplyTerm struct {
	Position lexer.Pos
	Name     string
	Direct   bool
	Piped    bool
	Args     []GrammarApplyArg
}
type GrammarApplyArg struct {
	Position lexer.Pos
	Name     string
	Term     GrammarTerm
}
type GrammarPostfixTerm struct {
	Position lexer.Pos
	LeftName string
	Seed     GrammarTerm
	Arms     []GrammarPostfixArm
}
type GrammarBindTerm struct {
	Position lexer.Pos
	Name     string
	Term     GrammarTerm
}
type GrammarAssignTerm struct {
	Position lexer.Pos
	Name     string
	Term     GrammarTerm
}
type GrammarReturnTerm struct {
	Position lexer.Pos
	Term     GrammarTerm
}
