package ast

import "elisacore/src/lexer"

type TypeExpr interface {
	Node
	typeExprTag()
}
type NamedType struct {
	Position lexer.Pos
	Name     string
	Region   string
	// Spelling is the name AS WRITTEN, with its real separators (`Pack::Item`). Name
	// flattens every separator to "." because it is the analyzer's symbol key, which
	// loses the one distinction the language cares about: `::` walks a namespace and `.`
	// reaches a member, so "Alpha.Result.Ok" could have been written `Alpha::Result.Ok`,
	// `Alpha::Result::Ok` or `Alpha.Result.Ok` and nothing downstream could tell.
	//
	// Empty when the name is a single segment or the two agree. Anything that reproduces
	// SOURCE (the formatter, the interface writer) must prefer this; anything that looks a
	// name up must keep using Name.
	Spelling string
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
	// Spelling is the name as written, with its real separators; see NamedType.Spelling.
	Spelling string
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
	// Region is an explicit @r lifetime on the aggregate. Semantic resolution
	// propagates it to every region-carrying field, rejecting mixed lifetimes.
	Region string
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
