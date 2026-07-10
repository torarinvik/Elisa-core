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
	Arms      []MachineFromArm
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
type Stmt interface {
	Node
	stmtTag()
}
type AssignStmt struct {
	Position lexer.Pos
	Target   Expr
	Value    Expr
	Optional bool
	// FastMath marks an assignment whose value expression should be emitted under full fast-math
	// FP (reassociation etc.), regardless of the enclosing function's setting. Set on the
	// accumulator update of every comprehension fold: a fold's reduction order is defined as a
	// vectorizable tree reduction, not strict left-to-right, so its FP accumulator reassociates by
	// default (no `by simd` marker needed; integer accumulators are unaffected) (docs/79 Part IV).
	FastMath bool
	// AsOverlayCall is set by the analyzer when the assignment TARGET is a guest-overlay write
	// accessor — `base.field[mem] = value` over a `GuestVAddr[L]` carrier (docs/107). It carries the
	// equivalent `MemoryManager_WriteU<N>(mem, base + offset, value)` CallExpr; when set, the backend
	// emits that call and ignores Target/Value, so the write lowers byte-identically to the
	// hand-written store. Mirrors IndexExpr.AsOverlayCall for the read form.
	AsOverlayCall *CallExpr
	// ArgManifest marks a docs/120 §8 arg-manifest `x <- x.method(…)`: the RHS is a void
	// call that mutates x in place (x is its receiver/argument), so there is nothing to
	// assign — the target is a manifest of what the call mutates. Set by the semantic pass;
	// codegen/interpret emit only Value (the call).
	ArgManifest bool
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
	// Ghost marks a verification-only local (`ghost x: T = expr`). It exists solely to give the
	// prover a value to reason about (usable in requires/ensure/invariant/assert) and is fully
	// ERASED from codegen. SOUNDNESS: no real variable/return/field/effect may depend on a ghost
	// value (enforced in the analyzer), and the backend emits nothing for a ghost decl.
	Ghost bool
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
	// ArgManifest marks a docs/120 §8 arg-manifest `t1, … <- call(…)`: the RHS is a void
	// call that mutates the named mutable bindings in place (they are its arguments), so
	// there is nothing to destructure — the names manifest what the call mutates. Set by the
	// semantic pass; codegen/interpret emit only Value (the call).
	ArgManifest bool
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
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Then     []Stmt
	Elifs    []ElifClause
	Else     []Stmt
	// FromSource marks an IfStmt the programmer WROTE as a block `if:` statement — set
	// only at the source-if lowering site (lowerIfClauses' plain-condition path). Every
	// synthetic IfStmt (postfix-guard desugar, loop-header wrapper, machine lowering,
	// comprehension filters, …) leaves it false, so the strict-flow block-`if` ban
	// (docs/125 §6b) is zero-FP by construction: it can only fire on written syntax.
	FromSource bool
}
type WhileStmt struct {
	Position lexer.Pos
	Hint     BranchHint
	Cond     Expr
	Body     []Stmt
	// Captures are the loop-header capture names (`while cond |parser|:`, docs/119 §6):
	// the loop's declared mutation manifest. Notation only — no codegen effect; the
	// §10 linear-mutation checker licenses mutating calls on captured bindings.
	Captures []string
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
	// PreReserve is a compiler-synthesized `ys.reserve(bound)` emitted before the loop when
	// analysis proves a bounded darray fill.
	PreReserve  Stmt
	PreReserves []Stmt
	// AutovecExpected marks a compiler-synthesized fused loop (an indexed-store comprehension
	// build) whose shape was deliberately lowered to be vectorizer-legal and whose body is
	// call-free — so if it fails to auto-vectorize at -O2/-O3 that is a real efficiency
	// regression worth a -Wperf warning (docs/79 Part IV). Set by the comprehension desugars; the
	// backend tags the loop's latch with `!llvm.loop` metadata carrying the marker + source
	// position, then a post-optimization pass warns for any marked loop left un-vectorized.
	AutovecExpected bool
	// AutovecReason is a short human label for the construct that produced this loop ("comprehension
	// map", "fold reduction"), embedded in the marker so a -Wperf warning can name what failed to
	// vectorize rather than describing it generically. Only meaningful when AutovecExpected is set.
	AutovecReason string
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
	// PreReserve is a compiler-synthesized `ys.reserve(src.count)` emitted before the loop when
	// auto-reservation infers a bounded fill over this iterable (region inference, Phase A). Set
	// by the analyzer, emitted by codegen; nil when the loop is not an eligible fill.
	PreReserve  Stmt
	PreReserves []Stmt
	// MovedSource marks a consuming move-drain: `for x in move c:`. Each element is moved OUT of
	// the owned container `c` (binds by value, like IterBindValue — codegen is identical), the
	// loop body must consume each moved element, and the container's must-consume obligation is
	// discharged after the loop. This is the ONLY sanctioned way to drain a container of affine
	// (must-consume) handles; non-consuming removal (clear/truncate/remove) is rejected for such
	// element types. Mode stays IterBindValue so backend value-binding lowering is reused verbatim.
	MovedSource bool
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
	// Guard is the optional arm-header guard (docs/122 §5.1: `Pattern if cond:`).
	// Pattern bindings are in scope; a guarded arm never discharges exhaustiveness.
	// Fanned-out `A | B:` alternatives share the same Guard expr (like Body).
	Guard Expr
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

// AssertByStmt is a Dafny-style proof-carrying assert: `assert COND by:` followed by an indented
// proof block. The block holds verification-only statements (lemma calls, nested asserts) that
// establish intermediate facts used ONLY to prove COND. After the assert, ONLY COND is exported as a
// fact (the block's facts are scoped out). The whole proof block is erased from codegen; COND itself
// lowers to an ordinary (debug-gated) assertion check.
type AssertByStmt struct {
	Position lexer.Pos
	Cond     Expr
	Proof    []Stmt
	// Scoped marks a CLOSED-WORLD proof block (docs/99): written `assert COND by scoped:`. The block is
	// discharged using ONLY the facts cited inside it (lemma `use`-calls, nested asserts) — ambient flow
	// and assert facts from the enclosing scope are walled out. This makes the proof STABLE: its verdict
	// depends only on the cited facts, not on unrelated surrounding code that might otherwise perturb the
	// solver's hypothesis set. A non-scoped `by:` block keeps the original open-world behavior.
	Scoped       bool
	ScopedTheory string // optional explicit theory marker: `assert COND by scoped: arithmetic|bitvector`
}

// ProofBlockStmt is a standalone closed-world proof region:
// `proof GOAL:` followed by verification-only statements. The block proves GOAL from facts cited
// inside the block only, exports GOAL as the sole downstream fact, and is erased from codegen.
type ProofBlockStmt struct {
	Position lexer.Pos
	Goal     Expr
	Proof    []Stmt
}

// AssertHoleStmt is the explicit docs/98 proof-hole query: `assert ?`. It asks the analyzer to print
// the in-scope goal/fact report at this position and is erased from execution/codegen.
type AssertHoleStmt struct {
	Position lexer.Pos
}

// ProofUseStmt cites lemmas inside an `assert ... by:` proof block:
// `use LemmaA, LemmaB(args)`. It is verification-only sugar over statement-position lemma calls.
type ProofUseStmt struct {
	Position  lexer.Pos
	Citations []Expr
}

// ContractKind distinguishes value-contract clauses.
type ContractKind int

const (
	ContractRequire       ContractKind = iota // precondition: `requires <bool-expr>` at function start
	ContractEnsure                            // postcondition: `ensure <bool-expr>` (may use `result`/`old(...)`)
	ContractInvariant                         // in-body assertion: `invariant <bool-expr>`, checked in place
	ContractDecreases                         // termination measure: `decreases <int-expr>` (docs/86 brick 86-7)
	ContractDecreasesWild                     // termination opt-out: `decreases * "reason"` (Dafny-style wildcard)
	ContractUses                              // named-contract application: `uses Name(args)` (docs/97)
)

// ContractStmt is a value-contract clause written as a leading body statement. The parser produces
// it for `requires <bool-expr>`; the function-decl parser lifts leading ones into FuncDecl.Requires
// (so it normally never reaches the statement analyzer/backend — see liftLeadingRequires).
type ContractStmt struct {
	Position    lexer.Pos
	Kind        ContractKind
	Cond        Expr
	Proof       []Stmt // non-empty for `requires/ensure COND by scoped:`
	ScopedProof bool
	WildReason  string // non-empty iff Kind == ContractDecreasesWild; the mandatory reason string
	// UsesName / UsesArgs hold a named-contract application `uses Name(args)` (Kind == ContractUses,
	// docs/97). UsesName is the contract's name; UsesArgs are the positional application arguments that
	// bind to the contract's formal parameters. Both empty for every other Kind.
	UsesName     string
	UsesTypeArgs []TypeExpr
	UsesArgs     []Expr
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
	// UserAuto marks a region the USER wrote as `in auto:` (vs the parser's own
	// function/loop auto-wraps, which share the synthesized name but must not
	// warn). Drives the `in auto:` deprecation diagnostic.
	UserAuto bool
	Body     []Stmt
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
func (n *LayoutDecl) Pos() lexer.Pos   { return n.Position }
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
func (n *PermissionDecl) Pos() lexer.Pos { return n.Position }
func (n *AliasDecl) Pos() lexer.Pos      { return n.Position }
func (n *NamespaceDecl) Pos() lexer.Pos  { return n.Position }
func (n *UsingDecl) Pos() lexer.Pos      { return n.Position }
func (n *ImportDecl) Pos() lexer.Pos     { return n.Position }
func (n *EnumDecl) Pos() lexer.Pos       { return n.Position }
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
func (n *GrammarDynamicClimbTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarFlatRepeatTerm) Pos() lexer.Pos {
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
