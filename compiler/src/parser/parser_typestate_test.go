package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func parseSourceFile(t *testing.T, src string) (*ast.File, []string) {
	t.Helper()
	l := lexer.New("test.llcontext", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	file := p.ParseFile("test.llcontext")
	return file, p.Errors()
}

func TestParseStructDeclWithAggregateStateParam(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[?]:\n    value: any i32&\n")
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
	file, errs := parseSourceFile(t, "struct Holder[?, ?]:\n    value: any i32&\n")
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

func TestParseNamedRefStateAttachesToNearestRef(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[refstate s]:\n    value: any i32&&[s]\n")
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
