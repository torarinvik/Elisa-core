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
	Position    lexer.Pos
	Annotations []Annotation
	Name        string
	Mutable     bool
	IsTail      bool
	// Ghost marks a verification-only model field (`ghost name: T`). It carries abstract
	// model state alongside the concrete representation, is readable only in contract/ghost
	// context, and is ERASED from codegen (no layout/size/offset impact on the real fields).
	Ghost        bool
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
	// TypeArgs are the type arguments of an abstract effect reference such as
	// `Writer[sview]`. They are deliberately retained separately from Name so
	// an effect remains distinguishable from a concrete permission family.
	TypeArgs []TypeExpr
	// Via is the concrete realization of an abstract effect, for example
	// `Writer[sview] via Console.Write`. It is metadata, not a second effect
	// row: callers can still inspect the abstract operation while permission
	// checking uses the concrete refs as needed.
	Via []PermissionRef
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
	// EnsuresKindRefinement is `ensures <param> is <BareLaw>` (docs/85, mutable refinement flow):
	// a law-predicate postcondition on a parameter, carried in RefinementLaw. Distinct from the
	// state kinds above — it rides the lightweight predicate-fact channel, not the type system.
	EnsuresKindRefinement
)

type EnsuresPath struct {
	Position lexer.Pos
	Root     string
	Fields   []string
}

