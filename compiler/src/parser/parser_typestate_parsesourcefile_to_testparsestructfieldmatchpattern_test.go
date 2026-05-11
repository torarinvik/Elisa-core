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
	return file, p.Errors()
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
	file, errs := parseSourceFile(t, `struct Header layout packed:
	tag: u4
	arity: u3
	active: u1

struct CHeader layout c:
	kind: u32
	flags: u32
	size: usize

layout aos struct Particle:
	x: f32
	y: f32

layout soa struct ParticleRows:
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
	file, errs := parseSourceFile(t, `struct Expr[region owner]:
	left: owner Expr&?
	right: owner Expr&?

layout soa struct SymbolRows in owner:
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
	if leftType.Region != "owner" && leftType.StorageParam != "owner" {
		t.Fatalf("expected left field to carry owner qualifier, got region=%q storageParam=%q", leftType.Region, leftType.StorageParam)
	}
	rows, ok := file.Decls[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected second decl to be a struct, got %T", file.Decls[1])
	}
	if rows.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected SymbolRows to keep SOA layout, got %v", rows.Layout)
	}
	if rows.RegionOwner != "owner" {
		t.Fatalf("expected sugar owner marker, got %q", rows.RegionOwner)
	}
	if len(rows.RegionParams) != 1 || rows.RegionParams[0] != "owner" {
		t.Fatalf("expected sugar form to desugar to region param [owner], got %v", rows.RegionParams)
	}
}

func TestFormatStructRegionOwnerFormsRoundTrips(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Expr[region owner]:
	left: owner Expr&?

struct Box[T] in owner:
	value: T
	next: owner Box[T, owner]&?

layout soa struct SymbolRows in owner:
	name_id: NameId
	span: Span
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"struct Expr[region owner]:",
		"struct Box[T] in owner:",
		"layout soa struct SymbolRows in owner:",
		"next: owner Box[T, owner]&?",
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
	_, errs := parseSourceFile(t, `struct Expr[region owner] in owner:
	next: owner Expr&?
`)
	if len(errs) == 0 {
		t.Fatalf("expected duplicate owner region diagnostic")
	}
	if !strings.Contains(strings.Join(errs, "\n"), `duplicate struct region parameter "owner"`) {
		t.Fatalf("expected duplicate owner region diagnostic, got: %v", errs)
	}
}

