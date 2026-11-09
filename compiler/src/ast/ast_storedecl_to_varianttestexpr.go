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
	Private     bool // Visible only within the declaring module and its descendants.
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
	// InferredRegionParams are region binders synthesized by semantic inference.
	// They are a subset of RegionParams; source-declared binders remain distinct
	// so escape checking can distinguish a user's independent lifetimes from
	// compiler-generated container-growth plumbing.
	InferredRegionParams []string
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
	ReturnType TypeExpr
	// ReturnRegion records the function-level `-> T @r` lifetime contract. It is
	// deliberately separate from TypeExpr.Region: the stage1 parser treats this
	// suffix as a return contract, not as a region-instantiated enum type.
	ReturnRegion string
	Body         []Stmt
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
	Changes   []EnsuresPath
	Preserves []EnsuresPath
	// Fulfills retains declared or named-contract-derived law obligations on a bodiless boundary.
	Fulfills   []FulfillsClause
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
	Annotations    []Annotation
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