// FulfillsClause is `fulfills <Param> is <Law>` (docs/88): the function declares it satisfies the
// named frame law, with the law's subject bound to Param.
// FulfillsClause is a `fulfills` application on a function. For a FRAME law it is `<param> is <Law>`
// (Param names the subject the law binds to). For a function-level EFFECT law it is the subject-free
// `fulfills <Law>` (Param == ""), since an effect law constrains the whole function, not a place.
type FulfillsClause struct {
	Position lexer.Pos
	Param    string
	Law      string
}
type ContractInclude struct {
	Position lexer.Pos
	Law      string
	Args     []Expr
}
type EnsuresClause struct {
	Position   lexer.Pos
	Condition  EnsuresCondition
	Target     EnsuresPath
	Kind       EnsuresKind
	StateCases []string
	RefState   RefState
	// RefinementLaw is the law name for EnsuresKindRefinement (`ensures arr is NonEmpty`).
	RefinementLaw string
	// RefinementArgs are the bracket arguments of a parametric refinement postcondition
	// (`ensures x is Bounded[0, 500]` / `ensures x is Bounded[0..500]`); empty for a bare law.
	RefinementArgs []Expr
}
type FuncDecl struct {
	Position     lexer.Pos
	Annotations  []Annotation
	Static       bool
	Override     bool
	Name         string
	TypeParams   []string
	RegionParams []string
	// AmbientGrownContainerRegion names a `__rg_<param>` region (one of RegionParams) inferred for a
	// container parameter this function grows by INSERTING region-allocated values (the void-grower
	// shape `out.push(make_node())`). The backend binds this function's ambient allocation region to
	// that region's arena so a region-poly callee producing the inserted value allocates into the
	// caller-owned container's region (adopted with it), not a per-call arena freed on return. Empty
	// otherwise.
	AmbientGrownContainerRegion string
	PermissionParams            []string
	GenericParams               []GenericParam
	Permissions                 []PermissionRef
	Ensures                     []EnsuresClause
	// Requires holds value-contract preconditions (`requires <bool-expr>` clauses), checked at
	// function entry in debug builds only (elided in release), like the bounds-check watchdog.
	// Distinct from Ensures, which is the typestate/resource-state contract.
	Requires       []Expr
	RequiresProofs []*ProofBlockStmt
	// EnsureValues holds value-contract postconditions (`ensure <bool-expr>`), checked at every
	// return in debug builds. They may reference `result` (the returned value) and `old(expr)`
	// (the value of expr at function entry). Distinct from Ensures (typestate).
	EnsureValues []Expr
	EnsureProofs []*ProofBlockStmt
	// Decreases holds the termination measure(s) from leading `decreases <int-expr>` clauses
	// (docs/86 brick 86-7). Multiple components form a lexicographic tuple. When present, every
	// self-recursive call must provably decrease the measure (and keep it bounded below), proving
	// termination at compile time. Empty = no termination obligation (status quo).
	Decreases []Expr
	// DecreasesWild is non-empty when the function carries a `decreases * "reason"` opt-out clause
	// (Dafny-style wildcard). The reason string is mandatory — a missing reason is a compile error.
	// This suppresses the "cannot prove termination" diagnostic but does NOT grant verifiedTerminating
	// status: defining-equation axiomatization remains disabled (soundness-critical: non-termination
	// implies no fixed point, so assuming the equation would be unsound).
	DecreasesWild string
	Params        []ParamDecl
	// Variadic marks a source-defined C-ABI variadic function (`def f(x: T, ...)`).
	// The unnamed tail is available through the llvm.va_* intrinsic surface.
	Variadic   bool
	ReturnType    TypeExpr
	Body          []Stmt
	// LmutThreadSlots records the declared lmut-threading manifest (docs/120 §2): return-tuple
	// fields spelled `name: lmut T` that were validated against the same-named `lmut` parameters
	// and ERASED — from ReturnType and from every return expression — by the parser post-pass
	// (applyDeclaredLmutThreading). The notation is checker-enforced and codegen-erased: the
	// emitted function threads those params in place exactly like plain lmut. Retained here so
	// the semantic layer can enforce the docs/120 §3 call-site rules (rebind form, must-use).
	LmutThreadSlots []LmutThreadSlot
	// Changes holds the frame condition `changes <path>, ...` (docs/87): the upper bound on which
	// caller-visible places the function may write. Each path is param-rooted (reusing EnsuresPath).
	// An empty slice means no clause (unconstrained); a present clause is enforced — a write outside
	// the set is a compile error.
	Changes []EnsuresPath
	// Preserves holds the frame condition `preserves <path>, ...` (docs/87 §7): the dual of Changes —
	// caller-visible places the function must NOT write. A write that overlaps a preserved path is a
	// compile error. `preserves Y ≡ Y ∩ changes(f) = ∅`.
	Preserves []EnsuresPath
	// Fulfills holds `fulfills <param> is <FrameLaw>` clauses (docs/88): applying a named frame law to
	// the function by binding the law's subject to the named param. Each expands into the function's
	// Changes/Preserves sets (the law's paths rebased from `self` to the param).
	Fulfills []FulfillsClause
	// ContractIncludes holds `includes Law(args)` clauses inside a named contract (docs/97 §6).
	// Value-law includes fold into Requires; frame/effect/shape/composite laws fold into their own
	// discharge class so facts are never laundered across classes.
	ContractIncludes []ContractInclude
	// Forbids holds the forbidden effects of an EFFECT law (docs/85 §4, Stage 4):
	// `law NoAlloc forbids Memory.Allocate, Abort.Panic`. An effect law is the function-level
	// discharge class — it names effects a conforming function must not use, discharged against the
	// function's inferred effect set. A non-empty Forbids (with no `=` body / `changes` clause) marks
	// the law as an effect law; a function declares conformance with the subject-free `fulfills <Law>`.
	Forbids []PermissionRef
	// Includes holds the member law names of a COMPOSITE law (docs/85 §6): `law Leaf includes
	// NoAlloc, NoBoundsChecks`. A composite is a function-level law whose obligation is the union of
	// its members' obligations (effect forbid-sets ∪ shape requirements, resolved transitively). It
	// has no subject, no body, and no direct `forbids` clause — its members supply everything.
	Includes []string
	// IsLaw marks a `law` declaration (docs/85): a pure, total, bool-returning predicate whose
	// first value parameter is its subject. It is represented as a FuncDecl so it reuses all the
	// function machinery (generics, modules, type checking, calls); the flag drives purity
	// enforcement and lets `is` resolve it as a predicate.
	IsLaw bool
	// IsContract marks a `contract Name(params):` declaration (docs/97): a named, reusable bundle of
	// value contracts (Requires/EnsureValues) and frame conditions (Changes/Preserves), parameterised
	// over its Params. It is represented as a FuncDecl (reusing decl/generic/module machinery, like a
	// law) but has no executable body and is never codegen'd — it exists only to be applied to real
	// functions via a leading `uses Name(args)` clause, whose expansion folds these slices (with
	// formals substituted by application arguments) into the applying function's own contract slices.
	IsContract bool
	// Uses holds leading `uses Name(args)` named-contract applications (docs/97), lifted off the front
	// of the body alongside the other leading contracts. The analyzer's expandUsesContracts pass
	// resolves each name to a `contract` decl, substitutes formals→args, and folds the contract's
	// clauses into this function's Requires/EnsureValues/Changes/Preserves before discharge runs.
	Uses []*ContractStmt
	// ParamContracts holds higher-order verification contracts attached to function-typed
	// parameters (docs/101): a leading `requires ensures(f, <pred over result>)` clause names a
	// function-typed parameter `f` and the postcondition the caller must guarantee `f` satisfies.
	// At every call site that passes a concrete NAMED function for `f`, the analyzer proves the
	// passed function's declared `ensure` entails the param contract's predicate; inside the body,
	// a call `f(...)` may ASSUME the predicate of its result. Populated by expandHigherOrderContracts
	// from the recognized `ensures(...)` spec-call form, which is then removed from Requires.
	ParamContracts []ParamContract
	// IsLemma marks a `lemma` declaration (ghost code): a verification-only function whose `ensure`
	// postconditions are PROVEN from its `requires` + body, and which is invoked as a statement to
	// INJECT those (proven) postconditions as assumable facts at the call site. It is erased from
	// codegen (no body emitted, no call emitted) — it exists purely to give the prover a manually
	// supplied, separately-verified fact it could not derive automatically.
	IsLemma bool
	// IsGhost marks a `ghost def`: a pure, total, verification-only value function. It may be called
	// from spec positions (requires/ensure/invariant/law/assert) and its defining equation may be used
	// by SMT, but it is erased from interpreter/codegen and cannot be called by runtime code.
	IsGhost bool
}

