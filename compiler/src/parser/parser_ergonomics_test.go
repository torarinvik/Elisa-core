package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
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

// The `;` statement separator has been removed (one statement per line). The
// multi-statement one-liner it enabled — almost always a native-binding stub —
// is now spelled with the expression-body form `def f(...) -> T = EXPR`.
func TestParseSemicolonStatementSeparatorRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def answer(x: int) -> int: _ = x; return 42\n")
	if len(errs) == 0 {
		t.Fatal("expected `;` removal diagnostic, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "`;` statement separator has been removed") {
		t.Fatalf("expected the semicolon removal diagnostic, got: %v", errs)
	}
}

// The canonical replacement: an expression-bodied function is exactly
// `def f(...) -> T: return EXPR`, and an unused parameter is named `_`.
func TestParseExpressionBodiedFunction(t *testing.T) {
	file, errs := parseSourceFile(t, "def answer(_: int) -> int = 42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if len(decl.Body) != 1 {
		t.Fatalf("expected a single synthesized return, got %d statements", len(decl.Body))
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected the body to be a return statement, got %T", decl.Body[0])
	}
	if ret.Value == nil {
		t.Fatal("expected the return to carry the body expression")
	}
}

// A single-statement inline body (no `;`) is unaffected — `def f() -> T: return X`.
func TestParseOneLineSingleStatementBody(t *testing.T) {
	file, errs := parseSourceFile(t, "def one() -> int: return 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	if len(decl.Body) != 1 {
		t.Fatalf("expected one inline statement, got %d", len(decl.Body))
	}
	if _, ok := decl.Body[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected return statement, got %T", decl.Body[0])
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
    return Span(start: start, finish: start + 1)
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
	if !strings.Contains(formatted, "return Span(start: start, finish: (start + 1))") {
		t.Fatalf("expected formatted output to preserve paren constructor names, got:\n%s", formatted)
	}
}

// The shorthand punned named argument `f(x:)` (== `f(x: x)`) has been removed:
// the parser reports a hard error pointing at the explicit form.
func TestParseShorthandNamedArgRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `struct Span:
    start: i64

def build(start: i64) -> Span:
    return Span(start:)
`)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "shorthand named argument") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected shorthand-named-argument rejection error, got: %v", errs)
	}
}

// Struct-literal spread `Type{...base, f: v}` has been removed in favor of record
// update `base{f = v}`: the parser rejects it with a message pointing at the
// record-update form.
func TestParseBraceStructLiteralSpreadRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `struct Accessors:
    read_name_id: i64?

def update(base: Accessors, name: i64) -> Accessors:
    return Accessors{...base, read_name_id: name}
`)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "struct-literal spread") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected struct-literal-spread rejection error, got: %v", errs)
	}
}

// Record update `base{f = v}` is the replacement for struct-literal spread: a copy
// of `base` with named fields overridden, distinguished from construction (`:`) by
// the `=` separator, and round-tripping through unparse.
func TestParseRecordUpdate(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Accessors:
    read_name_id: i64?
    default_enabled: bool

def update(base: Accessors, name: i64) -> Accessors:
    return base{read_name_id = name, default_enabled = true}
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
	upd, ok := ret.Value.(*ast.RecordUpdateExpr)
	if !ok {
		t.Fatalf("expected record-update return value, got %T", ret.Value)
	}
	if upd.ArgName(0) != "read_name_id" || upd.ArgName(1) != "default_enabled" {
		t.Fatalf("unexpected record-update field names: %q, %q", upd.ArgName(0), upd.ArgName(1))
	}
	formatted := unparse.FormatDecl(updateDecl)
	if !strings.Contains(formatted, "base{read_name_id = name, default_enabled = true}") {
		t.Fatalf("expected formatted record update, got:\n%s", formatted)
	}
}

func TestParseLowercaseAtomicGenericBraceLiteralWithoutChangingIndexedUpdates(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Cell:
    value: i64

def make_atomic[T](value: T) -> atomic[T]:
    return atomic[T]{value: value}

def update_cell(items: darray[Cell], index: usize, value: i64) -> Cell:
    return items[index]{value = value}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}

	makeDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected make_atomic function, got %T", file.Decls[1])
	}
	makeReturn, ok := makeDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected atomic return statement, got %T", makeDecl.Body[0])
	}
	atomicLiteral, ok := makeReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected lowercase atomic generic constructor, got %T", makeReturn.Value)
	}
	if atomicLiteral.Name != "atomic" || !atomicLiteral.Brace || len(atomicLiteral.TypeArgs) != 1 || atomicLiteral.ArgName(0) != "value" {
		t.Fatalf("unexpected atomic generic constructor shape: %#v", atomicLiteral)
	}

	updateDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected update_cell function, got %T", file.Decls[2])
	}
	updateReturn, ok := updateDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected record-update return statement, got %T", updateDecl.Body[0])
	}
	update, ok := updateReturn.Value.(*ast.RecordUpdateExpr)
	if !ok {
		t.Fatalf("expected lowercase indexed record update, got %T", updateReturn.Value)
	}
	if update.ArgName(0) != "value" {
		t.Fatalf("expected record-update field value, got %q", update.ArgName(0))
	}
}
