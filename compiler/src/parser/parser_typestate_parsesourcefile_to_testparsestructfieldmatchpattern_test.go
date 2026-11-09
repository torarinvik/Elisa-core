package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func parseSourceFile(t *testing.T, src string) (*ast.File, []string) {
	t.Helper()
	l := lexer.New("test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	file := p.ParseFile("test.elisa")
	errs := p.Errors()
	recordParserParityCase(t, src, len(errs), len(p.Notices()))
	return file, errs
}

func TestTupleRegionSuffixIsPreservedOnParametersAndPromotedOnReturns(t *testing.T) {
	file, errs := parseSourceFile(t, `def pair[@r](source: sview @r) -> (known: bool, value: sview) @r:
	return (true, source)

def project[@r](pair: (known: bool, value: sview) @r) -> sview @r:
	return pair.value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected two declarations, got %d", len(file.Decls))
	}
	pair, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first declaration to be a function, got %T", file.Decls[0])
	}
	if pair.ReturnRegion != "r" {
		t.Fatalf("expected tuple return region to become function contract @r, got %q", pair.ReturnRegion)
	}
	if tuple, ok := pair.ReturnType.(*ast.TupleTypeExpr); !ok || tuple.Region != "" {
		t.Fatalf("expected return region to be promoted off tuple type, got %T %#v", pair.ReturnType, pair.ReturnType)
	}
	project, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok || len(project.Params) != 1 {
		t.Fatalf("expected second declaration to have one function parameter, got %T", file.Decls[1])
	}
	tuple, ok := project.Params[0].Type.(*ast.TupleTypeExpr)
	if !ok || tuple.Region != "r" {
		t.Fatalf("expected tuple parameter to retain @r, got %T %#v", project.Params[0].Type, project.Params[0].Type)
	}
}

func TestParseCharLiteralInConstDecl(t *testing.T) {
	file, errs := parseSourceFile(t, "const VALUE: char = '\\n'\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected const decl, got %T", file.Decls[0])
	}
	lit, ok := decl.Value.(*ast.CharLit)
	if !ok {
		t.Fatalf("expected char literal, got %T", decl.Value)
	}
	if lit.Value != "\n" {
		t.Fatalf("expected decoded newline char literal, got %q", lit.Value)
	}
	if named, ok := decl.Type.(*ast.NamedType); !ok || named.Name != "char" {
		t.Fatalf("expected const type char, got %T %#v", decl.Type, decl.Type)
	}
}

func TestParseBitIntConstEnumAndPackedGroups(t *testing.T) {
	file, errs := parseSourceFile(t, `const enum Flags of u4:
	None = 0
	Read = 1

struct Header:
	flags: bitset:
		has_read
		has_write
	layout: bitfield:
		tag: u4
		arity: u3
		active: u1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	enumDecl, ok := file.Decls[0].(*ast.ConstEnumDecl)
	if !ok {
		t.Fatalf("expected const enum decl, got %T", file.Decls[0])
	}
	if named, ok := enumDecl.Storage.(*ast.NamedType); !ok || named.Name != "u4" {
		t.Fatalf("expected const enum storage u4, got %T %#v", enumDecl.Storage, enumDecl.Storage)
	}
	structDecl, ok := file.Decls[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[1])
	}
	if structDecl.Fields[0].BitGroup == nil || structDecl.Fields[0].BitGroup.Kind != ast.BitGroupBitset {
		t.Fatalf("expected first field to be a bitset group, got %#v", structDecl.Fields[0].BitGroup)
	}
	if structDecl.Fields[1].BitGroup == nil || structDecl.Fields[1].BitGroup.Kind != ast.BitGroupBitfield {
		t.Fatalf("expected second field to be a bitfield group, got %#v", structDecl.Fields[1].BitGroup)
	}
	if got := len(structDecl.Fields[1].BitGroup.Members); got != 3 {
		t.Fatalf("expected three bitfield members, got %d", got)
	}
}

func TestParseStructLayoutModes(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Header layout(packed):
	tag: u4
	arity: u3
	active: u1

struct CHeader layout(c):
	kind: u32
	flags: u32
	size: usize

struct Particle layout(aos):
	x: f32
	y: f32

struct ParticleRows layout(soa):
	x: f32
	y: f32
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	header, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected first decl to be a struct, got %T", file.Decls[0])
	}
	if header.Layout != ast.StructLayoutPacked || header.ReprC {
		t.Fatalf("expected packed struct layout with non-C ABI marker, got layout=%v reprC=%v", header.Layout, header.ReprC)
	}
	cHeader, ok := file.Decls[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected second decl to be a struct, got %T", file.Decls[1])
	}
	if cHeader.Layout != ast.StructLayoutC || !cHeader.ReprC {
		t.Fatalf("expected C struct layout with C ABI marker, got layout=%v reprC=%v", cHeader.Layout, cHeader.ReprC)
	}
	particle, ok := file.Decls[2].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected third decl to be a struct, got %T", file.Decls[2])
	}
	if particle.Layout != ast.StructLayoutAOS || particle.ReprC {
		t.Fatalf("expected AOS struct layout with non-C ABI marker, got layout=%v reprC=%v", particle.Layout, particle.ReprC)
	}
	particleRows, ok := file.Decls[3].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected fourth decl to be a struct, got %T", file.Decls[3])
	}
	if particleRows.Layout != ast.StructLayoutSOA || particleRows.ReprC {
		t.Fatalf("expected SOA struct layout with non-C ABI marker, got layout=%v reprC=%v", particleRows.Layout, particleRows.ReprC)
	}
}

