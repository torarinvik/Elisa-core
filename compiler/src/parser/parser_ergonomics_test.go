package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseExplicitArgErgonomicsAndDestructuring(t *testing.T) {
	file, errs := parseSourceFile(t, `params Pair:
    left: i64
    right: i64 = 7

struct PairRow:
    first: i64
    second: i64

def add(use Pair) -> i64:
    return left + right

def build(left: i64, width: i64, pair: PairRow, rows: darray[PairRow]) -> i64:
    with args(use Pair(left:), width:):
        let PairRow{first: local_first, second} = pair
        for {first, second} in rows:
            return add(use Pair(right: 5, left:), right: width)
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	paramsDecl, ok := file.Decls[0].(*ast.ParamsDecl)
	if !ok {
		t.Fatalf("expected params decl, got %T", file.Decls[0])
	}
	if paramsDecl.Name != "Pair" || len(paramsDecl.Params) != 2 || paramsDecl.Params[1].DefaultValue == nil {
		t.Fatalf("expected Pair params declaration with a defaulted field, got %#v", paramsDecl)
	}
	addDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected add function decl, got %T", file.Decls[2])
	}
	if len(addDecl.ParamPacks) != 1 || addDecl.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected add signature to use Pair pack, got %#v", addDecl.ParamPacks)
	}
	buildDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build function decl, got %T", file.Decls[3])
	}
	argsScope, ok := buildDecl.Body[0].(*ast.ArgsScopeStmt)
	if !ok {
		t.Fatalf("expected args scope stmt, got %T", buildDecl.Body[0])
	}
	if len(argsScope.ParamPacks) != 1 || argsScope.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected args scope to use Pair pack, got %#v", argsScope.ParamPacks)
	}
	if len(argsScope.Args) != 1 || argsScope.Args[0].Name != "width" || !argsScope.Args[0].Shorthand {
		t.Fatalf("expected shorthand width ambient arg, got %#v", argsScope.Args)
	}
	letStmt, ok := argsScope.Body[0].(*ast.LetDestructureStmt)
	if !ok {
		t.Fatalf("expected let destructure stmt, got %T", argsScope.Body[0])
	}
	if letStmt.Pattern.TypeName != "PairRow" || len(letStmt.Pattern.Args) != 2 || !letStmt.Pattern.Brace {
		t.Fatalf("expected typed brace destructure pattern, got %#v", letStmt.Pattern)
	}
	if letStmt.Pattern.Args[0].Field != "first" || letStmt.Pattern.Args[0].Name != "local_first" {
		t.Fatalf("expected renamed destructure field, got %#v", letStmt.Pattern.Args[0])
	}
	iterStmt, ok := argsScope.Body[1].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iter for stmt, got %T", argsScope.Body[1])
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindStructPattern)
	if !ok {
		t.Fatalf("expected struct destructure loop pattern, got %T", iterStmt.Pattern)
	}
	if pattern.TypeName != "" || len(pattern.Args) != 2 || !pattern.Brace || pattern.Args[0].Field != "first" || pattern.Args[1].Field != "second" {
		t.Fatalf("unexpected loop destructure pattern %#v", pattern)
	}
	ret, ok := iterStmt.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt inside loop, got %T", iterStmt.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	if len(call.ParamPacks) != 1 || call.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected call to use Pair pack, got %#v", call.ParamPacks)
	}
	foundPackShorthand := false
	for _, arg := range call.ParamPacks[0].Args {
		if arg.Name == "left" && arg.Shorthand {
			foundPackShorthand = true
		}
	}
	if !foundPackShorthand {
		t.Fatalf("expected pack application to preserve shorthand args, got %#v", call.ParamPacks[0].Args)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"params Pair:",
		"def add(use Pair) -> i64:",
		"with args(use Pair(left:), width:):",
		"let PairRow{first: local_first, second} = pair",
		"for {first, second} in rows:",
		"return add(use Pair(right: 5, left:), right: width)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
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
