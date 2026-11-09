package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestParseCatchExpr(t *testing.T) {
	file, errs := parseSourceFile(t, `error FileError:
	NotFound
	Busy

extern read_value(flag: bool) -> i64 error[FileError]

def load(flag: bool) -> i64:
	return catch read_value(flag):
		loaded: loaded
		error e:
			1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	loadDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	ret, ok := loadDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", loadDecl.Body[0])
	}
	catchExpr, ok := ret.Value.(*ast.CatchExpr)
	if !ok {
		t.Fatalf("expected catch expr, got %T", ret.Value)
	}
	if catchExpr.Success.Name != "loaded" || len(catchExpr.Success.Body) != 1 {
		t.Fatalf("unexpected catch success arm %#v", catchExpr.Success)
	}
	if len(catchExpr.Arms) != 1 {
		t.Fatalf("expected one error arm, got %#v", catchExpr.Arms)
	}
	if catchExpr.Arms[0].Name != "e" || !catchExpr.Arms[0].ErrorBinding {
		t.Fatalf("unexpected catch arm names %#v", catchExpr.Arms)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"catch read_value(flag):",
		"loaded:",
		"error e:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseCatchExprOkPayloadBinding(t *testing.T) {
	file, errs := parseSourceFile(t, `error ReadError:
	Failed
extern read_value() -> i64 error[ReadError]
def load() -> i64:
	return catch read_value():
		ok payload:
			payload
		error failure:
			0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[2].(*ast.FuncDecl)
	ret := fn.Body[0].(*ast.ReturnStmt)
	catchExpr := ret.Value.(*ast.CatchExpr)
	if catchExpr.Success.Name != "payload" || catchExpr.Success.ErrorBinding {
		t.Fatalf("unexpected success payload binding: %#v", catchExpr.Success)
	}
	if len(catchExpr.Arms) != 1 || catchExpr.Arms[0].Name != "failure" || !catchExpr.Arms[0].ErrorBinding {
		t.Fatalf("unexpected error arms: %#v", catchExpr.Arms)
	}
}

func TestParseErrorDeclPayloadVariants(t *testing.T) {
	file, errs := parseSourceFile(t, `error PascalBackendError:
	UnsupportedType(span: Span, type_expr: PascalType.Type)
	LLVMVerificationFailed(span: Span)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ErrorDecl)
	if !ok {
		t.Fatalf("expected error decl, got %T", file.Decls[0])
	}
	if len(decl.Tags) != 2 || decl.Tags[0].Name != "UnsupportedType" || len(decl.Tags[0].Payload) != 2 {
		t.Fatalf("unexpected error payload variants: %#v", decl.Tags)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"UnsupportedType(span: Span, type_expr: PascalType.Type)",
		"LLVMVerificationFailed(span: Span)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseFilteredIterableForBraceDestructure(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Row:
    left: int
    right: int

def total(items: array[Row, 2]) -> int:
    total: mutable int = 0
    for {left, right: value} in items if left != 0:
        total <- total + value
    return total
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	totalDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	iterStmt, ok := totalDecl.Body[1].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for statement, got %T", totalDecl.Body[1])
	}
	if iterStmt.Filter == nil {
		t.Fatal("expected iterable for filter expression")
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindStructPattern)
	if !ok {
		t.Fatalf("expected brace destructure pattern, got %T", iterStmt.Pattern)
	}
	if !pattern.Brace {
		t.Fatalf("expected brace destructure pattern, got %#v", pattern)
	}
	if got := pattern.Args[1].Field; got != "right" {
		t.Fatalf("expected second destructure field to be right, got %q", got)
	}
	if got := pattern.Args[1].Name; got != "value" {
		t.Fatalf("expected second destructure binding to be value, got %q", got)
	}

	formatted := unparse.FormatDecl(totalDecl)
	if !strings.Contains(formatted, "for {left, right: value} in items if (left != 0):") {
		t.Fatalf("expected formatted output to preserve filtered brace loop, got:\n%s", formatted)
	}
}

func TestParseStaticFunctionDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `static def answer() -> i64:
    return 42

def keep() -> i64:
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected static function decl, got %T", file.Decls[0])
	}
	if !decl.Static || decl.Name != "answer" {
		t.Fatalf("expected static answer function, got %#v", decl)
	}
}

func TestParseStaticFunctionNestedContextualStaticStmts(t *testing.T) {
	file, errs := parseSourceFile(t, `static def answer(flag: bool) -> i64:
    assert flag, "flag must be true"
    if flag:
        return 42
    else:
        error("inactive")
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected static function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 2 {
		t.Fatalf("expected 2 body statements, got %d", len(decl.Body))
	}
	if _, ok := decl.Body[0].(*ast.StaticAssertStmt); !ok {
		t.Fatalf("expected first statement to parse as static assert, got %T", decl.Body[0])
	}
	ifStmt, ok := decl.Body[1].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected second statement to parse as if, got %T", decl.Body[1])
	}
	if len(ifStmt.Else) != 1 {
		t.Fatalf("expected single else statement, got %d", len(ifStmt.Else))
	}
	if _, ok := ifStmt.Else[0].(*ast.StaticErrorStmt); !ok {
		t.Fatalf("expected else branch to parse contextual static error, got %T", ifStmt.Else[0])
	}
}

func TestParseStaticAssertBlocks(t *testing.T) {
	file, errs := parseSourceFile(t, `static assert:
    5 > 3
    8 > 4, "math still works"

def keep() -> void:
    static assert:
        true
        13 > 8
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	top, ok := file.Decls[0].(*ast.StaticAssertBlockDecl)
	if !ok {
		t.Fatalf("expected top-level static assert block, got %T", file.Decls[0])
	}
	if len(top.Assertions) != 2 {
		t.Fatalf("expected 2 top-level assertions, got %d", len(top.Assertions))
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	stmt, ok := decl.Body[0].(*ast.StaticAssertBlockStmt)
	if !ok {
		t.Fatalf("expected function static assert block, got %T", decl.Body[0])
	}
	if len(stmt.Assertions) != 2 {
		t.Fatalf("expected 2 statement assertions, got %d", len(stmt.Assertions))
	}
}

func TestParseStaticGenerateDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Int(value: i64)
    Bool(value: bool)

static generate:
    for variant in variants(Expr):
        emit def is_${variant.name}(expr: Expr) -> bool:
            return expr is Expr.${variant.name}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	gen, ok := file.Decls[1].(*ast.StaticGenerateDecl)
	if !ok {
		t.Fatalf("expected static generate decl, got %T", file.Decls[1])
	}
	if len(gen.Body) != 1 {
		t.Fatalf("expected one generate statement, got %d", len(gen.Body))
	}
	loop, ok := gen.Body[0].(*ast.StaticGenerateForDecl)
	if !ok {
		t.Fatalf("expected generate for statement, got %T", gen.Body[0])
	}
	if len(loop.Body) != 1 {
		t.Fatalf("expected one loop body statement, got %d", len(loop.Body))
	}
	if _, ok := loop.Body[0].(*ast.StaticGenerateEmitDecl); !ok {
		t.Fatalf("expected emit statement, got %T", loop.Body[0])
	}
}

func TestParseRejectsEmitOutsideStaticGenerate(t *testing.T) {
	_, errs := parseSourceFile(t, `emit def leaked() -> bool:
    return true
`)
	if len(errs) == 0 {
		t.Fatalf("expected parser error for emit outside static generate")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unexpected token") {
		t.Fatalf("expected unexpected-token diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestParseOwnerDeclarationSugar(t *testing.T) {
	file, errs := parseSourceFile(t, "def build(owner: mutable Arena&) -> void:\n    items: Widget[i64] in owner\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	if !decl.Mutable {
		t.Fatal("expected owner declaration to be mutable")
	}
	if ident, ok := decl.Owner.(*ast.Ident); !ok || ident.Name != "owner" {
		t.Fatalf("expected owner ident, got %T %#v", decl.Owner, decl.Owner)
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected owner declaration to synthesize builder call, got %T", decl.Value)
	}
	if field, ok := call.Func.(*ast.FieldExpr); !ok || field.Field != "builder" {
		t.Fatalf("expected synthesized owner.builder call, got %T %#v", call.Func, call.Func)
	}
	formatted := unparse.FormatStmt(fn.Body[0])
	if formatted != "items: Widget[i64] in owner" {
		t.Fatalf("expected formatter to preserve builder owner sugar, got %q", formatted)
	}
}
