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
	if len(decl.TypeParams) != 0 {
		t.Fatalf("expected no type params, got %v", decl.TypeParams)
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

func TestParseStructDeclRejectsNonPlaceholderStateMarker(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Holder[&]:\n    value: i32\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for non-placeholder struct state declaration")
	}
	if !strings.Contains(errs[0], "struct state parameter declaration must use [?]") {
		t.Fatalf("expected struct state parameter diagnostic, got %v", errs)
	}
}