func TestParseStructRegionOwnerForms(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Expr[@owner]:
	left: Expr&? @owner
	right: Expr&? @owner

struct SymbolRows[@owner] layout(soa):
	name_id: NameId
	span: Span
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	expr, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected first decl to be a struct, got %T", file.Decls[0])
	}
	if len(expr.RegionParams) != 1 || expr.RegionParams[0] != "owner" {
		t.Fatalf("expected explicit struct region param [owner], got %v", expr.RegionParams)
	}
	if expr.RegionOwner != "" {
		t.Fatalf("expected explicit generic region form to keep empty RegionOwner sugar marker, got %q", expr.RegionOwner)
	}
	leftType, ok := expr.Fields[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected left field to parse as ref type, got %T", expr.Fields[0].Type)
	}
	if leftType.Region != "owner" {
		t.Fatalf("expected left field to carry owner region qualifier, got region=%q", leftType.Region)
	}
	rows, ok := file.Decls[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected second decl to be a struct, got %T", file.Decls[1])
	}
	if rows.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected SymbolRows to keep SOA layout, got %v", rows.Layout)
	}
	if rows.RegionOwner != "" {
		t.Fatalf("expected explicit generic region form to keep empty RegionOwner sugar marker, got %q", rows.RegionOwner)
	}
	if len(rows.RegionParams) != 1 || rows.RegionParams[0] != "owner" {
		t.Fatalf("expected explicit region form to populate region param [owner], got %v", rows.RegionParams)
	}
}

