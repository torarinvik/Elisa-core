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
	Position lexer.Pos
	Value    Expr
	Fallback Expr
	Recovery *RecoveryClause
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
	// LegacyImplicitElse marks a parser-recovered `value else ...` optional unwrap.
	// The modern surface spelling is `get value else ...`; this flag lets later
	// stages preserve the shared recovery shape without accepting the legacy syntax
	// as an ordinary source construct.
	LegacyImplicitElse bool
}
type OptionalBindExpr struct {
	Position lexer.Pos
	Name     string
	Value    Expr
	// FromIs marks a bind produced by the `x is name` refinement spelling (docs/80)
	// rather than `let name = x`, so diagnostics can name the form the author wrote.
	FromIs bool
}
type AllocExpr struct {
	Position lexer.Pos
	Owner    Expr
	Value    Expr
	// AutoRegion is set for inferred-region allocations: explicit legacy `new[auto] T(...)`,
	// canonical bare `new T(...)` after semantic normalization, and implicit packed enum
	// constructors. Owner is left nil (so passes that resolve an explicit owner skip it).
	AutoRegion bool
	// ExplicitAutoRegion remembers that the source wrote the deprecated `new[auto]` spelling.
	// Bare `new` normalizes to AutoRegion too, but must not emit this deprecation.
	ExplicitAutoRegion bool
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

// MachineFromExpr is a docs/125 §5 `machine from START [decreases E]:` state machine
// expression: states are an enum's variants, each arm ends in `next State` or `done VALUE`,
// and the whole form yields the joined `done` value. Unlike `machine over` (docs/123, a
// pure parser desugar), the result TYPE is only known after analysis, so this is a real
// node: the analyzer determines the type (from context or the `done` values), builds the
// loop/mode/match desugar into `Lowered`, and the backend emits `Lowered` — the LoweredCall
// pattern. `StartEnum` is the state enum's name, `StartState` the entry variant.
type MachineFromExpr struct {
	Position   lexer.Pos
	StartEnum  string
	StartState string
	// StartArgs constructs the entry state's payload (docs/125 §5 state payloads):
	// `machine from Num.Exponent(false)`. Empty when the start variant is payload-free.
	StartArgs []Expr
	Decreases Expr // machine-level termination measure (docs/118); nil when absent
	// HeaderDecls are the machine-private accumulators from the header pipe
	// (`machine from START |r: i32 = 0, blocks: i32 = 0| decreases M:`): mutable
	// locals in scope for the `decreases` measure and every arm body, threaded
	// across transitions (the machine analogue of a loop-header accumulator). Empty
	// when the header carries no pipe. HeaderCaptures names outer mutables the pipe
	// threads by bare name (docs/119 §6). Both lower into the machine's ExprBlock.
	HeaderDecls    []Stmt
	HeaderCaptures []string
	// FromStartSugar marks a machine produced by the `start State:` local-state form (docs/125
	// §5) rather than a written `machine from Enum.State:`. Diagnostics use it to suggest the
	// `start` spelling instead of leaking the synthesized `__StartStates_…` enum name.
	FromStartSugar bool
	Arms           []MachineFromArm
	// Lowered is the analyzer-built ExprBlock desugar; the backend emits it in place of
	// this node (the mode enum decl is hoisted to pendingDecls at parse time).
	Lowered Expr
}

// MachineFromArm is one state's handler: body statements run unconditionally, then a
// trailing run of `next`/`done` terminators resolves the arm (the last one unguarded).
type MachineFromArm struct {
	Position lexer.Pos
	State    string
	// Bindings names the state's payload fields, bound for the arm body (docs/125 §5:
	// `Num.Exponent(was_fraction):`). Empty when the state carries no payload. Lowered
	// to a variant-pattern destructure on the `mode` local.
	Bindings []string
	// DeclaredOut is the R5 out-edge contract (`State -> {A, B}:`): the arm may only
	// `next` a listed successor. HasDeclaredOut distinguishes an empty declared set
	// (a terminal arm that promises no transitions) from an absent one (inferred).
	DeclaredOut    []string
	HasDeclaredOut bool
	Body           []Stmt
	Terminators    []MachineFromTerminator
}

// MachineFromTerminator is one `next State [if COND]` transition or `done VALUE [if COND]`
// exit. Exactly one of Target (next) / Value (done) is set; Guard is nil when unguarded.
type MachineFromTerminator struct {
	Position lexer.Pos
	IsDone   bool
	Target   string // next: the successor state variant
	Args     []Expr // next: payload construction args for the target state; empty when none
	Value    Expr   // done: the yielded value
	Guard    Expr   // optional `if COND`; nil when unguarded
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
	// Binder (docs/77 §2) is the optional second identifier of a category arm
	// (`Statement s:`): Name must then resolve to a sub-category of the hierarchy
	// scrutinee, and Binder binds the matched value at that narrowed type.
	// Empty for a plain bind arm. Only valid in top-level arm position.
	Binder string
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
	// Name binds the tail (docs/122 §5.3: `[first, ...rest]`) as a view over the
	// unmatched elements. Empty for the discard-only `...` marker.
	Name string
}

// MatchRangePattern is a numeric/char range arm (docs/122 §5.2): `'a'..<'z'` /
// `0..=127`. Lo and Hi are literal exprs (same restrictions as MatchLiteralPattern
// values); Inclusive distinguishes `..=` from `..<`.
type MatchRangePattern struct {
	Position  lexer.Pos
	Lo        Expr
	Hi        Expr
	Inclusive bool
}
type MatchStructPattern struct {
	Position     lexer.Pos
	TypeName     string
	Args         []MatchPatternArg
	Brace        bool
	ResolvedArgs []*MatchPatternArg
	// Rest is the explicit `_` final arg (docs/122 §5.7): match the named fields,
	// ignore the remainder — the positional form's opt-in to partial coverage.
	Rest bool
	// As binds the whole matched value alongside the destructure (docs/122 §5.4).
	As string
}
type MatchVariantPattern struct {
	Position     lexer.Pos
	EnumName     string
	Variant      string
	Args         []MatchPatternArg
	ResolvedArgs []*MatchPatternArg
	// As binds the whole matched value at the narrowed variant type alongside the
	// destructure (docs/122 §5.4: `Expr.Binary(l, op, r) as whole:`).
	As string
	// Rest is the explicit final `_` after named args (docs/122 §5.7:
	// `KeyEvent(key: k, _)`) — match the named fields, ignore the remainder. Only
	// meaningful alongside named args; pure-positional `_` stays a one-field wildcard.
	Rest bool
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
