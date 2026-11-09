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

// LayoutDecl is an in-source declaration of a typed guest-memory layout (docs/107): the carrier
// description that `GuestVAddr[Name]` field accessors resolve against. It mirrors the EASM `layout`
// model so the boot-assembly `N(%base)` reads (docs/104) and the high-level `base.field[mem]` reads
// are checked against ONE description. It emits no code — the analyzer registers it into the overlay
// registry, and accessors over it desugar to MemoryManager_Read/WriteU<N> calls (ABI-neutral).
type LayoutDecl struct {
	Position lexer.Pos
	Name     string
	Size     int64 // explicit `layout Name size N:`; 0 = unspecified (bounds derived from fields)
	Fields   []LayoutFieldDecl
}

// LayoutFieldDecl is one field of a LayoutDecl: a byte Offset, a Name, a width-bearing scalar Type
// (u8|u16|u32|u64), and an optional `requires size >= N` minimum-size obligation (0 = none).
type LayoutFieldDecl struct {
	Position            lexer.Pos
	Offset              int64
	Name                string
	Type                string
	RequiresSizeAtLeast int64
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
	// Foreign marks an `extern enum` (docs/127 D9): the member list mirrors a C enum, so a
	// value arriving from C is NOT known to be one of the members. Reinterpreting a raw
	// integer as one is rejected; `Name.from_c(raw)` validates instead.
	Foreign bool
	// Open marks `@open extern enum`: the C API may add members this list has not caught up
	// with, so there is nothing to validate and `match` must carry a default arm instead.
	Open bool
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

// AliasDecl names a fixed set of permission refs for reuse in local `can`
// grant blocks: `grant HostSeg = Segment.Host, Unsafe.SegmentMutation` then
// `can HostSeg:`. Unlike `effectalias` (signature-only) it abbreviates local
// grants, and unlike `includes` (whole-family) it captures an exact cross-family
// member set. Compile-time surface only: it expands to its refs during analysis.
type AliasDecl struct {
	Position lexer.Pos
	Name     string
	Refs     []PermissionRef
}
type PermissionDecl struct {
	Position lexer.Pos
	Name     string
	Members  []string
	Includes []string // families this one subsumes: `includes Disk, FileSystem`
}
type NamespaceDecl struct {
	Position lexer.Pos
	Name     string
	Decls    []Decl
	Module   bool
	Const    bool
	Private  bool
	// Extend marks an `extend Foo:` block: it contributes members to an
	// already-declared `module Foo:` rather than declaring it. Extending an
	// undeclared module is an error; the canonical declaration must be a
	// `module Foo:` (and only one may exist).
	Extend bool
}

// UsingDecl drops the qualifier on names from another module. Three forms:
//   - wildcard `using Foo`:        Name="Foo" — all of Foo's public names unqualified.
//   - selective `using Foo::bar`:  Name="Foo", Member="bar" — just `bar` unqualified.
//   - alias `using Foo as F`:      Name="Foo", Alias="F" — `F::x` resolves to `Foo.x`
//     (qualification kept, just shortened).
type UsingDecl struct {
	Position lexer.Pos
	Name     string
	Member   string
	Alias    string
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
	Layout       StructLayoutMode
	LayoutSet    bool
	LayoutSparse bool   // the `soa(sparse)` sub-option: variant-sparse payload columns
	IndexWidth   string // the `(handle: uN)` sub-option: "u8"|"u16"|"u32"|"u64"|"ptr"; "" = default (u32)
	// Parent is the qualified name of the enum this one refines (docs/77): the `enum Child is Parent:`
	// sealed-refinement suffix. "" means a root (no parent). Child's cases are a subset of Parent's, so
	// Child <: Parent.
	Parent string
	// StoreRegion is the declared store region (docs/128 §8): `enum Node layout(...) @source:`.
	// Every node of this store-backed root (and its refinements) lives in that region's store.
	StoreRegion string
}
