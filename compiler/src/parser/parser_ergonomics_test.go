package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseOneLineFunctionBodies(t *testing.T) {
	file, errs := parseSourceFile(t, "def answer() -> int: return 42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 1 {
		t.Fatalf("expected one inline statement, got %d", len(decl.Body))
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", decl.Body[0])
	}
	if lit, ok := ret.Value.(*ast.IntLit); !ok || lit.Value != "42" {
		t.Fatalf("expected return 42, got %#v", ret.Value)
	}
}

func TestParseInfersAutoRegionForUntypedDArrayBuilders(t *testing.T) {
	file, errs := parseSourceFile(t, `def build(items: darray[i64]) -> usize:
    out = [item + 1 for item in items]
    more = item + 2 for each item in items
    return out.count + more.count
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 1 {
		t.Fatalf("expected function body to be wrapped in one auto region, got %d statements", len(decl.Body))
	}
	region, ok := decl.Body[0].(*ast.RegionStmt)
	if !ok {
		t.Fatalf("expected synthesized auto region, got %T", decl.Body[0])
	}
	if !region.Lazy || !strings.HasPrefix(region.Name, "__auto_") {
		t.Fatalf("expected lazy synthesized auto region, got name=%q lazy=%v", region.Name, region.Lazy)
	}
	if len(region.Body) != 3 {
		t.Fatalf("expected original body inside auto region, got %d statements", len(region.Body))
	}
}

func TestParseOneLineFunctionBodiesWithSemicolonSeparatedStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "def answer(x: int) -> int: _ = x; return 42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 2 {
		t.Fatalf("expected two inline statements, got %d", len(decl.Body))
	}
	if _, ok := decl.Body[0].(*ast.DiscardStmt); !ok {
		t.Fatalf("expected assignment statement, got %T", decl.Body[0])
	}
	if _, ok := decl.Body[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected return statement, got %T", decl.Body[1])
	}
}

func TestParseOneLineIfBodyInsideOneLineFunction(t *testing.T) {
	file, errs := parseSourceFile(t, "def clamp(flag: bool, value: mutable int&) -> int: if flag: value <- 1; return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 2 {
		t.Fatalf("expected inline if plus return, got %d statements", len(decl.Body))
	}
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected inline if statement, got %T", decl.Body[0])
	}
	if len(ifStmt.Then) != 1 {
		t.Fatalf("expected one inline if body statement, got %d", len(ifStmt.Then))
	}
	if _, ok := decl.Body[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected outer return statement, got %T", decl.Body[1])
	}
}

func TestParseOneLineImplMethodBodies(t *testing.T) {
	file, errs := parseSourceFile(t, "protocol Box:\n    def get() -> int\n\nimpl Box for int:\n    def get() -> int: return 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	implDecl, ok := file.Decls[1].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[1])
	}
	method, ok := implDecl.Members[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected impl method func decl, got %T", implDecl.Members[0])
	}
	ret, ok := method.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", method.Body[0])
	}
	if lit, ok := ret.Value.(*ast.IntLit); !ok || lit.Value != "1" {
		t.Fatalf("expected return 1, got %#v", ret.Value)
	}
}

func TestParseOneLineStaticFunctionBodies(t *testing.T) {
	file, errs := parseSourceFile(t, "static def yes() -> bool: assert true\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if _, ok := decl.Body[0].(*ast.StaticAssertStmt); !ok {
		t.Fatalf("expected static assert statement, got %T", decl.Body[0])
	}
	if !decl.Static {
		t.Fatalf("expected static function")
	}
	if decl.Body[0].Pos().Line != 1 {
		t.Fatalf("expected inline static stmt to stay on line 1, got %s", decl.Body[0].Pos())
	}
}

func TestParseStructDefaultsAndDestructuring(t *testing.T) {
	file, errs := parseSourceFile(t, `struct PairRow:
    first: i64
    second: i64 = 0

def build(pair: PairRow, rows: darray[PairRow]) -> i64:
    let PairRow{first: local_first, second} = pair
    for {first, second} in rows:
        return local_first + first + second
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	pairRow, ok := file.Decls[0].(*ast.StructDecl)
	if !ok || len(pairRow.Fields) != 2 || pairRow.Fields[1].DefaultValue == nil {
		t.Fatalf("expected PairRow struct field default, got %#v", file.Decls[0])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"let PairRow{first: local_first, second} = pair",
		"for {first, second} in rows:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseWithArenaScopedAllocatorShorthand(t *testing.T) {
	file, errs := parseSourceFile(t, `def build() -> usize:
    with arena scratch(8192) as owner:
        xs: darray[int] = [1, 2]
        return xs.count
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	arena, ok := decl.Body[0].(*ast.RegionStmt)
	if !ok {
		t.Fatalf("expected scoped arena region stmt, got %T", decl.Body[0])
	}
	if arena.Name != "scratch" || arena.OwnerName != "owner" || arena.Capacity == nil || len(arena.Body) != 2 {
		t.Fatalf("unexpected scoped arena shape: %#v", arena)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "with arena scratch(8192) as owner:") {
		t.Fatalf("expected unparse to preserve scoped arena shorthand, got:\n%s", formatted)
	}
}

func TestParseBraceStructForms(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Row:
    left: int
    right: int
    flag: bool

def run(row: Row, flag: bool) -> int:
    let {left: first, right} = row
    built: Row = Row{left: first, right, flag}
    next: Row = built{flag, right = first}
    if row is Row{left, right: current, flag: row_flag}:
        return current
    match next:
        Row{left, right: current, flag}:
            return current
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	runDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	letStmt, ok := runDecl.Body[0].(*ast.LetDestructureStmt)
	if !ok {
		t.Fatalf("expected let destructure statement, got %T", runDecl.Body[0])
	}
	if letStmt.Pattern == nil || !letStmt.Pattern.Brace {
		t.Fatalf("expected brace destructure pattern, got %#v", letStmt.Pattern)
	}
	if got := letStmt.Pattern.Args[0].Field; got != "left" {
		t.Fatalf("expected first destructure field to be left, got %q", got)
	}
	if got := letStmt.Pattern.Args[0].Name; got != "first" {
		t.Fatalf("expected first destructure binding to be first, got %q", got)
	}
	if got := letStmt.Pattern.Args[1].Name; got != "right" {
		t.Fatalf("expected second destructure binding to be right, got %q", got)
	}

	builtDecl, ok := runDecl.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected struct literal var decl, got %T", runDecl.Body[1])
	}
	builtLit, ok := builtDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected brace struct literal, got %T", builtDecl.Value)
	}
	if !builtLit.Brace {
		t.Fatalf("expected brace struct literal, got %#v", builtLit)
	}
	if got := builtLit.ArgName(1); got != "right" {
		t.Fatalf("expected second brace literal field to be named right, got %q", got)
	}
	if got := builtLit.ArgName(2); got != "flag" {
		t.Fatalf("expected third brace literal field to be named flag, got %q", got)
	}

	nextDecl, ok := runDecl.Body[2].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected record update var decl, got %T", runDecl.Body[2])
	}
	update, ok := nextDecl.Value.(*ast.RecordUpdateExpr)
	if !ok {
		t.Fatalf("expected record update expression, got %T", nextDecl.Value)
	}
	if got := update.ArgName(0); got != "flag" {
		t.Fatalf("expected first record update field to be flag, got %q", got)
	}
	if got := update.ArgName(1); got != "right" {
		t.Fatalf("expected second record update field to be right, got %q", got)
	}

	matchStmt, ok := runDecl.Body[4].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match statement, got %T", runDecl.Body[4])
	}
	matchPattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected brace struct match pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if !matchPattern.Brace {
		t.Fatalf("expected brace struct match pattern, got %#v", matchPattern)
	}

	formatted := unparse.FormatDecl(runDecl)
	for _, want := range []string{
		"let {left: first, right} = row",
		"Row{left: first, right, flag}",
		"built{flag, right = first}",
		"if (row is Row{left, right: current, flag: row_flag}):",
		"Row{left, right: current, flag}:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseParenStructLiteralNamedArgs(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Span:
    start: i64
    finish: i64

def build(start: i64) -> Span:
    return Span(start:, finish: start + 1)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	buildDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build function decl, got %T", file.Decls[1])
	}
	ret, ok := buildDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", buildDecl.Body[0])
	}
	lit, ok := ret.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected struct literal return value, got %T", ret.Value)
	}
	if lit.Brace {
		t.Fatalf("expected paren struct literal, got %#v", lit)
	}
	if got := lit.ArgName(0); got != "start" {
		t.Fatalf("expected first paren constructor arg to target start, got %q", got)
	}
	if got := lit.ArgName(1); got != "finish" {
		t.Fatalf("expected second paren constructor arg to target finish, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "return Span(start:, finish: (start + 1))") {
		t.Fatalf("expected formatted output to preserve paren constructor names, got:\n%s", formatted)
	}
}

func TestParseBraceStructLiteralSpread(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Accessors:
    read_name_id: i64?
    write_name_id: i64?
    default_enabled: bool

def update(base: Accessors, name: i64) -> Accessors:
    return Accessors{...base, read_name_id: name, default_enabled: true}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	updateDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected update function decl, got %T", file.Decls[1])
	}
	ret, ok := updateDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", updateDecl.Body[0])
	}
	lit, ok := ret.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected struct literal return value, got %T", ret.Value)
	}
	if len(lit.Spreads) != 1 {
		t.Fatal("expected struct literal spread")
	}
	if got := lit.ArgName(0); got != "read_name_id" {
		t.Fatalf("expected first override to target read_name_id, got %q", got)
	}
	formatted := unparse.FormatDecl(updateDecl)
	if !strings.Contains(formatted, "Accessors{...base, read_name_id: name, default_enabled: true}") {
		t.Fatalf("expected formatted spread literal, got:\n%s", formatted)
	}
}

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
