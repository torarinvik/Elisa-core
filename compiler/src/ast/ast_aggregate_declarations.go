package ast

import "elisacore/src/lexer"

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

// GlobalRegionDecl is `global region NAME` (docs/128): a program-lived named region.
type GlobalRegionDecl struct {
	Position lexer.Pos
	Name     string
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
	Position           lexer.Pos
	Annotations        []Annotation
	Name               string
	TypeParams         []string
	RegionParams       []string
	RegionOwner        string
	GenericParams      []GenericParam
	HasStateParam      bool
	StateParamCount    int
	NamedStateCases    []string
	TerminalStateCases []string
	DerivedStates      []DerivedStateDecl
	// A protocol graph contains state edges, never function names or signatures.
	// HasDerivedStateBlock distinguishes an empty (invalid) derive block from
	// predicate-free protocol states.
	HasDerivedStateBlock bool
	StateTransitions     []StateTransitionDecl
	Affine               bool
	// Resource marks a struct synthesized from `extern resource Name` (docs/127 §3.2): its
	// single `__handle` field is the native handle, it must declare `__drop__`, and the
	// extern boundary passes the handle itself in place of the struct.
	Resource bool
	// Droppable distinguishes `affine` (use-at-most-once, may be dropped) from
	// `linear` (use-exactly-once, must be consumed). Only meaningful when
	// Affine is true; defaults false so a plain `linear` type is must-consume.
	Droppable bool
	ReprC     bool
	Layout    StructLayoutMode
	Fields    []FieldDecl
	// Invariants are value-contract conditions over the struct's fields (`invariant <bool-expr>`
	// in the struct body, referencing `self.field`). Checked in debug builds after construction and
	// after each `s.field <- ...` store (`zeroed` construction is exempt).
	Invariants []Expr
}

type StateTransitionDecl struct {
	Position lexer.Pos
	From     string
	To       string
}

// IsOldCall reports whether a call expression is the contract `old(expr)` pseudo-call (the value of
// expr at function entry, valid only inside an `ensure` clause).
func IsOldCall(c *CallExpr) bool {
	if c == nil {
		return false
	}
	id, ok := c.Func.(*Ident)
	return ok && id != nil && id.Name == "old"
}

// ForeignEnumValidatorName is the internal name of the validator the parser synthesizes for a
// closed `extern enum` (docs/127 D9). The analyzer maps the surface spelling `Name.from_c`
// onto it, and exempts its body from the ban on reinterpreting a raw integer as that enum.
// It lives here so the analyzer need not depend on the parser.
func ForeignEnumValidatorName(enumName string) string { return enumName + "__from_c" }