// ParamContract is a higher-order verification contract on a function-typed parameter (docs/101).
// `Param` is the parameter's name; `Pred` is the postcondition predicate (a bool expression that may
// mention the `result` keyword) the parameter's function is required to ensure. At a call site that
// passes a concrete named function, the passed function's declared `ensure` must entail `Pred`.
type ParamContract struct {
	Position lexer.Pos
	Param    string
	Pred     Expr
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
	// Requires holds value-contract preconditions on the boundary (`requires <bool-expr>`),
	// checked/discharged at every call site so the caller cannot pass the native function an
	// argument outside its specified domain. The native body is trusted to honour its `ensures`.
	Requires []Expr
	// EnsureValues holds trusted value-contract postconditions on the native boundary
	// (`ensure <bool-expr>`). They are never proven by Elisa; callers may assume them after the
	// extern preconditions have been discharged.
	EnsureValues []Expr
	Uses         []*ContractStmt
	// Changes / Preserves hold the frame condition (`changes`/`preserves <path>, ...`, docs/87) on a
	// bodiless boundary or protocol method. On a PROTOCOL method, Changes is the upper-bound frame an
	// impl may write (impl `changes` must be a subset of it; P4 effect/frame variance).
	Changes    []EnsuresPath
	Preserves  []EnsuresPath
	Params     []ParamDecl
	ReturnType TypeExpr
	Variadic   bool
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
	Position   lexer.Pos
	Name       string
	TypeParams []string // value parameters for parametric refinement aliases, e.g. `[cap]` in `type SlotIndex[cap] = u32 is InRange[0, cap]`
	Target     TypeExpr
}

// RefineDecl is a NAMED, reusable refinement alias (docs/85 "Level 2"):
//
//	refine Positive = i64 where self > 0
//	refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count
//
// It is purely a desugaring: a use of `Name` / `Name[..](..)` in a BINDER position
// (param / return / local) expands to the equivalent anonymous `Base where PRED`
// (a WhereRefinementTypeExpr) with the type-/value-parameters substituted, and the
// bound value substituted for `self`. RefineDecl is representation-erased — it never
// participates in SameType/AssignableTo and is rejected outside binder positions in
// this first version.
type RefineDecl struct {
	Position   lexer.Pos
	Name       string
	TypeParams []string    // generic type parameters, e.g. `[T]`
	Params     []ParamDecl // value parameters, e.g. `(xs: darray[T])`
	Base       TypeExpr    // the erased representation type
	Predicate  Expr        // the bool predicate; references `self` plus the params
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
	// Linear marks the `lmut T` mode: a linear-mutable parameter/binding. Layout and
	// codegen are identical to a `mutable T&` reference (in-place mutation through an
	// exclusive pointer); the distinction is enforced by the linear checker, which
	// invalidates the source binding when an `lmut` argument is passed (a move) and
	// reacquires it after the call. `lmut T` desugars to MutableType{Elem: RefType{T},
	// Linear: true} at parse time so all existing mutable-ref handling applies unchanged.
	Linear bool
}

// RefinementTypeExpr is `Base is Pred[…], …` (docs/85): a base type refined by one or more law
// predicates. It is REPRESENTATION-ERASED — it resolves to its base type for layout/codegen — and
// the predicates become discharge obligations + in-scope facts on the binding it annotates.
type RefinementTypeExpr struct {
	Position lexer.Pos
	Base     TypeExpr
	Preds    []RefinementPredExpr
}

// WhereRefinementTypeExpr is `Base where <bool-expr>`: anonymous binder refinement syntax. Like
// named law refinements, it is representation-erased; the predicate is preserved for later contract
// lowering/proof obligations.
type WhereRefinementTypeExpr struct {
	Position  lexer.Pos
	Base      TypeExpr
	Predicate Expr
}

// RefinementPredExpr is one predicate application in a refinement type: a law name plus optional
// static `[..]` args (e.g. `Bounded[0..500]`). `self` is bound to the refined value.
type RefinementPredExpr struct {
	Position lexer.Pos
	Name     string
	Args     []Expr
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

// LmutThreadSlot is one erased `name: lmut T` slot of a declared-threading return tuple
// (docs/120 §2): the slot's original tuple index and the lmut parameter it threads.
type LmutThreadSlot struct {
	TupleIndex int
	ParamIndex int
	ParamName  string
}

// LmutRebindClaim is one rebind target claiming a threaded lmut argument of a call
// (docs/120 §3). Name is the claimed binding (must equal the root of the argument
// bound to a declared thread slot); Position locates the target for diagnostics.
type LmutRebindClaim struct {
	Position lexer.Pos
	Name     string
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
	// LmutThreadEffect marks a mutating call the parser hoisted out of a docs/120 §6
	// thread slot (`parser.advance()` from `parser <- parser.advance()`). Its effect is
	// part of the visible multi-place manifest, so the §7 strict tier treats it as a
	// manifest point even when the all-thread desugar leaves it as a bare statement
	// (no value block whose captures would otherwise license it).
	LmutThreadEffect bool

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