func TestParseSOADecl(t *testing.T) {
	file, errs := parseSourceFile(t, `soa PascalSymbols:
	name_id: NameId
	span: Span
	flags: u32
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StoreDecl)
	if !ok {
		t.Fatalf("expected SOA decl to parse as a column store decl, got %T", file.Decls[0])
	}
	if !decl.Soa {
		t.Fatalf("expected SOA marker on parsed declaration")
	}
	if decl.Name != "PascalSymbols" || len(decl.Fields) != 3 {
		t.Fatalf("unexpected SOA declaration: %#v", decl)
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
func TestParseReturnQuestionPatternGuard(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return? value if node is Expr.Int(value)
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	if len(decl.Body) < 2 {
		t.Fatalf("expected guarded return plus fallback, got %d statements", len(decl.Body))
	}
	stmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected return? guard to lower to if statement, got %T", decl.Body[0])
	}
	if _, ok := stmt.Cond.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected pattern guard condition, got %T", stmt.Cond)
	}
	if len(stmt.Then) != 1 {
		t.Fatalf("expected one guarded return, got %d", len(stmt.Then))
	}
	ret, ok := stmt.Then[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected guarded return body, got %T", stmt.Then[0])
	}
	ident, ok := ret.Value.(*ast.Ident)
	if !ok || ident.Name != "value" {
		t.Fatalf("expected guarded return value binding, got %T %#v", ret.Value, ret.Value)
	}
}
func TestParseOptionalMatchLowersToOptionalBindThenMatch(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
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
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected match? to lower to if statement, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.OptionalBindExpr)
	if !ok || cond.Name != "node" {
		t.Fatalf("expected optional bind condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	if ident, ok := cond.Value.(*ast.Ident); !ok || ident.Name != "maybe" {
		t.Fatalf("expected optional bind source maybe, got %T %#v", cond.Value, cond.Value)
	}
	if len(ifStmt.Then) != 1 {
		t.Fatalf("expected one guarded match, got %d", len(ifStmt.Then))
	}
	matchStmt, ok := ifStmt.Then[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected guarded match statement, got %T", ifStmt.Then[0])
	}
	if ident, ok := matchStmt.Value.(*ast.Ident); !ok || ident.Name != "node" {
		t.Fatalf("expected match over unwrapped value, got %T %#v", matchStmt.Value, matchStmt.Value)
	}
	if len(matchStmt.Arms) != 2 {
		t.Fatalf("expected two match arms, got %d", len(matchStmt.Arms))
	}
}
func TestParseStructDeclWithAggregateStateParam(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[?]:\n    value: i32&\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if !decl.HasStateParam {
		t.Fatal("expected struct declaration to record aggregate state parameter")
	}
	if decl.StateParamCount != 1 {
		t.Fatalf("expected one aggregate state parameter, got %d", decl.StateParamCount)
	}
	if len(decl.TypeParams) != 0 {
		t.Fatalf("expected no type params, got %v", decl.TypeParams)
	}
}
func TestParseStructDeclWithMultipleAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[?, ?]:\n    value: i32&\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if decl.StateParamCount != 2 {
		t.Fatalf("expected two aggregate state parameters, got %d", decl.StateParamCount)
	}
}
func TestParseStructDeclWithTypeAndAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[T][?]:\n    value: T\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.TypeParams) != 1 || decl.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %v", decl.TypeParams)
	}
	if !decl.HasStateParam {
		t.Fatal("expected struct declaration to record aggregate state parameter")
	}
	if decl.StateParamCount != 1 {
		t.Fatalf("expected one aggregate state parameter, got %d", decl.StateParamCount)
	}
}
func TestParseStructDeclWithTypeAndMultipleAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[T][?, ?]:\n    value: T\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.TypeParams) != 1 || decl.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %v", decl.TypeParams)
	}
	if decl.StateParamCount != 2 {
		t.Fatalf("expected two aggregate state parameters, got %d", decl.StateParamCount)
	}
}
func TestParseAggregateStateInstantiationTypeExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Foo[&]) -> Foo[!]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	if paramType.State != ast.RefStateNonNull {
		t.Fatalf("expected non-null aggregate state, got %v", paramType.State)
	}
	base, ok := paramType.Base.(*ast.NamedType)
	if !ok || base.Name != "Foo" {
		t.Fatalf("expected base named type Foo, got %T %v", paramType.Base, paramType.Base)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state return type, got %T", decl.ReturnType)
	}
	if retType.State != ast.RefStateNull {
		t.Fatalf("expected null aggregate state, got %v", retType.State)
	}
}
func TestParseAggregateStateInstantiationAfterGenericArgs(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep[T](value: Foo[T][&]) -> Foo[T][?]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	base, ok := paramType.Base.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected generic base type, got %T", paramType.Base)
	}
	if base.Name != "Foo" || len(base.Args) != 1 {
		t.Fatalf("expected Foo[T], got %#v", base)
	}
	arg, ok := base.Args[0].(*ast.NamedType)
	if !ok || arg.Name != "T" {
		t.Fatalf("expected generic arg T, got %T %#v", base.Args[0], base.Args[0])
	}
	if paramType.State != ast.RefStateNonNull {
		t.Fatalf("expected non-null aggregate state, got %v", paramType.State)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok || retType.State != ast.RefStateNullable {
		t.Fatalf("expected maybe aggregate state return type, got %T %#v", decl.ReturnType, decl.ReturnType)
	}
}
func TestParseAggregateStateInstantiationWithMultipleStates(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Foo[!, &]) -> Foo[?, !]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	if len(paramType.States) != 2 || paramType.States[0] != ast.RefStateNull || paramType.States[1] != ast.RefStateNonNull {
		t.Fatalf("expected [!, &] aggregate states, got %#v", paramType.States)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state return type, got %T", decl.ReturnType)
	}
	if len(retType.States) != 2 || retType.States[0] != ast.RefStateNullable || retType.States[1] != ast.RefStateNull {
		t.Fatalf("expected [?, !] aggregate states, got %#v", retType.States)
	}
}
func TestParseStructDeclRejectsNonPlaceholderStateMarker(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Holder[?, &]:\n    value: i32\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for non-placeholder struct state declaration")
	}
	if !strings.Contains(errs[0], "struct state parameter declaration must use only [?] placeholders") {
		t.Fatalf("expected struct state parameter diagnostic, got %v", errs)
	}
}
func TestParseStructDeclWithNamedRefQualifiers(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[refstorage store, refstate state]:\n    value: store i32&[state]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.RefStorageParams) != 1 || decl.RefStorageParams[0] != "store" {
		t.Fatalf("expected refstorage param [store], got %v", decl.RefStorageParams)
	}
	if len(decl.RefStateParams) != 1 || decl.RefStateParams[0] != "state" {
		t.Fatalf("expected refstate param [state], got %v", decl.RefStateParams)
	}
	if len(decl.GenericParams) != 2 || decl.GenericParams[0].Kind != ast.GenericParamRefStorage || decl.GenericParams[1].Kind != ast.GenericParamRefState {
		t.Fatalf("expected ordered mixed generic params, got %#v", decl.GenericParams)
	}
	refType, ok := decl.Fields[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected ref field type, got %T", decl.Fields[0].Type)
	}
	if refType.StorageParam != "store" {
		t.Fatalf("expected storage param store, got %q", refType.StorageParam)
	}
	if refType.StateParam != "state" {
		t.Fatalf("expected state param state, got %q", refType.StateParam)
	}
}
func TestParseStructDeclWithValueGenericParam(t *testing.T) {
	file, errs := parseSourceFile(t, "struct InlineVec[T, N: usize]:\n    items: T[N]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.GenericParams) != 2 || decl.GenericParams[0].Kind != ast.GenericParamType || decl.GenericParams[1].Kind != ast.GenericParamValue {
		t.Fatalf("expected type param followed by value param, got %#v", decl.GenericParams)
	}
	if decl.GenericParams[1].Name != "N" || decl.GenericParams[1].InterfaceBound != "usize" {
		t.Fatalf("expected value param N: usize, got %#v", decl.GenericParams[1])
	}
	arrayType, ok := decl.Fields[0].Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected T[N] to parse as array type, got %T", decl.Fields[0].Type)
	}
	if _, ok := arrayType.Size.(*ast.Ident); !ok {
		t.Fatalf("expected array size to remain value param identifier, got %T", arrayType.Size)
	}
}
func TestParseNamedRefStateAttachesToNearestRef(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[refstate s]:\n    value: i32&&[s]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.StructDecl)
	outer, ok := decl.Fields[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected outer ref type, got %T", decl.Fields[0].Type)
	}
	inner, ok := outer.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected nested inner ref type, got %T", outer.Elem)
	}
	if outer.StateParam != "s" {
		t.Fatalf("expected outer ref to carry state param s, got %q", outer.StateParam)
	}
	if inner.StateParam != "" {
		t.Fatalf("expected inner ref to have no named state param, got %q", inner.StateParam)
	}
}
func TestParseLegacyNullableRefArraySuffixStillWorks(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: heap i32&?[COUNT]) -> void:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	arrayType, ok := decl.Params[0].Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected array type, got %T", decl.Params[0].Type)
	}
	refType, ok := arrayType.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected array element ref type, got %T", arrayType.Elem)
	}
	if refType.State != ast.RefStateNullable {
		t.Fatalf("expected nullable ref state, got %v", refType.State)
	}
	if refType.StateParam != "" {
		t.Fatalf("expected no named refstate param, got %q", refType.StateParam)
	}
}
func TestParsePackedIfPatternWithViewAliasBinding(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store as Expr.Lit(value: value):\n        lit: packedview[Expr.Lit] = node\n        return value + lit.span\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	if len(matchStmt.Arms) != 2 {
		t.Fatalf("expected match arm plus wildcard, got %d", len(matchStmt.Arms))
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected Expr.Lit pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if pattern.EnumName != "Expr" || pattern.Variant != "Lit" {
		t.Fatalf("expected Expr.Lit pattern, got %#v", pattern)
	}
	if len(pattern.Args) != 1 {
		t.Fatalf("expected one binding arg, got %d", len(pattern.Args))
	}
	bindPattern, ok := pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || bindPattern.Name != "value" {
		t.Fatalf("expected value payload binding, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if _, ok := matchStmt.Arms[0].Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected packedview alias binding in match body, got %T", matchStmt.Arms[0].Body[0])
	}
}
func TestParsePackedIfNestedPayloadPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n    Add(left: Expr, right: Expr)\n\ndef left_value(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store as Expr.Add(Expr.Int(value), rhs):\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok || len(pattern.Args) != 2 {
		t.Fatalf("expected two payload patterns, got %#v", matchStmt.Arms[0].Pattern)
	}
	leftPattern, ok := pattern.Args[0].Pattern.(*ast.MatchVariantPattern)
	if !ok || leftPattern.EnumName != "Expr" || leftPattern.Variant != "Int" || len(leftPattern.Args) != 1 {
		t.Fatalf("expected nested Expr.Int(value) pattern, got %#v", pattern.Args[0].Pattern)
	}
	leftBind, ok := leftPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "value" {
		t.Fatalf("expected nested bind pattern value, got %T %#v", leftPattern.Args[0].Pattern, leftPattern.Args[0].Pattern)
	}
	rightBind, ok := pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "rhs" {
		t.Fatalf("expected rhs bind pattern, got %T %#v", pattern.Args[1].Pattern, pattern.Args[1].Pattern)
	}
}

func TestParseNestedOrPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Token:\n    Ident\n    Keyword\n    Other\n\nenum Expr:\n    Leaf(kind: Token)\n\ndef score(expr: Expr) -> int:\n    match expr:\n        Expr.Leaf(Token.Ident | Token.Keyword):\n            return 1\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	matchStmt := decl.Body[0].(*ast.MatchStmt)
	pattern := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if len(pattern.Args) != 1 {
		t.Fatalf("expected one payload arg, got %#v", pattern.Args)
	}
	orPattern, ok := pattern.Args[0].Pattern.(*ast.MatchOrPattern)
	if !ok || len(orPattern.Options) != 2 {
		t.Fatalf("expected two-option nested or pattern, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "Expr.Leaf(Token.Ident | Token.Keyword):") {
		t.Fatalf("expected nested or pattern to format, got:\n%s", formatted)
	}
}
func TestParseNestedOrPatternAllowsBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Token:\n    Ident(value: int)\n    Keyword(value: int)\n\nenum Expr:\n    Leaf(kind: Token)\n\ndef score(expr: Expr) -> int:\n    match expr:\n        Expr.Leaf(Token.Ident(value) | Token.Keyword(value)):\n            return value\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	matchStmt := decl.Body[0].(*ast.MatchStmt)
	pattern := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	orPattern, ok := pattern.Args[0].Pattern.(*ast.MatchOrPattern)
	if !ok || len(orPattern.Options) != 2 {
		t.Fatalf("expected binding nested or pattern, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "Token.Ident(value) | Token.Keyword(value)") {
		t.Fatalf("expected binding nested or pattern to format, got:\n%s", formatted)
	}
}
func TestParseEnumVariantIsCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    Add(left: int, right: int)\n\ndef is_int(node: Expr) -> bool:\n    if node is Expr.Int:\n        return true\n    return false\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary condition, got %T", ifStmt.Cond)
	}
	if cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is operator, got %s", lexer.TokenName(cond.Op))
	}
	typeExpr, ok := cond.Right.(*ast.TypeExprExpr)
	if !ok {
		t.Fatalf("expected typed is RHS, got %T", cond.Right)
	}
	named, ok := typeExpr.Type.(*ast.NamedType)
	if !ok || named.Name != "Expr.Int" {
		t.Fatalf("expected Expr.Int typed RHS, got %#v", typeExpr.Type)
	}
}
func TestParseEnumVariantIsConditionWithPayloadPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Float(PI: f64)\n\ndef is_pi(node: Expr) -> bool:\n    return node is Expr.Float(3.14)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary condition, got %T", ret.Value)
	}
	variantTarget, ok := cond.Right.(*ast.VariantTestExpr)
	if !ok {
		t.Fatalf("expected variant is target, got %T", cond.Right)
	}
	if variantTarget.Pattern == nil || variantTarget.Pattern.EnumName != "Expr" || variantTarget.Pattern.Variant != "Float" {
		t.Fatalf("expected Expr.Float payload test, got %#v", variantTarget.Pattern)
	}
	if len(variantTarget.Pattern.Args) != 1 {
		t.Fatalf("expected one payload pattern, got %#v", variantTarget.Pattern.Args)
	}
	if _, ok := variantTarget.Pattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected positional literal payload pattern, got %T", variantTarget.Pattern.Args[0].Pattern)
	}
}
func TestParseIsConditionWithAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    LT = 1\n    LTEQ = 2\n    GT = 3\n\ndef is_rel(kind: Tok) -> bool:\n    return kind is .LT | .LTEQ | .GT\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is-expression return, got %T %#v", ret.Value, ret.Value)
	}
	alts, ok := cond.Right.(*ast.IsPatternExpr)
	if !ok {
		t.Fatalf("expected multi-target is-pattern RHS, got %T", cond.Right)
	}
	if len(alts.Targets) != 3 {
		t.Fatalf("expected three is-pattern targets, got %#v", alts.Targets)
	}
	for i, target := range alts.Targets {
		if _, ok := target.(*ast.ShorthandMemberExpr); !ok {
			t.Fatalf("expected shorthand member target at %d, got %T", i, target)
		}
	}
}
func TestParseIsConditionWithQualifiedAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Bool(value: bool)\n    Char(value: i64)\n\ndef is_scalar(value: Expr) -> bool:\n    return value is Expr.Int | Expr.Bool | Expr.Char\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is-expression return, got %T %#v", ret.Value, ret.Value)
	}
	alts, ok := cond.Right.(*ast.IsPatternExpr)
	if !ok || len(alts.Targets) != 3 {
		t.Fatalf("expected three qualified alternatives, got %T %#v", cond.Right, cond.Right)
	}
}
func TestParseIsConditionWithGroupedMultilineAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Decl:
    ProcedureDecl
    ProcedureQualifiedDecl
    ProcedureGenericDecl
    FunctionDecl

def has_body(decl: Decl) -> bool:
    return decl is (
        Decl.ProcedureDecl
        | Decl.ProcedureQualifiedDecl
        | Decl.ProcedureGenericDecl
        | Decl.FunctionDecl
    )
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"return decl is (",
		"    Decl.ProcedureDecl",
		"    | Decl.ProcedureQualifiedDecl",
		"    | Decl.ProcedureGenericDecl",
		"    | Decl.FunctionDecl",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted grouped is alternatives to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseStructFieldMatchPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n    value: int\n\ndef score(tok: Token) -> int:\n    match tok:\n        Token(kind: .INTEGER, span: Span(start: start), value: value):\n            return start + value\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match stmt, got %T", decl.Body[0])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected struct match pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if pattern.TypeName != "Token" || len(pattern.Args) != 3 {
		t.Fatalf("unexpected top-level struct pattern %#v", pattern)
	}
	kindPattern, ok := pattern.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok {
		t.Fatalf("expected literal kind pattern, got %T", pattern.Args[0].Pattern)
	}
	if _, ok := kindPattern.Value.(*ast.ShorthandMemberExpr); !ok {
		t.Fatalf("expected shorthand member kind pattern, got %T", kindPattern.Value)
	}
	spanPattern, ok := pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected nested span struct pattern, got %T", pattern.Args[1].Pattern)
	}
	if spanPattern.TypeName != "Span" || len(spanPattern.Args) != 1 || spanPattern.Args[0].Name != "start" {
		t.Fatalf("unexpected nested span pattern %#v", spanPattern)
	}
	startBind, ok := spanPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || startBind.Name != "start" {
		t.Fatalf("expected start bind pattern, got %T %#v", spanPattern.Args[0].Pattern, spanPattern.Args[0].Pattern)
	}
	valueBind, ok := pattern.Args[2].Pattern.(*ast.MatchBindPattern)
	if !ok || valueBind.Name != "value" {
		t.Fatalf("expected value bind pattern, got %T %#v", pattern.Args[2].Pattern, pattern.Args[2].Pattern)
	}
}
