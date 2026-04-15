package semantic_test

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func requireIdentExprName(t *testing.T, expr ast.Expr, expected string) {
	t.Helper()
	ident, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("expected identifier %q, got %T", expected, expr)
	}
	if ident.Name != expected {
		t.Fatalf("expected identifier %q, got %q", expected, ident.Name)
	}
}

func TestAnalyzeBraceStructErgonomics(t *testing.T) {
	src := `struct Row:
    left: int
    right: int
    flag: bool

def run(items: array[Row, 3], row: Row, flag: bool) -> int:
    let {left: first, right} = row
    total: mutable int = 0
    for {left, right: item_right, flag: keep} in items if keep:
        total <- total + left + item_right
    built: Row = Row{flag, right, left: first}
    next: Row = built{flag, right = total}
    match next:
        Row{left, right: current, flag}:
            return total + current
`
	result, errs := parseAndAnalyze(t, "brace_struct_ergonomics_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "int")

	runDecl := requireFuncDecl(t, result, "run")
	letStmt, ok := runDecl.Body[0].(*ast.LetDestructureStmt)
	if !ok {
		t.Fatalf("expected let destructure statement, got %T", runDecl.Body[0])
	}
	if letStmt.Pattern == nil || !letStmt.Pattern.Brace {
		t.Fatalf("expected brace destructure pattern, got %#v", letStmt.Pattern)
	}

	iterStmt, ok := runDecl.Body[2].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for statement, got %T", runDecl.Body[2])
	}
	if iterStmt.Filter == nil {
		t.Fatal("expected iterable for filter expression")
	}

	builtDecl, ok := runDecl.Body[3].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected built var decl, got %T", runDecl.Body[3])
	}
	builtLit, ok := builtDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected brace struct literal, got %T", builtDecl.Value)
	}
	if !builtLit.ResolvedArgsValid {
		t.Fatal("expected brace struct literal to resolve fields")
	}
	builtArgs := builtLit.LoweredArgs()
	if len(builtArgs) != 3 {
		t.Fatalf("expected 3 lowered struct literal args, got %d", len(builtArgs))
	}
	requireIdentExprName(t, builtArgs[0], "first")
	requireIdentExprName(t, builtArgs[1], "right")
	requireIdentExprName(t, builtArgs[2], "flag")

	nextDecl, ok := runDecl.Body[4].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected next var decl, got %T", runDecl.Body[4])
	}
	update, ok := nextDecl.Value.(*ast.RecordUpdateExpr)
	if !ok {
		t.Fatalf("expected record update expression, got %T", nextDecl.Value)
	}
	if !update.ResolvedArgsValid {
		t.Fatal("expected record update to resolve fields")
	}
	updateArgs := update.LoweredArgs()
	if len(updateArgs) != 3 {
		t.Fatalf("expected 3 lowered record update args, got %d", len(updateArgs))
	}
	if updateArgs[0] != nil {
		t.Fatalf("expected untouched left field to stay nil in lowered record update, got %T", updateArgs[0])
	}
	requireIdentExprName(t, updateArgs[1], "total")
	requireIdentExprName(t, updateArgs[2], "flag")

	matchStmt, ok := runDecl.Body[5].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match statement, got %T", runDecl.Body[5])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected brace struct match pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if !pattern.Brace {
		t.Fatalf("expected brace struct match pattern, got %#v", pattern)
	}
	if len(pattern.ResolvedArgs) != 3 {
		t.Fatalf("expected 3 resolved match args, got %d", len(pattern.ResolvedArgs))
	}
}

func TestAnalyzeRejectsUnknownLetDestructureField(t *testing.T) {
	src := `struct Row:
    left: int

def run(row: Row) -> int:
    let {missing} = row
    return 0
`
	_, errs := parseAndAnalyze(t, "let_destructure_missing_field_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `struct "Row" has no field "missing"`) {
		t.Fatalf("expected missing let destructure field diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsUnknownRecordUpdateField(t *testing.T) {
	src := `struct Row:
    left: int

def run(row: Row) -> Row:
    return row{missing = 1}
`
	_, errs := parseAndAnalyze(t, "record_update_missing_field_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `record update has no field "missing"`) {
		t.Fatalf("expected unknown record update field diagnostic, got:\n%s", all)
	}
}