func TestParseFuncRegionParamAtSigilForm(t *testing.T) {
	// `[@r]` is the canonical region-param spelling — the same `@r` token used at use sites —
	// and composes with type params in `[T, @r]`.
	file, errs := parseSourceFile(t, `def repl[@r](s: mutable darray[u8]& @r) -> void:
	s <- [1]

def merge[T, @r](dst: mutable darray[T]& @r, src: darray[T]) -> void:
	dst.push(src[0])
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	repl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first decl to be a func, got %T", file.Decls[0])
	}
	if len(repl.RegionParams) != 1 || repl.RegionParams[0] != "r" {
		t.Fatalf("expected [@r] to yield region param [r], got %v", repl.RegionParams)
	}
	merge, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected second decl to be a func, got %T", file.Decls[1])
	}
	if len(merge.TypeParams) != 1 || merge.TypeParams[0] != "T" {
		t.Fatalf("expected [T, @r] to keep type param [T], got %v", merge.TypeParams)
	}
	if len(merge.RegionParams) != 1 || merge.RegionParams[0] != "r" {
		t.Fatalf("expected [T, @r] to yield region param [r], got %v", merge.RegionParams)
	}
}

func TestLegacyRegionParamSpellingIsRejected(t *testing.T) {
	src := `def repl[region r](s: mutable darray[u8]& @r) -> void:
	s <- [1]

struct Box[T, region owner]:
	value: T
`
	l := lexer.New("test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	_ = p.ParseFile("test.elisa")
	recordParserParityCase(t, src, len(p.Errors()), len(p.Notices()))
	errs := strings.Join(p.Errors(), "\n")
	for _, want := range []string{"`region r` has been removed", "use `@r`", "`region owner` has been removed", "use `@owner`"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("expected removed region-param diagnostic containing %q, got:\n%s", want, errs)
		}
	}
}

func TestCanonicalRegionParamSpellingEmitsNoDeprecation(t *testing.T) {
	src := "def repl[@r](s: mutable darray[u8]& @r) -> void:\n\ts <- [1]\n"
	l := lexer.New("test.elisa", []byte(src))
	tokens := l.Tokenize()
	p := New(tokens)
	p.ParseFile("test.elisa")
	recordParserParityCase(t, src, len(p.Errors()), len(p.Notices()))
	if notices := p.Notices(); len(notices) != 0 {
		t.Fatalf("canonical `[@r]` spelling should emit no deprecation notice, got: %v", notices)
	}
}

func TestFormatStructRegionOwnerFormsRoundTrips(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Expr[@owner]:
	left: Expr&? @owner

struct Box[T, @owner]:
	value: T
	next: Box[T, owner]&? @owner

struct SymbolRows[@owner] layout(soa):
	name_id: NameId
	span: Span
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"struct Expr[@owner]:",
		"struct Box[T, @owner]:",
		"struct SymbolRows[@owner] layout(soa):",
		"next: Box[T, owner]&? @owner",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
	roundTripped, roundTripErrs := parseSourceFile(t, formatted)
	if len(roundTripErrs) != 0 {
		t.Fatalf("formatted region-owner source did not parse again: %v\n%s", roundTripErrs, formatted)
	}
	if len(roundTripped.Decls) != len(file.Decls) {
		t.Fatalf("expected %d decls after round trip, got %d", len(file.Decls), len(roundTripped.Decls))
	}
}

func TestParseStructRegionOwnerRejectsDuplicateExplicitRegion(t *testing.T) {
	_, errs := parseSourceFile(t, `struct Expr[@owner, @owner]:
	next: Expr&? @owner
`)
	if len(errs) == 0 {
		t.Fatalf("expected duplicate owner region diagnostic")
	}
	if !strings.Contains(strings.Join(errs, "\n"), `duplicate function generic parameter "owner"`) {
		t.Fatalf("expected duplicate owner region diagnostic, got: %v", errs)
	}
}

func TestParseSOADeclRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `soa PascalSymbols:
	name_id: NameId
	span: Span
	flags: u32
`)
	if len(errs) == 0 {
		t.Fatalf("expected `soa Name:` to be rejected")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "`soa Name:` declarations have been removed") {
		t.Fatalf("expected removed soa declaration diagnostic, got: %v", errs)
	}
}

func TestParseConstEnumAllowsInferredStorage(t *testing.T) {
	file, errs := parseSourceFile(t, `const enum Mode:
	None
	Fast
	Slow = 7
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	enumDecl, ok := file.Decls[0].(*ast.ConstEnumDecl)
	if !ok {
		t.Fatalf("expected const enum decl, got %T", file.Decls[0])
	}
	if enumDecl.Storage != nil {
		t.Fatalf("expected omitted const enum storage to parse as nil, got %#v", enumDecl.Storage)
	}
	if enumDecl.Members[0].Value != nil || enumDecl.Members[1].Value != nil {
		t.Fatalf("expected omitted member values to stay nil for semantic auto-numbering")
	}
}

// `return?` (including its pattern-guard form) has been removed: it is a hard parser error.
func TestParseReturnQuestionPatternGuard(t *testing.T) {
	_, errs := parseSourceFile(t, `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return? value if node is Expr.Int(value)
    return 0
`)
	if !strings.Contains(strings.Join(errs, "\n"), "`return?` is no longer supported") {
		t.Fatalf("expected `return?` removal diagnostic, got: %v", errs)
	}
}

// `match?` has been removed: it is a hard parser error.
func TestParseOptionalMatchLowersToOptionalBindThenMatch(t *testing.T) {
	_, errs := parseSourceFile(t, `enum Expr:
    Int(value: i64)
    Missing

def check(maybe: Expr?) -> i64:
    match? node = maybe:
        Expr.Int(value):
            return value
        _:
            return 0
    return -1
`)
	if !strings.Contains(strings.Join(errs, "\n"), "`match?` is no longer supported") {
		t.Fatalf("expected `match?` removal diagnostic, got: %v", errs)
	}
}
