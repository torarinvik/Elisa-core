package ast

import "elisacore/src/lexer"

type Node interface {
	Pos() lexer.Pos
	nodeTag()
}
type File struct {
	Filename       string
	Decls          []Decl
	DeclVisibility map[Decl]string
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
type TokenSetDecl struct {
	Position lexer.Pos
	Name     string
	ElemType TypeExpr
	Value    *ListLitExpr
}
type CharsetDecl struct {
	Position lexer.Pos
	Name     string
	Terms    []LexerCharClassTerm
}
type KeywordMapDecl struct {
	Position   lexer.Pos
	Name       string
	InputType  TypeExpr
	ReturnType TypeExpr
	Fallback   Expr
	Entries    []KeywordMapEntry
}
type KeywordMapEntry struct {
	Position lexer.Pos
	Text     string
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
	Tags     []ErrorVariantDecl
}
type ErrorVariantDecl struct {
	Position lexer.Pos
	Name     string
	Payload  []ParamDecl
}

// GrantAliasDecl names a fixed set of permission refs for reuse in local `can`
// grant blocks: `grant HostSeg = Segment.Host, Unsafe.SegmentMutation` then
// `can HostSeg:`. Unlike `effectalias` (signature-only) it abbreviates local
// grants, and unlike `includes` (whole-family) it captures an exact cross-family
// member set. Compile-time surface only: it expands to its refs during analysis.
type GrantAliasDecl struct {
	Position lexer.Pos
	Name     string
	Refs     []PermissionRef
}
type PermissionDecl struct {
	Position              lexer.Pos
	Name                  string
	Members               []string
	Includes              []string // families this one subsumes: `includes Disk, FileSystem`
	DeprecatedSyntax      string
	DeprecatedReplacement string
}
type NamespaceDecl struct {
	Position lexer.Pos
	Name     string
	Decls    []Decl
	Module   bool
	Const    bool
	Private  bool
}
type UsingDecl struct {
	Position lexer.Pos
	Name     string
}

// ImportDecl is a selective import: `from Module import a, b` brings only the
// named members of an in-program module/namespace into scope unqualified, unlike
// `using Module` which brings in everything.
type ImportDecl struct {
	Position lexer.Pos
	Module   string
	Names    []string
}
type EnumDecl struct {
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Packed      bool
	Common      []FieldDecl
	Variants    []EnumVariantDecl
	// Layout selects the physical layout of a recursive enum (docs/76): the `enum X layout soa|aos:`
	// suffix. StructLayoutDefault means the compiler's default (AoS-in-arena for a recursive enum).
	// LayoutSet records whether a `layout` suffix was written at all.
	Layout                   StructLayoutMode
	LayoutSet                bool
	LayoutSparse             bool   // the `soa(sparse)` sub-option: variant-sparse payload columns
	IndexWidth               string // the `(index: uN)` sub-option: "u8"|"u16"|"u32"|"u64"; "" = default (u32)
	IndexWidthLegacySpelling bool   // wrote the deprecated `index:` key instead of `handle:` (docs/82)
	// Parent is the qualified name of the enum this one refines (docs/77): the `enum Child is Parent:`
	// sealed-refinement suffix. "" means a root (no parent). Child's cases are a subset of Parent's, so
	// Child <: Parent.
	Parent string
}
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
type GrammarWhileTerm struct {
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
type EnumVariantDecl struct {
	Position lexer.Pos
	Name     string
	Payload  []EnumPayloadDecl
}
type EnumPayloadRelation string

const (
	EnumPayloadRelationNone     EnumPayloadRelation = ""
	EnumPayloadRelationChild    EnumPayloadRelation = "child"
	EnumPayloadRelationChildren EnumPayloadRelation = "children"
	EnumPayloadRelationLink     EnumPayloadRelation = "link"
)

type EnumPayloadDecl struct {
	Position lexer.Pos
	Relation EnumPayloadRelation
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

type StructLayoutMode int

const (
	StructLayoutDefault StructLayoutMode = iota
	StructLayoutAOS
	StructLayoutSOA
	StructLayoutC
	StructLayoutPacked
)

type StructDecl struct {
	Position        lexer.Pos
	Annotations     []Annotation
	Name            string
	TypeParams      []string
	RegionParams    []string
	RegionOwner     string
	GenericParams   []GenericParam
	HasStateParam   bool
	StateParamCount int
	NamedStateCases []string
	DerivedStates   []DerivedStateDecl
	Affine          bool
	// Droppable distinguishes `affine` (use-at-most-once, may be dropped) from
	// `linear` (use-exactly-once, must be consumed). Only meaningful when
	// Affine is true; defaults false so a plain `linear` type is must-consume.
	Droppable bool
	ReprC     bool
	Layout    StructLayoutMode
	Fields    []FieldDecl
}
