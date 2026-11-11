package ast

import "elisacore/src/lexer"

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
	// Captures names the outer mutable bindings a `|capture|` header (docs/119 §6)
	// threads through this block: the moved-in shadow's final value is written back to
	// the outer binding by a synthesized tail assignment. E4 (value-block purity)
	// exempts writes to these names — a capture is the sanctioned way a value block
	// updates outer state, sugar over `rebind`. Empty for ordinary blocks.
	Captures []string
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
	// LoweredCall, when non-nil, is the analyzer's desugar of an overloaded unary operator
	// (`-x` on a type impl-ing `Neg` -> `T.__neg__(x)`). The backend emits it in place of the
	// primitive unary op, and the effect/region collectors thread the callee's obligations to the
	// operator site. Mirrors BinaryExpr.LoweredCall.
	LoweredCall *CallExpr
}
type MoveExpr struct {
	Position lexer.Pos
	Operand  Expr
}
type CallExpr struct {
	Position      lexer.Pos
	Func          Expr
	SafeReceiver  Expr
	ArgForwardPos lexer.Pos
	Args          []Expr
	ArgNames      []string
	ArgShorthand  []bool
	ArgItemOrder  []CallArgItem
	// LmutRebindClaims records rebind targets that name a threaded lmut argument of
	// this call (docs/120 §3): `rebind ch, lexer = lexer.advance_char()` claims the
	// `lexer` thread. The parser drops a claimed target from the value bind (the
	// thread is in-place; there is no slot to bind) and records it here; the semantic
	// layer validates each claim against the callee's declared LmutThreadSlots — a
	// claim on a non-threading callee, or a declared thread left unclaimed (the
	// must-use rule), is a compile error. This is what keeps the parser's purely
	// syntactic target-vs-argument matching sound: a wrong guess cannot misbind, it
	// can only be rejected.
	LmutRebindClaims []LmutRebindClaim

	ResolvedArgs         []Expr
	ResolvedCommonArgs   map[string]Expr
	ResolvedImplicitArgs []Expr

	// Keep flags together to avoid alignment padding between syntax fields.
	// FStringLowering marks the parser-created call that implements an f-string.
	// Unlike spelling alone, this identity cannot be forged by an ordinary source call.
	FStringLowering bool
	HasArgForward   bool
	Safe            bool
	// SafeConcurrencySugar marks a call node that the parser synthesized from a
	// safe concurrency construct (e.g. `submit`/`pool` scope lowering to a
	// `pool_submit1` call). User code never wrote the raw primitive, so the
	// raw-concurrency-surface-removed diagnostic must not fire on it.
	SafeConcurrencySugar bool
	// LmutThreadEffect marks a mutating call the parser hoisted out of a docs/120 §6
	// thread slot (`parser.advance()` from `parser <- parser.advance()`). Its effect is
	// part of the visible multi-place manifest, so the §7 strict tier treats it as a
	// manifest point even when the all-thread desugar leaves it as a bare statement
	// (no value block whose captures would otherwise license it).
	LmutThreadEffect bool

	ResolvedArgsValid         bool
	ResolvedImplicitArgsValid bool
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
	// Index2 is the optional SECOND subscript of a two-operand index `obj[a, b]`. It is non-nil
	// only for the docs/108 fixed-stride table overlay accessor `table.field[mem, i]`, where
	// Index is the MemoryManager and Index2 is the entry index. nil for ordinary single-subscript
	// indexing; the analyzer rejects any non-overlay two-operand index.
	Index2   Expr
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
	// AsOverlayCall is set by the analyzer when `base.field[mem]` over a `GuestVAddr[L]` carrier is
	// recognized as a docs/107 typed guest-memory overlay accessor. When non-nil, codegen emits this
	// MemoryManager_ReadU<N>(mem, base + offset) call instead of an index, making the lowered code
	// byte-identical to the hand-written read. nil for ordinary indexing.
	AsOverlayCall *CallExpr
	// AsBuiltinDictGet is set by the analyzer when `dict[key]` is reinterpreted as the builtin
	// optional dictionary lookup. When non-nil, codegen emits this call instead of ordinary indexing.
	AsBuiltinDictGet *CallExpr
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
	Position lexer.Pos
	Value    Expr
	Name     string
	// SecondName, when non-empty, marks a two-binder head `for k, v in src` (tuple
	// destructuring, e.g. dict entries). Name binds the first tuple field, SecondName
	// the second. Empty for the ordinary single-binder head.
	SecondName string
	Source     Expr
	RangeEnd   Expr
	RangeStep  Expr
	RangeOp    lexer.TokenKind
	Filter     Expr
	Owner      Expr
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
	// ParallelBody marks a lambda synthesized as the body of a `by par` range-loop (the
	// `for_indices_par` lowering). Such a body runs across disjoint index bands in parallel, so the
	// analyzer rejects it if it captures thread-unsafe state (a darray header, a mutable ref, etc.).
	ParallelBody bool
	Params       []ParamDecl
	ReturnType   TypeExpr
	Body         []Stmt
	BodyExpr     Expr
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

// QuantifierExpr is a bound quantifier in a spec/law body (docs/90 brick 90-4): `forall i: <body>`
// or `exists i: <body>`. The binders range over the integers; the body is a boolean expression
// (typically a guarded implication `(0 <= i and i < n) implies <pred>`). Quantified bodies are
// SPEC-ONLY — provable by the SMT tier, never compiled to a runtime check (an unbounded quantifier
// is not executable).
type QuantifierExpr struct {
	Position lexer.Pos
	Exists   bool // false = forall
	Vars     []string
	In       Expr // optional collection source for `forall x in xs: ...`
	Body     Expr
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
	// CopyBase is the `..base` of a copy-update literal `T{..base, f: v}`.
	// Semantic analysis desugars it into explicit `f: base.f` entries for every
	// field not written, then clears it, so later passes see an ordinary literal.
	CopyBase Expr

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
